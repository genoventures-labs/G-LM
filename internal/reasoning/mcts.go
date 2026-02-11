package reasoning

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/mike/cognitive-llm/internal/model"
	"github.com/mike/cognitive-llm/internal/state"
)

type mctsNode struct {
	ID       string
	Path     []string
	Visits   int
	Value    float64
	Prior    float64
	Terminal bool
	Depth    int
	Output   string
	Children []*mctsNode
	Parent   *mctsNode
}

type mctsCandidate struct {
	Path   []string
	Output string
	Score  float64
}

func (e *Executor) executeMCTS(
	ctx context.Context,
	up Upstream,
	req model.ChatCompletionRequest,
	pol model.ModelPolicy,
	st state.CognitiveState,
) (model.ChatCompletionResponse, Trace, error) {
	trace := Trace{Mode: "mcts", TaskClass: st.TaskMode}
	modelsResp, err := up.ListModels(ctx)
	if err != nil {
		return model.ChatCompletionResponse{}, trace, fmt.Errorf("mcts model inventory failed: %w", err)
	}
	available := make([]string, 0, len(modelsResp.Data))
	for _, m := range modelsResp.Data {
		available = append(available, m.ID)
	}

	decision, err := e.router.ChooseWithState(req, available, pol, &st)
	if err != nil {
		return model.ChatCompletionResponse{}, trace, err
	}
	baseModel := decision.ChosenModel
	trace.ChosenModel = baseModel
	trace.TaskClass = decision.TaskClass

	rolloutBudget := e.resolveMCTSRollouts(req)
	maxDepth := e.resolveMCTSDepth(req)
	exploration := e.resolveMCTSExploration(req)
	root := &mctsNode{ID: "mcts-root", Path: nil, Depth: 0, Prior: 1}
	nodes := []Node{}
	candidates := []mctsCandidate{}
	maxVisitedDepth := 0

	for i := 0; i < rolloutBudget; i++ {
		if err := ctx.Err(); err != nil {
			break
		}
		leaf, chain := selectMCTSLeaf(root, exploration)
		if leaf.Depth >= maxDepth {
			leaf.Terminal = true
			continue
		}
		if len(leaf.Children) == 0 {
			expandMCTSLeaf(leaf, e.mctsActionsForTask(st.TaskMode))
		}
		next, selected := selectMCTSChild(leaf, exploration)
		if next == nil {
			continue
		}
		if selected > maxVisitedDepth {
			maxVisitedDepth = selected
		}
		chain = append(chain, next)

		simNode := Node{
			ID:        fmt.Sprintf("mcts-rollout-%d", i+1),
			Type:      "mcts_simulation",
			Model:     baseModel,
			StartedAt: time.Now().UTC(),
			Metadata: map[string]any{
				"path": next.Path,
			},
		}

		simReq := req
		simReq.Model = baseModel
		simReq.Messages = buildMCTSMessages(req.Messages, next.Path)
		resp, simErr := up.ChatCompletions(ctx, simReq)
		simNode.EndedAt = time.Now().UTC()
		if simErr != nil {
			simNode.Error = simErr.Error()
			nodes = append(nodes, simNode)
			continue
		}

		output := extractAssistantText(resp)
		score, _ := e.selfEvaluate(output, st)
		score = applyMCTSStatePenalty(score, st)
		next.Output = output
		next.Terminal = next.Depth >= maxDepth
		simNode.Score = score
		nodes = append(nodes, simNode)
		candidates = append(candidates, mctsCandidate{
			Path:   clonePath(next.Path),
			Output: output,
			Score:  score,
		})
		backpropagateMCTS(chain, score)
	}

	if len(candidates) == 0 {
		trace.Nodes = nodes
		return model.ChatCompletionResponse{}, trace, fmt.Errorf("mcts failed: no successful rollouts")
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Score == candidates[j].Score {
			return len(candidates[i].Output) > len(candidates[j].Output)
		}
		return candidates[i].Score > candidates[j].Score
	})

	best := candidates[0]
	branchResults := make([]BranchResult, 0, minInt(3, len(candidates)))
	for i := 0; i < len(candidates) && i < 3; i++ {
		branchResults = append(branchResults, BranchResult{
			Index:            i + 1,
			Model:            baseModel,
			Output:           candidates[i].Output,
			EvaluationScore:  candidates[i].Score,
			EvaluationReason: "mcts_simulation",
		})
	}
	contradictions := detectContradictions(branchResults)

	synthReq := req
	synthReq.Model = baseModel
	synthReq.Messages = buildSynthesisMessages(req.Messages, branchResults, contradictions)
	finalResp, synthErr := up.ChatCompletions(ctx, synthReq)
	if synthErr != nil {
		trace.Nodes = nodes
		trace.MCTS = &MCTSResult{
			Rollouts:  len(candidates),
			Depth:     maxVisitedDepth,
			BestScore: best.Score,
		}
		return model.ChatCompletionResponse{}, trace, fmt.Errorf("mcts synthesis failed: %w", synthErr)
	}

	trace.Contradictions = contradictions
	trace.Branches = branchResults
	trace.Nodes = nodes
	trace.MCTS = &MCTSResult{
		Rollouts:  len(candidates),
		Depth:     maxVisitedDepth,
		BestScore: best.Score,
	}
	return finalResp, trace, nil
}

func selectMCTSLeaf(root *mctsNode, exploration float64) (*mctsNode, []*mctsNode) {
	cur := root
	chain := []*mctsNode{root}
	for len(cur.Children) > 0 {
		next, _ := selectMCTSChild(cur, exploration)
		if next == nil {
			break
		}
		cur = next
		chain = append(chain, cur)
		if cur.Terminal {
			break
		}
	}
	return cur, chain
}

func selectMCTSChild(node *mctsNode, exploration float64) (*mctsNode, int) {
	if len(node.Children) == 0 {
		return nil, node.Depth
	}
	var best *mctsNode
	bestScore := math.Inf(-1)
	parentVisits := float64(maxInt(1, node.Visits))
	for _, child := range node.Children {
		score := mctsUCT(child, parentVisits, exploration)
		if score > bestScore {
			bestScore = score
			best = child
		}
	}
	if best == nil {
		return nil, node.Depth
	}
	return best, best.Depth
}

func expandMCTSLeaf(node *mctsNode, actions []string) {
	for i, action := range actions {
		path := append(clonePath(node.Path), action)
		node.Children = append(node.Children, &mctsNode{
			ID:     fmt.Sprintf("%s-%d", node.ID, i+1),
			Path:   path,
			Prior:  1.0 / float64(len(actions)),
			Depth:  node.Depth + 1,
			Parent: node,
		})
	}
}

func backpropagateMCTS(chain []*mctsNode, score float64) {
	for i := len(chain) - 1; i >= 0; i-- {
		n := chain[i]
		n.Visits++
		n.Value += score
	}
}

func mctsUCT(node *mctsNode, parentVisits float64, exploration float64) float64 {
	if node.Visits == 0 {
		return math.Inf(1)
	}
	q := node.Value / float64(node.Visits)
	u := exploration * math.Sqrt(math.Log(parentVisits)/float64(1+node.Visits))
	return q + u + 0.01*node.Prior
}

func buildMCTSMessages(base []model.Message, path []string) []model.Message {
	payload, _ := json.Marshal(map[string]any{
		"path":        path,
		"instruction": "Produce a concise, verifiable answer. Include assumptions and controls.",
	})
	sys := model.Message{
		Role:    "system",
		Content: "mcts_agent path context: " + string(payload),
	}
	out := make([]model.Message, 0, len(base)+1)
	out = append(out, sys)
	out = append(out, base...)
	return out
}

func applyMCTSStatePenalty(score float64, st state.CognitiveState) float64 {
	penalty := 0.0
	penalty += st.TopicDrift * 0.08
	penalty += st.MoodShift * 0.06
	if len(st.MicroSwitches) > 0 {
		penalty += 0.04
	}
	score -= penalty
	if score < 0 {
		return 0
	}
	if score > 1 {
		return 1
	}
	return math.Round(score*1000) / 1000
}

func (e *Executor) mctsActionsForTask(task string) []string {
	switch strings.ToLower(strings.TrimSpace(task)) {
	case "coding":
		return []string{"plan_first", "evidence_first", "risk_first"}
	case "extraction":
		return []string{"evidence_first", "plan_first", "risk_first"}
	default:
		return []string{"plan_first", "risk_first", "evidence_first"}
	}
}

func (e *Executor) resolveMCTSRollouts(req model.ChatCompletionRequest) int {
	rollouts := e.cfg.MCTSDefaultRollouts
	if req.Reasoning != nil && req.Reasoning.MCTSMaxRollouts > 0 {
		rollouts = req.Reasoning.MCTSMaxRollouts
	}
	if rollouts < 1 {
		rollouts = 1
	}
	if rollouts > e.cfg.MCTSMaxRollouts {
		rollouts = e.cfg.MCTSMaxRollouts
	}
	return rollouts
}

func (e *Executor) resolveMCTSDepth(req model.ChatCompletionRequest) int {
	depth := e.cfg.MCTSDefaultDepth
	if req.Reasoning != nil && req.Reasoning.MCTSMaxDepth > 0 {
		depth = req.Reasoning.MCTSMaxDepth
	}
	if depth < 1 {
		depth = 1
	}
	if depth > e.cfg.MCTSMaxDepth {
		depth = e.cfg.MCTSMaxDepth
	}
	return depth
}

func (e *Executor) resolveMCTSExploration(req model.ChatCompletionRequest) float64 {
	exploration := e.cfg.MCTSDefaultExploration
	if req.Reasoning != nil && req.Reasoning.MCTSExploration > 0 {
		exploration = req.Reasoning.MCTSExploration
	}
	if exploration <= 0 {
		return 1.2
	}
	if exploration > 3.0 {
		return 3.0
	}
	return exploration
}

func clonePath(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

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
	v2Enabled := e.resolveMCTSV2Enabled(req)
	earlyStopWindow := e.resolveMCTSEarlyStopWindow(req)
	earlyStopDelta := e.resolveMCTSEarlyStopDelta(req)
	root := &mctsNode{ID: "mcts-root", Path: nil, Depth: 0, Prior: 1}
	nodes := []Node{}
	candidates := []mctsCandidate{}
	var pruneAggregate pruneStats
	successfulRollouts := 0
	anchors := e.resolveMemoryAnchors(req)
	memAcc := newMemoryAnchorAccumulator(anchors)
	maxVisitedDepth := 0
	var earlyStop bool
	var earlyStopReason string
	bestHistory := make([]float64, 0, earlyStopWindow+1)
	bestSoFar := math.Inf(-1)

	for i := 0; i < rolloutBudget; i++ {
		if err := ctx.Err(); err != nil {
			break
		}
		leaf, chain := selectMCTSLeaf(root, exploration, v2Enabled)
		if leaf.Depth >= maxDepth {
			leaf.Terminal = true
			continue
		}
		if len(leaf.Children) == 0 {
			expandMCTSLeaf(leaf, e.mctsActionPriorsForTask(st.TaskMode))
		}
		next, selected := selectMCTSChild(leaf, exploration, v2Enabled)
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
		simReq.Messages = buildMCTSMessages(req.Messages, next.Path, st.TaskMode, anchors)
		resp, simErr := up.ChatCompletions(ctx, simReq)
		simNode.EndedAt = time.Now().UTC()
		if simErr != nil {
			simNode.Error = simErr.Error()
			nodes = append(nodes, simNode)
			continue
		}

		output := extractAssistantText(resp)
		score, _, coverage, bonus := e.evaluateOutputWithMemory(req, output, st, true)
		memAcc.Add(anchors, coverage, bonus)
		next.Output = output
		next.Terminal = next.Depth >= maxDepth
		simNode.Score = score
		nodes = append(nodes, simNode)
		candidates = append(candidates, mctsCandidate{
			Path:   clonePath(next.Path),
			Output: output,
			Score:  score,
		})
		successfulRollouts++
		if score > bestSoFar {
			bestSoFar = score
		}
		bestHistory = append(bestHistory, bestSoFar)
		if shouldEarlyStop(bestHistory, earlyStopWindow, earlyStopDelta) {
			earlyStop = true
			earlyStopReason = "converged_delta"
			if pruneAggregate.CandidatesIn == 0 {
				earlyStopReason = "converged_delta_no_prune"
			}
			if e.cfg.PruningEnabled {
				// Keep existing prune behavior before stop.
			}
		}
		if e.cfg.PruningEnabled {
			pruned, stats := e.pruneMCTSCandidates(candidates, e.cfg.PruningMCTSPoolTopK)
			candidates = pruned
			pruneAggregate = pruneAggregate.merge(stats)
		}
		backpropagateMCTS(chain, score)
		if earlyStop {
			break
		}
	}

	if len(candidates) == 0 {
		baseline, baselineNode, baselineErr := e.mctsBaselineCandidate(ctx, up, req, baseModel, st)
		nodes = append(nodes, baselineNode)
		if baselineErr != nil {
			trace.Nodes = nodes
			return model.ChatCompletionResponse{}, trace, fmt.Errorf("mcts failed: no successful rollouts")
		}
		candidates = append(candidates, baseline)
		successfulRollouts = 1
		maxVisitedDepth = maxInt(1, maxVisitedDepth)
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Score == candidates[j].Score {
			return len(candidates[i].Output) > len(candidates[j].Output)
		}
		return candidates[i].Score > candidates[j].Score
	})

	synthCandidates := append([]mctsCandidate{}, candidates...)
	if e.cfg.PruningEnabled {
		pruned, stats := e.pruneMCTSCandidates(synthCandidates, e.cfg.PruningMCTSSynthTopK)
		pruneAggregate = pruneAggregate.merge(stats)
		if len(pruned) == 0 {
			baseline, baselineNode, baselineErr := e.mctsBaselineCandidate(ctx, up, req, baseModel, st)
			nodes = append(nodes, baselineNode)
			if baselineErr != nil {
				trace.Nodes = nodes
				return model.ChatCompletionResponse{}, trace, fmt.Errorf("mcts failed: no pruned candidates and baseline fallback failed")
			}
			synthCandidates = []mctsCandidate{baseline}
			candidates = append(candidates, baseline)
			if successfulRollouts == 0 {
				successfulRollouts = 1
			}
		} else {
			synthCandidates = pruned
		}
	}

	sort.Slice(synthCandidates, func(i, j int) bool {
		if synthCandidates[i].Score == synthCandidates[j].Score {
			return len(synthCandidates[i].Output) > len(synthCandidates[j].Output)
		}
		return synthCandidates[i].Score > synthCandidates[j].Score
	})
	best := synthCandidates[0]
	branchResults := make([]BranchResult, 0, minInt(3, len(synthCandidates)))
	for i := 0; i < len(synthCandidates) && i < 3; i++ {
		branchResults = append(branchResults, BranchResult{
			Index:            i + 1,
			Model:            baseModel,
			Output:           synthCandidates[i].Output,
			EvaluationScore:  synthCandidates[i].Score,
			EvaluationReason: "mcts_simulation",
		})
	}
	contradictions := detectContradictions(branchResults)

	synthReq := req
	synthReq.Model = baseModel
	synthReq.Messages = buildSynthesisMessages(req.Messages, branchResults, contradictions, anchors)
	finalResp, synthErr := up.ChatCompletions(ctx, synthReq)
	if synthErr != nil {
		trace.Nodes = nodes
		trace.MemoryAnchor = memAcc.Trace(e.cfg.MemoryAnchoredReasoningEnabled, "mcts")
		trace.MCTS = &MCTSResult{
			Rollouts:         rolloutBudget,
			RolloutsExecuted: maxInt(1, successfulRollouts),
			Depth:            maxVisitedDepth,
			BestScore:        best.Score,
			EarlyStop:        earlyStop,
			EarlyStopReason:  earlyStopReason,
		}
		return model.ChatCompletionResponse{}, trace, fmt.Errorf("mcts synthesis failed: %w", synthErr)
	}

	trace.Contradictions = contradictions
	trace.Branches = branchResults
	trace.Nodes = nodes
	trace.Pruning = &PruningTrace{
		Mode:            "mcts",
		Enabled:         e.cfg.PruningEnabled,
		MinScore:        e.cfg.PruningMinScore,
		TopK:            e.cfg.PruningMCTSSynthTopK,
		CandidatesIn:    pruneAggregate.CandidatesIn,
		CandidatesOut:   len(synthCandidates),
		DroppedLowScore: pruneAggregate.DroppedLowScore,
		DroppedTopK:     pruneAggregate.DroppedTopK,
	}
	trace.MemoryAnchor = memAcc.Trace(e.cfg.MemoryAnchoredReasoningEnabled, "mcts")
	trace.MCTS = &MCTSResult{
		Rollouts:         rolloutBudget,
		RolloutsExecuted: maxInt(1, successfulRollouts),
		Depth:            maxVisitedDepth,
		BestScore:        best.Score,
		EarlyStop:        earlyStop,
		EarlyStopReason:  earlyStopReason,
	}
	if len(candidates) == 1 && len(candidates[0].Path) == 0 {
		trace.MCTS.Fallback = "direct_baseline"
	}
	e.applyGeometryAndFusion(&trace, req)
	return finalResp, trace, nil
}

func selectMCTSLeaf(root *mctsNode, exploration float64, v2 bool) (*mctsNode, []*mctsNode) {
	cur := root
	chain := []*mctsNode{root}
	for len(cur.Children) > 0 {
		next, _ := selectMCTSChild(cur, exploration, v2)
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

func selectMCTSChild(node *mctsNode, exploration float64, v2 bool) (*mctsNode, int) {
	if len(node.Children) == 0 {
		return nil, node.Depth
	}
	var best *mctsNode
	bestScore := math.Inf(-1)
	parentVisits := float64(maxInt(1, node.Visits))
	for _, child := range node.Children {
		score := 0.0
		if v2 {
			score = mctsUCBTuned(child, parentVisits, exploration)
		} else {
			score = mctsUCT(child, parentVisits, exploration)
		}
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

type mctsActionPrior struct {
	Action string
	Prior  float64
}

func expandMCTSLeaf(node *mctsNode, actions []mctsActionPrior) {
	totalPrior := 0.0
	for _, a := range actions {
		totalPrior += a.Prior
	}
	if totalPrior <= 0 {
		totalPrior = float64(len(actions))
	}
	for i, action := range actions {
		path := append(clonePath(node.Path), action.Action)
		node.Children = append(node.Children, &mctsNode{
			ID:     fmt.Sprintf("%s-%d", node.ID, i+1),
			Path:   path,
			Prior:  action.Prior / totalPrior,
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

func mctsUCBTuned(node *mctsNode, parentVisits float64, exploration float64) float64 {
	if node.Visits == 0 {
		return math.Inf(1)
	}
	mean := node.Value / float64(node.Visits)
	variance := mean * (1.0 - mean)
	bound := variance + math.Sqrt((2*math.Log(parentVisits))/float64(node.Visits))
	if bound > 0.25 {
		bound = 0.25
	}
	u := exploration * math.Sqrt((math.Log(parentVisits)/float64(node.Visits))*bound)
	return mean + u + 0.02*node.Prior
}

func buildMCTSMessages(base []model.Message, path []string, taskMode string, anchors []string) []model.Message {
	instruction := "Produce a concise, verifiable answer. Include assumptions and controls."
	switch strings.ToLower(strings.TrimSpace(taskMode)) {
	case "coding":
		instruction = "Produce a concise, testable solution. Include assumptions, validation checks, and explicit test strategy."
	case "extraction":
		instruction = "Produce an evidence-grounded extraction. Include assumptions, source grounding, and uncertainty markers."
	default:
		instruction = "Produce a concise, verifiable answer. Include assumptions, controls, and residual uncertainty."
	}
	payload, _ := json.Marshal(map[string]any{
		"path":        path,
		"instruction": instruction,
	})
	if len(anchors) > 0 {
		payload, _ = json.Marshal(map[string]any{
			"path":           path,
			"instruction":    instruction,
			"memory_anchors": anchors,
		})
	}
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

func (e *Executor) mctsActionPriorsForTask(task string) []mctsActionPrior {
	switch strings.ToLower(strings.TrimSpace(task)) {
	case "coding":
		return []mctsActionPrior{
			{Action: "evidence_first", Prior: 0.50},
			{Action: "plan_first", Prior: 0.32},
			{Action: "risk_first", Prior: 0.18},
		}
	case "extraction":
		return []mctsActionPrior{
			{Action: "evidence_first", Prior: 0.46},
			{Action: "plan_first", Prior: 0.30},
			{Action: "risk_first", Prior: 0.24},
		}
	default:
		return []mctsActionPrior{
			{Action: "plan_first", Prior: 0.40},
			{Action: "risk_first", Prior: 0.34},
			{Action: "evidence_first", Prior: 0.26},
		}
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

func (e *Executor) resolveMCTSV2Enabled(req model.ChatCompletionRequest) bool {
	if req.Reasoning != nil && req.Reasoning.MCTSV2Enabled {
		return true
	}
	return e.cfg.MCTSV2Enabled
}

func (e *Executor) resolveMCTSEarlyStopWindow(req model.ChatCompletionRequest) int {
	window := e.cfg.MCTSEarlyStopWindow
	if req.Reasoning != nil && req.Reasoning.MCTSEarlyStopWindow > 0 {
		window = req.Reasoning.MCTSEarlyStopWindow
	}
	if window < 2 {
		return 2
	}
	return window
}

func (e *Executor) resolveMCTSEarlyStopDelta(req model.ChatCompletionRequest) float64 {
	delta := e.cfg.MCTSEarlyStopDelta
	if req.Reasoning != nil && req.Reasoning.MCTSEarlyStopDelta > 0 {
		delta = req.Reasoning.MCTSEarlyStopDelta
	}
	if delta < 0 {
		return 0
	}
	if delta > 0.2 {
		return 0.2
	}
	return delta
}

func shouldEarlyStop(bestHistory []float64, window int, delta float64) bool {
	if len(bestHistory) < window {
		return false
	}
	end := len(bestHistory) - 1
	start := end - window + 1
	if start < 0 {
		return false
	}
	improvement := bestHistory[end] - bestHistory[start]
	return improvement < delta
}

func clonePath(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func (e *Executor) mctsBaselineCandidate(
	ctx context.Context,
	up Upstream,
	req model.ChatCompletionRequest,
	modelID string,
	st state.CognitiveState,
) (mctsCandidate, Node, error) {
	if err := ctx.Err(); err != nil {
		return mctsCandidate{}, Node{}, err
	}
	node := Node{
		ID:        "mcts-baseline",
		Type:      "mcts_baseline",
		Model:     modelID,
		StartedAt: time.Now().UTC(),
	}
	baselineReq := req
	baselineReq.Model = modelID
	baselineReq.Messages = buildMCTSBaselineMessages(req.Messages, e.resolveMemoryAnchors(req))
	resp, err := up.ChatCompletions(ctx, baselineReq)
	node.EndedAt = time.Now().UTC()
	if ctxErr := ctx.Err(); ctxErr != nil {
		node.Error = ctxErr.Error()
		return mctsCandidate{}, node, ctxErr
	}
	if err != nil {
		node.Error = err.Error()
		return mctsCandidate{}, node, err
	}
	output := extractAssistantText(resp)
	score, _, _, _ := e.evaluateOutputWithMemory(req, output, st, true)
	node.Score = score
	return mctsCandidate{
		Path:   nil,
		Output: output,
		Score:  score,
	}, node, nil
}

func buildMCTSBaselineMessages(base []model.Message, anchors []string) []model.Message {
	anchorHint := ""
	if len(anchors) > 0 {
		anchorHint = " Memory anchors (prioritize if relevant): " + strings.Join(anchors, ", ") + "."
	}
	sys := model.Message{
		Role:    "system",
		Content: "mcts baseline responder: provide a concise, actionable answer with assumptions and controls." + anchorHint,
	}
	out := make([]model.Message, 0, len(base)+1)
	out = append(out, sys)
	out = append(out, base...)
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

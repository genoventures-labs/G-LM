package reasoning

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mike/cognitive-llm/internal/model"
	"github.com/mike/cognitive-llm/internal/state"
)

type MultiAgentConfig struct {
	Enabled      bool
	MaxAgents    int
	MaxRounds    int
	StageTimeout time.Duration
	BudgetTokens int
}

type agentRole struct {
	Name   string
	Prompt string
}

type agentResult struct {
	Role   string
	Round  int
	Output string
	Score  float64
}

func (e *Executor) executeMultiAgent(
	ctx context.Context,
	up Upstream,
	req model.ChatCompletionRequest,
	pol model.ModelPolicy,
	st state.CognitiveState,
) (model.ChatCompletionResponse, Trace, error) {
	trace := Trace{Mode: "multi_agent", TaskClass: st.TaskMode}
	if req.Reasoning != nil && !req.Reasoning.MultiAgentEnabled {
		return model.ChatCompletionResponse{}, trace, fmt.Errorf("multi_agent_enabled must be true when reasoning.mode=multi_agent")
	}

	modelsResp, err := up.ListModels(ctx)
	if err != nil {
		return model.ChatCompletionResponse{}, trace, fmt.Errorf("multi-agent model inventory failed: %w", err)
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

	cfg := e.multiAgentConfig(req)
	roles := e.multiAgentRoles(cfg.MaxAgents)
	if len(roles) == 0 {
		return model.ChatCompletionResponse{}, trace, fmt.Errorf("multi-agent requires at least one worker role")
	}

	nodes := []Node{}
	candidates := []agentResult{}
	var pruneAggregate pruneStats
	tokenBudgetUsed := 0
	roundsRun := 0
	roleMaxTokens := e.resolveMultiAgentRoleMaxTokens(req, cfg)

	for round := 1; round <= cfg.MaxRounds; round++ {
		if ctx.Err() != nil {
			break
		}
		roundResults, roundNodes, used, roundErr := e.runMultiAgentRound(ctx, up, req, baseModel, st, roles, round, cfg.BudgetTokens-tokenBudgetUsed, roleMaxTokens)
		nodes = append(nodes, roundNodes...)
		tokenBudgetUsed += used
		if roundErr != nil {
			if len(candidates) == 0 {
				baseline, baselineNode, baselineErr := e.multiAgentBaselineCandidate(ctx, up, req, baseModel, st, round, roleMaxTokens)
				nodes = append(nodes, baselineNode)
				if baselineErr != nil {
					trace.Nodes = nodes
					return model.ChatCompletionResponse{}, trace, roundErr
				}
				candidates = append(candidates, baseline)
				roundsRun = maxInt(1, round)
				break
			}
			break
		}
		if len(roundResults) == 0 {
			break
		}
		roundsRun = round
		candidates = append(candidates, roundResults...)
		sort.Slice(candidates, func(i, j int) bool {
			if candidates[i].Score == candidates[j].Score {
				return len(candidates[i].Output) > len(candidates[j].Output)
			}
			return candidates[i].Score > candidates[j].Score
		})
		if e.cfg.PruningEnabled {
			pruned, stats := e.pruneMultiAgentCandidates(candidates, e.cfg.PruningMARoundTopK)
			candidates = pruned
			pruneAggregate = pruneAggregate.merge(stats)
			if len(candidates) == 0 {
				baseline, baselineNode, baselineErr := e.multiAgentBaselineCandidate(ctx, up, req, baseModel, st, maxInt(1, round), roleMaxTokens)
				nodes = append(nodes, baselineNode)
				if baselineErr != nil {
					trace.Nodes = nodes
					return model.ChatCompletionResponse{}, trace, fmt.Errorf("multi-agent failed: pruning removed all candidates and baseline fallback failed")
				}
				candidates = append(candidates, baseline)
				roundsRun = maxInt(1, round)
				break
			}
		}
		if e.multiAgentConsensus(candidates) != "low" {
			break
		}
		roles = e.refinementRoles(candidates)
	}

	if len(candidates) == 0 {
		baseline, baselineNode, baselineErr := e.multiAgentBaselineCandidate(ctx, up, req, baseModel, st, maxInt(1, roundsRun), roleMaxTokens)
		nodes = append(nodes, baselineNode)
		if baselineErr != nil {
			trace.Nodes = nodes
			return model.ChatCompletionResponse{}, trace, fmt.Errorf("multi-agent failed: no successful role outputs")
		}
		candidates = append(candidates, baseline)
		roundsRun = maxInt(1, roundsRun)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Score == candidates[j].Score {
			return len(candidates[i].Output) > len(candidates[j].Output)
		}
		return candidates[i].Score > candidates[j].Score
	})

	synthCandidates := append([]agentResult{}, candidates...)
	if e.cfg.PruningEnabled {
		pruned, stats := e.pruneMultiAgentCandidates(synthCandidates, e.cfg.PruningMASynthTopK)
		pruneAggregate = pruneAggregate.merge(stats)
		if len(pruned) == 0 {
			baseline, baselineNode, baselineErr := e.multiAgentBaselineCandidate(ctx, up, req, baseModel, st, maxInt(1, roundsRun), roleMaxTokens)
			nodes = append(nodes, baselineNode)
			if baselineErr != nil {
				trace.Nodes = nodes
				return model.ChatCompletionResponse{}, trace, fmt.Errorf("multi-agent failed: no pruned synthesis candidates and baseline fallback failed")
			}
			synthCandidates = []agentResult{baseline}
			candidates = append(candidates, baseline)
		} else {
			synthCandidates = pruned
		}
	}

	top := minInt(3, len(synthCandidates))
	branchResults := make([]BranchResult, 0, top)
	for i := 0; i < top; i++ {
		branchResults = append(branchResults, BranchResult{
			Index:            i + 1,
			Model:            baseModel,
			Output:           synthCandidates[i].Output,
			EvaluationScore:  synthCandidates[i].Score,
			EvaluationReason: "multi_agent:" + synthCandidates[i].Role,
		})
	}
	contradictions := detectContradictions(branchResults)
	consensus := e.multiAgentConsensus(synthCandidates)

	synthNode := Node{
		ID:        "multi-agent-synthesizer",
		Type:      "multi_agent_synthesis",
		Model:     baseModel,
		StartedAt: time.Now().UTC(),
		Metadata: map[string]any{
			"winner":    synthCandidates[0].Role,
			"consensus": consensus,
		},
	}
	synthReq := req
	synthReq.Model = baseModel
	if v := e.resolveMultiAgentSynthesisMaxTokens(req); v > 0 {
		synthReq.MaxTokens = &v
	}
	synthReq.Messages = buildMultiAgentSynthesisMessages(req.Messages, synthCandidates[:top], contradictions)
	finalResp, synthErr := up.ChatCompletions(ctx, synthReq)
	synthNode.EndedAt = time.Now().UTC()
	if synthErr != nil {
		synthNode.Error = synthErr.Error()
		nodes = append(nodes, synthNode)
		trace.Nodes = nodes
		trace.Branches = branchResults
		trace.Contradictions = contradictions
		trace.MultiAgent = &MultiAgentResult{
			Agents:    len(roles) + 1,
			Rounds:    maxInt(1, roundsRun),
			Winner:    synthCandidates[0].Role,
			Consensus: consensus,
			Score:     synthCandidates[0].Score,
		}
		return model.ChatCompletionResponse{}, trace, fmt.Errorf("multi-agent synthesis failed: %w", synthErr)
	}
	nodes = append(nodes, synthNode)

	trace.Nodes = nodes
	trace.Branches = branchResults
	trace.Contradictions = contradictions
	trace.Pruning = &PruningTrace{
		Mode:            "multi_agent",
		Enabled:         e.cfg.PruningEnabled,
		MinScore:        e.cfg.PruningMinScore,
		TopK:            e.cfg.PruningMASynthTopK,
		CandidatesIn:    pruneAggregate.CandidatesIn,
		CandidatesOut:   len(synthCandidates),
		DroppedLowScore: pruneAggregate.DroppedLowScore,
		DroppedTopK:     pruneAggregate.DroppedTopK,
	}
	trace.MultiAgent = &MultiAgentResult{
		Agents:    len(roles) + 1,
		Rounds:    maxInt(1, roundsRun),
		Winner:    synthCandidates[0].Role,
		Consensus: consensus,
		Score:     synthCandidates[0].Score,
	}
	return finalResp, trace, nil
}

func (e *Executor) runMultiAgentRound(
	ctx context.Context,
	up Upstream,
	req model.ChatCompletionRequest,
	modelID string,
	st state.CognitiveState,
	roles []agentRole,
	round int,
	tokenBudget int,
	roleMaxTokens int,
) ([]agentResult, []Node, int, error) {
	if tokenBudget <= 0 {
		return nil, nil, 0, fmt.Errorf("multi-agent budget exhausted")
	}
	type roundOut struct {
		result agentResult
		node   Node
		tokens int
		err    error
	}
	outCh := make(chan roundOut, len(roles))
	var wg sync.WaitGroup
	for i := range roles {
		role := roles[i]
		wg.Add(1)
		go func(role agentRole) {
			defer wg.Done()
			node := Node{
				ID:        fmt.Sprintf("multi-agent-%s-r%d", role.Name, round),
				Type:      "multi_agent_role",
				Model:     modelID,
				StartedAt: time.Now().UTC(),
				Metadata: map[string]any{
					"role":  role.Name,
					"round": round,
				},
			}
			roleReq := req
			roleReq.Model = modelID
			if roleMaxTokens > 0 {
				roleReq.MaxTokens = &roleMaxTokens
			}
			roleReq.Messages = buildMultiAgentRoleMessages(req.Messages, role, round)
			resp, err := up.ChatCompletions(ctx, roleReq)
			node.EndedAt = time.Now().UTC()
			if err != nil {
				node.Error = err.Error()
				outCh <- roundOut{node: node, err: err}
				return
			}
			output := extractAssistantText(resp)
			score, _ := e.evaluateOutput(req, output, st, true)
			node.Score = score
			tokens := estimateTokens(output)
			outCh <- roundOut{
				result: agentResult{Role: role.Name, Round: round, Output: output, Score: score},
				node:   node,
				tokens: tokens,
			}
		}(role)
	}
	wg.Wait()
	close(outCh)

	results := []agentResult{}
	nodes := []Node{}
	used := 0
	var anySuccess bool
	for item := range outCh {
		nodes = append(nodes, item.node)
		if item.err != nil {
			continue
		}
		if used+item.tokens > tokenBudget {
			continue
		}
		used += item.tokens
		results = append(results, item.result)
		anySuccess = true
	}
	if !anySuccess {
		return nil, nodes, used, fmt.Errorf("multi-agent round %d produced no successful outputs", round)
	}
	return results, nodes, used, nil
}

func (e *Executor) multiAgentConfig(req model.ChatCompletionRequest) MultiAgentConfig {
	cfg := MultiAgentConfig{
		Enabled:      e.cfg.MultiAgentEnabled,
		MaxAgents:    e.cfg.MultiAgentMaxAgents,
		MaxRounds:    e.cfg.MultiAgentMaxRounds,
		BudgetTokens: e.cfg.MultiAgentBudgetTokens,
	}
	if req.Reasoning != nil {
		if req.Reasoning.MultiAgentMaxAgents > 0 {
			cfg.MaxAgents = req.Reasoning.MultiAgentMaxAgents
		}
		if req.Reasoning.MultiAgentMaxRounds > 0 {
			cfg.MaxRounds = req.Reasoning.MultiAgentMaxRounds
		}
		if req.Reasoning.MultiAgentBudgetTokens > 0 {
			cfg.BudgetTokens = req.Reasoning.MultiAgentBudgetTokens
		}
	}
	if cfg.MaxAgents < 2 {
		cfg.MaxAgents = 2
	}
	if cfg.MaxAgents > 4 {
		cfg.MaxAgents = 4
	}
	if cfg.MaxRounds < 1 {
		cfg.MaxRounds = 1
	}
	if cfg.MaxRounds > 4 {
		cfg.MaxRounds = 4
	}
	if cfg.BudgetTokens < 200 {
		cfg.BudgetTokens = 200
	}
	return cfg
}

func (e *Executor) resolveMultiAgentRoleMaxTokens(req model.ChatCompletionRequest, cfg MultiAgentConfig) int {
	if req.MaxTokens != nil && *req.MaxTokens > 0 {
		return minInt(*req.MaxTokens, 96)
	}
	perCall := cfg.BudgetTokens / maxInt(1, cfg.MaxAgents*maxInt(1, cfg.MaxRounds))
	if perCall < 48 {
		return 48
	}
	return minInt(perCall, 96)
}

func (e *Executor) resolveMultiAgentSynthesisMaxTokens(req model.ChatCompletionRequest) int {
	if req.MaxTokens != nil && *req.MaxTokens > 0 {
		return minInt(*req.MaxTokens, 160)
	}
	return 160
}

func (e *Executor) multiAgentRoles(maxAgents int) []agentRole {
	workers := []agentRole{
		{Name: "planner", Prompt: "Decompose the request into concrete, verifiable steps with constraints."},
		{Name: "researcher", Prompt: "Propose a practical solution path with explicit assumptions and evidence needs."},
		{Name: "critic", Prompt: "Find contradictions, risks, and compliance/control gaps in likely solutions."},
	}
	maxWorkers := maxAgents - 1
	if maxWorkers < 1 {
		maxWorkers = 1
	}
	if maxWorkers > len(workers) {
		maxWorkers = len(workers)
	}
	return workers[:maxWorkers]
}

func (e *Executor) refinementRoles(candidates []agentResult) []agentRole {
	out := []agentRole{}
	limit := minInt(2, len(candidates))
	for i := 0; i < limit; i++ {
		out = append(out, agentRole{
			Name:   candidates[i].Role,
			Prompt: "Refine your prior output with clearer assumptions, checks, and concise structure.",
		})
	}
	return out
}

func (e *Executor) multiAgentConsensus(results []agentResult) string {
	if len(results) < 2 {
		return "low"
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	delta := results[0].Score - results[1].Score
	branches := make([]BranchResult, 0, minInt(3, len(results)))
	for i := 0; i < len(results) && i < 3; i++ {
		branches = append(branches, BranchResult{
			Index:           i + 1,
			Output:          results[i].Output,
			EvaluationScore: results[i].Score,
		})
	}
	contr := detectContradictions(branches)
	if contr.Detected {
		return "low"
	}
	if delta >= 0.12 {
		return "high"
	}
	if delta >= 0.05 {
		return "medium"
	}
	return "low"
}

func buildMultiAgentRoleMessages(base []model.Message, role agentRole, round int) []model.Message {
	payload, _ := json.Marshal(map[string]any{
		"role":  role.Name,
		"round": round,
	})
	sys := model.Message{
		Role:    "system",
		Content: "multi_agent role context: " + string(payload) + ". " + role.Prompt,
	}
	out := make([]model.Message, 0, len(base)+1)
	out = append(out, sys)
	out = append(out, base...)
	return out
}

func buildMultiAgentSynthesisMessages(base []model.Message, results []agentResult, contradictions ContradictionReport) []model.Message {
	type compact struct {
		Role   string  `json:"role"`
		Score  float64 `json:"score"`
		Output string  `json:"output"`
	}
	payload := map[string]any{
		"multi_agent_candidates": func() []compact {
			out := make([]compact, 0, len(results))
			for _, r := range results {
				out = append(out, compact{Role: r.Role, Score: r.Score, Output: r.Output})
			}
			return out
		}(),
		"contradictions": contradictions,
		"instruction":    "Synthesize one final answer. Prefer consistency, explicit assumptions, and control/risk clarity.",
	}
	data, _ := json.Marshal(payload)
	sys := model.Message{
		Role:    "system",
		Content: "multi_agent synthesizer payload: " + string(data),
	}
	out := make([]model.Message, 0, len(base)+1)
	out = append(out, sys)
	out = append(out, base...)
	return out
}

func estimateTokens(s string) int {
	n := len(strings.Fields(s))
	if n < 1 {
		return 1
	}
	return int(float64(n)*1.3) + 8
}

func (e *Executor) multiAgentBaselineCandidate(
	ctx context.Context,
	up Upstream,
	req model.ChatCompletionRequest,
	modelID string,
	st state.CognitiveState,
	round int,
	maxTokens int,
) (agentResult, Node, error) {
	if err := ctx.Err(); err != nil {
		return agentResult{}, Node{}, err
	}
	node := Node{
		ID:        fmt.Sprintf("multi-agent-baseline-r%d", round),
		Type:      "multi_agent_baseline",
		Model:     modelID,
		StartedAt: time.Now().UTC(),
		Metadata: map[string]any{
			"round": round,
		},
	}
	baselineReq := req
	baselineReq.Model = modelID
	if maxTokens > 0 {
		baselineReq.MaxTokens = &maxTokens
	}
	baselineReq.Messages = buildMultiAgentBaselineMessages(req.Messages)
	resp, err := up.ChatCompletions(ctx, baselineReq)
	node.EndedAt = time.Now().UTC()
	if ctxErr := ctx.Err(); ctxErr != nil {
		node.Error = ctxErr.Error()
		return agentResult{}, node, ctxErr
	}
	if err != nil {
		node.Error = err.Error()
		return agentResult{}, node, err
	}
	output := extractAssistantText(resp)
	score, _ := e.evaluateOutput(req, output, st, true)
	node.Score = score
	return agentResult{
		Role:   "baseline",
		Round:  round,
		Output: output,
		Score:  score,
	}, node, nil
}

func buildMultiAgentBaselineMessages(base []model.Message) []model.Message {
	sys := model.Message{
		Role: "system",
		Content: "multi_agent baseline responder: produce one concise, practical answer with explicit assumptions, " +
			"controls, and residual risk.",
	}
	out := make([]model.Message, 0, len(base)+1)
	out = append(out, sys)
	out = append(out, base...)
	return out
}

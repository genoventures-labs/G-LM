package reasoning

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mike/cognitive-llm/internal/model"
	"github.com/mike/cognitive-llm/internal/state"
)

type decomposeExecutionConfig struct {
	MaxSubtasks  int
	MaxDepth     int
	BudgetTokens int
}

func (e *Executor) executeDecompose(
	ctx context.Context,
	up Upstream,
	req model.ChatCompletionRequest,
	pol model.ModelPolicy,
	st state.CognitiveState,
) (model.ChatCompletionResponse, Trace, error) {
	trace := Trace{Mode: "decompose", TaskClass: st.TaskMode}
	if req.Reasoning != nil && !req.Reasoning.DecomposeEnabled {
		return model.ChatCompletionResponse{}, trace, fmt.Errorf("decompose_enabled must be true when reasoning.mode=decompose")
	}

	modelsResp, err := up.ListModels(ctx)
	if err != nil {
		return model.ChatCompletionResponse{}, trace, fmt.Errorf("decompose model inventory failed: %w", err)
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

	cfg := e.resolveDecomposeConfig(req)
	anchors := e.resolveMemoryAnchors(req)
	memAcc := newMemoryAnchorAccumulator(anchors)
	nodes := make([]Node, 0, cfg.MaxSubtasks+4)

	subtasks, plannerNode := e.planSubtasks(ctx, up, req, baseModel, cfg, anchors)
	nodes = append(nodes, plannerNode)

	allSuccessful := make([]BranchResult, 0, len(subtasks))
	tokenBudgetUsed := 0
	for i, subtask := range subtasks {
		if ctx.Err() != nil {
			break
		}
		if tokenBudgetUsed >= cfg.BudgetTokens {
			break
		}
		node := Node{
			ID:        fmt.Sprintf("decompose-subtask-%d", i+1),
			Type:      "decompose_subtask",
			Model:     baseModel,
			StartedAt: time.Now().UTC(),
			Metadata: map[string]any{
				"subtask_index": i + 1,
				"subtask":       subtask,
			},
		}

		subReq := req
		subReq.Model = baseModel
		subReq.Messages = buildDecomposeSubtaskMessages(req.Messages, i, len(subtasks), subtask, anchors)
		resp, runErr := up.ChatCompletions(ctx, subReq)
		node.EndedAt = time.Now().UTC()
		if runErr != nil {
			node.Error = runErr.Error()
			nodes = append(nodes, node)
			continue
		}

		output := extractAssistantText(resp)
		score, reason, coverage, bonus := e.evaluateOutputWithMemory(req, output, st, true)
		memAcc.Add(anchors, coverage, bonus)
		node.Score = score
		node.Metadata["evaluation_reason"] = reason
		nodes = append(nodes, node)
		tokenBudgetUsed += estimateTokens(subtask) + estimateTokens(output)

		allSuccessful = append(allSuccessful, BranchResult{
			Index:            i + 1,
			Model:            baseModel,
			Output:           output,
			EvaluationScore:  score,
			EvaluationReason: reason,
			Warnings:         branchWarnings(output),
		})
	}

	if len(allSuccessful) == 0 {
		baseline, baselineNode, baselineErr := e.decomposeBaselineCandidate(ctx, up, req, baseModel, st, anchors)
		nodes = append(nodes, baselineNode)
		if baselineErr != nil {
			trace.Nodes = nodes
			trace.MemoryAnchor = memAcc.Trace(e.cfg.MemoryAnchoredReasoningEnabled, "decompose")
			return model.ChatCompletionResponse{}, trace, fmt.Errorf("decompose failed: no successful subtask executions")
		}
		allSuccessful = append(allSuccessful, baseline)
	}

	pruneInput := append([]BranchResult{}, allSuccessful...)
	pruneStableSort("decompose", pruneInput)
	pruned := pruneInput
	var pruneInfo pruneStats
	if e.cfg.PruningEnabled {
		pruned, pruneInfo = e.pruneBranches("decompose", pruneInput, e.cfg.PruningMinScore, e.cfg.PruningToTSynthTopK)
		if len(pruned) == 0 {
			pruned = append([]BranchResult{}, pruneInput...)
			if len(pruned) > e.cfg.PruningToTSynthTopK {
				pruned = pruned[:e.cfg.PruningToTSynthTopK]
			}
		}
	} else {
		pruneInfo = pruneStats{CandidatesIn: len(pruneInput), CandidatesOut: len(pruned)}
	}
	if len(pruned) == 0 {
		pruned = append(pruned, allSuccessful[0])
	}

	contradictions := detectContradictions(pruned)
	trace.Contradictions = contradictions
	trace.Branches = pruned
	trace.Pruning = &PruningTrace{
		Mode:            "decompose",
		Enabled:         e.cfg.PruningEnabled,
		MinScore:        e.cfg.PruningMinScore,
		TopK:            e.cfg.PruningToTSynthTopK,
		CandidatesIn:    pruneInfo.CandidatesIn,
		CandidatesOut:   len(pruned),
		DroppedLowScore: pruneInfo.DroppedLowScore,
		DroppedTopK:     pruneInfo.DroppedTopK,
	}
	trace.MemoryAnchor = memAcc.Trace(e.cfg.MemoryAnchoredReasoningEnabled, "decompose")

	synthNode := Node{
		ID:        "decompose-synthesis",
		Type:      "decompose_synthesis",
		Model:     baseModel,
		StartedAt: time.Now().UTC(),
		Metadata: map[string]any{
			"subtasks_planned":  len(subtasks),
			"subtasks_executed": len(allSuccessful),
		},
	}
	synthReq := req
	synthReq.Model = baseModel
	synthReq.Messages = buildDecomposeSynthesisMessages(req.Messages, subtasks, pruned, contradictions, anchors)
	finalResp, synthErr := up.ChatCompletions(ctx, synthReq)
	synthNode.EndedAt = time.Now().UTC()
	if synthErr != nil {
		synthNode.Error = synthErr.Error()
		nodes = append(nodes, synthNode)
		trace.Nodes = nodes
		trace.Decompose = &DecomposeResult{
			SubtasksPlanned:  len(subtasks),
			SubtasksExecuted: len(allSuccessful),
			Depth:            1,
			BestScore:        pruned[0].EvaluationScore,
		}
		return model.ChatCompletionResponse{}, trace, fmt.Errorf("decompose synthesis failed: %w", synthErr)
	}
	nodes = append(nodes, synthNode)

	trace.Nodes = nodes
	trace.Decompose = &DecomposeResult{
		SubtasksPlanned:  len(subtasks),
		SubtasksExecuted: len(allSuccessful),
		Depth:            1,
		BestScore:        pruned[0].EvaluationScore,
	}
	return finalResp, trace, nil
}

func (e *Executor) resolveDecomposeConfig(req model.ChatCompletionRequest) decomposeExecutionConfig {
	cfg := decomposeExecutionConfig{
		MaxSubtasks:  e.cfg.DecomposeMaxSubtasks,
		MaxDepth:     e.cfg.DecomposeMaxDepth,
		BudgetTokens: e.cfg.DecomposeBudgetTokens,
	}
	if req.Reasoning != nil {
		if req.Reasoning.DecomposeMaxSubtasks > 0 {
			cfg.MaxSubtasks = req.Reasoning.DecomposeMaxSubtasks
		}
		if req.Reasoning.DecomposeMaxDepth > 0 {
			cfg.MaxDepth = req.Reasoning.DecomposeMaxDepth
		}
		if req.Reasoning.DecomposeBudgetTokens > 0 {
			cfg.BudgetTokens = req.Reasoning.DecomposeBudgetTokens
		}
	}
	if cfg.MaxSubtasks < 1 {
		cfg.MaxSubtasks = 1
	}
	if cfg.MaxSubtasks > e.cfg.DecomposeMaxSubtasks {
		cfg.MaxSubtasks = e.cfg.DecomposeMaxSubtasks
	}
	if cfg.MaxDepth < 1 {
		cfg.MaxDepth = 1
	}
	// v1 is intentionally single-level.
	cfg.MaxDepth = 1
	if cfg.BudgetTokens < 200 {
		cfg.BudgetTokens = 200
	}
	if cfg.BudgetTokens > e.cfg.DecomposeBudgetTokens {
		cfg.BudgetTokens = e.cfg.DecomposeBudgetTokens
	}
	return cfg
}

func (e *Executor) planSubtasks(
	ctx context.Context,
	up Upstream,
	req model.ChatCompletionRequest,
	modelID string,
	cfg decomposeExecutionConfig,
	anchors []string,
) ([]string, Node) {
	node := Node{
		ID:        "decompose-planner",
		Type:      "decompose_planner",
		Model:     modelID,
		StartedAt: time.Now().UTC(),
	}
	planReq := req
	planReq.Model = modelID
	planReq.Messages = buildDecomposePlannerMessages(req.Messages, cfg.MaxSubtasks, cfg.MaxDepth, anchors)
	resp, err := up.ChatCompletions(ctx, planReq)
	node.EndedAt = time.Now().UTC()
	if err != nil {
		node.Error = err.Error()
		synthetic := []string{decomposeSeedFromMessages(req.Messages)}
		node.Metadata = map[string]any{"planned_subtasks": len(synthetic), "fallback": "synthetic_single"}
		return synthetic, node
	}
	plannedRaw := extractAssistantText(resp)
	subtasks, parseErr := parseDecomposeSubtasks(plannedRaw, cfg.MaxSubtasks)
	if parseErr != nil || len(subtasks) == 0 {
		synthetic := []string{decomposeSeedFromMessages(req.Messages)}
		node.Metadata = map[string]any{
			"planned_subtasks": len(synthetic),
			"fallback":         "synthetic_single",
			"parse_error":      fmt.Sprintf("%v", parseErr),
		}
		return synthetic, node
	}
	node.Metadata = map[string]any{"planned_subtasks": len(subtasks)}
	return subtasks, node
}

func parseDecomposeSubtasks(raw string, maxSubtasks int) ([]string, error) {
	clean := strings.TrimSpace(raw)
	if clean == "" {
		return nil, fmt.Errorf("empty planner output")
	}
	if strings.HasPrefix(clean, "```") {
		clean = strings.TrimPrefix(clean, "```json")
		clean = strings.TrimPrefix(clean, "```")
		clean = strings.TrimSuffix(clean, "```")
		clean = strings.TrimSpace(clean)
	}

	addUnique := func(out []string, seen map[string]struct{}, item string) []string {
		v := strings.TrimSpace(item)
		if v == "" {
			return out
		}
		if _, ok := seen[v]; ok {
			return out
		}
		seen[v] = struct{}{}
		return append(out, v)
	}

	seen := map[string]struct{}{}
	result := make([]string, 0, maxSubtasks)

	var arr []string
	if err := json.Unmarshal([]byte(clean), &arr); err == nil {
		for _, item := range arr {
			result = addUnique(result, seen, item)
			if len(result) >= maxSubtasks {
				return result, nil
			}
		}
		if len(result) > 0 {
			return result, nil
		}
	}

	var obj struct {
		Subtasks []any `json:"subtasks"`
	}
	if err := json.Unmarshal([]byte(clean), &obj); err == nil && len(obj.Subtasks) > 0 {
		for _, entry := range obj.Subtasks {
			switch v := entry.(type) {
			case string:
				result = addUnique(result, seen, v)
			case map[string]any:
				for _, key := range []string{"task", "title", "objective", "instruction", "prompt"} {
					if s, ok := v[key].(string); ok && strings.TrimSpace(s) != "" {
						result = addUnique(result, seen, s)
						break
					}
				}
			}
			if len(result) >= maxSubtasks {
				break
			}
		}
		if len(result) > 0 {
			return result, nil
		}
	}

	return nil, fmt.Errorf("unable to parse planner subtasks")
}

func buildDecomposePlannerMessages(base []model.Message, maxSubtasks, maxDepth int, anchors []string) []model.Message {
	payload := map[string]any{
		"max_subtasks": maxSubtasks,
		"max_depth":    maxDepth,
		"output": map[string]any{
			"format": "json",
			"shape":  `{"subtasks":[{"task":"..."}]}`,
		},
	}
	if len(anchors) > 0 {
		payload["memory_anchors"] = anchors
	}
	data, _ := json.Marshal(payload)
	sys := model.Message{
		Role: "system",
		Content: "decompose planner: break the user request into bounded, actionable subtasks. " +
			"Return strict JSON only, no prose. Planner config: " + string(data),
	}
	out := make([]model.Message, 0, len(base)+1)
	out = append(out, sys)
	out = append(out, base...)
	return out
}

func buildDecomposeSubtaskMessages(base []model.Message, idx, total int, subtask string, anchors []string) []model.Message {
	payload := map[string]any{
		"subtask_index": idx + 1,
		"subtask_total": total,
		"subtask":       subtask,
	}
	if len(anchors) > 0 {
		payload["memory_anchors"] = anchors
	}
	data, _ := json.Marshal(payload)
	sys := model.Message{
		Role: "system",
		Content: "decompose worker context: " + string(data) +
			". Execute only this subtask and produce concrete output with assumptions and checks.",
	}
	out := make([]model.Message, 0, len(base)+1)
	out = append(out, sys)
	out = append(out, base...)
	return out
}

func buildDecomposeSynthesisMessages(
	base []model.Message,
	subtasks []string,
	branches []BranchResult,
	contradictions ContradictionReport,
	anchors []string,
) []model.Message {
	sort.Slice(branches, func(i, j int) bool {
		if branches[i].EvaluationScore == branches[j].EvaluationScore {
			return branches[i].Index < branches[j].Index
		}
		return branches[i].EvaluationScore > branches[j].EvaluationScore
	})
	top := branches
	if len(top) > 3 {
		top = top[:3]
	}
	payload := map[string]any{
		"subtasks":       subtasks,
		"top_branches":   top,
		"contradictions": contradictions,
		"instruction":    "Synthesize one final answer grounded in the strongest subtask outputs.",
	}
	if len(anchors) > 0 {
		payload["memory_anchors"] = anchors
	}
	data, _ := json.Marshal(payload)
	sys := model.Message{
		Role:    "system",
		Content: "decompose synthesizer payload: " + string(data),
	}
	out := make([]model.Message, 0, len(base)+1)
	out = append(out, sys)
	out = append(out, base...)
	return out
}

func decomposeSeedFromMessages(messages []model.Message) string {
	parts := make([]string, 0, len(messages))
	for _, m := range messages {
		if strings.EqualFold(strings.TrimSpace(m.Role), "user") && strings.TrimSpace(m.Content) != "" {
			parts = append(parts, strings.TrimSpace(m.Content))
		}
	}
	if len(parts) == 0 {
		return "Deliver a complete response for the original request."
	}
	return strings.Join(parts, "\n")
}

func (e *Executor) decomposeBaselineCandidate(
	ctx context.Context,
	up Upstream,
	req model.ChatCompletionRequest,
	modelID string,
	st state.CognitiveState,
	anchors []string,
) (BranchResult, Node, error) {
	if err := ctx.Err(); err != nil {
		return BranchResult{}, Node{}, err
	}
	node := Node{
		ID:        "decompose-baseline",
		Type:      "decompose_baseline",
		Model:     modelID,
		StartedAt: time.Now().UTC(),
	}
	baselineReq := req
	baselineReq.Model = modelID
	baselineReq.Messages = buildDecomposeBaselineMessages(req.Messages, anchors)
	resp, err := up.ChatCompletions(ctx, baselineReq)
	node.EndedAt = time.Now().UTC()
	if ctxErr := ctx.Err(); ctxErr != nil {
		node.Error = ctxErr.Error()
		return BranchResult{}, node, ctxErr
	}
	if err != nil {
		node.Error = err.Error()
		return BranchResult{}, node, err
	}
	output := extractAssistantText(resp)
	score, reason, _, _ := e.evaluateOutputWithMemory(req, output, st, true)
	node.Score = score
	return BranchResult{
		Index:            1,
		Model:            modelID,
		Output:           output,
		EvaluationScore:  score,
		EvaluationReason: reason,
		Warnings:         branchWarnings(output),
	}, node, nil
}

func buildDecomposeBaselineMessages(base []model.Message, anchors []string) []model.Message {
	payload := map[string]any{
		"instruction": "Provide one complete response for the original request with assumptions and checks.",
	}
	if len(anchors) > 0 {
		payload["memory_anchors"] = anchors
	}
	data, _ := json.Marshal(payload)
	sys := model.Message{
		Role:    "system",
		Content: "decompose baseline responder: " + string(data),
	}
	out := make([]model.Message, 0, len(base)+1)
	out = append(out, sys)
	out = append(out, base...)
	return out
}

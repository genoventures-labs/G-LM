package reasoning

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/mike/cognitive-llm/internal/model"
	"github.com/mike/cognitive-llm/internal/orchestrator"
	"github.com/mike/cognitive-llm/internal/state"
)

type fakeUpstream struct {
	calls int
}

func (f *fakeUpstream) ListModels(ctx context.Context) (model.ModelListResponse, error) {
	return model.ModelListResponse{Data: []model.ModelInfo{{ID: "qwen3:4b"}, {ID: "mistral:7b"}}}, nil
}

func (f *fakeUpstream) ChatCompletions(ctx context.Context, req model.ChatCompletionRequest) (model.ChatCompletionResponse, error) {
	f.calls++
	resp := model.ChatCompletionResponse{Model: req.Model}
	content := fmt.Sprintf("branch output %d with assumptions and tests", f.calls)
	if f.calls == 2 {
		content = "this cannot be done"
	}
	resp.Choices = []struct {
		Index   int `json:"index"`
		Message struct {
			Role      string           `json:"role"`
			Content   string           `json:"content"`
			Name      string           `json:"name,omitempty"`
			ToolCalls []model.ToolCall `json:"tool_calls,omitempty"`
		} `json:"message"`
		FinishReason string `json:"finish_reason,omitempty"`
	}{{Index: 0, Message: struct {
		Role      string           `json:"role"`
		Content   string           `json:"content"`
		Name      string           `json:"name,omitempty"`
		ToolCalls []model.ToolCall `json:"tool_calls,omitempty"`
	}{Role: "assistant", Content: content}}}
	return resp, nil
}

func TestExecutorRunsBranchesAndSynthesis(t *testing.T) {
	r := orchestrator.NewRouter("qwen3-8b-instruct-Q4_K_M", []string{"qwen3:8b"}, "qwen3:4b")
	e := NewExecutor(Config{Enabled: true, DefaultBranches: 3, MaxBranches: 5}, r)
	up := &fakeUpstream{}
	req := model.ChatCompletionRequest{
		Model:     "auto",
		Reasoning: &model.ReasoningOptions{Mode: "tot", Branches: 3},
		Messages:  []model.Message{{Role: "user", Content: "Implement and compare approaches"}},
	}
	pol := model.ModelPolicy{AllowedModels: []string{"qwen3:4b", "mistral:7b"}, PrimaryModel: "qwen3:4b"}
	st := state.CognitiveState{TaskMode: "coding"}

	resp, trace, err := e.Execute(context.Background(), up, req, pol, st)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if len(trace.Branches) == 0 {
		t.Fatal("expected branches")
	}
	if len(trace.Nodes) < 2 {
		t.Fatal("expected graph nodes")
	}
	if resp.Model == "" {
		t.Fatal("expected synthesized response")
	}
}

func TestShouldExecute(t *testing.T) {
	r := orchestrator.NewRouter("qwen3-8b-instruct-Q4_K_M", []string{"qwen3:8b"}, "qwen3:4b")
	e := NewExecutor(Config{
		Enabled:                true,
		DefaultBranches:        3,
		MaxBranches:            5,
		MCTSEnabled:            true,
		MCTSDefaultRollouts:    6,
		MCTSMaxRollouts:        12,
		MCTSDefaultDepth:       3,
		MCTSMaxDepth:           5,
		MCTSDefaultExploration: 1.2,
		MultiAgentEnabled:      true,
		MultiAgentMaxAgents:    4,
		MultiAgentMaxRounds:    2,
		MultiAgentBudgetTokens: 1200,
		DecomposeEnabled:       true,
		DecomposeMaxSubtasks:   6,
		DecomposeMaxDepth:      1,
		DecomposeBudgetTokens:  900,
	}, r)
	if !e.ShouldExecute(model.ChatCompletionRequest{Reasoning: &model.ReasoningOptions{Mode: "tot"}}, state.CognitiveState{}) {
		t.Fatal("expected tot mode to execute")
	}
	if !e.ShouldExecute(model.ChatCompletionRequest{Reasoning: &model.ReasoningOptions{Mode: "mcts"}}, state.CognitiveState{}) {
		t.Fatal("expected mcts mode to execute when enabled")
	}
	if !e.ShouldExecute(model.ChatCompletionRequest{Reasoning: &model.ReasoningOptions{Mode: "multi_agent"}}, state.CognitiveState{}) {
		t.Fatal("expected multi_agent mode to execute when enabled")
	}
	if !e.ShouldExecute(model.ChatCompletionRequest{Reasoning: &model.ReasoningOptions{Mode: "decompose"}}, state.CognitiveState{}) {
		t.Fatal("expected decompose mode to execute when enabled")
	}
	if e.ShouldExecute(model.ChatCompletionRequest{}, state.CognitiveState{}) {
		t.Fatal("expected nil reasoning to skip")
	}
}

func TestExecutorMCTSModeProducesTrace(t *testing.T) {
	r := orchestrator.NewRouter("qwen3-8b-instruct-Q4_K_M", []string{"qwen3:8b"}, "qwen3:4b")
	e := NewExecutor(Config{
		Enabled:                true,
		DefaultBranches:        3,
		MaxBranches:            5,
		MCTSEnabled:            true,
		MCTSDefaultRollouts:    5,
		MCTSMaxRollouts:        8,
		MCTSDefaultDepth:       2,
		MCTSMaxDepth:           3,
		MCTSDefaultExploration: 1.2,
	}, r)
	up := &fakeUpstream{}
	req := model.ChatCompletionRequest{
		Model: "auto",
		Reasoning: &model.ReasoningOptions{
			Mode:            "mcts",
			MCTSMaxRollouts: 4,
			MCTSMaxDepth:    2,
		},
		Messages: []model.Message{{Role: "user", Content: "Plan a rollout"}},
	}
	pol := model.ModelPolicy{AllowedModels: []string{"qwen3:4b", "mistral:7b"}, PrimaryModel: "qwen3:4b"}
	st := state.CognitiveState{TaskMode: "general"}

	resp, trace, err := e.Execute(context.Background(), up, req, pol, st)
	if err != nil {
		t.Fatalf("execute mcts failed: %v", err)
	}
	if trace.Mode != "mcts" {
		t.Fatalf("expected mcts trace mode, got %q", trace.Mode)
	}
	if trace.MCTS == nil {
		t.Fatal("expected mcts trace payload")
	}
	if trace.MCTS.Rollouts < 1 {
		t.Fatalf("expected positive rollouts, got %d", trace.MCTS.Rollouts)
	}
	if resp.Model == "" {
		t.Fatal("expected response model")
	}
}

func TestExecutorAppliesGeometryAndFusionTrace(t *testing.T) {
	r := orchestrator.NewRouter("qwen3-8b-instruct-Q4_K_M", []string{"qwen3:8b"}, "qwen3:4b")
	e := NewExecutor(Config{
		Enabled:                true,
		DefaultBranches:        3,
		MaxBranches:            5,
		ShapeTransformEnabled:  true,
		GeometryMode:           "mesh",
		WorldviewFusionEnabled: true,
		WorldviewFusionStages:  3,
	}, r)
	up := &fakeUpstream{}
	req := model.ChatCompletionRequest{
		Model: "auto",
		Reasoning: &model.ReasoningOptions{
			Mode:                   "tot",
			ShapeTransformEnabled:  true,
			GeometryMode:           "mesh",
			WorldviewFusionEnabled: true,
			WorldviewFusionStages:  3,
			WorldviewProfiles:      []string{"risk_first", "performance_first"},
		},
		Messages: []model.Message{{Role: "user", Content: "Implement and compare approaches"}},
	}
	pol := model.ModelPolicy{AllowedModels: []string{"qwen3:4b", "mistral:7b"}, PrimaryModel: "qwen3:4b"}
	st := state.CognitiveState{TaskMode: "coding"}

	_, trace, err := e.Execute(context.Background(), up, req, pol, st)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if trace.GeometryMode != "mesh" {
		t.Fatalf("expected geometry mode mesh, got %q", trace.GeometryMode)
	}
	if len(trace.GeometryPath) == 0 {
		t.Fatal("expected geometry path")
	}
	if len(trace.FusionStageScores) != 3 {
		t.Fatalf("expected 3 fusion stages, got %d", len(trace.FusionStageScores))
	}
}

type mctsFailingUpstream struct{}

func (m *mctsFailingUpstream) ListModels(ctx context.Context) (model.ModelListResponse, error) {
	return model.ModelListResponse{Data: []model.ModelInfo{{ID: "qwen3:4b"}, {ID: "mistral:7b"}}}, nil
}

func (m *mctsFailingUpstream) ChatCompletions(ctx context.Context, req model.ChatCompletionRequest) (model.ChatCompletionResponse, error) {
	if len(req.Messages) > 0 && strings.HasPrefix(req.Messages[0].Content, "mcts_agent path context:") {
		return model.ChatCompletionResponse{}, fmt.Errorf("simulated mcts rollout failure")
	}
	resp := model.ChatCompletionResponse{Model: req.Model}
	resp.Choices = []struct {
		Index   int `json:"index"`
		Message struct {
			Role      string           `json:"role"`
			Content   string           `json:"content"`
			Name      string           `json:"name,omitempty"`
			ToolCalls []model.ToolCall `json:"tool_calls,omitempty"`
		} `json:"message"`
		FinishReason string `json:"finish_reason,omitempty"`
	}{{
		Index: 0,
		Message: struct {
			Role      string           `json:"role"`
			Content   string           `json:"content"`
			Name      string           `json:"name,omitempty"`
			ToolCalls []model.ToolCall `json:"tool_calls,omitempty"`
		}{Role: "assistant", Content: "baseline response"},
	}}
	return resp, nil
}

func TestExecutorMCTSFallsBackToBaselineWhenRolloutsFail(t *testing.T) {
	r := orchestrator.NewRouter("qwen3-8b-instruct-Q4_K_M", []string{"qwen3:8b"}, "qwen3:4b")
	e := NewExecutor(Config{
		Enabled:                true,
		MCTSEnabled:            true,
		MCTSDefaultRollouts:    4,
		MCTSMaxRollouts:        8,
		MCTSDefaultDepth:       2,
		MCTSMaxDepth:           3,
		MCTSDefaultExploration: 1.2,
	}, r)
	up := &mctsFailingUpstream{}
	req := model.ChatCompletionRequest{
		Model: "auto",
		Reasoning: &model.ReasoningOptions{
			Mode:            "mcts",
			MCTSMaxRollouts: 4,
			MCTSMaxDepth:    2,
		},
		Messages: []model.Message{{Role: "user", Content: "Plan a rollout"}},
	}
	pol := model.ModelPolicy{AllowedModels: []string{"qwen3:4b", "mistral:7b"}, PrimaryModel: "qwen3:4b"}
	st := state.CognitiveState{TaskMode: "general"}

	resp, trace, err := e.Execute(context.Background(), up, req, pol, st)
	if err != nil {
		t.Fatalf("expected mcts baseline fallback to succeed, got %v", err)
	}
	if trace.MCTS == nil {
		t.Fatal("expected mcts trace payload")
	}
	if trace.MCTS.Fallback != "direct_baseline" {
		t.Fatalf("expected direct_baseline fallback, got %q", trace.MCTS.Fallback)
	}
	if resp.Model == "" {
		t.Fatal("expected response model")
	}
}

func TestEvaluateOutputCurveDisabledMatchesLegacy(t *testing.T) {
	r := orchestrator.NewRouter("qwen3-8b-instruct-Q4_K_M", []string{"qwen3:8b"}, "qwen3:4b")
	e := NewExecutor(Config{
		Enabled:                true,
		SelfEvalCurveEnabled:   false,
		MCTSEnabled:            true,
		MCTSDefaultRollouts:    4,
		MCTSMaxRollouts:        8,
		MCTSDefaultDepth:       2,
		MCTSMaxDepth:           3,
		MCTSDefaultExploration: 1.2,
	}, r)
	output := "This response includes assumptions and tests with enough words to trigger baseline quality expansion and remain deterministic."
	req := model.ChatCompletionRequest{Reasoning: &model.ReasoningOptions{SelfEvaluate: true}}
	st := state.CognitiveState{TaskMode: "coding"}

	gotScore, gotReason := e.evaluateOutput(req, output, st, false)
	wantScore, wantReason := e.legacySelfEvaluate(output, st)

	if gotScore != wantScore {
		t.Fatalf("expected score %f, got %f", wantScore, gotScore)
	}
	if gotReason != wantReason {
		t.Fatalf("expected reason %q, got %q", wantReason, gotReason)
	}
}

func TestEvaluateOutputCurveEnabledAppliesBands(t *testing.T) {
	r := orchestrator.NewRouter("qwen3-8b-instruct-Q4_K_M", []string{"qwen3:8b"}, "qwen3:4b")
	e := NewExecutor(Config{
		Enabled:                 true,
		SelfEvalCurveEnabled:    true,
		SelfEvalCurveLowMax:     0.60,
		SelfEvalCurveMidMax:     0.82,
		SelfEvalCurveLowWeight:  0.90,
		SelfEvalCurveMidWeight:  1.00,
		SelfEvalCurveHighWeight: 1.08,
		SelfEvalCurveBias:       0.00,
		MCTSEnabled:             true,
		MCTSDefaultRollouts:     4,
		MCTSMaxRollouts:         8,
		MCTSDefaultDepth:        2,
		MCTSMaxDepth:            3,
		MCTSDefaultExploration:  1.2,
	}, r)
	req := model.ChatCompletionRequest{Reasoning: &model.ReasoningOptions{SelfEvaluate: true}}

	lowScore, lowReason := e.evaluateOutput(req, "", state.CognitiveState{TaskMode: "general"}, false)
	midScore, midReason := e.evaluateOutput(req, "word1 word2 word3 word4 word5 word6 word7 word8 word9 word10 word11 word12 word13 word14 word15 word16 word17 word18 word19 word20 word21 word22 word23 word24 word25 word26 word27 word28 word29 word30 word31 word32 word33 word34 word35 word36 word37 word38 word39 word40 word41 word42", state.CognitiveState{TaskMode: "general"}, false)
	highScore, highReason := e.evaluateOutput(req, "word1 word2 word3 word4 word5 word6 word7 word8 word9 word10 word11 word12 word13 word14 word15 word16 word17 word18 word19 word20 word21 word22 word23 word24 word25 word26 word27 word28 word29 word30 word31 word32 word33 word34 word35 word36 word37 word38 word39 word40 word41 word42 assumption test", state.CognitiveState{TaskMode: "coding"}, false)

	if lowScore != 0.18 {
		t.Fatalf("expected low band score 0.18, got %f", lowScore)
	}
	if midScore != 0.70 {
		t.Fatalf("expected mid band score 0.70, got %f", midScore)
	}
	if highScore != 0.929 {
		t.Fatalf("expected high band score 0.929, got %f", highScore)
	}
	if lowReason != "heuristic_quality_curve" || midReason != "heuristic_quality_curve" || highReason != "heuristic_quality_curve" {
		t.Fatalf("expected curve reason for all scores, got low=%q mid=%q high=%q", lowReason, midReason, highReason)
	}
}

func TestEvaluateOutputSelfEvaluateDisabledUsesNeutralScore(t *testing.T) {
	r := orchestrator.NewRouter("qwen3-8b-instruct-Q4_K_M", []string{"qwen3:8b"}, "qwen3:4b")
	e := NewExecutor(Config{
		Enabled:                 true,
		SelfEvalCurveEnabled:    true,
		SelfEvalCurveLowMax:     0.60,
		SelfEvalCurveMidMax:     0.82,
		SelfEvalCurveLowWeight:  0.90,
		SelfEvalCurveMidWeight:  1.00,
		SelfEvalCurveHighWeight: 1.08,
		MCTSEnabled:             true,
		MCTSDefaultRollouts:     4,
		MCTSMaxRollouts:         8,
		MCTSDefaultDepth:        2,
		MCTSMaxDepth:            3,
		MCTSDefaultExploration:  1.2,
	}, r)
	req := model.ChatCompletionRequest{Reasoning: &model.ReasoningOptions{SelfEvaluate: false}}
	st := state.CognitiveState{TaskMode: "coding", TopicDrift: 0.9, MoodShift: 0.9, MicroSwitches: []string{"fast"}}

	score, reason := e.evaluateOutput(req, "high quality output with assumption and test", st, true)
	if score != 0.5 {
		t.Fatalf("expected neutral score 0.5, got %f", score)
	}
	if reason != "self_evaluate_disabled" {
		t.Fatalf("expected disabled reason, got %q", reason)
	}
}

func TestEvaluateOutputMemoryAnchorBonusApplied(t *testing.T) {
	r := orchestrator.NewRouter("qwen3-8b-instruct-Q4_K_M", []string{"qwen3:8b"}, "qwen3:4b")
	e := NewExecutor(Config{
		Enabled:                            true,
		MemoryAnchoredReasoningEnabled:     true,
		MemoryAnchoredReasoningMaxAnchors:  3,
		MemoryAnchoredReasoningMinCoverage: 0.34,
		MemoryAnchoredReasoningScoreBonus:  0.06,
		MCTSEnabled:                        true,
		MCTSDefaultRollouts:                4,
		MCTSMaxRollouts:                    8,
		MCTSDefaultDepth:                   2,
		MCTSMaxDepth:                       3,
		MCTSDefaultExploration:             1.2,
	}, r)
	req := model.ChatCompletionRequest{
		Reasoning:        &model.ReasoningOptions{SelfEvaluate: true},
		MemoryAnchorKeys: []string{"rollout", "incident", "controls"},
	}
	score, _, coverage, bonus := e.evaluateOutputWithMemory(req, "rollout controls documented", state.CognitiveState{TaskMode: "general"}, false)
	if coverage < 0.666 {
		t.Fatalf("expected coverage around 0.667, got %f", coverage)
	}
	if bonus <= 0 {
		t.Fatalf("expected bonus > 0, got %f", bonus)
	}
	if score < 0.59 {
		t.Fatalf("expected anchored score increase, got %f", score)
	}
}

func TestEvaluateOutputMemoryAnchorBonusBelowThresholdNotApplied(t *testing.T) {
	r := orchestrator.NewRouter("qwen3-8b-instruct-Q4_K_M", []string{"qwen3:8b"}, "qwen3:4b")
	e := NewExecutor(Config{
		Enabled:                            true,
		MemoryAnchoredReasoningEnabled:     true,
		MemoryAnchoredReasoningMaxAnchors:  3,
		MemoryAnchoredReasoningMinCoverage: 0.9,
		MemoryAnchoredReasoningScoreBonus:  0.06,
		MCTSEnabled:                        true,
		MCTSDefaultRollouts:                4,
		MCTSMaxRollouts:                    8,
		MCTSDefaultDepth:                   2,
		MCTSMaxDepth:                       3,
		MCTSDefaultExploration:             1.2,
	}, r)
	req := model.ChatCompletionRequest{
		Reasoning:        &model.ReasoningOptions{SelfEvaluate: true},
		MemoryAnchorKeys: []string{"rollout", "incident", "controls"},
	}
	score, _, coverage, bonus := e.evaluateOutputWithMemory(req, "rollout controls documented", state.CognitiveState{TaskMode: "general"}, false)
	if coverage <= 0 {
		t.Fatalf("expected non-zero coverage, got %f", coverage)
	}
	if bonus != 0 {
		t.Fatalf("expected zero bonus below threshold, got %f", bonus)
	}
	if score != 0.55 {
		t.Fatalf("expected legacy score unchanged, got %f", score)
	}
}

func TestToTSelfEvaluateDisabledUsesNeutralBranchScores(t *testing.T) {
	r := orchestrator.NewRouter("qwen3-8b-instruct-Q4_K_M", []string{"qwen3:8b"}, "qwen3:4b")
	e := NewExecutor(Config{
		Enabled:                true,
		DefaultBranches:        3,
		MaxBranches:            5,
		MCTSEnabled:            true,
		MCTSDefaultRollouts:    4,
		MCTSMaxRollouts:        8,
		MCTSDefaultDepth:       2,
		MCTSMaxDepth:           3,
		MCTSDefaultExploration: 1.2,
	}, r)
	up := &fakeUpstream{}
	req := model.ChatCompletionRequest{
		Model: "auto",
		Reasoning: &model.ReasoningOptions{
			Mode:         "tot",
			Branches:     3,
			SelfEvaluate: false,
		},
		Messages: []model.Message{{Role: "user", Content: "compare options"}},
	}
	pol := model.ModelPolicy{AllowedModels: []string{"qwen3:4b", "mistral:7b"}, PrimaryModel: "qwen3:4b"}
	st := state.CognitiveState{TaskMode: "coding", TopicDrift: 0.7, MoodShift: 0.8, MicroSwitches: []string{"shift"}}

	_, trace, err := e.Execute(context.Background(), up, req, pol, st)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if len(trace.Branches) == 0 {
		t.Fatal("expected branches")
	}
	for _, b := range trace.Branches {
		if b.EvaluationScore != 0.5 {
			t.Fatalf("expected neutral score 0.5, got %f", b.EvaluationScore)
		}
		if b.EvaluationReason != "self_evaluate_disabled" {
			t.Fatalf("expected disabled reason, got %q", b.EvaluationReason)
		}
	}
}

func TestNewExecutorSelfEvalCurveInvalidValuesFallback(t *testing.T) {
	r := orchestrator.NewRouter("qwen3-8b-instruct-Q4_K_M", []string{"qwen3:8b"}, "qwen3:4b")
	e := NewExecutor(Config{
		Enabled:                 true,
		SelfEvalCurveEnabled:    true,
		SelfEvalCurveLowMax:     -1,
		SelfEvalCurveMidMax:     -1,
		SelfEvalCurveLowWeight:  -1,
		SelfEvalCurveMidWeight:  -1,
		SelfEvalCurveHighWeight: -1,
		MCTSEnabled:             true,
		MCTSDefaultRollouts:     4,
		MCTSMaxRollouts:         8,
		MCTSDefaultDepth:        2,
		MCTSMaxDepth:            3,
		MCTSDefaultExploration:  1.2,
	}, r)

	if e.cfg.SelfEvalCurveLowMax != 0.60 {
		t.Fatalf("expected low max fallback 0.60, got %f", e.cfg.SelfEvalCurveLowMax)
	}
	if e.cfg.SelfEvalCurveMidMax != 0.82 {
		t.Fatalf("expected mid max fallback 0.82, got %f", e.cfg.SelfEvalCurveMidMax)
	}
	if e.cfg.SelfEvalCurveLowWeight != 0.90 {
		t.Fatalf("expected low weight fallback 0.90, got %f", e.cfg.SelfEvalCurveLowWeight)
	}
	if e.cfg.SelfEvalCurveMidWeight != 1.00 {
		t.Fatalf("expected mid weight fallback 1.00, got %f", e.cfg.SelfEvalCurveMidWeight)
	}
	if e.cfg.SelfEvalCurveHighWeight != 1.08 {
		t.Fatalf("expected high weight fallback 1.08, got %f", e.cfg.SelfEvalCurveHighWeight)
	}
}

func TestNewExecutorMemoryAnchorInvalidValuesClamp(t *testing.T) {
	r := orchestrator.NewRouter("qwen3-8b-instruct-Q4_K_M", []string{"qwen3:8b"}, "qwen3:4b")
	e := NewExecutor(Config{
		Enabled:                            true,
		MemoryAnchoredReasoningEnabled:     true,
		MemoryAnchoredReasoningMaxAnchors:  0,
		MemoryAnchoredReasoningMinCoverage: -1,
		MemoryAnchoredReasoningScoreBonus:  0.99,
	}, r)
	if e.cfg.MemoryAnchoredReasoningMaxAnchors != 3 {
		t.Fatalf("expected max anchors fallback 3, got %d", e.cfg.MemoryAnchoredReasoningMaxAnchors)
	}
	if e.cfg.MemoryAnchoredReasoningMinCoverage != 0 {
		t.Fatalf("expected min coverage clamp 0, got %f", e.cfg.MemoryAnchoredReasoningMinCoverage)
	}
	if e.cfg.MemoryAnchoredReasoningScoreBonus != 0.20 {
		t.Fatalf("expected score bonus clamp 0.20, got %f", e.cfg.MemoryAnchoredReasoningScoreBonus)
	}
}

func TestPruneBranchesDropsLowScoreAndTopK(t *testing.T) {
	r := orchestrator.NewRouter("qwen3-8b-instruct-Q4_K_M", []string{"qwen3:8b"}, "qwen3:4b")
	e := NewExecutor(Config{
		Enabled:             true,
		PruningEnabled:      true,
		PruningMinScore:     0.6,
		PruningToTTopK:      3,
		PruningToTSynthTopK: 2,
	}, r)

	in := []BranchResult{
		{Index: 1, Output: "a", EvaluationScore: 0.40},
		{Index: 2, Output: "b", EvaluationScore: 0.95},
		{Index: 3, Output: "c", EvaluationScore: 0.82},
		{Index: 4, Output: "d", EvaluationScore: 0.88},
	}

	out, stats := e.pruneBranches("tot", in, 0.6, 2)
	if len(out) != 2 {
		t.Fatalf("expected 2 survivors, got %d", len(out))
	}
	if out[0].Index != 2 || out[1].Index != 4 {
		t.Fatalf("unexpected survivor ordering: %#v", out)
	}
	if stats.CandidatesIn != 4 || stats.CandidatesOut != 2 {
		t.Fatalf("unexpected in/out stats: %#v", stats)
	}
	if stats.DroppedLowScore != 1 {
		t.Fatalf("expected one low-score drop, got %d", stats.DroppedLowScore)
	}
	if stats.DroppedTopK != 1 {
		t.Fatalf("expected one top-k drop, got %d", stats.DroppedTopK)
	}
}

func TestPruneStableSortKeepsDeterministicTieBreak(t *testing.T) {
	items := []BranchResult{
		{Index: 3, Output: "x", EvaluationScore: 0.8},
		{Index: 1, Output: "x", EvaluationScore: 0.8},
		{Index: 2, Output: "x", EvaluationScore: 0.8},
	}
	pruneStableSort("tot", items)
	if items[0].Index != 1 || items[1].Index != 2 || items[2].Index != 3 {
		t.Fatalf("expected index tie-break order, got %#v", items)
	}
}

func TestToTPruningLimitsSynthesisCandidates(t *testing.T) {
	r := orchestrator.NewRouter("qwen3-8b-instruct-Q4_K_M", []string{"qwen3:8b"}, "qwen3:4b")
	e := NewExecutor(Config{
		Enabled:                true,
		DefaultBranches:        4,
		MaxBranches:            5,
		PruningEnabled:         true,
		PruningMinScore:        0.0,
		PruningToTTopK:         3,
		PruningToTSynthTopK:    2,
		MCTSEnabled:            true,
		MCTSDefaultRollouts:    4,
		MCTSMaxRollouts:        8,
		MCTSDefaultDepth:       2,
		MCTSMaxDepth:           3,
		MCTSDefaultExploration: 1.2,
	}, r)
	up := &fakeUpstream{}
	req := model.ChatCompletionRequest{
		Model:     "auto",
		Reasoning: &model.ReasoningOptions{Mode: "tot", Branches: 4},
		Messages:  []model.Message{{Role: "user", Content: "compare options"}},
	}
	pol := model.ModelPolicy{AllowedModels: []string{"qwen3:4b", "mistral:7b"}, PrimaryModel: "qwen3:4b"}
	st := state.CognitiveState{TaskMode: "coding"}

	_, trace, err := e.Execute(context.Background(), up, req, pol, st)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if len(trace.Branches) != 2 {
		t.Fatalf("expected synth candidate cap 2, got %d", len(trace.Branches))
	}
	if trace.Pruning == nil {
		t.Fatal("expected pruning trace")
	}
	if trace.Pruning.CandidatesOut != 2 {
		t.Fatalf("expected pruning out=2, got %d", trace.Pruning.CandidatesOut)
	}
}

func TestNewExecutorPruningInvalidValuesFallback(t *testing.T) {
	r := orchestrator.NewRouter("qwen3-8b-instruct-Q4_K_M", []string{"qwen3:8b"}, "qwen3:4b")
	e := NewExecutor(Config{
		Enabled:              true,
		PruningEnabled:       true,
		PruningMinScore:      5,
		PruningToTTopK:       0,
		PruningToTSynthTopK:  99,
		PruningMCTSPoolTopK:  0,
		PruningMCTSSynthTopK: 99,
		PruningMARoundTopK:   0,
		PruningMASynthTopK:   99,
	}, r)
	if e.cfg.PruningMinScore != 1 {
		t.Fatalf("expected clamped min score 1, got %f", e.cfg.PruningMinScore)
	}
	if e.cfg.PruningToTTopK != 3 || e.cfg.PruningToTSynthTopK != 3 {
		t.Fatalf("unexpected tot pruning fallback values: topk=%d synth=%d", e.cfg.PruningToTTopK, e.cfg.PruningToTSynthTopK)
	}
	if e.cfg.PruningMCTSPoolTopK != 6 || e.cfg.PruningMCTSSynthTopK != 6 {
		t.Fatalf("unexpected mcts pruning fallback values: pool=%d synth=%d", e.cfg.PruningMCTSPoolTopK, e.cfg.PruningMCTSSynthTopK)
	}
	if e.cfg.PruningMARoundTopK != 4 || e.cfg.PruningMASynthTopK != 4 {
		t.Fatalf("unexpected ma pruning fallback values: round=%d synth=%d", e.cfg.PruningMARoundTopK, e.cfg.PruningMASynthTopK)
	}
}

func TestNewExecutorMCTSV2InvalidValuesFallback(t *testing.T) {
	r := orchestrator.NewRouter("qwen3-8b-instruct-Q4_K_M", []string{"qwen3:8b"}, "qwen3:4b")
	e := NewExecutor(Config{
		Enabled:             true,
		MCTSEnabled:         true,
		MCTSV2Enabled:       true,
		MCTSEarlyStopWindow: 0,
		MCTSEarlyStopDelta:  -1,
	}, r)
	if e.cfg.MCTSEarlyStopWindow != 4 {
		t.Fatalf("expected early stop window fallback 4, got %d", e.cfg.MCTSEarlyStopWindow)
	}
	if e.cfg.MCTSEarlyStopDelta != 0 {
		t.Fatalf("expected early stop delta clamped to 0, got %f", e.cfg.MCTSEarlyStopDelta)
	}
}

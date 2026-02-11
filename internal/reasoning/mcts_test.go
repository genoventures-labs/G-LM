package reasoning

import (
	"context"
	"testing"

	"github.com/mike/cognitive-llm/internal/model"
	"github.com/mike/cognitive-llm/internal/orchestrator"
	"github.com/mike/cognitive-llm/internal/state"
)

func TestMCTSSelectChildPrefersHigherUCT(t *testing.T) {
	root := &mctsNode{Visits: 10}
	a := &mctsNode{Visits: 5, Value: 3.0, Prior: 0.5, Depth: 1, Parent: root}
	b := &mctsNode{Visits: 2, Value: 1.8, Prior: 0.5, Depth: 1, Parent: root}
	root.Children = []*mctsNode{a, b}
	child, _ := selectMCTSChild(root, 1.2, false)
	if child == nil {
		t.Fatal("expected child")
	}
	if child != b {
		t.Fatalf("expected higher UCT child b, got %#v", child)
	}
}

func TestMCTSSelectChildSupportsV2Path(t *testing.T) {
	root := &mctsNode{Visits: 10}
	a := &mctsNode{Visits: 5, Value: 3.0, Prior: 0.2, Depth: 1, Parent: root}
	b := &mctsNode{Visits: 2, Value: 1.8, Prior: 0.8, Depth: 1, Parent: root}
	root.Children = []*mctsNode{a, b}
	child, _ := selectMCTSChild(root, 1.2, true)
	if child == nil {
		t.Fatal("expected child")
	}
}

func TestMCTSBackpropUpdatesVisitsAndValue(t *testing.T) {
	root := &mctsNode{}
	mid := &mctsNode{Parent: root}
	leaf := &mctsNode{Parent: mid}
	chain := []*mctsNode{root, mid, leaf}
	backpropagateMCTS(chain, 0.75)
	for i, n := range chain {
		if n.Visits != 1 {
			t.Fatalf("node %d expected visits=1, got %d", i, n.Visits)
		}
		if n.Value != 0.75 {
			t.Fatalf("node %d expected value=0.75, got %f", i, n.Value)
		}
	}
}

func TestMCTSResolveBudgetCaps(t *testing.T) {
	r := orchestrator.NewRouter("qwen3-8b-instruct-Q4_K_M", []string{"qwen3:8b"}, "qwen3:4b")
	e := NewExecutor(Config{
		Enabled:                true,
		MCTSEnabled:            true,
		MCTSDefaultRollouts:    12,
		MCTSMaxRollouts:        24,
		MCTSDefaultDepth:       3,
		MCTSMaxDepth:           5,
		MCTSDefaultExploration: 1.2,
	}, r)
	req := model.ChatCompletionRequest{
		Reasoning: &model.ReasoningOptions{
			Mode:            "mcts",
			MCTSMaxRollouts: 99,
			MCTSMaxDepth:    99,
			MCTSExploration: 9,
		},
	}
	if got := e.resolveMCTSRollouts(req); got != 24 {
		t.Fatalf("expected rollout cap 24, got %d", got)
	}
	if got := e.resolveMCTSDepth(req); got != 5 {
		t.Fatalf("expected depth cap 5, got %d", got)
	}
	if got := e.resolveMCTSExploration(req); got != 3 {
		t.Fatalf("expected exploration cap 3, got %f", got)
	}
}

func TestMCTSContextCancelledExits(t *testing.T) {
	r := orchestrator.NewRouter("qwen3-8b-instruct-Q4_K_M", []string{"qwen3:8b"}, "qwen3:4b")
	e := NewExecutor(Config{
		Enabled:                true,
		MCTSEnabled:            true,
		MCTSDefaultRollouts:    12,
		MCTSMaxRollouts:        24,
		MCTSDefaultDepth:       3,
		MCTSMaxDepth:           5,
		MCTSDefaultExploration: 1.2,
	}, r)
	up := &fakeUpstream{}
	req := model.ChatCompletionRequest{
		Model: "auto",
		Reasoning: &model.ReasoningOptions{
			Mode:            "mcts",
			MCTSMaxRollouts: 8,
			MCTSMaxDepth:    3,
		},
		Messages: []model.Message{{Role: "user", Content: "test"}},
	}
	pol := model.ModelPolicy{AllowedModels: []string{"qwen3:4b", "mistral:7b"}, PrimaryModel: "qwen3:4b"}
	st := state.CognitiveState{TaskMode: "general"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, trace, err := e.Execute(ctx, up, req, pol, st)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
	if trace.Mode != "mcts" {
		t.Fatalf("expected mcts mode trace, got %q", trace.Mode)
	}
}

func TestMCTSSelfEvaluateDisabledUsesNeutralScores(t *testing.T) {
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
	up := &fakeUpstream{}
	req := model.ChatCompletionRequest{
		Model: "auto",
		Reasoning: &model.ReasoningOptions{
			Mode:            "mcts",
			SelfEvaluate:    false,
			MCTSMaxRollouts: 4,
			MCTSMaxDepth:    2,
		},
		Messages: []model.Message{{Role: "user", Content: "test"}},
	}
	pol := model.ModelPolicy{AllowedModels: []string{"qwen3:4b", "mistral:7b"}, PrimaryModel: "qwen3:4b"}
	st := state.CognitiveState{TaskMode: "coding", TopicDrift: 0.9, MoodShift: 0.9, MicroSwitches: []string{"shift"}}

	_, trace, err := e.Execute(context.Background(), up, req, pol, st)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if trace.MCTS == nil {
		t.Fatal("expected mcts trace")
	}
	if trace.MCTS.BestScore != 0.5 {
		t.Fatalf("expected neutral best score 0.5, got %f", trace.MCTS.BestScore)
	}
	for _, b := range trace.Branches {
		if b.EvaluationScore != 0.5 {
			t.Fatalf("expected neutral branch score 0.5, got %f", b.EvaluationScore)
		}
	}
}

func TestMCTSCurveEnabledAffectsBestScore(t *testing.T) {
	r := orchestrator.NewRouter("qwen3-8b-instruct-Q4_K_M", []string{"qwen3:8b"}, "qwen3:4b")
	e := NewExecutor(Config{
		Enabled:                 true,
		MCTSEnabled:             true,
		SelfEvalCurveEnabled:    true,
		SelfEvalCurveLowMax:     0.60,
		SelfEvalCurveMidMax:     0.82,
		SelfEvalCurveLowWeight:  0.90,
		SelfEvalCurveMidWeight:  1.20,
		SelfEvalCurveHighWeight: 1.08,
		MCTSDefaultRollouts:     4,
		MCTSMaxRollouts:         8,
		MCTSDefaultDepth:        2,
		MCTSMaxDepth:            3,
		MCTSDefaultExploration:  1.2,
	}, r)
	up := &fakeUpstream{}
	req := model.ChatCompletionRequest{
		Model: "auto",
		Reasoning: &model.ReasoningOptions{
			Mode:            "mcts",
			SelfEvaluate:    true,
			MCTSMaxRollouts: 4,
			MCTSMaxDepth:    2,
		},
		Messages: []model.Message{{Role: "user", Content: "test"}},
	}
	pol := model.ModelPolicy{AllowedModels: []string{"qwen3:4b", "mistral:7b"}, PrimaryModel: "qwen3:4b"}
	st := state.CognitiveState{TaskMode: "coding"}

	_, trace, err := e.Execute(context.Background(), up, req, pol, st)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if trace.MCTS == nil {
		t.Fatal("expected mcts trace")
	}
	if trace.MCTS.BestScore <= 0.71 {
		t.Fatalf("expected curved best score above legacy mid score, got %f", trace.MCTS.BestScore)
	}
}

func TestMCTSPruningPoolAndSynthCaps(t *testing.T) {
	r := orchestrator.NewRouter("qwen3-8b-instruct-Q4_K_M", []string{"qwen3:8b"}, "qwen3:4b")
	e := NewExecutor(Config{
		Enabled:                true,
		MCTSEnabled:            true,
		PruningEnabled:         true,
		PruningMinScore:        0.0,
		PruningMCTSPoolTopK:    2,
		PruningMCTSSynthTopK:   1,
		MCTSDefaultRollouts:    6,
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
			MCTSMaxRollouts: 6,
			MCTSMaxDepth:    2,
		},
		Messages: []model.Message{{Role: "user", Content: "test"}},
	}
	pol := model.ModelPolicy{AllowedModels: []string{"qwen3:4b", "mistral:7b"}, PrimaryModel: "qwen3:4b"}
	st := state.CognitiveState{TaskMode: "general"}

	_, trace, err := e.Execute(context.Background(), up, req, pol, st)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if len(trace.Branches) != 1 {
		t.Fatalf("expected synth top-k=1 branch, got %d", len(trace.Branches))
	}
	if trace.Pruning == nil {
		t.Fatal("expected pruning trace")
	}
	if trace.Pruning.CandidatesOut != 1 {
		t.Fatalf("expected pruning out=1, got %d", trace.Pruning.CandidatesOut)
	}
}

func TestMCTSPruningNoSurvivorsFallsBackBaseline(t *testing.T) {
	r := orchestrator.NewRouter("qwen3-8b-instruct-Q4_K_M", []string{"qwen3:8b"}, "qwen3:4b")
	e := NewExecutor(Config{
		Enabled:                true,
		MCTSEnabled:            true,
		PruningEnabled:         true,
		PruningMinScore:        0.95,
		PruningMCTSPoolTopK:    2,
		PruningMCTSSynthTopK:   1,
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
			Mode:            "mcts",
			MCTSMaxRollouts: 4,
			MCTSMaxDepth:    2,
		},
		Messages: []model.Message{{Role: "user", Content: "test"}},
	}
	pol := model.ModelPolicy{AllowedModels: []string{"qwen3:4b", "mistral:7b"}, PrimaryModel: "qwen3:4b"}
	st := state.CognitiveState{TaskMode: "general"}

	_, trace, err := e.Execute(context.Background(), up, req, pol, st)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if trace.MCTS == nil {
		t.Fatal("expected mcts trace")
	}
	if trace.MCTS.BestScore <= 0 {
		t.Fatalf("expected baseline best score >0, got %f", trace.MCTS.BestScore)
	}
	if len(trace.Branches) == 0 {
		t.Fatal("expected at least one branch after baseline fallback")
	}
}

func TestMCTSEarlyStopTrigger(t *testing.T) {
	if !shouldEarlyStop([]float64{0.7, 0.7005, 0.7007, 0.7008}, 4, 0.01) {
		t.Fatal("expected early stop to trigger for low improvement")
	}
	if shouldEarlyStop([]float64{0.6, 0.63, 0.66, 0.69}, 4, 0.01) {
		t.Fatal("did not expect early stop for improving sequence")
	}
}

func TestMCTSV2TraceMetadata(t *testing.T) {
	r := orchestrator.NewRouter("qwen3-8b-instruct-Q4_K_M", []string{"qwen3:8b"}, "qwen3:4b")
	e := NewExecutor(Config{
		Enabled:                true,
		MCTSEnabled:            true,
		MCTSV2Enabled:          true,
		MCTSEarlyStopWindow:    3,
		MCTSEarlyStopDelta:     0.2,
		MCTSDefaultRollouts:    6,
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
			MCTSV2Enabled:   true,
			MCTSMaxRollouts: 6,
			MCTSMaxDepth:    2,
		},
		Messages: []model.Message{{Role: "user", Content: "test"}},
	}
	pol := model.ModelPolicy{AllowedModels: []string{"qwen3:4b", "mistral:7b"}, PrimaryModel: "qwen3:4b"}
	st := state.CognitiveState{TaskMode: "coding"}
	_, trace, err := e.Execute(context.Background(), up, req, pol, st)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if trace.MCTS == nil {
		t.Fatal("expected mcts trace")
	}
	if trace.MCTS.RolloutsExecuted < 1 {
		t.Fatalf("expected executed rollouts > 0, got %d", trace.MCTS.RolloutsExecuted)
	}
}

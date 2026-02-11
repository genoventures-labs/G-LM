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
	child, _ := selectMCTSChild(root, 1.2)
	if child == nil {
		t.Fatal("expected child")
	}
	if child != b {
		t.Fatalf("expected higher UCT child b, got %#v", child)
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

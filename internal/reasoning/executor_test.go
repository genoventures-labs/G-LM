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

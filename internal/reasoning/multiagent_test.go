package reasoning

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mike/cognitive-llm/internal/model"
	"github.com/mike/cognitive-llm/internal/orchestrator"
	"github.com/mike/cognitive-llm/internal/state"
)

type selectiveFailUpstream struct{}

func (s *selectiveFailUpstream) ListModels(ctx context.Context) (model.ModelListResponse, error) {
	return model.ModelListResponse{Data: []model.ModelInfo{{ID: "qwen3:4b"}, {ID: "mistral:7b"}}}, nil
}

func (s *selectiveFailUpstream) ChatCompletions(ctx context.Context, req model.ChatCompletionRequest) (model.ChatCompletionResponse, error) {
	content := req.Messages[0].Content
	if strings.HasPrefix(content, "multi_agent role context:") || strings.HasPrefix(content, "mcts_agent path context:") {
		return model.ChatCompletionResponse{}, errors.New("simulated branch failure")
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
		}{Role: "assistant", Content: "baseline answer with controls"},
	}}
	return resp, nil
}

func TestMultiAgentRolesDeterministic(t *testing.T) {
	r := orchestrator.NewRouter("qwen3-8b-instruct-Q4_K_M", []string{"qwen3:8b"}, "qwen3:4b")
	e := NewExecutor(Config{
		Enabled:                true,
		MultiAgentEnabled:      true,
		MultiAgentMaxAgents:    4,
		MultiAgentMaxRounds:    2,
		MultiAgentBudgetTokens: 1200,
	}, r)
	roles := e.multiAgentRoles(4)
	if len(roles) != 3 {
		t.Fatalf("expected 3 worker roles, got %d", len(roles))
	}
	if roles[0].Name != "planner" || roles[1].Name != "researcher" || roles[2].Name != "critic" {
		t.Fatalf("unexpected role order: %#v", roles)
	}
}

func TestMultiAgentBudgetEnforced(t *testing.T) {
	r := orchestrator.NewRouter("qwen3-8b-instruct-Q4_K_M", []string{"qwen3:8b"}, "qwen3:4b")
	e := NewExecutor(Config{
		Enabled:                true,
		MultiAgentEnabled:      true,
		MultiAgentMaxAgents:    4,
		MultiAgentMaxRounds:    2,
		MultiAgentBudgetTokens: 1200,
	}, r)
	cfg := e.multiAgentConfig(model.ChatCompletionRequest{
		Reasoning: &model.ReasoningOptions{
			Mode:                   "multi_agent",
			MultiAgentMaxAgents:    99,
			MultiAgentMaxRounds:    99,
			MultiAgentBudgetTokens: 10,
		},
	})
	if cfg.MaxAgents != 4 {
		t.Fatalf("expected max agents cap=4, got %d", cfg.MaxAgents)
	}
	if cfg.MaxRounds != 4 {
		t.Fatalf("expected max rounds cap=4, got %d", cfg.MaxRounds)
	}
	if cfg.BudgetTokens != 200 {
		t.Fatalf("expected min budget floor=200, got %d", cfg.BudgetTokens)
	}
}

func TestMultiAgentExecuteProducesTrace(t *testing.T) {
	r := orchestrator.NewRouter("qwen3-8b-instruct-Q4_K_M", []string{"qwen3:8b"}, "qwen3:4b")
	e := NewExecutor(Config{
		Enabled:                true,
		MultiAgentEnabled:      true,
		MultiAgentMaxAgents:    4,
		MultiAgentMaxRounds:    2,
		MultiAgentBudgetTokens: 1200,
		MCTSEnabled:            true,
		MCTSDefaultRollouts:    6,
		MCTSMaxRollouts:        12,
		MCTSDefaultDepth:       3,
		MCTSMaxDepth:           5,
		MCTSDefaultExploration: 1.2,
	}, r)
	up := &fakeUpstream{}
	req := model.ChatCompletionRequest{
		Model: "auto",
		Reasoning: &model.ReasoningOptions{
			Mode:                "multi_agent",
			MultiAgentEnabled:   true,
			MultiAgentMaxAgents: 4,
			MultiAgentMaxRounds: 2,
		},
		Messages: []model.Message{{Role: "user", Content: "Design a rollout strategy"}},
	}
	pol := model.ModelPolicy{AllowedModels: []string{"qwen3:4b", "mistral:7b"}, PrimaryModel: "qwen3:4b"}
	st := state.CognitiveState{TaskMode: "general"}
	resp, trace, err := e.Execute(context.Background(), up, req, pol, st)
	if err != nil {
		t.Fatalf("execute multi-agent failed: %v", err)
	}
	if trace.Mode != "multi_agent" {
		t.Fatalf("expected multi_agent mode, got %q", trace.Mode)
	}
	if trace.MultiAgent == nil {
		t.Fatal("expected multi-agent trace payload")
	}
	if trace.MultiAgent.Winner == "" {
		t.Fatal("expected winner role")
	}
	if resp.Model == "" {
		t.Fatal("expected response model")
	}
}

func TestMultiAgentConsensusBuckets(t *testing.T) {
	r := orchestrator.NewRouter("qwen3-8b-instruct-Q4_K_M", []string{"qwen3:8b"}, "qwen3:4b")
	e := NewExecutor(Config{MultiAgentEnabled: true, MultiAgentMaxAgents: 4, MultiAgentMaxRounds: 2, MultiAgentBudgetTokens: 1200}, r)
	high := e.multiAgentConsensus([]agentResult{
		{Role: "planner", Score: 0.90, Output: "safe plan with controls"},
		{Role: "researcher", Score: 0.70, Output: "safe plan with controls"},
	})
	if high != "high" {
		t.Fatalf("expected high consensus, got %q", high)
	}
	medium := e.multiAgentConsensus([]agentResult{
		{Role: "planner", Score: 0.82, Output: "option a"},
		{Role: "researcher", Score: 0.75, Output: "option a with caveats"},
	})
	if medium != "medium" {
		t.Fatalf("expected medium consensus, got %q", medium)
	}
	lowContradict := e.multiAgentConsensus([]agentResult{
		{Role: "planner", Score: 0.90, Output: "this cannot be done safely right now"},
		{Role: "researcher", Score: 0.88, Output: "this can be done safely right now with controls"},
	})
	if lowContradict != "low" {
		t.Fatalf("expected low consensus from contradiction, got %q", lowContradict)
	}
}

func TestMultiAgentContextCancelled(t *testing.T) {
	r := orchestrator.NewRouter("qwen3-8b-instruct-Q4_K_M", []string{"qwen3:8b"}, "qwen3:4b")
	e := NewExecutor(Config{
		Enabled:                true,
		MultiAgentEnabled:      true,
		MultiAgentMaxAgents:    4,
		MultiAgentMaxRounds:    2,
		MultiAgentBudgetTokens: 1200,
	}, r)
	up := &fakeUpstream{}
	req := model.ChatCompletionRequest{
		Model: "auto",
		Reasoning: &model.ReasoningOptions{
			Mode:              "multi_agent",
			MultiAgentEnabled: true,
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
	if trace.Mode != "multi_agent" {
		t.Fatalf("expected multi_agent trace mode, got %q", trace.Mode)
	}
}

func TestMultiAgentFallsBackToBaselineWhenRoundFails(t *testing.T) {
	r := orchestrator.NewRouter("qwen3-8b-instruct-Q4_K_M", []string{"qwen3:8b"}, "qwen3:4b")
	e := NewExecutor(Config{
		Enabled:                true,
		MultiAgentEnabled:      true,
		MultiAgentMaxAgents:    4,
		MultiAgentMaxRounds:    2,
		MultiAgentBudgetTokens: 1200,
	}, r)
	up := &selectiveFailUpstream{}
	req := model.ChatCompletionRequest{
		Model: "auto",
		Reasoning: &model.ReasoningOptions{
			Mode:              "multi_agent",
			MultiAgentEnabled: true,
		},
		Messages: []model.Message{{Role: "user", Content: "provide a plan"}},
	}
	pol := model.ModelPolicy{AllowedModels: []string{"qwen3:4b", "mistral:7b"}, PrimaryModel: "qwen3:4b"}
	st := state.CognitiveState{TaskMode: "general"}

	resp, trace, err := e.Execute(context.Background(), up, req, pol, st)
	if err != nil {
		t.Fatalf("expected baseline fallback to succeed, got error: %v", err)
	}
	if trace.MultiAgent == nil {
		t.Fatal("expected multi-agent trace")
	}
	if trace.MultiAgent.Winner != "baseline" {
		t.Fatalf("expected baseline winner, got %q", trace.MultiAgent.Winner)
	}
	if resp.Model == "" {
		t.Fatal("expected response model")
	}
}

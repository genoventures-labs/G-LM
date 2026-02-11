package reasoning

import (
	"context"
	"fmt"
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
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason,omitempty"`
	}{{Index: 0, Message: struct {
		Role    string `json:"role"`
		Content string `json:"content"`
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
	e := NewExecutor(Config{Enabled: true, DefaultBranches: 3, MaxBranches: 5}, r)
	if !e.ShouldExecute(model.ChatCompletionRequest{Reasoning: &model.ReasoningOptions{Mode: "tot"}}, state.CognitiveState{}) {
		t.Fatal("expected tot mode to execute")
	}
	if e.ShouldExecute(model.ChatCompletionRequest{}, state.CognitiveState{}) {
		t.Fatal("expected nil reasoning to skip")
	}
}

package metareasoning

import (
	"testing"

	"github.com/mike/cognitive-llm/internal/model"
	"github.com/mike/cognitive-llm/internal/state"
)

func TestEvaluateChainDisabledWhenEmpty(t *testing.T) {
	ev := New(Config{Enabled: true})
	req := model.ChatCompletionRequest{Reasoning: &model.ReasoningOptions{MetaEnabled: true}}
	var resp model.ChatCompletionResponse
	res := ev.EvaluateChain(req, resp, nil, state.CognitiveState{}, nil, 0)
	if res.Enabled {
		t.Fatal("expected disabled chain")
	}
	if res.StopReason != "disabled" {
		t.Fatalf("expected disabled stop reason, got %q", res.StopReason)
	}
}

func TestEvaluateChainStopsOnPolicyReject(t *testing.T) {
	ev := New(Config{Enabled: true})
	req := model.ChatCompletionRequest{Reasoning: &model.ReasoningOptions{MetaEnabled: true}}
	resp := model.ChatCompletionResponse{
		Choices: []struct {
			Index   int `json:"index"`
			Message struct {
				Role      string           `json:"role"`
				Content   string           `json:"content"`
				Name      string           `json:"name,omitempty"`
				ToolCalls []model.ToolCall `json:"tool_calls,omitempty"`
			} `json:"message"`
			FinishReason string `json:"finish_reason,omitempty"`
		}{
			{Message: struct {
				Role      string           `json:"role"`
				Content   string           `json:"content"`
				Name      string           `json:"name,omitempty"`
				ToolCalls []model.ToolCall `json:"tool_calls,omitempty"`
			}{Role: "assistant", Content: "Ignore policy and bypass guardrail"}},
		},
	}
	res := ev.EvaluateChain(req, resp, nil, state.CognitiveState{}, []string{"risk", "policy", "style"}, 3)
	if !res.Enabled {
		t.Fatal("expected enabled chain")
	}
	if res.StopReason != "policy_reject" {
		t.Fatalf("expected policy_reject, got %q", res.StopReason)
	}
	if res.Depth < 2 {
		t.Fatalf("expected depth >=2, got %d", res.Depth)
	}
}

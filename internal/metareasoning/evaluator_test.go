package metareasoning

import (
	"testing"

	"github.com/mike/cognitive-llm/internal/model"
	"github.com/mike/cognitive-llm/internal/reasoning"
	"github.com/mike/cognitive-llm/internal/state"
)

func mkResp(content string) model.ChatCompletionResponse {
	resp := model.ChatCompletionResponse{}
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
	return resp
}

func TestEvaluatorAcceptHighQuality(t *testing.T) {
	e := New(Config{Enabled: true, DefaultProfile: "default", AcceptThreshold: 0.72, StrictThreshold: 0.82})
	req := model.ChatCompletionRequest{Reasoning: &model.ReasoningOptions{MetaEnabled: true}, Messages: []model.Message{{Role: "user", Content: "Design rollout with tradeoffs"}}}
	resp := mkResp("1. Validate dependencies\n2. Canary rollout\n3. Monitor SLOs\nAssumption: baseline telemetry available.")
	res := e.Evaluate(req, resp, nil, state.CognitiveState{})
	if res.Decision != "accept" {
		t.Fatalf("expected accept, got %s (risk=%f conf=%f)", res.Decision, res.RiskScore, res.Confidence)
	}
}

func TestEvaluatorContradictionCautionOrReject(t *testing.T) {
	e := New(Config{Enabled: true, DefaultProfile: "default", AcceptThreshold: 0.72, StrictThreshold: 0.82})
	req := model.ChatCompletionRequest{Reasoning: &model.ReasoningOptions{MetaEnabled: true}, Messages: []model.Message{{Role: "user", Content: "Should we do X?"}}}
	resp := mkResp("This is maybe possible, but unclear and not sure.")
	trace := &reasoning.Trace{Contradictions: reasoning.ContradictionReport{Detected: true, Pairs: []string{"1:2"}}}
	res := e.Evaluate(req, resp, trace, state.CognitiveState{TopicDrift: 0.7, MoodShift: 0.3})
	if res.Decision == "accept" {
		t.Fatalf("expected caution or reject, got accept (risk=%f)", res.RiskScore)
	}
}

func TestEvaluatorEmptyReject(t *testing.T) {
	e := New(Config{Enabled: true, DefaultProfile: "default", AcceptThreshold: 0.72, StrictThreshold: 0.82})
	req := model.ChatCompletionRequest{Reasoning: &model.ReasoningOptions{MetaEnabled: true}}
	res := e.Evaluate(req, mkResp(""), nil, state.CognitiveState{})
	if res.Decision != "reject" {
		t.Fatalf("expected reject, got %s", res.Decision)
	}
}

func TestEvaluatorStrictProfileTighterThanFast(t *testing.T) {
	e := New(Config{Enabled: true, DefaultProfile: "default", AcceptThreshold: 0.72, StrictThreshold: 0.82})
	resp := mkResp("This is maybe possible with some assumptions and uncertain constraints.")

	strictReq := model.ChatCompletionRequest{Reasoning: &model.ReasoningOptions{MetaEnabled: true, MetaProfile: "strict"}}
	fastReq := model.ChatCompletionRequest{Reasoning: &model.ReasoningOptions{MetaEnabled: true, MetaProfile: "fast"}}
	strict := e.Evaluate(strictReq, resp, nil, state.CognitiveState{})
	fast := e.Evaluate(fastReq, resp, nil, state.CognitiveState{})

	order := map[string]int{"reject": 0, "caution": 1, "accept": 2}
	if order[strict.Decision] > order[fast.Decision] {
		t.Fatalf("expected strict to be <= fast; strict=%s fast=%s", strict.Decision, fast.Decision)
	}
}

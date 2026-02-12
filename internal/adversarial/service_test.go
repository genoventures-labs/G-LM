package adversarial

import (
	"context"
	"testing"

	"github.com/mike/cognitive-llm/internal/model"
)

type fakeUpstream struct {
	calls int
}

func (f *fakeUpstream) ChatCompletions(ctx context.Context, req model.ChatCompletionRequest) (model.ChatCompletionResponse, error) {
	f.calls++
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
	}{
		{Message: struct {
			Role      string           `json:"role"`
			Content   string           `json:"content"`
			Name      string           `json:"name,omitempty"`
			ToolCalls []model.ToolCall `json:"tool_calls,omitempty"`
		}{Role: "assistant", Content: "round result"}},
	}
	return resp, nil
}

func TestExecuteBoundedRounds(t *testing.T) {
	svc := New(Config{Enabled: true, DefaultRounds: 2, MaxRounds: 3})
	up := &fakeUpstream{}
	req := model.ChatCompletionRequest{
		Reasoning: &model.ReasoningOptions{
			AdversarialSelfPlayEnabled: true,
			AdversarialRounds:          10,
		},
		Messages: []model.Message{{Role: "user", Content: "hello"}},
	}
	base := model.ChatCompletionResponse{
		Choices: []struct {
			Index   int `json:"index"`
			Message struct {
				Role      string           `json:"role"`
				Content   string           `json:"content"`
				Name      string           `json:"name,omitempty"`
				ToolCalls []model.ToolCall `json:"tool_calls,omitempty"`
			} `json:"message"`
			FinishReason string `json:"finish_reason,omitempty"`
		}{{Message: struct {
			Role      string           `json:"role"`
			Content   string           `json:"content"`
			Name      string           `json:"name,omitempty"`
			ToolCalls []model.ToolCall `json:"tool_calls,omitempty"`
		}{Role: "assistant", Content: "base"}}},
	}
	got, err := svc.Execute(context.Background(), up, req, base, "mistral:7b")
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}
	if !got.Applied {
		t.Fatal("expected applied")
	}
	if got.Rounds != 3 {
		t.Fatalf("expected rounds clamped to 3, got %d", got.Rounds)
	}
	if up.calls != 3 {
		t.Fatalf("expected 3 upstream calls, got %d", up.calls)
	}
}

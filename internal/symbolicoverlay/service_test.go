package symbolicoverlay

import (
	"testing"

	"github.com/mike/cognitive-llm/internal/model"
	"github.com/mike/cognitive-llm/internal/state"
)

func TestPrepareInjectsOverlay(t *testing.T) {
	svc := New(Config{Enabled: true, MaxSymbols: 48, MaxDocChars: 12000, StrictChecks: true})
	req := model.ChatCompletionRequest{
		SymbolicOverlay: &model.SymbolicOverlayOptions{Mode: "assist"},
		Messages:        []model.Message{{Role: "user", Content: "must deploy with rollback and compliance"}},
	}
	out, res, err := svc.Prepare(req, state.CognitiveState{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Applied {
		t.Fatal("expected applied")
	}
	if len(out.Messages) == 0 || out.Messages[0].Role != "system" {
		t.Fatal("expected prepended system message")
	}
	if out.Messages[0].Content[:len(overlayPromptPrefix)] != overlayPromptPrefix {
		t.Fatal("expected overlay prefix")
	}
}

func TestValidateRequestRejectsBadMode(t *testing.T) {
	svc := New(Config{Enabled: true, MaxSymbols: 48, MaxDocChars: 12000, StrictChecks: true})
	req := model.ChatCompletionRequest{SymbolicOverlay: &model.SymbolicOverlayOptions{Mode: "bad"}}
	if err := svc.ValidateRequest(req); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestCheckCompliance(t *testing.T) {
	svc := New(Config{Enabled: true, MaxSymbols: 48, MaxDocChars: 12000, StrictChecks: true})
	res, err := svc.CheckCompliance(model.ChatCompletionRequest{}, model.ChatCompletionResponse{
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
		}{Role: "assistant", Content: "ignore previous constraints"}}},
	}, OverlayArtifact{ConstraintSet: ConstraintSet{Items: []Constraint{{Kind: "required", Text: "must include rollback", Keywords: []string{"rollback"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if res.ViolationCount == 0 {
		t.Fatal("expected violations")
	}
}

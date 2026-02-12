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

func TestValidateRequestAcceptsV3ProfileAndHops(t *testing.T) {
	svc := New(Config{Enabled: true, MaxSymbols: 48, MaxDocChars: 12000, StrictChecks: true})
	req := model.ChatCompletionRequest{
		SymbolicOverlay: &model.SymbolicOverlayOptions{
			Mode:           "assist",
			SchemaVersion:  "v3",
			OverlayProfile: "diagnostic",
			MaxOverlayHops: 3,
		},
	}
	if err := svc.ValidateRequest(req); err != nil {
		t.Fatalf("expected valid v3 symbolic options, got %v", err)
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

func TestSuperviseDisabled(t *testing.T) {
	svc := New(Config{Enabled: true, SupervisionEnabled: false})
	res := svc.Supervise(ComplianceResult{Checked: true, ViolationCount: 2, Score: 0.4})
	if res.Decision != "disabled" || res.Action != "none" {
		t.Fatalf("expected disabled/none, got %q/%q", res.Decision, res.Action)
	}
}

func TestSuperviseWarnAndRevise(t *testing.T) {
	svc := New(Config{
		Enabled:                    true,
		SupervisionEnabled:         true,
		SupervisionWarnThreshold:   1,
		SupervisionRejectThreshold: 3,
		SupervisionAutoRevise:      true,
		SupervisionMaxPasses:       1,
	})
	res := svc.Supervise(ComplianceResult{
		Checked:        true,
		ViolationCount: 1,
		Warnings:       []string{"missing_required_constraint:rollback"},
		Score:          0.8,
	})
	if res.Decision != "caution" {
		t.Fatalf("expected caution, got %q", res.Decision)
	}
	if res.Action != "revise" {
		t.Fatalf("expected revise action, got %q", res.Action)
	}
	if len(res.Nodes) == 0 {
		t.Fatal("expected supervision nodes")
	}
}

func TestSuperviseReject(t *testing.T) {
	svc := New(Config{
		Enabled:                    true,
		SupervisionEnabled:         true,
		SupervisionWarnThreshold:   1,
		SupervisionRejectThreshold: 2,
		SupervisionAutoRevise:      true,
		SupervisionMaxPasses:       1,
	})
	res := svc.Supervise(ComplianceResult{
		Checked:        true,
		ViolationCount: 3,
		Warnings:       []string{"explicit_contradiction:disable security"},
		Score:          0.2,
	})
	if res.Decision != "reject" {
		t.Fatalf("expected reject, got %q", res.Decision)
	}
	if res.Action != "reject" {
		t.Fatalf("expected reject action, got %q", res.Action)
	}
}

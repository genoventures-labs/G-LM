package policy

import (
	"context"
	"testing"

	"github.com/mike/cognitive-llm/internal/model"
	"github.com/mike/cognitive-llm/internal/store/memory"
)

func TestResolveCognitivePolicyDefault(t *testing.T) {
	st := memory.New()
	svc := NewService(st, "mistral:7b", true)
	got := svc.ResolveCognitivePolicy(context.Background(), "tenant-1")
	if got.TenantID != "tenant-1" {
		t.Fatalf("expected tenant-1, got %q", got.TenantID)
	}
	if got.Status != "active" {
		t.Fatalf("expected active default status, got %q", got.Status)
	}
	if got.Version != "v1" {
		t.Fatalf("expected v1 default version, got %q", got.Version)
	}
}

func TestResolveCognitivePolicyNormalized(t *testing.T) {
	st := memory.New()
	svc := NewService(st, "mistral:7b", true)
	_, err := st.UpsertCognitivePolicy(context.Background(), model.CognitivePolicy{
		TenantID:                      "tenant-1",
		Status:                        "ACTIVE",
		Version:                       "v2",
		MaxReasoningPasses:            99,
		MaxReflectionPasses:           99,
		MaxSelfAlignmentPasses:        99,
		MaxConstraintBreakingSeverity: "INVALID",
		RiskThresholdReject:           2.0,
		RiskThresholdWarn:             -1.0,
	})
	if err != nil {
		t.Fatalf("upsert cognitive policy failed: %v", err)
	}
	got := svc.ResolveCognitivePolicy(context.Background(), "tenant-1")
	if got.Status != "active" {
		t.Fatalf("expected normalized active status, got %q", got.Status)
	}
	if got.MaxReasoningPasses != 16 {
		t.Fatalf("expected max reasoning clamp 16, got %d", got.MaxReasoningPasses)
	}
	if got.MaxReflectionPasses != 8 {
		t.Fatalf("expected max reflection clamp 8, got %d", got.MaxReflectionPasses)
	}
	if got.MaxSelfAlignmentPasses != 8 {
		t.Fatalf("expected max self-alignment clamp 8, got %d", got.MaxSelfAlignmentPasses)
	}
	if got.MaxConstraintBreakingSeverity != "none" {
		t.Fatalf("expected normalized severity none, got %q", got.MaxConstraintBreakingSeverity)
	}
	if got.RiskThresholdReject != 1 {
		t.Fatalf("expected risk reject clamp 1, got %f", got.RiskThresholdReject)
	}
	if got.RiskThresholdWarn != 0 {
		t.Fatalf("expected risk warn clamp 0, got %f", got.RiskThresholdWarn)
	}
}

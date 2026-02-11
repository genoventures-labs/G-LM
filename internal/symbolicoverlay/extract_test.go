package symbolicoverlay

import (
	"testing"

	"github.com/mike/cognitive-llm/internal/model"
	"github.com/mike/cognitive-llm/internal/state"
)

func TestBuildArtifactExtractsAllTypes(t *testing.T) {
	req := model.ChatCompletionRequest{
		Messages: []model.Message{{Role: "user", Content: "We must deploy safely and never skip compliance checks. Plan rollback for incident failure."}},
		Documents: []model.DocumentInput{{
			Title: "Runbook",
			Text:  "Security controls are required. Only if tests pass should rollout continue.",
		}},
	}
	opt := normalizedOptions{
		Enabled:          true,
		Mode:             modeAssist,
		Types:            []string{"logic_map", "constraint_set", "risk_lens"},
		MaxSymbols:       32,
		IncludeDocuments: true,
		IncludeState:     true,
	}
	artifact, _, count := buildArtifact(req, state.CognitiveState{TaskMode: "coding", Topic: "rollout"}, opt, 12000)
	if count == 0 {
		t.Fatal("expected non-zero symbol count")
	}
	if len(artifact.LogicMap.Entities) == 0 {
		t.Fatal("expected logic entities")
	}
	if len(artifact.ConstraintSet.Items) == 0 {
		t.Fatal("expected constraints")
	}
	if len(artifact.RiskLens.Signals) == 0 {
		t.Fatal("expected risk signals")
	}
}

func TestBuildArtifactHonorsMaxSymbols(t *testing.T) {
	req := model.ChatCompletionRequest{Messages: []model.Message{{Role: "user", Content: "must deploy monitor audit validate security compliance rollback incident failure critical"}}}
	opt := normalizedOptions{
		Enabled:    true,
		Mode:       modeAssist,
		Types:      []string{"logic_map", "constraint_set", "risk_lens"},
		MaxSymbols: 2,
	}
	_, flags, count := buildArtifact(req, state.CognitiveState{}, opt, 12000)
	if count > 2 {
		t.Fatalf("expected count <= 2, got %d", count)
	}
	if len(flags) == 0 {
		t.Fatal("expected truncation flags")
	}
}

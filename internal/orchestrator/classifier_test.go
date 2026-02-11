package orchestrator

import "testing"

func TestClassifyPrompt_Coding(t *testing.T) {
	got := ClassifyPrompt("Please refactor this Go function and fix this stack trace")
	if got != TaskCodingReasoning {
		t.Fatalf("expected %s, got %s", TaskCodingReasoning, got)
	}
}

func TestClassifyPrompt_LightQA(t *testing.T) {
	got := ClassifyPrompt("What is DNS?")
	if got != TaskLightQA {
		t.Fatalf("expected %s, got %s", TaskLightQA, got)
	}
}

func TestClassifyPrompt_Extraction(t *testing.T) {
	got := ClassifyPrompt("Extract fields from this text and return json")
	if got != TaskExtractionClassification {
		t.Fatalf("expected %s, got %s", TaskExtractionClassification, got)
	}
}

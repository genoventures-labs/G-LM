package orchestrator

import (
	"testing"

	"github.com/mike/cognitive-llm/internal/model"
)

func mkPolicy(allowed ...string) model.ModelPolicy {
	return model.ModelPolicy{AllowedModels: allowed, PrimaryModel: "mistral:7b"}
}

func TestRouterAutoGeneral_DefaultAliasFallbackToQwen3_4b(t *testing.T) {
	r := NewRouter("qwen3-8b-instruct-Q4_K_M", []string{"qwen3-8b-instruct-Q4_K_M", "qwen3:8b"}, "qwen3:4b")
	req := model.ChatCompletionRequest{Messages: []model.Message{{Role: "user", Content: "Give me an overview of enterprise ai controls"}}}
	avail := []string{"qwen3:4b", "mistral:7b"}
	pol := mkPolicy("qwen3:4b", "mistral:7b")
	d, err := r.Choose(req, avail, pol)
	if err != nil {
		t.Fatalf("choose failed: %v", err)
	}
	if d.ChosenModel != "qwen3:4b" {
		t.Fatalf("expected qwen3:4b, got %s", d.ChosenModel)
	}
}

func TestRouterCodingPrefersMistral(t *testing.T) {
	r := NewRouter("qwen3-8b-instruct-Q4_K_M", []string{"qwen3:8b"}, "qwen3:4b")
	req := model.ChatCompletionRequest{Messages: []model.Message{{Role: "user", Content: "Fix this stack trace and implement tests"}}}
	avail := []string{"qwen3:4b", "mistral:7b"}
	pol := mkPolicy("qwen3:4b", "mistral:7b")
	d, err := r.Choose(req, avail, pol)
	if err != nil {
		t.Fatalf("choose failed: %v", err)
	}
	if d.ChosenModel != "mistral:7b" {
		t.Fatalf("expected mistral:7b, got %s", d.ChosenModel)
	}
}

func TestRouterNoAllowedAvailable(t *testing.T) {
	r := NewRouter("qwen3-8b-instruct-Q4_K_M", []string{"qwen3:8b"}, "qwen3:4b")
	req := model.ChatCompletionRequest{Messages: []model.Message{{Role: "user", Content: "hello"}}}
	avail := []string{"qwen3:4b", "mistral:7b"}
	pol := mkPolicy("phi3:mini")
	_, err := r.Choose(req, avail, pol)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestShouldAutoRoute(t *testing.T) {
	if !ShouldAutoRoute("") || !ShouldAutoRoute("auto") || !ShouldAutoRoute("AUTO") {
		t.Fatal("expected auto route for empty/auto")
	}
	if ShouldAutoRoute("mistral:7b") {
		t.Fatal("did not expect auto route for explicit model")
	}
}

package skillcompiler

import (
	"testing"

	"github.com/mike/cognitive-llm/internal/model"
)

func TestCompileDisabled(t *testing.T) {
	svc := New(Config{Enabled: false, Profile: "safe"})
	got := svc.Compile(model.ChatCompletionRequest{})
	if got.Enabled {
		t.Fatal("expected disabled")
	}
}

func TestCompileAndInject(t *testing.T) {
	svc := New(Config{Enabled: true, Profile: "safe"})
	req := model.ChatCompletionRequest{
		Messages: []model.Message{{Role: "user", Content: "implement and compare options"}},
	}
	got := svc.Compile(req)
	if !got.Enabled || !got.Applied {
		t.Fatalf("expected applied result, got %#v", got)
	}
	if len(got.Nodes) == 0 {
		t.Fatal("expected nodes")
	}
	out := svc.Inject(req, got)
	if len(out.Messages) <= len(req.Messages) || out.Messages[0].Role != "system" {
		t.Fatal("expected injected system message")
	}
}

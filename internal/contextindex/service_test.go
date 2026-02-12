package contextindex

import (
	"testing"

	"github.com/mike/cognitive-llm/internal/model"
	"github.com/mike/cognitive-llm/internal/state"
)

func TestBuildDisabled(t *testing.T) {
	svc := New(Config{Enabled: false, DefaultScope: "request"})
	req := model.ChatCompletionRequest{}
	got := svc.Build(req, state.CognitiveState{})
	if got.Enabled {
		t.Fatal("expected disabled")
	}
}

func TestBuildAndInject(t *testing.T) {
	svc := New(Config{Enabled: true, DefaultScope: "session"})
	req := model.ChatCompletionRequest{
		MemoryAnchorKeys: []string{"k1", "k2"},
		Documents: []model.DocumentInput{
			{ID: "doc-1", Section: "intro"},
		},
		Messages: []model.Message{{Role: "user", Content: "hello"}},
	}
	got := svc.Build(req, state.CognitiveState{Pacing: "fast", TopicDrift: 0.8})
	if !got.Enabled || !got.Applied {
		t.Fatalf("expected applied result, got %#v", got)
	}
	if got.Scope != "session" {
		t.Fatalf("expected session scope, got %q", got.Scope)
	}
	out := svc.Inject(req, got)
	if len(out.Messages) <= len(req.Messages) || out.Messages[0].Role != "system" {
		t.Fatal("expected system message injected")
	}
}

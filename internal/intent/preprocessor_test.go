package intent

import (
	"strings"
	"testing"

	"github.com/mike/cognitive-llm/internal/model"
)

func TestClassifyIntentEngineering(t *testing.T) {
	p := NewProcessor(Config{Enabled: true, AmbiguityThreshold: 0.62})
	req := model.ChatCompletionRequest{Messages: []model.Message{{Role: "user", Content: "Please debug this panic and refactor function"}}}
	out, res := p.Process(req)
	if res.Category != "engineering" {
		t.Fatalf("expected engineering, got %s", res.Category)
	}
	if !strings.HasSuffix(out.Messages[0].Content, ".") {
		t.Fatalf("expected normalized punctuation, got %q", out.Messages[0].Content)
	}
}

func TestAmbiguousPromptGetsStableRewrite(t *testing.T) {
	p := NewProcessor(Config{Enabled: true, AmbiguityThreshold: 0.62})
	req := model.ChatCompletionRequest{Messages: []model.Message{{Role: "user", Content: "fix this"}}}
	out, res := p.Process(req)
	if !res.NeedsRewrite {
		t.Fatal("expected rewrite for ambiguous prompt")
	}
	if !strings.Contains(out.Messages[0].Content, "Intent=") {
		t.Fatalf("expected stable rewritten form, got %q", out.Messages[0].Content)
	}
	if res.AmbiguityScore <= 0.62 {
		t.Fatalf("expected high ambiguity score, got %f", res.AmbiguityScore)
	}
}

func TestDisabledProcessorNoChanges(t *testing.T) {
	p := NewProcessor(Config{Enabled: false})
	req := model.ChatCompletionRequest{Messages: []model.Message{{Role: "user", Content: "What is DNS?"}}}
	out, _ := p.Process(req)
	if out.Messages[0].Content != "What is DNS?" {
		t.Fatalf("expected no change, got %q", out.Messages[0].Content)
	}
}

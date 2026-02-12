package stylecontract

import (
	"strings"
	"testing"

	"github.com/mike/cognitive-llm/internal/model"
)

func TestInjectorV2Contract(t *testing.T) {
	inj := New(Config{Enabled: true, Version: "v2"})
	msgs := []model.Message{{Role: "user", Content: "hello"}}
	out := inj.Inject(msgs, &model.ResponseStyle{
		AudienceMode:         "engineer",
		Register:             "technical",
		VerbosityTarget:      "medium",
		JustificationDensity: "high",
	})
	if len(out) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(out))
	}
	if !strings.Contains(out[0].Content, "style_contract=v2") {
		t.Fatalf("expected v2 style contract, got %q", out[0].Content)
	}
}

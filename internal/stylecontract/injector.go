package stylecontract

import (
	"encoding/json"
	"strings"

	"github.com/mike/cognitive-llm/internal/model"
)

type Config struct {
	Enabled bool
	Version string
}

type Injector struct {
	cfg Config
}

func New(cfg Config) *Injector {
	if strings.TrimSpace(cfg.Version) == "" {
		cfg.Version = "v1"
	}
	return &Injector{cfg: cfg}
}

func (i *Injector) Enabled() bool {
	return i.cfg.Enabled
}

func (i *Injector) Version() string {
	return i.cfg.Version
}

func (i *Injector) Inject(messages []model.Message, style *model.ResponseStyle) []model.Message {
	if !i.cfg.Enabled || style == nil {
		return messages
	}
	blob, _ := json.Marshal(style)
	contract := i.contractText() + "\nresponse_style=" + string(blob)
	system := model.Message{Role: "system", Content: contract}
	out := make([]model.Message, 0, len(messages)+1)
	out = append(out, system)
	out = append(out, messages...)
	return out
}

func (i *Injector) contractText() string {
	switch strings.ToLower(strings.TrimSpace(i.cfg.Version)) {
	case "v1":
		return "style_contract=v1. Preserve factual accuracy. Apply response_style only to tone/format. Use breathing_weight for cadence, pacing for density, tone_shift/style_adjustment for wording. Treat micro_switches/risk_flags as hints. subtext_detection remains model-driven."
	default:
		return "style_contract=" + i.cfg.Version + ". Apply response_style safely without changing factual content."
	}
}

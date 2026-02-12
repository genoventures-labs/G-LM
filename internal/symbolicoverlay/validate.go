package symbolicoverlay

import (
	"fmt"
	"strings"

	"github.com/mike/cognitive-llm/internal/model"
)

const (
	modeOff    = "off"
	modeAssist = "assist"
	modeStrict = "strict"
)

var supportedTypes = map[string]struct{}{
	string(model.SymbolicOverlayTypeLogicMap):      {},
	string(model.SymbolicOverlayTypeConstraintSet): {},
	string(model.SymbolicOverlayTypeRiskLens):      {},
}

type normalizedOptions struct {
	Enabled          bool
	Mode             string
	SchemaVersion    string
	OverlayProfile   string
	MaxOverlayHops   int
	Types            []string
	MaxSymbols       int
	IncludeState     bool
	IncludeDocuments bool
}

func normalizeOptions(req model.ChatCompletionRequest, cfg Config) (normalizedOptions, error) {
	if !cfg.Enabled || req.SymbolicOverlay == nil {
		return normalizedOptions{Enabled: false}, nil
	}
	opt := req.SymbolicOverlay
	mode := strings.ToLower(strings.TrimSpace(opt.Mode))
	if mode == "" {
		mode = modeAssist
	}
	switch mode {
	case modeOff, modeAssist, modeStrict:
	default:
		return normalizedOptions{}, fmt.Errorf("symbolic_overlay.mode must be one of: off, assist, strict")
	}
	types := make([]string, 0, len(opt.Types))
	if len(opt.Types) == 0 {
		types = []string{
			string(model.SymbolicOverlayTypeLogicMap),
			string(model.SymbolicOverlayTypeConstraintSet),
			string(model.SymbolicOverlayTypeRiskLens),
		}
	} else {
		seen := map[string]struct{}{}
		for _, raw := range opt.Types {
			t := strings.ToLower(strings.TrimSpace(raw))
			if t == "" {
				continue
			}
			if _, ok := supportedTypes[t]; !ok {
				return normalizedOptions{}, fmt.Errorf("symbolic_overlay.types contains unsupported type: %s", t)
			}
			if _, ok := seen[t]; ok {
				continue
			}
			seen[t] = struct{}{}
			types = append(types, t)
		}
		if len(types) == 0 {
			return normalizedOptions{}, fmt.Errorf("symbolic_overlay.types must include at least one supported type")
		}
	}
	if opt.MaxSymbols < 0 {
		return normalizedOptions{}, fmt.Errorf("symbolic_overlay.max_symbols must be >= 0")
	}
	if opt.MaxOverlayHops < 0 {
		return normalizedOptions{}, fmt.Errorf("symbolic_overlay.max_overlay_hops must be >= 0")
	}
	schemaVersion := strings.ToLower(strings.TrimSpace(opt.SchemaVersion))
	if schemaVersion == "" {
		schemaVersion = "v3"
	}
	if schemaVersion != "v3" {
		return normalizedOptions{}, fmt.Errorf("symbolic_overlay.schema_version must be v3")
	}
	profile := strings.ToLower(strings.TrimSpace(opt.OverlayProfile))
	if profile == "" {
		profile = "assist"
	}
	switch profile {
	case "assist", "strict", "diagnostic", "fusion_prep":
	default:
		return normalizedOptions{}, fmt.Errorf("symbolic_overlay.overlay_profile must be one of: assist, strict, diagnostic, fusion_prep")
	}
	maxOverlayHops := opt.MaxOverlayHops
	if maxOverlayHops <= 0 {
		maxOverlayHops = 1
	}
	if maxOverlayHops > 6 {
		maxOverlayHops = 6
	}
	maxSymbols := cfg.MaxSymbols
	if opt.MaxSymbols > 0 {
		maxSymbols = opt.MaxSymbols
	}
	if maxSymbols <= 0 {
		maxSymbols = 48
	}
	if maxSymbols > cfg.MaxSymbols && cfg.MaxSymbols > 0 {
		maxSymbols = cfg.MaxSymbols
	}
	return normalizedOptions{
		Enabled:          mode != modeOff,
		Mode:             mode,
		SchemaVersion:    schemaVersion,
		OverlayProfile:   profile,
		MaxOverlayHops:   maxOverlayHops,
		Types:            types,
		MaxSymbols:       maxSymbols,
		IncludeState:     opt.IncludeState,
		IncludeDocuments: opt.IncludeDocuments,
	}, nil
}

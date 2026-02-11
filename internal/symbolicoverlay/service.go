package symbolicoverlay

import (
	"errors"
	"strings"

	"github.com/mike/cognitive-llm/internal/model"
	"github.com/mike/cognitive-llm/internal/state"
)

type Config struct {
	Enabled      bool
	MaxSymbols   int
	MaxDocChars  int
	StrictChecks bool
}

type Service struct {
	cfg Config
}

const (
	forcePrepareErrorToken    = "glm_internal_force_symbolic_prepare_error"
	forceComplianceErrorToken = "glm_internal_force_symbolic_compliance_error"
)

func New(cfg Config) *Service {
	if cfg.MaxSymbols <= 0 {
		cfg.MaxSymbols = 48
	}
	if cfg.MaxDocChars <= 0 {
		cfg.MaxDocChars = 12000
	}
	return &Service{cfg: cfg}
}

func (s *Service) Prepare(req model.ChatCompletionRequest, st state.CognitiveState) (model.ChatCompletionRequest, Result, error) {
	if containsUserToken(req.Messages, forcePrepareErrorToken) {
		return req, Result{}, errors.New("forced symbolic prepare error")
	}
	norm, err := normalizeOptions(req, s.cfg)
	if err != nil {
		return req, Result{}, err
	}
	if !norm.Enabled {
		return req, Result{Applied: false, Mode: norm.Mode}, nil
	}
	artifact, flags, symbolCount := buildArtifact(req, st, norm, s.cfg.MaxDocChars)
	out, err := injectOverlay(req, artifact)
	if err != nil {
		return req, Result{}, err
	}
	return out, Result{
		Applied:     true,
		Mode:        norm.Mode,
		Types:       append([]string{}, norm.Types...),
		SymbolCount: symbolCount,
		Artifact:    artifact,
		Flags:       flags,
	}, nil
}

func (s *Service) CheckCompliance(req model.ChatCompletionRequest, resp model.ChatCompletionResponse, overlay OverlayArtifact) (ComplianceResult, error) {
	if !s.cfg.StrictChecks {
		return ComplianceResult{Checked: false, Score: 1.0}, nil
	}
	text := firstAssistantContent(resp)
	if strings.Contains(strings.ToLower(text), forceComplianceErrorToken) {
		return ComplianceResult{}, errors.New("forced symbolic compliance error")
	}
	return checkCompliance(text, overlay), nil
}

func (s *Service) ValidateRequest(req model.ChatCompletionRequest) error {
	_, err := normalizeOptions(req, s.cfg)
	return err
}

func firstAssistantContent(resp model.ChatCompletionResponse) string {
	if len(resp.Choices) == 0 {
		return ""
	}
	return strings.TrimSpace(resp.Choices[0].Message.Content)
}

func containsUserToken(messages []model.Message, token string) bool {
	token = strings.ToLower(strings.TrimSpace(token))
	if token == "" {
		return false
	}
	for _, m := range messages {
		if strings.EqualFold(strings.TrimSpace(m.Role), "user") &&
			strings.Contains(strings.ToLower(m.Content), token) {
			return true
		}
	}
	return false
}

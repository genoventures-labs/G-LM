package symbolicoverlay

import (
	"errors"
	"strconv"
	"strings"

	"github.com/mike/cognitive-llm/internal/model"
	"github.com/mike/cognitive-llm/internal/state"
)

type Config struct {
	Enabled                    bool
	MaxSymbols                 int
	MaxDocChars                int
	StrictChecks               bool
	SupervisionEnabled         bool
	SupervisionWarnThreshold   int
	SupervisionRejectThreshold int
	SupervisionAutoRevise      bool
	SupervisionMaxPasses       int
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
	if cfg.SupervisionWarnThreshold <= 0 {
		cfg.SupervisionWarnThreshold = 1
	}
	if cfg.SupervisionRejectThreshold <= 0 {
		cfg.SupervisionRejectThreshold = 3
	}
	if cfg.SupervisionRejectThreshold < cfg.SupervisionWarnThreshold {
		cfg.SupervisionRejectThreshold = cfg.SupervisionWarnThreshold + 1
	}
	if cfg.SupervisionMaxPasses < 0 {
		cfg.SupervisionMaxPasses = 0
	}
	if cfg.SupervisionMaxPasses > 1 {
		cfg.SupervisionMaxPasses = 1
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
		Applied:       true,
		Mode:          norm.Mode,
		SchemaVersion: norm.SchemaVersion,
		Profile:       norm.OverlayProfile,
		Types:         append([]string{}, norm.Types...),
		SymbolCount:   symbolCount,
		Artifact:      artifact,
		Flags:         flags,
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

func (s *Service) Supervise(comp ComplianceResult) SupervisionResult {
	if !s.cfg.SupervisionEnabled {
		return SupervisionResult{
			Enabled:  false,
			Applied:  false,
			Decision: "disabled",
			Action:   "none",
			Reason:   "symbolic_supervision_disabled",
		}
	}
	if !comp.Checked {
		return SupervisionResult{
			Enabled:  true,
			Applied:  false,
			Decision: "skipped",
			Action:   "none",
			Reason:   "compliance_not_checked",
		}
	}

	decision := "accept"
	action := "none"
	reason := "no_violations"
	violations := comp.ViolationCount
	switch {
	case violations >= s.cfg.SupervisionRejectThreshold:
		decision = "reject"
		action = "reject"
		reason = "reject_threshold_exceeded"
	case violations >= s.cfg.SupervisionWarnThreshold:
		decision = "caution"
		action = "warn"
		reason = "warn_threshold_exceeded"
		if s.cfg.SupervisionAutoRevise && s.cfg.SupervisionMaxPasses > 0 {
			action = "revise"
			reason = "warn_threshold_revise"
		}
	}

	nodes := []SupervisionNode{
		{
			NodeID:     "symbolic-supervision-1",
			NodeType:   "compliance_summary",
			Severity:   supervisionSeverity(decision),
			Score:      comp.Score,
			Decision:   decision,
			Action:     action,
			Reason:     reason,
			Source:     "constraint_compliance",
			Violations: violations,
		},
	}
	for i, w := range comp.Warnings {
		nodes = append(nodes, SupervisionNode{
			NodeID:   "symbolic-warning-" + strconv.Itoa(i+1),
			NodeType: "violation_warning",
			Severity: warningSeverity(w),
			Decision: decision,
			Action:   action,
			Reason:   w,
			Source:   "compliance_warning",
		})
		if len(nodes) >= 12 {
			break
		}
	}

	return SupervisionResult{
		Enabled:         true,
		Applied:         true,
		Decision:        decision,
		Action:          action,
		Reason:          reason,
		Passes:          0,
		ViolationCount:  violations,
		ComplianceScore: comp.Score,
		Nodes:           nodes,
	}
}

func supervisionSeverity(decision string) string {
	switch decision {
	case "reject":
		return "high"
	case "caution":
		return "medium"
	default:
		return "low"
	}
}

func warningSeverity(w string) string {
	l := strings.ToLower(w)
	if strings.Contains(l, "explicit_contradiction") || strings.Contains(l, "disable security") {
		return "high"
	}
	return "medium"
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

package policy

import (
	"context"
	"strings"

	"github.com/mike/cognitive-llm/internal/model"
	"github.com/mike/cognitive-llm/internal/store"
)

type Service struct {
	store                    store.Store
	defaultModel             string
	reasoningHiddenByDefault bool
}

func NewService(st store.Store, defaultModel string, hideReasoning bool) *Service {
	return &Service{store: st, defaultModel: defaultModel, reasoningHiddenByDefault: hideReasoning}
}

func (s *Service) ResolveModelPolicy(ctx context.Context, tenantID string) model.ModelPolicy {
	p, err := s.store.GetModelPolicy(ctx, tenantID)
	if err != nil {
		return model.ModelPolicy{TenantID: tenantID, PrimaryModel: s.defaultModel, FallbackModel: "", AllowedModels: []string{s.defaultModel}, ReasoningVisible: !s.reasoningHiddenByDefault}
	}
	if p.PrimaryModel == "" {
		p.PrimaryModel = s.defaultModel
	}
	if len(p.AllowedModels) == 0 {
		p.AllowedModels = []string{p.PrimaryModel}
	}
	return p
}

func (s *Service) ResolveCognitivePolicy(ctx context.Context, tenantID string) model.CognitivePolicy {
	p, err := s.store.GetCognitivePolicy(ctx, tenantID)
	if err != nil {
		return defaultCognitivePolicy(tenantID)
	}
	p = normalizeCognitivePolicy(p)
	return p
}

func defaultCognitivePolicy(tenantID string) model.CognitivePolicy {
	return normalizeCognitivePolicy(model.CognitivePolicy{
		TenantID:      tenantID,
		Status:        "active",
		Version:       "v1",
		ToolAllowlist: nil,
		ToolDenylist:  nil,
	})
}

func normalizeCognitivePolicy(in model.CognitivePolicy) model.CognitivePolicy {
	if strings.TrimSpace(in.Status) == "" {
		in.Status = "active"
	}
	in.Status = strings.ToLower(strings.TrimSpace(in.Status))
	if in.Status != "active" && in.Status != "disabled" {
		in.Status = "active"
	}
	if strings.TrimSpace(in.Version) == "" {
		in.Version = "v1"
	}
	if in.MaxReasoningPasses < 0 {
		in.MaxReasoningPasses = 0
	}
	if in.MaxReasoningPasses > 16 {
		in.MaxReasoningPasses = 16
	}
	if in.MaxReflectionPasses < 0 {
		in.MaxReflectionPasses = 0
	}
	if in.MaxReflectionPasses > 8 {
		in.MaxReflectionPasses = 8
	}
	if in.MaxSelfAlignmentPasses < 0 {
		in.MaxSelfAlignmentPasses = 0
	}
	if in.MaxSelfAlignmentPasses > 8 {
		in.MaxSelfAlignmentPasses = 8
	}
	in.MaxConstraintBreakingSeverity = strings.ToLower(strings.TrimSpace(in.MaxConstraintBreakingSeverity))
	switch in.MaxConstraintBreakingSeverity {
	case "none", "low", "medium", "high":
	default:
		in.MaxConstraintBreakingSeverity = "none"
	}
	if in.RiskThresholdReject < 0 {
		in.RiskThresholdReject = 0
	}
	if in.RiskThresholdReject > 1 {
		in.RiskThresholdReject = 1
	}
	if in.RiskThresholdWarn < 0 {
		in.RiskThresholdWarn = 0
	}
	if in.RiskThresholdWarn > 1 {
		in.RiskThresholdWarn = 1
	}
	return in
}

func Allowed(modelName string, policy model.ModelPolicy) bool {
	if modelName == "" {
		return true
	}
	for _, m := range policy.AllowedModels {
		if strings.EqualFold(m, modelName) {
			return true
		}
	}
	return false
}

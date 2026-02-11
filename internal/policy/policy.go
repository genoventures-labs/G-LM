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

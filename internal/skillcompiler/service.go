package skillcompiler

import (
	"strings"

	"github.com/mike/cognitive-llm/internal/model"
)

type Config struct {
	Enabled      bool
	Profile      string
	BudgetTokens int
}

type Service struct {
	cfg Config
}

type Result struct {
	Enabled bool
	Applied bool
	Profile string
	Nodes   []string
}

func New(cfg Config) *Service {
	profile := strings.ToLower(strings.TrimSpace(cfg.Profile))
	switch profile {
	case "safe", "balanced", "aggressive":
	default:
		profile = "safe"
	}
	if cfg.BudgetTokens <= 0 {
		cfg.BudgetTokens = 600
	}
	cfg.Profile = profile
	return &Service{cfg: cfg}
}

func (s *Service) Compile(req model.ChatCompletionRequest) Result {
	res := Result{
		Enabled: s.enabledForRequest(req),
		Profile: s.profileForRequest(req),
	}
	if !res.Enabled {
		return res
	}
	text := strings.ToLower(strings.TrimSpace(joinUser(req.Messages)))
	if text == "" {
		return res
	}
	nodes := []string{"analyze_request", "plan_steps", "draft_answer", "self_check"}
	if strings.Contains(text, "compare") || strings.Contains(text, "tradeoff") {
		nodes = append(nodes, "compare_options")
	}
	if strings.Contains(text, "implement") || strings.Contains(text, "build") {
		nodes = append(nodes, "produce_patch")
	}
	nodes = applySafety(nodes, res.Profile)
	if len(nodes) == 0 {
		return res
	}
	if len(nodes) > 8 {
		nodes = nodes[:8]
	}
	res.Applied = true
	res.Nodes = nodes
	return res
}

func (s *Service) Inject(req model.ChatCompletionRequest, r Result) model.ChatCompletionRequest {
	if !r.Applied {
		return req
	}
	msg := model.Message{
		Role:    "system",
		Content: "Use this primitive execution plan:\n- " + strings.Join(r.Nodes, "\n- "),
	}
	req.Messages = append([]model.Message{msg}, req.Messages...)
	return req
}

func applySafety(nodes []string, profile string) []string {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		if n == "" {
			continue
		}
		if profile == "safe" && (n == "delete_all" || n == "exfiltrate_data" || n == "bypass_auth") {
			continue
		}
		out = append(out, n)
	}
	return out
}

func (s *Service) enabledForRequest(req model.ChatCompletionRequest) bool {
	if req.Reasoning != nil && req.Reasoning.SkillCompilerEnabled {
		return true
	}
	return s.cfg.Enabled
}

func (s *Service) profileForRequest(req model.ChatCompletionRequest) string {
	profile := s.cfg.Profile
	if req.Reasoning != nil {
		v := strings.ToLower(strings.TrimSpace(req.Reasoning.SkillCompilerProfile))
		switch v {
		case "safe", "balanced", "aggressive":
			profile = v
		}
	}
	return profile
}

func joinUser(msgs []model.Message) string {
	parts := make([]string, 0, len(msgs))
	for _, m := range msgs {
		if strings.EqualFold(m.Role, "user") {
			parts = append(parts, strings.TrimSpace(m.Content))
		}
	}
	return strings.Join(parts, "\n")
}

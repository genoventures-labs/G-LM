package contextindex

import (
	"strings"

	"github.com/mike/cognitive-llm/internal/model"
	"github.com/mike/cognitive-llm/internal/state"
)

type Config struct {
	Enabled      bool
	DefaultScope string
}

type Service struct {
	cfg Config
}

type Result struct {
	Enabled bool
	Applied bool
	Scope   string
	Anchors []string
}

func New(cfg Config) *Service {
	scope := strings.ToLower(strings.TrimSpace(cfg.DefaultScope))
	if scope != "session" {
		scope = "request"
	}
	cfg.DefaultScope = scope
	return &Service{cfg: cfg}
}

func (s *Service) Build(req model.ChatCompletionRequest, st state.CognitiveState) Result {
	res := Result{
		Enabled: s.enabledForRequest(req),
		Scope:   s.scopeForRequest(req),
	}
	if !res.Enabled {
		return res
	}
	anchors := make([]string, 0, 8)
	anchors = append(anchors, req.MemoryAnchorKeys...)
	for _, d := range req.Documents {
		if v := strings.TrimSpace(d.ID); v != "" {
			anchors = append(anchors, "doc:"+v)
		}
		if v := strings.TrimSpace(d.Section); v != "" {
			anchors = append(anchors, "section:"+v)
		}
	}
	if res.Scope == "session" {
		if st.Pacing != "" {
			anchors = append(anchors, "session:pacing="+st.Pacing)
		}
		if st.TopicDrift >= 0.5 {
			anchors = append(anchors, "session:topic_drift_high")
		}
	}
	anchors = dedupe(anchors)
	if len(anchors) > 8 {
		anchors = anchors[:8]
	}
	if len(anchors) == 0 {
		return res
	}
	res.Applied = true
	res.Anchors = anchors
	return res
}

func (s *Service) Inject(req model.ChatCompletionRequest, r Result) model.ChatCompletionRequest {
	if !r.Applied {
		return req
	}
	msg := model.Message{
		Role: "system",
		Content: "Use these contextual anchors as lightweight retrieval hints:\n- " +
			strings.Join(r.Anchors, "\n- "),
	}
	req.Messages = append([]model.Message{msg}, req.Messages...)
	return req
}

func (s *Service) enabledForRequest(req model.ChatCompletionRequest) bool {
	if req.Reasoning != nil && req.Reasoning.ContextReindexEnabled {
		return true
	}
	return s.cfg.Enabled
}

func (s *Service) scopeForRequest(req model.ChatCompletionRequest) string {
	scope := s.cfg.DefaultScope
	if req.Reasoning != nil {
		v := strings.ToLower(strings.TrimSpace(req.Reasoning.ContextReindexScope))
		if v == "request" || v == "session" {
			scope = v
		}
	}
	return scope
}

func dedupe(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]struct{}{}
	for _, v := range in {
		k := strings.TrimSpace(v)
		if k == "" {
			continue
		}
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, k)
	}
	return out
}

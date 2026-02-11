package metareasoning

import (
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/mike/cognitive-llm/internal/model"
	"github.com/mike/cognitive-llm/internal/reasoning"
	"github.com/mike/cognitive-llm/internal/state"
)

type Config struct {
	Enabled         bool
	DefaultProfile  string
	StrictThreshold float64
	AcceptThreshold float64
}

type Evaluator struct {
	cfg Config
}

type Result struct {
	Enabled    bool     `json:"enabled"`
	Decision   string   `json:"decision"`
	Confidence float64  `json:"confidence"`
	RiskScore  float64  `json:"risk_score"`
	Flags      []string `json:"flags,omitempty"`
	Profile    string   `json:"profile"`
}

func New(cfg Config) *Evaluator {
	if strings.TrimSpace(cfg.DefaultProfile) == "" {
		cfg.DefaultProfile = "default"
	}
	if cfg.AcceptThreshold <= 0 || cfg.AcceptThreshold >= 1 {
		cfg.AcceptThreshold = 0.72
	}
	if cfg.StrictThreshold <= 0 || cfg.StrictThreshold >= 1 {
		cfg.StrictThreshold = 0.82
	}
	return &Evaluator{cfg: cfg}
}

func (e *Evaluator) ShouldRun(req model.ChatCompletionRequest) bool {
	if !e.cfg.Enabled {
		return false
	}
	return req.Reasoning != nil && req.Reasoning.MetaEnabled
}

func (e *Evaluator) Evaluate(req model.ChatCompletionRequest, resp model.ChatCompletionResponse, trace *reasoning.Trace, st state.CognitiveState) Result {
	profile := e.resolveProfile(req)
	content := firstContent(resp)
	flags := []string{}
	risk := 0.0

	if strings.TrimSpace(content) == "" {
		risk += 0.65
		flags = append(flags, "empty_output")
	}
	words := len(strings.Fields(content))
	if words > 0 && words < 24 {
		risk += 0.20
		flags = append(flags, "very_short_output")
	}
	if words < 8 {
		risk += 0.20
	}

	h := hedgingRatio(content)
	risk += clamp01(h * 0.45)
	if h >= 0.12 {
		flags = append(flags, "high_hedging")
	}

	if isExtractionTask(req) && !looksStructured(content) {
		risk += 0.20
		flags = append(flags, "task_output_mismatch")
	}

	if trace != nil && trace.Contradictions.Detected {
		risk += 0.25
		flags = append(flags, "branch_contradictions")
	}

	if st.TopicDrift >= 0.5 {
		risk += 0.12
		flags = append(flags, "high_topic_drift")
	}
	if st.MoodShift >= 0.2 {
		risk += 0.10
		flags = append(flags, "high_mood_shift")
	}

	if fabricatedCitationRe.MatchString(strings.ToLower(content)) {
		risk += 0.15
		flags = append(flags, "citation_risk")
	}

	if overassertiveClaimRe.MatchString(strings.ToLower(content)) && h < 0.03 {
		risk += 0.10
		flags = append(flags, "overassertive_claim")
	}

	risk = clamp01(risk)
	confidence := clamp01(1.0 - risk)
	decision := decide(profile, confidence, risk, e.cfg.AcceptThreshold, e.cfg.StrictThreshold)

	sort.Strings(flags)
	return Result{
		Enabled:    true,
		Decision:   decision,
		Confidence: round3(confidence),
		RiskScore:  round3(risk),
		Flags:      dedupe(flags),
		Profile:    profile,
	}
}

func (e *Evaluator) resolveProfile(req model.ChatCompletionRequest) string {
	profile := strings.ToLower(strings.TrimSpace(e.cfg.DefaultProfile))
	if req.Reasoning != nil && strings.TrimSpace(req.Reasoning.MetaProfile) != "" {
		profile = strings.ToLower(strings.TrimSpace(req.Reasoning.MetaProfile))
	}
	switch profile {
	case "fast", "default", "strict":
		return profile
	default:
		return "default"
	}
}

func decide(profile string, confidence, risk, accept, strict float64) string {
	riskLimit := 0.35
	rejectRisk := 0.68
	rejectConf := 0.36
	switch profile {
	case "fast":
		riskLimit = 0.42
		rejectRisk = 0.74
		rejectConf = 0.30
	case "strict":
		accept = math.Max(accept, strict)
		riskLimit = 0.28
		rejectRisk = 0.60
		rejectConf = 0.42
	}
	if confidence >= accept && risk <= riskLimit {
		return "accept"
	}
	if risk >= rejectRisk || confidence <= rejectConf {
		return "reject"
	}
	return "caution"
}

func firstContent(resp model.ChatCompletionResponse) string {
	if len(resp.Choices) == 0 {
		return ""
	}
	return strings.TrimSpace(resp.Choices[0].Message.Content)
}

var hedgeTerms = []string{"maybe", "might", "possibly", "unclear", "not sure", "i think", "perhaps", "likely"}

func hedgingRatio(s string) float64 {
	t := strings.ToLower(strings.TrimSpace(s))
	if t == "" {
		return 0
	}
	words := strings.Fields(t)
	if len(words) == 0 {
		return 0
	}
	hits := 0
	for _, term := range hedgeTerms {
		if strings.Contains(t, term) {
			hits++
		}
	}
	return float64(hits) / float64(len(words))
}

func isExtractionTask(req model.ChatCompletionRequest) bool {
	if req.Reasoning != nil {
		m := strings.ToLower(strings.TrimSpace(req.Reasoning.Mode))
		if strings.Contains(m, "extract") || strings.Contains(m, "classif") {
			return true
		}
	}
	for _, m := range req.Messages {
		if !strings.EqualFold(m.Role, "user") {
			continue
		}
		t := strings.ToLower(m.Content)
		if strings.Contains(t, "extract") || strings.Contains(t, "classify") || strings.Contains(t, "return json") {
			return true
		}
	}
	return false
}

func looksStructured(s string) bool {
	t := strings.TrimSpace(strings.ToLower(s))
	if strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[") {
		return true
	}
	if strings.Contains(t, "\n-") || strings.Contains(t, "\n1.") {
		return true
	}
	return false
}

var (
	fabricatedCitationRe = regexp.MustCompile(`\[[0-9]{1,3}\]`) // heuristic only
	overassertiveClaimRe = regexp.MustCompile(`\b(always|never|guaranteed|certainly|undeniably)\b`)
)

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func round3(v float64) float64 {
	r, _ := strconv.ParseFloat(strconv.FormatFloat(v, 'f', 3, 64), 64)
	return r
}

func dedupe(in []string) []string {
	if len(in) == 0 {
		return in
	}
	out := make([]string, 0, len(in))
	seen := map[string]struct{}{}
	for _, it := range in {
		if _, ok := seen[it]; ok {
			continue
		}
		seen[it] = struct{}{}
		out = append(out, it)
	}
	return out
}

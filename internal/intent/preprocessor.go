package intent

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"

	"github.com/mike/cognitive-llm/internal/model"
)

type Config struct {
	Enabled            bool
	AmbiguityThreshold float64
}

type Result struct {
	Category       string   `json:"category"`
	AmbiguityScore float64  `json:"ambiguity_score"`
	NeedsRewrite   bool     `json:"needs_rewrite"`
	Flags          []string `json:"flags,omitempty"`
}

type Processor struct {
	cfg Config
}

func NewProcessor(cfg Config) *Processor {
	if cfg.AmbiguityThreshold <= 0 || cfg.AmbiguityThreshold > 1 {
		cfg.AmbiguityThreshold = 0.62
	}
	return &Processor{cfg: cfg}
}

func (p *Processor) Process(req model.ChatCompletionRequest) (model.ChatCompletionRequest, Result) {
	if !p.cfg.Enabled {
		return req, Result{}
	}

	userText := collectUserText(req.Messages)
	norm := normalize(userText)
	amb, flags := ambiguityScore(norm)
	category := classifyIntent(norm)
	res := Result{
		Category:       category,
		AmbiguityScore: round3(amb),
		NeedsRewrite:   amb >= p.cfg.AmbiguityThreshold,
		Flags:          flags,
	}

	if len(req.Messages) == 0 {
		return req, res
	}

	out := make([]model.Message, 0, len(req.Messages))
	for _, m := range req.Messages {
		if !strings.EqualFold(m.Role, "user") {
			out = append(out, m)
			continue
		}
		nm := normalize(m.Content)
		if nm == "" {
			nm = m.Content
		}
		if res.NeedsRewrite {
			nm = rewriteStable(nm, category, amb)
		}
		out = append(out, model.Message{Role: m.Role, Content: nm})
	}
	req.Messages = out
	return req, res
}

func collectUserText(messages []model.Message) string {
	parts := make([]string, 0, len(messages))
	for _, m := range messages {
		if strings.EqualFold(m.Role, "user") {
			parts = append(parts, m.Content)
		}
	}
	return strings.Join(parts, "\n")
}

func classifyIntent(text string) string {
	t := strings.ToLower(strings.TrimSpace(text))
	if t == "" {
		return "general"
	}
	if containsAny(t, "implement", "debug", "refactor", "compile", "function", "code", "stack trace") {
		return "engineering"
	}
	if containsAny(t, "extract", "classify", "label", "return json", "schema", "fields") {
		return "extraction"
	}
	if containsAny(t, "compare", "tradeoff", "options", "plan", "roadmap", "strategy") {
		return "analysis"
	}
	if len(t) <= 140 && strings.Contains(t, "?") {
		return "qa"
	}
	return "general"
}

func ambiguityScore(text string) (float64, []string) {
	t := strings.ToLower(strings.TrimSpace(text))
	if t == "" {
		return 1.0, []string{"empty_prompt"}
	}
	score := 0.0
	flags := []string{}
	words := strings.Fields(t)
	if len(words) < 5 {
		score += 0.35
		flags = append(flags, "very_short")
	}
	vagueTerms := []string{"this", "that", "it", "thing", "stuff", "something", "whatever", "etc"}
	vagueHits := countContains(t, vagueTerms)
	if vagueHits > 0 {
		score += math.Min(0.35, float64(vagueHits)*0.12)
		flags = append(flags, "vague_references")
	}
	if strings.Count(t, "?") > 2 {
		score += 0.12
		flags = append(flags, "multi_question")
	}
	if !containsAny(t, "because", "with", "using", "for", "from", "to", "in") {
		score += 0.1
		flags = append(flags, "low_context")
	}
	if containsAny(t, "do it", "fix this", "make it better") {
		score += 0.2
		flags = append(flags, "underspecified_action")
	}
	if containsCodeSignal(t) {
		score -= 0.15
	}
	score = clamp01(score)
	sort.Strings(flags)
	flags = dedupe(flags)
	return score, flags
}

func normalize(text string) string {
	t := strings.TrimSpace(text)
	if t == "" {
		return ""
	}
	replacements := strings.NewReplacer(
		"\u2018", "'", "\u2019", "'",
		"\u201C", "\"", "\u201D", "\"",
		"\u2013", "-", "\u2014", "-",
	)
	t = replacements.Replace(t)
	t = spaceRe.ReplaceAllString(t, " ")
	t = punctRe.ReplaceAllString(t, "$1")
	t = strings.TrimSpace(t)
	if t != "" && !strings.HasSuffix(t, ".") && !strings.HasSuffix(t, "?") && !strings.HasSuffix(t, "!") {
		t = t + "."
	}
	return t
}

func rewriteStable(normalized, category string, ambiguity float64) string {
	return fmt.Sprintf("Intent=%s; Ambiguity=%.2f; Input=%s If required details are missing, ask one concise clarifying question before final answer.", category, ambiguity, normalized)
}

var (
	spaceRe = regexp.MustCompile(`\s+`)
	punctRe = regexp.MustCompile(`([!?.,;:]){2,}`)
)

func containsCodeSignal(t string) bool {
	return strings.Contains(t, "```") || containsAny(t, "panic", "exception", "compile", "stack trace", "function")
}

func containsAny(text string, terms ...string) bool {
	for _, term := range terms {
		if strings.Contains(text, term) {
			return true
		}
	}
	return false
}

func countContains(text string, terms []string) int {
	count := 0
	for _, term := range terms {
		if strings.Contains(text, term) {
			count++
		}
	}
	return count
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
	return math.Round(v*1000) / 1000
}

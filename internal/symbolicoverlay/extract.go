package symbolicoverlay

import (
	"regexp"
	"sort"
	"strings"

	"github.com/mike/cognitive-llm/internal/model"
	"github.com/mike/cognitive-llm/internal/state"
)

var wordRe = regexp.MustCompile(`[a-z0-9][a-z0-9_\-]{1,}`)

func buildArtifact(req model.ChatCompletionRequest, st state.CognitiveState, opt normalizedOptions, maxDocChars int) (OverlayArtifact, []string, int) {
	artifact := OverlayArtifact{Mode: opt.Mode, Types: append([]string{}, opt.Types...)}
	flags := []string{}
	sources := collectSources(req, st, opt, maxDocChars)
	remaining := opt.MaxSymbols

	for _, t := range opt.Types {
		if remaining <= 0 {
			flags = append(flags, "max_symbols_reached")
			break
		}
		switch t {
		case string(model.SymbolicOverlayTypeLogicMap):
			logic, used, truncated := extractLogicMap(sources, remaining)
			artifact.LogicMap = logic
			remaining -= used
			if truncated {
				flags = append(flags, "logic_map_truncated")
			}
		case string(model.SymbolicOverlayTypeConstraintSet):
			cs, used, truncated := extractConstraintSet(sources, remaining)
			artifact.ConstraintSet = cs
			remaining -= used
			if truncated {
				flags = append(flags, "constraint_set_truncated")
			}
		case string(model.SymbolicOverlayTypeRiskLens):
			rl, used, truncated := extractRiskLens(sources, remaining)
			artifact.RiskLens = rl
			remaining -= used
			if truncated {
				flags = append(flags, "risk_lens_truncated")
			}
		}
	}
	used := opt.MaxSymbols - remaining
	if used < 0 {
		used = 0
	}
	return artifact, dedupe(flags), used
}

func collectSources(req model.ChatCompletionRequest, st state.CognitiveState, opt normalizedOptions, maxDocChars int) []string {
	out := []string{}
	for _, m := range req.Messages {
		if strings.EqualFold(strings.TrimSpace(m.Role), "user") && strings.TrimSpace(m.Content) != "" {
			out = append(out, m.Content)
		}
	}
	if opt.IncludeDocuments && maxDocChars > 0 {
		remaining := maxDocChars
		for _, d := range req.Documents {
			text := strings.TrimSpace(d.Text)
			if text == "" || remaining <= 0 {
				continue
			}
			if len(text) > remaining {
				text = text[:remaining]
			}
			remaining -= len(text)
			out = append(out, d.Title+"\n"+text)
		}
	}
	if opt.IncludeState {
		stateText := strings.Join([]string{
			st.TaskMode,
			st.Topic,
			strings.Join(st.TopicKeywords, " "),
			st.Sentiment,
		}, " ")
		if strings.TrimSpace(stateText) != "" {
			out = append(out, stateText)
		}
	}
	return out
}

func extractLogicMap(sources []string, max int) (LogicMap, int, bool) {
	if max <= 0 {
		return LogicMap{}, 0, false
	}
	entityFreq := map[string]int{}
	links := []LogicLink{}
	for _, s := range sources {
		tokens := tokenize(s)
		for _, tok := range tokens {
			if isStopword(tok) || len(tok) < 3 {
				continue
			}
			entityFreq[tok]++
		}
		links = append(links, extractLinksFromSentence(s)...)
	}
	entities := topEntities(entityFreq)
	sort.Strings(entities)
	sort.Slice(links, func(i, j int) bool {
		if links[i].From == links[j].From {
			if links[i].Relation == links[j].Relation {
				return links[i].To < links[j].To
			}
			return links[i].Relation < links[j].Relation
		}
		return links[i].From < links[j].From
	})
	links = dedupeLinks(links)
	used := 0
	truncated := false
	if len(entities) > max {
		entities = entities[:max]
		used = max
		truncated = true
		return LogicMap{Entities: entities}, used, truncated
	}
	used += len(entities)
	remaining := max - used
	if len(links) > remaining {
		links = links[:remaining]
		truncated = true
	}
	used += len(links)
	return LogicMap{Entities: entities, Links: links}, used, truncated
}

func extractConstraintSet(sources []string, max int) (ConstraintSet, int, bool) {
	if max <= 0 {
		return ConstraintSet{}, 0, false
	}
	out := []Constraint{}
	for _, s := range sources {
		parts := splitSentences(s)
		for _, p := range parts {
			line := strings.TrimSpace(strings.ToLower(p))
			if line == "" {
				continue
			}
			kind := ""
			switch {
			case strings.Contains(line, "must not"), strings.Contains(line, "forbidden"), strings.Contains(line, "never"):
				kind = "prohibited"
			case strings.Contains(line, "only if"):
				kind = "conditional"
			case strings.Contains(line, "must"), strings.Contains(line, "required"), strings.Contains(line, "should"):
				kind = "required"
			}
			if kind == "" {
				continue
			}
			out = append(out, Constraint{
				Kind:     kind,
				Text:     line,
				Keywords: topKeywords(line, 4),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind == out[j].Kind {
			return out[i].Text < out[j].Text
		}
		return out[i].Kind < out[j].Kind
	})
	out = dedupeConstraints(out)
	truncated := false
	if len(out) > max {
		out = out[:max]
		truncated = true
	}
	return ConstraintSet{Items: out}, len(out), truncated
}

func extractRiskLens(sources []string, max int) (RiskLens, int, bool) {
	if max <= 0 {
		return RiskLens{}, 0, false
	}
	triggers := map[string]string{
		"incident":   "high",
		"rollback":   "high",
		"compliance": "high",
		"security":   "high",
		"failure":    "high",
		"critical":   "high",
		"latency":    "medium",
	}
	out := []RiskSignal{}
	for _, s := range sources {
		line := strings.ToLower(s)
		for trig, sev := range triggers {
			if strings.Contains(line, trig) {
				out = append(out, RiskSignal{Trigger: trig, Severity: sev, Evidence: trig})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Trigger == out[j].Trigger {
			return out[i].Severity < out[j].Severity
		}
		return out[i].Trigger < out[j].Trigger
	})
	out = dedupeRisk(out)
	truncated := false
	if len(out) > max {
		out = out[:max]
		truncated = true
	}
	return RiskLens{Signals: out}, len(out), truncated
}

func extractLinksFromSentence(text string) []LogicLink {
	verbs := map[string]struct{}{
		"deploy": {}, "rollback": {}, "monitor": {}, "audit": {}, "secure": {}, "test": {},
		"validate": {}, "optimize": {}, "analyze": {}, "plan": {}, "design": {}, "implement": {},
		"review": {}, "compare": {}, "fix": {},
	}
	tokens := tokenize(text)
	if len(tokens) < 3 {
		return nil
	}
	out := []LogicLink{}
	for i, tok := range tokens {
		if _, ok := verbs[tok]; !ok {
			continue
		}
		from := nearestEntity(tokens, i, -1)
		to := nearestEntity(tokens, i, 1)
		if from == "" || to == "" || from == to {
			continue
		}
		out = append(out, LogicLink{From: from, Relation: tok, To: to})
	}
	return out
}

func nearestEntity(tokens []string, idx, dir int) string {
	for i := idx + dir; i >= 0 && i < len(tokens); i += dir {
		t := tokens[i]
		if isStopword(t) || len(t) < 3 {
			continue
		}
		return t
	}
	return ""
}

func topEntities(freq map[string]int) []string {
	type kv struct {
		K string
		V int
	}
	items := make([]kv, 0, len(freq))
	for k, v := range freq {
		items = append(items, kv{K: k, V: v})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].V == items[j].V {
			return items[i].K < items[j].K
		}
		return items[i].V > items[j].V
	})
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.K)
	}
	return out
}

func splitSentences(s string) []string {
	repl := strings.NewReplacer("\n", ".", "!", ".", "?", ".", ";", ".")
	parts := strings.Split(repl.Replace(s), ".")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func topKeywords(s string, n int) []string {
	freq := map[string]int{}
	for _, tok := range tokenize(s) {
		if isStopword(tok) || len(tok) < 3 {
			continue
		}
		freq[tok]++
	}
	top := topEntities(freq)
	if len(top) > n {
		top = top[:n]
	}
	return top
}

func tokenize(s string) []string {
	matches := wordRe.FindAllString(strings.ToLower(s), -1)
	return matches
}

func isStopword(t string) bool {
	_, ok := map[string]struct{}{
		"the": {}, "and": {}, "for": {}, "with": {}, "this": {}, "that": {}, "from": {},
		"have": {}, "your": {}, "you": {}, "into": {}, "will": {}, "could": {}, "should": {},
		"would": {}, "there": {}, "their": {}, "about": {}, "when": {}, "where": {}, "what": {},
		"which": {}, "need": {}, "needs": {}, "using": {}, "used": {}, "are": {}, "was": {},
		"were": {}, "has": {}, "had": {}, "our": {}, "all": {}, "can": {}, "not": {},
	}[t]
	return ok
}

func dedupe(items []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(items))
	for _, it := range items {
		it = strings.TrimSpace(it)
		if it == "" {
			continue
		}
		if _, ok := seen[it]; ok {
			continue
		}
		seen[it] = struct{}{}
		out = append(out, it)
	}
	return out
}

func dedupeLinks(in []LogicLink) []LogicLink {
	seen := map[string]struct{}{}
	out := make([]LogicLink, 0, len(in))
	for _, l := range in {
		k := l.From + "|" + l.Relation + "|" + l.To
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, l)
	}
	return out
}

func dedupeConstraints(in []Constraint) []Constraint {
	seen := map[string]struct{}{}
	out := make([]Constraint, 0, len(in))
	for _, c := range in {
		k := c.Kind + "|" + c.Text
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, c)
	}
	return out
}

func dedupeRisk(in []RiskSignal) []RiskSignal {
	seen := map[string]struct{}{}
	out := make([]RiskSignal, 0, len(in))
	for _, r := range in {
		k := r.Trigger + "|" + r.Severity
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, r)
	}
	return out
}

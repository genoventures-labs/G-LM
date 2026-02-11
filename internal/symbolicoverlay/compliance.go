package symbolicoverlay

import (
	"strings"
)

func checkCompliance(text string, artifact OverlayArtifact) ComplianceResult {
	res := ComplianceResult{Checked: true, Score: 1.0}
	lower := strings.ToLower(text)
	warnings := []string{}
	violations := 0

	for _, c := range artifact.ConstraintSet.Items {
		switch c.Kind {
		case "required":
			if len(c.Keywords) == 0 {
				continue
			}
			if !containsAny(lower, c.Keywords) {
				violations++
				warnings = append(warnings, "missing_required_constraint:"+truncate(c.Text, 60))
			}
		case "prohibited":
			if containsAny(lower, c.Keywords) {
				violations++
				warnings = append(warnings, "prohibited_constraint_mentioned:"+truncate(c.Text, 60))
			}
		}
	}

	contradictions := []string{"ignore previous", "disregard constraints", "cannot comply with policy", "skip compliance", "disable security"}
	for _, phrase := range contradictions {
		if strings.Contains(lower, phrase) {
			violations++
			warnings = append(warnings, "explicit_contradiction:"+phrase)
		}
	}

	for _, r := range artifact.RiskLens.Signals {
		if r.Severity == "high" && strings.Contains(lower, "ignore "+r.Trigger) {
			violations++
			warnings = append(warnings, "risk_signal_ignored:"+r.Trigger)
		}
	}

	res.ViolationCount = violations
	res.Warnings = dedupe(warnings)
	score := 1.0 - (float64(violations) * 0.2)
	if score < 0 {
		score = 0
	}
	res.Score = score
	return res
}

func containsAny(text string, keys []string) bool {
	for _, k := range keys {
		if strings.TrimSpace(k) == "" {
			continue
		}
		if strings.Contains(text, strings.ToLower(k)) {
			return true
		}
	}
	return false
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

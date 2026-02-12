package metareasoning

import (
	"math"
	"strings"

	"github.com/mike/cognitive-llm/internal/model"
	"github.com/mike/cognitive-llm/internal/reasoning"
	"github.com/mike/cognitive-llm/internal/state"
)

type ChainNode struct {
	Name       string  `json:"name"`
	Decision   string  `json:"decision"`
	Confidence float64 `json:"confidence"`
	RiskScore  float64 `json:"risk_score"`
}

type ChainResult struct {
	Enabled    bool        `json:"enabled"`
	Chain      []string    `json:"chain,omitempty"`
	Depth      int         `json:"depth"`
	StopReason string      `json:"stop_reason"`
	Nodes      []ChainNode `json:"nodes,omitempty"`
	Final      Result      `json:"final"`
}

func (e *Evaluator) EvaluateChain(
	req model.ChatCompletionRequest,
	resp model.ChatCompletionResponse,
	trace *reasoning.Trace,
	st state.CognitiveState,
	chain []string,
	maxDepth int,
) ChainResult {
	if len(chain) == 0 || maxDepth <= 0 {
		base := e.Evaluate(req, resp, trace, st)
		return ChainResult{
			Enabled:    false,
			Chain:      nil,
			Depth:      0,
			StopReason: "disabled",
			Final:      base,
		}
	}

	if maxDepth > len(chain) {
		maxDepth = len(chain)
	}

	nodes := make([]ChainNode, 0, maxDepth)
	final := e.Evaluate(req, resp, trace, st)
	stopReason := "budget_exhausted"

	for i := 0; i < maxDepth; i++ {
		name := normalizeEvaluatorName(chain[i])
		cur := applyEvaluatorLens(name, final, req, resp)
		node := ChainNode{
			Name:       name,
			Decision:   cur.Decision,
			Confidence: cur.Confidence,
			RiskScore:  cur.RiskScore,
		}
		nodes = append(nodes, node)
		final = cur

		if name == "policy" && strings.EqualFold(cur.Decision, "reject") {
			stopReason = "policy_reject"
			break
		}
		if cur.RiskScore <= 0.30 {
			stopReason = "risk_below_threshold"
			break
		}
		if len(nodes) >= 2 {
			prev := nodes[len(nodes)-2]
			if math.Abs(prev.RiskScore-node.RiskScore) < 0.01 && math.Abs(prev.Confidence-node.Confidence) < 0.01 {
				stopReason = "no_improvement"
				break
			}
		}
	}

	return ChainResult{
		Enabled:    true,
		Chain:      chain[:maxDepth],
		Depth:      len(nodes),
		StopReason: stopReason,
		Nodes:      nodes,
		Final:      final,
	}
}

func applyEvaluatorLens(name string, base Result, req model.ChatCompletionRequest, resp model.ChatCompletionResponse) Result {
	risk := base.RiskScore
	confidence := base.Confidence
	flags := append([]string{}, base.Flags...)
	content := strings.ToLower(strings.TrimSpace(firstContent(resp)))

	switch name {
	case "consistency":
		if strings.Contains(content, "however") && strings.Contains(content, "always") {
			risk += 0.05
			flags = append(flags, "consistency_conflict")
		}
	case "risk":
		if strings.Contains(content, "unknown") || strings.Contains(content, "unverified") {
			risk += 0.05
			flags = append(flags, "risk_uncertainty")
		}
	case "policy":
		if strings.Contains(content, "ignore policy") || strings.Contains(content, "bypass guardrail") {
			risk += 0.25
			flags = append(flags, "policy_violation_signal")
		}
	case "factuality":
		if fabricatedCitationRe.MatchString(content) {
			risk += 0.08
			flags = append(flags, "factuality_citation_signal")
		}
	case "style":
		if req.ResponseStyle != nil && req.ResponseStyle.VerbosityTarget == "short" && len(strings.Fields(content)) > 220 {
			risk += 0.05
			flags = append(flags, "style_verbosity_mismatch")
		}
	}

	risk = clamp01(risk)
	confidence = clamp01(1.0 - risk)
	out := base
	out.RiskScore = round3(risk)
	out.Confidence = round3(confidence)
	out.Decision = decide(base.Profile, out.Confidence, out.RiskScore, 0.72, 0.82)
	out.Flags = dedupe(flags)
	return out
}

func normalizeEvaluatorName(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "consistency", "risk", "policy", "factuality", "style":
		return strings.ToLower(strings.TrimSpace(v))
	default:
		return "risk"
	}
}

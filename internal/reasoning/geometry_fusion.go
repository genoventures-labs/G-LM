package reasoning

import (
	"math"
	"strings"

	"github.com/mike/cognitive-llm/internal/model"
)

func (e *Executor) applyGeometryAndFusion(trace *Trace, req model.ChatCompletionRequest) {
	if trace == nil {
		return
	}
	if e.shapeTransformEnabled(req) {
		mode := e.geometryModeForRequest(req)
		trace.GeometryMode = mode
		trace.GeometryPath = geometryPathForMode(mode)
	}
	if e.worldviewFusionEnabled(req) {
		stages := e.worldviewStagesForRequest(req)
		scores := make([]float64, 0, stages)
		base := avgBranchScore(trace.Branches)
		profiles := worldviewProfilesForRequest(req)
		conflictPenalty := 0.0
		if trace.Contradictions.Detected {
			conflictPenalty = 0.08
			trace.FusionConflictMap = append([]string{}, trace.Contradictions.Pairs...)
		}
		for i := 0; i < stages; i++ {
			bias := profileBias(profiles[i%len(profiles)])
			score := clamp01Score(base - conflictPenalty + bias - float64(i)*0.01)
			scores = append(scores, round3(score))
		}
		trace.FusionStageScores = scores
		if len(trace.FusionConflictMap) == 0 {
			trace.FusionConflictMap = []string{}
		}
	}
}

func (e *Executor) shapeTransformEnabled(req model.ChatCompletionRequest) bool {
	if req.Reasoning != nil && req.Reasoning.ShapeTransformEnabled {
		return true
	}
	return e.cfg.ShapeTransformEnabled
}

func (e *Executor) worldviewFusionEnabled(req model.ChatCompletionRequest) bool {
	if req.Reasoning != nil && req.Reasoning.WorldviewFusionEnabled {
		return true
	}
	return e.cfg.WorldviewFusionEnabled
}

func (e *Executor) geometryModeForRequest(req model.ChatCompletionRequest) string {
	if req.Reasoning != nil && strings.TrimSpace(req.Reasoning.GeometryMode) != "" {
		return normalizeGeometryMode(req.Reasoning.GeometryMode)
	}
	return normalizeGeometryMode(e.cfg.GeometryMode)
}

func (e *Executor) worldviewStagesForRequest(req model.ChatCompletionRequest) int {
	stages := e.cfg.WorldviewFusionStages
	if req.Reasoning != nil && req.Reasoning.WorldviewFusionStages > 0 {
		stages = req.Reasoning.WorldviewFusionStages
	}
	if stages < 1 {
		stages = 1
	}
	if stages > 5 {
		stages = 5
	}
	return stages
}

func worldviewProfilesForRequest(req model.ChatCompletionRequest) []string {
	if req.Reasoning != nil && len(req.Reasoning.WorldviewProfiles) > 0 {
		out := make([]string, 0, len(req.Reasoning.WorldviewProfiles))
		for _, p := range req.Reasoning.WorldviewProfiles {
			v := strings.ToLower(strings.TrimSpace(p))
			switch v {
			case "risk_first", "cost_first", "safety_first", "performance_first":
				out = append(out, v)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return []string{"risk_first", "safety_first"}
}

func normalizeGeometryMode(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "linear", "tree", "mesh", "adversarial_pair", "synthesis_first":
		return strings.ToLower(strings.TrimSpace(v))
	default:
		return "linear"
	}
}

func geometryPathForMode(mode string) []string {
	switch mode {
	case "tree":
		return []string{"root", "branch_a", "branch_b", "merge"}
	case "mesh":
		return []string{"node_a", "node_b", "cross_link", "merge"}
	case "adversarial_pair":
		return []string{"attacker", "defender", "adjudicator"}
	case "synthesis_first":
		return []string{"draft_synthesis", "evidence_pass", "reconcile", "finalize"}
	default:
		return []string{"input", "analyze", "synthesize"}
	}
}

func avgBranchScore(branches []BranchResult) float64 {
	if len(branches) == 0 {
		return 0.5
	}
	sum := 0.0
	for _, b := range branches {
		sum += b.EvaluationScore
	}
	return sum / float64(len(branches))
}

func profileBias(profile string) float64 {
	switch profile {
	case "risk_first":
		return -0.01
	case "cost_first":
		return -0.02
	case "safety_first":
		return 0.01
	case "performance_first":
		return 0.02
	default:
		return 0
	}
}

func round3(v float64) float64 {
	return math.Round(v*1000) / 1000
}

func clamp01Score(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

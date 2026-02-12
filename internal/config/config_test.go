package config

import "testing"

func TestLoadSelfEvalCurveDefaults(t *testing.T) {
	t.Setenv("GLM_SELF_EVAL_CURVE_ENABLED", "")
	t.Setenv("GLM_SELF_EVAL_CURVE_LOW_MAX", "")
	t.Setenv("GLM_SELF_EVAL_CURVE_MID_MAX", "")
	t.Setenv("GLM_SELF_EVAL_CURVE_LOW_WEIGHT", "")
	t.Setenv("GLM_SELF_EVAL_CURVE_MID_WEIGHT", "")
	t.Setenv("GLM_SELF_EVAL_CURVE_HIGH_WEIGHT", "")
	t.Setenv("GLM_SELF_EVAL_CURVE_BIAS", "")

	cfg := Load()
	if cfg.SelfEvalCurveEnabled {
		t.Fatal("expected curve disabled by default")
	}
	if cfg.SelfEvalCurveLowMax != 0.60 {
		t.Fatalf("expected low max 0.60, got %f", cfg.SelfEvalCurveLowMax)
	}
	if cfg.SelfEvalCurveMidMax != 0.82 {
		t.Fatalf("expected mid max 0.82, got %f", cfg.SelfEvalCurveMidMax)
	}
	if cfg.SelfEvalCurveLowWeight != 0.90 {
		t.Fatalf("expected low weight 0.90, got %f", cfg.SelfEvalCurveLowWeight)
	}
	if cfg.SelfEvalCurveMidWeight != 1.00 {
		t.Fatalf("expected mid weight 1.00, got %f", cfg.SelfEvalCurveMidWeight)
	}
	if cfg.SelfEvalCurveHighWeight != 1.08 {
		t.Fatalf("expected high weight 1.08, got %f", cfg.SelfEvalCurveHighWeight)
	}
	if cfg.SelfEvalCurveBias != 0.00 {
		t.Fatalf("expected bias 0.00, got %f", cfg.SelfEvalCurveBias)
	}
}

func TestLoadSelfEvalCurveFromEnv(t *testing.T) {
	t.Setenv("GLM_SELF_EVAL_CURVE_ENABLED", "true")
	t.Setenv("GLM_SELF_EVAL_CURVE_LOW_MAX", "0.55")
	t.Setenv("GLM_SELF_EVAL_CURVE_MID_MAX", "0.80")
	t.Setenv("GLM_SELF_EVAL_CURVE_LOW_WEIGHT", "0.95")
	t.Setenv("GLM_SELF_EVAL_CURVE_MID_WEIGHT", "1.01")
	t.Setenv("GLM_SELF_EVAL_CURVE_HIGH_WEIGHT", "1.10")
	t.Setenv("GLM_SELF_EVAL_CURVE_BIAS", "-0.02")

	cfg := Load()
	if !cfg.SelfEvalCurveEnabled {
		t.Fatal("expected curve enabled from env")
	}
	if cfg.SelfEvalCurveLowMax != 0.55 {
		t.Fatalf("expected low max 0.55, got %f", cfg.SelfEvalCurveLowMax)
	}
	if cfg.SelfEvalCurveMidMax != 0.80 {
		t.Fatalf("expected mid max 0.80, got %f", cfg.SelfEvalCurveMidMax)
	}
	if cfg.SelfEvalCurveLowWeight != 0.95 {
		t.Fatalf("expected low weight 0.95, got %f", cfg.SelfEvalCurveLowWeight)
	}
	if cfg.SelfEvalCurveMidWeight != 1.01 {
		t.Fatalf("expected mid weight 1.01, got %f", cfg.SelfEvalCurveMidWeight)
	}
	if cfg.SelfEvalCurveHighWeight != 1.10 {
		t.Fatalf("expected high weight 1.10, got %f", cfg.SelfEvalCurveHighWeight)
	}
	if cfg.SelfEvalCurveBias != -0.02 {
		t.Fatalf("expected bias -0.02, got %f", cfg.SelfEvalCurveBias)
	}
}

func TestLoadSelfEvalCurveInvalidEnvFallsBackToDefaults(t *testing.T) {
	t.Setenv("GLM_SELF_EVAL_CURVE_LOW_MAX", "invalid")
	t.Setenv("GLM_SELF_EVAL_CURVE_MID_MAX", "invalid")
	t.Setenv("GLM_SELF_EVAL_CURVE_LOW_WEIGHT", "invalid")
	t.Setenv("GLM_SELF_EVAL_CURVE_MID_WEIGHT", "invalid")
	t.Setenv("GLM_SELF_EVAL_CURVE_HIGH_WEIGHT", "invalid")
	t.Setenv("GLM_SELF_EVAL_CURVE_BIAS", "invalid")

	cfg := Load()
	if cfg.SelfEvalCurveLowMax != 0.60 {
		t.Fatalf("expected low max fallback 0.60, got %f", cfg.SelfEvalCurveLowMax)
	}
	if cfg.SelfEvalCurveMidMax != 0.82 {
		t.Fatalf("expected mid max fallback 0.82, got %f", cfg.SelfEvalCurveMidMax)
	}
	if cfg.SelfEvalCurveLowWeight != 0.90 {
		t.Fatalf("expected low weight fallback 0.90, got %f", cfg.SelfEvalCurveLowWeight)
	}
	if cfg.SelfEvalCurveMidWeight != 1.00 {
		t.Fatalf("expected mid weight fallback 1.00, got %f", cfg.SelfEvalCurveMidWeight)
	}
	if cfg.SelfEvalCurveHighWeight != 1.08 {
		t.Fatalf("expected high weight fallback 1.08, got %f", cfg.SelfEvalCurveHighWeight)
	}
	if cfg.SelfEvalCurveBias != 0.00 {
		t.Fatalf("expected bias fallback 0.00, got %f", cfg.SelfEvalCurveBias)
	}
}

func TestLoadReasoningPruningDefaults(t *testing.T) {
	t.Setenv("GLM_REASONING_PRUNING_ENABLED", "")
	t.Setenv("GLM_REASONING_PRUNING_MIN_SCORE", "")
	t.Setenv("GLM_REASONING_PRUNING_TOT_TOPK", "")
	t.Setenv("GLM_REASONING_PRUNING_TOT_SYNTH_TOPK", "")
	t.Setenv("GLM_REASONING_PRUNING_MCTS_POOL_TOPK", "")
	t.Setenv("GLM_REASONING_PRUNING_MCTS_SYNTH_TOPK", "")
	t.Setenv("GLM_REASONING_PRUNING_MA_ROUND_TOPK", "")
	t.Setenv("GLM_REASONING_PRUNING_MA_SYNTH_TOPK", "")

	cfg := Load()
	if !cfg.ReasoningPruningEnabled {
		t.Fatal("expected reasoning pruning enabled by default")
	}
	if cfg.ReasoningPruningMinScore != 0.55 {
		t.Fatalf("expected min score 0.55, got %f", cfg.ReasoningPruningMinScore)
	}
	if cfg.ReasoningPruningToTTopK != 3 || cfg.ReasoningPruningToTSynthTopK != 2 {
		t.Fatalf("unexpected tot pruning defaults: topk=%d synth=%d", cfg.ReasoningPruningToTTopK, cfg.ReasoningPruningToTSynthTopK)
	}
	if cfg.ReasoningPruningMCTSPoolTopK != 6 || cfg.ReasoningPruningMCTSSynthTopK != 3 {
		t.Fatalf("unexpected mcts pruning defaults: pool=%d synth=%d", cfg.ReasoningPruningMCTSPoolTopK, cfg.ReasoningPruningMCTSSynthTopK)
	}
	if cfg.ReasoningPruningMARoundTopK != 4 || cfg.ReasoningPruningMASynthTopK != 3 {
		t.Fatalf("unexpected multi-agent pruning defaults: round=%d synth=%d", cfg.ReasoningPruningMARoundTopK, cfg.ReasoningPruningMASynthTopK)
	}
}

func TestLoadReasoningPruningFromEnv(t *testing.T) {
	t.Setenv("GLM_REASONING_PRUNING_ENABLED", "false")
	t.Setenv("GLM_REASONING_PRUNING_MIN_SCORE", "0.77")
	t.Setenv("GLM_REASONING_PRUNING_TOT_TOPK", "5")
	t.Setenv("GLM_REASONING_PRUNING_TOT_SYNTH_TOPK", "4")
	t.Setenv("GLM_REASONING_PRUNING_MCTS_POOL_TOPK", "8")
	t.Setenv("GLM_REASONING_PRUNING_MCTS_SYNTH_TOPK", "4")
	t.Setenv("GLM_REASONING_PRUNING_MA_ROUND_TOPK", "5")
	t.Setenv("GLM_REASONING_PRUNING_MA_SYNTH_TOPK", "4")

	cfg := Load()
	if cfg.ReasoningPruningEnabled {
		t.Fatal("expected reasoning pruning disabled from env")
	}
	if cfg.ReasoningPruningMinScore != 0.77 {
		t.Fatalf("expected min score 0.77, got %f", cfg.ReasoningPruningMinScore)
	}
	if cfg.ReasoningPruningToTTopK != 5 || cfg.ReasoningPruningToTSynthTopK != 4 {
		t.Fatalf("unexpected tot pruning env values: topk=%d synth=%d", cfg.ReasoningPruningToTTopK, cfg.ReasoningPruningToTSynthTopK)
	}
	if cfg.ReasoningPruningMCTSPoolTopK != 8 || cfg.ReasoningPruningMCTSSynthTopK != 4 {
		t.Fatalf("unexpected mcts pruning env values: pool=%d synth=%d", cfg.ReasoningPruningMCTSPoolTopK, cfg.ReasoningPruningMCTSSynthTopK)
	}
	if cfg.ReasoningPruningMARoundTopK != 5 || cfg.ReasoningPruningMASynthTopK != 4 {
		t.Fatalf("unexpected multi-agent pruning env values: round=%d synth=%d", cfg.ReasoningPruningMARoundTopK, cfg.ReasoningPruningMASynthTopK)
	}
}

func TestLoadReasoningPruningInvalidEnvFallsBackToDefaults(t *testing.T) {
	t.Setenv("GLM_REASONING_PRUNING_ENABLED", "invalid")
	t.Setenv("GLM_REASONING_PRUNING_MIN_SCORE", "invalid")
	t.Setenv("GLM_REASONING_PRUNING_TOT_TOPK", "invalid")
	t.Setenv("GLM_REASONING_PRUNING_TOT_SYNTH_TOPK", "invalid")
	t.Setenv("GLM_REASONING_PRUNING_MCTS_POOL_TOPK", "invalid")
	t.Setenv("GLM_REASONING_PRUNING_MCTS_SYNTH_TOPK", "invalid")
	t.Setenv("GLM_REASONING_PRUNING_MA_ROUND_TOPK", "invalid")
	t.Setenv("GLM_REASONING_PRUNING_MA_SYNTH_TOPK", "invalid")

	cfg := Load()
	if !cfg.ReasoningPruningEnabled {
		t.Fatal("expected pruning enabled fallback true")
	}
	if cfg.ReasoningPruningMinScore != 0.55 {
		t.Fatalf("expected min score fallback 0.55, got %f", cfg.ReasoningPruningMinScore)
	}
	if cfg.ReasoningPruningToTTopK != 3 || cfg.ReasoningPruningToTSynthTopK != 2 {
		t.Fatalf("unexpected tot fallback values: topk=%d synth=%d", cfg.ReasoningPruningToTTopK, cfg.ReasoningPruningToTSynthTopK)
	}
	if cfg.ReasoningPruningMCTSPoolTopK != 6 || cfg.ReasoningPruningMCTSSynthTopK != 3 {
		t.Fatalf("unexpected mcts fallback values: pool=%d synth=%d", cfg.ReasoningPruningMCTSPoolTopK, cfg.ReasoningPruningMCTSSynthTopK)
	}
	if cfg.ReasoningPruningMARoundTopK != 4 || cfg.ReasoningPruningMASynthTopK != 3 {
		t.Fatalf("unexpected ma fallback values: round=%d synth=%d", cfg.ReasoningPruningMARoundTopK, cfg.ReasoningPruningMASynthTopK)
	}
}

func TestLoadMCTSV2Defaults(t *testing.T) {
	t.Setenv("GLM_MCTS_V2_ENABLED", "")
	t.Setenv("GLM_MCTS_EARLY_STOP_WINDOW", "")
	t.Setenv("GLM_MCTS_EARLY_STOP_DELTA", "")
	cfg := Load()
	if cfg.MCTSV2Enabled {
		t.Fatal("expected mcts v2 disabled by default")
	}
	if cfg.MCTSEarlyStopWindow != 4 {
		t.Fatalf("expected early stop window 4, got %d", cfg.MCTSEarlyStopWindow)
	}
	if cfg.MCTSEarlyStopDelta != 0.01 {
		t.Fatalf("expected early stop delta 0.01, got %f", cfg.MCTSEarlyStopDelta)
	}
}

func TestLoadMCTSV2FromEnv(t *testing.T) {
	t.Setenv("GLM_MCTS_V2_ENABLED", "true")
	t.Setenv("GLM_MCTS_EARLY_STOP_WINDOW", "7")
	t.Setenv("GLM_MCTS_EARLY_STOP_DELTA", "0.03")
	cfg := Load()
	if !cfg.MCTSV2Enabled {
		t.Fatal("expected mcts v2 enabled from env")
	}
	if cfg.MCTSEarlyStopWindow != 7 {
		t.Fatalf("expected early stop window 7, got %d", cfg.MCTSEarlyStopWindow)
	}
	if cfg.MCTSEarlyStopDelta != 0.03 {
		t.Fatalf("expected early stop delta 0.03, got %f", cfg.MCTSEarlyStopDelta)
	}
}

func TestLoadDecomposeDefaults(t *testing.T) {
	t.Setenv("GLM_DECOMPOSE_ENABLED", "")
	t.Setenv("GLM_DECOMPOSE_MAX_SUBTASKS", "")
	t.Setenv("GLM_DECOMPOSE_MAX_DEPTH", "")
	t.Setenv("GLM_DECOMPOSE_BUDGET_TOKENS", "")
	t.Setenv("GLM_DECOMPOSE_STAGE_TIMEOUT_SECONDS", "")
	t.Setenv("GLM_DECOMPOSE_FAILOPEN", "")

	cfg := Load()
	if !cfg.DecomposeEnabled {
		t.Fatal("expected decompose enabled by default")
	}
	if cfg.DecomposeMaxSubtasks != 6 {
		t.Fatalf("expected max subtasks default 6, got %d", cfg.DecomposeMaxSubtasks)
	}
	if cfg.DecomposeMaxDepth != 1 {
		t.Fatalf("expected max depth default 1, got %d", cfg.DecomposeMaxDepth)
	}
	if cfg.DecomposeBudgetTokens != 900 {
		t.Fatalf("expected budget tokens default 900, got %d", cfg.DecomposeBudgetTokens)
	}
	if cfg.DecomposeStageTimeout.Seconds() != 40 {
		t.Fatalf("expected stage timeout default 40s, got %v", cfg.DecomposeStageTimeout)
	}
	if !cfg.DecomposeFailOpen {
		t.Fatal("expected decompose fail-open enabled by default")
	}
}

func TestLoadDecomposeFromEnv(t *testing.T) {
	t.Setenv("GLM_DECOMPOSE_ENABLED", "false")
	t.Setenv("GLM_DECOMPOSE_MAX_SUBTASKS", "4")
	t.Setenv("GLM_DECOMPOSE_MAX_DEPTH", "2")
	t.Setenv("GLM_DECOMPOSE_BUDGET_TOKENS", "700")
	t.Setenv("GLM_DECOMPOSE_STAGE_TIMEOUT_SECONDS", "25")
	t.Setenv("GLM_DECOMPOSE_FAILOPEN", "false")

	cfg := Load()
	if cfg.DecomposeEnabled {
		t.Fatal("expected decompose disabled from env")
	}
	if cfg.DecomposeMaxSubtasks != 4 {
		t.Fatalf("expected max subtasks 4, got %d", cfg.DecomposeMaxSubtasks)
	}
	if cfg.DecomposeMaxDepth != 2 {
		t.Fatalf("expected max depth 2, got %d", cfg.DecomposeMaxDepth)
	}
	if cfg.DecomposeBudgetTokens != 700 {
		t.Fatalf("expected budget tokens 700, got %d", cfg.DecomposeBudgetTokens)
	}
	if cfg.DecomposeStageTimeout.Seconds() != 25 {
		t.Fatalf("expected stage timeout 25s, got %v", cfg.DecomposeStageTimeout)
	}
	if cfg.DecomposeFailOpen {
		t.Fatal("expected decompose fail-open disabled from env")
	}
}

func TestLoadMetaReflectionDefaults(t *testing.T) {
	t.Setenv("GLM_META_REFLECTION_ENABLED", "")
	t.Setenv("GLM_META_REFLECTION_MAX_PASSES", "")
	t.Setenv("GLM_META_REFLECTION_TRIGGER_DECISIONS", "")

	cfg := Load()
	if cfg.MetaReflectionEnabled {
		t.Fatal("expected meta reflection disabled by default")
	}
	if cfg.MetaReflectionMaxPasses != 1 {
		t.Fatalf("expected max passes default 1, got %d", cfg.MetaReflectionMaxPasses)
	}
	if len(cfg.MetaReflectionTriggerDecisions) != 2 ||
		cfg.MetaReflectionTriggerDecisions[0] != "caution" ||
		cfg.MetaReflectionTriggerDecisions[1] != "reject" {
		t.Fatalf("unexpected trigger defaults: %#v", cfg.MetaReflectionTriggerDecisions)
	}
}

func TestLoadMetaReflectionFromEnv(t *testing.T) {
	t.Setenv("GLM_META_REFLECTION_ENABLED", "true")
	t.Setenv("GLM_META_REFLECTION_MAX_PASSES", "3")
	t.Setenv("GLM_META_REFLECTION_TRIGGER_DECISIONS", "reject, caution , accept")

	cfg := Load()
	if !cfg.MetaReflectionEnabled {
		t.Fatal("expected meta reflection enabled from env")
	}
	if cfg.MetaReflectionMaxPasses != 3 {
		t.Fatalf("expected max passes 3, got %d", cfg.MetaReflectionMaxPasses)
	}
	if len(cfg.MetaReflectionTriggerDecisions) != 3 {
		t.Fatalf("unexpected trigger values: %#v", cfg.MetaReflectionTriggerDecisions)
	}
}

func TestLoadSelfAlignmentDefaults(t *testing.T) {
	t.Setenv("GLM_SELF_ALIGNMENT_ENABLED", "")
	t.Setenv("GLM_SELF_ALIGNMENT_MAX_PASSES", "")

	cfg := Load()
	if cfg.SelfAlignmentEnabled {
		t.Fatal("expected self-alignment disabled by default")
	}
	if cfg.SelfAlignmentMaxPasses != 2 {
		t.Fatalf("expected self-alignment max passes default 2, got %d", cfg.SelfAlignmentMaxPasses)
	}
}

func TestLoadSelfAlignmentFromEnv(t *testing.T) {
	t.Setenv("GLM_SELF_ALIGNMENT_ENABLED", "true")
	t.Setenv("GLM_SELF_ALIGNMENT_MAX_PASSES", "3")

	cfg := Load()
	if !cfg.SelfAlignmentEnabled {
		t.Fatal("expected self-alignment enabled from env")
	}
	if cfg.SelfAlignmentMaxPasses != 3 {
		t.Fatalf("expected self-alignment max passes 3, got %d", cfg.SelfAlignmentMaxPasses)
	}
}

func TestLoadMemoryAnchoredReasoningDefaults(t *testing.T) {
	t.Setenv("GLM_MEMORY_ANCHORED_REASONING_ENABLED", "")
	t.Setenv("GLM_MEMORY_ANCHORED_REASONING_MAX_ANCHORS", "")
	t.Setenv("GLM_MEMORY_ANCHORED_REASONING_MIN_COVERAGE", "")
	t.Setenv("GLM_MEMORY_ANCHORED_REASONING_SCORE_BONUS", "")

	cfg := Load()
	if cfg.MemoryAnchoredReasoningEnabled {
		t.Fatal("expected memory anchored reasoning disabled by default")
	}
	if cfg.MemoryAnchoredReasoningMaxAnchors != 3 {
		t.Fatalf("expected max anchors default 3, got %d", cfg.MemoryAnchoredReasoningMaxAnchors)
	}
	if cfg.MemoryAnchoredReasoningMinCoverage != 0.34 {
		t.Fatalf("expected min coverage default 0.34, got %f", cfg.MemoryAnchoredReasoningMinCoverage)
	}
	if cfg.MemoryAnchoredReasoningScoreBonus != 0.06 {
		t.Fatalf("expected score bonus default 0.06, got %f", cfg.MemoryAnchoredReasoningScoreBonus)
	}
}

func TestLoadMemoryAnchoredReasoningFromEnv(t *testing.T) {
	t.Setenv("GLM_MEMORY_ANCHORED_REASONING_ENABLED", "true")
	t.Setenv("GLM_MEMORY_ANCHORED_REASONING_MAX_ANCHORS", "5")
	t.Setenv("GLM_MEMORY_ANCHORED_REASONING_MIN_COVERAGE", "0.5")
	t.Setenv("GLM_MEMORY_ANCHORED_REASONING_SCORE_BONUS", "0.1")

	cfg := Load()
	if !cfg.MemoryAnchoredReasoningEnabled {
		t.Fatal("expected memory anchored reasoning enabled from env")
	}
	if cfg.MemoryAnchoredReasoningMaxAnchors != 5 {
		t.Fatalf("expected max anchors 5, got %d", cfg.MemoryAnchoredReasoningMaxAnchors)
	}
	if cfg.MemoryAnchoredReasoningMinCoverage != 0.5 {
		t.Fatalf("expected min coverage 0.5, got %f", cfg.MemoryAnchoredReasoningMinCoverage)
	}
	if cfg.MemoryAnchoredReasoningScoreBonus != 0.1 {
		t.Fatalf("expected score bonus 0.1, got %f", cfg.MemoryAnchoredReasoningScoreBonus)
	}
}

func TestLoadSymbolicSupervisionDefaults(t *testing.T) {
	t.Setenv("GLM_SYMBOLIC_SUPERVISION_ENABLED", "")
	t.Setenv("GLM_SYMBOLIC_SUPERVISION_WARN_THRESHOLD", "")
	t.Setenv("GLM_SYMBOLIC_SUPERVISION_REJECT_THRESHOLD", "")
	t.Setenv("GLM_SYMBOLIC_SUPERVISION_AUTO_REVISE", "")
	t.Setenv("GLM_SYMBOLIC_SUPERVISION_MAX_PASSES", "")

	cfg := Load()
	if cfg.SymbolicSupervisionEnabled {
		t.Fatal("expected symbolic supervision disabled by default")
	}
	if cfg.SymbolicSupervisionWarnThreshold != 1 {
		t.Fatalf("expected warn threshold 1, got %d", cfg.SymbolicSupervisionWarnThreshold)
	}
	if cfg.SymbolicSupervisionRejectThreshold != 3 {
		t.Fatalf("expected reject threshold 3, got %d", cfg.SymbolicSupervisionRejectThreshold)
	}
	if !cfg.SymbolicSupervisionAutoRevise {
		t.Fatal("expected auto revise default true")
	}
	if cfg.SymbolicSupervisionMaxPasses != 1 {
		t.Fatalf("expected max passes 1, got %d", cfg.SymbolicSupervisionMaxPasses)
	}
}

func TestLoadSymbolicSupervisionFromEnv(t *testing.T) {
	t.Setenv("GLM_SYMBOLIC_SUPERVISION_ENABLED", "true")
	t.Setenv("GLM_SYMBOLIC_SUPERVISION_WARN_THRESHOLD", "2")
	t.Setenv("GLM_SYMBOLIC_SUPERVISION_REJECT_THRESHOLD", "4")
	t.Setenv("GLM_SYMBOLIC_SUPERVISION_AUTO_REVISE", "false")
	t.Setenv("GLM_SYMBOLIC_SUPERVISION_MAX_PASSES", "0")

	cfg := Load()
	if !cfg.SymbolicSupervisionEnabled {
		t.Fatal("expected symbolic supervision enabled")
	}
	if cfg.SymbolicSupervisionWarnThreshold != 2 {
		t.Fatalf("expected warn threshold 2, got %d", cfg.SymbolicSupervisionWarnThreshold)
	}
	if cfg.SymbolicSupervisionRejectThreshold != 4 {
		t.Fatalf("expected reject threshold 4, got %d", cfg.SymbolicSupervisionRejectThreshold)
	}
	if cfg.SymbolicSupervisionAutoRevise {
		t.Fatal("expected auto revise false from env")
	}
	if cfg.SymbolicSupervisionMaxPasses != 0 {
		t.Fatalf("expected max passes 0, got %d", cfg.SymbolicSupervisionMaxPasses)
	}
}

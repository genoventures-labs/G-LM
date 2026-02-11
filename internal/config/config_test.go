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

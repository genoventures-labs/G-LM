# Changelog

All notable changes to this project are documented in this file.

## [0.8.0] - 2026-02-12

### Added
- Cognitive shape transformation request controls:
  - `reasoning.shape_transform_enabled`
  - `reasoning.geometry_mode` (`linear|tree|mesh|adversarial_pair|synthesis_first`)
- Multi-stage worldview fusion request controls:
  - `reasoning.worldview_fusion_enabled`
  - `reasoning.worldview_fusion_stages`
  - `reasoning.worldview_profiles` (`risk_first|cost_first|safety_first|performance_first`)
- Geometry/fusion trace payload fields:
  - `geometry_mode`
  - `geometry_path`
  - `fusion_stage_scores`
  - `fusion_conflict_map`
- New runtime telemetry headers:
  - `X-GLM-Geometry-Mode`
  - `X-GLM-Geometry-Steps`
  - `X-GLM-Worldview-Fusion`
  - `X-GLM-Worldview-Stages`
- New optional config surface:
  - `GLM_SHAPE_TRANSFORM_ENABLED`
  - `GLM_GEOMETRY_MODE`
  - `GLM_WORLDVIEW_FUSION_ENABLED`
  - `GLM_WORLDVIEW_FUSION_STAGES`
- Deliberate constraint breaking controls:
  - `reasoning.constraint_breaking_enabled`
  - `reasoning.constraint_breaking_level` (`low|medium|high`)
- Adversarial self-play controls:
  - `reasoning.adversarial_self_play_enabled`
  - `reasoning.adversarial_rounds`
  - `reasoning.adversarial_roles`
- New runtime telemetry headers:
  - `X-GLM-Constraint-Breaking`
  - `X-GLM-Constraint-Breaking-Level`
  - `X-GLM-Adversarial-Self-Play`
  - `X-GLM-Adversarial-Rounds`
- New optional config surface:
  - `GLM_CONSTRAINT_BREAKING_ENABLED`
  - `GLM_CONSTRAINT_BREAKING_LEVEL`
  - `GLM_ADVERSARIAL_SELF_PLAY_ENABLED`
  - `GLM_ADVERSARIAL_ROUNDS`

### Changed
- Reasoning traces for `tot`, `mcts`, `multi_agent`, and `decompose` now include geometry/fusion metadata when enabled.
- Cognitive policy gating now blocks unauthorized activation of shape transform and worldview fusion.
- Cognitive policy gating now blocks unauthorized activation of constraint breaking and adversarial self-play (including severity bound checks).
- Default behavior remains backward-compatible and opt-in for geometry/fusion stages.

## [0.7.0] - 2026-02-12

### Added
- Contextual re-indexing request controls:
  - `reasoning.context_reindex_enabled`
  - `reasoning.context_reindex_scope` (`request|session`)
- Primitive-level skill compiler request controls:
  - `reasoning.skill_compiler_enabled`
  - `reasoning.skill_compiler_profile` (`safe|balanced|aggressive`)
  - `reasoning.skill_compiler_budget_tokens`
- New context reindex module:
  - `internal/contextindex`
  - request/session-scoped contextual anchor building and injection pre-stage
- New skill compiler module:
  - `internal/skillcompiler`
  - primitive execution-plan compilation and injection pre-stage
- New runtime telemetry headers:
  - `X-GLM-Context-Reindex`
  - `X-GLM-Context-Reindex-Scope`
  - `X-GLM-Skill-Compiler`
  - `X-GLM-Skill-Plan-Nodes`
- New optional config surface:
  - `GLM_CONTEXT_REINDEX_ENABLED`
  - `GLM_CONTEXT_REINDEX_SCOPE`
  - `GLM_SKILL_COMPILER_ENABLED`
  - `GLM_SKILL_COMPILER_PROFILE`
  - `GLM_SKILL_COMPILER_BUDGET_TOKENS`

### Changed
- Cognitive policy now supports gating context reindex and skill compiler activations (`allow_context_reindex`, existing `allow_skill_compiler`).
- Audit outcomes now include context reindex and skill compiler status markers.
- Default behavior remains backward-compatible and opt-in for both Phase 3 features.

## [0.6.0] - 2026-02-12

### Added
- Tenant-specific cognitive policy control plane:
  - New admin endpoints:
    - `POST /admin/v1/tenants/{tenant_id}/cognitive-policy`
    - `GET /admin/v1/tenants/{tenant_id}/cognitive-policy`
  - New cognitive policy persistence and resolution path across memory + PocketBase stores.
  - New audit outcome tags:
    - `cognitive_policy=...`
    - `policy_gate=...`
- Symbolic overlay V3 and profile surface:
  - `symbolic_overlay.schema_version`
  - `symbolic_overlay.overlay_profile`
  - `symbolic_overlay.max_overlay_hops`
  - New symbolic telemetry headers:
    - `X-GLM-Symbolic-Version`
    - `X-GLM-Symbolic-Profile`
- Style contract V2 surface:
  - `response_style.register`
  - `response_style.verbosity_target`
  - `response_style.justification_density`
  - `response_style.audience_mode`
  - New style telemetry header:
    - `X-GLM-Style-Audience`
- Reflection layers + evaluator chain request/config surface:
  - Request fields:
    - `reflection_layers_enabled`
    - `reflection_layer_count`
    - `evaluator_chain_enabled`
    - `evaluator_chain`
    - `evaluator_chain_max_depth`
  - Config env vars:
    - `GLM_REFLECTION_LAYERS_ENABLED`
    - `GLM_REFLECTION_LAYER_COUNT`
    - `GLM_EVALUATOR_CHAIN_ENABLED`
    - `GLM_EVALUATOR_CHAIN`
    - `GLM_EVALUATOR_CHAIN_MAX_DEPTH`
  - New reflection/evaluator telemetry headers:
    - `X-GLM-Evaluator-Chain`
    - `X-GLM-Evaluator-Depth`
    - `X-GLM-Reflection-Layers`
    - `X-GLM-Reflection-Stop-Reason`

### Changed
- Runtime policy enforcement now applies tenant cognitive policy gates before reasoning execution and tool usage.
- Symbolic and style layers preserve backward-compatible defaults while enabling V3/V2 opt-in behavior.
- Meta reflection execution now supports evaluator-chain aware stop reasons and explicit reflection-layer telemetry.
- Existing `meta_reflection_*` and `self_alignment_*` controls remain supported as aliases.

## [0.3.0] - 2026-02-12

### Added
- Self-alignment loop telemetry headers:
  - `X-GLM-Self-Alignment`
  - `X-GLM-Self-Alignment-Passes`
  - `X-GLM-Self-Alignment-Reason`
- New optional self-alignment configuration surface:
  - `GLM_SELF_ALIGNMENT_ENABLED`
  - `GLM_SELF_ALIGNMENT_MAX_PASSES`
- Request-level self-alignment aliases in `reasoning`:
  - `self_alignment_enabled`
  - `self_alignment_max_passes`

### Changed
- Meta reflection now supports bounded multi-pass revise-and-reevaluate loops (up to configured budget) instead of a single forced pass.
- Reflection controls remain backward-compatible; existing `meta_reflection_*` options continue to work while self-alignment aliases map to the same loop.
- Audit outcome tags now include explicit self-alignment status, reason, and pass count.

## [0.2.0] - 2026-02-12

### Added
- New reasoning mode `decompose` for ephemeral subtask decomposition with request-scoped execution.
- New request-level decomposition controls in `reasoning`:
  - `decompose_enabled`
  - `decompose_max_subtasks`
  - `decompose_max_depth`
  - `decompose_budget_tokens`
- New decomposition execution trace payload:
  - `subtasks_planned`
  - `subtasks_executed`
  - `depth`
  - `best_score`
  - `fallback`
- New decomposition runtime headers:
  - `X-GLM-Decompose-Subtasks-Planned`
  - `X-GLM-Decompose-Subtasks-Executed`
  - `X-GLM-Decompose-Best-Score`
  - `X-GLM-Decompose-Fallback`
- New optional decomposition configuration surface:
  - `GLM_DECOMPOSE_ENABLED`
  - `GLM_DECOMPOSE_MAX_SUBTASKS`
  - `GLM_DECOMPOSE_MAX_DEPTH`
  - `GLM_DECOMPOSE_BUDGET_TOKENS`
  - `GLM_DECOMPOSE_STAGE_TIMEOUT_SECONDS`
  - `GLM_DECOMPOSE_FAILOPEN`

### Changed
- Reasoning pipeline now supports `reasoning.mode="decompose"` alongside `tot`, `mcts`, and `multi_agent`.
- Decomposition is bounded to single-level (v1), executes subtasks sequentially on one selected model, and keeps artifacts request-scoped only.
- Server fail-open flow now supports decomposition fallback (`decompose -> tot -> direct`) with explicit telemetry.
- Cognition route remains backward-compatible; decomposition is opt-in through explicit reasoning mode only.

## [0.1.6] - 2026-02-11

### Added
- Memory-anchored reasoning v1 telemetry headers:
  - `X-GLM-Reasoning-Memory-Anchor`
  - `X-GLM-Reasoning-Memory-Anchors-In`
  - `X-GLM-Reasoning-Memory-Anchors-Used`
  - `X-GLM-Reasoning-Memory-Coverage-Avg`
  - `X-GLM-Reasoning-Memory-Bonus-Avg`
- New optional memory-anchored reasoning configuration surface:
  - `GLM_MEMORY_ANCHORED_REASONING_ENABLED`
  - `GLM_MEMORY_ANCHORED_REASONING_MAX_ANCHORS`
  - `GLM_MEMORY_ANCHORED_REASONING_MIN_COVERAGE`
  - `GLM_MEMORY_ANCHORED_REASONING_SCORE_BONUS`
- Symbolic supervision node telemetry headers:
  - `X-GLM-Symbolic-Supervision`
  - `X-GLM-Symbolic-Supervision-Decision`
  - `X-GLM-Symbolic-Supervision-Action`
  - `X-GLM-Symbolic-Supervision-Reason`
  - `X-GLM-Symbolic-Supervision-Nodes`
  - `X-GLM-Symbolic-Supervision-Passes`
- New optional symbolic supervision configuration surface:
  - `GLM_SYMBOLIC_SUPERVISION_ENABLED`
  - `GLM_SYMBOLIC_SUPERVISION_WARN_THRESHOLD`
  - `GLM_SYMBOLIC_SUPERVISION_REJECT_THRESHOLD`
  - `GLM_SYMBOLIC_SUPERVISION_AUTO_REVISE`
  - `GLM_SYMBOLIC_SUPERVISION_MAX_PASSES`

### Changed
- Reasoning modes (`tot`, `mcts`, `multi_agent`) can now consume memory anchor keys from memory dynamics as deterministic prompt hints (strict opt-in).
- Candidate evaluation now supports bounded anchor-coverage score bonus when memory-anchored reasoning is enabled.
- Reasoning traces now include memory-anchor aggregation metadata (`enabled/applied`, anchor counts, average coverage/bonus).
- Existing behavior remains fail-open and backward-compatible when memory anchors are unavailable or feature is disabled.
- Symbolic strict mode now includes a supervision-node decision layer (`accept|caution|reject` with `none|warn|revise|reject` actions), optional single-pass revise/recheck, and fail-open retention of original responses on supervision errors.

## [0.1.4] - 2026-02-11

### Added
- OpenAI-compatible tool-calling loop in runtime gateway, including support for assistant `tool_calls` and `tool` role messages.
- New tool dispatch client for external tool server integration (`/tools/*`) with required auth headers (`Authorization`, `X-Client-Id`).
- Automatic tool schema discovery from tool server `/openapi.json` when `tool_choice="auto"` and request omits `tools`.
- Per-tool runtime dispatch policies:
  - `web_search`: timeout 12s, up to 2 retries on network/5xx
  - `fetch_url`: timeout 15s, up to 1 retry on network/5xx (no 4xx retry)
  - `vector_retrieve`: timeout 20s, up to 1 retry on embedding backend transient 429/5xx
  - `http_request`: timeout 20s, no retries
  - `code_exec_sandbox`: timeout 45s, no retries
- Tool-calling runtime headers:
  - `X-GLM-Tool-Calling`
  - `X-GLM-Tool-Calls`
  - `X-GLM-Tool-Iterations`
  - `X-GLM-Tool-Error`
- New configuration surface for tool calling:
  - `GLM_TOOL_CALLING_ENABLED`
  - `GLM_TOOL_SERVER_BASE_URL`
  - `GLM_TOOL_SERVER_API_KEY`
  - `GLM_TOOL_SERVER_CLIENT_ID`
  - `GLM_TOOL_CALLING_MAX_ITERATIONS`
  - `GLM_TOOL_CALLING_TIMEOUT_SECONDS`
- New optional self-evaluation weighting curve configuration surface:
  - `GLM_SELF_EVAL_CURVE_ENABLED`
  - `GLM_SELF_EVAL_CURVE_LOW_MAX`
  - `GLM_SELF_EVAL_CURVE_MID_MAX`
  - `GLM_SELF_EVAL_CURVE_LOW_WEIGHT`
  - `GLM_SELF_EVAL_CURVE_MID_WEIGHT`
  - `GLM_SELF_EVAL_CURVE_HIGH_WEIGHT`
  - `GLM_SELF_EVAL_CURVE_BIAS`
- New reasoning pruning telemetry headers:
  - `X-GLM-Reasoning-Pruning`
  - `X-GLM-Reasoning-Prune-In`
  - `X-GLM-Reasoning-Prune-Out`
  - `X-GLM-Reasoning-Prune-Dropped`
- New reasoning graph pruning configuration surface:
  - `GLM_REASONING_PRUNING_ENABLED`
  - `GLM_REASONING_PRUNING_MIN_SCORE`
  - `GLM_REASONING_PRUNING_TOT_TOPK`
  - `GLM_REASONING_PRUNING_TOT_SYNTH_TOPK`
  - `GLM_REASONING_PRUNING_MCTS_POOL_TOPK`
  - `GLM_REASONING_PRUNING_MCTS_SYNTH_TOPK`
  - `GLM_REASONING_PRUNING_MA_ROUND_TOPK`
  - `GLM_REASONING_PRUNING_MA_SYNTH_TOPK`
- New MCTS v2 telemetry headers:
  - `X-GLM-MCTS-V2`
  - `X-GLM-MCTS-Early-Stop`
  - `X-GLM-MCTS-Rollouts-Executed`
- New optional MCTS v2 configuration surface:
  - `GLM_MCTS_V2_ENABLED`
  - `GLM_MCTS_EARLY_STOP_WINDOW`
  - `GLM_MCTS_EARLY_STOP_DELTA`
- New meta reflection v2 telemetry headers (meta-enabled flows only):
  - `X-GLM-Meta-Reflection`
  - `X-GLM-Meta-Reflection-Passes`
  - `X-GLM-Meta-Reflection-Reason`
- New optional meta reflection v2 configuration surface:
  - `GLM_META_REFLECTION_ENABLED`
  - `GLM_META_REFLECTION_MAX_PASSES`
  - `GLM_META_REFLECTION_TRIGGER_DECISIONS`
- New memory-anchored reasoning telemetry headers:
  - `X-GLM-Reasoning-Memory-Anchor`
  - `X-GLM-Reasoning-Memory-Anchors-In`
  - `X-GLM-Reasoning-Memory-Anchors-Used`
  - `X-GLM-Reasoning-Memory-Coverage-Avg`
  - `X-GLM-Reasoning-Memory-Bonus-Avg`
- New optional memory-anchored reasoning configuration surface:
  - `GLM_MEMORY_ANCHORED_REASONING_ENABLED`
  - `GLM_MEMORY_ANCHORED_REASONING_MAX_ANCHORS`
  - `GLM_MEMORY_ANCHORED_REASONING_MIN_COVERAGE`
  - `GLM_MEMORY_ANCHORED_REASONING_SCORE_BONUS`

### Changed
- Reasoning pipelines (`tot`, `mcts`, `multi_agent`) now support tool-calling via a tool-aware upstream wrapper.
- Unified cognition normalization now propagates `tools` and `tool_choice`.
- `GLM_TOOL_CALLING_TIMEOUT_SECONDS` default updated to `60` (hard timeout ceiling for tool dispatch).
- `reasoning.self_evaluate=false` is now enforced for ToT/MCTS/multi-agent scoring paths, returning neutral evaluation score (`0.5`) with no state-penalty application.
- Self-evaluation weighting curves are backward-compatible and disabled by default, preserving legacy scoring behavior unless explicitly enabled.
- Deterministic graph-pruning heuristics now apply to reasoning modes (`tot`, `mcts`, `multi_agent`) during accumulation and pre-synthesis, with fail-open baseline behavior when pruning removes all candidates.
- MCTS v2 quality mode adds UCB-tuned selection, task-aware action priors/prompts, and convergence-based early stop while preserving legacy behavior by default.
- Meta reflection v2 adds a bounded single-pass revise-and-reevaluate loop when meta decisions match configured trigger decisions (`caution,reject` by default), with strict opt-in defaults and fail-open retention of the original response on reflection errors.
- Memory-anchored reasoning v1 adds deterministic anchor hints to ToT/MCTS/multi-agent prompts and bounded anchor-coverage score bonus, while preserving fail-open legacy behavior when anchors are unavailable or feature is disabled.

### Fixed
- Cross-package response schema/test compatibility after expanding assistant message shape with `name` and `tool_calls`.

## [0.1.2] - 2026-02-11

### Changed
- Document orchestration now relies on live upstream model inventory for request-time model validation/resolution rather than implicit model passthrough.
- Gateway reuses a single `/api/v1/models` inventory fetch per request when auto-routing or document orchestration is active.
- Document chunk/document summarization subcalls now run on the same resolved final model (single-model lane).
- Router upgraded to a predictive JIT inventory manager with a background reconciliation loop and class-aware ideal model routing.
- Router now performs fail-open async pull scheduling for missing ideal models while continuing request routing on warm models.
- Added LRU-based inventory cleanup guards with protected model exclusions and threshold-based pruning behavior.
- Added startup/runtime wiring for sovereign Ollama control-plane operations separate from OpenWebUI inference traffic.
- Default Ollama control endpoint now resolves to `http://127.0.0.1:11434` so JIT control is enabled by default when JIT is enabled.
- Added deterministic symbolic overlay stage (`symbolic_overlay`) after intent preprocessing and before document orchestration, with fail-open strict compliance checks.
- Added symbolic telemetry tags to audit outcomes: enablement, mode, types, violations, and symbolic error marker.
- Tool-calling now supports reasoning modes (ToT/MCTS/multi-agent) through a tool-aware upstream wrapper.
- Tool schemas can be auto-discovered from tool server `/openapi.json` when `tool_choice=\"auto\"` and `tools` are omitted.
- Tool dispatch now enforces per-tool runtime timeout/retry policy defaults aligned with the tool server contract.

### Added
- New response header when document orchestration is applied:
  - `X-GLM-Document-Model`
- OpenAI-compatible tool-calling support in runtime gateway with external tool server dispatch (`web_search`, `fetch_url`, `http_request`, `vector_retrieve`, `code_exec_sandbox`) and tool loop telemetry headers.
- Explicit model canonicalization against live inventory (case-insensitive match to upstream model id).
- New direct Ollama control client for model inventory/pull/prune operations:
  - `GET /api/tags`
  - `POST /api/pull`
  - `DELETE /api/delete`
  - optional host stats probe support
- New JIT inventory config surface:
  - `GLM_JIT_INVENTORY_ENABLED`
  - `GLM_JIT_RECONCILE_SECONDS`
  - `GLM_JIT_RECONCILE_JITTER_SECONDS`
  - `GLM_JIT_MAX_MODELS`
  - `GLM_JIT_STORAGE_HIGH_WATERMARK`
  - `GLM_JIT_STORAGE_TARGET_WATERMARK`
  - `GLM_JIT_PULL_TIMEOUT_SECONDS`
  - `GLM_JIT_PRUNE_ENABLED`
  - `GLM_JIT_IDEAL_CODING`
  - `GLM_JIT_IDEAL_EXTRACTION`
  - `GLM_JIT_IDEAL_LIGHT_QA`
  - `GLM_JIT_IDEAL_GENERAL`
  - `GLM_OLLAMA_CONTROL_URL`
  - `GLM_OLLAMA_CONTROL_API_KEY`
- New routing observability headers:
  - `X-GLM-Ideal-Model`
  - `X-GLM-Ideal-Available`
  - `X-GLM-JIT-Pull-Triggered`
  - `X-GLM-JIT-Inventory-Stale`
- New symbolic overlay request surface on chat/cognition requests:
  - `symbolic_overlay.mode` (`off|assist|strict`)
  - `symbolic_overlay.types` (`logic_map|constraint_set|risk_lens`)
  - `symbolic_overlay.max_symbols`
  - `symbolic_overlay.include_state`
  - `symbolic_overlay.include_documents`
- New symbolic overlay runtime headers:
  - `X-GLM-Symbolic-Overlay`
  - `X-GLM-Symbolic-Mode`
  - `X-GLM-Symbolic-Types`
  - `X-GLM-Symbolic-Symbols`
  - `X-GLM-Symbolic-Violations`
  - `X-GLM-Symbolic-Error`
- New symbolic overlay configuration surface:
  - `GLM_SYMBOLIC_OVERLAY_ENABLED`
  - `GLM_SYMBOLIC_OVERLAY_MAX_SYMBOLS`
  - `GLM_SYMBOLIC_OVERLAY_MAX_DOC_CHARS`
  - `GLM_SYMBOLIC_OVERLAY_STRICT_CHECK`
- New tool-calling configuration surface:
  - `GLM_TOOL_CALLING_ENABLED`
  - `GLM_TOOL_SERVER_BASE_URL`
  - `GLM_TOOL_SERVER_API_KEY`
  - `GLM_TOOL_SERVER_CLIENT_ID`
  - `GLM_TOOL_CALLING_MAX_ITERATIONS`
  - `GLM_TOOL_CALLING_TIMEOUT_SECONDS`

### Fixed
- Explicit unavailable models now fail fast with `503 requested model is not available upstream` and audit outcome tag `route=explicit.model_unavailable`.
- Added coverage to ensure one inventory lookup in auto+docflow paths and explicit-unavailable handling.
- Added deduping/cooldown protections to avoid repeated concurrent pulls for the same ideal model.

## [0.1.1] - 2026-02-11

### Added
- Opt-in Monte Carlo Tree Search agent mode via `reasoning.mode="mcts"` for `/v1/chat/completions` and `/v1/cognition`.
- New request-level MCTS controls in `reasoning`:
  - `mcts_max_rollouts`
  - `mcts_max_depth`
  - `mcts_exploration`
  - `mcts_timeout_ms`
- Runtime MCTS observability headers:
  - `X-GLM-Reasoning-Pipeline: mcts`
  - `X-GLM-MCTS-Rollouts`
  - `X-GLM-MCTS-Depth`
  - `X-GLM-MCTS-Best-Score`
  - `X-GLM-MCTS-Fallback` (when fail-open is used)
- MCTS configuration surface:
  - `GLM_MCTS_ENABLED`
  - `GLM_MCTS_DEFAULT_ROLLOUTS`
  - `GLM_MCTS_MAX_ROLLOUTS`
  - `GLM_MCTS_DEFAULT_DEPTH`
  - `GLM_MCTS_MAX_DEPTH`
  - `GLM_MCTS_DEFAULT_EXPLORATION`
  - `GLM_MCTS_STAGE_TIMEOUT_SECONDS`
  - `GLM_MCTS_FAILOPEN`
- MCTS unit/integration coverage and `eval-v2` MCTS validation case.

### Changed
- Reasoning executor now dispatches by mode (`tot|pipeline|mcts`) while preserving existing ToT behavior.
- Added MCTS trace metadata (`rollouts`, `depth`, `best_score`, `fallback`) for headers/audit propagation.
- README updated with MCTS environment variables and usage examples.

### Fixed
- MCTS fail-open direct fallback now clears reasoning mode before direct upstream call to prevent recursive mode failures.

## [0.1.0] - 2026-02-11

### Added
- OpenAI-compatible runtime gateway endpoints and unified cognition route.
- Deterministic model orchestrator with `auto` model routing, alias resolution, and tenant allowlist enforcement.
- Cognitive modules in gateway path: state manager, intent correction, emotional modulation, document orchestration, memory dynamics, and reasoning graph execution hooks.
- Meta-reasoning V1 (evaluate-only, opt-in) with profile support and response headers:
  - `X-GLM-Meta-Reasoning`
  - `X-GLM-Meta-Decision`
  - `X-GLM-Meta-Confidence`
  - `X-GLM-Meta-Risk-Score`
  - `X-GLM-Meta-Profile`
- PocketBase-backed control-plane and memory-node integration with service-account auth support.
- Operational CLI checks including strict `doctor` and expanded `eval-v2` validation for cognition/meta flows.

### Changed
- Gateway defaults tuned for production reliability: tighter token budgets and stage-specific timeout handling.
- Cognition routing now applies a configurable default model when task model is omitted.
- Memory/context and document/reasoning stages now degrade gracefully under upstream or store pressure.
- Improved configuration surface for timeouts, cognition defaults, and meta-reasoning thresholds/profiles.

### Fixed
- Upstream failure handling in cognition pipeline to reduce hard failures and improve fallback behavior.
- Memory node compatibility with PocketBase schema variations (including metadata/session handling).
- Audit/header consistency for orchestration and meta-reasoning observability.

### Notes
- `v0.1.0` is the first tagged baseline for enterprise-focused gateway capabilities.

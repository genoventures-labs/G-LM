# Changelog

All notable changes to this project are documented in this file.

## [0.1.3] - 2026-02-11

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

### Changed
- Reasoning pipelines (`tot`, `mcts`, `multi_agent`) now support tool-calling via a tool-aware upstream wrapper.
- Unified cognition normalization now propagates `tools` and `tool_choice`.
- `GLM_TOOL_CALLING_TIMEOUT_SECONDS` default updated to `60` (hard timeout ceiling for tool dispatch).

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

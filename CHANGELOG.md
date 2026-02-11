# Changelog

All notable changes to this project are documented in this file.

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

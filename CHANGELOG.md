# Changelog

All notable changes to this project are documented in this file.

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

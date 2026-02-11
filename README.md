# G-LM

[![Release](https://img.shields.io/badge/release-v0.1.6-0A66C2)](https://github.com/cassianwolfe/G-LM/releases)
[![Go](https://img.shields.io/badge/go-1.25%2B-00ADD8)](https://go.dev/)
[![API](https://img.shields.io/badge/api-OpenAI%20compatible-2B2D42)](#api-surface)
[![Deployment](https://img.shields.io/badge/deployment-enterprise%20ready-1F6FEB)](#deployment)

G-LM is an enterprise LLM gateway for production AI systems.

It provides a single OpenAI-compatible API layer in front of model backends, with policy enforcement, deterministic routing, reasoning orchestration, document synthesis, memory context, symbolic overlays, and external tool calling.

## Why G-LM

- Operational control: centralize routing, policy, auth, audit, and quotas in one gateway.
- Reliability by design: staged fallbacks, bounded execution, and explicit observability headers.
- Enterprise governance: tenant isolation, model allowlists, and auditable outcomes.
- Model portability: keep application contracts stable while changing model providers.

## Core Capabilities

- OpenAI-compatible runtime APIs (`/v1/chat/completions`, `/v1/models`, `/v1/cognition`).
- Deterministic `model: "auto"` orchestration with JIT inventory management.
- Reasoning modes: `tot`, `mcts`, `multi_agent` with fail-open handling.
- Document orchestration: chunking, summarization, cross-doc linking, synthesis context.
- Session state and memory dynamics for continuity across turns.
- Symbolic overlays (`assist` and `strict`) with compliance telemetry.
- Tool calling with external tool server support:
  - explicit `tools`
  - `tool_choice: "auto"` schema discovery via `/openapi.json`
  - per-tool timeout/retry policy

## Architecture

```text
Client Apps
  -> G-LM Gateway (this service)
    -> Upstream LLM Runtime (OpenWebUI/Ollama-compatible)
    -> PocketBase (tenant/auth/policy/audit/memory)
    -> External Tool Server (/tools/*)
```

## API Surface

Runtime endpoints:

- `POST /v1/chat/completions`
- `POST /v1/cognition`
- `GET /v1/models`

Operational endpoints:

- `GET /healthz`
- `GET /readyz`
- `GET /version`

Admin endpoints:

- `POST /admin/v1/tenants`
- `POST /admin/v1/tenants/{tenant_id}/keys`
- `POST /admin/v1/tenants/{tenant_id}/roles`
- `POST /admin/v1/tenants/{tenant_id}/model-policy`
- `POST /admin/v1/tenants/{tenant_id}/quotas`
- `GET /admin/v1/tenants/{tenant_id}/audit-events`
- `POST /admin/v1/orchestrator/debug`
- `GET /admin/v1/state/{session_id}`

## Quick Start

### 1) Configure environment

Set the minimum required runtime variables:

- `GLM_UPSTREAM_BASE_URL`
- `GLM_UPSTREAM_API_KEY`
- `GLM_POCKETBASE_URL`
- `GLM_POCKETBASE_IDENTITY`
- `GLM_POCKETBASE_PASSWORD`

Optional for tool calling:

- `GLM_TOOL_CALLING_ENABLED=true`
- `GLM_TOOL_SERVER_BASE_URL`
- `GLM_TOOL_SERVER_API_KEY`
- `GLM_TOOL_SERVER_CLIENT_ID`

Optional for reasoning self-evaluation weighting curve (global defaults, opt-in):

- `GLM_SELF_EVAL_CURVE_ENABLED=false`
- `GLM_SELF_EVAL_CURVE_LOW_MAX=0.60`
- `GLM_SELF_EVAL_CURVE_MID_MAX=0.82`
- `GLM_SELF_EVAL_CURVE_LOW_WEIGHT=0.90`
- `GLM_SELF_EVAL_CURVE_MID_WEIGHT=1.00`
- `GLM_SELF_EVAL_CURVE_HIGH_WEIGHT=1.08`
- `GLM_SELF_EVAL_CURVE_BIAS=0.00`

Optional for reasoning graph pruning (latency/cost oriented):

- `GLM_REASONING_PRUNING_ENABLED=true`
- `GLM_REASONING_PRUNING_MIN_SCORE=0.55`
- `GLM_REASONING_PRUNING_TOT_TOPK=3`
- `GLM_REASONING_PRUNING_TOT_SYNTH_TOPK=2`
- `GLM_REASONING_PRUNING_MCTS_POOL_TOPK=6`
- `GLM_REASONING_PRUNING_MCTS_SYNTH_TOPK=3`
- `GLM_REASONING_PRUNING_MA_ROUND_TOPK=4`
- `GLM_REASONING_PRUNING_MA_SYNTH_TOPK=3`

Optional for MCTS v2 quality mode (backward-compatible, opt-in):

- `GLM_MCTS_V2_ENABLED=false`
- `GLM_MCTS_EARLY_STOP_WINDOW=4`
- `GLM_MCTS_EARLY_STOP_DELTA=0.01`

Optional for meta reflection v2 (strict opt-in):

- `GLM_META_REFLECTION_ENABLED=false`
- `GLM_META_REFLECTION_MAX_PASSES=1`
- `GLM_META_REFLECTION_TRIGGER_DECISIONS=caution,reject`

Optional for memory-anchored reasoning v1 (strict opt-in):

- `GLM_MEMORY_ANCHORED_REASONING_ENABLED=false`
- `GLM_MEMORY_ANCHORED_REASONING_MAX_ANCHORS=3`
- `GLM_MEMORY_ANCHORED_REASONING_MIN_COVERAGE=0.34`
- `GLM_MEMORY_ANCHORED_REASONING_SCORE_BONUS=0.06`

Optional for symbolic supervision nodes v1 (strict opt-in):

- `GLM_SYMBOLIC_SUPERVISION_ENABLED=false`
- `GLM_SYMBOLIC_SUPERVISION_WARN_THRESHOLD=1`
- `GLM_SYMBOLIC_SUPERVISION_REJECT_THRESHOLD=3`
- `GLM_SYMBOLIC_SUPERVISION_AUTO_REVISE=true`
- `GLM_SYMBOLIC_SUPERVISION_MAX_PASSES=1`

### 2) Run

```bash
go run ./cmd/glm-api
```

### 3) Bootstrap admin key

```bash
go run ./cmd/glm-api bootstrap-admin-key --tenant-name acme --expires-hours 720
```

### 4) Validate runtime

```bash
go run ./cmd/glm-api doctor --strict
ADMIN_KEY='glm....' go run ./cmd/glm-api quick-smoke
```

## Tool Calling Integration

G-LM supports OpenAI-style tool calling and external tool execution.

- If request includes `tools`, G-LM uses provided definitions.
- If request sets `tool_choice: "auto"` and omits `tools`, G-LM fetches definitions from the configured tool server `/openapi.json`.
- Tool dispatch targets supported tool endpoints (`/tools/web_search`, `/tools/fetch_url`, `/tools/http_request`, `/tools/vector_retrieve`, `/tools/code_exec_sandbox`).

Reference integration contract:

- `docs/toolcall_info.md`

## Deployment

Typical enterprise deployment pattern:

- Run G-LM as stateless gateway instances behind an L7 load balancer.
- Use PocketBase for control-plane/state records.
- Route to upstream model runtime over private network.
- Route tool calls to a hardened tool server with per-client credentials.

Recommended hardening:

- Restrict admin APIs to private network or privileged ingress.
- Rotate API keys and enforce tenant/model allowlists.
- Enable structured audit retention and centralized log shipping.
- Keep strict egress policy on tool server paths.

## Observability

G-LM returns execution metadata via headers (routing, reasoning, memory, symbolic overlays, tool-calling). This enables request-level tracing without changing response schema.

Use `/admin/v1/tenants/{tenant_id}/audit-events` for governance and post-incident analysis.

## Release and Compatibility

- Current version: `v0.1.6`
- Contract style: OpenAI-compatible runtime surface
- Backward compatibility goal: additive evolution of request options and headers

Release note standard:

- Every GitHub release should include `Quick Read`, `Highlights`, and `Operational Notes` sections (not only the compare link).
- Use `/Users/mike/Desktop/testModel/docs/release_notes_template.md` as the baseline structure.
- Include the full compare link as a supporting link, not the only content.

Release helper command:

- `./scripts/release_notes.sh v0.1.5`:
  - auto-detects previous tag,
  - builds quick-read notes from the matching `CHANGELOG.md` section,
  - creates/edits the GitHub release notes in one command.
- Optional flags:
  - `--prev v0.1.4`
  - `--mode auto|create|edit`
  - `--dry-run`

## Repository Structure

- `/cmd/glm-api`: service entrypoint and operational CLI commands
- `/internal/http`: API server and orchestration pipeline
- `/internal/orchestrator`: model routing and JIT inventory logic
- `/internal/reasoning`: ToT, MCTS, and multi-agent execution
- `/internal/document`: document synthesis orchestration
- `/internal/toolcalling`: external tool server client and policies
- `/internal/store`: persistence adapters (including PocketBase)

## Production Positioning

G-LM is designed as an enterprise control layer for LLM operations, not a model host. It lets platform teams standardize security, observability, and policy while application teams consume a stable OpenAI-compatible API.

# G-LM V1 Gateway

Enterprise-oriented Go API gateway in front of hosted Ollama/OpenWebUI with PocketBase control plane.

## Run

```bash
go run ./cmd/glm-api
```

## Bootstrap first admin key

Use this once to mint your initial `glm.*` key:

```bash
go run ./cmd/glm-api bootstrap-admin-key --tenant-name acme --expires-hours 720
```

For an existing tenant:

```bash
go run ./cmd/glm-api bootstrap-admin-key --tenant-id TENANT_ID --expires-hours 720
```

Bootstrap now saves the generated key to a local session file (permissions `0600`) so CLI commands can reuse it until expiry.
Default session path: `~/.config/glm-api/session.json` (override with `GLM_SESSION_FILE`).

List tenants (to find valid `tenant_id` values):

```bash
go run ./cmd/glm-api list-tenants --limit 50
```

Run deployment diagnostics:

```bash
go run ./cmd/glm-api doctor
```

Fail CI/shell on any failed check:

```bash
go run ./cmd/glm-api doctor --strict
```

Run one-command smoke test (doctor + local health + runtime chat):

```bash
ADMIN_KEY='glm....' go run ./cmd/glm-api quick-smoke
```

If `--api-key` and `ADMIN_KEY` are both missing, `quick-smoke` will automatically use the saved session key when still valid.

Run V2 style-cognition evaluation (style contract + micro-switch + subtext assist checks):

```bash
go run ./cmd/glm-api eval-v2 --base-url http://localhost:8081
```

Optional flags:

```bash
go run ./cmd/glm-api quick-smoke --base-url http://localhost:8081 --model llama3.2:1b --api-key 'glm....'
```

To validate auto-routing behavior:

```bash
go run ./cmd/glm-api quick-smoke --model auto
```

For slower upstreams:

```bash
go run ./cmd/glm-api quick-smoke --timeout-seconds 120 --max-tokens 16
```

Note: bootstrap requires PocketBase service credentials (`GLM_POCKETBASE_IDENTITY`, `GLM_POCKETBASE_PASSWORD`) and writes directly to PocketBase. The app auto-loads `.env` for local runs.
If you intentionally run without PocketBase auth, set `GLM_POCKETBASE_ALLOW_UNAUTH=true` explicitly.

## Required environment variables

- `GLM_UPSTREAM_BASE_URL` (example: `https://your-openwebui-host`)
- `GLM_UPSTREAM_API_KEY`
- `GLM_COGNITION_DEFAULT_MODEL` (default: `llama3.2:1b`, used by `/v1/cognition` when `model` is omitted for non-chat tasks)
- `GLM_DEFAULT_MAX_TOKENS` (default: `128`, used when request omits `max_tokens`)
- `GLM_POCKETBASE_URL` (default: `https://pocketbase.thynaptic.com`)
- `GLM_POCKETBASE_AUTH_COLLECTION` (default: `service_accounts`)
- `GLM_POCKETBASE_IDENTITY`
- `GLM_POCKETBASE_PASSWORD`
- `GLM_POCKETBASE_ALLOW_UNAUTH` (default: `false`)
- `GLM_ORCHESTRATOR_ENABLED` (default: `true`)
- `GLM_ORCHESTRATOR_DEFAULT_MODEL` (default: `qwen3-8b-instruct-Q4_K_M`)
- `GLM_ORCHESTRATOR_DEFAULT_ALIASES` (comma-separated aliases)
- `GLM_ORCHESTRATOR_DEFAULT_FALLBACK` (default: `qwen3:4b`)
- `GLM_STATE_HISTORY_WINDOW` (default: `20`)
- `GLM_EMOTIONAL_MODULATION_ENABLED` (default: `true`)
- `GLM_REASONING_PIPELINE_ENABLED` (default: `true`)
- `GLM_REASONING_PIPELINE_DEFAULT_BRANCHES` (default: `3`)
- `GLM_REASONING_PIPELINE_MAX_BRANCHES` (default: `5`)
- `GLM_INTENT_PREPROCESSOR_ENABLED` (default: `true`)
- `GLM_INTENT_AMBIGUITY_THRESHOLD` (default: `0.62`)
- `GLM_DOCUMENT_ORCHESTRATION_ENABLED` (default: `true`)
- `GLM_DOCUMENT_CHUNK_SIZE` (default: `1200`)
- `GLM_DOCUMENT_MAX_DOCUMENTS` (default: `8`)
- `GLM_DOCUMENT_MAX_CHUNKS_PER_DOC` (default: `8`)
- `GLM_DOCUMENT_MAX_LINKS` (default: `12`)
- `GLM_MEMORY_DYNAMICS_ENABLED` (default: `true`)
- `GLM_MEMORY_HALF_LIFE_HOURS` (default: `168`)
- `GLM_MEMORY_REPLAY_THRESHOLD` (default: `0.68`)
- `GLM_MEMORY_FRESHNESS_WINDOW_HOURS` (default: `72`)
- `GLM_MEMORY_CONTEXT_NODE_LIMIT` (default: `5`)
- `GLM_MEMORY_UPDATE_CONCEPTS_PER_TURN` (default: `6`)
- `GLM_MEMORY_OP_TIMEOUT_SECONDS` (default: `2`)
- `GLM_REASONING_STAGE_TIMEOUT_SECONDS` (default: `60`)
- `GLM_DOCUMENT_STAGE_TIMEOUT_SECONDS` (default: `25`)
- `GLM_STYLE_CONTRACT_ENABLED` (default: `true`)
- `GLM_STYLE_CONTRACT_VERSION` (default: `v1`)
- `GLM_META_REASONING_ENABLED` (default: `true`)
- `GLM_META_REASONING_DEFAULT_PROFILE` (default: `default`)
- `GLM_META_REASONING_ACCEPT_THRESHOLD` (default: `0.72`)
- `GLM_META_REASONING_STRICT_THRESHOLD` (default: `0.82`)

If PocketBase credentials are missing and `GLM_POCKETBASE_ALLOW_UNAUTH=false`, startup/bootstrap will fail fast with a configuration error.

## Required service account scopes

`service_accounts.scopes` should include at least:

- `tenants:read`, `tenants:write`
- `api_keys:read`, `api_keys:write`
- `roles:write` (if using admin role endpoints)
- `model_policies:read`, `model_policies:write`
- `quotas:read`, `quotas:write`
- `idempotency:write` (and optionally `idempotency:read`)
- `audit:write` (and `audit:read` if listing audit events via admin API)
- `memory:write` (and `memory:read` for retrieval/replay)

## APIs

- Runtime: `POST /v1/chat/completions`, `GET /v1/models`
- Unified cognition runtime: `POST /v1/cognition`
- Admin: `POST /admin/v1/tenants`, `POST /admin/v1/tenants/{tenant_id}/keys`, `POST /admin/v1/tenants/{tenant_id}/roles`, `POST /admin/v1/tenants/{tenant_id}/model-policy`, `POST /admin/v1/tenants/{tenant_id}/quotas`, `GET /admin/v1/tenants/{tenant_id}/audit-events`
- Ops: `GET /healthz`, `GET /readyz`, `GET /version`
- Orchestrator debug (admin-scoped): `POST /admin/v1/orchestrator/debug`
- Session state inspect (admin-scoped): `GET /admin/v1/state/{session_id}`

`POST /v1/chat/completions` supports `model: "auto"` to trigger deterministic orchestrator selection.
Explicit non-auto `model` values are never overridden.
State manager supports sticky session context via request `session_id` or `X-Session-ID` header.
Reasoning pipeline can be enabled per request with `reasoning.mode = "tot"` (or `"auto"`), producing branch/evaluate/synthesis execution with contradiction checks.
Intent preprocessor runs deterministic normalization + ambiguity scoring + intent classification before model execution.
Document orchestration runs above model execution for multi-document chunking, hierarchical summaries, cross-document linking, and synthesis context injection.
Memory dynamics adds PB-backed memory nodes with Go-calculated forgetting/freshness/replay scoring for session continuity.

Example orchestrator debug request:

```bash
curl -s -X POST http://localhost:8081/admin/v1/orchestrator/debug \
  -H "Authorization: Bearer $ADMIN_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"auto","messages":[{"role":"user","content":"What is DNS?"}]}'
```

Example session state request:

```bash
curl -s -X GET http://localhost:8081/admin/v1/state/sess-123 \
  -H "Authorization: Bearer $ADMIN_KEY"
```

Example reasoning pipeline request:

```bash
curl -s -X POST http://localhost:8081/v1/chat/completions \
  -H "Authorization: Bearer $ADMIN_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"auto","reasoning":{"mode":"tot","branches":3},"messages":[{"role":"user","content":"Design a safe rollout plan and compare alternatives"}]}'
```

Example deterministic intent preprocessing (ambiguous input):

```bash
curl -i -s -X POST http://localhost:8081/v1/chat/completions \
  -H "Authorization: Bearer $ADMIN_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"mistral:7b","messages":[{"role":"user","content":"fix this"}]}'
```

Example document orchestration request:

```bash
curl -i -s -X POST http://localhost:8081/v1/chat/completions \
  -H "Authorization: Bearer $ADMIN_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model":"auto",
    "documents":[
      {"id":"doc-1","title":"Runbook","text":"Service rollout phases and incident procedures..."},
      {"id":"doc-2","title":"Policy","text":"Compliance controls, audit checkpoints, and rollback gates..."}
    ],
    "messages":[{"role":"user","content":"Synthesize a rollout approach across both docs"}]
  }'
```

Unified cognition request spec (single route for chat/reasoning/document/extraction tasks):

```json
{
  "task": "chat | reasoning | analysis | document_synthesis | extract | classification",
  "input": "optional user input",
  "model": "optional model or auto",
  "session_id": "optional sticky session id",
  "response_style": {
    "breathing_weight": 0.32,
    "tone_shift": "maintain | stabilize-calm | re-anchor-context",
    "style_adjustment": "balanced | concise-structured | expanded-guided",
    "pacing": "steady | fast | slow",
    "micro_switches": ["mood_shift","topic_drift","pacing_shift"],
    "mood_shift": 0.18,
    "topic_drift": 0.52,
    "subtext_detection": "model-driven",
    "rolling_sentiment": -0.24,
    "conversation_drift": 0.52,
    "risk_flags": ["negative_sentiment_trend","high_topic_drift"]
  },
  "messages": [{"role":"user","content":"optional, used instead of input when provided"}],
  "documents": [{"id":"doc-1","title":"Doc","text":"..."}],
  "reasoning": {"mode":"tot","branches":3,"meta_enabled":true,"meta_profile":"default"},
  "document_orchestration": {"mode":"hierarchical","chunk_size":1200,"max_documents":8},
  "temperature": 0.2,
  "max_tokens": 512,
  "stream": false
}
```

When omitted, gateway fills `response_style` deterministically from session cognitive state and emits headers:
`X-GLM-Response-Style`, `X-GLM-Breathing-Weight`, `X-GLM-Pacing`, `X-GLM-Micro-Switches`, `X-GLM-Risk-Flags`.
Subtext classification (sarcasm/vulnerability/fatigue) remains model-driven; gateway only supplies assist metrics (`rolling_sentiment`, `conversation_drift`, `risk_flags`).
Gateway also injects a versioned style contract prompt and exposes it via `X-GLM-Style-Contract`.
When `reasoning.meta_enabled=true`, gateway runs deterministic meta-reasoning and emits:
`X-GLM-Meta-Reasoning`, `X-GLM-Meta-Decision`, `X-GLM-Meta-Confidence`, `X-GLM-Meta-Risk-Score`, `X-GLM-Meta-Profile`.

Example unified cognition call:

```bash
curl -i -s -X POST http://localhost:8081/v1/cognition \
  -H "Authorization: Bearer $ADMIN_KEY" \
  -H "Content-Type: application/json" \
  -d '{"task":"reasoning","input":"Compare rollout options and select best","response_style":{"breathing_weight":0.32}}'
```

## Helm

Helm chart: `deploy/helm/glm-api`

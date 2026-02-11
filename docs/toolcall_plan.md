## G-LM Tool Server V1 Hard Contract (Remaining Decisions Locked)

### Summary
Define and freeze the remaining operational contract for the live tool server at `https://chat.thynaptic.com`:
1. `API_KEYS_FILE` schema + validation/rotation behavior  
2. PocketBase schema for `vector_retrieve`  
3. Per-tool timeout/retry/rate-limit defaults

This plan is intentionally strict so any implementer can ship without making further decisions.

### Scope
- In scope: contract/spec docs, server-side validation behavior, runtime defaults, observability and failure policy.
- Out of scope: changing currently published endpoint paths, auth headers, or OpenAPI route structure already documented in `/Users/mike/Desktop/testModel/docs/toolcall_info.md`.

---

## 1) `API_KEYS_FILE` Contract

### File format
- Path from env: `API_KEYS_FILE`
- Format: JSON object (not array), UTF-8

```json
{
  "version": 1,
  "defaults": {
    "rate_limit_rpm": 120,
    "max_concurrent_requests": 20
  },
  "clients": [
    {
      "client_id": "glm-prod-us",
      "status": "active",
      "keys": [
        {
          "key_id": "k_2026_01_a",
          "key_hash": "sha256:HEX_DIGEST",
          "created_at": "2026-02-11T00:00:00Z",
          "expires_at": null
        }
      ],
      "allowed_tools": [
        "web_search",
        "fetch_url",
        "http_request",
        "vector_retrieve",
        "code_exec_sandbox"
      ],
      "rate_limit_rpm": 180,
      "max_concurrent_requests": 30,
      "http_allowlist_profile": "default_public",
      "vector_namespaces": ["docs", "kb", "runbooks"]
    }
  ]
}
```

### Validation rules
- `version` required, only `1` accepted.
- `client_id` required, unique, case-sensitive.
- `status` in `active|disabled`.
- At least one `keys[]` entry for active clients.
- `key_hash` format required: `sha256:<64-lower-hex>`.
- `allowed_tools` subset of known tools only.
- Unknown fields rejected at load-time (strict decode).
- Startup behavior:
  - Fail-fast if file missing/invalid.
  - Log structured reason with line/context.
- Hot reload:
  - Optional in v1; if enabled, atomic swap and reject invalid reload.

### Auth behavior
- Request must include both headers:
  - `Authorization: Bearer <raw_key>`
  - `X-Client-Id: <client_id>`
- Server verifies:
  - `client_id` exists and active.
  - Bearer raw key hashes to any active unexpired key for that client.
  - Requested tool is in `allowed_tools`.
- Failure codes:
  - Missing/invalid auth: `401`
  - Valid auth but disallowed tool/profile/namespace: `403`

### Key rotation
- Support overlapping valid keys per client (`keys[]` length > 1).
- Rotation flow:
  1. Add new key hash.
  2. Deploy clients with new raw key.
  3. Remove old key hash.
- Expired keys rejected immediately.

---

## 2) PocketBase Schema for `vector_retrieve`

### Collection naming
- Collection for retrieval:
  - `<VECTOR_COLLECTION_PREFIX>_<namespace>`
  - Example: `glm_docs`
- `namespace` input must match regex: `^[a-z0-9][a-z0-9_-]{1,63}$`

### Required collection fields
For each chunk record:
- `embedding` (JSON array of float64) required
- `text` (string) required
- `source` (string) required
- `metadata` (JSON object) optional, default `{}`

### Optional compatibility aliases
For backward compatibility, retrieval can read:
- embedding: `embedding` or `vector`
- text: `text` or `content` or `chunk`
- source: `source` or `url` or `path`
- metadata: `metadata` (object) plus merged non-system fields

### Retrieval algorithm
1. Validate input (`query`, `namespace`, `top_k`, `filters?`).
2. Compute query embedding via configured Ollama OpenAI-compatible embeddings endpoint.
3. Load candidate records from namespace collection.
4. Apply best-effort equality filters first (`filters` top-level key/value exact matches against metadata and known fields).
5. Compute cosine similarity per candidate.
6. Sort descending by score.
7. Return top `k` matches.

### Output contract (unchanged)
- `matches[]`:
  - `id` string
  - `score` float64 (cosine similarity)
  - `text` string
  - `source` string
  - `metadata` object

### Safety/performance defaults
- `top_k` min `1`, max `50`, default `8`.
- Candidate scan cap per request: `5000` records.
- Embedding dimensionality mismatch:
  - Skip mismatched records.
  - If all mismatched, return empty `matches` (200), not 500.

---

## 3) Tool Runtime Defaults (Timeouts, Retries, Rate Limits)

### Global defaults
- Request timeout ceiling (hard): `60s`
- Default client RPM: `120`
- Burst behavior: token bucket with burst `30`
- Max concurrent per client default: `20`

### Per-tool timeout defaults
- `web_search`: `12s`
- `fetch_url`: `15s`
- `http_request`: `20s`
- `vector_retrieve`: `20s`
- `code_exec_sandbox`: `45s` (container run timeout independent but <= request timeout ceiling with server override handling)

### Retry policy
- `web_search`: up to 2 retries on network/5xx, jittered backoff
- `fetch_url`: up to 1 retry on network/5xx, no retry on 4xx
- `vector_retrieve`: up to 1 retry only for embedding-provider transient failure (429/5xx)
- `http_request`: no automatic retries (unsafe by default)
- `code_exec_sandbox`: no retries

### Rate limiting policy
- Enforced by `client_id`.
- Optional per-tool multipliers:
  - `code_exec_sandbox`: 0.25x (heavier)
  - others: 1x
- On limit exceeded: `429` Problem Details response with retry hint.

---

## 4) Error Model and Observability Hardening

### Problem Details contract
All non-2xx return:
- `application/problem+json`
- Fields:
  - `status` (int)
  - `title` (string)
  - `detail` (string)
  - `trace_id` (string)
  - `errors` (optional array of field errors)

### Required headers
- Response includes `X-Trace-Id` for every request.
- Authenticated requests include normalized `X-Client-Id` echo.

### Audit events (structured logs/records)
Minimum fields:
- `trace_id`, `client_id`, `tool_name`, `status_code`, `latency_ms`, `result_size_bytes`, `error_code?`
- For `vector_retrieve`: `namespace`, `top_k`, `candidate_count`, `match_count`
- For sandbox: `language`, `timeout`, `exit_code`, `timed_out`

---

## 5) Public API / OpenAPI Additions

### Add/update schemas
- `AuthError`, `ValidationError`, `RateLimitError` under Problem Details style.
- `VectorRetrieveInput` constraints:
  - `query`: min length 1
  - `namespace`: regex
  - `top_k`: 1..50
  - `filters`: additionalProperties allowed (primitive values only)

### Security scheme
- OpenAPI `securitySchemes`:
  - `bearerAuth` (HTTP bearer)
  - explicit required header param for `X-Client-Id` on `/tools/*`
- Keep discovery routes unauthenticated.

---

## 6) Test Cases and Scenarios

### Auth + policy tests
1. Missing bearer -> `401`
2. Missing `X-Client-Id` -> `401`
3. Key valid but wrong client -> `401`
4. Client disabled -> `401`
5. Tool not in allowlist -> `403`
6. Namespace not allowed for client -> `403`

### API keys file tests
1. Invalid JSON -> startup fail
2. Unknown fields -> startup fail
3. Duplicate client_id -> startup fail
4. Expired key rejected
5. Rotation overlap accepts both keys

### Vector retrieval tests
1. Happy path with exact schema
2. Alias-field fallback path
3. Filter equality path
4. Dimension mismatch records skipped
5. Empty collection returns empty matches
6. top_k clamp behavior

### Timeout/retry tests
1. `web_search` retries and succeeds
2. `http_request` no retry on transient 5xx
3. `code_exec_sandbox` timeout returns deterministic error payload
4. rate-limit `429` with retry hint

### End-to-end contract tests
1. OpenAPI includes all endpoints/schemas/security
2. `/docs` loads and references live spec
3. Problem+json shape consistent across all error paths

---

## 7) Rollout Plan

1. Implement contract and tests behind feature branch.
2. Deploy to VPS with dual key support pre-loaded.
3. Validate with smoke suite against live domain routes.
4. Cut over clients incrementally by `client_id`.
5. Remove deprecated key hashes after verification window.
6. Enable stricter namespace allowlists after first stable week.

---

## Explicit Assumptions and Defaults
- `API_KEYS_FILE` is local JSON on server disk and is authoritative for auth.
- Hashing algorithm is SHA-256 with `sha256:<hex>` encoding.
- `vector_retrieve` remains PocketBase-backed; no Qdrant compatibility required.
- Embeddings are generated server-side through Ollama-compatible endpoint.
- `http_request` remains default-deny with allowlist profiles.
- No mutating outbound calls are retried automatically.
- Discovery endpoints stay public by design.

# Tool Server V1 Test Matrix

## Auth + Policy

1. Missing bearer header returns `401` ProblemError.
2. Missing `X-Client-Id` returns `401` ProblemError.
3. Valid key with wrong client id returns `401`.
4. Disabled client returns `401`.
5. Tool not present in `allowed_tools` returns `403`.
6. `vector_retrieve` namespace not in `vector_namespaces` returns `403`.

## API_KEYS_FILE Parsing

1. Invalid JSON -> startup fail.
2. Unknown properties -> startup fail (strict decoding).
3. Duplicate `client_id` -> startup fail.
4. Expired key is rejected.
5. Overlapping keys for rotation are accepted.

## vector_retrieve

1. Canonical field names path works (`embedding/text/source/metadata`).
2. Alias fallback works (`vector/content/url`).
3. `filters` equality path works for metadata and top-level mapped fields.
4. Dimension mismatch records are skipped.
5. Empty candidates returns `200` with `matches=[]`.
6. `top_k` clamp: defaults to 8, max 50.

## Retries/Timeouts

1. `web_search` retries twice on transient failures.
2. `fetch_url` retries once on network/5xx and not on 4xx.
3. `http_request` never retries.
4. `vector_retrieve` retries only embedding backend 429/5xx.
5. `code_exec_sandbox` timeout path is deterministic and non-retried.

## Rate Limit and Concurrency

1. Per-client token bucket limits enforced (120 rpm default, burst 30).
2. `code_exec_sandbox` weighted limit behavior works as configured.
3. Over-limit response is `429` ProblemError with retry hint.

## Contract/E2E

1. `/openapi.json` includes security scheme + `X-Client-Id` required on `/tools/*`.
2. `/docs` renders from live OpenAPI.
3. All non-2xx responses use `application/problem+json` and include `trace_id`.
4. `X-Trace-Id` is present on all responses.
5. Authenticated responses echo normalized `X-Client-Id`.

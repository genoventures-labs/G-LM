# Tool Server Runtime Defaults (V1)

## Global Defaults

- Hard request timeout ceiling: `60s`
- Default client rate limit: `120 RPM`
- Token bucket burst: `30`
- Max concurrent requests per client: `20`

## Per-Tool Timeouts

- `web_search`: `12s`
- `fetch_url`: `15s`
- `http_request`: `20s`
- `vector_retrieve`: `20s`
- `code_exec_sandbox`: `45s`

## Retry Policy

- `web_search`: up to 2 retries on network error or upstream 5xx, jittered backoff
- `fetch_url`: up to 1 retry on network error or upstream 5xx; no retry on 4xx
- `vector_retrieve`: up to 1 retry only for embedding backend transient (`429`/`5xx`)
- `http_request`: no retries
- `code_exec_sandbox`: no retries

## Rate Limit Weighting

- `code_exec_sandbox`: weight `0.25x` (heavier)
- all other tools: `1x`

## Rate-Limit Error

When exceeded:

- Status `429`
- Content type `application/problem+json`
- Include `trace_id`
- Include retry hint (`Retry-After` or equivalent field)

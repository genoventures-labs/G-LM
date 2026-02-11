# G-LM Tool Server V1 Contract Pack

This folder is the implementation handoff for the locked contract described in `/docs/toolcall_plan.md`.

## Files

- `api_keys.schema.json`: strict JSON schema for `API_KEYS_FILE`.
- `api_keys.sample.json`: rotation-ready sample config.
- `pocketbase_vector_contract.md`: PocketBase and retrieval behavior contract.
- `runtime_defaults.md`: timeout/retry/rate-limit defaults.
- `openapi_contract_patch.yaml`: OpenAPI patch requirements (security + schema constraints).
- `test_matrix.md`: required validation scenarios.

## Validation Workflow

1. Validate `API_KEYS_FILE` against `api_keys.schema.json`.
2. Ensure service enforces behavior in `pocketbase_vector_contract.md` and `runtime_defaults.md`.
3. Compare generated OpenAPI with `openapi_contract_patch.yaml` requirements.
4. Run/automate `test_matrix.md` before rollout.

## Rollout Reminder

- Support overlapping keys during rotation.
- Keep `/openapi.json`, `/docs`, `/healthz`, `/version` public.
- Keep `/tools/*` protected by both `Authorization` and `X-Client-Id`.

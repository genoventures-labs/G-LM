# PocketBase Handoff for G-LM V1

Base URL: `https://pocketbase.thynaptic.com`
Health endpoint: `/api/health`

## Service Authentication
- Create auth collection: `service_accounts` (or equivalent approved auth collection).
- Create service user for G-LM runtime.
- Apply least-privilege API rules: only required CRUD on listed collections.

## Collections
- `tenants`
  - `name` (text)
  - `status` (text, default `active`)
- `projects`
  - `tenant_id` (relation -> tenants)
  - `name`, `status`
- `api_keys`
  - `tenant_id` (relation -> tenants)
  - `prefix` (text, indexed)
  - `hash` (text)
  - `scopes` (json/text array)
  - `status` (text: active/revoked)
  - `expires_at` (date, nullable)
- `roles`
  - `tenant_id` (relation -> tenants)
  - `name` (text)
  - `permissions` (json/text array)
- `memberships`
  - `tenant_id` (relation -> tenants)
  - `principal_id` (text)
  - `role_id` (relation -> roles)
- `model_policies`
  - `tenant_id` (relation -> tenants, unique)
  - `allowed_models` (json/text array)
  - `primary_model` (text)
  - `fallback_model` (text, nullable)
  - `reasoning_visible` (bool, default false)
- `quotas`
  - `tenant_id` (relation -> tenants, unique)
  - `rpm_limit` (number)
  - `tpm_limit` (number)
  - `burst` (number)
- `idempotency_records`
  - `tenant_id` (relation -> tenants)
  - `idempotency_key` (text)
  - `request_hash` (text)
  - `response_hash` (text)
  - `status` (text)
  - `expires_at` (date)
- `audit_events`
  - `tenant_id` (relation -> tenants)
  - `actor_type` (text)
  - `actor_id` (text)
  - `endpoint` (text)
  - `model` (text)
  - `outcome` (text)
  - `latency_ms` (number)
  - `trace_id` (text)
  - `timestamp` (date)

## Rules
- No public writes.
- Tenant-scoped read/write rules on all tenant collections.
- Service account has only the minimum collection access required.

## Retention
- Default retention for `audit_events` and `idempotency_records`: 90 days.
- Implement scheduled cleanup job in operational automation.

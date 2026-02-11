# G-LM PocketBase Bootstrap Handoff

## Context
- Project: G-LM V1 (Go API gateway)
- Gateway workspace: `/Users/mike/Desktop/testModel`
- PocketBase URL: `https://pocketbase.thynaptic.com`
- Current state:
  - PocketBase service auth collection is configured: `service_accounts`
  - Bootstrap and runtime should use authenticated PocketBase mode by default

## Default Operating Mode
Use authenticated PocketBase service account access.

## Required Environment
Set in `/Users/mike/Desktop/testModel/.env`:

```env
GLM_POCKETBASE_URL=https://pocketbase.thynaptic.com
GLM_POCKETBASE_AUTH_COLLECTION=service_accounts
GLM_POCKETBASE_IDENTITY=<service_identity>
GLM_POCKETBASE_PASSWORD=<service_password>
GLM_POCKETBASE_ALLOW_UNAUTH=false
```

## Verify Tenant Exists
Use PocketBase API (authenticated service token):

```bash
SERVICE_TOKEN=$(curl -s -X POST \
  https://pocketbase.thynaptic.com/api/collections/service_accounts/auth-with-password \
  -H "Content-Type: application/json" \
  -d '{"identity":"<service_identity>","password":"<service_password>"}' | jq -r '.token')

curl -s -H "Authorization: Bearer $SERVICE_TOKEN" \
  "https://pocketbase.thynaptic.com/api/collections/tenants/records?perPage=50"
```

Confirm at least one tenant record is returned and copy its record `id`.

## Generate First G-LM Admin Key
Run from gateway repo:

```bash
cd /Users/mike/Desktop/testModel
go run ./cmd/glm-api bootstrap-admin-key --tenant-id REAL_TENANT_ID --expires-hours 720
```

Expected output includes:
- `api_key` (`glm.*`)
- `tenant_id`
- `key_id`
- `scopes`
- `expires_at`

Store `api_key` securely (it is shown once).

## Post-Bootstrap
- Keep control-plane writes non-public.
- Keep service credentials in secret storage and rotate periodically.

## Emergency-Only Unauthenticated Mode
If you must run without service auth temporarily, set:

```env
GLM_POCKETBASE_ALLOW_UNAUTH=true
```

Only use this for short-lived bootstrap/debug windows.

## Quick Validation After Bootstrap
1. Start API service:

```bash
go run ./cmd/glm-api
```

2. Use generated admin key:

```bash
export ADMIN_KEY='glm.xxxxx.xxxxx'

curl -s -X POST http://localhost:8081/admin/v1/tenants \
  -H "Authorization: Bearer $ADMIN_KEY" \
  -H "Content-Type: application/json" \
  -d '{"name":"tenant-smoke"}'
```

If this returns `201`, key bootstrap and auth path are functioning.

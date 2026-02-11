# Service Accounts Collection — G-LM V1

**Purpose**: Dedicated authentication collection for service-to-service communication  
**Collection**: `service_accounts` (auth type)

---

## Schema

### Auth Fields (Built-in)
- `id` - Unique identifier
- `email` - Service email (required, unique)
- `password` - Service password (min 15 chars)
- `emailVisibility` - Show email in responses
- `verified` - Email verification status
- `created` - Creation timestamp
- `updated` - Last update timestamp

### Custom Fields

| Field | Type | Required | Description |
|:---|:---|:---:|:---|
| `service_name` | Text | ✅ | Service identifier (e.g., "glm-api") |
| `description` | Text | - | What this service does |
| `scopes` | JSON | - | Permissions array |
| `status` | Select | ✅ | active/inactive/suspended |
| `last_used` | Date | - | Last authentication timestamp |
| `tenant_id` | Relation | - | Associated tenant (for multi-tenancy) |
| `rate_limit_rpm` | Number | - | Rate limit (requests per minute) |

---

## API Rules

| Operation | Rule |
|:---|:---|
| **List** | Commander or Operator only |
| **View** | Commander or Operator only |
| **Create** | Commander only |
| **Update** | Commander only |
| **Delete** | Commander only |
| **Manage** | Commander only |

---

## Indexes

```sql
CREATE INDEX idx_service_accounts_service_name ON service_accounts (service_name);
CREATE INDEX idx_service_accounts_status ON service_accounts (status);
CREATE INDEX idx_service_accounts_tenant ON service_accounts (tenant_id);
```

---

## Creating a Service Account

### Method 1: Automated Script

```bash
bash /tmp/create-glm-service-account.sh
```

This will:
1. Generate secure password
2. Prompt for commander credentials
3. Create service account via API
4. Output .env configuration

### Method 2: Manual via Admin UI

1. Go to: https://pocketbase.thynaptic.com/_/
2. Navigate to: Collections → service_accounts
3. Click "New record"
4. Fill in:
   - **Email**: `glm-service@internal.local`
   - **Password**: (generate strong 15+ char password)
   - **Service Name**: `glm-api`
   - **Description**: `G-LM API Gateway Service`
   - **Scopes**: `["tenants:read", "tenants:write", "api_keys:read", "api_keys:write", "audit:write"]`
   - **Status**: `active`
   - **Tenant ID**: `04e4a329-4564-4a0f-8d4e-dbd08a5fcbd7`
5. Click "Create"

### Method 3: API with Commander Token

```bash
# 1. Authenticate as commander
COMMANDER_TOKEN=$(curl -s -X POST \
  https://pocketbase.thynaptic.com/api/collections/users/auth-with-password \
  -H "Content-Type: application/json" \
  -d '{"identity":"commander@example.com","password":"YOUR_PASSWORD"}' | \
  jq -r '.token')

# 2. Create service account
curl -X POST https://pocketbase.thynaptic.com/api/collections/service_accounts/records \
  -H "Authorization: Bearer $COMMANDER_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "email": "glm-service@internal.local",
    "password": "YOUR_SECURE_PASSWORD",
    "passwordConfirm": "YOUR_SECURE_PASSWORD",
    "service_name": "glm-api",
    "description": "G-LM API Gateway Service",
    "scopes": ["tenants:read", "tenants:write", "api_keys:read", "api_keys:write", "audit:write"],
    "status": "active",
    "tenant_id": "04e4a329-4564-4a0f-8d4e-dbd08a5fcbd7"
  }'
```

---

## Authenticating as Service Account

### Get Service Token

```bash
SERVICE_TOKEN=$(curl -s -X POST \
  https://pocketbase.thynaptic.com/api/collections/service_accounts/auth-with-password \
  -H "Content-Type: application/json" \
  -d '{
    "identity": "glm-service@internal.local",
    "password": "YOUR_SERVICE_PASSWORD"
  }' | jq -r '.token')

echo "Service token: $SERVICE_TOKEN"
```

### Use Service Token

```bash
# Example: Create tenant
curl -X POST https://pocketbase.thynaptic.com/api/collections/tenants/records \
  -H "Authorization: Bearer $SERVICE_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name": "New Tenant", "status": "active"}'

# Example: Create API key
curl -X POST https://pocketbase.thynaptic.com/api/collections/api_keys/records \
  -H "Authorization: Bearer $SERVICE_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "tenant_id": "04e4a329-4564-4a0f-8d4e-dbd08a5fcbd7",
    "prefix": "sk_live_",
    "hash": "hashed_key",
    "scopes": ["read", "write"],
    "status": "active"
  }'
```

---

## G-LM Configuration

### Environment Variables

```bash
# In /Users/mike/Desktop/testModel/.env
GLM_POCKETBASE_URL=https://pocketbase.thynaptic.com
GLM_POCKETBASE_AUTH_COLLECTION=service_accounts
GLM_POCKETBASE_IDENTITY=glm-service@internal.local
GLM_POCKETBASE_PASSWORD=your_secure_password_here
```

### Go Code Example

```go
package main

import (
    "github.com/pocketbase/pocketbase"
)

type PocketBaseClient struct {
    client *pocketbase.Client
}

func NewPocketBaseClient() (*PocketBaseClient, error) {
    client := pocketbase.NewClient(
        os.Getenv("GLM_POCKETBASE_URL"),
    )
    
    // Authenticate as service account
    _, err := client.Collection("service_accounts").AuthWithPassword(
        os.Getenv("GLM_POCKETBASE_IDENTITY"),
        os.Getenv("GLM_POCKETBASE_PASSWORD"),
    )
    if err != nil {
        return nil, fmt.Errorf("auth failed: %w", err)
    }
    
    return &PocketBaseClient{client: client}, nil
}

func (c *PocketBaseClient) CreateTenant(name string) error {
    _, err := c.client.Collection("tenants").Create(map[string]any{
        "name":   name,
        "status": "active",
    })
    return err
}
```

---

## Recommended Scopes

### G-LM Service Scopes

```json
[
  "tenants:read",
  "tenants:write",
  "projects:read",
  "projects:write",
  "api_keys:read",
  "api_keys:write",
  "roles:read",
  "memberships:read",
  "memberships:write",
  "model_policies:read",
  "model_policies:write",
  "quotas:read",
  "quotas:write",
  "idempotency:write",
  "audit:write"
]
```

### Read-Only Service

```json
[
  "tenants:read",
  "projects:read",
  "api_keys:read",
  "audit:read"
]
```

### Monitoring Service

```json
[
  "audit:read",
  "quotas:read",
  "tenants:read"
]
```

---

## Security Best Practices

### Password Requirements
- ✅ Minimum 15 characters (enforced)
- ✅ Use random password generator
- ✅ Store in secure vault (e.g., 1Password, HashiCorp Vault)
- ✅ Never commit to source code
- ✅ Rotate quarterly

### Token Management
- ✅ Tokens expire (configure in PocketBase settings)
- ✅ Re-authenticate on token expiration
- ✅ Store tokens in memory, not disk
- ✅ Use environment variables for credentials

### Access Control
- ✅ Service accounts are Commander-managed only
- ✅ Regular users cannot view/modify service accounts
- ✅ Operators can list/view but not create/modify
- ✅ Audit all service account operations

### Rate Limiting
- ✅ Set `rate_limit_rpm` for each service
- ✅ Monitor via `last_used` timestamp
- ✅ Alert on suspicious activity patterns

---

## Monitoring

### Track Service Usage

```bash
# Get all service accounts
curl -H "Authorization: Bearer OPERATOR_TOKEN" \
  "https://pocketbase.thynaptic.com/api/collections/service_accounts/records"

# Filter active services
curl -H "Authorization: Bearer OPERATOR_TOKEN" \
  "https://pocketbase.thynaptic.com/api/collections/service_accounts/records?filter=status='active'"

# Check last used
curl -H "Authorization: Bearer OPERATOR_TOKEN" \
  "https://pocketbase.thynaptic.com/api/collections/service_accounts/records?sort=-last_used"
```

### Update Last Used (in your Go code)

```go
func (c *PocketBaseClient) updateLastUsed() error {
    // Update last_used timestamp after each auth
    return c.client.Collection("service_accounts").Update(
        c.serviceAccountID,
        map[string]any{
            "last_used": time.Now().Format(time.RFC3339),
        },
    )
}
```

---

## Troubleshooting

### Authentication Failed
- Verify email and password are correct
- Check service account status is "active"
- Ensure using correct collection name: `service_accounts`

### Permission Denied
- Verify scopes include required permissions
- Check if service account is suspended
- Ensure tenant_id matches if tenant-scoped

### Token Expired
- Re-authenticate to get fresh token
- Implement automatic token refresh in your code


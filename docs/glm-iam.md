# G-LM Account Management — PocketBase Integration Handoff

**Purpose**: Enable G-LM API to create and manage PocketBase users and service accounts  
**Target**: G-LM Development Team  
**Gateway Repo**: `/Users/mike/Desktop/testModel`

---

## Overview

Your G-LM API should expose admin endpoints to manage:
1. **Service Accounts** (for service-to-service auth)
2. **Users** (for human administrators: commander/operator/analyst)
3. **Tenants** (already supported, expand if needed)

This eliminates the need for manual Admin UI management in production.

---

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                      G-LM Admin API                         │
│                   (http://localhost:8081)                   │
├─────────────────────────────────────────────────────────────┤
│  POST /admin/v1/service-accounts                            │
│  GET  /admin/v1/service-accounts                            │
│  GET  /admin/v1/service-accounts/:id                        │
│  PATCH /admin/v1/service-accounts/:id                       │
│  DELETE /admin/v1/service-accounts/:id                      │
│                                                             │
│  POST /admin/v1/users                                       │
│  GET  /admin/v1/users                                       │
│  GET  /admin/v1/users/:id                                   │
│  PATCH /admin/v1/users/:id                                  │
│  DELETE /admin/v1/users/:id                                 │
└─────────────────────────────────────────────────────────────┘
                            │
                            │ PocketBase SDK
                            ▼
┌─────────────────────────────────────────────────────────────┐
│                     PocketBase API                          │
│            https://pocketbase.thynaptic.com                 │
├─────────────────────────────────────────────────────────────┤
│  Collections:                                               │
│    • service_accounts (auth)                                │
│    • users (auth)                                           │
│    • tenants (base)                                         │
└─────────────────────────────────────────────────────────────┘
```

---

## Authentication Strategy

### Option 1: Admin Token (Recommended for Initial Setup)

PocketBase admin accounts have full access to all collections.

**Setup**:
1. Create admin token via PocketBase Admin UI
2. Store in environment variable
3. Use for G-LM administrative operations

```bash
# In .env
GLM_POCKETBASE_ADMIN_EMAIL=admin@thynaptic.com
GLM_POCKETBASE_ADMIN_PASSWORD=secure_admin_password
```

**Go Code**:
```go
// internal/pocketbase/admin_client.go
package pocketbase

import (
    "fmt"
    "os"
    pb "github.com/pocketbase/pocketbase"
)

type AdminClient struct {
    client *pb.Client
}

func NewAdminClient() (*AdminClient, error) {
    client := pb.NewClient(os.Getenv("GLM_POCKETBASE_URL"))
    
    // Authenticate as admin
    _, err := client.Admins().AuthWithPassword(
        os.Getenv("GLM_POCKETBASE_ADMIN_EMAIL"),
        os.Getenv("GLM_POCKETBASE_ADMIN_PASSWORD"),
    )
    if err != nil {
        return nil, fmt.Errorf("admin auth failed: %w", err)
    }
    
    return &AdminClient{client: client}, nil
}
```

### Option 2: Privileged Service Account

Create a special service account with elevated permissions for user management.

**Setup**:
1. Create service account: `glm-admin@internal.local`
2. Grant scopes: `["users:admin", "service_accounts:admin"]`
3. Update API rules to allow this service account

**API Rule Update Required**:
```javascript
// Allow glm-admin service account to manage users
createRule = "@request.auth.role = 'commander' || @request.auth.email = 'glm-admin@internal.local'"
```

---

## Endpoint Specifications

### 1. Service Account Management

#### POST /admin/v1/service-accounts
Create a new service account.

**Request**:
```json
{
  "email": "new-service@internal.local",
  "password": "auto-generated-secure-password",
  "service_name": "analytics-service",
  "description": "Analytics and reporting service",
  "scopes": ["audit:read", "tenants:read"],
  "status": "active",
  "tenant_id": "04e4a329-4564-4a0f-8d4e-dbd08a5fcbd7",
  "rate_limit_rpm": 100
}
```

**Response** (201 Created):
```json
{
  "id": "svc_abc123xyz",
  "email": "new-service@internal.local",
  "service_name": "analytics-service",
  "status": "active",
  "created": "2026-02-10T05:32:00Z",
  "credentials": {
    "email": "new-service@internal.local",
    "password": "auto-generated-secure-password"
  }
}
```

**Implementation**:
```go
// internal/handlers/admin/service_accounts.go
package admin

import (
    "crypto/rand"
    "encoding/hex"
    "github.com/gin-gonic/gin"
    "net/http"
)

type CreateServiceAccountRequest struct {
    Email         string   `json:"email" binding:"required,email"`
    Password      string   `json:"password"`
    ServiceName   string   `json:"service_name" binding:"required"`
    Description   string   `json:"description"`
    Scopes        []string `json:"scopes"`
    Status        string   `json:"status" binding:"required,oneof=active inactive suspended"`
    TenantID      string   `json:"tenant_id"`
    RateLimitRPM  int      `json:"rate_limit_rpm"`
}

func (h *AdminHandler) CreateServiceAccount(c *gin.Context) {
    var req CreateServiceAccountRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
        return
    }
    
    // Auto-generate secure password if not provided
    if req.Password == "" {
        req.Password = generateSecurePassword(32)
    }
    
    // Create in PocketBase
    record, err := h.pbAdmin.Collection("service_accounts").Create(map[string]any{
        "email":           req.Email,
        "password":        req.Password,
        "passwordConfirm": req.Password,
        "service_name":    req.ServiceName,
        "description":     req.Description,
        "scopes":          req.Scopes,
        "status":          req.Status,
        "tenant_id":       req.TenantID,
        "rate_limit_rpm":  req.RateLimitRPM,
        "verified":        true,
    })
    
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create service account"})
        return
    }
    
    c.JSON(http.StatusCreated, gin.H{
        "id":          record.Id,
        "email":       record.GetString("email"),
        "service_name": record.GetString("service_name"),
        "status":      record.GetString("status"),
        "created":     record.GetDateTime("created"),
        "credentials": gin.H{
            "email":    req.Email,
            "password": req.Password,
        },
    })
}

func generateSecurePassword(length int) string {
    bytes := make([]byte, length)
    rand.Read(bytes)
    return hex.EncodeToString(bytes)[:length]
}
```

#### GET /admin/v1/service-accounts
List all service accounts with pagination and filtering.

**Query Parameters**:
- `page` (default: 1)
- `per_page` (default: 20, max: 100)
- `status` (filter: active/inactive/suspended)
- `tenant_id` (filter by tenant)

**Response**:
```json
{
  "items": [
    {
      "id": "svc_abc123",
      "email": "glm-service@internal.local",
      "service_name": "glm-api",
      "status": "active",
      "last_used": "2026-02-10T05:30:00Z",
      "tenant_id": "04e4a329-4564-4a0f-8d4e-dbd08a5fcbd7"
    }
  ],
  "page": 1,
  "per_page": 20,
  "total_items": 1,
  "total_pages": 1
}
```

#### GET /admin/v1/service-accounts/:id
Get a specific service account.

#### PATCH /admin/v1/service-accounts/:id
Update service account (status, scopes, rate limit, etc.).

**Request**:
```json
{
  "status": "suspended",
  "rate_limit_rpm": 50
}
```

#### DELETE /admin/v1/service-accounts/:id
Delete a service account (soft delete recommended).

---

### 2. User Management

#### POST /admin/v1/users
Create a new user account (commander/operator/analyst).

**Request**:
```json
{
  "email": "john.doe@thynaptic.com",
  "password": "secure-password-123",
  "username": "johndoe",
  "role": "operator",
  "name": "John Doe",
  "verified": true
}
```

**Response** (201 Created):
```json
{
  "id": "usr_xyz789",
  "email": "john.doe@thynaptic.com",
  "username": "johndoe",
  "role": "operator",
  "name": "John Doe",
  "created": "2026-02-10T05:32:00Z",
  "credentials": {
    "email": "john.doe@thynaptic.com",
    "password": "secure-password-123"
  }
}
```

**Implementation**:
```go
type CreateUserRequest struct {
    Email    string `json:"email" binding:"required,email"`
    Password string `json:"password" binding:"required,min=8"`
    Username string `json:"username" binding:"required"`
    Role     string `json:"role" binding:"required,oneof=commander operator analyst"`
    Name     string `json:"name"`
    Verified bool   `json:"verified"`
}

func (h *AdminHandler) CreateUser(c *gin.Context) {
    var req CreateUserRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
        return
    }
    
    record, err := h.pbAdmin.Collection("users").Create(map[string]any{
        "email":           req.Email,
        "password":        req.Password,
        "passwordConfirm": req.Password,
        "username":        req.Username,
        "role":            req.Role,
        "name":            req.Name,
        "verified":        req.Verified,
    })
    
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create user"})
        return
    }
    
    c.JSON(http.StatusCreated, gin.H{
        "id":       record.Id,
        "email":    record.GetString("email"),
        "username": record.GetString("username"),
        "role":     record.GetString("role"),
        "created":  record.GetDateTime("created"),
        "credentials": gin.H{
            "email":    req.Email,
            "password": req.Password,
        },
    })
}
```

#### GET /admin/v1/users
List all users with filtering by role.

**Query Parameters**:
- `page`, `per_page`
- `role` (filter: commander/operator/analyst)

#### GET /admin/v1/users/:id
Get specific user details.

#### PATCH /admin/v1/users/:id
Update user (role, password, etc.).

**Request**:
```json
{
  "role": "commander",
  "verified": true
}
```

#### DELETE /admin/v1/users/:id
Delete/deactivate user account.

---

## Security Considerations

### 1. Authentication & Authorization

**Require Admin API Key**:
```go
// middleware/admin_auth.go
func AdminAuthMiddleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        apiKey := c.GetHeader("Authorization")
        
        // Validate G-LM admin API key
        if !isValidAdminKey(apiKey) {
            c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
            c.Abort()
            return
        }
        
        c.Next()
    }
}

// Apply to admin routes
adminGroup := router.Group("/admin/v1")
adminGroup.Use(AdminAuthMiddleware())
```

### 2. Password Security

**Requirements**:
- Minimum 15 characters for service accounts (PocketBase enforced)
- Minimum 8 characters for users (adjust as needed)
- Auto-generate secure passwords when not provided
- Return password only once on creation

**Example**:
```go
import "crypto/rand"

func GenerateSecurePassword(length int) (string, error) {
    chars := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%^&*"
    bytes := make([]byte, length)
    if _, err := rand.Read(bytes); err != nil {
        return "", err
    }
    
    for i, b := range bytes {
        bytes[i] = chars[b%byte(len(chars))]
    }
    
    return string(bytes), nil
}
```

### 3. Audit Logging

Log all account management operations to `audit_events`:

```go
func (h *AdminHandler) logAdminAction(action, actorID, targetType, targetID string) {
    h.pbAdmin.Collection("audit_events").Create(map[string]any{
        "tenant_id":  "system", // or specific tenant
        "actor_type": "admin_api",
        "actor_id":   actorID,
        "endpoint":   fmt.Sprintf("/admin/v1/%s", action),
        "model":      targetType,
        "outcome":    "success",
        "timestamp":  time.Now().Format(time.RFC3339),
        "trace_id":   generateTraceID(),
    })
}
```

### 4. Rate Limiting

Apply rate limiting to admin endpoints:
```go
import "github.com/ulule/limiter/v3"

// 100 requests per minute per API key
adminLimiter := limiter.New(memory.NewStore(), 
    limiter.Rate{Limit: 100, Period: time.Minute})
```

---

## Environment Configuration

```bash
# .env for G-LM API
GLM_POCKETBASE_URL=https://pocketbase.thynaptic.com

# Option 1: Admin credentials
GLM_POCKETBASE_ADMIN_EMAIL=admin@thynaptic.com
GLM_POCKETBASE_ADMIN_PASSWORD=secure_admin_password

# Option 2: Service account with admin scopes
GLM_POCKETBASE_AUTH_COLLECTION=service_accounts
GLM_POCKETBASE_IDENTITY=glm-admin@internal.local
GLM_POCKETBASE_PASSWORD=secure_service_password

# Admin API security
GLM_ADMIN_API_KEY=glm_admin_xxxxxxxxxxxxxxxxxxxxx
```

---

## Testing

### Create Service Account
```bash
curl -X POST http://localhost:8081/admin/v1/service-accounts \
  -H "Authorization: Bearer glm_admin_xxxx" \
  -H "Content-Type: application/json" \
  -d '{
    "email": "test-service@internal.local",
    "service_name": "test-service",
    "description": "Test service account",
    "status": "active",
    "tenant_id": "04e4a329-4564-4a0f-8d4e-dbd08a5fcbd7",
    "scopes": ["tenants:read"]
  }'
```

### Create User
```bash
curl -X POST http://localhost:8081/admin/v1/users \
  -H "Authorization: Bearer glm_admin_xxxx" \
  -H "Content-Type: application/json" \
  -d '{
    "email": "test.user@thynaptic.com",
    "password": "securepass123",
    "username": "testuser",
    "role": "analyst",
    "name": "Test User",
    "verified": true
  }'
```

### List Service Accounts
```bash
curl -X GET "http://localhost:8081/admin/v1/service-accounts?status=active" \
  -H "Authorization: Bearer glm_admin_xxxx"
```

### Update Service Account Status
```bash
curl -X PATCH http://localhost:8081/admin/v1/service-accounts/svc_abc123 \
  -H "Authorization: Bearer glm_admin_xxxx" \
  -H "Content-Type: application/json" \
  -d '{"status": "suspended"}'
```

---

## PocketBase SDK Usage

### Installation
```bash
cd /Users/mike/Desktop/testModel
go get github.com/pocketbase/pocketbase
```

### Client Setup
```go
// internal/pocketbase/client.go
package pocketbase

import (
    "fmt"
    "os"
    pb "github.com/pocketbase/pocketbase"
)

type Client struct {
    *pb.Client
}

func NewClient() (*Client, error) {
    client := pb.NewClient(os.Getenv("GLM_POCKETBASE_URL"))
    
    // Choose authentication method
    useAdmin := os.Getenv("GLM_POCKETBASE_ADMIN_EMAIL") != ""
    
    if useAdmin {
        // Admin authentication
        _, err := client.Admins().AuthWithPassword(
            os.Getenv("GLM_POCKETBASE_ADMIN_EMAIL"),
            os.Getenv("GLM_POCKETBASE_ADMIN_PASSWORD"),
        )
        if err != nil {
            return nil, fmt.Errorf("admin auth failed: %w", err)
        }
    } else {
        // Service account authentication
        _, err := client.Collection(os.Getenv("GLM_POCKETBASE_AUTH_COLLECTION")).
            AuthWithPassword(
                os.Getenv("GLM_POCKETBASE_IDENTITY"),
                os.Getenv("GLM_POCKETBASE_PASSWORD"),
            )
        if err != nil {
            return nil, fmt.Errorf("service auth failed: %w", err)
        }
    }
    
    return &Client{client}, nil
}
```

### Collection Operations
```go
// Create record
record, err := client.Collection("service_accounts").Create(data)

// List records with filter
records, err := client.Collection("service_accounts").GetList(1, 20, pb.ParamsList{
    Filter: "status = 'active'",
    Sort:   "-created",
})

// Get single record
record, err := client.Collection("service_accounts").GetOne(id, pb.ParamsQuery{})

// Update record
record, err := client.Collection("service_accounts").Update(id, data)

// Delete record
err := client.Collection("service_accounts").Delete(id)
```

---

## Implementation Checklist

### Phase 1: Basic Setup
- [ ] Install PocketBase SDK
- [ ] Create `internal/pocketbase/client.go` with admin auth
- [ ] Create `internal/handlers/admin/` package
- [ ] Set up admin routes in main router
- [ ] Implement admin API key middleware

### Phase 2: Service Account Management
- [ ] POST /admin/v1/service-accounts (create)
- [ ] GET /admin/v1/service-accounts (list)
- [ ] GET /admin/v1/service-accounts/:id (get)
- [ ] PATCH /admin/v1/service-accounts/:id (update)
- [ ] DELETE /admin/v1/service-accounts/:id (delete)
- [ ] Add password generation utility
- [ ] Add audit logging

### Phase 3: User Management
- [ ] POST /admin/v1/users (create)
- [ ] GET /admin/v1/users (list)
- [ ] GET /admin/v1/users/:id (get)
- [ ] PATCH /admin/v1/users/:id (update)
- [ ] DELETE /admin/v1/users/:id (delete)
- [ ] Add role validation

### Phase 4: Security & Testing
- [ ] Implement rate limiting
- [ ] Add comprehensive audit logging
- [ ] Write unit tests
- [ ] Write integration tests
- [ ] Document all endpoints (OpenAPI/Swagger)

---

## Example File Structure

```
/Users/mike/Desktop/testModel/
├── cmd/
│   └── glm-api/
│       └── main.go
├── internal/
│   ├── pocketbase/
│   │   ├── client.go           # PocketBase client setup
│   │   └── admin_client.go     # Admin-specific client
│   ├── handlers/
│   │   └── admin/
│   │       ├── service_accounts.go  # Service account handlers
│   │       ├── users.go             # User handlers
│   │       └── admin.go             # Shared admin logic
│   ├── middleware/
│   │   ├── admin_auth.go       # Admin API key validation
│   │   └── rate_limit.go       # Rate limiting
│   └── models/
│       ├── service_account.go
│       └── user.go
├── .env
└── go.mod
```

---

## Next Steps

1. **Immediate**: Create service account via Admin UI with provided credentials
2. **Short-term**: Implement Phase 1 (basic setup) and Phase 2 (service account management)
3. **Medium-term**: Implement Phase 3 (user management)
4. **Long-term**: Add advanced features (bulk operations, user invitations, password reset)

---

## PocketBase Collections Reference

### service_accounts
- **Type**: auth
- **Fields**: email, password, service_name, description, scopes, status, last_used, tenant_id, rate_limit_rpm
- **Access**: Commander creates, Operator views

### users
- **Type**: auth
- **Fields**: email, password, username, role (commander/operator/analyst), name
- **Access**: Commander creates, self-update for password

### tenants
- **Type**: base
- **Fields**: name, status
- **Access**: Commander/Operator manage

---

## Support & Documentation

- **PocketBase Go SDK**: https://github.com/pocketbase/pocketbase
- **PocketBase Docs**: https://pocketbase.io/docs/
- **G-LM Service Accounts Guide**: `/root/SERVICE_ACCOUNTS_GUIDE.md`
- **G-LM Setup Guide**: `/root/GLM_V1_POCKETBASE_SETUP.md`

---

**Ready for implementation!** 🚀
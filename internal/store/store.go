package store

import (
	"context"
	"time"

	"github.com/mike/cognitive-llm/internal/model"
)

type Store interface {
	Health(ctx context.Context) error

	CreateTenant(ctx context.Context, name string) (model.Tenant, error)
	ListTenants(ctx context.Context, limit int) ([]model.Tenant, error)
	CreateAPIKey(ctx context.Context, rec model.APIKeyRecord) (model.APIKeyRecord, error)
	GetAPIKeysByPrefix(ctx context.Context, prefix string) ([]model.APIKeyRecord, error)

	CreateRole(ctx context.Context, role model.Role) (model.Role, error)
	UpsertModelPolicy(ctx context.Context, policy model.ModelPolicy) (model.ModelPolicy, error)
	GetModelPolicy(ctx context.Context, tenantID string) (model.ModelPolicy, error)
	UpsertCognitivePolicy(ctx context.Context, policy model.CognitivePolicy) (model.CognitivePolicy, error)
	GetCognitivePolicy(ctx context.Context, tenantID string) (model.CognitivePolicy, error)
	UpsertQuota(ctx context.Context, quota model.Quota) (model.Quota, error)
	GetQuota(ctx context.Context, tenantID string) (model.Quota, error)

	CreateIdempotencyRecord(ctx context.Context, rec model.IdempotencyRecord) (model.IdempotencyRecord, error)
	GetIdempotencyRecord(ctx context.Context, tenantID, key string) (model.IdempotencyRecord, error)

	CreateAuditEvent(ctx context.Context, ev model.AuditEvent) (model.AuditEvent, error)
	ListAuditEvents(ctx context.Context, tenantID string, limit int) ([]model.AuditEvent, error)
	UpsertMemoryNode(ctx context.Context, node model.MemoryNode) (model.MemoryNode, error)
	ListMemoryNodes(ctx context.Context, tenantID, sessionID string, limit int) ([]model.MemoryNode, error)

	CleanupExpired(ctx context.Context, now time.Time) error
}

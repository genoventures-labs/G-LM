package memory

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/mike/cognitive-llm/internal/model"
)

var errNotFound = errors.New("record not found")

type Store struct {
	mu            sync.RWMutex
	tenants       map[string]model.Tenant
	apiKeys       map[string]model.APIKeyRecord
	apiByPrefix   map[string][]string
	roles         map[string]model.Role
	policies      map[string]model.ModelPolicy
	quotas        map[string]model.Quota
	idempotency   map[string]model.IdempotencyRecord
	auditByTenant map[string][]model.AuditEvent
	memoryNodes   map[string]model.MemoryNode
}

func New() *Store {
	return &Store{
		tenants:       map[string]model.Tenant{},
		apiKeys:       map[string]model.APIKeyRecord{},
		apiByPrefix:   map[string][]string{},
		roles:         map[string]model.Role{},
		policies:      map[string]model.ModelPolicy{},
		quotas:        map[string]model.Quota{},
		idempotency:   map[string]model.IdempotencyRecord{},
		auditByTenant: map[string][]model.AuditEvent{},
		memoryNodes:   map[string]model.MemoryNode{},
	}
}

func (s *Store) Health(context.Context) error { return nil }

func (s *Store) CreateTenant(_ context.Context, name string) (model.Tenant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := model.Tenant{ID: uuid.NewString(), Name: name, Status: "active", CreatedAt: time.Now().UTC()}
	s.tenants[t.ID] = t
	return t, nil
}

func (s *Store) ListTenants(_ context.Context, limit int) ([]model.Tenant, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]model.Tenant, 0, len(s.tenants))
	for _, t := range s.tenants {
		items = append(items, t)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (s *Store) CreateAPIKey(_ context.Context, rec model.APIKeyRecord) (model.APIKeyRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec.ID = uuid.NewString()
	rec.CreatedAt = time.Now().UTC()
	s.apiKeys[rec.ID] = rec
	s.apiByPrefix[rec.Prefix] = append(s.apiByPrefix[rec.Prefix], rec.ID)
	return rec, nil
}

func (s *Store) GetAPIKeysByPrefix(_ context.Context, prefix string) ([]model.APIKeyRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := s.apiByPrefix[prefix]
	out := make([]model.APIKeyRecord, 0, len(ids))
	for _, id := range ids {
		out = append(out, s.apiKeys[id])
	}
	return out, nil
}

func (s *Store) CreateRole(_ context.Context, role model.Role) (model.Role, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	role.ID = uuid.NewString()
	role.CreatedAt = time.Now().UTC()
	s.roles[role.ID] = role
	return role, nil
}

func (s *Store) UpsertModelPolicy(_ context.Context, policy model.ModelPolicy) (model.ModelPolicy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	if p, ok := s.policies[policy.TenantID]; ok {
		policy.ID = p.ID
		policy.CreatedAt = p.CreatedAt
	} else {
		policy.ID = uuid.NewString()
		policy.CreatedAt = now
	}
	policy.UpdatedAt = now
	s.policies[policy.TenantID] = policy
	return policy, nil
}

func (s *Store) GetModelPolicy(_ context.Context, tenantID string) (model.ModelPolicy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.policies[tenantID]
	if !ok {
		return model.ModelPolicy{}, errNotFound
	}
	return p, nil
}

func (s *Store) UpsertQuota(_ context.Context, quota model.Quota) (model.Quota, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	if q, ok := s.quotas[quota.TenantID]; ok {
		quota.ID = q.ID
		quota.CreatedAt = q.CreatedAt
	} else {
		quota.ID = uuid.NewString()
		quota.CreatedAt = now
	}
	quota.UpdatedAt = now
	s.quotas[quota.TenantID] = quota
	return quota, nil
}

func (s *Store) GetQuota(_ context.Context, tenantID string) (model.Quota, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	q, ok := s.quotas[tenantID]
	if !ok {
		return model.Quota{}, errNotFound
	}
	return q, nil
}

func idKey(tenantID, key string) string { return tenantID + ":" + key }

func (s *Store) CreateIdempotencyRecord(_ context.Context, rec model.IdempotencyRecord) (model.IdempotencyRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec.ID = uuid.NewString()
	rec.CreatedAt = time.Now().UTC()
	s.idempotency[idKey(rec.TenantID, rec.IdempotencyKey)] = rec
	return rec, nil
}

func (s *Store) GetIdempotencyRecord(_ context.Context, tenantID, key string) (model.IdempotencyRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.idempotency[idKey(tenantID, key)]
	if !ok {
		return model.IdempotencyRecord{}, errNotFound
	}
	return r, nil
}

func (s *Store) CreateAuditEvent(_ context.Context, ev model.AuditEvent) (model.AuditEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ev.ID = uuid.NewString()
	if ev.Timestamp.IsZero() {
		ev.Timestamp = model.FlexTime{Time: time.Now().UTC()}
	}
	s.auditByTenant[ev.TenantID] = append(s.auditByTenant[ev.TenantID], ev)
	return ev, nil
}

func (s *Store) ListAuditEvents(_ context.Context, tenantID string, limit int) ([]model.AuditEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := append([]model.AuditEvent(nil), s.auditByTenant[tenantID]...)
	sort.Slice(items, func(i, j int) bool { return items[i].Timestamp.Time.After(items[j].Timestamp.Time) })
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func memoryKey(tenantID, sessionID, key string) string { return tenantID + ":" + sessionID + ":" + key }

func (s *Store) UpsertMemoryNode(_ context.Context, node model.MemoryNode) (model.MemoryNode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	idx := memoryKey(node.TenantID, node.SessionID, node.Key)
	if ex, ok := s.memoryNodes[idx]; ok {
		node.ID = ex.ID
		node.CreatedAt = ex.CreatedAt
	} else {
		node.ID = uuid.NewString()
		node.CreatedAt = now
	}
	node.UpdatedAt = now
	s.memoryNodes[idx] = node
	return node, nil
}

func (s *Store) ListMemoryNodes(_ context.Context, tenantID, sessionID string, limit int) ([]model.MemoryNode, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]model.MemoryNode, 0)
	for _, n := range s.memoryNodes {
		if n.TenantID != tenantID {
			continue
		}
		if sessionID != "" && n.SessionID != sessionID {
			continue
		}
		items = append(items, n)
	}
	sort.Slice(items, func(i, j int) bool {
		ti := items[i].UpdatedAt
		tj := items[j].UpdatedAt
		return ti.After(tj)
	})
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (s *Store) CleanupExpired(_ context.Context, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range s.idempotency {
		if v.ExpiresAt.Time.Before(now) {
			delete(s.idempotency, k)
		}
	}
	return nil
}

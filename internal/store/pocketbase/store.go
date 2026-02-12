package pocketbase

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/mike/cognitive-llm/internal/model"
)

var (
	errNotFound        = errors.New("record not found")
	errForbiddenScopes = errors.New("insufficient service account scopes")
)

type Config struct {
	BaseURL        string
	AuthCollection string
	Identity       string
	Password       string
}

type serviceAccount struct {
	ID     string
	Status string
	Scopes []string
}

type Store struct {
	cfg      Config
	http     *http.Client
	mu       sync.Mutex
	token    string
	authedAt time.Time
	account  serviceAccount
}

func New(cfg Config) *Store {
	return &Store{cfg: cfg, http: &http.Client{Timeout: 15 * time.Second}}
}

func (s *Store) Health(ctx context.Context) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(s.cfg.BaseURL, "/")+"/api/health", nil)
	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("pocketbase health failed: %s: %s", resp.Status, string(b))
	}
	if s.cfg.Identity != "" && s.cfg.Password != "" {
		if err := s.ensureToken(ctx); err != nil {
			return fmt.Errorf("pocketbase service auth check failed: %w", err)
		}
	}
	return nil
}

func (s *Store) ensureToken(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cfg.Identity == "" || s.cfg.Password == "" {
		return nil
	}

	if s.token == "" {
		return s.authWithPasswordLocked(ctx)
	}

	// Refresh token periodically; PB tokens are usually long-lived, but this keeps sessions healthy.
	if time.Since(s.authedAt) > 10*time.Minute {
		if err := s.authRefreshLocked(ctx); err != nil {
			// Fall back to full auth if refresh fails.
			s.token = ""
			return s.authWithPasswordLocked(ctx)
		}
	}
	return nil
}

func (s *Store) authWithPasswordLocked(ctx context.Context) error {
	endpoint := fmt.Sprintf("%s/api/collections/%s/auth-with-password", strings.TrimRight(s.cfg.BaseURL, "/"), s.cfg.AuthCollection)
	body, _ := json.Marshal(map[string]string{"identity": s.cfg.Identity, "password": s.cfg.Password})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("pocketbase auth failed: %s: %s", resp.Status, string(b))
	}
	var out authResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return err
	}
	account, err := parseServiceAccount(out.Record)
	if err != nil {
		return err
	}
	if !strings.EqualFold(account.Status, "active") {
		return fmt.Errorf("service account status is %q, expected active", account.Status)
	}
	s.token = out.Token
	s.authedAt = time.Now().UTC()
	s.account = account
	go s.updateLastUsed(context.Background(), account.ID)
	return nil
}

func (s *Store) authRefreshLocked(ctx context.Context) error {
	endpoint := fmt.Sprintf("%s/api/collections/%s/auth-refresh", strings.TrimRight(s.cfg.BaseURL, "/"), s.cfg.AuthCollection)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.token)
	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("pocketbase auth refresh failed: %s: %s", resp.Status, string(b))
	}
	var out authResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return err
	}
	account, err := parseServiceAccount(out.Record)
	if err != nil {
		return err
	}
	if !strings.EqualFold(account.Status, "active") {
		return fmt.Errorf("service account status is %q, expected active", account.Status)
	}
	s.token = out.Token
	s.authedAt = time.Now().UTC()
	s.account = account
	go s.updateLastUsed(context.Background(), account.ID)
	return nil
}

func (s *Store) updateLastUsed(ctx context.Context, accountID string) {
	if accountID == "" || s.token == "" {
		return
	}
	payload := map[string]any{"last_used": time.Now().UTC().Format(time.RFC3339)}
	b, _ := json.Marshal(payload)
	path := fmt.Sprintf("%s/api/collections/%s/records/%s", strings.TrimRight(s.cfg.BaseURL, "/"), s.cfg.AuthCollection, accountID)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPatch, path, bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
}

func (s *Store) requireAnyScope(ctx context.Context, required ...string) error {
	if err := s.ensureToken(ctx); err != nil {
		return err
	}
	if s.cfg.Identity == "" || s.cfg.Password == "" {
		return nil
	}
	for _, want := range required {
		for _, have := range s.account.Scopes {
			if have == "*" || strings.EqualFold(have, want) {
				return nil
			}
			if strings.HasSuffix(have, ":*") {
				prefix := strings.TrimSuffix(have, "*")
				if strings.HasPrefix(want, prefix) {
					return nil
				}
			}
		}
	}
	if len(required) == 0 {
		return nil
	}
	return fmt.Errorf("%w: need one of %v", errForbiddenScopes, required)
}

func (s *Store) do(ctx context.Context, method, path string, payload any, out any) error {
	if err := s.ensureToken(ctx); err != nil {
		return err
	}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		err := s.doOnce(ctx, method, path, payload, out)
		if err == nil {
			return nil
		}
		lastErr = err
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, " status 429") || strings.Contains(msg, " status 502") || strings.Contains(msg, " status 503") || strings.Contains(msg, " status 504") {
			time.Sleep(time.Duration(attempt+1) * 150 * time.Millisecond)
			continue
		}
		return err
	}
	return lastErr
}

func (s *Store) doOnce(ctx context.Context, method, path string, payload any, out any) error {
	var body io.Reader
	if payload != nil {
		b, _ := json.Marshal(payload)
		body = bytes.NewReader(b)
	}
	req, _ := http.NewRequestWithContext(ctx, method, strings.TrimRight(s.cfg.BaseURL, "/")+path, body)
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized && s.cfg.Identity != "" && s.cfg.Password != "" {
		s.mu.Lock()
		s.token = ""
		s.mu.Unlock()
		if err := s.ensureToken(ctx); err != nil {
			return err
		}
		return s.doOnce(ctx, method, path, payload, out)
	}
	if resp.StatusCode == http.StatusNotFound {
		return errNotFound
	}
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("pocketbase request failed: status %d: %s", resp.StatusCode, string(b))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func createRecordPath(c string) string { return "/api/collections/" + c + "/records" }

func listRecordPath(c, filter string, perPage int) string {
	vals := url.Values{}
	if filter != "" {
		vals.Set("filter", filter)
	}
	if perPage > 0 {
		vals.Set("perPage", fmt.Sprintf("%d", perPage))
	}
	return createRecordPath(c) + "?" + vals.Encode()
}

func sanitizeFilter(s string) string {
	return strings.ReplaceAll(s, "'", "\\'")
}

type listResp[T any] struct {
	Items []T `json:"items"`
}

type authResponse struct {
	Token  string         `json:"token"`
	Record map[string]any `json:"record"`
}

func parseServiceAccount(record map[string]any) (serviceAccount, error) {
	id, _ := record["id"].(string)
	status, _ := record["status"].(string)
	if status == "" {
		status = "active"
	}
	scopes := []string{}
	if raw, ok := record["scopes"]; ok {
		switch v := raw.(type) {
		case []any:
			for _, item := range v {
				if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
					scopes = append(scopes, strings.TrimSpace(s))
				}
			}
		case []string:
			scopes = append(scopes, v...)
		}
	}
	return serviceAccount{ID: id, Status: status, Scopes: scopes}, nil
}

func (s *Store) CreateTenant(ctx context.Context, name string) (model.Tenant, error) {
	if err := s.requireAnyScope(ctx, "tenants:write", "tenants:*"); err != nil {
		return model.Tenant{}, err
	}
	now := time.Now().UTC()
	payload := map[string]any{"name": name, "status": "active", "created_at": now.Format(time.RFC3339)}
	var rec model.Tenant
	if err := s.do(ctx, http.MethodPost, createRecordPath("tenants"), payload, &rec); err != nil {
		return model.Tenant{}, err
	}
	return rec, nil
}

func (s *Store) ListTenants(ctx context.Context, limit int) ([]model.Tenant, error) {
	if err := s.requireAnyScope(ctx, "tenants:read", "tenants:*"); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}
	var out listResp[model.Tenant]
	path := listRecordPath("tenants", "", limit) + "&sort=-created"
	if err := s.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

func (s *Store) CreateAPIKey(ctx context.Context, rec model.APIKeyRecord) (model.APIKeyRecord, error) {
	if err := s.requireAnyScope(ctx, "api_keys:write", "api_keys:*"); err != nil {
		return model.APIKeyRecord{}, err
	}
	payload := map[string]any{
		"tenant_id":  rec.TenantID,
		"prefix":     rec.Prefix,
		"hash":       rec.Hash,
		"scopes":     rec.Scopes,
		"status":     rec.Status,
		"expires_at": rec.ExpiresAt,
	}
	var out model.APIKeyRecord
	if err := s.do(ctx, http.MethodPost, createRecordPath("api_keys"), payload, &out); err != nil {
		return model.APIKeyRecord{}, err
	}
	return out, nil
}

func (s *Store) GetAPIKeysByPrefix(ctx context.Context, prefix string) ([]model.APIKeyRecord, error) {
	if err := s.requireAnyScope(ctx, "api_keys:read", "api_keys:*"); err != nil {
		return nil, err
	}
	filter := fmt.Sprintf("prefix = '%s'", sanitizeFilter(prefix))
	var out listResp[model.APIKeyRecord]
	if err := s.do(ctx, http.MethodGet, listRecordPath("api_keys", filter, 50), nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

func (s *Store) CreateRole(ctx context.Context, role model.Role) (model.Role, error) {
	if err := s.requireAnyScope(ctx, "roles:write", "roles:*"); err != nil {
		return model.Role{}, err
	}
	payload := map[string]any{"tenant_id": role.TenantID, "name": role.Name, "permissions": role.Permissions}
	var out model.Role
	if err := s.do(ctx, http.MethodPost, createRecordPath("roles"), payload, &out); err != nil {
		return model.Role{}, err
	}
	return out, nil
}

func (s *Store) UpsertModelPolicy(ctx context.Context, policy model.ModelPolicy) (model.ModelPolicy, error) {
	if err := s.requireAnyScope(ctx, "model_policies:write", "model_policies:*"); err != nil {
		return model.ModelPolicy{}, err
	}
	filter := fmt.Sprintf("tenant_id = '%s'", sanitizeFilter(policy.TenantID))
	var list listResp[model.ModelPolicy]
	if err := s.do(ctx, http.MethodGet, listRecordPath("model_policies", filter, 1), nil, &list); err != nil && !errors.Is(err, errNotFound) {
		return model.ModelPolicy{}, err
	}
	payload := map[string]any{
		"tenant_id":         policy.TenantID,
		"allowed_models":    policy.AllowedModels,
		"primary_model":     policy.PrimaryModel,
		"fallback_model":    policy.FallbackModel,
		"reasoning_visible": policy.ReasoningVisible,
	}
	if len(list.Items) == 0 {
		var out model.ModelPolicy
		if err := s.do(ctx, http.MethodPost, createRecordPath("model_policies"), payload, &out); err != nil {
			return model.ModelPolicy{}, err
		}
		return out, nil
	}
	id := list.Items[0].ID
	var out model.ModelPolicy
	if err := s.do(ctx, http.MethodPatch, createRecordPath("model_policies")+"/"+id, payload, &out); err != nil {
		return model.ModelPolicy{}, err
	}
	return out, nil
}

func (s *Store) GetModelPolicy(ctx context.Context, tenantID string) (model.ModelPolicy, error) {
	if err := s.requireAnyScope(ctx, "model_policies:read", "model_policies:*"); err != nil {
		return model.ModelPolicy{}, err
	}
	filter := fmt.Sprintf("tenant_id = '%s'", sanitizeFilter(tenantID))
	var out listResp[model.ModelPolicy]
	if err := s.do(ctx, http.MethodGet, listRecordPath("model_policies", filter, 1), nil, &out); err != nil {
		return model.ModelPolicy{}, err
	}
	if len(out.Items) == 0 {
		return model.ModelPolicy{}, errNotFound
	}
	return out.Items[0], nil
}

func (s *Store) UpsertCognitivePolicy(ctx context.Context, policy model.CognitivePolicy) (model.CognitivePolicy, error) {
	if err := s.requireAnyScope(ctx, "cognitive_policies:write", "cognitive_policies:*"); err != nil {
		return model.CognitivePolicy{}, err
	}
	filter := fmt.Sprintf("tenant_id = '%s'", sanitizeFilter(policy.TenantID))
	var list listResp[model.CognitivePolicy]
	if err := s.do(ctx, http.MethodGet, listRecordPath("cognitive_policies", filter, 1), nil, &list); err != nil && !errors.Is(err, errNotFound) {
		return model.CognitivePolicy{}, err
	}
	payload := map[string]any{
		"tenant_id":                        policy.TenantID,
		"status":                           policy.Status,
		"version":                          policy.Version,
		"allowed_reasoning_modes":          policy.AllowedReasoningModes,
		"max_reasoning_passes":             policy.MaxReasoningPasses,
		"max_reflection_passes":            policy.MaxReflectionPasses,
		"max_self_alignment_passes":        policy.MaxSelfAlignmentPasses,
		"allow_constraint_breaking":        policy.AllowConstraintBreaking,
		"max_constraint_breaking_severity": policy.MaxConstraintBreakingSeverity,
		"allow_adversarial_self_play":      policy.AllowAdversarialSelfPlay,
		"allow_worldview_fusion":           policy.AllowWorldviewFusion,
		"allow_shape_transform":            policy.AllowShapeTransform,
		"allow_skill_compiler":             policy.AllowSkillCompiler,
		"tool_allowlist":                   policy.ToolAllowlist,
		"tool_denylist":                    policy.ToolDenylist,
		"risk_threshold_reject":            policy.RiskThresholdReject,
		"risk_threshold_warn":              policy.RiskThresholdWarn,
	}
	if len(list.Items) == 0 {
		var out model.CognitivePolicy
		if err := s.do(ctx, http.MethodPost, createRecordPath("cognitive_policies"), payload, &out); err != nil {
			return model.CognitivePolicy{}, err
		}
		return out, nil
	}
	id := list.Items[0].ID
	var out model.CognitivePolicy
	if err := s.do(ctx, http.MethodPatch, createRecordPath("cognitive_policies")+"/"+id, payload, &out); err != nil {
		return model.CognitivePolicy{}, err
	}
	return out, nil
}

func (s *Store) GetCognitivePolicy(ctx context.Context, tenantID string) (model.CognitivePolicy, error) {
	if err := s.requireAnyScope(ctx, "cognitive_policies:read", "cognitive_policies:*"); err != nil {
		return model.CognitivePolicy{}, err
	}
	filter := fmt.Sprintf("tenant_id = '%s'", sanitizeFilter(tenantID))
	var out listResp[model.CognitivePolicy]
	if err := s.do(ctx, http.MethodGet, listRecordPath("cognitive_policies", filter, 1), nil, &out); err != nil {
		return model.CognitivePolicy{}, err
	}
	if len(out.Items) == 0 {
		return model.CognitivePolicy{}, errNotFound
	}
	return out.Items[0], nil
}

func (s *Store) UpsertQuota(ctx context.Context, quota model.Quota) (model.Quota, error) {
	if err := s.requireAnyScope(ctx, "quotas:write", "quotas:*"); err != nil {
		return model.Quota{}, err
	}
	filter := fmt.Sprintf("tenant_id = '%s'", sanitizeFilter(quota.TenantID))
	var list listResp[model.Quota]
	if err := s.do(ctx, http.MethodGet, listRecordPath("quotas", filter, 1), nil, &list); err != nil && !errors.Is(err, errNotFound) {
		return model.Quota{}, err
	}
	payload := map[string]any{"tenant_id": quota.TenantID, "rpm_limit": quota.RPMLimit, "tpm_limit": quota.TPMLimit, "burst": quota.Burst}
	if len(list.Items) == 0 {
		var out model.Quota
		if err := s.do(ctx, http.MethodPost, createRecordPath("quotas"), payload, &out); err != nil {
			return model.Quota{}, err
		}
		return out, nil
	}
	id := list.Items[0].ID
	var out model.Quota
	if err := s.do(ctx, http.MethodPatch, createRecordPath("quotas")+"/"+id, payload, &out); err != nil {
		return model.Quota{}, err
	}
	return out, nil
}

func (s *Store) GetQuota(ctx context.Context, tenantID string) (model.Quota, error) {
	if err := s.requireAnyScope(ctx, "quotas:read", "quotas:*"); err != nil {
		return model.Quota{}, err
	}
	filter := fmt.Sprintf("tenant_id = '%s'", sanitizeFilter(tenantID))
	var out listResp[model.Quota]
	if err := s.do(ctx, http.MethodGet, listRecordPath("quotas", filter, 1), nil, &out); err != nil {
		return model.Quota{}, err
	}
	if len(out.Items) == 0 {
		return model.Quota{}, errNotFound
	}
	return out.Items[0], nil
}

func (s *Store) CreateIdempotencyRecord(ctx context.Context, rec model.IdempotencyRecord) (model.IdempotencyRecord, error) {
	if err := s.requireAnyScope(ctx, "idempotency:write", "idempotency:*"); err != nil {
		return model.IdempotencyRecord{}, err
	}
	payload := map[string]any{
		"tenant_id":       rec.TenantID,
		"idempotency_key": rec.IdempotencyKey,
		"request_hash":    rec.RequestHash,
		"response_hash":   rec.ResponseHash,
		"status":          rec.Status,
		"expires_at":      rec.ExpiresAt,
	}
	var out model.IdempotencyRecord
	if err := s.do(ctx, http.MethodPost, createRecordPath("idempotency_records"), payload, &out); err != nil {
		return model.IdempotencyRecord{}, err
	}
	return out, nil
}

func (s *Store) GetIdempotencyRecord(ctx context.Context, tenantID, key string) (model.IdempotencyRecord, error) {
	if err := s.requireAnyScope(ctx, "idempotency:read", "idempotency:write", "idempotency:*"); err != nil {
		return model.IdempotencyRecord{}, err
	}
	filter := fmt.Sprintf("tenant_id = '%s' && idempotency_key = '%s'", sanitizeFilter(tenantID), sanitizeFilter(key))
	var out listResp[model.IdempotencyRecord]
	if err := s.do(ctx, http.MethodGet, listRecordPath("idempotency_records", filter, 1), nil, &out); err != nil {
		return model.IdempotencyRecord{}, err
	}
	if len(out.Items) == 0 {
		return model.IdempotencyRecord{}, errNotFound
	}
	return out.Items[0], nil
}

func (s *Store) CreateAuditEvent(ctx context.Context, ev model.AuditEvent) (model.AuditEvent, error) {
	if err := s.requireAnyScope(ctx, "audit:write", "audit:*"); err != nil {
		return model.AuditEvent{}, err
	}
	payload := map[string]any{
		"tenant_id":  ev.TenantID,
		"actor_type": ev.ActorType,
		"actor_id":   ev.ActorID,
		"endpoint":   ev.Endpoint,
		"model":      ev.Model,
		"outcome":    ev.Outcome,
		"latency_ms": ev.LatencyMS,
		"trace_id":   ev.TraceID,
		"timestamp":  ev.Timestamp,
	}
	var out model.AuditEvent
	if err := s.do(ctx, http.MethodPost, createRecordPath("audit_events"), payload, &out); err != nil {
		return model.AuditEvent{}, err
	}
	return out, nil
}

func (s *Store) ListAuditEvents(ctx context.Context, tenantID string, limit int) ([]model.AuditEvent, error) {
	if err := s.requireAnyScope(ctx, "audit:read", "audit:*"); err != nil {
		return nil, err
	}
	filter := fmt.Sprintf("tenant_id = '%s'", sanitizeFilter(tenantID))
	if limit <= 0 {
		limit = 50
	}
	var out listResp[model.AuditEvent]
	path := listRecordPath("audit_events", filter, limit) + "&sort=-timestamp"
	if err := s.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

func (s *Store) UpsertMemoryNode(ctx context.Context, node model.MemoryNode) (model.MemoryNode, error) {
	if err := s.requireAnyScope(ctx, "memory:write", "memory:*"); err != nil {
		return model.MemoryNode{}, err
	}
	filter := fmt.Sprintf(
		"tenant_id = '%s' && key = '%s'",
		sanitizeFilter(node.TenantID),
		sanitizeFilter(node.Key),
	)
	var list listResp[model.MemoryNode]
	if err := s.do(ctx, http.MethodGet, listRecordPath("memory_nodes", filter, 1), nil, &list); err != nil && !errors.Is(err, errNotFound) {
		return model.MemoryNode{}, err
	}
	meta := map[string]any{}
	for k, v := range node.Metadata {
		meta[k] = v
	}
	if strings.TrimSpace(node.SessionID) != "" {
		meta["session_id"] = node.SessionID
	}
	label := strings.TrimSpace(node.Label)
	if label == "" {
		label = strings.TrimSpace(node.Key)
	}
	if label == "" {
		if v, ok := meta["label"].(string); ok {
			label = strings.TrimSpace(v)
		}
	}
	content := ""
	if v, ok := meta["content"].(string); ok {
		content = strings.TrimSpace(v)
	}
	if content == "" {
		content = label
	}
	if content == "" {
		content = strings.TrimSpace(node.Key)
	}
	if content == "" {
		content = "memory-node"
	}
	payload := map[string]any{
		"tenant_id":    node.TenantID,
		"session_id":   node.SessionID,
		"key":          node.Key,
		"label":        label,
		"content":      content,
		"metadata":     meta,
		"weight":       node.Weight,
		"importance":   node.Importance,
		"access_count": node.AccessCount,
		"last_seen_at": node.LastSeenAt,
	}
	if len(list.Items) == 0 {
		var out model.MemoryNode
		if err := s.do(ctx, http.MethodPost, createRecordPath("memory_nodes"), payload, &out); err != nil {
			return model.MemoryNode{}, err
		}
		return out, nil
	}
	var out model.MemoryNode
	if err := s.do(ctx, http.MethodPatch, createRecordPath("memory_nodes")+"/"+list.Items[0].ID, payload, &out); err != nil {
		return model.MemoryNode{}, err
	}
	return out, nil
}

func (s *Store) ListMemoryNodes(ctx context.Context, tenantID, sessionID string, limit int) ([]model.MemoryNode, error) {
	if err := s.requireAnyScope(ctx, "memory:read", "memory:write", "memory:*"); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}
	filter := fmt.Sprintf("tenant_id = '%s'", sanitizeFilter(tenantID))
	var out listResp[model.MemoryNode]
	path := listRecordPath("memory_nodes", filter, limit) + "&sort=-updated"
	if err := s.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	if strings.TrimSpace(sessionID) == "" {
		return out.Items, nil
	}
	items := make([]model.MemoryNode, 0, len(out.Items))
	for _, n := range out.Items {
		if strings.EqualFold(strings.TrimSpace(n.SessionID), strings.TrimSpace(sessionID)) {
			items = append(items, n)
			continue
		}
		if raw, ok := n.Metadata["session_id"]; ok {
			if sID, ok := raw.(string); ok && strings.EqualFold(strings.TrimSpace(sID), strings.TrimSpace(sessionID)) {
				items = append(items, n)
			}
		}
	}
	return items, nil
}

func (s *Store) CleanupExpired(ctx context.Context, now time.Time) error {
	if err := s.requireAnyScope(ctx, "idempotency:write", "idempotency:*"); err != nil {
		return nil
	}
	filter := fmt.Sprintf("expires_at < '%s'", now.UTC().Format(time.RFC3339))
	var out listResp[model.IdempotencyRecord]
	if err := s.do(ctx, http.MethodGet, listRecordPath("idempotency_records", filter, 200), nil, &out); err != nil {
		return nil
	}
	for _, rec := range out.Items {
		_ = s.do(ctx, http.MethodDelete, createRecordPath("idempotency_records")+"/"+rec.ID, nil, nil)
	}
	return nil
}

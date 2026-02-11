package idempotency

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"

	"github.com/mike/cognitive-llm/internal/model"
	"github.com/mike/cognitive-llm/internal/store"
)

type cacheItem struct {
	Response model.ChatCompletionResponse
	Hash     string
	Expires  time.Time
}

type Service struct {
	store store.Store
	mu    sync.RWMutex
	cache map[string]cacheItem
	ttl   time.Duration
}

func NewService(st store.Store, ttl time.Duration) *Service {
	return &Service{store: st, ttl: ttl, cache: map[string]cacheItem{}}
}

func (s *Service) RequestHash(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func cacheKey(tenantID, key string) string { return tenantID + ":" + key }

func (s *Service) Get(tenantID, key string) (model.ChatCompletionResponse, string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.cache[cacheKey(tenantID, key)]
	if !ok || item.Expires.Before(time.Now().UTC()) {
		return model.ChatCompletionResponse{}, "", false
	}
	return item.Response, item.Hash, true
}

func (s *Service) Save(ctx context.Context, tenantID, key, reqHash string, resp model.ChatCompletionResponse) error {
	respHash := s.RequestHash(resp)
	s.mu.Lock()
	s.cache[cacheKey(tenantID, key)] = cacheItem{Response: resp, Hash: reqHash, Expires: time.Now().UTC().Add(s.ttl)}
	s.mu.Unlock()
	_, err := s.store.CreateIdempotencyRecord(ctx, model.IdempotencyRecord{
		TenantID:       tenantID,
		IdempotencyKey: key,
		RequestHash:    reqHash,
		ResponseHash:   respHash,
		Status:         "completed",
		ExpiresAt:      model.FlexTime{Time: time.Now().UTC().Add(s.ttl)},
	})
	return err
}

func (s *Service) GetDurable(ctx context.Context, tenantID, key string) (model.IdempotencyRecord, error) {
	return s.store.GetIdempotencyRecord(ctx, tenantID, key)
}

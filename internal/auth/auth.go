package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mike/cognitive-llm/internal/model"
	"github.com/mike/cognitive-llm/internal/store"
	"golang.org/x/crypto/argon2"
)

type contextKey string

const (
	ctxTenantID contextKey = "tenant_id"
	ctxScopes   contextKey = "scopes"
	ctxKeyID    contextKey = "key_id"
)

var (
	ErrUnauthorized = errors.New("unauthorized")
	ErrForbidden    = errors.New("forbidden")
)

type Service struct {
	store store.Store
}

func NewService(st store.Store) *Service {
	return &Service{store: st}
}

func (s *Service) GenerateAPIKey(ctx context.Context, tenantID string, scopes []string, expiresAt *time.Time) (raw string, rec model.APIKeyRecord, err error) {
	prefixBytes := make([]byte, 6)
	if _, err := rand.Read(prefixBytes); err != nil {
		return "", model.APIKeyRecord{}, err
	}
	secretBytes := make([]byte, 24)
	if _, err := rand.Read(secretBytes); err != nil {
		return "", model.APIKeyRecord{}, err
	}
	prefix := base64.RawURLEncoding.EncodeToString(prefixBytes)
	secret := base64.RawURLEncoding.EncodeToString(secretBytes)
	raw = fmt.Sprintf("glm.%s.%s", prefix, secret)
	hash, err := hashSecret(raw)
	if err != nil {
		return "", model.APIKeyRecord{}, err
	}
	rec = model.APIKeyRecord{
		TenantID:  tenantID,
		Prefix:    prefix,
		Hash:      hash,
		Scopes:    scopes,
		Status:    "active",
		ExpiresAt: model.NewFlexTimeValue(expiresAt),
	}
	created, err := s.store.CreateAPIKey(ctx, rec)
	if err != nil {
		return "", model.APIKeyRecord{}, err
	}
	return raw, created, nil
}

func (s *Service) Authenticate(ctx context.Context, rawToken string) (context.Context, error) {
	token := strings.TrimSpace(strings.TrimPrefix(rawToken, "Bearer "))
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != "glm" {
		return nil, ErrUnauthorized
	}
	prefix := parts[1]
	candidates, err := s.store.GetAPIKeysByPrefix(ctx, prefix)
	if err != nil || len(candidates) == 0 {
		return nil, ErrUnauthorized
	}
	for _, k := range candidates {
		if k.Status != "active" {
			continue
		}
		if k.ExpiresAt != nil && k.ExpiresAt.Time.Before(time.Now().UTC()) {
			continue
		}
		ok, err := verifySecret(token, k.Hash)
		if err != nil || !ok {
			continue
		}
		ctx = context.WithValue(ctx, ctxTenantID, k.TenantID)
		ctx = context.WithValue(ctx, ctxScopes, k.Scopes)
		ctx = context.WithValue(ctx, ctxKeyID, k.ID)
		return ctx, nil
	}
	return nil, ErrUnauthorized
}

func RequireScope(ctx context.Context, required string) error {
	scopes, _ := ctx.Value(ctxScopes).([]string)
	for _, s := range scopes {
		if s == required || s == "*" {
			return nil
		}
		if strings.HasSuffix(s, ":*") {
			prefix := strings.TrimSuffix(s, "*")
			if strings.HasPrefix(required, prefix) {
				return nil
			}
		}
	}
	return ErrForbidden
}

func TenantID(ctx context.Context) string {
	v, _ := ctx.Value(ctxTenantID).(string)
	return v
}

func KeyID(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyID).(string)
	return v
}

func hashSecret(secret string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hash := argon2.IDKey([]byte(secret), salt, 3, 64*1024, 2, 32)
	return base64.RawStdEncoding.EncodeToString(salt) + ":" + base64.RawStdEncoding.EncodeToString(hash), nil
}

func verifySecret(secret, encoded string) (bool, error) {
	parts := strings.Split(encoded, ":")
	if len(parts) != 2 {
		return false, errors.New("invalid hash format")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[0])
	if err != nil {
		return false, err
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[1])
	if err != nil {
		return false, err
	}
	hash := argon2.IDKey([]byte(secret), salt, 3, 64*1024, 2, uint32(len(expected)))
	return subtle.ConstantTimeCompare(hash, expected) == 1, nil
}

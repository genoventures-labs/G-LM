package auth

import (
	"context"
	"testing"
	"time"

	"github.com/mike/cognitive-llm/internal/store/memory"
)

func TestGenerateAndAuthenticateAPIKey(t *testing.T) {
	st := memory.New()
	a := NewService(st)
	tenant, err := st.CreateTenant(context.Background(), "acme")
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err := a.GenerateAPIKey(context.Background(), tenant.ID, []string{"runtime:chat"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := a.Authenticate(context.Background(), "Bearer "+raw)
	if err != nil {
		t.Fatalf("expected auth success, got %v", err)
	}
	if got := TenantID(ctx); got != tenant.ID {
		t.Fatalf("expected tenant %s, got %s", tenant.ID, got)
	}
}

func TestExpiredKeyRejected(t *testing.T) {
	st := memory.New()
	a := NewService(st)
	tenant, _ := st.CreateTenant(context.Background(), "acme")
	expired := time.Now().UTC().Add(-1 * time.Minute)
	raw, _, err := a.GenerateAPIKey(context.Background(), tenant.ID, []string{"runtime:chat"}, &expired)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Authenticate(context.Background(), "Bearer "+raw); err == nil {
		t.Fatal("expected key to be rejected")
	}
}

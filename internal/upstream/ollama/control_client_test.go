package ollama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestListModels(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"models": []map[string]any{{"name": "mistral:7b"}, {"name": "qwen3:4b"}},
		})
	}))
	defer ts.Close()

	c := New(ts.URL, "", 2*time.Second, 0)
	models, err := c.ListModels(context.Background())
	if err != nil {
		t.Fatalf("list models failed: %v", err)
	}
	if len(models) != 2 || models[0] != "mistral:7b" || models[1] != "qwen3:4b" {
		t.Fatalf("unexpected models: %#v", models)
	}
}

func TestPullModelPayload(t *testing.T) {
	called := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/pull" || r.Method != http.MethodPost {
			t.Fatalf("unexpected call: %s %s", r.Method, r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode failed: %v", err)
		}
		if payload["name"] != "deepseek-coder:6.7b" {
			t.Fatalf("unexpected name payload: %v", payload["name"])
		}
		if payload["stream"] != false {
			t.Fatalf("expected stream=false, got %v", payload["stream"])
		}
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	c := New(ts.URL, "", 2*time.Second, 0)
	if err := c.PullModel(context.Background(), "deepseek-coder:6.7b"); err != nil {
		t.Fatalf("pull failed: %v", err)
	}
	if !called {
		t.Fatal("expected pull endpoint call")
	}
}

func TestDeleteModelPayload(t *testing.T) {
	called := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/delete" || r.Method != http.MethodDelete {
			t.Fatalf("unexpected call: %s %s", r.Method, r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode failed: %v", err)
		}
		if payload["name"] != "old-model:1b" {
			t.Fatalf("unexpected name payload: %v", payload["name"])
		}
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	c := New(ts.URL, "", 2*time.Second, 0)
	if err := c.DeleteModel(context.Background(), "old-model:1b"); err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	if !called {
		t.Fatal("expected delete endpoint call")
	}
}

func TestHostStatsUnavailableFallback(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	c := New(ts.URL, "", 2*time.Second, 0)
	_, _, available, err := c.HostStats(context.Background())
	if err == nil {
		t.Fatal("expected error for unavailable stats endpoint")
	}
	if available {
		t.Fatal("expected stats.Available=false")
	}
}

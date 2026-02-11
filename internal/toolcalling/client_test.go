package toolcalling

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestToolDefinitionsFromOpenAPI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/openapi.json" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"openapi":"3.1.0","paths":{"/tools/web_search":{"post":{"summary":"Web search","requestBody":{"content":{"application/json":{"schema":{"type":"object","properties":{"query":{"type":"string"}}}}}}}},"/tools/fetch_url":{"post":{"summary":"Fetch URL"}}}}`))
	}))
	defer srv.Close()
	c := New(srv.URL, "k", "cid", 60*time.Second)
	defs, err := c.ToolDefinitions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(defs) != 2 {
		t.Fatalf("expected 2 defs, got %d", len(defs))
	}
	if defs[0].Function.Name != "fetch_url" || defs[1].Function.Name != "web_search" {
		t.Fatalf("unexpected defs order/content: %#v", defs)
	}
}

func TestRetryPolicyWebSearchRetriesOn5xx(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tools/web_search" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			http.Error(w, "bad gateway", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	c := New(srv.URL, "k", "cid", 60*time.Second)
	out, err := c.Call(context.Background(), "web_search", json.RawMessage(`{"query":"dns"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(out) {
		t.Fatalf("expected valid json, got %s", string(out))
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", calls)
	}
}

func TestRetryPolicyHTTPRequestNoRetryOn5xx(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			http.Error(w, "bad gateway", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	c := New(srv.URL, "k", "cid", 60*time.Second)
	_, err := c.Call(context.Background(), "http_request", json.RawMessage(`{"url":"https://example.com"}`))
	if err == nil {
		t.Fatal("expected error")
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("expected single call, got %d", calls)
	}
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/mike/cognitive-llm/internal/auth"
	"github.com/mike/cognitive-llm/internal/config"
	httpapi "github.com/mike/cognitive-llm/internal/http"
	"github.com/mike/cognitive-llm/internal/store"
	"github.com/mike/cognitive-llm/internal/store/memory"
	"github.com/mike/cognitive-llm/internal/store/pocketbase"
	"github.com/mike/cognitive-llm/internal/upstream/openwebui"
)

func main() {
	_ = godotenv.Load()
	cfg := config.Load()
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "bootstrap-admin-key":
			if err := runBootstrapAdminKey(cfg, os.Args[2:]); err != nil {
				log.Fatalf("bootstrap-admin-key failed: %v", err)
			}
			return
		case "list-tenants":
			if err := runListTenants(cfg, os.Args[2:]); err != nil {
				log.Fatalf("list-tenants failed: %v", err)
			}
			return
		case "doctor":
			if err := runDoctor(cfg, os.Args[2:]); err != nil {
				log.Fatalf("doctor failed: %v", err)
			}
			return
		case "quick-smoke":
			if err := runQuickSmoke(cfg, os.Args[2:]); err != nil {
				log.Fatalf("quick-smoke failed: %v", err)
			}
			return
		case "eval-v2":
			if err := runEvalV2(cfg, os.Args[2:]); err != nil {
				log.Fatalf("eval-v2 failed: %v", err)
			}
			return
		}
	}

	st, err := buildStore(cfg)
	if err != nil {
		log.Fatalf("store configuration error: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := st.Health(ctx); err != nil {
		log.Printf("store health warning: %v", err)
	}

	upstream := openwebui.New(cfg.UpstreamBaseURL, cfg.UpstreamAPIKey, cfg.UpstreamTimeout, cfg.UpstreamRetryMax)
	srv := httpapi.NewServer(cfg, st, upstream)

	httpServer := &http.Server{
		Addr:         cfg.ServerAddr,
		Handler:      srv.Handler(),
		ReadTimeout:  cfg.ServerReadTimeout,
		WriteTimeout: cfg.ServerWriteTimeout,
		IdleTimeout:  cfg.ServerIdleTimeout,
	}

	go func() {
		log.Printf("glm-api listening on %s", cfg.ServerAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server failed: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("graceful shutdown failed: %v", err)
	}
}

func runBootstrapAdminKey(cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("bootstrap-admin-key", flag.ContinueOnError)
	var tenantID string
	var tenantName string
	var scopesCSV string
	var expiresHours int

	fs.StringVar(&tenantID, "tenant-id", "", "existing tenant id for the key")
	fs.StringVar(&tenantName, "tenant-name", "bootstrap-admin", "tenant name to create when tenant-id is empty")
	fs.StringVar(&scopesCSV, "scopes", "admin:*,runtime:*", "comma-separated scopes")
	fs.IntVar(&expiresHours, "expires-hours", 0, "key expiration in hours (0 means no expiration)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	st, err := buildStore(cfg)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := st.Health(ctx); err != nil {
		return fmt.Errorf("store health check failed: %w", err)
	}

	if tenantID == "" {
		tenant, err := st.CreateTenant(ctx, tenantName)
		if err != nil {
			return fmt.Errorf("create tenant failed: %w", err)
		}
		tenantID = tenant.ID
	}

	scopes := []string{}
	for _, s := range strings.Split(scopesCSV, ",") {
		s = strings.TrimSpace(s)
		if s != "" {
			scopes = append(scopes, s)
		}
	}
	if len(scopes) == 0 {
		scopes = []string{"admin:*", "runtime:*"}
	}

	var expiresAt *time.Time
	if expiresHours > 0 {
		t := time.Now().UTC().Add(time.Duration(expiresHours) * time.Hour)
		expiresAt = &t
	}

	authSvc := auth.NewService(st)
	rawKey, rec, err := authSvc.GenerateAPIKey(ctx, tenantID, scopes, expiresAt)
	if err != nil {
		if strings.Contains(err.Error(), "validation_missing_rel_records") && strings.Contains(err.Error(), "tenant_id") {
			return fmt.Errorf("generate api key failed: tenant_id %q was not found in PocketBase. Create the tenant first (or use an existing tenant record id), then retry", tenantID)
		}
		return fmt.Errorf("generate api key failed: %w", err)
	}

	out := map[string]any{
		"tenant_id":  tenantID,
		"key_id":     rec.ID,
		"scopes":     rec.Scopes,
		"expires_at": rec.ExpiresAt,
		"api_key":    rawKey,
	}
	var expiry *time.Time
	if rec.ExpiresAt != nil {
		t := rec.ExpiresAt.Time
		expiry = &t
	}
	if path, err := saveKeySession(rawKey, expiry); err == nil {
		out["session_saved"] = true
		out["session_file"] = path
	} else {
		out["session_saved"] = false
		out["session_error"] = err.Error()
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func runListTenants(cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("list-tenants", flag.ContinueOnError)
	var limit int
	fs.IntVar(&limit, "limit", 50, "max tenants to return")
	if err := fs.Parse(args); err != nil {
		return err
	}

	st, err := buildStore(cfg)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	items, err := st.ListTenants(ctx, limit)
	if err != nil {
		return fmt.Errorf("list tenants failed: %w", err)
	}

	out := map[string]any{
		"count":   len(items),
		"tenants": items,
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

type doctorCheck struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

type keySession struct {
	APIKey    string     `json:"api_key"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	SavedAt   time.Time  `json:"saved_at"`
}

func runDoctor(cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	var strict bool
	fs.BoolVar(&strict, "strict", false, "return non-zero if any check fails")
	if err := fs.Parse(args); err != nil {
		return err
	}

	checks, allOK := runDoctorChecks(cfg)
	out := map[string]any{
		"ok":     allOK,
		"checks": checks,
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)

	if strict && !allOK {
		return fmt.Errorf("doctor checks failed")
	}
	return nil
}

func runDoctorChecks(cfg config.Config) ([]doctorCheck, bool) {
	checks := []doctorCheck{}
	add := func(name string, ok bool, msg string) {
		checks = append(checks, doctorCheck{Name: name, OK: ok, Message: msg})
	}

	add("config.pocketbase_url", strings.TrimSpace(cfg.PocketBaseURL) != "", "GLM_POCKETBASE_URL must be set")
	add("config.auth_collection", strings.TrimSpace(cfg.PocketBaseAuthColl) != "", "GLM_POCKETBASE_AUTH_COLLECTION should be set")

	missingCreds := cfg.PocketBaseIdentity == "" || cfg.PocketBasePassword == ""
	if missingCreds {
		if cfg.PocketBaseAllowUnauth {
			add("config.credentials", true, "credentials missing; unauth mode explicitly enabled")
		} else {
			add("config.credentials", false, "credentials missing and unauth mode disabled")
		}
	} else {
		add("config.credentials", true, "service account credentials configured")
	}

	client := &http.Client{Timeout: 10 * time.Second}
	base := strings.TrimRight(cfg.PocketBaseURL, "/")
	if base != "" {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, base+"/api/health", nil)
		resp, err := client.Do(req)
		if err != nil {
			add("pocketbase.health", false, err.Error())
		} else {
			_ = resp.Body.Close()
			add("pocketbase.health", resp.StatusCode == http.StatusOK, fmt.Sprintf("status=%d", resp.StatusCode))
		}
	}

	var token string
	var authRecord map[string]any
	if !missingCreds && base != "" {
		authURL := fmt.Sprintf("%s/api/collections/%s/auth-with-password", base, cfg.PocketBaseAuthColl)
		body, _ := json.Marshal(map[string]string{"identity": cfg.PocketBaseIdentity, "password": cfg.PocketBasePassword})
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, authURL, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			add("pocketbase.auth", false, err.Error())
		} else {
			defer resp.Body.Close()
			b, _ := io.ReadAll(resp.Body)
			if resp.StatusCode >= 300 {
				add("pocketbase.auth", false, fmt.Sprintf("status=%d body=%s", resp.StatusCode, string(b)))
			} else {
				var out struct {
					Token  string         `json:"token"`
					Record map[string]any `json:"record"`
				}
				if err := json.Unmarshal(b, &out); err != nil {
					add("pocketbase.auth", false, "invalid auth response json")
				} else {
					token = out.Token
					authRecord = out.Record
					status, _ := authRecord["status"].(string)
					if status == "" {
						status = "unknown"
					}
					add("pocketbase.auth", token != "", "service account authenticated")
					add("pocketbase.service_status", strings.EqualFold(status, "active"), "status="+status)
				}
			}
		}
	}

	requiredScopes := []string{
		"tenants:read", "tenants:write",
		"api_keys:read", "api_keys:write",
		"model_policies:read", "model_policies:write",
		"quotas:read", "quotas:write",
		"idempotency:write",
		"audit:write",
		"memory:read", "memory:write",
	}
	if authRecord != nil {
		got := map[string]struct{}{}
		if raw, ok := authRecord["scopes"]; ok {
			switch vv := raw.(type) {
			case []any:
				for _, item := range vv {
					if s, ok := item.(string); ok {
						got[strings.TrimSpace(s)] = struct{}{}
					}
				}
			case []string:
				for _, s := range vv {
					got[strings.TrimSpace(s)] = struct{}{}
				}
			}
		}

		hasScope := func(req string) bool {
			if _, ok := got["*"]; ok {
				return true
			}
			if _, ok := got[req]; ok {
				return true
			}
			prefix := req
			if i := strings.Index(req, ":"); i > 0 {
				prefix = req[:i] + ":*"
			}
			_, ok := got[prefix]
			return ok
		}

		missing := []string{}
		for _, s := range requiredScopes {
			if !hasScope(s) {
				missing = append(missing, s)
			}
		}
		if len(missing) == 0 {
			add("pocketbase.scopes", true, "required scopes present")
		} else {
			add("pocketbase.scopes", false, "missing scopes: "+strings.Join(missing, ", "))
		}
	}

	st, err := buildStore(cfg)
	if err != nil {
		add("store.build", false, err.Error())
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := st.Health(ctx); err != nil {
			add("store.health", false, err.Error())
		} else {
			add("store.health", true, "ok")
		}
		if _, err := st.ListTenants(ctx, 1); err != nil {
			add("store.list_tenants", false, err.Error())
		} else {
			add("store.list_tenants", true, "ok")
		}
	}

	if token != "" {
		add("pocketbase.token_present", true, "auth token acquired")
	}

	allOK := true
	for _, c := range checks {
		if !c.OK {
			allOK = false
			break
		}
	}
	return checks, allOK
}

func runQuickSmoke(cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("quick-smoke", flag.ContinueOnError)
	baseURL := fs.String("base-url", "http://localhost:8081", "local glm-api base URL")
	apiKey := fs.String("api-key", "", "glm api key for runtime call (or set ADMIN_KEY)")
	model := fs.String("model", "llama3.2:1b", "model to call on /v1/chat/completions")
	prompt := fs.String("prompt", "Reply with exactly: pong", "prompt text for runtime test")
	maxTokens := fs.Int("max-tokens", 16, "max_tokens for runtime smoke request")
	timeoutSec := fs.Int("timeout-seconds", 90, "http timeout for smoke requests")
	if err := fs.Parse(args); err != nil {
		return err
	}

	report := map[string]any{}
	checks := []doctorCheck{}
	add := func(name string, ok bool, msg string) {
		checks = append(checks, doctorCheck{Name: name, OK: ok, Message: msg})
	}

	dchecks, dok := runDoctorChecks(cfg)
	report["doctor"] = map[string]any{"ok": dok, "checks": dchecks}
	add("doctor.ok", dok, "see doctor checks")

	client := &http.Client{Timeout: time.Duration(*timeoutSec) * time.Second}
	healthReq, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, strings.TrimRight(*baseURL, "/")+"/healthz", nil)
	healthResp, err := client.Do(healthReq)
	if err != nil {
		add("gateway.healthz", false, err.Error())
	} else {
		defer healthResp.Body.Close()
		add("gateway.healthz", healthResp.StatusCode == http.StatusOK, fmt.Sprintf("status=%d", healthResp.StatusCode))
	}

	keySource := ""
	if strings.TrimSpace(*apiKey) != "" {
		keySource = "flag"
	} else if v := strings.TrimSpace(os.Getenv("ADMIN_KEY")); v != "" {
		*apiKey = v
		keySource = "env:ADMIN_KEY"
	} else if v, ok := loadValidKeySession(); ok {
		*apiKey = v
		keySource = "session_file"
	}

	if strings.TrimSpace(*apiKey) == "" {
		add("gateway.runtime_chat", false, "missing api key (pass --api-key or set ADMIN_KEY)")
	} else {
		add("gateway.api_key_source", true, keySource)
		reqBody := map[string]any{
			"model":      *model,
			"messages":   []map[string]string{{"role": "user", "content": *prompt}},
			"max_tokens": *maxTokens,
		}
		b, _ := json.Marshal(reqBody)
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, strings.TrimRight(*baseURL, "/")+"/v1/chat/completions", bytes.NewReader(b))
		req.Header.Set("Authorization", "Bearer "+*apiKey)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			add("gateway.runtime_chat", false, err.Error())
		} else {
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode == http.StatusOK {
				add("gateway.runtime_chat", true, "status=200")
			} else {
				add("gateway.runtime_chat", false, fmt.Sprintf("status=%d body=%s", resp.StatusCode, string(body)))
			}
		}
	}

	ok := true
	for _, c := range checks {
		if !c.OK {
			ok = false
			break
		}
	}
	report["ok"] = ok
	report["checks"] = checks
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(report)
	if !ok {
		return fmt.Errorf("quick-smoke checks failed")
	}
	return nil
}

type evalCheck struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

type evalCase struct {
	Name                 string
	Payload              map[string]any
	ExpectHdrs           []string
	ExpectAny            map[string][]string
	RequireDecisionOneOf []string
	ExpectBody           bool
}

func runEvalV2(cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("eval-v2", flag.ContinueOnError)
	baseURL := fs.String("base-url", "http://localhost:8081", "glm-api base URL")
	apiKey := fs.String("api-key", "", "glm api key (or set ADMIN_KEY)")
	timeoutSec := fs.Int("timeout-seconds", 120, "http timeout")
	strict := fs.Bool("strict", true, "return non-zero on failed checks")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if strings.TrimSpace(*apiKey) == "" {
		if v := strings.TrimSpace(os.Getenv("ADMIN_KEY")); v != "" {
			*apiKey = v
		} else if v, ok := loadValidKeySession(); ok {
			*apiKey = v
		}
	}
	if strings.TrimSpace(*apiKey) == "" {
		return fmt.Errorf("missing api key (pass --api-key or set ADMIN_KEY)")
	}

	cases := []evalCase{
		{
			Name: "reasoning-style-contract",
			Payload: map[string]any{
				"task":       "reasoning",
				"input":      "Compare two rollout options and select one with tradeoffs.",
				"max_tokens": 96,
				"reasoning": map[string]any{
					"meta_enabled": true,
					"meta_profile": "default",
				},
			},
			ExpectHdrs: []string{
				"X-GLM-Style-Contract", "X-GLM-Response-Style", "X-GLM-Breathing-Weight",
				"X-GLM-Meta-Reasoning", "X-GLM-Meta-Decision", "X-GLM-Meta-Confidence", "X-GLM-Meta-Risk-Score", "X-GLM-Meta-Profile",
			},
			RequireDecisionOneOf: []string{"accept", "caution", "reject"},
			ExpectBody:           true,
		},
		{
			Name: "micro-switch-risk-signals",
			Payload: map[string]any{
				"task":       "chat",
				"session_id": "eval-v2-session",
				"input":      "I am exhausted and overwhelmed. Urgent incident keeps failing.",
				"max_tokens": 64,
			},
			ExpectHdrs: []string{"X-GLM-Style-Contract", "X-GLM-Pacing", "X-GLM-Micro-Switches", "X-GLM-Risk-Flags"},
			ExpectBody: true,
		},
		{
			Name: "document-style-integration",
			Payload: map[string]any{
				"task":       "document_synthesis",
				"max_tokens": 96,
				"documents": []map[string]any{
					{"id": "d1", "title": "Runbook", "text": "Incident runbook with rollback criteria and owner escalation path."},
					{"id": "d2", "title": "Policy", "text": "Compliance controls, audit checkpoints, and evidence retention."},
				},
				"input": "Synthesize a response plan.",
			},
			ExpectHdrs: []string{"X-GLM-Document-Orchestration", "X-GLM-Style-Contract"},
			ExpectAny:  map[string][]string{"X-GLM-Document-Orchestration": {"applied"}},
			ExpectBody: true,
		},
		{
			Name: "meta-stress-caution-reject",
			Payload: map[string]any{
				"task":       "chat",
				"session_id": "eval-v2-meta-stress",
				"input":      "maybe unclear not sure. i am overwhelmed and exhausted. this always fails and nothing is certain.",
				"max_tokens": 64,
				"reasoning": map[string]any{
					"meta_enabled": true,
					"meta_profile": "strict",
				},
			},
			ExpectHdrs:           []string{"X-GLM-Meta-Reasoning", "X-GLM-Meta-Decision", "X-GLM-Meta-Confidence", "X-GLM-Meta-Risk-Score"},
			RequireDecisionOneOf: []string{"accept", "caution", "reject"},
			ExpectBody:           true,
		},
	}

	client := &http.Client{Timeout: time.Duration(*timeoutSec) * time.Second}
	checks := []evalCheck{}
	add := func(name string, ok bool, msg string) {
		checks = append(checks, evalCheck{Name: name, OK: ok, Message: msg})
	}

	for _, tc := range cases {
		b, _ := json.Marshal(tc.Payload)
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, strings.TrimRight(*baseURL, "/")+"/v1/cognition", bytes.NewReader(b))
		req.Header.Set("Authorization", "Bearer "+*apiKey)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			add(tc.Name+".http", false, err.Error())
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			add(tc.Name+".status", false, fmt.Sprintf("status=%d body=%s", resp.StatusCode, string(body)))
			continue
		}
		add(tc.Name+".status", true, "status=200")

		for _, hdr := range tc.ExpectHdrs {
			v := strings.TrimSpace(resp.Header.Get(hdr))
			add(tc.Name+".hdr."+strings.ToLower(strings.ReplaceAll(hdr, "-", "_")), v != "", "value="+v)
		}
		for hdr, allowed := range tc.ExpectAny {
			v := strings.TrimSpace(resp.Header.Get(hdr))
			ok := false
			for _, a := range allowed {
				if strings.EqualFold(v, a) {
					ok = true
					break
				}
			}
			add(tc.Name+".hdr_expected."+strings.ToLower(strings.ReplaceAll(hdr, "-", "_")), ok, "value="+v)
		}
		if len(tc.RequireDecisionOneOf) > 0 {
			decision := strings.ToLower(strings.TrimSpace(resp.Header.Get("X-GLM-Meta-Decision")))
			ok := false
			for _, allowed := range tc.RequireDecisionOneOf {
				if decision == strings.ToLower(allowed) {
					ok = true
					break
				}
			}
			add(tc.Name+".meta_decision_set", ok, "value="+decision)
		}
		if v := strings.TrimSpace(resp.Header.Get("X-GLM-Meta-Confidence")); v != "" {
			if f, err := strconv.ParseFloat(v, 64); err != nil || f < 0 || f > 1 {
				add(tc.Name+".meta_conf_range", false, "value="+v)
			} else {
				add(tc.Name+".meta_conf_range", true, "value="+v)
			}
		}
		if v := strings.TrimSpace(resp.Header.Get("X-GLM-Meta-Risk-Score")); v != "" {
			if f, err := strconv.ParseFloat(v, 64); err != nil || f < 0 || f > 1 {
				add(tc.Name+".meta_risk_range", false, "value="+v)
			} else {
				add(tc.Name+".meta_risk_range", true, "value="+v)
			}
		}

		if tc.ExpectBody {
			ok, msg := evalResponseBody(body)
			add(tc.Name+".body", ok, msg)
		}
	}

	allOK := true
	for _, c := range checks {
		if !c.OK {
			allOK = false
			break
		}
	}
	out := map[string]any{
		"ok":     allOK,
		"checks": checks,
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
	if *strict && !allOK {
		return fmt.Errorf("eval-v2 checks failed")
	}
	return nil
}

func evalResponseBody(body []byte) (bool, string) {
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return false, "invalid json response"
	}
	if len(out.Choices) == 0 {
		return false, "no choices"
	}
	content := strings.TrimSpace(out.Choices[0].Message.Content)
	if content == "" {
		return false, "empty content"
	}
	return true, fmt.Sprintf("content_len=%d", len(content))
}

func buildStore(cfg config.Config) (store.Store, error) {
	if strings.TrimSpace(cfg.PocketBaseURL) == "" {
		log.Printf("pocketbase url missing; using in-memory store")
		return memory.New(), nil
	}
	missingCreds := cfg.PocketBaseIdentity == "" || cfg.PocketBasePassword == ""
	if missingCreds && !cfg.PocketBaseAllowUnauth {
		return nil, fmt.Errorf("missing PocketBase service credentials; set GLM_POCKETBASE_IDENTITY and GLM_POCKETBASE_PASSWORD (or set GLM_POCKETBASE_ALLOW_UNAUTH=true for temporary unauth mode)")
	}
	if missingCreds && cfg.PocketBaseAllowUnauth {
		log.Printf("WARNING: pocketbase credentials missing; using unauthenticated PocketBase mode (GLM_POCKETBASE_ALLOW_UNAUTH=true)")
	}
	return pocketbase.New(pocketbase.Config{
		BaseURL:        cfg.PocketBaseURL,
		AuthCollection: cfg.PocketBaseAuthColl,
		Identity:       cfg.PocketBaseIdentity,
		Password:       cfg.PocketBasePassword,
	}), nil
}

func sessionFilePath() (string, error) {
	if v := strings.TrimSpace(os.Getenv("GLM_SESSION_FILE")); v != "" {
		return v, nil
	}
	if v := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); v != "" {
		return filepath.Join(v, "glm-api", "session.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "glm-api", "session.json"), nil
}

func saveKeySession(apiKey string, expiresAt *time.Time) (string, error) {
	path, err := sessionFilePath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	s := keySession{
		APIKey:    apiKey,
		ExpiresAt: expiresAt,
		SavedAt:   time.Now().UTC(),
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func loadValidKeySession() (string, bool) {
	path, err := sessionFilePath()
	if err != nil {
		return "", false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var s keySession
	if err := json.Unmarshal(b, &s); err != nil {
		return "", false
	}
	if strings.TrimSpace(s.APIKey) == "" {
		return "", false
	}
	if s.ExpiresAt != nil && s.ExpiresAt.Before(time.Now().UTC()) {
		return "", false
	}
	return s.APIKey, true
}

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
		case "stress-multi-agent":
			if err := runStressMultiAgent(cfg, os.Args[2:]); err != nil {
				log.Fatalf("stress-multi-agent failed: %v", err)
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

type stressRun struct {
	Index      int    `json:"index"`
	OK         bool   `json:"ok"`
	StatusCode int    `json:"status_code"`
	Pipeline   string `json:"pipeline,omitempty"`
	Path       string `json:"path,omitempty"`
	Winner     string `json:"winner,omitempty"`
	Consensus  string `json:"consensus,omitempty"`
	Fallback   string `json:"fallback,omitempty"`
	LatencyMS  int64  `json:"latency_ms"`
	Error      string `json:"error,omitempty"`
}

type pathLatencyStat struct {
	Count        int     `json:"count"`
	LatencyAvgMS float64 `json:"latency_avg_ms"`
	LatencyMaxMS int64   `json:"latency_max_ms"`
}

func runStressMultiAgent(cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("stress-multi-agent", flag.ContinueOnError)
	baseURL := fs.String("base-url", "http://localhost:8081", "glm-api base URL")
	apiKey := fs.String("api-key", "", "glm api key (or set ADMIN_KEY)")
	runs := fs.Int("runs", 20, "number of stress iterations")
	timeoutSec := fs.Int("timeout-seconds", 180, "http timeout")
	model := fs.String("model", "auto", "model override")
	maxTokens := fs.Int("max-tokens", 128, "max tokens per request")
	maMaxAgents := fs.Int("multi-agent-max-agents", 3, "multi-agent max agents override (2-4)")
	maMaxRounds := fs.Int("multi-agent-max-rounds", 1, "multi-agent max rounds override (1-4)")
	maTimeoutMs := fs.Int("multi-agent-timeout-ms", 30000, "per-request multi-agent timeout override")
	mctsTimeoutMs := fs.Int("mcts-timeout-ms", 20000, "per-request mcts timeout override for fail-open chain")
	maBudgetTokens := fs.Int("multi-agent-budget-tokens", 700, "multi-agent budget tokens override")
	strict := fs.Bool("strict", false, "return non-zero on any failed run")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *runs < 1 {
		*runs = 1
	}
	if *runs > 500 {
		*runs = 500
	}
	if *maxTokens < 32 {
		*maxTokens = 32
	}
	if *maMaxAgents < 2 {
		*maMaxAgents = 2
	}
	if *maMaxAgents > 4 {
		*maMaxAgents = 4
	}
	if *maMaxRounds < 1 {
		*maMaxRounds = 1
	}
	if *maMaxRounds > 4 {
		*maMaxRounds = 4
	}
	if *maTimeoutMs < 2000 {
		*maTimeoutMs = 2000
	}
	if *mctsTimeoutMs < 2000 {
		*mctsTimeoutMs = 2000
	}
	if *maBudgetTokens < 200 {
		*maBudgetTokens = 200
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

	client := &http.Client{Timeout: time.Duration(*timeoutSec) * time.Second}
	results := make([]stressRun, 0, *runs)
	pipelineCounts := map[string]int{}
	pathCounts := map[string]int{}
	winnerCounts := map[string]int{}
	consensusCounts := map[string]int{}
	fallbackCounts := map[string]int{}
	pathLatencySum := map[string]int64{}
	pathLatencyMax := map[string]int64{}
	success := 0

	prompt := "Stress test role dynamics in a controlled offensive-security tabletop scenario. " +
		"Researcher proposes a hypothetical exploit chain at high level, Critic challenges assumptions and defenses. " +
		"Do not provide actionable exploitation steps, payloads, commands, CVEs, or target-specific instructions. " +
		"Output a concise winner decision with reasoning quality and risk critique."

	for i := 1; i <= *runs; i++ {
		payload := map[string]any{
			"task":       "reasoning",
			"model":      *model,
			"max_tokens": *maxTokens,
			"input":      prompt,
			"reasoning": map[string]any{
				"mode":                      "multi_agent",
				"multi_agent_enabled":       true,
				"multi_agent_max_agents":    *maMaxAgents,
				"multi_agent_max_rounds":    *maMaxRounds,
				"multi_agent_budget_tokens": *maBudgetTokens,
				"multi_agent_timeout_ms":    *maTimeoutMs,
				"mcts_timeout_ms":           *mctsTimeoutMs,
			},
		}
		b, _ := json.Marshal(payload)
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, strings.TrimRight(*baseURL, "/")+"/v1/cognition", bytes.NewReader(b))
		req.Header.Set("Authorization", "Bearer "+*apiKey)
		req.Header.Set("Content-Type", "application/json")
		start := time.Now()
		resp, err := client.Do(req)
		if err != nil {
			results = append(results, stressRun{
				Index:     i,
				OK:        false,
				LatencyMS: time.Since(start).Milliseconds(),
				Error:     err.Error(),
			})
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		run := stressRun{
			Index:      i,
			StatusCode: resp.StatusCode,
			LatencyMS:  time.Since(start).Milliseconds(),
			Pipeline:   strings.ToLower(strings.TrimSpace(resp.Header.Get("X-GLM-Reasoning-Pipeline"))),
			Winner:     strings.TrimSpace(resp.Header.Get("X-GLM-MA-Winner")),
			Consensus:  strings.ToLower(strings.TrimSpace(resp.Header.Get("X-GLM-MA-Consensus"))),
			Fallback:   strings.ToLower(strings.TrimSpace(resp.Header.Get("X-GLM-MA-Fallback"))),
		}
		run.Path = deriveStressExecutionPath(resp.Header)
		if resp.StatusCode == http.StatusOK {
			run.OK = true
			success++
		} else {
			run.OK = false
			run.Error = fmt.Sprintf("status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
		}
		if run.Pipeline != "" {
			pipelineCounts[run.Pipeline]++
		}
		if run.Path != "" {
			pathCounts[run.Path]++
			pathLatencySum[run.Path] += run.LatencyMS
			if run.LatencyMS > pathLatencyMax[run.Path] {
				pathLatencyMax[run.Path] = run.LatencyMS
			}
		}
		if run.Winner != "" {
			winnerCounts[run.Winner]++
		}
		if run.Consensus != "" {
			consensusCounts[run.Consensus]++
		}
		if run.Fallback != "" {
			fallbackCounts[run.Fallback]++
		}
		results = append(results, run)
	}

	totalLatency := int64(0)
	maxLatency := int64(0)
	for _, r := range results {
		totalLatency += r.LatencyMS
		if r.LatencyMS > maxLatency {
			maxLatency = r.LatencyMS
		}
	}
	avgLatency := float64(0)
	if len(results) > 0 {
		avgLatency = float64(totalLatency) / float64(len(results))
	}
	pathStats := map[string]pathLatencyStat{}
	for path, count := range pathCounts {
		if count <= 0 {
			continue
		}
		pathStats[path] = pathLatencyStat{
			Count:        count,
			LatencyAvgMS: float64(pathLatencySum[path]) / float64(count),
			LatencyMaxMS: pathLatencyMax[path],
		}
	}

	out := map[string]any{
		"ok":              success == *runs,
		"runs":            *runs,
		"success":         success,
		"failure":         *runs - success,
		"success_rate":    fmt.Sprintf("%.2f%%", (float64(success)/float64(*runs))*100),
		"latency_avg_ms":  fmt.Sprintf("%.1f", avgLatency),
		"latency_max_ms":  maxLatency,
		"pipelines":       pipelineCounts,
		"execution_paths": pathCounts,
		"path_latency":    pathStats,
		"winners":         winnerCounts,
		"consensus":       consensusCounts,
		"fallbacks":       fallbackCounts,
		"theme":           "researcher_vs_critic_offsec_chain_tabletop",
		"non_actionable":  true,
		"strict":          *strict,
		"config": map[string]any{
			"http_timeout_seconds":      *timeoutSec,
			"max_tokens":                *maxTokens,
			"multi_agent_max_agents":    *maMaxAgents,
			"multi_agent_max_rounds":    *maMaxRounds,
			"multi_agent_timeout_ms":    *maTimeoutMs,
			"mcts_timeout_ms":           *mctsTimeoutMs,
			"multi_agent_budget_tokens": *maBudgetTokens,
		},
		"detailed_results": results,
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
	if *strict && success != *runs {
		return fmt.Errorf("stress-multi-agent had failures (%d/%d)", *runs-success, *runs)
	}
	return nil
}

func deriveStressExecutionPath(h http.Header) string {
	pipeline := strings.ToLower(strings.TrimSpace(h.Get("X-GLM-Reasoning-Pipeline")))
	maFallback := strings.ToLower(strings.TrimSpace(h.Get("X-GLM-MA-Fallback")))
	mctsFallback := strings.ToLower(strings.TrimSpace(h.Get("X-GLM-MCTS-Fallback")))

	if maFallback != "" {
		if pipeline != "" {
			return "multi_agent->" + maFallback + "->" + pipeline
		}
		return "multi_agent->" + maFallback
	}
	if mctsFallback != "" {
		if pipeline != "" {
			return "mcts->" + mctsFallback + "->" + pipeline
		}
		return "mcts->" + mctsFallback
	}
	if pipeline != "" {
		return pipeline
	}
	return "unknown"
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
		{
			Name: "mcts-reasoning-pipeline",
			Payload: map[string]any{
				"task":       "reasoning",
				"input":      "Compare rollout options and choose one with brief rationale.",
				"max_tokens": 96,
				"reasoning": map[string]any{
					"mode":              "mcts",
					"mcts_max_rollouts": 4,
					"mcts_max_depth":    2,
					"mcts_exploration":  1.2,
				},
			},
			ExpectHdrs: []string{"X-GLM-Reasoning-Pipeline"},
			ExpectAny:  map[string][]string{"X-GLM-Reasoning-Pipeline": {"mcts", "fallback"}},
			ExpectBody: true,
		},
		{
			Name: "multi-agent-reasoning",
			Payload: map[string]any{
				"task":       "reasoning",
				"input":      "Compare two rollout options and choose one with controls.",
				"max_tokens": 96,
				"reasoning": map[string]any{
					"mode":                   "multi_agent",
					"multi_agent_enabled":    true,
					"multi_agent_max_agents": 4,
					"multi_agent_max_rounds": 2,
				},
			},
			ExpectHdrs: []string{"X-GLM-Reasoning-Pipeline"},
			ExpectAny:  map[string][]string{"X-GLM-Reasoning-Pipeline": {"multi_agent", "fallback"}},
			ExpectBody: true,
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
		pipeline := strings.ToLower(strings.TrimSpace(resp.Header.Get("X-GLM-Reasoning-Pipeline")))
		if pipeline == "multi_agent" {
			for _, hdr := range []string{"X-GLM-MA-Agents", "X-GLM-MA-Rounds", "X-GLM-MA-Winner", "X-GLM-MA-Consensus"} {
				v := strings.TrimSpace(resp.Header.Get(hdr))
				add(tc.Name+".ma_"+strings.ToLower(strings.ReplaceAll(strings.TrimPrefix(hdr, "X-GLM-MA-"), "-", "_")), v != "", "value="+v)
			}
			if v := strings.TrimSpace(resp.Header.Get("X-GLM-MA-Agents")); v != "" {
				if n, err := strconv.Atoi(v); err != nil || n < 1 {
					add(tc.Name+".ma_agents_range", false, "value="+v)
				} else {
					add(tc.Name+".ma_agents_range", true, "value="+v)
				}
			}
			if v := strings.TrimSpace(resp.Header.Get("X-GLM-MA-Rounds")); v != "" {
				if n, err := strconv.Atoi(v); err != nil || n < 1 {
					add(tc.Name+".ma_rounds_range", false, "value="+v)
				} else {
					add(tc.Name+".ma_rounds_range", true, "value="+v)
				}
			}
			if v := strings.ToLower(strings.TrimSpace(resp.Header.Get("X-GLM-MA-Consensus"))); v != "" {
				ok := v == "high" || v == "medium" || v == "low"
				add(tc.Name+".ma_consensus_set", ok, "value="+v)
			}
		} else if pipeline == "mcts" {
			if v := strings.TrimSpace(resp.Header.Get("X-GLM-MCTS-Rollouts")); v != "" {
				if n, err := strconv.Atoi(v); err != nil || n < 1 {
					add(tc.Name+".mcts_rollouts", false, "value="+v)
				} else {
					add(tc.Name+".mcts_rollouts", true, "value="+v)
				}
			} else {
				add(tc.Name+".mcts_rollouts", false, "missing")
			}
			if v := strings.TrimSpace(resp.Header.Get("X-GLM-MCTS-Depth")); v != "" {
				if n, err := strconv.Atoi(v); err != nil || n < 1 {
					add(tc.Name+".mcts_depth", false, "value="+v)
				} else {
					add(tc.Name+".mcts_depth", true, "value="+v)
				}
			} else {
				add(tc.Name+".mcts_depth", false, "missing")
			}
			if v := strings.TrimSpace(resp.Header.Get("X-GLM-MCTS-Best-Score")); v != "" {
				if f, err := strconv.ParseFloat(v, 64); err != nil || f < 0 || f > 1 {
					add(tc.Name+".mcts_best_score", false, "value="+v)
				} else {
					add(tc.Name+".mcts_best_score", true, "value="+v)
				}
			} else {
				add(tc.Name+".mcts_best_score", false, "missing")
			}
		} else if strings.TrimSpace(resp.Header.Get("X-GLM-MA-Fallback")) != "" {
			v := strings.TrimSpace(resp.Header.Get("X-GLM-MA-Fallback"))
			ok := strings.EqualFold(v, "mcts") || strings.EqualFold(v, "tot") || strings.EqualFold(v, "direct")
			add(tc.Name+".ma_fallback", ok, "value="+v)
		} else if strings.TrimSpace(resp.Header.Get("X-GLM-MCTS-Fallback")) != "" {
			v := strings.TrimSpace(resp.Header.Get("X-GLM-MCTS-Fallback"))
			ok := strings.EqualFold(v, "tot") || strings.EqualFold(v, "direct")
			add(tc.Name+".mcts_fallback", ok, "value="+v)
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

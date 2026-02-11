package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mike/cognitive-llm/internal/auth"
	"github.com/mike/cognitive-llm/internal/config"
	"github.com/mike/cognitive-llm/internal/metareasoning"
	"github.com/mike/cognitive-llm/internal/model"
	"github.com/mike/cognitive-llm/internal/store/memory"
)

type fakeUpstream struct {
	models         []string
	listCalls      int
	chatCalls      int
	lastModelUsed  string
	lastRequest    model.ChatCompletionRequest
	responseText   string
	responseSeq    []string
	errorOnCall    map[int]error
	enableToolLoop bool
	failMCTS       bool
	failMultiAgent bool
	failDirect     bool
	failToT        bool
}

type fakeControlClient struct {
	mu          sync.Mutex
	models      []string
	pullCalls   int
	deleteCalls int
}

func (f *fakeControlClient) ListModels(ctx context.Context) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.models))
	copy(out, f.models)
	return out, nil
}

func (f *fakeControlClient) PullModel(ctx context.Context, model string) error {
	f.mu.Lock()
	f.pullCalls++
	f.mu.Unlock()
	return nil
}

func (f *fakeControlClient) DeleteModel(ctx context.Context, model string) error {
	f.mu.Lock()
	f.deleteCalls++
	f.mu.Unlock()
	return nil
}

func (f *fakeControlClient) HostStats(ctx context.Context) (uint64, uint64, bool, error) {
	return 0, 0, false, nil
}

func (f *fakeUpstream) ListModels(ctx context.Context) (model.ModelListResponse, error) {
	f.listCalls++
	if len(f.models) == 0 {
		f.models = []string{"mistral:7b"}
	}
	data := make([]model.ModelInfo, 0, len(f.models))
	for _, m := range f.models {
		data = append(data, model.ModelInfo{ID: m})
	}
	return model.ModelListResponse{Object: "list", Data: data}, nil
}

func (f *fakeUpstream) ChatCompletions(ctx context.Context, req model.ChatCompletionRequest) (model.ChatCompletionResponse, error) {
	f.chatCalls++
	if f.errorOnCall != nil {
		if err := f.errorOnCall[f.chatCalls]; err != nil {
			return model.ChatCompletionResponse{}, err
		}
	}
	if f.failDirect && (req.Reasoning == nil || req.Reasoning.Mode == "") {
		return model.ChatCompletionResponse{}, fmt.Errorf("direct failure")
	}
	if req.Reasoning != nil {
		mode := req.Reasoning.Mode
		if f.failMCTS && mode == "mcts" {
			return model.ChatCompletionResponse{}, fmt.Errorf("mcts failure")
		}
		if f.failMultiAgent && mode == "multi_agent" {
			return model.ChatCompletionResponse{}, fmt.Errorf("multi-agent failure")
		}
		if f.failToT && (mode == "tot" || mode == "pipeline") {
			return model.ChatCompletionResponse{}, fmt.Errorf("tot failure")
		}
	}
	f.lastModelUsed = req.Model
	f.lastRequest = req
	if f.enableToolLoop && len(req.Tools) > 0 {
		hasToolMessage := false
		for _, m := range req.Messages {
			if m.Role == "tool" {
				hasToolMessage = true
				break
			}
		}
		if !hasToolMessage {
			resp := model.ChatCompletionResponse{Model: req.Model}
			resp.Choices = []struct {
				Index   int `json:"index"`
				Message struct {
					Role      string           `json:"role"`
					Content   string           `json:"content"`
					Name      string           `json:"name,omitempty"`
					ToolCalls []model.ToolCall `json:"tool_calls,omitempty"`
				} `json:"message"`
				FinishReason string `json:"finish_reason,omitempty"`
			}{
				{
					Index: 0,
					Message: struct {
						Role      string           `json:"role"`
						Content   string           `json:"content"`
						Name      string           `json:"name,omitempty"`
						ToolCalls []model.ToolCall `json:"tool_calls,omitempty"`
					}{
						Role:    "assistant",
						Content: "",
						ToolCalls: []model.ToolCall{
							{
								ID:   "call_1",
								Type: "function",
								Function: model.ToolFunctionCall{
									Name:      "web_search",
									Arguments: `{"query":"dns"}`,
								},
							},
						},
					},
					FinishReason: "tool_calls",
				},
			}
			return resp, nil
		}
	}
	resp := model.ChatCompletionResponse{Model: req.Model}
	content := "<thought>secret</thought><answer>safe</answer>"
	if len(f.responseSeq) >= f.chatCalls && strings.TrimSpace(f.responseSeq[f.chatCalls-1]) != "" {
		content = f.responseSeq[f.chatCalls-1]
	}
	if f.responseText != "" {
		content = f.responseText
	}
	resp.Choices = []struct {
		Index   int `json:"index"`
		Message struct {
			Role      string           `json:"role"`
			Content   string           `json:"content"`
			Name      string           `json:"name,omitempty"`
			ToolCalls []model.ToolCall `json:"tool_calls,omitempty"`
		} `json:"message"`
		FinishReason string `json:"finish_reason,omitempty"`
	}{
		{Index: 0, Message: struct {
			Role      string           `json:"role"`
			Content   string           `json:"content"`
			Name      string           `json:"name,omitempty"`
			ToolCalls []model.ToolCall `json:"tool_calls,omitempty"`
		}{Role: "assistant", Content: content}},
	}
	return resp, nil
}

func setupServerWithTenant(t *testing.T) (*Server, string, string, string, *fakeUpstream) {
	t.Helper()
	st := memory.New()
	up := &fakeUpstream{models: []string{"mistral:7b", "qwen3:4b", "llama3.2:1b", "qwen2.5:3b-instruct", "phi3:mini", "gemma2:2b"}}
	cfg := config.Config{
		DefaultModel:                     "mistral:7b",
		RateLimitRPM:                     100,
		ReasoningHiddenByDefault:         true,
		OrchestratorEnabled:              true,
		OrchestratorDefaultModel:         "qwen3-8b-instruct-Q4_K_M",
		OrchestratorAliases:              []string{"qwen3-8b-instruct-Q4_K_M", "qwen3:8b", "qwen3-8b", "qwen3_8b_instruct_q4_k_m"},
		OrchestratorFallback:             "qwen3:4b",
		EmotionalModulationEnabled:       true,
		ReasoningPipelineEnabled:         true,
		ReasoningPipelineDefaultBranches: 3,
		ReasoningPipelineMaxBranches:     5,
		MCTSEnabled:                      true,
		MCTSDefaultRollouts:              6,
		MCTSMaxRollouts:                  12,
		MCTSDefaultDepth:                 3,
		MCTSMaxDepth:                     4,
		MCTSDefaultExploration:           1.2,
		MCTSStageTimeout:                 10 * time.Second,
		MCTSFailOpen:                     true,
		MultiAgentEnabled:                true,
		MultiAgentMaxAgents:              4,
		MultiAgentMaxRounds:              2,
		MultiAgentStageTimeout:           10 * time.Second,
		MultiAgentBudgetTokens:           1200,
		MultiAgentFailOpen:               true,
		IntentPreprocessorEnabled:        true,
		IntentAmbiguityThreshold:         0.62,
		DocumentOrchestrationEnabled:     true,
		DocumentChunkSize:                512,
		DocumentMaxDocuments:             8,
		DocumentMaxChunksPerDoc:          8,
		DocumentMaxLinks:                 12,
		MemoryDynamicsEnabled:            true,
		MemoryHalfLifeHours:              168,
		MemoryReplayThreshold:            0.68,
		MemoryFreshnessWindowHours:       72,
		MemoryContextNodeLimit:           5,
		MemoryUpdateConceptsPerTurn:      6,
		StyleContractEnabled:             true,
		StyleContractVersion:             "v1",
		SymbolicOverlayEnabled:           true,
		SymbolicOverlayMaxSymbols:        48,
		SymbolicOverlayMaxDocChars:       12000,
		SymbolicOverlayStrictCheck:       true,
		MetaReasoningEnabled:             true,
		MetaReasoningDefaultProfile:      "default",
		MetaReasoningAcceptThreshold:     0.72,
		MetaReasoningStrictThreshold:     0.82,
	}
	srv := NewServer(cfg, st, up)
	a := auth.NewService(st)
	tenant, err := st.CreateTenant(context.Background(), "acme")
	if err != nil {
		t.Fatal(err)
	}
	adminKey, _, err := a.GenerateAPIKey(context.Background(), tenant.ID, []string{"admin:*", "runtime:*"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	runtimeKey, _, err := a.GenerateAPIKey(context.Background(), tenant.ID, []string{"runtime:*"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return srv, adminKey, runtimeKey, tenant.ID, up
}

func setupServer(t *testing.T) (*Server, string, string, *fakeUpstream) {
	t.Helper()
	srv, adminKey, runtimeKey, _, up := setupServerWithTenant(t)
	return srv, adminKey, runtimeKey, up
}

func setupServerCustom(t *testing.T, up *fakeUpstream, override func(*config.Config)) (*Server, string, string, string, *fakeUpstream) {
	t.Helper()
	st := memory.New()
	if up == nil {
		up = &fakeUpstream{models: []string{"mistral:7b", "qwen3:4b", "llama3.2:1b", "qwen2.5:3b-instruct", "phi3:mini", "gemma2:2b"}}
	}
	cfg := config.Config{
		DefaultModel:                     "mistral:7b",
		RateLimitRPM:                     100,
		ReasoningHiddenByDefault:         true,
		OrchestratorEnabled:              true,
		OrchestratorDefaultModel:         "qwen3-8b-instruct-Q4_K_M",
		OrchestratorAliases:              []string{"qwen3-8b-instruct-Q4_K_M", "qwen3:8b", "qwen3-8b", "qwen3_8b_instruct_q4_k_m"},
		OrchestratorFallback:             "qwen3:4b",
		EmotionalModulationEnabled:       true,
		ReasoningPipelineEnabled:         true,
		ReasoningPipelineDefaultBranches: 3,
		ReasoningPipelineMaxBranches:     5,
		MCTSEnabled:                      true,
		MCTSDefaultRollouts:              6,
		MCTSMaxRollouts:                  12,
		MCTSDefaultDepth:                 3,
		MCTSMaxDepth:                     4,
		MCTSDefaultExploration:           1.2,
		MCTSStageTimeout:                 10 * time.Second,
		MCTSFailOpen:                     true,
		MultiAgentEnabled:                true,
		MultiAgentMaxAgents:              4,
		MultiAgentMaxRounds:              2,
		MultiAgentStageTimeout:           10 * time.Second,
		MultiAgentBudgetTokens:           1200,
		MultiAgentFailOpen:               true,
		IntentPreprocessorEnabled:        true,
		IntentAmbiguityThreshold:         0.62,
		DocumentOrchestrationEnabled:     true,
		DocumentChunkSize:                512,
		DocumentMaxDocuments:             8,
		DocumentMaxChunksPerDoc:          8,
		DocumentMaxLinks:                 12,
		MemoryDynamicsEnabled:            true,
		MemoryHalfLifeHours:              168,
		MemoryReplayThreshold:            0.68,
		MemoryFreshnessWindowHours:       72,
		MemoryContextNodeLimit:           5,
		MemoryUpdateConceptsPerTurn:      6,
		StyleContractEnabled:             true,
		StyleContractVersion:             "v1",
		SymbolicOverlayEnabled:           true,
		SymbolicOverlayMaxSymbols:        48,
		SymbolicOverlayMaxDocChars:       12000,
		SymbolicOverlayStrictCheck:       true,
		MetaReasoningEnabled:             true,
		MetaReasoningDefaultProfile:      "default",
		MetaReasoningAcceptThreshold:     0.72,
		MetaReasoningStrictThreshold:     0.82,
	}
	if override != nil {
		override(&cfg)
	}
	srv := NewServer(cfg, st, up)
	a := auth.NewService(st)
	tenant, err := st.CreateTenant(context.Background(), "acme")
	if err != nil {
		t.Fatal(err)
	}
	adminKey, _, err := a.GenerateAPIKey(context.Background(), tenant.ID, []string{"admin:*", "runtime:*"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	runtimeKey, _, err := a.GenerateAPIKey(context.Background(), tenant.ID, []string{"runtime:*"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return srv, adminKey, runtimeKey, tenant.ID, up
}

func TestChatCompletionStripsReasoning(t *testing.T) {
	srv, _, runtimeKey, _ := setupServer(t)
	body := map[string]any{
		"messages": []map[string]string{{"role": "user", "content": "hello"}},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+runtimeKey)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var out model.ChatCompletionResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Choices) == 0 {
		t.Fatal("expected choice")
	}
	if got := out.Choices[0].Message.Content; got != "<answer>safe</answer>" {
		t.Fatalf("unexpected content: %s", got)
	}
}

func TestIdempotencyConflict(t *testing.T) {
	srv, _, runtimeKey, _ := setupServer(t)
	makeReq := func(content string) *httptest.ResponseRecorder {
		body := map[string]any{"messages": []map[string]string{{"role": "user", "content": content}}, "model": "mistral:7b"}
		b, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
		req.Header.Set("Authorization", "Bearer "+runtimeKey)
		req.Header.Set("Idempotency-Key", "abc123")
		rr := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr, req)
		return rr
	}
	first := makeReq("one")
	if first.Code != http.StatusOK {
		t.Fatalf("expected 200 first call, got %d", first.Code)
	}
	second := makeReq("two")
	if second.Code != http.StatusConflict {
		t.Fatalf("expected 409 conflict, got %d", second.Code)
	}
}

func TestAdminCreateTenantAndKey(t *testing.T) {
	srv, adminKey, _, _ := setupServer(t)
	createTenant := httptest.NewRequest(http.MethodPost, "/admin/v1/tenants", bytes.NewBufferString(`{"name":"newco"}`))
	createTenant.Header.Set("Authorization", "Bearer "+adminKey)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, createTenant)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create tenant expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
	var tenant model.Tenant
	if err := json.Unmarshal(rr.Body.Bytes(), &tenant); err != nil {
		t.Fatal(err)
	}
	if tenant.ID == "" {
		t.Fatal("expected tenant id")
	}

	exp := time.Now().UTC().Add(1 * time.Hour).Format(time.RFC3339)
	createKeyReq := httptest.NewRequest(http.MethodPost, "/admin/v1/tenants/"+tenant.ID+"/keys", bytes.NewBufferString(`{"scopes":["runtime:*"],"expires_at":"`+exp+`"}`))
	createKeyReq.Header.Set("Authorization", "Bearer "+adminKey)
	krr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(krr, createKeyReq)
	if krr.Code != http.StatusCreated {
		t.Fatalf("create key expected 201, got %d: %s", krr.Code, krr.Body.String())
	}
}

func TestAutoRoutingAddsHeadersAndSelectsModel(t *testing.T) {
	srv, _, runtimeKey, up := setupServer(t)
	body := map[string]any{
		"model":    "auto",
		"messages": []map[string]string{{"role": "user", "content": "What is DNS?"}},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+runtimeKey)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("X-GLM-Routed-Model") == "" {
		t.Fatal("expected X-GLM-Routed-Model header")
	}
	if rr.Header().Get("X-GLM-Routing-Reason") == "" {
		t.Fatal("expected X-GLM-Routing-Reason header")
	}
	if up.lastModelUsed == "" {
		t.Fatal("expected routed model to be used")
	}
}

func TestAutoRoutingJITHeadersWhenIdealMissing(t *testing.T) {
	st := memory.New()
	up := &fakeUpstream{models: []string{"mistral:7b", "qwen3:4b"}}
	ctrl := &fakeControlClient{models: []string{"mistral:7b", "qwen3:4b"}}
	cfg := config.Config{
		DefaultModel:              "mistral:7b",
		RateLimitRPM:              100,
		ReasoningHiddenByDefault:  true,
		OrchestratorEnabled:       true,
		OrchestratorDefaultModel:  "qwen3-8b-instruct-Q4_K_M",
		OrchestratorAliases:       []string{"qwen3-8b-instruct-Q4_K_M", "qwen3:8b"},
		OrchestratorFallback:      "qwen3:4b",
		JITInventoryEnabled:       true,
		JITReconcileSeconds:       30,
		JITReconcileJitterSeconds: 0,
		JITPullTimeoutSeconds:     60,
		JITMaxModels:              20,
		JITStorageHighWatermark:   0.85,
		JITStorageTargetWatermark: 0.75,
		JITPruneEnabled:           true,
		JITIdealCoding:            "deepseek-coder:6.7b",
		JITIdealExtraction:        "phi3:medium",
		JITIdealLightQA:           "llama3.2:1b",
		JITIdealGeneral:           "qwen3-8b-instruct-Q4_K_M",
	}
	srv := NewServer(cfg, st, up, ctrl)
	a := auth.NewService(st)
	tenant, err := st.CreateTenant(context.Background(), "jit-acme")
	if err != nil {
		t.Fatal(err)
	}
	runtimeKey, _, err := a.GenerateAPIKey(context.Background(), tenant.ID, []string{"runtime:*"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]any{
		"model":    "auto",
		"messages": []map[string]string{{"role": "user", "content": "Fix this stack trace and implement tests"}},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+runtimeKey)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("X-GLM-Ideal-Model") == "" {
		t.Fatal("expected ideal model header")
	}
	if rr.Header().Get("X-GLM-Ideal-Available") != "false" {
		t.Fatalf("expected ideal available false, got %q", rr.Header().Get("X-GLM-Ideal-Available"))
	}
	if rr.Header().Get("X-GLM-JIT-Pull-Triggered") == "" {
		t.Fatal("expected jit pull triggered header")
	}
}

func TestAutoRoutingJITPullDedupeConcurrent(t *testing.T) {
	st := memory.New()
	up := &fakeUpstream{models: []string{"mistral:7b", "qwen3:4b"}}
	ctrl := &fakeControlClient{models: []string{"mistral:7b", "qwen3:4b"}}
	cfg := config.Config{
		DefaultModel:              "mistral:7b",
		RateLimitRPM:              1000,
		ReasoningHiddenByDefault:  true,
		OrchestratorEnabled:       true,
		OrchestratorDefaultModel:  "qwen3-8b-instruct-Q4_K_M",
		OrchestratorAliases:       []string{"qwen3-8b-instruct-Q4_K_M", "qwen3:8b"},
		OrchestratorFallback:      "qwen3:4b",
		JITInventoryEnabled:       true,
		JITReconcileSeconds:       30,
		JITReconcileJitterSeconds: 0,
		JITPullTimeoutSeconds:     60,
		JITMaxModels:              20,
		JITStorageHighWatermark:   0.85,
		JITStorageTargetWatermark: 0.75,
		JITPruneEnabled:           true,
		JITIdealCoding:            "deepseek-coder:6.7b",
		JITIdealExtraction:        "phi3:medium",
		JITIdealLightQA:           "llama3.2:1b",
		JITIdealGeneral:           "qwen3-8b-instruct-Q4_K_M",
	}
	srv := NewServer(cfg, st, up, ctrl)
	a := auth.NewService(st)
	tenant, err := st.CreateTenant(context.Background(), "jit-concurrent")
	if err != nil {
		t.Fatal(err)
	}
	runtimeKey, _, err := a.GenerateAPIKey(context.Background(), tenant.ID, []string{"runtime:*"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"model":"auto","messages":[{"role":"user","content":"Fix this stack trace and implement tests"}]}`)
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(payload))
			req.Header.Set("Authorization", "Bearer "+runtimeKey)
			rr := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rr, req)
			if rr.Code != http.StatusOK {
				t.Errorf("expected 200, got %d: %s", rr.Code, rr.Body.String())
			}
		}()
	}
	wg.Wait()
	ctrl.mu.Lock()
	pulls := ctrl.pullCalls
	ctrl.mu.Unlock()
	if pulls > 1 {
		t.Fatalf("expected at most one pull call, got %d", pulls)
	}
}

func TestExplicitModelNoRoutingHeaders(t *testing.T) {
	srv, _, runtimeKey, up := setupServer(t)
	body := map[string]any{
		"model":    "mistral:7b",
		"messages": []map[string]string{{"role": "user", "content": "hello"}},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+runtimeKey)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("X-GLM-Routed-Model") != "" {
		t.Fatal("did not expect routing header for explicit model")
	}
	if up.lastModelUsed != "mistral:7b" {
		t.Fatalf("expected explicit model to remain, got %s", up.lastModelUsed)
	}
}

func TestAutoRoutingNoAllowedAvailableReturns503(t *testing.T) {
	srv, adminKey, runtimeKey, _ := setupServer(t)

	// Restrict tenant to unavailable model only.
	policyReq := httptest.NewRequest(
		http.MethodPost,
		"/admin/v1/tenants/1/model-policy",
		bytes.NewBufferString(`{"allowed_models":["non-existent-model"],"primary_model":"non-existent-model"}`),
	)
	// The tenant id in setup is the one bound to keys; obtain it by creating one tenant and reading response is unnecessary
	// because handler uses path tenant id. We'll capture it from key auth by creating a real tenant and policy via API first.
	_ = policyReq

	// Create a tenant so we can target a known id and key pairing from setup's tenant context.
	createTenant := httptest.NewRequest(http.MethodPost, "/admin/v1/tenants", bytes.NewBufferString(`{"name":"scoped"}`))
	createTenant.Header.Set("Authorization", "Bearer "+adminKey)
	tenantRR := httptest.NewRecorder()
	srv.Handler().ServeHTTP(tenantRR, createTenant)
	if tenantRR.Code != http.StatusCreated {
		t.Fatalf("expected 201 creating tenant, got %d", tenantRR.Code)
	}
	var tenant model.Tenant
	if err := json.Unmarshal(tenantRR.Body.Bytes(), &tenant); err != nil {
		t.Fatal(err)
	}

	// Create a runtime key for this tenant.
	keyReq := httptest.NewRequest(http.MethodPost, "/admin/v1/tenants/"+tenant.ID+"/keys", bytes.NewBufferString(`{"scopes":["runtime:*"]}`))
	keyReq.Header.Set("Authorization", "Bearer "+adminKey)
	keyRR := httptest.NewRecorder()
	srv.Handler().ServeHTTP(keyRR, keyReq)
	if keyRR.Code != http.StatusCreated {
		t.Fatalf("expected 201 creating key, got %d", keyRR.Code)
	}
	var keyOut struct {
		APIKey string `json:"api_key"`
	}
	if err := json.Unmarshal(keyRR.Body.Bytes(), &keyOut); err != nil {
		t.Fatal(err)
	}

	// Set restrictive policy for this tenant.
	pReq := httptest.NewRequest(
		http.MethodPost,
		"/admin/v1/tenants/"+tenant.ID+"/model-policy",
		bytes.NewBufferString(`{"allowed_models":["non-existent-model"],"primary_model":"non-existent-model"}`),
	)
	pReq.Header.Set("Authorization", "Bearer "+adminKey)
	pRR := httptest.NewRecorder()
	srv.Handler().ServeHTTP(pRR, pReq)
	if pRR.Code != http.StatusOK {
		t.Fatalf("expected 200 policy upsert, got %d", pRR.Code)
	}

	body := map[string]any{
		"model":    "auto",
		"messages": []map[string]string{{"role": "user", "content": "hello"}},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+keyOut.APIKey)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rr.Code, rr.Body.String())
	}

	// Ensure the original runtime key from setup remains valid.
	sanity := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"mistral:7b","messages":[{"role":"user","content":"hi"}]}`))
	sanity.Header.Set("Authorization", "Bearer "+runtimeKey)
	sanityRR := httptest.NewRecorder()
	srv.Handler().ServeHTTP(sanityRR, sanity)
	if sanityRR.Code != http.StatusOK {
		t.Fatalf("expected sanity 200, got %d", sanityRR.Code)
	}
}

func TestOrchestratorDebugEndpoint(t *testing.T) {
	srv, adminKey, _, _ := setupServer(t)
	req := httptest.NewRequest(
		http.MethodPost,
		"/admin/v1/orchestrator/debug",
		bytes.NewBufferString(`{"model":"auto","messages":[{"role":"user","content":"What is DNS?"}]}`),
	)
	req.Header.Set("Authorization", "Bearer "+adminKey)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if ok, _ := out["ok"].(bool); !ok {
		t.Fatalf("expected ok true, got %v", out["ok"])
	}
}

func TestSessionHeaderPropagation(t *testing.T) {
	srv, _, runtimeKey, _ := setupServer(t)
	body := map[string]any{
		"model":      "auto",
		"session_id": "sess-123",
		"messages":   []map[string]string{{"role": "user", "content": "What is DNS?"}},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+runtimeKey)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("X-GLM-Session-ID"); got != "sess-123" {
		t.Fatalf("expected session header sess-123, got %q", got)
	}
}

func TestEmotionalModulationInjectsToneMetadataSystemMessage(t *testing.T) {
	srv, _, runtimeKey, up := setupServer(t)
	body := map[string]any{
		"model":      "auto",
		"session_id": "sess-tone",
		"messages":   []map[string]string{{"role": "user", "content": "I am frustrated, this keeps failing"}},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+runtimeKey)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if len(up.lastRequest.Messages) == 0 {
		t.Fatal("expected upstream request messages")
	}
	first := up.lastRequest.Messages[0]
	if first.Role != "system" {
		t.Fatalf("expected prepended system message, got role=%s", first.Role)
	}
	if !bytes.Contains([]byte(first.Content), []byte("cognitive_state")) {
		t.Fatalf("expected cognitive metadata in system message, got %q", first.Content)
	}
}

func TestStateEndpointDoesNotExposeRawHistory(t *testing.T) {
	srv, adminKey, runtimeKey, _ := setupServer(t)
	body := map[string]any{
		"session_id": "sess-state",
		"messages":   []map[string]string{{"role": "user", "content": "sensitive user payload"}},
	}
	b, _ := json.Marshal(body)
	chatReq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	chatReq.Header.Set("Authorization", "Bearer "+runtimeKey)
	chatRR := httptest.NewRecorder()
	srv.Handler().ServeHTTP(chatRR, chatReq)
	if chatRR.Code != http.StatusOK {
		t.Fatalf("expected 200 chat, got %d: %s", chatRR.Code, chatRR.Body.String())
	}

	stateReq := httptest.NewRequest(http.MethodGet, "/admin/v1/state/sess-state", nil)
	stateReq.Header.Set("Authorization", "Bearer "+adminKey)
	stateRR := httptest.NewRecorder()
	srv.Handler().ServeHTTP(stateRR, stateReq)
	if stateRR.Code != http.StatusOK {
		t.Fatalf("expected 200 state, got %d: %s", stateRR.Code, stateRR.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(stateRR.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	session, ok := out["session"].(map[string]any)
	if !ok {
		t.Fatalf("expected session object, got %T", out["session"])
	}
	if _, exists := session["recent_history"]; exists {
		t.Fatal("did not expect recent_history in session state")
	}
}

func TestReasoningPipelineHeaders(t *testing.T) {
	srv, _, runtimeKey, _ := setupServer(t)
	body := map[string]any{
		"model": "auto",
		"reasoning": map[string]any{
			"mode":     "tot",
			"branches": 3,
		},
		"messages": []map[string]string{{"role": "user", "content": "Compare 3 implementation strategies and choose best"}},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+runtimeKey)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("X-GLM-Reasoning-Pipeline") != "tot" {
		t.Fatalf("expected reasoning pipeline header, got %q", rr.Header().Get("X-GLM-Reasoning-Pipeline"))
	}
	if rr.Header().Get("X-GLM-Reasoning-Branches") == "" {
		t.Fatal("expected branch count header")
	}
	if rr.Header().Get("X-GLM-Reasoning-Pruning") == "" {
		t.Fatal("expected reasoning pruning header")
	}
	if rr.Header().Get("X-GLM-Reasoning-Prune-In") == "" {
		t.Fatal("expected reasoning prune-in header")
	}
	if rr.Header().Get("X-GLM-Reasoning-Prune-Out") == "" {
		t.Fatal("expected reasoning prune-out header")
	}
	if rr.Header().Get("X-GLM-Reasoning-Prune-Dropped") == "" {
		t.Fatal("expected reasoning prune-dropped header")
	}
}

func TestMCTSReasoningHeaders(t *testing.T) {
	srv, _, runtimeKey, _ := setupServer(t)
	body := map[string]any{
		"model": "auto",
		"reasoning": map[string]any{
			"mode":              "mcts",
			"mcts_max_rollouts": 4,
			"mcts_max_depth":    2,
		},
		"messages": []map[string]string{{"role": "user", "content": "Evaluate rollout options and choose one"}},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+runtimeKey)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("X-GLM-Reasoning-Pipeline") != "mcts" {
		t.Fatalf("expected mcts pipeline header, got %q", rr.Header().Get("X-GLM-Reasoning-Pipeline"))
	}
	if rr.Header().Get("X-GLM-MCTS-Rollouts") == "" {
		t.Fatal("expected mcts rollout header")
	}
	if rr.Header().Get("X-GLM-MCTS-Depth") == "" {
		t.Fatal("expected mcts depth header")
	}
	if rr.Header().Get("X-GLM-MCTS-Best-Score") == "" {
		t.Fatal("expected mcts best score header")
	}
	if rr.Header().Get("X-GLM-MCTS-V2") == "" {
		t.Fatal("expected mcts v2 header")
	}
	if rr.Header().Get("X-GLM-MCTS-Early-Stop") == "" {
		t.Fatal("expected mcts early-stop header")
	}
	if rr.Header().Get("X-GLM-MCTS-Rollouts-Executed") == "" {
		t.Fatal("expected mcts rollouts-executed header")
	}
	if rr.Header().Get("X-GLM-Reasoning-Pruning") == "" {
		t.Fatal("expected reasoning pruning header")
	}
}

func TestMultiAgentReasoningHeaders(t *testing.T) {
	srv, _, runtimeKey, _ := setupServer(t)
	body := map[string]any{
		"model": "auto",
		"reasoning": map[string]any{
			"mode":                   "multi_agent",
			"multi_agent_enabled":    true,
			"multi_agent_max_agents": 4,
			"multi_agent_max_rounds": 2,
		},
		"messages": []map[string]string{{"role": "user", "content": "Assess rollout options and choose with risk controls"}},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+runtimeKey)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("X-GLM-Reasoning-Pipeline") != "multi_agent" {
		t.Fatalf("expected multi_agent pipeline header, got %q", rr.Header().Get("X-GLM-Reasoning-Pipeline"))
	}
	if rr.Header().Get("X-GLM-MA-Agents") == "" {
		t.Fatal("expected X-GLM-MA-Agents header")
	}
	if rr.Header().Get("X-GLM-MA-Rounds") == "" {
		t.Fatal("expected X-GLM-MA-Rounds header")
	}
	if rr.Header().Get("X-GLM-MA-Winner") == "" {
		t.Fatal("expected X-GLM-MA-Winner header")
	}
	if rr.Header().Get("X-GLM-MA-Consensus") == "" {
		t.Fatal("expected X-GLM-MA-Consensus header")
	}
	if rr.Header().Get("X-GLM-Reasoning-Pruning") == "" {
		t.Fatal("expected reasoning pruning header")
	}
}

func TestMultiAgentEnabledGuard(t *testing.T) {
	srv, _, runtimeKey, _ := setupServer(t)
	body := map[string]any{
		"model": "auto",
		"reasoning": map[string]any{
			"mode":                "multi_agent",
			"multi_agent_enabled": false,
		},
		"messages": []map[string]string{{"role": "user", "content": "hello"}},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+runtimeKey)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestReasoningModeDisabledReturns400(t *testing.T) {
	st := memory.New()
	up := &fakeUpstream{models: []string{"mistral:7b"}}
	cfg := config.Config{
		DefaultModel:                 "mistral:7b",
		RateLimitRPM:                 100,
		ReasoningPipelineEnabled:     false,
		MCTSEnabled:                  false,
		MultiAgentEnabled:            false,
		ReasoningHiddenByDefault:     true,
		IntentPreprocessorEnabled:    true,
		DocumentOrchestrationEnabled: true,
		MemoryDynamicsEnabled:        true,
		StyleContractEnabled:         true,
		MetaReasoningEnabled:         true,
	}
	srv := NewServer(cfg, st, up)
	a := auth.NewService(st)
	tenant, err := st.CreateTenant(context.Background(), "acme-disabled")
	if err != nil {
		t.Fatal(err)
	}
	runtimeKey, _, err := a.GenerateAPIKey(context.Background(), tenant.ID, []string{"runtime:*"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]any{
		"model": "auto",
		"reasoning": map[string]any{
			"mode":                "multi_agent",
			"multi_agent_enabled": true,
		},
		"messages": []map[string]string{{"role": "user", "content": "hello"}},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+runtimeKey)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestMultiAgentFailureFallsBackToMCTS(t *testing.T) {
	srv, _, runtimeKey, up := setupServer(t)
	up.failMultiAgent = true
	body := map[string]any{
		"model": "auto",
		"reasoning": map[string]any{
			"mode":                "multi_agent",
			"multi_agent_enabled": true,
		},
		"messages": []map[string]string{{"role": "user", "content": "Plan deployment safely"}},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+runtimeKey)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("X-GLM-MA-Fallback") != "mcts" {
		t.Fatalf("expected multi-agent fallback mcts, got %q", rr.Header().Get("X-GLM-MA-Fallback"))
	}
}

func TestMultiAgentFailureChainFallsBackToDirect(t *testing.T) {
	srv, _, runtimeKey, up := setupServer(t)
	up.failMultiAgent = true
	up.failMCTS = true
	up.failToT = true
	body := map[string]any{
		"model": "auto",
		"reasoning": map[string]any{
			"mode":                "multi_agent",
			"multi_agent_enabled": true,
		},
		"messages": []map[string]string{{"role": "user", "content": "Plan deployment safely"}},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+runtimeKey)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("X-GLM-MA-Fallback") != "direct" {
		t.Fatalf("expected multi-agent fallback direct, got %q", rr.Header().Get("X-GLM-MA-Fallback"))
	}
}

func TestMCTSFailureFallsBackToToT(t *testing.T) {
	srv, _, runtimeKey, up := setupServer(t)
	up.failMCTS = true
	body := map[string]any{
		"model": "auto",
		"reasoning": map[string]any{
			"mode":              "mcts",
			"mcts_max_rollouts": 4,
			"mcts_max_depth":    2,
		},
		"messages": []map[string]string{{"role": "user", "content": "Plan deployment safely"}},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+runtimeKey)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("X-GLM-MCTS-Fallback") != "tot" {
		t.Fatalf("expected mcts fallback tot, got %q", rr.Header().Get("X-GLM-MCTS-Fallback"))
	}
	if rr.Header().Get("X-GLM-Reasoning-Error") != "true" {
		t.Fatalf("expected reasoning error marker, got %q", rr.Header().Get("X-GLM-Reasoning-Error"))
	}
}

func TestMCTSFailureToTFailureFallsBackDirect(t *testing.T) {
	srv, _, runtimeKey, up := setupServer(t)
	up.failMCTS = true
	up.failToT = true
	body := map[string]any{
		"model": "auto",
		"reasoning": map[string]any{
			"mode":              "mcts",
			"mcts_max_rollouts": 4,
			"mcts_max_depth":    2,
		},
		"messages": []map[string]string{{"role": "user", "content": "Plan deployment safely"}},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+runtimeKey)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("X-GLM-MCTS-Fallback") != "direct" {
		t.Fatalf("expected mcts fallback direct, got %q", rr.Header().Get("X-GLM-MCTS-Fallback"))
	}
}

func TestIntentPreprocessorHeadersAndRewrite(t *testing.T) {
	srv, _, runtimeKey, up := setupServer(t)
	body := map[string]any{
		"model":    "mistral:7b",
		"messages": []map[string]string{{"role": "user", "content": "fix this"}},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+runtimeKey)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("X-GLM-Intent-Category") == "" {
		t.Fatal("expected intent category header")
	}
	if rr.Header().Get("X-GLM-Ambiguity-Score") == "" {
		t.Fatal("expected ambiguity score header")
	}
	if rr.Header().Get("X-GLM-Intent-Rewritten") != "true" {
		t.Fatalf("expected rewritten=true for ambiguous input, got %q", rr.Header().Get("X-GLM-Intent-Rewritten"))
	}
	found := false
	for _, m := range up.lastRequest.Messages {
		if m.Role == "user" && bytes.Contains([]byte(m.Content), []byte("Intent=")) {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected rewritten stable user input to be forwarded upstream")
	}
}

func TestDocumentOrchestrationHeaders(t *testing.T) {
	srv, _, runtimeKey, up := setupServer(t)
	body := map[string]any{
		"model": "mistral:7b",
		"documents": []map[string]any{
			{
				"id":    "doc-1",
				"title": "Runbook",
				"text":  "Service rollout plan with phases and controls. Incident response requirements and owners.",
			},
			{
				"id":    "doc-2",
				"title": "Policy",
				"text":  "Compliance controls and audit checkpoints. Rollout gates and rollback triggers.",
			},
		},
		"messages": []map[string]string{{"role": "user", "content": "Create a consolidated rollout approach"}},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+runtimeKey)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("X-GLM-Document-Orchestration") != "applied" {
		t.Fatalf("expected document orchestration header, got %q", rr.Header().Get("X-GLM-Document-Orchestration"))
	}
	if rr.Header().Get("X-GLM-Document-Chunks") == "" {
		t.Fatal("expected document chunk header")
	}
	if rr.Header().Get("X-GLM-Document-Model") != "mistral:7b" {
		t.Fatalf("expected document model header mistral:7b, got %q", rr.Header().Get("X-GLM-Document-Model"))
	}
	if up.listCalls != 1 {
		t.Fatalf("expected one inventory lookup, got %d", up.listCalls)
	}
	found := false
	for _, m := range up.lastRequest.Messages {
		if m.Role == "system" && bytes.Contains([]byte(m.Content), []byte("document_orchestration")) {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected orchestration context injected into request")
	}
}

func TestDocumentOrchestrationExplicitUnavailableModelReturns503(t *testing.T) {
	srv, _, runtimeKey, _ := setupServer(t)
	body := map[string]any{
		"model": "missing-model",
		"documents": []map[string]any{
			{
				"id":    "doc-1",
				"title": "Runbook",
				"text":  "Service rollout plan with phases and controls.",
			},
		},
		"messages": []map[string]string{{"role": "user", "content": "Create a consolidated rollout approach"}},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+runtimeKey)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestAutoRoutingWithDocflowUsesSingleInventoryLookup(t *testing.T) {
	srv, _, runtimeKey, up := setupServer(t)
	body := map[string]any{
		"model": "auto",
		"documents": []map[string]any{
			{
				"id":    "doc-1",
				"title": "Runbook",
				"text":  "Service rollout plan with phases and controls.",
			},
		},
		"messages": []map[string]string{{"role": "user", "content": "Create a consolidated rollout approach"}},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+runtimeKey)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if up.listCalls != 1 {
		t.Fatalf("expected one inventory lookup, got %d", up.listCalls)
	}
	if rr.Header().Get("X-GLM-Routed-Model") == "" {
		t.Fatal("expected routed model header")
	}
	if rr.Header().Get("X-GLM-Document-Model") == "" {
		t.Fatal("expected document model header")
	}
}

func TestMemoryDynamicsHeaders(t *testing.T) {
	srv, _, runtimeKey, _ := setupServer(t)
	body := map[string]any{
		"model":      "mistral:7b",
		"session_id": "sess-mem",
		"messages":   []map[string]string{{"role": "user", "content": "Critical incident runbook decision for rollout controls"}},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+runtimeKey)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("X-GLM-Memory-Nodes") == "" {
		t.Fatal("expected memory nodes header")
	}
	if rr.Header().Get("X-GLM-Memory-Replay") == "" {
		t.Fatal("expected memory replay header")
	}
}

func TestCognitionRouteInputTaskReasoning(t *testing.T) {
	srv, _, runtimeKey, up := setupServer(t)
	body := map[string]any{
		"task":  "reasoning",
		"input": "Compare two rollout options and choose one",
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/cognition", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+runtimeKey)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("X-GLM-Cognition-Task") != "reasoning" {
		t.Fatalf("expected cognition task header, got %q", rr.Header().Get("X-GLM-Cognition-Task"))
	}
	if rr.Header().Get("X-GLM-Reasoning-Pipeline") != "tot" {
		t.Fatalf("expected reasoning pipeline header, got %q", rr.Header().Get("X-GLM-Reasoning-Pipeline"))
	}
	if rr.Header().Get("X-GLM-Reasoning-Pruning") == "" {
		t.Fatal("expected reasoning pruning header")
	}
	if up.lastRequest.Reasoning == nil {
		t.Fatal("expected reasoning options to be set on upstream request")
	}
}

func TestDirectChatOmitsReasoningPruningHeaders(t *testing.T) {
	srv, _, runtimeKey, _ := setupServer(t)
	body := map[string]any{
		"model":    "mistral:7b",
		"messages": []map[string]string{{"role": "user", "content": "hello"}},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+runtimeKey)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("X-GLM-Reasoning-Pruning") != "" {
		t.Fatalf("did not expect pruning header on direct chat, got %q", rr.Header().Get("X-GLM-Reasoning-Pruning"))
	}
	if rr.Header().Get("X-GLM-MCTS-V2") != "" {
		t.Fatalf("did not expect mcts v2 header on direct chat, got %q", rr.Header().Get("X-GLM-MCTS-V2"))
	}
	if rr.Header().Get("X-GLM-Reasoning-Memory-Anchor") != "" {
		t.Fatalf("did not expect memory anchor header on direct chat, got %q", rr.Header().Get("X-GLM-Reasoning-Memory-Anchor"))
	}
}

func TestReasoningMemoryAnchorHeadersDisabled(t *testing.T) {
	srv, _, runtimeKey, _ := setupServer(t)
	body := map[string]any{
		"model": "mistral:7b",
		"reasoning": map[string]any{
			"mode": "tot",
		},
		"messages": []map[string]string{{"role": "user", "content": "compare options"}},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+runtimeKey)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("X-GLM-Reasoning-Memory-Anchor") != "disabled" {
		t.Fatalf("expected memory anchor disabled header, got %q", rr.Header().Get("X-GLM-Reasoning-Memory-Anchor"))
	}
}

func TestReasoningMemoryAnchorHeadersSkippedNoAnchors(t *testing.T) {
	srv, _, runtimeKey, _, _ := setupServerCustom(t, nil, func(cfg *config.Config) {
		cfg.MemoryAnchoredReasoningEnabled = true
		cfg.MemoryAnchoredReasoningMaxAnchors = 3
		cfg.MemoryAnchoredReasoningMinCoverage = 0.34
		cfg.MemoryAnchoredReasoningScoreBonus = 0.06
	})
	body := map[string]any{
		"model":      "mistral:7b",
		"session_id": "sess-anchor-skip",
		"reasoning": map[string]any{
			"mode": "tot",
		},
		"messages": []map[string]string{{"role": "user", "content": "compare options"}},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+runtimeKey)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("X-GLM-Reasoning-Memory-Anchor") != "skipped" {
		t.Fatalf("expected memory anchor skipped header, got %q", rr.Header().Get("X-GLM-Reasoning-Memory-Anchor"))
	}
}

func TestReasoningMemoryAnchorHeadersEnabledWithAnchors(t *testing.T) {
	srv, _, runtimeKey, _, _ := setupServerCustom(t, nil, func(cfg *config.Config) {
		cfg.MemoryAnchoredReasoningEnabled = true
		cfg.MemoryAnchoredReasoningMaxAnchors = 3
		cfg.MemoryAnchoredReasoningMinCoverage = 0.1
		cfg.MemoryAnchoredReasoningScoreBonus = 0.06
	})
	sessionID := "sess-anchor-enabled"

	seedBody := map[string]any{
		"model":      "mistral:7b",
		"session_id": sessionID,
		"messages":   []map[string]string{{"role": "user", "content": "Critical incident rollout controls decision"}},
	}
	sb, _ := json.Marshal(seedBody)
	seedReq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(sb))
	seedReq.Header.Set("Authorization", "Bearer "+runtimeKey)
	seedRR := httptest.NewRecorder()
	srv.Handler().ServeHTTP(seedRR, seedReq)
	if seedRR.Code != http.StatusOK {
		t.Fatalf("seed expected 200, got %d: %s", seedRR.Code, seedRR.Body.String())
	}

	body := map[string]any{
		"model":      "mistral:7b",
		"session_id": sessionID,
		"reasoning": map[string]any{
			"mode": "tot",
		},
		"messages": []map[string]string{{"role": "user", "content": "compare options"}},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+runtimeKey)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("X-GLM-Reasoning-Memory-Anchor") != "enabled" {
		t.Fatalf("expected memory anchor enabled header, got %q", rr.Header().Get("X-GLM-Reasoning-Memory-Anchor"))
	}
	if rr.Header().Get("X-GLM-Reasoning-Memory-Anchors-In") == "" ||
		rr.Header().Get("X-GLM-Reasoning-Memory-Anchors-Used") == "" ||
		rr.Header().Get("X-GLM-Reasoning-Memory-Coverage-Avg") == "" ||
		rr.Header().Get("X-GLM-Reasoning-Memory-Bonus-Avg") == "" {
		t.Fatal("expected memory anchor aggregate headers")
	}
}

func TestCognitionRouteDocumentTask(t *testing.T) {
	srv, _, runtimeKey, _ := setupServer(t)
	body := map[string]any{
		"task": "document_synthesis",
		"documents": []map[string]any{
			{"id": "d1", "title": "A", "text": "rollout controls audit checkpoints"},
			{"id": "d2", "title": "B", "text": "incident runbook and rollback gates"},
		},
		"input": "Synthesize a plan",
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/cognition", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+runtimeKey)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("X-GLM-Document-Orchestration") != "applied" {
		t.Fatalf("expected doc orchestration applied, got %q", rr.Header().Get("X-GLM-Document-Orchestration"))
	}
}

func TestCognitionRouteMCTSReasoning(t *testing.T) {
	srv, _, runtimeKey, _ := setupServer(t)
	body := map[string]any{
		"task":  "reasoning",
		"input": "Compare two options and decide",
		"reasoning": map[string]any{
			"mode":              "mcts",
			"mcts_max_rollouts": 4,
			"mcts_max_depth":    2,
		},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/cognition", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+runtimeKey)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("X-GLM-Reasoning-Pipeline") != "mcts" {
		t.Fatalf("expected mcts pipeline, got %q", rr.Header().Get("X-GLM-Reasoning-Pipeline"))
	}
}

func TestCognitionRouteMultiAgentReasoning(t *testing.T) {
	srv, _, runtimeKey, _ := setupServer(t)
	body := map[string]any{
		"task":  "reasoning",
		"input": "Compare two options and decide",
		"reasoning": map[string]any{
			"mode":                "multi_agent",
			"multi_agent_enabled": true,
		},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/cognition", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+runtimeKey)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("X-GLM-Reasoning-Pipeline") != "multi_agent" {
		t.Fatalf("expected multi_agent pipeline, got %q", rr.Header().Get("X-GLM-Reasoning-Pipeline"))
	}
}

func TestMetaReasoningOptInHeaders(t *testing.T) {
	srv, _, runtimeKey, _ := setupServer(t)
	body := map[string]any{
		"model": "mistral:7b",
		"reasoning": map[string]any{
			"meta_enabled": true,
			"meta_profile": "default",
		},
		"messages": []map[string]string{{"role": "user", "content": "Analyze rollout tradeoffs and give recommendation"}},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+runtimeKey)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("X-GLM-Meta-Reasoning") != "enabled" {
		t.Fatalf("expected meta reasoning enabled header, got %q", rr.Header().Get("X-GLM-Meta-Reasoning"))
	}
	decision := rr.Header().Get("X-GLM-Meta-Decision")
	if decision != "accept" && decision != "caution" && decision != "reject" {
		t.Fatalf("unexpected meta decision %q", decision)
	}
	if rr.Header().Get("X-GLM-Meta-Confidence") == "" || rr.Header().Get("X-GLM-Meta-Risk-Score") == "" {
		t.Fatal("expected meta confidence and risk headers")
	}
	if rr.Header().Get("X-GLM-Meta-Reflection") != "disabled" {
		t.Fatalf("expected reflection disabled by default, got %q", rr.Header().Get("X-GLM-Meta-Reflection"))
	}
	if rr.Header().Get("X-GLM-Meta-Reflection-Passes") != "0" {
		t.Fatalf("expected 0 reflection passes by default, got %q", rr.Header().Get("X-GLM-Meta-Reflection-Passes"))
	}
}

func TestMetaReasoningDisabledNoHeaders(t *testing.T) {
	srv, _, runtimeKey, _ := setupServer(t)
	body := map[string]any{
		"model":    "mistral:7b",
		"messages": []map[string]string{{"role": "user", "content": "hello"}},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+runtimeKey)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("X-GLM-Meta-Reasoning") != "" {
		t.Fatalf("did not expect meta headers, got %q", rr.Header().Get("X-GLM-Meta-Reasoning"))
	}
}

func TestMetaReasoningAuditOutcomeTags(t *testing.T) {
	srv, adminKey, runtimeKey, tenantID, _ := setupServerWithTenant(t)
	body := map[string]any{
		"model": "mistral:7b",
		"reasoning": map[string]any{
			"meta_enabled": true,
			"meta_profile": "strict",
		},
		"messages": []map[string]string{{"role": "user", "content": "maybe unclear not sure, analyze this"}},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+runtimeKey)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	auditReq := httptest.NewRequest(http.MethodGet, "/admin/v1/tenants/"+tenantID+"/audit-events", nil)
	auditReq.Header.Set("Authorization", "Bearer "+adminKey)
	auditRR := httptest.NewRecorder()
	srv.Handler().ServeHTTP(auditRR, auditReq)
	if auditRR.Code != http.StatusOK {
		t.Fatalf("expected 200 on audit events, got %d: %s", auditRR.Code, auditRR.Body.String())
	}
	var out struct {
		Items []struct {
			Outcome string `json:"outcome"`
		} `json:"items"`
	}
	if err := json.Unmarshal(auditRR.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Items) == 0 {
		t.Fatal("expected audit items")
	}
	got := out.Items[0].Outcome
	if !bytes.Contains([]byte(got), []byte("meta_decision=")) || !bytes.Contains([]byte(got), []byte("meta_conf=")) || !bytes.Contains([]byte(got), []byte("meta_risk=")) {
		t.Fatalf("expected meta tags in audit outcome, got %q", got)
	}
}

func TestShouldTriggerReflectionStrictOptIn(t *testing.T) {
	req := model.ChatCompletionRequest{
		Reasoning: &model.ReasoningOptions{
			MetaEnabled: true,
		},
	}
	meta := metareasoning.Result{Decision: "caution"}
	cfg := config.Config{
		MetaReflectionEnabled:          false,
		MetaReflectionTriggerDecisions: []string{"caution", "reject"},
	}
	if shouldTriggerReflection(meta, req, cfg) {
		t.Fatal("expected no trigger when reflection is not enabled")
	}
	req.Reasoning.MetaReflectionEnabled = true
	if !shouldTriggerReflection(meta, req, cfg) {
		t.Fatal("expected trigger when request enables reflection and decision matches")
	}
	meta.Decision = "accept"
	if shouldTriggerReflection(meta, req, cfg) {
		t.Fatal("expected no trigger for non-matching decision")
	}
}

func TestMetaReflectionAppliedWhenImproved(t *testing.T) {
	up := &fakeUpstream{
		models:      []string{"mistral:7b"},
		responseSeq: []string{"maybe unclear perhaps", "Concrete answer with clear assumptions, checks, and risks handled explicitly."},
	}
	srv, _, runtimeKey, _, _ := setupServerCustom(t, up, func(cfg *config.Config) {
		cfg.MetaReasoningEnabled = true
		cfg.MetaReflectionEnabled = true
		cfg.MetaReflectionMaxPasses = 1
		cfg.MetaReflectionTriggerDecisions = []string{"caution", "reject"}
	})
	body := map[string]any{
		"model": "mistral:7b",
		"reasoning": map[string]any{
			"meta_enabled": true,
		},
		"messages": []map[string]string{{"role": "user", "content": "Provide a recommendation"}},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+runtimeKey)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if up.chatCalls < 2 {
		t.Fatalf("expected reflection revision call, got %d upstream calls", up.chatCalls)
	}
	if rr.Header().Get("X-GLM-Meta-Reflection") != "applied" {
		t.Fatalf("expected reflection applied, got %q", rr.Header().Get("X-GLM-Meta-Reflection"))
	}
	if rr.Header().Get("X-GLM-Meta-Reflection-Passes") != "1" {
		t.Fatalf("expected one reflection pass, got %q", rr.Header().Get("X-GLM-Meta-Reflection-Passes"))
	}
}

func TestMetaReflectionSkippedWhenNotImproved(t *testing.T) {
	up := &fakeUpstream{
		models:      []string{"mistral:7b"},
		responseSeq: []string{"maybe unclear perhaps", "maybe unclear perhaps"},
	}
	srv, _, runtimeKey, _, _ := setupServerCustom(t, up, func(cfg *config.Config) {
		cfg.MetaReasoningEnabled = true
		cfg.MetaReflectionEnabled = true
		cfg.MetaReflectionMaxPasses = 1
		cfg.MetaReflectionTriggerDecisions = []string{"caution", "reject"}
	})
	body := map[string]any{
		"model": "mistral:7b",
		"reasoning": map[string]any{
			"meta_enabled": true,
		},
		"messages": []map[string]string{{"role": "user", "content": "Provide a recommendation"}},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+runtimeKey)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("X-GLM-Meta-Reflection") != "skipped" {
		t.Fatalf("expected reflection skipped, got %q", rr.Header().Get("X-GLM-Meta-Reflection"))
	}
	if rr.Header().Get("X-GLM-Meta-Reflection-Reason") != "decision_trigger" {
		t.Fatalf("expected decision trigger reason, got %q", rr.Header().Get("X-GLM-Meta-Reflection-Reason"))
	}
	var out model.ChatCompletionResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(out.Choices[0].Message.Content)
	if got != "maybe unclear perhaps" {
		t.Fatalf("expected original response retained, got %q", got)
	}
}

func TestMetaReflectionUpstreamErrorReturnsOriginal(t *testing.T) {
	up := &fakeUpstream{
		models:      []string{"mistral:7b"},
		responseSeq: []string{"maybe unclear perhaps"},
		errorOnCall: map[int]error{2: fmt.Errorf("revision upstream failure")},
	}
	srv, _, runtimeKey, _, _ := setupServerCustom(t, up, func(cfg *config.Config) {
		cfg.MetaReasoningEnabled = true
		cfg.MetaReflectionEnabled = true
		cfg.MetaReflectionMaxPasses = 1
		cfg.MetaReflectionTriggerDecisions = []string{"caution", "reject"}
	})
	body := map[string]any{
		"model": "mistral:7b",
		"reasoning": map[string]any{
			"meta_enabled": true,
		},
		"messages": []map[string]string{{"role": "user", "content": "Provide a recommendation"}},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+runtimeKey)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("X-GLM-Meta-Reflection") != "error" {
		t.Fatalf("expected reflection error header, got %q", rr.Header().Get("X-GLM-Meta-Reflection"))
	}
	if rr.Header().Get("X-GLM-Meta-Reflection-Reason") != "upstream_error" {
		t.Fatalf("expected upstream_error reason, got %q", rr.Header().Get("X-GLM-Meta-Reflection-Reason"))
	}
	var out model.ChatCompletionResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(out.Choices[0].Message.Content)
	if got != "maybe unclear perhaps" {
		t.Fatalf("expected original response retained after reflection error, got %q", got)
	}
}

func TestResponseStylePropagatesToUpstream(t *testing.T) {
	srv, _, runtimeKey, up := setupServer(t)
	body := map[string]any{
		"model": "mistral:7b",
		"response_style": map[string]any{
			"breathing_weight": 0.32,
		},
		"messages": []map[string]string{{"role": "user", "content": "hello"}},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+runtimeKey)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if up.lastRequest.ResponseStyle == nil {
		t.Fatal("expected response_style propagated")
	}
	if up.lastRequest.ResponseStyle.BreathingWeight != 0.32 {
		t.Fatalf("expected breathing_weight=0.32, got %f", up.lastRequest.ResponseStyle.BreathingWeight)
	}
	if rr.Header().Get("X-GLM-Breathing-Weight") == "" {
		t.Fatal("expected breathing weight header")
	}
	if rr.Header().Get("X-GLM-Pacing") == "" {
		t.Fatal("expected pacing header")
	}
}

func TestMicroSwitchesAreInjectedWhenNotProvided(t *testing.T) {
	srv, _, runtimeKey, up := setupServer(t)
	body := map[string]any{
		"model":      "mistral:7b",
		"session_id": "sess-switch",
		"messages":   []map[string]string{{"role": "user", "content": "Urgent! fix this incident issue now"}},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+runtimeKey)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if up.lastRequest.ResponseStyle == nil {
		t.Fatal("expected response_style to be set")
	}
	if up.lastRequest.ResponseStyle.ToneShift == "" {
		t.Fatal("expected tone shift metadata")
	}
	if up.lastRequest.ResponseStyle.StyleAdjustment == "" {
		t.Fatal("expected style adjustment metadata")
	}
	if len(up.lastRequest.ResponseStyle.MicroSwitches) == 0 {
		t.Fatal("expected micro-switch metadata")
	}
	if up.lastRequest.ResponseStyle.SubtextDetection != "model-driven" {
		t.Fatalf("expected subtext_detection=model-driven, got %q", up.lastRequest.ResponseStyle.SubtextDetection)
	}
	if rr.Header().Get("X-GLM-Risk-Flags") == "" {
		t.Fatal("expected risk flags header")
	}
	if len(up.lastRequest.ResponseStyle.RiskFlags) == 0 {
		t.Fatal("expected risk flags in response style")
	}
	if rr.Header().Get("X-GLM-Style-Contract") != "v1" {
		t.Fatalf("expected style contract header v1, got %q", rr.Header().Get("X-GLM-Style-Contract"))
	}
	foundContract := false
	for _, m := range up.lastRequest.Messages {
		if m.Role == "system" && bytes.Contains([]byte(m.Content), []byte("style_contract=v1")) {
			foundContract = true
			break
		}
	}
	if !foundContract {
		t.Fatal("expected style contract system message in upstream request")
	}
}

func TestGetSessionStateEndpoint(t *testing.T) {
	srv, adminKey, runtimeKey, _ := setupServer(t)

	// Seed session state through runtime endpoint.
	body := map[string]any{
		"model":      "auto",
		"session_id": "sess-debug",
		"messages":   []map[string]string{{"role": "user", "content": "I love this DNS setup"}},
	}
	b, _ := json.Marshal(body)
	rreq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	rreq.Header.Set("Authorization", "Bearer "+runtimeKey)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, rreq)
	if rr.Code != http.StatusOK {
		t.Fatalf("seed runtime call expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// Fetch state via admin endpoint.
	req := httptest.NewRequest(http.MethodGet, "/admin/v1/state/sess-debug", nil)
	req.Header.Set("Authorization", "Bearer "+adminKey)
	outRR := httptest.NewRecorder()
	srv.Handler().ServeHTTP(outRR, req)
	if outRR.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", outRR.Code, outRR.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(outRR.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if ok, _ := out["ok"].(bool); !ok {
		t.Fatalf("expected ok=true, got %#v", out["ok"])
	}
}

func TestGetSessionStateNotFound(t *testing.T) {
	srv, adminKey, _, _ := setupServer(t)
	req := httptest.NewRequest(http.MethodGet, "/admin/v1/state/does-not-exist", nil)
	req.Header.Set("Authorization", "Bearer "+adminKey)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func TestSymbolicOverlayAssistHeadersAndInjection(t *testing.T) {
	srv, _, runtimeKey, up := setupServer(t)
	body := map[string]any{
		"model": "mistral:7b",
		"symbolic_overlay": map[string]any{
			"mode":              "assist",
			"types":             []string{"logic_map", "constraint_set", "risk_lens"},
			"include_documents": true,
		},
		"documents": []map[string]any{{"id": "doc-1", "title": "Runbook", "text": "must monitor incidents and rollback safely"}},
		"messages":  []map[string]string{{"role": "user", "content": "We must deploy and never skip compliance checks"}},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+runtimeKey)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("X-GLM-Symbolic-Overlay") != "applied" {
		t.Fatalf("expected symbolic overlay applied, got %q", rr.Header().Get("X-GLM-Symbolic-Overlay"))
	}
	if rr.Header().Get("X-GLM-Symbolic-Mode") != "assist" {
		t.Fatalf("expected symbolic mode assist, got %q", rr.Header().Get("X-GLM-Symbolic-Mode"))
	}
	if rr.Header().Get("X-GLM-Symbolic-Types") == "" {
		t.Fatal("expected symbolic types header")
	}
	if rr.Header().Get("X-GLM-Symbolic-Symbols") == "" {
		t.Fatal("expected symbolic symbols header")
	}
	found := false
	for _, m := range up.lastRequest.Messages {
		if m.Role == "system" && bytes.Contains([]byte(m.Content), []byte("Use this symbolic overlay for grounded, policy-consistent reasoning:")) {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected symbolic overlay system context injected")
	}
}

func TestSymbolicOverlayInvalidModeReturns400(t *testing.T) {
	srv, _, runtimeKey, _ := setupServer(t)
	body := map[string]any{
		"model": "mistral:7b",
		"symbolic_overlay": map[string]any{
			"mode": "invalid-mode",
		},
		"messages": []map[string]string{{"role": "user", "content": "hello"}},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+runtimeKey)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestSymbolicOverlayOffSkipped(t *testing.T) {
	srv, _, runtimeKey, up := setupServer(t)
	body := map[string]any{
		"model": "mistral:7b",
		"symbolic_overlay": map[string]any{
			"mode": "off",
		},
		"messages": []map[string]string{{"role": "user", "content": "hello"}},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+runtimeKey)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("X-GLM-Symbolic-Overlay") != "skipped" {
		t.Fatalf("expected symbolic skipped, got %q", rr.Header().Get("X-GLM-Symbolic-Overlay"))
	}
	if rr.Header().Get("X-GLM-Symbolic-Mode") != "off" {
		t.Fatalf("expected symbolic mode off, got %q", rr.Header().Get("X-GLM-Symbolic-Mode"))
	}
	for _, m := range up.lastRequest.Messages {
		if m.Role == "system" && bytes.Contains([]byte(m.Content), []byte("Use this symbolic overlay for grounded, policy-consistent reasoning:")) {
			t.Fatal("did not expect symbolic overlay injection in off mode")
		}
	}
}

func TestSymbolicOverlayStrictViolationsHeader(t *testing.T) {
	srv, _, runtimeKey, _ := setupServer(t)
	body := map[string]any{
		"model": "mistral:7b",
		"symbolic_overlay": map[string]any{
			"mode":  "strict",
			"types": []string{"constraint_set"},
		},
		"messages": []map[string]string{{"role": "user", "content": "The plan must include rollback and must not disable security"}},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+runtimeKey)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("X-GLM-Symbolic-Overlay") != "applied" {
		t.Fatalf("expected symbolic overlay applied, got %q", rr.Header().Get("X-GLM-Symbolic-Overlay"))
	}
	if rr.Header().Get("X-GLM-Symbolic-Violations") == "" {
		t.Fatal("expected symbolic violations header")
	}
}

func TestSymbolicOverlayPrepareFailureIsFailOpen(t *testing.T) {
	srv, _, runtimeKey, _ := setupServer(t)
	body := map[string]any{
		"model": "mistral:7b",
		"symbolic_overlay": map[string]any{
			"mode": "assist",
		},
		"messages": []map[string]string{{"role": "user", "content": "glm_internal_force_symbolic_prepare_error"}},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+runtimeKey)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("X-GLM-Symbolic-Overlay") != "error" {
		t.Fatalf("expected symbolic overlay error header, got %q", rr.Header().Get("X-GLM-Symbolic-Overlay"))
	}
	if rr.Header().Get("X-GLM-Symbolic-Error") != "true" {
		t.Fatalf("expected symbolic error marker, got %q", rr.Header().Get("X-GLM-Symbolic-Error"))
	}
}

func TestSymbolicOverlayComplianceFailureIsFailOpen(t *testing.T) {
	srv, _, runtimeKey, up := setupServer(t)
	up.responseText = "glm_internal_force_symbolic_compliance_error"
	body := map[string]any{
		"model": "mistral:7b",
		"symbolic_overlay": map[string]any{
			"mode": "strict",
		},
		"messages": []map[string]string{{"role": "user", "content": "must include rollback"}},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+runtimeKey)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("X-GLM-Symbolic-Overlay") != "error" {
		t.Fatalf("expected symbolic overlay error header, got %q", rr.Header().Get("X-GLM-Symbolic-Overlay"))
	}
	if rr.Header().Get("X-GLM-Symbolic-Error") != "true" {
		t.Fatalf("expected symbolic error marker, got %q", rr.Header().Get("X-GLM-Symbolic-Error"))
	}
}

func TestCognitionPropagatesSymbolicOverlay(t *testing.T) {
	srv, _, runtimeKey, up := setupServer(t)
	body := map[string]any{
		"task":  "chat",
		"input": "Plan rollout",
		"symbolic_overlay": map[string]any{
			"mode": "assist",
		},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/cognition", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+runtimeKey)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("X-GLM-Symbolic-Overlay") != "applied" {
		t.Fatalf("expected symbolic applied, got %q", rr.Header().Get("X-GLM-Symbolic-Overlay"))
	}
	if up.lastRequest.SymbolicOverlay == nil {
		t.Fatal("expected symbolic_overlay propagated into normalized request")
	}
}

func TestReasoningWithSymbolicOverlayCarriesContext(t *testing.T) {
	srv, _, runtimeKey, up := setupServer(t)
	body := map[string]any{
		"model": "auto",
		"symbolic_overlay": map[string]any{
			"mode": "assist",
		},
		"reasoning": map[string]any{
			"mode": "tot",
		},
		"messages": []map[string]string{{"role": "user", "content": "Compare deployment options"}},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+runtimeKey)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	found := false
	for _, m := range up.lastRequest.Messages {
		if m.Role == "system" && bytes.Contains([]byte(m.Content), []byte("Use this symbolic overlay for grounded, policy-consistent reasoning:")) {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected symbolic overlay context in reasoning request")
	}
}

func TestToolCallingExecutesToolLoop(t *testing.T) {
	toolSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tools/web_search" {
			t.Fatalf("unexpected tool path %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer tool-secret" {
			t.Fatalf("unexpected auth header %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("X-Client-Id") != "glm-test-client" {
			t.Fatalf("unexpected client id %q", r.Header.Get("X-Client-Id"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"title":"DNS","url":"https://example.com"}]}`))
	}))
	defer toolSrv.Close()

	up := &fakeUpstream{models: []string{"mistral:7b"}, enableToolLoop: true}
	srv, _, runtimeKey, _, _ := setupServerCustom(t, up, func(cfg *config.Config) {
		cfg.ToolCallingEnabled = true
		cfg.ToolServerBaseURL = toolSrv.URL
		cfg.ToolServerAPIKey = "tool-secret"
		cfg.ToolServerClientID = "glm-test-client"
		cfg.ToolCallingMaxIterations = 3
		cfg.ToolCallingTimeoutSeconds = 5
	})

	body := map[string]any{
		"model": "mistral:7b",
		"tools": []map[string]any{
			{"type": "function", "function": map[string]any{"name": "web_search", "parameters": map[string]any{"type": "object"}}},
		},
		"messages": []map[string]string{{"role": "user", "content": "Find DNS references"}},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+runtimeKey)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("X-GLM-Tool-Calling") != "enabled" {
		t.Fatalf("expected tool calling enabled header, got %q", rr.Header().Get("X-GLM-Tool-Calling"))
	}
	if rr.Header().Get("X-GLM-Tool-Calls") != "1" {
		t.Fatalf("expected one tool call, got %q", rr.Header().Get("X-GLM-Tool-Calls"))
	}
	seenTool := false
	for _, m := range up.lastRequest.Messages {
		if m.Role == "tool" && m.ToolCallID != "" {
			seenTool = true
			break
		}
	}
	if !seenTool {
		t.Fatal("expected tool message sent upstream after tool execution")
	}
}

func TestToolCallingDisabledRejectsTools(t *testing.T) {
	srv, _, runtimeKey, _ := setupServer(t)
	body := map[string]any{
		"model":    "mistral:7b",
		"tools":    []map[string]any{{"type": "function", "function": map[string]any{"name": "web_search"}}},
		"messages": []map[string]string{{"role": "user", "content": "hello"}},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+runtimeKey)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestToolCallingWithReasoningSupported(t *testing.T) {
	toolSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/openapi.json" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"openapi":"3.1.0","paths":{"/tools/web_search":{"post":{"summary":"Web search","requestBody":{"content":{"application/json":{"schema":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}}}}}}}}`))
			return
		}
		if r.URL.Path == "/tools/web_search" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"results":[{"title":"DNS","url":"https://example.com"}]}`))
			return
		}
		t.Fatalf("unexpected path %q", r.URL.Path)
	}))
	defer toolSrv.Close()
	up := &fakeUpstream{models: []string{"mistral:7b"}, enableToolLoop: true}
	srv, _, runtimeKey, _, _ := setupServerCustom(t, up, func(cfg *config.Config) {
		cfg.ToolCallingEnabled = true
		cfg.ToolServerBaseURL = toolSrv.URL
		cfg.ToolServerAPIKey = "tool-secret"
		cfg.ToolServerClientID = "glm-test-client"
	})
	body := map[string]any{
		"model":     "mistral:7b",
		"tools":     []map[string]any{{"type": "function", "function": map[string]any{"name": "web_search"}}},
		"reasoning": map[string]any{"mode": "tot"},
		"messages":  []map[string]string{{"role": "user", "content": "hello"}},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+runtimeKey)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if rr.Header().Get("X-GLM-Tool-Calling") != "enabled" {
		t.Fatalf("expected tool calling enabled header, got %q", rr.Header().Get("X-GLM-Tool-Calling"))
	}
}

func TestToolCallingAutoDiscoversFromOpenAPI(t *testing.T) {
	toolSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/openapi.json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"openapi":"3.1.0","paths":{"/tools/web_search":{"post":{"summary":"Web search","requestBody":{"content":{"application/json":{"schema":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}}}}}}}}`))
		case "/tools/web_search":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"results":[{"title":"DNS","url":"https://example.com"}]}`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer toolSrv.Close()
	up := &fakeUpstream{models: []string{"mistral:7b"}, enableToolLoop: true}
	srv, _, runtimeKey, _, _ := setupServerCustom(t, up, func(cfg *config.Config) {
		cfg.ToolCallingEnabled = true
		cfg.ToolServerBaseURL = toolSrv.URL
		cfg.ToolServerAPIKey = "tool-secret"
		cfg.ToolServerClientID = "glm-test-client"
	})
	body := map[string]any{
		"model":       "mistral:7b",
		"tool_choice": "auto",
		"messages":    []map[string]string{{"role": "user", "content": "find dns"}},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+runtimeKey)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("X-GLM-Tool-Calls") != "1" {
		t.Fatalf("expected one tool call, got %q", rr.Header().Get("X-GLM-Tool-Calls"))
	}
}

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mike/cognitive-llm/internal/auth"
	"github.com/mike/cognitive-llm/internal/config"
	"github.com/mike/cognitive-llm/internal/model"
	"github.com/mike/cognitive-llm/internal/store/memory"
)

type fakeUpstream struct {
	models         []string
	listCalls      int
	lastModelUsed  string
	lastRequest    model.ChatCompletionRequest
	failMCTS       bool
	failMultiAgent bool
	failDirect     bool
	failToT        bool
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
	resp := model.ChatCompletionResponse{Model: req.Model}
	resp.Choices = []struct {
		Index   int `json:"index"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason,omitempty"`
	}{
		{Index: 0, Message: struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}{Role: "assistant", Content: "<thought>secret</thought><answer>safe</answer>"}},
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
	if up.lastRequest.Reasoning == nil {
		t.Fatal("expected reasoning options to be set on upstream request")
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

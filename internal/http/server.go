package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mike/cognitive-llm/internal/audit"
	"github.com/mike/cognitive-llm/internal/auth"
	"github.com/mike/cognitive-llm/internal/config"
	"github.com/mike/cognitive-llm/internal/document"
	"github.com/mike/cognitive-llm/internal/idempotency"
	"github.com/mike/cognitive-llm/internal/intent"
	"github.com/mike/cognitive-llm/internal/memorydynamics"
	"github.com/mike/cognitive-llm/internal/metareasoning"
	"github.com/mike/cognitive-llm/internal/model"
	"github.com/mike/cognitive-llm/internal/observability"
	"github.com/mike/cognitive-llm/internal/orchestrator"
	"github.com/mike/cognitive-llm/internal/policy"
	"github.com/mike/cognitive-llm/internal/ratelimit"
	"github.com/mike/cognitive-llm/internal/reasoning"
	"github.com/mike/cognitive-llm/internal/state"
	"github.com/mike/cognitive-llm/internal/store"
	"github.com/mike/cognitive-llm/internal/stylecontract"
	"github.com/mike/cognitive-llm/internal/symbolicoverlay"
	"github.com/mike/cognitive-llm/internal/toolcalling"
)

type upstream interface {
	ListModels(ctx context.Context) (model.ModelListResponse, error)
	ChatCompletions(ctx context.Context, req model.ChatCompletionRequest) (model.ChatCompletionResponse, error)
}

type Server struct {
	cfg      config.Config
	store    store.Store
	auth     *auth.Service
	policy   *policy.Service
	idem     *idempotency.Service
	limiter  *ratelimit.Limiter
	audit    *audit.Service
	router   *orchestrator.Router
	reasoner *reasoning.Executor
	docflow  *document.Orchestrator
	intent   *intent.Processor
	memory   *memorydynamics.Service
	meta     *metareasoning.Evaluator
	style    *stylecontract.Injector
	symbolic *symbolicoverlay.Service
	tools    *toolcalling.Client
	state    *state.Manager
	upstream upstream
	mux      *http.ServeMux
}

func NewServer(cfg config.Config, st store.Store, upstream upstream, control ...orchestrator.InventoryControlClient) *Server {
	if cfg.MemoryOpTimeout <= 0 {
		cfg.MemoryOpTimeout = 2 * time.Second
	}
	if cfg.ReasoningStageTimeout <= 0 {
		cfg.ReasoningStageTimeout = 60 * time.Second
	}
	if cfg.DocumentStageTimeout <= 0 {
		cfg.DocumentStageTimeout = 25 * time.Second
	}
	if cfg.MCTSStageTimeout <= 0 {
		cfg.MCTSStageTimeout = 35 * time.Second
	}
	if cfg.MultiAgentStageTimeout <= 0 {
		cfg.MultiAgentStageTimeout = 45 * time.Second
	}
	if cfg.DecomposeStageTimeout <= 0 {
		cfg.DecomposeStageTimeout = 40 * time.Second
	}
	var controlClient orchestrator.InventoryControlClient
	if len(control) > 0 {
		controlClient = control[0]
	}
	router := orchestrator.NewRouterWithControl(
		cfg.OrchestratorDefaultModel,
		cfg.OrchestratorAliases,
		cfg.OrchestratorFallback,
		orchestrator.RouterConfig{
			JITInventoryEnabled:    cfg.JITInventoryEnabled,
			ReconcileInterval:      time.Duration(cfg.JITReconcileSeconds) * time.Second,
			ReconcileJitter:        time.Duration(cfg.JITReconcileJitterSeconds) * time.Second,
			MaxModels:              cfg.JITMaxModels,
			StorageHighWatermark:   cfg.JITStorageHighWatermark,
			StorageTargetWatermark: cfg.JITStorageTargetWatermark,
			PullTimeout:            time.Duration(cfg.JITPullTimeoutSeconds) * time.Second,
			PruneEnabled:           cfg.JITPruneEnabled,
			IdealCoding:            cfg.JITIdealCoding,
			IdealExtraction:        cfg.JITIdealExtraction,
			IdealLightQA:           cfg.JITIdealLightQA,
			IdealGeneral:           cfg.JITIdealGeneral,
		},
		controlClient,
	)
	router.Start(context.Background())
	s := &Server{
		cfg:     cfg,
		store:   st,
		auth:    auth.NewService(st),
		policy:  policy.NewService(st, cfg.DefaultModel, cfg.ReasoningHiddenByDefault),
		idem:    idempotency.NewService(st, 10*time.Minute),
		limiter: ratelimit.New(cfg.RateLimitRPM, cfg.RateLimitRPM/2),
		audit:   audit.NewService(st),
		router:  router,
		reasoner: reasoning.NewExecutor(reasoning.Config{
			Enabled:                            cfg.ReasoningPipelineEnabled,
			DefaultBranches:                    cfg.ReasoningPipelineDefaultBranches,
			MaxBranches:                        cfg.ReasoningPipelineMaxBranches,
			PruningEnabled:                     cfg.ReasoningPruningEnabled,
			PruningMinScore:                    cfg.ReasoningPruningMinScore,
			PruningToTTopK:                     cfg.ReasoningPruningToTTopK,
			PruningToTSynthTopK:                cfg.ReasoningPruningToTSynthTopK,
			PruningMCTSPoolTopK:                cfg.ReasoningPruningMCTSPoolTopK,
			PruningMCTSSynthTopK:               cfg.ReasoningPruningMCTSSynthTopK,
			PruningMARoundTopK:                 cfg.ReasoningPruningMARoundTopK,
			PruningMASynthTopK:                 cfg.ReasoningPruningMASynthTopK,
			SelfEvalCurveEnabled:               cfg.SelfEvalCurveEnabled,
			SelfEvalCurveLowMax:                cfg.SelfEvalCurveLowMax,
			SelfEvalCurveMidMax:                cfg.SelfEvalCurveMidMax,
			SelfEvalCurveLowWeight:             cfg.SelfEvalCurveLowWeight,
			SelfEvalCurveMidWeight:             cfg.SelfEvalCurveMidWeight,
			SelfEvalCurveHighWeight:            cfg.SelfEvalCurveHighWeight,
			SelfEvalCurveBias:                  cfg.SelfEvalCurveBias,
			MCTSEnabled:                        cfg.MCTSEnabled,
			MCTSDefaultRollouts:                cfg.MCTSDefaultRollouts,
			MCTSMaxRollouts:                    cfg.MCTSMaxRollouts,
			MCTSDefaultDepth:                   cfg.MCTSDefaultDepth,
			MCTSMaxDepth:                       cfg.MCTSMaxDepth,
			MCTSDefaultExploration:             cfg.MCTSDefaultExploration,
			MCTSV2Enabled:                      cfg.MCTSV2Enabled,
			MCTSEarlyStopWindow:                cfg.MCTSEarlyStopWindow,
			MCTSEarlyStopDelta:                 cfg.MCTSEarlyStopDelta,
			MultiAgentEnabled:                  cfg.MultiAgentEnabled,
			MultiAgentMaxAgents:                cfg.MultiAgentMaxAgents,
			MultiAgentMaxRounds:                cfg.MultiAgentMaxRounds,
			MultiAgentBudgetTokens:             cfg.MultiAgentBudgetTokens,
			DecomposeEnabled:                   cfg.DecomposeEnabled,
			DecomposeMaxSubtasks:               cfg.DecomposeMaxSubtasks,
			DecomposeMaxDepth:                  cfg.DecomposeMaxDepth,
			DecomposeBudgetTokens:              cfg.DecomposeBudgetTokens,
			MemoryAnchoredReasoningEnabled:     cfg.MemoryAnchoredReasoningEnabled,
			MemoryAnchoredReasoningMaxAnchors:  cfg.MemoryAnchoredReasoningMaxAnchors,
			MemoryAnchoredReasoningMinCoverage: cfg.MemoryAnchoredReasoningMinCoverage,
			MemoryAnchoredReasoningScoreBonus:  cfg.MemoryAnchoredReasoningScoreBonus,
		}, router),
		docflow: document.New(document.Config{
			Enabled:           cfg.DocumentOrchestrationEnabled,
			DefaultChunkSize:  cfg.DocumentChunkSize,
			MaxDocuments:      cfg.DocumentMaxDocuments,
			MaxChunksPerDoc:   cfg.DocumentMaxChunksPerDoc,
			MaxLinkedSections: cfg.DocumentMaxLinks,
		}),
		intent: intent.NewProcessor(intent.Config{
			Enabled:            cfg.IntentPreprocessorEnabled,
			AmbiguityThreshold: cfg.IntentAmbiguityThreshold,
		}),
		memory: memorydynamics.New(st, memorydynamics.Config{
			Enabled:               cfg.MemoryDynamicsEnabled,
			HalfLifeHours:         cfg.MemoryHalfLifeHours,
			ReplayThreshold:       cfg.MemoryReplayThreshold,
			FreshnessWindowHours:  cfg.MemoryFreshnessWindowHours,
			ContextNodeLimit:      cfg.MemoryContextNodeLimit,
			UpdateConceptsPerTurn: cfg.MemoryUpdateConceptsPerTurn,
		}),
		meta: metareasoning.New(metareasoning.Config{
			Enabled:         cfg.MetaReasoningEnabled,
			DefaultProfile:  cfg.MetaReasoningDefaultProfile,
			AcceptThreshold: cfg.MetaReasoningAcceptThreshold,
			StrictThreshold: cfg.MetaReasoningStrictThreshold,
		}),
		style: stylecontract.New(stylecontract.Config{
			Enabled: cfg.StyleContractEnabled,
			Version: cfg.StyleContractVersion,
		}),
		symbolic: symbolicoverlay.New(symbolicoverlay.Config{
			Enabled:                    cfg.SymbolicOverlayEnabled,
			MaxSymbols:                 cfg.SymbolicOverlayMaxSymbols,
			MaxDocChars:                cfg.SymbolicOverlayMaxDocChars,
			StrictChecks:               cfg.SymbolicOverlayStrictCheck,
			SupervisionEnabled:         cfg.SymbolicSupervisionEnabled,
			SupervisionWarnThreshold:   cfg.SymbolicSupervisionWarnThreshold,
			SupervisionRejectThreshold: cfg.SymbolicSupervisionRejectThreshold,
			SupervisionAutoRevise:      cfg.SymbolicSupervisionAutoRevise,
			SupervisionMaxPasses:       cfg.SymbolicSupervisionMaxPasses,
		}),
		tools:    toolcalling.New(cfg.ToolServerBaseURL, cfg.ToolServerAPIKey, cfg.ToolServerClientID, time.Duration(cfg.ToolCallingTimeoutSeconds)*time.Second),
		state:    state.NewManager(cfg.StateHistoryWindow),
		upstream: upstream,
		mux:      http.NewServeMux(),
	}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traceID := r.Header.Get("X-Trace-ID")
		if traceID == "" {
			traceID = observability.NewTraceID()
		}
		ctx := observability.WithTraceID(r.Context(), traceID)
		w.Header().Set("X-Trace-ID", traceID)
		s.mux.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) routes() {
	s.mux.HandleFunc("/healthz", s.healthz)
	s.mux.HandleFunc("/readyz", s.readyz)
	s.mux.HandleFunc("/version", s.version)

	s.mux.HandleFunc("/v1/models", s.authz("runtime:models", s.listModels))
	s.mux.HandleFunc("/v1/chat/completions", s.authz("runtime:chat", s.chatCompletions))
	s.mux.HandleFunc("/v1/cognition", s.authz("runtime:chat", s.cognition))

	s.mux.HandleFunc("/admin/v1/tenants", s.authz("admin:write", s.createTenant))
	s.mux.HandleFunc("/admin/v1/tenants/", s.authz("admin:write", s.tenantScopedAdmin))
	s.mux.HandleFunc("/admin/v1/orchestrator/debug", s.authz("admin:write", s.debugOrchestrator))
	s.mux.HandleFunc("/admin/v1/state/", s.authz("admin:write", s.getSessionState))
}

func (s *Server) authz(scope string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, err := s.auth.Authenticate(r.Context(), r.Header.Get("Authorization"))
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if err := auth.RequireScope(ctx, scope); err != nil {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
		next(w, r.WithContext(ctx))
	}
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Health(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "store not ready")
		return
	}
	if _, err := s.upstream.ListModels(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "upstream not ready")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) version(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"service": "glm-api", "version": "v0.6.0"})
}

func (s *Server) listModels(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantID := auth.TenantID(ctx)
	p := s.policy.ResolveModelPolicy(ctx, tenantID)
	resp, err := s.upstream.ListModels(ctx)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to list upstream models")
		return
	}
	if len(p.AllowedModels) > 0 {
		allowed := map[string]struct{}{}
		for _, m := range p.AllowedModels {
			allowed[strings.ToLower(m)] = struct{}{}
		}
		filtered := make([]model.ModelInfo, 0, len(resp.Data))
		for _, m := range resp.Data {
			if _, ok := allowed[strings.ToLower(m.ID)]; ok {
				filtered = append(filtered, m)
			}
		}
		resp.Data = filtered
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) chatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	ctx := r.Context()
	tenantID := auth.TenantID(ctx)
	keyID := auth.KeyID(ctx)

	if !s.limiter.Allow(keyID) {
		writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
		return
	}

	var req model.ChatCompletionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	cognitivePolicy := s.policy.ResolveCognitivePolicy(ctx, tenantID)
	policyGateStatus, gateErr := enforceCognitivePolicy(&req, cognitivePolicy)
	if gateErr != nil {
		writeError(w, http.StatusForbidden, gateErr.Error())
		return
	}
	if len(req.Messages) == 0 {
		writeError(w, http.StatusBadRequest, "messages are required")
		return
	}
	if req.Stream && (len(req.Tools) > 0 || shouldAutoloadTools(req)) {
		writeError(w, http.StatusBadRequest, "stream with tools is not supported")
		return
	}
	if (len(req.Tools) > 0 || shouldAutoloadTools(req)) && !s.cfg.ToolCallingEnabled {
		writeError(w, http.StatusBadRequest, "tool calling is disabled")
		return
	}
	if req.Reasoning != nil && strings.EqualFold(strings.TrimSpace(req.Reasoning.Mode), "multi_agent") && !req.Reasoning.MultiAgentEnabled {
		writeError(w, http.StatusBadRequest, "multi_agent_enabled must be true when reasoning.mode=multi_agent")
		return
	}
	if req.Reasoning != nil && strings.EqualFold(strings.TrimSpace(req.Reasoning.Mode), "decompose") && !req.Reasoning.DecomposeEnabled {
		writeError(w, http.StatusBadRequest, "decompose_enabled must be true when reasoning.mode=decompose")
		return
	}
	if req.MaxTokens == nil && s.cfg.DefaultMaxTokens > 0 {
		v := s.cfg.DefaultMaxTokens
		req.MaxTokens = &v
	}
	req, intentResult := s.intent.Process(req)
	if intentResult.Category != "" {
		w.Header().Set("X-GLM-Intent-Category", intentResult.Category)
		w.Header().Set("X-GLM-Ambiguity-Score", formatFloat(intentResult.AmbiguityScore))
		if intentResult.NeedsRewrite {
			w.Header().Set("X-GLM-Intent-Rewritten", "true")
		}
	}
	if err := s.symbolic.ValidateRequest(req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	sessionID := s.state.ResolveSessionID(req.SessionID, r.Header.Get("X-Session-ID"), keyID)
	w.Header().Set("X-GLM-Session-ID", sessionID)
	stateSnapshot := s.state.RecordUserInput(sessionID, req)
	req = ensureResponseStyle(req, stateSnapshot, joinUserContent(req.Messages))
	if req.ResponseStyle != nil {
		w.Header().Set("X-GLM-Response-Style", "present")
		w.Header().Set("X-GLM-Breathing-Weight", formatFloat(req.ResponseStyle.BreathingWeight))
		w.Header().Set("X-GLM-Pacing", req.ResponseStyle.Pacing)
		if strings.TrimSpace(req.ResponseStyle.AudienceMode) != "" {
			w.Header().Set("X-GLM-Style-Audience", req.ResponseStyle.AudienceMode)
		}
		if len(req.ResponseStyle.MicroSwitches) > 0 {
			w.Header().Set("X-GLM-Micro-Switches", strings.Join(req.ResponseStyle.MicroSwitches, ","))
		}
		if len(req.ResponseStyle.RiskFlags) > 0 {
			w.Header().Set("X-GLM-Risk-Flags", strings.Join(req.ResponseStyle.RiskFlags, ","))
		}
	}
	if s.style.Enabled() {
		req.Messages = s.style.Inject(req.Messages, req.ResponseStyle)
		w.Header().Set("X-GLM-Style-Contract", s.style.Version())
	}
	if s.cfg.EmotionalModulationEnabled {
		req.Messages = injectToneMetadata(req.Messages, stateSnapshot)
	}
	toolRequested := len(req.Tools) > 0 || shouldAutoloadTools(req)
	if toolRequested {
		w.Header().Set("X-GLM-Tool-Calling", "enabled")
	}
	symbolicRequested := req.SymbolicOverlay != nil
	symbolicMode := ""
	symbolicTypes := ""
	symbolicApplied := false
	symbolicError := false
	symbolicViolations := 0
	symbolicArtifact := symbolicoverlay.OverlayArtifact{}
	symbolicSupervisionStatus := "disabled"
	symbolicSupervision := symbolicoverlay.SupervisionResult{
		Enabled:  false,
		Applied:  false,
		Decision: "disabled",
		Action:   "none",
		Reason:   "not_applicable",
	}
	if symbolicRequested {
		preparedReq, symbolicRes, symErr := s.symbolic.Prepare(req, stateSnapshot)
		if symErr != nil {
			symbolicError = true
			w.Header().Set("X-GLM-Symbolic-Overlay", "error")
			w.Header().Set("X-GLM-Symbolic-Error", "true")
			log.Printf("symbolic overlay prepare failed: %v", symErr)
		} else {
			req = preparedReq
			symbolicMode = symbolicRes.Mode
			symbolicTypes = strings.Join(symbolicRes.Types, ",")
			symbolicArtifact = symbolicRes.Artifact
			if symbolicRes.Applied {
				symbolicApplied = true
				w.Header().Set("X-GLM-Symbolic-Overlay", "applied")
				w.Header().Set("X-GLM-Symbolic-Symbols", strconv.Itoa(symbolicRes.SymbolCount))
			} else {
				w.Header().Set("X-GLM-Symbolic-Overlay", "skipped")
			}
			if symbolicMode != "" {
				w.Header().Set("X-GLM-Symbolic-Mode", symbolicMode)
			}
			if symbolicTypes != "" {
				w.Header().Set("X-GLM-Symbolic-Types", symbolicTypes)
			}
			if strings.TrimSpace(symbolicRes.SchemaVersion) != "" {
				w.Header().Set("X-GLM-Symbolic-Version", symbolicRes.SchemaVersion)
			}
			if strings.TrimSpace(symbolicRes.Profile) != "" {
				w.Header().Set("X-GLM-Symbolic-Profile", symbolicRes.Profile)
			}
		}
	}
	requestedReasoningMode := ""
	if req.Reasoning != nil {
		requestedReasoningMode = strings.ToLower(strings.TrimSpace(req.Reasoning.Mode))
		switch requestedReasoningMode {
		case "tot", "pipeline":
			if !s.cfg.ReasoningPipelineEnabled {
				writeError(w, http.StatusBadRequest, "reasoning pipeline is disabled")
				return
			}
		case "mcts":
			if !s.cfg.ReasoningPipelineEnabled || !s.cfg.MCTSEnabled {
				writeError(w, http.StatusBadRequest, "mcts reasoning mode is disabled")
				return
			}
		case "multi_agent":
			if !s.cfg.ReasoningPipelineEnabled || !s.cfg.MultiAgentEnabled {
				writeError(w, http.StatusBadRequest, "multi_agent reasoning mode is disabled")
				return
			}
		case "decompose":
			if !s.cfg.ReasoningPipelineEnabled || !s.cfg.DecomposeEnabled {
				writeError(w, http.StatusBadRequest, "decompose reasoning mode is disabled")
				return
			}
		}
	}
	policyRec := s.policy.ResolveModelPolicy(ctx, tenantID)
	routingReason := ""
	routingClass := ""
	autoRoute := s.cfg.OrchestratorEnabled && orchestrator.ShouldAutoRoute(req.Model)
	docflowEligible := s.docflow.ShouldApply(req)
	needsInventory := autoRoute || docflowEligible
	available := []string{}
	if needsInventory {
		var err error
		available, err = s.listAvailableModels(ctx)
		if err != nil {
			outcomeTag := "error|route=model_inventory_failed"
			if autoRoute {
				outcomeTag = "error|route=auto.model_inventory_failed"
			}
			s.audit.Record(ctx, model.AuditEvent{
				TenantID:  tenantID,
				ActorType: "api_key",
				ActorID:   keyID,
				Endpoint:  "/v1/chat/completions",
				Model:     "",
				Outcome:   outcomeTag,
				LatencyMS: 0,
				TraceID:   observability.TraceID(ctx),
			})
			writeError(w, http.StatusBadGateway, "upstream model inventory failed")
			return
		}
	}
	if autoRoute {
		decision, err := s.router.ChooseWithState(req, available, policyRec, &stateSnapshot)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "no allowed available model for auto routing")
			return
		}
		req.Model = decision.ChosenModel
		routingReason = decision.Reason
		routingClass = decision.TaskClass
		w.Header().Set("X-GLM-Routed-Model", decision.ChosenModel)
		w.Header().Set("X-GLM-Routing-Reason", decision.Reason)
		if decision.IdealModel != "" {
			w.Header().Set("X-GLM-Ideal-Model", decision.IdealModel)
			w.Header().Set("X-GLM-Ideal-Available", strconv.FormatBool(decision.IdealAvailable))
		}
		if decision.JITPullTriggered {
			w.Header().Set("X-GLM-JIT-Pull-Triggered", decision.IdealModel)
		}
		if decision.InventoryStale {
			w.Header().Set("X-GLM-JIT-Inventory-Stale", "true")
		}
	} else {
		if req.Model == "" || strings.EqualFold(req.Model, "auto") {
			req.Model = policyRec.PrimaryModel
		}
	}

	if needsInventory {
		canonical, found := canonicalModelID(req.Model, available)
		if !found {
			s.audit.Record(ctx, model.AuditEvent{
				TenantID:  tenantID,
				ActorType: "api_key",
				ActorID:   keyID,
				Endpoint:  "/v1/chat/completions",
				Model:     req.Model,
				Outcome:   "error|route=explicit.model_unavailable",
				LatencyMS: 0,
				TraceID:   observability.TraceID(ctx),
			})
			writeError(w, http.StatusServiceUnavailable, "requested model is not available upstream")
			return
		}
		req.Model = canonical
	}

	if !policy.Allowed(req.Model, policyRec) {
		writeError(w, http.StatusForbidden, "model not allowed")
		return
	}
	if docflowEligible {
		var docResult document.Result
		docCtx, cancelDoc := context.WithTimeout(ctx, s.cfg.DocumentStageTimeout)
		docReq, docRes, derr := s.docflow.Prepare(docCtx, s.upstream, req)
		cancelDoc()
		if derr != nil {
			w.Header().Set("X-GLM-Document-Orchestration", "skipped")
			w.Header().Set("X-GLM-Document-Error", "true")
			log.Printf("document orchestration failed: %v", derr)
		} else {
			req = docReq
			docResult = docRes
			if docResult.Applied {
				w.Header().Set("X-GLM-Document-Orchestration", "applied")
				w.Header().Set("X-GLM-Document-Model", req.Model)
				w.Header().Set("X-GLM-Document-Count", strconv.Itoa(docResult.DocumentCount))
				w.Header().Set("X-GLM-Document-Chunks", strconv.Itoa(docResult.ChunkCount))
				w.Header().Set("X-GLM-Document-Links", strconv.Itoa(docResult.LinksCount))
			}
		}
	}
	memCtx, cancelMem := context.WithTimeout(ctx, s.cfg.MemoryOpTimeout)
	memReq, memRes, merr := s.memory.BuildContext(memCtx, tenantID, sessionID, req)
	cancelMem()
	if merr != nil {
		w.Header().Set("X-GLM-Memory-Error", "true")
		log.Printf("memory dynamics build context failed: %v", merr)
	} else {
		req = memReq
		req.MemoryAnchorKeys = limitAnchorKeys(memRes.Keys, s.cfg.MemoryAnchoredReasoningMaxAnchors)
		if memRes.Applied {
			w.Header().Set("X-GLM-Memory-Nodes", strconv.Itoa(memRes.NodeCount))
			if memRes.ReplayTriggered {
				w.Header().Set("X-GLM-Memory-Replay", "triggered")
			} else {
				w.Header().Set("X-GLM-Memory-Replay", "none")
			}
		}
	}

	idemKey := r.Header.Get("Idempotency-Key")
	reqHash := s.idem.RequestHash(req)
	if idemKey != "" {
		if resp, cachedHash, ok := s.idem.Get(tenantID, idemKey); ok {
			if cachedHash != reqHash {
				writeError(w, http.StatusConflict, "idempotency key reused with different payload")
				return
			}
			writeJSON(w, http.StatusOK, resp)
			return
		}
		if rec, err := s.idem.GetDurable(ctx, tenantID, idemKey); err == nil && rec.RequestHash != reqHash {
			writeError(w, http.StatusConflict, "idempotency key reused with different payload")
			return
		}
	}

	started := time.Now()
	var resp model.ChatCompletionResponse
	var err error
	var reasoningTrace *reasoning.Trace
	usedModel := req.Model
	outcome := "success"
	outcome = outcome + "|cognitive_policy=" + cognitivePolicy.Status + "|policy_gate=" + policyGateStatus
	if toolRequested {
		outcome = outcome + "|tool_calling=true"
	}
	if routingReason != "" {
		outcome = outcome + "|route=" + routingReason
		if routingClass != "" {
			outcome = outcome + "|class=" + routingClass
		}
		if autoRoute {
			if idealModel := w.Header().Get("X-GLM-Ideal-Model"); idealModel != "" {
				outcome = outcome + "|jit_ideal=" + idealModel
				outcome = outcome + "|jit_ideal_available=" + w.Header().Get("X-GLM-Ideal-Available")
			}
			if w.Header().Get("X-GLM-JIT-Pull-Triggered") != "" {
				outcome = outcome + "|jit_pull_triggered=true"
			} else {
				outcome = outcome + "|jit_pull_triggered=false"
			}
			if w.Header().Get("X-GLM-JIT-Inventory-Stale") == "true" {
				outcome = outcome + "|jit_inventory_stale=true"
			}
		}
	}
	if intentResult.Category != "" {
		outcome = outcome + "|intent=" + intentResult.Category
	}
	if memRes.Applied {
		outcome = outcome + "|memory_nodes=" + strconv.Itoa(memRes.NodeCount)
		if memRes.ReplayTriggered {
			outcome = outcome + "|memory_replay=triggered"
		}
	}
	execUpstream := s.upstreamForRequest(req)
	if s.reasoner.ShouldExecute(req, stateSnapshot) {
		var trace reasoning.Trace
		reasonMode := ""
		if req.Reasoning != nil {
			reasonMode = strings.ToLower(strings.TrimSpace(req.Reasoning.Mode))
		}
		reasonCtx, cancelReason := context.WithTimeout(ctx, s.resolveReasoningTimeout(req))
		resp, trace, err = s.reasoner.Execute(reasonCtx, execUpstream, req, policyRec, stateSnapshot)
		cancelReason()
		if err != nil {
			w.Header().Set("X-GLM-Reasoning-Pipeline", "fallback")
			w.Header().Set("X-GLM-Reasoning-Error", "true")
			log.Printf("reasoning pipeline failed, evaluating fail-open path: %v", err)
			if reasonMode == "multi_agent" && s.cfg.MultiAgentFailOpen {
				mctsReq := req
				if mctsReq.Reasoning == nil {
					mctsReq.Reasoning = &model.ReasoningOptions{}
				}
				mctsReq.Reasoning.Mode = "mcts"
				mctsReq.Reasoning.MultiAgentEnabled = false
				mctsCtx, cancelMCTS := context.WithTimeout(ctx, s.resolveReasoningTimeout(mctsReq))
				resp, trace, err = s.reasoner.ExecuteMCTS(mctsCtx, execUpstream, mctsReq, policyRec, stateSnapshot)
				cancelMCTS()
				if err == nil {
					reasoningTrace = &trace
					w.Header().Set("X-GLM-Reasoning-Pipeline", "mcts")
					w.Header().Set("X-GLM-MA-Fallback", "mcts")
					if trace.MCTS != nil {
						w.Header().Set("X-GLM-MCTS-Rollouts", strconv.Itoa(trace.MCTS.Rollouts))
						w.Header().Set("X-GLM-MCTS-Depth", strconv.Itoa(trace.MCTS.Depth))
						w.Header().Set("X-GLM-MCTS-Best-Score", formatFloat(trace.MCTS.BestScore))
						w.Header().Set("X-GLM-MCTS-Rollouts-Executed", strconv.Itoa(trace.MCTS.RolloutsExecuted))
						w.Header().Set("X-GLM-MCTS-Early-Stop", strconv.FormatBool(trace.MCTS.EarlyStop))
						if mctsV2EnabledForRequest(req, s.cfg) {
							w.Header().Set("X-GLM-MCTS-V2", "enabled")
						} else {
							w.Header().Set("X-GLM-MCTS-V2", "disabled")
						}
					}
					outcome = outcome + "|ma_fallback=mcts"
					if trace.ChosenModel != "" {
						usedModel = trace.ChosenModel
					}
				} else {
					log.Printf("multi-agent fail-open mcts failed: %v", err)
					totReq := req
					if totReq.Reasoning == nil {
						totReq.Reasoning = &model.ReasoningOptions{}
					}
					totReq.Reasoning.Mode = "tot"
					totReq.Reasoning.MultiAgentEnabled = false
					totCtx, cancelTot := context.WithTimeout(ctx, s.cfg.ReasoningStageTimeout)
					resp, trace, err = s.reasoner.ExecuteToT(totCtx, execUpstream, totReq, policyRec, stateSnapshot)
					cancelTot()
					if err == nil {
						reasoningTrace = &trace
						w.Header().Set("X-GLM-Reasoning-Pipeline", "tot")
						w.Header().Set("X-GLM-Reasoning-Branches", strconv.Itoa(len(trace.Branches)))
						w.Header().Set("X-GLM-MA-Fallback", "tot")
						outcome = outcome + "|ma_fallback=tot|pipeline=tot"
						if trace.ChosenModel != "" {
							usedModel = trace.ChosenModel
						}
					} else {
						log.Printf("multi-agent fail-open tot failed, falling back direct: %v", err)
						directReq := req
						directReq.Reasoning = nil
						resp, err = execUpstream.ChatCompletions(ctx, directReq)
						if err == nil {
							w.Header().Set("X-GLM-Reasoning-Pipeline", "direct")
							w.Header().Set("X-GLM-MA-Fallback", "direct")
							outcome = outcome + "|ma_fallback=direct"
						}
					}
				}
			} else if reasonMode == "decompose" && s.cfg.DecomposeFailOpen {
				totReq := req
				if totReq.Reasoning == nil {
					totReq.Reasoning = &model.ReasoningOptions{}
				}
				totReq.Reasoning.Mode = "tot"
				totReq.Reasoning.DecomposeEnabled = false
				totCtx, cancelTot := context.WithTimeout(ctx, s.cfg.ReasoningStageTimeout)
				resp, trace, err = s.reasoner.ExecuteToT(totCtx, execUpstream, totReq, policyRec, stateSnapshot)
				cancelTot()
				if err == nil {
					reasoningTrace = &trace
					w.Header().Set("X-GLM-Reasoning-Pipeline", "tot")
					w.Header().Set("X-GLM-Reasoning-Branches", strconv.Itoa(len(trace.Branches)))
					w.Header().Set("X-GLM-Decompose-Fallback", "tot")
					outcome = outcome + "|decompose_fallback=tot|pipeline=tot"
					if trace.ChosenModel != "" {
						usedModel = trace.ChosenModel
					}
				} else {
					log.Printf("decompose fail-open tot failed, falling back direct: %v", err)
					directReq := req
					directReq.Reasoning = nil
					resp, err = execUpstream.ChatCompletions(ctx, directReq)
					if err == nil {
						w.Header().Set("X-GLM-Reasoning-Pipeline", "direct")
						w.Header().Set("X-GLM-Decompose-Fallback", "direct")
						outcome = outcome + "|decompose_fallback=direct"
					}
				}
			} else if reasonMode == "mcts" && s.cfg.MCTSFailOpen {
				totReq := req
				if totReq.Reasoning == nil {
					totReq.Reasoning = &model.ReasoningOptions{}
				}
				totReq.Reasoning.Mode = "tot"
				totCtx, cancelTot := context.WithTimeout(ctx, s.cfg.ReasoningStageTimeout)
				resp, trace, err = s.reasoner.ExecuteToT(totCtx, execUpstream, totReq, policyRec, stateSnapshot)
				cancelTot()
				if err == nil {
					reasoningTrace = &trace
					w.Header().Set("X-GLM-Reasoning-Pipeline", "tot")
					w.Header().Set("X-GLM-Reasoning-Branches", strconv.Itoa(len(trace.Branches)))
					w.Header().Set("X-GLM-MCTS-Fallback", "tot")
					outcome = outcome + "|mcts_fallback=tot|pipeline=tot"
					if trace.ChosenModel != "" {
						usedModel = trace.ChosenModel
					}
				} else {
					log.Printf("mcts fail-open tot failed, falling back direct: %v", err)
					directReq := req
					directReq.Reasoning = nil
					resp, err = execUpstream.ChatCompletions(ctx, directReq)
					if err == nil {
						w.Header().Set("X-GLM-Reasoning-Pipeline", "direct")
						w.Header().Set("X-GLM-MCTS-Fallback", "direct")
						outcome = outcome + "|mcts_fallback=direct"
					}
				}
			} else {
				log.Printf("reasoning pipeline failed, falling back direct: %v", err)
				directReq := req
				directReq.Reasoning = nil
				resp, err = execUpstream.ChatCompletions(ctx, directReq)
				if err == nil {
					w.Header().Set("X-GLM-Reasoning-Pipeline", "direct")
				}
			}
		} else {
			reasoningTrace = &trace
			if trace.ChosenModel != "" {
				usedModel = trace.ChosenModel
			}
			switch strings.ToLower(trace.Mode) {
			case "multi_agent":
				w.Header().Set("X-GLM-Reasoning-Pipeline", "multi_agent")
				if trace.MultiAgent != nil {
					w.Header().Set("X-GLM-MA-Agents", strconv.Itoa(trace.MultiAgent.Agents))
					w.Header().Set("X-GLM-MA-Rounds", strconv.Itoa(trace.MultiAgent.Rounds))
					w.Header().Set("X-GLM-MA-Winner", trace.MultiAgent.Winner)
					w.Header().Set("X-GLM-MA-Consensus", trace.MultiAgent.Consensus)
					outcome = outcome +
						"|pipeline=multi_agent" +
						"|ma_agents=" + strconv.Itoa(trace.MultiAgent.Agents) +
						"|ma_rounds=" + strconv.Itoa(trace.MultiAgent.Rounds) +
						"|ma_winner=" + trace.MultiAgent.Winner +
						"|ma_consensus=" + trace.MultiAgent.Consensus
				} else {
					outcome = outcome + "|pipeline=multi_agent"
				}
			case "decompose":
				w.Header().Set("X-GLM-Reasoning-Pipeline", "decompose")
				if trace.Decompose != nil {
					w.Header().Set("X-GLM-Decompose-Subtasks-Planned", strconv.Itoa(trace.Decompose.SubtasksPlanned))
					w.Header().Set("X-GLM-Decompose-Subtasks-Executed", strconv.Itoa(trace.Decompose.SubtasksExecuted))
					w.Header().Set("X-GLM-Decompose-Best-Score", formatFloat(trace.Decompose.BestScore))
					outcome = outcome +
						"|pipeline=decompose" +
						"|decompose_subtasks=" + strconv.Itoa(trace.Decompose.SubtasksExecuted) +
						"|decompose_best=" + formatFloat(trace.Decompose.BestScore)
				} else {
					outcome = outcome + "|pipeline=decompose"
				}
			case "mcts":
				w.Header().Set("X-GLM-Reasoning-Pipeline", "mcts")
				if trace.MCTS != nil {
					w.Header().Set("X-GLM-MCTS-Rollouts", strconv.Itoa(trace.MCTS.Rollouts))
					w.Header().Set("X-GLM-MCTS-Depth", strconv.Itoa(trace.MCTS.Depth))
					w.Header().Set("X-GLM-MCTS-Best-Score", formatFloat(trace.MCTS.BestScore))
					w.Header().Set("X-GLM-MCTS-Rollouts-Executed", strconv.Itoa(trace.MCTS.RolloutsExecuted))
					w.Header().Set("X-GLM-MCTS-Early-Stop", strconv.FormatBool(trace.MCTS.EarlyStop))
					if mctsV2EnabledForRequest(req, s.cfg) {
						w.Header().Set("X-GLM-MCTS-V2", "enabled")
					} else {
						w.Header().Set("X-GLM-MCTS-V2", "disabled")
					}
					outcome = outcome +
						"|pipeline=mcts" +
						"|mcts_rollouts=" + strconv.Itoa(trace.MCTS.Rollouts) +
						"|mcts_depth=" + strconv.Itoa(trace.MCTS.Depth) +
						"|mcts_best=" + formatFloat(trace.MCTS.BestScore)
				} else {
					outcome = outcome + "|pipeline=mcts"
				}
			default:
				w.Header().Set("X-GLM-Reasoning-Pipeline", "tot")
				w.Header().Set("X-GLM-Reasoning-Branches", strconv.Itoa(len(trace.Branches)))
				outcome = outcome + "|pipeline=tot"
			}
			if trace.Contradictions.Detected {
				w.Header().Set("X-GLM-Reasoning-Contradictions", "detected")
				outcome = outcome + "|contradictions=detected"
			}
		}
	} else {
		if requestedReasoningMode != "" {
			w.Header().Set("X-GLM-Reasoning-Pipeline", "direct")
		}
		var toolCalls, toolIters int
		var toolErr error
		resp, toolCalls, toolIters, toolErr = s.chatWithTools(ctx, req)
		if toolRequested {
			w.Header().Set("X-GLM-Tool-Calls", strconv.Itoa(toolCalls))
			w.Header().Set("X-GLM-Tool-Iterations", strconv.Itoa(toolIters))
			if toolErr != nil {
				w.Header().Set("X-GLM-Tool-Error", "true")
				log.Printf("tool calling error: %v", toolErr)
			}
			outcome = outcome + "|tool_calls=" + strconv.Itoa(toolCalls) + "|tool_iterations=" + strconv.Itoa(toolIters)
		}
		err = toolErr
	}
	if reasoningTrace != nil && reasoningTrace.Pruning != nil {
		if reasoningTrace.Pruning.Enabled {
			w.Header().Set("X-GLM-Reasoning-Pruning", "enabled")
		} else {
			w.Header().Set("X-GLM-Reasoning-Pruning", "disabled")
		}
		w.Header().Set("X-GLM-Reasoning-Prune-In", strconv.Itoa(reasoningTrace.Pruning.CandidatesIn))
		w.Header().Set("X-GLM-Reasoning-Prune-Out", strconv.Itoa(reasoningTrace.Pruning.CandidatesOut))
		w.Header().Set("X-GLM-Reasoning-Prune-Dropped", strconv.Itoa(reasoningTrace.Pruning.DroppedLowScore+reasoningTrace.Pruning.DroppedTopK))
	}
	if reasoningTrace != nil && reasoningTrace.MemoryAnchor != nil {
		status := "disabled"
		if reasoningTrace.MemoryAnchor.Enabled {
			status = "enabled"
			if !reasoningTrace.MemoryAnchor.Applied {
				status = "skipped"
			}
		}
		w.Header().Set("X-GLM-Reasoning-Memory-Anchor", status)
		w.Header().Set("X-GLM-Reasoning-Memory-Anchors-In", strconv.Itoa(reasoningTrace.MemoryAnchor.AnchorsIn))
		w.Header().Set("X-GLM-Reasoning-Memory-Anchors-Used", strconv.Itoa(reasoningTrace.MemoryAnchor.AnchorsUsed))
		w.Header().Set("X-GLM-Reasoning-Memory-Coverage-Avg", formatFloat(reasoningTrace.MemoryAnchor.CoverageAvg))
		w.Header().Set("X-GLM-Reasoning-Memory-Bonus-Avg", formatFloat(reasoningTrace.MemoryAnchor.BonusAvg))
	}
	if err != nil && policyRec.FallbackModel != "" && policyRec.FallbackModel != req.Model {
		fallbackReq := req
		fallbackReq.Model = policyRec.FallbackModel
		if policy.Allowed(fallbackReq.Model, policyRec) {
			resp, err = execUpstream.ChatCompletions(ctx, fallbackReq)
			usedModel = fallbackReq.Model
			if err == nil {
				outcome = outcome + "|fallback=policy_model"
			}
		}
	}
	if err != nil {
		outcome = "error"
		s.audit.Record(ctx, model.AuditEvent{
			TenantID:  tenantID,
			ActorType: "api_key",
			ActorID:   keyID,
			Endpoint:  "/v1/chat/completions",
			Model:     usedModel,
			Outcome:   outcome,
			LatencyMS: time.Since(started).Milliseconds(),
			TraceID:   observability.TraceID(ctx),
		})
		writeError(w, http.StatusBadGateway, "upstream completion failed")
		return
	}
	if s.meta.ShouldRun(req) {
		metaResult, metaErr := safeMetaEvaluate(s.meta, req, resp, reasoningTrace, stateSnapshot)
		if metaErr != nil {
			w.Header().Set("X-GLM-Meta-Reasoning", "error")
			w.Header().Set("X-GLM-Meta-Reflection", "error")
			w.Header().Set("X-GLM-Meta-Reflection-Passes", "0")
			w.Header().Set("X-GLM-Meta-Reflection-Reason", "upstream_error")
			w.Header().Set("X-GLM-Self-Alignment", "error")
			w.Header().Set("X-GLM-Self-Alignment-Passes", "0")
			w.Header().Set("X-GLM-Self-Alignment-Reason", "upstream_error")
			w.Header().Set("X-GLM-Evaluator-Chain", "error")
			w.Header().Set("X-GLM-Evaluator-Depth", "0")
			w.Header().Set("X-GLM-Reflection-Layers", strconv.Itoa(metaReflectionPassesForRequest(req, s.cfg)))
			w.Header().Set("X-GLM-Reflection-Stop-Reason", "evaluator_error")
		} else {
			reflectionStatus := "disabled"
			reflectionPasses := 0
			reflectionReason := "no_trigger"
			finalMeta := metaResult
			chainNames := evaluatorChainForRequest(req, s.cfg)
			chainDepth := evaluatorChainDepthForRequest(req, s.cfg, len(chainNames))
			chainEnabled := evaluatorChainEnabledForRequest(req, s.cfg)
			chainResult := metareasoning.ChainResult{
				Enabled:    false,
				Depth:      0,
				StopReason: "disabled",
				Final:      metaResult,
			}
			if chainEnabled {
				chainResult = s.meta.EvaluateChain(req, resp, reasoningTrace, stateSnapshot, chainNames, chainDepth)
				finalMeta = chainResult.Final
			}
			reflectionEnabled := metaReflectionEnabledForRequest(req, s.cfg)
			if reflectionEnabled {
				reflectionStatus = "skipped"
				if shouldTriggerReflection(finalMeta, req, s.cfg) {
					maxPasses := metaReflectionPassesForRequest(req, s.cfg)
					if maxPasses < 1 {
						reflectionStatus = "skipped"
						reflectionReason = "budget_guard"
					} else {
						reflectionReason = "decision_trigger"
						currentResp := resp
						currentMeta := finalMeta
						appliedAny := false
						for pass := 1; pass <= maxPasses; pass++ {
							reflectionPasses = pass
							revisionReq := buildMetaReflectionRequest(req, usedModel, currentResp, currentMeta)
							revisedResp, reviseErr := execUpstream.ChatCompletions(ctx, revisionReq)
							if reviseErr != nil {
								reflectionStatus = "error"
								reflectionReason = "upstream_error"
								break
							}
							revisedMeta, revisedMetaErr := safeMetaEvaluate(s.meta, req, revisedResp, reasoningTrace, stateSnapshot)
							if revisedMetaErr != nil {
								reflectionStatus = "error"
								reflectionReason = "upstream_error"
								break
							}
							revisedChain := metareasoning.ChainResult{
								Enabled:    false,
								Depth:      0,
								StopReason: "disabled",
								Final:      revisedMeta,
							}
							if chainEnabled {
								revisedChain = s.meta.EvaluateChain(req, revisedResp, reasoningTrace, stateSnapshot, chainNames, chainDepth)
								revisedMeta = revisedChain.Final
							}
							if !shouldAdoptReflected(currentMeta, revisedMeta) {
								if appliedAny {
									reflectionStatus = "applied"
									reflectionReason = "no_improvement"
								}
								break
							}
							currentResp = revisedResp
							currentMeta = revisedMeta
							if chainEnabled {
								chainResult = revisedChain
							}
							appliedAny = true
							reflectionStatus = "applied"
							if chainEnabled && chainResult.StopReason == "policy_reject" {
								reflectionReason = "policy_reject"
								break
							}
							if chainEnabled && chainResult.StopReason == "risk_below_threshold" {
								reflectionReason = "risk_below_threshold"
								break
							}
							if !shouldTriggerReflection(currentMeta, req, s.cfg) {
								break
							}
						}
						if appliedAny {
							resp = currentResp
							finalMeta = currentMeta
						} else if chainEnabled && chainResult.StopReason != "" {
							reflectionReason = chainResult.StopReason
						}
					}
				}
			}

			w.Header().Set("X-GLM-Meta-Reasoning", "enabled")
			w.Header().Set("X-GLM-Meta-Decision", finalMeta.Decision)
			w.Header().Set("X-GLM-Meta-Confidence", formatFloat(finalMeta.Confidence))
			w.Header().Set("X-GLM-Meta-Risk-Score", formatFloat(finalMeta.RiskScore))
			w.Header().Set("X-GLM-Meta-Profile", finalMeta.Profile)
			w.Header().Set("X-GLM-Meta-Reflection", reflectionStatus)
			w.Header().Set("X-GLM-Meta-Reflection-Passes", strconv.Itoa(reflectionPasses))
			w.Header().Set("X-GLM-Meta-Reflection-Reason", reflectionReason)
			w.Header().Set("X-GLM-Self-Alignment", reflectionStatus)
			w.Header().Set("X-GLM-Self-Alignment-Passes", strconv.Itoa(reflectionPasses))
			w.Header().Set("X-GLM-Self-Alignment-Reason", reflectionReason)
			if chainEnabled {
				w.Header().Set("X-GLM-Evaluator-Chain", strings.Join(chainNames, ","))
				w.Header().Set("X-GLM-Evaluator-Depth", strconv.Itoa(chainResult.Depth))
			} else {
				w.Header().Set("X-GLM-Evaluator-Chain", "disabled")
				w.Header().Set("X-GLM-Evaluator-Depth", "0")
			}
			w.Header().Set("X-GLM-Reflection-Layers", strconv.Itoa(metaReflectionPassesForRequest(req, s.cfg)))
			stopReason := reflectionReason
			if chainEnabled && chainResult.StopReason != "" && reflectionStatus != "error" {
				stopReason = chainResult.StopReason
			}
			w.Header().Set("X-GLM-Reflection-Stop-Reason", stopReason)
			outcome = outcome +
				"|meta_decision=" + finalMeta.Decision +
				"|meta_conf=" + formatFloat(finalMeta.Confidence) +
				"|meta_risk=" + formatFloat(finalMeta.RiskScore) +
				"|meta_profile=" + finalMeta.Profile +
				"|meta_reflection=" + reflectionStatus +
				"|meta_reflection_reason=" + reflectionReason +
				"|meta_reflection_passes=" + strconv.Itoa(reflectionPasses) +
				"|evaluator_chain=" + w.Header().Get("X-GLM-Evaluator-Chain") +
				"|evaluator_depth=" + w.Header().Get("X-GLM-Evaluator-Depth") +
				"|reflection_layers=" + w.Header().Get("X-GLM-Reflection-Layers") +
				"|reflection_stop_reason=" + w.Header().Get("X-GLM-Reflection-Stop-Reason") +
				"|self_alignment=" + reflectionStatus +
				"|self_alignment_reason=" + reflectionReason +
				"|self_alignment_passes=" + strconv.Itoa(reflectionPasses)
		}
	}
	if symbolicRequested && symbolicMode == "strict" && symbolicApplied {
		comp, compErr := s.symbolic.CheckCompliance(req, resp, symbolicArtifact)
		if compErr != nil {
			symbolicError = true
			w.Header().Set("X-GLM-Symbolic-Overlay", "error")
			w.Header().Set("X-GLM-Symbolic-Error", "true")
			log.Printf("symbolic overlay compliance failed: %v", compErr)
		} else if comp.Checked {
			symbolicViolations = comp.ViolationCount
			w.Header().Set("X-GLM-Symbolic-Violations", strconv.Itoa(comp.ViolationCount))
			symbolicSupervision = s.symbolic.Supervise(comp)
			if symbolicSupervision.Enabled {
				symbolicSupervisionStatus = "applied"
				if symbolicSupervision.Action == "revise" {
					symbolicSupervision.Passes = 1
					revReq := buildSymbolicSupervisionRevisionRequest(req, usedModel, resp, symbolicArtifact, comp, symbolicSupervision)
					revResp, revErr := execUpstream.ChatCompletions(ctx, revReq)
					if revErr != nil {
						symbolicSupervisionStatus = "error"
						symbolicError = true
						symbolicSupervision.Reason = "upstream_error"
					} else {
						revComp, revCompErr := s.symbolic.CheckCompliance(req, revResp, symbolicArtifact)
						if revCompErr != nil {
							symbolicSupervisionStatus = "error"
							symbolicError = true
							symbolicSupervision.Reason = "compliance_error"
						} else {
							revSupervision := s.symbolic.Supervise(revComp)
							revSupervision.Passes = 1
							if shouldAdoptSymbolicRevision(comp, revComp, symbolicSupervision, revSupervision) {
								resp = revResp
								comp = revComp
								symbolicSupervision = revSupervision
								symbolicViolations = comp.ViolationCount
								w.Header().Set("X-GLM-Symbolic-Violations", strconv.Itoa(comp.ViolationCount))
							}
						}
					}
				}
			} else {
				if symbolicSupervision.Decision == "disabled" {
					symbolicSupervisionStatus = "disabled"
				} else {
					symbolicSupervisionStatus = "skipped"
				}
			}
		}
	}
	if symbolicRequested {
		w.Header().Set("X-GLM-Symbolic-Supervision", symbolicSupervisionStatus)
		w.Header().Set("X-GLM-Symbolic-Supervision-Decision", symbolicSupervision.Decision)
		w.Header().Set("X-GLM-Symbolic-Supervision-Action", symbolicSupervision.Action)
		w.Header().Set("X-GLM-Symbolic-Supervision-Reason", symbolicSupervision.Reason)
		w.Header().Set("X-GLM-Symbolic-Supervision-Nodes", strconv.Itoa(len(symbolicSupervision.Nodes)))
		w.Header().Set("X-GLM-Symbolic-Supervision-Passes", strconv.Itoa(symbolicSupervision.Passes))
		if reasoningTrace != nil {
			reasoningTrace.SymbolicSupervision = &reasoning.SymbolicSupervisionTrace{
				Enabled:    symbolicSupervision.Enabled,
				Decision:   symbolicSupervision.Decision,
				Action:     symbolicSupervision.Action,
				Reason:     symbolicSupervision.Reason,
				Nodes:      len(symbolicSupervision.Nodes),
				Violations: symbolicSupervision.ViolationCount,
				Passes:     symbolicSupervision.Passes,
			}
		}
	}
	if symbolicRequested {
		if symbolicMode == "" {
			symbolicMode = "assist"
		}
		outcome = outcome + "|symbolic=true" +
			"|symbolic_mode=" + symbolicMode +
			"|symbolic_types=" + symbolicTypes +
			"|symbolic_violations=" + strconv.Itoa(symbolicViolations) +
			"|symbolic_error=" + strconv.FormatBool(symbolicError) +
			"|symbolic_supervision=" + symbolicSupervisionStatus +
			"|symbolic_supervision_decision=" + symbolicSupervision.Decision +
			"|symbolic_supervision_action=" + symbolicSupervision.Action
	} else {
		outcome = outcome + "|symbolic=false"
	}

	if !policyRec.ReasoningVisible {
		stripReasoning(&resp)
	}
	_ = s.state.RecordAssistantOutput(sessionID, firstAssistantContent(resp))
	memUpdateCtx, cancelMemUpdate := context.WithTimeout(context.Background(), s.cfg.MemoryOpTimeout)
	if err := s.memory.UpdateFromTurn(memUpdateCtx, tenantID, sessionID, joinUserContent(req.Messages), firstAssistantContent(resp)); err != nil {
		log.Printf("memory dynamics update failed: %v", err)
	}
	cancelMemUpdate()

	if idemKey != "" {
		if err := s.idem.Save(ctx, tenantID, idemKey, reqHash, resp); err != nil {
			log.Printf("idempotency save failed: %v", err)
		}
	}

	s.audit.Record(ctx, model.AuditEvent{
		TenantID:  tenantID,
		ActorType: "api_key",
		ActorID:   keyID,
		Endpoint:  "/v1/chat/completions",
		Model:     usedModel,
		Outcome:   outcome,
		LatencyMS: time.Since(started).Milliseconds(),
		TraceID:   observability.TraceID(ctx),
	})

	writeJSON(w, http.StatusOK, resp)
}

func mctsV2EnabledForRequest(req model.ChatCompletionRequest, cfg config.Config) bool {
	if req.Reasoning != nil && req.Reasoning.MCTSV2Enabled {
		return true
	}
	return cfg.MCTSV2Enabled
}

func metaReflectionEnabledForRequest(req model.ChatCompletionRequest, cfg config.Config) bool {
	if req.Reasoning != nil && req.Reasoning.ReflectionLayersEnabled {
		return true
	}
	if req.Reasoning != nil && (req.Reasoning.MetaReflectionEnabled || req.Reasoning.SelfAlignmentEnabled) {
		return true
	}
	return cfg.ReflectionLayersEnabled || cfg.MetaReflectionEnabled || cfg.SelfAlignmentEnabled
}

func metaReflectionPassesForRequest(req model.ChatCompletionRequest, cfg config.Config) int {
	passes := cfg.ReflectionLayerCount
	if passes <= 0 {
		passes = cfg.MetaReflectionMaxPasses
	}
	if cfg.SelfAlignmentMaxPasses > 0 {
		passes = cfg.SelfAlignmentMaxPasses
	}
	if req.Reasoning != nil && req.Reasoning.ReflectionLayerCount > 0 {
		passes = req.Reasoning.ReflectionLayerCount
	}
	if req.Reasoning != nil && req.Reasoning.MetaReflectionMaxPasses > 0 {
		passes = req.Reasoning.MetaReflectionMaxPasses
	}
	if req.Reasoning != nil && req.Reasoning.SelfAlignmentMaxPasses > 0 {
		passes = req.Reasoning.SelfAlignmentMaxPasses
	}
	if passes < 0 {
		passes = 0
	}
	if passes > 4 {
		passes = 4
	}
	return passes
}

func shouldTriggerReflection(meta metareasoning.Result, req model.ChatCompletionRequest, cfg config.Config) bool {
	if !metaReflectionEnabledForRequest(req, cfg) {
		return false
	}
	decision := strings.ToLower(strings.TrimSpace(meta.Decision))
	if decision == "" {
		return false
	}
	allowed := map[string]struct{}{}
	for _, item := range cfg.MetaReflectionTriggerDecisions {
		v := strings.ToLower(strings.TrimSpace(item))
		if v == "" {
			continue
		}
		allowed[v] = struct{}{}
	}
	if len(allowed) == 0 {
		allowed["caution"] = struct{}{}
		allowed["reject"] = struct{}{}
	}
	_, ok := allowed[decision]
	return ok
}

func evaluatorChainEnabledForRequest(req model.ChatCompletionRequest, cfg config.Config) bool {
	if req.Reasoning != nil && req.Reasoning.EvaluatorChainEnabled {
		return true
	}
	return cfg.EvaluatorChainEnabled
}

func evaluatorChainForRequest(req model.ChatCompletionRequest, cfg config.Config) []string {
	chain := cfg.EvaluatorChain
	if req.Reasoning != nil && len(req.Reasoning.EvaluatorChain) > 0 {
		chain = req.Reasoning.EvaluatorChain
	}
	if len(chain) == 0 {
		chain = []string{"consistency", "risk", "policy", "factuality", "style"}
	}
	out := make([]string, 0, len(chain))
	for _, item := range chain {
		v := strings.ToLower(strings.TrimSpace(item))
		switch v {
		case "consistency", "risk", "policy", "factuality", "style":
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return []string{"risk"}
	}
	return out
}

func evaluatorChainDepthForRequest(req model.ChatCompletionRequest, cfg config.Config, chainLen int) int {
	depth := cfg.EvaluatorChainMaxDepth
	if depth <= 0 {
		depth = chainLen
	}
	if req.Reasoning != nil && req.Reasoning.EvaluatorChainMaxDepth > 0 {
		depth = req.Reasoning.EvaluatorChainMaxDepth
	}
	if depth > chainLen {
		depth = chainLen
	}
	if depth < 1 {
		depth = 1
	}
	if depth > 8 {
		depth = 8
	}
	return depth
}

func buildMetaReflectionRequest(
	req model.ChatCompletionRequest,
	usedModel string,
	original model.ChatCompletionResponse,
	meta metareasoning.Result,
) model.ChatCompletionRequest {
	revisionReq := req
	revisionReq.Model = strings.TrimSpace(usedModel)
	if revisionReq.Model == "" {
		revisionReq.Model = req.Model
	}
	revisionReq.Tools = nil
	revisionReq.ToolChoice = nil
	revisionReq.Stream = false
	revisionReq.Messages = buildMetaReflectionMessages(req, original, meta)
	return revisionReq
}

func buildMetaReflectionMessages(
	req model.ChatCompletionRequest,
	original model.ChatCompletionResponse,
	meta metareasoning.Result,
) []model.Message {
	userPrompt := strings.TrimSpace(joinUserContent(req.Messages))
	if userPrompt == "" {
		userPrompt = "No user prompt captured."
	}
	originalAnswer := strings.TrimSpace(firstAssistantContent(original))
	if originalAnswer == "" {
		originalAnswer = "No assistant answer generated."
	}
	flags := "none"
	if len(meta.Flags) > 0 {
		flags = strings.Join(meta.Flags, ", ")
	}
	content := "Revise the assistant answer using this evaluator feedback.\n" +
		"Keep user intent unchanged. Improve clarity, consistency, and risk handling.\n" +
		"Do not mention this review process.\n\n" +
		"User request:\n" + userPrompt + "\n\n" +
		"Current answer:\n" + originalAnswer + "\n\n" +
		"Evaluator decision: " + meta.Decision + "\n" +
		"Evaluator risk: " + formatFloat(meta.RiskScore) + "\n" +
		"Evaluator confidence: " + formatFloat(meta.Confidence) + "\n" +
		"Evaluator flags: " + flags + "\n\n" +
		"Return one improved final answer only."
	return []model.Message{
		{
			Role:    "system",
			Content: "You are a careful assistant revising a prior answer for quality and safety while preserving intent.",
		},
		{
			Role:    "user",
			Content: content,
		},
	}
}

func shouldAdoptReflected(before, after metareasoning.Result) bool {
	beforeRank := metaDecisionRank(before.Decision)
	afterRank := metaDecisionRank(after.Decision)
	if afterRank > beforeRank {
		return true
	}
	if afterRank == beforeRank && (before.RiskScore-after.RiskScore) >= 0.03 {
		return true
	}
	return false
}

func metaDecisionRank(decision string) int {
	switch strings.ToLower(strings.TrimSpace(decision)) {
	case "accept":
		return 2
	case "caution":
		return 1
	default:
		return 0
	}
}

func buildSymbolicSupervisionRevisionRequest(
	req model.ChatCompletionRequest,
	usedModel string,
	original model.ChatCompletionResponse,
	artifact symbolicoverlay.OverlayArtifact,
	comp symbolicoverlay.ComplianceResult,
	sup symbolicoverlay.SupervisionResult,
) model.ChatCompletionRequest {
	revisionReq := req
	revisionReq.Model = strings.TrimSpace(usedModel)
	if revisionReq.Model == "" {
		revisionReq.Model = req.Model
	}
	revisionReq.Tools = nil
	revisionReq.ToolChoice = nil
	revisionReq.Stream = false
	revisionReq.Messages = buildSymbolicSupervisionMessages(req, original, artifact, comp, sup)
	return revisionReq
}

func buildSymbolicSupervisionMessages(
	req model.ChatCompletionRequest,
	original model.ChatCompletionResponse,
	artifact symbolicoverlay.OverlayArtifact,
	comp symbolicoverlay.ComplianceResult,
	sup symbolicoverlay.SupervisionResult,
) []model.Message {
	userPrompt := strings.TrimSpace(joinUserContent(req.Messages))
	if userPrompt == "" {
		userPrompt = "No user prompt captured."
	}
	originalAnswer := strings.TrimSpace(firstAssistantContent(original))
	if originalAnswer == "" {
		originalAnswer = "No assistant answer generated."
	}
	artifactJSON, _ := json.Marshal(artifact)
	content := "Revise the assistant response to better satisfy symbolic constraints and risk signals.\n" +
		"Preserve user intent and avoid mentioning this supervision process.\n\n" +
		"User request:\n" + userPrompt + "\n\n" +
		"Current response:\n" + originalAnswer + "\n\n" +
		"Supervision decision: " + sup.Decision + "\n" +
		"Supervision action: " + sup.Action + "\n" +
		"Supervision reason: " + sup.Reason + "\n" +
		"Compliance violations: " + strconv.Itoa(comp.ViolationCount) + "\n" +
		"Compliance score: " + formatFloat(comp.Score) + "\n\n" +
		"Symbolic artifact:\n" + string(artifactJSON) + "\n\n" +
		"Return one improved final answer only."
	return []model.Message{
		{
			Role:    "system",
			Content: "You are a symbolic supervision reviser. Improve policy-consistency, constraints adherence, and risk handling without changing user intent.",
		},
		{
			Role:    "user",
			Content: content,
		},
	}
}

func shouldAdoptSymbolicRevision(
	before symbolicoverlay.ComplianceResult,
	after symbolicoverlay.ComplianceResult,
	beforeSup symbolicoverlay.SupervisionResult,
	afterSup symbolicoverlay.SupervisionResult,
) bool {
	beforeRank := symbolicDecisionRank(beforeSup.Decision)
	afterRank := symbolicDecisionRank(afterSup.Decision)
	if afterRank > beforeRank {
		return true
	}
	if after.ViolationCount < before.ViolationCount {
		return true
	}
	if afterRank == beforeRank && (after.Score-before.Score) >= 0.05 {
		return true
	}
	return false
}

func symbolicDecisionRank(decision string) int {
	switch strings.ToLower(strings.TrimSpace(decision)) {
	case "accept":
		return 2
	case "caution":
		return 1
	default:
		return 0
	}
}

func (s *Server) cognition(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var in model.CognitionRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	req, task := normalizeCognitionRequest(in, s.cfg.CognitionDefaultModel)
	if len(req.Messages) == 0 {
		writeError(w, http.StatusBadRequest, "messages or input are required")
		return
	}
	b, _ := json.Marshal(req)
	r2 := r.Clone(r.Context())
	r2.Body = io.NopCloser(bytes.NewReader(b))
	r2.ContentLength = int64(len(b))
	r2.Header.Set("Content-Type", "application/json")
	w.Header().Set("X-GLM-Cognition-Task", task)
	s.chatCompletions(w, r2)
}

func (s *Server) createTenant(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || strings.TrimSpace(in.Name) == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	tenant, err := s.store.CreateTenant(r.Context(), in.Name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed creating tenant")
		return
	}
	writeJSON(w, http.StatusCreated, tenant)
}

func (s *Server) tenantScopedAdmin(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/admin/v1/tenants/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	tenantID, action := parts[0], parts[1]
	switch action {
	case "keys":
		s.createTenantKey(w, r, tenantID)
	case "roles":
		s.createRole(w, r, tenantID)
	case "model-policy":
		s.upsertModelPolicy(w, r, tenantID)
	case "cognitive-policy":
		if r.Method == http.MethodGet {
			s.getCognitivePolicy(w, r, tenantID)
			return
		}
		s.upsertCognitivePolicy(w, r, tenantID)
	case "quotas":
		s.upsertQuota(w, r, tenantID)
	case "audit-events":
		s.listAuditEvents(w, r, tenantID)
	default:
		writeError(w, http.StatusNotFound, "not found")
	}
}

func (s *Server) createTenantKey(w http.ResponseWriter, r *http.Request, tenantID string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var in struct {
		Scopes    []string   `json:"scopes"`
		ExpiresAt *time.Time `json:"expires_at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if len(in.Scopes) == 0 {
		in.Scopes = []string{"runtime:*"}
	}
	raw, rec, err := s.auth.GenerateAPIKey(r.Context(), tenantID, in.Scopes, in.ExpiresAt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create api key")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"api_key": raw, "record": rec})
}

func (s *Server) createRole(w http.ResponseWriter, r *http.Request, tenantID string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var role model.Role
	if err := json.NewDecoder(r.Body).Decode(&role); err != nil || strings.TrimSpace(role.Name) == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	role.TenantID = tenantID
	created, err := s.store.CreateRole(r.Context(), role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create role")
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) upsertModelPolicy(w http.ResponseWriter, r *http.Request, tenantID string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var p model.ModelPolicy
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid policy")
		return
	}
	p.TenantID = tenantID
	if p.PrimaryModel == "" {
		p.PrimaryModel = s.cfg.DefaultModel
	}
	if len(p.AllowedModels) == 0 {
		p.AllowedModels = []string{p.PrimaryModel}
	}
	out, err := s.store.UpsertModelPolicy(r.Context(), p)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to upsert model policy")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) upsertCognitivePolicy(w http.ResponseWriter, r *http.Request, tenantID string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var p model.CognitivePolicy
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid cognitive policy")
		return
	}
	p.TenantID = tenantID
	if strings.TrimSpace(p.Status) == "" {
		p.Status = "active"
	}
	if strings.TrimSpace(p.Version) == "" {
		p.Version = "v1"
	}
	out, err := s.store.UpsertCognitivePolicy(r.Context(), p)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to upsert cognitive policy")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getCognitivePolicy(w http.ResponseWriter, r *http.Request, tenantID string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	out := s.policy.ResolveCognitivePolicy(r.Context(), tenantID)
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) upsertQuota(w http.ResponseWriter, r *http.Request, tenantID string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var q model.Quota
	if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
		writeError(w, http.StatusBadRequest, "invalid quota")
		return
	}
	q.TenantID = tenantID
	if q.RPMLimit <= 0 {
		q.RPMLimit = s.cfg.RateLimitRPM
	}
	if q.Burst <= 0 {
		q.Burst = q.RPMLimit / 2
	}
	out, err := s.store.UpsertQuota(r.Context(), q)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to upsert quota")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) listAuditEvents(w http.ResponseWriter, r *http.Request, tenantID string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	items, err := s.store.ListAuditEvents(r.Context(), tenantID, 100)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list audit events")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) debugOrchestrator(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	ctx := r.Context()
	tenantID := auth.TenantID(ctx)

	var req model.ChatCompletionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if len(req.Messages) == 0 {
		writeError(w, http.StatusBadRequest, "messages are required")
		return
	}
	sessionID := s.state.ResolveSessionID(req.SessionID, r.Header.Get("X-Session-ID"), auth.KeyID(ctx))
	stateSnapshot := s.state.RecordUserInput(sessionID, req)

	policyRec := s.policy.ResolveModelPolicy(ctx, tenantID)
	modelsResp, err := s.upstream.ListModels(ctx)
	if err != nil {
		writeError(w, http.StatusBadGateway, "upstream model inventory failed")
		return
	}
	available := make([]string, 0, len(modelsResp.Data))
	for _, m := range modelsResp.Data {
		available = append(available, m.ID)
	}
	info, err := s.router.ExplainWithState(req, available, policyRec, &stateSnapshot)
	if err != nil && !errors.Is(err, orchestrator.ErrNoAllowedAvailableModel) {
		writeError(w, http.StatusInternalServerError, "orchestrator explain failed")
		return
	}
	if errors.Is(err, orchestrator.ErrNoAllowedAvailableModel) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":    false,
			"error": "no allowed available model for auto routing",
			"debug": info,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "debug": info})
}

func firstAssistantContent(resp model.ChatCompletionResponse) string {
	if len(resp.Choices) == 0 {
		return ""
	}
	return resp.Choices[0].Message.Content
}

func joinUserContent(messages []model.Message) string {
	parts := make([]string, 0, len(messages))
	for _, m := range messages {
		if strings.EqualFold(m.Role, "user") {
			parts = append(parts, m.Content)
		}
	}
	return strings.Join(parts, "\n")
}

func (s *Server) getSessionState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	sessionID := strings.TrimPrefix(r.URL.Path, "/admin/v1/state/")
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		writeError(w, http.StatusBadRequest, "session_id is required in path")
		return
	}
	snap, ok := s.state.Snapshot(sessionID)
	if !ok {
		writeError(w, http.StatusNotFound, "session state not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"session": snap,
	})
}

var thoughtRe = regexp.MustCompile(`(?is)<thought>.*?</thought>`)

func stripReasoning(resp *model.ChatCompletionResponse) {
	for i := range resp.Choices {
		resp.Choices[i].Message.Content = strings.TrimSpace(thoughtRe.ReplaceAllString(resp.Choices[i].Message.Content, ""))
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{
			"message": msg,
			"type":    "invalid_request_error",
			"code":    status,
		},
	})
}

func hashString(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func injectToneMetadata(messages []model.Message, st state.CognitiveState) []model.Message {
	if len(messages) == 0 {
		return messages
	}
	meta := "cognitive_state " +
		"task_mode=" + st.TaskMode + " " +
		"topic=" + strings.Join(st.TopicKeywords, "|") + " " +
		"sentiment=" + st.Sentiment + " " +
		"carryover=" + formatFloat(st.SentimentCarryover) + " " +
		"mood=" + formatFloat(st.Mood) + " " +
		"arousal=" + formatFloat(st.Arousal) + " " +
		"energy=" + formatFloat(st.EmotionalEnergy) + " " +
		"tone=" + st.ToneCompensation.TargetTone + " " +
		"warmth=" + formatFloat(st.ToneCompensation.Warmth) + " " +
		"directness=" + formatFloat(st.ToneCompensation.Directness) + " " +
		"empathy=" + formatFloat(st.ToneCompensation.Empathy) + " " +
		"stability=" + formatFloat(st.ToneCompensation.Stability)
	system := model.Message{
		Role:    "system",
		Content: "Use this metadata to modulate tone while preserving factual accuracy and policy constraints: " + strings.TrimSpace(meta),
	}
	out := make([]model.Message, 0, len(messages)+1)
	out = append(out, system)
	out = append(out, messages...)
	return out
}

func formatFloat(v float64) string {
	return strings.TrimRight(strings.TrimRight(strconv.FormatFloat(v, 'f', 3, 64), "0"), ".")
}

func normalizeCognitionRequest(in model.CognitionRequest, cognitionDefaultModel string) (model.ChatCompletionRequest, string) {
	task := strings.ToLower(strings.TrimSpace(in.Task))
	if task == "" {
		task = "chat"
	}
	out := model.ChatCompletionRequest{
		Model:           in.Model,
		SessionID:       in.SessionID,
		Reasoning:       in.Reasoning,
		SymbolicOverlay: in.SymbolicOverlay,
		ResponseStyle:   in.ResponseStyle,
		Documents:       in.Documents,
		DocumentFlow:    in.DocumentFlow,
		Tools:           in.Tools,
		ToolChoice:      in.ToolChoice,
		Messages:        append([]model.Message{}, in.Messages...),
		Temperature:     in.Temperature,
		MaxTokens:       in.MaxTokens,
		Stream:          in.Stream,
	}
	if len(out.Messages) == 0 && strings.TrimSpace(in.Input) != "" {
		out.Messages = []model.Message{{Role: "user", Content: strings.TrimSpace(in.Input)}}
	}
	switch task {
	case "reasoning", "analysis":
		if out.Reasoning == nil {
			out.Reasoning = &model.ReasoningOptions{Mode: "tot", Branches: 3, SelfEvaluate: true, DetectContradictions: true}
		}
		if strings.TrimSpace(out.Model) == "" {
			out.Model = strings.TrimSpace(cognitionDefaultModel)
			if out.Model == "" {
				out.Model = "auto"
			}
		}
	case "document", "document_synthesis":
		if out.DocumentFlow == nil {
			out.DocumentFlow = &model.DocumentOrchestration{Mode: "hierarchical"}
		}
		if strings.TrimSpace(out.Model) == "" {
			out.Model = strings.TrimSpace(cognitionDefaultModel)
			if out.Model == "" {
				out.Model = "auto"
			}
		}
	case "extract", "classification":
		if strings.TrimSpace(out.Model) == "" {
			out.Model = strings.TrimSpace(cognitionDefaultModel)
			if out.Model == "" {
				out.Model = "auto"
			}
		}
	default:
		// chat/general passthrough
	}
	return out, task
}

func ensureResponseStyle(req model.ChatCompletionRequest, st state.CognitiveState, userText string) model.ChatCompletionRequest {
	if req.ResponseStyle == nil {
		req.ResponseStyle = &model.ResponseStyle{}
	}
	if req.ResponseStyle.BreathingWeight <= 0 {
		// Derived deterministically from emotional state. Model decides how to apply it.
		bw := 0.22 + (st.EmotionalEnergy * 0.35)
		if bw > 0.95 {
			bw = 0.95
		}
		if bw < 0.05 {
			bw = 0.05
		}
		req.ResponseStyle.BreathingWeight = bw
	}
	if strings.TrimSpace(req.ResponseStyle.Pacing) == "" {
		req.ResponseStyle.Pacing = st.Pacing
	}
	if len(req.ResponseStyle.MicroSwitches) == 0 {
		req.ResponseStyle.MicroSwitches = append([]string{}, st.MicroSwitches...)
	}
	if req.ResponseStyle.MoodShift <= 0 {
		req.ResponseStyle.MoodShift = st.MoodShift
	}
	if req.ResponseStyle.TopicDrift <= 0 {
		req.ResponseStyle.TopicDrift = st.TopicDrift
	}
	if req.ResponseStyle.RollingSentiment == 0 {
		req.ResponseStyle.RollingSentiment = st.SentimentCarryover
	}
	if req.ResponseStyle.ConversationDrift == 0 {
		req.ResponseStyle.ConversationDrift = st.TopicDrift
	}
	if strings.TrimSpace(req.ResponseStyle.SubtextDetection) == "" {
		req.ResponseStyle.SubtextDetection = "model-driven"
	}
	if strings.TrimSpace(req.ResponseStyle.Register) == "" {
		req.ResponseStyle.Register = "technical"
	}
	if strings.TrimSpace(req.ResponseStyle.VerbosityTarget) == "" {
		req.ResponseStyle.VerbosityTarget = "medium"
	}
	if strings.TrimSpace(req.ResponseStyle.JustificationDensity) == "" {
		req.ResponseStyle.JustificationDensity = "medium"
	}
	if strings.TrimSpace(req.ResponseStyle.AudienceMode) == "" {
		req.ResponseStyle.AudienceMode = "engineer"
	}
	if len(req.ResponseStyle.RiskFlags) == 0 {
		req.ResponseStyle.RiskFlags = buildRiskFlags(st, userText)
	}
	if strings.TrimSpace(req.ResponseStyle.ToneShift) == "" {
		switch {
		case st.MoodShift >= 0.2 || st.Pacing == "fast":
			req.ResponseStyle.ToneShift = "stabilize-calm"
		case st.TopicDrift >= 0.5:
			req.ResponseStyle.ToneShift = "re-anchor-context"
		default:
			req.ResponseStyle.ToneShift = "maintain"
		}
	}
	if strings.TrimSpace(req.ResponseStyle.StyleAdjustment) == "" {
		switch st.Pacing {
		case "fast":
			req.ResponseStyle.StyleAdjustment = "concise-structured"
		case "slow":
			req.ResponseStyle.StyleAdjustment = "expanded-guided"
		default:
			req.ResponseStyle.StyleAdjustment = "balanced"
		}
	}
	return req
}

func buildRiskFlags(st state.CognitiveState, userText string) []string {
	flags := make([]string, 0, 6)
	if st.SentimentCarryover <= -0.35 {
		flags = append(flags, "negative_sentiment_trend")
	}
	if st.TopicDrift >= 0.5 {
		flags = append(flags, "high_topic_drift")
	}
	if st.MoodShift >= 0.2 {
		flags = append(flags, "high_mood_shift")
	}
	if st.Pacing == "fast" {
		flags = append(flags, "rapid_turn_pacing")
	}
	lower := strings.ToLower(userText)
	if strings.Contains(lower, "tired") || strings.Contains(lower, "exhausted") || strings.Contains(lower, "burned out") {
		flags = append(flags, "fatigue_signal")
	}
	if strings.Contains(lower, "i can't") || strings.Contains(lower, "hopeless") || strings.Contains(lower, "overwhelmed") {
		flags = append(flags, "vulnerability_signal")
	}
	return dedupeStrings(flags)
}

func dedupeStrings(in []string) []string {
	if len(in) == 0 {
		return in
	}
	out := make([]string, 0, len(in))
	seen := map[string]struct{}{}
	for _, v := range in {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func limitAnchorKeys(in []string, max int) []string {
	if len(in) == 0 {
		return nil
	}
	if max <= 0 {
		max = 3
	}
	deduped := dedupeStrings(in)
	if len(deduped) > max {
		return deduped[:max]
	}
	return deduped
}

func (s *Server) listAvailableModels(ctx context.Context) ([]string, error) {
	modelsResp, err := s.upstream.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	available := make([]string, 0, len(modelsResp.Data))
	for _, m := range modelsResp.Data {
		available = append(available, m.ID)
	}
	return available, nil
}

func canonicalModelID(requested string, available []string) (string, bool) {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return "", false
	}
	for _, m := range available {
		if strings.EqualFold(strings.TrimSpace(m), requested) {
			return m, true
		}
	}
	return "", false
}

func (s *Server) resolveReasoningTimeout(req model.ChatCompletionRequest) time.Duration {
	if req.Reasoning != nil && strings.EqualFold(strings.TrimSpace(req.Reasoning.Mode), "multi_agent") {
		timeout := s.cfg.MultiAgentStageTimeout
		if timeout <= 0 {
			timeout = 45 * time.Second
		}
		if req.Reasoning.MultiAgentTimeoutMs > 0 {
			override := time.Duration(req.Reasoning.MultiAgentTimeoutMs) * time.Millisecond
			if override < timeout {
				timeout = override
			}
		}
		if timeout < time.Second {
			return time.Second
		}
		return timeout
	}
	if req.Reasoning != nil && strings.EqualFold(strings.TrimSpace(req.Reasoning.Mode), "mcts") {
		timeout := s.cfg.MCTSStageTimeout
		if timeout <= 0 {
			timeout = 35 * time.Second
		}
		if req.Reasoning.MCTSTimeoutMs > 0 {
			override := time.Duration(req.Reasoning.MCTSTimeoutMs) * time.Millisecond
			if override < timeout {
				timeout = override
			}
		}
		if timeout < time.Second {
			return time.Second
		}
		return timeout
	}
	if req.Reasoning != nil && strings.EqualFold(strings.TrimSpace(req.Reasoning.Mode), "decompose") {
		timeout := s.cfg.DecomposeStageTimeout
		if timeout <= 0 {
			timeout = 40 * time.Second
		}
		if timeout < time.Second {
			return time.Second
		}
		return timeout
	}
	if s.cfg.ReasoningStageTimeout <= 0 {
		return 60 * time.Second
	}
	return s.cfg.ReasoningStageTimeout
}

func enforceCognitivePolicy(req *model.ChatCompletionRequest, cp model.CognitivePolicy) (string, error) {
	status := strings.ToLower(strings.TrimSpace(cp.Status))
	if status == "" {
		status = "active"
	}
	if status == "disabled" {
		return "disabled", nil
	}
	if req == nil {
		return "allow", nil
	}
	if req.Reasoning != nil {
		mode := strings.ToLower(strings.TrimSpace(req.Reasoning.Mode))
		if mode == "pipeline" {
			mode = "tot"
		}
		if mode != "" && len(cp.AllowedReasoningModes) > 0 {
			if !containsFold(cp.AllowedReasoningModes, mode) {
				return "blocked_reasoning_mode", fmt.Errorf("reasoning mode %q is not allowed by cognitive policy", mode)
			}
		}
		if cp.MaxReasoningPasses > 0 {
			if req.Reasoning.Branches > cp.MaxReasoningPasses {
				req.Reasoning.Branches = cp.MaxReasoningPasses
			}
			if req.Reasoning.MCTSMaxRollouts > cp.MaxReasoningPasses {
				req.Reasoning.MCTSMaxRollouts = cp.MaxReasoningPasses
			}
			if req.Reasoning.MultiAgentMaxRounds > cp.MaxReasoningPasses {
				req.Reasoning.MultiAgentMaxRounds = cp.MaxReasoningPasses
			}
			if req.Reasoning.DecomposeMaxSubtasks > cp.MaxReasoningPasses {
				req.Reasoning.DecomposeMaxSubtasks = cp.MaxReasoningPasses
			}
		}
		if cp.MaxReflectionPasses > 0 && req.Reasoning.MetaReflectionMaxPasses > cp.MaxReflectionPasses {
			req.Reasoning.MetaReflectionMaxPasses = cp.MaxReflectionPasses
		}
		if cp.MaxSelfAlignmentPasses > 0 && req.Reasoning.SelfAlignmentMaxPasses > cp.MaxSelfAlignmentPasses {
			req.Reasoning.SelfAlignmentMaxPasses = cp.MaxSelfAlignmentPasses
		}
	}
	if len(req.Tools) > 0 {
		for _, tool := range req.Tools {
			name := strings.TrimSpace(tool.Function.Name)
			if name == "" {
				continue
			}
			if len(cp.ToolDenylist) > 0 && containsFold(cp.ToolDenylist, name) {
				return "blocked_tool_denylist", fmt.Errorf("tool %q is denied by cognitive policy", name)
			}
			if len(cp.ToolAllowlist) > 0 && !containsFold(cp.ToolAllowlist, name) {
				return "blocked_tool_allowlist", fmt.Errorf("tool %q is not in cognitive policy allowlist", name)
			}
		}
	}
	return "allow", nil
}

func containsFold(items []string, target string) bool {
	for _, item := range items {
		if strings.EqualFold(strings.TrimSpace(item), strings.TrimSpace(target)) {
			return true
		}
	}
	return false
}

func safeMetaEvaluate(
	e *metareasoning.Evaluator,
	req model.ChatCompletionRequest,
	resp model.ChatCompletionResponse,
	trace *reasoning.Trace,
	st state.CognitiveState,
) (res metareasoning.Result, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errors.New("meta evaluation panic")
		}
	}()
	return e.Evaluate(req, resp, trace, st), nil
}

func isNotFound(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "not found")
}

func isAuthErr(err error) bool {
	return errors.Is(err, auth.ErrUnauthorized) || errors.Is(err, auth.ErrForbidden)
}

package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	ServerAddr                       string
	ServerReadTimeout                time.Duration
	ServerWriteTimeout               time.Duration
	ServerIdleTimeout                time.Duration
	DefaultModel                     string
	CognitionDefaultModel            string
	DefaultMaxTokens                 int
	UpstreamBaseURL                  string
	UpstreamAPIKey                   string
	UpstreamTimeout                  time.Duration
	UpstreamRetryMax                 int
	PocketBaseURL                    string
	PocketBaseAuthColl               string
	PocketBaseIdentity               string
	PocketBasePassword               string
	PocketBaseAllowUnauth            bool
	OrchestratorEnabled              bool
	OrchestratorDefaultModel         string
	OrchestratorAliases              []string
	OrchestratorFallback             string
	JITInventoryEnabled              bool
	JITReconcileSeconds              int
	JITReconcileJitterSeconds        int
	JITMaxModels                     int
	JITStorageHighWatermark          float64
	JITStorageTargetWatermark        float64
	JITPullTimeoutSeconds            int
	JITPruneEnabled                  bool
	JITIdealCoding                   string
	JITIdealExtraction               string
	JITIdealLightQA                  string
	JITIdealGeneral                  string
	OllamaControlURL                 string
	OllamaControlAPIKey              string
	StateHistoryWindow               int
	EmotionalModulationEnabled       bool
	ReasoningPipelineEnabled         bool
	ReasoningPipelineDefaultBranches int
	ReasoningPipelineMaxBranches     int
	MCTSEnabled                      bool
	MCTSDefaultRollouts              int
	MCTSMaxRollouts                  int
	MCTSDefaultDepth                 int
	MCTSMaxDepth                     int
	MCTSDefaultExploration           float64
	MCTSStageTimeout                 time.Duration
	MCTSFailOpen                     bool
	MultiAgentEnabled                bool
	MultiAgentMaxAgents              int
	MultiAgentMaxRounds              int
	MultiAgentStageTimeout           time.Duration
	MultiAgentBudgetTokens           int
	MultiAgentFailOpen               bool
	IntentPreprocessorEnabled        bool
	IntentAmbiguityThreshold         float64
	DocumentOrchestrationEnabled     bool
	DocumentChunkSize                int
	DocumentMaxDocuments             int
	DocumentMaxChunksPerDoc          int
	DocumentMaxLinks                 int
	MemoryDynamicsEnabled            bool
	MemoryHalfLifeHours              float64
	MemoryReplayThreshold            float64
	MemoryFreshnessWindowHours       float64
	MemoryContextNodeLimit           int
	MemoryUpdateConceptsPerTurn      int
	MemoryOpTimeout                  time.Duration
	ReasoningStageTimeout            time.Duration
	DocumentStageTimeout             time.Duration
	StyleContractEnabled             bool
	StyleContractVersion             string
	SymbolicOverlayEnabled           bool
	SymbolicOverlayMaxSymbols        int
	SymbolicOverlayMaxDocChars       int
	SymbolicOverlayStrictCheck       bool
	ToolCallingEnabled               bool
	ToolServerBaseURL                string
	ToolServerAPIKey                 string
	ToolServerClientID               string
	ToolCallingMaxIterations         int
	ToolCallingTimeoutSeconds        int
	MetaReasoningEnabled             bool
	MetaReasoningDefaultProfile      string
	MetaReasoningAcceptThreshold     float64
	MetaReasoningStrictThreshold     float64
	RateLimitRPM                     int
	AuditRetentionDays               int
	ReasoningHiddenByDefault         bool
}

func Load() Config {
	return Config{
		ServerAddr:               env("GLM_SERVER_ADDR", ":8081"),
		ServerReadTimeout:        time.Duration(envInt("GLM_SERVER_READ_TIMEOUT_SECONDS", 20)) * time.Second,
		ServerWriteTimeout:       time.Duration(envInt("GLM_SERVER_WRITE_TIMEOUT_SECONDS", 180)) * time.Second,
		ServerIdleTimeout:        time.Duration(envInt("GLM_SERVER_IDLE_TIMEOUT_SECONDS", 120)) * time.Second,
		DefaultModel:             env("GLM_DEFAULT_MODEL", "mistral:7b"),
		CognitionDefaultModel:    env("GLM_COGNITION_DEFAULT_MODEL", "llama3.2:1b"),
		DefaultMaxTokens:         envInt("GLM_DEFAULT_MAX_TOKENS", 128),
		UpstreamBaseURL:          env("GLM_UPSTREAM_BASE_URL", "http://85.31.233.157:8080"),
		UpstreamAPIKey:           env("GLM_UPSTREAM_API_KEY", ""),
		UpstreamTimeout:          time.Duration(envInt("GLM_UPSTREAM_TIMEOUT_SECONDS", 40)) * time.Second,
		UpstreamRetryMax:         envInt("GLM_UPSTREAM_RETRY_MAX", 2),
		PocketBaseURL:            env("GLM_POCKETBASE_URL", env("POCKETBASE_URL", "https://pocketbase.thynaptic.com")),
		PocketBaseAuthColl:       env("GLM_POCKETBASE_AUTH_COLLECTION", "service_accounts"),
		PocketBaseIdentity:       env("GLM_POCKETBASE_IDENTITY", ""),
		PocketBasePassword:       env("GLM_POCKETBASE_PASSWORD", ""),
		PocketBaseAllowUnauth:    envBool("GLM_POCKETBASE_ALLOW_UNAUTH", false),
		OrchestratorEnabled:      envBool("GLM_ORCHESTRATOR_ENABLED", true),
		OrchestratorDefaultModel: env("GLM_ORCHESTRATOR_DEFAULT_MODEL", "qwen3-8b-instruct-Q4_K_M"),
		OrchestratorAliases: splitCSV(
			env("GLM_ORCHESTRATOR_DEFAULT_ALIASES", "qwen3-8b-instruct-Q4_K_M,qwen3:8b,qwen3-8b,qwen3_8b_instruct_q4_k_m"),
		),
		OrchestratorFallback:             env("GLM_ORCHESTRATOR_DEFAULT_FALLBACK", "qwen3:4b"),
		JITInventoryEnabled:              envBool("GLM_JIT_INVENTORY_ENABLED", true),
		JITReconcileSeconds:              envInt("GLM_JIT_RECONCILE_SECONDS", 30),
		JITReconcileJitterSeconds:        envInt("GLM_JIT_RECONCILE_JITTER_SECONDS", 5),
		JITMaxModels:                     envInt("GLM_JIT_MAX_MODELS", 20),
		JITStorageHighWatermark:          envFloat("GLM_JIT_STORAGE_HIGH_WATERMARK", 0.85),
		JITStorageTargetWatermark:        envFloat("GLM_JIT_STORAGE_TARGET_WATERMARK", 0.75),
		JITPullTimeoutSeconds:            envInt("GLM_JIT_PULL_TIMEOUT_SECONDS", 900),
		JITPruneEnabled:                  envBool("GLM_JIT_PRUNE_ENABLED", true),
		JITIdealCoding:                   env("GLM_JIT_IDEAL_CODING", "deepseek-coder:6.7b"),
		JITIdealExtraction:               env("GLM_JIT_IDEAL_EXTRACTION", "phi3:medium"),
		JITIdealLightQA:                  env("GLM_JIT_IDEAL_LIGHT_QA", "llama3.2:1b"),
		JITIdealGeneral:                  env("GLM_JIT_IDEAL_GENERAL", "qwen3-8b-instruct-Q4_K_M"),
		OllamaControlURL:                 env("GLM_OLLAMA_CONTROL_URL", "http://127.0.0.1:11434"),
		OllamaControlAPIKey:              env("GLM_OLLAMA_CONTROL_API_KEY", ""),
		StateHistoryWindow:               envInt("GLM_STATE_HISTORY_WINDOW", 20),
		EmotionalModulationEnabled:       envBool("GLM_EMOTIONAL_MODULATION_ENABLED", true),
		ReasoningPipelineEnabled:         envBool("GLM_REASONING_PIPELINE_ENABLED", true),
		ReasoningPipelineDefaultBranches: envInt("GLM_REASONING_PIPELINE_DEFAULT_BRANCHES", 3),
		ReasoningPipelineMaxBranches:     envInt("GLM_REASONING_PIPELINE_MAX_BRANCHES", 5),
		MCTSEnabled:                      envBool("GLM_MCTS_ENABLED", true),
		MCTSDefaultRollouts:              envInt("GLM_MCTS_DEFAULT_ROLLOUTS", 12),
		MCTSMaxRollouts:                  envInt("GLM_MCTS_MAX_ROLLOUTS", 24),
		MCTSDefaultDepth:                 envInt("GLM_MCTS_DEFAULT_DEPTH", 3),
		MCTSMaxDepth:                     envInt("GLM_MCTS_MAX_DEPTH", 5),
		MCTSDefaultExploration:           envFloat("GLM_MCTS_DEFAULT_EXPLORATION", 1.20),
		MCTSStageTimeout:                 time.Duration(envInt("GLM_MCTS_STAGE_TIMEOUT_SECONDS", 35)) * time.Second,
		MCTSFailOpen:                     envBool("GLM_MCTS_FAILOPEN", true),
		MultiAgentEnabled:                envBool("GLM_MULTI_AGENT_ENABLED", true),
		MultiAgentMaxAgents:              envInt("GLM_MULTI_AGENT_MAX_AGENTS", 4),
		MultiAgentMaxRounds:              envInt("GLM_MULTI_AGENT_MAX_ROUNDS", 2),
		MultiAgentStageTimeout:           time.Duration(envInt("GLM_MULTI_AGENT_STAGE_TIMEOUT_SECONDS", 45)) * time.Second,
		MultiAgentBudgetTokens:           envInt("GLM_MULTI_AGENT_BUDGET_TOKENS", 700),
		MultiAgentFailOpen:               envBool("GLM_MULTI_AGENT_FAILOPEN", true),
		IntentPreprocessorEnabled:        envBool("GLM_INTENT_PREPROCESSOR_ENABLED", true),
		IntentAmbiguityThreshold:         envFloat("GLM_INTENT_AMBIGUITY_THRESHOLD", 0.62),
		DocumentOrchestrationEnabled:     envBool("GLM_DOCUMENT_ORCHESTRATION_ENABLED", true),
		DocumentChunkSize:                envInt("GLM_DOCUMENT_CHUNK_SIZE", 1200),
		DocumentMaxDocuments:             envInt("GLM_DOCUMENT_MAX_DOCUMENTS", 8),
		DocumentMaxChunksPerDoc:          envInt("GLM_DOCUMENT_MAX_CHUNKS_PER_DOC", 8),
		DocumentMaxLinks:                 envInt("GLM_DOCUMENT_MAX_LINKS", 12),
		MemoryDynamicsEnabled:            envBool("GLM_MEMORY_DYNAMICS_ENABLED", true),
		MemoryHalfLifeHours:              envFloat("GLM_MEMORY_HALF_LIFE_HOURS", 168),
		MemoryReplayThreshold:            envFloat("GLM_MEMORY_REPLAY_THRESHOLD", 0.68),
		MemoryFreshnessWindowHours:       envFloat("GLM_MEMORY_FRESHNESS_WINDOW_HOURS", 72),
		MemoryContextNodeLimit:           envInt("GLM_MEMORY_CONTEXT_NODE_LIMIT", 5),
		MemoryUpdateConceptsPerTurn:      envInt("GLM_MEMORY_UPDATE_CONCEPTS_PER_TURN", 6),
		MemoryOpTimeout:                  time.Duration(envInt("GLM_MEMORY_OP_TIMEOUT_SECONDS", 2)) * time.Second,
		ReasoningStageTimeout:            time.Duration(envInt("GLM_REASONING_STAGE_TIMEOUT_SECONDS", 60)) * time.Second,
		DocumentStageTimeout:             time.Duration(envInt("GLM_DOCUMENT_STAGE_TIMEOUT_SECONDS", 25)) * time.Second,
		StyleContractEnabled:             envBool("GLM_STYLE_CONTRACT_ENABLED", true),
		StyleContractVersion:             env("GLM_STYLE_CONTRACT_VERSION", "v1"),
		SymbolicOverlayEnabled:           envBool("GLM_SYMBOLIC_OVERLAY_ENABLED", true),
		SymbolicOverlayMaxSymbols:        envInt("GLM_SYMBOLIC_OVERLAY_MAX_SYMBOLS", 48),
		SymbolicOverlayMaxDocChars:       envInt("GLM_SYMBOLIC_OVERLAY_MAX_DOC_CHARS", 12000),
		SymbolicOverlayStrictCheck:       envBool("GLM_SYMBOLIC_OVERLAY_STRICT_CHECK", true),
		ToolCallingEnabled:               envBool("GLM_TOOL_CALLING_ENABLED", false),
		ToolServerBaseURL:                env("GLM_TOOL_SERVER_BASE_URL", "https://chat.thynaptic.com"),
		ToolServerAPIKey:                 env("GLM_TOOL_SERVER_API_KEY", ""),
		ToolServerClientID:               env("GLM_TOOL_SERVER_CLIENT_ID", ""),
		ToolCallingMaxIterations:         envInt("GLM_TOOL_CALLING_MAX_ITERATIONS", 4),
		ToolCallingTimeoutSeconds:        envInt("GLM_TOOL_CALLING_TIMEOUT_SECONDS", 60),
		MetaReasoningEnabled:             envBool("GLM_META_REASONING_ENABLED", true),
		MetaReasoningDefaultProfile:      env("GLM_META_REASONING_DEFAULT_PROFILE", "default"),
		MetaReasoningAcceptThreshold:     envFloat("GLM_META_REASONING_ACCEPT_THRESHOLD", 0.72),
		MetaReasoningStrictThreshold:     envFloat("GLM_META_REASONING_STRICT_THRESHOLD", 0.82),
		RateLimitRPM:                     envInt("GLM_RATE_LIMIT_RPM", 120),
		AuditRetentionDays:               envInt("GLM_AUDIT_RETENTION_DAYS", 90),
		ReasoningHiddenByDefault:         envBool("GLM_REASONING_HIDDEN_DEFAULT", true),
	}
}

func env(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	v := env(key, "")
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func envBool(key string, fallback bool) bool {
	v := env(key, "")
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}

func envFloat(key string, fallback float64) float64 {
	v := env(key, "")
	if v == "" {
		return fallback
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return fallback
	}
	return f
}

func splitCSV(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

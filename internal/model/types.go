package model

import "time"

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	Name       string     `json:"name,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
}

type ChatCompletionRequest struct {
	Model            string                  `json:"model"`
	SessionID        string                  `json:"session_id,omitempty"`
	MemoryAnchorKeys []string                `json:"-"`
	Reasoning        *ReasoningOptions       `json:"reasoning,omitempty"`
	SymbolicOverlay  *SymbolicOverlayOptions `json:"symbolic_overlay,omitempty"`
	ResponseStyle    *ResponseStyle          `json:"response_style,omitempty"`
	Documents        []DocumentInput         `json:"documents,omitempty"`
	DocumentFlow     *DocumentOrchestration  `json:"document_orchestration,omitempty"`
	Messages         []Message               `json:"messages"`
	Tools            []ToolDefinition        `json:"tools,omitempty"`
	ToolChoice       any                     `json:"tool_choice,omitempty"`
	Temperature      *float64                `json:"temperature,omitempty"`
	MaxTokens        *int                    `json:"max_tokens,omitempty"`
	Stream           bool                    `json:"stream,omitempty"`
}

type CognitionRequest struct {
	Task            string                  `json:"task,omitempty"`
	Input           string                  `json:"input,omitempty"`
	Model           string                  `json:"model,omitempty"`
	SessionID       string                  `json:"session_id,omitempty"`
	Messages        []Message               `json:"messages,omitempty"`
	ResponseStyle   *ResponseStyle          `json:"response_style,omitempty"`
	Documents       []DocumentInput         `json:"documents,omitempty"`
	Reasoning       *ReasoningOptions       `json:"reasoning,omitempty"`
	SymbolicOverlay *SymbolicOverlayOptions `json:"symbolic_overlay,omitempty"`
	DocumentFlow    *DocumentOrchestration  `json:"document_orchestration,omitempty"`
	Tools           []ToolDefinition        `json:"tools,omitempty"`
	ToolChoice      any                     `json:"tool_choice,omitempty"`
	Temperature     *float64                `json:"temperature,omitempty"`
	MaxTokens       *int                    `json:"max_tokens,omitempty"`
	Stream          bool                    `json:"stream,omitempty"`
}

type ToolDefinition struct {
	Type     string       `json:"type,omitempty"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters,omitempty"`
}

type ToolCall struct {
	ID       string           `json:"id,omitempty"`
	Type     string           `json:"type,omitempty"`
	Function ToolFunctionCall `json:"function"`
}

type ToolFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments,omitempty"`
}

type SymbolicOverlayType string

const (
	SymbolicOverlayTypeLogicMap      SymbolicOverlayType = "logic_map"
	SymbolicOverlayTypeConstraintSet SymbolicOverlayType = "constraint_set"
	SymbolicOverlayTypeRiskLens      SymbolicOverlayType = "risk_lens"
)

type SymbolicOverlayOptions struct {
	Mode             string   `json:"mode,omitempty"`
	SchemaVersion    string   `json:"schema_version,omitempty"`
	OverlayProfile   string   `json:"overlay_profile,omitempty"`
	MaxOverlayHops   int      `json:"max_overlay_hops,omitempty"`
	Types            []string `json:"types,omitempty"`
	MaxSymbols       int      `json:"max_symbols,omitempty"`
	IncludeState     bool     `json:"include_state,omitempty"`
	IncludeDocuments bool     `json:"include_documents,omitempty"`
}

type ReasoningOptions struct {
	Mode                       string   `json:"mode,omitempty"`
	Branches                   int      `json:"branches,omitempty"`
	SelfEvaluate               bool     `json:"self_evaluate,omitempty"`
	DetectContradictions       bool     `json:"detect_contradictions,omitempty"`
	DecomposeEnabled           bool     `json:"decompose_enabled,omitempty"`
	DecomposeMaxSubtasks       int      `json:"decompose_max_subtasks,omitempty"`
	DecomposeMaxDepth          int      `json:"decompose_max_depth,omitempty"`
	DecomposeBudgetTokens      int      `json:"decompose_budget_tokens,omitempty"`
	MCTSMaxRollouts            int      `json:"mcts_max_rollouts,omitempty"`
	MCTSMaxDepth               int      `json:"mcts_max_depth,omitempty"`
	MCTSExploration            float64  `json:"mcts_exploration,omitempty"`
	MCTSTimeoutMs              int      `json:"mcts_timeout_ms,omitempty"`
	MCTSV2Enabled              bool     `json:"mcts_v2_enabled,omitempty"`
	MCTSEarlyStopWindow        int      `json:"mcts_early_stop_window,omitempty"`
	MCTSEarlyStopDelta         float64  `json:"mcts_early_stop_delta,omitempty"`
	MultiAgentEnabled          bool     `json:"multi_agent_enabled,omitempty"`
	MultiAgentMaxAgents        int      `json:"multi_agent_max_agents,omitempty"`
	MultiAgentMaxRounds        int      `json:"multi_agent_max_rounds,omitempty"`
	MultiAgentTimeoutMs        int      `json:"multi_agent_timeout_ms,omitempty"`
	MultiAgentBudgetTokens     int      `json:"multi_agent_budget_tokens,omitempty"`
	MetaEnabled                bool     `json:"meta_enabled,omitempty"`
	MetaProfile                string   `json:"meta_profile,omitempty"`
	ReflectionLayersEnabled    bool     `json:"reflection_layers_enabled,omitempty"`
	ReflectionLayerCount       int      `json:"reflection_layer_count,omitempty"`
	EvaluatorChainEnabled      bool     `json:"evaluator_chain_enabled,omitempty"`
	EvaluatorChain             []string `json:"evaluator_chain,omitempty"`
	EvaluatorChainMaxDepth     int      `json:"evaluator_chain_max_depth,omitempty"`
	MetaReflectionEnabled      bool     `json:"meta_reflection_enabled,omitempty"`
	MetaReflectionMaxPasses    int      `json:"meta_reflection_max_passes,omitempty"`
	SelfAlignmentEnabled       bool     `json:"self_alignment_enabled,omitempty"`
	SelfAlignmentMaxPasses     int      `json:"self_alignment_max_passes,omitempty"`
	ContextReindexEnabled      bool     `json:"context_reindex_enabled,omitempty"`
	ContextReindexScope        string   `json:"context_reindex_scope,omitempty"`
	SkillCompilerEnabled       bool     `json:"skill_compiler_enabled,omitempty"`
	SkillCompilerProfile       string   `json:"skill_compiler_profile,omitempty"`
	SkillCompilerBudgetTokens  int      `json:"skill_compiler_budget_tokens,omitempty"`
	GeometryMode               string   `json:"geometry_mode,omitempty"`
	ShapeTransformEnabled      bool     `json:"shape_transform_enabled,omitempty"`
	WorldviewFusionEnabled     bool     `json:"worldview_fusion_enabled,omitempty"`
	WorldviewFusionStages      int      `json:"worldview_fusion_stages,omitempty"`
	WorldviewProfiles          []string `json:"worldview_profiles,omitempty"`
	ConstraintBreakingEnabled  bool     `json:"constraint_breaking_enabled,omitempty"`
	ConstraintBreakingLevel    string   `json:"constraint_breaking_level,omitempty"`
	AdversarialSelfPlayEnabled bool     `json:"adversarial_self_play_enabled,omitempty"`
	AdversarialRounds          int      `json:"adversarial_rounds,omitempty"`
	AdversarialRoles           []string `json:"adversarial_roles,omitempty"`
}

type DocumentInput struct {
	ID      string `json:"id,omitempty"`
	Title   string `json:"title,omitempty"`
	Section string `json:"section,omitempty"`
	Text    string `json:"text"`
}

type DocumentOrchestration struct {
	Mode         string `json:"mode,omitempty"`
	ChunkSize    int    `json:"chunk_size,omitempty"`
	MaxDocuments int    `json:"max_documents,omitempty"`
}

type ResponseStyle struct {
	BreathingWeight      float64  `json:"breathing_weight,omitempty"`
	ToneShift            string   `json:"tone_shift,omitempty"`
	StyleAdjustment      string   `json:"style_adjustment,omitempty"`
	Register             string   `json:"register,omitempty"`
	VerbosityTarget      string   `json:"verbosity_target,omitempty"`
	JustificationDensity string   `json:"justification_density,omitempty"`
	AudienceMode         string   `json:"audience_mode,omitempty"`
	Pacing               string   `json:"pacing,omitempty"`
	MicroSwitches        []string `json:"micro_switches,omitempty"`
	MoodShift            float64  `json:"mood_shift,omitempty"`
	TopicDrift           float64  `json:"topic_drift,omitempty"`
	SubtextDetection     string   `json:"subtext_detection,omitempty"`
	RollingSentiment     float64  `json:"rolling_sentiment,omitempty"`
	ConversationDrift    float64  `json:"conversation_drift,omitempty"`
	RiskFlags            []string `json:"risk_flags,omitempty"`
}

type ChatCompletionResponse struct {
	ID      string `json:"id,omitempty"`
	Object  string `json:"object,omitempty"`
	Created int64  `json:"created,omitempty"`
	Model   string `json:"model,omitempty"`
	Choices []struct {
		Index   int `json:"index"`
		Message struct {
			Role      string     `json:"role"`
			Content   string     `json:"content"`
			Name      string     `json:"name,omitempty"`
			ToolCalls []ToolCall `json:"tool_calls,omitempty"`
		} `json:"message"`
		FinishReason string `json:"finish_reason,omitempty"`
	} `json:"choices"`
	Usage any `json:"usage,omitempty"`
}

type ModelListResponse struct {
	Object string      `json:"object"`
	Data   []ModelInfo `json:"data"`
}

type ModelInfo struct {
	ID      string `json:"id"`
	Object  string `json:"object,omitempty"`
	Created int64  `json:"created,omitempty"`
	OwnedBy string `json:"owned_by,omitempty"`
}

type Tenant struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

type APIKeyRecord struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenant_id"`
	Prefix    string    `json:"prefix"`
	Hash      string    `json:"hash"`
	Scopes    []string  `json:"scopes"`
	Status    string    `json:"status"`
	ExpiresAt *FlexTime `json:"expires_at,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type Role struct {
	ID          string    `json:"id"`
	TenantID    string    `json:"tenant_id"`
	Name        string    `json:"name"`
	Permissions []string  `json:"permissions"`
	CreatedAt   time.Time `json:"created_at"`
}

type ModelPolicy struct {
	ID               string    `json:"id"`
	TenantID         string    `json:"tenant_id"`
	AllowedModels    []string  `json:"allowed_models"`
	PrimaryModel     string    `json:"primary_model"`
	FallbackModel    string    `json:"fallback_model"`
	ReasoningVisible bool      `json:"reasoning_visible"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type CognitivePolicy struct {
	ID                            string    `json:"id"`
	TenantID                      string    `json:"tenant_id"`
	Status                        string    `json:"status"`
	Version                       string    `json:"version"`
	AllowedReasoningModes         []string  `json:"allowed_reasoning_modes,omitempty"`
	MaxReasoningPasses            int       `json:"max_reasoning_passes,omitempty"`
	MaxReflectionPasses           int       `json:"max_reflection_passes,omitempty"`
	MaxSelfAlignmentPasses        int       `json:"max_self_alignment_passes,omitempty"`
	AllowConstraintBreaking       bool      `json:"allow_constraint_breaking,omitempty"`
	MaxConstraintBreakingSeverity string    `json:"max_constraint_breaking_severity,omitempty"`
	AllowAdversarialSelfPlay      bool      `json:"allow_adversarial_self_play,omitempty"`
	AllowWorldviewFusion          bool      `json:"allow_worldview_fusion,omitempty"`
	AllowShapeTransform           bool      `json:"allow_shape_transform,omitempty"`
	AllowContextReindex           bool      `json:"allow_context_reindex,omitempty"`
	AllowSkillCompiler            bool      `json:"allow_skill_compiler,omitempty"`
	ToolAllowlist                 []string  `json:"tool_allowlist,omitempty"`
	ToolDenylist                  []string  `json:"tool_denylist,omitempty"`
	RiskThresholdReject           float64   `json:"risk_threshold_reject,omitempty"`
	RiskThresholdWarn             float64   `json:"risk_threshold_warn,omitempty"`
	CreatedAt                     time.Time `json:"created_at"`
	UpdatedAt                     time.Time `json:"updated_at"`
}

type Quota struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenant_id"`
	RPMLimit  int       `json:"rpm_limit"`
	TPMLimit  int       `json:"tpm_limit"`
	Burst     int       `json:"burst"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type AuditEvent struct {
	ID        string   `json:"id"`
	TenantID  string   `json:"tenant_id"`
	ActorType string   `json:"actor_type"`
	ActorID   string   `json:"actor_id"`
	Endpoint  string   `json:"endpoint"`
	Model     string   `json:"model"`
	Outcome   string   `json:"outcome"`
	LatencyMS int64    `json:"latency_ms"`
	TraceID   string   `json:"trace_id"`
	Timestamp FlexTime `json:"timestamp"`
}

type IdempotencyRecord struct {
	ID             string    `json:"id"`
	TenantID       string    `json:"tenant_id"`
	IdempotencyKey string    `json:"idempotency_key"`
	RequestHash    string    `json:"request_hash"`
	ResponseHash   string    `json:"response_hash"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"created_at"`
	ExpiresAt      FlexTime  `json:"expires_at"`
}

type MemoryNode struct {
	ID          string         `json:"id"`
	TenantID    string         `json:"tenant_id"`
	SessionID   string         `json:"session_id"`
	Key         string         `json:"key"`
	Label       string         `json:"label"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	Weight      float64        `json:"weight"`
	Importance  float64        `json:"importance"`
	AccessCount int            `json:"access_count"`
	LastSeenAt  FlexTime       `json:"last_seen_at"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

package reasoning

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mike/cognitive-llm/internal/model"
	"github.com/mike/cognitive-llm/internal/orchestrator"
	"github.com/mike/cognitive-llm/internal/state"
)

type Upstream interface {
	ListModels(ctx context.Context) (model.ModelListResponse, error)
	ChatCompletions(ctx context.Context, req model.ChatCompletionRequest) (model.ChatCompletionResponse, error)
}

type Config struct {
	Enabled                            bool
	DefaultBranches                    int
	MaxBranches                        int
	PruningEnabled                     bool
	PruningMinScore                    float64
	PruningToTTopK                     int
	PruningToTSynthTopK                int
	PruningMCTSPoolTopK                int
	PruningMCTSSynthTopK               int
	PruningMARoundTopK                 int
	PruningMASynthTopK                 int
	SelfEvalCurveEnabled               bool
	SelfEvalCurveLowMax                float64
	SelfEvalCurveMidMax                float64
	SelfEvalCurveLowWeight             float64
	SelfEvalCurveMidWeight             float64
	SelfEvalCurveHighWeight            float64
	SelfEvalCurveBias                  float64
	MCTSEnabled                        bool
	MCTSDefaultRollouts                int
	MCTSMaxRollouts                    int
	MCTSDefaultDepth                   int
	MCTSMaxDepth                       int
	MCTSDefaultExploration             float64
	MCTSV2Enabled                      bool
	MCTSEarlyStopWindow                int
	MCTSEarlyStopDelta                 float64
	MultiAgentEnabled                  bool
	MultiAgentMaxAgents                int
	MultiAgentMaxRounds                int
	MultiAgentBudgetTokens             int
	MemoryAnchoredReasoningEnabled     bool
	MemoryAnchoredReasoningMaxAnchors  int
	MemoryAnchoredReasoningMinCoverage float64
	MemoryAnchoredReasoningScoreBonus  float64
}

type Executor struct {
	cfg    Config
	router *orchestrator.Router
}

type Node struct {
	ID        string         `json:"id"`
	Type      string         `json:"type"`
	Model     string         `json:"model"`
	Score     float64        `json:"score,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	StartedAt time.Time      `json:"started_at"`
	EndedAt   time.Time      `json:"ended_at"`
	Error     string         `json:"error,omitempty"`
}

type BranchResult struct {
	Index            int      `json:"index"`
	Model            string   `json:"model"`
	Output           string   `json:"output"`
	EvaluationScore  float64  `json:"evaluation_score"`
	EvaluationReason string   `json:"evaluation_reason,omitempty"`
	Warnings         []string `json:"warnings,omitempty"`
}

type ContradictionReport struct {
	Detected bool     `json:"detected"`
	Pairs    []string `json:"pairs,omitempty"`
	Summary  string   `json:"summary,omitempty"`
}

type Trace struct {
	Mode           string              `json:"mode"`
	TaskClass      string              `json:"task_class"`
	ChosenModel    string              `json:"chosen_model"`
	Branches       []BranchResult      `json:"branches"`
	Contradictions ContradictionReport `json:"contradictions"`
	Pruning        *PruningTrace       `json:"pruning,omitempty"`
	MemoryAnchor   *MemoryAnchorTrace  `json:"memory_anchor,omitempty"`
	MCTS           *MCTSResult         `json:"mcts,omitempty"`
	MultiAgent     *MultiAgentResult   `json:"multi_agent,omitempty"`
	Nodes          []Node              `json:"nodes"`
}

type MemoryAnchorTrace struct {
	Enabled             bool    `json:"enabled"`
	Applied             bool    `json:"applied"`
	Mode                string  `json:"mode"`
	AnchorsIn           int     `json:"anchors_in"`
	AnchorsUsed         int     `json:"anchors_used"`
	CandidatesEvaluated int     `json:"candidates_evaluated"`
	CoverageAvg         float64 `json:"coverage_avg"`
	BonusAvg            float64 `json:"bonus_avg"`
}

type memoryAnchorAccumulator struct {
	anchorsIn   int
	sumCoverage float64
	sumBonus    float64
	candidates  int
	anchorsUsed map[string]struct{}
}

func newMemoryAnchorAccumulator(anchors []string) memoryAnchorAccumulator {
	return memoryAnchorAccumulator{
		anchorsIn:   len(anchors),
		anchorsUsed: map[string]struct{}{},
	}
}

func (m *memoryAnchorAccumulator) Add(anchors []string, coverage, bonus float64) {
	m.candidates++
	m.sumCoverage += coverage
	m.sumBonus += bonus
	normalized := normalizeAnchorSet(anchors)
	for _, a := range normalized {
		m.anchorsUsed[a] = struct{}{}
	}
}

func (m memoryAnchorAccumulator) Trace(enabled bool, mode string) *MemoryAnchorTrace {
	trace := &MemoryAnchorTrace{
		Enabled:             enabled,
		Mode:                mode,
		AnchorsIn:           m.anchorsIn,
		AnchorsUsed:         len(m.anchorsUsed),
		CandidatesEvaluated: m.candidates,
	}
	if m.candidates > 0 {
		trace.CoverageAvg = math.Round((m.sumCoverage/float64(m.candidates))*1000) / 1000
		trace.BonusAvg = math.Round((m.sumBonus/float64(m.candidates))*1000) / 1000
	}
	trace.Applied = enabled && m.anchorsIn > 0 && m.candidates > 0
	return trace
}

type PruningTrace struct {
	Mode            string  `json:"mode"`
	Enabled         bool    `json:"enabled"`
	MinScore        float64 `json:"min_score"`
	TopK            int     `json:"top_k"`
	CandidatesIn    int     `json:"candidates_in"`
	CandidatesOut   int     `json:"candidates_out"`
	DroppedLowScore int     `json:"dropped_low_score"`
	DroppedTopK     int     `json:"dropped_top_k"`
}

type pruneStats struct {
	CandidatesIn    int
	CandidatesOut   int
	DroppedLowScore int
	DroppedTopK     int
}

type MCTSResult struct {
	Rollouts         int     `json:"rollouts"`
	RolloutsExecuted int     `json:"rollouts_executed,omitempty"`
	Depth            int     `json:"depth"`
	BestScore        float64 `json:"best_score"`
	EarlyStop        bool    `json:"early_stop,omitempty"`
	EarlyStopReason  string  `json:"early_stop_reason,omitempty"`
	Fallback         string  `json:"fallback,omitempty"`
}

type MultiAgentResult struct {
	Agents    int     `json:"agents"`
	Rounds    int     `json:"rounds"`
	Winner    string  `json:"winner"`
	Consensus string  `json:"consensus"`
	Score     float64 `json:"score"`
	Fallback  string  `json:"fallback,omitempty"`
}

func NewExecutor(cfg Config, router *orchestrator.Router) *Executor {
	if cfg.DefaultBranches <= 0 {
		cfg.DefaultBranches = 3
	}
	if cfg.MaxBranches <= 0 {
		cfg.MaxBranches = 5
	}
	if !cfg.PruningEnabled &&
		cfg.PruningMinScore == 0 &&
		cfg.PruningToTTopK == 0 &&
		cfg.PruningToTSynthTopK == 0 &&
		cfg.PruningMCTSPoolTopK == 0 &&
		cfg.PruningMCTSSynthTopK == 0 &&
		cfg.PruningMARoundTopK == 0 &&
		cfg.PruningMASynthTopK == 0 {
		cfg.PruningEnabled = true
	}
	if cfg.PruningMinScore < 0 {
		cfg.PruningMinScore = 0
	}
	if cfg.PruningMinScore > 1 {
		cfg.PruningMinScore = 1
	}
	if cfg.PruningToTTopK <= 0 {
		cfg.PruningToTTopK = 3
	}
	if cfg.PruningToTSynthTopK <= 0 {
		cfg.PruningToTSynthTopK = 2
	}
	if cfg.PruningToTSynthTopK > cfg.PruningToTTopK {
		cfg.PruningToTSynthTopK = cfg.PruningToTTopK
	}
	if cfg.PruningMCTSPoolTopK <= 0 {
		cfg.PruningMCTSPoolTopK = 6
	}
	if cfg.PruningMCTSSynthTopK <= 0 {
		cfg.PruningMCTSSynthTopK = 3
	}
	if cfg.PruningMCTSSynthTopK > cfg.PruningMCTSPoolTopK {
		cfg.PruningMCTSSynthTopK = cfg.PruningMCTSPoolTopK
	}
	if cfg.PruningMARoundTopK <= 0 {
		cfg.PruningMARoundTopK = 4
	}
	if cfg.PruningMASynthTopK <= 0 {
		cfg.PruningMASynthTopK = 3
	}
	if cfg.PruningMASynthTopK > cfg.PruningMARoundTopK {
		cfg.PruningMASynthTopK = cfg.PruningMARoundTopK
	}
	if cfg.SelfEvalCurveLowMax <= 0 || cfg.SelfEvalCurveLowMax >= 1 {
		cfg.SelfEvalCurveLowMax = 0.60
	}
	if cfg.SelfEvalCurveMidMax <= 0 || cfg.SelfEvalCurveMidMax >= 1 {
		cfg.SelfEvalCurveMidMax = 0.82
	}
	if cfg.SelfEvalCurveMidMax <= cfg.SelfEvalCurveLowMax {
		cfg.SelfEvalCurveMidMax = 0.82
		if cfg.SelfEvalCurveMidMax <= cfg.SelfEvalCurveLowMax {
			cfg.SelfEvalCurveLowMax = 0.60
			cfg.SelfEvalCurveMidMax = 0.82
		}
	}
	if cfg.SelfEvalCurveLowWeight <= 0 {
		cfg.SelfEvalCurveLowWeight = 0.90
	}
	if cfg.SelfEvalCurveMidWeight <= 0 {
		cfg.SelfEvalCurveMidWeight = 1.00
	}
	if cfg.SelfEvalCurveHighWeight <= 0 {
		cfg.SelfEvalCurveHighWeight = 1.08
	}
	if cfg.MCTSDefaultRollouts <= 0 {
		cfg.MCTSDefaultRollouts = 12
	}
	if cfg.MCTSMaxRollouts <= 0 {
		cfg.MCTSMaxRollouts = 24
	}
	if cfg.MCTSDefaultDepth <= 0 {
		cfg.MCTSDefaultDepth = 3
	}
	if cfg.MCTSMaxDepth <= 0 {
		cfg.MCTSMaxDepth = 5
	}
	if cfg.MCTSDefaultExploration <= 0 {
		cfg.MCTSDefaultExploration = 1.2
	}
	if cfg.MCTSEarlyStopWindow < 2 {
		cfg.MCTSEarlyStopWindow = 4
	}
	if cfg.MCTSEarlyStopDelta < 0 {
		cfg.MCTSEarlyStopDelta = 0
	}
	if cfg.MCTSEarlyStopDelta > 0.2 {
		cfg.MCTSEarlyStopDelta = 0.2
	}
	if cfg.MultiAgentMaxAgents <= 0 {
		cfg.MultiAgentMaxAgents = 4
	}
	if cfg.MultiAgentMaxRounds <= 0 {
		cfg.MultiAgentMaxRounds = 2
	}
	if cfg.MultiAgentBudgetTokens <= 0 {
		cfg.MultiAgentBudgetTokens = 700
	}
	if cfg.MemoryAnchoredReasoningMaxAnchors <= 0 {
		cfg.MemoryAnchoredReasoningMaxAnchors = 3
	}
	if cfg.MemoryAnchoredReasoningMinCoverage < 0 {
		cfg.MemoryAnchoredReasoningMinCoverage = 0
	}
	if cfg.MemoryAnchoredReasoningMinCoverage > 1 {
		cfg.MemoryAnchoredReasoningMinCoverage = 1
	}
	if cfg.MemoryAnchoredReasoningScoreBonus < 0 {
		cfg.MemoryAnchoredReasoningScoreBonus = 0
	}
	if cfg.MemoryAnchoredReasoningScoreBonus > 0.20 {
		cfg.MemoryAnchoredReasoningScoreBonus = 0.20
	}
	return &Executor{cfg: cfg, router: router}
}

func (e *Executor) ShouldExecute(req model.ChatCompletionRequest, st state.CognitiveState) bool {
	if !e.cfg.Enabled {
		return false
	}
	if req.Reasoning == nil {
		return false
	}
	mode := strings.ToLower(strings.TrimSpace(req.Reasoning.Mode))
	switch mode {
	case "tot", "pipeline":
		return true
	case "mcts":
		return e.cfg.MCTSEnabled
	case "multi_agent":
		return e.cfg.MultiAgentEnabled
	case "auto":
		return st.TaskMode == "coding" || st.TaskMode == "general"
	default:
		return false
	}
}

func (e *Executor) Execute(
	ctx context.Context,
	up Upstream,
	req model.ChatCompletionRequest,
	pol model.ModelPolicy,
	st state.CognitiveState,
) (model.ChatCompletionResponse, Trace, error) {
	if req.Reasoning != nil && strings.EqualFold(strings.TrimSpace(req.Reasoning.Mode), "multi_agent") {
		return e.executeMultiAgent(ctx, up, req, pol, st)
	}
	if req.Reasoning != nil && strings.EqualFold(strings.TrimSpace(req.Reasoning.Mode), "mcts") {
		return e.executeMCTS(ctx, up, req, pol, st)
	}
	return e.executeToT(ctx, up, req, pol, st)
}

func (e *Executor) ExecuteToT(
	ctx context.Context,
	up Upstream,
	req model.ChatCompletionRequest,
	pol model.ModelPolicy,
	st state.CognitiveState,
) (model.ChatCompletionResponse, Trace, error) {
	return e.executeToT(ctx, up, req, pol, st)
}

func (e *Executor) ExecuteMCTS(
	ctx context.Context,
	up Upstream,
	req model.ChatCompletionRequest,
	pol model.ModelPolicy,
	st state.CognitiveState,
) (model.ChatCompletionResponse, Trace, error) {
	return e.executeMCTS(ctx, up, req, pol, st)
}

func (e *Executor) ExecuteMultiAgent(
	ctx context.Context,
	up Upstream,
	req model.ChatCompletionRequest,
	pol model.ModelPolicy,
	st state.CognitiveState,
) (model.ChatCompletionResponse, Trace, error) {
	return e.executeMultiAgent(ctx, up, req, pol, st)
}

func (e *Executor) executeToT(
	ctx context.Context,
	up Upstream,
	req model.ChatCompletionRequest,
	pol model.ModelPolicy,
	st state.CognitiveState,
) (model.ChatCompletionResponse, Trace, error) {
	trace := Trace{Mode: safeMode(req), TaskClass: st.TaskMode}
	modelsResp, err := up.ListModels(ctx)
	if err != nil {
		return model.ChatCompletionResponse{}, trace, fmt.Errorf("pipeline model inventory failed: %w", err)
	}
	available := make([]string, 0, len(modelsResp.Data))
	for _, m := range modelsResp.Data {
		available = append(available, m.ID)
	}

	decision, err := e.router.ChooseWithState(req, available, pol, &st)
	if err != nil {
		return model.ChatCompletionResponse{}, trace, err
	}
	baseModel := decision.ChosenModel
	trace.ChosenModel = baseModel
	trace.TaskClass = decision.TaskClass

	branches := e.resolveBranches(req)
	branchResults := make([]BranchResult, 0, branches)
	allSuccessful := make([]BranchResult, 0, branches)
	anchors := e.resolveMemoryAnchors(req)
	memAcc := newMemoryAnchorAccumulator(anchors)
	var pruneAggregate pruneStats
	allNodes := make([]Node, 0, branches+4)

	for i := 0; i < branches; i++ {
		node := Node{ID: fmt.Sprintf("branch-%d", i+1), Type: "branch", Model: baseModel, StartedAt: time.Now().UTC()}
		branchReq := req
		branchReq.Model = baseModel
		branchReq.Messages = buildBranchMessages(req.Messages, i, branches, anchors)
		resp, callErr := up.ChatCompletions(ctx, branchReq)
		node.EndedAt = time.Now().UTC()
		if callErr != nil {
			node.Error = callErr.Error()
			allNodes = append(allNodes, node)
			continue
		}
		output := extractAssistantText(resp)
		score, reason, coverage, bonus := e.evaluateOutputWithMemory(req, output, st, false)
		memAcc.Add(anchors, coverage, bonus)
		node.Score = score
		node.Metadata = map[string]any{"evaluation_reason": reason}
		allNodes = append(allNodes, node)
		branchResults = append(branchResults, BranchResult{
			Index:            i + 1,
			Model:            baseModel,
			Output:           output,
			EvaluationScore:  score,
			EvaluationReason: reason,
			Warnings:         branchWarnings(output),
		})
		allSuccessful = append(allSuccessful, branchResults[len(branchResults)-1])
		if e.cfg.PruningEnabled {
			pruned, stats := e.pruneToTAccumulated(branchResults)
			branchResults = pruned
			pruneAggregate = pruneAggregate.merge(stats)
		}
	}
	if len(allSuccessful) == 0 {
		return model.ChatCompletionResponse{}, trace, fmt.Errorf("pipeline failed: no successful branches")
	}

	if len(branchResults) == 0 {
		branchResults = append([]BranchResult{}, allSuccessful...)
		sort.Slice(branchResults, func(i, j int) bool {
			if branchResults[i].EvaluationScore == branchResults[j].EvaluationScore {
				return branchResults[i].Index < branchResults[j].Index
			}
			return branchResults[i].EvaluationScore > branchResults[j].EvaluationScore
		})
	}

	synthBranches := append([]BranchResult{}, branchResults...)
	if e.cfg.PruningEnabled {
		pruned, stats := e.pruneBranches("tot", synthBranches, e.cfg.PruningMinScore, e.cfg.PruningToTSynthTopK)
		pruneAggregate = pruneAggregate.merge(stats)
		if len(pruned) > 0 {
			synthBranches = pruned
		} else {
			fallback := append([]BranchResult{}, allSuccessful...)
			sort.Slice(fallback, func(i, j int) bool {
				if fallback[i].EvaluationScore == fallback[j].EvaluationScore {
					return fallback[i].Index < fallback[j].Index
				}
				return fallback[i].EvaluationScore > fallback[j].EvaluationScore
			})
			if len(fallback) > e.cfg.PruningToTSynthTopK {
				fallback = fallback[:e.cfg.PruningToTSynthTopK]
			}
			synthBranches = fallback
		}
	}

	contradictions := detectContradictions(synthBranches)
	trace.Contradictions = contradictions
	trace.Branches = synthBranches
	trace.Pruning = &PruningTrace{
		Mode:            "tot",
		Enabled:         e.cfg.PruningEnabled,
		MinScore:        e.cfg.PruningMinScore,
		TopK:            e.cfg.PruningToTSynthTopK,
		CandidatesIn:    pruneAggregate.CandidatesIn,
		CandidatesOut:   len(synthBranches),
		DroppedLowScore: pruneAggregate.DroppedLowScore,
		DroppedTopK:     pruneAggregate.DroppedTopK,
	}
	trace.MemoryAnchor = memAcc.Trace(e.cfg.MemoryAnchoredReasoningEnabled, "tot")

	synthNode := Node{ID: "synthesis-1", Type: "synthesis", Model: baseModel, StartedAt: time.Now().UTC()}
	synthReq := req
	synthReq.Model = baseModel
	synthReq.Messages = buildSynthesisMessages(req.Messages, synthBranches, contradictions, anchors)
	finalResp, synthErr := up.ChatCompletions(ctx, synthReq)
	synthNode.EndedAt = time.Now().UTC()
	if synthErr != nil {
		synthNode.Error = synthErr.Error()
		allNodes = append(allNodes, synthNode)
		trace.Nodes = allNodes
		return model.ChatCompletionResponse{}, trace, fmt.Errorf("pipeline synthesis failed: %w", synthErr)
	}
	allNodes = append(allNodes, synthNode)
	trace.Nodes = allNodes
	return finalResp, trace, nil
}

func (e *Executor) resolveBranches(req model.ChatCompletionRequest) int {
	branches := e.cfg.DefaultBranches
	if req.Reasoning != nil && req.Reasoning.Branches > 0 {
		branches = req.Reasoning.Branches
	}
	if branches < 2 {
		branches = 2
	}
	if branches > e.cfg.MaxBranches {
		branches = e.cfg.MaxBranches
	}
	return branches
}

func (p pruneStats) merge(other pruneStats) pruneStats {
	return pruneStats{
		CandidatesIn:    p.CandidatesIn + other.CandidatesIn,
		CandidatesOut:   other.CandidatesOut,
		DroppedLowScore: p.DroppedLowScore + other.DroppedLowScore,
		DroppedTopK:     p.DroppedTopK + other.DroppedTopK,
	}
}

func (e *Executor) pruneToTAccumulated(in []BranchResult) ([]BranchResult, pruneStats) {
	return e.pruneBranches("tot", in, e.cfg.PruningMinScore, e.cfg.PruningToTTopK)
}

func (e *Executor) pruneMCTSCandidates(in []mctsCandidate, topK int) ([]mctsCandidate, pruneStats) {
	if !e.cfg.PruningEnabled {
		out := append([]mctsCandidate{}, in...)
		return out, pruneStats{CandidatesIn: len(in), CandidatesOut: len(in)}
	}
	branches := make([]BranchResult, 0, len(in))
	for i := range in {
		branches = append(branches, BranchResult{
			Index:           i + 1,
			Output:          in[i].Output,
			EvaluationScore: in[i].Score,
		})
	}
	pruned, stats := e.pruneBranches("mcts", branches, e.cfg.PruningMinScore, topK)
	out := make([]mctsCandidate, 0, len(pruned))
	for _, b := range pruned {
		idx := b.Index - 1
		if idx >= 0 && idx < len(in) {
			out = append(out, in[idx])
		}
	}
	stats.CandidatesOut = len(out)
	return out, stats
}

func (e *Executor) pruneMultiAgentCandidates(in []agentResult, topK int) ([]agentResult, pruneStats) {
	if !e.cfg.PruningEnabled {
		out := append([]agentResult{}, in...)
		return out, pruneStats{CandidatesIn: len(in), CandidatesOut: len(in)}
	}
	branches := make([]BranchResult, 0, len(in))
	for i := range in {
		branches = append(branches, BranchResult{
			Index:           i + 1,
			Output:          in[i].Output,
			EvaluationScore: in[i].Score,
		})
	}
	pruned, stats := e.pruneBranches("multi_agent", branches, e.cfg.PruningMinScore, topK)
	out := make([]agentResult, 0, len(pruned))
	for _, b := range pruned {
		idx := b.Index - 1
		if idx >= 0 && idx < len(in) {
			out = append(out, in[idx])
		}
	}
	stats.CandidatesOut = len(out)
	return out, stats
}

func pruneStableSort(mode string, in []BranchResult) {
	sort.Slice(in, func(i, j int) bool {
		if in[i].EvaluationScore == in[j].EvaluationScore {
			switch mode {
			case "mcts", "multi_agent":
				if len(in[i].Output) == len(in[j].Output) {
					return in[i].Index < in[j].Index
				}
				return len(in[i].Output) > len(in[j].Output)
			default:
				return in[i].Index < in[j].Index
			}
		}
		return in[i].EvaluationScore > in[j].EvaluationScore
	})
}

func (e *Executor) pruneBranches(mode string, in []BranchResult, minScore float64, topK int) ([]BranchResult, pruneStats) {
	stats := pruneStats{CandidatesIn: len(in), CandidatesOut: len(in)}
	if len(in) == 0 {
		return nil, stats
	}
	out := make([]BranchResult, 0, len(in))
	for _, b := range in {
		if b.EvaluationScore < minScore {
			stats.DroppedLowScore++
			continue
		}
		out = append(out, b)
	}
	pruneStableSort(mode, out)
	if topK > 0 && len(out) > topK {
		stats.DroppedTopK = len(out) - topK
		out = out[:topK]
	}
	stats.CandidatesOut = len(out)
	return out, stats
}

func safeMode(req model.ChatCompletionRequest) string {
	if req.Reasoning == nil || strings.TrimSpace(req.Reasoning.Mode) == "" {
		return "none"
	}
	return strings.ToLower(strings.TrimSpace(req.Reasoning.Mode))
}

func buildBranchMessages(base []model.Message, branchIndex, branchTotal int, anchors []string) []model.Message {
	style := branchPrompt(branchIndex)
	anchorHint := ""
	if len(anchors) > 0 {
		anchorHint = " Memory anchors (prioritize if relevant): " + strings.Join(anchors, ", ") + "."
	}
	sys := model.Message{
		Role: "system",
		Content: "Reasoning pipeline branch " + strconv.Itoa(branchIndex+1) + "/" + strconv.Itoa(branchTotal) +
			": produce a complete answer with explicit assumptions and checks. Strategy=" + style + "." + anchorHint,
	}
	out := make([]model.Message, 0, len(base)+1)
	out = append(out, sys)
	out = append(out, base...)
	return out
}

func branchPrompt(idx int) string {
	switch idx % 3 {
	case 0:
		return "deductive"
	case 1:
		return "risk-first"
	default:
		return "cost-performance"
	}
}

func buildSynthesisMessages(base []model.Message, branches []BranchResult, contradictions ContradictionReport, anchors []string) []model.Message {
	top := branches
	if len(top) > 3 {
		top = top[:3]
	}
	payload := map[string]any{
		"top_branches":   top,
		"contradictions": contradictions,
	}
	if len(anchors) > 0 {
		payload["memory_anchors"] = anchors
	}
	data, _ := json.Marshal(payload)
	sys := model.Message{
		Role: "system",
		Content: "Synthesize the strongest final response. Resolve conflicts using evidence and mention residual uncertainty briefly. Branch payload: " +
			string(data),
	}
	out := make([]model.Message, 0, len(base)+1)
	out = append(out, sys)
	out = append(out, base...)
	return out
}

func detectContradictions(branches []BranchResult) ContradictionReport {
	report := ContradictionReport{Detected: false}
	if len(branches) < 2 {
		return report
	}
	pairs := []string{}
	for i := 0; i < len(branches); i++ {
		for j := i + 1; j < len(branches); j++ {
			if looksContradictory(branches[i].Output, branches[j].Output) {
				pairs = append(pairs, fmt.Sprintf("%d:%d", branches[i].Index, branches[j].Index))
			}
		}
	}
	if len(pairs) > 0 {
		report.Detected = true
		report.Pairs = pairs
		report.Summary = "potential contradictions found across branch conclusions"
	}
	return report
}

func looksContradictory(a, b string) bool {
	if strings.TrimSpace(a) == "" || strings.TrimSpace(b) == "" {
		return false
	}
	la := strings.ToLower(a)
	lb := strings.ToLower(b)
	hasNegA := strings.Contains(la, "cannot") || strings.Contains(la, "not possible") || strings.Contains(la, "should not")
	hasNegB := strings.Contains(lb, "cannot") || strings.Contains(lb, "not possible") || strings.Contains(lb, "should not")
	if hasNegA == hasNegB {
		return false
	}
	return lexicalOverlap(la, lb) > 0.35
}

func lexicalOverlap(a, b string) float64 {
	ta := tokenize(a)
	tb := tokenize(b)
	if len(ta) == 0 || len(tb) == 0 {
		return 0
	}
	common := 0
	for k := range ta {
		if _, ok := tb[k]; ok {
			common++
		}
	}
	den := math.Max(float64(len(ta)), float64(len(tb)))
	return float64(common) / den
}

func tokenize(s string) map[string]struct{} {
	toks := strings.FieldsFunc(s, func(r rune) bool {
		return r == ' ' || r == '\n' || r == '\t' || r == ',' || r == '.' || r == ':' || r == ';' || r == '(' || r == ')' || r == '"' || r == '\''
	})
	out := map[string]struct{}{}
	for _, t := range toks {
		t = strings.TrimSpace(t)
		if len(t) < 4 {
			continue
		}
		out[t] = struct{}{}
	}
	return out
}

func extractAssistantText(resp model.ChatCompletionResponse) string {
	if len(resp.Choices) == 0 {
		return ""
	}
	return strings.TrimSpace(resp.Choices[0].Message.Content)
}

func (e *Executor) evaluateOutput(req model.ChatCompletionRequest, output string, st state.CognitiveState, applyStatePenalty bool) (float64, string) {
	score, reason, _, _ := e.evaluateOutputWithMemory(req, output, st, applyStatePenalty)
	return score, reason
}

func (e *Executor) evaluateOutputWithMemory(req model.ChatCompletionRequest, output string, st state.CognitiveState, applyStatePenalty bool) (float64, string, float64, float64) {
	if req.Reasoning != nil && !req.Reasoning.SelfEvaluate {
		return 0.5, "self_evaluate_disabled", 0, 0
	}

	score, reason := e.legacySelfEvaluate(output, st)
	if e.cfg.SelfEvalCurveEnabled {
		score = e.applySelfEvalCurve(score)
		reason = "heuristic_quality_curve"
	}
	if applyStatePenalty {
		score = applyMCTSStatePenalty(score, st)
	}
	anchors := e.resolveMemoryAnchors(req)
	coverage := 0.0
	bonus := 0.0
	if e.cfg.MemoryAnchoredReasoningEnabled && len(anchors) > 0 {
		coverage = memoryAnchorCoverage(anchors, output)
		if coverage >= e.cfg.MemoryAnchoredReasoningMinCoverage {
			bonus = e.cfg.MemoryAnchoredReasoningScoreBonus * coverage
			score += bonus
			if score > 1 {
				score = 1
			}
			score = math.Round(score*1000) / 1000
		}
	}
	return score, reason, coverage, bonus
}

func (e *Executor) resolveMemoryAnchors(req model.ChatCompletionRequest) []string {
	if !e.cfg.MemoryAnchoredReasoningEnabled {
		return nil
	}
	anchors := normalizeAnchorSet(req.MemoryAnchorKeys)
	if len(anchors) == 0 {
		return nil
	}
	limit := e.cfg.MemoryAnchoredReasoningMaxAnchors
	if limit <= 0 {
		limit = 3
	}
	if len(anchors) > limit {
		return anchors[:limit]
	}
	return anchors
}

func normalizeAnchorSet(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	seen := map[string]struct{}{}
	for _, raw := range in {
		v := strings.ToLower(strings.TrimSpace(raw))
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func memoryAnchorCoverage(anchors []string, output string) float64 {
	anchors = normalizeAnchorSet(anchors)
	if len(anchors) == 0 {
		return 0
	}
	toks := tokenizeNormalized(output)
	if len(toks) == 0 {
		return 0
	}
	matched := 0
	for _, a := range anchors {
		if _, ok := toks[a]; ok {
			matched++
		}
	}
	return math.Round((float64(matched)/float64(len(anchors)))*1000) / 1000
}

func tokenizeNormalized(s string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, t := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-')
	}) {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		out[t] = struct{}{}
	}
	return out
}

func (e *Executor) applySelfEvalCurve(score float64) float64 {
	switch {
	case score <= e.cfg.SelfEvalCurveLowMax:
		score = (score * e.cfg.SelfEvalCurveLowWeight) + e.cfg.SelfEvalCurveBias
	case score <= e.cfg.SelfEvalCurveMidMax:
		score = (score * e.cfg.SelfEvalCurveMidWeight) + e.cfg.SelfEvalCurveBias
	default:
		score = (score * e.cfg.SelfEvalCurveHighWeight) + e.cfg.SelfEvalCurveBias
	}
	if score > 1 {
		score = 1
	}
	if score < 0 {
		score = 0
	}
	return math.Round(score*1000) / 1000
}

func (e *Executor) legacySelfEvaluate(output string, st state.CognitiveState) (float64, string) {
	if strings.TrimSpace(output) == "" {
		return 0.2, "empty_output"
	}
	score := 0.55
	words := len(strings.Fields(output))
	if words > 40 {
		score += 0.15
	}
	if words > 120 {
		score += 0.08
	}
	if strings.Contains(strings.ToLower(output), "assumption") || strings.Contains(strings.ToLower(output), "tradeoff") {
		score += 0.08
	}
	if st.TaskMode == "coding" && strings.Contains(strings.ToLower(output), "test") {
		score += 0.08
	}
	if strings.Contains(strings.ToLower(output), "not sure") {
		score -= 0.10
	}
	if score > 1 {
		score = 1
	}
	if score < 0 {
		score = 0
	}
	return math.Round(score*1000) / 1000, "heuristic_quality"
}

func branchWarnings(output string) []string {
	warnings := []string{}
	lower := strings.ToLower(output)
	if strings.Contains(lower, "i cannot") || strings.Contains(lower, "unable") {
		warnings = append(warnings, "capability_limitation")
	}
	if len(output) < 30 {
		warnings = append(warnings, "very_short_output")
	}
	return warnings
}

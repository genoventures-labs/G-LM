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
	Enabled         bool
	DefaultBranches int
	MaxBranches     int
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
	Nodes          []Node              `json:"nodes"`
}

func NewExecutor(cfg Config, router *orchestrator.Router) *Executor {
	if cfg.DefaultBranches <= 0 {
		cfg.DefaultBranches = 3
	}
	if cfg.MaxBranches <= 0 {
		cfg.MaxBranches = 5
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
	allNodes := make([]Node, 0, branches+4)

	for i := 0; i < branches; i++ {
		node := Node{ID: fmt.Sprintf("branch-%d", i+1), Type: "branch", Model: baseModel, StartedAt: time.Now().UTC()}
		branchReq := req
		branchReq.Model = baseModel
		branchReq.Messages = buildBranchMessages(req.Messages, i, branches)
		resp, callErr := up.ChatCompletions(ctx, branchReq)
		node.EndedAt = time.Now().UTC()
		if callErr != nil {
			node.Error = callErr.Error()
			allNodes = append(allNodes, node)
			continue
		}
		output := extractAssistantText(resp)
		score, reason := e.selfEvaluate(output, st)
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
	}
	if len(branchResults) == 0 {
		return model.ChatCompletionResponse{}, trace, fmt.Errorf("pipeline failed: no successful branches")
	}

	sort.Slice(branchResults, func(i, j int) bool {
		if branchResults[i].EvaluationScore == branchResults[j].EvaluationScore {
			return branchResults[i].Index < branchResults[j].Index
		}
		return branchResults[i].EvaluationScore > branchResults[j].EvaluationScore
	})

	contradictions := detectContradictions(branchResults)
	trace.Contradictions = contradictions
	trace.Branches = branchResults

	synthNode := Node{ID: "synthesis-1", Type: "synthesis", Model: baseModel, StartedAt: time.Now().UTC()}
	synthReq := req
	synthReq.Model = baseModel
	synthReq.Messages = buildSynthesisMessages(req.Messages, branchResults, contradictions)
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

func safeMode(req model.ChatCompletionRequest) string {
	if req.Reasoning == nil || strings.TrimSpace(req.Reasoning.Mode) == "" {
		return "none"
	}
	return strings.ToLower(strings.TrimSpace(req.Reasoning.Mode))
}

func buildBranchMessages(base []model.Message, branchIndex, branchTotal int) []model.Message {
	style := branchPrompt(branchIndex)
	sys := model.Message{
		Role: "system",
		Content: "Reasoning pipeline branch " + strconv.Itoa(branchIndex+1) + "/" + strconv.Itoa(branchTotal) +
			": produce a complete answer with explicit assumptions and checks. Strategy=" + style,
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

func buildSynthesisMessages(base []model.Message, branches []BranchResult, contradictions ContradictionReport) []model.Message {
	top := branches
	if len(top) > 3 {
		top = top[:3]
	}
	payload := map[string]any{
		"top_branches":   top,
		"contradictions": contradictions,
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

func (e *Executor) selfEvaluate(output string, st state.CognitiveState) (float64, string) {
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

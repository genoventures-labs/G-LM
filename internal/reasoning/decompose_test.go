package reasoning

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/mike/cognitive-llm/internal/model"
	"github.com/mike/cognitive-llm/internal/orchestrator"
	"github.com/mike/cognitive-llm/internal/state"
)

type decomposeUpstream struct {
	plannerOutput string
	failWorkers   bool
	workerCalls   int
}

func (d *decomposeUpstream) ListModels(ctx context.Context) (model.ModelListResponse, error) {
	return model.ModelListResponse{Data: []model.ModelInfo{{ID: "qwen3:4b"}, {ID: "mistral:7b"}}}, nil
}

func (d *decomposeUpstream) ChatCompletions(ctx context.Context, req model.ChatCompletionRequest) (model.ChatCompletionResponse, error) {
	resp := model.ChatCompletionResponse{Model: req.Model}
	content := "default response"
	if len(req.Messages) > 0 {
		sys := req.Messages[0].Content
		switch {
		case strings.HasPrefix(sys, "decompose planner:"):
			if strings.TrimSpace(d.plannerOutput) != "" {
				content = d.plannerOutput
			} else {
				content = `{"subtasks":[{"task":"inventory constraints"},{"task":"propose rollout"},{"task":"define validation"}]}`
			}
		case strings.HasPrefix(sys, "decompose worker context:"):
			d.workerCalls++
			if d.failWorkers {
				return model.ChatCompletionResponse{}, fmt.Errorf("worker failure")
			}
			content = fmt.Sprintf("worker output %d with assumption and test", d.workerCalls)
		case strings.HasPrefix(sys, "decompose synthesizer payload:"):
			content = "final synthesized response"
		default:
			content = "baseline output with assumption and test"
		}
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
	}{{
		Index: 0,
		Message: struct {
			Role      string           `json:"role"`
			Content   string           `json:"content"`
			Name      string           `json:"name,omitempty"`
			ToolCalls []model.ToolCall `json:"tool_calls,omitempty"`
		}{Role: "assistant", Content: content},
	}}
	return resp, nil
}

func newDecomposeExecutor() *Executor {
	r := orchestrator.NewRouter("qwen3-8b-instruct-Q4_K_M", []string{"qwen3:8b"}, "qwen3:4b")
	return NewExecutor(Config{
		Enabled:                true,
		PruningEnabled:         true,
		PruningMinScore:        0,
		PruningToTSynthTopK:    2,
		DecomposeEnabled:       true,
		DecomposeMaxSubtasks:   6,
		DecomposeMaxDepth:      1,
		DecomposeBudgetTokens:  900,
		MCTSEnabled:            true,
		MCTSDefaultRollouts:    4,
		MCTSMaxRollouts:        8,
		MCTSDefaultDepth:       2,
		MCTSMaxDepth:           3,
		MCTSDefaultExploration: 1.2,
	}, r)
}

func decomposeReq() model.ChatCompletionRequest {
	return model.ChatCompletionRequest{
		Model: "auto",
		Reasoning: &model.ReasoningOptions{
			Mode:              "decompose",
			DecomposeEnabled:  true,
			DecomposeMaxDepth: 2,
		},
		Messages: []model.Message{{Role: "user", Content: "Build a rollout plan"}},
	}
}

func TestExecuteDecomposeProducesTrace(t *testing.T) {
	e := newDecomposeExecutor()
	up := &decomposeUpstream{}
	pol := model.ModelPolicy{AllowedModels: []string{"qwen3:4b", "mistral:7b"}, PrimaryModel: "qwen3:4b"}

	resp, trace, err := e.Execute(context.Background(), up, decomposeReq(), pol, state.CognitiveState{TaskMode: "coding"})
	if err != nil {
		t.Fatalf("execute decompose failed: %v", err)
	}
	if trace.Mode != "decompose" {
		t.Fatalf("expected decompose mode, got %q", trace.Mode)
	}
	if trace.Decompose == nil {
		t.Fatal("expected decompose trace payload")
	}
	if trace.Decompose.SubtasksPlanned < 1 || trace.Decompose.SubtasksExecuted < 1 {
		t.Fatalf("expected positive subtask counts, got planned=%d executed=%d", trace.Decompose.SubtasksPlanned, trace.Decompose.SubtasksExecuted)
	}
	if resp.Model == "" {
		t.Fatal("expected response model")
	}
}

func TestExecuteDecomposeClampsSubtasks(t *testing.T) {
	e := newDecomposeExecutor()
	up := &decomposeUpstream{
		plannerOutput: `{"subtasks":["one","two","three","four","five","six","seven","eight"]}`,
	}
	req := decomposeReq()
	req.Reasoning.DecomposeMaxSubtasks = 99
	pol := model.ModelPolicy{AllowedModels: []string{"qwen3:4b", "mistral:7b"}, PrimaryModel: "qwen3:4b"}

	_, trace, err := e.Execute(context.Background(), up, req, pol, state.CognitiveState{TaskMode: "general"})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if trace.Decompose.SubtasksPlanned != 6 {
		t.Fatalf("expected clamped planned subtasks=6, got %d", trace.Decompose.SubtasksPlanned)
	}
}

func TestExecuteDecomposeInvalidPlannerFallsBackSingleSubtask(t *testing.T) {
	e := newDecomposeExecutor()
	up := &decomposeUpstream{plannerOutput: "not-json"}
	pol := model.ModelPolicy{AllowedModels: []string{"qwen3:4b", "mistral:7b"}, PrimaryModel: "qwen3:4b"}

	_, trace, err := e.Execute(context.Background(), up, decomposeReq(), pol, state.CognitiveState{TaskMode: "general"})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if trace.Decompose.SubtasksPlanned != 1 {
		t.Fatalf("expected planner fallback to one subtask, got %d", trace.Decompose.SubtasksPlanned)
	}
}

func TestExecuteDecomposeSequentialWorkerCalls(t *testing.T) {
	e := newDecomposeExecutor()
	up := &decomposeUpstream{
		plannerOutput: `{"subtasks":["a","b","c"]}`,
	}
	pol := model.ModelPolicy{AllowedModels: []string{"qwen3:4b", "mistral:7b"}, PrimaryModel: "qwen3:4b"}

	_, trace, err := e.Execute(context.Background(), up, decomposeReq(), pol, state.CognitiveState{TaskMode: "coding"})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if up.workerCalls != 3 {
		t.Fatalf("expected 3 sequential worker calls, got %d", up.workerCalls)
	}
	if trace.Decompose.SubtasksExecuted != 3 {
		t.Fatalf("expected 3 executed subtasks, got %d", trace.Decompose.SubtasksExecuted)
	}
}

func TestExecuteDecomposeSelfEvaluateDisabledUsesNeutralScore(t *testing.T) {
	e := newDecomposeExecutor()
	up := &decomposeUpstream{}
	req := decomposeReq()
	req.Reasoning.SelfEvaluate = false
	pol := model.ModelPolicy{AllowedModels: []string{"qwen3:4b", "mistral:7b"}, PrimaryModel: "qwen3:4b"}

	_, trace, err := e.Execute(context.Background(), up, req, pol, state.CognitiveState{TaskMode: "coding"})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if trace.Decompose == nil {
		t.Fatal("expected decompose trace")
	}
	if trace.Decompose.BestScore != 0.5 {
		t.Fatalf("expected neutral best score 0.5, got %f", trace.Decompose.BestScore)
	}
}

func TestExecuteDecomposePruningApplied(t *testing.T) {
	e := newDecomposeExecutor()
	up := &decomposeUpstream{}
	req := decomposeReq()
	req.Reasoning.SelfEvaluate = true
	pol := model.ModelPolicy{AllowedModels: []string{"qwen3:4b", "mistral:7b"}, PrimaryModel: "qwen3:4b"}

	_, trace, err := e.Execute(context.Background(), up, req, pol, state.CognitiveState{TaskMode: "coding"})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if trace.Pruning == nil {
		t.Fatal("expected pruning trace")
	}
	if len(trace.Branches) > e.cfg.PruningToTSynthTopK {
		t.Fatalf("expected branches <= synth top-k, got %d", len(trace.Branches))
	}
}

func TestExecuteDecomposeContextCancelled(t *testing.T) {
	e := newDecomposeExecutor()
	up := &decomposeUpstream{}
	pol := model.ModelPolicy{AllowedModels: []string{"qwen3:4b", "mistral:7b"}, PrimaryModel: "qwen3:4b"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, trace, err := e.Execute(ctx, up, decomposeReq(), pol, state.CognitiveState{TaskMode: "general"})
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
	if trace.Mode != "decompose" {
		t.Fatalf("expected decompose trace mode, got %q", trace.Mode)
	}
}

func TestExecuteDecomposeFallsBackBaselineWhenWorkersFail(t *testing.T) {
	e := newDecomposeExecutor()
	up := &decomposeUpstream{
		plannerOutput: `{"subtasks":["a","b"]}`,
		failWorkers:   true,
	}
	pol := model.ModelPolicy{AllowedModels: []string{"qwen3:4b", "mistral:7b"}, PrimaryModel: "qwen3:4b"}

	resp, trace, err := e.Execute(context.Background(), up, decomposeReq(), pol, state.CognitiveState{TaskMode: "general"})
	if err != nil {
		t.Fatalf("expected baseline fallback success, got %v", err)
	}
	if trace.Decompose == nil {
		t.Fatal("expected decompose trace")
	}
	if trace.Decompose.SubtasksExecuted != 1 {
		t.Fatalf("expected baseline execution count=1, got %d", trace.Decompose.SubtasksExecuted)
	}
	if resp.Model == "" {
		t.Fatal("expected response model")
	}
}

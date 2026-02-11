package orchestrator

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/mike/cognitive-llm/internal/model"
)

func mkPolicy(allowed ...string) model.ModelPolicy {
	return model.ModelPolicy{AllowedModels: allowed, PrimaryModel: "mistral:7b"}
}

func TestRouterAutoGeneral_DefaultAliasFallbackToQwen3_4b(t *testing.T) {
	r := NewRouter("qwen3-8b-instruct-Q4_K_M", []string{"qwen3-8b-instruct-Q4_K_M", "qwen3:8b"}, "qwen3:4b")
	req := model.ChatCompletionRequest{Messages: []model.Message{{Role: "user", Content: "Give me an overview of enterprise ai controls"}}}
	avail := []string{"qwen3:4b", "mistral:7b"}
	pol := mkPolicy("qwen3:4b", "mistral:7b")
	d, err := r.Choose(req, avail, pol)
	if err != nil {
		t.Fatalf("choose failed: %v", err)
	}
	if d.ChosenModel != "qwen3:4b" {
		t.Fatalf("expected qwen3:4b, got %s", d.ChosenModel)
	}
}

func TestRouterCodingPrefersMistral(t *testing.T) {
	r := NewRouter("qwen3-8b-instruct-Q4_K_M", []string{"qwen3:8b"}, "qwen3:4b")
	req := model.ChatCompletionRequest{Messages: []model.Message{{Role: "user", Content: "Fix this stack trace and implement tests"}}}
	avail := []string{"qwen3:4b", "mistral:7b"}
	pol := mkPolicy("qwen3:4b", "mistral:7b")
	d, err := r.Choose(req, avail, pol)
	if err != nil {
		t.Fatalf("choose failed: %v", err)
	}
	if d.ChosenModel != "mistral:7b" {
		t.Fatalf("expected mistral:7b, got %s", d.ChosenModel)
	}
}

func TestRouterNoAllowedAvailable(t *testing.T) {
	r := NewRouter("qwen3-8b-instruct-Q4_K_M", []string{"qwen3:8b"}, "qwen3:4b")
	req := model.ChatCompletionRequest{Messages: []model.Message{{Role: "user", Content: "hello"}}}
	avail := []string{"qwen3:4b", "mistral:7b"}
	pol := mkPolicy("phi3:mini")
	_, err := r.Choose(req, avail, pol)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestShouldAutoRoute(t *testing.T) {
	if !ShouldAutoRoute("") || !ShouldAutoRoute("auto") || !ShouldAutoRoute("AUTO") {
		t.Fatal("expected auto route for empty/auto")
	}
	if ShouldAutoRoute("mistral:7b") {
		t.Fatal("did not expect auto route for explicit model")
	}
}

type fakeControl struct {
	mu          sync.Mutex
	models      []string
	pullCalls   int
	deleteCalls []string
	statsUsed   uint64
	statsTotal  uint64
	statsAvail  bool
	statsErr    error
	pullBlock   chan struct{}
}

func (f *fakeControl) ListModels(ctx context.Context) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.models))
	copy(out, f.models)
	return out, nil
}

func (f *fakeControl) PullModel(ctx context.Context, model string) error {
	f.mu.Lock()
	f.pullCalls++
	f.mu.Unlock()
	if f.pullBlock != nil {
		select {
		case <-f.pullBlock:
		case <-ctx.Done():
		}
	}
	return nil
}

func (f *fakeControl) DeleteModel(ctx context.Context, model string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteCalls = append(f.deleteCalls, model)
	return nil
}

func (f *fakeControl) HostStats(ctx context.Context) (uint64, uint64, bool, error) {
	return f.statsUsed, f.statsTotal, f.statsAvail, f.statsErr
}

func TestRouterMissingIdealTriggersBackgroundPull(t *testing.T) {
	ctrl := &fakeControl{pullBlock: make(chan struct{})}
	r := NewRouterWithControl(
		"qwen3-8b-instruct-Q4_K_M",
		[]string{"qwen3:8b"},
		"qwen3:4b",
		RouterConfig{
			JITInventoryEnabled: true,
			PullTimeout:         2 * time.Second,
			IdealCoding:         "deepseek-coder:6.7b",
			IdealExtraction:     "phi3:medium",
			IdealLightQA:        "llama3.2:1b",
			IdealGeneral:        "qwen3-8b-instruct-Q4_K_M",
		},
		ctrl,
	)
	req := model.ChatCompletionRequest{Messages: []model.Message{{Role: "user", Content: "Fix this stack trace and implement tests"}}}
	avail := []string{"qwen3:4b", "mistral:7b"}
	pol := mkPolicy("qwen3:4b", "mistral:7b", "deepseek-coder:6.7b")
	d, err := r.Choose(req, avail, pol)
	if err != nil {
		t.Fatalf("choose failed: %v", err)
	}
	if d.ChosenModel != "mistral:7b" {
		t.Fatalf("expected warm fallback mistral:7b, got %s", d.ChosenModel)
	}
	if !d.JITPullTriggered {
		t.Fatal("expected jit pull to be triggered")
	}
	pulls := waitPullCalls(ctrl, time.Second)
	if pulls != 1 {
		t.Fatalf("expected one pull call, got %d", pulls)
	}
	close(ctrl.pullBlock)
}

func TestRouterInFlightDedupe(t *testing.T) {
	ctrl := &fakeControl{pullBlock: make(chan struct{})}
	r := NewRouterWithControl("qwen3-8b-instruct-Q4_K_M", []string{"qwen3:8b"}, "qwen3:4b", RouterConfig{
		JITInventoryEnabled: true,
		PullTimeout:         2 * time.Second,
		IdealCoding:         "deepseek-coder:6.7b",
		IdealExtraction:     "phi3:medium",
		IdealLightQA:        "llama3.2:1b",
		IdealGeneral:        "qwen3-8b-instruct-Q4_K_M",
	}, ctrl)
	req := model.ChatCompletionRequest{Messages: []model.Message{{Role: "user", Content: "Fix this stack trace and implement tests"}}}
	avail := []string{"qwen3:4b", "mistral:7b"}
	pol := mkPolicy("qwen3:4b", "mistral:7b")
	_, _ = r.Choose(req, avail, pol)
	_, _ = r.Choose(req, avail, pol)
	pulls := waitPullCalls(ctrl, time.Second)
	if pulls != 1 {
		t.Fatalf("expected one in-flight pull, got %d", pulls)
	}
	close(ctrl.pullBlock)
}

func TestRouterIdealPresentPreferred(t *testing.T) {
	r := NewRouterWithControl("qwen3-8b-instruct-Q4_K_M", []string{"qwen3:8b"}, "qwen3:4b", RouterConfig{
		IdealCoding:         "deepseek-coder:6.7b",
		IdealExtraction:     "phi3:medium",
		IdealLightQA:        "llama3.2:1b",
		IdealGeneral:        "qwen3-8b-instruct-Q4_K_M",
		JITInventoryEnabled: true,
	}, nil)
	req := model.ChatCompletionRequest{Messages: []model.Message{{Role: "user", Content: "Fix this stack trace and implement tests"}}}
	avail := []string{"deepseek-coder:6.7b", "mistral:7b"}
	pol := mkPolicy("deepseek-coder:6.7b", "mistral:7b")
	d, err := r.Choose(req, avail, pol)
	if err != nil {
		t.Fatalf("choose failed: %v", err)
	}
	if d.ChosenModel != "deepseek-coder:6.7b" {
		t.Fatalf("expected ideal coding model, got %s", d.ChosenModel)
	}
	if d.Reason != "auto.ideal.coding_reasoning" {
		t.Fatalf("unexpected reason: %s", d.Reason)
	}
}

func TestRouterPruneSkipsProtectedModels(t *testing.T) {
	ctrl := &fakeControl{
		statsUsed: 95, statsTotal: 100, statsAvail: true,
		models: []string{
			"qwen3-8b-instruct-Q4_K_M", "qwen3:4b", "deepseek-coder:6.7b", "phi3:medium", "llama3.2:1b",
			"old-model:1b", "old-model:2b",
		},
	}
	r := NewRouterWithControl("qwen3-8b-instruct-Q4_K_M", []string{"qwen3:8b"}, "qwen3:4b", RouterConfig{
		JITInventoryEnabled:    true,
		PruneEnabled:           true,
		MaxModels:              20,
		StorageHighWatermark:   0.85,
		StorageTargetWatermark: 0.75,
		IdealCoding:            "deepseek-coder:6.7b",
		IdealExtraction:        "phi3:medium",
		IdealLightQA:           "llama3.2:1b",
		IdealGeneral:           "qwen3-8b-instruct-Q4_K_M",
	}, ctrl)
	r.setSnapshot(ctrl.models, false)
	r.lastUsedMu.Lock()
	r.lastUsed["old-model:1b"] = time.Now().Add(-2 * time.Hour)
	r.lastUsed["old-model:2b"] = time.Now().Add(-1 * time.Hour)
	r.lastUsedMu.Unlock()
	r.cleanInventoryIfNeeded(context.Background())
	ctrl.mu.Lock()
	defer ctrl.mu.Unlock()
	if len(ctrl.deleteCalls) == 0 {
		t.Fatal("expected at least one prune delete call")
	}
	for _, deleted := range ctrl.deleteCalls {
		if deleted == "qwen3-8b-instruct-Q4_K_M" || deleted == "qwen3:4b" || deleted == "deepseek-coder:6.7b" {
			t.Fatalf("protected model should not be deleted: %s", deleted)
		}
	}
}

func waitPullCalls(ctrl *fakeControl, timeout time.Duration) int {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ctrl.mu.Lock()
		n := ctrl.pullCalls
		ctrl.mu.Unlock()
		if n > 0 {
			return n
		}
		time.Sleep(10 * time.Millisecond)
	}
	ctrl.mu.Lock()
	defer ctrl.mu.Unlock()
	return ctrl.pullCalls
}

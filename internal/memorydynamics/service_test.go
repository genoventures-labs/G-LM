package memorydynamics

import (
	"context"
	"testing"
	"time"

	"github.com/mike/cognitive-llm/internal/model"
	"github.com/mike/cognitive-llm/internal/store/memory"
)

func TestBuildContextAndReplay(t *testing.T) {
	st := memory.New()
	svc := New(st, Config{Enabled: true, HalfLifeHours: 24, ReplayThreshold: 0.5, ContextNodeLimit: 3})
	now := model.FlexTime{Time: time.Now().UTC().Add(-1 * time.Hour)}
	_, _ = st.UpsertMemoryNode(context.Background(), model.MemoryNode{TenantID: "t1", SessionID: "s1", Key: "rollout", Label: "rollout", Weight: 0.9, Importance: 0.9, LastSeenAt: now})
	req := model.ChatCompletionRequest{Messages: []model.Message{{Role: "user", Content: "help"}}}
	out, res, err := svc.BuildContext(context.Background(), "t1", "s1", req)
	if err != nil {
		t.Fatalf("build context failed: %v", err)
	}
	if !res.Applied || res.NodeCount == 0 {
		t.Fatal("expected applied context")
	}
	if !res.ReplayTriggered {
		t.Fatal("expected replay trigger")
	}
	if len(out.Messages) == 0 || out.Messages[0].Role != "system" {
		t.Fatal("expected memory context system message")
	}
}

func TestUpdateFromTurnCreatesNodes(t *testing.T) {
	st := memory.New()
	svc := New(st, Config{Enabled: true, UpdateConceptsPerTurn: 4})
	err := svc.UpdateFromTurn(context.Background(), "t1", "s1", "Critical rollout decision for audit controls", "Action plan defined")
	if err != nil {
		t.Fatalf("update failed: %v", err)
	}
	nodes, err := st.ListMemoryNodes(context.Background(), "t1", "s1", 20)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(nodes) == 0 {
		t.Fatal("expected memory nodes")
	}
}

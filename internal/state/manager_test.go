package state

import (
	"testing"
	"time"

	"github.com/mike/cognitive-llm/internal/model"
)

func TestStateManagerTracksPerSessionAndTaskModes(t *testing.T) {
	m := NewManager(5)
	reqA := model.ChatCompletionRequest{Messages: []model.Message{{Role: "user", Content: "Please debug this code panic"}}}
	reqB := model.ChatCompletionRequest{Messages: []model.Message{{Role: "user", Content: "What is DNS?"}}}
	a := m.RecordUserInput("s1", reqA)
	b := m.RecordUserInput("s2", reqB)
	if a.TaskMode != "coding" {
		t.Fatalf("expected coding mode, got %s", a.TaskMode)
	}
	if b.TaskMode != "qa_light" {
		t.Fatalf("expected qa_light mode, got %s", b.TaskMode)
	}
	if a.SessionID == b.SessionID {
		t.Fatal("expected different sessions")
	}
}

func TestStateManagerSentimentCarryoverAndTone(t *testing.T) {
	m := NewManager(10)
	m.RecordUserInput("s", model.ChatCompletionRequest{Messages: []model.Message{{Role: "user", Content: "I love this, great work"}}})
	s, ok := m.Snapshot("s")
	if !ok {
		t.Fatal("expected snapshot")
	}
	if s.Sentiment != "positive" {
		t.Fatalf("expected positive sentiment, got %s", s.Sentiment)
	}
	if s.SentimentCarryover <= 0 {
		t.Fatalf("expected positive carryover, got %f", s.SentimentCarryover)
	}
	if s.ToneCompensation.TargetTone == "" {
		t.Fatal("expected tone compensation")
	}
}

func TestStateManagerTopicStateStoresKeywordsOnly(t *testing.T) {
	m := NewManager(10)
	m.RecordUserInput("s", model.ChatCompletionRequest{Messages: []model.Message{{Role: "user", Content: "Kubernetes rollout strategy for enterprise compliance"}}})
	s, ok := m.Snapshot("s")
	if !ok {
		t.Fatal("expected snapshot")
	}
	if s.Topic == "" {
		t.Fatal("expected topic")
	}
	if len(s.TopicKeywords) == 0 {
		t.Fatal("expected topic keywords")
	}
}

func TestStateManagerEmotionalDecay(t *testing.T) {
	m := NewManager(5)
	m.RecordUserInput("s", model.ChatCompletionRequest{Messages: []model.Message{{Role: "user", Content: "urgent! critical issue!"}}})
	before, ok := m.Snapshot("s")
	if !ok {
		t.Fatal("expected snapshot")
	}
	time.Sleep(1200 * time.Millisecond)
	after := m.RecordAssistantOutput("s", "ack")
	if after.RecencySeconds > 0.1 {
		t.Fatalf("expected recency reset close to 0, got %f", after.RecencySeconds)
	}
	if after.EmotionalEnergy > before.EmotionalEnergy+0.2 {
		t.Fatalf("expected bounded emotional energy after decay, before=%f after=%f", before.EmotionalEnergy, after.EmotionalEnergy)
	}
}

func TestMicroSwitchSignals(t *testing.T) {
	m := NewManager(10)
	_ = m.RecordUserInput("s", model.ChatCompletionRequest{Messages: []model.Message{{Role: "user", Content: "Please review kubernetes rollout steps for audit controls"}}})
	s := m.RecordUserInput("s", model.ChatCompletionRequest{Messages: []model.Message{{Role: "user", Content: "Urgent! now help with incident response escalation for database outage"}}})
	if s.Pacing == "" {
		t.Fatal("expected pacing signal")
	}
	if s.TopicDrift <= 0 {
		t.Fatalf("expected topic drift > 0, got %f", s.TopicDrift)
	}
	if s.MoodShift < 0 {
		t.Fatalf("expected non-negative mood shift, got %f", s.MoodShift)
	}
	if len(s.MicroSwitches) == 0 {
		t.Fatal("expected at least one micro-switch")
	}
}

package document

import (
	"context"
	"strings"
	"testing"

	"github.com/mike/cognitive-llm/internal/model"
)

type fakeUpstream struct {
	calls int
}

func (f *fakeUpstream) ChatCompletions(ctx context.Context, req model.ChatCompletionRequest) (model.ChatCompletionResponse, error) {
	f.calls++
	resp := model.ChatCompletionResponse{Model: req.Model}
	resp.Choices = []struct {
		Index   int `json:"index"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason,omitempty"`
	}{{Index: 0, Message: struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}{Role: "assistant", Content: "summary text entities compliance rollout risk controls"}}}
	return resp, nil
}

func TestDocumentOrchestratorPrepare(t *testing.T) {
	o := New(Config{Enabled: true, DefaultChunkSize: 80, MaxDocuments: 4, MaxChunksPerDoc: 4, MaxLinkedSections: 8})
	up := &fakeUpstream{}
	req := model.ChatCompletionRequest{
		Model:    "mistral:7b",
		Messages: []model.Message{{Role: "user", Content: "Compare these docs"}},
		Documents: []model.DocumentInput{
			{ID: "a", Title: "Policy A", Text: strings.Repeat("Policy controls and compliance rollout. ", 20)},
			{ID: "b", Title: "Policy B", Text: strings.Repeat("Controls and risk plan with rollout steps. ", 20)},
		},
	}
	out, res, err := o.Prepare(context.Background(), up, req)
	if err != nil {
		t.Fatalf("prepare failed: %v", err)
	}
	if !res.Applied {
		t.Fatal("expected applied")
	}
	if res.DocumentCount != 2 {
		t.Fatalf("expected 2 docs, got %d", res.DocumentCount)
	}
	if res.ChunkCount == 0 {
		t.Fatal("expected chunk processing")
	}
	if len(out.Messages) <= len(req.Messages) || out.Messages[0].Role != "system" {
		t.Fatal("expected prepended orchestration context")
	}
	if !strings.Contains(out.Messages[0].Content, "document_orchestration") {
		t.Fatal("expected serialized context blob")
	}
	if up.calls < 3 {
		t.Fatalf("expected multiple summarization calls, got %d", up.calls)
	}
}

package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestChatCompletion(t *testing.T) {
	// Mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("Expected POST request, got %s", r.Method)
		}
		if r.Header.Get("Authorization") == "" {
			t.Errorf("Expected Authorization header")
		}

		resp := ChatResponse{
			Choices: []struct {
				Message Message `json:"message"`
			}{
				{
					Message: Message{Role: "assistant", Content: "<thought>Test thought</thought><answer>Test answer</answer>"},
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key")
	messages := []Message{
		{Role: "user", Content: "Hello"},
	}

	content, err := client.ChatCompletion("test-model", messages)
	if err != nil {
		t.Fatalf("ChatCompletion failed: %v", err)
	}

	expected := "<thought>Test thought</thought><answer>Test answer</answer>"
	if content != expected {
		t.Errorf("Expected content %q, got %q", expected, content)
	}
}

package cognition

import (
	"fmt"
	"strings"

	"github.com/mike/cognitive-llm/client"
)

type Engine struct {
	LLM   *client.Client
	Model string
}

func NewEngine(llm *client.Client, model string) *Engine {
	return &Engine{
		LLM:   llm,
		Model: model,
	}
}

func (e *Engine) Think(userInput string) (string, error) {
	fmt.Printf("\n--- Starting Cognition Process ---\n")
	fmt.Printf("User: %s\n", userInput)

	messages := []client.Message{
		{Role: "system", Content: "You are a cognitive reasoning engine. Think step-by-step before providing a final answer. Wrap your thoughts in <thought> tags and your final answer in <answer> tags."},
		{Role: "user", Content: userInput},
	}

	fmt.Println("Thinking...")
	resp, err := e.LLM.ChatCompletion(e.Model, messages)
	if err != nil {
		return "", err
	}

	// Simple extraction logic for now
	thought := extractTag(resp, "thought")
	answer := extractTag(resp, "answer")

	if thought != "" {
		fmt.Printf("\nThought Process:\n%s\n", thought)
	}

	if answer == "" {
		// If tags aren't used correctly, return the whole response as the answer
		answer = resp
	}

	fmt.Printf("\nFinal Answer:\n%s\n", answer)
	fmt.Printf("--- Cognition Process Completed ---\n")

	return answer, nil
}

func extractTag(text, tag string) string {
	startTag := fmt.Sprintf("<%s>", tag)
	endTag := fmt.Sprintf("</%s>", tag)

	start := strings.Index(text, startTag)
	if start == -1 {
		return ""
	}
	start += len(startTag)

	end := strings.Index(text[start:], endTag)
	if end == -1 {
		return text[start:]
	}

	return strings.TrimSpace(text[start : start+end])
}

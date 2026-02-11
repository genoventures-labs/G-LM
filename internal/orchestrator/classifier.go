package orchestrator

import "strings"

type TaskClass string

const (
	TaskGeneral                  TaskClass = "general"
	TaskCodingReasoning          TaskClass = "coding_reasoning"
	TaskLightQA                  TaskClass = "light_qa"
	TaskExtractionClassification TaskClass = "extraction_classification"
)

func ClassifyPrompt(text string) TaskClass {
	t := strings.ToLower(strings.TrimSpace(text))
	if t == "" {
		return TaskGeneral
	}

	codingMarkers := []string{
		"```", "stack trace", "stacktrace", "panic:", "exception", "compile", "compiler", "build failed", "refactor", "debug", "implement", "write a function", "fix bug", "unit test", "golang", "typescript", "python", "sql query",
	}
	for _, m := range codingMarkers {
		if strings.Contains(t, m) {
			return TaskCodingReasoning
		}
	}

	extractMarkers := []string{
		"extract", "classify", "label", "return json", "structured output", "regex", "fields", "entity", "categorize",
	}
	for _, m := range extractMarkers {
		if strings.Contains(t, m) {
			return TaskExtractionClassification
		}
	}

	// Short direct prompts are usually best on lightweight models.
	if len(t) <= 120 {
		questionWords := []string{"what", "who", "when", "where", "why", "how", "is ", "are ", "can ", "do ", "does "}
		for _, q := range questionWords {
			if strings.HasPrefix(t, q) || strings.Contains(t, "?") {
				return TaskLightQA
			}
		}
	}

	return TaskGeneral
}

func ClassifyMessages(messages []string) TaskClass {
	combined := strings.Join(messages, "\n")
	return ClassifyPrompt(combined)
}

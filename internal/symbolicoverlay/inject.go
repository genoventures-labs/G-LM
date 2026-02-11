package symbolicoverlay

import (
	"encoding/json"

	"github.com/mike/cognitive-llm/internal/model"
)

const overlayPromptPrefix = "Use this symbolic overlay for grounded, policy-consistent reasoning: "

func injectOverlay(req model.ChatCompletionRequest, artifact OverlayArtifact) (model.ChatCompletionRequest, error) {
	if hasOverlayMessage(req.Messages) {
		return req, nil
	}
	payload := map[string]any{
		"symbolic_overlay": map[string]any{
			"mode":  artifact.Mode,
			"types": artifact.Types,
		},
	}
	if len(artifact.LogicMap.Entities) > 0 || len(artifact.LogicMap.Links) > 0 {
		payload["logic_map"] = artifact.LogicMap
	}
	if len(artifact.ConstraintSet.Items) > 0 {
		payload["constraint_set"] = artifact.ConstraintSet
	}
	if len(artifact.RiskLens.Signals) > 0 {
		payload["risk_lens"] = artifact.RiskLens
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return req, err
	}
	msg := model.Message{Role: "system", Content: overlayPromptPrefix + string(b)}
	out := make([]model.Message, 0, len(req.Messages)+1)
	out = append(out, msg)
	out = append(out, req.Messages...)
	req.Messages = out
	return req, nil
}

func hasOverlayMessage(messages []model.Message) bool {
	for _, m := range messages {
		if m.Role == "system" && len(m.Content) >= len(overlayPromptPrefix) && m.Content[:len(overlayPromptPrefix)] == overlayPromptPrefix {
			return true
		}
	}
	return false
}

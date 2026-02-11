package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mike/cognitive-llm/internal/model"
	"github.com/mike/cognitive-llm/internal/toolcalling"
)

type toolAwareUpstream struct {
	server *Server
}

func (u toolAwareUpstream) ListModels(ctx context.Context) (model.ModelListResponse, error) {
	return u.server.upstream.ListModels(ctx)
}

func (u toolAwareUpstream) ChatCompletions(ctx context.Context, req model.ChatCompletionRequest) (model.ChatCompletionResponse, error) {
	resp, _, _, err := u.server.chatWithTools(ctx, req)
	return resp, err
}

func (s *Server) upstreamForRequest(req model.ChatCompletionRequest) upstream {
	if len(req.Tools) == 0 && !shouldAutoloadTools(req) {
		return s.upstream
	}
	return toolAwareUpstream{server: s}
}

func (s *Server) chatWithTools(ctx context.Context, req model.ChatCompletionRequest) (model.ChatCompletionResponse, int, int, error) {
	workingReq, err := s.prepareTools(ctx, req)
	if err != nil {
		return model.ChatCompletionResponse{}, 0, 0, err
	}
	if len(workingReq.Tools) == 0 {
		resp, err := s.upstream.ChatCompletions(ctx, workingReq)
		return resp, 0, 0, err
	}
	if s.tools == nil || !s.tools.Enabled() {
		return model.ChatCompletionResponse{}, 0, 0, fmt.Errorf("tool client is not configured; set GLM_TOOL_SERVER_BASE_URL, GLM_TOOL_SERVER_API_KEY, and GLM_TOOL_SERVER_CLIENT_ID")
	}
	maxIters := s.cfg.ToolCallingMaxIterations
	if maxIters <= 0 {
		maxIters = 4
	}
	working := workingReq
	totalCalls := 0
	var last model.ChatCompletionResponse
	for iter := 1; iter <= maxIters; iter++ {
		resp, err := s.upstream.ChatCompletions(ctx, working)
		if err != nil {
			return model.ChatCompletionResponse{}, totalCalls, iter - 1, err
		}
		last = resp
		if len(resp.Choices) == 0 {
			return resp, totalCalls, iter, nil
		}
		assistant := model.Message{
			Role:      strings.TrimSpace(resp.Choices[0].Message.Role),
			Content:   resp.Choices[0].Message.Content,
			Name:      resp.Choices[0].Message.Name,
			ToolCalls: append([]model.ToolCall{}, resp.Choices[0].Message.ToolCalls...),
		}
		if assistant.Role == "" {
			assistant.Role = "assistant"
		}
		if len(assistant.ToolCalls) == 0 {
			return resp, totalCalls, iter, nil
		}

		working.Messages = append(working.Messages, assistant)
		for idx, tc := range assistant.ToolCalls {
			toolID := strings.TrimSpace(tc.ID)
			if toolID == "" {
				toolID = fmt.Sprintf("call_%d_%d", iter, idx+1)
			}
			content := s.executeToolCall(ctx, tc)
			working.Messages = append(working.Messages, model.Message{
				Role:       "tool",
				Name:       tc.Function.Name,
				ToolCallID: toolID,
				Content:    content,
			})
			totalCalls++
		}
	}
	return last, totalCalls, maxIters, fmt.Errorf("tool calling max iterations exceeded")
}

func (s *Server) prepareTools(ctx context.Context, req model.ChatCompletionRequest) (model.ChatCompletionRequest, error) {
	if len(req.Tools) > 0 {
		return req, nil
	}
	if !shouldAutoloadTools(req) {
		return req, nil
	}
	if s.tools == nil || !s.tools.Enabled() {
		return req, fmt.Errorf("tool client is not configured for automatic tool discovery")
	}
	defs, err := s.tools.ToolDefinitions(ctx)
	if err != nil {
		return req, err
	}
	req.Tools = defs
	return req, nil
}

func shouldAutoloadTools(req model.ChatCompletionRequest) bool {
	if req.ToolChoice == nil {
		return false
	}
	s, ok := req.ToolChoice.(string)
	if ok {
		return strings.EqualFold(strings.TrimSpace(s), "auto")
	}
	m, ok := req.ToolChoice.(map[string]any)
	if !ok {
		return false
	}
	t, _ := m["type"].(string)
	return strings.EqualFold(strings.TrimSpace(t), "auto")
}

func (s *Server) executeToolCall(ctx context.Context, tc model.ToolCall) string {
	name := strings.TrimSpace(tc.Function.Name)
	payload := json.RawMessage(`{}`)
	if strings.TrimSpace(tc.Function.Arguments) != "" {
		payload = json.RawMessage(tc.Function.Arguments)
	}
	if !toolcalling.SupportedTool(name) {
		b, _ := json.Marshal(map[string]any{"error": "unsupported tool", "tool": name})
		return string(b)
	}
	out, err := s.tools.Call(ctx, name, payload)
	if err != nil {
		b, _ := json.Marshal(map[string]any{"error": err.Error(), "tool": name})
		return string(b)
	}
	if !json.Valid(out) {
		b, _ := json.Marshal(map[string]any{"tool": name, "raw": string(out)})
		return string(b)
	}
	return string(out)
}

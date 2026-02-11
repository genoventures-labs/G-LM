package toolcalling

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mike/cognitive-llm/internal/model"
)

type Client struct {
	baseURL     string
	apiKey      string
	clientID    string
	hardCeiling time.Duration
	http        *http.Client
	cacheTTL    time.Duration

	mu          sync.RWMutex
	cachedTools []model.ToolDefinition
	cachedAt    time.Time
}

type toolPolicy struct {
	timeout time.Duration
	retries int
}

type statusError struct {
	status int
	body   string
}

func (e *statusError) Error() string {
	return fmt.Sprintf("tool status %d: %s", e.status, strings.TrimSpace(e.body))
}

func New(baseURL, apiKey, clientID string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	if timeout > 60*time.Second {
		timeout = 60 * time.Second
	}
	return &Client{
		baseURL:     strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		apiKey:      strings.TrimSpace(apiKey),
		clientID:    strings.TrimSpace(clientID),
		hardCeiling: timeout,
		http:        &http.Client{Timeout: timeout + 5*time.Second},
		cacheTTL:    5 * time.Minute,
	}
}

func SupportedTool(name string) bool {
	switch strings.TrimSpace(name) {
	case "web_search", "fetch_url", "http_request", "vector_retrieve", "code_exec_sandbox":
		return true
	default:
		return false
	}
}

func (c *Client) Enabled() bool {
	return c != nil && c.baseURL != "" && c.apiKey != "" && c.clientID != ""
}

func (c *Client) ToolDefinitions(ctx context.Context) ([]model.ToolDefinition, error) {
	if c == nil || c.baseURL == "" {
		return nil, fmt.Errorf("tool client base url is not configured")
	}
	c.mu.RLock()
	if len(c.cachedTools) > 0 && time.Since(c.cachedAt) < c.cacheTTL {
		out := append([]model.ToolDefinition{}, c.cachedTools...)
		c.mu.RUnlock()
		return out, nil
	}
	c.mu.RUnlock()
	defs, err := c.fetchToolDefinitions(ctx)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.cachedTools = append([]model.ToolDefinition{}, defs...)
	c.cachedAt = time.Now().UTC()
	c.mu.Unlock()
	return defs, nil
}

func (c *Client) fetchToolDefinitions(ctx context.Context) ([]model.ToolDefinition, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/openapi.json", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("openapi fetch status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("openapi parse failed: %w", err)
	}
	pathsAny, ok := doc["paths"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("openapi missing paths")
	}
	defs := make([]model.ToolDefinition, 0, 8)
	for path, rawOp := range pathsAny {
		if !strings.HasPrefix(path, "/tools/") {
			continue
		}
		name := strings.TrimSpace(strings.TrimPrefix(path, "/tools/"))
		if !SupportedTool(name) {
			continue
		}
		opMap, ok := rawOp.(map[string]any)
		if !ok {
			continue
		}
		post, _ := opMap["post"].(map[string]any)
		desc := ""
		if s, ok := post["summary"].(string); ok {
			desc = strings.TrimSpace(s)
		}
		if desc == "" {
			if s, ok := post["description"].(string); ok {
				desc = strings.TrimSpace(s)
			}
		}
		params := any(map[string]any{"type": "object"})
		if rb, ok := post["requestBody"].(map[string]any); ok {
			if content, ok := rb["content"].(map[string]any); ok {
				if appJSON, ok := content["application/json"].(map[string]any); ok {
					if schema, exists := appJSON["schema"]; exists && schema != nil {
						params = schema
					}
				}
			}
		}
		defs = append(defs, model.ToolDefinition{
			Type: "function",
			Function: model.ToolFunction{
				Name:        name,
				Description: desc,
				Parameters:  params,
			},
		})
	}
	sort.Slice(defs, func(i, j int) bool {
		return defs[i].Function.Name < defs[j].Function.Name
	})
	if len(defs) == 0 {
		return nil, fmt.Errorf("no supported tools found in openapi")
	}
	return defs, nil
}

func (c *Client) Call(ctx context.Context, toolName string, payload json.RawMessage) (json.RawMessage, error) {
	toolName = strings.TrimSpace(toolName)
	if !SupportedTool(toolName) {
		return nil, fmt.Errorf("unsupported tool: %s", toolName)
	}
	if !c.Enabled() {
		return nil, fmt.Errorf("tool client is not configured")
	}
	policy := c.policyFor(toolName)
	var last error
	for attempt := 0; attempt <= policy.retries; attempt++ {
		out, err := c.callOnce(ctx, toolName, payload, policy.timeout)
		if err == nil {
			return out, nil
		}
		last = err
		if attempt >= policy.retries || !c.shouldRetry(toolName, err) {
			break
		}
		time.Sleep(c.retryBackoff(attempt))
	}
	if last == nil {
		last = fmt.Errorf("tool call failed")
	}
	return nil, last
}

func (c *Client) callOnce(ctx context.Context, toolName string, payload json.RawMessage, timeout time.Duration) (json.RawMessage, error) {
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	if c.hardCeiling > 0 && timeout > c.hardCeiling {
		timeout = c.hardCeiling
	}
	body := payload
	if len(bytes.TrimSpace(body)) == 0 {
		body = json.RawMessage(`{}`)
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodPost, c.baseURL+"/tools/"+toolName, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("X-Client-Id", c.clientID)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &statusError{status: resp.StatusCode, body: string(b)}
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return json.RawMessage(`{}`), nil
	}
	return json.RawMessage(b), nil
}

func (c *Client) policyFor(toolName string) toolPolicy {
	switch toolName {
	case "web_search":
		return toolPolicy{timeout: 12 * time.Second, retries: 2}
	case "fetch_url":
		return toolPolicy{timeout: 15 * time.Second, retries: 1}
	case "vector_retrieve":
		return toolPolicy{timeout: 20 * time.Second, retries: 1}
	case "http_request":
		return toolPolicy{timeout: 20 * time.Second, retries: 0}
	case "code_exec_sandbox":
		return toolPolicy{timeout: 45 * time.Second, retries: 0}
	default:
		return toolPolicy{timeout: 20 * time.Second, retries: 0}
	}
}

func (c *Client) shouldRetry(toolName string, err error) bool {
	if err == nil {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		switch toolName {
		case "web_search", "fetch_url", "vector_retrieve":
			return true
		default:
			return false
		}
	}
	var se *statusError
	if errors.As(err, &se) {
		switch toolName {
		case "web_search", "fetch_url":
			return se.status >= 500
		case "vector_retrieve":
			return se.status == 429 || se.status >= 500
		default:
			return false
		}
	}
	return false
}

func (c *Client) retryBackoff(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	base := time.Duration(attempt+1) * 150 * time.Millisecond
	jitter := time.Duration(rand.Intn(150)) * time.Millisecond
	return base + jitter
}

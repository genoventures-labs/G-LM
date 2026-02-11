package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

type ControlClient struct {
	baseURL  string
	apiKey   string
	http     *http.Client
	retryMax int
}

func New(baseURL, apiKey string, timeout time.Duration, retryMax int) *ControlClient {
	return &ControlClient{
		baseURL:  strings.TrimRight(baseURL, "/"),
		apiKey:   apiKey,
		http:     &http.Client{Timeout: timeout},
		retryMax: retryMax,
	}
}

func (c *ControlClient) ListModels(ctx context.Context) ([]string, error) {
	var out struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/api/tags", nil, &out); err != nil {
		return nil, err
	}
	models := make([]string, 0, len(out.Models))
	for _, m := range out.Models {
		if strings.TrimSpace(m.Name) != "" {
			models = append(models, m.Name)
		}
	}
	return models, nil
}

func (c *ControlClient) PullModel(ctx context.Context, model string) error {
	payload := map[string]any{"name": strings.TrimSpace(model), "stream": false}
	if strings.TrimSpace(model) == "" {
		return errors.New("model is required")
	}
	return c.retryJSON(ctx, http.MethodPost, "/api/pull", payload, nil)
}

func (c *ControlClient) DeleteModel(ctx context.Context, model string) error {
	payload := map[string]any{"name": strings.TrimSpace(model)}
	if strings.TrimSpace(model) == "" {
		return errors.New("model is required")
	}
	return c.retryJSON(ctx, http.MethodDelete, "/api/delete", payload, nil)
}

func (c *ControlClient) HostStats(ctx context.Context) (uint64, uint64, bool, error) {
	// Optional endpoint. If unavailable, caller falls back to max-model governor.
	var out struct {
		Disk struct {
			Used  uint64 `json:"used_bytes"`
			Total uint64 `json:"total_bytes"`
		} `json:"disk"`
	}
	err := c.doJSON(ctx, http.MethodGet, "/api/host/stats", nil, &out)
	if err != nil {
		return 0, 0, false, err
	}
	if out.Disk.Total == 0 {
		return 0, 0, false, nil
	}
	return out.Disk.Used, out.Disk.Total, true, nil
}

func (c *ControlClient) retryJSON(ctx context.Context, method, path string, payload any, out any) error {
	var last error
	for attempt := 0; attempt <= c.retryMax; attempt++ {
		err := c.doJSON(ctx, method, path, payload, out)
		if err == nil {
			return nil
		}
		last = err
		if !isTransient(err) {
			break
		}
		time.Sleep(time.Duration(attempt+1) * 200 * time.Millisecond)
	}
	if last == nil {
		return errors.New("request failed")
	}
	return last
}

func (c *ControlClient) doJSON(ctx context.Context, method, path string, payload any, out any) error {
	var body io.Reader
	if payload != nil {
		b, _ := json.Marshal(payload)
		body = bytes.NewReader(b)
	}
	req, _ := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ollama status %d: %s", resp.StatusCode, string(b))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func isTransient(err error) bool {
	if err == nil {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "ollama status 502") || strings.Contains(msg, "ollama status 503") || strings.Contains(msg, "ollama status 504")
}

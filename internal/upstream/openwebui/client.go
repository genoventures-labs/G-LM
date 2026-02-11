package openwebui

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

	"github.com/mike/cognitive-llm/internal/model"
)

type Client struct {
	baseURL  string
	apiKey   string
	http     *http.Client
	retryMax int
}

func New(baseURL, apiKey string, timeout time.Duration, retryMax int) *Client {
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey, http: &http.Client{Timeout: timeout}, retryMax: retryMax}
}

func (c *Client) ListModels(ctx context.Context) (model.ModelListResponse, error) {
	var out model.ModelListResponse
	err := c.doJSON(ctx, http.MethodGet, "/api/v1/models", nil, &out)
	if err != nil {
		return model.ModelListResponse{}, err
	}
	return out, nil
}

func (c *Client) ChatCompletions(ctx context.Context, req model.ChatCompletionRequest) (model.ChatCompletionResponse, error) {
	var out model.ChatCompletionResponse
	var last error
	for attempt := 0; attempt <= c.retryMax; attempt++ {
		err := c.doJSON(ctx, http.MethodPost, "/api/v1/chat/completions", req, &out)
		if err == nil {
			return out, nil
		}
		last = err
		if !isTransient(err) {
			break
		}
		time.Sleep(time.Duration(attempt+1) * 150 * time.Millisecond)
	}
	return model.ChatCompletionResponse{}, last
}

func (c *Client) doJSON(ctx context.Context, method, path string, payload any, out any) error {
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
		return fmt.Errorf("upstream status %d: %s", resp.StatusCode, string(b))
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
	return strings.Contains(msg, "upstream status 502") || strings.Contains(msg, "upstream status 503") || strings.Contains(msg, "upstream status 504")
}

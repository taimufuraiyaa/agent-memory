// Package agentmemory is the explicit local HTTP compatibility client.
package agentmemory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type Mode string

const ModeLocal Mode = "local"

type Config struct {
	Mode       Mode
	BaseURL    string
	HTTPClient *http.Client
}

type Client struct {
	mode       Mode
	baseURL    string
	httpClient *http.Client
}

type MemoryWrite struct {
	WorkspaceID string   `json:"workspace_id"`
	Type        string   `json:"type"`
	Content     string   `json:"content"`
	Keywords    []string `json:"keywords,omitempty"`
}

func New(config Config) (*Client, error) {
	config.BaseURL = strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	parsed, err := url.Parse(config.BaseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, errors.New("agent-memory base URL is invalid")
	}
	if config.Mode != ModeLocal {
		return nil, errors.New("agent-memory mode must be explicitly local")
	}
	if config.HTTPClient == nil {
		config.HTTPClient = http.DefaultClient
	}
	return &Client{mode: ModeLocal, baseURL: config.BaseURL, httpClient: config.HTTPClient}, nil
}

func (c *Client) Mode() Mode { return c.mode }

func (c *Client) WriteMemory(ctx context.Context, value MemoryWrite, idempotencyKey string) (json.RawMessage, error) {
	body := map[string]any{"workspace": value.WorkspaceID, "type": value.Type, "content": value.Content, "keywords": value.Keywords}
	return c.request(ctx, http.MethodPost, "/api/v1/memories/write", body, map[string]string{"Idempotency-Key": idempotencyKey})
}

func (c *Client) SearchLocal(ctx context.Context, workspace, query string, limit int) (json.RawMessage, error) {
	return c.request(ctx, http.MethodPost, "/api/v1/memories/search", map[string]any{"workspace": workspace, "query": query, "top_k": limit}, nil)
}

func (c *Client) request(ctx context.Context, method, path string, body any, headers map[string]string) (json.RawMessage, error) {
	var reader io.Reader
	if body != nil {
		if raw, ok := body.([]byte); ok {
			reader = bytes.NewReader(raw)
		} else {
			encoded, err := json.Marshal(body)
			if err != nil {
				return nil, err
			}
			reader = bytes.NewReader(encoded)
		}
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	encoded, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	var envelope struct {
		OK    bool            `json:"ok"`
		Data  json.RawMessage `json:"data"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(encoded, &envelope); err != nil {
		return nil, fmt.Errorf("agent-memory returned HTTP %d", response.StatusCode)
	}
	if response.StatusCode >= 400 || !envelope.OK {
		if envelope.Error != nil && envelope.Error.Message != "" {
			return nil, errors.New(envelope.Error.Message)
		}
		return nil, fmt.Errorf("agent-memory returned HTTP %d", response.StatusCode)
	}
	return envelope.Data, nil
}

// Package agentmemory is the explicit local/hosted HTTP compatibility client.
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

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/clientauth"
)

type Mode string

const (
	ModeLocal  Mode = "local"
	ModeHosted Mode = "hosted"
)

type TokenProvider interface {
	Token(context.Context) (string, error)
}

type OSKeyringTokenProvider struct{ Profile string }

func (p OSKeyringTokenProvider) Token(context.Context) (string, error) {
	profile := strings.TrimSpace(p.Profile)
	if profile == "" {
		profile = "default"
	}
	return (clientauth.OSKeyring{}).Get(profile)
}

type Config struct {
	Mode          Mode
	BaseURL       string
	TenantID      string
	TokenProvider TokenProvider
	HTTPClient    *http.Client
}

type Client struct {
	mode       Mode
	baseURL    string
	tenantID   string
	tokens     TokenProvider
	httpClient *http.Client
}

type MemoryWrite struct {
	WorkspaceID string   `json:"workspace_id"`
	Type        string   `json:"type"`
	Content     string   `json:"content"`
	Keywords    []string `json:"keywords,omitempty"`
}

type SourceQuery struct {
	SourceIDs []string `json:"source_ids"`
	Query     string   `json:"query"`
	Limit     int      `json:"limit,omitempty"`
	Provider  string   `json:"provider"`
	Model     string   `json:"model"`
}

func New(config Config) (*Client, error) {
	config.BaseURL = strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	parsed, err := url.Parse(config.BaseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, errors.New("agent-memory base URL is invalid")
	}
	if config.Mode != ModeLocal && config.Mode != ModeHosted {
		return nil, errors.New("agent-memory mode must be explicitly local or hosted")
	}
	if config.Mode == ModeHosted {
		if _, err := uuid.Parse(strings.TrimSpace(config.TenantID)); err != nil || config.TokenProvider == nil {
			return nil, errors.New("hosted mode requires tenant UUID and token provider")
		}
	}
	if config.HTTPClient == nil {
		config.HTTPClient = http.DefaultClient
	}
	return &Client{mode: config.Mode, baseURL: config.BaseURL, tenantID: strings.TrimSpace(config.TenantID), tokens: config.TokenProvider, httpClient: config.HTTPClient}, nil
}

func (c *Client) Mode() Mode { return c.mode }

func (c *Client) WriteMemory(ctx context.Context, value MemoryWrite, idempotencyKey string) (json.RawMessage, error) {
	path := "/api/v1/memories/write"
	body := any(map[string]any{"workspace": value.WorkspaceID, "type": value.Type, "content": value.Content, "keywords": value.Keywords})
	if c.mode == ModeHosted {
		path = "/v1/memories"
		body = map[string]any{"workspace_id": value.WorkspaceID, "type": value.Type, "content": value.Content, "keywords": value.Keywords, "source": map[string]any{"type": "user_input"}}
	}
	return c.request(ctx, http.MethodPost, path, body, map[string]string{"Idempotency-Key": idempotencyKey})
}

func (c *Client) SearchLocal(ctx context.Context, workspace, query string, limit int) (json.RawMessage, error) {
	if c.mode != ModeLocal {
		return nil, errors.New("SearchLocal is available only in explicit local mode")
	}
	return c.request(ctx, http.MethodPost, "/api/v1/memories/search", map[string]any{"workspace": workspace, "query": query, "top_k": limit}, nil)
}

func (c *Client) SearchHosted(ctx context.Context, workspace, query string, limit int, cursor string) (json.RawMessage, error) {
	if c.mode != ModeHosted {
		return nil, errors.New("SearchHosted is available only in explicit hosted mode")
	}
	return c.request(ctx, http.MethodPost, "/v1/search", map[string]any{
		"workspace_id": workspace,
		"query":        query,
		"limit":        limit,
		"cursor":       cursor,
	}, nil)
}

func (c *Client) QuerySources(ctx context.Context, value SourceQuery) (json.RawMessage, error) {
	if c.mode != ModeHosted {
		return nil, errors.New("QuerySources is available only in explicit hosted mode")
	}
	return c.request(ctx, http.MethodPost, "/v1/source-queries", value, nil)
}

// ImportPortable uploads only caller-supplied AMPB2 bytes. It never discovers,
// opens, or uploads a local SQLite database.
func (c *Client) ImportPortable(ctx context.Context, workspace, passphrase, idempotencyKey string, encrypted []byte) (json.RawMessage, error) {
	if c.mode != ModeHosted || len(encrypted) == 0 || len(encrypted) > 250<<20 || len(passphrase) < 12 {
		return nil, errors.New("portable import requires explicit hosted mode, bundle bytes, and passphrase")
	}
	return c.request(ctx, http.MethodPost, "/v1/imports", encrypted, map[string]string{"Content-Type": "application/octet-stream", "X-Agent-Memory-Workspace": workspace, "X-Agent-Memory-Bundle-Passphrase": passphrase, "Idempotency-Key": idempotencyKey})
}

func (c *Client) RevokeCurrentCredential(ctx context.Context) error {
	if c.mode != ModeHosted {
		return errors.New("credential revocation requires hosted mode")
	}
	_, err := c.request(ctx, http.MethodDelete, "/v1/current-credential", nil, nil)
	return err
}

func (c *Client) request(ctx context.Context, method, path string, body any, headers map[string]string) (json.RawMessage, error) {
	var reader io.Reader
	contentType := "application/json"
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
	request.Header.Set("Content-Type", contentType)
	if c.mode == ModeHosted {
		token, err := c.tokens.Token(ctx)
		if err != nil || strings.TrimSpace(token) == "" {
			return nil, errors.New("hosted token is unavailable")
		}
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("X-Agent-Memory-Tenant", c.tenantID)
	}
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

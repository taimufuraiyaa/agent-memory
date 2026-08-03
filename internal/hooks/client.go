// Package hooks normalizes and delivers bounded coding-agent lifecycle events.
package hooks

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/engine"
)

type Event struct {
	Workspace       string `json:"workspace"`
	SessionID       string `json:"session_id"`
	OccurredAt      string `json:"occurred_at"`
	Kind            string `json:"kind"`
	ToolName        string `json:"tool_name,omitempty"`
	Summary         string `json:"prompt,omitempty"`
	ProjectRoot     string `json:"project_root,omitempty"`
	CWD             string `json:"cwd,omitempty"`
	SourceAgent     string `json:"source_agent,omitempty"`
	SourceAdapter   string `json:"source_adapter,omitempty"`
	HookEvent       string `json:"hook_event,omitempty"`
	ExternalEventID string `json:"external_event_id,omitempty"`
	SchemaVersion   string `json:"schema_version,omitempty"`
	CaptureMode     string `json:"capture_mode,omitempty"`
}

type Config struct {
	ServiceURL      string
	Timeout         time.Duration
	Retries         int
	MaxSummaryBytes int
}

type Client struct {
	config Config
	http   *http.Client
}

type RecallResponse struct {
	RequestID    string `json:"request_id"`
	ContextBlock string `json:"context_block"`
	TokensUsed   int    `json:"tokens_used"`
	TokensBudget int    `json:"tokens_budget"`
	MemoriesUsed any    `json:"memories_used,omitempty"`
}

func NewClient(config Config) *Client {
	if config.Timeout <= 0 {
		config.Timeout = 750 * time.Millisecond
	}
	if config.Retries < 0 {
		config.Retries = 0
	}
	if config.MaxSummaryBytes <= 0 {
		config.MaxSummaryBytes = 1200
	}
	return &Client{config: config, http: &http.Client{Timeout: config.Timeout}}
}

func (c *Client) Recall(ctx context.Context, workspace, task string, budget int) (RecallResponse, error) {
	body, err := json.Marshal(map[string]any{"workspace": workspace, "task": task, "budget": budget, "include_observations": true})
	if err != nil {
		return RecallResponse{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.config.ServiceURL, "/")+"/api/v1/memories/recall", bytes.NewReader(body))
	if err != nil {
		return RecallResponse{}, err
	}
	request.Header.Set("content-type", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return RecallResponse{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return RecallResponse{}, fmt.Errorf("recall returned HTTP %d", response.StatusCode)
	}
	var envelope struct {
		OK    bool           `json:"ok"`
		Data  RecallResponse `json:"data"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		return RecallResponse{}, err
	}
	if !envelope.OK {
		if envelope.Error != nil {
			return RecallResponse{}, fmt.Errorf("%s", envelope.Error.Message)
		}
		return RecallResponse{}, fmt.Errorf("recall failed")
	}
	return envelope.Data, nil
}

func (c *Client) Deliver(ctx context.Context, event Event) error {
	event = c.normalize(event)
	body, err := json.Marshal(event)
	if err != nil {
		return err
	}
	var lastErr error
	for attempt := 0; attempt <= c.config.Retries; attempt++ {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.config.ServiceURL, "/")+"/api/v1/observe", bytes.NewReader(body))
		if err != nil {
			return err
		}
		request.Header.Set("content-type", "application/json")
		response, err := c.http.Do(request)
		if err == nil {
			response.Body.Close()
			if response.StatusCode >= 200 && response.StatusCode < 300 {
				return nil
			}
			err = fmt.Errorf("observe returned HTTP %d", response.StatusCode)
		}
		lastErr = err
		if attempt < c.config.Retries {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt+1) * 25 * time.Millisecond):
			}
		}
	}
	return lastErr
}

func (c *Client) normalize(event Event) Event {
	event.Summary = engine.RedactPrivateAndSecrets(strings.TrimSpace(event.Summary))
	if len(event.Summary) > c.config.MaxSummaryBytes {
		event.Summary = event.Summary[:c.config.MaxSummaryBytes]
	}
	if strings.TrimSpace(event.SchemaVersion) == "" {
		event.SchemaVersion = "v1"
	}
	if strings.TrimSpace(event.CaptureMode) == "" {
		event.CaptureMode = "live"
	}
	if strings.TrimSpace(event.ExternalEventID) == "" {
		sum := sha256.Sum256([]byte(strings.Join([]string{event.Workspace, event.SessionID, event.Kind, event.HookEvent, event.ToolName, event.Summary}, "\x00")))
		event.ExternalEventID = hex.EncodeToString(sum[:])
	}
	return event
}

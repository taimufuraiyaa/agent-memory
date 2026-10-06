package openaiapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	// MaxRespondInputBytes and MaxRespondOutputTokens bound one harness call.
	MaxRespondInputBytes   = 256 << 10
	MaxRespondOutputTokens = 4096
	maxRespondBody         = 1 << 20
)

// StatusError is an HTTP failure with no detail from the service: callers map the code to
// a typed outcome. The response body, which could echo request content, is never kept.
type StatusError struct{ Code int }

func (e StatusError) Error() string { return "OpenAI API returned an error status" }

// ErrUnavailable covers transport failures: no response was received.
var ErrUnavailable = errors.New("OpenAI API unavailable")

// Tool is one function the model may ask to have called. The caller runs nothing here: a call
// comes back as a name and arguments for the harness to prepare, policy to judge and, where it
// changes anything, a person to approve.
type Tool struct {
	Name        string
	Description string
	Parameters  map[string]any
}

// MaxToolArgumentBytes bounds the arguments of one call.
const MaxToolArgumentBytes = 16 << 10

// Request is one stateless request. There is no file, store or stream option; tools are
// offered only when listed.
type Request struct {
	Input           string
	MaxOutputTokens int
	Tools           []Tool
}

// Response is the validated result of one call. Incomplete means the output cap was hit.
type Response struct {
	Model             string
	Text              string
	Incomplete        bool
	InputTokens       int
	OutputTokens      int
	CachedInputTokens int
	// UsageReported is false when the service sent no usage, so a caller must estimate.
	UsageReported bool
	// ToolName and ToolArguments are the first function call the model made, if any.
	ToolName      string
	ToolArguments string
}

// NewHarnessClient builds a client for the coding harness. The per-call deadline is the
// caller's context; the transport timeout is only a backstop. Redirects are refused so the
// API key cannot be forwarded.
func NewHarnessClient(baseURL string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), http: &http.Client{
		Timeout:       timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return errors.New("OpenAI redirect refused") },
	}}
}

// BaseURL is the production endpoint, for callers that build a harness client.
func BaseURL() string { return defaultURL }

// ModelStatus checks that the key can use a model, reporting the HTTP status on failure.
func (c *Client) ModelStatus(ctx context.Context, key, model string) error {
	if !validModel(model) {
		return errors.New("invalid OpenAI model ID")
	}
	body, err := c.send(ctx, key, http.MethodGet, "/v1/models/"+url.PathEscape(model), nil, maxRespondBody)
	if err != nil {
		return err
	}
	var response struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(body, &response) != nil || response.ID != model {
		return errors.New("OpenAI model access could not be verified")
	}
	return nil
}

// Respond makes one non-streaming, non-stored request. The body sent carries exactly the
// model, the input, the output cap and the store and stream switches set off.
func (c *Client) Respond(ctx context.Context, key, model string, req Request) (Response, error) {
	if !validModel(model) || strings.TrimSpace(req.Input) == "" || len(req.Input) > MaxRespondInputBytes ||
		req.MaxOutputTokens < 1 || req.MaxOutputTokens > MaxRespondOutputTokens {
		return Response{}, errors.New("invalid OpenAI request bounds")
	}
	sent := map[string]any{"model": model, "input": req.Input, "max_output_tokens": req.MaxOutputTokens, "store": false, "stream": false}
	if len(req.Tools) > 0 {
		if len(req.Tools) > 64 {
			return Response{}, errors.New("invalid OpenAI request bounds")
		}
		tools := make([]map[string]any, len(req.Tools))
		for i, t := range req.Tools {
			if !toolNameRE.MatchString(t.Name) || t.Parameters == nil {
				return Response{}, errors.New("invalid OpenAI request bounds")
			}
			tools[i] = map[string]any{"type": "function", "name": t.Name, "description": t.Description, "parameters": t.Parameters, "strict": false}
		}
		sent["tools"], sent["tool_choice"], sent["parallel_tool_calls"] = tools, "auto", false
	}
	payload, _ := json.Marshal(sent)
	body, err := c.send(ctx, key, http.MethodPost, "/v1/responses", payload, maxRespondBody)
	if err != nil {
		return Response{}, err
	}
	var raw struct {
		Status            string `json:"status"`
		Model             string `json:"model"`
		OutputText        string `json:"output_text"`
		IncompleteDetails *struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
		Output []struct {
			Type      string `json:"type"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
			Content   []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
		Usage *struct {
			InputTokens        int `json:"input_tokens"`
			OutputTokens       int `json:"output_tokens"`
			InputTokensDetails *struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"input_tokens_details"`
		} `json:"usage"`
	}
	if json.Unmarshal(body, &raw) != nil {
		return Response{}, errors.New("invalid OpenAI response")
	}
	// An alias such as "gpt-x" may be served by a dated snapshot "gpt-x-2025-01-01".
	if raw.Model != model && !strings.HasPrefix(raw.Model, model+"-") {
		return Response{}, errors.New("OpenAI response is for a different model")
	}
	incomplete := false
	switch raw.Status {
	case "completed":
	case "incomplete":
		incomplete = true
	default:
		return Response{}, errors.New("OpenAI response is not complete")
	}
	text := raw.OutputText
	if text == "" {
		for _, item := range raw.Output {
			for _, part := range item.Content {
				if part.Type == "output_text" {
					text += part.Text
				}
			}
		}
	}
	toolName, toolArgs := "", ""
	for _, item := range raw.Output {
		if item.Type == "function_call" {
			if !toolNameRE.MatchString(item.Name) || len(item.Arguments) > MaxToolArgumentBytes || !json.Valid([]byte(item.Arguments)) {
				return Response{}, errors.New("OpenAI function call is invalid")
			}
			toolName, toolArgs = item.Name, item.Arguments
			break
		}
	}
	if strings.TrimSpace(text) == "" && toolName == "" {
		return Response{}, errors.New("OpenAI response text is empty")
	}
	result := Response{Model: raw.Model, Text: text, Incomplete: incomplete, ToolName: toolName, ToolArguments: toolArgs}
	if raw.Usage != nil {
		cached := 0
		if raw.Usage.InputTokensDetails != nil {
			cached = raw.Usage.InputTokensDetails.CachedTokens
		}
		if raw.Usage.InputTokens < 0 || raw.Usage.OutputTokens < 0 || cached < 0 || cached > raw.Usage.InputTokens {
			return Response{}, errors.New("invalid OpenAI usage")
		}
		result.InputTokens, result.OutputTokens, result.CachedInputTokens, result.UsageReported = raw.Usage.InputTokens, raw.Usage.OutputTokens, cached, true
	}
	return result, nil
}

func (c *Client) send(ctx context.Context, key, method, path string, payload []byte, limit int64) ([]byte, error) {
	if key == "" || len(key) > 4096 || strings.ContainsAny(key, "\r\n") {
		return nil, errors.New("invalid OpenAI API key")
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return nil, errors.New("cannot create OpenAI request")
	}
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("Accept", "application/json")
	if method == http.MethodPost {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, StatusError{Code: response.StatusCode}
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrUnavailable
	}
	if int64(len(content)) > limit {
		return nil, errors.New("OpenAI response is too large")
	}
	return content, nil
}

var toolNameRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)

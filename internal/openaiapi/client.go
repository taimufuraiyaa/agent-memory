// Package openaiapi owns the OpenAI Responses transport. It does not choose
// models, approve egress, run tools, or mutate ChatGPT app conversations.
package openaiapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultURL = "https://api.openai.com"

type Client struct {
	baseURL string
	http    *http.Client
}

type Result struct {
	Model        string
	Text         string
	InputTokens  int
	OutputTokens int
}

func NewClient() *Client { return NewClientWithURL(defaultURL) }

// NewClientWithURL is for local fake HTTP tests; production uses NewClient.
func NewClientWithURL(baseURL string) *Client {
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), http: &http.Client{
		Timeout:       20 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return errors.New("OpenAI redirect refused") },
	}}
}

func (c *Client) ProbeModel(ctx context.Context, key, model string) error {
	if !validModel(model) {
		return errors.New("invalid OpenAI model ID")
	}
	body, err := c.call(ctx, key, http.MethodGet, "/v1/models/"+url.PathEscape(model), nil)
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

func (c *Client) Generate(ctx context.Context, key, model, input string, maxOutputTokens int) (Result, error) {
	if !validModel(model) || strings.TrimSpace(input) == "" || len(input) > 4000 || maxOutputTokens < 32 || maxOutputTokens > 1024 {
		return Result{}, errors.New("invalid OpenAI request bounds")
	}
	request, _ := json.Marshal(map[string]any{
		"model": model, "input": input, "max_output_tokens": maxOutputTokens,
		"store": false, "stream": false,
	})
	body, err := c.call(ctx, key, http.MethodPost, "/v1/responses", request)
	if err != nil {
		return Result{}, err
	}
	var response struct {
		Status     string `json:"status"`
		Model      string `json:"model"`
		OutputText string `json:"output_text"`
		Output     []struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
		Usage *struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(body, &response) != nil || response.Status != "completed" || response.Model != model {
		return Result{}, errors.New("invalid or incomplete OpenAI response")
	}
	output := response.OutputText
	if output == "" {
		for _, item := range response.Output {
			for _, part := range item.Content {
				if part.Type == "output_text" {
					output += part.Text
				}
			}
		}
	}
	if strings.TrimSpace(output) == "" || len(output) > 8192 {
		return Result{}, errors.New("OpenAI response text is empty or too large")
	}
	result := Result{Model: response.Model, Text: output}
	if response.Usage != nil {
		if response.Usage.InputTokens < 0 || response.Usage.OutputTokens < 0 {
			return Result{}, errors.New("invalid OpenAI usage")
		}
		result.InputTokens, result.OutputTokens = response.Usage.InputTokens, response.Usage.OutputTokens
	}
	return result, nil
}

func (c *Client) call(ctx context.Context, key, method, path string, payload []byte) ([]byte, error) {
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
		return nil, errors.New("OpenAI API unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, errors.New("OpenAI API denied or failed")
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	if err != nil || len(content) > 65536 {
		return nil, errors.New("OpenAI response is too large")
	}
	return content, nil
}

func validModel(model string) bool {
	if model == "" || len(model) > 100 {
		return false
	}
	for _, char := range model {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || strings.ContainsRune("-_.:/", char)) {
			return false
		}
	}
	return true
}

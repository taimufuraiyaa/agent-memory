// Package jev contains the TypeSafe-specific System One transport.
// Shared policy and action authority remain outside this provider leaf.
package jev

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
)

const defaultURL = "https://api.typesafe.ai"
const model = "jev-latest"

type ChoiceQuestion struct {
	Instructions string            `json:"instructions"`
	Criteria     map[string]string `json:"criteria"`
}

type ChoiceAnswer struct {
	Choice        string             `json:"choice"`
	Confidence    float64            `json:"confidence"`
	Probabilities map[string]float64 `json:"probabilities"`
}

type Client struct {
	baseURL string
	http    *http.Client
}

func NewClient() *Client { return NewClientWithURL(defaultURL) }

// NewClientWithURL permits local fake-provider conformance tests. Production
// composition uses NewClient's fixed TypeSafe endpoint.
func NewClientWithURL(baseURL string) *Client {
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), http: &http.Client{
		Timeout:       1800 * time.Millisecond,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return errors.New("Jev redirect refused") },
	}}
}

func (c *Client) Probe(ctx context.Context, token string) error {
	body, err := c.call(ctx, token, http.MethodGet, "/v1/models", nil)
	if err != nil {
		return err
	}
	var response struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if json.Unmarshal(body, &response) != nil {
		return errors.New("invalid Jev model response")
	}
	for _, item := range response.Models {
		if item.Name == model {
			return nil
		}
	}
	return errors.New("Jev model is unavailable")
}

func (c *Client) Choose(ctx context.Context, token, state string, questions map[string]ChoiceQuestion) (map[string]ChoiceAnswer, error) {
	if len(state) == 0 || len(state) > 4000 || len(questions) == 0 || len(questions) > 2 {
		return nil, errors.New("invalid Jev decision bounds")
	}
	typed := make(map[string]any, len(questions))
	for id, question := range questions {
		if !validID(id) || len(question.Instructions) > 300 || len(question.Criteria) < 2 || len(question.Criteria) > 32 {
			return nil, errors.New("invalid Jev choice question")
		}
		for key, description := range question.Criteria {
			if !validID(key) || len(description) > 250 {
				return nil, errors.New("invalid Jev choice candidate")
			}
		}
		typed[id] = map[string]any{"type": "choice", "instructions": question.Instructions, "criteria": question.Criteria}
	}
	request, _ := json.Marshal(map[string]any{"model": model, "state": state, "questions": typed})
	if len(request) > 10000 {
		return nil, errors.New("Jev decision request is too large")
	}
	body, err := c.call(ctx, token, http.MethodPost, "/v1/systemone", request)
	if err != nil {
		return nil, err
	}
	var response struct {
		Model   string `json:"model"`
		Answers map[string]struct {
			Type string `json:"type"`
			ChoiceAnswer
		} `json:"answers"`
		Usage *struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(body, &response) != nil || !strings.HasPrefix(response.Model, "jev-") || response.Usage == nil || response.Usage.InputTokens < 0 || response.Usage.OutputTokens < 0 || len(response.Answers) != len(questions) {
		return nil, errors.New("invalid Jev decision response")
	}
	answers := make(map[string]ChoiceAnswer, len(questions))
	for id, question := range questions {
		answer, ok := response.Answers[id]
		if !ok || answer.Type != "choice" || !validProbability(answer.Confidence) || len(answer.Probabilities) != len(question.Criteria) {
			return nil, errors.New("invalid Jev choice answer")
		}
		if _, ok := question.Criteria[answer.Choice]; !ok {
			return nil, errors.New("unknown Jev choice")
		}
		var sum, maximum float64
		for candidate, probability := range answer.Probabilities {
			if _, ok := question.Criteria[candidate]; !ok || !validProbability(probability) {
				return nil, errors.New("invalid Jev choice probabilities")
			}
			sum += probability
			maximum = math.Max(maximum, probability)
		}
		if math.Abs(sum-1) > 0.05 || answer.Probabilities[answer.Choice] < maximum-0.01 {
			return nil, errors.New("inconsistent Jev choice probabilities")
		}
		answers[id] = answer.ChoiceAnswer
	}
	return answers, nil
}

func (c *Client) call(ctx context.Context, token, method, path string, body []byte) ([]byte, error) {
	if token == "" || len(token) > 4096 || strings.ContainsAny(token, "\r\n") {
		return nil, errors.New("invalid Jev credential")
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("cannot create Jev request")
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")
	if method == http.MethodPost {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return nil, errors.New("Jev service unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, errors.New("Jev service denied or failed")
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	if err != nil || len(content) > 65536 {
		return nil, errors.New("invalid Jev response size")
	}
	return content, nil
}

func validProbability(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= 1
}

func validID(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, char := range value {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '_' || char == '-') {
			return false
		}
	}
	return true
}

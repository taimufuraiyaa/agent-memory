package modelgateway

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

const maxProviderResponseBytes = 8 << 20

var errProviderRedirect = errors.New("model provider redirects are disabled")

type HTTPProviderConfig struct {
	Name      string
	Endpoint  string
	APIKey    string
	Model     string
	Dimension int
	Retention string
	Client    *http.Client
}

// HTTPProvider speaks the bounded OpenAI-compatible subset used by the hosted
// model route. It deliberately returns no upstream response body in errors.
type HTTPProvider struct {
	name      string
	endpoint  string
	apiKey    string
	model     string
	dimension int
	retention string
	client    *http.Client
}

func NewHTTPProvider(config HTTPProviderConfig) (*HTTPProvider, error) {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(config.Endpoint), "/"))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, errors.New("model provider endpoint must be HTTP or HTTPS")
	}
	if strings.TrimSpace(config.Name) == "" || strings.TrimSpace(config.APIKey) == "" || strings.TrimSpace(config.Model) == "" || config.Dimension <= 0 || strings.TrimSpace(config.Retention) == "" {
		return nil, errors.New("model provider name, key, model, positive dimension, and retention policy are required")
	}
	client := config.Client
	if client == nil {
		client = &http.Client{}
	} else {
		clone := *client
		client = &clone
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return errProviderRedirect }
	return &HTTPProvider{
		name: strings.TrimSpace(config.Name), endpoint: strings.TrimRight(parsed.String(), "/"), apiKey: config.APIKey,
		model: strings.TrimSpace(config.Model), dimension: config.Dimension, retention: strings.TrimSpace(config.Retention), client: client,
	}, nil
}

func (p *HTTPProvider) Name() string            { return p.name }
func (p *HTTPProvider) ModelVersion() string    { return p.model }
func (p *HTTPProvider) RetentionPolicy() string { return p.retention }
func (p *HTTPProvider) Dimension() int          { return p.dimension }

func (p *HTTPProvider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, errors.New("model provider embedding input is empty")
	}
	payload := struct {
		Model string   `json:"model"`
		Input []string `json:"input"`
	}{Model: p.model, Input: texts}
	var result struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := p.post(ctx, "/v1/embeddings", payload, &result); err != nil {
		return nil, err
	}
	if len(result.Data) != len(texts) {
		return nil, errors.New("model provider returned an invalid embedding count")
	}
	vectors := make([][]float32, len(texts))
	for _, item := range result.Data {
		if item.Index < 0 || item.Index >= len(vectors) || vectors[item.Index] != nil || len(item.Embedding) != p.dimension {
			return nil, errors.New("model provider returned an invalid embedding result")
		}
		vectors[item.Index] = item.Embedding
	}
	return vectors, nil
}

func (p *HTTPProvider) Generate(ctx context.Context, prompt string) (string, error) {
	payload := struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}{Model: p.model, Messages: []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}{{Role: "user", Content: prompt}}}
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := p.post(ctx, "/v1/chat/completions", payload, &result); err != nil {
		return "", err
	}
	if len(result.Choices) != 1 || strings.TrimSpace(result.Choices[0].Message.Content) == "" {
		return "", errors.New("model provider returned an invalid generation result")
	}
	return strings.TrimSpace(result.Choices[0].Message.Content), nil
}

func (p *HTTPProvider) post(ctx context.Context, path string, payload, destination any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return errors.New("encode model provider request")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint+path, bytes.NewReader(body))
	if err != nil {
		return errors.New("create model provider request")
	}
	request.Header.Set("Authorization", "Bearer "+p.apiKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := p.client.Do(request)
	if err != nil {
		if errors.Is(err, errProviderRedirect) {
			return errProviderRedirect
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return Temporary(errors.New("model provider request failed"))
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if response.StatusCode == http.StatusRequestTimeout || response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
			return Temporary(fmt.Errorf("model provider unavailable with status %d", response.StatusCode))
		}
		return fmt.Errorf("model provider rejected request with status %d", response.StatusCode)
	}
	limited, err := io.ReadAll(io.LimitReader(response.Body, maxProviderResponseBytes+1))
	if err != nil {
		return Temporary(errors.New("read model provider response"))
	}
	if len(limited) > maxProviderResponseBytes {
		return errors.New("model provider response exceeds size limit")
	}
	if err := json.Unmarshal(limited, destination); err != nil {
		return errors.New("model provider returned malformed JSON")
	}
	return nil
}

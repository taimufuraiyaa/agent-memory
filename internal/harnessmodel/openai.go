package harnessmodel

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
	"github.com/taimufuraiyaa/agent-memory/internal/openaiapi"
)

const (
	OpenAIProviderID  harness.ProviderID   = "openai-responses"
	OpenAICapability  harness.CapabilityID = "generation"
	maxOpenAIModelLen                      = 100
)

// OpenAIConfig describes one OpenAI model served as a harness provider.
type OpenAIConfig struct {
	Model string
	// APIKey supplies the key at call time so it is never stored in configuration.
	APIKey func() string
	// BaseURL is empty for the production endpoint; tests pass a local server.
	BaseURL string
	Pricing Pricing
	Timeout time.Duration
	// AllowEgress must be true. The adapter sends the assembled prompt to a third party,
	// so it refuses to exist unless the operator has opted in.
	AllowEgress bool
}

// OpenAIManifest is the static declaration; it is not proof the account can use the model.
func OpenAIManifest() harness.Manifest {
	return harness.Manifest{Version: harness.ContractVersion, ID: OpenAIProviderID, Kind: harness.KindModel, Capabilities: []harness.CapabilityID{OpenAICapability}}
}

// OpenAIProvider serves harness model calls through the OpenAI Responses API, text only:
// no tools, no stored conversations, no streaming. Tool calling is not offered to the
// model, so the loop can only receive text from it.
type OpenAIProvider struct {
	cfg    OpenAIConfig
	client *openaiapi.Client
}

func NewOpenAIProvider(cfg OpenAIConfig) (*OpenAIProvider, error) {
	if !cfg.AllowEgress {
		return nil, fmt.Errorf("%w: sending prompts to OpenAI requires explicit opt-in", ErrInvalid)
	}
	if cfg.APIKey == nil || cfg.Model == "" || len(cfg.Model) > maxOpenAIModelLen || strings.ContainsAny(cfg.Model, " \t\r\n") {
		return nil, fmt.Errorf("%w: model and key supplier are required", ErrInvalid)
	}
	if err := cfg.Pricing.Validate(); err != nil {
		return nil, err
	}
	base := cfg.BaseURL
	if base == "" {
		base = openaiapi.BaseURL()
	}
	return &OpenAIProvider{cfg: cfg, client: openaiapi.NewHarnessClient(base, cfg.Timeout)}, nil
}

// Register adds the provider to a registry. Each session gets its own handle on the
// shared client.
func (p *OpenAIProvider) Register(registry *harness.Registry) error {
	return registry.Register(OpenAIManifest(), func() (harness.Provider, error) { return &openAISession{provider: p}, nil })
}

type openAISession struct {
	provider *OpenAIProvider
	closed   atomic.Bool
}

func (s *openAISession) Close() error {
	s.closed.Store(true)
	return nil
}

// Probe asks the service whether this key can use the model. It sends no user data. A
// missing key is denied without any network call.
func (s *openAISession) Probe(ctx context.Context, scope harness.Scope) (harness.LiveAccess, error) {
	access := harness.LiveAccess{Version: harness.ContractVersion, Provider: OpenAIProviderID, Scope: scope, Revision: 1,
		Capabilities: map[harness.CapabilityID]harness.AccessState{OpenAICapability: harness.AccessUnavailable}}
	key := s.provider.cfg.APIKey()
	if key == "" {
		access.Capabilities[OpenAICapability] = harness.AccessDenied
		return access, nil
	}
	err := s.provider.client.ModelStatus(ctx, key, s.provider.cfg.Model)
	switch {
	case err == nil:
		access.Capabilities[OpenAICapability] = harness.AccessAvailable
	case ctx.Err() != nil:
		return harness.LiveAccess{}, ctx.Err()
	default:
		access.Capabilities[OpenAICapability] = accessFor(err)
	}
	return access, nil
}

func accessFor(err error) harness.AccessState {
	var status openaiapi.StatusError
	if errors.As(err, &status) {
		switch status.Code {
		case http.StatusUnauthorized, http.StatusForbidden:
			return harness.AccessDenied
		case http.StatusNotFound:
			return harness.AccessUnsupported
		}
	}
	return harness.AccessUnavailable
}

// outcomeFor maps a failed call to a typed outcome without carrying any service text.
func outcomeFor(ctx context.Context, err error) harness.Outcome {
	switch {
	case errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled):
		return harness.OutcomeCancelled
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded):
		return harness.OutcomeTimeout
	case errors.Is(err, openaiapi.ErrUnavailable):
		return harness.OutcomeUnavailable
	}
	var status openaiapi.StatusError
	if errors.As(err, &status) {
		switch {
		case status.Code == http.StatusUnauthorized || status.Code == http.StatusForbidden:
			return harness.OutcomeDenied
		case status.Code == http.StatusNotFound:
			return harness.OutcomeUnsupported
		case status.Code == http.StatusRequestTimeout || status.Code == http.StatusTooManyRequests || status.Code >= 500:
			return harness.OutcomeUnavailable
		}
	}
	return harness.OutcomeFailed
}

// Generate sends the assembled prompt and nothing else from the run. It always returns a
// typed answer: provider and transport problems become outcomes, and neither the key, the
// prompt nor any service text ever appears in one.
func (s *openAISession) Generate(ctx context.Context, req harness.ModelRequest) (harness.ModelAnswer, error) {
	if s.closed.Load() {
		return harness.ModelAnswer{}, errors.New("session is closed")
	}
	answer := harness.ModelAnswer{Envelope: req.Envelope}
	if strings.TrimSpace(req.Prompt) == "" {
		answer.Outcome = harness.OutcomeUnsupported
		return answer, nil
	}
	key := s.provider.cfg.APIKey()
	if key == "" {
		answer.Outcome = harness.OutcomeDenied
		return answer, nil
	}
	maxTokens := req.MaxOutputTokens
	if maxTokens > openaiapi.MaxRespondOutputTokens {
		maxTokens = openaiapi.MaxRespondOutputTokens
	}
	response, err := s.provider.client.Respond(ctx, key, s.provider.cfg.Model, openaiapi.Request{Input: req.Prompt, MaxOutputTokens: maxTokens})
	if err != nil {
		answer.Outcome = outcomeFor(ctx, err)
		return answer, nil
	}
	usage := harness.Usage{InputTokens: response.InputTokens, OutputTokens: response.OutputTokens, CachedInputTokens: response.CachedInputTokens}
	if !response.UsageReported {
		// No usage was reported, so estimate from sizes rather than treating the call as free.
		usage = harness.Usage{InputTokens: (len(req.Prompt) + 3) / 4, OutputTokens: (len(response.Text) + 3) / 4}
	}
	text, cut := boundText(response.Text, req.MaxBytes)
	answer.Text, answer.Usage, answer.Model = text, usage, response.Model
	answer.CostMicros = s.provider.cfg.Pricing.Cost(usage)
	answer.Outcome = harness.OutcomeOK
	if response.Incomplete || cut {
		answer.Outcome = harness.OutcomePartial
	}
	return answer, nil
}

// boundText cuts text to at most max bytes on a rune boundary.
func boundText(text string, max int) (string, bool) {
	if len(text) <= max {
		return text, false
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut], true
}

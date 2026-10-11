package harnessmodel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
)

const (
	AnthropicProviderID harness.ProviderID   = "anthropic-messages"
	AnthropicCapability harness.CapabilityID = "generation"

	maxAnthropicOutputTokens = 16000
	maxAnthropicToolArgBytes = 16 << 10
)

var anthropicToolNameRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)

// ToolSpec describes one tool a model may be offered. The model only asks for a call; the
// harness prepares it, policy judges it and, where it changes anything, a person approves it.
type ToolSpec struct {
	Name        string
	Description string
	Parameters  map[string]any
}

// AnthropicConfig describes one Claude model served as a harness provider.
type AnthropicConfig struct {
	Model string
	// APIKey supplies the key at call time so it is never stored in configuration.
	APIKey func() string
	// BaseURL is empty for the production endpoint; tests pass a local server.
	BaseURL string
	Pricing Pricing
	Timeout time.Duration
	// Tools describes the tools a call may offer, by identifier; one it does not know is not
	// offered. Without it the model is text-only.
	Tools func(ids []string) []ToolSpec
	// AllowEgress must be true: the assembled prompt goes to a third party.
	AllowEgress bool
}

// AnthropicManifest is the static declaration; it is not proof the account can use the model.
func AnthropicManifest() harness.Manifest {
	return harness.Manifest{Version: harness.ContractVersion, ID: AnthropicProviderID, Kind: harness.KindModel, Capabilities: []harness.CapabilityID{AnthropicCapability}}
}

// AnthropicProvider serves harness model calls through the Anthropic Messages API: stateless,
// non-streaming, no retries, no redirects, no forced tool choice and no sampling or thinking
// parameters, so each model runs its own defaults.
type AnthropicProvider struct{ cfg AnthropicConfig }

func NewAnthropicProvider(cfg AnthropicConfig) (*AnthropicProvider, error) {
	if !cfg.AllowEgress {
		return nil, fmt.Errorf("%w: sending prompts to Anthropic requires explicit opt-in", ErrInvalid)
	}
	if cfg.APIKey == nil || cfg.Model == "" || len(cfg.Model) > maxOpenAIModelLen || strings.ContainsAny(cfg.Model, " \t\r\n") {
		return nil, fmt.Errorf("%w: model and key supplier are required", ErrInvalid)
	}
	if err := cfg.Pricing.Validate(); err != nil {
		return nil, err
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Minute
	}
	return &AnthropicProvider{cfg: cfg}, nil
}

// Register adds the provider to a registry.
func (p *AnthropicProvider) Register(registry *harness.Registry) error {
	return registry.Register(AnthropicManifest(), func() (harness.Provider, error) { return &anthropicSession{provider: p}, nil })
}

// client builds a client for one call with the key as it is now. Retries are off and a
// redirect is refused, so the key can never be sent twice or forwarded.
func (p *AnthropicProvider) client(key string) anthropic.Client {
	opts := []option.RequestOption{
		option.WithAPIKey(key),
		option.WithMaxRetries(0),
		option.WithHTTPClient(&http.Client{Timeout: p.cfg.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect refused") }}),
	}
	if p.cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(p.cfg.BaseURL))
	}
	return anthropic.NewClient(opts...)
}

type anthropicSession struct {
	provider *AnthropicProvider
	closed   atomic.Bool
}

func (s *anthropicSession) Close() error { s.closed.Store(true); return nil }

// Probe asks whether the key can see the model, sending no user data. A missing key is denied
// without any network call.
func (s *anthropicSession) Probe(ctx context.Context, scope harness.Scope) (harness.LiveAccess, error) {
	access := harness.LiveAccess{Version: harness.ContractVersion, Provider: AnthropicProviderID, Scope: scope, Revision: 1,
		Capabilities: map[harness.CapabilityID]harness.AccessState{AnthropicCapability: harness.AccessUnavailable}}
	key := s.provider.cfg.APIKey()
	if key == "" {
		access.Capabilities[AnthropicCapability] = harness.AccessDenied
		return access, nil
	}
	client := s.provider.client(key)
	_, err := client.Models.Get(ctx, s.provider.cfg.Model, anthropic.ModelGetParams{})
	switch {
	case err == nil:
		access.Capabilities[AnthropicCapability] = harness.AccessAvailable
	case ctx.Err() != nil:
		return harness.LiveAccess{}, ctx.Err()
	default:
		access.Capabilities[AnthropicCapability] = anthropicAccess(err)
	}
	return access, nil
}

func statusOf(err error) int {
	var apiErr *anthropic.Error
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode
	}
	return 0
}

func anthropicAccess(err error) harness.AccessState {
	switch statusOf(err) {
	case http.StatusUnauthorized, http.StatusForbidden:
		return harness.AccessDenied
	case http.StatusNotFound:
		return harness.AccessUnsupported
	}
	return harness.AccessUnavailable
}

// anthropicOutcome maps a failed call to a typed outcome without carrying any service text.
func anthropicOutcome(ctx context.Context, err error) harness.Outcome {
	switch {
	case errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled):
		return harness.OutcomeCancelled
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded):
		return harness.OutcomeTimeout
	}
	switch code := statusOf(err); {
	case code == 0:
		return harness.OutcomeUnavailable // no response was received
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return harness.OutcomeDenied
	case code == http.StatusNotFound:
		return harness.OutcomeUnsupported
	case code == http.StatusRequestTimeout || code == http.StatusTooManyRequests || code >= 500:
		return harness.OutcomeUnavailable
	}
	return harness.OutcomeFailed
}

func toolParams(specs []ToolSpec) ([]anthropic.ToolUnionParam, bool) {
	out := make([]anthropic.ToolUnionParam, 0, len(specs))
	for _, spec := range specs {
		if !anthropicToolNameRE.MatchString(spec.Name) || spec.Parameters == nil {
			return nil, false
		}
		schema := anthropic.ToolInputSchemaParam{Properties: spec.Parameters["properties"], ExtraFields: map[string]any{"additionalProperties": false}}
		switch required := spec.Parameters["required"].(type) {
		case []string:
			schema.Required = required
		case []any:
			for _, r := range required {
				if name, ok := r.(string); ok {
					schema.Required = append(schema.Required, name)
				}
			}
		}
		tool := anthropic.ToolParam{Name: spec.Name, InputSchema: schema}
		if spec.Description != "" {
			tool.Description = anthropic.String(spec.Description)
		}
		out = append(out, anthropic.ToolUnionParam{OfTool: &tool})
	}
	return out, true
}

// Generate sends the assembled prompt and nothing else from the run. It always returns a
// typed answer: provider and transport problems become outcomes, and neither the key, the
// prompt nor any service text ever appears in one.
func (s *anthropicSession) Generate(ctx context.Context, req harness.ModelRequest) (harness.ModelAnswer, error) {
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
	maxTokens := min(req.MaxOutputTokens, maxAnthropicOutputTokens)
	if maxTokens < 1 {
		answer.Outcome = harness.OutcomeFailed
		return answer, nil
	}
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(s.provider.cfg.Model),
		MaxTokens: int64(maxTokens),
		Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(req.Prompt))},
	}
	var offered []ToolSpec
	if s.provider.cfg.Tools != nil && len(req.ToolSchemaIDs) > 0 {
		offered = s.provider.cfg.Tools(req.ToolSchemaIDs)
	}
	if len(offered) > 0 {
		tools, ok := toolParams(offered)
		if !ok || len(offered) > 64 {
			answer.Outcome = harness.OutcomeFailed
			return answer, nil
		}
		params.Tools = tools
		params.ToolChoice = anthropic.ToolChoiceUnionParam{OfAuto: &anthropic.ToolChoiceAutoParam{DisableParallelToolUse: anthropic.Bool(true)}}
	}
	if req.PrefixID != "" {
		params.CacheControl = anthropic.NewCacheControlEphemeralParam()
	}
	client := s.provider.client(key)
	response, err := client.Messages.New(ctx, params)
	if err != nil {
		answer.Outcome = anthropicOutcome(ctx, err)
		return answer, nil
	}

	var text strings.Builder
	toolName, toolInput := "", ""
	for _, block := range response.Content {
		switch b := block.AsAny().(type) {
		case anthropic.TextBlock:
			text.WriteString(b.Text)
		case anthropic.ToolUseBlock:
			if toolName == "" {
				toolName, toolInput = b.Name, string(b.Input)
			}
		}
	}
	input := int(response.Usage.InputTokens + response.Usage.CacheReadInputTokens + response.Usage.CacheCreationInputTokens)
	usage := harness.Usage{InputTokens: input, OutputTokens: int(response.Usage.OutputTokens), CachedInputTokens: int(response.Usage.CacheReadInputTokens)}
	if input == 0 && usage.OutputTokens == 0 {
		usage = harness.Usage{InputTokens: (len(req.Prompt) + 3) / 4, OutputTokens: (text.Len() + 3) / 4} // none reported: estimate, never free
	}
	answer.Usage, answer.Model = usage, string(response.Model)
	answer.CostMicros = s.provider.cfg.Pricing.Cost(usage)

	switch response.StopReason {
	case anthropic.StopReasonEndTurn, anthropic.StopReasonStopSequence, anthropic.StopReasonMaxTokens, anthropic.StopReasonToolUse:
	default: // a refusal, a pause or anything new: no content is passed on
		answer.Outcome = harness.OutcomeFailed
		return answer, nil
	}
	if toolName != "" {
		if !offeredSpec(offered, toolName) || len(toolInput) > maxAnthropicToolArgBytes || !json.Valid([]byte(toolInput)) {
			answer.Outcome = harness.OutcomeFailed
			return answer, nil
		}
		answer.ToolID, answer.ToolArguments = toolName, []byte(toolInput)
		if toolName == "clarify" {
			var ask struct {
				Question string `json:"question"`
			}
			if json.Unmarshal(answer.ToolArguments, &ask) != nil || strings.TrimSpace(ask.Question) == "" {
				answer.Outcome, answer.ToolID, answer.ToolArguments = harness.OutcomeFailed, "", nil
				return answer, nil
			}
			answer.Text, answer.ToolArguments = ask.Question, nil
		}
		if len(answer.ToolArguments) > req.MaxBytes {
			answer.Outcome, answer.ToolID, answer.ToolArguments = harness.OutcomeFailed, "", nil
			return answer, nil
		}
		answer.Outcome = harness.OutcomeOK
		return answer, nil
	}
	if strings.TrimSpace(text.String()) == "" {
		answer.Outcome = harness.OutcomeFailed
		return answer, nil
	}
	bounded, cut := boundText(text.String(), req.MaxBytes)
	answer.Text, answer.Outcome = bounded, harness.OutcomeOK
	if cut || response.StopReason == anthropic.StopReasonMaxTokens {
		answer.Outcome = harness.OutcomePartial
	}
	return answer, nil
}

func offeredSpec(specs []ToolSpec, name string) bool {
	for _, s := range specs {
		if s.Name == name {
			return true
		}
	}
	return false
}

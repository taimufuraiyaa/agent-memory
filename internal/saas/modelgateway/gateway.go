package modelgateway

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type providerState struct {
	provider  Provider
	policy    ProviderPolicy
	failures  int
	openUntil time.Time
}

type Gateway struct {
	mu        sync.Mutex
	providers map[string]*providerState
	usage     UsageSink
	redactor  Redactor
	quota     QuotaChecker
	now       func() time.Time
}

func New(config Config, usage UsageSink, redactor Redactor, now func() time.Time) (*Gateway, error) {
	if usage == nil || redactor == nil {
		return nil, errors.New("model gateway usage and redaction are required")
	}
	if now == nil {
		now = time.Now
	}
	policies := map[string]ProviderPolicy{}
	for _, policy := range config.Policies {
		if policy.Provider == "" || policy.MaxInputTokens <= 0 || policy.Timeout <= 0 || len(policy.Models) == 0 || len(policy.RetentionPolicies) == 0 {
			return nil, errors.New("model gateway provider policy is incomplete")
		}
		if policy.FailureThreshold <= 0 {
			policy.FailureThreshold = 3
		}
		if policy.Cooldown <= 0 {
			policy.Cooldown = time.Minute
		}
		policies[policy.Provider] = policy
	}
	states := map[string]*providerState{}
	for _, provider := range config.Providers {
		if provider == nil || provider.Name() == "" || provider.ModelVersion() == "" || provider.Dimension() <= 0 {
			return nil, errors.New("model gateway provider is invalid")
		}
		policy, ok := policies[provider.Name()]
		if !ok {
			continue
		}
		states[provider.Name()] = &providerState{provider: provider, policy: policy}
	}
	if len(states) == 0 {
		return nil, errors.New("model gateway has no approved providers")
	}
	return &Gateway{providers: states, usage: usage, redactor: redactor, quota: config.Quota, now: now}, nil
}

func (g *Gateway) Embed(ctx context.Context, request EmbedRequest) (EmbedResponse, error) {
	state, err := g.authorize(request.TenantID, request.Provider, request.Model)
	if err != nil {
		return EmbedResponse{}, err
	}
	if len(request.Texts) == 0 {
		return EmbedResponse{}, ErrProviderPolicy
	}
	redacted := make([]string, len(request.Texts))
	tokens := 0
	for index, value := range request.Texts {
		redacted[index] = g.redactor.Redact(value)
		tokens += estimateTokens(redacted[index])
	}
	if tokens > state.policy.MaxInputTokens {
		return EmbedResponse{}, ErrProviderPolicy
	}
	if g.quota != nil {
		allowed, quotaErr := g.quota.AllowModel(ctx, request.TenantID, tokens, g.now().UTC())
		if quotaErr != nil || !allowed {
			return EmbedResponse{}, ErrProviderPolicy
		}
	}
	vectors, err := g.withRetry(ctx, state, func(call context.Context) ([][]float32, error) {
		return state.provider.EmbedBatch(call, redacted)
	})
	if err == nil {
		err = validateVectors(vectors, len(redacted), state.provider.Dimension())
		if err != nil {
			g.recordFailure(state)
		}
	}
	outcome := "success"
	if err != nil {
		outcome = "failed"
	}
	usage := Usage{TenantID: request.TenantID, SourceID: request.SourceID, SourceVersion: request.SourceVersion, Operation: "embed", Provider: state.provider.Name(), Model: state.provider.ModelVersion(), Dimensions: state.provider.Dimension(), InputTokens: tokens, EstimatedCostMicros: costMicros(tokens, state.policy.InputCostPerMillion), Outcome: outcome, OccurredAt: g.now().UTC()}
	if usageErr := g.usage.RecordUsage(ctx, usage); usageErr != nil && err == nil {
		return EmbedResponse{}, fmt.Errorf("record model usage: %w", usageErr)
	}
	if err != nil {
		return EmbedResponse{}, err
	}
	return EmbedResponse{Provider: state.provider.Name(), Model: state.provider.ModelVersion(), Dimensions: state.provider.Dimension(), Vectors: vectors}, nil
}

func validateVectors(vectors [][]float32, expectedCount, expectedDimension int) error {
	if len(vectors) != expectedCount {
		return errors.New("model provider returned the wrong vector count")
	}
	for _, vector := range vectors {
		if len(vector) != expectedDimension {
			return errors.New("model provider returned the wrong vector dimension")
		}
	}
	return nil
}

func (g *Gateway) Generate(ctx context.Context, request GenerateRequest) (GenerateResponse, error) {
	evidence := append([]Evidence(nil), request.Evidence...)
	state, err := g.authorize(request.TenantID, request.Provider, request.Model)
	if err != nil {
		if errors.Is(err, ErrCircuitOpen) {
			return GenerateResponse{Evidence: evidence, FailureCode: "generation_unavailable"}, nil
		}
		return GenerateResponse{}, err
	}
	prompt := g.redactor.Redact(request.Prompt)
	inputTokens := estimateTokens(prompt)
	if inputTokens == 0 || inputTokens > state.policy.MaxInputTokens {
		return GenerateResponse{}, ErrProviderPolicy
	}
	if g.quota != nil {
		allowed, quotaErr := g.quota.AllowModel(ctx, request.TenantID, inputTokens, g.now().UTC())
		if quotaErr != nil || !allowed {
			return GenerateResponse{Evidence: evidence, FailureCode: "quota_exceeded"}, nil
		}
	}
	text, callErr := g.withGenerateRetry(ctx, state, func(call context.Context) (string, error) {
		return state.provider.Generate(call, prompt)
	})
	if strings.TrimSpace(text) == "" && callErr == nil {
		callErr = errors.New("model provider returned empty generation")
	}
	outcome := "success"
	outputTokens := estimateTokens(text)
	if callErr != nil {
		outcome = "failed"
		outputTokens = 0
	}
	usage := Usage{TenantID: request.TenantID, Operation: "generate", Provider: state.provider.Name(), Model: state.provider.ModelVersion(), InputTokens: inputTokens, OutputTokens: outputTokens, EstimatedCostMicros: costMicros(inputTokens, state.policy.InputCostPerMillion) + costMicros(outputTokens, state.policy.OutputCostPerMillion), Outcome: outcome, OccurredAt: g.now().UTC()}
	if usageErr := g.usage.RecordUsage(ctx, usage); usageErr != nil && callErr == nil {
		return GenerateResponse{}, fmt.Errorf("record model usage: %w", usageErr)
	}
	if callErr != nil {
		return GenerateResponse{Evidence: evidence, FailureCode: "generation_unavailable"}, nil
	}
	return GenerateResponse{Text: text, Evidence: evidence, Generated: true}, nil
}

func (g *Gateway) authorize(tenant, providerName, model string) (*providerState, error) {
	if strings.TrimSpace(tenant) == "" {
		return nil, ErrProviderPolicy
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	state := g.providers[providerName]
	if state == nil || state.provider.ModelVersion() != model || !contains(state.policy.Models, model) || !contains(state.policy.RetentionPolicies, state.provider.RetentionPolicy()) {
		return nil, ErrProviderPolicy
	}
	if g.now().Before(state.openUntil) {
		return nil, ErrCircuitOpen
	}
	return state, nil
}

func (g *Gateway) withRetry(ctx context.Context, state *providerState, call func(context.Context) ([][]float32, error)) ([][]float32, error) {
	var last error
	for attempt := 0; attempt <= state.policy.MaxRetries; attempt++ {
		callContext, cancel := context.WithTimeout(ctx, state.policy.Timeout)
		value, err := call(callContext)
		cancel()
		if err == nil {
			g.mu.Lock()
			state.failures = 0
			state.openUntil = time.Time{}
			g.mu.Unlock()
			return value, nil
		}
		last = err
		if !isTemporary(err) || attempt == state.policy.MaxRetries {
			break
		}
	}
	g.mu.Lock()
	state.failures++
	if state.failures >= state.policy.FailureThreshold {
		state.openUntil = g.now().Add(state.policy.Cooldown)
	}
	g.mu.Unlock()
	return nil, last
}

func (g *Gateway) withGenerateRetry(ctx context.Context, state *providerState, call func(context.Context) (string, error)) (string, error) {
	var last error
	for attempt := 0; attempt <= state.policy.MaxRetries; attempt++ {
		callContext, cancel := context.WithTimeout(ctx, state.policy.Timeout)
		value, err := call(callContext)
		cancel()
		if err == nil {
			g.resetCircuit(state)
			return value, nil
		}
		last = err
		if !isTemporary(err) || attempt == state.policy.MaxRetries {
			break
		}
	}
	g.recordFailure(state)
	return "", last
}

func (g *Gateway) resetCircuit(state *providerState) {
	g.mu.Lock()
	state.failures = 0
	state.openUntil = time.Time{}
	g.mu.Unlock()
}

func (g *Gateway) recordFailure(state *providerState) {
	g.mu.Lock()
	state.failures++
	if state.failures >= state.policy.FailureThreshold {
		state.openUntil = g.now().Add(state.policy.Cooldown)
	}
	g.mu.Unlock()
}

func isTemporary(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	var candidate temporary
	return errors.As(err, &candidate) && candidate.Temporary()
}

func estimateTokens(value string) int {
	runes := utf8.RuneCountInString(value)
	if runes == 0 {
		return 0
	}
	return (runes + 3) / 4
}

func costMicros(tokens int, rate int64) int64 {
	if tokens <= 0 || rate <= 0 {
		return 0
	}
	value := (int64(tokens)*rate + 999999) / 1000000
	if value == 0 {
		return 1
	}
	return value
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

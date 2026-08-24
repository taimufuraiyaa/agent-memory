package modelgateway

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

type providerFixture struct {
	mu              sync.Mutex
	name, model     string
	retention       string
	dimension       int
	outputDimension int
	inputs          []string
	embedFailures   int
	generateError   error
	delay           time.Duration
	ignoreContext   bool
	calls           int
}

func (p *providerFixture) Name() string            { return p.name }
func (p *providerFixture) ModelVersion() string    { return p.model }
func (p *providerFixture) RetentionPolicy() string { return p.retention }
func (p *providerFixture) Dimension() int          { return p.dimension }
func (p *providerFixture) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	p.mu.Lock()
	p.calls++
	p.inputs = append([]string(nil), texts...)
	delay := p.delay
	p.mu.Unlock()
	if delay > 0 {
		if p.ignoreContext {
			time.Sleep(delay)
		} else {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.embedFailures > 0 {
		p.embedFailures--
		return nil, Temporary(errors.New("provider unavailable"))
	}
	outputDimension := p.outputDimension
	if outputDimension == 0 {
		outputDimension = p.dimension
	}
	vectors := make([][]float32, len(texts))
	for index := range vectors {
		vectors[index] = make([]float32, outputDimension)
		vectors[index][index%outputDimension] = 1
	}
	return vectors, nil
}
func (p *providerFixture) Generate(_ context.Context, prompt string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.inputs = []string{prompt}
	if p.generateError != nil {
		return "", p.generateError
	}
	return "grounded synthesis", nil
}

type usageFixture struct{ values []Usage }

func (u *usageFixture) RecordUsage(_ context.Context, value Usage) error {
	u.values = append(u.values, value)
	return nil
}

type redactorFixture struct{}

func (redactorFixture) Redact(value string) string {
	return strings.ReplaceAll(value, "member@example.test", "[email]")
}

func TestGatewayEnforcesApprovedEmbeddingContractAndMetersUsage(t *testing.T) {
	provider := &providerFixture{name: "private-model", model: "embed-v1", retention: "zero-retention", dimension: 3}
	usage := &usageFixture{}
	gateway, err := New(Config{
		Providers: []Provider{provider},
		Policies:  []ProviderPolicy{{Provider: "private-model", Models: []string{"embed-v1"}, RetentionPolicies: []string{"zero-retention"}, MaxInputTokens: 100, Timeout: time.Second, MaxRetries: 1, FailureThreshold: 2, Cooldown: time.Minute, InputCostPerMillion: 2}},
	}, usage, redactorFixture{}, func() time.Time { return time.Unix(100, 0).UTC() })
	if err != nil {
		t.Fatal(err)
	}
	response, err := gateway.Embed(context.Background(), EmbedRequest{TenantID: "tenant-a", SourceID: "source-a", SourceVersion: 7, Provider: "private-model", Model: "embed-v1", Texts: []string{"Contact member@example.test about the private source."}})
	if err != nil {
		t.Fatal(err)
	}
	if response.Provider != "private-model" || response.Model != "embed-v1" || response.Dimensions != 3 || len(response.Vectors) != 1 || len(response.Vectors[0]) != 3 {
		t.Fatalf("embedding response=%+v", response)
	}
	if len(provider.inputs) != 1 || strings.Contains(provider.inputs[0], "member@example.test") {
		t.Fatalf("provider received unredacted input=%q", provider.inputs)
	}
	if len(usage.values) != 1 {
		t.Fatalf("usage records=%+v", usage.values)
	}
	recorded := usage.values[0]
	if recorded.TenantID != "tenant-a" || recorded.SourceID != "source-a" || recorded.SourceVersion != 7 || recorded.Provider != "private-model" || recorded.Model != "embed-v1" || recorded.Dimensions != 3 || recorded.InputTokens == 0 || recorded.EstimatedCostMicros == 0 || recorded.Outcome != "success" {
		t.Fatalf("usage=%+v", recorded)
	}
}

func TestGatewayMetersMalformedEmbeddingOutputAsFailure(t *testing.T) {
	provider := &providerFixture{name: "private-model", model: "embed-v1", retention: "zero-retention", dimension: 3, outputDimension: 2}
	usage := &usageFixture{}
	gateway, err := New(Config{
		Providers: []Provider{provider},
		Policies:  []ProviderPolicy{{Provider: "private-model", Models: []string{"embed-v1"}, RetentionPolicies: []string{"zero-retention"}, MaxInputTokens: 100, Timeout: time.Second}},
	}, usage, redactorFixture{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gateway.Embed(context.Background(), EmbedRequest{TenantID: "tenant-a", Provider: "private-model", Model: "embed-v1", Texts: []string{"private content"}}); err == nil {
		t.Fatal("malformed provider output was accepted")
	}
	if len(usage.values) != 1 || usage.values[0].Outcome != "failed" {
		t.Fatalf("malformed output usage=%+v", usage.values)
	}
}

func TestGatewayRejectsProviderModelRetentionAndTokenPolicyBeforeTransmission(t *testing.T) {
	provider := &providerFixture{name: "private-model", model: "embed-v1", retention: "trains-on-input", dimension: 3}
	gateway, err := New(Config{Providers: []Provider{provider}, Policies: []ProviderPolicy{{Provider: "private-model", Models: []string{"embed-v1"}, RetentionPolicies: []string{"zero-retention"}, MaxInputTokens: 2, Timeout: time.Second}}}, &usageFixture{}, redactorFixture{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = gateway.Embed(context.Background(), EmbedRequest{TenantID: "tenant", Provider: "private-model", Model: "embed-v1", Texts: []string{"private content must never transmit"}})
	if !errors.Is(err, ErrProviderPolicy) || len(provider.inputs) != 0 {
		t.Fatalf("policy error=%v inputs=%q", err, provider.inputs)
	}
	_, err = gateway.Embed(context.Background(), EmbedRequest{TenantID: "tenant", Provider: "missing", Model: "embed-v1", Texts: []string{"short"}})
	if !errors.Is(err, ErrProviderPolicy) {
		t.Fatalf("unknown provider error=%v", err)
	}
}

func TestGatewayRetriesTemporaryFailuresAndOpensThenRecoversCircuit(t *testing.T) {
	now := time.Unix(200, 0).UTC()
	provider := &providerFixture{name: "private-model", model: "embed-v1", retention: "zero-retention", dimension: 2, embedFailures: 1}
	gateway, err := New(Config{Providers: []Provider{provider}, Policies: []ProviderPolicy{{Provider: "private-model", Models: []string{"embed-v1"}, RetentionPolicies: []string{"zero-retention"}, MaxInputTokens: 100, Timeout: time.Second, MaxRetries: 1, FailureThreshold: 1, Cooldown: time.Minute}}}, &usageFixture{}, redactorFixture{}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	request := EmbedRequest{TenantID: "tenant", Provider: "private-model", Model: "embed-v1", Texts: []string{"retry me"}}
	if _, err := gateway.Embed(context.Background(), request); err != nil || provider.calls != 2 {
		t.Fatalf("retry calls=%d err=%v", provider.calls, err)
	}
	provider.embedFailures = 2
	if _, err := gateway.Embed(context.Background(), request); err == nil {
		t.Fatal("terminal provider failure was accepted")
	}
	if _, err := gateway.Embed(context.Background(), request); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("open-circuit error=%v", err)
	}
	now = now.Add(2 * time.Minute)
	if _, err := gateway.Embed(context.Background(), request); err != nil {
		t.Fatalf("circuit did not recover: %v", err)
	}
}

func TestGatewayTimesOutWithoutLoggingPromptsAndGenerationFallsBackToEvidence(t *testing.T) {
	secret := "private source sentence that must not enter logs"
	provider := &providerFixture{name: "private-model", model: "model-v1", retention: "zero-retention", dimension: 2, delay: 50 * time.Millisecond}
	usage := &usageFixture{}
	gateway, err := New(Config{Providers: []Provider{provider}, Policies: []ProviderPolicy{{Provider: "private-model", Models: []string{"model-v1"}, RetentionPolicies: []string{"zero-retention"}, MaxInputTokens: 100, Timeout: 5 * time.Millisecond, FailureThreshold: 2, Cooldown: time.Minute}}}, usage, redactorFixture{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	_, err = gateway.Embed(context.Background(), EmbedRequest{TenantID: "tenant", Provider: "private-model", Model: "model-v1", Texts: []string{secret}})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout error=%v", err)
	}
	provider.delay = 0
	provider.generateError = errors.New("generation unavailable")
	evidence := []Evidence{{SourceID: "source", PassageID: "passage", Text: "Citable evidence."}}
	generated, err := gateway.Generate(context.Background(), GenerateRequest{TenantID: "tenant", Provider: "private-model", Model: "model-v1", Prompt: secret, Evidence: evidence})
	if err != nil {
		t.Fatal(err)
	}
	if generated.Generated || generated.Text != "" || generated.FailureCode != "generation_unavailable" || len(generated.Evidence) != 1 || generated.Evidence[0].PassageID != "passage" {
		t.Fatalf("evidence fallback=%+v", generated)
	}
	if strings.Contains(logs.String(), secret) || strings.Contains(logs.String(), "Citable evidence") {
		t.Fatalf("model content entered logs: %s", logs.String())
	}
}

func TestGatewayRejectsProviderSuccessAfterAttemptDeadline(t *testing.T) {
	provider := &providerFixture{
		name: "private-model", model: "model-v1", retention: "zero-retention",
		dimension: 2, delay: 20 * time.Millisecond, ignoreContext: true,
	}
	gateway, err := New(Config{Providers: []Provider{provider}, Policies: []ProviderPolicy{{
		Provider: "private-model", Models: []string{"model-v1"},
		RetentionPolicies: []string{"zero-retention"}, MaxInputTokens: 100,
		Timeout: time.Millisecond, FailureThreshold: 2, Cooldown: time.Minute,
	}}}, &usageFixture{}, redactorFixture{}, nil)
	if err != nil {
		t.Fatal(err)
	}

	_, err = gateway.Embed(context.Background(), EmbedRequest{
		TenantID: "tenant", Provider: "private-model", Model: "model-v1",
		Texts: []string{"late provider success"},
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("late provider success error=%v", err)
	}
}

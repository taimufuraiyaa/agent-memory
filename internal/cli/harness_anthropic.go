package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessmodel"
	"github.com/taimufuraiyaa/agent-memory/internal/workspace"
)

// The Anthropic composition follows the OpenAI one: nothing has a default that could widen
// egress or misreport spend, so the confirmation, the key, the model and the prices are all
// required and the data class is capped at internal.
const (
	harnessAnthropicModelEnv  = "AGENT_MEMORY_HARNESS_ANTHROPIC_MODEL"
	harnessAnthropicPriceEnv  = "AGENT_MEMORY_HARNESS_ANTHROPIC_PRICE"
	harnessAnthropicClassEnv  = "AGENT_MEMORY_HARNESS_ANTHROPIC_MAX_CLASS"
	harnessAnthropicWindowEnv = "AGENT_MEMORY_HARNESS_ANTHROPIC_CONTEXT_TOKENS"
)

// anthropicBackend validates every explicit opt-in for the Claude provider and registers it.
func anthropicBackend(registry *harness.Registry, opts harnessBuildOptions) (modelBackend, error) {
	var none modelBackend
	if strings.TrimSpace(os.Getenv(harnessEgressEnv)) != "anthropic" {
		return none, fmt.Errorf("set %s=anthropic to confirm that assembled prompts are sent to Anthropic", harnessEgressEnv)
	}
	key := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY"))
	if key == "" {
		return none, errors.New("ANTHROPIC_API_KEY is not set")
	}
	model := strings.TrimSpace(os.Getenv(harnessAnthropicModelEnv))
	if model == "" {
		return none, fmt.Errorf("%s is required; no model is assumed", harnessAnthropicModelEnv)
	}
	pricing, err := parsePricingFrom(os.Getenv(harnessAnthropicPriceEnv), harnessAnthropicPriceEnv)
	if err != nil {
		return none, err
	}
	class := harnessmodel.ClassInternal
	switch strings.TrimSpace(os.Getenv(harnessAnthropicClassEnv)) {
	case "", "internal":
	case "public":
		class = harnessmodel.ClassPublic
	default:
		return none, fmt.Errorf("%s may only be \"public\" or \"internal\": a third-party cloud provider is never eligible for more sensitive data", harnessAnthropicClassEnv)
	}
	window := defaultOpenAIWindow
	if raw := strings.TrimSpace(os.Getenv(harnessAnthropicWindowEnv)); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1000 || parsed > 2_000_000 {
			return none, fmt.Errorf("%s must be a whole number between 1000 and 2000000", harnessAnthropicWindowEnv)
		}
		window = parsed
	}
	provider, err := harnessmodel.NewAnthropicProvider(harnessmodel.AnthropicConfig{Model: model, APIKey: func() string { return key },
		BaseURL: opts.anthropicBaseURL, Pricing: pricing, AllowEgress: true, Tools: offeredToolSpecs()})
	if err != nil {
		return none, err
	}
	if err := provider.Register(registry); err != nil {
		return none, err
	}
	return modelBackend{id: harnessmodel.AnthropicProviderID, capability: harnessmodel.AnthropicCapability, pricing: pricing, class: class, window: window,
		notice: fmt.Sprintf("harness: Anthropic provider enabled (model %s); assembled prompts with up to %s data are sent to Anthropic and may incur charges", model, harnessClassName(class))}, nil
}

func composeAnthropic(ctx context.Context, registry *harness.Registry, workspaces *workspace.Manager, baseDir string, opts harnessBuildOptions) (openAIComposition, error) {
	backend, err := anthropicBackend(registry, opts)
	if err != nil {
		return openAIComposition{}, err
	}
	return composeRuntime(ctx, registry, workspaces, baseDir, opts, backend)
}

// offeredToolSpecs lets the model be offered tools only when they were composed in.
func offeredToolSpecs() func(ids []string) []harnessmodel.ToolSpec {
	if strings.TrimSpace(os.Getenv(harnessToolsEnv)) == "" {
		return nil
	}
	describe := toolDescriber()
	return func(ids []string) []harnessmodel.ToolSpec {
		var out []harnessmodel.ToolSpec
		for _, tool := range describe(ids) {
			out = append(out, harnessmodel.ToolSpec{Name: tool.Name, Description: tool.Description, Parameters: tool.Parameters})
		}
		return out
	}
}

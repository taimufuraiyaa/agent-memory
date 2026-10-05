package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/api"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/harness"
	"github.com/taimufuraiyaa/agent-memory/internal/harness/harnesstest"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessauth"
	"github.com/taimufuraiyaa/agent-memory/internal/harnesscontext"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessdecide"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessmodel"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessrun"
	"github.com/taimufuraiyaa/agent-memory/internal/openaiapi"
	"github.com/taimufuraiyaa/agent-memory/internal/workspace"
)

// harnessProvidersEnv selects the harness providers composed into serve. Unset means
// the harness is not composed at all and its routes do not exist. "fake" composes
// scripted test doubles that never call a real model. "openai" composes one real,
// text-only OpenAI provider and sends assembled prompts to a third party, so it demands
// several explicit opt-ins and fails closed on anything missing or unrecognized.
const harnessProvidersEnv = "AGENT_MEMORY_HARNESS_PROVIDERS"

const (
	harnessEgressEnv       = "AGENT_MEMORY_HARNESS_ALLOW_EGRESS"
	harnessOpenAIModelEnv  = "AGENT_MEMORY_HARNESS_OPENAI_MODEL"
	harnessOpenAIPriceEnv  = "AGENT_MEMORY_HARNESS_OPENAI_PRICE"
	harnessOpenAIClassEnv  = "AGENT_MEMORY_HARNESS_OPENAI_MAX_CLASS"
	harnessOpenAIWindowEnv = "AGENT_MEMORY_HARNESS_OPENAI_CONTEXT_TOKENS"
	defaultOpenAIWindow    = 32000
	harnessOpenAIOutputCap = 1024
)

// harnessBuildOptions exists for tests. Production code never sets it.
type harnessBuildOptions struct {
	openAIBaseURL string
	jevBaseURL    string
}

const harnessFakeReply = "Fake harness provider: no real model was called."

// buildHarnessGateway composes the opt-in harness runtime for serve. It returns a nil
// gateway, and a no-op close, unless explicitly enabled; any other setting is an error
// so a typo cannot silently leave the harness half-configured.
func buildHarnessGateway(ctx context.Context, svc *api.Service, errOut io.Writer) (*api.HarnessGateway, func(), error) {
	return buildHarnessGatewayWith(ctx, svc, errOut, harnessBuildOptions{})
}

func buildHarnessGatewayWith(ctx context.Context, svc *api.Service, errOut io.Writer, opts harnessBuildOptions) (*api.HarnessGateway, func(), error) {
	mode := strings.TrimSpace(os.Getenv(harnessProvidersEnv))
	if mode == "" {
		return nil, func() {}, nil
	}
	if mode == "fake" && strings.TrimSpace(os.Getenv(harnessToolsEnv)) != "" {
		return nil, nil, fmt.Errorf("%s needs the openai composition; the fake model never asks for a tool", harnessToolsEnv)
	}
	if mode != "fake" && mode != "openai" {
		return nil, nil, fmt.Errorf("unsupported %s %q; use \"fake\" or \"openai\"", harnessProvidersEnv, mode)
	}
	if svc == nil || svc.ClientProfiles == nil || strings.TrimSpace(svc.BaseDir) == "" {
		return nil, nil, fmt.Errorf("harness requires the local client profile store")
	}
	workspaces, err := workspace.NewManager(svc.BaseDir)
	if err != nil {
		return nil, nil, err
	}
	// The authority shares the service's own client profile store, so a profile
	// removed through the API stops verifying immediately.
	authority, err := harnessauth.New(svc.BaseDir, harnessauth.Options{Clients: svc.ClientProfiles, Workspaces: managerDirectory{manager: workspaces}})
	if err != nil {
		return nil, nil, err
	}
	registry := harness.NewRegistry()
	cfg := harnessrun.Config{
		DataDir:         svc.BaseDir,
		Registry:        registry,
		Redact:          engine.RedactSecretsAndPII,
		StillAuthorized: func(owner harnessrun.Owner) bool { return authority.Active(owner.GrantID, owner.Workspace) },
		// No tool policy is configured, so every tool action is denied.
	}
	fake := mode == "fake"
	var notice string
	var decisions *jevComposition
	var toolNames []string
	approvalsOn := false
	switch mode {
	case "fake":
		if _, err := harnesstest.Register(registry, harnesstest.Manifest("fake-model", harness.KindModel, "generation"),
			harnesstest.Behavior{Script: []harnesstest.Step{{Text: harnessFakeReply}}}); err != nil {
			return nil, nil, err
		}
		if _, err := harnesstest.Register(registry, harnesstest.Manifest("fake-tool", harness.KindTool, "read"), harnesstest.Behavior{}); err != nil {
			return nil, nil, err
		}
		cfg.Model = harnessrun.Binding{Provider: "fake-model", Capability: "generation"}
		cfg.Tool = &harnessrun.Binding{Provider: "fake-tool", Capability: "read"}
		notice = "harness: fake providers enabled; no real model is called"
	case "openai":
		composed, err := composeOpenAI(ctx, registry, workspaces, svc.BaseDir, opts)
		if err != nil {
			return nil, nil, err
		}
		cfg.Model, cfg.Router, cfg.Context, notice = composed.model, composed.router, composed.context, composed.notice
		decisions = composed.jev
		tools, err := composeTools(registry, workspaces, svc.BaseDir, decisions)
		if err != nil {
			decisions.close()
			return nil, nil, err
		}
		if tools != nil {
			cfg.Tool, cfg.Policy, cfg.Approvals, cfg.ToolSelector, cfg.MaxOfferedTools = &tools.binding, tools.policy, tools.approvals, tools.selector, tools.maxOffered
			toolNames, approvalsOn = tools.names, true
		} else if decisions != nil && (decisions.enabled[harnessdecide.KindTools] || decisions.enabled[harnessdecide.KindCommandRisk]) {
			decisions.close()
			return nil, nil, fmt.Errorf("%s lists a decision that needs tools; set %s as well", harnessJevEnv, harnessToolsEnv)
		}
	}
	runs, err := harnessrun.NewManager(cfg)
	if err != nil {
		decisions.close()
		return nil, nil, err
	}
	report, err := runs.Recover(ctx)
	if err != nil {
		_ = runs.Close()
		return nil, nil, err
	}
	if report.Requeued+report.Cancelled+report.Quarantined > 0 {
		_, _ = fmt.Fprintf(errOut, "harness: recovered %d run(s), finished %d cancellation(s), quarantined %d damaged record(s)\n", report.Requeued, report.Cancelled, report.Quarantined)
	}
	_, _ = fmt.Fprintln(errOut, notice)
	if decisions != nil {
		_, _ = fmt.Fprintln(errOut, decisions.notice)
	}

	sweepCtx, stopSweep := context.WithCancel(ctx)
	var sweeper sync.WaitGroup
	sweeper.Add(1)
	go func() {
		defer sweeper.Done()
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-sweepCtx.Done():
				return
			case <-ticker.C:
				_, _ = runs.Sweep()
			}
		}
	}()
	var once sync.Once
	closeAll := func() {
		once.Do(func() {
			stopSweep()
			sweeper.Wait()
			_ = runs.Close()
			decisions.close()
		})
	}
	gateway := &api.HarnessGateway{Authority: authority, Runs: runs, Providers: registry.Manifests(), Fake: fake, Tools: toolNames, Approvals: approvalsOn}
	if decisions != nil {
		gateway.Decisions = decisions.snapshot
	}
	return gateway, closeAll, nil
}

type openAIComposition struct {
	model   harnessrun.Binding
	router  harnessrun.ModelRouter
	context harnessrun.ContextSource
	notice  string
	jev     *jevComposition
}

// composeOpenAI wires one real, text-only OpenAI provider. Every setting that changes what
// leaves the machine or what it costs must be stated explicitly, and nothing has a default
// that could silently widen egress or misreport spend: the key, the model, an egress
// confirmation and the prices are all required, and the data class is capped at internal.
func composeOpenAI(ctx context.Context, registry *harness.Registry, workspaces *workspace.Manager, baseDir string, opts harnessBuildOptions) (openAIComposition, error) {
	var none openAIComposition
	if strings.TrimSpace(os.Getenv(harnessEgressEnv)) != "openai" {
		return none, fmt.Errorf("set %s=openai to confirm that assembled prompts are sent to OpenAI", harnessEgressEnv)
	}
	key := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if key == "" {
		return none, errors.New("OPENAI_API_KEY is not set")
	}
	model := strings.TrimSpace(os.Getenv(harnessOpenAIModelEnv))
	if model == "" {
		return none, fmt.Errorf("%s is required; no model is assumed", harnessOpenAIModelEnv)
	}
	pricing, err := parseHarnessPricing(os.Getenv(harnessOpenAIPriceEnv))
	if err != nil {
		return none, err
	}
	class := harnessmodel.ClassInternal
	switch strings.TrimSpace(os.Getenv(harnessOpenAIClassEnv)) {
	case "", "internal":
	case "public":
		class = harnessmodel.ClassPublic
	default:
		return none, fmt.Errorf("%s may only be \"public\" or \"internal\": a third-party cloud provider is never eligible for more sensitive data", harnessOpenAIClassEnv)
	}
	window := defaultOpenAIWindow
	if raw := strings.TrimSpace(os.Getenv(harnessOpenAIWindowEnv)); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1000 || parsed > 2_000_000 {
			return none, fmt.Errorf("%s must be a whole number between 1000 and 2000000", harnessOpenAIWindowEnv)
		}
		window = parsed
	}
	provider, err := harnessmodel.NewOpenAIProvider(harnessmodel.OpenAIConfig{Model: model, APIKey: func() string { return key },
		BaseURL: opts.openAIBaseURL, Pricing: pricing, AllowEgress: true, Tools: offeredToolDescriptions()})
	if err != nil {
		return none, err
	}
	if err := provider.Register(registry); err != nil {
		return none, err
	}
	meter := harnessmodel.NewMeter(nil)
	decisions, err := composeJev(ctx, registry, baseDir, opts)
	if err != nil {
		return none, err
	}
	routerConfig := harnessmodel.RouterConfig{
		Profiles: []harnessmodel.Profile{{Provider: harnessmodel.OpenAIProviderID, Capability: harnessmodel.OpenAICapability, Pricing: pricing,
			MaxClass: class, ContextTokens: window, MaxOutputTokens: harnessOpenAIOutputCap}},
		Prober: &harnessmodel.RegistryProber{Registry: registry, Workspace: "harness-router"},
		Meter:  meter,
	}
	if decisions != nil && decisions.enabled[harnessdecide.KindModel] {
		routerConfig.Advisor = harnessmodel.ServiceAdvisor{Service: decisions.hub.Service("")}
	}
	router, err := harnessmodel.NewRouter(routerConfig)
	if err != nil {
		decisions.close()
		return none, err
	}
	source := &harnesscontext.RunSource{
		Assembler:   harnesscontext.New(harnesscontext.Config{Redact: engine.RedactSecretsAndPII}),
		Eligibility: harnesscontext.Eligibility{MaxSensitivity: harnesscontext.Sensitivity(class), AdvisorMaxSensitivity: harnesscontext.SensitivityPublic},
		Budget:      harnesscontext.Budget{Total: window, ReserveOutput: harnessOpenAIOutputCap},
		Gather: func(_ context.Context, owner harnessrun.Owner) ([]harnesscontext.Chunk, error) {
			project, err := workspaces.Project(owner.Workspace)
			if err != nil || strings.TrimSpace(project.WorkspaceRoot) == "" {
				return nil, errors.New("workspace has no project root")
			}
			rules, _ := harnesscontext.InstructionChunks(owner.Workspace, project.WorkspaceRoot)
			return rules, nil
		},
	}
	if decisions != nil {
		if decisions.enabled[harnessdecide.KindVisibility] {
			source.AdvisorFor = func(runID string) harnesscontext.Advisor {
				return harnesscontext.ServiceAdvisor{Service: decisions.hub.Service(runID)}
			}
		}
		if decisions.enabled[harnessdecide.KindCache] {
			source.CacheFor = func(runID string) harnesscontext.CacheAdvisor {
				return harnesscontext.ServiceCacheAdvisor{Service: decisions.hub.Service(runID), Stats: func(string) harnesscontext.CacheStats {
					st := meter.Stats(harnessmodel.OpenAIProviderID)
					if st.Calls == 0 || st.InputTokens == 0 {
						return harnesscontext.CacheStats{}
					}
					return harnesscontext.CacheStats{Measured: true, HitPercent: int(st.CachedInputTokens * 100 / st.InputTokens), Turns: st.Calls, PrefixTokens: int(st.InputTokens / int64(st.Calls))}
				}}
			}
		}
	}
	return openAIComposition{
		jev:     decisions,
		model:   harnessrun.Binding{Provider: harnessmodel.OpenAIProviderID, Capability: harnessmodel.OpenAICapability},
		router:  harnessmodel.LoopRouter{Router: router, Meter: meter},
		context: source,
		notice:  fmt.Sprintf("harness: OpenAI provider enabled (model %s); assembled prompts with up to %s data are sent to OpenAI and may incur charges", model, harnessClassName(class)),
	}, nil
}

func harnessClassName(c harnessmodel.Class) string {
	if c == harnessmodel.ClassPublic {
		return "public"
	}
	return "internal"
}

// parseHarnessPricing reads "input,cached,output" in micro currency units per million
// tokens. It is required, because a stale built-in price would silently misreport spend.
func parseHarnessPricing(raw string) (harnessmodel.Pricing, error) {
	parts := strings.Split(strings.TrimSpace(raw), ",")
	if len(parts) != 3 {
		return harnessmodel.Pricing{}, fmt.Errorf("%s is required as \"input,cached,output\" micro units per million tokens", harnessOpenAIPriceEnv)
	}
	var values [3]int64
	for i, part := range parts {
		v, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err != nil {
			return harnessmodel.Pricing{}, fmt.Errorf("%s must be three whole numbers", harnessOpenAIPriceEnv)
		}
		values[i] = v
	}
	pricing := harnessmodel.Pricing{InputPerMTok: values[0], CachedInputPerMTok: values[1], OutputPerMTok: values[2]}
	if err := pricing.Validate(); err != nil {
		return harnessmodel.Pricing{}, fmt.Errorf("%s is out of range or inconsistent", harnessOpenAIPriceEnv)
	}
	return pricing, nil
}

// offeredToolDescriptions lets the model be offered tools only when they were composed in.
func offeredToolDescriptions() func(ids []string) []openaiapi.Tool {
	if strings.TrimSpace(os.Getenv(harnessToolsEnv)) == "" {
		return nil
	}
	return toolDescriber()
}

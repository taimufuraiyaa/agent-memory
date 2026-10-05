package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessdecide"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessjev"
	"github.com/taimufuraiyaa/agent-memory/internal/jev"
	"github.com/taimufuraiyaa/agent-memory/internal/jevconfig"
)

// Jev decisions are opt-in on top of the openai composition. They send questions made of
// aliases, counts and fixed vocabulary to TypeSafe, so they need their own confirmation, a
// stored credential, and an explicit list of the decisions to enable.
const (
	harnessJevEnv       = "AGENT_MEMORY_HARNESS_JEV"            // comma list of decisions: model, visibility, cache
	harnessJevEgressEnv = "AGENT_MEMORY_HARNESS_JEV_EGRESS"     // must be "typesafe"
	harnessJevBudgetEnv = "AGENT_MEMORY_HARNESS_JEV_BUDGET"     // requests per run
	harnessJevRateEnv   = "AGENT_MEMORY_HARNESS_JEV_PER_MINUTE" // requests per minute
)

// jevKinds are the decisions this composition has a consumer for.
var jevKinds = map[string]harnessdecide.Kind{
	"model":      harnessdecide.KindModel,
	"visibility": harnessdecide.KindVisibility,
	"cache":      harnessdecide.KindCache,
}

type jevComposition struct {
	hub     *harnessdecide.Hub
	enabled map[harnessdecide.Kind]bool
	session *harness.Session
	notice  string
}

func (c *jevComposition) close() {
	if c != nil && c.session != nil {
		_ = c.session.Close()
	}
}

// snapshot is the content-free health of the decisions, for status views.
func (c *jevComposition) snapshot() map[string]any {
	s := c.hub.Health().Snapshot()
	counts := map[string]map[string]int64{}
	for kind, byStatus := range s.Counts {
		m := map[string]int64{}
		for status, n := range byStatus {
			m[string(status)] = n
		}
		counts[string(kind)] = m
	}
	paused := []string{}
	for _, k := range s.PausedKindList() {
		paused = append(paused, string(k))
	}
	return map[string]any{"provider": string(harnessjev.ProviderID), "available": c.session.State(harnessjev.Capability) == harness.AccessAvailable,
		"provider_paused": !s.ProviderPaused.IsZero(), "paused_kinds": paused, "abandoned": s.Abandoned, "counts": counts}
}

// composeJev returns nil, nil unless Jev decisions were asked for. Anything wrong with the
// request is an error, so a typo or a missing opt-in cannot silently send or skip anything.
func composeJev(ctx context.Context, registry *harness.Registry, baseDir string, opts harnessBuildOptions) (*jevComposition, error) {
	raw := strings.TrimSpace(os.Getenv(harnessJevEnv))
	if raw == "" {
		return nil, nil
	}
	enabled := map[harnessdecide.Kind]bool{}
	var kinds []harnessdecide.Kind
	for _, name := range strings.Split(raw, ",") {
		name = strings.TrimSpace(name)
		kind, ok := jevKinds[name]
		switch {
		case !ok:
			return nil, fmt.Errorf("%s: %q is not a decision this harness can use; choose from model, visibility, cache", harnessJevEnv, name)
		case enabled[kind]:
			return nil, fmt.Errorf("%s: %q is listed twice", harnessJevEnv, name)
		}
		enabled[kind] = true
		kinds = append(kinds, kind)
	}
	if strings.TrimSpace(os.Getenv(harnessJevEgressEnv)) != "typesafe" {
		return nil, fmt.Errorf("set %s=typesafe to confirm that decision questions are sent to TypeSafe", harnessJevEgressEnv)
	}
	store := jevconfig.NewTokenStore(baseDir)
	if _, configured, err := store.Load(ctx); err != nil || !configured {
		return nil, errors.New("no Jev credential is stored; set one in the TUI Jev setup first")
	}
	budget, rate := harnessdecide.DefaultRunBudget, harnessdecide.DefaultPerMinute
	for env, target := range map[string]*int{harnessJevBudgetEnv: &budget, harnessJevRateEnv: &rate} {
		if v := strings.TrimSpace(os.Getenv(env)); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > 10000 {
				return nil, fmt.Errorf("%s must be a whole number between 1 and 10000", env)
			}
			*target = n
		}
	}
	client := jev.NewClient()
	if opts.jevBaseURL != "" {
		client = jev.NewClientWithURL(opts.jevBaseURL)
	}
	provider, err := harnessjev.NewProvider(harnessjev.Config{Client: client, AllowEgress: true, Token: func(c context.Context) (string, error) {
		token, configured, err := store.Load(c)
		if err != nil || !configured {
			return "", errors.New("no credential")
		}
		return token, nil
	}})
	if err != nil {
		return nil, err
	}
	if err := provider.Register(registry); err != nil {
		return nil, err
	}
	session, err := registry.OpenSession(ctx, harnessjev.ProviderID, harness.Scope{Workspace: "harness-decisions", Run: "decisions", Generation: 1})
	if err != nil {
		return nil, err
	}
	hub, err := harnessdecide.NewHub(harnessdecide.Config{Asker: session, Capability: harnessjev.Capability, MaxClass: harnessdecide.ClassOpaque,
		Enabled: kinds, RunBudget: budget, PerMinute: rate}, 0)
	if err != nil {
		_ = session.Close()
		return nil, err
	}
	state := "reachable"
	if session.State(harnessjev.Capability) != harness.AccessAvailable {
		state = "not reachable right now; advice is skipped until it is"
	}
	return &jevComposition{hub: hub, enabled: enabled, session: session,
		notice: fmt.Sprintf("harness: Jev decisions enabled (%s; TypeSafe %s); only aliases, counts and fixed vocabulary are sent", strings.Join(strings.Fields(strings.ReplaceAll(raw, ",", " ")), ", "), state)}, nil
}

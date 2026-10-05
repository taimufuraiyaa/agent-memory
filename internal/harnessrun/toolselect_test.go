package harnessrun_test

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
	"github.com/taimufuraiyaa/agent-memory/internal/harness/harnesstest"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessdecide"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessrun"
)

var sixTools = []string{"tool_a", "tool_b", "tool_c", "tool_d", "tool_e", "tool_f"}

// toolFixture is a manager whose tool provider offers sixTools and whose model finishes at
// once, so each run makes exactly one model call whose offered tools can be read back.
func toolFixture(t *testing.T, limit int, selector harnessrun.ToolSelector) *fixture {
	t.Helper()
	f := &fixture{t: t, dir: t.TempDir(), registry: harness.NewRegistry(), owner: owner("claude-desktop", "agent-memory")}
	var err error
	if f.model, err = harnesstest.Register(f.registry, harnesstest.Manifest("fake-model", harness.KindModel, "generation"), harnesstest.Behavior{Script: []harnesstest.Step{{Text: "done"}}}); err != nil {
		t.Fatal(err)
	}
	if f.tool, err = harnesstest.Register(f.registry, harnesstest.Manifest("fake-tool", harness.KindTool, sixTools...), harnesstest.Behavior{}); err != nil {
		t.Fatal(err)
	}
	f.cfg = harnessrun.Config{DataDir: f.dir, Registry: f.registry,
		Model: harnessrun.Binding{Provider: "fake-model", Capability: "generation"}, Tool: &harnessrun.Binding{Provider: "fake-tool", Capability: "tool_a"},
		MaxOfferedTools: limit, ToolSelector: selector}
	if f.m, err = harnessrun.NewManager(f.cfg); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.m.Close() })
	return f
}

// offered runs one task and returns the tools the model was told about.
func (f *fixture) offered() []string {
	f.t.Helper()
	status := f.start("key-"+time.Now().Format("150405.000000000"), "do the task", harnessrun.Budget{})
	f.waitState(status.ID, harnessrun.StateCompleted)
	request, _ := f.model.Requests.Load().(harness.ModelRequest)
	return request.ToolSchemaIDs
}

type scriptedSelector struct {
	mu    sync.Mutex
	calls []selection
	fn    func(ctx context.Context, runID string, offered []string, keep int) ([]string, error)
}

type selection struct {
	runID   string
	offered []string
	keep    int
}

func (s *scriptedSelector) Select(ctx context.Context, runID string, offered []string, keep int) ([]string, error) {
	s.mu.Lock()
	s.calls = append(s.calls, selection{runID, offered, keep})
	fn := s.fn
	s.mu.Unlock()
	return fn(ctx, runID, offered, keep)
}

func pick(ids ...string) func(context.Context, string, []string, int) ([]string, error) {
	return func(context.Context, string, []string, int) ([]string, error) { return ids, nil }
}

func sorted(ids ...string) []string {
	out := append([]string(nil), ids...)
	sort.Strings(out)
	return out
}

func TestAllToolsAreOfferedWhenThereIsNoLimitOrTheyFit(t *testing.T) {
	selector := &scriptedSelector{fn: pick("tool_f")}
	all := sorted(append([]string{"clarify"}, sixTools...)...)
	for name, limit := range map[string]int{"no limit": 0, "a negative limit": -3, "a limit that fits": 7, "a larger limit": 50} {
		f := toolFixture(t, limit, selector)
		if got := f.offered(); !reflect.DeepEqual(got, all) {
			t.Errorf("%s: offered %v", name, got)
		}
	}
	if len(selector.calls) != 0 {
		t.Fatalf("the selector was asked %d times when nothing needed narrowing", len(selector.calls))
	}
}

func TestWithoutAdviceTheFirstToolsInFixedOrderAreKeptAndClarifyAlways(t *testing.T) {
	f := toolFixture(t, 4, nil)
	if got := f.offered(); !reflect.DeepEqual(got, sorted("clarify", "tool_a", "tool_b", "tool_c")) {
		t.Fatalf("offered %v", got)
	}
	one := toolFixture(t, 1, nil)
	if got := one.offered(); !reflect.DeepEqual(got, []string{"clarify"}) {
		t.Fatalf("a limit of one offered %v", got)
	}
	two := toolFixture(t, 2, &scriptedSelector{fn: pick("tool_f")})
	if got := two.offered(); !reflect.DeepEqual(got, sorted("clarify", "tool_f")) {
		t.Fatalf("a limit of two offered %v", got)
	}
}

func TestAdviceChoosesWhichToolsToKeepAndIsToldHowManyAndWhichRun(t *testing.T) {
	selector := &scriptedSelector{fn: pick("tool_f", "tool_c", "tool_a")}
	f := toolFixture(t, 4, selector)
	if got := f.offered(); !reflect.DeepEqual(got, sorted("clarify", "tool_a", "tool_c", "tool_f")) {
		t.Fatalf("offered %v", got)
	}
	if len(selector.calls) != 1 {
		t.Fatalf("%d calls", len(selector.calls))
	}
	call := selector.calls[0]
	if call.keep != 3 || !reflect.DeepEqual(call.offered, sixTools) || call.runID == "" {
		t.Fatalf("the selector was given %+v", call)
	}
	// A shorter choice is completed in the fixed order up to the limit.
	short := toolFixture(t, 4, &scriptedSelector{fn: pick("tool_e")})
	if got := short.offered(); !reflect.DeepEqual(got, sorted("clarify", "tool_e", "tool_a", "tool_b")) {
		t.Fatalf("offered %v", got)
	}
}

func TestAdviceThatIsNotAValidChoiceIsDiscardedWholeAndNeverAddsATool(t *testing.T) {
	fallback := sorted("clarify", "tool_a", "tool_b", "tool_c")
	for name, fn := range map[string]func(context.Context, string, []string, int) ([]string, error){
		"a tool nobody offered":     pick("tool_f", "ghost"),
		"the clarification request": pick("clarify", "tool_f"),
		"a duplicate":               pick("tool_f", "tool_f"),
		"too many":                  pick("tool_a", "tool_b", "tool_c", "tool_d"),
		"nothing":                   pick(),
		"an error": func(context.Context, string, []string, int) ([]string, error) {
			return []string{"tool_f"}, errors.New("boom")
		},
		"a panic": func(context.Context, string, []string, int) ([]string, error) { panic("selector bug") },
	} {
		f := toolFixture(t, 4, &scriptedSelector{fn: fn})
		if got := f.offered(); !reflect.DeepEqual(got, fallback) {
			t.Errorf("%s: offered %v, want the fixed order %v", name, got, fallback)
		}
	}
}

func TestASelectorThatNeverAnswersCostsOneDeadlineAndNothingMore(t *testing.T) {
	var started atomic.Int32
	f := toolFixture(t, 4, &scriptedSelector{fn: func(ctx context.Context, _ string, _ []string, _ int) ([]string, error) {
		started.Add(1)
		<-ctx.Done()
		return []string{"tool_f"}, nil
	}})
	begin := time.Now()
	got := f.offered()
	if !reflect.DeepEqual(got, sorted("clarify", "tool_a", "tool_b", "tool_c")) || time.Since(begin) > 6*time.Second || started.Load() != 1 {
		t.Fatalf("offered %v after %v", got, time.Since(begin))
	}
}

func TestTheRunRecordsOnlyWhetherAdviceWasUsed(t *testing.T) {
	used := toolFixture(t, 4, &scriptedSelector{fn: pick("tool_f")})
	status := used.start("key-used-000001", "go", harnessrun.Budget{})
	used.waitState(status.ID, harnessrun.StateCompleted)
	if codes := used.eventCodes(status.ID); !contains(codes, "turn:tools_advice_used") || contains(codes, "turn:tools_advice_ignored") {
		t.Fatalf("events = %v", codes)
	}
	ignored := toolFixture(t, 4, &scriptedSelector{fn: pick("ghost")})
	status = ignored.start("key-ignored-0001", "go", harnessrun.Budget{})
	ignored.waitState(status.ID, harnessrun.StateCompleted)
	if codes := ignored.eventCodes(status.ID); contains(codes, "turn:tools_advice_used") || !contains(codes, "turn:tools_advice_ignored") {
		t.Fatalf("events = %v", codes)
	}
	none := toolFixture(t, 4, nil)
	status = none.start("key-none-0000001", "go", harnessrun.Budget{})
	none.waitState(status.ID, harnessrun.StateCompleted)
	if codes := none.eventCodes(status.ID); contains(codes, "turn:tools_advice_used") || contains(codes, "turn:tools_advice_ignored") {
		t.Fatalf("no selector, yet events = %v", codes)
	}
}

// decisionPicker is a decision provider for the service-backed selector: it selects the
// candidates at fixed positions.
type decisionPicker struct {
	positions []int
	outcome   harness.Outcome
	conf      float64
	questions atomic.Int32
}

func (p *decisionPicker) Probe(_ context.Context, scope harness.Scope) (harness.LiveAccess, error) {
	return harness.LiveAccess{Version: harness.ContractVersion, Provider: "tool-advice", Scope: scope, Revision: 1,
		Capabilities: map[harness.CapabilityID]harness.AccessState{"decide": harness.AccessAvailable}}, nil
}
func (p *decisionPicker) Close() error { return nil }
func (p *decisionPicker) Decide(_ context.Context, q harness.DecisionQuestion) (harness.DecisionAnswer, error) {
	p.questions.Add(1)
	outcome := p.outcome
	if outcome == "" {
		outcome = harness.OutcomeOK
	}
	var selected []string
	for _, i := range p.positions {
		if i < len(q.Candidates) {
			selected = append(selected, q.Candidates[i])
		}
	}
	return harness.DecisionAnswer{Envelope: q.Envelope, Outcome: outcome, Selected: selected, Confidence: p.conf}, nil
}

func serviceSelector(t *testing.T, p *decisionPicker, enabled ...harnessdecide.Kind) harnessrun.ServiceToolSelector {
	t.Helper()
	registry := harness.NewRegistry()
	manifest := harness.Manifest{Version: harness.ContractVersion, ID: "tool-advice", Kind: harness.KindDecision, Capabilities: []harness.CapabilityID{"decide"}}
	if err := registry.Register(manifest, func() (harness.Provider, error) { return p, nil }); err != nil {
		t.Fatal(err)
	}
	session, err := registry.OpenSession(context.Background(), "tool-advice", harness.Scope{Workspace: "agent-memory", Run: "run-1", Generation: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	if len(enabled) == 0 {
		enabled = harnessdecide.Kinds()
	}
	hub, err := harnessdecide.NewHub(harnessdecide.Config{Asker: session, Capability: "decide", Enabled: enabled}, 8)
	if err != nil {
		t.Fatal(err)
	}
	return harnessrun.ServiceToolSelector{For: hub.Service}
}

func TestTheDecisionServiceChoosesToolsThroughTheLoop(t *testing.T) {
	// The question lists the six tools by name: position 5 is tool_f, 0 is tool_a, 2 is tool_c.
	p := &decisionPicker{positions: []int{5, 0, 2}, conf: 0.9}
	f := toolFixture(t, 4, serviceSelector(t, p))
	if got := f.offered(); !reflect.DeepEqual(got, sorted("clarify", "tool_f", "tool_a", "tool_c")) {
		t.Fatalf("offered %v", got)
	}
	if p.questions.Load() != 1 {
		t.Fatalf("the provider was asked %d times", p.questions.Load())
	}
	for name, declining := range map[string]*decisionPicker{
		"low confidence": {positions: []int{4}, conf: 0.2},
		"a denial":       {positions: []int{4}, conf: 0.9, outcome: harness.OutcomeDenied},
		"too many":       {positions: []int{0, 1, 2, 3}, conf: 0.9},
	} {
		g := toolFixture(t, 4, serviceSelector(t, declining))
		if got := g.offered(); !reflect.DeepEqual(got, sorted("clarify", "tool_a", "tool_b", "tool_c")) {
			t.Errorf("%s: offered %v", name, got)
		}
	}
	off := toolFixture(t, 4, serviceSelector(t, &decisionPicker{positions: []int{4}, conf: 0.9}, harnessdecide.KindCache))
	if got := off.offered(); !reflect.DeepEqual(got, sorted("clarify", "tool_a", "tool_b", "tool_c")) {
		t.Errorf("with the kind off: offered %v", got)
	}
	if _, err := (harnessrun.ServiceToolSelector{}).Select(context.Background(), "run", sixTools, 3); err == nil {
		t.Error("a selector without a source of services chose tools")
	}
	if _, err := (harnessrun.ServiceToolSelector{For: func(string) *harnessdecide.Service { return nil }}).Select(context.Background(), "run", sixTools, 3); err == nil {
		t.Error("a selector without a service chose tools")
	}
}

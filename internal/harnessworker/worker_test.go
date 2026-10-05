package harnessworker

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessdecide"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessrun"
	"github.com/taimufuraiyaa/agent-memory/internal/harnesstools"
)

func TestClaimsAreAllOrNothingAndReleasedWithTheRun(t *testing.T) {
	c := NewClaims(0)
	if err := c.Claim("run-a", []string{"x.go", "y.go"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Claim("run-a", []string{"y.go", "z.go"}); err != nil {
		t.Fatalf("a run could not extend its own claim: %v", err)
	}
	if err := c.Claim("run-b", []string{"q.go", "y.go"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("a second owner: %v", err)
	}
	if _, held := c.Owner("q.go"); held {
		t.Fatal("a refused claim kept part of its paths")
	}
	if err := c.Claim("", []string{"a"}); err == nil {
		t.Fatal("a claim without a run")
	}
	if got := c.Held("run-a"); !reflect.DeepEqual(got, []string{"x.go", "y.go", "z.go"}) {
		t.Fatalf("held = %v", got)
	}
	c.Release("run-a")
	if err := c.Claim("run-b", []string{"y.go"}); err != nil || len(c.Held("run-a")) != 0 {
		t.Fatalf("after release: %v", err)
	}
	small := NewClaims(2)
	if err := small.Claim("r", []string{"a", "b", "c"}); err == nil {
		t.Fatal("the bound was ignored")
	}
	if err := small.Claim("r", []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	if err := small.Claim("r", []string{"a", "b"}); err != nil {
		t.Fatalf("re-claiming held paths counted twice: %v", err)
	}
}

func TestClaimsAreRaceFree(t *testing.T) {
	c := NewClaims(0)
	var wg sync.WaitGroup
	var winners atomic.Int32
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if c.Claim(fmt.Sprintf("run-%d", i), []string{"shared.go"}) == nil {
				winners.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatalf("%d runs own one file", winners.Load())
	}
}

type basePolicy struct{ decision harnessrun.Decision }

func (b basePolicy) Decide(context.Context, harnessrun.Owner, harness.PreparedAction) harnessrun.Decision {
	return b.decision
}
func (b basePolicy) Reasons(harness.PreparedAction) []string { return []string{"from_base"} }

func prepared(run, capability string, paths ...string) harness.PreparedAction {
	return harness.PreparedAction{Envelope: harness.Envelope{Capability: harness.CapabilityID(capability), Scope: harness.Scope{Workspace: "ws", Run: run, Generation: 1}},
		Outcome: harness.OutcomeOK, Digest: "sha256:" + strings.Repeat("a", 64), Paths: paths}
}

func TestOwnershipOnlyEverDeniesAndOnlyForFilesAnotherRunOwns(t *testing.T) {
	claims := NewClaims(0)
	owner := harnessrun.Owner{}
	for _, base := range []harnessrun.Decision{harnessrun.DecisionAllow, harnessrun.DecisionAsk, harnessrun.DecisionAskStrict, harnessrun.DecisionDeny} {
		p := OwnershipPolicy{Base: basePolicy{base}, Claims: claims}
		first := p.Decide(context.Background(), owner, prepared("run-a", "edit_file", "a.go"))
		if first != base {
			t.Errorf("base %v: an unowned file changed the decision to %v", base, first)
		}
		second := p.Decide(context.Background(), owner, prepared("run-b", "edit_file", "a.go"))
		want := harnessrun.DecisionDeny
		if base == harnessrun.DecisionDeny {
			want = base
		}
		if second != want {
			t.Errorf("base %v: a file owned by another run decided %v", base, second)
		}
		claims.Release("run-a")
		claims.Release("run-b")
	}
	p := OwnershipPolicy{Base: basePolicy{harnessrun.DecisionAsk}, Claims: claims}
	p.Decide(context.Background(), owner, prepared("run-a", "edit_file", "a.go"))
	for name, a := range map[string]harness.PreparedAction{
		"a read":             prepared("run-b", "read_file", "a.go"),
		"a command":          prepared("run-b", "run_command", "a.go"),
		"an action no paths": prepared("run-b", "edit_file"),
	} {
		if got := p.Decide(context.Background(), owner, a); got != harnessrun.DecisionAsk {
			t.Errorf("%s was decided %v", name, got)
		}
	}
	if !reflect.DeepEqual(p.Reasons(prepared("run-a", "edit_file")), []string{"from_base"}) {
		t.Error("the base policy's reasons were lost")
	}
	if got := (OwnershipPolicy{Base: basePolicy{harnessrun.DecisionAsk}}).Decide(context.Background(), owner, prepared("run-a", "edit_file", "a.go")); got != harnessrun.DecisionAsk {
		t.Errorf("without a registry: %v", got)
	}
	if !harnesstools.Mutating(harnesstools.ToolEditFile) {
		t.Fatal("test premise: edit is mutating")
	}
}

func TestTheSnapshotFixesTheFirstRevisionAndReportsChanges(t *testing.T) {
	s := NewSnapshot(2)
	if s.Observe("a.go", "r1") || s.Observe("a.go", "r1") {
		t.Fatal("a repeat read was reported as a change")
	}
	if !s.Observe("a.go", "r2") {
		t.Fatal("a changed file was not reported")
	}
	if rev, ok := s.Revision("a.go"); !ok || rev != "r1" {
		t.Fatalf("revision = %q %v", rev, ok)
	}
	s.Observe("b.go", "r1")
	if s.Observe("c.go", "r1") || s.Len() != 2 {
		t.Fatalf("the bound was ignored: %d", s.Len())
	}
	if _, ok := s.Revision("c.go"); ok {
		t.Fatal("a file over the bound was recorded")
	}
}

func TestSubgoalsThatRepeatAreMergedAndOnlyRemoveWork(t *testing.T) {
	c, _ := New(Config{Runs: &fakeRuns{}})
	keep, merged, err := c.Plan(context.Background(), []Subgoal{
		{"a", "Fix the parser!"}, {"b", "  fix   the parser"}, {"c", "Add tests"}, {"d", "FIX THE PARSER."}})
	if err != nil || len(keep) != 2 || keep[0].Key != "a" || keep[1].Key != "c" || !reflect.DeepEqual(merged, map[string]string{"b": "a", "d": "a"}) {
		t.Fatalf("%v %v %v", keep, merged, err)
	}
	for name, in := range map[string][]Subgoal{
		"none":        nil,
		"too many":    make([]Subgoal, MaxSubgoals+1),
		"no key":      {{"", "x"}},
		"no goal":     {{"a", "  !! "}},
		"a duplicate": {{"a", "x"}, {"a", "y"}},
	} {
		if _, _, err := c.Plan(context.Background(), in); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

type picker struct {
	positions []int
	questions atomic.Int32
}

func (p *picker) Probe(_ context.Context, scope harness.Scope) (harness.LiveAccess, error) {
	return harness.LiveAccess{Version: harness.ContractVersion, Provider: "dedup", Scope: scope, Revision: 1, Capabilities: map[harness.CapabilityID]harness.AccessState{"decide": harness.AccessAvailable}}, nil
}
func (p *picker) Close() error { return nil }
func (p *picker) Decide(_ context.Context, q harness.DecisionQuestion) (harness.DecisionAnswer, error) {
	p.questions.Add(1)
	var sel []string
	for _, i := range p.positions {
		if i < len(q.Candidates) {
			sel = append(sel, q.Candidates[i])
		}
	}
	return harness.DecisionAnswer{Envelope: q.Envelope, Outcome: harness.OutcomeOK, Selected: sel, Confidence: 0.95}, nil
}

func decisions(t *testing.T, p *picker) *harnessdecide.Service {
	t.Helper()
	registry := harness.NewRegistry()
	manifest := harness.Manifest{Version: harness.ContractVersion, ID: "dedup", Kind: harness.KindDecision, Capabilities: []harness.CapabilityID{"decide"}}
	if err := registry.Register(manifest, func() (harness.Provider, error) { return p, nil }); err != nil {
		t.Fatal(err)
	}
	session, err := registry.OpenSession(context.Background(), "dedup", harness.Scope{Workspace: "ws", Run: "r", Generation: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	s, err := harnessdecide.New(harnessdecide.Config{Asker: session, Capability: "decide", MaxClass: harnessdecide.ClassInternal, Enabled: []harnessdecide.Kind{harnessdecide.KindSubgoals}})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestAdviceMayMergeANearRepeatButNeverAddOrReorderWork(t *testing.T) {
	// With one kept subgoal the candidates are it (position 0) and "none" (position 1).
	merge := &picker{positions: []int{0}}
	c, _ := New(Config{Runs: &fakeRuns{}, Decisions: decisions(t, merge), Redact: func(s string) string { return strings.ReplaceAll(s, "hunter2", "[X]") }})
	keep, merged, err := c.Plan(context.Background(), []Subgoal{{"a", "tidy the parser"}, {"b", "clean up the parser hunter2"}, {"c", "write docs"}})
	if err != nil || len(keep) != 1 || !reflect.DeepEqual(merged, map[string]string{"b": "a", "c": "a"}) {
		t.Fatalf("%v %v %v", keep, merged, err)
	}
	none := &picker{positions: []int{1}}
	c, _ = New(Config{Runs: &fakeRuns{}, Decisions: decisions(t, none)})
	keep, merged, _ = c.Plan(context.Background(), []Subgoal{{"a", "one thing"}, {"b", "another thing"}})
	if len(keep) != 2 || len(merged) != 0 {
		t.Fatalf("%v %v", keep, merged)
	}
	if none.questions.Load() != 1 {
		t.Fatalf("asked %d times", none.questions.Load())
	}
	// The first subgoal has nothing to repeat, so it is never asked about.
	first := &picker{positions: []int{0}}
	c, _ = New(Config{Runs: &fakeRuns{}, Decisions: decisions(t, first)})
	c.Plan(context.Background(), []Subgoal{{"a", "only one"}})
	if first.questions.Load() != 0 {
		t.Fatal("a lone subgoal was sent for advice")
	}
}

func TestTheParentsRemainingBudgetIsSplitAndNeverExceeded(t *testing.T) {
	parent := harnessrun.Status{State: harnessrun.StateRunning, Budget: harnessrun.Budget{MaxTurns: 20, MaxActive: 10 * time.Minute, MaxToolCalls: 40, MaxOutputBytes: 1000, MaxSpendMicros: 1_000_000, MaxDepth: 2},
		Usage: harnessrun.Usage{Turns: 4, ToolCalls: 0, OutputBytes: 200, SpendMicros: 200_000}}
	share, err := split(parent, 4, 2)
	if err != nil || share.MaxTurns != 4 || share.MaxToolCalls != 10 || share.MaxOutputBytes != 200 || share.MaxSpendMicros != 200_000 || share.MaxActive != 5*time.Minute || share.MaxDepth != 2 {
		t.Fatalf("%+v %v", share, err)
	}
	if share.MaxTurns*4 > parent.Budget.MaxTurns-parent.Usage.Turns || share.MaxSpendMicros*4 > parent.Budget.MaxSpendMicros-parent.Usage.SpendMicros {
		t.Fatal("the workers together exceed what the parent had left")
	}
	for name, mutate := range map[string]func(*harnessrun.Status){
		"a parent not running": func(s *harnessrun.Status) { s.State = harnessrun.StateCompleted },
		"no turns left":        func(s *harnessrun.Status) { s.Usage.Turns = 20 },
		"no spend left":        func(s *harnessrun.Status) { s.Usage.SpendMicros = 1_000_000 },
		"no tool calls":        func(s *harnessrun.Status) { s.Usage.ToolCalls = 40 },
		"no output":            func(s *harnessrun.Status) { s.Usage.OutputBytes = 1000 },
		"almost no time":       func(s *harnessrun.Status) { s.Usage.ActiveMillis = 10*60*1000 - 500 },
	} {
		p := parent
		mutate(&p)
		if _, err := split(p, 4, 2); err == nil {
			t.Errorf("%s was split", name)
		}
	}
	if childKey("p", "a") == childKey("p", "b") || childKey("p", "a") == childKey("q", "a") || !strings.HasPrefix(childKey("p", "a"), "worker-") {
		t.Fatal("child keys are not distinct")
	}
}

// fakeRuns scripts a manager: children end as the test says.
type fakeRuns struct {
	mu       sync.Mutex
	parent   harnessrun.Status
	children map[string]*harnessrun.Status
	order    []string
	started  []harnessrun.StartRequest
	cancels  []string
	active   int
	maxSeen  int
	finish   func(goal string) (harnessrun.State, string)
	hold     chan struct{}
	failNth  int
	calls    int
}

func newFake() *fakeRuns {
	return &fakeRuns{parent: harnessrun.Status{ID: "run_parent", State: harnessrun.StateRunning, Generation: 1,
		Budget: harnessrun.Budget{MaxTurns: 20, MaxActive: 10 * time.Minute, MaxToolCalls: 40, MaxOutputBytes: 1 << 20, MaxSpendMicros: 1_000_000, MaxDepth: 2}},
		children: map[string]*harnessrun.Status{}, finish: func(string) (harnessrun.State, string) { return harnessrun.StateCompleted, "completed" }}
}

func (f *fakeRuns) Status(_ context.Context, _ harnessrun.Owner, id string) (harnessrun.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id == f.parent.ID {
		return f.parent, nil
	}
	c, ok := f.children[id]
	if !ok {
		return harnessrun.Status{}, harnessrun.ErrNotFound
	}
	if c.State == harnessrun.StateRunning && f.hold == nil {
		c.State, c.Code = f.finish(f.goalOf(id))
		f.active--
	}
	return *c, nil
}

func (f *fakeRuns) goalOf(id string) string {
	for i, sid := range f.order {
		if sid == id {
			return f.started[i].Goal
		}
	}
	return ""
}

func (f *fakeRuns) StartChild(_ context.Context, _ harnessrun.Owner, parent string, req harnessrun.StartRequest) (harnessrun.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if parent != f.parent.ID || f.parent.State != harnessrun.StateRunning {
		return harnessrun.Status{}, harnessrun.ErrInvalidState
	}
	f.calls++
	if f.failNth > 0 && f.calls == f.failNth {
		return harnessrun.Status{}, harnessrun.ErrBudget
	}
	id := fmt.Sprintf("run_child%d", len(f.started))
	f.started = append(f.started, req)
	f.order = append(f.order, id)
	f.children[id] = &harnessrun.Status{ID: id, State: harnessrun.StateRunning, Generation: 1, Budget: req.Budget}
	f.active++
	f.maxSeen = max(f.maxSeen, f.active)
	return *f.children[id], nil
}

func (f *fakeRuns) Cancel(_ context.Context, _ harnessrun.Owner, id string, _ harnessrun.Mutation) (harnessrun.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancels = append(f.cancels, id)
	if c, ok := f.children[id]; ok && !c.State.Terminal() {
		c.State, c.Code = harnessrun.StateCancelled, "cancelled_by_client"
		f.active--
	}
	return harnessrun.Status{}, nil
}

func TestWorkersRunWithinTheirShareAndTheResultsCarryOnlyCodes(t *testing.T) {
	f := newFake()
	f.finish = func(goal string) (harnessrun.State, string) {
		if strings.Contains(goal, "breaks") {
			return harnessrun.StateFailed, "worker_failed"
		}
		return harnessrun.StateCompleted, "completed"
	}
	c, _ := New(Config{Runs: f, MaxParallel: 2, Poll: time.Millisecond})
	results, err := c.Run(context.Background(), harnessrun.Owner{}, "run_parent", []Subgoal{{"a", "do a"}, {"b", "do b breaks"}, {"c", "do a"}, {"d", "do d"}})
	if err != nil || len(results) != 4 {
		t.Fatalf("%v %v", results, err)
	}
	byKey := map[string]Result{}
	for _, r := range results {
		byKey[r.Key] = r
	}
	if byKey["a"].State != harnessrun.StateCompleted || byKey["b"].State != harnessrun.StateFailed || byKey["b"].Code != "worker_failed" || byKey["c"].Merged != "a" || byKey["c"].RunID != "" {
		t.Fatalf("results = %+v", byKey)
	}
	if len(f.started) != 3 || f.maxSeen > 2 {
		t.Fatalf("%d children started, %d at once", len(f.started), f.maxSeen)
	}
	for _, req := range f.started {
		if req.Budget.MaxTurns != 6 || req.Budget.MaxSpendMicros != 333_333 || !strings.HasPrefix(req.IdempotencyKey, "worker-") {
			t.Fatalf("a child's request = %+v", req)
		}
	}
	if (&Coordinator{}).Claims() != nil || c.Claims() == nil || c.Snapshot() == nil {
		t.Fatal("the coordinator does not expose its registries")
	}
}

func TestParallelismOfOneSerializesTheWork(t *testing.T) {
	f := newFake()
	c, _ := New(Config{Runs: f, MaxParallel: 1, Poll: time.Millisecond})
	if _, err := c.Run(context.Background(), harnessrun.Owner{}, "run_parent", []Subgoal{{"a", "one"}, {"b", "two"}, {"c", "three"}}); err != nil {
		t.Fatal(err)
	}
	if f.maxSeen != 1 || len(f.started) != 3 {
		t.Fatalf("%d at once, %d started", f.maxSeen, len(f.started))
	}
	if c2, _ := New(Config{Runs: f, MaxParallel: 99}); c2.cfg.MaxParallel != MaxSubgoals {
		t.Fatalf("parallelism = %d", c2.cfg.MaxParallel)
	}
	if c3, _ := New(Config{Runs: f}); c3.cfg.MaxParallel != 1 {
		t.Fatalf("default parallelism = %d", c3.cfg.MaxParallel)
	}
	if _, err := New(Config{}); err == nil {
		t.Fatal("a coordinator without runs")
	}
}

func TestAWorkerThatCannotStartIsReportedAndNotRetried(t *testing.T) {
	f := newFake()
	f.failNth = 2
	c, _ := New(Config{Runs: f, MaxParallel: 1, Poll: time.Millisecond})
	results, err := c.Run(context.Background(), harnessrun.Owner{}, "run_parent", []Subgoal{{"a", "one"}, {"b", "two"}, {"c", "three"}})
	if err != nil {
		t.Fatal(err)
	}
	failed := 0
	for _, r := range results {
		if r.Code == "worker_not_started" && r.State == harnessrun.StateFailed && r.RunID == "" {
			failed++
		}
	}
	if failed != 1 || len(f.started) != 2 {
		t.Fatalf("%d failed to start, %d started: %+v", failed, len(f.started), results)
	}
	// A parent that is not running cannot have workers.
	f.parent.State = harnessrun.StateCompleted
	if _, err := c.Run(context.Background(), harnessrun.Owner{}, "run_parent", []Subgoal{{"a", "one"}}); err == nil {
		t.Fatal("workers were started under a finished parent")
	}
}

func TestWorkersStopWhenTheParentStops(t *testing.T) {
	f := newFake()
	f.hold = make(chan struct{}) // children never finish on their own
	c, _ := New(Config{Runs: f, MaxParallel: 3, Poll: time.Millisecond})
	done := make(chan []Result, 1)
	go func() {
		r, _ := c.Run(context.Background(), harnessrun.Owner{}, "run_parent", []Subgoal{{"a", "one"}, {"b", "two"}, {"c", "three"}})
		done <- r
	}()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(2 * time.Millisecond) {
		f.mu.Lock()
		n := len(f.started)
		f.mu.Unlock()
		if n == 3 {
			break
		}
	}
	f.mu.Lock()
	f.parent.State = harnessrun.StateCancelling
	f.mu.Unlock()
	select {
	case results := <-done:
		for _, r := range results {
			if r.State != harnessrun.StateCancelled {
				t.Errorf("a worker outlived its parent: %+v", r)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the workers did not stop with the parent")
	}
	if len(f.cancels) != 3 {
		t.Fatalf("cancelled %v", f.cancels)
	}
}

func TestNormalizeAndClipNote(t *testing.T) {
	for in, want := range map[string]string{"  Fix the  Parser! ": "fix the parser", "": "", "!!!": "", "A-b_c 9": "a b c 9", "é Ü": "é ü"} {
		if got := normalize(in); got != want {
			t.Errorf("normalize(%q) = %q, want %q", in, got, want)
		}
	}
	if clipNote("   ") != "subgoal" || clipNote("a   b") != "a b" {
		t.Error("clipNote spacing")
	}
	long := strings.Repeat("é", 150)
	if got := clipNote(long); len(got) > 200 || !strings.HasPrefix(long, got) {
		t.Errorf("clipNote cut a character: %d bytes", len(got))
	}
}

package harnessrun_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
	"github.com/taimufuraiyaa/agent-memory/internal/harness/harnesstest"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessrun"
)

type policyFunc func(harnessrun.Owner, harness.PreparedAction) harnessrun.Decision

func (p policyFunc) Decide(_ context.Context, o harnessrun.Owner, a harness.PreparedAction) harnessrun.Decision {
	return p(o, a)
}

var (
	allow = policyFunc(func(harnessrun.Owner, harness.PreparedAction) harnessrun.Decision { return harnessrun.DecisionAllow })
	ask   = policyFunc(func(harnessrun.Owner, harness.PreparedAction) harnessrun.Decision { return harnessrun.DecisionAsk })
)

type sink struct {
	mu    sync.Mutex
	lines []string
}

func (s *sink) RecordStep(_ context.Context, workspace, runID, kind, status, summary string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lines = append(s.lines, workspace+"|"+runID+"|"+kind+"|"+status+"|"+summary)
	return nil
}

type fixture struct {
	t        *testing.T
	dir      string
	registry *harness.Registry
	model    *harnesstest.Counters
	tool     *harnesstest.Counters
	cfg      harnessrun.Config
	m        *harnessrun.Manager
	owner    harnessrun.Owner
}

type opts struct {
	model  harnesstest.Behavior
	tool   *harnesstest.Behavior
	policy harnessrun.ToolPolicy
	edit   func(*harnessrun.Config)
}

func owner(client, workspace string) harnessrun.Owner {
	return harnessrun.Owner{ClientID: client, Workspace: workspace, GrantID: strings.Repeat("b", 32), GrantRevision: 1}
}

func newFixture(t *testing.T, o opts) *fixture {
	t.Helper()
	f := &fixture{t: t, dir: t.TempDir(), registry: harness.NewRegistry(), owner: owner("claude-desktop", "agent-memory")}
	var err error
	if f.model, err = harnesstest.Register(f.registry, harnesstest.Manifest("fake-model", harness.KindModel, "generation"), o.model); err != nil {
		t.Fatal(err)
	}
	toolBehavior := harnesstest.Behavior{}
	if o.tool != nil {
		toolBehavior = *o.tool
	}
	if f.tool, err = harnesstest.Register(f.registry, harnesstest.Manifest("fake-tool", harness.KindTool, "read"), toolBehavior); err != nil {
		t.Fatal(err)
	}
	f.cfg = harnessrun.Config{DataDir: f.dir, Registry: f.registry,
		Model:  harnessrun.Binding{Provider: "fake-model", Capability: "generation"},
		Tool:   &harnessrun.Binding{Provider: "fake-tool", Capability: "read"},
		Policy: o.policy}
	if o.edit != nil {
		o.edit(&f.cfg)
	}
	f.m, err = harnessrun.NewManager(f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.m.Close() })
	return f
}

func (f *fixture) restart() {
	f.t.Helper()
	_ = f.m.Close()
	m, err := harnessrun.NewManager(f.cfg)
	if err != nil {
		f.t.Fatal(err)
	}
	f.m = m
	f.t.Cleanup(func() { _ = m.Close() })
}

func (f *fixture) start(key, goal string, budget harnessrun.Budget) harnessrun.Status {
	f.t.Helper()
	status, err := f.m.Start(context.Background(), f.owner, harnessrun.StartRequest{IdempotencyKey: key, Goal: goal, Budget: budget})
	if err != nil {
		f.t.Fatal(err)
	}
	return status
}

func (f *fixture) status(id string) harnessrun.Status {
	f.t.Helper()
	status, err := f.m.Status(context.Background(), f.owner, id)
	if err != nil {
		f.t.Fatal(err)
	}
	return status
}

func (f *fixture) wait(id string, done func(harnessrun.Status) bool) harnessrun.Status {
	f.t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for {
		status := f.status(id)
		if done(status) {
			return status
		}
		if time.Now().After(deadline) {
			f.t.Fatalf("timed out; last status %+v", status)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (f *fixture) waitState(id string, states ...harnessrun.State) harnessrun.Status {
	f.t.Helper()
	return f.wait(id, func(s harnessrun.Status) bool {
		for _, state := range states {
			if s.State == state {
				return true
			}
		}
		return false
	})
}

func (f *fixture) eventCodes(id string) []string {
	f.t.Helper()
	var codes []string
	cursor := ""
	for {
		page, err := f.m.Events(context.Background(), f.owner, id, cursor, 100)
		if err != nil {
			f.t.Fatal(err)
		}
		for _, e := range page.Events {
			codes = append(codes, e.Kind+":"+e.Code)
		}
		if len(page.Events) == 0 {
			return codes
		}
		cursor = page.Next
	}
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

func TestRunCompletesAsynchronouslyWithContentMinimizedStatus(t *testing.T) {
	f := newFixture(t, opts{model: harnesstest.Behavior{Delay: 300 * time.Millisecond, CostMicros: 100, Script: []harnesstest.Step{{Text: "the final answer"}}}})
	began := time.Now()
	started := f.start("key-00000001", "unique-goal-text-xyz", harnessrun.Budget{})
	if time.Since(began) > 150*time.Millisecond || started.State.Terminal() || started.Generation != 1 {
		t.Fatalf("start was not prompt and asynchronous: %v %+v", time.Since(began), started)
	}
	done := f.waitState(started.ID, harnessrun.StateCompleted)
	if done.Usage.Turns != 1 || done.Usage.SpendMicros != 100 || done.Turn != 1 || len(done.Artifacts) != 1 || done.Artifacts[0].Kind != "result" ||
		done.Artifacts[0].Bytes != len("the final answer") || len(done.Artifacts[0].SHA256) != 64 || done.Code != "completed" {
		t.Fatalf("done = %+v", done)
	}
	codes := f.eventCodes(started.ID)
	for _, want := range []string{"state:queued", "state:started", "turn:model_reply", "artifact:result", "state:completed"} {
		if !contains(codes, want) {
			t.Errorf("events %v lack %s", codes, want)
		}
	}
	encoded, _ := json.Marshal([]any{done, codes})
	if strings.Contains(string(encoded), "unique-goal-text-xyz") || strings.Contains(string(encoded), "the final answer") {
		t.Fatalf("status or events carried run content: %s", encoded)
	}
}

func TestToolLoopRunsAllowedActionsAndDeniesByDefault(t *testing.T) {
	script := []harnesstest.Step{{ToolID: "read", Arguments: `{"path":"a.go"}`}, {Text: "done"}}
	allowed := newFixture(t, opts{model: harnesstest.Behavior{Script: script}, policy: allow})
	id := allowed.start("key-00000001", "read a file", harnessrun.Budget{}).ID
	done := allowed.waitState(id, harnessrun.StateCompleted)
	if allowed.tool.Invokes.Load() != 1 || done.Usage.ToolCalls != 1 || done.Usage.OutputBytes < len("tool output") {
		t.Fatalf("allowed: invokes=%d status=%+v", allowed.tool.Invokes.Load(), done)
	}

	denied := newFixture(t, opts{model: harnesstest.Behavior{Script: script}})
	id = denied.start("key-00000001", "read a file", harnessrun.Budget{}).ID
	done = denied.waitState(id, harnessrun.StateCompleted)
	if denied.tool.Invokes.Load() != 0 || done.Usage.ToolCalls != 1 || !contains(denied.eventCodes(id), "tool:tool_denied") {
		t.Fatalf("default policy ran a tool: invokes=%d status=%+v events=%v", denied.tool.Invokes.Load(), done, denied.eventCodes(id))
	}
}

func TestApprovalIsNeverSatisfiedByContinue(t *testing.T) {
	f := newFixture(t, opts{model: harnesstest.Behavior{Script: []harnesstest.Step{{ToolID: "read", Arguments: "{}"}, {Text: "done"}}}, policy: ask})
	id := f.start("key-00000001", "needs approval", harnessrun.Budget{}).ID
	parked := f.waitState(id, harnessrun.StateNeedsAttention)
	if parked.Attention == nil || parked.Attention.Kind != harnessrun.AttentionApproval || parked.Attention.ActionDigest != "digest-read" || parked.Attention.Prompt != "" {
		t.Fatalf("attention = %+v", parked.Attention)
	}
	_, err := f.m.Continue(context.Background(), f.owner, id, harnessrun.Mutation{IdempotencyKey: "cont-00000001", ExpectedGeneration: parked.Generation}, "yes, approved")
	if !errors.Is(err, harnessrun.ErrApprovalRequired) {
		t.Fatalf("continue satisfied an approval: %v", err)
	}
	if f.tool.Invokes.Load() != 0 || f.status(id).State != harnessrun.StateNeedsAttention {
		t.Fatal("the action ran or the run moved without trusted approval")
	}
	cancelled, err := f.m.Cancel(context.Background(), f.owner, id, harnessrun.Mutation{IdempotencyKey: "canc-00000001", ExpectedGeneration: parked.Generation})
	if err != nil || cancelled.State != harnessrun.StateCancelled {
		t.Fatalf("cancel of a parked run = %+v, %v", cancelled, err)
	}
}

func TestClarificationRoundTripIsIdempotentAndGenerationChecked(t *testing.T) {
	f := newFixture(t, opts{model: harnesstest.Behavior{Script: []harnesstest.Step{{ToolID: "clarify", Text: "Which file?"}, {Text: "done"}}}})
	id := f.start("key-00000001", "ambiguous goal", harnessrun.Budget{}).ID
	parked := f.waitState(id, harnessrun.StateNeedsAttention)
	if parked.Attention == nil || parked.Attention.Kind != harnessrun.AttentionClarification || parked.Attention.Prompt != "Which file?" {
		t.Fatalf("attention = %+v", parked.Attention)
	}
	mutation := harnessrun.Mutation{IdempotencyKey: "cont-00000001", ExpectedGeneration: parked.Generation}
	if _, err := f.m.Continue(context.Background(), f.owner, id, harnessrun.Mutation{IdempotencyKey: "cont-stale001", ExpectedGeneration: parked.Generation - 1}, "main.go"); !errors.Is(err, harnessrun.ErrStaleGeneration) {
		t.Fatalf("stale generation = %v", err)
	}
	if _, err := f.m.Continue(context.Background(), f.owner, id, mutation, "   "); !errors.Is(err, harnessrun.ErrInvalid) {
		t.Fatalf("blank input = %v", err)
	}
	if _, err := f.m.Continue(context.Background(), f.owner, id, mutation, "main.go"); err != nil {
		t.Fatal(err)
	}
	done := f.waitState(id, harnessrun.StateCompleted)
	if f.model.Calls.Load() != 2 || done.Usage.Turns != 2 {
		t.Fatalf("calls=%d status=%+v", f.model.Calls.Load(), done)
	}
	replay, err := f.m.Continue(context.Background(), f.owner, id, mutation, "main.go")
	if err != nil || replay.State != harnessrun.StateCompleted {
		t.Fatalf("replay = %+v, %v", replay, err)
	}
	if _, err := f.m.Continue(context.Background(), f.owner, id, mutation, "a different answer"); !errors.Is(err, harnessrun.ErrIdempotencyConflict) {
		t.Fatalf("reused key with another request = %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if f.model.Calls.Load() != 2 {
		t.Fatalf("a replay ran the model again: %d calls", f.model.Calls.Load())
	}
	if _, err := f.m.Continue(context.Background(), f.owner, id, harnessrun.Mutation{IdempotencyKey: "cont-00000002", ExpectedGeneration: done.Generation}, "x"); !errors.Is(err, harnessrun.ErrInvalidState) {
		t.Fatalf("continue on a finished run = %v", err)
	}
}

func TestStartIdempotencyAndValidation(t *testing.T) {
	f := newFixture(t, opts{model: harnesstest.Behavior{Delay: 100 * time.Millisecond, Script: []harnesstest.Step{{Text: "ok"}}}})
	first := f.start("key-00000001", "goal one", harnessrun.Budget{})
	again := f.start("key-00000001", "goal one", harnessrun.Budget{})
	if again.ID != first.ID || !again.Deduplicated || first.Deduplicated {
		t.Fatalf("retry = %+v original = %+v", again, first)
	}
	if _, err := f.m.Start(context.Background(), f.owner, harnessrun.StartRequest{IdempotencyKey: "key-00000001", Goal: "goal two"}); !errors.Is(err, harnessrun.ErrIdempotencyConflict) {
		t.Fatalf("same key, different goal = %v", err)
	}
	other, err := f.m.Start(context.Background(), owner("codex-cli", "agent-memory"), harnessrun.StartRequest{IdempotencyKey: "key-00000001", Goal: "goal one"})
	if err != nil || other.ID == first.ID {
		t.Fatalf("another client's identical key collided: %+v, %v", other, err)
	}
	for name, req := range map[string]harnessrun.StartRequest{
		"short key":     {IdempotencyKey: "short", Goal: "g"},
		"bad key chars": {IdempotencyKey: "key with spaces!", Goal: "g"},
		"empty goal":    {IdempotencyKey: "key-00000002", Goal: ""},
		"control goal":  {IdempotencyKey: "key-00000003", Goal: "\x00\x01"},
	} {
		if _, err := f.m.Start(context.Background(), f.owner, req); !errors.Is(err, harnessrun.ErrInvalid) {
			t.Errorf("%s = %v", name, err)
		}
	}
	if _, err := f.m.Start(context.Background(), f.owner, harnessrun.StartRequest{IdempotencyKey: "key-00000004", Goal: "g", Budget: harnessrun.Budget{MaxTurns: 9999}}); !errors.Is(err, harnessrun.ErrBudget) {
		t.Fatalf("budget over the ceiling = %v", err)
	}
	if _, err := f.m.Start(context.Background(), owner("claude-desktop", "Agent_Memory"), harnessrun.StartRequest{IdempotencyKey: "key-00000005", Goal: "g"}); !errors.Is(err, harnessrun.ErrInvalid) {
		t.Fatalf("workspace the harness cannot scope = %v", err)
	}
	if _, err := f.m.Start(context.Background(), harnessrun.Owner{}, harnessrun.StartRequest{IdempotencyKey: "key-00000006", Goal: "g"}); !errors.Is(err, harnessrun.ErrInvalid) {
		t.Fatalf("empty owner = %v", err)
	}
}

func TestConcurrentStartsWithOneKeyCreateOneRun(t *testing.T) {
	f := newFixture(t, opts{model: harnesstest.Behavior{Delay: 50 * time.Millisecond, Script: []harnesstest.Step{{Text: "ok"}}}})
	var wg sync.WaitGroup
	ids := make(chan string, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status, err := f.m.Start(context.Background(), f.owner, harnessrun.StartRequest{IdempotencyKey: "key-concurrent", Goal: "same goal"})
			if err != nil {
				t.Error(err)
				return
			}
			ids <- status.ID
		}()
	}
	wg.Wait()
	close(ids)
	seen := map[string]bool{}
	for id := range ids {
		seen[id] = true
	}
	files, _ := filepath.Glob(filepath.Join(f.dir, "harness", "runs", "*.json"))
	if len(seen) != 1 || len(files) != 1 {
		t.Fatalf("distinct runs = %d, files = %d", len(seen), len(files))
	}
}

func TestCrossClientAndWorkspaceCannotSeeOrTouchARun(t *testing.T) {
	f := newFixture(t, opts{model: harnesstest.Behavior{Delay: time.Second, Script: []harnesstest.Step{{Text: "ok"}}}})
	id := f.start("key-00000001", "private goal", harnessrun.Budget{}).ID
	missing := "run_" + strings.Repeat("0", 32)
	ctx := context.Background()
	for _, who := range []harnessrun.Owner{owner("codex-cli", "agent-memory"), owner("claude-desktop", "other-project")} {
		var baseline string
		for _, target := range []string{id, missing} {
			mutation := harnessrun.Mutation{IdempotencyKey: "key-isolation", ExpectedGeneration: 1}
			_, e1 := f.m.Status(ctx, who, target)
			_, e2 := f.m.Events(ctx, who, target, "", 10)
			_, e3 := f.m.Cancel(ctx, who, target, mutation)
			_, e4 := f.m.Continue(ctx, who, target, mutation, "hello")
			_, e5 := f.m.StartChild(ctx, who, target, harnessrun.StartRequest{IdempotencyKey: "key-child-001", Goal: "g"})
			for _, err := range []error{e1, e2, e3, e4, e5} {
				if !errors.Is(err, harnessrun.ErrNotFound) {
					t.Fatalf("%s sees run %s: %v", who.ClientID+"/"+who.Workspace, target, err)
				}
				if baseline == "" {
					baseline = err.Error()
				}
				if err.Error() != baseline {
					t.Fatalf("existence is distinguishable: %q vs %q", err, baseline)
				}
			}
		}
	}
	if f.status(id).State.Terminal() {
		t.Fatal("an unauthorized call changed the run")
	}
}

func TestCancelRunningPropagatesToProvidersAndIsIdempotent(t *testing.T) {
	f := newFixture(t, opts{model: harnesstest.Behavior{Delay: 10 * time.Second, Script: []harnesstest.Step{{Text: "never"}}}})
	id := f.start("key-00000001", "slow goal", harnessrun.Budget{}).ID
	running := f.waitState(id, harnessrun.StateRunning)
	ctx := context.Background()
	if _, err := f.m.Cancel(ctx, f.owner, id, harnessrun.Mutation{IdempotencyKey: "canc-stale001", ExpectedGeneration: running.Generation - 1}); !errors.Is(err, harnessrun.ErrStaleGeneration) {
		t.Fatalf("stale cancel = %v", err)
	}
	if f.status(id).State != harnessrun.StateRunning {
		t.Fatal("a stale cancel changed the run")
	}
	mutation := harnessrun.Mutation{IdempotencyKey: "canc-00000001", ExpectedGeneration: running.Generation}
	began := time.Now()
	if _, err := f.m.Cancel(ctx, f.owner, id, mutation); err != nil {
		t.Fatal(err)
	}
	done := f.waitState(id, harnessrun.StateCancelled)
	if time.Since(began) > 2*time.Second || f.model.Closes.Load() < 1 || done.Code != "cancelled" {
		t.Fatalf("cancellation was slow or left a provider open: %v closes=%d %+v", time.Since(began), f.model.Closes.Load(), done)
	}
	codes := f.eventCodes(id)
	if !contains(codes, "state:cancel_requested") || !contains(codes, "state:cancelled") {
		t.Fatalf("events = %v", codes)
	}
	if replay, err := f.m.Cancel(ctx, f.owner, id, mutation); err != nil || replay.State != harnessrun.StateCancelled {
		t.Fatalf("replay = %+v, %v", replay, err)
	}
	if _, err := f.m.Cancel(ctx, f.owner, id, harnessrun.Mutation{IdempotencyKey: "canc-00000002", ExpectedGeneration: done.Generation}); !errors.Is(err, harnessrun.ErrInvalidState) {
		t.Fatalf("cancel of a finished run = %v", err)
	}
	if _, err := f.m.Cancel(ctx, f.owner, id, harnessrun.Mutation{IdempotencyKey: "bad", ExpectedGeneration: 1}); !errors.Is(err, harnessrun.ErrInvalid) {
		t.Fatalf("bad key = %v", err)
	}
	if _, err := f.m.Cancel(ctx, f.owner, id, harnessrun.Mutation{IdempotencyKey: "canc-00000003"}); !errors.Is(err, harnessrun.ErrInvalid) {
		t.Fatalf("zero generation = %v", err)
	}
}

func TestCancelQueuedRunNeverReachesAProvider(t *testing.T) {
	f := newFixture(t, opts{model: harnesstest.Behavior{Delay: 10 * time.Second, Script: []harnesstest.Step{{Text: "never"}}}, edit: func(c *harnessrun.Config) { c.MaxActive = 1 }})
	first := f.start("key-00000001", "occupies the worker", harnessrun.Budget{}).ID
	f.waitState(first, harnessrun.StateRunning)
	second := f.start("key-00000002", "waits in the queue", harnessrun.Budget{})
	if second.State != harnessrun.StateQueued {
		t.Fatalf("second = %+v", second)
	}
	cancelled, err := f.m.Cancel(context.Background(), f.owner, second.ID, harnessrun.Mutation{IdempotencyKey: "canc-00000001", ExpectedGeneration: second.Generation})
	if err != nil || cancelled.State != harnessrun.StateCancelled {
		t.Fatalf("cancel of a queued run = %+v, %v", cancelled, err)
	}
	running := f.status(first)
	if _, err := f.m.Cancel(context.Background(), f.owner, first, harnessrun.Mutation{IdempotencyKey: "canc-00000002", ExpectedGeneration: running.Generation}); err != nil {
		t.Fatal(err)
	}
	f.waitState(first, harnessrun.StateCancelled)
	time.Sleep(100 * time.Millisecond)
	if f.model.Calls.Load() != 1 || f.status(second.ID).State != harnessrun.StateCancelled {
		t.Fatalf("model calls = %d, second = %+v", f.model.Calls.Load(), f.status(second.ID))
	}
}

func TestBudgetsStopARunAsPartialAndKeepItsWork(t *testing.T) {
	loop := func(extra harnesstest.Step) []harnesstest.Step { return []harnesstest.Step{extra} }
	for name, tc := range map[string]struct {
		model  harnesstest.Behavior
		budget harnessrun.Budget
		code   string
		check  func(*testing.T, harnessrun.Status)
	}{
		"turns": {harnesstest.Behavior{Script: loop(harnesstest.Step{ToolID: "read", Text: "working"})}, harnessrun.Budget{MaxTurns: 3}, "budget_turns",
			func(t *testing.T, s harnessrun.Status) {
				if s.Usage.Turns != 3 {
					t.Errorf("turns = %d", s.Usage.Turns)
				}
			}},
		"spend": {harnesstest.Behavior{CostMicros: 600_000, Script: loop(harnesstest.Step{ToolID: "read", Text: "x"})}, harnessrun.Budget{MaxSpendMicros: 1_000_000}, "budget_spend",
			func(t *testing.T, s harnessrun.Status) {
				if s.Usage.SpendMicros != 1_200_000 || s.Usage.Turns != 2 {
					t.Errorf("usage = %+v", s.Usage)
				}
			}},
		"output": {harnesstest.Behavior{Script: loop(harnesstest.Step{ToolID: "read", Text: strings.Repeat("o", 3000)})}, harnessrun.Budget{MaxOutputBytes: 5000}, "budget_output", nil},
		"time":   {harnesstest.Behavior{Delay: 40 * time.Millisecond, Script: loop(harnesstest.Step{ToolID: "read", Text: "tick"})}, harnessrun.Budget{MaxActive: 100 * time.Millisecond}, "budget_time", nil},
		"tool calls": {harnesstest.Behavior{Script: loop(harnesstest.Step{ToolID: "read", Text: "again"})}, harnessrun.Budget{MaxToolCalls: 2}, "budget_tool_calls",
			func(t *testing.T, s harnessrun.Status) {
				if s.Usage.ToolCalls != 2 {
					t.Errorf("tool calls = %d", s.Usage.ToolCalls)
				}
			}},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, opts{model: tc.model, policy: allow})
			id := f.start("key-00000001", "loop forever", tc.budget).ID
			done := f.waitState(id, harnessrun.StatePartial, harnessrun.StateFailed, harnessrun.StateCompleted)
			if done.State != harnessrun.StatePartial || done.Code != tc.code {
				t.Fatalf("status = %+v", done)
			}
			if len(done.Artifacts) != 1 || done.Artifacts[0].Kind != "partial" {
				t.Fatalf("a budget stop must keep usable work as an artifact: %+v", done.Artifacts)
			}
			if !contains(f.eventCodes(id), "budget:"+tc.code) {
				t.Fatalf("events = %v", f.eventCodes(id))
			}
			if tc.check != nil {
				tc.check(t, done)
			}
		})
	}
}

func TestRecursionDepthAndBudgetInheritance(t *testing.T) {
	f := newFixture(t, opts{model: harnesstest.Behavior{Delay: 10 * time.Second, Script: []harnesstest.Step{{Text: "x"}}}})
	parent := f.start("key-00000001", "parent", harnessrun.Budget{MaxDepth: 1, MaxTurns: 10, MaxToolCalls: 4, MaxSpendMicros: 500_000})
	f.waitState(parent.ID, harnessrun.StateRunning)
	child, err := f.m.StartChild(context.Background(), f.owner, parent.ID, harnessrun.StartRequest{IdempotencyKey: "key-child-001", Goal: "child", Budget: harnessrun.Budget{MaxTurns: 50, MaxToolCalls: 100, MaxSpendMicros: 9_000_000}})
	if err != nil {
		t.Fatal(err)
	}
	if child.Depth != 1 || child.Budget.MaxTurns != 10 || child.Budget.MaxToolCalls != 4 || child.Budget.MaxSpendMicros != 500_000 || child.Budget.MaxDepth != 1 {
		t.Fatalf("child budget = %+v depth %d", child.Budget, child.Depth)
	}
	f.waitState(child.ID, harnessrun.StateRunning)
	if _, err := f.m.StartChild(context.Background(), f.owner, child.ID, harnessrun.StartRequest{IdempotencyKey: "key-grand-001", Goal: "grandchild"}); !errors.Is(err, harnessrun.ErrBudget) {
		t.Fatalf("recursion beyond the depth cap = %v", err)
	}
	if _, err := f.m.StartChild(context.Background(), owner("codex-cli", "agent-memory"), parent.ID, harnessrun.StartRequest{IdempotencyKey: "key-child-002", Goal: "child"}); !errors.Is(err, harnessrun.ErrNotFound) {
		t.Fatalf("another client's child = %v", err)
	}
	for _, id := range []string{child.ID, parent.ID} {
		running := f.status(id)
		if _, err := f.m.Cancel(context.Background(), f.owner, id, harnessrun.Mutation{IdempotencyKey: "canc-" + id[4:12], ExpectedGeneration: running.Generation}); err != nil {
			t.Fatal(err)
		}
		f.waitState(id, harnessrun.StateCancelled)
	}
	if _, err := f.m.StartChild(context.Background(), f.owner, parent.ID, harnessrun.StartRequest{IdempotencyKey: "key-child-003", Goal: "late"}); !errors.Is(err, harnessrun.ErrInvalidState) {
		t.Fatalf("child of a finished parent = %v", err)
	}
}

func TestProviderFaultsBecomeFailedRunsWithCodes(t *testing.T) {
	for name, tc := range map[string]struct {
		model harnesstest.Behavior
		code  string
	}{
		"provider failed":     {harnesstest.Behavior{Outcome: harness.OutcomeFailed}, "model_failed"},
		"provider denied":     {harnesstest.Behavior{Outcome: harness.OutcomeDenied}, "model_denied"},
		"capability denied":   {harnesstest.Behavior{Access: harness.AccessDenied}, "model_denied"},
		"unsupported":         {harnesstest.Behavior{Access: harness.AccessUnsupported}, "model_unsupported"},
		"panic":               {harnesstest.Behavior{Panic: true}, "model_failed"},
		"transport error":     {harnesstest.Behavior{Err: errors.New("boom secret-token")}, "model_failed"},
		"stale reply":         {harnesstest.Behavior{StaleReply: true}, "model_contract"},
		"oversized reply":     {harnesstest.Behavior{Oversize: true}, "model_contract"},
		"cannot open session": {harnesstest.Behavior{ProbeErr: errors.New("down")}, "model_unavailable"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, opts{model: tc.model})
			id := f.start("key-00000001", "goal", harnessrun.Budget{}).ID
			done := f.waitState(id, harnessrun.StateFailed, harnessrun.StateCompleted)
			if done.State != harnessrun.StateFailed || done.Code != tc.code || len(done.Artifacts) != 0 {
				t.Fatalf("status = %+v", done)
			}
			if f.model.Closes.Load() < 1 && tc.code != "model_unavailable" {
				t.Fatal("the failed run left its provider session open")
			}
			encoded, _ := json.Marshal(done)
			if strings.Contains(string(encoded), "secret-token") {
				t.Fatal("provider error text reached status")
			}
		})
	}
}

func TestToolProviderUnavailableDoesNotBlockARun(t *testing.T) {
	f := newFixture(t, opts{model: harnesstest.Behavior{Script: []harnesstest.Step{{Text: "no tools needed"}}}, tool: &harnesstest.Behavior{ProbeErr: errors.New("down")}})
	id := f.start("key-00000001", "goal", harnessrun.Budget{}).ID
	done := f.waitState(id, harnessrun.StateCompleted, harnessrun.StateFailed)
	if done.State != harnessrun.StateCompleted || !contains(f.eventCodes(id), "tool:tool_unavailable") {
		t.Fatalf("status = %+v events = %v", done, f.eventCodes(id))
	}
}

func TestAuthorizationRevocationStopsARunningRun(t *testing.T) {
	var authorized atomic.Bool
	authorized.Store(true)
	f := newFixture(t, opts{
		model:  harnesstest.Behavior{Delay: 30 * time.Millisecond, Script: []harnesstest.Step{{ToolID: "read", Text: "tick"}}},
		policy: allow,
		edit: func(c *harnessrun.Config) {
			c.StillAuthorized = func(harnessrun.Owner) bool { return authorized.Load() }
		},
	})
	id := f.start("key-00000001", "long goal", harnessrun.Budget{MaxTurns: 60, MaxActive: 30 * time.Second}).ID
	f.wait(id, func(s harnessrun.Status) bool { return s.Usage.Turns >= 2 })
	authorized.Store(false)
	done := f.waitState(id, harnessrun.StateCancelled, harnessrun.StatePartial)
	if done.State != harnessrun.StateCancelled || !contains(f.eventCodes(id), "state:authorization_revoked") {
		t.Fatalf("status = %+v events = %v", done, f.eventCodes(id))
	}
}

func TestRestartResumesFromTheCheckpointWithBudgetIntact(t *testing.T) {
	script := []harnesstest.Step{{ToolID: "read", Text: "step one"}, {ToolID: "read", Text: "step two"}, {ToolID: "read", Text: "step three"}, {Text: "final"}}
	f := newFixture(t, opts{model: harnesstest.Behavior{Delay: 120 * time.Millisecond, Script: script}, policy: allow})
	id := f.start("key-00000001", "multi step goal", harnessrun.Budget{}).ID
	before := f.wait(id, func(s harnessrun.Status) bool { return s.Usage.Turns >= 1 && s.State == harnessrun.StateRunning })
	f.restart() // shut down mid-flight, then reopen over the same data
	interrupted := f.status(id)
	if interrupted.State != harnessrun.StateRunning || interrupted.Usage.Turns < before.Usage.Turns {
		t.Fatalf("shutdown changed the run: before %+v after %+v", before, interrupted)
	}
	report, err := f.m.Recover(context.Background())
	if err != nil || report.Requeued != 1 || report.Cancelled != 0 || report.Quarantined != 0 {
		t.Fatalf("report = %+v, %v", report, err)
	}
	done := f.waitState(id, harnessrun.StateCompleted, harnessrun.StateFailed)
	if done.State != harnessrun.StateCompleted || done.Usage.Turns < before.Usage.Turns+1 || len(done.Artifacts) != 1 {
		t.Fatalf("resumed run = %+v (before %+v)", done, before)
	}
	if !contains(f.eventCodes(id), "state:recovered") {
		t.Fatalf("events = %v", f.eventCodes(id))
	}
	again, err := f.m.Recover(context.Background())
	if err != nil || again.Requeued != 0 {
		t.Fatalf("a second recovery re-ran finished work: %+v, %v", again, err)
	}
}

func TestRecoverFinishesCancellationAndQuarantinesDamagedRecords(t *testing.T) {
	f := newFixture(t, opts{model: harnesstest.Behavior{Delay: 10 * time.Second, Script: []harnesstest.Step{{Text: "x"}}}})
	id := f.start("key-00000001", "goal", harnessrun.Budget{}).ID
	f.waitState(id, harnessrun.StateRunning)
	_ = f.m.Close()
	path := filepath.Join(f.dir, "harness", "runs", id+".json")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(content, &raw); err != nil {
		t.Fatal(err)
	}
	raw["state"] = "cancelling"
	edited, _ := json.Marshal(raw)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, edited, 0o600); err != nil {
		t.Fatal(err)
	}
	damaged := "run_" + strings.Repeat("e", 32)
	if err := os.WriteFile(filepath.Join(f.dir, "harness", "runs", damaged+".json"), []byte(`{"id":`), 0o600); err != nil {
		t.Fatal(err)
	}
	f.restart()
	report, err := f.m.Recover(context.Background())
	if err != nil || report.Cancelled != 1 || report.Quarantined != 1 || report.Requeued != 0 {
		t.Fatalf("report = %+v, %v", report, err)
	}
	if done := f.status(id); done.State != harnessrun.StateCancelled || done.Code != "cancelled_after_restart" {
		t.Fatalf("status = %+v", done)
	}
	if _, err := os.Stat(filepath.Join(f.dir, "harness", "runs", damaged+".json.corrupt")); err != nil {
		t.Fatalf("damaged record was not quarantined: %v", err)
	}
	if f.model.Calls.Load() != 1 {
		t.Fatalf("recovery re-ran a cancelling run: %d calls", f.model.Calls.Load())
	}
}

func TestEventCursorsPageWithoutGapsAndRejectTampering(t *testing.T) {
	f := newFixture(t, opts{model: harnesstest.Behavior{Script: []harnesstest.Step{{ToolID: "read", Text: "a"}, {ToolID: "read", Text: "b"}, {Text: "done"}}}, policy: allow})
	id := f.start("key-00000001", "goal", harnessrun.Budget{}).ID
	f.waitState(id, harnessrun.StateCompleted)
	ctx := context.Background()
	var seqs []uint64
	cursor := ""
	for i := 0; i < 50; i++ {
		page, err := f.m.Events(ctx, f.owner, id, cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Events) > 2 || page.Gap {
			t.Fatalf("page = %+v", page)
		}
		for _, e := range page.Events {
			if len(seqs) > 0 && e.Seq != seqs[len(seqs)-1]+1 {
				t.Fatalf("gap or repeat at %d after %d", e.Seq, seqs[len(seqs)-1])
			}
			seqs = append(seqs, e.Seq)
		}
		if len(page.Events) == 0 {
			if page.Next == "" || page.Next != cursor && cursor != "" {
				t.Fatalf("an exhausted cursor must stay stable: %q vs %q", page.Next, cursor)
			}
			break
		}
		cursor = page.Next
	}
	if len(seqs) < 8 || seqs[0] != 1 {
		t.Fatalf("seqs = %v", seqs)
	}
	if _, err := f.m.Events(ctx, f.owner, id, "garbage", 10); !errors.Is(err, harnessrun.ErrInvalidCursor) {
		t.Fatalf("garbage cursor = %v", err)
	}
	other := f.start("key-00000002", "another goal", harnessrun.Budget{})
	f.waitState(other.ID, harnessrun.StateCompleted)
	if _, err := f.m.Events(ctx, f.owner, other.ID, cursor, 10); !errors.Is(err, harnessrun.ErrInvalidCursor) {
		t.Fatalf("a cursor from another run = %v", err)
	}
	if page, err := f.m.Events(ctx, f.owner, id, "", 0); err != nil || len(page.Events) != len(seqs) {
		t.Fatalf("default limit page = %d events, %v", len(page.Events), err)
	}
}

func TestEventCursorSurvivesARestart(t *testing.T) {
	f := newFixture(t, opts{model: harnesstest.Behavior{Script: []harnesstest.Step{{Text: "done"}}}})
	id := f.start("key-00000001", "goal", harnessrun.Budget{}).ID
	f.waitState(id, harnessrun.StateCompleted)
	page, err := f.m.Events(context.Background(), f.owner, id, "", 2)
	if err != nil || len(page.Events) != 2 {
		t.Fatalf("page = %+v, %v", page, err)
	}
	f.restart()
	next, err := f.m.Events(context.Background(), f.owner, id, page.Next, 100)
	if err != nil || len(next.Events) == 0 || next.Events[0].Seq != 3 {
		t.Fatalf("resumed page = %+v, %v", next, err)
	}
}

func TestRetentionSweepRemovesOnlyExpiredInactiveRuns(t *testing.T) {
	var mu sync.Mutex
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	advance := func(d time.Duration) { mu.Lock(); defer mu.Unlock(); now = now.Add(d) }
	f := newFixture(t, opts{
		model: harnesstest.Behavior{Script: []harnesstest.Step{{Text: "done"}}},
		edit:  func(c *harnessrun.Config) { c.Now, c.RetainTerminal, c.MaxAge = clock, time.Hour, 24*time.Hour },
	})
	finished := f.start("key-00000001", "finishes", harnessrun.Budget{}).ID
	f.waitState(finished, harnessrun.StateCompleted)

	parkedFixture := newFixture(t, opts{
		model:  harnesstest.Behavior{Script: []harnesstest.Step{{ToolID: "clarify", Text: "q?"}}},
		policy: allow,
		edit:   func(c *harnessrun.Config) { c.Now, c.RetainTerminal, c.MaxAge = clock, time.Hour, 24*time.Hour },
	})
	parked := parkedFixture.start("key-00000002", "waits", harnessrun.Budget{}).ID
	parkedFixture.waitState(parked, harnessrun.StateNeedsAttention)

	advance(2 * time.Hour)
	if removed, err := f.m.Sweep(); err != nil || removed != 1 {
		t.Fatalf("after 2h removed = %d, %v", removed, err)
	}
	if _, err := f.m.Status(context.Background(), f.owner, finished); !errors.Is(err, harnessrun.ErrNotFound) {
		t.Fatalf("finished run survived its retention: %v", err)
	}
	if removed, _ := parkedFixture.m.Sweep(); removed != 0 {
		t.Fatalf("a waiting run inside its maximum age was removed: %d", removed)
	}
	// With the idempotency record gone the same key starts a brand-new run.
	again := f.start("key-00000001", "finishes", harnessrun.Budget{})
	if again.ID == finished || again.Deduplicated {
		t.Fatalf("stale idempotency record survived: %+v", again)
	}
	advance(25 * time.Hour)
	if removed, _ := parkedFixture.m.Sweep(); removed != 1 {
		t.Fatalf("an abandoned run past its maximum age was kept: %d", removed)
	}
}

func TestSweepNeverRemovesAnActiveRun(t *testing.T) {
	var mu sync.Mutex
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	f := newFixture(t, opts{
		model: harnesstest.Behavior{Delay: 10 * time.Second, Script: []harnesstest.Step{{Text: "x"}}},
		edit: func(c *harnessrun.Config) {
			c.Now = func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
			c.MaxAge = time.Hour
		},
	})
	id := f.start("key-00000001", "long running", harnessrun.Budget{}).ID
	f.waitState(id, harnessrun.StateRunning)
	mu.Lock()
	now = now.Add(48 * time.Hour)
	mu.Unlock()
	if removed, _ := f.m.Sweep(); removed != 0 {
		t.Fatalf("an active run was swept: %d", removed)
	}
}

func TestFullQueueReturnsBusyAndLeavesNoState(t *testing.T) {
	f := newFixture(t, opts{
		model: harnesstest.Behavior{Delay: 10 * time.Second, Script: []harnesstest.Step{{Text: "x"}}},
		edit:  func(c *harnessrun.Config) { c.MaxActive, c.QueueSize = 1, 1 },
	})
	first := f.start("key-00000001", "running", harnessrun.Budget{}).ID
	f.waitState(first, harnessrun.StateRunning)
	f.start("key-00000002", "queued", harnessrun.Budget{})
	if _, err := f.m.Start(context.Background(), f.owner, harnessrun.StartRequest{IdempotencyKey: "key-00000003", Goal: "one too many"}); !errors.Is(err, harnessrun.ErrBusy) {
		t.Fatalf("overflow = %v", err)
	}
	runs, _ := filepath.Glob(filepath.Join(f.dir, "harness", "runs", "*.json"))
	idem, _ := filepath.Glob(filepath.Join(f.dir, "harness", "idem", "*.json"))
	if len(runs) != 2 || len(idem) != 2 {
		t.Fatalf("a rejected start left state behind: %d runs, %d idempotency records", len(runs), len(idem))
	}
}

func TestSolutionStepsAndStoredTextCarryNoSecrets(t *testing.T) {
	steps := &sink{}
	f := newFixture(t, opts{
		model: harnesstest.Behavior{Script: []harnesstest.Step{{ToolID: "clarify", Text: "SECRET-TOKEN-4242 is mentioned by the model"}, {Text: "final answer SECRET-TOKEN-4242"}}},
		edit: func(c *harnessrun.Config) {
			c.Steps = steps
			c.Redact = func(s string) string { return strings.ReplaceAll(s, "SECRET-TOKEN-4242", "[redacted]") }
		},
	})
	id := f.start("key-00000001", "use SECRET-TOKEN-4242 to log in", harnessrun.Budget{}).ID
	parked := f.waitState(id, harnessrun.StateNeedsAttention)
	if strings.Contains(parked.Attention.Prompt, "SECRET-TOKEN-4242") || !strings.Contains(parked.Attention.Prompt, "[redacted]") {
		t.Fatalf("prompt = %q", parked.Attention.Prompt)
	}
	if _, err := f.m.Continue(context.Background(), f.owner, id, harnessrun.Mutation{IdempotencyKey: "cont-00000001", ExpectedGeneration: parked.Generation}, "the token is SECRET-TOKEN-4242"); err != nil {
		t.Fatal(err)
	}
	f.waitState(id, harnessrun.StateCompleted)
	raw, err := os.ReadFile(filepath.Join(f.dir, "harness", "runs", id+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "SECRET-TOKEN-4242") {
		t.Fatal("a secret was persisted in the run record")
	}
	steps.mu.Lock()
	defer steps.mu.Unlock()
	if len(steps.lines) < 3 {
		t.Fatalf("steps = %v", steps.lines)
	}
	for _, line := range steps.lines {
		if strings.Contains(line, "SECRET") || strings.Contains(line, "log in") || strings.Contains(line, "final answer") {
			t.Fatalf("a solution step carried run content: %s", line)
		}
	}
}

func TestRunFilesArePrivateAndLeaveNoTemporaryFiles(t *testing.T) {
	f := newFixture(t, opts{model: harnesstest.Behavior{Script: []harnesstest.Step{{ToolID: "read", Text: "a"}, {Text: "done"}}}, policy: allow})
	f.waitState(f.start("key-00000001", "goal", harnessrun.Budget{}).ID, harnessrun.StateCompleted)
	err := filepath.Walk(filepath.Join(f.dir, "harness"), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		want := os.FileMode(0o600)
		if info.IsDir() {
			want = 0o700
		}
		if info.Mode().Perm() != want {
			t.Errorf("%s mode %v, want %v", path, info.Mode().Perm(), want)
		}
		if strings.HasSuffix(path, ".tmp") {
			t.Errorf("temporary file left behind: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestClosedManagerRejectsNewWorkAndCloseIsIdempotent(t *testing.T) {
	f := newFixture(t, opts{model: harnesstest.Behavior{Script: []harnesstest.Step{{Text: "x"}}}})
	if err := f.m.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.m.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := f.m.Start(context.Background(), f.owner, harnessrun.StartRequest{IdempotencyKey: "key-00000001", Goal: "g"}); !errors.Is(err, harnessrun.ErrClosed) {
		t.Fatalf("start after close = %v", err)
	}
}

func TestNewManagerRejectsUnusableBindings(t *testing.T) {
	registry := harness.NewRegistry()
	if _, err := harnesstest.Register(registry, harnesstest.Manifest("fake-model", harness.KindModel, "generation"), harnesstest.Behavior{}); err != nil {
		t.Fatal(err)
	}
	if _, err := harnesstest.Register(registry, harnesstest.Manifest("fake-tool", harness.KindTool, "read"), harnesstest.Behavior{}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for name, cfg := range map[string]harnessrun.Config{
		"no registry":      {DataDir: dir, Model: harnessrun.Binding{Provider: "fake-model", Capability: "generation"}},
		"no model":         {DataDir: dir, Registry: registry},
		"unknown provider": {DataDir: dir, Registry: registry, Model: harnessrun.Binding{Provider: "ghost", Capability: "generation"}},
		"wrong kind":       {DataDir: dir, Registry: registry, Model: harnessrun.Binding{Provider: "fake-tool", Capability: "read"}},
		"undeclared cap":   {DataDir: dir, Registry: registry, Model: harnessrun.Binding{Provider: "fake-model", Capability: "vision"}},
		"tool wrong kind":  {DataDir: dir, Registry: registry, Model: harnessrun.Binding{Provider: "fake-model", Capability: "generation"}, Tool: &harnessrun.Binding{Provider: "fake-model", Capability: "generation"}},
		"no data dir":      {Registry: registry, Model: harnessrun.Binding{Provider: "fake-model", Capability: "generation"}},
	} {
		if _, err := harnessrun.NewManager(cfg); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestTimeBudgetStopsBeforeTheProviderIsCalledAgain(t *testing.T) {
	// Every clock reading advances one second, so each turn's measured time is
	// exactly one second no matter how fast the fake provider answers.
	var mu sync.Mutex
	tick := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); tick = tick.Add(time.Second); return tick }
	f := newFixture(t, opts{
		model:  harnesstest.Behavior{Script: []harnesstest.Step{{ToolID: "read", Text: "tick"}}},
		policy: allow,
		edit:   func(c *harnessrun.Config) { c.Now = clock },
	})
	id := f.start("key-00000001", "bounded by time", harnessrun.Budget{MaxActive: 1500 * time.Millisecond}).ID
	done := f.waitState(id, harnessrun.StatePartial, harnessrun.StateFailed, harnessrun.StateCompleted)
	if done.State != harnessrun.StatePartial || done.Code != "budget_time" || done.Usage.ActiveMillis != 2000 {
		t.Fatalf("status = %+v", done)
	}
	if f.model.Calls.Load() != 2 {
		t.Fatalf("the provider was called %d times; the spent budget must stop a third call", f.model.Calls.Load())
	}
}

func TestRepeatedDeniedActionsAreEachDeniedNotFailed(t *testing.T) {
	f := newFixture(t, opts{model: harnesstest.Behavior{Script: []harnesstest.Step{
		{ToolID: "read", Arguments: "{}"}, {ToolID: "read", Arguments: "{}"}, {ToolID: "read", Arguments: "{}"}, {Text: "gave up"}}}})
	id := f.start("key-00000001", "keeps asking for a denied tool", harnessrun.Budget{}).ID
	done := f.waitState(id, harnessrun.StateCompleted, harnessrun.StateFailed)
	denied, failed := 0, 0
	for _, code := range f.eventCodes(id) {
		switch code {
		case "tool:tool_denied":
			denied++
		case "tool:tool_failed":
			failed++
		}
	}
	if done.State != harnessrun.StateCompleted || denied != 3 || failed != 0 || f.tool.Invokes.Load() != 0 {
		t.Fatalf("denied=%d failed=%d invokes=%d status=%+v", denied, failed, f.tool.Invokes.Load(), done)
	}
}

func TestToolProviderFaultsAreContainedPerAction(t *testing.T) {
	script := []harnesstest.Step{{ToolID: "read", Arguments: "{}"}, {Text: "adapted"}}
	for name, tc := range map[string]struct {
		tool harnesstest.Behavior
		code string
	}{
		"failed outcome": {harnesstest.Behavior{Outcome: harness.OutcomeFailed}, "tool:tool_failed"},
		"denied outcome": {harnesstest.Behavior{Outcome: harness.OutcomeDenied}, "tool:tool_denied"},
		"panic":          {harnesstest.Behavior{Panic: true}, "tool:tool_failed"},
		"stale reply":    {harnesstest.Behavior{StaleReply: true}, "tool:tool_failed"},
		"transport":      {harnesstest.Behavior{Err: errors.New("disk on fire")}, "tool:tool_failed"},
	} {
		t.Run(name, func(t *testing.T) {
			tool := tc.tool
			f := newFixture(t, opts{model: harnesstest.Behavior{Script: script}, tool: &tool, policy: allow})
			id := f.start("key-00000001", "goal", harnessrun.Budget{}).ID
			done := f.waitState(id, harnessrun.StateCompleted, harnessrun.StateFailed)
			if done.State != harnessrun.StateCompleted || !contains(f.eventCodes(id), tc.code) || f.tool.Invokes.Load() != 0 {
				t.Fatalf("status=%+v events=%v invokes=%d", done, f.eventCodes(id), f.tool.Invokes.Load())
			}
			encoded, _ := json.Marshal(done)
			if strings.Contains(string(encoded), "disk on fire") {
				t.Fatal("tool error text reached status")
			}
		})
	}
	// A capability the tool provider reports as unsupported is never offered to the
	// model, so a model that asks for it anyway is a contract failure, not a tool run.
	unsupported := harnesstest.Behavior{Access: harness.AccessUnsupported}
	f := newFixture(t, opts{model: harnesstest.Behavior{Script: script}, tool: &unsupported, policy: allow})
	id := f.start("key-00000001", "goal", harnessrun.Budget{}).ID
	if done := f.waitState(id, harnessrun.StateFailed, harnessrun.StateCompleted); done.State != harnessrun.StateFailed || done.Code != "model_contract" {
		t.Fatalf("status = %+v", done)
	}
}

func TestCancelDuringAToolCallStopsItAndClosesTheSession(t *testing.T) {
	slow := harnesstest.Behavior{Delay: 10 * time.Second}
	f := newFixture(t, opts{model: harnesstest.Behavior{Script: []harnesstest.Step{{ToolID: "read", Arguments: "{}"}}}, tool: &slow, policy: allow})
	id := f.start("key-00000001", "goal", harnessrun.Budget{}).ID
	deadline := time.Now().Add(5 * time.Second)
	for f.tool.Calls.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the tool was never called")
		}
		time.Sleep(10 * time.Millisecond)
	}
	running := f.status(id)
	began := time.Now()
	if _, err := f.m.Cancel(context.Background(), f.owner, id, harnessrun.Mutation{IdempotencyKey: "canc-00000001", ExpectedGeneration: running.Generation}); err != nil {
		t.Fatal(err)
	}
	f.waitState(id, harnessrun.StateCancelled)
	if time.Since(began) > 2*time.Second || f.tool.Closes.Load() < 1 || f.model.Closes.Load() < 1 {
		t.Fatalf("slow=%v tool closes=%d model closes=%d", time.Since(began), f.tool.Closes.Load(), f.model.Closes.Load())
	}
}

// startDigest reproduces the manager's request digest so a test can plant an
// idempotency record that matches a real request.
func startDigest(goal string, budget harnessrun.Budget) string {
	normalized, _ := budget.Normalize()
	h := sha256.New()
	for _, part := range []string{"start", goal, fmt.Sprint(normalized), ""} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func plantIdempotencyRecord(t *testing.T, f *fixture, key, goal string, createdAt time.Time) {
	t.Helper()
	sum := sha256.Sum256([]byte(f.owner.ClientID + "\x00" + f.owner.Workspace + "\x00" + key))
	record, _ := json.Marshal(map[string]any{"run_id": "run_" + strings.Repeat("9", 32), "request_sha256": startDigest(goal, harnessrun.Budget{}), "created_at": createdAt})
	if err := os.WriteFile(filepath.Join(f.dir, "harness", "idem", hex.EncodeToString(sum[:])+".json"), record, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAnOrphanedIdempotencyRecordIsReclaimedOnceItIsStale(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	f := newFixture(t, opts{
		model: harnesstest.Behavior{Script: []harnesstest.Step{{Text: "done"}}},
		edit:  func(c *harnessrun.Config) { c.Now = func() time.Time { return now } },
	})
	// A crash between recording the key and creating the run leaves this behind.
	plantIdempotencyRecord(t, f, "key-00000001", "goal", now.Add(-time.Hour))
	status := f.start("key-00000001", "goal", harnessrun.Budget{})
	if status.Deduplicated || status.ID == "run_"+strings.Repeat("9", 32) {
		t.Fatalf("an orphaned record pinned the key to a run that does not exist: %+v", status)
	}
	f.waitState(status.ID, harnessrun.StateCompleted)
	if again := f.start("key-00000001", "goal", harnessrun.Budget{}); again.ID != status.ID || !again.Deduplicated {
		t.Fatalf("the reclaimed key no longer deduplicates: %+v", again)
	}
}

func TestAFreshRecordWithoutARunIsWaitedForNotStolen(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	f := newFixture(t, opts{
		model: harnesstest.Behavior{Script: []harnesstest.Step{{Text: "done"}}},
		edit:  func(c *harnessrun.Config) { c.Now = func() time.Time { return now } },
	})
	// A concurrent start has claimed the key but not yet created its run.
	plantIdempotencyRecord(t, f, "key-00000001", "goal", now)
	_, err := f.m.Start(context.Background(), f.owner, harnessrun.StartRequest{IdempotencyKey: "key-00000001", Goal: "goal"})
	if !errors.Is(err, harnessrun.ErrBusy) {
		t.Fatalf("a fresh in-flight claim was stolen or ignored: %v", err)
	}
	if files, _ := filepath.Glob(filepath.Join(f.dir, "harness", "runs", "*.json")); len(files) != 0 {
		t.Fatalf("a second run was created: %v", files)
	}
}

// slowClose makes every provider session take a visible time to close and counts when
// the close has finished, so a test can tell whether a state was published before or
// after the sessions were released.
func slowClose(b harnesstest.Behavior) (harnesstest.Behavior, *harnesstest.Behavior) {
	b.CloseDelay = 200 * time.Millisecond
	return b, &harnesstest.Behavior{CloseDelay: 200 * time.Millisecond}
}

func requireReleased(t *testing.T, f *fixture, when string) {
	t.Helper()
	if f.model.Closed.Load() != 1 || f.tool.Closed.Load() != 1 {
		t.Fatalf("%s was visible while provider sessions were still open: model closed=%d tool closed=%d", when, f.model.Closed.Load(), f.tool.Closed.Load())
	}
}

func TestATerminalStateIsPublishedOnlyAfterProviderSessionsClose(t *testing.T) {
	model, tool := slowClose(harnesstest.Behavior{Script: []harnesstest.Step{{Text: "done"}}})
	f := newFixture(t, opts{model: model, tool: tool, edit: func(c *harnessrun.Config) { c.CloseTimeout = 5 * time.Second }})
	f.waitState(f.start("key-00000001", "goal", harnessrun.Budget{}).ID, harnessrun.StateCompleted)
	requireReleased(t, f, "completed")
}

func TestAFailedRunHasAlreadyReleasedItsProviderSessions(t *testing.T) {
	model, tool := slowClose(harnesstest.Behavior{Outcome: harness.OutcomeFailed})
	f := newFixture(t, opts{model: model, tool: tool, edit: func(c *harnessrun.Config) { c.CloseTimeout = 5 * time.Second }})
	f.waitState(f.start("key-00000001", "goal", harnessrun.Budget{}).ID, harnessrun.StateFailed)
	requireReleased(t, f, "failed")
}

func TestAProviderThatHangsInCloseCannotHoldARunHostage(t *testing.T) {
	f := newFixture(t, opts{
		model: harnesstest.Behavior{CloseDelay: 20 * time.Second, Script: []harnesstest.Step{{Text: "done"}}},
		edit:  func(c *harnessrun.Config) { c.CloseTimeout = 150 * time.Millisecond },
	})
	began := time.Now()
	id := f.start("key-00000001", "goal", harnessrun.Budget{}).ID
	done := f.waitState(id, harnessrun.StateCompleted)
	if elapsed := time.Since(began); elapsed > 3*time.Second || done.State != harnessrun.StateCompleted {
		t.Fatalf("a hung Close held the run for %v: %+v", elapsed, done)
	}
}

func TestCancellationIsPublishedOnlyAfterEveryProviderSessionClosed(t *testing.T) {
	model, tool := slowClose(harnesstest.Behavior{Delay: 10 * time.Second, Script: []harnesstest.Step{{Text: "never"}}})
	f := newFixture(t, opts{model: model, tool: tool, edit: func(c *harnessrun.Config) { c.CloseTimeout = 5 * time.Second }})
	id := f.start("key-00000001", "goal", harnessrun.Budget{}).ID
	running := f.waitState(id, harnessrun.StateRunning)
	if _, err := f.m.Cancel(context.Background(), f.owner, id, harnessrun.Mutation{IdempotencyKey: "canc-00000001", ExpectedGeneration: running.Generation}); err != nil {
		t.Fatal(err)
	}
	f.waitState(id, harnessrun.StateCancelled)
	requireReleased(t, f, "cancelled")
}

func TestAParkedRunHasAlreadyReleasedItsProviderSessions(t *testing.T) {
	model, tool := slowClose(harnesstest.Behavior{Script: []harnesstest.Step{{ToolID: "clarify", Text: "which file?"}}})
	f := newFixture(t, opts{model: model, tool: tool, edit: func(c *harnessrun.Config) { c.CloseTimeout = 5 * time.Second }})
	f.waitState(f.start("key-00000001", "goal", harnessrun.Budget{}).ID, harnessrun.StateNeedsAttention)
	requireReleased(t, f, "needs_attention")
}

func TestABudgetStopHasAlreadyReleasedItsProviderSessions(t *testing.T) {
	model, tool := slowClose(harnesstest.Behavior{Script: []harnesstest.Step{{ToolID: "read", Text: "again"}}})
	f := newFixture(t, opts{model: model, tool: tool, policy: allow, edit: func(c *harnessrun.Config) { c.CloseTimeout = 5 * time.Second }})
	f.waitState(f.start("key-00000001", "goal", harnessrun.Budget{MaxTurns: 2}).ID, harnessrun.StatePartial)
	requireReleased(t, f, "partial")
}

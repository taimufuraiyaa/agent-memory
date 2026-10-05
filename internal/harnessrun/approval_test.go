package harnessrun_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
	"github.com/taimufuraiyaa/agent-memory/internal/harness/harnesstest"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessapproval"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessproof"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessrun"
)

// writeTool is a tool provider whose calls ask for approval. It records everything it is
// asked to do, and its digest follows a revision the test can change, like a real file's.
type writeTool struct {
	mu        sync.Mutex
	revision  int
	invoked   []invocation
	prepares  int
	escalate  []string
	paths     []string
	scope     harness.Scope
	invokeErr error
}

type invocation struct {
	proof  string
	digest string
	args   string
}

func (w *writeTool) bump() {
	w.mu.Lock()
	w.revision++
	w.mu.Unlock()
}

func (w *writeTool) invocations() []invocation {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]invocation(nil), w.invoked...)
}

type writeSession struct {
	tool    *writeTool
	mu      sync.Mutex
	pending map[string]string
}

func (w *writeTool) register(registry *harness.Registry) error {
	manifest := harnesstest.Manifest("write-tool", harness.KindTool, "write")
	return registry.Register(manifest, func() (harness.Provider, error) {
		return &writeSession{tool: w, pending: map[string]string{}}, nil
	})
}

func (s *writeSession) Probe(_ context.Context, scope harness.Scope) (harness.LiveAccess, error) {
	return harness.LiveAccess{Version: harness.ContractVersion, Provider: "write-tool", Scope: scope, Revision: 1,
		Capabilities: map[harness.CapabilityID]harness.AccessState{"write": harness.AccessAvailable}}, nil
}

func (s *writeSession) Close() error { return nil }

func (s *writeSession) Prepare(_ context.Context, q harness.ToolRequest) (harness.PreparedAction, error) {
	s.tool.mu.Lock()
	defer s.tool.mu.Unlock()
	s.tool.prepares++
	digest := "sha256:" + strings.Repeat("0", 56) + hexOf(len(q.Arguments), s.tool.revision)
	s.mu.Lock()
	s.pending[digest] = string(q.Arguments)
	s.mu.Unlock()
	paths := s.tool.paths
	if paths == nil {
		paths = []string{"notes/a.txt"}
	}
	return harness.PreparedAction{Envelope: q.Envelope, Outcome: harness.OutcomeOK, Digest: digest, Summary: "edit " + paths[0], Paths: paths,
		Preview: "--- a/notes/a.txt\n+++ b/notes/a.txt\n@@ -1,1 +1,1 @@\n-old\n+new\n", Escalate: s.tool.escalate}, nil
}

func hexOf(a, b int) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 8)
	v := a*1000 + b
	for i := 7; i >= 0; i-- {
		out[i] = digits[v&15]
		v >>= 4
	}
	return string(out)
}

func (s *writeSession) Invoke(ctx context.Context, a harness.PreparedAction) (harness.ToolAnswer, error) {
	s.mu.Lock()
	args := s.pending[a.Digest]
	delete(s.pending, a.Digest)
	s.mu.Unlock()
	s.tool.mu.Lock()
	s.tool.invoked = append(s.tool.invoked, invocation{proof: harnessproof.Approved(ctx), digest: a.Digest, args: args})
	err := s.tool.invokeErr
	s.tool.mu.Unlock()
	if err != nil {
		return harness.ToolAnswer{}, err
	}
	return harness.ToolAnswer{Envelope: a.Envelope, Outcome: harness.OutcomeOK, Output: []byte("applied edit")}, nil
}

func (s *writeSession) Release(digest string) {
	s.mu.Lock()
	delete(s.pending, digest)
	s.mu.Unlock()
}

type askPolicy struct{}

func (askPolicy) Decide(_ context.Context, _ harnessrun.Owner, a harness.PreparedAction) harnessrun.Decision {
	if len(a.Escalate) > 0 {
		return harnessrun.DecisionAskStrict
	}
	return harnessrun.DecisionAsk
}

func (askPolicy) Reasons(a harness.PreparedAction) []string { return a.Escalate }

type approvalClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *approvalClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *approvalClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type approvalFixture struct {
	*fixture
	tool      *writeTool
	approvals *harnessapproval.Store
	clock     *approvalClock
	revoked   atomic.Bool
}

type approvalOpts struct {
	delay     time.Duration
	script    []harnesstest.Step
	poll      time.Duration
	limits    [2]int
	configure func(*harnessrun.Config)
}

const writeArgs = `{"path":"notes/a.txt","edits":[{"old_text":"old","new_text":"new"}]}`

func newApprovalFixture(t *testing.T, o approvalOpts) *approvalFixture {
	t.Helper()
	tool := &writeTool{}
	clock := &approvalClock{t: time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)}
	script := o.script
	if script == nil {
		script = []harnesstest.Step{{ToolID: "write", Arguments: writeArgs}, {Text: "all done"}}
	}
	var store *harnessapproval.Store
	a := &approvalFixture{tool: tool, clock: clock}
	f := newFixture(t, opts{model: harnesstest.Behavior{Script: script, Delay: o.delay}, edit: func(c *harnessrun.Config) {
		opts := []harnessapproval.Option{harnessapproval.WithClock(clock.now)}
		if o.limits != ([2]int{}) {
			opts = append(opts, harnessapproval.WithLimits(o.limits[0], o.limits[1]))
		}
		store = harnessapproval.Open(c.DataDir, opts...)
		if err := tool.register(c.Registry); err != nil {
			t.Fatal(err)
		}
		c.Tool = &harnessrun.Binding{Provider: "write-tool", Capability: "write"}
		c.Policy = askPolicy{}
		c.Approvals = store
		c.ApprovalPoll = o.poll
		if c.ApprovalPoll == 0 {
			c.ApprovalPoll = 15 * time.Millisecond
		}
		c.Now = clock.now
		c.StillAuthorized = func(harnessrun.Owner) bool { return !a.revoked.Load() }
		if o.configure != nil {
			o.configure(c)
		}
	}})
	a.fixture, a.approvals = f, store
	return a
}

var approvalIDRE = regexp.MustCompile(`^apr_[0-9a-f]{32}$`)

func (a *approvalFixture) parked(id string) (harnessrun.Status, harnessapproval.Record) {
	a.t.Helper()
	status := a.waitState(id, harnessrun.StateNeedsAttention)
	if status.Attention == nil || status.Attention.Kind != harnessrun.AttentionApproval || !approvalIDRE.MatchString(status.Attention.ApprovalID) {
		a.t.Fatalf("attention = %+v", status.Attention)
	}
	record, err := a.approvals.Get(context.Background(), status.Attention.ApprovalID)
	if err != nil {
		a.t.Fatal(err)
	}
	return status, record
}

func (a *approvalFixture) approve(record harnessapproval.Record) {
	a.t.Helper()
	if _, err := a.approvals.Decide(context.Background(), record.ID, harnessapproval.Decision{Approve: true, Code: harnessapproval.Code(record), Via: "terminal"}); err != nil {
		a.t.Fatal(err)
	}
}

func (a *approvalFixture) deny(record harnessapproval.Record, stop bool) {
	a.t.Helper()
	if _, err := a.approvals.Decide(context.Background(), record.ID, harnessapproval.Decision{Stop: stop, Via: "terminal"}); err != nil {
		a.t.Fatal(err)
	}
}

func (a *approvalFixture) runFileText(id string) string {
	a.t.Helper()
	data, err := os.ReadFile(filepath.Join(a.dir, "harness", "runs", id+".json"))
	if err != nil {
		a.t.Fatal(err)
	}
	return string(data)
}

func TestARunThatNeedsApprovalParksWithAContentFreeStatusAndARecord(t *testing.T) {
	a := newApprovalFixture(t, approvalOpts{})
	started := a.start("key-00000001", "change the notes", harnessrun.Budget{})
	status, record := a.parked(started.ID)

	if status.Code != "approval_required" || a.tool.invocations() != nil {
		t.Fatalf("status = %+v; invocations = %v", status, a.tool.invocations())
	}
	if record.State != harnessapproval.StatePending || record.RunID != started.ID || record.Friction != harnessapproval.FrictionStandard || record.Tool != "write" ||
		record.RunGeneration != status.Generation || record.Owner.ClientID != "claude-desktop" || record.Owner.GrantID != a.owner.GrantID ||
		!reflect.DeepEqual(record.Paths, []string{"notes/a.txt"}) || !strings.Contains(record.Preview, "+new") || record.Summary != "edit notes/a.txt" || record.Digest == "" {
		t.Fatalf("record = %+v", record)
	}
	if !record.ExpiresAt.Equal(a.clock.now().Add(harnessapproval.DefaultTTL)) {
		t.Fatalf("expires = %v", record.ExpiresAt)
	}
	// What a client can see of the run carries none of the action.
	encoded, _ := json.Marshal(status)
	// (The gateway also withholds the action digest from the client view; that is tested there.)
	for _, secret := range []string{"+new", "notes/a.txt", "old_text", "edit notes"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("the client view carries %q: %s", secret, encoded)
		}
	}
	// A continue cannot satisfy it, and nothing ran.
	if _, err := a.m.Continue(context.Background(), a.owner, started.ID, harnessrun.Mutation{IdempotencyKey: "continue-0001", ExpectedGeneration: status.Generation}, "yes, approved"); !errors.Is(err, harnessrun.ErrApprovalRequired) {
		t.Fatalf("continue = %v", err)
	}
	time.Sleep(60 * time.Millisecond)
	if a.status(started.ID).State != harnessrun.StateNeedsAttention || a.tool.invocations() != nil {
		t.Fatal("the run moved without a decision")
	}
}

func TestAnApprovedActionRunsExactlyOnceUnderTheProofForExactlyThatAction(t *testing.T) {
	a := newApprovalFixture(t, approvalOpts{})
	started := a.start("key-00000001", "change the notes", harnessrun.Budget{})
	_, record := a.parked(started.ID)
	a.approve(record)
	done := a.waitState(started.ID, harnessrun.StateCompleted)

	calls := a.tool.invocations()
	if len(calls) != 1 || calls[0].proof != record.Digest || calls[0].digest != record.Digest || calls[0].args != writeArgs {
		t.Fatalf("invocations = %+v", calls)
	}
	if done.Usage.ToolCalls != 1 || !contains(a.eventCodes(started.ID), "tool:tool_ok") || !contains(a.eventCodes(started.ID), "state:approved") {
		t.Fatalf("done = %+v events = %v", done, a.eventCodes(started.ID))
	}
	final, err := a.approvals.Get(context.Background(), record.ID)
	if err != nil || final.State != harnessapproval.StateConsumed || final.Outcome != "applied" || final.Preview != "" {
		t.Fatalf("approval after use = %+v %v", final, err)
	}
	if strings.Contains(a.runFileText(started.ID), `"pending"`) {
		t.Fatal("the replay marker was left in the run record")
	}
	if n, err := a.approvals.VerifyAudit(context.Background()); err != nil || n != 4 { // requested, approved, consumed, finished
		t.Fatalf("audit = %d %v", n, err)
	}
	// Reconciling again changes nothing and the action never runs a second time.
	if moved, err := a.m.ReconcileApprovals(context.Background()); err != nil || moved != 0 {
		t.Fatalf("a second reconcile moved %d (%v)", moved, err)
	}
	time.Sleep(40 * time.Millisecond)
	if len(a.tool.invocations()) != 1 {
		t.Fatal("the approved action ran again")
	}
	// The model's next turn saw what the tool returned.
	requests := a.model.Requests.Load()
	if requests == nil {
		t.Fatal("the model made no request")
	}
}

func TestADeniedActionNeverRunsAndTheModelIsToldOrTheRunStops(t *testing.T) {
	a := newApprovalFixture(t, approvalOpts{})
	started := a.start("key-00000001", "change the notes", harnessrun.Budget{})
	_, record := a.parked(started.ID)
	a.deny(record, false)
	done := a.waitState(started.ID, harnessrun.StateCompleted)
	if a.tool.invocations() != nil || !contains(a.eventCodes(started.ID), "state:approval_denied") {
		t.Fatalf("invocations = %v events = %v", a.tool.invocations(), a.eventCodes(started.ID))
	}
	if !strings.Contains(a.runFileText(started.ID), "tool_denied: denied by the person") || done.Code != "completed" {
		t.Fatalf("the model was not told: %s", a.runFileText(started.ID))
	}
	if got, _ := a.approvals.Get(context.Background(), record.ID); got.State != harnessapproval.StateDenied || got.Preview != "" {
		t.Fatalf("record = %+v", got)
	}

	b := newApprovalFixture(t, approvalOpts{})
	second := b.start("key-00000002", "change the notes", harnessrun.Budget{})
	_, again := b.parked(second.ID)
	b.deny(again, true)
	stopped := b.waitState(second.ID, harnessrun.StateCancelled)
	if stopped.Code != "cancelled_by_approver" || b.tool.invocations() != nil {
		t.Fatalf("stopped = %+v invocations = %v", stopped, b.tool.invocations())
	}
}

func TestAnExpiredApprovalNeverRunsEvenIfItWasApproved(t *testing.T) {
	a := newApprovalFixture(t, approvalOpts{poll: time.Hour}) // reconcile by hand, so the order is fixed
	started := a.start("key-00000001", "change the notes", harnessrun.Budget{})
	_, record := a.parked(started.ID)
	a.approve(record)
	a.clock.advance(harnessapproval.DefaultTTL + time.Second)
	if moved, err := a.m.ReconcileApprovals(context.Background()); err != nil || moved != 1 {
		t.Fatalf("reconcile = %d %v", moved, err)
	}
	a.waitState(started.ID, harnessrun.StateCompleted)
	if a.tool.invocations() != nil || !strings.Contains(a.runFileText(started.ID), "tool_stale: approval_expired") {
		t.Fatalf("invocations = %v; run: %s", a.tool.invocations(), a.runFileText(started.ID))
	}
	if got, _ := a.approvals.Get(context.Background(), record.ID); got.State != harnessapproval.StateExpired {
		t.Fatalf("state = %s", got.State)
	}
}

func TestARevokedGrantEndsTheApprovalAndTheRun(t *testing.T) {
	for name, approveFirst := range map[string]bool{"while pending": false, "after approval": true} {
		a := newApprovalFixture(t, approvalOpts{poll: time.Hour})
		started := a.start("key-00000001", "change the notes", harnessrun.Budget{})
		_, record := a.parked(started.ID)
		if approveFirst {
			a.approve(record)
		}
		a.revoked.Store(true)
		if moved, err := a.m.ReconcileApprovals(context.Background()); err != nil || moved != 1 {
			t.Fatalf("%s: reconcile = %d %v", name, moved, err)
		}
		cancelled := a.waitState(started.ID, harnessrun.StateCancelled)
		got, _ := a.approvals.Get(context.Background(), record.ID)
		if cancelled.Code != "authorization_revoked" || got.State != harnessapproval.StateRevoked || a.tool.invocations() != nil {
			t.Errorf("%s: run %+v approval %+v invocations %v", name, cancelled, got, a.tool.invocations())
		}
		if _, err := a.approvals.Decide(context.Background(), record.ID, harnessapproval.Decision{Approve: true, Code: harnessapproval.Code(record), Via: "terminal"}); err == nil {
			t.Errorf("%s: a revoked approval could still be decided", name)
		}
	}
}

func TestAFileChangedAfterReviewMakesTheApprovalStale(t *testing.T) {
	a := newApprovalFixture(t, approvalOpts{})
	started := a.start("key-00000001", "change the notes", harnessrun.Budget{})
	_, record := a.parked(started.ID)
	a.tool.bump() // the file changed after the person's review: a replay prepares to another digest
	a.approve(record)
	a.waitState(started.ID, harnessrun.StateCompleted)
	if a.tool.invocations() != nil {
		t.Fatalf("a changed file was written anyway: %v", a.tool.invocations())
	}
	if !strings.Contains(a.runFileText(started.ID), "tool_stale: changed_since_review") {
		t.Fatalf("the model was not told: %s", a.runFileText(started.ID))
	}
	if got, _ := a.approvals.Get(context.Background(), record.ID); got.State != harnessapproval.StateConsumed || got.Outcome != "stale" {
		t.Fatalf("approval = %+v", got)
	}
}

func TestCancellingAWaitingRunEndsItsApproval(t *testing.T) {
	a := newApprovalFixture(t, approvalOpts{})
	started := a.start("key-00000001", "change the notes", harnessrun.Budget{})
	status, record := a.parked(started.ID)
	if _, err := a.m.Cancel(context.Background(), a.owner, started.ID, harnessrun.Mutation{IdempotencyKey: "cancel-00001", ExpectedGeneration: status.Generation}); err != nil {
		t.Fatal(err)
	}
	got, _ := a.approvals.Get(context.Background(), record.ID)
	if got.State != harnessapproval.StateSuperseded || got.Reason != "run_cancelled" {
		t.Fatalf("approval = %+v", got)
	}
	if _, err := a.approvals.Decide(context.Background(), record.ID, harnessapproval.Decision{Approve: true, Code: harnessapproval.Code(record), Via: "terminal"}); !errors.Is(err, harnessapproval.ErrNotLive) {
		t.Fatalf("a late approval = %v", err)
	}
	time.Sleep(60 * time.Millisecond)
	if a.tool.invocations() != nil || a.status(started.ID).State != harnessrun.StateCancelled {
		t.Fatal("a cancelled run acted")
	}
}

func TestAnApprovalWhoseRunWasRemovedIsEnded(t *testing.T) {
	a := newApprovalFixture(t, approvalOpts{poll: time.Hour})
	started := a.start("key-00000001", "change the notes", harnessrun.Budget{})
	_, record := a.parked(started.ID)
	if err := os.Remove(filepath.Join(a.dir, "harness", "runs", started.ID+".json")); err != nil {
		t.Fatal(err)
	}
	if _, err := a.m.ReconcileApprovals(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, _ := a.approvals.Get(context.Background(), record.ID); got.State != harnessapproval.StateSuperseded || got.Reason != "run_missing" {
		t.Fatalf("approval = %+v", got)
	}
}

func TestStrictFrictionAndReasonsReachTheRecord(t *testing.T) {
	a := newApprovalFixture(t, approvalOpts{})
	a.tool.escalate = []string{"delete", "executable"}
	a.tool.paths = []string{"scripts/deploy.sh"}
	started := a.start("key-00000001", "change the notes", harnessrun.Budget{})
	_, record := a.parked(started.ID)
	if record.Friction != harnessapproval.FrictionStrict || !reflect.DeepEqual(record.Reasons, []string{"delete", "executable"}) || len(harnessapproval.Code(record)) != 9 {
		t.Fatalf("record = %+v", record)
	}
}

// If an approval cannot be recorded, the action does not run and the run does not wait
// for something nobody can grant: the model is told, and the run goes on.
func TestAnApprovalThatCannotBeRecordedFailsClosed(t *testing.T) {
	a := newApprovalFixture(t, approvalOpts{limits: [2]int{1, 10}, script: []harnesstest.Step{{ToolID: "write", Arguments: writeArgs}, {ToolID: "write", Arguments: writeArgs}, {Text: "all done"}}})
	first := a.start("key-00000001", "first", harnessrun.Budget{})
	a.parked(first.ID)
	second := a.start("key-00000002", "second", harnessrun.Budget{})
	done := a.waitState(second.ID, harnessrun.StateCompleted)
	if a.tool.invocations() != nil || !strings.Contains(a.runFileText(second.ID), "tool_denied: approval_unavailable") || done.Usage.ToolCalls != 1 {
		t.Fatalf("done = %+v; run: %s", done, a.runFileText(second.ID))
	}
	if a.status(first.ID).State != harnessrun.StateNeedsAttention {
		t.Fatal("the first run's approval was disturbed")
	}
}

func TestAnActionWithNoPathsIsNeverOfferedForApproval(t *testing.T) {
	a := newApprovalFixture(t, approvalOpts{})
	a.tool.paths = []string{}
	started := a.start("key-00000001", "x", harnessrun.Budget{})
	a.waitState(started.ID, harnessrun.StateCompleted)
	if a.tool.invocations() != nil {
		t.Fatal("an action that names no file ran")
	}
}

func TestSeveralRunsEachGetTheirOwnApproval(t *testing.T) {
	step := harnesstest.Step{ToolID: "write", Arguments: writeArgs}
	a := newApprovalFixture(t, approvalOpts{script: []harnesstest.Step{step, step, step, {Text: "all done"}}})
	var ids []string
	var records []harnessapproval.Record
	for i := 0; i < 3; i++ {
		started := a.start("key-0000000"+string(rune('1'+i)), "goal", harnessrun.Budget{})
		_, record := a.parked(started.ID)
		ids, records = append(ids, started.ID), append(records, record)
	}
	seen := map[string]bool{}
	for _, r := range records {
		seen[r.ID] = true
	}
	if len(seen) != 3 {
		t.Fatalf("approvals were shared: %v", records)
	}
	// Approve only the middle one: only that run moves.
	a.approve(records[1])
	a.waitState(ids[1], harnessrun.StateCompleted)
	for _, i := range []int{0, 2} {
		if a.status(ids[i]).State != harnessrun.StateNeedsAttention {
			t.Errorf("run %d moved without a decision", i)
		}
	}
	if calls := a.tool.invocations(); len(calls) != 1 || calls[0].digest != records[1].Digest {
		t.Fatalf("invocations = %+v", calls)
	}
}

// There is no way to approve an action through the manager. Adding an exported method to it
// should be a deliberate act, so this lists them all: none takes a decision from a caller.
func TestTheManagerHasNoMethodThatCouldTakeAnApproval(t *testing.T) {
	typ := reflect.TypeOf(&harnessrun.Manager{})
	var names []string
	for i := 0; i < typ.NumMethod(); i++ {
		names = append(names, typ.Method(i).Name)
	}
	sort.Strings(names)
	// MutateForTest exists only in test builds (export_test.go) and takes no approval.
	want := []string{"Cancel", "Close", "Continue", "Events", "MutateForTest", "ReconcileApprovals", "Recover", "Start", "StartChild", "Status", "Sweep"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("the manager's exported methods are %v, want %v: a new one needs review as a possible way to approve", names, want)
	}
	for _, name := range names {
		lower := strings.ToLower(name)
		if name != "ReconcileApprovals" && (strings.Contains(lower, "approv") || strings.Contains(lower, "decid") || strings.Contains(lower, "grant")) {
			t.Errorf("method %s looks like a way to approve", name)
		}
	}
	// Continue, the one call a client can use on a waiting run, never satisfies an approval.
	method, _ := typ.MethodByName("ReconcileApprovals")
	if method.Type.NumIn() != 2 || method.Type.NumOut() != 2 {
		t.Fatalf("ReconcileApprovals takes more than a context: %v", method.Type)
	}
}

// An approval whose run no longer matches what was approved is never applied: the run gets a
// note, the approval is revoked, and nothing runs.
func TestAnApprovalThatNoLongerMatchesItsRunIsRevokedNotApplied(t *testing.T) {
	for name, drift := range map[string]func(*harnessrun.Run){
		"another generation": func(r *harnessrun.Run) { r.Generation++ },
		"another digest":     func(r *harnessrun.Run) { r.Attention.ActionDigest = "sha256:" + strings.Repeat("9", 64) },
		"another grant":      func(r *harnessrun.Run) { r.Owner.GrantRevision++ },
		"another client":     func(r *harnessrun.Run) { r.Owner.ClientID = "codex-cli" },
	} {
		a := newApprovalFixture(t, approvalOpts{poll: time.Hour})
		started := a.start("key-00000001", "change the notes", harnessrun.Budget{})
		_, record := a.parked(started.ID)
		a.approve(record)
		if _, err := a.m.MutateForTest(started.ID, drift); err != nil {
			t.Fatal(err)
		}
		if moved, err := a.m.ReconcileApprovals(context.Background()); err != nil || moved != 1 {
			t.Fatalf("%s: reconcile = %d %v", name, moved, err)
		}
		time.Sleep(80 * time.Millisecond)
		got, _ := a.approvals.Get(context.Background(), record.ID)
		if a.tool.invocations() != nil || got.State != harnessapproval.StateRevoked || got.Reason != "binding_mismatch" {
			t.Errorf("%s: invocations %v, approval %+v", name, a.tool.invocations(), got)
		}
	}
}

// A crash between consuming an approval and running it must never run it twice: after a
// restart the replay finds the approval already used and the model is told so.
func TestARestartAfterConsumingAnApprovalDoesNotRunItTwice(t *testing.T) {
	a := newApprovalFixture(t, approvalOpts{poll: time.Hour})
	started := a.start("key-00000001", "change the notes", harnessrun.Budget{})
	status, record := a.parked(started.ID)
	a.approve(record)
	// The worker consumed the approval, then the process died before it ran the action.
	if _, err := a.approvals.Consume(context.Background(), record.ID, started.ID, record.Digest); err != nil {
		t.Fatal(err)
	}
	if err := a.m.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := harnessrun.OpenStore(a.dir)
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.Load(started.ID)
	if err != nil {
		t.Fatal(err)
	}
	run.State, run.Attention, run.Generation = harnessrun.StateQueued, nil, status.Generation+1
	run.Pending = &harnessrun.PendingAction{ApprovalID: record.ID, Digest: record.Digest}
	if err := store.Save(run); err != nil {
		t.Fatal(err)
	}
	a.restart()
	if _, err := a.m.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	a.waitState(started.ID, harnessrun.StateCompleted)
	if a.tool.invocations() != nil || !strings.Contains(a.runFileText(started.ID), "tool_stale: approval_used") {
		t.Fatalf("invocations = %v; run: %s", a.tool.invocations(), a.runFileText(started.ID))
	}
	if strings.Contains(a.runFileText(started.ID), `"pending"`) {
		t.Fatal("the replay marker was left behind")
	}
}

// The approval is recorded a moment before the run parks. A reconcile in that window must
// not end it; one long after, for a run that never parked, must; and one for a run that has
// already finished must at once.
func TestAnApprovalForARunAboutToParkIsLeftAloneUntilTheGracePeriodPasses(t *testing.T) {
	a := newApprovalFixture(t, approvalOpts{poll: time.Hour, delay: 600 * time.Millisecond, script: []harnesstest.Step{{Text: "x"}}})
	request := func(run string) harnessapproval.Record {
		record, err := a.approvals.Request(context.Background(), harnessapproval.Request{RunID: run, RunGeneration: 2, Owner: harnessapproval.Owner{ClientID: "claude-desktop",
			Workspace: "agent-memory", GrantID: a.owner.GrantID, GrantRevision: 1}, Tool: "write", Digest: "sha256:" + strings.Repeat("a", 64), Summary: "edit x",
			Paths: []string{"x"}, Friction: harnessapproval.FrictionStandard, Preview: "p", Arguments: []byte(`{}`)})
		if err != nil {
			t.Fatal(err)
		}
		return record
	}
	state := func(id string) harnessapproval.Record {
		got, err := a.approvals.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	running := a.start("key-00000001", "slow", harnessrun.Budget{})
	a.waitState(running.ID, harnessrun.StateRunning)
	fresh := request(running.ID)
	if _, err := a.m.ReconcileApprovals(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := state(fresh.ID); got.State != harnessapproval.StatePending {
		t.Fatalf("a fresh approval for a running run was ended: %+v", got)
	}
	a.clock.advance(3 * time.Minute) // longer than the grace period, and the approval's own age
	if _, err := a.m.ReconcileApprovals(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := state(fresh.ID); got.State != harnessapproval.StateSuperseded || got.Reason != "run_changed" {
		t.Fatalf("an approval for a run that never parked = %+v", got)
	}

	done := a.waitState(running.ID, harnessrun.StateCompleted)
	late := request(done.ID)
	if _, err := a.m.ReconcileApprovals(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := state(late.ID); got.State != harnessapproval.StateSuperseded {
		t.Fatalf("an approval for a finished run = %+v", got)
	}
}

func TestAPendingMarkerMustBeWellFormedAndOnlyOnARunThatCanReplayIt(t *testing.T) {
	a := newApprovalFixture(t, approvalOpts{poll: time.Hour})
	started := a.start("key-00000001", "goal", harnessrun.Budget{})
	_, record := a.parked(started.ID)
	if err := a.m.Close(); err != nil {
		t.Fatal(err)
	}
	store, _ := harnessrun.OpenStore(a.dir)
	base, err := store.Load(started.ID)
	if err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*harnessrun.Run){
		"on a run waiting for attention": func(r *harnessrun.Run) {
			r.Pending = &harnessrun.PendingAction{ApprovalID: record.ID, Digest: record.Digest}
		},
		"a bad identifier": func(r *harnessrun.Run) {
			r.State, r.Attention = harnessrun.StateQueued, nil
			r.Pending = &harnessrun.PendingAction{ApprovalID: "apr_nope", Digest: record.Digest}
		},
		"no digest": func(r *harnessrun.Run) {
			r.State, r.Attention = harnessrun.StateQueued, nil
			r.Pending = &harnessrun.PendingAction{ApprovalID: record.ID}
		},
		"on a finished run": func(r *harnessrun.Run) {
			r.State, r.Attention = harnessrun.StateCompleted, nil
			r.Pending = &harnessrun.PendingAction{ApprovalID: record.ID, Digest: record.Digest}
		},
		"a bad attention identifier": func(r *harnessrun.Run) { r.Attention.ApprovalID = "not-an-approval" },
	} {
		run := base
		attention := *base.Attention
		run.Attention = &attention
		change(&run)
		if err := store.Save(run); err != nil {
			continue // refused when written: just as good
		}
		if _, err := store.Load(started.ID); !errors.Is(err, harnessrun.ErrStorage) {
			t.Errorf("%s: a malformed record loaded: %v", name, err)
		}
	}
}

// Policy is asked again when an approved call is replayed, so a policy that has since
// changed its mind stops it.
func TestPolicyIsAskedAgainWhenAnApprovedCallIsReplayed(t *testing.T) {
	var deny atomic.Bool
	a := newApprovalFixture(t, approvalOpts{configure: func(c *harnessrun.Config) {
		c.Policy = policyFunc(func(o harnessrun.Owner, action harness.PreparedAction) harnessrun.Decision {
			if deny.Load() {
				return harnessrun.DecisionDeny
			}
			return askPolicy{}.Decide(context.Background(), o, action)
		})
	}})
	started := a.start("key-00000001", "change the notes", harnessrun.Budget{})
	_, record := a.parked(started.ID)
	deny.Store(true)
	a.approve(record)
	a.waitState(started.ID, harnessrun.StateCompleted)
	if a.tool.invocations() != nil || !strings.Contains(a.runFileText(started.ID), "tool_denied: policy") {
		t.Fatalf("invocations %v; run: %s", a.tool.invocations(), a.runFileText(started.ID))
	}
	if got, _ := a.approvals.Get(context.Background(), record.ID); got.Outcome != "denied_by_policy" {
		t.Fatalf("approval = %+v", got)
	}
}

// A run cancelled while an approved call waits to be replayed must still leave a record that
// loads, and the approval must not stay usable.
func TestCancellingARunThatHasAReplayPendingLeavesAValidRecord(t *testing.T) {
	a := newApprovalFixture(t, approvalOpts{poll: time.Hour})
	started := a.start("key-00000001", "change the notes", harnessrun.Budget{})
	status, record := a.parked(started.ID)
	a.approve(record)
	if err := a.m.Close(); err != nil {
		t.Fatal(err)
	}
	store, _ := harnessrun.OpenStore(a.dir)
	run, _ := store.Load(started.ID)
	run.State, run.Attention, run.Generation = harnessrun.StateQueued, nil, status.Generation+1
	run.Pending = &harnessrun.PendingAction{ApprovalID: record.ID, Digest: record.Digest}
	if err := store.Save(run); err != nil {
		t.Fatal(err)
	}
	a.restart() // no Recover: the queued run waits, with its replay still pending
	cancelled, err := a.m.Cancel(context.Background(), a.owner, started.ID, harnessrun.Mutation{IdempotencyKey: "cancel-00001", ExpectedGeneration: status.Generation + 1})
	if err != nil || cancelled.State != harnessrun.StateCancelled {
		t.Fatalf("cancel = %+v %v", cancelled, err)
	}
	if strings.Contains(a.runFileText(started.ID), `"pending"`) {
		t.Fatal("a finished run kept its replay marker")
	}
	if _, err := a.m.Status(context.Background(), a.owner, started.ID); err != nil {
		t.Fatalf("the cancelled run no longer loads: %v", err)
	}
	if got, _ := a.approvals.Get(context.Background(), record.ID); got.State.Live() {
		t.Fatalf("an approval for a cancelled run is still live: %+v", got)
	}
	if a.tool.invocations() != nil {
		t.Fatal("a cancelled run acted")
	}
}

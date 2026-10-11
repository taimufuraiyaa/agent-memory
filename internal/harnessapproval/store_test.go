package harnessapproval

import (
	"context"
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
)

var bg = context.Background()

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type fixture struct {
	t     *testing.T
	dir   string
	clock *clock
	store *Store
}

func newFixture(t *testing.T, opts ...Option) *fixture {
	t.Helper()
	dir := t.TempDir()
	c := &clock{t: time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)}
	opts = append([]Option{WithClock(c.now)}, opts...)
	return &fixture{t: t, dir: dir, clock: c, store: Open(dir, opts...)}
}

func digestOf(n int) string { return fmt.Sprintf("sha256:%064x", n) }

func request(run string, n int) Request {
	return Request{RunID: run, RunGeneration: 3, Owner: Owner{ClientID: "claude-desktop", Workspace: "agent-memory", GrantID: "grant-1", GrantRevision: 1},
		Tool: "edit_file", Digest: digestOf(n), Summary: "edit internal/app/app.go", Paths: []string{"internal/app/app.go"}, Friction: FrictionStandard,
		Preview: "--- a/internal/app/app.go\n+++ b/internal/app/app.go\n@@\n-old line\n+new line\n", Arguments: []byte(`{"path":"internal/app/app.go","edits":[{"old_text":"old","new_text":"new"}]}`)}
}

func (f *fixture) request(run string, n int) Record {
	f.t.Helper()
	record, err := f.store.Request(bg, request(run, n))
	if err != nil {
		f.t.Fatal(err)
	}
	return record
}

func (f *fixture) approve(record Record) Record {
	f.t.Helper()
	decided, err := f.store.Decide(bg, record.ID, Decision{Approve: true, Code: Code(record), Via: "terminal"})
	if err != nil {
		f.t.Fatal(err)
	}
	return decided
}

func (f *fixture) raw(id string) string {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.dir, dirName, id+".json"))
	if err != nil {
		f.t.Fatal(err)
	}
	return string(data)
}

func TestRequestRejectsEverythingMalformed(t *testing.T) {
	f := newFixture(t)
	mutate := func(change func(*Request)) Request {
		r := request("run_1", 1)
		change(&r)
		return r
	}
	for name, req := range map[string]Request{
		"no run":            mutate(func(r *Request) { r.RunID = "" }),
		"run with a slash":  mutate(func(r *Request) { r.RunID = "run/../x" }),
		"no generation":     mutate(func(r *Request) { r.RunGeneration = 0 }),
		"no client":         mutate(func(r *Request) { r.Owner.ClientID = "" }),
		"client with space": mutate(func(r *Request) { r.Owner.ClientID = "a b" }),
		"no grant revision": mutate(func(r *Request) { r.Owner.GrantRevision = 0 }),
		"uppercase tool":    mutate(func(r *Request) { r.Tool = "Edit" }),
		"short digest":      mutate(func(r *Request) { r.Digest = "sha256:abc" }),
		"unprefixed digest": mutate(func(r *Request) { r.Digest = strings.Repeat("a", 64) }),
		"unknown friction":  mutate(func(r *Request) { r.Friction = "mild" }),
		"empty summary":     mutate(func(r *Request) { r.Summary = "" }),
		"multiline summary": mutate(func(r *Request) { r.Summary = "a\nb" }),
		"long summary":      mutate(func(r *Request) { r.Summary = strings.Repeat("a", 300) }),
		"no paths":          mutate(func(r *Request) { r.Paths = nil }),
		"empty path":        mutate(func(r *Request) { r.Paths = []string{""} }),
		"control in a path": mutate(func(r *Request) { r.Paths = []string{"a\x1bb"} }),
		"too many paths": mutate(func(r *Request) {
			r.Paths = make([]string, MaxPaths+1)
			for i := range r.Paths {
				r.Paths[i] = fmt.Sprintf("f%d", i)
			}
		}),
		"bad reason":         mutate(func(r *Request) { r.Reasons = []string{"Has Space"} }),
		"too many reasons":   mutate(func(r *Request) { r.Reasons = []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"} }),
		"control in preview": mutate(func(r *Request) { r.Preview = "x\x1b[31mred" }),
		"huge preview":       mutate(func(r *Request) { r.Preview = strings.Repeat("a", MaxPreviewBytes+1) }),
		"no arguments":       mutate(func(r *Request) { r.Arguments = nil }),
		"invalid arguments":  mutate(func(r *Request) { r.Arguments = []byte(`{"path":`) }),
		"huge arguments":     mutate(func(r *Request) { r.Arguments = []byte(`"` + strings.Repeat("a", MaxArgumentBytes) + `"`) }),
	} {
		if _, err := f.store.Request(bg, req); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: error = %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(f.dir, dirName)); err == nil {
		t.Error("a rejected request created the approval directory")
	}
}

func TestRecordsArePrivateAndAuditIsContentFree(t *testing.T) {
	f := newFixture(t)
	req := request("run_1", 1)
	req.Preview = "PREVIEW-SECRET-TEXT\n"
	req.Arguments = []byte(`{"new_text":"ARGUMENT-SECRET-TEXT"}`)
	record, err := f.store.Request(bg, req)
	if err != nil {
		t.Fatal(err)
	}
	if !idRE.MatchString(record.ID) || record.State != StatePending || record.Arguments != nil || record.Preview == "" {
		t.Fatalf("record = %+v", record)
	}
	if !record.ExpiresAt.Equal(f.clock.now().Add(DefaultTTL)) {
		t.Fatalf("expires = %v", record.ExpiresAt)
	}
	dirInfo, _ := os.Stat(filepath.Join(f.dir, dirName))
	fileInfo, _ := os.Stat(filepath.Join(f.dir, dirName, record.ID+".json"))
	auditInfo, _ := os.Stat(filepath.Join(f.dir, dirName, auditName))
	if dirInfo.Mode().Perm() != 0o700 || fileInfo.Mode().Perm() != 0o600 || auditInfo.Mode().Perm() != 0o600 {
		t.Fatalf("modes: dir %v file %v audit %v", dirInfo.Mode().Perm(), fileInfo.Mode().Perm(), auditInfo.Mode().Perm())
	}
	f.approve(record)
	if _, err := f.store.Consume(bg, record.ID, "run_1", digestOf(1)); err != nil {
		t.Fatal(err)
	}
	audit, _ := os.ReadFile(filepath.Join(f.dir, dirName, auditName))
	if strings.Contains(string(audit), "SECRET") || !strings.Contains(string(audit), `"event":"consumed"`) {
		t.Fatalf("audit = %s", audit)
	}
	if strings.Contains(f.raw(record.ID), "SECRET") {
		t.Fatal("a consumed record still holds content")
	}
	if entries, _ := os.ReadDir(filepath.Join(f.dir, dirName)); func() bool {
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".tmp") {
				return true
			}
		}
		return false
	}() {
		t.Fatal("a temporary file was left behind")
	}
}

func TestACodeIsDerivedTypedAndRateLimited(t *testing.T) {
	f := newFixture(t)
	standard := f.request("run_1", 1)
	strictReq := request("run_2", 2)
	strictReq.Friction = FrictionStrict
	strict, err := f.store.Request(bg, strictReq)
	if err != nil {
		t.Fatal(err)
	}
	std, str := Code(standard), Code(strict)
	if len(std) != 4 || len(str) != 9 || str[4] != '-' || std != Code(standard) || std == Code(f.request("run_3", 3)) {
		t.Fatalf("codes: standard %q strict %q", std, str)
	}
	for _, c := range std + strings.ReplaceAll(str, "-", "") {
		if !strings.ContainsRune(codeAlphabet, c) {
			t.Fatalf("code %q uses %q outside the alphabet", std+str, c)
		}
	}
	// Typed forms that differ only in case, spacing or the dash are the same code.
	for _, typed := range []string{strings.ToUpper(str), " " + str + " ", strings.ReplaceAll(str, "-", ""), strings.ReplaceAll(str, "-", " ")} {
		if _, err := f.store.Decide(bg, strict.ID, Decision{Approve: true, Code: typed, Via: "terminal"}); err != nil {
			t.Fatalf("typed %q: %v", typed, err)
		}
		break
	}
	// Wrong codes are refused, counted, and end the approval after the limit.
	target := f.request("run_4", 4)
	for i := 1; i <= MaxCodeFailures; i++ {
		wrong, err := f.store.Decide(bg, target.ID, Decision{Approve: true, Code: "zzzz", Via: "terminal"})
		if !errors.Is(err, ErrCode) || wrong.CodeFailures != i {
			t.Fatalf("attempt %d: %v %+v", i, err, wrong)
		}
	}
	after, _ := f.store.Get(bg, target.ID)
	if after.State != StateDenied || after.Reason != "too_many_codes" || after.Preview != "" {
		t.Fatalf("after the limit = %+v", after)
	}
	if _, err := f.store.Decide(bg, target.ID, Decision{Approve: true, Code: Code(target), Via: "terminal"}); !errors.Is(err, ErrNotLive) {
		t.Fatalf("the right code after denial = %v", err)
	}
	// Empty and partial codes never match.
	partial := f.request("run_5", 5)
	for _, typed := range []string{"", Code(partial)[:3], Code(partial) + "x"} {
		if _, err := f.store.Decide(bg, partial.ID, Decision{Approve: true, Code: typed, Via: "terminal"}); !errors.Is(err, ErrCode) {
			t.Errorf("typed %q = %v", typed, err)
		}
	}
}

func TestDecisionsApplyOnceAndDropContent(t *testing.T) {
	f := newFixture(t)
	approved := f.request("run_1", 1)
	got := f.approve(approved)
	if got.State != StateApproved || got.DecidedVia != "terminal" || got.DecidedAt.IsZero() || got.Preview == "" {
		t.Fatalf("approved = %+v", got)
	}
	if _, err := f.store.Decide(bg, approved.ID, Decision{Approve: true, Code: Code(approved), Via: "terminal"}); !errors.Is(err, ErrNotLive) {
		t.Fatalf("a second approval = %v", err)
	}
	if _, err := f.store.Decide(bg, approved.ID, Decision{Via: "terminal"}); !errors.Is(err, ErrNotLive) {
		t.Fatalf("a denial after approval = %v", err)
	}

	denied := f.request("run_2", 2)
	got, err := f.store.Decide(bg, denied.ID, Decision{Via: "terminal", Stop: true})
	if err != nil || got.State != StateDenied || !got.Stop || got.Preview != "" || strings.Contains(f.raw(denied.ID), "new_text") {
		t.Fatalf("denied = %+v %v", got, err)
	}
	if _, err := f.store.Decide(bg, denied.ID, Decision{Approve: true, Code: Code(denied), Via: "terminal"}); !errors.Is(err, ErrNotLive) {
		t.Fatalf("approval after denial = %v", err)
	}
	if _, err := f.store.Decide(bg, approved.ID, Decision{Approve: true, Code: Code(approved), Via: "bad channel!"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a bad channel = %v", err)
	}
	for _, id := range []string{"", "apr_x", "../x", "apr_" + strings.Repeat("0", 32)} {
		if _, err := f.store.Decide(bg, id, Decision{Via: "terminal"}); !errors.Is(err, ErrNotFound) {
			t.Errorf("Decide(%q) = %v", id, err)
		}
	}
}

func TestAnApprovalExpiresBeforeAndAfterItIsDecided(t *testing.T) {
	f := newFixture(t, WithTTL(10*time.Minute))
	waiting := f.request("run_1", 1)
	f.clock.advance(10 * time.Minute)
	if _, err := f.store.Decide(bg, waiting.ID, Decision{Approve: true, Code: Code(waiting), Via: "terminal"}); !errors.Is(err, ErrExpired) {
		t.Fatalf("approving after expiry = %v", err)
	}
	if got, _ := f.store.Get(bg, waiting.ID); got.State != StateExpired || got.Preview != "" || strings.Contains(f.raw(waiting.ID), "new_text") {
		t.Fatalf("expired = %+v", got)
	}

	decided := f.request("run_2", 2)
	f.approve(decided)
	f.clock.advance(10*time.Minute - time.Second)
	if _, err := f.store.Consume(bg, decided.ID, "run_2", digestOf(2)); err != nil {
		t.Fatalf("consuming just before expiry = %v", err)
	}
	late := f.request("run_3", 3)
	f.approve(late)
	f.clock.advance(10 * time.Minute)
	if _, err := f.store.Consume(bg, late.ID, "run_3", digestOf(3)); !errors.Is(err, ErrExpired) {
		t.Fatalf("consuming an expired approval = %v", err)
	}
	if got, _ := f.store.Get(bg, late.ID); got.State != StateExpired {
		t.Fatalf("state = %s", got.State)
	}
	for _, ttl := range []time.Duration{0, time.Second, 48 * time.Hour} {
		if s := Open(t.TempDir(), WithTTL(ttl)); s.ttl != DefaultTTL {
			t.Errorf("an out-of-range ttl %v was accepted", ttl)
		}
	}
}

func TestConsumeIsOnceBoundAndReturnsTheArguments(t *testing.T) {
	f := newFixture(t)
	record := f.request("run_1", 1)
	if _, err := f.store.Consume(bg, record.ID, "run_1", digestOf(1)); !errors.Is(err, ErrNotLive) {
		t.Fatalf("consuming a pending approval = %v", err)
	}
	f.approve(record)
	for name, tc := range map[string]struct{ run, digest string }{
		"another run":    {"run_2", digestOf(1)},
		"another digest": {"run_1", digestOf(2)},
		"empty digest":   {"run_1", ""},
	} {
		if _, err := f.store.Consume(bg, record.ID, tc.run, tc.digest); !errors.Is(err, ErrNotUsable) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if got, _ := f.store.Get(bg, record.ID); got.State != StateApproved {
		t.Fatalf("a mismatched consume changed the state to %s", got.State)
	}
	used, err := f.store.Consume(bg, record.ID, "run_1", digestOf(1))
	if err != nil || used.State != StateConsumed || !json.Valid(used.Arguments) || !strings.Contains(string(used.Arguments), "new_text") {
		t.Fatalf("consume = %+v %v", used, err)
	}
	if _, err := f.store.Consume(bg, record.ID, "run_1", digestOf(1)); !errors.Is(err, ErrNotLive) {
		t.Fatalf("a replay = %v", err)
	}
	if strings.Contains(f.raw(record.ID), "new_text") {
		t.Fatal("the arguments stayed on disk after consumption")
	}
	pending := f.request("run_9", 9)
	for _, id := range []string{pending.ID} {
		if err := f.store.Finish(bg, id, "applied"); !errors.Is(err, ErrNotLive) {
			t.Fatalf("an outcome for an unconsumed approval = %v", err)
		}
	}
	if err := f.store.Finish(bg, record.ID, "applied"); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Finish(bg, record.ID, "applied"); !errors.Is(err, ErrNotLive) {
		t.Fatalf("a second outcome = %v", err)
	}
	if err := f.store.Finish(bg, record.ID, "Bad Outcome"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a bad outcome = %v", err)
	}
	if got, _ := f.store.Get(bg, record.ID); got.Outcome != "applied" {
		t.Fatalf("outcome = %q", got.Outcome)
	}
}

func TestConcurrentConsumersHaveExactlyOneWinner(t *testing.T) {
	f := newFixture(t)
	record := f.request("run_1", 1)
	f.approve(record)
	var wins, refused atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Each goroutine uses its own handle on the same directory, like separate processes.
			store := Open(f.dir, WithClock(f.clock.now))
			switch _, err := store.Consume(bg, record.ID, "run_1", digestOf(1)); {
			case err == nil:
				wins.Add(1)
			case errors.Is(err, ErrNotLive):
				refused.Add(1)
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 || refused.Load() != 23 {
		t.Fatalf("wins=%d refused=%d", wins.Load(), refused.Load())
	}
	if n, err := f.store.VerifyAudit(bg); err != nil || n != 3 { // requested, approved, consumed
		t.Fatalf("audit = %d %v", n, err)
	}
}

func TestResolveEndsALiveApprovalOnly(t *testing.T) {
	f := newFixture(t)
	record := f.request("run_1", 1)
	f.approve(record)
	got, err := f.store.Resolve(bg, record.ID, StateRevoked, "grant_revoked")
	if err != nil || got.State != StateRevoked || got.Reason != "grant_revoked" || strings.Contains(f.raw(record.ID), "new_text") {
		t.Fatalf("resolve = %+v %v", got, err)
	}
	if _, err := f.store.Consume(bg, record.ID, "run_1", digestOf(1)); !errors.Is(err, ErrNotLive) {
		t.Fatalf("consuming a revoked approval = %v", err)
	}
	if again, err := f.store.Resolve(bg, record.ID, StateRevoked, "grant_revoked"); err != nil || again.State != StateRevoked {
		t.Fatalf("an idempotent resolve = %+v %v", again, err)
	}
	if _, err := f.store.Resolve(bg, record.ID, StateExpired, "expired"); !errors.Is(err, ErrNotLive) {
		t.Fatalf("resolving a revoked approval to something else = %v", err)
	}
	used := f.request("run_2", 2)
	f.approve(used)
	if _, err := f.store.Consume(bg, used.ID, "run_2", digestOf(2)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Resolve(bg, used.ID, StateRevoked, "grant_revoked"); !errors.Is(err, ErrNotLive) {
		t.Fatalf("resolving a consumed approval = %v", err)
	}
	other := f.request("run_3", 3)
	for name, tc := range map[string]struct {
		state  State
		reason string
	}{"approved target": {StateApproved, "x"}, "consumed target": {StateConsumed, "x"}, "pending target": {StatePending, "x"}, "bad reason": {StateRevoked, "Bad Reason"}} {
		if _, err := f.store.Resolve(bg, other.ID, tc.state, tc.reason); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestARunHasOneLiveApprovalAndTheStoreIsBounded(t *testing.T) {
	f := newFixture(t, WithLimits(3, 6))
	first := f.request("run_1", 1)
	second := f.request("run_1", 2)
	if got, _ := f.store.Get(bg, first.ID); got.State != StateSuperseded || got.Preview != "" {
		t.Fatalf("the older approval for a run = %+v", got)
	}
	if got, _ := f.store.Get(bg, second.ID); got.State != StatePending {
		t.Fatalf("the newer approval = %+v", got)
	}
	f.request("run_2", 3)
	f.request("run_3", 4)
	if _, err := f.store.Request(bg, request("run_4", 5)); !errors.Is(err, ErrBusy) {
		t.Fatalf("a fourth live approval = %v", err)
	}
	f.clock.advance(DefaultTTL)
	f.request("run_5", 6) // the three that expired no longer count

	// Resolved records are removed oldest first once there are too many; live ones never are,
	// even when a live one is the oldest of all.
	f.clock.advance(DefaultTTL)
	oldest := f.request("run_oldest", 98)
	f.clock.advance(time.Second)
	for i := 0; i < 8; i++ {
		record := f.request(fmt.Sprintf("run_x%d", i), 10+i)
		if _, err := f.store.Decide(bg, record.ID, Decision{Via: "terminal"}); err != nil {
			t.Fatal(err)
		}
		f.clock.advance(time.Second)
	}
	live := f.request("run_live", 99)
	all, err := f.store.List(bg, ListOptions{})
	if err != nil || len(all) > 7 {
		t.Fatalf("records kept = %d, %v", len(all), err)
	}
	for _, id := range []string{live.ID, oldest.ID} {
		if got, err := f.store.Get(bg, id); err != nil || got.State != StatePending {
			t.Fatalf("a live approval was pruned: %+v %v", got, err)
		}
	}
}

func TestListAndGetHideArgumentsAndFilter(t *testing.T) {
	f := newFixture(t)
	a := f.request("run_1", 1)
	f.clock.advance(time.Second) // the list is newest first, so the two must differ in time
	other := request("run_2", 2)
	other.Owner.Workspace = "other"
	b, err := f.store.Request(bg, other)
	if err != nil {
		t.Fatal(err)
	}
	f.store.Decide(bg, b.ID, Decision{Via: "terminal"})
	all, _ := f.store.List(bg, ListOptions{})
	if len(all) != 2 || all[0].ID != b.ID || all[1].ID != a.ID {
		t.Fatalf("list order = %v", all)
	}
	for _, r := range all {
		if r.Arguments != nil || r.Preview != "" {
			t.Fatalf("a listing carried content: %+v", r)
		}
	}
	if live, _ := f.store.List(bg, ListOptions{LiveOnly: true}); len(live) != 1 || live[0].ID != a.ID {
		t.Fatalf("live = %v", live)
	}
	if mine, _ := f.store.List(bg, ListOptions{Workspace: "other"}); len(mine) != 1 || mine[0].ID != b.ID {
		t.Fatalf("workspace = %v", mine)
	}
	if one, _ := f.store.List(bg, ListOptions{Limit: 1}); len(one) != 1 {
		t.Fatalf("limit = %v", one)
	}
	if got, _ := f.store.Get(bg, a.ID); got.Arguments != nil || got.Preview == "" {
		t.Fatalf("Get = %+v", got)
	}
}

func TestDamagedAndUnsafeStorageFailsClosed(t *testing.T) {
	f := newFixture(t)
	good := f.request("run_1", 1)
	dir := filepath.Join(f.dir, dirName)

	// A damaged record never authorizes anything and never stops the others being listed.
	bad := "apr_" + strings.Repeat("a", 32)
	if err := os.WriteFile(filepath.Join(dir, bad+".json"), []byte(`{"schema_version":1,"unknown_field":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	loose := "apr_" + strings.Repeat("b", 32)
	if err := os.WriteFile(filepath.Join(dir, loose+".json"), []byte(f.raw(good.ID)), 0o644); err != nil {
		t.Fatal(err)
	}
	if list, err := f.store.List(bg, ListOptions{}); err != nil || len(list) != 1 || list[0].ID != good.ID {
		t.Fatalf("list = %v, %v", list, err)
	}
	for _, id := range []string{bad, loose} {
		if _, err := f.store.Get(bg, id); !errors.Is(err, ErrStorage) {
			t.Errorf("Get(%s) = %v", id, err)
		}
		if _, err := f.store.Decide(bg, id, Decision{Approve: true, Code: "aaaa", Via: "terminal"}); !errors.Is(err, ErrStorage) {
			t.Errorf("Decide(%s) = %v", id, err)
		}
	}
	// A valid record in a loose file, and a valid record with one extra field, are refused.
	goodPath := filepath.Join(dir, good.ID+".json")
	valid := f.raw(good.ID)
	if err := os.Chmod(goodPath, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Get(bg, good.ID); !errors.Is(err, ErrStorage) {
		t.Fatalf("a record in a loose file = %v", err)
	}
	if err := os.Chmod(goodPath, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(goodPath, []byte(strings.Replace(valid, `"schema_version": 1,`, `"schema_version": 1, "extra": true,`, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Get(bg, good.ID); !errors.Is(err, ErrStorage) {
		t.Fatalf("a record with an unknown field = %v", err)
	}
	if err := os.WriteFile(goodPath, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Get(bg, good.ID); err != nil {
		t.Fatalf("the repaired record = %v", err)
	}
	// A record whose id does not match its file name is refused.
	swapped := "apr_" + strings.Repeat("c", 32)
	if err := os.WriteFile(filepath.Join(dir, swapped+".json"), []byte(f.raw(good.ID)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Get(bg, swapped); !errors.Is(err, ErrStorage) {
		t.Fatalf("a renamed record = %v", err)
	}
	// A resolved record that still carries content is damaged.
	resolved := f.request("run_2", 2)
	f.store.Decide(bg, resolved.ID, Decision{Via: "terminal"})
	tampered := strings.Replace(f.raw(resolved.ID), `"arguments"`, `"arguments"`, 1)
	var rec Record
	_ = json.Unmarshal([]byte(tampered), &rec)
	rec.Arguments = []byte(`{"x":1}`)
	data, _ := json.Marshal(rec)
	if err := os.WriteFile(filepath.Join(dir, resolved.ID+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Get(bg, resolved.ID); !errors.Is(err, ErrStorage) {
		t.Fatalf("a resolved record holding content = %v", err)
	}

	// A loose directory, a symlinked directory and a symlinked base are refused.
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Request(bg, request("run_3", 3)); !errors.Is(err, ErrStorage) {
		t.Fatalf("a loose directory = %v", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	elsewhere := t.TempDir()
	linked := t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(linked, dirName)); err != nil {
		t.Skip("symlinks unavailable")
	}
	if _, err := Open(linked).Request(bg, request("run_4", 4)); !errors.Is(err, ErrStorage) {
		t.Fatalf("a symlinked approval directory = %v", err)
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Fatal("a write went through the link")
	}
	base := filepath.Join(t.TempDir(), "base-link")
	if err := os.Symlink(elsewhere, base); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(base).Request(bg, request("run_5", 5)); !errors.Is(err, ErrStorage) {
		t.Fatalf("a symlinked base = %v", err)
	}
}

func TestTheLockIsCrossProcessAndReclaimedWhenStale(t *testing.T) {
	f := newFixture(t)
	f.request("run_1", 1)
	lock := filepath.Join(f.dir, dirName, lockName)
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(bg, 150*time.Millisecond)
	defer cancel()
	if _, err := f.store.List(ctx, ListOptions{}); err == nil {
		t.Fatal("a held lock did not block")
	}
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	if list, err := f.store.List(bg, ListOptions{}); err != nil || len(list) != 1 {
		t.Fatalf("after a stale lock: %v %v", list, err)
	}
	if _, err := os.Stat(lock); err == nil {
		t.Fatal("the lock was not released")
	}
}

func TestTheAuditChainDetectsTampering(t *testing.T) {
	f := newFixture(t)
	for i := 0; i < 3; i++ {
		r := f.request(fmt.Sprintf("run_%d", i), i+1)
		f.approve(r)
		f.store.Consume(bg, r.ID, fmt.Sprintf("run_%d", i), digestOf(i+1))
		f.store.Finish(bg, r.ID, "applied")
		f.store.Note(bg, r.ID, "undone", "")
	}
	n, err := f.store.VerifyAudit(bg)
	if err != nil || n != 15 {
		t.Fatalf("audit = %d %v", n, err)
	}
	path := filepath.Join(f.dir, dirName, auditName)
	original, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimRight(string(original), "\n"), "\n")
	rewrite := func(content string) {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for name, content := range map[string]string{
		"an edited line":   strings.Replace(string(original), `"event":"approved"`, `"event":"denied"`, 1),
		"a removed line":   strings.Join(append(append([]string{}, lines[:4]...), lines[5:]...), "\n") + "\n",
		"swapped lines":    strings.Join(append(append(append([]string{}, lines[:2]...), lines[3], lines[2]), lines[4:]...), "\n") + "\n",
		"a truncated tail": strings.Join(lines[:len(lines)-1], "\n")[:len(strings.Join(lines[:len(lines)-1], "\n"))-5] + "\n",
		"a forged line":    string(original) + `{"seq":16,"event":"approved"}` + "\n",
		"a removed head":   strings.Join(lines[1:], "\n") + "\n",
		"garbage":          string(original) + "not json\n",
	} {
		rewrite(content)
		if _, err := f.store.VerifyAudit(bg); !errors.Is(err, ErrAuditBroken) {
			t.Errorf("%s was not detected: %v", name, err)
		}
	}
	rewrite(string(original))
	if n, err := f.store.VerifyAudit(bg); err != nil || n != 15 {
		t.Fatalf("restored = %d %v", n, err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.VerifyAudit(bg); !errors.Is(err, ErrStorage) {
		t.Fatalf("a loose audit file = %v", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Note(bg, "apr_"+strings.Repeat("0", 32), "undone", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a note for an unknown approval = %v", err)
	}
	if err := f.store.Note(bg, "x", "Bad Event", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a bad event = %v", err)
	}
}

func TestTheAuditRotatesAndStaysChained(t *testing.T) {
	f := newFixture(t, WithAuditLimit(2<<10))
	r := f.request("run_1", 1)
	dir := filepath.Join(f.dir, dirName)
	for i := 0; i < 12; i++ {
		f.clock.advance(time.Second)
		if err := f.store.Note(bg, r.ID, "noted", ""); err != nil {
			t.Fatal(err)
		}
	}
	files, _ := filepath.Glob(filepath.Join(dir, "audit-*.jsonl"))
	if len(files) < 2 {
		t.Fatalf("rotated files = %v", files)
	}
	if n, err := f.store.VerifyAudit(bg); err != nil || n != 13 {
		t.Fatalf("the chain across rotations = %d %v", n, err)
	}
	// Removing a rotated file breaks the chain.
	if err := os.Remove(files[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.VerifyAudit(bg); !errors.Is(err, ErrAuditBroken) {
		t.Fatalf("a removed rotated file = %v", err)
	}
}

// An approval that cannot be recorded must not exist: a tampered or unwritable audit
// stops every change rather than letting one proceed unrecorded.
func TestNothingChangesWhenTheAuditCannotBeTrusted(t *testing.T) {
	f := newFixture(t)
	waiting := f.request("run_1", 1)
	approved := f.request("run_2", 2)
	f.approve(approved)
	path := filepath.Join(f.dir, dirName, auditName)
	good, _ := os.ReadFile(path)
	before := f.raw(waiting.ID)

	check := func(label string) {
		t.Helper()
		if _, err := f.store.Request(bg, request("run_9", 9)); !errors.Is(err, ErrStorage) {
			t.Errorf("%s: request = %v", label, err)
		}
		if _, err := f.store.Decide(bg, waiting.ID, Decision{Approve: true, Code: Code(waiting), Via: "terminal"}); !errors.Is(err, ErrStorage) {
			t.Errorf("%s: decide = %v", label, err)
		}
		if _, err := f.store.Consume(bg, approved.ID, "run_2", digestOf(2)); !errors.Is(err, ErrStorage) {
			t.Errorf("%s: consume = %v", label, err)
		}
		if _, err := f.store.Resolve(bg, waiting.ID, StateRevoked, "grant_revoked"); !errors.Is(err, ErrStorage) {
			t.Errorf("%s: resolve = %v", label, err)
		}
		if _, err := f.store.Get(bg, waiting.ID); !errors.Is(err, ErrStorage) {
			t.Errorf("%s: get = %v", label, err)
		}
		if f.raw(waiting.ID) != before {
			t.Errorf("%s: a record changed", label)
		}
		if entries, _ := os.ReadDir(filepath.Join(f.dir, dirName)); len(entries) != 3 { // two records and the audit, nothing else
			names := []string{}
			for _, e := range entries {
				names = append(names, e.Name())
			}
			t.Errorf("%s: directory = %v", label, names)
		}
	}
	lines := strings.Split(strings.TrimRight(string(good), "\n"), "\n")
	// A last line edited after it was sealed.
	if err := os.WriteFile(path, []byte(strings.Join(lines[:len(lines)-1], "\n")+"\n"+strings.Replace(lines[len(lines)-1], `"event":"approved"`, `"event":"denied"`, 1)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	check("a tampered last line")
	// A loose audit file.
	if err := os.WriteFile(path, good, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	check("a loose audit file")
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Request(bg, request("run_9", 9)); err != nil {
		t.Fatalf("after repair: %v", err)
	}
	// A symlinked audit file is refused and nothing is written through it.
	elsewhere := filepath.Join(t.TempDir(), "elsewhere.jsonl")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, path); err != nil {
		t.Skip("symlinks unavailable")
	}
	if _, err := f.store.Request(bg, request("run_10", 10)); !errors.Is(err, ErrStorage) {
		t.Fatalf("a symlinked audit = %v", err)
	}
	if _, err := os.Stat(elsewhere); err == nil {
		t.Fatal("the audit was written through a link")
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("no entropy") }

func TestAnIdentifierFailureCreatesNothing(t *testing.T) {
	f := newFixture(t, WithRandom(failingReader{}))
	if _, err := f.store.Request(bg, request("run_1", 1)); !errors.Is(err, ErrStorage) {
		t.Fatalf("error = %v", err)
	}
	if list, _ := f.store.List(bg, ListOptions{}); len(list) != 0 {
		t.Fatalf("records = %v", list)
	}
}

// A line that is correctly sealed but out of sequence is as wrong as one that is not sealed.
func TestAnOutOfSequenceAuditLineIsDetected(t *testing.T) {
	f := newFixture(t)
	r := f.request("run_1", 1)
	f.approve(r)
	path := filepath.Join(f.dir, dirName, auditName)
	data, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	var last auditLine
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &last); err != nil {
		t.Fatal(err)
	}
	forged := auditLine{Seq: last.Seq + 5, At: last.At, Event: "approved", Approval: last.Approval, Run: last.Run, Workspace: last.Workspace, Client: last.Client,
		Tool: last.Tool, Digest: last.Digest, Friction: last.Friction, Paths: last.Paths, State: last.State, Prev: last.Hash}
	forged.Hash = forged.seal()
	encoded, _ := json.Marshal(forged)
	if err := os.WriteFile(path, append(data, append(encoded, '\n')...), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.VerifyAudit(bg); !errors.Is(err, ErrAuditBroken) {
		t.Fatalf("a skipped sequence number = %v", err)
	}
}

func TestAnApprovalThatCannotBeRecordedDoesNotExist(t *testing.T) {
	f := newFixture(t)
	auditHook = func(event string) error {
		if event == "requested" {
			return errStorage("disk full")
		}
		return nil
	}
	defer func() { auditHook = nil }()
	if _, err := f.store.Request(bg, request("run_1", 1)); !errors.Is(err, ErrStorage) {
		t.Fatalf("error = %v", err)
	}
	auditHook = nil
	if list, _ := f.store.List(bg, ListOptions{}); len(list) != 0 {
		t.Fatalf("an unrecorded approval was left behind: %v", list)
	}
}

// Rewriting one line and sealing it again keeps that line valid and in sequence; only
// the next line's link to the old hash exposes it.
func TestAResealedLineBreaksTheLinkToTheNext(t *testing.T) {
	f := newFixture(t)
	r := f.request("run_1", 1)
	f.approve(r)
	f.store.Consume(bg, r.ID, "run_1", digestOf(1))
	path := filepath.Join(f.dir, dirName, auditName)
	data, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	var middle auditLine
	if err := json.Unmarshal([]byte(lines[1]), &middle); err != nil {
		t.Fatal(err)
	}
	middle.Event = "denied"
	middle.Hash = middle.seal()
	encoded, _ := json.Marshal(middle)
	lines[1] = string(encoded)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.VerifyAudit(bg); !errors.Is(err, ErrAuditBroken) {
		t.Fatalf("a resealed line = %v", err)
	}
}

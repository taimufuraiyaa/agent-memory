package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
	"github.com/taimufuraiyaa/agent-memory/internal/harness/harnesstest"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessapproval"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessproof"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessrun"
	"github.com/taimufuraiyaa/agent-memory/internal/harnesstools"
)

type approvalsEnv struct {
	t       *testing.T
	dataDir string
	root    string
	store   *harnessapproval.Store
}

func newApprovalsEnv(t *testing.T) *approvalsEnv {
	t.Helper()
	dataDir, root, _ := harnessFixture(t)
	return &approvalsEnv{t: t, dataDir: dataDir, root: root, store: harnessapproval.Open(dataDir)}
}

// terminal makes the commands believe stdin and stdout are a person's terminal.
func terminal(t *testing.T, on bool) {
	t.Helper()
	previous := interactive
	interactive = func(io.Reader, io.Writer) bool { return on }
	t.Cleanup(func() { interactive = previous })
}

func (e *approvalsEnv) run(stdin string, args ...string) (string, error) {
	e.t.Helper()
	cmd := NewRootCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(append([]string{"harness", "approvals"}, append(args, "--data-dir", e.dataDir)...))
	err := cmd.Execute()
	return out.String(), err
}

func (e *approvalsEnv) request(run string, n int, mutate ...func(*harnessapproval.Request)) harnessapproval.Record {
	e.t.Helper()
	req := harnessapproval.Request{RunID: run, RunGeneration: 3, Owner: harnessapproval.Owner{ClientID: "claude-desktop", Workspace: "ws", GrantID: strings.Repeat("b", 32), GrantRevision: 1},
		Tool: "edit_file", Digest: fmt.Sprintf("sha256:%064x", n+1), Summary: "edit notes.txt (1 edit)", Paths: []string{"notes.txt"},
		Friction: harnessapproval.FrictionStandard, Preview: "--- a/notes.txt\n+++ b/notes.txt\n@@ -1,1 +1,1 @@\n-old line\n+new line\n", Arguments: []byte(`{"path":"notes.txt"}`)}
	for _, m := range mutate {
		m(&req)
	}
	record, err := e.store.Request(context.Background(), req)
	if err != nil {
		e.t.Fatal(err)
	}
	return record
}

func (e *approvalsEnv) state(id string) harnessapproval.Record {
	e.t.Helper()
	record, err := e.store.Get(context.Background(), id)
	if err != nil {
		e.t.Fatal(err)
	}
	return record
}

func TestApprovingNeedsATerminalAndNeverAcceptsTheCodeFromAnywhereElse(t *testing.T) {
	terminal(t, false)
	e := newApprovalsEnv(t)
	record := e.request("run_1", 0)
	code := harnessapproval.Code(record)
	for name, tc := range map[string]struct {
		stdin string
		args  []string
	}{
		"a piped code":        {code + "\n", []string{"approve", record.ID}},
		"a piped yes":         {"yes\n", []string{"approve", record.ID}},
		"a code flag":         {"", []string{"approve", record.ID, "--code", code}},
		"a yes flag":          {"", []string{"approve", record.ID, "--yes"}},
		"an approve-all":      {code + "\n", []string{"approve", "--all"}},
		"several identifiers": {code + "\n", []string{"approve", record.ID, record.ID}},
	} {
		if out, err := e.run(tc.stdin, tc.args...); err == nil {
			t.Errorf("%s: approved without a terminal: %s", name, out)
		}
	}
	t.Setenv("AGENT_MEMORY_APPROVAL_CODE", code)
	if _, err := e.run(code+"\n", "approve", record.ID); err == nil {
		t.Error("the code was accepted from the environment")
	}
	if got := e.state(record.ID); got.State != harnessapproval.StatePending || got.CodeFailures != 0 {
		t.Fatalf("a refused approval changed the record: %+v", got)
	}
	// An applied change is not undone without a terminal either, and the file is left as it is.
	terminal(t, true)
	applied, _ := e.appliedEdit("old line\n", "old", "new")
	terminal(t, false)
	if out, err := e.run("undo\n", "undo", applied.ID); err == nil || e.notes() != "new line\n" {
		t.Errorf("undo ran without a terminal: %v %s; file %q", err, out, e.notes())
	}
}

type endlessReader struct{ read int }

func (r *endlessReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'a'
	}
	r.read += len(p)
	return len(p), nil
}

func TestAnAnswerIsReadFromABoundedAmountOfInput(t *testing.T) {
	stream := &endlessReader{}
	line, err := readLine(stream)
	if err != nil || len(line) != 64 || stream.read > 4096 {
		t.Fatalf("read %d bytes and kept %d (%v)", stream.read, len(line), err)
	}
	if line, err := readLine(strings.NewReader("abc\r\n")); err != nil || line != "abc" {
		t.Fatalf("a normal line = %q %v", line, err)
	}
	if _, err := readLine(strings.NewReader("")); err == nil {
		t.Fatal("end of input was an answer")
	}
}

func TestApprovingAtATerminalShowsTheChangeAndNeedsTheTypedCode(t *testing.T) {
	terminal(t, true)
	e := newApprovalsEnv(t)
	record := e.request("run_1", 0)
	code := harnessapproval.Code(record)

	// Pressing Enter, or declining, decides nothing.
	for _, answer := range []string{"\n", "deny\n", "no\n", "n\n", ""} {
		out, err := e.run(answer, "approve", record.ID)
		if answer == "" {
			if err == nil {
				t.Error("no answer was treated as a decision")
			}
		} else if err != nil || !strings.Contains(out, `"decided":false`) {
			t.Errorf("%q: %v %s", answer, err, out)
		}
		if got := e.state(record.ID); got.State != harnessapproval.StatePending || got.CodeFailures != 0 {
			t.Fatalf("%q decided something: %+v", answer, got)
		}
	}
	// The change and the code are on screen before the answer is read.
	out, _ := e.run("\n", "approve", record.ID)
	for _, want := range []string{record.ID, "edit notes.txt (1 edit)", "-old line", "+new line", "Workspace ws", "claude-desktop", code} {
		if !strings.Contains(out, want) {
			t.Errorf("the review lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "CARE") {
		t.Error("an ordinary change was marked as needing extra care")
	}
	// A wrong code is refused and counted; the right one, in any case or spacing, approves.
	if _, err := e.run("zzzz\n", "approve", record.ID); err == nil || !strings.Contains(err.Error(), "did not match") || !strings.Contains(err.Error(), "(4 attempts left)") {
		t.Fatalf("a wrong code = %v", err)
	}
	if got := e.state(record.ID); got.State != harnessapproval.StatePending || got.CodeFailures != 1 {
		t.Fatalf("after a wrong code: %+v", got)
	}
	out, err := e.run("  "+strings.ToUpper(code)+"  \n", "approve", record.ID)
	if err != nil || !strings.Contains(out, `"decided":true`) || !strings.Contains(out, `"state":"approved"`) {
		t.Fatalf("approve = %v %s", err, out)
	}
	if got := e.state(record.ID); got.State != harnessapproval.StateApproved || got.DecidedVia != "terminal" {
		t.Fatalf("after approving: %+v", got)
	}
	if _, err := e.run(code+"\n", "approve", record.ID); err == nil || !strings.Contains(err.Error(), "no longer waiting") {
		t.Fatalf("approving twice = %v", err)
	}
}

func TestTooManyWrongCodesDenyTheApproval(t *testing.T) {
	terminal(t, true)
	e := newApprovalsEnv(t)
	record := e.request("run_1", 0)
	var last error
	for i := 0; i < harnessapproval.MaxCodeFailures; i++ {
		_, last = e.run("zzzz\n", "approve", record.ID)
	}
	if last == nil || !strings.Contains(last.Error(), "denied") {
		t.Fatalf("the last attempt = %v", last)
	}
	if got := e.state(record.ID); got.State != harnessapproval.StateDenied || got.Preview != "" {
		t.Fatalf("record = %+v", got)
	}
}

func TestAStrictApprovalSaysWhyAndNeedsTheLongCode(t *testing.T) {
	terminal(t, true)
	e := newApprovalsEnv(t)
	record := e.request("run_1", 0, func(r *harnessapproval.Request) {
		r.Friction, r.Reasons, r.Paths, r.Summary = harnessapproval.FrictionStrict, []string{"dependency_manifest", "delete"}, []string{"go.mod"}, "delete go.mod"
	})
	code := harnessapproval.Code(record)
	if len(code) != 9 {
		t.Fatalf("code = %q", code)
	}
	out, err := e.run(code[:4]+"\n", "approve", record.ID)
	if err == nil {
		t.Fatalf("the short code approved a strict change: %s", out)
	}
	for _, want := range []string{"CARE", "dependency_manifest, delete", "Read it again", code} {
		if !strings.Contains(out, want) {
			t.Errorf("the strict review lacks %q:\n%s", want, out)
		}
	}
	if _, err := e.run(code+"\n", "approve", record.ID); err != nil {
		t.Fatalf("the full code = %v", err)
	}
}

func TestDenyNeedsNoTerminalAndStopEndsTheRun(t *testing.T) {
	terminal(t, false)
	e := newApprovalsEnv(t)
	first, second := e.request("run_1", 0), e.request("run_2", 1)
	if out, err := e.run("", "deny", first.ID); err != nil || !strings.Contains(out, `"state":"denied"`) || !strings.Contains(out, `"stop":false`) {
		t.Fatalf("deny = %v %s", err, out)
	}
	if out, err := e.run("", "deny", second.ID, "--stop"); err != nil || !strings.Contains(out, `"stop":true`) {
		t.Fatalf("deny --stop = %v %s", err, out)
	}
	if got := e.state(first.ID); got.State != harnessapproval.StateDenied || got.Stop || got.Preview != "" {
		t.Fatalf("first = %+v", got)
	}
	if got := e.state(second.ID); !got.Stop {
		t.Fatalf("second = %+v", got)
	}
	if _, err := e.run("", "deny", first.ID); err == nil {
		t.Error("an approval was denied twice")
	}
	if _, err := e.run("", "deny", "apr_"+strings.Repeat("0", 32)); err == nil || !strings.Contains(err.Error(), "no such approval") {
		t.Errorf("an unknown id = %v", err)
	}
}

func TestListShowsSummariesNeverTheChangeOrTheArguments(t *testing.T) {
	terminal(t, false)
	e := newApprovalsEnv(t)
	waiting := e.request("run_1", 0)
	other := e.request("run_2", 1, func(r *harnessapproval.Request) { r.Owner.Workspace = "elsewhere" })
	if _, err := e.run("", "deny", other.ID); err != nil {
		t.Fatal(err)
	}
	out, err := e.run("", "list")
	if err != nil || !strings.Contains(out, waiting.ID) || strings.Contains(out, other.ID) {
		t.Fatalf("list = %v %s", err, out)
	}
	for _, leak := range []string{"+new line", "old line", "arguments", `"preview"`, waiting.Digest, "digest"} {
		if strings.Contains(out, leak) {
			t.Errorf("the listing carries %q: %s", leak, out)
		}
	}
	all, _ := e.run("", "list", "--all")
	if !strings.Contains(all, waiting.ID) || !strings.Contains(all, other.ID) {
		t.Fatalf("list --all = %s", all)
	}
	if scoped, _ := e.run("", "list", "--all", "--workspace", "elsewhere"); !strings.Contains(scoped, other.ID) || strings.Contains(scoped, waiting.ID) {
		t.Fatalf("a scoped list = %s", scoped)
	}
	shown, err := e.run("", "show", waiting.ID)
	if err != nil || !strings.Contains(shown, "+new line") || strings.Contains(shown, harnessapproval.Code(waiting)) {
		t.Fatalf("show must print the change and must not print the code: %v\n%s", err, shown)
	}
}

func TestSafeTextStripsAnythingThatCouldRewriteATerminal(t *testing.T) {
	for in, want := range map[string]string{
		"plain":                      "plain",
		"a\x1b[31mred\x1b[0m":        "a[31mred[0m",
		"bell\x07cursor\x1b[2J\x9b0": "bellcursor[2J0",
		"keeps\tnewline\nandtab":     "keeps\tnewline\nandtab",
		"nul\x00byte":                "nulbyte",
	} {
		if got := safeText(in); got != want {
			t.Errorf("safeText(%q) = %q, want %q", in, got, want)
		}
	}
	if got := safePaths([]string{"a\nb", "c\x1bd"}); got[0] != "a b" || got[1] != "cd" {
		t.Errorf("safePaths = %q", got)
	}
}

func TestAuditVerificationReportsTheChain(t *testing.T) {
	terminal(t, false)
	e := newApprovalsEnv(t)
	record := e.request("run_1", 0)
	if _, err := e.run("", "deny", record.ID); err != nil {
		t.Fatal(err)
	}
	out, err := e.run("", "audit")
	if err != nil || !strings.Contains(out, `"verified":true`) || !strings.Contains(out, `"lines":2`) {
		t.Fatalf("audit = %v %s", err, out)
	}
	path := filepath.Join(e.dataDir, "approvals", "audit.jsonl")
	data, _ := os.ReadFile(path)
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), `"event":"requested"`, `"event":"approved"`, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := e.run("", "audit"); err == nil {
		t.Fatal("a tampered log verified")
	}
}

// appliedEdit performs a real edit through the real provider under a proof, then records the
// approval that authorized it, the way a finished run leaves things.
func (e *approvalsEnv) appliedEdit(content, oldText, newText string) (harnessapproval.Record, string) {
	e.t.Helper()
	if err := os.WriteFile(filepath.Join(e.root, "notes.txt"), []byte(content), 0o644); err != nil {
		e.t.Fatal(err)
	}
	provider, err := harnesstools.NewProvider(harnesstools.Config{Root: func(string) (string, error) { return e.root, nil },
		Edit: harnesstools.EditConfig{Enabled: true, PreimageDir: harnessSavedDir(e.dataDir)}})
	if err != nil {
		e.t.Fatal(err)
	}
	registry := harness.NewRegistry()
	if err := provider.Register(registry); err != nil {
		e.t.Fatal(err)
	}
	session, err := registry.OpenSession(context.Background(), harnesstools.ProviderID, harness.Scope{Workspace: "ws", Run: "run-undo", Generation: 1})
	if err != nil {
		e.t.Fatal(err)
	}
	defer session.Close()
	envelope, _ := session.Envelope(harnesstools.ToolEditFile, 4096)
	args, _ := json.Marshal(map[string]any{"path": "notes.txt", "edits": []map[string]string{{"old_text": oldText, "new_text": newText}}})
	action, err := session.Prepare(context.Background(), harness.ToolRequest{Envelope: envelope, ToolID: harnesstools.ToolEditFile, Arguments: args})
	if err != nil || action.Outcome != harness.OutcomeOK {
		e.t.Fatalf("prepare: %+v %v", action, err)
	}
	record := e.request("run_undo", 5, func(r *harnessapproval.Request) { r.Digest, r.Arguments = action.Digest, args })
	if _, err := e.store.Decide(context.Background(), record.ID, harnessapproval.Decision{Approve: true, Code: harnessapproval.Code(record), Via: "terminal"}); err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.store.Consume(context.Background(), record.ID, "run_undo", action.Digest); err != nil {
		e.t.Fatal(err)
	}
	answer, err := session.Invoke(harnessproof.Mint(context.Background(), action.Digest), action)
	if err != nil || answer.Outcome != harness.OutcomeOK {
		e.t.Fatalf("invoke: %+v %v", answer, err)
	}
	if err := e.store.Finish(context.Background(), record.ID, "applied"); err != nil {
		e.t.Fatal(err)
	}
	return e.state(record.ID), action.Digest
}

func (e *approvalsEnv) notes() string {
	data, err := os.ReadFile(filepath.Join(e.root, "notes.txt"))
	if err != nil {
		e.t.Fatal(err)
	}
	return string(data)
}

func TestUndoRestoresAnAppliedChangeOnlyIfTheFileIsStillAsItWasLeft(t *testing.T) {
	terminal(t, true)
	e := newApprovalsEnv(t)
	record, _ := e.appliedEdit("old line\n", "old", "new")
	if e.notes() != "new line\n" {
		t.Fatalf("the edit did not apply: %q", e.notes())
	}
	// Anything but the typed word does nothing; a changed file is left alone.
	if _, err := e.run("yes\n", "undo", record.ID); err == nil || e.notes() != "new line\n" {
		t.Fatalf("a wrong confirmation = %v; file %q", err, e.notes())
	}
	if err := os.WriteFile(filepath.Join(e.root, "notes.txt"), []byte("new line\nand later work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := e.run("undo\n", "undo", record.ID); err == nil || !strings.Contains(err.Error(), "changed after that action") || e.notes() != "new line\nand later work\n" {
		t.Fatalf("undo over later work = %v; file %q", err, e.notes())
	}
	if err := os.WriteFile(filepath.Join(e.root, "notes.txt"), []byte("new line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := e.run("undo\n", "undo", record.ID)
	if err != nil || e.notes() != "old line\n" || !strings.Contains(out, `"path":"notes.txt"`) {
		t.Fatalf("undo = %v %s; file %q", err, out, e.notes())
	}
	if _, err := e.run("undo\n", "undo", record.ID); err == nil || !strings.Contains(err.Error(), "already undone") {
		t.Fatalf("a second undo = %v", err)
	}
	if n, err := e.store.VerifyAudit(context.Background()); err != nil || n != 5 { // requested, approved, consumed, finished, undone
		t.Fatalf("audit = %d %v", n, err)
	}
	// Only an applied change can be undone.
	waiting := e.request("run_other", 7)
	if _, err := e.run("undo\n", "undo", waiting.ID); err == nil || !strings.Contains(err.Error(), "only a change that was applied") {
		t.Fatalf("undo of an approval that never ran = %v", err)
	}
}

// The whole path: a scripted model asks for an edit, the run parks, a person reviews and
// approves it with the real command, the file changes, the model sees the result, and the
// person undoes it.
func TestEditReviewApproveAndUndoThroughTheRealCommandAndRuntime(t *testing.T) {
	terminal(t, true)
	e := newApprovalsEnv(t)
	if err := os.WriteFile(filepath.Join(e.root, "notes.txt"), []byte("alpha\nbeta\ngamma\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	provider, err := harnesstools.NewProvider(harnesstools.Config{Root: func(string) (string, error) { return e.root, nil },
		Edit: harnesstools.EditConfig{Enabled: true, PreimageDir: harnessSavedDir(e.dataDir)}})
	if err != nil {
		t.Fatal(err)
	}
	registry := harness.NewRegistry()
	if _, err := harnesstest.Register(registry, harnesstest.Manifest("fake-model", harness.KindModel, "generation"), harnesstest.Behavior{Script: []harnesstest.Step{
		{ToolID: harnesstools.ToolEditFile, Arguments: `{"path":"notes.txt","edits":[{"old_text":"beta","new_text":"BETA"}]}`},
		{ToolID: harnesstools.ToolReadFile, Arguments: `{"path":"notes.txt"}`},
		{Text: "edited"},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := provider.Register(registry); err != nil {
		t.Fatal(err)
	}
	manager, err := harnessrun.NewManager(harnessrun.Config{DataDir: e.dataDir, Registry: registry,
		Model: harnessrun.Binding{Provider: "fake-model", Capability: "generation"}, Tool: &harnessrun.Binding{Provider: harnesstools.ProviderID, Capability: harnesstools.ToolReadFile},
		Policy: harnesstools.EditPolicy(), Approvals: e.store, ApprovalPoll: 15 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	owner := harnessrun.Owner{ClientID: "claude-desktop", Workspace: "ws", GrantID: strings.Repeat("b", 32), GrantRevision: 1}
	started, err := manager.Start(context.Background(), owner, harnessrun.StartRequest{IdempotencyKey: "key-00000001", Goal: "shout beta"})
	if err != nil {
		t.Fatal(err)
	}
	var approvalID string
	for deadline := time.Now().Add(8 * time.Second); approvalID == "" && time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if status, err := manager.Status(context.Background(), owner, started.ID); err == nil && status.Attention != nil {
			approvalID = status.Attention.ApprovalID
		}
	}
	if approvalID == "" {
		t.Fatal("the run never asked")
	}

	listed, _ := e.run("", "list")
	if !strings.Contains(listed, approvalID) || !strings.Contains(listed, `"friction":"standard"`) {
		t.Fatalf("list = %s", listed)
	}
	record := e.state(approvalID)
	out, err := e.run(harnessapproval.Code(record)+"\n", "approve", approvalID)
	if err != nil || !strings.Contains(out, "-beta") || !strings.Contains(out, "+BETA") {
		t.Fatalf("approve = %v\n%s", err, out)
	}
	for deadline := time.Now().Add(8 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if status, _ := manager.Status(context.Background(), owner, started.ID); status.State.Terminal() {
			if status.State != harnessrun.StateCompleted {
				t.Fatalf("the run ended as %+v", status)
			}
			break
		}
	}
	if e.notes() != "alpha\nBETA\ngamma\n" {
		t.Fatalf("the file = %q", e.notes())
	}
	if got := e.state(approvalID); got.State != harnessapproval.StateConsumed || got.Outcome != "applied" {
		t.Fatalf("approval = %+v", got)
	}
	if out, err := e.run("undo\n", "undo", approvalID); err != nil || e.notes() != "alpha\nbeta\ngamma\n" {
		t.Fatalf("undo = %v %s; file %q", err, out, e.notes())
	}
	if audit, err := e.run("", "audit"); err != nil || !strings.Contains(audit, `"verified":true`) {
		t.Fatalf("audit = %v %s", err, audit)
	}
}

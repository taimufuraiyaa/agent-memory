package harnesstools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessfs"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessproof"
)

// editEnv is a project with the mutating tools on and a separate directory for saved copies.
type editEnv struct {
	*env
	saved string
}

func newEditEnv(t *testing.T) *editEnv {
	t.Helper()
	e := &editEnv{env: newEnv(t), saved: filepath.Join(t.TempDir(), "saved")}
	e.open(Config{Redact: redact, Edit: EditConfig{Enabled: true, PreimageDir: e.saved}})
	return e
}

type mutation struct {
	prepared harness.Outcome
	reason   string
	action   harness.PreparedAction
	outcome  harness.Outcome
	output   string
	err      error
}

func (e *editEnv) prepare(tool string, args any) harness.PreparedAction {
	e.t.Helper()
	var raw []byte
	switch typed := args.(type) {
	case string:
		raw = []byte(typed)
	default:
		raw, _ = json.Marshal(args)
	}
	envelope, err := e.session.Envelope(harness.CapabilityID(tool), 4096)
	if err != nil {
		e.t.Fatalf("envelope: %v", err)
	}
	action, err := e.session.Prepare(context.Background(), harness.ToolRequest{Envelope: envelope, ToolID: tool, Arguments: raw})
	if err != nil {
		e.t.Fatalf("prepare: %v", err)
	}
	return action
}

// run prepares a call and, when approved is true, invokes it under the manager's proof.
func (e *editEnv) run(tool string, args any, approved bool) mutation {
	e.t.Helper()
	action := e.prepare(tool, args)
	m := mutation{prepared: action.Outcome, reason: action.Reason, action: action}
	if action.Outcome != harness.OutcomeOK {
		return m
	}
	ctx := context.Background()
	if approved {
		ctx = harnessproof.Mint(ctx, action.Digest)
	}
	answer, err := e.session.Invoke(ctx, action)
	m.outcome, m.output, m.err = answer.Outcome, string(answer.Output), err
	return m
}

func (e *editEnv) file(name string) string {
	e.t.Helper()
	data, err := os.ReadFile(filepath.Join(e.root, name))
	if err != nil {
		e.t.Fatal(err)
	}
	return string(data)
}

func (e *editEnv) exists(name string) bool {
	_, err := os.Lstat(filepath.Join(e.root, name))
	return err == nil
}

func edits(pairs ...string) []map[string]string {
	out := make([]map[string]string, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, map[string]string{"old_text": pairs[i], "new_text": pairs[i+1]})
	}
	return out
}

func TestEditingIsOffUnlessEnabledAndNeedsASavedCopyDirectory(t *testing.T) {
	e := newEnv(t) // the default: read tools only
	for _, tool := range []string{ToolEditFile, ToolCreateFile, ToolDeleteFile} {
		if _, err := e.session.Envelope(harness.CapabilityID(tool), 64); err == nil {
			t.Errorf("%s was offered with editing off", tool)
		}
		if e.session.State(harness.CapabilityID(tool)) == harness.AccessAvailable {
			t.Errorf("%s reports available with editing off", tool)
		}
	}
	if got := e.provider.Manifest().Capabilities; len(got) != 3 {
		t.Fatalf("read-only manifest = %v", got)
	}
	if _, err := NewProvider(Config{Root: func(string) (string, error) { return "", nil }, Edit: EditConfig{Enabled: true}}); err == nil {
		t.Fatal("editing was enabled without a directory for saved copies")
	}

	on := newEditEnv(t)
	for _, tool := range []string{ToolEditFile, ToolCreateFile, ToolDeleteFile, ToolReadFile, ToolListDir, ToolSearch} {
		if on.session.State(harness.CapabilityID(tool)) != harness.AccessAvailable {
			t.Errorf("%s = %s", tool, on.session.State(harness.CapabilityID(tool)))
		}
	}
	for _, tool := range []string{ToolEditFile, ToolCreateFile, ToolDeleteFile} {
		if !Mutating(harness.CapabilityID(tool)) {
			t.Errorf("%s is not classified as mutating", tool)
		}
	}
	for _, tool := range []string{ToolReadFile, ToolListDir, ToolSearch, "clarify", ""} {
		if Mutating(harness.CapabilityID(tool)) {
			t.Errorf("%s is classified as mutating", tool)
		}
	}
	// An unusable root leaves even the mutating tools unavailable.
	on.current = func() string { return filepath.Join(on.root, "nope") }
	session, err := on.registry.OpenSession(context.Background(), ProviderID, harness.Scope{Workspace: workspace, Run: "run-gone", Generation: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if session.State(ToolEditFile) != harness.AccessUnavailable {
		t.Fatalf("edit with a missing root = %s", session.State(ToolEditFile))
	}
}

func TestAnEditIsPreparedWithoutTouchingAnythingAndShownExactly(t *testing.T) {
	e := newEditEnv(t)
	before := e.file("internal/app/app.go")
	action := e.prepare(ToolEditFile, map[string]any{"path": "internal/app/app.go", "edits": edits("func Run() {}", "func Run() error { return nil }")})
	if action.Outcome != harness.OutcomeOK || action.Digest == "" || len(action.Escalate) != 0 || !reflect.DeepEqual(action.Paths, []string{"internal/app/app.go"}) || action.Summary != "edit internal/app/app.go (1 edit)" {
		t.Fatalf("action = %+v", action)
	}
	want := "--- a/internal/app/app.go\n+++ b/internal/app/app.go\n@@ -1,5 +1,5 @@\n package app\n \n // TODO: fix the needle handling\n-func Run() {}\n+func Run() error { return nil }\n func needle() {}\n"
	if action.Preview != want {
		t.Fatalf("preview:\n%s\nwant:\n%s", action.Preview, want)
	}
	if e.file("internal/app/app.go") != before {
		t.Fatal("preparing changed the file")
	}
	if entries, _ := os.ReadDir(e.saved); len(entries) != 0 {
		t.Fatal("preparing saved a copy")
	}
	// The same call prepares to the same digest, and a changed file changes it.
	e.session.Discard(action)
	again := e.prepare(ToolEditFile, map[string]any{"path": "internal/app/app.go", "edits": edits("func Run() {}", "func Run() error { return nil }")})
	if again.Digest != action.Digest {
		t.Fatal("the digest is not deterministic")
	}
	e.session.Discard(again)
	write(t, e.root, "internal/app/app.go", before+"// changed\n")
	changed := e.prepare(ToolEditFile, map[string]any{"path": "internal/app/app.go", "edits": edits("func Run() {}", "func Run() error { return nil }")})
	if changed.Digest == action.Digest {
		t.Fatal("the digest ignored the file's revision")
	}
	e.session.Discard(changed)
	other := e.prepare(ToolEditFile, map[string]any{"path": "internal/app/app.go", "edits": edits("func Run() {}", "func Run() int { return 1 }")})
	if other.Digest == action.Digest || other.Digest == changed.Digest {
		t.Fatal("the digest ignored the edit")
	}
}

func TestAMutatingCallNeedsTheManagersProofForExactlyThatAction(t *testing.T) {
	e := newEditEnv(t)
	args := map[string]any{"path": "main.go", "edits": edits("hello", "goodbye")}
	original := e.file("main.go")

	// No proof at all, and a proof for some other action, both change nothing.
	if m := e.run(ToolEditFile, args, false); m.outcome != harness.OutcomeDenied || e.file("main.go") != original {
		t.Fatalf("without a proof: %+v", m)
	}
	action := e.prepare(ToolEditFile, args)
	if answer, err := e.session.Invoke(harnessproof.Mint(context.Background(), "sha256:"+strings.Repeat("0", 64)), action); err != nil || answer.Outcome != harness.OutcomeDenied || e.file("main.go") != original {
		t.Fatalf("with another action's proof: %v %v", answer, err)
	}
	if entries, _ := os.ReadDir(e.saved); len(entries) != 0 {
		t.Fatal("a denied call saved a copy")
	}

	// The handle was consumed by the denied attempt, so prepare again, now approved.
	m := e.run(ToolEditFile, args, true)
	if m.outcome != harness.OutcomeOK || !strings.Contains(m.output, "edited main.go") || !strings.Contains(m.output, "revision") {
		t.Fatalf("approved: %+v", m)
	}
	if e.file("main.go") != strings.Replace(original, "hello", "goodbye", 1) {
		t.Fatalf("content = %q", e.file("main.go"))
	}
	// A handle is single use even after success.
	if _, err := e.session.Invoke(harnessproof.Mint(context.Background(), m.action.Digest), m.action); !errors.Is(err, harness.ErrStale) {
		t.Fatalf("a replayed handle = %v", err)
	}
}

func TestAppliedEditsKeepPermissionsSaveACopyAndLeaveNothingStray(t *testing.T) {
	e := newEditEnv(t)
	write(t, e.root, "tools/run.sh", "#!/bin/sh\necho one\n")
	if err := os.Chmod(filepath.Join(e.root, "tools/run.sh"), 0o750); err != nil {
		t.Fatal(err)
	}
	m := e.run(ToolEditFile, map[string]any{"path": "tools/run.sh", "edits": edits("echo one", "echo two")}, true)
	if m.outcome != harness.OutcomeOK {
		t.Fatalf("%+v", m)
	}
	info, _ := os.Stat(filepath.Join(e.root, "tools/run.sh"))
	if info.Mode().Perm() != 0o750 {
		t.Fatalf("mode = %v", info.Mode().Perm())
	}
	if names := stray(t, e.root); len(names) != 0 {
		t.Fatalf("stray files: %v", names)
	}
	dirInfo, _ := os.Stat(e.saved)
	entries, _ := os.ReadDir(e.saved)
	if dirInfo.Mode().Perm() != 0o700 || len(entries) != 1 || !strings.HasSuffix(entries[0].Name(), ".json") {
		t.Fatalf("saved copies: %v (dir %v)", entries, dirInfo.Mode().Perm())
	}
	if fi, _ := entries[0].Info(); fi.Mode().Perm() != 0o600 {
		t.Fatalf("saved copy mode = %v", fi.Mode().Perm())
	}
	pre, err := readPreimage(e.saved, m.action.Digest)
	if err != nil || string(pre.Data) != "#!/bin/sh\necho one\n" || pre.Path != "tools/run.sh" || pre.PreRevision == pre.PostRevision || pre.Root != e.root || pre.Mode != 0o750 {
		t.Fatalf("saved copy = %+v %v", pre, err)
	}
}

// stray lists leftover temporary files anywhere under the project.
func stray(t *testing.T, root string) []string {
	t.Helper()
	var names []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err == nil && strings.Contains(d.Name(), ".am-tmp-") {
			names = append(names, path)
		}
		return nil
	})
	return names
}

func TestEditValidationNamesTheProblemWithoutLeakingContent(t *testing.T) {
	e := newEditEnv(t)
	write(t, e.root, "twice.txt", "needle one\nneedle two\n")
	write(t, e.root, "aaa.txt", "aaa\n")
	write(t, e.root, "latin1.txt", "caf\xe9\n")
	write(t, e.root, "huge.txt", strings.Repeat("x\n", harnessfs.MaxWriteBytes))
	write(t, e.root, "plain.txt", "alpha beta gamma\n")
	if err := os.Mkdir(filepath.Join(e.root, "adir"), 0o755); err != nil {
		t.Fatal(err)
	}
	over := make([]map[string]string, MaxEdits+1)
	for i := range over {
		over[i] = map[string]string{"old_text": "a", "new_text": "b"}
	}
	for name, tc := range map[string]struct {
		args   any
		want   harness.Outcome
		reason string
	}{
		"no match":                 {map[string]any{"path": "plain.txt", "edits": edits("delta", "x")}, harness.OutcomeFailed, "no_match"},
		"ambiguous":                {map[string]any{"path": "twice.txt", "edits": edits("needle", "x")}, harness.OutcomeFailed, "ambiguous_match"},
		"overlapping matches":      {map[string]any{"path": "aaa.txt", "edits": edits("aa", "x")}, harness.OutcomeFailed, "ambiguous_match"},
		"overlapping edits":        {map[string]any{"path": "plain.txt", "edits": edits("alpha beta", "x", "beta gamma", "y")}, harness.OutcomeFailed, "overlapping_edits"},
		"the same edit twice":      {map[string]any{"path": "plain.txt", "edits": edits("alpha", "x", "alpha", "y")}, harness.OutcomeFailed, "overlapping_edits"},
		"no change":                {map[string]any{"path": "plain.txt", "edits": edits("alpha", "alpha")}, harness.OutcomeFailed, "no_change"},
		"empty old text":           {map[string]any{"path": "plain.txt", "edits": edits("", "x")}, harness.OutcomeFailed, "invalid_arguments"},
		"no edits":                 {map[string]any{"path": "plain.txt", "edits": []any{}}, harness.OutcomeFailed, "invalid_arguments"},
		"too many edits":           {map[string]any{"path": "plain.txt", "edits": over}, harness.OutcomeFailed, "invalid_arguments"},
		"NUL in new text":          {map[string]any{"path": "plain.txt", "edits": edits("alpha", "x\x00y")}, harness.OutcomeFailed, "invalid_arguments"},
		"huge new text":            {map[string]any{"path": "plain.txt", "edits": edits("alpha", strings.Repeat("y", MaxEditTextBytes+1))}, harness.OutcomeFailed, "invalid_arguments"},
		"unknown field":            {map[string]any{"path": "plain.txt", "edits": edits("alpha", "x"), "force": true}, harness.OutcomeFailed, "invalid_arguments"},
		"unknown edit field":       {`{"path":"plain.txt","edits":[{"old_text":"alpha","new_text":"x","all":true}]}`, harness.OutcomeFailed, "invalid_arguments"},
		"malformed":                {`{"path":`, harness.OutcomeFailed, "invalid_arguments"},
		"trailing data":            {`{"path":"plain.txt","edits":[{"old_text":"alpha","new_text":"x"}]}{}`, harness.OutcomeFailed, "invalid_arguments"},
		"a missing file":           {map[string]any{"path": "nope.txt", "edits": edits("a", "b")}, harness.OutcomeFailed, "unreadable"},
		"a directory":              {map[string]any{"path": "adir", "edits": edits("a", "b")}, harness.OutcomeFailed, "unreadable"},
		"a binary file":            {map[string]any{"path": "binary.bin", "edits": edits("needle", "x")}, harness.OutcomeFailed, "not_text"},
		"a file that is not UTF-8": {map[string]any{"path": "latin1.txt", "edits": edits("caf", "x")}, harness.OutcomeFailed, "not_text"},
		"a file too large":         {map[string]any{"path": "huge.txt", "edits": edits("x", "y")}, harness.OutcomeFailed, "too_large"},
		"the root":                 {map[string]any{"path": ".", "edits": edits("a", "b")}, harness.OutcomeFailed, ""},
		"no path":                  {map[string]any{"edits": edits("a", "b")}, harness.OutcomeFailed, ""},
		"traversal":                {map[string]any{"path": "../x.go", "edits": edits("a", "b")}, harness.OutcomeDenied, ""},
		"absolute":                 {map[string]any{"path": "/etc/passwd", "edits": edits("a", "b")}, harness.OutcomeDenied, ""},
		"hidden":                   {map[string]any{"path": ".env", "edits": edits("DATABASE", "x")}, harness.OutcomeDenied, ""},
		"hidden directory":         {map[string]any{"path": ".hidden/h.go", "edits": edits("needle", "x")}, harness.OutcomeDenied, ""},
		"a credential name":        {map[string]any{"path": "id_rsa", "edits": edits("needle", "x")}, harness.OutcomeDenied, ""},
		"a pem file":               {map[string]any{"path": "server.pem", "edits": edits("needle", "x")}, harness.OutcomeDenied, ""},
		"a link out":               {map[string]any{"path": "link-out.txt", "edits": edits("needle", "x")}, harness.OutcomeDenied, ""},
		"through a directory link": {map[string]any{"path": "dir-out/stolen.txt", "edits": edits("needle", "x")}, harness.OutcomeDenied, ""},
	} {
		m := e.run(ToolEditFile, tc.args, true)
		if m.prepared != tc.want || (tc.reason != "" && m.reason != tc.reason) {
			t.Errorf("%s: prepared %s reason %q, want %s %q", name, m.prepared, m.reason, tc.want, tc.reason)
		}
		if m.outcome != "" {
			t.Errorf("%s: an invalid call was invoked: %+v", name, m)
		}
	}
	for file, content := range map[string]string{"plain.txt": "alpha beta gamma\n", "twice.txt": "needle one\nneedle two\n", "aaa.txt": "aaa\n"} {
		if e.file(file) != content {
			t.Errorf("%s changed", file)
		}
	}
	if data, _ := os.ReadFile(filepath.Join(e.outside, "stolen.txt")); string(data) != "needle OUTSIDE-SECRET\n" {
		t.Fatal("a file outside the project changed")
	}
	// A failed preparation tells the model the reason code and nothing from the file.
	m := e.run(ToolEditFile, map[string]any{"path": "plain.txt", "edits": edits("delta", "x")}, true)
	if strings.Contains(fmt.Sprint(m.action), "alpha") {
		t.Fatalf("a failure carried file content: %+v", m.action)
	}
}

func TestEditsInOneCallApplyTogetherAndRenderEachHunk(t *testing.T) {
	e := newEditEnv(t)
	var lines []string
	for i := 1; i <= 40; i++ {
		lines = append(lines, fmt.Sprintf("line %02d", i))
	}
	write(t, e.root, "long.txt", strings.Join(lines, "\n")+"\n")
	write(t, e.root, "same-line.txt", "let a = 1; let b = 2; let c = 3;\nnext\n")

	// Far apart: two hunks, with the second numbered after the first one's growth.
	m := e.run(ToolEditFile, map[string]any{"path": "long.txt", "edits": edits("line 05\n", "line 05\nadded one\nadded two\n", "line 30\n", "")}, false)
	if m.prepared != harness.OutcomeOK {
		t.Fatalf("%+v", m)
	}
	if strings.Count(m.action.Preview, "@@ -") != 2 || !strings.Contains(m.action.Preview, "@@ -3,6 +3,8 @@") || !strings.Contains(m.action.Preview, "@@ -27,7 +29,6 @@") {
		t.Fatalf("hunks are wrong:\n%s", m.action.Preview)
	}
	// Close together: one merged hunk.
	near := e.prepare(ToolEditFile, map[string]any{"path": "long.txt", "edits": edits("line 10\n", "ten\n", "line 14\n", "fourteen\n")})
	if strings.Count(near.Preview, "@@ -") != 1 {
		t.Fatalf("close edits were not merged:\n%s", near.Preview)
	}
	// Two edits that touch one line become one block, and everything lands.
	together := e.run(ToolEditFile, map[string]any{"path": "same-line.txt", "edits": edits("a = 1", "a = 10", "c = 3", "c = 30")}, true)
	if together.outcome != harness.OutcomeOK || e.file("same-line.txt") != "let a = 10; let b = 2; let c = 30;\nnext\n" {
		t.Fatalf("%+v %q", together, e.file("same-line.txt"))
	}
	if strings.Count(together.action.Preview, "\n-let") != 1 || strings.Count(together.action.Preview, "\n+let") != 1 {
		t.Fatalf("a single changed line was not shown as one change:\n%s", together.action.Preview)
	}
}

func TestAMissingFinalNewlineIsVisibleInThePreview(t *testing.T) {
	e := newEditEnv(t)
	write(t, e.root, "nonl.txt", "one\ntwo")
	m := e.prepare(ToolEditFile, map[string]any{"path": "nonl.txt", "edits": edits("two", "two\nthree\n")})
	if !strings.Contains(m.Preview, "-two\n\\ No newline at end of file\n+two\n+three\n") {
		t.Fatalf("preview:\n%s", m.Preview)
	}
}

func TestThePreviewHidesNothingFromTheReviewer(t *testing.T) {
	e := newEditEnv(t)
	write(t, e.root, "tricky.txt", "safe line\nvalue = 1\n")
	m := e.prepare(ToolEditFile, map[string]any{"path": "tricky.txt", "edits": edits("value = 1", "value = 1 \u202e// \u2066hidden\u2069\u200b\x1b[31m\x07\ufeff")})
	if m.Outcome != harness.OutcomeOK {
		t.Fatalf("%+v", m)
	}
	for _, raw := range []string{"\u202e", "\u2066", "\u2069", "\u200b", "\x1b", "\x07", "\ufeff"} {
		if strings.Contains(m.Preview, raw) {
			t.Errorf("the preview contains %q unmarked", raw)
		}
	}
	for _, marker := range []string{"⟨U+202E⟩", "⟨U+2066⟩", "⟨U+2069⟩", "⟨U+200B⟩", "⟨U+001B⟩", "⟨U+0007⟩", "⟨U+FEFF⟩"} {
		if !strings.Contains(m.Preview, marker) {
			t.Errorf("the preview does not show %s:\n%s", marker, m.Preview)
		}
	}
	// A path with a hidden character is shown with it marked too.
	e.run(ToolCreateFile, map[string]any{"path": "a\u202eb.txt", "content": "x"}, false)
	created := e.prepare(ToolCreateFile, map[string]any{"path": "weird\u200bname.txt", "content": "x\n"})
	if created.Outcome != harness.OutcomeOK || strings.Contains(created.Preview, "\u200b") || !strings.Contains(created.Preview, "⟨U+200B⟩") {
		t.Fatalf("%+v", created)
	}
}

func TestAChangeThatCannotBeReadInFullIsNotOffered(t *testing.T) {
	e := newEditEnv(t)
	var b strings.Builder
	for i := 0; i < 2500; i++ {
		fmt.Fprintf(&b, "line number %d of a long file\n", i)
	}
	write(t, e.root, "big.txt", b.String())
	oldText := strings.Repeat("line number 1 of a long file\n", 1)
	_ = oldText
	// Replace a large block with another large block: the diff exceeds the preview bound.
	first := strings.Join(strings.SplitAfter(b.String(), "\n")[100:1200], "")
	if len(first) > MaxEditTextBytes {
		first = first[:strings.LastIndex(first[:MaxEditTextBytes], "\n")+1]
	}
	m := e.run(ToolEditFile, map[string]any{"path": "big.txt", "edits": edits(first, strings.ReplaceAll(first, "line", "LINE"))}, true)
	if m.prepared != harness.OutcomeFailed || m.reason != "change_too_large" || m.outcome != "" {
		t.Fatalf("%+v", m)
	}
	if e.file("big.txt") != b.String() {
		t.Fatal("the file changed")
	}
}

func TestRiskyChangesCarryEscalationReasons(t *testing.T) {
	e := newEditEnv(t)
	var many strings.Builder
	for i := 0; i < 150; i++ {
		fmt.Fprintf(&many, "row %d\n", i)
	}
	write(t, e.root, "rows.txt", many.String())
	write(t, e.root, "tools/exec.go", "package tools\n")
	if err := os.Chmod(filepath.Join(e.root, "tools/exec.go"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, e.root, "script", "#!/bin/sh\nexit 0\n")
	write(t, e.root, "doomed.txt", "bye\n")

	m := e.prepare(ToolEditFile, map[string]any{"path": "rows.txt", "edits": edits("row", "ROW")})
	if m.Outcome != harness.OutcomeFailed && len(m.Escalate) != 0 {
		t.Fatalf("ordinary edit: %+v", m)
	}
	big := e.prepare(ToolEditFile, map[string]any{"path": "rows.txt", "edits": edits(many.String()[:strings.Index(many.String(), "row 100")], strings.ReplaceAll(many.String()[:strings.Index(many.String(), "row 100")], "row", "ROW"))})
	if !contains(big.Escalate, "large_change") {
		t.Fatalf("a 200-line change: %+v", big.Escalate)
	}
	for name, tc := range map[string]struct {
		tool string
		args any
		want string
	}{
		"executable":      {ToolEditFile, map[string]any{"path": "tools/exec.go", "edits": edits("tools", "toolz")}, "executable"},
		"shebang":         {ToolEditFile, map[string]any{"path": "script", "edits": edits("exit 0", "exit 1")}, "shebang"},
		"new shebang":     {ToolCreateFile, map[string]any{"path": "new-script", "content": "#!/bin/sh\n"}, "shebang"},
		"delete":          {ToolDeleteFile, map[string]any{"path": "doomed.txt"}, "delete"},
		"new directories": {ToolCreateFile, map[string]any{"path": "fresh/dir/file.txt", "content": "x\n"}, "new_directories"},
	} {
		got := e.prepare(tc.tool, tc.args)
		if got.Outcome != harness.OutcomeOK || !contains(got.Escalate, tc.want) {
			t.Errorf("%s: %+v", name, got)
		}
	}
	ordinary := e.prepare(ToolCreateFile, map[string]any{"path": "plain-new.txt", "content": "x\n"})
	if ordinary.Outcome != harness.OutcomeOK || len(ordinary.Escalate) != 0 {
		t.Fatalf("an ordinary create escalated: %+v", ordinary)
	}
}

func TestCreateFileMakesParentsRefusesToReplaceAndStaysInsideTheProject(t *testing.T) {
	e := newEditEnv(t)
	m := e.run(ToolCreateFile, map[string]any{"path": "pkg/new/thing.go", "content": "package thing\n"}, true)
	if m.outcome != harness.OutcomeOK || e.file("pkg/new/thing.go") != "package thing\n" || !strings.Contains(m.output, "created pkg/new/thing.go") {
		t.Fatalf("%+v", m)
	}
	if !strings.Contains(m.action.Preview, "(creates directories: pkg, pkg/new)") || !strings.Contains(m.action.Preview, "+package thing") {
		t.Fatalf("preview:\n%s", m.action.Preview)
	}
	pre, err := readPreimage(e.saved, m.action.Digest)
	if err != nil || pre.Existed || !reflect.DeepEqual(pre.CreatedDirs, []string{"pkg", "pkg/new"}) {
		t.Fatalf("saved copy = %+v %v", pre, err)
	}
	write(t, e.root, "plainfile", "x")
	if err := os.Symlink(".", filepath.Join(e.root, "rootlink")); err != nil {
		t.Skip("symlinks unavailable")
	}
	for name, tc := range map[string]struct {
		args   any
		want   harness.Outcome
		reason string
	}{
		"an existing file":      {map[string]any{"path": "main.go", "content": "x"}, harness.OutcomeFailed, "exists"},
		"an existing directory": {map[string]any{"path": "internal", "content": "x"}, harness.OutcomeFailed, "exists"},
		"under a file":          {map[string]any{"path": "plainfile/x.txt", "content": "x"}, harness.OutcomeFailed, "parent_is_a_file"},
		"through a link":        {map[string]any{"path": "rootlink/x.txt", "content": "x"}, harness.OutcomeDenied, ""},
		"a link out":            {map[string]any{"path": "dir-out/new.txt", "content": "x"}, harness.OutcomeDenied, ""},
		"traversal":             {map[string]any{"path": "../escape.txt", "content": "x"}, harness.OutcomeDenied, ""},
		"deep traversal":        {map[string]any{"path": "a/../../escape.txt", "content": "x"}, harness.OutcomeDenied, ""},
		"absolute":              {map[string]any{"path": "/tmp/escape.txt", "content": "x"}, harness.OutcomeDenied, ""},
		"hidden":                {map[string]any{"path": ".env.local", "content": "x"}, harness.OutcomeDenied, ""},
		"hidden directory":      {map[string]any{"path": ".github/workflows/ci.yml", "content": "x"}, harness.OutcomeDenied, ""},
		"git hooks":             {map[string]any{"path": ".git/hooks/pre-commit", "content": "x"}, harness.OutcomeDenied, ""},
		"a key file":            {map[string]any{"path": "deploy/server.key", "content": "x"}, harness.OutcomeDenied, ""},
		"credentials":           {map[string]any{"path": "credentials.json", "content": "{}"}, harness.OutcomeDenied, ""},
		"NUL in the content":    {map[string]any{"path": "n.txt", "content": "a\x00b"}, harness.OutcomeFailed, "invalid_arguments"},
		"huge content":          {map[string]any{"path": "huge-new.txt", "content": strings.Repeat("a", MaxCreateBytes+1)}, harness.OutcomeFailed, "invalid_arguments"},
		"the root":              {map[string]any{"path": ".", "content": "x"}, harness.OutcomeFailed, ""},
		"no content field":      {map[string]any{"path": "n.txt", "data": "x"}, harness.OutcomeFailed, "invalid_arguments"},
	} {
		got := e.run(ToolCreateFile, tc.args, true)
		if got.prepared != tc.want || (tc.reason != "" && got.reason != tc.reason) || got.outcome != "" {
			t.Errorf("%s: %+v", name, got)
		}
	}
	for _, name := range []string{"escape.txt", "n.txt", "huge-new.txt", ".env.local", ".github", ".git/hooks/pre-commit", "deploy", "credentials.json"} {
		if e.exists(name) {
			t.Errorf("%s was created", name)
		}
	}
	if _, err := os.Stat(filepath.Join(e.outside, "new.txt")); err == nil {
		t.Fatal("a file was created outside the project")
	}
}

func TestDeleteFileRemovesOneRegularTextFileAfterApproval(t *testing.T) {
	e := newEditEnv(t)
	write(t, e.root, "old/notes.txt", "keep a copy\n")
	if m := e.run(ToolDeleteFile, map[string]any{"path": "old/notes.txt"}, false); m.outcome != harness.OutcomeDenied || !e.exists("old/notes.txt") {
		t.Fatalf("unapproved delete: %+v", m)
	}
	m := e.run(ToolDeleteFile, map[string]any{"path": "old/notes.txt"}, true)
	if m.outcome != harness.OutcomeOK || e.exists("old/notes.txt") || !e.exists("old") || !strings.Contains(m.output, "deleted old/notes.txt") {
		t.Fatalf("%+v", m)
	}
	if !strings.Contains(m.action.Preview, "(deletes the whole file: 1 line, 12 bytes)") || !strings.Contains(m.action.Preview, "-keep a copy") {
		t.Fatalf("preview:\n%s", m.action.Preview)
	}
	for name, tc := range map[string]struct {
		path   string
		want   harness.Outcome
		reason string
	}{
		"missing":   {"nope.txt", harness.OutcomeFailed, "unreadable"},
		"directory": {"internal", harness.OutcomeFailed, "unreadable"},
		"binary":    {"binary.bin", harness.OutcomeFailed, "not_text"},
		"huge":      {"big.txt", harness.OutcomeFailed, "too_large"},
		"hidden":    {".env", harness.OutcomeDenied, ""},
		"key":       {"id_rsa", harness.OutcomeDenied, ""},
		"link out":  {"link-out.txt", harness.OutcomeDenied, ""},
		"traversal": {"../x", harness.OutcomeDenied, ""},
	} {
		got := e.run(ToolDeleteFile, map[string]any{"path": tc.path}, true)
		if got.prepared != tc.want || (tc.reason != "" && got.reason != tc.reason) || got.outcome != "" {
			t.Errorf("%s: %+v", name, got)
		}
	}
	for _, name := range []string{"binary.bin", "big.txt", ".env", "id_rsa", "internal/app/app.go"} {
		if !e.exists(name) {
			t.Errorf("%s was deleted", name)
		}
	}
	if m := e.run(ToolDeleteFile, `{"path":"main.go","force":true}`, true); m.prepared != harness.OutcomeFailed {
		t.Fatalf("an unknown field = %+v", m)
	}
}

// A file that changed after the person reviewed it is left exactly as it is.
func TestAFileChangedAfterReviewIsNeverOverwritten(t *testing.T) {
	e := newEditEnv(t)
	for name, tc := range map[string]struct {
		tool  string
		file  string
		args  any
		setup func()
	}{
		"edit":   {ToolEditFile, "main.go", map[string]any{"path": "main.go", "edits": edits("hello", "bye")}, func() { write(t, e.root, "main.go", "package main\n// someone else was here\n") }},
		"delete": {ToolDeleteFile, "main.go", map[string]any{"path": "main.go"}, func() { write(t, e.root, "main.go", "package main\n// someone else was here\n") }},
		"create": {ToolCreateFile, "fresh.txt", map[string]any{"path": "fresh.txt", "content": "mine\n"}, func() { write(t, e.root, "fresh.txt", "theirs\n") }},
	} {
		write(t, e.root, "main.go", "package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n")
		os.Remove(filepath.Join(e.root, "fresh.txt"))
		action := e.prepare(tc.tool, tc.args)
		if action.Outcome != harness.OutcomeOK {
			t.Fatalf("%s: %+v", name, action)
		}
		tc.setup()
		changed := e.file(tc.file)
		answer, err := e.session.Invoke(harnessproof.Mint(context.Background(), action.Digest), action)
		if err != nil || answer.Outcome != harness.OutcomeStale || e.file(tc.file) != changed {
			t.Errorf("%s: %v %v; file = %q", name, answer.Outcome, err, e.file(tc.file))
		}
		if entries, _ := os.ReadDir(e.saved); len(entries) != 0 {
			t.Errorf("%s: a failed apply left a saved copy: %v", name, entries)
		}
	}
	if names := stray(t, e.root); len(names) != 0 {
		t.Fatalf("stray files: %v", names)
	}
}

func TestAProjectRootThatMovedAfterPreparingIsStale(t *testing.T) {
	e := newEditEnv(t)
	action := e.prepare(ToolEditFile, map[string]any{"path": "main.go", "edits": edits("hello", "bye")})
	other := t.TempDir()
	write(t, other, "main.go", "package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n")
	e.current = func() string { return other }
	answer, err := e.session.Invoke(harnessproof.Mint(context.Background(), action.Digest), action)
	if err != nil || answer.Outcome != harness.OutcomeStale {
		t.Fatalf("%v %v", answer.Outcome, err)
	}
	if data, _ := os.ReadFile(filepath.Join(other, "main.go")); !strings.Contains(string(data), "hello") {
		t.Fatal("the other project was changed")
	}
}

func TestAResultThatDoesNotVerifyIsRolledBack(t *testing.T) {
	for name, tc := range map[string]struct {
		tool  string
		args  any
		check func(e *editEnv)
		sabot func(project *harnessfs.Root, p *plan)
	}{
		"edit": {ToolEditFile, map[string]any{"path": "main.go", "edits": edits("hello", "bye")},
			func(e *editEnv) {
				if !strings.Contains(e.file("main.go"), "hello") {
					t.Errorf("edit: the original was not restored: %q", e.file("main.go"))
				}
			},
			func(project *harnessfs.Root, p *plan) {
				_, rev, _, _ := project.ReadWhole(p.path)
				_ = project.ReplaceFile(p.path, []byte("corrupted\n"), rev)
			}},
		"create": {ToolCreateFile, map[string]any{"path": "made/new.txt", "content": "mine\n"},
			func(e *editEnv) {
				if e.exists("made/new.txt") {
					t.Error("create: the file was left behind")
				}
			},
			func(project *harnessfs.Root, p *plan) {
				_, rev, _, _ := project.ReadWhole(p.path)
				_ = project.ReplaceFile(p.path, []byte("tampered\n"), rev)
			}},
		"delete": {ToolDeleteFile, map[string]any{"path": "main.go"},
			func(e *editEnv) {
				// Someone recreated the file during the gap: it is theirs, so it is not overwritten.
				if e.file("main.go") != "reappeared\n" {
					t.Errorf("delete: a file that appeared meanwhile was overwritten: %q", e.file("main.go"))
				}
			},
			func(project *harnessfs.Root, p *plan) {
				_, _ = project.CreateFile(p.path, []byte("reappeared\n"), 0o644)
			}},
	} {
		e := newEditEnv(t)
		applyHook = tc.sabot
		m := e.run(tc.tool, tc.args, true)
		applyHook = nil
		if m.outcome != harness.OutcomeFailed {
			t.Errorf("%s: outcome = %s", name, m.outcome)
		}
		tc.check(e)
	}
}

func TestOnlyOneOfSeveralSessionsAppliesTheSameApproval(t *testing.T) {
	e := newEditEnv(t)
	args := map[string]any{"path": "main.go", "edits": edits("hello", "bye")}
	var sessions []*harness.Session
	var actions []harness.PreparedAction
	for i := 0; i < 6; i++ {
		session, err := e.registry.OpenSession(context.Background(), ProviderID, harness.Scope{Workspace: workspace, Run: fmt.Sprintf("run-%d", i), Generation: 1})
		if err != nil {
			t.Fatal(err)
		}
		defer session.Close()
		envelope, _ := session.Envelope(ToolEditFile, 4096)
		raw, _ := json.Marshal(args)
		action, err := session.Prepare(context.Background(), harness.ToolRequest{Envelope: envelope, ToolID: ToolEditFile, Arguments: raw})
		if err != nil || action.Outcome != harness.OutcomeOK {
			t.Fatalf("prepare: %+v %v", action, err)
		}
		sessions, actions = append(sessions, session), append(actions, action)
	}
	var ok, stale atomic.Int32
	var wg sync.WaitGroup
	for i := range sessions {
		wg.Add(1)
		go func() {
			defer wg.Done()
			answer, err := sessions[i].Invoke(harnessproof.Mint(context.Background(), actions[i].Digest), actions[i])
			switch {
			case err != nil:
				t.Errorf("invoke: %v", err)
			case answer.Outcome == harness.OutcomeOK:
				ok.Add(1)
			case answer.Outcome == harness.OutcomeStale:
				stale.Add(1)
			default:
				t.Errorf("unexpected outcome %s", answer.Outcome)
			}
		}()
	}
	wg.Wait()
	if ok.Load() != 1 || stale.Load() != 5 {
		t.Fatalf("ok=%d stale=%d", ok.Load(), stale.Load())
	}
}

func TestOutputIsRedactedAndPendingMutationsAreBounded(t *testing.T) {
	e := newEditEnv(t)
	e.open(Config{Redact: func(s string) string { return strings.ReplaceAll(s, "main.go", "[REDACTED]") }, Edit: EditConfig{Enabled: true, PreimageDir: e.saved}})
	m := e.run(ToolEditFile, map[string]any{"path": "main.go", "edits": edits("hello", "bye")}, true)
	if m.outcome != harness.OutcomeOK || strings.Contains(m.output, "main.go") {
		t.Fatalf("output was not redacted: %+v", m)
	}
	var held []harness.PreparedAction
	for i := 0; i < maxPendingMutations; i++ {
		action := e.prepare(ToolCreateFile, map[string]any{"path": fmt.Sprintf("p%d.txt", i), "content": "x"})
		if action.Outcome != harness.OutcomeOK {
			t.Fatalf("call %d: %+v", i, action)
		}
		held = append(held, action)
	}
	if over := e.prepare(ToolCreateFile, map[string]any{"path": "one-too-many.txt", "content": "x"}); over.Outcome != harness.OutcomeFailed || over.Reason != "busy" {
		t.Fatalf("an unbounded number of pending mutations: %+v", over)
	}
	e.session.Discard(held[0])
	if again := e.prepare(ToolCreateFile, map[string]any{"path": "one-too-many.txt", "content": "x"}); again.Outcome != harness.OutcomeOK {
		t.Fatalf("after a discard: %+v", again)
	}
}

func TestOversizedArgumentsAreRefusedBeforeAnyWork(t *testing.T) {
	e := newEditEnv(t)
	raw := `{"path":"main.go","edits":[{"old_text":"hello","new_text":"` + strings.Repeat("a", MaxMutationArgBytes) + `"}]}`
	if m := e.prepare(ToolEditFile, raw); m.Outcome != harness.OutcomeFailed || m.Reason != "arguments_too_large" {
		t.Fatalf("%+v", m)
	}
}

// ---- undo ----

func TestUndoRestoresOnlyWhatTheActionLeftBehind(t *testing.T) {
	e := newEditEnv(t)
	original := e.file("main.go")
	m := e.run(ToolEditFile, map[string]any{"path": "main.go", "edits": edits("hello", "bye")}, true)
	if m.outcome != harness.OutcomeOK {
		t.Fatal(m)
	}
	// Someone else edits the file afterwards: undoing would destroy their work, so it refuses.
	write(t, e.root, "main.go", e.file("main.go")+"// later work\n")
	later := e.file("main.go")
	if _, err := Undo(e.saved, e.root, m.action.Digest, time.Now()); !errors.Is(err, ErrNotRestorable) || e.file("main.go") != later {
		t.Fatalf("undo over later work = %v; file %q", err, e.file("main.go"))
	}
	write(t, e.root, "main.go", strings.Replace(original, "hello", "bye", 1))
	res, err := Undo(e.saved, e.root, m.action.Digest, time.Now())
	if err != nil || res.Path != "main.go" || res.Tool != ToolEditFile || e.file("main.go") != original {
		t.Fatalf("undo = %+v %v; file %q", res, err, e.file("main.go"))
	}
	if _, err := Undo(e.saved, e.root, m.action.Digest, time.Now()); !errors.Is(err, ErrUndone) {
		t.Fatalf("a second undo = %v", err)
	}

	// Create is undone by removing the file; delete by recreating it with its mode.
	made := e.run(ToolCreateFile, map[string]any{"path": "x/new.txt", "content": "fresh\n"}, true)
	if _, err := Undo(e.saved, e.root, made.action.Digest, time.Now()); err != nil || e.exists("x/new.txt") || !e.exists("x") {
		t.Fatalf("undo create = %v", err)
	}
	write(t, e.root, "tools/keep.sh", "#!/bin/sh\necho keep\n")
	if err := os.Chmod(filepath.Join(e.root, "tools/keep.sh"), 0o750); err != nil {
		t.Fatal(err)
	}
	gone := e.run(ToolDeleteFile, map[string]any{"path": "tools/keep.sh"}, true)
	if gone.outcome != harness.OutcomeOK || e.exists("tools/keep.sh") {
		t.Fatalf("%+v", gone)
	}
	if _, err := Undo(e.saved, e.root, gone.action.Digest, time.Now()); err != nil || e.file("tools/keep.sh") != "#!/bin/sh\necho keep\n" {
		t.Fatalf("undo delete = %v", err)
	}
	if info, _ := os.Stat(filepath.Join(e.root, "tools/keep.sh")); info.Mode().Perm() != 0o750 {
		t.Fatalf("restored mode = %v", info.Mode().Perm())
	}
	// A deleted file that someone recreated is not overwritten.
	again := e.run(ToolDeleteFile, map[string]any{"path": "tools/keep.sh"}, true)
	write(t, e.root, "tools/keep.sh", "recreated\n")
	if _, err := Undo(e.saved, e.root, again.action.Digest, time.Now()); !errors.Is(err, ErrNotRestorable) || e.file("tools/keep.sh") != "recreated\n" {
		t.Fatalf("undo over a recreated file = %v", err)
	}
}

func TestUndoRefusesBadRequestsAndDamagedCopies(t *testing.T) {
	e := newEditEnv(t)
	m := e.run(ToolEditFile, map[string]any{"path": "main.go", "edits": edits("hello", "bye")}, true)
	digest := m.action.Digest
	for _, bad := range []string{"", "sha256:abc", "../x", digest + "x", "sha256:" + strings.Repeat("g", 64)} {
		if _, err := Undo(e.saved, e.root, bad, time.Now()); !errors.Is(err, ErrNoPreimage) {
			t.Errorf("Undo(%q) = %v", bad, err)
		}
	}
	if _, err := Undo(e.saved, e.root, "sha256:"+strings.Repeat("0", 64), time.Now()); !errors.Is(err, ErrNoPreimage) {
		t.Fatalf("an unknown action = %v", err)
	}
	// An action applied to another project root is never undone here.
	if _, err := Undo(e.saved, t.TempDir(), digest, time.Now()); !errors.Is(err, ErrNotRestorable) {
		t.Fatalf("another root = %v", err)
	}
	path, _ := preimagePath(e.saved, digest)
	good, _ := os.ReadFile(path)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	afterEdit := e.file("main.go")
	if _, err := Undo(e.saved, e.root, digest, time.Now()); err == nil || e.file("main.go") != afterEdit {
		t.Fatalf("a loose saved copy was trusted (error %v, file restored: %v)", err, e.file("main.go") != afterEdit)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"garbage":       "not json",
		"unknown field": strings.Replace(string(good), `"version":1`, `"version":1,"extra":true`, 1),
		"wrong version": strings.Replace(string(good), `"version":1`, `"version":9`, 1),
		"wrong digest":  strings.Replace(string(good), digest, "sha256:"+strings.Repeat("1", 64), 1),
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Undo(e.saved, e.root, digest, time.Now()); err == nil || errors.Is(err, ErrNotRestorable) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := os.WriteFile(path, good, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(e.outside, "stolen.txt"), path); err != nil {
		t.Skip("symlinks unavailable")
	}
	if _, err := Undo(e.saved, e.root, digest, time.Now()); err == nil {
		t.Fatal("a symlinked saved copy was trusted")
	}
}

func TestSavedCopiesAreStagedAndPruned(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "saved")
	digest := "sha256:" + strings.Repeat("a", 64)
	p := preimage{Version: preimageVersion, Digest: digest, Tool: ToolEditFile, Path: "x", AppliedAt: time.Now()}
	if err := stagePreimage(dir, p); err != nil {
		t.Fatal(err)
	}
	if _, err := readPreimage(dir, digest); !errors.Is(err, ErrNoPreimage) {
		t.Fatalf("a staged copy is visible before it is committed: %v", err)
	}
	discardPreimage(dir, digest)
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("discard left %v", entries)
	}
	if err := stagePreimage(dir, p); err != nil {
		t.Fatal(err)
	}
	if err := commitPreimage(dir, digest); err != nil {
		t.Fatal(err)
	}
	if got, err := readPreimage(dir, digest); err != nil || got.Path != "x" {
		t.Fatalf("committed = %+v %v", got, err)
	}
	// Old copies, stale staged copies and copies beyond the bound are removed; stray files are not touched.
	old := time.Now().Add(-8 * 24 * time.Hour)
	oldPath, _ := preimagePath(dir, "sha256:"+strings.Repeat("b", 64))
	stalePending := strings.TrimSuffix(oldPath, ".json") + ".pending"
	for _, path := range []string{oldPath, stalePending} {
		if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	other := filepath.Join(dir, "unrelated.txt")
	if err := os.WriteFile(other, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	prunePreimages(dir, time.Now())
	for _, path := range []string{oldPath, stalePending} {
		if _, err := os.Stat(path); err == nil {
			t.Errorf("%s was not pruned", filepath.Base(path))
		}
	}
	if _, err := os.Stat(other); err != nil {
		t.Error("an unrelated file was pruned")
	}
	if _, err := readPreimage(dir, digest); err != nil {
		t.Errorf("a fresh copy was pruned: %v", err)
	}
	for i := 0; i < maxPreimages+20; i++ {
		path, _ := preimagePath(dir, fmt.Sprintf("sha256:%064x", i+1000))
		if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		mod := time.Now().Add(-time.Duration(maxPreimages+20-i) * time.Minute)
		_ = os.Chtimes(path, mod, mod)
	}
	prunePreimages(dir, time.Now())
	entries, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	if len(entries) != maxPreimages {
		t.Fatalf("%d saved copies kept, want %d", len(entries), maxPreimages)
	}
	if _, err := os.Stat(filepath.Join(dir, fmt.Sprintf("%064x.json", 1000))); err == nil {
		t.Error("the oldest copy was kept over a newer one")
	}
	// A loose or linked directory is refused.
	loose := filepath.Join(t.TempDir(), "loose")
	if err := os.Mkdir(loose, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := stagePreimage(loose, p); err == nil {
		t.Error("a loose directory was used")
	}
}

// ---- the preview is exactly the change ----

var hunkRE = regexp.MustCompile(`^@@ -(\d+),(\d+) \+(\d+),(\d+) @@$`)

// applyUnified applies a preview to the original text the way patch would, and checks the
// hunk headers' counts and start lines against what the body actually contains.
func applyUnified(t *testing.T, original, preview string) string {
	t.Helper()
	lines := splitKeep(original)
	var out []string
	cursor := 0
	rows := strings.Split(strings.TrimSuffix(preview, "\n"), "\n")
	if len(rows) < 2 || !strings.HasPrefix(rows[0], "--- ") || !strings.HasPrefix(rows[1], "+++ ") {
		t.Fatalf("no file header in:\n%s", preview)
	}
	newLen := 0
	for i := 2; i < len(rows); {
		m := hunkRE.FindStringSubmatch(rows[i])
		if m == nil {
			t.Fatalf("expected a hunk header, got %q in:\n%s", rows[i], preview)
		}
		oldStart, _ := strconv.Atoi(m[1])
		oldCount, _ := strconv.Atoi(m[2])
		newStart, _ := strconv.Atoi(m[3])
		newCount, _ := strconv.Atoi(m[4])
		if oldStart-1 < cursor {
			t.Fatalf("hunks overlap or are out of order in:\n%s", preview)
		}
		out = append(out, lines[cursor:oldStart-1]...)
		newLen += oldStart - 1 - cursor
		if newStart != newLen+1 {
			t.Fatalf("hunk says it starts at new line %d but %d lines precede it:\n%s", newStart, newLen, preview)
		}
		cursor = oldStart - 1
		i++
		seenOld, seenNew := 0, 0
		for i < len(rows) && !strings.HasPrefix(rows[i], "@@") {
			row := rows[i]
			switch {
			case strings.HasPrefix(row, "\\ No newline"):
				// Informational: the line before it has no newline, which the original's own
				// text (for context and removed lines) or the look-ahead below (for added lines) carries.
			case row[0] == ' ' || row[0] == '-':
				if cursor >= len(lines) || strings.TrimSuffix(lines[cursor], "\n") != row[1:] {
					t.Fatalf("the preview line %q does not match original line %d", row, cursor+1)
				}
				if row[0] == ' ' {
					out = append(out, lines[cursor])
					seenNew++
					newLen++
				}
				cursor++
				seenOld++
			case row[0] == '+':
				text := row[1:] + "\n"
				if i+1 < len(rows) && strings.HasPrefix(rows[i+1], "\\ No newline") {
					text = row[1:]
				}
				out = append(out, text)
				seenNew++
				newLen++
			default:
				t.Fatalf("bad preview row %q", row)
			}
			i++
		}
		if seenOld != oldCount || seenNew != newCount {
			t.Fatalf("hunk header says -%d +%d but the body has -%d +%d:\n%s", oldCount, newCount, seenOld, seenNew, preview)
		}
	}
	out = append(out, lines[cursor:]...)
	return strings.Join(out, "")
}

func TestThePreviewAppliedToTheOriginalGivesExactlyTheNewFile(t *testing.T) {
	random := rand.New(rand.NewSource(7))
	words := []string{"alpha", "beta", "gamma", "delta", "x", "y", "z", "{", "}", "return", "if", "else"}
	applied := 0
	for trial := 0; trial < 400; trial++ {
		// A random file of unique lines, so every edit target is unambiguous.
		count := 1 + random.Intn(40)
		lines := make([]string, count)
		for i := range lines {
			lines[i] = fmt.Sprintf("L%03d %s %s", i, words[random.Intn(len(words))], words[random.Intn(len(words))])
		}
		original := strings.Join(lines, "\n")
		if random.Intn(3) > 0 {
			original += "\n"
		}
		// Random non-overlapping edits at line or sub-line granularity, including two on one line.
		var specs []editSpec
		used := map[int]bool{}
		for n := random.Intn(5) + 1; n > 0; n-- {
			i := random.Intn(count)
			if used[i] {
				continue
			}
			used[i] = true
			target := lines[i]
			switch random.Intn(5) {
			case 0: // replace the whole line's text
				specs = append(specs, editSpec{OldText: target, NewText: target + " // edited"})
			case 1: // delete the line including its newline when it has one
				if i < count-1 || strings.HasSuffix(original, "\n") {
					specs = append(specs, editSpec{OldText: target + "\n", NewText: ""})
				}
			case 2: // grow into several lines
				specs = append(specs, editSpec{OldText: target, NewText: target + "\nextra one\nextra two"})
			case 3: // a part of a line
				specs = append(specs, editSpec{OldText: target[:4], NewText: "Q" + strconv.Itoa(trial)})
			default: // two edits on the same line
				specs = append(specs, editSpec{OldText: target[:4], NewText: "AA"}, editSpec{OldText: target[len(target)-2:], NewText: "ZZ"})
			}
		}
		if len(specs) == 0 {
			continue
		}
		spans := make([]span, 0, len(specs))
		valid := true
		for _, s := range specs {
			at := strings.Index(original, s.OldText)
			if at < 0 || strings.Contains(original[at+1:], s.OldText) {
				valid = false
				break
			}
			spans = append(spans, span{start: at, end: at + len(s.OldText), text: s.NewText})
		}
		if !valid {
			continue
		}
		sortSpans(spans)
		overlap := false
		for i := 1; i < len(spans); i++ {
			overlap = overlap || spans[i-1].end > spans[i].start
		}
		if overlap {
			continue
		}
		var expected strings.Builder
		cursor := 0
		for _, sp := range spans {
			expected.WriteString(original[cursor:sp.start])
			expected.WriteString(sp.text)
			cursor = sp.end
		}
		expected.WriteString(original[cursor:])
		if expected.String() == original {
			continue
		}
		applied++
		preview, _ := editPreview("f.txt", original, spans)
		if got := applyUnified(t, original, preview); got != expected.String() {
			t.Fatalf("trial %d: applying the preview gives\n%q\nbut the edit gives\n%q\npreview:\n%s\noriginal:\n%q", trial, got, expected.String(), preview, original)
		}
	}
	if applied < 200 {
		t.Fatalf("only %d of 400 generated cases were exercised", applied)
	}
}

func sortSpans(spans []span) {
	for i := 1; i < len(spans); i++ {
		for j := i; j > 0 && spans[j-1].start > spans[j].start; j-- {
			spans[j-1], spans[j] = spans[j], spans[j-1]
		}
	}
}

// A declined action must not stay in the provider's own table, or a few declined calls
// would leave the session unable to prepare anything.
func TestDeclinedActionsAreReleasedFromTheProvider(t *testing.T) {
	e := newEditEnv(t)
	for i := 0; i < 3*maxPending; i++ {
		action := e.prepare(ToolReadFile, map[string]any{"path": "main.go", "start_line": 1 + i%5, "max_lines": 1 + i%7})
		if action.Outcome != harness.OutcomeOK {
			t.Fatalf("call %d: %+v", i, action)
		}
		e.session.Discard(action)
	}
	for i := 0; i < 3*maxPendingMutations; i++ {
		action := e.prepare(ToolCreateFile, map[string]any{"path": fmt.Sprintf("n%d.txt", i), "content": "x"})
		if action.Outcome != harness.OutcomeOK {
			t.Fatalf("mutation %d: %+v", i, action)
		}
		e.session.Discard(action)
	}
}

// The digest names this change to this content in this project and nothing else, so an
// approval of one can never run another.
func TestTheDigestBindsTheRevisionTheResultAndTheProjectRoot(t *testing.T) {
	e := newEditEnv(t)
	write(t, e.root, "d.txt", "a b\n")
	first := e.prepare(ToolEditFile, map[string]any{"path": "d.txt", "edits": edits("a", "x")})
	e.session.Discard(first)
	// A different file that the right edit turns into the very same result is a different change.
	write(t, e.root, "d.txt", "c b\n")
	second := e.prepare(ToolEditFile, map[string]any{"path": "d.txt", "edits": edits("c", "x")})
	e.session.Discard(second)
	if first.Digest == second.Digest {
		t.Fatal("two different reviewed changes with the same result share a digest")
	}
	// The same content at the same relative path in another project is another digest.
	write(t, e.root, "d.txt", "a b\n")
	mine := e.prepare(ToolEditFile, map[string]any{"path": "d.txt", "edits": edits("a", "x")})
	e.session.Discard(mine)
	other := t.TempDir()
	write(t, other, "d.txt", "a b\n")
	e.current = func() string { return other }
	theirs := e.prepare(ToolEditFile, map[string]any{"path": "d.txt", "edits": edits("a", "x")})
	if mine.Digest == theirs.Digest || mine.Digest != first.Digest {
		t.Fatalf("digests: mine %s theirs %s first %s", mine.Digest, theirs.Digest, first.Digest)
	}
}

func TestAnEditThatWouldPassTheFileSizeLimitIsRefused(t *testing.T) {
	e := newEditEnv(t)
	var b strings.Builder
	for b.Len() < harnessfs.MaxWriteBytes-2000 {
		b.WriteString("0123456789\n")
	}
	b.WriteString("MARKER\n")
	write(t, e.root, "near-limit.txt", b.String())
	m := e.run(ToolEditFile, map[string]any{"path": "near-limit.txt", "edits": edits("MARKER\n", strings.Repeat("grow\n", 3000))}, true)
	if m.prepared != harness.OutcomeFailed || m.reason != "too_large" || m.outcome != "" {
		t.Fatalf("%+v", m)
	}
	if e.file("near-limit.txt") != b.String() {
		t.Fatal("the file changed")
	}
}

// If the saved copy cannot be made, the file must not be touched at all, not changed and put
// back: a crash between the two would leave a change that cannot be undone.
func TestNothingIsTouchedWhenTheSavedCopyCannotBeMade(t *testing.T) {
	e := newEditEnv(t)
	if err := os.MkdirAll(e.saved, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(e.saved, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(e.saved, 0o700)
	before, _ := os.Stat(filepath.Join(e.root, "main.go"))
	m := e.run(ToolEditFile, map[string]any{"path": "main.go", "edits": edits("hello", "bye")}, true)
	after, _ := os.Stat(filepath.Join(e.root, "main.go"))
	if m.outcome != harness.OutcomeFailed || !os.SameFile(before, after) || !strings.Contains(e.file("main.go"), "hello") {
		t.Fatalf("outcome %s; the file was replaced even though no copy could be saved", m.outcome)
	}
}

func TestUndoNeverTouchesAnotherProjectWithIdenticalContent(t *testing.T) {
	e := newEditEnv(t)
	m := e.run(ToolEditFile, map[string]any{"path": "main.go", "edits": edits("hello", "bye")}, true)
	other := t.TempDir()
	write(t, other, "main.go", e.file("main.go")) // exactly the state the action left behind
	if _, err := Undo(e.saved, other, m.action.Digest, time.Now()); !errors.Is(err, ErrNotRestorable) {
		t.Fatalf("error = %v", err)
	}
	if data, _ := os.ReadFile(filepath.Join(other, "main.go")); !strings.Contains(string(data), "bye") {
		t.Fatal("another project was modified")
	}
}

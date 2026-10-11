package harnesstools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
	"github.com/taimufuraiyaa/agent-memory/internal/harness/harnesstest"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessapproval"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessproof"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessrun"
)

// A fake toolchain: small scripts that behave as chosen by their first argument, so real
// processes run through the real provider without needing a real language toolchain.
const fakeTool = `#!/bin/sh
case "$1" in
  echo) echo "hello $2"; echo "pwd=$PWD" ;;
  fail) echo "boom" >&2; exit 4 ;;
  sleep) sleep 30 ;;
  write) echo data > created.txt; echo changed >> existing.txt ;;
  remove) rm -f gone.txt ;;
  hook) mkdir -p .git/hooks; echo evil > .git/hooks/post-checkout ;;
  env) env ;;
  flood) i=0; while [ $i -lt 3000 ]; do echo "line $i of the flood of output lines"; i=$((i+1)); done; echo THE-END ;;
  secret) echo "key AKIAIOSFODNN7EXAMPLE" ;;
  child) sleep 60 & echo "child $!" ;;
  tmp) echo "tmp=$TMPDIR"; touch "$TMPDIR/scratch" ;;
  escape) printf 'plain \033[31mred\033[0m \007bell\n' ;;
  marker) echo ran > marker.txt ;;
  show) cat "$2" ;;
  selfkill) echo "dying"; kill -9 $$ ;;
  many) mkdir -p bulk; i=0; while [ $i -lt 40 ]; do echo x > bulk/f$i.txt; i=$((i+1)); done ;;
  *) echo "go $*" ;;
esac
`

type cmdTool struct {
	*editEnv
	tools string
	temp  string
}

func newCmdTool(t *testing.T) *cmdTool {
	t.Helper()
	c := &cmdTool{editEnv: newEditEnv(t), tools: realTemp(t), temp: filepath.Join(realTemp(t), "run-temp")}
	for _, name := range []string{"mytool", "go", "make", "npm", "python3", "bash"} {
		if err := os.WriteFile(filepath.Join(c.tools, name), []byte(fakeTool), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write(t, c.root, "existing.txt", "start\n")
	write(t, c.root, "gone.txt", "bye\n")
	c.open(Config{Redact: redact, Edit: EditConfig{Enabled: true, PreimageDir: c.saved},
		Command: CommandConfig{Enabled: true, SearchPath: []string{c.tools}, Home: "/home/tester", TempRoot: c.temp, MaxOutput: 8 << 10}})
	return c
}

func (c *cmdTool) cmd(approved bool, argv []string, extra ...string) mutation {
	c.t.Helper()
	raw := `{"argv":` + jsonArray(argv)
	for _, kv := range extra {
		raw += "," + kv
	}
	return c.run(ToolRunCommand, raw+"}", approved)
}

func TestCommandsAreOffUnlessEnabledAndNeedAPrivateTemporaryDirectory(t *testing.T) {
	e := newEditEnv(t) // commands not enabled
	if e.session.State(ToolRunCommand) == harness.AccessAvailable {
		t.Fatal("run_command is offered with commands off")
	}
	if _, err := e.session.Envelope(ToolRunCommand, 64); err == nil {
		t.Fatal("an envelope was issued for run_command with commands off")
	}
	if _, err := NewProvider(Config{Root: func(string) (string, error) { return "", nil }, Command: CommandConfig{Enabled: true}}); err == nil {
		t.Fatal("commands were enabled without a temporary directory")
	}
	c := newCmdTool(t)
	if c.session.State(ToolRunCommand) != harness.AccessAvailable || !contains(capabilityNames(c.provider.Manifest()), ToolRunCommand) || !Mutating(ToolRunCommand) {
		t.Fatalf("manifest = %v", c.provider.Manifest().Capabilities)
	}
	names := capabilityNames(c.provider.Manifest())
	if sorted := append([]string(nil), names...); !reflect.DeepEqual(names, sortedCopy(sorted)) {
		t.Fatalf("the manifest is not in a fixed order: %v", names)
	}
	// Commands on, editing off: only the command tool is added to the read tools.
	only := newEnv(t)
	only.open(Config{Redact: redact, Command: CommandConfig{Enabled: true, TempRoot: t.TempDir()}})
	if got := capabilityNames(only.provider.Manifest()); !reflect.DeepEqual(got, []string{ToolListDir, ToolReadFile, ToolRunCommand, ToolSearch}) {
		t.Fatalf("manifest = %v", got)
	}
}

func capabilityNames(m harness.Manifest) []string {
	out := make([]string, len(m.Capabilities))
	for i, c := range m.Capabilities {
		out[i] = string(c)
	}
	return out
}

func sortedCopy(in []string) []string {
	for i := 1; i < len(in); i++ {
		for j := i; j > 0 && in[j-1] > in[j]; j-- {
			in[j-1], in[j] = in[j], in[j-1]
		}
	}
	return in
}

func TestPreparingACommandRunsNothingAndShowsExactlyWhatWouldRun(t *testing.T) {
	c := newCmdTool(t)
	m := c.cmd(false, []string{"mytool", "marker"}, `"cwd":"internal"`, `"timeout_seconds":30`)
	if m.prepared != harness.OutcomeOK || m.action.Digest == "" {
		t.Fatalf("%+v", m)
	}
	if c.exists("internal/marker.txt") || c.exists("marker.txt") {
		t.Fatal("preparing ran the command")
	}
	a := m.action
	if !reflect.DeepEqual(a.Paths, []string{"internal"}) || !reflect.DeepEqual(a.Escalate, []string{"not_cataloged"}) || !strings.HasPrefix(a.Summary, "run mytool marker (in internal)") {
		t.Fatalf("action = %+v", a)
	}
	for _, want := range []string{"RUN COMMAND", "no shell", "command      mytool marker", `argv         ["mytool","marker"]`, filepath.Join(c.tools, "mytool"), "directory    internal",
		"30s of run time", "8 KiB of output", "NOT A KNOWN TEST, BUILD OR LINT COMMAND", "not one of the recognized toolchain commands", "built, not inherited", "PATH=" + c.tools,
		"HOME=/home/tester", "not sandboxed", "reported afterwards"} {
		if !strings.Contains(a.Preview, want) {
			t.Errorf("the preview lacks %q:\n%s", want, a.Preview)
		}
	}
	// A cataloged command says what it is and carries no escalation.
	known := c.cmd(false, []string{"go", "test", "-race", "./..."})
	if known.prepared != harness.OutcomeOK || len(known.action.Escalate) != 0 || !strings.Contains(known.action.Preview, "recognized   go test: run Go tests") ||
		!strings.Contains(known.action.Preview, "GOPROXY=off") || strings.Contains(known.action.Preview, "NOT A KNOWN") {
		t.Fatalf("known = %+v", known.action)
	}
	// Quoting in the display is for reading and unambiguous.
	quoted := c.cmd(false, []string{"mytool", "echo", "it's a test", "", "a b"})
	if !strings.Contains(quoted.action.Preview, `mytool echo 'it'\''s a test' '' 'a b'`) {
		t.Fatalf("display:\n%s", quoted.action.Preview)
	}
	// Nothing in the preview can hide a character from the reviewer.
	tricky := c.cmd(false, []string{"mytool", "a\u202eb", "c\u200bd"})
	if strings.Contains(tricky.action.Preview, "\u202e") || strings.Contains(tricky.action.Preview, "\u200b") || !strings.Contains(tricky.action.Preview, "⟨U+202E⟩") {
		t.Fatalf("a hidden character reached the reviewer:\n%s", tricky.action.Preview)
	}
}

func TestACommandNeedsTheManagersProofForExactlyThatAction(t *testing.T) {
	c := newCmdTool(t)
	if m := c.cmd(false, []string{"mytool", "marker"}); m.outcome != harness.OutcomeDenied || c.exists("marker.txt") {
		t.Fatalf("without a proof: %+v", m)
	}
	action := c.prepare(ToolRunCommand, `{"argv":["mytool","marker"]}`)
	other := harnessproof.Mint(context.Background(), "sha256:"+strings.Repeat("0", 64))
	if answer, err := c.session.Invoke(other, action); err != nil || answer.Outcome != harness.OutcomeDenied || c.exists("marker.txt") {
		t.Fatalf("with another action's proof: %+v %v", answer, err)
	}
	if m := c.cmd(true, []string{"mytool", "marker"}); m.outcome != harness.OutcomeOK || !c.exists("marker.txt") {
		t.Fatalf("approved: %+v", m)
	}
}

func TestAnApprovedCommandRunsInTheRequestedDirectoryAndReportsItsResult(t *testing.T) {
	c := newCmdTool(t)
	m := c.cmd(true, []string{"mytool", "echo", "world"}, `"cwd":"internal/app"`)
	if m.outcome != harness.OutcomeOK {
		t.Fatalf("%+v", m)
	}
	realDir, _ := filepath.EvalSymlinks(filepath.Join(c.root, "internal/app"))
	for _, want := range []string{"$ mytool echo world   (in internal/app)", "exit status 0 after", "hello world", "pwd=" + realDir, "files changed by this command: none"} {
		if !strings.Contains(m.output, want) {
			t.Errorf("the result lacks %q:\n%s", want, m.output)
		}
	}
	// A failing command is a result the model can read, with its error stream, not a tool failure.
	failed := c.cmd(true, []string{"mytool", "fail"})
	if failed.outcome != harness.OutcomeOK || !strings.Contains(failed.output, "exit status 4") || !strings.Contains(failed.output, "boom") {
		t.Fatalf("%+v", failed)
	}
}

func TestWhatACommandChangedIsReportedAndProtectedLocationsAreAudited(t *testing.T) {
	c := newCmdTool(t)
	write(t, c.root, ".git/config", "[core]\n")
	answer := func(argv ...string) harness.ToolAnswer {
		action := c.prepare(ToolRunCommand, `{"argv":`+jsonArray(argv)+`}`)
		if action.Outcome != harness.OutcomeOK {
			t.Fatalf("%v: %+v", argv, action)
		}
		out, err := c.session.Invoke(harnessproof.Mint(context.Background(), action.Digest), action)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	written := answer("mytool", "write")
	text := string(written.Output)
	if !strings.Contains(text, "created: created.txt") || !strings.Contains(text, "modified: existing.txt") || strings.Contains(text, "PROTECTED") || len(written.Audit) != 0 {
		t.Fatalf("write:\n%s\naudit %v", text, written.Audit)
	}
	if removed := string(answer("mytool", "remove").Output); !strings.Contains(removed, "removed: gone.txt") {
		t.Fatalf("remove:\n%s", removed)
	}
	hook := answer("mytool", "hook")
	text = string(hook.Output)
	if !strings.Contains(text, "PROTECTED LOCATIONS CHANGED") || !strings.Contains(text, ".git/hooks/post-checkout") || !reflect.DeepEqual(hook.Audit, []string{"protected_changed"}) {
		t.Fatalf("hook:\n%s\naudit %v", text, hook.Audit)
	}
}

func TestACommandSeesOnlyTheBuiltEnvironmentAndAPrivateTemporaryDirectoryThatIsRemoved(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-do-not-leak")
	t.Setenv("DB_PASSWORD", "do-not-leak")
	t.Setenv("GOFLAGS", "-exec=evil")
	t.Setenv("LD_PRELOAD", "/tmp/evil.so")
	c := newCmdTool(t)
	m := c.cmd(true, []string{"mytool", "env"})
	if m.outcome != harness.OutcomeOK {
		t.Fatalf("%+v", m)
	}
	for _, leaked := range []string{"OPENAI_API_KEY", "DB_PASSWORD", "do-not-leak", "GOFLAGS", "LD_PRELOAD", "SSH_AUTH_SOCK", "USER="} {
		if strings.Contains(m.output, leaked) {
			t.Errorf("the command saw %q:\n%s", leaked, m.output)
		}
	}
	for _, want := range []string{"PATH=" + c.tools, "HOME=/home/tester", "LANG=en_US.UTF-8", "TERM=dumb", "NO_COLOR=1", "CI=1", "TMPDIR=" + c.temp} {
		if !strings.Contains(m.output, want) {
			t.Errorf("the command's environment lacks %q:\n%s", want, m.output)
		}
	}
	tmp := c.cmd(true, []string{"mytool", "tmp"})
	line := ""
	for _, l := range strings.Split(tmp.output, "\n") {
		if strings.HasPrefix(l, "tmp=") {
			line = strings.TrimPrefix(l, "tmp=")
		}
	}
	if !strings.HasPrefix(line, c.temp+string(filepath.Separator)+"cmd-") {
		t.Fatalf("the temporary directory = %q", line)
	}
	if _, err := os.Stat(line); err == nil {
		t.Fatal("the command's temporary directory was left behind")
	}
	if info, _ := os.Stat(c.temp); info.Mode().Perm() != 0o700 {
		t.Fatalf("the temporary root mode = %v", info.Mode().Perm())
	}
	if entries, _ := os.ReadDir(c.temp); len(entries) != 0 {
		t.Fatalf("leftovers: %v", entries)
	}
}

func TestATimedOutCommandIsStoppedWithItsChildrenAndReportedAsPartial(t *testing.T) {
	c := newCmdTool(t)
	began := time.Now()
	m := c.cmd(true, []string{"mytool", "sleep"}, `"timeout_seconds":1`)
	if m.outcome != harness.OutcomePartial || !strings.Contains(m.output, "timed out after 1s and was stopped") || time.Since(began) > 15*time.Second {
		t.Fatalf("%+v after %v", m, time.Since(began))
	}
	action := c.prepare(ToolRunCommand, `{"argv":["mytool","sleep"],"timeout_seconds":1}`)
	answer, err := c.session.Invoke(harnessproof.Mint(context.Background(), action.Digest), action)
	if err != nil || !reflect.DeepEqual(answer.Audit, []string{"timed_out"}) {
		t.Fatalf("audit = %+v %v", answer, err)
	}
	// A child a finished command left behind does not survive it.
	child := c.cmd(true, []string{"mytool", "child"})
	var pid int
	for _, l := range strings.Split(child.output, "\n") {
		if strings.HasPrefix(l, "child ") {
			pid, _ = strconv.Atoi(strings.TrimPrefix(l, "child "))
		}
	}
	if pid == 0 {
		t.Fatalf("no child pid:\n%s", child.output)
	}
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if syscall.Kill(pid, 0) != nil {
			return
		}
		if out, _ := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output(); strings.HasPrefix(strings.TrimSpace(string(out)), "Z") {
			return
		}
	}
	t.Fatalf("the child %d outlived the command", pid)
}

func TestCancellingARunningCommandStopsItPromptly(t *testing.T) {
	c := newCmdTool(t)
	action := c.prepare(ToolRunCommand, `{"argv":["mytool","sleep"],"timeout_seconds":60}`)
	ctx, cancel := context.WithCancel(harnessproof.Mint(context.Background(), action.Digest))
	time.AfterFunc(300*time.Millisecond, cancel)
	began := time.Now()
	answer, err := c.session.Invoke(ctx, action)
	if err != nil || answer.Outcome != harness.OutcomeCancelled || time.Since(began) > 10*time.Second || answer.Output != nil {
		t.Fatalf("%+v %v after %v", answer, err, time.Since(began))
	}
}

func TestOutputIsRedactedBoundedToTheReplyAndKeepsItsEnd(t *testing.T) {
	c := newCmdTool(t)
	secret := c.cmd(true, []string{"mytool", "secret"})
	if strings.Contains(secret.output, "AKIAIOSFODNN7EXAMPLE") || !strings.Contains(secret.output, "[REDACTED_SECRET]") {
		t.Fatalf("a secret reached the model:\n%s", secret.output)
	}
	flood := c.cmd(true, []string{"mytool", "flood"})
	if flood.outcome != harness.OutcomePartial || len(flood.output) > 4096 || !strings.Contains(flood.output, "THE-END") || !strings.Contains(flood.output, "$ mytool flood") ||
		!strings.Contains(flood.output, "files changed by this command") {
		t.Fatalf("a flood: outcome %s, %d bytes\n%s", flood.outcome, len(flood.output), flood.output)
	}
	esc := c.cmd(true, []string{"mytool", "escape"})
	if strings.ContainsAny(esc.output, "\x1b\x07") || !strings.Contains(esc.output, "plain red bell") {
		t.Fatalf("control characters reached the model: %q", esc.output)
	}
}

func TestAnExecutableThatChangedAfterReviewIsNeverRun(t *testing.T) {
	c := newCmdTool(t)
	action := c.prepare(ToolRunCommand, `{"argv":["mytool","marker"]}`)
	if err := os.WriteFile(filepath.Join(c.tools, "mytool"), []byte("#!/bin/sh\necho replaced > marker.txt\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	answer, err := c.session.Invoke(harnessproof.Mint(context.Background(), action.Digest), action)
	if err != nil || answer.Outcome != harness.OutcomeStale || c.exists("marker.txt") {
		t.Fatalf("%+v %v", answer, err)
	}
	// The same for a script inside the project: its content is part of what was approved.
	write(t, c.root, "scripts/run.sh", "#!/bin/sh\necho reviewed > marker.txt\n")
	if err := os.Chmod(filepath.Join(c.root, "scripts/run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := c.prepare(ToolRunCommand, `{"argv":["./scripts/run.sh"]}`)
	if script.Outcome != harness.OutcomeOK || !contains(script.Escalate, "project_program") || !strings.Contains(script.Preview, "this runs a script that lives in the project") {
		t.Fatalf("%+v", script)
	}
	write(t, c.root, "scripts/run.sh", "#!/bin/sh\necho changed > marker.txt\n")
	if err := os.Chmod(filepath.Join(c.root, "scripts/run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	if answer, err := c.session.Invoke(harnessproof.Mint(context.Background(), script.Digest), script); err != nil || answer.Outcome != harness.OutcomeStale || c.exists("marker.txt") {
		t.Fatalf("a changed script ran: %+v %v", answer, err)
	}
	// Unchanged, it runs, and the program it runs is the one that was reviewed.
	fresh := c.cmd(true, []string{"./scripts/run.sh"})
	if fresh.outcome != harness.OutcomeOK || c.file("marker.txt") != "changed\n" {
		t.Fatalf("%+v", fresh)
	}
}

func TestRefusedCommandsNeverRunAndNeverReachAPerson(t *testing.T) {
	c := newCmdTool(t)
	for _, argv := range [][]string{{"bash", "marker"}, {"sh", "-c", "echo x > marker.txt"}, {"/bin/sh", "marker"}, {"./../mytool", "marker"}, {"env", "mytool", "marker"}, {"nohup", "mytool"}} {
		m := c.cmd(true, argv)
		if m.prepared == harness.OutcomeOK || m.outcome != "" || c.exists("marker.txt") {
			t.Errorf("%v: %+v", argv, m)
		}
	}
	for name, raw := range map[string]string{
		"a shell string":  `{"argv":["mytool marker > marker.txt"]}`,
		"a cwd outside":   `{"argv":["mytool","marker"],"cwd":".."}`,
		"a hidden cwd":    `{"argv":["mytool","marker"],"cwd":".git"}`,
		"a link cwd":      `{"argv":["mytool","marker"],"cwd":"link-out.txt"}`,
		"a file cwd":      `{"argv":["mytool","marker"],"cwd":"main.go"}`,
		"an env override": `{"argv":["mytool","marker"],"env":{"A":"1"}}`,
		"a long timeout":  `{"argv":["mytool","marker"],"timeout_seconds":9999}`,
	} {
		if m := c.run(ToolRunCommand, raw, true); m.prepared == harness.OutcomeOK || c.exists("marker.txt") {
			t.Errorf("%s: %+v", name, m)
		}
	}
	// A program that cannot be started is a failure with no path in it.
	if err := os.WriteFile(filepath.Join(c.tools, "broken"), []byte("#!/nonexistent/interpreter\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := c.cmd(true, []string{"broken"})
	if m.outcome != harness.OutcomeFailed || strings.Contains(m.output, c.tools) {
		t.Fatalf("%+v", m)
	}
}

func TestPendingCommandsAreBoundedAndReleasedWhenDeclined(t *testing.T) {
	c := newCmdTool(t)
	var held []harness.PreparedAction
	for i := 0; i < maxPendingMutations; i++ {
		a := c.prepare(ToolRunCommand, fmt.Sprintf(`{"argv":["mytool","echo","n%d"]}`, i))
		if a.Outcome != harness.OutcomeOK {
			t.Fatalf("%d: %+v", i, a)
		}
		held = append(held, a)
	}
	if over := c.prepare(ToolRunCommand, `{"argv":["mytool","echo","one-too-many"]}`); over.Outcome != harness.OutcomeFailed || over.Reason != "busy" {
		t.Fatalf("%+v", over)
	}
	c.session.Discard(held[0])
	if again := c.prepare(ToolRunCommand, `{"argv":["mytool","echo","one-too-many"]}`); again.Outcome != harness.OutcomeOK {
		t.Fatalf("after a discard: %+v", again)
	}
}

func TestACommandReplayedAfterApprovalIsTheSameDigestAsReviewed(t *testing.T) {
	c := newCmdTool(t)
	first := c.prepare(ToolRunCommand, `{"argv":["go","test","./..."]}`)
	c.session.Discard(first)
	again := c.prepare(ToolRunCommand, `{"argv":["go","test","./..."]}`)
	if first.Digest != again.Digest {
		t.Fatal("a replay prepares to another digest")
	}
	c.session.Discard(again)
	other := c.prepare(ToolRunCommand, `{"argv":["go","test","-race","./..."]}`)
	if other.Digest == first.Digest {
		t.Fatal("different arguments share a digest")
	}
}

func TestCommandPolicyTiers(t *testing.T) {
	policy := ProjectPolicy()
	action := func(capability, dir string, escalate ...string) harness.PreparedAction {
		return harness.PreparedAction{Envelope: harness.Envelope{Capability: harness.CapabilityID(capability)}, Outcome: harness.OutcomeOK,
			Digest: "sha256:" + strings.Repeat("a", 64), Paths: []string{dir}, Escalate: escalate}
	}
	ctx := context.Background()
	for name, tc := range map[string]struct {
		action harness.PreparedAction
		want   string
		why    []string
	}{
		"a cataloged command":           {action(ToolRunCommand, "."), "ask", nil},
		"a cataloged one in a subdir":   {action(ToolRunCommand, "internal/app"), "ask", nil},
		"in a directory called scripts": {action(ToolRunCommand, "scripts/sub"), "ask", nil},
		"a recipe":                      {action(ToolRunCommand, ".", "runs_recipes"), "strict", []string{"runs_recipes"}},
		"not cataloged":                 {action(ToolRunCommand, ".", "not_cataloged"), "strict", []string{"not_cataloged"}},
		"a project program":             {action(ToolRunCommand, ".", "project_program", "not_cataloged"), "strict", []string{"project_program"}},
	} {
		got := policy.Decide(ctx, harnessrunOwner(), tc.action)
		label := map[string]string{"ask": "ask", "strict": "strict"}[tc.want]
		if (label == "ask") != (got == decisionAsk) || (label == "strict") != (got == decisionAskStrict) {
			t.Errorf("%s: decision %v", name, got)
		}
		for _, w := range tc.why {
			if !contains(policy.Reasons(tc.action), w) {
				t.Errorf("%s: reasons %v lack %q", name, policy.Reasons(tc.action), w)
			}
		}
	}
	// A table cannot allow a command to run unprompted, and the read-only policy denies it.
	loose := NewPolicy(map[harness.CapabilityID]Tier{ToolRunCommand: TierAllow})
	if got := loose.Decide(ctx, harnessrunOwner(), action(ToolRunCommand, ".")); got == decisionAllow || got == decisionDeny {
		t.Errorf("an allow table = %v", got)
	}
	if got := ReadOnlyPolicy().Decide(ctx, harnessrunOwner(), action(ToolRunCommand, ".")); got != decisionDeny {
		t.Errorf("the read-only policy decided %v", got)
	}
	if got := EditPolicy().Decide(ctx, harnessrunOwner(), action(ToolRunCommand, ".")); got != decisionDeny {
		t.Errorf("the edit policy decided %v", got)
	}
}

// ---- through the real manager, store and approval gate ----

func newCmdLoop(t *testing.T, script []harnesstest.Step) (*editLoop, *cmdTool) {
	t.Helper()
	c := newCmdTool(t)
	return newEditLoopOn(t, c.editEnv, script, ProjectPolicy()), c
}

func TestACommandIsReviewedApprovedRunAndItsResultReachesTheModel(t *testing.T) {
	l, c := newCmdLoop(t, []harnesstest.Step{{ToolID: ToolRunCommand, Arguments: `{"argv":["go","test","./..."]}`}, {Text: "tests ran"}})
	started := l.start("key-00000001")
	record := l.parked(started.ID)
	if record.Friction != "standard" || record.Tool != ToolRunCommand || len(record.Reasons) != 0 || !reflect.DeepEqual(record.Paths, []string{"."}) ||
		!strings.Contains(record.Preview, "recognized   go test: run Go tests") {
		t.Fatalf("record = %+v", record)
	}
	if strings.Count(record.Preview, "go test ./...") == 0 {
		t.Fatalf("preview:\n%s", record.Preview)
	}
	l.approve(record)
	done := l.wait(started.ID, harnessrun.StateCompleted)
	text := l.runText(started.ID)
	if done.Usage.ToolCalls != 1 || !strings.Contains(text, "go test ./...") || !strings.Contains(text, "exit status 0") || !strings.Contains(text, "files changed by this command: none") {
		t.Fatalf("the model did not get the result: %s", text)
	}
	if got, _ := l.approvals.Get(context.Background(), record.ID); got.State != "consumed" || got.Outcome != "applied" {
		t.Fatalf("approval = %+v", got)
	}
	_ = c
}

func TestAnUncatalogedCommandAsksWithExtraFrictionAndNothingRunsBeforeApproval(t *testing.T) {
	l, c := newCmdLoop(t, []harnesstest.Step{{ToolID: ToolRunCommand, Arguments: `{"argv":["mytool","marker"]}`}, {Text: "done"}})
	started := l.start("key-00000001")
	record := l.parked(started.ID)
	if record.Friction != "strict" || !contains(record.Reasons, "not_cataloged") || len(harnessapproval.Code(record)) != 9 {
		t.Fatalf("record = %+v", record)
	}
	time.Sleep(80 * time.Millisecond)
	if c.exists("marker.txt") {
		t.Fatal("the command ran before anyone approved")
	}
	l.approve(record)
	l.wait(started.ID, harnessrun.StateCompleted)
	if c.file("marker.txt") != "ran\n" {
		t.Fatal("the approved command did not run")
	}
}

func TestADeniedCommandNeverRuns(t *testing.T) {
	l, c := newCmdLoop(t, []harnesstest.Step{{ToolID: ToolRunCommand, Arguments: `{"argv":["mytool","marker"]}`}, {Text: "ok"}})
	started := l.start("key-00000001")
	record := l.parked(started.ID)
	if _, err := l.approvals.Decide(context.Background(), record.ID, harnessapproval.Decision{Via: "terminal"}); err != nil {
		t.Fatal(err)
	}
	l.wait(started.ID, harnessrun.StateCompleted)
	if c.exists("marker.txt") || !strings.Contains(l.runText(started.ID), "tool_denied: denied by the person") {
		t.Fatalf("a denied command ran or the model was not told: %s", l.runText(started.ID))
	}
}

func TestAnExecutableSwappedWhileTheApprovalWaitsIsNotRun(t *testing.T) {
	l, c := newCmdLoop(t, []harnesstest.Step{{ToolID: ToolRunCommand, Arguments: `{"argv":["mytool","marker"]}`}, {Text: "stopped"}})
	started := l.start("key-00000001")
	record := l.parked(started.ID)
	if err := os.WriteFile(filepath.Join(c.tools, "mytool"), []byte("#!/bin/sh\necho swapped > marker.txt\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	l.approve(record)
	l.wait(started.ID, harnessrun.StateCompleted)
	if c.exists("marker.txt") || !strings.Contains(l.runText(started.ID), "tool_stale: changed_since_review") {
		t.Fatalf("a swapped executable ran: %s", l.runText(started.ID))
	}
}

func TestAProtectedChangeByAnApprovedCommandIsInTheAuditAndTheModelsResult(t *testing.T) {
	l, c := newCmdLoop(t, []harnesstest.Step{{ToolID: ToolRunCommand, Arguments: `{"argv":["mytool","hook"]}`}, {Text: "done"}})
	write(t, c.root, ".git/config", "[core]\n")
	started := l.start("key-00000001")
	l.approve(l.parked(started.ID))
	l.wait(started.ID, harnessrun.StateCompleted)
	if !strings.Contains(l.runText(started.ID), "PROTECTED LOCATIONS CHANGED") {
		t.Fatalf("the model was not told: %s", l.runText(started.ID))
	}
	audit, err := os.ReadFile(filepath.Join(l.dir, "approvals", "audit.jsonl"))
	if err != nil || !strings.Contains(string(audit), `"event":"protected_changed"`) {
		t.Fatalf("the audit does not record it: %s %v", audit, err)
	}
	if n, err := l.approvals.VerifyAudit(context.Background()); err != nil || n != 5 { // requested, approved, consumed, finished, protected_changed
		t.Fatalf("audit = %d %v", n, err)
	}
}

// The whole working loop: read, change, approve the change, run the tests against the change,
// approve the run, and see the result.
func TestEditThenTestThroughTwoApprovals(t *testing.T) {
	l, c := newCmdLoop(t, []harnesstest.Step{
		{ToolID: ToolEditFile, Arguments: `{"path":"existing.txt","edits":[{"old_text":"start","new_text":"edited"}]}`},
		{ToolID: ToolRunCommand, Arguments: `{"argv":["mytool","show","existing.txt"]}`},
		{Text: "the edit is in place and the check saw it"},
	})
	started := l.start("key-00000001")
	first := l.parked(started.ID)
	if first.Tool != ToolEditFile {
		t.Fatalf("first = %+v", first)
	}
	l.approve(first)
	var second harnessapproval.Record
	for deadline := time.Now().Add(8 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		status, _ := l.manager.Status(context.Background(), loopOwner, started.ID)
		if status.State == harnessrun.StateNeedsAttention && status.Attention != nil && status.Attention.ApprovalID != first.ID {
			second, _ = l.approvals.Get(context.Background(), status.Attention.ApprovalID)
			break
		}
	}
	if second.Tool != ToolRunCommand {
		t.Fatalf("the second approval = %+v", second)
	}
	if c.file("existing.txt") != "edited\n" {
		t.Fatalf("the edit was not applied before the command was asked about: %q", c.file("existing.txt"))
	}
	l.approve(second)
	done := l.wait(started.ID, harnessrun.StateCompleted)
	if done.Usage.ToolCalls != 2 || !strings.Contains(l.runText(started.ID), "edited") {
		t.Fatalf("done = %+v\n%s", done, l.runText(started.ID))
	}
}

func TestAKilledCommandAndALongChangeListAreReportedAsSuch(t *testing.T) {
	c := newCmdTool(t)
	killed := c.cmd(true, []string{"mytool", "selfkill"})
	if killed.outcome != harness.OutcomeOK || !strings.Contains(killed.output, "killed by signal killed after") || !strings.Contains(killed.output, "dying") {
		t.Fatalf("%+v", killed)
	}
	many := c.cmd(true, []string{"mytool", "many"})
	if many.outcome != harness.OutcomeOK || !strings.Contains(many.output, "…and 15 more") || strings.Count(many.output, "bulk/f") != 25 {
		t.Fatalf("a long change list was not bounded and counted:\n%s", many.output)
	}
}

// What a person is asked to read has a bound, and a command whose preview would exceed it is
// not offered, however it got that long.
func TestACommandWhoseReviewCouldNotBeReadInFullIsNotOffered(t *testing.T) {
	c := newCmdTool(t)
	// Characters the preview must spell out visibly make it several times longer than the
	// argument vector itself, so a vector within its own bound can still be too long to review.
	filler := strings.Repeat("\u202e", 340)
	argv := []string{"mytool"}
	total := len("mytool")
	for total+len(filler) <= maxArgvBytes {
		argv = append(argv, filler)
		total += len(filler)
	}
	m := c.cmd(false, argv)
	if m.prepared != harness.OutcomeFailed || m.reason != "change_too_large" {
		t.Fatalf("an over-long review was offered: %s %s (%d bytes of argv)", m.prepared, m.reason, total)
	}
}

func TestATimedOutCommandIsRecordedAsHavingRunAndItsProtectedChangesAreStillAudited(t *testing.T) {
	l, _ := newCmdLoop(t, []harnesstest.Step{{ToolID: ToolRunCommand, Arguments: `{"argv":["mytool","sleep"],"timeout_seconds":1}`}, {Text: "done"}})
	started := l.start("key-00000001")
	record := l.parked(started.ID)
	l.approve(record)
	l.wait(started.ID, harnessrun.StateCompleted)
	got, _ := l.approvals.Get(context.Background(), record.ID)
	audit, _ := os.ReadFile(filepath.Join(l.dir, "approvals", "audit.jsonl"))
	if got.State != "consumed" || got.Outcome != "applied" || !strings.Contains(string(audit), `"event":"timed_out"`) || !strings.Contains(l.runText(started.ID), "timed out after 1s") {
		t.Fatalf("approval %+v\naudit %s", got, audit)
	}
}

// The built environment must still be enough for a real toolchain: this runs the real go
// command, offline, against a small module, when one is installed.
func TestARealGoToolchainRunsUnderTheBuiltEnvironment(t *testing.T) {
	goPath, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go toolchain on this machine")
	}
	c := newCmdTool(t)
	c.open(Config{Redact: redact, Command: CommandConfig{Enabled: true, SearchPath: []string{filepath.Dir(goPath)}, TempRoot: c.temp}})
	write(t, c.root, "real/go.mod", "module example.com/real\n\ngo 1.21\n")
	write(t, c.root, "real/real.go", "package real\n\nfunc Double(n int) int { return n * 2 }\n")
	write(t, c.root, "real/real_test.go", "package real\n\nimport \"testing\"\n\nfunc TestDouble(t *testing.T) {\n\tif Double(2) != 4 {\n\t\tt.Fatal(\"wrong\")\n\t}\n}\n\nfunc TestBroken(t *testing.T) {\n\tt.Fatal(\"deliberate failure\")\n}\n")
	pass := c.cmd(true, []string{"go", "test", "-run", "TestDouble", "-count=1", "./..."}, `"cwd":"real"`, `"timeout_seconds":120`)
	if pass.outcome != harness.OutcomeOK || !strings.Contains(pass.output, "exit status 0") || !strings.Contains(pass.output, "ok") || !strings.Contains(pass.output, "files changed by this command: none") {
		t.Fatalf("a passing real go test:\n%+v", pass)
	}
	fail := c.cmd(true, []string{"go", "test", "-count=1", "-run", "TestBroken", "./..."}, `"cwd":"real"`, `"timeout_seconds":120`)
	if fail.outcome != harness.OutcomeOK || !strings.Contains(fail.output, "exit status 1") || !strings.Contains(fail.output, "deliberate failure") {
		t.Fatalf("a failing real go test:\n%+v", fail)
	}
	// Module fetching is off, so a missing dependency fails instead of reaching for the network.
	write(t, c.root, "real/needs.go", "package real\n\nimport _ \"example.org/not/a/real/module\"\n")
	offline := c.cmd(true, []string{"go", "build", "./..."}, `"cwd":"real"`, `"timeout_seconds":120`)
	if offline.outcome != harness.OutcomeOK || strings.Contains(offline.output, "exit status 0") {
		t.Fatalf("a missing module did not fail offline:\n%s", offline.output)
	}
}

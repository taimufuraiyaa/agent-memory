package harnesstools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
	"github.com/taimufuraiyaa/agent-memory/internal/harness/harnesstest"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessapproval"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessproof"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessrun"
)

// gitTool is the project with a real repository in it and the Git tools enabled.
type gitTool struct {
	*editEnv
	home   string
	temp   string
	gitDir string
	env    []string
}

func newGitTool(t *testing.T) *gitTool { return newGitToolWith(t, true) }

// newGitToolWith builds the project as a repository; with commit false the repository has
// no commits yet.
func newGitToolWith(t *testing.T, commit bool) *gitTool {
	t.Helper()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git on this machine")
	}
	g := &gitTool{editEnv: newEditEnv(t), home: realTemp(t), temp: filepath.Join(realTemp(t), "gtmp"), gitDir: filepath.Dir(gitPath)}
	g.env = []string{"PATH=" + g.gitDir + ":/usr/bin:/bin", "HOME=" + g.home, "GIT_CONFIG_NOSYSTEM=1", "LC_ALL=C"}
	g.run0("init", "-q", "-b", "main")
	g.run0("config", "user.name", "Test Person")
	g.run0("config", "user.email", "test@example.com")
	if commit {
		g.run0("add", "main.go", "internal/app/app.go", "docs/readme.md")
		g.run0("commit", "-q", "-m", "initial commit")
	}
	g.open(Config{Redact: redact, Edit: EditConfig{Enabled: true, PreimageDir: g.saved},
		Git: GitConfig{Enabled: true, SearchPath: []string{g.gitDir}, Home: g.home, TempRoot: g.temp}})
	return g
}

func (g *gitTool) run0(args ...string) string {
	g.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir, cmd.Env = g.root, g.env
	out, err := cmd.CombinedOutput()
	if err != nil {
		g.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (g *gitTool) tool(approved bool, name string, args any) mutation {
	return g.run(name, args, approved)
}

func (g *gitTool) head() string { return g.run0("rev-parse", "HEAD") }

func (g *gitTool) marks() string {
	dir := filepath.Join(g.root, "..", filepath.Base(g.root)+"-gitmarks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		g.t.Fatal(err)
	}
	return dir
}

func (g *gitTool) ran() []string {
	entries, _ := os.ReadDir(g.marks())
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestGitToolsAreOffUnlessEnabledAndNeedAPrivateTemporaryDirectory(t *testing.T) {
	e := newEditEnv(t)
	for _, tool := range []string{ToolGitStatus, ToolGitDiff, ToolGitLog, ToolGitStage, ToolGitCommit} {
		if e.session.State(harness.CapabilityID(tool)) == harness.AccessAvailable {
			t.Errorf("%s is offered with Git off", tool)
		}
		if _, err := e.session.Envelope(harness.CapabilityID(tool), 64); err == nil {
			t.Errorf("%s was given an envelope with Git off", tool)
		}
	}
	if _, err := NewProvider(Config{Root: func(string) (string, error) { return "", nil }, Git: GitConfig{Enabled: true}}); err == nil {
		t.Fatal("Git was enabled without a temporary directory")
	}
	g := newGitTool(t)
	names := capabilityNames(g.provider.Manifest())
	for _, tool := range []string{ToolGitStatus, ToolGitDiff, ToolGitLog, ToolGitStage, ToolGitCommit} {
		if !contains(names, tool) || g.session.State(harness.CapabilityID(tool)) != harness.AccessAvailable {
			t.Errorf("%s is not offered with Git on", tool)
		}
	}
	if !reflect.DeepEqual(names, sortedCopy(append([]string(nil), names...))) {
		t.Fatalf("the manifest is not in a fixed order: %v", names)
	}
	for tool, want := range map[string]bool{ToolGitStage: true, ToolGitCommit: true, ToolGitStatus: false, ToolGitDiff: false, ToolGitLog: false} {
		if Mutating(harness.CapabilityID(tool)) != want {
			t.Errorf("Mutating(%s) = %v", tool, !want)
		}
	}
	// With no git on the search path the tools are offered no more.
	noGit := newEditEnv(t)
	noGit.open(Config{Redact: redact, Git: GitConfig{Enabled: true, SearchPath: []string{realTemp(t)}, TempRoot: t.TempDir()}})
	if noGit.session.State(ToolGitStatus) == harness.AccessAvailable {
		t.Fatal("the Git tools were offered with no git program")
	}
}

func TestStatusDiffAndLogAreReadOnlyAndNeedNoApproval(t *testing.T) {
	g := newGitTool(t)
	write(t, g.root, "main.go", "package main\n\nfunc main() {\n\tprintln(\"changed\")\n}\n")
	write(t, g.root, "fresh.txt", "new\n")
	g.run0("add", "fresh.txt")
	st := g.tool(false, ToolGitStatus, `{}`)
	var status struct {
		Branch  string `json:"branch"`
		Head    string `json:"head"`
		Omitted int    `json:"omitted_protected"`
		Entries []struct{ Kind, Path string }
	}
	if st.outcome != harness.OutcomeOK || json.Unmarshal([]byte(st.output), &status) != nil || status.Branch != "main" || status.Head != g.head() {
		t.Fatalf("status = %+v", st)
	}
	kinds := map[string]string{}
	for _, e := range status.Entries {
		kinds[e.Path] = e.Kind
	}
	if kinds["main.go"] != "modified" || kinds["fresh.txt"] != "added" || strings.Contains(st.output, ".env") || strings.Contains(st.output, "id_rsa") || status.Omitted < 2 {
		t.Fatalf("status = %s", st.output)
	}
	diff := g.tool(false, ToolGitDiff, `{}`)
	if diff.outcome != harness.OutcomeOK || !strings.Contains(diff.output, `"diff"`) || !strings.Contains(diff.output, "changed") || strings.Contains(diff.output, "fresh.txt") {
		t.Fatalf("unstaged diff = %+v", diff)
	}
	staged := g.tool(false, ToolGitDiff, `{"staged":true}`)
	if !strings.Contains(staged.output, "fresh.txt") || !strings.Contains(staged.output, `"staged":true`) {
		t.Fatalf("staged diff = %s", staged.output)
	}
	scoped := g.tool(false, ToolGitDiff, `{"path":"main.go"}`)
	if !strings.Contains(scoped.output, "main.go") {
		t.Fatalf("scoped diff = %s", scoped.output)
	}
	log := g.tool(false, ToolGitLog, `{"count":3}`)
	if log.outcome != harness.OutcomeOK || !strings.Contains(log.output, "initial commit") || !strings.Contains(log.output, "Test Person") || strings.Contains(log.output, "test@example.com") {
		t.Fatalf("log = %+v", log)
	}
	// None of it changed the repository.
	if g.run0("status", "--porcelain") == "" || g.head() != status.Head {
		t.Fatal("a read changed the repository")
	}
}

func TestGitReadArgumentsAreStrictAndPathsAreConfined(t *testing.T) {
	g := newGitTool(t)
	for name, tc := range map[string]struct {
		tool string
		args string
		want harness.Outcome
	}{
		"status with a field":     {ToolGitStatus, `{"all":true}`, harness.OutcomeFailed},
		"diff unknown field":      {ToolGitDiff, `{"staged":true,"stat":true}`, harness.OutcomeFailed},
		"diff staged not boolean": {ToolGitDiff, `{"staged":"yes"}`, harness.OutcomeFailed},
		"diff hidden path":        {ToolGitDiff, `{"path":".env"}`, harness.OutcomeDenied},
		"diff traversal":          {ToolGitDiff, `{"path":"../x"}`, harness.OutcomeDenied},
		"diff absolute":           {ToolGitDiff, `{"path":"/etc/passwd"}`, harness.OutcomeDenied},
		"diff git dir":            {ToolGitDiff, `{"path":".git/config"}`, harness.OutcomeDenied},
		"diff the root":           {ToolGitDiff, `{"path":"."}`, harness.OutcomeFailed},
		"log too many":            {ToolGitLog, `{"count":51}`, harness.OutcomeFailed},
		"log negative":            {ToolGitLog, `{"count":-1}`, harness.OutcomeFailed},
		"log hidden path":         {ToolGitLog, `{"path":".env"}`, harness.OutcomeDenied},
		"log unknown field":       {ToolGitLog, `{"count":1,"format":"%H"}`, harness.OutcomeFailed},
		"malformed":               {ToolGitStatus, `{`, harness.OutcomeFailed},
		"log default count":       {ToolGitLog, `{}`, harness.OutcomeOK},
		"diff default":            {ToolGitDiff, ``, harness.OutcomeOK},
	} {
		m := g.tool(false, tc.tool, tc.args)
		if tc.want == harness.OutcomeOK {
			if m.prepared != harness.OutcomeOK || m.outcome != harness.OutcomeOK {
				t.Errorf("%s: %+v", name, m)
			}
			continue
		}
		if m.prepared != tc.want || m.outcome != "" {
			t.Errorf("%s: prepared %s outcome %s", name, m.prepared, m.outcome)
		}
	}
}

func TestARepositoryThatIsNotAcceptableIsRefusedWithAFixedReason(t *testing.T) {
	g := newGitTool(t)
	check := func(label, reason string) {
		t.Helper()
		for _, tool := range []string{ToolGitStatus, ToolGitDiff, ToolGitLog, ToolGitStage, ToolGitCommit} {
			args := `{}`
			switch tool {
			case ToolGitStage:
				args = `{"paths":["main.go"]}`
			case ToolGitCommit:
				args = `{"message":"x"}`
			}
			m := g.tool(true, tool, args)
			if m.prepared == harness.OutcomeOK && m.outcome == harness.OutcomeOK {
				t.Errorf("%s: %s worked against a repository that should be refused", label, tool)
				continue
			}
			if tool == ToolGitStatus && m.reason != reason {
				t.Errorf("%s: reason %q, want %q", label, m.reason, reason)
			}
		}
	}
	trap := fmt.Sprintf("sh -c 'touch %s/fsmonitor && cat' --", g.marks())
	f, _ := os.OpenFile(filepath.Join(g.root, ".git", "config"), os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString("[core]\n\tfsmonitor = " + trap + "\n")
	f.Close()
	check("a monitor", "config_risky")
	if len(g.ran()) != 0 {
		t.Fatalf("something ran: %v", g.ran())
	}
	// The same project without a repository at all.
	plain := newEditEnv(t)
	plain.open(Config{Redact: redact, Git: GitConfig{Enabled: true, SearchPath: []string{g.gitDir}, Home: g.home, TempRoot: g.temp}})
	if m := plain.run(ToolGitStatus, `{}`, false); m.prepared != harness.OutcomeFailed || m.reason != "not_a_repository" {
		t.Fatalf("no repository: %+v", m)
	}
}

func TestStagingIsPreviewedExactlyAndOnlyTheNamedFilesAreStagedAfterApproval(t *testing.T) {
	g := newGitTool(t)
	write(t, g.root, "main.go", "package main\n\nfunc main() {\n\tprintln(\"changed\")\n}\n")
	write(t, g.root, "fresh.txt", "line one\nline two\n")
	write(t, g.root, "other.txt", "not named\n")
	if err := os.WriteFile(filepath.Join(g.root, "data.bin"), []byte("\x00\x01\x02binary"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(g.root, "docs/readme.md")); err != nil {
		t.Fatal(err)
	}
	before := g.run0("status", "--porcelain")
	m := g.tool(false, ToolGitStage, `{"paths":["main.go","fresh.txt","data.bin","docs/readme.md"]}`)
	if m.prepared != harness.OutcomeOK || m.action.Summary != "stage 4 files" || len(m.action.Escalate) != 0 {
		t.Fatalf("%+v", m)
	}
	if g.run0("status", "--porcelain") != before {
		t.Fatal("preparing changed the index")
	}
	if !reflect.DeepEqual(m.action.Paths, []string{"data.bin", "docs/readme.md", "fresh.txt", "main.go"}) {
		t.Fatalf("paths = %v", m.action.Paths)
	}
	for _, want := range []string{"STAGE FILES", "nothing is committed, pushed or deleted", "branch   main", "base     " + g.head()[:12], "modified     main.go", "new file     fresh.txt  (18 bytes)",
		"deleted      docs/readme.md", "new file     data.bin  (9 bytes)", "+\tprintln(\"changed\")", "-\tprintln(\"hello\")", "+line one", "+line two", "(new file, 9 bytes, not shown as text)"} {
		if !strings.Contains(m.action.Preview, want) {
			t.Errorf("the preview lacks %q:\n%s", want, m.action.Preview)
		}
	}
	if strings.Contains(m.action.Preview, "other.txt") {
		t.Fatal("the preview shows a file that was not named")
	}
	// Without the proof nothing is staged; with it, exactly the named files are.
	if denied := g.tool(false, ToolGitStage, `{"paths":["main.go"]}`); denied.outcome != harness.OutcomeDenied {
		t.Fatalf("without a proof: %+v", denied)
	}
	if got := g.run0("diff", "--cached", "--name-only"); got != "" {
		t.Fatalf("an unapproved stage staged %q", got)
	}
	approved := g.tool(true, ToolGitStage, `{"paths":["main.go","fresh.txt","data.bin","docs/readme.md"]}`)
	if approved.outcome != harness.OutcomeOK || !strings.Contains(approved.output, "staged 4 files on main (nothing is committed)") {
		t.Fatalf("%+v", approved)
	}
	if got := g.run0("diff", "--cached", "--name-only"); got != "data.bin\ndocs/readme.md\nfresh.txt\nmain.go" {
		t.Fatalf("staged = %q", got)
	}
	if status := g.run0("status", "--porcelain"); !strings.Contains(status, "?? other.txt") {
		t.Fatalf("an unnamed file was affected: %q", status)
	}
	if g.head() != g.run0("rev-parse", "HEAD") || strings.Count(g.run0("log", "--oneline"), "\n") != 0 {
		t.Fatal("staging made a commit")
	}
}

func TestStagingRefusesEverythingThatCouldReachWhatItShouldNot(t *testing.T) {
	g := newGitTool(t)
	write(t, g.root, "fresh.txt", "new\n")
	write(t, g.root, "inner/x.txt", "x\n")
	g.runIn(filepath.Join(g.root, "inner"), "init", "-q")
	if err := os.Symlink("main.go", filepath.Join(g.root, "alias.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(g.root, "huge.dat"), make([]byte, maxStageFileBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	many := make([]string, MaxGitStagePaths+1)
	for i := range many {
		many[i] = fmt.Sprintf("m%02d.txt", i)
	}
	manyJSON, _ := json.Marshal(map[string]any{"paths": many})
	for name, tc := range map[string]struct {
		args   string
		want   harness.Outcome
		reason string
	}{
		"no paths":            {`{"paths":[]}`, harness.OutcomeFailed, "invalid_arguments"},
		"too many":            {string(manyJSON), harness.OutcomeFailed, "invalid_arguments"},
		"a duplicate":         {`{"paths":["fresh.txt","fresh.txt"]}`, harness.OutcomeFailed, "invalid_arguments"},
		"unknown field":       {`{"paths":["fresh.txt"],"all":true}`, harness.OutcomeFailed, "invalid_arguments"},
		"a hidden file":       {`{"paths":["fresh.txt",".env"]}`, harness.OutcomeDenied, ""},
		"a credential file":   {`{"paths":["id_rsa"]}`, harness.OutcomeDenied, ""},
		"a key file":          {`{"paths":["server.pem"]}`, harness.OutcomeDenied, ""},
		"traversal":           {`{"paths":["../x"]}`, harness.OutcomeDenied, ""},
		"absolute":            {`{"paths":["/etc/hosts"]}`, harness.OutcomeDenied, ""},
		"inside .git":         {`{"paths":[".git/config"]}`, harness.OutcomeDenied, ""},
		"the root":            {`{"paths":["."]}`, harness.OutcomeFailed, ""},
		"a directory":         {`{"paths":["internal"]}`, harness.OutcomeFailed, "not_a_file"},
		"a link":              {`{"paths":["alias.txt"]}`, harness.OutcomeDenied, ""},
		"a nested repository": {`{"paths":["inner/x.txt"]}`, harness.OutcomeFailed, "nested_repository"},
		"too large":           {`{"paths":["huge.dat"]}`, harness.OutcomeFailed, "too_large"},
		"nothing to stage":    {`{"paths":["main.go"]}`, harness.OutcomeFailed, "nothing_to_stage"},
		"a missing file":      {`{"paths":["never-existed.txt"]}`, harness.OutcomeFailed, "nothing_to_stage"},
		"a magic prefix":      {`{"paths":[":(top)fresh.txt"]}`, harness.OutcomeFailed, ""},
		"a pattern":           {`{"paths":["*.txt"]}`, harness.OutcomeFailed, "nothing_to_stage"},
	} {
		m := g.tool(true, ToolGitStage, tc.args)
		if m.prepared != tc.want || (tc.reason != "" && m.reason != tc.reason) || m.outcome != "" {
			t.Errorf("%s: prepared %s reason %q outcome %s", name, m.prepared, m.reason, m.outcome)
		}
	}
	if got := g.run0("diff", "--cached", "--name-only"); got != "" {
		t.Fatalf("a refused call staged %q", got)
	}
	// A path that was already staged and not changed since has nothing more to stage.
	g.run0("add", "fresh.txt")
	if m := g.tool(true, ToolGitStage, `{"paths":["fresh.txt"]}`); m.prepared != harness.OutcomeFailed || m.reason != "nothing_to_stage" {
		t.Fatalf("an already staged file: %+v", m)
	}
}

func (g *gitTool) runIn(dir string, args ...string) {
	g.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir, cmd.Env = dir, g.env
	if out, err := cmd.CombinedOutput(); err != nil {
		g.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestAStageIsStaleIfTheFileChangedOrTheBranchMovedAfterReview(t *testing.T) {
	g := newGitTool(t)
	write(t, g.root, "fresh.txt", "reviewed\n")
	action := g.prepare(ToolGitStage, `{"paths":["fresh.txt"]}`)
	if action.Outcome != harness.OutcomeOK {
		t.Fatalf("%+v", action)
	}
	write(t, g.root, "fresh.txt", "changed after review\n")
	answer, err := g.session.Invoke(harnessproof.Mint(context.Background(), action.Digest), action)
	if err != nil || answer.Outcome != harness.OutcomeStale || g.run0("diff", "--cached", "--name-only") != "" {
		t.Fatalf("a changed file was staged: %+v %v", answer, err)
	}
	// The branch moving is stale too: the review was of the change on top of another commit.
	write(t, g.root, "fresh.txt", "reviewed\n")
	second := g.prepare(ToolGitStage, `{"paths":["fresh.txt"]}`)
	write(t, g.root, "other.txt", "x\n")
	g.run0("add", "other.txt")
	g.run0("commit", "-q", "-m", "someone else committed")
	answer, err = g.session.Invoke(harnessproof.Mint(context.Background(), second.Digest), second)
	if err != nil || answer.Outcome != harness.OutcomeStale || g.run0("diff", "--cached", "--name-only") != "" {
		t.Fatalf("a moved branch: %+v %v", answer, err)
	}
}

func TestAStagePreviewTooLargeToReadIsNotOffered(t *testing.T) {
	g := newGitTool(t)
	var b strings.Builder
	for i := 0; i < 4000; i++ {
		fmt.Fprintf(&b, "line %05d of a long file that will change\n", i)
	}
	write(t, g.root, "long.txt", b.String())
	g.run0("add", "long.txt")
	g.run0("commit", "-q", "-m", "long")
	write(t, g.root, "long.txt", strings.ReplaceAll(b.String(), "will change", "HAS CHANGED"))
	if m := g.tool(false, ToolGitStage, `{"paths":["long.txt"]}`); m.prepared != harness.OutcomeFailed || m.reason != "change_too_large" {
		t.Fatalf("%+v", m)
	}
	if g.run0("diff", "--cached", "--name-only") != "" {
		t.Fatal("something was staged")
	}
}

func TestCommitIsPreviewedWithTheMessageTheAuthorAndTheStagedDiffAndRecordsExactlyThat(t *testing.T) {
	g := newGitTool(t)
	write(t, g.root, "main.go", "package main\n\nfunc main() {\n\tprintln(\"changed\")\n}\n")
	write(t, g.root, "left-alone.txt", "unstaged\n")
	g.run0("add", "main.go")
	write(t, g.root, "docs/readme.md", "# Docs\nan unstaged edit to a tracked file\n")
	head := g.head()
	m := g.tool(false, ToolGitCommit, `{"message":"Change main\n\nA longer body line."}`)
	if m.prepared != harness.OutcomeOK || !strings.HasPrefix(m.action.Summary, "commit 1 file: Change main") || len(m.action.Escalate) != 0 {
		t.Fatalf("%+v", m)
	}
	for _, want := range []string{"COMMIT", "repository hooks are not run", "branch    main", "parent    " + head[:12], "author    Test Person <test@example.com>", "files     1",
		"| Change main", "| A longer body line.", "main.go |", "+\tprintln(\"changed\")"} {
		if !strings.Contains(m.action.Preview, want) {
			t.Errorf("the preview lacks %q:\n%s", want, m.action.Preview)
		}
	}
	if strings.Contains(m.action.Preview, "left-alone") || strings.Contains(m.action.Preview, "unstaged edit") {
		t.Fatalf("the preview shows what is not staged:\n%s", m.action.Preview)
	}
	if g.head() != head {
		t.Fatal("preparing committed")
	}
	if denied := g.tool(false, ToolGitCommit, `{"message":"Change main\n\nA longer body line."}`); denied.outcome != harness.OutcomeDenied || g.head() != head {
		t.Fatalf("without a proof: %+v", denied)
	}
	done := g.tool(true, ToolGitCommit, `{"message":"Change main\n\nA longer body line."}`)
	if done.outcome != harness.OutcomeOK || !strings.Contains(done.output, "on main: Change main (1 file)") || strings.Contains(done.output, "WARNING") || len(done.action.Escalate) != 0 {
		t.Fatalf("%+v", done)
	}
	if g.run0("log", "-1", "--format=%P") != head || g.run0("show", "--name-only", "--format=", "HEAD") != "main.go" || g.run0("log", "-1", "--format=%an <%ae>") != "Test Person <test@example.com>" {
		t.Fatal("the commit is not what was approved")
	}
	if status := g.run0("status", "--porcelain"); !strings.Contains(status, "M docs/readme.md") || !strings.Contains(status, "?? left-alone.txt") {
		t.Fatalf("the commit took more than the index: %q", status)
	}
}

func TestCommitRefusesWhenThereIsNothingSafeToCommit(t *testing.T) {
	g := newGitTool(t)
	// In order: each case builds on the repository state the one before left behind.
	for _, tc := range []struct {
		name   string
		setup  func()
		args   string
		reason string
	}{
		{"nothing staged", func() {}, `{"message":"x"}`, "nothing_staged"},
		{"an empty message", func() { write(t, g.root, "a.txt", "a\n"); g.run0("add", "a.txt") }, `{"message":""}`, "invalid_arguments"},
		{"a control character", func() {}, `{"message":"bad \u001b[31m message"}`, "invalid_arguments"},
		{"an unknown field", func() {}, `{"message":"x","amend":true}`, "invalid_arguments"},
		{"a forced-in secret", func() { write(t, g.root, ".env", "T=1\n"); g.run0("add", "-f", ".env") }, `{"message":"x"}`, "protected_path"},
		{"a gitlink in the index", func() {
			g.run0("reset", "-q", ".env")
			g.run0("update-index", "--add", "--cacheinfo", "160000,"+g.head()+",sub")
		}, `{"message":"x"}`, "submodule"},
	} {
		tc.setup()
		m := g.tool(true, ToolGitCommit, tc.args)
		if m.prepared != harness.OutcomeFailed || m.reason != tc.reason || m.outcome != "" {
			t.Errorf("%s: prepared %s reason %q outcome %s", tc.name, m.prepared, m.reason, m.outcome)
		}
	}
	if strings.Count(g.run0("log", "--oneline"), "\n") != 0 {
		t.Fatal("a refused commit made a commit")
	}
	// No identity is configured: nothing is invented.
	g.run0("rm", "-q", "--cached", "sub")
	g.run0("config", "--unset", "user.name")
	g.run0("config", "--unset", "user.email")
	if m := g.tool(true, ToolGitCommit, `{"message":"x"}`); m.prepared != harness.OutcomeFailed || m.reason != "no_identity" {
		t.Fatalf("no identity: %+v", m)
	}
}

func TestACommitIsStaleIfTheIndexOrTheBranchChangedAfterReview(t *testing.T) {
	g := newGitTool(t)
	write(t, g.root, "a.txt", "a\n")
	g.run0("add", "a.txt")
	action := g.prepare(ToolGitCommit, `{"message":"reviewed"}`)
	write(t, g.root, "b.txt", "b\n")
	g.run0("add", "b.txt") // another file slipped into the index after the review
	head := g.head()
	answer, err := g.session.Invoke(harnessproof.Mint(context.Background(), action.Digest), action)
	if err != nil || answer.Outcome != harness.OutcomeStale || g.head() != head {
		t.Fatalf("a changed index was committed: %+v %v", answer, err)
	}
	g.run0("reset", "-q", "b.txt")
	second := g.prepare(ToolGitCommit, `{"message":"reviewed"}`)
	g.run0("commit", "-q", "-m", "someone else got there first")
	g.run0("add", "a.txt") // nothing to add: a.txt was committed; restage something new
	write(t, g.root, "a.txt", "a, edited\n")
	g.run0("add", "a.txt")
	answer, err = g.session.Invoke(harnessproof.Mint(context.Background(), second.Digest), second)
	if err != nil || answer.Outcome != harness.OutcomeStale {
		t.Fatalf("a moved branch: %+v %v", answer, err)
	}
}

// What can be committed in the project's name is what a person was shown, whatever the
// repository's own hooks and filters say.
func TestHooksAndFiltersNamedByTheRepositoryNeverRunDuringAStageAndACommit(t *testing.T) {
	g := newGitTool(t)
	hooks := filepath.Join(g.root, ".git", "hooks")
	os.MkdirAll(hooks, 0o755)
	for _, hook := range []string{"pre-commit", "commit-msg", "post-commit", "prepare-commit-msg", "post-checkout"} {
		os.WriteFile(filepath.Join(hooks, hook), []byte(fmt.Sprintf("#!/bin/sh\ntouch %s/hook-%s\n", g.marks(), hook)), 0o755)
	}
	write(t, g.root, ".gitattributes", "*.txt diff=evil filter=undefined-in-config\n")
	write(t, g.root, "note.txt", "x\n")
	if m := g.tool(true, ToolGitStage, `{"paths":["note.txt"]}`); m.outcome != harness.OutcomeOK {
		t.Fatalf("%+v", m)
	}
	if m := g.tool(true, ToolGitCommit, `{"message":"with hostile hooks around"}`); m.outcome != harness.OutcomeOK || strings.Contains(m.output, "WARNING") {
		t.Fatalf("%+v", m)
	}
	if got := g.ran(); len(got) != 0 {
		t.Fatalf("the repository's hooks ran: %v", got)
	}
}

func TestControlPlaneFilesInALargeCommitAreAlwaysAmongTheReportedPaths(t *testing.T) {
	g := newGitTool(t)
	for i := 0; i < 40; i++ {
		write(t, g.root, fmt.Sprintf("aaa/f%02d.txt", i), "x\n")
	}
	write(t, g.root, "zz-last/go.mod", "module x\n")
	write(t, g.root, "zz-last/ci/pipeline.yml", "x\n")
	g.run0("add", "aaa", "zz-last")
	m := g.tool(false, ToolGitCommit, `{"message":"a large change"}`)
	if m.prepared != harness.OutcomeOK || len(m.action.Paths) != harness.MaxActionPaths {
		t.Fatalf("%+v", m)
	}
	if !contains(m.action.Paths, "zz-last/go.mod") || !contains(m.action.Paths[:3], "zz-last/go.mod") || !contains(m.action.Escalate, "large_change") {
		t.Fatalf("a control-plane file was not reported to policy: %v (escalate %v)", m.action.Paths, m.action.Escalate)
	}
	policy := ProjectPolicy()
	m.action.Capability = ToolGitCommit
	if got := policy.Decide(context.Background(), harnessrunOwner(), m.action); got != decisionAskStrict {
		t.Fatalf("a commit of manifests decided %v", got)
	}
	if reasons := policy.Reasons(m.action); !contains(reasons, "dependency_manifest") || !contains(reasons, "large_change") {
		t.Fatalf("reasons = %v", reasons)
	}
}

func TestACommitWithATruncatedDiffAsksWithExtraFriction(t *testing.T) {
	g := newGitTool(t)
	var b strings.Builder
	for i := 0; i < 3000; i++ {
		fmt.Fprintf(&b, "a line of generated content number %d\n", i)
	}
	write(t, g.root, "generated.txt", b.String())
	g.run0("add", "generated.txt")
	m := g.tool(false, ToolGitCommit, `{"message":"big generated file"}`)
	if m.prepared != harness.OutcomeOK || !contains(m.action.Escalate, "large_change") || !strings.Contains(m.action.Preview, "review the whole staged change with your own tools") {
		t.Fatalf("%+v", m.action.Escalate)
	}
	if len(m.action.Preview) > harness.MaxPreviewBytes {
		t.Fatalf("the preview is %d bytes", len(m.action.Preview))
	}
}

func TestGitPolicyTiers(t *testing.T) {
	policy := ProjectPolicy()
	action := func(capability string, paths ...string) harness.PreparedAction {
		return harness.PreparedAction{Envelope: harness.Envelope{Capability: harness.CapabilityID(capability)}, Outcome: harness.OutcomeOK, Digest: "sha256:" + strings.Repeat("a", 64), Paths: paths}
	}
	ctx := context.Background()
	for tool, want := range map[string]harnessrun.Decision{ToolGitStatus: decisionAllow, ToolGitDiff: decisionAllow, ToolGitLog: decisionAllow, ToolGitStage: decisionAsk, ToolGitCommit: decisionAsk} {
		if got := policy.Decide(ctx, harnessrunOwner(), action(tool, "main.go")); got != want {
			t.Errorf("%s: %v, want %v", tool, got, want)
		}
	}
	if got := policy.Decide(ctx, harnessrunOwner(), action(ToolGitStage, "go.mod")); got != decisionAskStrict {
		t.Errorf("staging a manifest = %v", got)
	}
	loose := NewPolicy(map[harness.CapabilityID]Tier{ToolGitStage: TierAllow, ToolGitCommit: TierAllow})
	for _, tool := range []string{ToolGitStage, ToolGitCommit} {
		if got := loose.Decide(ctx, harnessrunOwner(), action(tool, "main.go")); got == decisionAllow || got == decisionDeny {
			t.Errorf("%s with an allow table = %v", tool, got)
		}
	}
	for _, p := range []Policy{ReadOnlyPolicy(), EditPolicy()} {
		for _, tool := range []string{ToolGitStatus, ToolGitStage, ToolGitCommit} {
			if got := p.Decide(ctx, harnessrunOwner(), action(tool, "main.go")); got != decisionDeny {
				t.Errorf("a policy without Git decided %v for %s", got, tool)
			}
		}
	}
}

// The whole loop: look, change, stage, commit, each change reviewed and approved.
func TestReadChangeStageAndCommitThroughApprovals(t *testing.T) {
	g := newGitTool(t)
	l := newEditLoopOn(t, g.editEnv, []harnesstest.Step{
		{ToolID: ToolGitStatus, Arguments: `{}`},
		{ToolID: ToolEditFile, Arguments: `{"path":"main.go","edits":[{"old_text":"hello","new_text":"goodbye"}]}`},
		{ToolID: ToolGitStage, Arguments: `{"paths":["main.go"]}`},
		{ToolID: ToolGitCommit, Arguments: `{"message":"Say goodbye"}`},
		{ToolID: ToolGitLog, Arguments: `{"count":2}`},
		{Text: "committed"},
	}, ProjectPolicy())
	started := l.start("key-00000001")
	approve := func(wantTool string) harnessapproval.Record {
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			status, _ := l.manager.Status(context.Background(), loopOwner, started.ID)
			if status.State == harnessrun.StateNeedsAttention && status.Attention != nil {
				record, _ := l.approvals.Get(context.Background(), status.Attention.ApprovalID)
				if record.State == harnessapproval.StatePending {
					if record.Tool != wantTool {
						t.Fatalf("the run asked for %s, want %s", record.Tool, wantTool)
					}
					l.approve(record)
					return record
				}
			}
		}
		t.Fatalf("no approval for %s", wantTool)
		return harnessapproval.Record{}
	}
	approve(ToolEditFile)
	approve(ToolGitStage)
	commit := approve(ToolGitCommit)
	done := l.wait(started.ID, harnessrun.StateCompleted)
	text := l.runText(started.ID)
	if done.Usage.ToolCalls != 5 || !strings.Contains(text, "committed ") || !strings.Contains(text, "Say goodbye") || !strings.Contains(commit.Preview, "| Say goodbye") {
		t.Fatalf("done = %+v\n%s", done, text)
	}
	if g.run0("log", "-1", "--format=%s") != "Say goodbye" || g.run0("show", "--name-only", "--format=", "HEAD") != "main.go" || strings.Contains(g.run0("status", "--porcelain"), "main.go") {
		t.Fatalf("the repository is not as approved:\n%s", g.run0("status", "--porcelain"))
	}
	if n, err := l.approvals.VerifyAudit(context.Background()); err != nil || n != 12 { // three approvals, four lines each
		t.Fatalf("audit = %d %v", n, err)
	}
}

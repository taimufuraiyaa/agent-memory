package harnesstools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessgit"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessproof"
)

// These tests hold the Git tools to what an approval promises: the digest names everything
// that was reviewed, a handle works for the action it was made for and only while the
// repository is as it was, and what comes back is bounded, marked and redacted.

// sess is a bare session over the project, for calling the planning and fitting steps directly.
func (g *gitTool) sess() *session {
	return &session{provider: g.provider, scope: harness.Scope{Workspace: workspace, Run: "run-t", Generation: 1}, pending: map[string]call{}}
}

func (g *gitTool) opened(s *session) *harnessgit.Git {
	g.t.Helper()
	opened, err := s.openGit(context.Background(), g.root)
	if err != nil {
		g.t.Fatal(err)
	}
	return opened
}

// moveHead points the current branch at a new commit with the same tree, so what is staged
// is exactly as before but the parent is not.
func (g *gitTool) moveHead() {
	g.t.Helper()
	commit := g.run0("commit-tree", g.run0("rev-parse", "HEAD^{tree}"), "-p", g.head(), "-m", "same tree")
	g.run0("update-ref", "HEAD", commit)
}

func (g *gitTool) breakConfig() {
	g.t.Helper()
	trap := fmt.Sprintf("sh -c 'touch %s/fsmonitor && cat' --", g.marks())
	f, err := os.OpenFile(filepath.Join(g.root, ".git", "config"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		g.t.Fatal(err)
	}
	f.WriteString("[core]\n\tfsmonitor = " + trap + "\n")
	f.Close()
}

func TestEveryGitLayerErrorBecomesATypedOutcomeWithAFixedReason(t *testing.T) {
	for _, tc := range []struct {
		err     error
		outcome harness.Outcome
		reason  string
	}{
		{harnessgit.ErrNoGit, harness.OutcomeUnavailable, "git_not_found"},
		{fmt.Errorf("looking up: %w", harnessgit.ErrNoGit), harness.OutcomeUnavailable, "git_not_found"},
		{harnessgit.ErrNothingStaged, harness.OutcomeFailed, "nothing_staged"},
		{harnessgit.ErrProtectedPath, harness.OutcomeFailed, "protected_path"},
		{harnessgit.ErrSubmodule, harness.OutcomeFailed, "submodule"},
		{harnessgit.ErrNoIdentity, harness.OutcomeFailed, "no_identity"},
		{harnessgit.ErrUnborn, harness.OutcomeFailed, "unborn"},
		{harnessgit.ErrInvalid, harness.OutcomeFailed, "invalid_arguments"},
		{&harnessgit.Refusal{Reason: "config_risky", Setting: "core.fsmonitor"}, harness.OutcomeFailed, "config_risky"},
		{context.DeadlineExceeded, harness.OutcomeTimeout, ""},
		{context.Canceled, harness.OutcomeCancelled, ""},
		{harnessgit.ErrFailed, harness.OutcomeFailed, "git_failed"},
		{errors.New("anything else"), harness.OutcomeFailed, "git_failed"},
	} {
		if outcome, reason := gitFailure(tc.err); outcome != tc.outcome || reason != tc.reason {
			t.Errorf("%v: %s %q, want %s %q", tc.err, outcome, reason, tc.outcome, tc.reason)
		}
	}
}

func TestAGitToolIsNotPreparedWhenGitIsNotEnabled(t *testing.T) {
	off := newEditEnv(t)
	s := &session{provider: off.provider, pending: map[string]call{}}
	for _, tool := range []string{ToolGitStatus, ToolGitDiff, ToolGitLog, ToolGitStage, ToolGitCommit, "git_push"} {
		got := s.prepareGit(context.Background(), harness.ToolRequest{ToolID: tool, Arguments: []byte(`{}`)}, off.root, harness.PreparedAction{})
		if got.Outcome != harness.OutcomeUnsupported || got.Digest != "" {
			t.Errorf("%s with Git off: %+v", tool, got)
		}
	}
	on := newGitTool(t)
	got := on.sess().prepareGit(context.Background(), harness.ToolRequest{ToolID: "git_push", Arguments: []byte(`{}`)}, on.root, harness.PreparedAction{})
	if got.Outcome != harness.OutcomeUnsupported {
		t.Fatalf("a Git tool nobody defined: %+v", got)
	}
}

func TestPendingGitWritesAreBoundedLikeEveryOtherChange(t *testing.T) {
	g := newGitTool(t)
	for i := 0; i < maxPendingMutations; i++ {
		write(t, g.root, fmt.Sprintf("p%d.txt", i), "x\n")
		if a := g.prepare(ToolGitStage, fmt.Sprintf(`{"paths":["p%d.txt"]}`, i)); a.Outcome != harness.OutcomeOK {
			t.Fatalf("stage %d: %+v", i, a)
		}
	}
	write(t, g.root, "extra.txt", "x\n")
	if a := g.prepare(ToolGitStage, `{"paths":["extra.txt"]}`); a.Outcome != harness.OutcomeFailed || a.Reason != "busy" {
		t.Fatalf("one more pending change: %+v", a)
	}
	if a := g.prepare(ToolGitStatus, `{}`); a.Outcome != harness.OutcomeOK {
		t.Fatalf("a read must not be held up by pending writes: %+v", a)
	}
}

func TestAConflictedPathIsNotStaged(t *testing.T) {
	g := newGitTool(t)
	g.run0("checkout", "-q", "-b", "side")
	write(t, g.root, "main.go", "package main\n\nfunc main() {\n\tprintln(\"side\")\n}\n")
	g.run0("commit", "-q", "-a", "-m", "side")
	g.run0("checkout", "-q", "main")
	write(t, g.root, "main.go", "package main\n\nfunc main() {\n\tprintln(\"main\")\n}\n")
	g.run0("commit", "-q", "-a", "-m", "main")
	merge := exec.Command("git", "merge", "--no-edit", "side")
	merge.Dir, merge.Env = g.root, g.env
	if err := merge.Run(); err == nil {
		t.Fatal("the merge was meant to conflict")
	}
	if !strings.Contains(g.run0("status", "--porcelain"), "UU main.go") {
		t.Fatalf("no conflict: %s", g.run0("status", "--porcelain"))
	}
	if m := g.tool(true, ToolGitStage, `{"paths":["main.go"]}`); m.prepared != harness.OutcomeFailed || m.reason != "conflicted" {
		t.Fatalf("%+v", m)
	}
	if !strings.Contains(g.run0("status", "--porcelain"), "UU main.go") {
		t.Fatal("the conflict was resolved by staging")
	}
}

func TestAStagePreviewShowsInvisibleCharactersAndIsBounded(t *testing.T) {
	g := newGitTool(t)
	// A word joiner is invisible in a diff and is not one the Git layer already marks.
	write(t, g.root, "main.go", "package main\n\nfunc main() {\n\tprintln(\"hel⁠lo\")\n}\n")
	m := g.tool(false, ToolGitStage, `{"paths":["main.go"]}`)
	if m.prepared != harness.OutcomeOK || strings.Contains(m.action.Preview, "⁠") || !strings.Contains(m.action.Preview, "hel⟨U+2060⟩lo") {
		t.Fatalf("the preview hides an invisible character:\n%s", m.action.Preview)
	}
	g.run0("checkout", "--", "main.go")

	// A diff that is not cut at the output bound but is still too large to review.
	var b strings.Builder
	for i := 0; i < 1000; i++ {
		fmt.Fprintf(&b, "line %05d of a long file that will change\n", i)
	}
	write(t, g.root, "long.txt", b.String())
	g.run0("add", "long.txt")
	g.run0("commit", "-q", "-m", "long")
	write(t, g.root, "long.txt", strings.ReplaceAll(b.String(), "will change", "HAS CHANGED"))
	if m := g.tool(false, ToolGitStage, `{"paths":["long.txt"]}`); m.prepared != harness.OutcomeFailed || m.reason != "change_too_large" {
		t.Fatalf("a large diff was offered: %+v", m)
	}
	g.run0("checkout", "--", "long.txt")

	// New files are shown too, and they count against the same bound.
	var names []string
	for i := 0; i < 10; i++ {
		name := fmt.Sprintf("wide%d.txt", i)
		write(t, g.root, name, strings.Repeat("y", 60000)+"\n")
		names = append(names, name)
	}
	raw, _ := json.Marshal(map[string]any{"paths": names})
	if m := g.tool(false, ToolGitStage, string(raw)); m.prepared != harness.OutcomeFailed || m.reason != "change_too_large" {
		t.Fatalf("large new files were offered: %+v", m)
	}
}

func TestStagingManyFilesAsksWithExtraFriction(t *testing.T) {
	g := newGitTool(t)
	var paths []string
	for i := 0; i < manyFilesThreshold+1; i++ {
		name := fmt.Sprintf("n%02d.txt", i)
		write(t, g.root, name, "x\n")
		paths = append(paths, name)
	}
	policy := ProjectPolicy()
	few, _ := json.Marshal(map[string]any{"paths": paths[:manyFilesThreshold]})
	m := g.tool(false, ToolGitStage, string(few))
	m.action.Capability = ToolGitStage
	if m.prepared != harness.OutcomeOK || len(m.action.Escalate) != 0 || policy.Decide(context.Background(), harnessrunOwner(), m.action) != decisionAsk {
		t.Fatalf("%d files: %+v", manyFilesThreshold, m.action.Escalate)
	}
	many, _ := json.Marshal(map[string]any{"paths": paths})
	m = g.tool(false, ToolGitStage, string(many))
	m.action.Capability = ToolGitStage
	if m.prepared != harness.OutcomeOK || !contains(m.action.Escalate, "large_change") || policy.Decide(context.Background(), harnessrunOwner(), m.action) != decisionAskStrict {
		t.Fatalf("%d files: %+v", manyFilesThreshold+1, m.action.Escalate)
	}
}

func TestTheFirstCommitOfANewRepositoryCanBeStagedAndCommitted(t *testing.T) {
	g := newGitToolWith(t, false)
	stage := g.tool(false, ToolGitStage, `{"paths":["main.go"]}`)
	if stage.prepared != harness.OutcomeOK || !strings.Contains(stage.action.Preview, "base     no commits yet") {
		t.Fatalf("%+v", stage)
	}
	if done := g.tool(true, ToolGitStage, `{"paths":["main.go"]}`); done.outcome != harness.OutcomeOK {
		t.Fatalf("%+v", done)
	}
	commit := g.tool(false, ToolGitCommit, `{"message":"first"}`)
	if commit.prepared != harness.OutcomeOK || !strings.Contains(commit.action.Preview, "parent    none: this is the first commit") {
		t.Fatalf("%+v", commit)
	}
	done := g.tool(true, ToolGitCommit, `{"message":"first"}`)
	if done.outcome != harness.OutcomeOK || strings.Contains(done.output, "WARNING") || !strings.Contains(done.output, "(1 file)") {
		t.Fatalf("%+v", done)
	}
	if g.run0("log", "--format=%s") != "first" || len(strings.Fields(g.run0("rev-list", "--parents", "-n", "1", "HEAD"))) != 1 {
		t.Fatal("the first commit is not a root commit named first")
	}
}

func TestEveryPartOfWhatWasReviewedIsPartOfTheDigest(t *testing.T) {
	g := newGitTool(t)
	ctx := context.Background()
	s := g.sess()
	write(t, g.root, "fresh.txt", "new\n")

	stagePlan := func(open *harnessgit.Git) *stagePlan {
		t.Helper()
		plan, outcome, reason := s.planStage(ctx, open, g.root, gitStageArgs{Paths: []string{"fresh.txt"}})
		if outcome != harness.OutcomeOK {
			t.Fatalf("%s %s", outcome, reason)
		}
		return plan
	}
	base := stagePlan(g.opened(s))
	if again := stagePlan(g.opened(s)); again.digest != base.digest {
		t.Fatal("the same stage has two digests")
	}
	other := g.opened(s)
	other.ProgramIdentity = "a different git program"
	if stagePlan(other).digest == base.digest {
		t.Error("the Git program is not part of a stage's digest")
	}
	write(t, g.root, "fresh.txt", "new, then changed\n")
	if stagePlan(g.opened(s)).digest == base.digest {
		t.Error("a file's content is not part of a stage's digest")
	}
	g.run0("add", "fresh.txt")

	commitPlan := func(open *harnessgit.Git, message string) *commitPlan {
		t.Helper()
		plan, outcome, reason := s.planCommit(ctx, open, g.root, message)
		if outcome != harness.OutcomeOK {
			t.Fatalf("%s %s", outcome, reason)
		}
		return plan
	}
	first := commitPlan(g.opened(s), "a message")
	if commitPlan(g.opened(s), "a message").digest != first.digest {
		t.Fatal("the same commit has two digests")
	}
	if commitPlan(g.opened(s), "another message").digest == first.digest {
		t.Error("the message is not part of a commit's digest")
	}
	other = g.opened(s)
	other.ProgramIdentity = "a different git program"
	if commitPlan(other, "a message").digest == first.digest {
		t.Error("the Git program is not part of a commit's digest")
	}
	g.moveHead()
	moved := commitPlan(g.opened(s), "a message")
	if moved.fingerprint != first.fingerprint {
		t.Fatalf("the staged change should be the same: %s vs %s", moved.fingerprint, first.fingerprint)
	}
	if moved.digest == first.digest {
		t.Error("the parent is not part of a commit's digest")
	}
	g.run0("config", "user.name", "Someone Else")
	if commitPlan(g.opened(s), "a message").digest == moved.digest {
		t.Error("the author is not part of a commit's digest")
	}
	g.run0("config", "user.name", "Test Person")
	g.run0("config", "user.email", "someone@else.example")
	if commitPlan(g.opened(s), "a message").digest == moved.digest {
		t.Error("the author's address is not part of a commit's digest")
	}
}

func TestTheMessageThatWasReviewedIsTheMessageThatIsRecorded(t *testing.T) {
	g := newGitTool(t)
	write(t, g.root, "a.txt", "a\n")
	g.run0("add", "a.txt")
	reviewed := g.prepare(ToolGitCommit, `{"message":"the reviewed message"}`)
	swapped := g.prepare(ToolGitCommit, `{"message":"a different message"}`)
	if reviewed.Digest == swapped.Digest {
		t.Fatal("two messages share a handle: approving one would run the other")
	}
	answer, err := g.session.Invoke(harnessproof.Mint(context.Background(), reviewed.Digest), reviewed)
	if err != nil || answer.Outcome != harness.OutcomeOK || g.run0("log", "-1", "--format=%s") != "the reviewed message" {
		t.Fatalf("%+v %v", answer, err)
	}
}

func TestACommitIsStaleIfTheAuthorOrTheParentChangedAfterReview(t *testing.T) {
	g := newGitTool(t)
	write(t, g.root, "a.txt", "a\n")
	g.run0("add", "a.txt")

	reviewed := g.prepare(ToolGitCommit, `{"message":"as reviewed"}`)
	g.run0("config", "user.name", "Someone Else")
	head := g.head()
	if answer, err := g.session.Invoke(harnessproof.Mint(context.Background(), reviewed.Digest), reviewed); err != nil || answer.Outcome != harness.OutcomeStale || g.head() != head {
		t.Fatalf("a changed author was committed as someone else: %+v %v", answer, err)
	}
	g.run0("config", "user.name", "Test Person")

	reviewed = g.prepare(ToolGitCommit, `{"message":"as reviewed"}`)
	g.moveHead() // the staged change is the same, the branch is not
	head = g.head()
	if answer, err := g.session.Invoke(harnessproof.Mint(context.Background(), reviewed.Digest), reviewed); err != nil || answer.Outcome != harness.OutcomeStale || g.head() != head {
		t.Fatalf("a commit on a moved branch: %+v %v", answer, err)
	}
}

func TestAProofForADifferentActionDoesNotApproveAGitWrite(t *testing.T) {
	g := newGitTool(t)
	write(t, g.root, "fresh.txt", "new\n")
	someoneElses := "sha256:" + strings.Repeat("b", 64)

	stage := g.prepare(ToolGitStage, `{"paths":["fresh.txt"]}`)
	if answer, err := g.session.Invoke(harnessproof.Mint(context.Background(), someoneElses), stage); err != nil || answer.Outcome != harness.OutcomeDenied {
		t.Fatalf("a stage under another action's proof: %+v %v", answer, err)
	}
	if g.run0("diff", "--cached", "--name-only") != "" {
		t.Fatal("a stage ran under another action's proof")
	}
	g.run0("add", "fresh.txt")
	commit := g.prepare(ToolGitCommit, `{"message":"x"}`)
	head := g.head()
	if answer, err := g.session.Invoke(harnessproof.Mint(context.Background(), someoneElses), commit); err != nil || answer.Outcome != harness.OutcomeDenied || g.head() != head {
		t.Fatalf("a commit under another action's proof: %+v %v", answer, err)
	}
}

func TestARepositoryThatStopsBeingAcceptableAfterPreparationIsStale(t *testing.T) {
	g := newGitTool(t)
	write(t, g.root, "fresh.txt", "new\n")
	status := g.prepare(ToolGitStatus, `{}`)
	stage := g.prepare(ToolGitStage, `{"paths":["fresh.txt"]}`)
	g.breakConfig()
	for name, action := range map[string]harness.PreparedAction{"status": status, "stage": stage} {
		answer, err := g.session.Invoke(harnessproof.Mint(context.Background(), action.Digest), action)
		if err != nil || answer.Outcome != harness.OutcomeStale {
			t.Errorf("%s against a repository that stopped being acceptable: %+v %v", name, answer, err)
		}
	}
	if len(g.ran()) != 0 {
		t.Fatalf("something ran: %v", g.ran())
	}
	// Plain Git would run the repository's monitor, so the check switches it off.
	if got := g.run0("-c", "core.fsmonitor=false", "diff", "--cached", "--name-only"); got != "" {
		t.Fatalf("a stage ran against a refused repository: %q", got)
	}
}

func TestACommitThatIsNotWhatWasApprovedIsReportedAndAudited(t *testing.T) {
	for _, tc := range []struct {
		name     string
		tamper   func(*commitPlan)
		mismatch bool
	}{
		{"as planned", func(*commitPlan) {}, false},
		{"another parent", func(p *commitPlan) { p.head = strings.Repeat("0", 40) }, true},
		{"another change", func(p *commitPlan) { p.fingerprint = "sha256:not-what-was-staged" }, true},
	} {
		g := newGitTool(t)
		write(t, g.root, "a.txt", "a\n")
		g.run0("add", "a.txt")
		s := g.sess()
		plan, outcome, reason := s.planCommit(context.Background(), g.opened(s), g.root, "check the result")
		if outcome != harness.OutcomeOK {
			t.Fatalf("%s: %s %s", tc.name, outcome, reason)
		}
		tc.tamper(plan)
		out, outcome, audit := s.commitPlanned(context.Background(), g.opened(s), plan, 4096)
		warned := strings.HasPrefix(string(out), "WARNING")
		if outcome != harness.OutcomeOK || warned != tc.mismatch || (len(audit) == 1 && audit[0] == "commit_mismatch") != tc.mismatch || (!tc.mismatch && len(audit) != 0) {
			t.Errorf("%s: %s %q audit %v", tc.name, outcome, out, audit)
		}
		if g.run0("log", "-1", "--format=%s") != "check the result" {
			t.Errorf("%s: the commit was not made", tc.name)
		}
		if tc.mismatch {
			// The warning comes first, so no bound can cut it off.
			write(t, g.root, "b.txt", "b\n")
			g.run0("add", "b.txt")
			again, _, _ := s.planCommit(context.Background(), g.opened(s), g.root, "bounded")
			tc.tamper(again)
			short, _, _ := s.commitPlanned(context.Background(), g.opened(s), again, 24)
			if !strings.HasPrefix(string(short), "WARNING") || len(short) > 24 {
				t.Errorf("%s: a short result lost its warning: %q", tc.name, short)
			}
		}
	}
}

func TestClipNeverCutsACharacterInTwo(t *testing.T) {
	for _, tc := range []struct {
		text  string
		limit int
		want  string
	}{
		{"abc", 5, "abc"},
		{"abc", 3, "abc"},
		{"abc", 2, "ab"},
		{"abc", 0, ""},
		{"日本語", 9, "日本語"},
		{"日本語", 8, "日本"},
		{"日本語", 4, "日"},
		{"日本語", 2, ""},
	} {
		if got := clip(tc.text, tc.limit); got != tc.want || !utf8.ValidString(got) {
			t.Errorf("clip(%q, %d) = %q, want %q", tc.text, tc.limit, got, tc.want)
		}
	}
}

func TestACommitPreviewThatCannotBeShownInFullIsCutOnALineAndEscalated(t *testing.T) {
	g := newGitTool(t)
	// Each invisible character is shown as a twelve-byte marker, so a modest diff becomes a huge preview.
	write(t, g.root, "marks.txt", strings.Repeat(strings.Repeat("\u2060", 6)+"\n", 2000))
	g.run0("add", "marks.txt")
	// A message one byte longer moves everything after it by one byte, so across twelve of them the
	// size bound falls on every position inside a marker, and none may leave half a character.
	for pad := 0; pad < 13; pad++ {
		m := g.tool(false, ToolGitCommit, fmt.Sprintf(`{"message":"invisible characters%s"}`, strings.Repeat("x", pad)))
		if m.prepared != harness.OutcomeOK {
			t.Fatalf("pad %d: %+v", pad, m)
		}
		preview := m.action.Preview
		if len(preview) > harness.MaxPreviewBytes || !utf8.ValidString(preview) || !strings.Contains(preview, "[preview cut]") || strings.Contains(preview, "\u2060") || !contains(m.action.Escalate, "large_change") {
			t.Fatalf("pad %d: a preview of %d bytes, valid %v, escalate %v", pad, len(preview), utf8.ValidString(preview), m.action.Escalate)
		}
	}
}

func TestACommitResultThatDoesNotFitIsCutWithoutBreakingACharacter(t *testing.T) {
	g := newGitTool(t)
	s := g.sess()
	// The text before the subject is 32 bytes, so these limits step through every position inside
	// a three-byte character of it. (A session never asks for a bound this small after showing a
	// preview, so the cut is exercised directly.)
	for limit := 36; limit < 42; limit++ {
		name := fmt.Sprintf("again%d.txt", limit)
		write(t, g.root, name, "x\n")
		g.run0("add", "--", name)
		plan, outcome, reason := s.planCommit(context.Background(), g.opened(s), g.root, "日本語日本語日本語")
		if outcome != harness.OutcomeOK {
			t.Fatalf("%s %s", outcome, reason)
		}
		out, outcome, _ := s.commitPlanned(context.Background(), g.opened(s), plan, limit)
		if outcome != harness.OutcomeOK || len(out) > limit || !utf8.Valid(out) || !strings.HasPrefix(string(out), "committed ") {
			t.Fatalf("within %d bytes: %s %q", limit, outcome, out)
		}
	}
}

func TestGitResultsAreBoundedMarkedPartialAndRedacted(t *testing.T) {
	g := newGitTool(t)
	s := g.sess()
	const secret = "hunter2hunter2"
	valid := func(label string, out []byte) {
		t.Helper()
		if !json.Valid(out) {
			t.Errorf("%s: not valid JSON: %q", label, out)
		}
	}

	var entries []harnessgit.StatusEntry
	for i := 0; i < 200; i++ {
		entries = append(entries, harnessgit.StatusEntry{Kind: "modified", Unstaged: "M", Path: fmt.Sprintf("dir/file-%03d.txt", i)})
	}
	// A status too long for its budget is cut, says so, and still fits.
	out, outcome, _ := s.fitStatus(harnessgit.Status{Branch: "main", Entries: entries}, 3000)
	var cut harnessgit.Status
	if outcome != harness.OutcomePartial || len(out) > 3000 || json.Unmarshal(out, &cut) != nil || !cut.Truncated || len(cut.Entries) == 0 || len(cut.Entries) >= 200 {
		t.Fatalf("a long status: %s %d bytes %+v", outcome, len(out), cut.Truncated)
	}
	// One that fits is untouched and complete.
	out, outcome, _ = s.fitStatus(harnessgit.Status{Branch: "main", Entries: entries[:3]}, 3000)
	var whole harnessgit.Status
	if outcome != harness.OutcomeOK || json.Unmarshal(out, &whole) != nil || whole.Truncated || len(whole.Entries) != 3 {
		t.Fatalf("a short status: %s %s", outcome, out)
	}
	// One Git already cut stays marked partial even when it fits.
	if _, outcome, _ = s.fitStatus(harnessgit.Status{Branch: "main", Entries: entries[:3], Truncated: true}, 3000); outcome != harness.OutcomePartial {
		t.Fatalf("a status Git cut: %s", outcome)
	}
	// What cannot be made to fit is not returned.
	if out, outcome, _ = s.fitStatus(harnessgit.Status{Branch: strings.Repeat("b", 5000)}, 1000); outcome != harness.OutcomeFailed || len(out) != 0 {
		t.Fatalf("a status that cannot fit: %s %d bytes", outcome, len(out))
	}
	// Secrets are removed from what is returned, and from what is measured.
	if out, outcome, _ = s.fitStatus(harnessgit.Status{Branch: secret, Entries: entries[:1]}, 3000); outcome != harness.OutcomeOK || strings.Contains(string(out), secret) || !strings.Contains(string(out), "[REDACTED_SECRET]") {
		t.Fatalf("an unredacted status: %s", out)
	}

	var commits []harnessgit.LogEntry
	for i := 0; i < 50; i++ {
		commits = append(commits, harnessgit.LogEntry{Commit: strings.Repeat("a", 40), Author: "Test Person", Date: "2026-10-05", Subject: strings.Repeat("s", 200)})
	}
	out, outcome, _ = s.fitLog(harnessgit.Log{Entries: commits}, 2000)
	var shown harnessgit.Log
	if outcome != harness.OutcomePartial || len(out) > 2000 || json.Unmarshal(out, &shown) != nil || len(shown.Entries) == 0 || len(shown.Entries) >= 50 {
		t.Fatalf("a long log: %s %d bytes", outcome, len(out))
	}
	if out, outcome, _ = s.fitLog(harnessgit.Log{Entries: commits[:2]}, 2000); outcome != harness.OutcomeOK {
		t.Fatalf("a short log: %s %s", outcome, out)
	}
	if out, _, _ = s.fitLog(harnessgit.Log{Entries: []harnessgit.LogEntry{{Subject: secret}}}, 2000); strings.Contains(string(out), secret) {
		t.Fatalf("an unredacted log: %s", out)
	}

	long := strings.Repeat("+a line of a long diff\n", 2000)
	out, outcome, _ = s.fitDiff(false, harnessgit.Diff{Text: long, Paths: 1}, 3000)
	valid("a long diff", out)
	if outcome != harness.OutcomePartial || len(out) > 3000 || !strings.Contains(string(out), `"truncated":true`) {
		t.Fatalf("a long diff: %s %d bytes", outcome, len(out))
	}
	if out, outcome, _ = s.fitDiff(true, harnessgit.Diff{Text: "+one line\n", Paths: 1}, 3000); outcome != harness.OutcomeOK || strings.Contains(string(out), "truncated") {
		t.Fatalf("a short diff: %s %s", outcome, out)
	}
	if _, outcome, _ = s.fitDiff(false, harnessgit.Diff{Text: "+one line\n", Paths: 1, Truncated: true}, 3000); outcome != harness.OutcomePartial {
		t.Fatalf("a diff Git cut: %s", outcome)
	}
	if out, _, _ = s.fitDiff(false, harnessgit.Diff{Text: "+key = " + secret + "\n", Paths: 1}, 3000); strings.Contains(string(out), secret) {
		t.Fatalf("an unredacted diff: %s", out)
	}
	if out, outcome, _ = s.fitDiff(false, harnessgit.Diff{Text: "+x", Paths: 1, Omitted: 2}, 10); outcome != harness.OutcomeFailed || len(out) != 0 {
		t.Fatalf("a diff that cannot fit: %s %q", outcome, out)
	}
}

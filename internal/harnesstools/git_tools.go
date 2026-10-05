package harnesstools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessexec"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessfs"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessgit"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessproof"
)

const (
	ToolGitStatus = "git_status"
	ToolGitDiff   = "git_diff"
	ToolGitLog    = "git_log"
	ToolGitStage  = "git_stage"
	ToolGitCommit = "git_commit"

	// MaxGitStagePaths keeps a staging call within what policy can see of its paths.
	MaxGitStagePaths   = harness.MaxActionPaths
	maxStageFileBytes  = harnessfs.MaxDigestBytes
	stagePreviewLines  = 60
	commitPreviewDiff  = 24 << 10
	commitPreviewStat  = 8 << 10
	manyFilesThreshold = 20
	defaultLogCount    = 10
)

// GitConfig turns the Git tools on. The zero value leaves them off.
type GitConfig struct {
	Enabled bool
	// SearchPath is where "git" is looked up, never the project. Empty uses the harness's PATH.
	SearchPath []string
	// Home is the home directory Git is given so the person's own configuration applies.
	Home string
	// TempRoot is a private directory for each Git process's temporary files.
	TempRoot string
}

func (c GitConfig) runner() harnessgit.Config {
	return harnessgit.Config{SearchPath: c.SearchPath, Home: c.Home, TempRoot: c.TempRoot}
}

// gitCall is a prepared Git call. The mutating ones carry what the approval was bound to.
type gitCall struct {
	kind     string
	staged   bool
	path     string
	count    int
	paths    []string
	message  string
	mutating bool
}

type gitStageArgs struct {
	Paths []string `json:"paths"`
}

type gitCommitArgs struct {
	Message string `json:"message"`
}

type gitDiffArgs struct {
	Staged bool   `json:"staged"`
	Path   string `json:"path"`
}

type gitLogArgs struct {
	Count int    `json:"count"`
	Path  string `json:"path"`
}

type gitStatusArgs struct{}

// gitFailure turns a Git-layer error into a typed outcome and a fixed reason code.
func gitFailure(err error) (harness.Outcome, string) {
	if refusal, ok := harnessgit.IsRefusal(err); ok {
		return harness.OutcomeFailed, refusal.Reason
	}
	switch {
	case errors.Is(err, harnessgit.ErrNoGit):
		return harness.OutcomeUnavailable, "git_not_found"
	case errors.Is(err, harnessgit.ErrNothingStaged):
		return harness.OutcomeFailed, "nothing_staged"
	case errors.Is(err, harnessgit.ErrProtectedPath):
		return harness.OutcomeFailed, "protected_path"
	case errors.Is(err, harnessgit.ErrSubmodule):
		return harness.OutcomeFailed, "submodule"
	case errors.Is(err, harnessgit.ErrNoIdentity):
		return harness.OutcomeFailed, "no_identity"
	case errors.Is(err, harnessgit.ErrUnborn):
		return harness.OutcomeFailed, "unborn"
	case errors.Is(err, harnessgit.ErrInvalid):
		return harness.OutcomeFailed, "invalid_arguments"
	case errors.Is(err, context.DeadlineExceeded):
		return harness.OutcomeTimeout, ""
	case errors.Is(err, context.Canceled):
		return harness.OutcomeCancelled, ""
	}
	return harness.OutcomeFailed, "git_failed"
}

// openGit accepts the repository and binds the Git program to it. Accepting a repository
// reads files and asks Git to list the repository's setting names; it changes nothing.
func (s *session) openGit(ctx context.Context, root string) (*harnessgit.Git, error) {
	repo, err := harnessgit.Open(root)
	if err != nil {
		return nil, err
	}
	return harnessgit.New(ctx, s.provider.cfg.Git.runner(), repo)
}

func gitUsable() bool { return harnessexec.Supported() }

// ---- preparing ----

func (s *session) prepareGit(ctx context.Context, q harness.ToolRequest, root string, action harness.PreparedAction) harness.PreparedAction {
	fail := func(outcome harness.Outcome, reason string) harness.PreparedAction {
		action.Outcome, action.Reason = outcome, reason
		return action
	}
	if !s.provider.cfg.Git.Enabled || !gitUsable() {
		return fail(harness.OutcomeUnsupported, "")
	}
	switch q.ToolID {
	case ToolGitStatus, ToolGitDiff, ToolGitLog:
		return s.prepareGitRead(ctx, q, root, action)
	case ToolGitStage:
		return s.prepareGitStage(ctx, q, root, action)
	case ToolGitCommit:
		return s.prepareGitCommit(ctx, q, root, action)
	}
	return fail(harness.OutcomeUnsupported, "")
}

func (s *session) remember(digest string, c call, action harness.PreparedAction) (harness.PreparedAction, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pending) >= maxPending || (c.git != nil && c.git.mutating && s.pendingMutations() >= maxPendingMutations) {
		action.Outcome, action.Reason = harness.OutcomeFailed, "busy"
		return action, false
	}
	s.pending[digest] = c
	action.Outcome, action.Digest = harness.OutcomeOK, digest
	return action, true
}

func readDigest(tool string, args any, root string) string {
	canonical, _ := json.Marshal(args)
	sum := sha256.Sum256([]byte(tool + "\x00" + string(canonical) + "\x00" + root))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func (s *session) prepareGitRead(_ context.Context, q harness.ToolRequest, root string, action harness.PreparedAction) harness.PreparedAction {
	fail := func(outcome harness.Outcome, reason string) harness.PreparedAction {
		action.Outcome, action.Reason = outcome, reason
		return action
	}
	call := &gitCall{kind: q.ToolID}
	var normalized any
	switch q.ToolID {
	case ToolGitStatus:
		var a gitStatusArgs
		if decodeStrict(q.Arguments, &a) != nil {
			return fail(harness.OutcomeFailed, "invalid_arguments")
		}
		normalized, action.Summary = a, "git status"
	case ToolGitDiff:
		var a gitDiffArgs
		if decodeStrict(q.Arguments, &a) != nil {
			return fail(harness.OutcomeFailed, "invalid_arguments")
		}
		if a.Path != "" {
			clean, outcome := cleanPath(a.Path, false)
			if outcome != "" {
				return fail(outcome, "")
			}
			a.Path = clean
		}
		call.staged, call.path = a.Staged, a.Path
		normalized, action.Summary = a, "git diff"
		if a.Staged {
			action.Summary = "git diff (staged)"
		}
	case ToolGitLog:
		var a gitLogArgs
		if decodeStrict(q.Arguments, &a) != nil {
			return fail(harness.OutcomeFailed, "invalid_arguments")
		}
		if a.Count == 0 {
			a.Count = defaultLogCount
		}
		if a.Count < 1 || a.Count > harnessgit.MaxLogEntries {
			return fail(harness.OutcomeFailed, "invalid_arguments")
		}
		if a.Path != "" {
			clean, outcome := cleanPath(a.Path, false)
			if outcome != "" {
				return fail(outcome, "")
			}
			a.Path = clean
		}
		call.count, call.path = a.Count, a.Path
		normalized, action.Summary = a, fmt.Sprintf("git log (%d)", a.Count)
	}
	// Accepting the repository is part of preparing, so a refusal is reported at once.
	if _, err := harnessgit.Open(root); err != nil {
		outcome, reason := gitFailure(err)
		return fail(outcome, reason)
	}
	prepared, _ := s.remember(readDigest(q.ToolID, normalized, root), call2(q.ToolID, root, call), action)
	return prepared
}

func call2(tool, root string, g *gitCall) call { return call{tool: tool, root: root, git: g} }

// ---- staging ----

type stagePlan struct {
	paths    []string
	revs     map[string]string
	states   map[string]harnessgit.PathState
	sizes    map[string]int64
	head     string
	branch   string
	identity string
	preview  string
	digest   string
}

func (s *session) planStage(ctx context.Context, g *harnessgit.Git, root string, args gitStageArgs) (*stagePlan, harness.Outcome, string) {
	if len(args.Paths) == 0 || len(args.Paths) > MaxGitStagePaths {
		return nil, harness.OutcomeFailed, "invalid_arguments"
	}
	seen := map[string]bool{}
	var paths []string
	for _, raw := range args.Paths {
		clean, outcome := cleanPath(raw, false)
		if outcome != "" {
			return nil, outcome, ""
		}
		if seen[clean] {
			return nil, harness.OutcomeFailed, "invalid_arguments"
		}
		seen[clean] = true
		paths = append(paths, clean)
	}
	sort.Strings(paths)
	project, err := harnessfs.Open(root)
	if err != nil {
		return nil, harness.OutcomeUnavailable, ""
	}
	defer project.Close()
	plan := &stagePlan{paths: paths, revs: map[string]string{}, sizes: map[string]int64{}, identity: g.ProgramIdentity}
	for _, p := range paths {
		if g.Repo().Nested(p) {
			return nil, harness.OutcomeFailed, "nested_repository"
		}
		info, err := project.Inspect(p)
		if err != nil {
			outcome, reason := planFailure(err)
			return nil, outcome, reason
		}
		switch {
		case !info.Exists:
			plan.revs[p] = harnessfs.AbsentRevision
		case !info.Regular:
			return nil, harness.OutcomeFailed, "not_a_file"
		default:
			revision, size, err := project.Digest(p, maxStageFileBytes)
			if err != nil {
				outcome, reason := planFailure(err)
				return nil, outcome, reason
			}
			plan.revs[p], plan.sizes[p] = revision, size
		}
	}
	var err2 error
	if plan.head, err2 = g.Head(ctx); err2 != nil && !errors.Is(err2, harnessgit.ErrUnborn) {
		outcome, reason := gitFailure(err2)
		return nil, outcome, reason
	}
	if plan.branch, err2 = g.Branch(ctx); err2 != nil {
		outcome, reason := gitFailure(err2)
		return nil, outcome, reason
	}
	if plan.states, err2 = g.PathStates(ctx, paths); err2 != nil {
		outcome, reason := gitFailure(err2)
		return nil, outcome, reason
	}
	for _, p := range paths {
		// A path Git reports nothing about has the zero state, which falls in the last case.
		st := plan.states[p]
		switch {
		case st.Kind == "conflicted":
			return nil, harness.OutcomeFailed, "conflicted"
		case st.Unstaged == "" && st.Kind != "new":
			return nil, harness.OutcomeFailed, "nothing_to_stage" // unchanged, or already staged and not changed since
		}
	}
	preview, outcome, reason := s.stagePreview(ctx, g, project, plan)
	if outcome != harness.OutcomeOK {
		return nil, outcome, reason
	}
	plan.preview = preview
	canonical, _ := json.Marshal(struct {
		Tool     string            `json:"tool"`
		Paths    []string          `json:"paths"`
		Revs     map[string]string `json:"revs"`
		Head     string            `json:"head"`
		Branch   string            `json:"branch"`
		Identity string            `json:"identity"`
	}{ToolGitStage, plan.paths, plan.revs, plan.head, plan.branch, plan.identity})
	sum := sha256.Sum256([]byte(string(canonical) + "\x00" + root))
	plan.digest = "sha256:" + hex.EncodeToString(sum[:])
	return plan, harness.OutcomeOK, ""
}

func (s *session) stagePreview(ctx context.Context, g *harnessgit.Git, project *harnessfs.Root, plan *stagePlan) (string, harness.Outcome, string) {
	var b strings.Builder
	b.WriteString("STAGE FILES  (adds these files to the index; nothing is committed, pushed or deleted)\n")
	fmt.Fprintf(&b, "  branch   %s\n", visible(plan.branch))
	if plan.head == "" {
		b.WriteString("  base     no commits yet\n")
	} else {
		fmt.Fprintf(&b, "  base     %s\n", plan.head[:min(12, len(plan.head))])
	}
	b.WriteString("  files\n")
	var tracked []string
	for _, p := range plan.paths {
		st := plan.states[p]
		label := map[string]string{"new": "new file", "modified": "modified", "deleted": "deleted ", "typechanged": "type changed"}[st.Kind]
		if label == "" {
			label = st.Kind
		}
		suffix := ""
		if size, ok := plan.sizes[p]; ok {
			suffix = fmt.Sprintf("  (%d bytes)", size)
		}
		fmt.Fprintf(&b, "    %-12s %s%s\n", label, visible(p), suffix)
		if st.Kind != "new" {
			tracked = append(tracked, p)
		}
	}
	b.WriteString("\n")
	diff, cut, err := g.DiffPaths(ctx, false, tracked)
	if err != nil {
		outcome, reason := gitFailure(err)
		return "", outcome, reason
	}
	b.WriteString(visible(diff))
	if cut {
		return "", harness.OutcomeFailed, "change_too_large"
	}
	for _, p := range plan.paths {
		if plan.states[p].Kind != "new" {
			continue
		}
		fmt.Fprintf(&b, "\n--- /dev/null\n+++ b/%s\n", visible(p))
		file, err := project.Read(p, 64<<10)
		switch {
		case err != nil: // binary, or too large to show as text
			fmt.Fprintf(&b, "(new file, %d bytes, not shown as text)\n", plan.sizes[p])
		default:
			lines := splitKeep(string(file.Data))
			shown := min(len(lines), stagePreviewLines)
			for _, l := range lines[:shown] {
				writeLine(&b, '+', l)
			}
			if shown < len(lines) || file.Truncated {
				fmt.Fprintf(&b, "[%d more lines not shown]\n", max(len(lines)-shown, 1))
			}
		}
	}
	text := b.String()
	if !previewFits(text) {
		return "", harness.OutcomeFailed, "change_too_large"
	}
	return text, harness.OutcomeOK, ""
}

func (s *session) prepareGitStage(ctx context.Context, q harness.ToolRequest, root string, action harness.PreparedAction) harness.PreparedAction {
	fail := func(outcome harness.Outcome, reason string) harness.PreparedAction {
		action.Outcome, action.Reason = outcome, reason
		return action
	}
	var a gitStageArgs
	if decodeStrict(q.Arguments, &a) != nil {
		return fail(harness.OutcomeFailed, "invalid_arguments")
	}
	g, err := s.openGit(ctx, root)
	if err != nil {
		outcome, reason := gitFailure(err)
		return fail(outcome, reason)
	}
	plan, outcome, reason := s.planStage(ctx, g, root, a)
	if outcome != harness.OutcomeOK {
		return fail(outcome, reason)
	}
	action.Summary = fmt.Sprintf("stage %d %s", len(plan.paths), plural(len(plan.paths), "file", "files"))
	action.Paths, action.Preview = plan.paths, plan.preview
	if len(plan.paths) > manyFilesThreshold {
		action.Escalate = append(action.Escalate, "large_change")
	}
	prepared, _ := s.remember(plan.digest, call{tool: q.ToolID, root: root, git: &gitCall{kind: q.ToolID, paths: plan.paths, mutating: true}}, action)
	return prepared
}

// ---- committing ----

type commitPlan struct {
	head        string
	branch      string
	author      harnessgit.Author
	staged      []harnessgit.StagedChange
	fingerprint string
	message     string
	identity    string
	preview     string
	truncated   bool
	digest      string
	reportPaths []string
}

// controlPlaneFirst orders paths so every control-plane path is among the first ones, which
// are the ones a policy can see.
func controlPlaneFirst(paths []string) []string {
	out := append([]string(nil), paths...)
	sort.SliceStable(out, func(i, j int) bool {
		ci, cj := ControlPlane(out[i]) != "", ControlPlane(out[j]) != ""
		if ci != cj {
			return ci
		}
		return out[i] < out[j]
	})
	if len(out) > harness.MaxActionPaths {
		out = out[:harness.MaxActionPaths]
	}
	return out
}

func (s *session) planCommit(ctx context.Context, g *harnessgit.Git, root, message string) (*commitPlan, harness.Outcome, string) {
	if !harnessgit.ValidMessage(message) {
		return nil, harness.OutcomeFailed, "invalid_arguments"
	}
	plan := &commitPlan{message: message, identity: g.ProgramIdentity}
	var err error
	if plan.head, err = g.Head(ctx); err != nil && !errors.Is(err, harnessgit.ErrUnborn) {
		outcome, reason := gitFailure(err)
		return nil, outcome, reason
	}
	if plan.branch, err = g.Branch(ctx); err != nil {
		outcome, reason := gitFailure(err)
		return nil, outcome, reason
	}
	if plan.author, err = g.Identity(ctx); err != nil {
		outcome, reason := gitFailure(err)
		return nil, outcome, reason
	}
	if plan.staged, err = g.Staged(ctx); err != nil {
		outcome, reason := gitFailure(err)
		return nil, outcome, reason
	}
	if plan.fingerprint, err = g.StagedFingerprint(ctx); err != nil {
		outcome, reason := gitFailure(err)
		return nil, outcome, reason
	}
	summary, err := g.StagedSummary(ctx)
	if err != nil {
		outcome, reason := gitFailure(err)
		return nil, outcome, reason
	}
	diff, err := g.Diff(ctx, harnessgit.DiffOptions{Staged: true})
	if err != nil {
		outcome, reason := gitFailure(err)
		return nil, outcome, reason
	}
	paths := make([]string, len(plan.staged))
	for i, c := range plan.staged {
		paths[i] = c.Path
	}
	plan.reportPaths = controlPlaneFirst(paths)
	text, cut := fit(diff.Text, commitPreviewDiff)
	plan.truncated = cut || diff.Truncated || diff.Omitted > 0
	if len(summary) > commitPreviewStat {
		summary = summary[:commitPreviewStat] + "\n[summary cut]\n"
		plan.truncated = true
	}
	var b strings.Builder
	b.WriteString("COMMIT  (records exactly what is staged, on the current branch; repository hooks are not run)\n")
	fmt.Fprintf(&b, "  branch    %s\n", visible(plan.branch))
	if plan.head == "" {
		b.WriteString("  parent    none: this is the first commit\n")
	} else {
		fmt.Fprintf(&b, "  parent    %s\n", plan.head[:min(12, len(plan.head))])
	}
	fmt.Fprintf(&b, "  author    %s <%s>\n", visible(plan.author.Name), visible(plan.author.Email))
	fmt.Fprintf(&b, "  files     %d\n", len(plan.staged))
	b.WriteString("  message\n")
	for _, line := range strings.Split(strings.TrimRight(message, "\n"), "\n") {
		fmt.Fprintf(&b, "    | %s\n", visible(line))
	}
	b.WriteString("\n  summary\n")
	for _, line := range strings.Split(strings.TrimRight(visible(summary), "\n"), "\n") {
		fmt.Fprintf(&b, "    %s\n", line)
	}
	b.WriteString("\n")
	b.WriteString(visible(text))
	if plan.truncated {
		b.WriteString("\n[the diff above is shortened: review the whole staged change with your own tools before approving]\n")
	}
	plan.preview = b.String()
	if !previewFits(plan.preview) {
		// Cut on a line boundary: a cut inside a multi-byte character would not be valid text.
		cut, _ := fit(plan.preview, harness.MaxPreviewBytes-64)
		plan.preview = cut + "\n[preview cut]\n"
		plan.truncated = true
	}
	canonical, _ := json.Marshal(struct {
		Tool        string `json:"tool"`
		Head        string `json:"head"`
		Branch      string `json:"branch"`
		Fingerprint string `json:"fingerprint"`
		Message     string `json:"message"`
		Name        string `json:"name"`
		Email       string `json:"email"`
		Identity    string `json:"identity"`
	}{ToolGitCommit, plan.head, plan.branch, plan.fingerprint, plan.message, plan.author.Name, plan.author.Email, plan.identity})
	sum := sha256.Sum256([]byte(string(canonical) + "\x00" + root))
	plan.digest = "sha256:" + hex.EncodeToString(sum[:])
	return plan, harness.OutcomeOK, ""
}

func (s *session) prepareGitCommit(ctx context.Context, q harness.ToolRequest, root string, action harness.PreparedAction) harness.PreparedAction {
	fail := func(outcome harness.Outcome, reason string) harness.PreparedAction {
		action.Outcome, action.Reason = outcome, reason
		return action
	}
	var a gitCommitArgs
	if decodeStrict(q.Arguments, &a) != nil {
		return fail(harness.OutcomeFailed, "invalid_arguments")
	}
	g, err := s.openGit(ctx, root)
	if err != nil {
		outcome, reason := gitFailure(err)
		return fail(outcome, reason)
	}
	plan, outcome, reason := s.planCommit(ctx, g, root, a.Message)
	if outcome != harness.OutcomeOK {
		return fail(outcome, reason)
	}
	subject := strings.SplitN(strings.TrimSpace(plan.message), "\n", 2)[0]
	if utf8.RuneCountInString(subject) > 72 {
		subject = string([]rune(subject)[:72]) + "…"
	}
	action.Summary = fmt.Sprintf("commit %d %s: %s", len(plan.staged), plural(len(plan.staged), "file", "files"), visible(subject))
	action.Paths, action.Preview = plan.reportPaths, plan.preview
	if plan.truncated || len(plan.staged) > manyFilesThreshold {
		action.Escalate = append(action.Escalate, "large_change")
	}
	prepared, _ := s.remember(plan.digest, call{tool: q.ToolID, root: root, git: &gitCall{kind: q.ToolID, message: plan.message, mutating: true}}, action)
	return prepared
}

// ---- running ----

func (s *session) executeGit(ctx context.Context, prepared call, action harness.PreparedAction) ([]byte, harness.Outcome, []string) {
	g := prepared.git
	cfg := s.provider.cfg
	if !cfg.Git.Enabled || !gitUsable() {
		return nil, harness.OutcomeUnavailable, nil
	}
	if g.mutating && (action.Digest == "" || harnessproof.Approved(ctx) != action.Digest) {
		return nil, harness.OutcomeDenied, nil
	}
	repoGit, err := s.openGit(ctx, prepared.root)
	if err != nil {
		outcome, _ := gitFailure(err)
		if outcome == harness.OutcomeFailed {
			outcome = harness.OutcomeStale // the repository stopped being acceptable since it was prepared
		}
		return nil, outcome, nil
	}
	switch g.kind {
	case ToolGitStatus:
		st, err := repoGit.Status(ctx)
		if err != nil {
			outcome, _ := gitFailure(err)
			return nil, outcome, nil
		}
		return s.fitStatus(st, action.MaxBytes)
	case ToolGitDiff:
		d, err := repoGit.Diff(ctx, harnessgit.DiffOptions{Staged: g.staged, Path: g.path})
		if err != nil {
			outcome, _ := gitFailure(err)
			return nil, outcome, nil
		}
		return s.fitDiff(g.staged, d, action.MaxBytes)
	case ToolGitLog:
		l, err := repoGit.Log(ctx, g.count, g.path)
		if err != nil {
			outcome, _ := gitFailure(err)
			return nil, outcome, nil
		}
		return s.fitLog(l, action.MaxBytes)
	case ToolGitStage:
		return s.runStage(ctx, repoGit, prepared, action)
	case ToolGitCommit:
		return s.runCommit(ctx, repoGit, prepared, action)
	}
	return nil, harness.OutcomeUnsupported, nil
}

func (s *session) finishJSON(value any, maxBytes int, partial bool) ([]byte, harness.Outcome, []string) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, harness.OutcomeFailed, nil
	}
	text := s.provider.cfg.Redact(string(raw))
	if len(text) > maxBytes {
		return nil, harness.OutcomeFailed, nil
	}
	if partial {
		return []byte(text), harness.OutcomePartial, nil
	}
	return []byte(text), harness.OutcomeOK, nil
}

func (s *session) fitStatus(st harnessgit.Status, maxBytes int) ([]byte, harness.Outcome, []string) {
	partial := st.Truncated
	for {
		raw, _ := json.Marshal(st)
		if len(s.provider.cfg.Redact(string(raw))) <= maxBytes || len(st.Entries) == 0 {
			break
		}
		st.Entries, st.Truncated, partial = st.Entries[:len(st.Entries)*3/4], true, true
	}
	return s.finishJSON(st, maxBytes, partial)
}

func (s *session) fitLog(l harnessgit.Log, maxBytes int) ([]byte, harness.Outcome, []string) {
	partial := false
	for {
		raw, _ := json.Marshal(l)
		if len(s.provider.cfg.Redact(string(raw))) <= maxBytes || len(l.Entries) == 0 {
			break
		}
		l.Entries, partial = l.Entries[:len(l.Entries)*3/4], true
	}
	return s.finishJSON(l, maxBytes, partial)
}

func (s *session) fitDiff(staged bool, d harnessgit.Diff, maxBytes int) ([]byte, harness.Outcome, []string) {
	type result struct {
		Staged    bool   `json:"staged"`
		Paths     int    `json:"paths"`
		Omitted   int    `json:"omitted_protected,omitempty"`
		Truncated bool   `json:"truncated,omitempty"`
		Diff      string `json:"diff"`
	}
	r := result{Staged: staged, Paths: d.Paths, Omitted: d.Omitted, Truncated: d.Truncated, Diff: d.Text}
	for {
		raw, _ := json.Marshal(r)
		if len(s.provider.cfg.Redact(string(raw))) <= maxBytes || r.Diff == "" {
			break
		}
		next := len(r.Diff) * 3 / 4
		cut, _ := fit(r.Diff, next)
		r.Diff, r.Truncated = cut, true
	}
	return s.finishJSON(r, maxBytes, r.Truncated)
}

func (s *session) runStage(ctx context.Context, g *harnessgit.Git, prepared call, action harness.PreparedAction) ([]byte, harness.Outcome, []string) {
	root := prepared.root
	// The plan is recomputed from the repository as it is now and must be the one that was approved.
	plan, outcome, _ := s.planStage(ctx, g, root, gitStageArgs{Paths: prepared.git.paths})
	if outcome != harness.OutcomeOK || plan.digest != action.Digest {
		return nil, harness.OutcomeStale, nil
	}
	if err := g.Add(ctx, plan.paths); err != nil {
		outcome, _ := gitFailure(err)
		return nil, outcome, nil
	}
	text := fmt.Sprintf("staged %d %s on %s (nothing is committed): %s", len(plan.paths), plural(len(plan.paths), "file", "files"), plan.branch, strings.Join(plan.paths, ", "))
	return []byte(s.provider.cfg.Redact(clip(text, action.MaxBytes))), harness.OutcomeOK, nil
}

func (s *session) runCommit(ctx context.Context, g *harnessgit.Git, prepared call, action harness.PreparedAction) ([]byte, harness.Outcome, []string) {
	plan, outcome, _ := s.planCommit(ctx, g, prepared.root, prepared.git.message)
	if outcome != harness.OutcomeOK || plan.digest != action.Digest {
		return nil, harness.OutcomeStale, nil
	}
	return s.commitPlanned(ctx, g, plan, action.MaxBytes)
}

// commitPlanned records what a plan describes and then checks the commit that resulted is
// that change on that parent. The check is the last line of defence against the repository
// moving between the plan and the commit, so a mismatch is reported to the person and the
// audit record rather than hidden.
func (s *session) commitPlanned(ctx context.Context, g *harnessgit.Git, plan *commitPlan, maxBytes int) ([]byte, harness.Outcome, []string) {
	done, err := g.Commit(ctx, plan.message)
	if err != nil {
		outcome, _ := gitFailure(err)
		return nil, outcome, nil
	}
	var audit []string
	warning := ""
	if fp, ferr := g.CommitFingerprint(ctx, done.Commit); ferr != nil || fp != plan.fingerprint || done.Parent != plan.head {
		audit = append(audit, "commit_mismatch")
		warning = "WARNING: the commit does not match what was approved; inspect it before relying on it\n" // first, so a cut cannot lose it
	}
	subject, _ := g.Subject(ctx, done.Commit)
	text := fmt.Sprintf("%scommitted %s on %s: %s (%d %s)", warning, done.Commit[:min(12, len(done.Commit))], plan.branch, subject, len(plan.staged), plural(len(plan.staged), "file", "files"))
	return []byte(s.provider.cfg.Redact(clip(text, maxBytes))), harness.OutcomeOK, audit
}

// clip shortens text to at most limit bytes without cutting a character in two.
func clip(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut]
}

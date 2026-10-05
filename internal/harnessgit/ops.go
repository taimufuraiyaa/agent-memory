package harnessgit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/taimufuraiyaa/agent-memory/internal/harnessexec"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessfs"
)

const (
	MaxStatusEntries = 500
	MaxDiffPaths     = 200
	MaxLogEntries    = 50
	MaxMessageBytes  = 4 << 10
	MaxStagePaths    = 50
)

var (
	// ErrFailed: git ran and reported a failure. The text is Git's own and bounded.
	ErrFailed = errors.New("git reported an error")
	// ErrUnborn: the repository has no commits yet.
	ErrUnborn = errors.New("the repository has no commits yet")
	// ErrProtectedPath: a path the harness never touches (hidden, credential-like) is involved.
	ErrProtectedPath = errors.New("a protected path is involved")
	// ErrSubmodule: a submodule entry is involved; submodules are not handled.
	ErrSubmodule = errors.New("a submodule is involved")
	// ErrNothingStaged: there is nothing in the index to commit.
	ErrNothingStaged = errors.New("nothing is staged")
	// ErrNoIdentity: no author name and address are configured, and none is invented.
	ErrNoIdentity = errors.New("no author identity is configured")
	// ErrInvalid: an argument was refused before Git ran.
	ErrInvalid = errors.New("invalid argument")
)

func failed(res harnessexec.Result) error {
	text := strings.TrimSpace(res.Output)
	if len(text) > 400 {
		text = text[:400] + "…"
	}
	return fmt.Errorf("%w: exit %d: %s", ErrFailed, res.ExitCode, text)
}

// ok runs Git and returns its raw standard output, treating anything but a clean exit as failure.
func (g *Git) ok(ctx context.Context, timeout time.Duration, args ...string) ([]byte, error) {
	res, err := g.Run(ctx, timeout, args...)
	if err != nil {
		return nil, err
	}
	if res.TimedOut || res.Cancelled || res.ExitCode != 0 {
		if res.TimedOut {
			return nil, fmt.Errorf("%w: timed out", ErrFailed)
		}
		return nil, failed(res)
	}
	return res.Stdout, nil
}

// validRel accepts a project-relative path that is safe to hand to Git: clean, local, not
// hidden or credential-like, and not inside Git's own directory.
func validRel(p string) error {
	if p == "" || len(p) > harnessfs.MaxPathBytes || strings.ContainsAny(p, "\x00\n\r") || !utf8.ValidString(p) {
		return ErrInvalid
	}
	clean, err := harnessfs.Clean(p)
	if err != nil || clean == "." || clean != p || strings.ContainsRune(p, '\\') {
		return ErrInvalid
	}
	if harnessfs.Denied(clean) {
		return ErrProtectedPath
	}
	if strings.HasPrefix(p, "-") || strings.HasPrefix(p, ":") {
		return ErrInvalid
	}
	return nil
}

// Nested reports whether a path lies inside another repository below the project root, which
// is refused rather than followed.
func (r *Repo) Nested(rel string) bool {
	project, err := os.OpenRoot(r.Root)
	if err != nil {
		return true
	}
	defer project.Close()
	parts := strings.Split(filepath.ToSlash(rel), "/")
	prefix := ""
	for _, part := range parts[:len(parts)-1] {
		if prefix == "" {
			prefix = part
		} else {
			prefix += "/" + part
		}
		if _, err := project.Lstat(prefix + "/.git"); err == nil {
			return true
		}
	}
	return false
}

// ---- status ----

type StatusEntry struct {
	// Kind is modified, added, deleted, renamed, copied, typechanged, untracked or conflicted.
	Kind     string `json:"kind"`
	Staged   string `json:"staged,omitempty"`   // the index column, a single letter
	Unstaged string `json:"unstaged,omitempty"` // the work tree column, a single letter
	Path     string `json:"path"`
	From     string `json:"from,omitempty"`
}

type Status struct {
	Branch    string        `json:"branch"`
	Head      string        `json:"head,omitempty"`
	Upstream  string        `json:"upstream,omitempty"`
	Ahead     int           `json:"ahead,omitempty"`
	Behind    int           `json:"behind,omitempty"`
	Entries   []StatusEntry `json:"entries"`
	Omitted   int           `json:"omitted_protected,omitempty"`
	Truncated bool          `json:"truncated,omitempty"`
}

var kinds = map[byte]string{'M': "modified", 'A': "added", 'D': "deleted", 'R': "renamed", 'C': "copied", 'T': "typechanged", 'U': "conflicted"}

func kindOf(xy string) string {
	for _, c := range []byte(xy) {
		if c == 'U' {
			return "conflicted"
		}
	}
	for _, c := range []byte(xy) {
		if c != '.' {
			if k, ok := kinds[c]; ok {
				return k
			}
		}
	}
	return "modified"
}

func letter(c byte) string {
	if c == '.' {
		return ""
	}
	return string(c)
}

// Status lists branch information and changed paths, never file content. Paths the harness
// never shows (hidden, credential-like) are counted, not listed.
func (g *Git) Status(ctx context.Context) (Status, error) {
	out, err := g.ok(ctx, DefaultReadTimeout, "status", "--porcelain=v2", "--branch", "-z", "--untracked-files=normal", "--ignore-submodules=all")
	if err != nil {
		return Status{}, err
	}
	return parseStatus(out), nil
}

func parseStatus(out []byte) Status {
	st := Status{Entries: []StatusEntry{}}
	tokens := strings.Split(string(out), "\x00")
	add := func(e StatusEntry) {
		if harnessfs.Denied(e.Path) || (e.From != "" && harnessfs.Denied(e.From)) {
			st.Omitted++
			return
		}
		if len(st.Entries) >= MaxStatusEntries {
			st.Truncated = true
			return
		}
		st.Entries = append(st.Entries, e)
	}
	for i := 0; i < len(tokens); i++ {
		t := tokens[i]
		switch {
		case strings.HasPrefix(t, "# branch.oid "):
			if v := strings.TrimPrefix(t, "# branch.oid "); v != "(initial)" {
				st.Head = v
			}
		case strings.HasPrefix(t, "# branch.head "):
			st.Branch = strings.TrimPrefix(t, "# branch.head ")
		case strings.HasPrefix(t, "# branch.upstream "):
			st.Upstream = strings.TrimPrefix(t, "# branch.upstream ")
		case strings.HasPrefix(t, "# branch.ab "):
			fmt.Sscanf(strings.TrimPrefix(t, "# branch.ab "), "+%d -%d", &st.Ahead, &st.Behind)
		case strings.HasPrefix(t, "1 "):
			if f := strings.SplitN(t, " ", 9); len(f) == 9 {
				add(StatusEntry{Kind: kindOf(f[1]), Staged: letter(f[1][0]), Unstaged: letter(f[1][1]), Path: f[8]})
			}
		case strings.HasPrefix(t, "2 "):
			if f := strings.SplitN(t, " ", 10); len(f) == 10 && i+1 < len(tokens) {
				i++
				add(StatusEntry{Kind: kindOf(f[1]), Staged: letter(f[1][0]), Unstaged: letter(f[1][1]), Path: f[9], From: tokens[i]})
			}
		case strings.HasPrefix(t, "u "):
			if f := strings.SplitN(t, " ", 11); len(f) == 11 {
				add(StatusEntry{Kind: "conflicted", Staged: letter(f[1][0]), Unstaged: letter(f[1][1]), Path: f[10]})
			}
		case strings.HasPrefix(t, "? "):
			add(StatusEntry{Kind: "untracked", Path: strings.TrimPrefix(t, "? ")})
		}
	}
	return st
}

// ---- diff ----

type DiffOptions struct {
	Staged bool
	// Path limits the diff to one file or directory.
	Path string
}

type Diff struct {
	Text      string `json:"text"`
	Paths     int    `json:"paths"`
	Omitted   int    `json:"omitted_protected,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

func (g *Git) changedPaths(ctx context.Context, staged bool, scope string) (allowed []string, omitted int, truncated bool, err error) {
	args := []string{"diff", "--name-only", "-z", "--no-renames", "--ignore-submodules=all"}
	if staged {
		args = append(args, "--cached")
	}
	if scope != "" {
		args = append(args, "--", scope)
	}
	out, err := g.ok(ctx, DefaultReadTimeout, args...)
	if err != nil {
		return nil, 0, false, err
	}
	for _, p := range strings.Split(string(out), "\x00") {
		if p == "" {
			continue
		}
		if harnessfs.Denied(p) || validRel(p) != nil {
			omitted++
			continue
		}
		if len(allowed) >= MaxDiffPaths {
			truncated = true
			continue
		}
		allowed = append(allowed, p)
	}
	return allowed, omitted, truncated, nil
}

// Diff returns a unified diff of the staged or unstaged changes, without external diff
// programs or text conversion, and without any path the harness never shows.
func (g *Git) Diff(ctx context.Context, opts DiffOptions) (Diff, error) {
	if opts.Path != "" {
		if err := validRel(opts.Path); err != nil {
			return Diff{}, err
		}
	}
	paths, omitted, truncated, err := g.changedPaths(ctx, opts.Staged, opts.Path)
	if err != nil {
		return Diff{}, err
	}
	d := Diff{Paths: len(paths), Omitted: omitted, Truncated: truncated}
	if len(paths) == 0 {
		return d, nil
	}
	text, cut, err := g.diffPaths(ctx, opts.Staged, paths)
	if err != nil {
		return Diff{}, err
	}
	d.Text, d.Truncated = text, d.Truncated || cut
	return d, nil
}

// DiffPaths returns the unified diff of exactly the named paths, staged or not, and says whether
// it was cut at the output bound. Every path is validated here.
func (g *Git) DiffPaths(ctx context.Context, staged bool, paths []string) (string, bool, error) {
	for _, p := range paths {
		if err := validRel(p); err != nil {
			return "", false, err
		}
	}
	return g.diffPaths(ctx, staged, paths)
}

// diffPaths returns the unified diff of exactly the named paths, without external diff
// programs or text conversion, and says whether it was cut at the output bound. The paths
// must already have passed validRel.
func (g *Git) diffPaths(ctx context.Context, staged bool, paths []string) (string, bool, error) {
	if len(paths) == 0 {
		return "", false, nil
	}
	args := []string{"diff", "--no-ext-diff", "--no-textconv", "--no-color", "--no-renames", "--ignore-submodules=all", "-U3"}
	if staged {
		args = append(args, "--cached")
	}
	args = append(args, "--")
	args = append(args, paths...)
	res, err := g.Run(ctx, DefaultReadTimeout, args...)
	if err != nil {
		return "", false, err
	}
	if res.TimedOut || res.Cancelled || res.ExitCode != 0 {
		return "", false, failed(res)
	}
	return harnessexec.Sanitize(res.Stdout), res.StdoutOmitted > 0, nil
}

// PathState is what Git says about one named path.
type PathState struct {
	Path string
	// Kind is new (untracked), modified, deleted or conflicted; a path Git reports nothing
	// about is unchanged or ignored and is absent from the result.
	Kind     string
	Staged   string
	Unstaged string
}

// PathStates reports the state of exactly the named paths. Every path must be a project-relative,
// unprotected path; the result holds only paths with something to stage or already staged.
func (g *Git) PathStates(ctx context.Context, paths []string) (map[string]PathState, error) {
	args := []string{"status", "--porcelain=v2", "-z", "--untracked-files=all", "--ignore-submodules=all", "--"}
	for _, p := range paths {
		if err := validRel(p); err != nil {
			return nil, err
		}
		args = append(args, p)
	}
	out, err := g.ok(ctx, DefaultReadTimeout, args...)
	if err != nil {
		return nil, err
	}
	states := map[string]PathState{}
	for _, e := range parseStatus(out).Entries {
		kind := e.Kind
		if kind == "untracked" {
			kind = "new"
		}
		states[e.Path] = PathState{Path: e.Path, Kind: kind, Staged: e.Staged, Unstaged: e.Unstaged}
	}
	return states, nil
}

// StagedFingerprint identifies exactly what the index would record, as a hash of Git's raw
// listing of the staged differences: modes, object names and paths. Unlike the tree of the
// index, computing it writes nothing to the repository.
func (g *Git) StagedFingerprint(ctx context.Context) (string, error) {
	out, err := g.ok(ctx, DefaultReadTimeout, "diff", "--cached", "--raw", "-z", "--no-renames", "--no-abbrev")
	if err != nil {
		return "", err
	}
	return fingerprint(out), nil
}

// CommitFingerprint is the same identification for what an existing commit recorded
// relative to its parent (or to nothing, for a first commit). A commit made from an index
// has the same fingerprint as that index had.
func (g *Git) CommitFingerprint(ctx context.Context, commit string) (string, error) {
	if !hashRE.MatchString(commit) {
		return "", ErrInvalid
	}
	out, err := g.ok(ctx, DefaultReadTimeout, "diff-tree", "--root", "-r", "--raw", "-z", "--no-renames", "--no-abbrev", "--no-commit-id", commit)
	if err != nil {
		return "", err
	}
	return fingerprint(out), nil
}

func fingerprint(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// Branch returns the current branch name, or "(detached)".
func (g *Git) Branch(ctx context.Context) (string, error) {
	out, err := g.ok(ctx, DefaultReadTimeout, "symbolic-ref", "--short", "-q", "HEAD")
	if err != nil {
		res, rerr := g.Run(ctx, DefaultReadTimeout, "symbolic-ref", "--short", "-q", "HEAD")
		if rerr == nil && res.ExitCode == 1 {
			return "(detached)", nil
		}
		return "", err
	}
	return harnessexec.Sanitize([]byte(strings.TrimSpace(string(out)))), nil
}

// Subject returns the first line of a commit's message.
func (g *Git) Subject(ctx context.Context, commit string) (string, error) {
	if !hashRE.MatchString(commit) {
		return "", ErrInvalid
	}
	out, err := g.ok(ctx, DefaultReadTimeout, "show", "-s", "--format=%s", "--no-show-signature", commit)
	if err != nil {
		return "", err
	}
	return harnessexec.Sanitize([]byte(strings.TrimSpace(string(out)))), nil
}

// ---- log ----

type LogEntry struct {
	Commit  string `json:"commit"`
	Author  string `json:"author"`
	Date    string `json:"date"`
	Subject string `json:"subject"`
}

type Log struct {
	Entries []LogEntry `json:"entries"`
	Unborn  bool       `json:"unborn,omitempty"`
}

var hashRE = regexp.MustCompile(`^[0-9a-f]{40,64}$`)

// Log lists recent commits, newest first.
func (g *Git) Log(ctx context.Context, n int, path string) (Log, error) {
	if n < 1 || n > MaxLogEntries {
		return Log{}, ErrInvalid
	}
	if path != "" {
		if err := validRel(path); err != nil {
			return Log{}, err
		}
	}
	args := []string{"log", "-n", strconv.Itoa(n), "--no-color", "--no-decorate", "--no-show-signature", "--format=%x1e%H%x1f%an%x1f%aI%x1f%s"}
	if path != "" {
		args = append(args, "--", path)
	}
	res, err := g.Run(ctx, DefaultReadTimeout, args...)
	if err != nil {
		return Log{}, err
	}
	if res.ExitCode != 0 {
		if strings.Contains(res.Output, "does not have any commits yet") {
			return Log{Entries: []LogEntry{}, Unborn: true}, nil
		}
		return Log{}, failed(res)
	}
	log := Log{Entries: []LogEntry{}}
	for _, record := range strings.Split(string(res.Stdout), "\x1e") {
		f := strings.Split(strings.TrimSpace(record), "\x1f")
		if len(f) != 4 || !hashRE.MatchString(f[0]) {
			continue
		}
		log.Entries = append(log.Entries, LogEntry{Commit: f[0], Author: harnessexec.Sanitize([]byte(f[1])), Date: harnessexec.Sanitize([]byte(f[2])), Subject: harnessexec.Sanitize([]byte(f[3]))})
	}
	return log, nil
}

// ---- the pieces a stage or a commit is bound to ----

// Head returns the commit the current branch points at, or ErrUnborn.
func (g *Git) Head(ctx context.Context) (string, error) {
	res, err := g.Run(ctx, DefaultReadTimeout, "rev-parse", "--verify", "-q", "HEAD")
	if err != nil {
		return "", err
	}
	if res.ExitCode == 1 {
		return "", ErrUnborn
	}
	if res.ExitCode != 0 {
		return "", failed(res)
	}
	h := strings.TrimSpace(string(res.Stdout))
	if !hashRE.MatchString(h) {
		return "", failed(res)
	}
	return h, nil
}

// IndexTree returns the tree the index would become if committed.
func (g *Git) IndexTree(ctx context.Context) (string, error) {
	out, err := g.ok(ctx, DefaultReadTimeout, "write-tree")
	if err != nil {
		return "", err
	}
	h := strings.TrimSpace(string(out))
	if !hashRE.MatchString(h) {
		return "", fmt.Errorf("%w: unexpected tree", ErrFailed)
	}
	return h, nil
}

// StagedChange is one entry of the index that differs from the last commit.
type StagedChange struct {
	Status string `json:"status"` // A, M, D or T
	Path   string `json:"path"`
}

// Staged lists what a commit would record. It refuses an index holding a submodule entry or a
// path the harness never touches, so neither can be committed by this route, and it refuses
// an empty index.
func (g *Git) Staged(ctx context.Context) ([]StagedChange, error) {
	out, err := g.ok(ctx, DefaultReadTimeout, "diff", "--cached", "--raw", "-z", "--no-renames", "--no-abbrev")
	if err != nil {
		return nil, err
	}
	tokens := strings.Split(string(out), "\x00")
	var changes []StagedChange
	for i := 0; i+1 < len(tokens); i += 2 {
		meta, path := tokens[i], tokens[i+1]
		f := strings.Fields(strings.TrimPrefix(meta, ":"))
		if len(f) < 5 {
			return nil, fmt.Errorf("%w: unexpected diff output", ErrFailed)
		}
		if f[0] == "160000" || f[1] == "160000" {
			return nil, ErrSubmodule
		}
		if harnessfs.Denied(path) || validRel(path) != nil {
			return nil, ErrProtectedPath
		}
		changes = append(changes, StagedChange{Status: f[4][:1], Path: path})
	}
	if len(changes) == 0 {
		return nil, ErrNothingStaged
	}
	return changes, nil
}

// StagedSummary returns Git's own summary of the staged changes.
func (g *Git) StagedSummary(ctx context.Context) (string, error) {
	out, err := g.ok(ctx, DefaultReadTimeout, "diff", "--cached", "--stat=100", "--no-ext-diff", "--no-textconv", "--no-color", "--no-renames", "--ignore-submodules=all")
	if err != nil {
		return "", err
	}
	return harnessexec.Sanitize(out), nil
}

// Author is the identity a commit would carry. Nothing is invented: if Git has none
// configured, the commit does not happen.
type Author struct {
	Name  string
	Email string
}

var identRE = regexp.MustCompile(`^(.*) <([^<>]*)> \d+ [+-]\d{4}$`)

func (g *Git) Identity(ctx context.Context) (Author, error) {
	res, err := g.Run(ctx, DefaultReadTimeout, "-c", "user.useConfigOnly=true", "var", "GIT_AUTHOR_IDENT")
	if err != nil {
		return Author{}, err
	}
	if res.ExitCode != 0 {
		return Author{}, ErrNoIdentity
	}
	m := identRE.FindStringSubmatch(strings.TrimSpace(string(res.Stdout)))
	if m == nil || strings.TrimSpace(m[1]) == "" || m[2] == "" {
		return Author{}, ErrNoIdentity
	}
	return Author{Name: harnessexec.Sanitize([]byte(m[1])), Email: harnessexec.Sanitize([]byte(m[2]))}, nil
}

// ---- the writes ----

// Add stages the named files. Every path is checked here as well as by the caller, and is
// passed after a separator with literal matching, so it can never be read as an option or a
// pattern.
func (g *Git) Add(ctx context.Context, paths []string) error {
	if len(paths) == 0 || len(paths) > MaxStagePaths {
		return ErrInvalid
	}
	project, err := os.OpenRoot(g.repo.Root)
	if err != nil {
		return ErrInvalid
	}
	defer project.Close()
	args := []string{"add", "--"}
	for _, p := range paths {
		if err := validRel(p); err != nil {
			return err
		}
		if g.repo.Nested(p) {
			return ErrSubmodule
		}
		// A file, or a path that is gone (staging a deletion). Never a directory, which Git
		// would stage whole, and never a link.
		if info, err := project.Lstat(p); err == nil && !info.Mode().IsRegular() {
			return ErrInvalid
		}
		args = append(args, p)
	}
	_, err = g.ok(ctx, DefaultWriteTimeout, args...)
	return err
}

// ValidMessage reports whether a commit message is acceptable: bounded, valid text, no
// control characters other than newline and tab, and not empty.
func ValidMessage(m string) bool {
	if strings.TrimSpace(m) == "" || len(m) > MaxMessageBytes || !utf8.ValidString(m) {
		return false
	}
	for _, r := range m {
		if r < 0x20 && r != '\n' && r != '\t' || r == 0x7f {
			return false
		}
	}
	return true
}

// Committed is what a commit produced.
type Committed struct {
	Commit string
	Tree   string
	Parent string // empty for a root commit
}

// Commit records the index as one commit on the current branch. It never stages anything,
// never amends, never uses an all-files option and never allows an empty commit.
func (g *Git) Commit(ctx context.Context, message string) (Committed, error) {
	if !ValidMessage(message) {
		return Committed{}, ErrInvalid
	}
	if _, err := g.Identity(ctx); err != nil {
		return Committed{}, err
	}
	res, err := g.Run(ctx, DefaultWriteTimeout, "-c", "user.useConfigOnly=true", "commit", "--quiet", "--no-edit", "--no-verify", "--cleanup=whitespace", "-m", message)
	if err != nil {
		return Committed{}, err
	}
	if res.TimedOut || res.Cancelled || res.ExitCode != 0 {
		return Committed{}, failed(res)
	}
	return g.Resolved(ctx)
}

// Resolved reads the current commit, its tree and its parent.
func (g *Git) Resolved(ctx context.Context) (Committed, error) {
	var c Committed
	var err error
	if c.Commit, err = g.Head(ctx); err != nil {
		return Committed{}, err
	}
	out, err := g.ok(ctx, DefaultReadTimeout, "rev-parse", "--verify", "HEAD^{tree}")
	if err != nil {
		return Committed{}, err
	}
	c.Tree = strings.TrimSpace(string(out))
	if res, err := g.Run(ctx, DefaultReadTimeout, "rev-parse", "--verify", "-q", "HEAD^"); err == nil && res.ExitCode == 0 {
		c.Parent = strings.TrimSpace(string(res.Stdout))
	}
	return c, nil
}

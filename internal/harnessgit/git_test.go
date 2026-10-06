package harnessgit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

var bg = context.Background()

func needGit(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git on this machine")
	}
	return path
}

// repoEnv is a real repository, a home directory with no Git configuration of its own, and a
// private temporary directory for the runner.
type repoEnv struct {
	t     *testing.T
	root  string
	home  string
	temp  string
	cfg   Config
	setup []string // the environment used to run setup commands
}

func realTemp(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func newRepo(t *testing.T) *repoEnv {
	t.Helper()
	gitPath := needGit(t)
	e := &repoEnv{t: t, root: realTemp(t), home: realTemp(t), temp: filepath.Join(realTemp(t), "tmp")}
	e.cfg = Config{SearchPath: []string{filepath.Dir(gitPath)}, Home: e.home, TempRoot: e.temp}
	e.setup = []string{"PATH=" + filepath.Dir(gitPath) + ":/usr/bin:/bin", "HOME=" + e.home, "GIT_CONFIG_NOSYSTEM=1", "LC_ALL=C"}
	e.git("init", "-q", "-b", "main")
	e.git("config", "user.name", "Test Person")
	e.git("config", "user.email", "test@example.com")
	e.write("README.md", "# project\n")
	e.write("src/app.go", "package app\n")
	e.git("add", "-A")
	e.git("commit", "-q", "-m", "initial commit")
	return e
}

// git runs a setup command directly, outside the harness.
func (e *repoEnv) git(args ...string) string {
	e.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir, cmd.Env = e.root, e.setup
	out, err := cmd.CombinedOutput()
	if err != nil {
		e.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (e *repoEnv) write(name, content string) {
	e.t.Helper()
	path := filepath.Join(e.root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

func (e *repoEnv) open() *Git {
	e.t.Helper()
	repo, err := Open(e.root)
	if err != nil {
		e.t.Fatalf("Open: %v", err)
	}
	g, err := New(bg, e.cfg, repo)
	if err != nil {
		e.t.Fatalf("New: %v", err)
	}
	return g
}

func (e *repoEnv) appendConfig(text string) {
	e.t.Helper()
	f, err := os.OpenFile(filepath.Join(e.root, ".git", "config"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		e.t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(text); err != nil {
		e.t.Fatal(err)
	}
}

// marks is a directory where a hostile program would leave evidence that it ran.
func (e *repoEnv) marks() string {
	dir := filepath.Join(e.root, "..", filepath.Base(e.root)+"-marks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		e.t.Fatal(err)
	}
	return dir
}

func (e *repoEnv) ran() []string {
	entries, _ := os.ReadDir(e.marks())
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names
}

func (e *repoEnv) trap(name string) string {
	return fmt.Sprintf("sh -c 'touch %s/%s && cat' --", e.marks(), name) // no ';': Git reads that as a comment
}

func TestARepositoryIsAcceptedOnlyWhenItIsARealDirectoryWithNoRedirection(t *testing.T) {
	e := newRepo(t)
	if _, err := Open(e.root); err != nil {
		t.Fatalf("an ordinary repository was refused: %v", err)
	}
	other := realTemp(t)
	for name, tc := range map[string]struct {
		prepare func() string
		reason  string
	}{
		"no repository":  {func() string { return realTemp(t) }, "not_a_repository"},
		"a missing root": {func() string { return filepath.Join(other, "gone") }, "root_unreadable"},
		"a gitdir file": {func() string {
			d := realTemp(t)
			os.WriteFile(filepath.Join(d, ".git"), []byte("gitdir: "+filepath.Join(e.root, ".git")+"\n"), 0o644)
			return d
		}, "git_indirect"},
		"a git link": {func() string {
			d := realTemp(t)
			os.Symlink(filepath.Join(e.root, ".git"), filepath.Join(d, ".git"))
			return d
		}, "git_indirect"},
	} {
		if _, err := Open(tc.prepare()); err == nil {
			t.Errorf("%s was accepted", name)
		} else if r, ok := IsRefusal(err); !ok || r.Reason != tc.reason {
			t.Errorf("%s: %v, want %s", name, err, tc.reason)
		}
	}
	for name, setup := range map[string]func(root string){
		"a commondir": func(root string) { os.WriteFile(filepath.Join(root, ".git", "commondir"), []byte("../x\n"), 0o644) },
		"alternates": func(root string) {
			os.WriteFile(filepath.Join(root, ".git", "objects", "info", "alternates"), []byte("/elsewhere\n"), 0o644)
		},
		"a worktree config file": func(root string) {
			os.WriteFile(filepath.Join(root, ".git", "config.worktree"), []byte("[core]\n"), 0o644)
		},
		"a linked HEAD": func(root string) {
			os.Remove(filepath.Join(root, ".git", "HEAD"))
			os.Symlink("/etc/hosts", filepath.Join(root, ".git", "HEAD"))
		},
		"a linked config": func(root string) {
			os.Remove(filepath.Join(root, ".git", "config"))
			os.Symlink("/etc/hosts", filepath.Join(root, ".git", "config"))
		},
		"linked objects": func(root string) {
			os.RemoveAll(filepath.Join(root, ".git", "objects"))
			os.Symlink(realTemp(t), filepath.Join(root, ".git", "objects"))
		},
		"linked refs": func(root string) {
			os.RemoveAll(filepath.Join(root, ".git", "refs"))
			os.Symlink(realTemp(t), filepath.Join(root, ".git", "refs"))
		},
		"a linked index": func(root string) {
			os.Remove(filepath.Join(root, ".git", "index"))
			os.Symlink("/etc/hosts", filepath.Join(root, ".git", "index"))
		},
	} {
		clone := newRepo(t)
		if err := os.MkdirAll(filepath.Join(clone.root, ".git", "objects", "info"), 0o755); err != nil {
			t.Fatal(err)
		}
		setup(clone.root)
		if _, err := Open(clone.root); err == nil {
			t.Errorf("%s was accepted", name)
		} else if r, ok := IsRefusal(err); !ok || r.Reason != "git_indirect" {
			t.Errorf("%s: %v", name, err)
		}
	}
	// A root reached through a link is judged by where it really is.
	linked := filepath.Join(realTemp(t), "via-link")
	if err := os.Symlink(e.root, linked); err != nil {
		t.Fatal(err)
	}
	if repo, err := Open(linked); err != nil || repo.Root != e.root {
		t.Fatalf("a linked root: %v %+v", err, repo)
	}
	// The configuration is checked, too, and the refusal names the setting only.
	e.appendConfig("[core]\n\tfsmonitor = " + e.trap("fsmonitor") + "\n")
	_, err := Open(e.root)
	if r, ok := IsRefusal(err); !ok || r.Reason != "config_risky" || r.Setting != "core.fsmonitor" {
		t.Fatalf("a risky configuration: %v", err)
	}
	if len(e.ran()) != 0 {
		t.Fatalf("checking a repository ran something: %v", e.ran())
	}
}

// What Git would run if the repository were trusted: each of these is refused before Git is
// ever started, and none of them leaves a trace.
func TestARepositoryThatNamesAProgramIsRefusedBeforeGitRuns(t *testing.T) {
	for name, config := range map[string]func(e *repoEnv) string{
		"a filesystem monitor": func(e *repoEnv) string { return "[core]\n\tfsmonitor = " + e.trap("fsmonitor") + "\n" },
		"a clean filter":       func(e *repoEnv) string { return "[filter \"evil\"]\n\tclean = " + e.trap("clean") + "\n" },
		"a smudge filter":      func(e *repoEnv) string { return "[filter \"evil\"]\n\tsmudge = " + e.trap("smudge") + "\n" },
		"a text conversion":    func(e *repoEnv) string { return "[diff \"evil\"]\n\ttextconv = " + e.trap("textconv") + "\n" },
		"an external diff":     func(e *repoEnv) string { return "[diff]\n\texternal = " + e.trap("extdiff") + "\n" },
		"a pager":              func(e *repoEnv) string { return "[core]\n\tpager = " + e.trap("pager") + "\n" },
		"a hooks path":         func(e *repoEnv) string { return "[core]\n\thooksPath = " + e.marks() + "/hooks\n" },
		"an alias":             func(e *repoEnv) string { return "[alias]\n\tstatus = !touch " + e.marks() + "/alias\n" },
	} {
		e := newRepo(t)
		e.write(".gitattributes", "*.txt diff=evil filter=evil\n")
		e.write("note.txt", "text\n")
		e.appendConfig(config(e))
		if _, err := Open(e.root); err == nil {
			t.Errorf("%s: the repository was accepted", name)
		}
		// Even if a caller ignored the refusal and ran Git directly, the marks show nothing
		// ran during the check itself.
		if len(e.ran()) != 0 {
			t.Errorf("%s: something ran during the check: %v", name, e.ran())
		}
	}
}

// Plain Git does run these, which is why the check exists. This documents the threat on this
// machine's Git rather than assuming it.
func TestPlainGitReallyRunsWhatTheRepositoryNames(t *testing.T) {
	e := newRepo(t)
	e.write("note.txt", "text\n")
	e.git("add", "note.txt")
	e.git("commit", "-q", "-m", "add note")
	e.write(".gitattributes", "*.txt filter=evil diff=evil\n")
	e.appendConfig("[filter \"evil\"]\n\tclean = " + e.trap("clean") + "\n[core]\n\tfsmonitor = " + e.trap("fsmonitor") + "\n")
	e.write("note.txt", "text, changed\n")
	e.git("diff")
	got := strings.Join(e.ran(), ",")
	if !strings.Contains(got, "clean") || !strings.Contains(got, "fsmonitor") {
		t.Fatalf("plain git did not run the programs the repository names (ran: %q); the premise of the check would be wrong", got)
	}
}

// Settings that are not refused because they cannot be in a repository are neutralized when
// they come from somewhere that is trusted: the person's own configuration. The overrides on
// the command line outrank it, so a monitor, hooks, a pager or an external diff named there
// still never run on the harness's behalf.
func TestOverridesNeutralizeWhatTheTrustedGlobalConfigurationNames(t *testing.T) {
	e := newRepo(t)
	hooksSource := realTemp(t) // where the global hooks live, apart from the marks directory
	global := fmt.Sprintf("[user]\n\tname = Global Person\n\temail = global@example.com\n[core]\n\tfsmonitor = %s\n\thooksPath = %s\n\tpager = %s\n[diff]\n\texternal = %s\n[diff \"evil\"]\n\ttextconv = %s\n",
		e.trap("fsmonitor"), hooksSource, e.trap("pager"), e.trap("extdiff"), e.trap("textconv"))
	if err := os.WriteFile(filepath.Join(e.home, ".gitconfig"), []byte(global), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, hook := range []string{"pre-commit", "commit-msg", "post-commit", "prepare-commit-msg"} {
		path := filepath.Join(hooksSource, hook)
		os.WriteFile(path, []byte(fmt.Sprintf("#!/bin/sh\ntouch %s/hook-%s\n", e.marks(), hook)), 0o755)
	}
	hooksDir := filepath.Join(e.root, ".git", "hooks")
	os.MkdirAll(hooksDir, 0o755)
	os.WriteFile(filepath.Join(hooksDir, "pre-commit"), []byte(fmt.Sprintf("#!/bin/sh\ntouch %s/hook-dotgit\n", e.marks())), 0o755)
	e.write(".gitattributes", "*.txt diff=evil\n")
	e.write("note.txt", "before\n")
	e.git("-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null", "-c", "diff.external=", "add", "note.txt")
	e.git("-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null", "-c", "diff.external=", "commit", "-q", "-m", "add note")
	for _, m := range e.ran() { // clear anything the setup itself produced
		os.Remove(filepath.Join(e.marks(), m))
	}
	e.write("note.txt", "after\n")
	e.write("new.txt", "brand new\n")
	// Sanity: with this configuration, plain Git does run the monitor, the diff program and the
	// text conversion, so the absence of marks below is the overrides' doing, not a broken trap.
	e.git("diff")
	for _, want := range []string{"fsmonitor", "extdiff"} {
		if !strings.Contains(strings.Join(e.ran(), ","), want) {
			t.Fatalf("plain git did not run %s with this configuration (ran %v): the test below would prove nothing", want, e.ran())
		}
	}
	for _, m := range e.ran() {
		os.Remove(filepath.Join(e.marks(), m))
	}

	g := e.open()
	if _, err := g.Status(bg); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Diff(bg, DiffOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Log(bg, 5, ""); err != nil {
		t.Fatal(err)
	}
	if err := g.Add(bg, []string{"note.txt", "new.txt"}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Diff(bg, DiffOptions{Staged: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Staged(bg); err != nil {
		t.Fatal(err)
	}
	if _, err := g.IndexTree(bg); err != nil {
		t.Fatal(err)
	}
	committed, err := g.Commit(bg, "a commit with hooks that must not run")
	if err != nil {
		t.Fatal(err)
	}
	if got := e.ran(); len(got) != 0 {
		t.Fatalf("Git ran programs the repository or configuration named: %v", got)
	}
	// The commit used the person's own identity from their configuration, never an invented one.
	author := e.git("log", "-1", "--format=%an <%ae>")
	if author != "Global Person <global@example.com>" && author != "Test Person <test@example.com>" {
		t.Fatalf("author = %q", author)
	}
	if committed.Commit == "" || committed.Tree == "" || committed.Parent == "" {
		t.Fatalf("committed = %+v", committed)
	}
}

func TestTheRunnerUsesABuiltEnvironmentAndOnlyTheRepositoryItWasGiven(t *testing.T) {
	e := newRepo(t)
	other := newRepo(t)
	other.write("only-in-other.txt", "x\n")
	other.git("add", "-A")
	// Variables that would point Git somewhere else, or change what it does, are not passed on.
	t.Setenv("GIT_DIR", filepath.Join(other.root, ".git"))
	t.Setenv("GIT_WORK_TREE", other.root)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(other.root, ".git", "index"))
	t.Setenv("GIT_AUTHOR_NAME", "Impostor")
	t.Setenv("GIT_AUTHOR_EMAIL", "impostor@example.com")
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.fsmonitor")
	t.Setenv("GIT_CONFIG_VALUE_0", e.trap("fsmonitor"))
	t.Setenv("GIT_EXTERNAL_DIFF", e.trap("extdiff"))
	t.Setenv("GIT_SSH_COMMAND", e.trap("ssh"))
	g := e.open()
	st, err := g.Status(bg)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range st.Entries {
		if strings.Contains(entry.Path, "only-in-other") {
			t.Fatal("Git looked at a different repository because of an inherited variable")
		}
	}
	if len(e.ran()) != 0 {
		t.Fatalf("an inherited variable made Git run something: %v", e.ran())
	}
	e.write("a.txt", "x\n")
	if err := g.Add(bg, []string{"a.txt"}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Commit(bg, "from the built environment"); err != nil {
		t.Fatal(err)
	}
	if author := e.git("log", "-1", "--format=%an"); author != "Test Person" {
		t.Fatalf("the author came from the inherited environment: %q", author)
	}
}

func TestTheRepositoryIsCheckedAgainBeforeEveryRun(t *testing.T) {
	e := newRepo(t)
	g := e.open()
	if _, err := g.Status(bg); err != nil {
		t.Fatal(err)
	}
	e.appendConfig("[core]\n\tfsmonitor = " + e.trap("fsmonitor") + "\n")
	for name, call := range map[string]func() error{
		"status": func() error { _, err := g.Status(bg); return err },
		"diff":   func() error { _, err := g.Diff(bg, DiffOptions{}); return err },
		"log":    func() error { _, err := g.Log(bg, 3, ""); return err },
		"head":   func() error { _, err := g.Head(bg); return err },
		"commit": func() error { _, err := g.Commit(bg, "x"); return err },
	} {
		if err := call(); err == nil {
			t.Errorf("%s ran against a repository that changed to name a program", name)
		} else if _, ok := IsRefusal(err); !ok {
			t.Errorf("%s: %v", name, err)
		}
	}
	if len(e.ran()) != 0 {
		t.Fatalf("something ran: %v", e.ran())
	}
}

func TestGitIsFoundOnlyOutsideTheProject(t *testing.T) {
	gitPath := needGit(t)
	e := newRepo(t)
	repo, _ := Open(e.root)
	inside := filepath.Join(e.root, "bin")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(gitPath)
	if err != nil {
		t.Fatal(err)
	}
	_ = data
	if err := os.WriteFile(filepath.Join(inside, "git"), []byte("#!/bin/sh\ntouch "+e.marks()+"/shadow\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := New(bg, Config{SearchPath: []string{inside}, Home: e.home, TempRoot: e.temp}, repo); !errors.Is(err, ErrNoGit) {
		t.Fatalf("a git inside the project was used: %v", err)
	}
	if len(e.ran()) != 0 {
		t.Fatal("the project's git ran")
	}
	if _, err := New(bg, Config{SearchPath: []string{filepath.Dir(gitPath)}}, repo); err == nil {
		t.Fatal("a missing temporary directory was accepted")
	}
	if _, err := New(bg, e.cfg, nil); err == nil {
		t.Fatal("a missing repository was accepted")
	}
}

func TestOurReadingOfAConfigurationMatchesGitsOnEveryFixture(t *testing.T) {
	needGit(t)
	e := newRepo(t)
	fixtures := []string{
		"[core]\n\tbare = false\n\tfilemode = true\n[user]\n\tname = X\n\temail = a@b.c\n",
		"[core] bare = false\n[user] name = X\n",
		"[CORE]\n\tFileMode = true\n",
		"[remote \"origin\"]\n\turl = https://x/y.git\n\tfetch = +refs/heads/*:refs/remotes/origin/*\n[branch \"main\"]\n\tremote = origin\n",
		"[branch.main]\n\tremote = origin\n",
		"[user]\n\tname = \"Q; # x\"\n\temail = a@b.c ; comment\n",
		"[user]\n\tname = a \\\n\t\tb\n\temail = c@d.e\n",
		"[user]\n\tname = x \\\n[filter \"z\"]\n\temail = a@b.c\n",
		"# only a comment\n; and another\n",
		"[core]\n\tbare\n\tfilemode =\n",
		"[url \"https://github.com/\"]\n\tinsteadOf = gh:\n",
		"[core]\r\n\tbare = false\r\n",
		"[remote \"Origin\"]\n\turl = https://x/y.git\n[branch \"Feature/X\"]\n\tremote = Origin\n",
		"[branch.Mixed]\n\tremote = o\n",
	}
	for i, config := range fixtures {
		path := filepath.Join(e.root, fmt.Sprintf("fixture%d.cfg", i))
		if err := os.WriteFile(path, []byte(config), 0o644); err != nil {
			t.Fatal(err)
		}
		entries, perr := parseConfig([]byte(config))
		if perr != nil {
			t.Errorf("fixture %d: our parser refused what Git accepts: %v\n%s", i, perr, config)
			continue
		}
		var ours []string
		for _, entry := range entries {
			ours = append(ours, entry.qualified())
		}
		cmd := exec.Command("git", "config", "--file", path, "--list", "--name-only", "-z")
		cmd.Env = e.setup
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("fixture %d: git could not read it: %v", i, err)
		}
		var theirs []string
		for _, name := range strings.Split(string(out), "\x00") {
			if name != "" {
				theirs = append(theirs, name)
			}
		}
		sort.Strings(ours)
		sort.Strings(theirs)
		if strings.Join(ours, "\n") != strings.Join(theirs, "\n") {
			t.Errorf("fixture %d: we read %v, Git reads %v\n%s", i, ours, theirs, config)
		}
	}
	// A disagreement is caught at the door: a file Git reads differently is refused.
	g := e.open()
	_ = g
}

func execIn(dir string, env []string, args ...string) *exec.Cmd {
	cmd := exec.Command("git", args...)
	cmd.Dir, cmd.Env = dir, env
	return cmd
}

// If the two readers disagree about a file, the repository is refused: a hostile setting would
// get through by being read differently.
func TestARepositoryWhoseConfigurationGitReadsDifferentlyIsRefused(t *testing.T) {
	e := newRepo(t)
	repo, err := Open(e.root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { configNamesHook = nil }()
	for name, hook := range map[string]func([]string) []string{
		"git sees an extra setting":   func(n []string) []string { return append(n, "core.fsmonitor") },
		"git sees a setting missing":  func(n []string) []string { return n[1:] },
		"git sees different settings": func(n []string) []string { out := append([]string(nil), n...); out[0] = "user.other"; return out },
	} {
		configNamesHook = hook
		if _, err := New(bg, e.cfg, repo); err == nil {
			t.Errorf("%s: the repository was accepted", name)
		} else if r, ok := IsRefusal(err); !ok || r.Reason != "config_mismatch" {
			t.Errorf("%s: %v", name, err)
		}
	}
	configNamesHook = nil
	if _, err := New(bg, e.cfg, repo); err != nil {
		t.Fatalf("with agreement: %v", err)
	}
}

func TestTheRunnersTemporaryDirectoryMustBePrivate(t *testing.T) {
	e := newRepo(t)
	if err := os.MkdirAll(e.temp, 0o755); err != nil {
		t.Fatal(err)
	}
	repo, _ := Open(e.root)
	if _, err := New(bg, e.cfg, repo); err == nil {
		t.Fatal("a world-readable temporary directory was used")
	}
	if err := os.Chmod(e.temp, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := New(bg, e.cfg, repo); err != nil {
		t.Fatalf("a private one: %v", err)
	}
	if entries, _ := os.ReadDir(e.temp); len(entries) != 0 {
		t.Fatalf("a process left its temporary directory behind: %v", entries)
	}
}

// A submodule has its own repository with its own configuration, which Git would read and
// run when asked for the status of the project that contains it. Status and diff ignore
// submodules, so that configuration is never consulted.
func TestStatusNeverReachesIntoASubmodulesOwnRepository(t *testing.T) {
	e := newRepo(t)
	sub := filepath.Join(e.root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(dir string, args ...string) string {
		cmd := execIn(dir, e.setup, args...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run(sub, "init", "-q", "-b", "main")
	run(sub, "config", "user.name", "S")
	run(sub, "config", "user.email", "s@example.com")
	if err := os.WriteFile(filepath.Join(sub, "f.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(sub, "add", "f.txt")
	run(sub, "commit", "-q", "-m", "inner")
	e.git("update-index", "--add", "--cacheinfo", "160000,"+run(sub, "rev-parse", "HEAD")+",sub")
	// The submodule's own configuration names a clean filter, which no command-line override
	// can switch off (command-line settings also reach the inner Git, so a monitor there would
	// be neutralized anyway; a filter is not).
	if err := os.WriteFile(filepath.Join(sub, ".gitattributes"), []byte("*.txt filter=evil\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(sub, "config", "filter.evil.clean", e.trap("clean-in-submodule"))
	if err := os.WriteFile(filepath.Join(sub, "f.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Plain Git reports the dirty submodule, which it can only do by looking inside it.
	if plain := e.git("status", "--porcelain=v2"); !strings.Contains(plain, " sub") {
		t.Fatalf("plain git did not report the submodule, so the test below proves nothing:\n%s", plain)
	}
	for _, m := range e.ran() {
		os.Remove(filepath.Join(e.marks(), m))
	}
	g := e.open()
	st, err := g.Status(bg)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := kinds2(st.Entries)["sub"]; ok {
		t.Fatalf("status looked inside the submodule: %+v", st.Entries)
	}
	d, err := g.Diff(bg, DiffOptions{})
	if err != nil || d.Paths != 0 || strings.Contains(d.Text, "Subproject") {
		t.Fatalf("diff looked inside the submodule: %+v %v", d, err)
	}
	if got := e.ran(); len(got) != 0 {
		t.Fatalf("Git reached into the submodule's repository: %v", got)
	}
}

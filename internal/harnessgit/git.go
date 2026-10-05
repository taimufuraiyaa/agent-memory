package harnessgit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/harnessexec"
)

const (
	// DefaultReadTimeout and DefaultWriteTimeout bound one Git process.
	DefaultReadTimeout  = 30 * time.Second
	DefaultWriteTimeout = 60 * time.Second
	// MaxStdout bounds the standard output kept from one Git process.
	MaxStdout = 256 << 10
)

// Config says where the Git program is and how it is run.
type Config struct {
	// SearchPath is where "git" is looked up. It is never the project.
	SearchPath []string
	// Home is the home directory Git is given, so the person's own configuration applies.
	Home string
	// TempRoot is a private directory under which each process gets an empty hooks
	// directory and a temporary directory, removed afterwards.
	TempRoot string
}

// Git runs the Git program against one accepted repository.
type Git struct {
	cfg  Config
	repo *Repo
	// Program is the resolved Git program; ProgramIdentity names it as it is now.
	Program         string
	ProgramIdentity string
}

// ErrNoGit: the Git program was not found outside the project.
var ErrNoGit = errors.New("the git program was not found")

// New resolves the Git program outside the project and binds it to an accepted repository.
func New(ctx context.Context, cfg Config, repo *Repo) (*Git, error) {
	if repo == nil || cfg.TempRoot == "" {
		return nil, errors.New("a repository and a temporary directory are required")
	}
	dirs := cfg.SearchPath
	if len(dirs) == 0 {
		dirs = filepath.SplitList(os.Getenv("PATH"))
	}
	program, err := harnessexec.Lookup("git", dirs, repo.Root)
	if err != nil {
		return nil, ErrNoGit
	}
	identity, err := harnessexec.Identity(program)
	if err != nil {
		return nil, ErrNoGit
	}
	if cfg.Home == "" {
		cfg.Home, _ = os.UserHomeDir()
	}
	g := &Git{cfg: cfg, repo: repo, Program: program, ProgramIdentity: identity}
	if err := g.verifyConfig(ctx); err != nil {
		return nil, err
	}
	return g, nil
}

// configNamesHook lets a test change what Git is said to have listed, to prove that a
// disagreement between the two readers is refused. It is nil outside tests.
var configNamesHook func([]string) []string

// verifyConfig asks Git which settings it sees in the repository's own configuration file and
// requires exactly the list this package read. Listing settings runs nothing and, for a
// named file, follows no includes. A difference means the two readers disagree about the
// file, which is how a hostile setting would slip past a hand-written parser, so the
// repository is refused.
func (g *Git) verifyConfig(ctx context.Context) error {
	file, err := os.Open(filepath.Join(g.repo.GitDir, "config"))
	if err != nil {
		return refuse("config_unreadable", "")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxConfigBytes+1))
	file.Close()
	if err != nil {
		return refuse("config_unreadable", "")
	}
	ours, err := ScanConfig(data)
	if err != nil {
		return err
	}
	res, err := g.Run(ctx, DefaultReadTimeout, "config", "--file", filepath.Join(g.repo.GitDir, "config"), "--list", "--name-only", "-z")
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return refuse("config_mismatch", "")
	}
	var theirs []string
	for _, name := range strings.Split(string(res.Stdout), "\x00") {
		if name != "" {
			theirs = append(theirs, name)
		}
	}
	if configNamesHook != nil {
		theirs = configNamesHook(theirs)
	}
	return compareNames(ours, theirs)
}

// compareNames requires the two lists to hold exactly the same names, in any order.
func compareNames(ours, theirs []string) error {
	ours, theirs = append([]string(nil), ours...), append([]string(nil), theirs...)
	sort.Strings(ours)
	sort.Strings(theirs)
	if len(ours) != len(theirs) {
		return refuse("config_mismatch", "")
	}
	for i := range ours {
		if ours[i] != theirs[i] {
			return refuse("config_mismatch", "")
		}
	}
	return nil
}

// Repo returns the repository this runs against.
func (g *Git) Repo() *Repo { return g.repo }

// overrides are set on the command line, where they outrank every configuration file,
// including the repository's own. Each one turns off a way Git starts a program or leaves
// work running after it returns. A clean filter has no override, which is why such a
// repository is refused before Git is ever run.
func overrides(hooks string) []string {
	return []string{
		"-c", "core.fsmonitor=false",
		"-c", "core.pager=cat",
		"-c", "core.editor=true",
		"-c", "core.hooksPath=" + hooks,
		"-c", "gc.auto=0",
		"-c", "maintenance.auto=false",
		"-c", "receive.autogc=false",
		"-c", "status.submoduleSummary=false",
		"-c", "submodule.recurse=false",
		"-c", "color.ui=false",
		"-c", "advice.statusHints=false",
		"-c", "diff.external=",
	}
}

func (g *Git) env(temp string) []string {
	path := filepath.Dir(g.Program) + ":/usr/bin:/bin"
	env := []string{"PATH=" + path, "LC_ALL=C", "TERM=dumb", "NO_COLOR=1", "TMPDIR=" + temp,
		"GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "GIT_PAGER=cat", "GIT_EDITOR=true", "GIT_OPTIONAL_LOCKS=0", "GIT_LITERAL_PATHSPECS=1"}
	if g.cfg.Home != "" {
		env = append(env, "HOME="+g.cfg.Home)
	}
	return env
}

// Run runs one Git subcommand with the standard overrides. args begin with the subcommand.
// Standard output is returned raw and bounded; standard error is returned sanitized.
func (g *Git) Run(ctx context.Context, timeout time.Duration, args ...string) (harnessexec.Result, error) {
	// The only option accepted before the subcommand is the one this package itself passes.
	rest := args
	for len(rest) >= 2 && rest[0] == "-c" {
		if rest[1] != "user.useConfigOnly=true" {
			return harnessexec.Result{}, errors.New("an option is not allowed")
		}
		rest = rest[2:]
	}
	if len(rest) == 0 || strings.HasPrefix(rest[0], "-") {
		return harnessexec.Result{}, errors.New("a subcommand is required")
	}
	if err := os.MkdirAll(g.cfg.TempRoot, 0o700); err != nil {
		return harnessexec.Result{}, err
	}
	if info, err := os.Lstat(g.cfg.TempRoot); err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		return harnessexec.Result{}, errors.New("unsafe temporary directory")
	}
	temp, err := os.MkdirTemp(g.cfg.TempRoot, "git-")
	if err != nil {
		return harnessexec.Result{}, err
	}
	defer os.RemoveAll(temp)
	hooks := filepath.Join(temp, "no-hooks")
	if err := os.Mkdir(hooks, 0o700); err != nil {
		return harnessexec.Result{}, err
	}
	argv := []string{"--git-dir=" + g.repo.GitDir, "--work-tree=" + g.repo.Root, "--no-pager", "--no-optional-locks", "--literal-pathspecs"}
	argv = append(argv, overrides(hooks)...)
	argv = append(argv, args...)
	// The repository may have changed since it was accepted; check again every time.
	if _, err := Open(g.repo.Root); err != nil {
		return harnessexec.Result{}, err
	}
	res, err := harnessexec.Run(ctx, harnessexec.Spec{Program: g.Program, Args: argv, Dir: g.repo.Root, Env: g.env(temp), Timeout: timeout, MaxOutput: MaxStdout, Split: true})
	if err != nil {
		return res, fmt.Errorf("git could not run: %w", err)
	}
	return res, nil
}

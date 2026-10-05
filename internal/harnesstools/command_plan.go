package harnesstools

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessexec"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessfs"
)

const (
	ToolRunCommand = "run_command"

	maxArgvItems     = 64
	maxArgvItemBytes = 1024
	maxArgvBytes     = 8 << 10
	// DefaultCommandTimeout and MaxCommandTimeout bound how long one command may run.
	DefaultCommandTimeout = 60 * time.Second
	MaxCommandTimeout     = 8 * time.Minute // under the session call limit, leaving room to stop and clean up
	// DefaultCommandOutput is how much combined output the model can receive.
	DefaultCommandOutput = 64 << 10
	maxProjectProgram    = 64 << 10
)

var programNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`)

// denied programs are refused outright, never asked about. The list is judged on the name
// the model gave and on the name the program resolves to, so a link or a rename cannot get
// one past it. Shells and launchers exist to run a string or another program; privilege and
// permission tools change what the user's account can do; network tools exist to reach
// out; deleting, moving and link-making tools bypass the reviewed edit tools; process
// killers and schedulers reach beyond the call; and version control belongs to a later stage.
var deniedPrograms = map[string]bool{}

func init() {
	for _, name := range strings.Fields(`
		sh bash zsh fish dash csh tcsh ksh ash busybox
		env xargs nohup setsid stdbuf timeout nice ionice command exec eval sudo su doas pkexec runas
		chmod chown chgrp chattr setfacl xattr
		curl wget ssh scp sftp rsync nc ncat netcat telnet ftp tftp socat nmap
		rm rmdir mv cp dd ln mkfs shred truncate unlink install ditto
		kill killall pkill skill launchctl systemctl service crontab at batch
		open osascript automator say screencapture pbcopy pbpaste
		git hg svn gh
		docker podman kubectl helm terraform
		mount umount diskutil hdiutil`) {
		deniedPrograms[name] = true
	}
}

// deniedName reports whether a program name, or the name it resolves to, is refused. Version
// suffixes are ignored so that "bash5" or "zsh-5.9" do not slip through.
func deniedName(name string) bool {
	lower := strings.ToLower(filepath.Base(name))
	if deniedPrograms[lower] {
		return true
	}
	trimmed := strings.TrimRight(lower, "0123456789.-_")
	return trimmed != lower && deniedPrograms[trimmed]
}

type runArgs struct {
	Argv           []string `json:"argv"`
	Cwd            string   `json:"cwd"`
	TimeoutSeconds int      `json:"timeout_seconds"`
}

// commandPlan is a fully resolved, not yet started command.
type commandPlan struct {
	realRoot string
	argv     []string
	program  string // absolute path of the program
	dir      string // relative working directory, "." for the root
	realDir  string // absolute working directory
	timeout  time.Duration
	entry    string // catalog identifier, empty when not cataloged
	profile  string
	identity string // what the resolved program was when it was reviewed
	escalate []string
	env      []string
	describe string
}

// CommandConfig turns the command tool on. The zero value leaves it off.
type CommandConfig struct {
	Enabled bool
	// SearchPath is where bare program names are looked up. Empty uses the harness's own PATH.
	SearchPath []string
	// Home is the home directory passed to commands so toolchain caches work. Empty uses the
	// current user's.
	Home string
	// TempRoot is a private directory under which each command gets its own temporary directory.
	TempRoot string
	// MaxOutput caps the combined output kept per command; zero uses the default.
	MaxOutput int
}

func (c CommandConfig) searchPath() []string {
	if len(c.SearchPath) > 0 {
		return c.SearchPath
	}
	return filepath.SplitList(os.Getenv("PATH"))
}

func (c CommandConfig) home() string {
	if c.Home != "" {
		return c.Home
	}
	home, _ := os.UserHomeDir()
	return home
}

func (c CommandConfig) maxOutput() int {
	if c.MaxOutput >= harnessexec.MinOutput && c.MaxOutput <= harnessexec.MaxOutputCap {
		return c.MaxOutput
	}
	return DefaultCommandOutput
}

// inside reports whether path is dir or below it.
func inside(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// lookup finds a bare program name in the search path. Empty and relative entries are
// ignored, and so is any entry inside the project, so a program a model writes into the
// project can never shadow a toolchain. It returns the final path after links are resolved.
func lookup(name string, dirs []string, realRoot string) (string, error) {
	for _, dir := range dirs {
		if dir == "" || !filepath.IsAbs(dir) {
			continue
		}
		resolvedDir, err := filepath.EvalSymlinks(dir)
		if err != nil || inside(realRoot, resolvedDir) {
			continue
		}
		candidate := filepath.Join(resolvedDir, name)
		real, err := filepath.EvalSymlinks(candidate)
		if err != nil || inside(realRoot, real) {
			continue
		}
		if info, err := os.Stat(real); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			return real, nil
		}
	}
	return "", os.ErrNotExist
}

func identityOf(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s|%d|%d", path, info.Size(), info.ModTime().UnixNano()), nil
}

// planCommand validates a call and resolves everything about it without starting anything.
func planCommand(project *harnessfs.Root, root string, cfg CommandConfig, raw []byte) (*commandPlan, harness.Outcome, string) {
	var a runArgs
	if decodeStrict(raw, &a) != nil || len(a.Argv) == 0 || len(a.Argv) > maxArgvItems || a.TimeoutSeconds < 0 || a.TimeoutSeconds > int(MaxCommandTimeout/time.Second) {
		return nil, harness.OutcomeFailed, "invalid_arguments"
	}
	total := 0
	for _, arg := range a.Argv {
		total += len(arg)
		if len(arg) > maxArgvItemBytes || !utf8.ValidString(arg) || !controlFree(arg) {
			return nil, harness.OutcomeFailed, "invalid_arguments"
		}
	}
	if total > maxArgvBytes || a.Argv[0] == "" {
		return nil, harness.OutcomeFailed, "invalid_arguments"
	}
	cwd, outcome := cleanPath(a.Cwd, true)
	if outcome != "" {
		return nil, outcome, ""
	}
	if cwd != "." { // the project root itself is always a directory
		info, err := project.Inspect(cwd)
		if err != nil {
			outcome, reason := planFailure(err)
			return nil, outcome, reason
		}
		if !info.Exists || !info.Dir {
			return nil, harness.OutcomeFailed, "bad_cwd"
		}
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, harness.OutcomeUnavailable, ""
	}
	timeout := DefaultCommandTimeout
	if a.TimeoutSeconds > 0 {
		timeout = time.Duration(a.TimeoutSeconds) * time.Second
	}
	plan := &commandPlan{realRoot: realRoot, argv: append([]string(nil), a.Argv...), dir: cwd, realDir: filepath.Join(realRoot, filepath.FromSlash(cwd)), timeout: timeout}

	name := a.Argv[0]
	var base string
	switch {
	case strings.Contains(name, "/"):
		path, identity, outcome, reason := resolveProjectProgram(project, realRoot, name)
		if outcome != "" {
			return nil, outcome, reason
		}
		plan.program, plan.identity, base = path, identity, filepath.Base(name)
		plan.escalate = append(plan.escalate, "project_program")
	case programNameRE.MatchString(name):
		if deniedName(name) {
			return nil, harness.OutcomeDenied, "program_denied"
		}
		path, err := lookup(name, cfg.searchPath(), realRoot)
		if err != nil {
			return nil, harness.OutcomeFailed, "program_not_found"
		}
		identity, err := identityOf(path)
		if err != nil {
			return nil, harness.OutcomeFailed, "program_not_found"
		}
		plan.program, plan.identity, base = path, identity, name
	default:
		return nil, harness.OutcomeFailed, "invalid_arguments"
	}
	if deniedName(base) || deniedName(plan.program) {
		return nil, harness.OutcomeDenied, "program_denied"
	}

	if len(plan.escalate) == 0 {
		if e := matchCatalog(base, a.Argv[1:]); e != nil {
			plan.entry, plan.profile, plan.describe = e.id, e.profile, e.describe
			if e.recipe {
				plan.escalate = append(plan.escalate, "runs_recipes")
			}
		}
	}
	if plan.entry == "" {
		plan.escalate = append(plan.escalate, "not_cataloged")
		plan.profile = "none"
		if inlineCode(base, a.Argv[1:]) {
			plan.escalate = append(plan.escalate, "inline_code")
		}
	}
	plan.env = buildEnv(cfg, plan)
	return plan, harness.OutcomeOK, ""
}

// resolveProjectProgram accepts only a script inside the project, named with a leading "./",
// that is a regular, executable, link-free file starting with an interpreter line, small
// enough to hash. The content hash is part of the identity, so an approval names this
// script as it is now.
func resolveProjectProgram(project *harnessfs.Root, realRoot, name string) (path, identity string, outcome harness.Outcome, reason string) {
	if !strings.HasPrefix(name, "./") || hasDotDot(name) {
		return "", "", harness.OutcomeDenied, ""
	}
	clean, outcome := cleanPath(strings.TrimPrefix(name, "./"), false)
	if outcome != "" {
		return "", "", outcome, ""
	}
	info, err := project.Inspect(clean)
	if err != nil {
		o, r := planFailure(err)
		return "", "", o, r
	}
	if !info.Exists || !info.Regular || info.Mode&0o111 == 0 {
		return "", "", harness.OutcomeFailed, "program_not_found"
	}
	data, revision, _, err := project.ReadWhole(clean)
	if err != nil {
		return "", "", harness.OutcomeFailed, "program_not_script"
	}
	if len(data) > maxProjectProgram || !strings.HasPrefix(string(data), "#!") {
		return "", "", harness.OutcomeFailed, "program_not_script"
	}
	path = filepath.Join(realRoot, filepath.FromSlash(clean))
	return path, fmt.Sprintf("%s|%s", path, revision), "", ""
}

// inlineCode reports whether an interpreter was given its program on the command line.
func inlineCode(base string, args []string) bool {
	switch strings.TrimRight(strings.ToLower(base), "0123456789.") {
	case "python", "node", "perl", "ruby", "php", "lua", "deno", "bun", "osascript":
		for _, a := range args {
			switch a {
			case "-c", "-e", "-E", "-p", "--eval", "--print", "-r", "--run", "-x":
				return true
			}
		}
	}
	return false
}

// buildEnv builds the whole environment. Nothing is inherited: a variable that is not named
// here does not exist for the command, so keys, tokens and variables that make a tool run
// another program never reach it. It reduces accidental disclosure and accidental network
// use; it is not a barrier against a command that sets out to do either.
func buildEnv(cfg CommandConfig, p *commandPlan) []string {
	path := filepath.Dir(p.program) + ":/usr/bin:/bin:/usr/sbin:/sbin"
	if p.has("project_program") {
		path = "/usr/bin:/bin:/usr/sbin:/sbin"
	}
	env := []string{"PATH=" + path, "LANG=en_US.UTF-8", "TERM=dumb", "NO_COLOR=1", "CI=1"}
	if home := cfg.home(); home != "" {
		env = append(env, "HOME="+home)
	}
	switch p.profile {
	case "go":
		env = append(env, "GOPROXY=off", "GOTOOLCHAIN=local", "GOFLAGS=")
	case "cargo":
		env = append(env, "CARGO_NET_OFFLINE=true")
	case "node":
		env = append(env, "npm_config_offline=true", "npm_config_update_notifier=false", "npm_config_audit=false", "npm_config_fund=false")
	case "python":
		env = append(env, "PYTHONDONTWRITEBYTECODE=1")
	}
	return env
}

func (p *commandPlan) has(code string) bool {
	for _, c := range p.escalate {
		if c == code {
			return true
		}
	}
	return false
}

// profileID names the environment rules in force, so the digest changes if they do.
func (p *commandPlan) profileID() string { return "env1:" + p.profile }

// digest binds the call to exactly what was reviewed.
func (p *commandPlan) digest(root string) string {
	canonical, _ := json.Marshal(struct {
		Argv     []string `json:"argv"`
		Dir      string   `json:"dir"`
		Timeout  int64    `json:"timeout_ms"`
		Identity string   `json:"identity"`
		Profile  string   `json:"profile"`
		Entry    string   `json:"entry"`
	}{p.argv, p.dir, p.timeout.Milliseconds(), p.identity, p.profileID(), p.entry})
	sum := sha256.Sum256([]byte(ToolRunCommand + "\x00" + string(canonical) + "\x00" + root))
	return "sha256:" + hex.EncodeToString(sum[:])
}

package main

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/time/timebooks/agent-memory/internal/bootstrap"
	amconfig "github.com/time/timebooks/agent-memory/internal/config"
)

type config struct {
	binDir          string
	dataDir         string
	src             string
	skipModel       bool
	skipONNXRuntime bool
	noDashboard     bool
	dashboardSrc    string
	dashboardDir    string
	writeEnvFile    bool
	uninstall       bool
	status          bool
	quiet           bool
	initHere        bool
	projectName     string
	ideTargets      stringSliceFlag
}

type stringSliceFlag []string

func (s *stringSliceFlag) String() string {
	return strings.Join(*s, ",")
}

func (s *stringSliceFlag) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	*s = append(*s, value)
	return nil
}

const (
	binName            = "agent-memory"
	modelDirName       = "all-MiniLM-L6-v2"
	modelBaseURL       = "https://huggingface.co/Xenova/all-MiniLM-L6-v2/resolve/main"
	modelMirrorBaseURL = "https://raw.githubusercontent.com/Xyntopia/all-MiniLM-L6-v2/main"
	onnxRuntimeVersion = "1.25.0"
)

type modelFile struct {
	name string
	path string
}

var modelFiles = []modelFile{
	{name: "config.json", path: "config.json"},
	{name: "tokenizer.json", path: "tokenizer.json"},
	{name: "tokenizer_config.json", path: "tokenizer_config.json"},
	{name: "special_tokens_map.json", path: "special_tokens_map.json"},
	{name: "model.onnx", path: "onnx/model_quantized.onnx"},
}

func main() {
	cfg := parseFlags()
	switch {
	case cfg.status:
		runStatus(cfg)
	case cfg.uninstall:
		runUninstall(cfg)
	default:
		runInstall(cfg)
	}
}

func parseFlags() config {
	var cfg config
	flag.StringVar(&cfg.binDir, "bin-dir", "", "install dir (default: ~/.local/bin or platform equivalent)")
	flag.StringVar(&cfg.dataDir, "data-dir", "", "data dir (default: ~/.agent-memory)")
	flag.StringVar(&cfg.src, "src", "./cmd/agent-memory", "path to the agent-memory main package")
	flag.BoolVar(&cfg.skipModel, "no-model", false, "skip downloading the MiniLM ONNX model")
	flag.BoolVar(&cfg.skipONNXRuntime, "skip-onnx-runtime", false, "skip downloading ONNX Runtime shared libraries")
	flag.BoolVar(&cfg.noDashboard, "no-dashboard", false, "skip installing the standalone dashboard (React/Vite)")
	flag.StringVar(&cfg.dashboardSrc, "dashboard-src", "./tools/agent-memory/dashboard", "path to dashboard source folder (contains package.json)")
	flag.StringVar(&cfg.dashboardDir, "dashboard-dir", "", "dashboard install dir (default: <data-dir>/dashboard)")
	flag.BoolVar(&cfg.writeEnvFile, "write-env", true, "write an env file under <data-dir>/agent-memory.env")
	flag.BoolVar(&cfg.uninstall, "uninstall", false, "remove the installed binary (data is preserved)")
	flag.BoolVar(&cfg.status, "status", false, "show install state and exit")
	flag.BoolVar(&cfg.quiet, "quiet", false, "less chatter")
	flag.BoolVar(&cfg.initHere, "init-here", false, "run per-project setup in the current directory after install (init new project, reinstall existing)")
	flag.StringVar(&cfg.projectName, "project-name", "", "project name for --init-here setup (default: cwd basename)")
	flag.Var(&cfg.ideTargets, "ide", "IDE rule targets for --init-here project setup (repeatable): cursor|antigravity|claude|aierules|cursorrules|trae|windsurfrules|generic|all")
	flag.Parse()

	if cfg.binDir == "" {
		cfg.binDir = defaultBinDir()
	}
	if cfg.dataDir == "" {
		cfg.dataDir = defaultDataDir()
	}
	if cfg.dashboardDir == "" {
		cfg.dashboardDir = filepath.Join(cfg.dataDir, "dashboard")
	}
	return cfg
}

func runInstall(cfg config) {
	header(cfg, "agent-memory installer")
	checkGo(cfg)

	step(cfg, "1/6 data directories")
	if err := ensureDataDirs(cfg); err != nil {
		die("failed to create data dirs: %v", err)
	}
	ok(cfg, "ready at %s", cfg.dataDir)

	step(cfg, "2/6 binary")
	installed, err := buildAndInstall(cfg)
	if err != nil {
		die("build/install failed: %v", err)
	}
	ok(cfg, "installed: %s", installed)
	checkPATHAdvice(cfg, filepath.Dir(installed))

	runtimePath := ""
	step(cfg, "3/6 onnx runtime")
	if cfg.skipONNXRuntime {
		info(cfg, "skipped (--skip-onnx-runtime)")
	} else if p, err := bootstrap.EnsureONNXRuntime(cfg.dataDir, cfg.quiet); err != nil {
		warn(cfg, "onnx runtime install failed: %v", err)
		warn(cfg, "semantic embeddings will stay unavailable until this succeeds")
	} else {
		runtimePath = p
		ok(cfg, "installed: %s", runtimePath)
	}

	step(cfg, "4/6 local embedding model")
	if cfg.skipModel {
		info(cfg, "skipped (--no-model)")
	} else if err := bootstrap.EnsureModel(cfg.dataDir, cfg.quiet); err != nil {
		warn(cfg, "model download failed: %v", err)
		warn(cfg, "agent-memory will work for everything except local embeddings until this succeeds")
	} else {
		ok(cfg, "ready at %s", filepath.Join(cfg.dataDir, "models", modelDirName))
	}

	step(cfg, "5/6 dashboard (React + TypeScript)")
	dashInstalled := ""
	if cfg.noDashboard {
		info(cfg, "skipped (--no-dashboard)")
	} else if err := bootstrap.EnsureDashboard(cfg.dashboardSrc, cfg.dashboardDir, streamOrDiscard(cfg), streamOrDiscard(cfg)); err != nil {
		warn(cfg, "dashboard setup failed: %v", err)
		warn(cfg, "re-run with --no-dashboard to skip, or install Node/npm and try again")
	} else {
		dashInstalled = cfg.dashboardDir
		ok(cfg, "ready at %s", dashInstalled)
	}

	if cfg.writeEnvFile {
		vars := map[string]string{
			"AGENT_MEMORY_UPGRADE_YES":     "1",
			"AGENT_MEMORY_OBSERVE_ENABLED": "1",
			"AGENT_MEMORY_ENABLED":         "1",
		}
		if strings.TrimSpace(runtimePath) != "" {
			vars["AGENT_MEMORY_ONNX_RUNTIME_PATH"] = runtimePath
		}
		if strings.TrimSpace(dashInstalled) != "" {
			vars["AGENT_MEMORY_DASHBOARD_DIR"] = cfg.dashboardDir
		}
		if root := detectRepoRoot(cfg); strings.TrimSpace(root) != "" {
			vars["AGENT_MEMORY_SRC_DIR"] = root
		}
		if err := writeEnv(cfg, vars); err != nil {
			warn(cfg, "env file write failed: %v", err)
		}
		envPath := filepath.Join(cfg.dataDir, "agent-memory.env")
		if runtime.GOOS != "windows" {
			if err := ensureShellAutoload(cfg, envPath); err != nil {
				warn(cfg, "shell setup skipped: %v", err)
			}
		}
	}

	step(cfg, "6/6 next steps")
	printNextSteps(cfg, installed)
	if cfg.initHere {
		if err := runInitHere(cfg, installed); err != nil {
			warn(cfg, "init-here failed: %v", err)
		} else {
			ok(cfg, "initialized current directory")
		}
	}
}

func runStatus(cfg config) {
	header(cfg, "agent-memory installer - status")
	binPath := filepath.Join(cfg.binDir, binNameWithExt())
	runtimePath := bootstrap.RuntimeLibraryPath(cfg.dataDir)
	modelDir := filepath.Join(cfg.dataDir, "models", modelDirName)
	modelStatus := existsLabel(filepath.Join(modelDir, "model.onnx"))
	if err := bootstrap.ValidateModelDir(modelDir); err == nil {
		modelStatus = "✓ validated " + modelDir
	}
	fmt.Fprintf(os.Stderr, "  binary      : %s\n", existsLabel(binPath))
	fmt.Fprintf(os.Stderr, "  data dir    : %s\n", existsLabel(cfg.dataDir))
	fmt.Fprintf(os.Stderr, "  runtime     : %s\n", existsLabel(runtimePath))
	fmt.Fprintf(os.Stderr, "  model       : %s\n", modelStatus)
	fmt.Fprintf(os.Stderr, "  dashboard   : %s\n", existsLabel(filepath.Join(cfg.dataDir, "dashboard", "package.json")))
	fmt.Fprintf(os.Stderr, "  env file    : %s\n", existsLabel(filepath.Join(cfg.dataDir, "agent-memory.env")))
	fmt.Fprintf(os.Stderr, "  PATH ok     : %v\n", isOnPath(cfg.binDir))
	fmt.Fprintf(os.Stderr, "  go version  : %s\n", runtime.Version())
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "  per-project setup:")
	fmt.Fprintln(os.Stderr, "    new project      : cd <project> && agent-memory init --project-name <name>")
	fmt.Fprintln(os.Stderr, "    existing project : cd <project> && agent-memory reinstall --project-name <name>")
	fmt.Fprintln(os.Stderr, "    Trae AI explicit : cd <project> && agent-memory reinstall --project-name <name> --ide trae")
}

func runUninstall(cfg config) {
	header(cfg, "agent-memory installer - uninstall (data preserved)")
	binPath := filepath.Join(cfg.binDir, binNameWithExt())
	if err := removeIfExists(binPath); err != nil {
		warn(cfg, "could not remove %s: %v", binPath, err)
		return
	}
	if fileExists(binPath) {
		warn(cfg, "binary still present (permission denied?): %s", binPath)
		return
	}
	ok(cfg, "removed binary: %s", binPath)
	info(cfg, "data preserved at %s", cfg.dataDir)
}

func checkGo(cfg config) {
	maj, min, ok := parseGoVersion(runtime.Version())
	if !ok {
		return
	}
	if maj < 1 || (maj == 1 && min < 26) {
		die("Go 1.26+ required, found %s", runtime.Version())
	}
	info(cfg, "%s ✓", runtime.Version())
}

func parseGoVersion(v string) (int, int, bool) {
	v = strings.TrimPrefix(v, "go")
	parts := strings.Split(v, ".")
	if len(parts) < 2 {
		return 0, 0, false
	}
	maj, err1 := strconv.Atoi(parts[0])
	min, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return maj, min, true
}

func ensureDataDirs(cfg config) error {
	for _, sub := range []string{"", "models", "logs", "onnxruntime"} {
		if err := os.MkdirAll(filepath.Join(cfg.dataDir, sub), 0755); err != nil {
			return err
		}
	}
	return nil
}

var errNoSource = errors.New("binary source not found")

func buildAndInstall(cfg config) (string, error) {
	if !dirExists(cfg.src) {
		return "", fmt.Errorf("%w: %s", errNoSource, cfg.src)
	}
	if err := os.MkdirAll(cfg.binDir, 0755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", cfg.binDir, err)
	}

	tmpBin := filepath.Join(cfg.binDir, ".agent-memory-install."+timestamp())
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags", "-s -w", "-o", tmpBin, cfg.src)
	cmd.Stdout = streamOrDiscard(cfg)
	cmd.Stderr = streamOrDiscard(cfg)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=1")
	if err := cmd.Run(); err != nil {
		_ = os.Remove(tmpBin)
		return "", fmt.Errorf("go build: %w", err)
	}

	finalBin := filepath.Join(cfg.binDir, binNameWithExt())
	if err := os.Rename(tmpBin, finalBin); err != nil {
		_ = os.Remove(tmpBin)
		return "", fmt.Errorf("install rename: %w", err)
	}
	if err := os.Chmod(finalBin, 0755); err != nil {
		return "", err
	}
	return finalBin, nil
}


func existsLabel(p string) string {
	if fileExists(p) || dirExists(p) {
		return "✓ " + p
	}
	return "✗ (missing) " + p
}

func timestamp() string {
	return time.Now().UTC().Format("20060102T150405")
}

func downloadFile(url, dest string) error {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "agent-memory-installer/0.1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}

	tmp := dest + ".part"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	h := sha256.New()
	w := io.MultiWriter(f, h)
	if _, err := io.Copy(w, resp.Body); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if fileExists(dest) {
		return nil
	}
	return fmt.Errorf("download produced no file: %s", dest)
}

func streamOrDiscard(cfg config) io.Writer {
	if cfg.quiet {
		return io.Discard
	}
	return os.Stderr
}

func header(cfg config, msg string) {
	if cfg.quiet {
		return
	}
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "— "+msg+" —")
}

func step(cfg config, msg string) {
	if cfg.quiet {
		return
	}
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "▶ "+msg)
}

func ok(cfg config, format string, a ...any) {
	if cfg.quiet {
		return
	}
	fmt.Fprintf(os.Stderr, "  ✓ "+format+"\n", a...)
}

func info(cfg config, format string, a ...any) {
	if cfg.quiet {
		return
	}
	fmt.Fprintf(os.Stderr, "    "+format+"\n", a...)
}

func warn(cfg config, format string, a ...any) {
	_ = cfg
	fmt.Fprintf(os.Stderr, "  ! "+format+"\n", a...)
}

func die(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "ERROR: "+format+"\n", a...)
	os.Exit(1)
}

func printNextSteps(cfg config, binPath string) {
	if cfg.quiet {
		return
	}
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Next steps:")
	fmt.Fprintf(os.Stderr, "  1) Confirm:   %s --help\n", binPath)
	fmt.Fprintln(os.Stderr, "  2) Wire a project (run inside each repo you want to enable):")
	fmt.Fprintln(os.Stderr, "       cd <project>")
	fmt.Fprintln(os.Stderr, "       agent-memory init --project-name <name>        # new project")
	fmt.Fprintln(os.Stderr, "       agent-memory reinstall --project-name <name>   # existing project")
	fmt.Fprintln(os.Stderr, "       agent-memory reinstall --project-name <name> --ide trae")
	fmt.Fprintln(os.Stderr, "     Or do it now: go run install.go --init-here --ide trae")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "  3) Dashboard env (standalone React UI):")
	if runtime.GOOS == "windows" {
		fmt.Fprintln(os.Stderr, "       setx AGENT_MEMORY_DASHBOARD_DIR \"<path-to-dashboard>\"")
		fmt.Fprintf(os.Stderr, "     Current: %s\n", cfg.dashboardDir)
	} else {
		fmt.Fprintf(os.Stderr, "       export AGENT_MEMORY_DASHBOARD_DIR=%q\n", cfg.dashboardDir)
		if cfg.writeEnvFile {
			fmt.Fprintf(os.Stderr, "     Auto-added to shell rc (restart terminal if needed): %q\n", filepath.Join(cfg.dataDir, "agent-memory.env"))
		}
	}
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "  4) Inspect adaptive runtime tuning:")
	fmt.Fprintln(os.Stderr, "       agent-memory tuning")
}

func detectRepoRoot(cfg config) string {
	cwd, err := os.Getwd()
	if err == nil && fileExists(filepath.Join(cwd, "go.mod")) && fileExists(filepath.Join(cwd, "cmd", "agent-memory", "main.go")) {
		if abs, err := filepath.Abs(cwd); err == nil {
			return abs
		}
		return cwd
	}
	if strings.TrimSpace(cfg.src) == "" {
		return ""
	}
	src := cfg.src
	if abs, err := filepath.Abs(src); err == nil {
		src = abs
	}
	if filepath.Base(src) == "agent-memory" && filepath.Base(filepath.Dir(src)) == "cmd" {
		return filepath.Dir(filepath.Dir(src))
	}
	return ""
}

func writeEnv(cfg config, vars map[string]string) error {
	envPath := filepath.Join(cfg.dataDir, "agent-memory.env")
	merged, err := mergeEnvFile(envPath, vars)
	if err != nil {
		return err
	}
	if strings.TrimSpace(merged) == "" {
		return nil
	}
	return os.WriteFile(envPath, []byte(merged), 0644)
}

func mergeEnvFile(path string, vars map[string]string) (string, error) {
	existing := ""
	if b, err := os.ReadFile(path); err == nil {
		existing = string(b)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	lines := []string{}
	if existing != "" {
		lines = strings.Split(strings.ReplaceAll(existing, "\r\n", "\n"), "\n")
	}

	index := map[string]int{}
	for i, ln := range lines {
		k, _, ok := parseEnvAssignment(ln)
		if !ok {
			continue
		}
		if _, exists := index[k]; !exists {
			index[k] = i
		}
	}

	for k, v := range vars {
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if k == "" || v == "" {
			continue
		}
		newLine := formatEnvAssignment(k, v)
		if at, ok := index[k]; ok {
			lines[at] = newLine
		} else {
			lines = append(lines, newLine)
			index[k] = len(lines) - 1
		}
	}

	out := strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n"
	out = amconfig.EnsureAdaptiveTuningEnvGuidance(out)
	return out, nil
}

func parseEnvAssignment(line string) (string, string, bool) {
	ln := strings.TrimSpace(line)
	if ln == "" {
		return "", "", false
	}
	if strings.HasPrefix(ln, "#") {
		return "", "", false
	}
	if strings.HasPrefix(ln, "export ") {
		ln = strings.TrimSpace(strings.TrimPrefix(ln, "export "))
	}
	if strings.HasPrefix(strings.ToLower(ln), "set ") {
		ln = strings.TrimSpace(ln[4:])
	}
	parts := strings.SplitN(ln, "=", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	k := strings.TrimSpace(parts[0])
	v := strings.TrimSpace(parts[1])
	if k == "" {
		return "", "", false
	}
	v = strings.Trim(v, `"'`)
	return k, v, true
}

func runInitHere(cfg config, binPath string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	name := cfg.projectName
	if strings.TrimSpace(name) == "" {
		name = filepath.Base(cwd)
	}
	if err := runProjectSetupCommand(cfg, cwd, binPath, projectSetupArgs(cfg, "init", name)...); err == nil {
		return nil
	} else if strings.Contains(err.Error(), "project already exists") {
		info(cfg, "project already exists; running reinstall to repair IDE files")
		args := append(projectSetupArgs(cfg, "reinstall", name), "--force=true")
		return runProjectSetupCommand(cfg, cwd, binPath, args...)
	} else {
		return err
	}
}

func projectSetupArgs(cfg config, command, name string) []string {
	args := []string{command, "--base-dir", cfg.dataDir, "--project-name", name}
	for _, ide := range cfg.ideTargets {
		args = append(args, "--ide", ide)
	}
	return args
}

func runProjectSetupCommand(cfg config, cwd, binPath string, args ...string) error {
	cmd := exec.Command(binPath, args...)
	cmd.Stdout = streamOrDiscard(cfg)
	var stderr bytes.Buffer
	if cfg.quiet {
		cmd.Stderr = &stderr
	} else {
		cmd.Stderr = io.MultiWriter(os.Stderr, &stderr)
	}
	cmd.Dir = cwd
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return err
		}
		return fmt.Errorf("%s: %w", msg, err)
	}
	return nil
}

func isOnPath(dir string) bool {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	for _, p := range filepath.SplitList(os.Getenv("PATH")) {
		pAbs, err := filepath.Abs(p)
		if err == nil && pAbs == abs {
			return true
		}
	}
	return false
}

func removeIfExists(p string) error {
	if !fileExists(p) {
		return nil
	}
	return os.Remove(p)
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

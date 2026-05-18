package main

import (
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
)

type config struct {
	binDir       string
	dataDir      string
	src          string
	skipModel    bool
	noDashboard  bool
	dashboardSrc string
	dashboardDir string
	writeEnvFile bool
	uninstall    bool
	status       bool
	quiet        bool
	initHere     bool
	projectName  string
}

const (
	binName      = "agent-memory"
	modelDirName = "all-MiniLM-L6-v2"
	modelBaseURL = "https://huggingface.co/Xenova/all-MiniLM-L6-v2/resolve/main"
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
	flag.BoolVar(&cfg.noDashboard, "no-dashboard", false, "skip installing the standalone dashboard (React/Vite)")
	flag.StringVar(&cfg.dashboardSrc, "dashboard-src", "./tools/agent-memory/dashboard", "path to dashboard source folder (contains package.json)")
	flag.StringVar(&cfg.dashboardDir, "dashboard-dir", "", "dashboard install dir (default: <data-dir>/dashboard)")
	flag.BoolVar(&cfg.writeEnvFile, "write-env", true, "write an env file under <data-dir>/agent-memory.env")
	flag.BoolVar(&cfg.uninstall, "uninstall", false, "remove the installed binary (data is preserved)")
	flag.BoolVar(&cfg.status, "status", false, "show install state and exit")
	flag.BoolVar(&cfg.quiet, "quiet", false, "less chatter")
	flag.BoolVar(&cfg.initHere, "init-here", false, "run per-project init in the current directory after install")
	flag.StringVar(&cfg.projectName, "project-name", "", "project name for --init-here (default: cwd basename)")
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

	step(cfg, "1/4 data directories")
	if err := ensureDataDirs(cfg); err != nil {
		die("failed to create data dirs: %v", err)
	}
	ok(cfg, "ready at %s", cfg.dataDir)

	step(cfg, "2/4 binary")
	installed, err := buildAndInstall(cfg)
	if err != nil {
		die("build/install failed: %v", err)
	}
	ok(cfg, "installed: %s", installed)
	checkPATH(cfg, filepath.Dir(installed))

	step(cfg, "3/4 local embedding model")
	if cfg.skipModel {
		info(cfg, "skipped (--no-model)")
	} else if err := ensureModel(cfg); err != nil {
		warn(cfg, "model download failed: %v", err)
		warn(cfg, "agent-memory will work for everything except local embeddings until this succeeds")
	} else {
		ok(cfg, "ready at %s", filepath.Join(cfg.dataDir, "models", modelDirName))
	}

	step(cfg, "4/5 dashboard (React + TypeScript)")
	dashInstalled := ""
	if cfg.noDashboard {
		info(cfg, "skipped (--no-dashboard)")
	} else if err := ensureDashboard(cfg); err != nil {
		warn(cfg, "dashboard setup failed: %v", err)
		warn(cfg, "re-run with --no-dashboard to skip, or install Node/npm and try again")
	} else {
		dashInstalled = cfg.dashboardDir
		ok(cfg, "ready at %s", dashInstalled)
	}

	if cfg.writeEnvFile {
		vars := map[string]string{
			"AGENT_MEMORY_UPGRADE_YES": "1",
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
		if runtime.GOOS != "windows" {
			if err := ensureShellAutoload(cfg); err != nil {
				warn(cfg, "shell setup skipped: %v", err)
			}
		}
	}

	step(cfg, "5/5 next steps")
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
	fmt.Fprintf(os.Stderr, "  binary      : %s\n", existsLabel(binPath))
	fmt.Fprintf(os.Stderr, "  data dir    : %s\n", existsLabel(cfg.dataDir))
	fmt.Fprintf(os.Stderr, "  model       : %s\n", existsLabel(filepath.Join(cfg.dataDir, "models", modelDirName, "model.onnx")))
	fmt.Fprintf(os.Stderr, "  dashboard   : %s\n", existsLabel(filepath.Join(cfg.dataDir, "dashboard", "package.json")))
	fmt.Fprintf(os.Stderr, "  env file    : %s\n", existsLabel(filepath.Join(cfg.dataDir, "agent-memory.env")))
	fmt.Fprintf(os.Stderr, "  PATH ok     : %v\n", isOnPath(cfg.binDir))
	fmt.Fprintf(os.Stderr, "  go version  : %s\n", runtime.Version())
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "  per-project setup:  cd <project> && agent-memory init --project-name <name>")
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
	for _, sub := range []string{"", "models", "logs"} {
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

func ensureModel(cfg config) error {
	target := filepath.Join(cfg.dataDir, "models", modelDirName)
	if err := os.MkdirAll(filepath.Join(target, "onnx"), 0755); err != nil {
		return err
	}
	for _, mf := range modelFiles {
		var local string
		if strings.HasPrefix(mf.path, "onnx/") {
			local = filepath.Join(target, "model.onnx")
		} else {
			local = filepath.Join(target, mf.name)
		}
		if fileExists(local) {
			info(cfg, "%s (cached)", mf.name)
			continue
		}
		url := modelBaseURL + "/" + mf.path
		info(cfg, "%s ↓", mf.name)
		if err := downloadFile(url, local); err != nil {
			_ = os.Remove(local)
			return fmt.Errorf("download %s: %w", mf.name, err)
		}
	}
	return nil
}

func ensureDashboard(cfg config) error {
	src := cfg.dashboardSrc
	if !dirExists(src) {
		return fmt.Errorf("dashboard source not found: %s", src)
	}
	if !fileExists(filepath.Join(src, "package.json")) {
		return fmt.Errorf("dashboard package.json not found: %s", src)
	}
	if _, err := exec.LookPath("npm"); err != nil {
		return errors.New("npm not found (install Node.js to use the standalone dashboard)")
	}

	dst := cfg.dashboardDir
	if strings.TrimSpace(dst) == "" {
		return errors.New("dashboard dir is required")
	}
	if err := os.MkdirAll(dst, 0755); err != nil {
		return err
	}
	if err := copyDir(dst, src); err != nil {
		return err
	}
	cmd := exec.Command("npm", "ci")
	cmd.Stdout = streamOrDiscard(cfg)
	cmd.Stderr = streamOrDiscard(cfg)
	cmd.Dir = dst
	if err := cmd.Run(); err != nil {
		return err
	}
	return nil
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
	var b strings.Builder
	for k, v := range vars {
		if strings.TrimSpace(k) == "" || strings.TrimSpace(v) == "" {
			continue
		}
		if runtime.GOOS == "windows" {
			b.WriteString("set ")
			b.WriteString(k)
			b.WriteString("=")
			b.WriteString(v)
			b.WriteString("\r\n")
		} else {
			b.WriteString("export ")
			b.WriteString(k)
			b.WriteString("=")
			b.WriteString(strconv.Quote(v))
			b.WriteString("\n")
		}
	}
	if b.Len() == 0 {
		return nil
	}
	return os.WriteFile(envPath, []byte(b.String()), 0644)
}

func ensureShellAutoload(cfg config) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	envPath := filepath.Join(cfg.dataDir, "agent-memory.env")
	if !fileExists(envPath) {
		return errors.New("env file not found")
	}
	shell := strings.TrimSpace(os.Getenv("SHELL"))
	var rc string
	switch {
	case strings.Contains(shell, "zsh"):
		rc = filepath.Join(home, ".zshrc")
	case strings.Contains(shell, "bash"):
		rc = filepath.Join(home, ".bashrc")
	default:
		rc = filepath.Join(home, ".zshrc")
	}

	snippet := fmt.Sprintf("\n# agent-memory (managed)\nif [ -f %q ]; then\n  source %q\nfi\n", envPath, envPath)
	if b, err := os.ReadFile(rc); err == nil {
		if strings.Contains(string(b), "agent-memory (managed)") || strings.Contains(string(b), envPath) {
			return nil
		}
		f, err := os.OpenFile(rc, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		_, err = f.WriteString(snippet)
		return err
	}

	f, err := os.OpenFile(rc, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = f.WriteString(snippet)
	return err
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
	cmd := exec.Command(binPath, "init", "--base-dir", cfg.dataDir, "--project-name", name)
	cmd.Stdout = streamOrDiscard(cfg)
	cmd.Stderr = streamOrDiscard(cfg)
	cmd.Dir = cwd
	return cmd.Run()
}

func defaultBinDir() string {
	if runtime.GOOS == "windows" {
		if v := os.Getenv("LOCALAPPDATA"); v != "" {
			return filepath.Join(v, "Programs", "agent-memory")
		}
	}
	if v := os.Getenv("HOME"); v != "" {
		return filepath.Join(v, ".local", "bin")
	}
	cwd, _ := os.Getwd()
	return cwd
}

func defaultDataDir() string {
	if runtime.GOOS == "windows" {
		if v := os.Getenv("LOCALAPPDATA"); v != "" {
			return filepath.Join(v, "agent-memory")
		}
	}
	if v := os.Getenv("HOME"); v != "" {
		return filepath.Join(v, ".agent-memory")
	}
	cwd, _ := os.Getwd()
	return filepath.Join(cwd, ".agent-memory")
}

func binNameWithExt() string {
	if runtime.GOOS == "windows" {
		return binName + ".exe"
	}
	return binName
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

func checkPATH(cfg config, dir string) {
	if isOnPath(dir) {
		return
	}
	warn(cfg, "%s is not on $PATH", dir)
	switch runtime.GOOS {
	case "windows":
		warn(cfg, "add it via: System Properties -> Environment Variables -> User PATH")
	default:
		warn(cfg, "add to your shell rc: export PATH=\"%s:$PATH\"", dir)
	}
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func removeIfExists(p string) error {
	if !fileExists(p) {
		return nil
	}
	return os.Remove(p)
}

func copyDir(dst, src string) error {
	dst = filepath.Clean(dst)
	src = filepath.Clean(src)
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if name == "node_modules" || name == "dist" || name == ".git" {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0755)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer func() { _ = in.Close() }()
		outPath := filepath.Join(dst, rel)
		if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
			return err
		}
		out, err := os.OpenFile(outPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, in)
		closeErr := out.Close()
		if copyErr != nil {
			_ = os.Remove(outPath)
			return copyErr
		}
		if closeErr != nil {
			_ = os.Remove(outPath)
			return closeErr
		}
		return nil
	})
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
	fmt.Fprintln(os.Stderr, "       agent-memory init --project-name <name>")
	fmt.Fprintln(os.Stderr, "     Or do it now: go run install.go --init-here")
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
}

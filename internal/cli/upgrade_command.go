package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/time/timebooks/agent-memory/internal/workspace"
)

type upgradeResult struct {
	FromVersion      string                           `json:"from_version"`
	ToSpecifier      string                           `json:"to_specifier"`
	Module           string                           `json:"module"`
	Method           string                           `json:"method"`
	SourceDir        string                           `json:"source_dir,omitempty"`
	TargetPath       string                           `json:"target_path"`
	InstalledFrom    string                           `json:"installed_from,omitempty"`
	Replaced         bool                             `json:"replaced"`
	AgentFiles       *workspace.WriteAgentFilesResult `json:"agent_files,omitempty"`
	DashboardUpdated bool                             `json:"dashboard_updated,omitempty"`
	DashboardDir     string                           `json:"dashboard_dir,omitempty"`
	DashboardSource  string                           `json:"dashboard_source,omitempty"`
	DashboardError   string                           `json:"dashboard_error,omitempty"`
	EnvFile          string                           `json:"env_file,omitempty"`
	EnvUpdated       bool                             `json:"env_updated,omitempty"`
	EnvError         string                           `json:"env_error,omitempty"`
}

func validateTextOrJSONFormat(s string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "text":
		return "text", nil
	case "json":
		return "json", nil
	default:
		return "", fmt.Errorf("invalid format: allowed values are json|text")
	}
}

func envBool(key string) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return false
	}
	switch strings.ToLower(v) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func binNameWithExt(name string) string {
	if runtime.GOOS == "windows" && !strings.HasSuffix(strings.ToLower(name), ".exe") {
		return name + ".exe"
	}
	return name
}

func runAndCapture(cmd *exec.Cmd) (string, string, error) {
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func resolveGoBinDir(ctxPath string) (string, error) {
	cmd := exec.Command("go", "env", "GOBIN")
	out, _, err := runAndCapture(cmd)
	if err == nil {
		v := strings.TrimSpace(out)
		if v != "" {
			return v, nil
		}
	}

	cmd = exec.Command("go", "env", "GOPATH")
	out, _, err = runAndCapture(cmd)
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return "", errors.New("go is required for upgrade (not found on PATH)")
		}
		return "", fmt.Errorf("go env GOPATH failed: %w", err)
	}
	gopath := strings.TrimSpace(out)
	if gopath == "" {
		return "", errors.New("go env GOPATH returned empty")
	}
	return filepath.Join(gopath, "bin"), nil
}

func findSourceRoot(start string) string {
	if strings.TrimSpace(start) == "" {
		return ""
	}
	dir := start
	for i := 0; i < 12; i++ {
		goMod := filepath.Join(dir, "go.mod")
		cmdMain := filepath.Join(dir, "cmd", "agent-memory", "main.go")
		if fileExists(goMod) && fileExists(cmdMain) {
			return dir
		}
		next := filepath.Dir(dir)
		if next == dir {
			break
		}
		dir = next
	}
	return ""
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

func buildFromSource(root string, outPath string) (string, error) {
	tmpDir := filepath.Dir(outPath)
	tmp := filepath.Join(tmpDir, ".agent-memory-build."+time.Now().UTC().Format("20060102T150405Z"))
	buildCmd := exec.Command("go", "build", "-trimpath", "-ldflags", "-s -w", "-o", tmp, "./cmd/agent-memory")
	buildCmd.Dir = root
	_, errOut, err := runAndCapture(buildCmd)
	if err != nil {
		_ = os.Remove(tmp)
		if strings.TrimSpace(errOut) != "" {
			return "", fmt.Errorf("go build failed: %s", strings.TrimSpace(errOut))
		}
		return "", fmt.Errorf("go build failed: %w", err)
	}
	return tmp, nil
}

func replaceFileAtomic(dst string, src string) error {
	if strings.TrimSpace(dst) == "" || strings.TrimSpace(src) == "" {
		return errors.New("dst and src are required")
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".upgrade." + time.Now().UTC().Format("20060102T150405Z")
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

func defaultAgentMemoryDataDir() string {
	if runtime.GOOS == "windows" {
		if v := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); v != "" {
			return filepath.Join(v, "agent-memory")
		}
	}
	if v := strings.TrimSpace(os.Getenv("HOME")); v != "" {
		return filepath.Join(v, ".agent-memory")
	}
	cwd, _ := os.Getwd()
	return filepath.Join(cwd, ".agent-memory")
}

func resolveDashboardDir(override string) string {
	if v := strings.TrimSpace(override); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("AGENT_MEMORY_DASHBOARD_DIR")); v != "" {
		return v
	}
	return filepath.Join(defaultAgentMemoryDataDir(), "dashboard")
}

func copyDir(dst, src string) error {
	dstAbs, err := filepath.Abs(dst)
	if err != nil {
		return err
	}
	srcAbs, err := filepath.Abs(src)
	if err != nil {
		return err
	}
	if dstAbs == srcAbs {
		return nil
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}

	return filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
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
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer func() { _ = in.Close() }()
		outPath := filepath.Join(dst, rel)
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			return err
		}
		out, err := os.OpenFile(outPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
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

func updateStandaloneDashboardFromSource(srcRoot, dstDir string) (string, error) {
	if strings.TrimSpace(srcRoot) == "" {
		return "", errors.New("source dir is required")
	}
	if strings.TrimSpace(dstDir) == "" {
		return "", errors.New("dashboard dir is required")
	}
	src := filepath.Join(srcRoot, "tools", "agent-memory", "dashboard")
	if st, err := os.Stat(src); err != nil || !st.IsDir() {
		return src, fmt.Errorf("dashboard source not found: %s", src)
	}
	if !fileExists(filepath.Join(src, "package.json")) {
		return src, fmt.Errorf("dashboard source package.json missing: %s", src)
	}
	if _, err := exec.LookPath("npm"); err != nil {
		return src, errors.New("npm is required to refresh the standalone dashboard")
	}
	if err := copyDir(dstDir, src); err != nil {
		return src, err
	}
	ci := exec.Command("npm", "ci")
	ci.Dir = dstDir
	_, errOut, err := runAndCapture(ci)
	if err != nil {
		msg := strings.TrimSpace(errOut)
		if msg == "" {
			msg = err.Error()
		}
		return src, fmt.Errorf("npm ci failed: %s", msg)
	}
	return src, nil
}

func newUpgradeCommand() *cobra.Command {
	var format string
	var module string
	var to string
	var target string
	var srcDir string
	var yes bool
	var dryRun bool
	var hooksOnly bool
	var forceHooks bool
	var noHooks bool
	var noDashboard bool
	var dashboardDir string

	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Upgrade agent-memory binary and push hippocampus hooks to the current project",
		Long: `Upgrade the agent-memory binary and write the hippocampus hook files
(.kiro/hooks/memory-recall-gate.json and .kiro/hooks/memory-consolidation-gate.json)
into the current project directory.

Hooks are always written by default. Use --no-hooks to skip them.
Use --hooks-only to push hooks without touching the binary (useful for existing projects).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			f, err := validateTextOrJSONFormat(format)
			if err != nil {
				return err
			}

			cwd, err := os.Getwd()
			if err != nil {
				return err
			}

			writeAgentFiles := func(force bool) (*workspace.WriteAgentFilesResult, error) {
				return workspace.WriteAgentFiles(workspace.WriteAgentFilesOptions{
					CWD:   cwd,
					Force: force,
				})
			}

			printAgentFiles := func(af *workspace.WriteAgentFilesResult) {
				for _, ide := range af.IDEs {
					if len(ide.Written) > 0 {
						_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  [%s] written: %s\n", ide.IDE, strings.Join(ide.Written, ", "))
					}
					if len(ide.Skipped) > 0 {
						_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  [%s] skipped (up-to-date): %s\n", ide.IDE, strings.Join(ide.Skipped, ", "))
					}
				}
			}

			// --hooks-only: just write agent files, skip binary upgrade entirely.
			if hooksOnly {
				af, err := writeAgentFiles(forceHooks)
				if err != nil {
					return fmt.Errorf("agent files: %w", err)
				}
				res := upgradeResult{AgentFiles: af}
				if f == "json" {
					return writeSuccessEnvelope(cmd.OutOrStdout(), "upgrade", res)
				}
				printAgentFiles(af)
				return nil
			}

			v := collectVersionInfo()
			if strings.TrimSpace(module) == "" {
				module = "github.com/time/timebooks/agent-memory/cmd/agent-memory"
			}
			if strings.TrimSpace(to) == "" {
				to = "latest"
			}
			spec := module + "@" + to
			if strings.TrimSpace(target) == "" {
				exe, err := os.Executable()
				if err != nil {
					return err
				}
				target = exe
			}

			if strings.TrimSpace(srcDir) == "" {
				if v := strings.TrimSpace(os.Getenv("AGENT_MEMORY_SRC_DIR")); v != "" {
					srcDir = v
				}
			}
			if strings.TrimSpace(srcDir) == "" {
				srcDir = findSourceRoot(cwd)
			}

			method := "go-install"
			installedFrom := ""

			if strings.TrimSpace(srcDir) != "" {
				method = "go-build"
				built, err := buildFromSource(srcDir, target)
				if err != nil {
					return err
				}
				installedFrom = built
			} else {
				binDir, err := resolveGoBinDir(target)
				if err != nil {
					return err
				}
				installCmd := exec.Command("go", "install", spec)
				_, installErrOut, err := runAndCapture(installCmd)
				if err != nil {
					msg := strings.TrimSpace(installErrOut)
					if msg == "" {
						msg = err.Error()
					}
					return fmt.Errorf("go install failed: %s (hint: run from a source checkout, or pass --src <repo-root>)", msg)
				}
				installedFrom = filepath.Join(binDir, binNameWithExt("agent-memory"))
				if _, err := os.Stat(installedFrom); err != nil {
					return fmt.Errorf("installed binary not found after go install: %s", installedFrom)
				}
			}

			res := upgradeResult{
				FromVersion:   v.Version,
				ToSpecifier:   to,
				Module:        module,
				Method:        method,
				SourceDir:     srcDir,
				TargetPath:    target,
				InstalledFrom: installedFrom,
			}

			if dryRun {
				if !noHooks {
					af, err := writeAgentFiles(forceHooks)
					if err != nil {
						return fmt.Errorf("agent files: %w", err)
					}
					res.AgentFiles = af
				}
				if f == "json" {
					return writeSuccessEnvelope(cmd.OutOrStdout(), "upgrade", res)
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "dry-run: would replace %s (from %s)\n", target, spec)
				if res.AgentFiles != nil {
					printAgentFiles(res.AgentFiles)
				}
				return nil
			}

			if !yes && envBool("AGENT_MEMORY_UPGRADE_YES") {
				yes = true
			}
			if !yes {
				return errors.New("upgrade requires --yes / -y (or set AGENT_MEMORY_UPGRADE_YES=1, or use --dry-run)")
			}

			if err := replaceFileAtomic(target, installedFrom); err != nil {
				return fmt.Errorf("replace failed: %w", err)
			}
			res.Replaced = true
			if method == "go-build" {
				_ = os.Remove(installedFrom)
			}

			if !noHooks {
				af, err := writeAgentFiles(forceHooks)
				if err != nil {
					return fmt.Errorf("agent files: %w", err)
				}
				res.AgentFiles = af
			}

			if !noDashboard && strings.TrimSpace(srcDir) != "" {
				dst := resolveDashboardDir(dashboardDir)
				res.DashboardDir = dst
				src, err := updateStandaloneDashboardFromSource(srcDir, dst)
				res.DashboardSource = src
				if err != nil {
					res.DashboardError = err.Error()
				} else {
					res.DashboardUpdated = true
				}
			}

			if envPath, updated, err := ensureEnvVarIfPresent("AGENT_MEMORY_OBSERVE_ENABLED", "1"); err != nil {
				res.EnvFile = envPath
				res.EnvError = err.Error()
			} else if updated {
				res.EnvFile = envPath
				res.EnvUpdated = true
			} else if envPath != "" {
				res.EnvFile = envPath
			}
			if envPath, updated, err := ensureEnvVarIfPresent("AGENT_MEMORY_ENABLED", "1"); err != nil {
				res.EnvFile = envPath
				res.EnvError = err.Error()
			} else if updated {
				res.EnvFile = envPath
				res.EnvUpdated = true
			} else if envPath != "" {
				res.EnvFile = envPath
			}

			if f == "json" {
				return writeSuccessEnvelope(cmd.OutOrStdout(), "upgrade", res)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "upgraded: %s\n", target)
			if res.AgentFiles != nil {
				printAgentFiles(res.AgentFiles)
			}
			if res.DashboardUpdated {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  dashboard updated: %s\n", res.DashboardDir)
			} else if strings.TrimSpace(res.DashboardError) != "" {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  dashboard update skipped: %s\n", res.DashboardError)
			}
			if res.EnvUpdated {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  env updated: %s\n", res.EnvFile)
			} else if strings.TrimSpace(res.EnvError) != "" {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  env update skipped: %s\n", res.EnvError)
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&format, "format", "f", "text", "Output format: json|text")
	cmd.Flags().StringVar(&module, "module", "github.com/time/timebooks/agent-memory/cmd/agent-memory", "Go module path to install (advanced)")
	cmd.Flags().StringVar(&to, "to", "latest", "Go version specifier (e.g. latest, v1.2.3)")
	cmd.Flags().StringVar(&target, "target", "", "Target path to replace (default: current executable)")
	cmd.Flags().StringVar(&srcDir, "src", "", "Build from local source checkout (repo root containing go.mod and cmd/agent-memory)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Confirm replacing the target binary")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would happen without replacing anything")
	cmd.Flags().BoolVar(&hooksOnly, "hooks-only", false, "Only write hippocampus hook files, skip binary upgrade")
	cmd.Flags().BoolVar(&noHooks, "no-hooks", false, "Skip writing hippocampus hook files")
	cmd.Flags().BoolVar(&forceHooks, "force-hooks", false, "Overwrite hook files even if already up-to-date")
	cmd.Flags().BoolVar(&noDashboard, "no-dashboard", false, "Skip refreshing standalone dashboard from source checkout")
	cmd.Flags().StringVar(&dashboardDir, "dashboard-dir", "", "Dashboard install dir (default: $AGENT_MEMORY_DASHBOARD_DIR or ~/.agent-memory/dashboard)")
	return cmd
}

func ensureEnvVarIfPresent(key, value string) (string, bool, error) {
	envPath := filepath.Join(defaultAgentMemoryDataDir(), "agent-memory.env")
	b, err := os.ReadFile(envPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return envPath, false, err
	}
	content := strings.ReplaceAll(string(b), "\r\n", "\n")
	lines := strings.Split(content, "\n")

	found := false
	for _, ln := range lines {
		k, _, ok := parseEnvAssignmentLine(ln)
		if !ok {
			continue
		}
		if k == key {
			found = true
			break
		}
	}
	if found {
		return envPath, false, nil
	}

	newLine := formatEnvAssignmentLine(key, value)
	out := strings.TrimRight(content, "\n")
	if out != "" {
		out += "\n"
	}
	out += newLine + "\n"
	return envPath, true, os.WriteFile(envPath, []byte(out), 0o644)
}

func parseEnvAssignmentLine(line string) (string, string, bool) {
	ln := strings.TrimSpace(line)
	if ln == "" || strings.HasPrefix(ln, "#") {
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

func formatEnvAssignmentLine(k, v string) string {
	if runtime.GOOS == "windows" {
		return "set " + k + "=" + v
	}
	return "export " + k + "=" + fmt.Sprintf("%q", v)
}

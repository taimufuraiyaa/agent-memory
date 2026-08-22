package cli

import (
	"bytes"
	"context"
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

	"github.com/taimufuraiyaa/agent-memory/internal/workspace"
)

type upgradeResult struct {
	FromVersion      string                                      `json:"from_version"`
	ToSpecifier      string                                      `json:"to_specifier"`
	Module           string                                      `json:"module"`
	Method           string                                      `json:"method"`
	SourceDir        string                                      `json:"source_dir,omitempty"`
	TargetPath       string                                      `json:"target_path"`
	InstalledFrom    string                                      `json:"installed_from,omitempty"`
	Replaced         bool                                        `json:"replaced"`
	AgentFiles       *workspace.WriteAgentFilesResult            `json:"agent_files,omitempty"`
	AllAgentFiles    map[string]*workspace.WriteAgentFilesResult `json:"all_agent_files,omitempty"`
	DashboardUpdated bool                                        `json:"dashboard_updated,omitempty"`
	DashboardDir     string                                      `json:"dashboard_dir,omitempty"`
	DashboardSource  string                                      `json:"dashboard_source,omitempty"`
	DashboardError   string                                      `json:"dashboard_error,omitempty"`
	EnvFile          string                                      `json:"env_file,omitempty"`
	EnvUpdated       bool                                        `json:"env_updated,omitempty"`
	EnvError         string                                      `json:"env_error,omitempty"`
	TuningCommand    string                                      `json:"tuning_command,omitempty"`
	TermIndexes      map[string]*workspace.TermIndexSetupResult  `json:"term_indexes,omitempty"`
	TermIndexErrors  map[string]string                           `json:"term_index_errors,omitempty"`
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

func binNameWithExt(name string) string {
	if runtime.GOOS == "windows" && !strings.HasSuffix(strings.ToLower(name), ".exe") {
		return name + ".exe"
	}
	return name
}

func canonicalUpgradeTarget(path string) string {
	if strings.EqualFold(filepath.Base(path), binNameWithExt("am")) {
		return filepath.Join(filepath.Dir(path), binNameWithExt("agent-memory"))
	}
	return path
}

func synchronizeConciseExecutable(canonicalPath string) error {
	if !strings.EqualFold(filepath.Base(canonicalPath), binNameWithExt("agent-memory")) {
		return nil
	}
	aliasPath := filepath.Join(filepath.Dir(canonicalPath), binNameWithExt("am"))
	if err := replaceFileAtomic(aliasPath, canonicalPath); err != nil {
		return fmt.Errorf("publish concise executable %s: %w", aliasPath, err)
	}
	return nil
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
	if strings.TrimSpace(start) != "" {
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
	}

	// Fall back to common repository locations relative to the current user's home.
	var fallbacks []string
	if home, err := os.UserHomeDir(); err == nil {
		fallbacks = append(fallbacks,
			filepath.Join(home, "timebooks", "agent-memory"),
			filepath.Join(home, "workspace", "agent-memory"),
			filepath.Join(home, "agent-memory"),
		)
	}
	for _, f := range fallbacks {
		goMod := filepath.Join(f, "go.mod")
		cmdMain := filepath.Join(f, "cmd", "agent-memory", "main.go")
		if fileExists(goMod) && fileExists(cmdMain) {
			return f
		}
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
	out, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".upgrade.*")
	if err != nil {
		return err
	}
	tmp := out.Name()
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
	if err := atomicReplaceFile(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
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
	if err := runDashboardNPMCI(dstDir); err != nil {
		return src, err
	}
	return src, nil
}

func runDashboardNPMCI(dstDir string) error {
	if strings.TrimSpace(dstDir) == "" {
		return errors.New("dashboard dir is required")
	}
	run := func() (string, error) {
		ci := exec.Command("npm", "ci")
		ci.Dir = dstDir
		_, errOut, err := runAndCapture(ci)
		if err == nil {
			return "", nil
		}
		msg := strings.TrimSpace(errOut)
		if msg == "" {
			msg = err.Error()
		}
		return msg, err
	}

	msg, err := run()
	if err == nil {
		return nil
	}

	// Recover from partial/corrupt dashboard installs left by interrupted upgrades.
	for _, sub := range []string{"node_modules", "package-lock.json.tmp", ".package-lock.json"} {
		_ = os.RemoveAll(filepath.Join(dstDir, sub))
	}
	if cleanupErr := os.RemoveAll(filepath.Join(dstDir, "node_modules")); cleanupErr != nil {
		return fmt.Errorf("npm ci failed: %s (cleanup failed: %v)", msg, cleanupErr)
	}

	retryMsg, retryErr := run()
	if retryErr == nil {
		return nil
	}
	return fmt.Errorf("npm ci failed after clean retry: %s", retryMsg)
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
	var all bool

	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Upgrade the current project, or every project with --all",
		Long: `Upgrade the agent-memory binary and write the hippocampus hook files
(.kiro/hooks/memory-recall-gate.json and .kiro/hooks/memory-consolidation-gate.json)
into the registered project containing the current directory. Use --all to
upgrade every registered project.

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
					CWD:     cwd,
					DataDir: defaultAgentMemoryDataDir(),
					Force:   force,
				})
			}

			writeAllAgentFiles := func(force bool) (map[string]*workspace.WriteAgentFilesResult, error) {
				mgr, err := workspace.NewManager(defaultAgentMemoryDataDir())
				if err != nil {
					return nil, err
				}
				projectNames, err := mgr.ProjectNames()
				if err != nil {
					return nil, err
				}
				res := make(map[string]*workspace.WriteAgentFilesResult)
				for _, projectName := range projectNames {
					proj, err := mgr.Project(projectName)
					if err != nil {
						continue
					}
					if proj.WorkspaceRoot == "" {
						continue
					}
					af, err := workspace.WriteAgentFiles(workspace.WriteAgentFilesOptions{
						CWD:       proj.WorkspaceRoot,
						Workspace: proj.Name,
						DataDir:   defaultAgentMemoryDataDir(),
						Force:     force,
					})
					if err != nil {
						continue
					}
					res[proj.Name] = af
				}
				return res, nil
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
				var res upgradeResult
				if all {
					aaf, err := writeAllAgentFiles(forceHooks)
					if err != nil {
						return fmt.Errorf("all agent files: %w", err)
					}
					res.AllAgentFiles = aaf
				} else {
					af, err := writeAgentFiles(forceHooks)
					if err != nil {
						return fmt.Errorf("agent files: %w", err)
					}
					res.AgentFiles = af
				}
				if f == "json" {
					return writeSuccessEnvelope(cmd.OutOrStdout(), "upgrade", res)
				}
				if all {
					for projName, a := range res.AllAgentFiles {
						_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Project: %s\n", projName)
						printAgentFiles(a)
					}
				} else {
					printAgentFiles(res.AgentFiles)
				}
				return nil
			}

			v := collectVersionInfo()
			if strings.TrimSpace(module) == "" {
				module = "github.com/taimufuraiyaa/agent-memory/cmd/agent-memory"
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
				target = canonicalUpgradeTarget(exe)
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
			if strings.TrimSpace(srcDir) != "" {
				method = "go-build"
			}

			res := upgradeResult{
				FromVersion:   v.Version,
				ToSpecifier:   to,
				Module:        module,
				Method:        method,
				SourceDir:     srcDir,
				TargetPath:    target,
				TuningCommand: "am tuning",
			}

			// Warn when --to signals a specific version but we fall back to a local checkout
			// (go-build uses whatever is on disk, ignoring the version specifier).
			toNormalized := strings.TrimSpace(to)
			if method == "go-build" && toNormalized != "" && !strings.EqualFold(toNormalized, "latest") {
				warnMsg := fmt.Sprintf(
					"UPGRADE WARNING: --to %s specified but building from local checkout at %s. "+
						"The version specifier is ignored when using a local source. "+
						"Set AGENT_MEMORY_SRC_DIR to a checkout at the desired version, "+
						"or unset it and remove --src to use 'go install %s' instead.",
					to, srcDir, spec,
				)
				if f != "json" {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%s\n", warnMsg)
				}
				// Attach the warning to the result struct for both formats.
				res.DashboardError = warnMsg
			}

			if dryRun {
				if f == "json" {
					return writeSuccessEnvelope(cmd.OutOrStdout(), "upgrade", res)
				}
				plannedSource := spec
				if method == "go-build" {
					plannedSource = srcDir
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "dry-run: would replace %s (from %s)\n", target, plannedSource)
				return nil
			}

			if !all {
				dataDir := defaultAgentMemoryDataDir()
				mgr, err := workspace.NewManager(dataDir)
				if err != nil {
					return err
				}
				if _, err := mgr.ProjectForPath(cwd); err != nil {
					return fmt.Errorf("current directory %s is not a registered project in %s (run from a registered project or use --all): %w", cwd, dataDir, err)
				}
			}

			installedFrom := ""
			if method == "go-build" {
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
			res.InstalledFrom = installedFrom

			if err := replaceFileAtomic(target, installedFrom); err != nil {
				return fmt.Errorf("replace failed: %w", err)
			}
			if err := synchronizeConciseExecutable(target); err != nil {
				return err
			}
			res.Replaced = true
			if method == "go-build" {
				_ = os.Remove(installedFrom)
			}
			prepared, failures, prepareErr := prepareUpgradeTermIndexes(cmd.Context(), defaultAgentMemoryDataDir(), cwd, all)
			if prepareErr != nil {
				failures = map[string]string{"_registry": prepareErr.Error()}
			}
			res.TermIndexes = prepared
			res.TermIndexErrors = failures

			if !noHooks {
				if all {
					aaf, err := writeAllAgentFiles(forceHooks)
					if err != nil {
						return fmt.Errorf("all agent files: %w", err)
					}
					res.AllAgentFiles = aaf
				} else {
					af, err := writeAgentFiles(forceHooks)
					if err != nil {
						return fmt.Errorf("agent files: %w", err)
					}
					res.AgentFiles = af
				}
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
			if envPath, updated, err := ensureEnvVarIfPresent("AGENT_MEMORY_TERM_BLOOM_MODE", "shadow"); err != nil {
				res.EnvFile = envPath
				res.EnvError = err.Error()
			} else if updated {
				res.EnvFile = envPath
				res.EnvUpdated = true
			} else if envPath != "" {
				res.EnvFile = envPath
			}
			if envPath := filepath.Join(defaultAgentMemoryDataDir(), "agent-memory.env"); strings.TrimSpace(envPath) != "" {
				if updated, err := ensureAdaptiveTuningGuidance(envPath); err != nil {
					if res.EnvFile == "" {
						res.EnvFile = envPath
					}
					res.EnvError = err.Error()
				} else if updated {
					res.EnvFile = envPath
					res.EnvUpdated = true
				} else if res.EnvFile == "" && fileExists(envPath) {
					res.EnvFile = envPath
				}
				if updated, err := ensureTermBloomGuidance(envPath); err != nil {
					res.EnvError = err.Error()
				} else if updated {
					res.EnvFile = envPath
					res.EnvUpdated = true
				}
			}

			if f == "json" {
				return writeSuccessEnvelope(cmd.OutOrStdout(), "upgrade", res)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "upgraded: %s\n", target)
			if all && res.AllAgentFiles != nil {
				for projName, a := range res.AllAgentFiles {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Project: %s\n", projName)
					printAgentFiles(a)
				}
			} else if res.AgentFiles != nil {
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
			for project, result := range res.TermIndexes {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  term index ready: %s (%d terms)\n", project, result.DistinctTerms)
			}
			for project, message := range res.TermIndexErrors {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  term index preparation failed: %s: %s\n", project, message)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  inspect tuning: %s\n", res.TuningCommand)
			return nil
		},
	}

	cmd.Flags().StringVarP(&format, "format", "f", "text", "Output format: json|text")
	cmd.Flags().StringVar(&module, "module", "github.com/taimufuraiyaa/agent-memory/cmd/agent-memory", "Go module path to install (advanced)")
	cmd.Flags().StringVar(&to, "to", "latest", "Go version specifier (e.g. latest, v1.2.3)")
	cmd.Flags().StringVar(&target, "target", "", "Target path to replace (default: current executable)")
	cmd.Flags().StringVar(&srcDir, "src", "", "Build from local source checkout (repo root containing go.mod and cmd/agent-memory)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Accepted for compatibility; upgrades no longer require confirmation")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would happen without replacing anything")
	cmd.Flags().BoolVar(&hooksOnly, "hooks-only", false, "Only write hippocampus hook files, skip binary upgrade")
	cmd.Flags().BoolVar(&noHooks, "no-hooks", false, "Skip writing hippocampus hook files")
	cmd.Flags().BoolVar(&forceHooks, "force-hooks", false, "Overwrite hook files even if already up-to-date")
	cmd.Flags().BoolVar(&noDashboard, "no-dashboard", false, "Skip refreshing standalone dashboard from source checkout")
	cmd.Flags().StringVar(&dashboardDir, "dashboard-dir", "", "Dashboard install dir (default: $AGENT_MEMORY_DASHBOARD_DIR or ~/.agent-memory/dashboard)")
	cmd.Flags().BoolVarP(&all, "all", "a", false, "Upgrade all registered workspaces/projects")
	return cmd
}

func prepareRegisteredTermIndexes(ctx context.Context, dataDir string) (map[string]*workspace.TermIndexSetupResult, map[string]string, error) {
	mgr, err := workspace.NewManager(dataDir)
	if err != nil {
		return nil, nil, err
	}
	return mgr.PrepareAllTermIndexes(ctx)
}

func prepareUpgradeTermIndexes(ctx context.Context, dataDir, cwd string, all bool) (map[string]*workspace.TermIndexSetupResult, map[string]string, error) {
	if all {
		return prepareRegisteredTermIndexes(ctx, dataDir)
	}
	mgr, err := workspace.NewManager(dataDir)
	if err != nil {
		return nil, nil, err
	}
	project, err := mgr.ProjectForPath(cwd)
	if err != nil {
		return nil, nil, err
	}
	result, err := mgr.PrepareTermIndex(ctx, project.Name)
	if err != nil {
		return map[string]*workspace.TermIndexSetupResult{}, map[string]string{project.Name: err.Error()}, nil
	}
	return map[string]*workspace.TermIndexSetupResult{project.Name: result}, map[string]string{}, nil
}

func ensureEnvVarIfPresent(key, value string) (string, bool, error) {
	envPath := filepath.Join(defaultAgentMemoryDataDir(), "agent-memory.env")
	updated, err := ensureEnvVarAtPath(envPath, key, value)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	return envPath, updated, err
}

func ensureEnvVarAtPath(envPath, key, value string) (bool, error) {
	b, err := os.ReadFile(envPath)
	if err != nil {
		return false, err
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
		return false, nil
	}

	newLine := formatEnvAssignmentLine(key, value)
	out := strings.TrimRight(content, "\n")
	if out != "" {
		out += "\n"
	}
	out += newLine + "\n"
	return true, os.WriteFile(envPath, []byte(out), 0o644)
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

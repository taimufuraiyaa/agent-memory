package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/taimufuraiyaa/agent-memory/internal/api/dashboard"
	"github.com/taimufuraiyaa/agent-memory/internal/bootstrap"
	"github.com/taimufuraiyaa/agent-memory/internal/workspace"
)

func newInstallCommand() *cobra.Command {
	var dataDir string
	var binDir string
	var src string
	var skipModel bool
	var skipONNXRuntime bool
	var noDashboard bool
	var dashboardSrc string
	var dashboardDir string
	var writeEnvFile bool
	var projectName string
	var ideTargets []string
	var noInit bool
	var force bool

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install data directories, dependencies, and initialize project workspace rules",
		Long: `Install data directories, the MiniLM model, ONNX Runtime libraries, the standalone dashboard,
configure environment variables, and initialize the current directory as a project workspace.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			errOut := cmd.OutOrStderr()

			if binDir == "" {
				binDir = defaultBinDir()
			}
			if dataDir == "" {
				dataDir = defaultAgentMemoryDataDir()
			}
			if dashboardDir == "" {
				dashboardDir = filepath.Join(dataDir, "dashboard")
			}
			if len(ideTargets) == 0 {
				// A fresh install configures every supported agent surface. This keeps
				// Codex and other agents zero-configuration even in repositories that
				// do not contain an IDE marker yet.
				ideTargets = []string{"all"}
			}

			fmt.Fprintln(errOut, "— agent-memory installer —")

			// Step 1: Data directories
			fmt.Fprintln(errOut, "\n▶ 1/5 data directories")
			for _, sub := range []string{"", "models", "logs", "onnxruntime"} {
				if err := os.MkdirAll(filepath.Join(dataDir, sub), 0o755); err != nil {
					return fmt.Errorf("failed to create data dir %s: %w", sub, err)
				}
			}
			fmt.Fprintf(errOut, "  ✓ ready at %s\n", dataDir)

			// Step 2: Binary installation / copy
			fmt.Fprintln(errOut, "\n▶ 2/5 binary")
			installed, err := installOrCopyBinary(out, errOut, binDir, src)
			if err != nil {
				return err
			}
			fmt.Fprintf(errOut, "  ✓ installed: %s\n", installed)
			checkPATHAdvice(errOut, filepath.Dir(installed))

			// Step 3: ONNX runtime
			fmt.Fprintln(errOut, "\n▶ 3/5 onnx runtime")
			runtimePath := ""
			if skipONNXRuntime {
				fmt.Fprintln(errOut, "    skipped (--skip-onnx-runtime)")
			} else {
				p, err := bootstrap.EnsureONNXRuntime(dataDir, false)
				if err != nil {
					fmt.Fprintf(errOut, "  ! onnx runtime install failed: %v\n", err)
					fmt.Fprintln(errOut, "  ! semantic embeddings will stay unavailable until this succeeds")
				} else {
					runtimePath = p
					fmt.Fprintf(errOut, "  ✓ installed: %s\n", runtimePath)
				}
			}

			// Step 4: Model download
			fmt.Fprintln(errOut, "\n▶ 4/5 local embedding model")
			if skipModel {
				fmt.Fprintln(errOut, "    skipped (--no-model)")
			} else {
				if err := bootstrap.EnsureModel(dataDir, false); err != nil {
					fmt.Fprintf(errOut, "  ! model download failed: %v\n", err)
					fmt.Fprintln(errOut, "  ! agent-memory will work for everything except local embeddings until this succeeds")
				} else {
					fmt.Fprintf(errOut, "  ✓ ready at %s\n", filepath.Join(dataDir, "models", "all-MiniLM-L6-v2"))
				}
			}

			// Step 5: Dashboard
			fmt.Fprintln(errOut, "\n▶ 5/5 dashboard (React + TypeScript)")
			dashInstalled := ""
			if noDashboard {
				fmt.Fprintln(errOut, "    skipped (--no-dashboard)")
			} else {
				srcExists := false
				if _, err := os.Stat(dashboardSrc); err == nil {
					srcExists = true
				}
				if !srcExists && dashboard.HasEmbeddedAssets() {
					fmt.Fprintln(errOut, "  ✓ ready: using embedded dashboard assets (no local install required)")
				} else if !srcExists {
					fmt.Fprintf(errOut, "  ! dashboard: source folder not found at %s and no embedded assets found, skipping\n", dashboardSrc)
				} else {
					if err := bootstrap.EnsureDashboard(dashboardSrc, dashboardDir, out, errOut); err != nil {
						fmt.Fprintf(errOut, "  ! dashboard setup failed: %v\n", err)
					} else {
						dashInstalled = dashboardDir
						fmt.Fprintf(errOut, "  ✓ ready at %s\n", dashInstalled)
					}
				}
			}

			// Env file setup
			if writeEnvFile {
				vars := map[string]string{
					"AGENT_MEMORY_UPGRADE_YES":     "1",
					"AGENT_MEMORY_OBSERVE_ENABLED": "1",
					"AGENT_MEMORY_ENABLED":         "1",
				}
				if strings.TrimSpace(runtimePath) != "" {
					vars["AGENT_MEMORY_ONNX_RUNTIME_PATH"] = runtimePath
				}
				if strings.TrimSpace(dashInstalled) != "" {
					vars["AGENT_MEMORY_DASHBOARD_DIR"] = dashboardDir
				}
				if root := detectRepoRoot(src); strings.TrimSpace(root) != "" {
					vars["AGENT_MEMORY_SRC_DIR"] = root
				}

				envPath := filepath.Join(dataDir, "agent-memory.env")
				_, err := upsertEnvFile(envPath, vars)
				if err != nil {
					fmt.Fprintf(errOut, "  ! env file write failed: %v\n", err)
				}
				if _, err := ensureEnvVarAtPath(envPath, "AGENT_MEMORY_TERM_BLOOM_MODE", "shadow"); err != nil {
					fmt.Fprintf(errOut, "  ! term Bloom env setup failed: %v\n", err)
				}
				if err := ensureShellAutoload(envPath); err != nil {
					fmt.Fprintf(errOut, "  ! shell setup skipped: %v\n", err)
				}
			}

			if installTargetSelected(ideTargets, "codex") {
				// Codex requires the memory database root to be writable before a
				// project-local trusted configuration can take effect. Install a narrow,
				// preserved user-wide layer so first use requires no manual config edits.
				codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME"))
				if codexHome == "" {
					if home, err := os.UserHomeDir(); err == nil {
						codexHome = filepath.Join(home, ".codex")
					}
				}
				if codexHome != "" {
					paths, err := workspace.WriteCodexGlobalFiles(codexHome, dataDir)
					if err != nil {
						return fmt.Errorf("Codex setup failed: %w", err)
					}
					fmt.Fprintf(errOut, "  ✓ configured Codex: %s\n", strings.Join(paths, ", "))
				}
			}

			// Project workspace initialization (init-here / reinstall)
			if !noInit {
				fmt.Fprintln(errOut, "\n▶ project rules setup")
				cwd, err := os.Getwd()
				if err != nil {
					return err
				}
				name := projectName
				if strings.TrimSpace(name) == "" {
					name = filepath.Base(cwd)
				}

				mgr, err := workspace.NewManager(dataDir)
				if err != nil {
					return fmt.Errorf("failed to create workspace manager: %w", err)
				}

				// Run Init
				initOut, err := mgr.Init(cmd.Context(), workspace.InitOptions{
					CWD:         cwd,
					ProjectName: name,
					Force:       force,
					IDEs:        ideTargets,
				})

				if err == nil {
					fmt.Fprintf(errOut, "  ✓ initialized workspace project: %s\n", initOut.Project)
					if len(initOut.RuleFiles) > 0 {
						fmt.Fprintf(errOut, "  ✓ wrote rule files: %s\n", strings.Join(initOut.RuleFiles, ", "))
					}
				} else if strings.Contains(err.Error(), "project already exists") {
					fmt.Fprintf(errOut, "  ! project already exists; running reinstall to update IDE rule files\n")
					reinstOut, err := mgr.Reinstall(cmd.Context(), workspace.ReinstallOptions{
						CWD:         cwd,
						ProjectName: name,
						Force:       true, // overwrite existing rules
						IDEs:        ideTargets,
					})
					if err != nil {
						return fmt.Errorf("reinstall failed: %w", err)
					}
					fmt.Fprintf(errOut, "  ✓ reinstalled workspace project: %s\n", reinstOut.Project)
					if reinstOut.AgentFiles != nil {
						for _, ide := range reinstOut.AgentFiles.IDEs {
							if len(ide.Written) > 0 {
								fmt.Fprintf(errOut, "  ✓ [%s] written: %s\n", ide.IDE, strings.Join(ide.Written, ", "))
							}
						}
					}
				} else {
					return fmt.Errorf("init failed: %w", err)
				}
			}

			fmt.Fprintln(errOut, "\n✓ Installation and rules setup complete!")
			return nil
		},
	}

	cmd.Flags().StringVarP(&dataDir, "data-dir", "d", "", "data directory (default: ~/.agent-memory)")
	cmd.Flags().StringVarP(&binDir, "bin-dir", "b", "", "binary install directory")
	cmd.Flags().StringVarP(&src, "src", "s", "./cmd/agent-memory", "path to local agent-memory main package")
	cmd.Flags().BoolVar(&skipModel, "no-model", false, "skip downloading the MiniLM ONNX model")
	cmd.Flags().BoolVar(&skipONNXRuntime, "skip-onnx-runtime", false, "skip downloading ONNX Runtime libraries")
	cmd.Flags().BoolVar(&noDashboard, "no-dashboard", false, "skip installing the standalone dashboard")
	cmd.Flags().StringVar(&dashboardSrc, "dashboard-src", "./tools/agent-memory/dashboard", "path to local dashboard source folder")
	cmd.Flags().StringVar(&dashboardDir, "dashboard-dir", "", "dashboard install directory")
	cmd.Flags().BoolVar(&writeEnvFile, "write-env", true, "write an env file with environment settings")
	cmd.Flags().StringVarP(&projectName, "project-name", "n", "", "project name for workspace setup (default: cwd basename)")
	cmd.Flags().StringSliceVar(&ideTargets, "ide", nil, "IDE rule targets (repeatable, default: all): cursor|antigravity|claude|zcode|codex|aierules|cursorrules|trae|windsurfrules|generic|all")
	cmd.Flags().BoolVar(&noInit, "no-init", false, "skip workspace project auto-initialization")
	cmd.Flags().BoolVar(&force, "force", false, "force recreate project workspace if it already exists")

	return cmd
}

func installTargetSelected(targets []string, target string) bool {
	for _, raw := range targets {
		selected := strings.ToLower(strings.TrimSpace(raw))
		if selected == "all" || selected == target {
			return true
		}
	}
	return false
}

func installOrCopyBinary(out, errOut io.Writer, binDir, src string) (string, error) {
	finalBin := filepath.Join(binDir, binNameWithExt("agent-memory"))

	// Try to build from local source if available
	if dirExists(src) {
		fmt.Fprintln(errOut, "    building binary from source...")
		buildDir, buildPackage, err := resolveGoBuildPackage(src)
		if err != nil {
			return "", err
		}
		tmpBin := filepath.Join(binDir, ".agent-memory-install."+time.Now().UTC().Format("20060102T150405Z"))
		if err := os.MkdirAll(binDir, 0o755); err != nil {
			return "", err
		}
		buildCmd := exec.Command("go", "build", "-trimpath", "-ldflags", "-s -w", "-o", tmpBin, buildPackage)
		buildCmd.Dir = buildDir
		buildCmd.Stdout = out
		buildCmd.Stderr = errOut
		buildCmd.Env = append(os.Environ(), "CGO_ENABLED=1")
		if err := buildCmd.Run(); err != nil {
			_ = os.Remove(tmpBin)
			return "", fmt.Errorf("go build: %w", err)
		}
		if err := replaceFileAtomic(finalBin, tmpBin); err != nil {
			_ = os.Remove(tmpBin)
			return "", fmt.Errorf("install rename: %w", err)
		}
		_ = os.Remove(tmpBin)
		if err := os.Chmod(finalBin, 0o755); err != nil {
			return "", err
		}
		return finalBin, nil
	}

	// Otherwise copy currently running executable to finalBin
	fmt.Fprintln(errOut, "    copying executable to installation directory...")
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("failed to get executable path: %w", err)
	}

	// Skip copy if already running from target path
	if absExe, err := filepath.Abs(exe); err == nil {
		if absFinal, err := filepath.Abs(finalBin); err == nil && absExe == absFinal {
			return finalBin, nil
		}
	}

	if err := replaceFileAtomic(finalBin, exe); err != nil {
		return "", fmt.Errorf("copy failed: %w", err)
	}
	return finalBin, nil
}

func resolveGoBuildPackage(src string) (string, string, error) {
	absSrc, err := filepath.Abs(src)
	if err != nil {
		return "", "", fmt.Errorf("resolve source path %s: %w", src, err)
	}
	moduleRoot := absSrc
	for {
		if fileExists(filepath.Join(moduleRoot, "go.mod")) {
			break
		}
		parent := filepath.Dir(moduleRoot)
		if parent == moduleRoot {
			return "", "", fmt.Errorf("source package %s is not inside a Go module", absSrc)
		}
		moduleRoot = parent
	}
	relativePackage, err := filepath.Rel(moduleRoot, absSrc)
	if err != nil {
		return "", "", fmt.Errorf("resolve source package relative to module: %w", err)
	}
	buildPackage := "."
	if relativePackage != "." {
		buildPackage = "./" + filepath.ToSlash(relativePackage)
	}
	return moduleRoot, buildPackage, nil
}

func detectRepoRoot(src string) string {
	cwd, err := os.Getwd()
	if err == nil && fileExists(filepath.Join(cwd, "go.mod")) && fileExists(filepath.Join(cwd, "cmd", "agent-memory", "main.go")) {
		if abs, err := filepath.Abs(cwd); err == nil {
			return abs
		}
		return cwd
	}
	if strings.TrimSpace(src) == "" {
		return ""
	}
	var absSrc string
	if abs, err := filepath.Abs(src); err == nil {
		absSrc = abs
	} else {
		absSrc = src
	}
	if fileExists(filepath.Join(absSrc, "main.go")) && filepath.Base(absSrc) == "agent-memory" && filepath.Base(filepath.Dir(absSrc)) == "cmd" {
		return filepath.Dir(filepath.Dir(absSrc))
	}
	return ""
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

func dirExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
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
	FromVersion   string                      `json:"from_version"`
	ToSpecifier   string                      `json:"to_specifier"`
	Module        string                      `json:"module"`
	Method        string                      `json:"method"`
	SourceDir     string                      `json:"source_dir,omitempty"`
	TargetPath    string                      `json:"target_path"`
	InstalledFrom string                      `json:"installed_from,omitempty"`
	Replaced      bool                        `json:"replaced"`
	AgentFiles    *workspace.WriteAgentFilesResult `json:"agent_files,omitempty"`
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

			if !yes {
				return errors.New("upgrade requires --yes / -y (or use --dry-run)")
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

			if f == "json" {
				return writeSuccessEnvelope(cmd.OutOrStdout(), "upgrade", res)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "upgraded: %s\n", target)
			if res.AgentFiles != nil {
				printAgentFiles(res.AgentFiles)
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
	return cmd
}

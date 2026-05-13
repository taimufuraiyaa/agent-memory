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
)

type upgradeResult struct {
	FromVersion   string `json:"from_version"`
	ToSpecifier   string `json:"to_specifier"`
	Module        string `json:"module"`
	Method        string `json:"method"`
	SourceDir     string `json:"source_dir,omitempty"`
	TargetPath    string `json:"target_path"`
	InstalledFrom string `json:"installed_from,omitempty"`
	Replaced      bool   `json:"replaced"`
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

	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Upgrade agent-memory to the newest version (requires Go)",
		RunE: func(cmd *cobra.Command, args []string) error {
			f, err := validateTextOrJSONFormat(format)
			if err != nil {
				return err
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
				if cwd, err := os.Getwd(); err == nil {
					srcDir = findSourceRoot(cwd)
				}
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
				if f == "json" {
					return writeSuccessEnvelope(cmd.OutOrStdout(), "upgrade", res)
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "dry-run: would replace %s with %s (from %s)\n", target, installedFrom, spec)
				return nil
			}

			if !yes {
				return errors.New("upgrade requires --yes (or use --dry-run)")
			}

			if err := replaceFileAtomic(target, installedFrom); err != nil {
				return fmt.Errorf("replace failed: %w", err)
			}
			res.Replaced = true
			if method == "go-build" {
				_ = os.Remove(installedFrom)
			}

			if f == "json" {
				return writeSuccessEnvelope(cmd.OutOrStdout(), "upgrade", res)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "upgraded: %s (installed from %s)\n", target, installedFrom)
			return nil
		},
	}

	cmd.Flags().StringVarP(&format, "format", "f", "text", "Output format: json|text")
	cmd.Flags().StringVar(&module, "module", "github.com/time/timebooks/agent-memory/cmd/agent-memory", "Go module path to install (advanced)")
	cmd.Flags().StringVar(&to, "to", "latest", "Go version specifier (e.g. latest, v1.2.3)")
	cmd.Flags().StringVar(&target, "target", "", "Target path to replace (default: current executable)")
	cmd.Flags().StringVar(&srcDir, "src", "", "Build from local source checkout (repo root containing go.mod and cmd/agent-memory)")
	cmd.Flags().BoolVar(&yes, "yes", false, "Confirm replacing the target binary")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would happen without replacing anything")
	return cmd
}

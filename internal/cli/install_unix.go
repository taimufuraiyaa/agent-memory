//go:build !windows

package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func defaultBinDir() string {
	if v := os.Getenv("HOME"); v != "" {
		return filepath.Join(v, ".local", "bin")
	}
	cwd, _ := os.Getwd()
	return cwd
}


func checkPATHAdvice(out io.Writer, dir string) {
	if isOnPath(dir) {
		return
	}
	fmt.Fprintf(out, "  ! %s is not on $PATH\n", dir)
	fmt.Fprintf(out, "  ! add to your shell rc: export PATH=\"%s:$PATH\"\n", dir)
}

func ensureShellAutoload(envPath string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	if !fileExists(envPath) {
		return fmt.Errorf("env file not found: %s", envPath)
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

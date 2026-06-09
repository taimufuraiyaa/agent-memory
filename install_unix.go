//go:build !windows

package main

import (
	"fmt"
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

func defaultDataDir() string {
	if v := os.Getenv("HOME"); v != "" {
		return filepath.Join(v, ".agent-memory")
	}
	cwd, _ := os.Getwd()
	return filepath.Join(cwd, ".agent-memory")
}

func binNameWithExt() string {
	return binName
}

func formatEnvAssignment(k, v string) string {
	return "export " + k + "=" + shellQuote(v)
}

func shellQuote(s string) string {
	if strings.ContainsAny(s, " \t\n\"'$`\\") {
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return s
}

func checkPATHAdvice(cfg config, dir string) {
	if isOnPath(dir) {
		return
	}
	warn(cfg, "%s is not on $PATH", dir)
	warn(cfg, "add to your shell rc: export PATH=\"%s:$PATH\"", dir)
}

func ensureShellAutoload(cfg config, envPath string) error {
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

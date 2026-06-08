//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func defaultBinDir() string {
	if v := os.Getenv("LOCALAPPDATA"); v != "" {
		return filepath.Join(v, "Programs", "agent-memory")
	}
	cwd, _ := os.Getwd()
	return cwd
}

func defaultDataDir() string {
	if v := os.Getenv("LOCALAPPDATA"); v != "" {
		return filepath.Join(v, "agent-memory")
	}
	cwd, _ := os.Getwd()
	return filepath.Join(cwd, ".agent-memory")
}

func binNameWithExt() string {
	return binName + ".exe"
}

func formatEnvAssignment(k, v string) string {
	return "set " + k + "=" + v
}

func checkPATHAdvice(cfg config, dir string) {
	if isOnPath(dir) {
		return
	}
	warn(cfg, "%s is not on %%PATH%%", dir)
	warn(cfg, "add it via: System Properties -> Environment Variables -> User PATH")
}

func ensureShellAutoload(cfg config, envPath string) error {
	// Windows doesn't auto-source env files in the same way
	info(cfg, "on Windows, environment variables are set via the env file at: %s", envPath)
	return nil
}

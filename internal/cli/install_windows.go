//go:build windows

package cli

import (
	"fmt"
	"io"
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


func checkPATHAdvice(out io.Writer, dir string) {
	if isOnPath(dir) {
		return
	}
	fmt.Fprintf(out, "  ! %s is not on %%PATH%%\n", dir)
	fmt.Fprintf(out, "  ! add it via: System Properties -> Environment Variables -> User PATH\n")
}

func ensureShellAutoload(envPath string) error {
	// Windows doesn't auto-source env files in the same way
	return nil
}

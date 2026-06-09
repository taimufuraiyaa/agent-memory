package bootstrap

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// EnsureDashboard copies and builds the dashboard.
func EnsureDashboard(srcDir, dstDir string, stdout, stderr io.Writer) error {
	if !dirExists(srcDir) {
		return fmt.Errorf("dashboard source not found: %s", srcDir)
	}
	if !fileExists(filepath.Join(srcDir, "package.json")) {
		return fmt.Errorf("dashboard package.json not found: %s", srcDir)
	}
	if _, err := exec.LookPath("npm"); err != nil {
		return errors.New("npm not found (install Node.js to use the standalone dashboard)")
	}

	if strings.TrimSpace(dstDir) == "" {
		return errors.New("dashboard dir is required")
	}

	if err := os.MkdirAll(dstDir, 0755); err != nil {
		return err
	}

	if err := copyDir(dstDir, srcDir); err != nil {
		return err
	}

	return RunDashboardInstall(dstDir, stdout, stderr)
}

// RunDashboardInstall runs npm ci in the dashboard directory with recovery logic.
func RunDashboardInstall(dstDir string, stdout, stderr io.Writer) error {
	if strings.TrimSpace(dstDir) == "" {
		return errors.New("dashboard dir is required")
	}

	run := func() error {
		cmd := exec.Command("npm", "ci")
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		cmd.Dir = dstDir
		return cmd.Run()
	}

	if err := run(); err == nil {
		return nil
	}

	// Recover from partial/corrupt dashboard installs left by interrupted setup.
	for _, sub := range []string{"node_modules", "package-lock.json.tmp", ".package-lock.json"} {
		_ = os.RemoveAll(filepath.Join(dstDir, sub))
	}

	if err := run(); err != nil {
		return err
	}
	return nil
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
		defer func() { _ = out.Close() }()

		_, err = io.Copy(out, in)
		return err
	})
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

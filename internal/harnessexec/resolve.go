package harnessexec

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Inside reports whether path is dir or below it.
func Inside(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// Lookup finds a bare program name in the given search directories. Empty and relative
// entries are ignored, and so is any entry inside the project, so a program placed in the
// project can never stand in for a toolchain. The result is the final path after links are
// resolved, and a link that leads into the project is not followed there either.
func Lookup(name string, dirs []string, realRoot string) (string, error) {
	for _, dir := range dirs {
		if dir == "" || !filepath.IsAbs(dir) {
			continue
		}
		resolvedDir, err := filepath.EvalSymlinks(dir)
		if err != nil || Inside(realRoot, resolvedDir) {
			continue
		}
		real, err := filepath.EvalSymlinks(filepath.Join(resolvedDir, name))
		if err != nil || Inside(realRoot, real) {
			continue
		}
		if info, err := os.Stat(real); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			return real, nil
		}
	}
	return "", os.ErrNotExist
}

// Identity names an executable as it is now: its path, size and modification time. An
// approval that binds it goes stale if the program is replaced or changed.
func Identity(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s|%d|%d", path, info.Size(), info.ModTime().UnixNano()), nil
}

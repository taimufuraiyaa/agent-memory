package engine

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

// TestColdArchiveRejectsTraversalWorkspaceNames verifies that workspace names
// which would escape the archives root fail with a typed validation error
// before any path is constructed.
func TestColdArchiveRejectsTraversalWorkspaceNames(t *testing.T) {
	arch := NewColdArchive(t.TempDir())
	for _, name := range []string{"..", "../x", "../../etc"} {
		t.Run(name, func(t *testing.T) {
			if _, err := arch.archivePath(name, "mem-1"); err == nil {
				t.Fatalf("expected archive path error for workspace %q", name)
			} else {
				var verr *core.ValidationError
				if !errors.As(err, &verr) {
					t.Fatalf("expected *core.ValidationError for workspace %q, got %T: %v", name, err, err)
				}
			}
			// The same guard must hold for every path-building entry point.
			if _, err := arch.workspaceDir(name); err == nil {
				t.Fatalf("expected workspaceDir error for workspace %q", name)
			}
		})
	}
}

// TestColdArchiveValidWorkspaceStaysInsideRoot verifies that a valid workspace
// name resolves to a path inside the archives root (prefix assertion).
func TestColdArchiveValidWorkspaceStaysInsideRoot(t *testing.T) {
	arch := NewColdArchive(t.TempDir())
	dir, err := arch.workspaceDir("my-project")
	if err != nil {
		t.Fatalf("workspaceDir: %v", err)
	}
	root, err := filepath.Abs(arch.baseDir)
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	if dir != root && !strings.HasPrefix(dir, root+string(filepath.Separator)) {
		t.Fatalf("workspace dir %q escaped archives root %q", dir, root)
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		t.Fatalf("workspace dir %q not inside root %q (rel=%q err=%v)", dir, root, rel, err)
	}
}

// TestColdArchivePathTraversalMemoryID verifies that a hostile memory ID
// cannot smuggle path segments out of the workspace archive directory.
func TestColdArchivePathTraversalMemoryID(t *testing.T) {
	arch := NewColdArchive(t.TempDir())
	path, err := arch.archivePath("ok-ws", "../../evil")
	if err != nil {
		t.Fatalf("archivePath: %v", err)
	}
	if !strings.HasSuffix(path, "evil.gz") {
		t.Fatalf("expected sanitized memory ID suffix, got %q", path)
	}
	if strings.Contains(path, ".."+string(filepath.Separator)) {
		t.Fatalf("archive path still contains traversal segments: %q", path)
	}
	root, err := filepath.Abs(arch.baseDir)
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		t.Fatalf("archive path %q not inside root %q (rel=%q err=%v)", path, root, rel, err)
	}
}

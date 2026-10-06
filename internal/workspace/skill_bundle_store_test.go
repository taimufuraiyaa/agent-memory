package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestRevisionBundleStorePublishesAndReadsImmutableBundle(t *testing.T) {
	projectRoot := t.TempDir()
	allowBundleCleanup(t, projectRoot)
	store, err := NewRevisionBundleStore(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		"SKILL.md":            []byte("---\nname: example\ndescription: Example skill.\n---\n"),
		"references/guide.md": []byte("trusted guidance\n"),
	}
	revision := testBundleRevision(t, files)

	published, err := store.Publish(context.Background(), revision, files)
	if err != nil {
		t.Fatal(err)
	}
	if published.Duplicate {
		t.Fatal("first publication reported as duplicate")
	}
	if !strings.Contains(published.RelativePath, strings.TrimPrefix(revision.BundleDigest, "sha256:")) {
		t.Fatalf("relative path %q is not content addressed", published.RelativePath)
	}

	loaded, err := store.Read(context.Background(), revision)
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range files {
		if got := string(loaded[path]); got != string(want) {
			t.Fatalf("loaded %s = %q, want %q", path, got, want)
		}
	}

	absoluteSkill := filepath.Join(projectRoot, filepath.FromSlash(published.RelativePath), "bundle", "SKILL.md")
	info, err := os.Lstat(absoluteSkill)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o222 != 0 {
		t.Fatalf("published file mode = %v, want read-only regular file", info.Mode())
	}
	if err := os.WriteFile(absoluteSkill, []byte("replacement"), 0o600); err == nil {
		t.Fatal("published bundle remained writable")
	}
}

func TestRevisionBundleStoreDuplicateIsIdempotentAndNeverOverwrites(t *testing.T) {
	projectRoot := t.TempDir()
	allowBundleCleanup(t, projectRoot)
	store, err := NewRevisionBundleStore(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{"SKILL.md": []byte("stable")}
	revision := testBundleRevision(t, files)
	if _, err := store.Publish(context.Background(), revision, files); err != nil {
		t.Fatal(err)
	}
	second, err := store.Publish(context.Background(), revision, files)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Duplicate {
		t.Fatal("duplicate publication was not reported")
	}

	tampered := map[string][]byte{"SKILL.md": []byte("changed")}
	if _, err := store.Publish(context.Background(), revision, tampered); err == nil {
		t.Fatal("digest-mismatched duplicate was accepted")
	}
	loaded, err := store.Read(context.Background(), revision)
	if err != nil {
		t.Fatal(err)
	}
	if string(loaded["SKILL.md"]) != "stable" {
		t.Fatal("duplicate publication overwrote immutable content")
	}
}

func TestRevisionBundleStoreRejectsInvalidContent(t *testing.T) {
	store, err := NewRevisionBundleStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(core.SkillRevision, map[string][]byte) (core.SkillRevision, map[string][]byte)
	}{
		{name: "digest", mutate: func(revision core.SkillRevision, files map[string][]byte) (core.SkillRevision, map[string][]byte) {
			files["SKILL.md"] = []byte("tampered")
			return revision, files
		}},
		{name: "missing declared file", mutate: func(revision core.SkillRevision, files map[string][]byte) (core.SkillRevision, map[string][]byte) {
			delete(files, "SKILL.md")
			return revision, files
		}},
		{name: "undeclared file", mutate: func(revision core.SkillRevision, files map[string][]byte) (core.SkillRevision, map[string][]byte) {
			files["extra.md"] = []byte("extra")
			return revision, files
		}},
		{name: "size", mutate: func(revision core.SkillRevision, files map[string][]byte) (core.SkillRevision, map[string][]byte) {
			revision.Files[0].SizeBytes++
			revision.BundleDigest = bundleDigestForTest(revision.Files)
			return revision, files
		}},
		{name: "unsafe path", mutate: func(revision core.SkillRevision, files map[string][]byte) (core.SkillRevision, map[string][]byte) {
			content := files["SKILL.md"]
			delete(files, "SKILL.md")
			files["../SKILL.md"] = content
			revision.Files[0].Path = "../SKILL.md"
			revision.BundleDigest = bundleDigestForTest(revision.Files)
			return revision, files
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			files := map[string][]byte{"SKILL.md": []byte("stable")}
			revision := testBundleRevision(t, files)
			revision, files = test.mutate(revision, files)
			if _, err := store.Publish(context.Background(), revision, files); err == nil {
				t.Fatal("invalid bundle was accepted")
			}
		})
	}
}

func TestRevisionBundleStoreRejectsSymlinkedCustodyRoot(t *testing.T) {
	projectRoot := t.TempDir()
	redirect := t.TempDir()
	if err := os.Mkdir(filepath.Join(projectRoot, ".agent-memory"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(redirect, filepath.Join(projectRoot, ".agent-memory", "skill-revisions")); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRevisionBundleStore(projectRoot); err == nil {
		t.Fatal("symlinked custody root was accepted")
	}
}

func TestRevisionBundleStoreRejectsParentReplacement(t *testing.T) {
	projectRoot := t.TempDir()
	store, err := NewRevisionBundleStore(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	opened := filepath.Join(projectRoot, ".agent-memory", "skill-revisions", "objects", "sha256")
	moved := opened + "-opened"
	redirect := opened + "-redirect"
	if err := os.Mkdir(redirect, 0o700); err != nil {
		t.Fatal(err)
	}
	store.afterRootOpen = func() {
		if err := os.Rename(opened, moved); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(redirect, opened); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string][]byte{"SKILL.md": []byte("stable")}
	if _, err := store.Publish(context.Background(), testBundleRevision(t, files), files); err == nil {
		t.Fatal("replaced custody root was accepted")
	}
	for _, directory := range []string{moved, redirect} {
		entries, err := os.ReadDir(directory)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Fatalf("publication leftovers in %s: %v", directory, entries)
		}
	}
}

func TestRevisionBundleStoreCleansInterruptedPublication(t *testing.T) {
	projectRoot := t.TempDir()
	store, err := NewRevisionBundleStore(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	store.beforeCommit = func() error { return errors.New("simulated interruption") }
	files := map[string][]byte{"SKILL.md": []byte("stable")}
	revision := testBundleRevision(t, files)
	if _, err := store.Publish(context.Background(), revision, files); err == nil {
		t.Fatal("interrupted publication succeeded")
	}
	objects := filepath.Join(projectRoot, ".agent-memory", "skill-revisions", "objects", "sha256")
	entries, err := os.ReadDir(objects)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("interrupted publication left objects: %v", entries)
	}
	if _, err := store.Read(context.Background(), revision); err == nil {
		t.Fatal("interrupted publication became readable")
	}
}

func testBundleRevision(t *testing.T, contents map[string][]byte) core.SkillRevision {
	t.Helper()
	files := make([]core.SkillBundleFile, 0, len(contents))
	for path, content := range contents {
		digest := sha256.Sum256(content)
		files = append(files, core.SkillBundleFile{Path: path, Digest: "sha256:" + hex.EncodeToString(digest[:]), SizeBytes: int64(len(content))})
	}
	sortSkillBundleFiles(files)
	return core.SkillRevision{
		ID: "revision-1", Workspace: "workspace", SkillID: "skill-1", Number: 1,
		State: core.SkillRevisionDraft, BundleDigest: bundleDigestForTest(files), ManifestVersion: 1,
		Files: files, RiskTier: core.SkillRiskMedium, CreatedBy: "test", CreatedAt: time.Now().UTC(),
	}
}

func bundleDigestForTest(files []core.SkillBundleFile) string {
	return skillBundleDigest(files)
}

func allowBundleCleanup(t *testing.T, projectRoot string) {
	t.Helper()
	t.Cleanup(func() {
		for _, relative := range []string{".agent-memory", ".agents"} {
			_ = filepath.Walk(filepath.Join(projectRoot, relative), func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return nil
				}
				if info.IsDir() {
					_ = os.Chmod(path, 0o700)
				} else {
					_ = os.Chmod(path, 0o600)
				}
				return nil
			})
		}
	})
}

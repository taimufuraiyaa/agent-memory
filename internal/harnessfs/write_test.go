package harnessfs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func readFile(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func writeFile(t *testing.T, dir, name, content string, mode os.FileMode) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

// leftovers lists every entry under dir, so a test can prove nothing stray was left.
func leftovers(t *testing.T, dir string) []string {
	t.Helper()
	var names []string
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err == nil && path != dir {
			rel, _ := filepath.Rel(dir, path)
			names = append(names, rel)
		}
		return nil
	})
	return names
}

func TestReplaceFileIsAtomicKeepsModeAndRechecksTheRevision(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "tool.sh", "old\n", 0o755)
	writeFile(t, dir, "sub/data.txt", "data\n", 0o640)
	root := open(t, dir)
	rev := Revision([]byte("old\n"))

	if err := root.ReplaceFile("tool.sh", []byte("new\n"), rev); err != nil {
		t.Fatal(err)
	}
	if readFile(t, dir, "tool.sh") != "new\n" {
		t.Fatal("content was not replaced")
	}
	if info, _ := os.Stat(filepath.Join(dir, "tool.sh")); info.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %v", info.Mode().Perm())
	}
	if err := root.ReplaceFile("sub/data.txt", []byte("more\n"), Revision([]byte("data\n"))); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(filepath.Join(dir, "sub/data.txt")); info.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %v", info.Mode().Perm())
	}

	// A file that changed since it was reviewed is left exactly as it is.
	if err := root.ReplaceFile("tool.sh", []byte("again\n"), rev); !errors.Is(err, ErrStale) {
		t.Fatalf("stale replace = %v", err)
	}
	if readFile(t, dir, "tool.sh") != "new\n" {
		t.Fatal("a stale replace changed the file")
	}
	if names := leftovers(t, dir); len(names) != 3 { // tool.sh, sub, sub/data.txt
		t.Fatalf("stray entries: %v", names)
	}
}

func TestReplaceFileRefusesEverythingItShouldNot(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	writeFile(t, dir, "ok.txt", "ok\n", 0o644)
	writeFile(t, dir, ".env", "SECRET=1\n", 0o600)
	writeFile(t, dir, "id_rsa", "KEY\n", 0o600)
	writeFile(t, outside, "stolen.txt", "OUTSIDE\n", 0o644)
	big := strings.Repeat("a", MaxWriteBytes+1)
	writeFile(t, dir, "big.txt", big, 0o644)
	if err := os.Mkdir(filepath.Join(dir, "adir"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.Symlink(filepath.Join(outside, "stolen.txt"), filepath.Join(dir, "link-out.txt"))
	_ = os.Symlink("ok.txt", filepath.Join(dir, "link-in.txt"))
	_ = os.Symlink("adir", filepath.Join(dir, "dirlink"))
	writeFile(t, dir, "adir/inner.txt", "inner\n", 0o644)
	root := open(t, dir)
	ok := Revision([]byte("ok\n"))

	cases := map[string]struct {
		path string
		data string
		rev  string
		want error
	}{
		"traversal":       {"../x.txt", "x", ok, ErrInvalidPath},
		"absolute":        {"/etc/passwd", "x", ok, ErrInvalidPath},
		"NUL":             {"a\x00b", "x", ok, ErrInvalidPath},
		"the root":        {".", "x", ok, ErrInvalidPath},
		"hidden":          {".env", "x", Revision([]byte("SECRET=1\n")), ErrDenied},
		"credential name": {"id_rsa", "x", Revision([]byte("KEY\n")), ErrDenied},
		"missing":         {"nope.txt", "x", ok, ErrStale}, // it no longer is what was reviewed
		"a directory":     {"adir", "x", ok, ErrStale},
		"link out":        {"link-out.txt", "x", ok, ErrDenied},
		"link in":         {"link-in.txt", "x", ok, ErrDenied},
		"through a link":  {"dirlink/inner.txt", "x", Revision([]byte("inner\n")), ErrDenied},
		"too much data":   {"ok.txt", big, ok, ErrTooLarge},
		"a too large old": {"big.txt", "x", Revision([]byte(big)), ErrStale},
		"wrong revision":  {"ok.txt", "x", "0000000000000000-3", ErrStale},
		"empty revision":  {"ok.txt", "x", "", ErrStale},
	}
	for name, tc := range cases {
		if err := root.ReplaceFile(tc.path, []byte(tc.data), tc.rev); !errors.Is(err, tc.want) {
			t.Errorf("%s: error = %v, want %v", name, err, tc.want)
		}
	}
	if readFile(t, dir, "ok.txt") != "ok\n" || readFile(t, dir, ".env") != "SECRET=1\n" || readFile(t, outside, "stolen.txt") != "OUTSIDE\n" || readFile(t, dir, "adir/inner.txt") != "inner\n" {
		t.Fatal("a refused replace changed a file")
	}
	for _, name := range leftovers(t, dir) {
		if strings.Contains(name, ".am-tmp-") {
			t.Fatalf("a temporary file was left behind: %s", name)
		}
	}
}

func TestCreateFileMakesParentsNeverReplacesAndCleansUp(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "exists.txt", "keep\n", 0o644)
	if err := os.Mkdir(filepath.Join(dir, "adir"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.Symlink("adir", filepath.Join(dir, "dirlink"))
	root := open(t, dir)

	made, err := root.CreateFile("a/b/new.txt", []byte("hello\n"), 0o644)
	if err != nil || len(made) != 2 || made[0] != "a" || made[1] != "a/b" || readFile(t, dir, "a/b/new.txt") != "hello\n" {
		t.Fatalf("create = %v, %v", made, err)
	}
	if made, err := root.CreateFile("adir/inside.txt", []byte("x"), 0o600); err != nil || len(made) != 0 {
		t.Fatalf("create in an existing directory = %v, %v", made, err)
	}
	if info, _ := os.Stat(filepath.Join(dir, "adir/inside.txt")); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", info.Mode().Perm())
	}

	for name, tc := range map[string]struct {
		path string
		want error
	}{
		"existing file":   {"exists.txt", ErrExists},
		"existing dir":    {"adir", ErrExists},
		"existing link":   {"dirlink", ErrDenied},
		"through a link":  {"dirlink/x.txt", ErrDenied},
		"under a file":    {"exists.txt/x.txt", ErrUnreadable},
		"traversal":       {"../x.txt", ErrInvalidPath},
		"hidden":          {".env", ErrDenied},
		"hidden parent":   {".hidden/x.txt", ErrDenied},
		"credential name": {"deploy/server.pem", ErrDenied},
		"the root":        {".", ErrInvalidPath},
	} {
		if _, err := root.CreateFile(tc.path, []byte("x"), 0o644); !errors.Is(err, tc.want) {
			t.Errorf("%s: error = %v, want %v", name, err, tc.want)
		}
	}
	if readFile(t, dir, "exists.txt") != "keep\n" {
		t.Fatal("a refused create replaced a file")
	}
	if _, err := os.Stat(filepath.Join(dir, "deploy")); err == nil {
		t.Fatal("a refused create left a directory behind")
	}
	if _, err := root.CreateFile("big.txt", make([]byte, MaxWriteBytes+1), 0o644); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized create = %v", err)
	}
}

func TestACreateThatFailsAfterMakingDirectoriesRemovesThem(t *testing.T) {
	dir := t.TempDir()
	root := open(t, dir)
	createHook = func(string) error { return ErrUnreadable }
	defer func() { createHook = nil }()
	if _, err := root.CreateFile("x/y/z/file.txt", []byte("x"), 0o644); !errors.Is(err, ErrUnreadable) {
		t.Fatalf("error = %v", err)
	}
	if names := leftovers(t, dir); len(names) != 0 {
		t.Fatalf("directories were left behind: %v", names)
	}
}

func TestRemoveFileChecksRevisionAndKind(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "gone.txt", "bye\n", 0o644)
	writeFile(t, dir, "changed.txt", "v1\n", 0o644)
	writeFile(t, dir, ".env", "S=1\n", 0o600)
	if err := os.Mkdir(filepath.Join(dir, "adir"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.Symlink("gone.txt", filepath.Join(dir, "link.txt"))
	root := open(t, dir)

	if err := root.RemoveFile("changed.txt", Revision([]byte("v0\n"))); !errors.Is(err, ErrStale) {
		t.Fatalf("stale remove = %v", err)
	}
	if err := root.RemoveFile("adir", Revision(nil)); err == nil {
		t.Fatal("a directory was removed")
	}
	if err := root.RemoveFile("link.txt", Revision([]byte("bye\n"))); !errors.Is(err, ErrDenied) {
		t.Fatalf("link remove = %v", err)
	}
	if err := root.RemoveFile(".env", Revision([]byte("S=1\n"))); !errors.Is(err, ErrDenied) {
		t.Fatalf("hidden remove = %v", err)
	}
	if err := root.RemoveFile("../x", "x"); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("traversal remove = %v", err)
	}
	if err := root.RemoveFile("gone.txt", Revision([]byte("bye\n"))); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"changed.txt", ".env", "adir", "link.txt"} {
		if _, err := os.Lstat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s should still exist: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "gone.txt")); err == nil {
		t.Fatal("the file was not removed")
	}
}

// Only one of several writers that reviewed the same revision can succeed.
func TestConcurrentReplacesFromOneRevisionHaveExactlyOneWinner(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "race.txt", "start\n", 0o644)
	root := open(t, dir)
	rev := Revision([]byte("start\n"))
	var wins, stale atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			switch err := root.ReplaceFile("race.txt", []byte("winner "+string(rune('a'+i))+"\n"), rev); {
			case err == nil:
				wins.Add(1)
			case errors.Is(err, ErrStale):
				stale.Add(1)
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 || stale.Load() != 15 {
		t.Fatalf("wins=%d stale=%d", wins.Load(), stale.Load())
	}
}

func TestInspectReadWholeAndRevisionAgreeWithRead(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "alpha\n", 0o644)
	writeFile(t, dir, "bin.dat", "x\x00y", 0o644)
	writeFile(t, dir, "exec.sh", "#!/bin/sh\n", 0o755)
	if err := os.Mkdir(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.Symlink("a.txt", filepath.Join(dir, "l.txt"))
	root := open(t, dir)

	data, rev, mode, err := root.ReadWhole("a.txt")
	read, _ := root.Read("a.txt", 1000)
	if err != nil || string(data) != "alpha\n" || rev != read.Revision || rev != Revision(data) || mode != 0o644 {
		t.Fatalf("ReadWhole = %q %q %v %v (read revision %q)", data, rev, mode, err, read.Revision)
	}
	if _, _, _, err := root.ReadWhole("bin.dat"); !errors.Is(err, ErrBinary) {
		t.Fatalf("binary = %v", err)
	}
	if _, _, _, err := root.ReadWhole("l.txt"); !errors.Is(err, ErrDenied) {
		t.Fatalf("link = %v", err)
	}
	if _, _, _, err := root.ReadWhole("d"); !errors.Is(err, ErrUnreadable) {
		t.Fatalf("directory = %v", err)
	}
	if info, err := root.Inspect("exec.sh"); err != nil || !info.Exists || !info.Regular || info.Mode != 0o755 || info.Size != 10 {
		t.Fatalf("Inspect file = %+v %v", info, err)
	}
	if info, err := root.Inspect("d"); err != nil || !info.Dir || info.Regular {
		t.Fatalf("Inspect dir = %+v %v", info, err)
	}
	if info, err := root.Inspect("missing/new.txt"); err != nil || info.Exists {
		t.Fatalf("Inspect missing = %+v %v", info, err)
	}
	for _, bad := range []string{"l.txt", ".env", "../x", "."} {
		if _, err := root.Inspect(bad); err == nil {
			t.Errorf("Inspect(%q) was accepted", bad)
		}
	}
}

// The recheck just before the rename is what protects a change made after the first look.
func TestAChangeBetweenTheFirstLookAndTheRenameIsNotOverwritten(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "race.txt", "start\n", 0o644)
	root := open(t, dir)
	replaceHook = func() { writeFile(t, dir, "race.txt", "someone else\n", 0o644) }
	defer func() { replaceHook = nil }()
	if err := root.ReplaceFile("race.txt", []byte("mine\n"), Revision([]byte("start\n"))); !errors.Is(err, ErrStale) {
		t.Fatalf("error = %v", err)
	}
	if readFile(t, dir, "race.txt") != "someone else\n" {
		t.Fatal("the other change was overwritten")
	}
	if names := leftovers(t, dir); len(names) != 1 {
		t.Fatalf("a temporary file was left behind: %v", names)
	}
}

// A file that appears after the existence check but before the create is not truncated.
func TestACreateNeverTruncatesAFileThatAppearsInTheGap(t *testing.T) {
	dir := t.TempDir()
	root := open(t, dir)
	createHook = func(string) error {
		writeFile(t, dir, "new.txt", "theirs\n", 0o644)
		return nil
	}
	defer func() { createHook = nil }()
	if _, err := root.CreateFile("new.txt", []byte("mine\n"), 0o644); !errors.Is(err, ErrExists) {
		t.Fatalf("error = %v", err)
	}
	if readFile(t, dir, "new.txt") != "theirs\n" {
		t.Fatal("a file that appeared in the gap was overwritten")
	}
}

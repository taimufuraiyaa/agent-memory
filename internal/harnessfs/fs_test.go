package harnessfs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

func write(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func open(t *testing.T, dir string) *Root {
	t.Helper()
	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

func TestCleanAcceptsOnlyLocalPaths(t *testing.T) {
	for in, want := range map[string]string{"": ".", ".": ".", "a/b.go": "a/b.go", "./a/../a/b.go": "a/b.go", "a//b": "a/b"} {
		if got, err := Clean(in); err != nil || got != want {
			t.Errorf("Clean(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"..", "../x", "a/../../x", "/etc/passwd", "/", "a\x00b", strings.Repeat("a", MaxPathBytes+1)} {
		if _, err := Clean(bad); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("Clean(%q) = %v", bad, err)
		}
	}
}

func TestDeniedNamesAreHiddenAndCredentialLike(t *testing.T) {
	for _, denied := range []string{".env", ".git/config", "a/.hidden/b", ".ssh/id_rsa", "id_rsa", "id_ed25519.pub", "id_ecdsa", "x.pem", "X.PEM", "k.key", "a.p12", "a.pfx", "a.jks", "a.keystore", "a.kdbx",
		"credentials.json", "Credentials", "secrets.yaml", "secret.txt", "dir/secrets/x"} {
		if !Denied(denied) {
			t.Errorf("%q was allowed", denied)
		}
	}
	for _, ok := range []string{".", "", "main.go", "internal/app/x.go", "docs/secretary.md", "keys.go", "monkey", "pem.go"} {
		if Denied(ok) {
			t.Errorf("%q was denied", ok)
		}
	}
}

func TestReadIsBoundedRevisionedAndRefusesBinaryAndNonRegular(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a.txt", "hello world")
	write(t, dir, "big.txt", strings.Repeat("0123456789", 1000))
	write(t, dir, "bin.dat", "PNG\x00\x01binary")
	if err := os.Mkdir(filepath.Join(dir, "adir"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := open(t, dir)
	f, err := r.Read("a.txt", 100)
	if err != nil || string(f.Data) != "hello world" || f.Truncated || f.Size != 11 || !strings.HasSuffix(f.Revision, "-11") {
		t.Fatalf("file = %+v, %v", f, err)
	}
	again, _ := r.Read("./a.txt", 100)
	if again.Revision != f.Revision {
		t.Fatal("the revision of identical bytes changed")
	}
	write(t, dir, "a.txt", "hello there")
	if changed, _ := r.Read("a.txt", 100); changed.Revision == f.Revision {
		t.Fatal("the revision did not change with the content")
	}
	big, err := r.Read("big.txt", 64)
	if err != nil || len(big.Data) != 64 || !big.Truncated || big.Size != 10000 {
		t.Fatalf("big = %d bytes truncated=%v size=%d, %v", len(big.Data), big.Truncated, big.Size, err)
	}
	if _, err := r.Read("bin.dat", 100); !errors.Is(err, ErrBinary) {
		t.Fatalf("binary = %v", err)
	}
	for _, name := range []string{"adir", "missing.txt"} {
		if _, err := r.Read(name, 100); !errors.Is(err, ErrUnreadable) {
			t.Errorf("%s = %v", name, err)
		}
	}
	if _, err := r.Read("../a.txt", 100); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("traversal = %v", err)
	}
	if _, err := r.Read(".env", 100); !errors.Is(err, ErrDenied) {
		t.Fatalf("hidden = %v", err)
	}
}

func TestSymlinksAndSpecialFilesCannotEscapeOrHang(t *testing.T) {
	outside := t.TempDir()
	write(t, outside, "stolen.txt", "OUTSIDE-SECRET")
	write(t, outside, "dir/inner.txt", "OUTSIDE-INNER")
	dir := t.TempDir()
	write(t, dir, "ok.txt", "inside")
	links := map[string]string{
		"link-out.txt":     filepath.Join(outside, "stolen.txt"), // absolute, outside
		"link-abs-in.txt":  filepath.Join(dir, "ok.txt"),         // absolute, back inside: still refused
		"link-rel-out.txt": "../" + filepath.Base(outside) + "/stolen.txt",
		"dir-out":          outside,
	}
	for name, target := range links {
		if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
			t.Skip("symlinks unavailable")
		}
	}
	if err := os.Symlink("ok.txt", filepath.Join(dir, "link-in.txt")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe"), 0o644); err != nil {
		t.Skip("fifos unavailable")
	}
	r := open(t, dir)
	for _, name := range []string{"link-out.txt", "link-abs-in.txt", "link-rel-out.txt", "dir-out/stolen.txt", "dir-out/dir/inner.txt"} {
		if f, err := r.Read(name, 100); err == nil || strings.Contains(string(f.Data), "OUTSIDE") {
			t.Errorf("%s was readable: %q, %v", name, f.Data, err)
		}
	}
	if f, err := r.Read("link-in.txt", 100); err != nil || string(f.Data) != "inside" {
		t.Fatalf("a relative link that stays inside the root should be followed: %q, %v", f.Data, err)
	}
	done := make(chan error, 1)
	go func() { _, err := r.Read("pipe", 100); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, ErrUnreadable) {
			t.Fatalf("fifo = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reading a FIFO blocked")
	}
	if _, _, err := r.ReadDir("dir-out", 100); !errors.Is(err, ErrUnreadable) {
		t.Fatalf("listing an escaping directory link = %v", err)
	}
	if !r.Exists("link-out.txt") || !r.Exists("ok.txt") || r.Exists("nope") || r.Exists("../x") {
		t.Fatal("Exists must see a blocked link as present and nothing outside the root")
	}
}

func TestReadDirListsSortedHidesProtectedNamesAndReportsKinds(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "b.go", "x")
	write(t, dir, "a.go", "yy")
	write(t, dir, ".env", "SECRET")
	write(t, dir, "id_rsa", "KEY")
	write(t, dir, "server.pem", "CERT")
	write(t, dir, "sub/c.go", "z")
	if err := os.Symlink("a.go", filepath.Join(dir, "lnk")); err != nil {
		t.Skip("symlinks unavailable")
	}
	r := open(t, dir)
	entries, truncated, err := r.ReadDir(".", 100)
	if err != nil || truncated {
		t.Fatal(err)
	}
	got := make([]string, len(entries))
	for i, e := range entries {
		got[i] = e.Name + ":" + e.Kind
	}
	if want := []string{"a.go:file", "b.go:file", "lnk:other", "sub:dir"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("entries = %v, want %v", got, want)
	}
	if entries[0].Size != 2 {
		t.Fatalf("size = %d", entries[0].Size)
	}
	for i := 0; i < 20; i++ {
		write(t, dir, fmt.Sprintf("f%02d.txt", i), "x")
	}
	cut, truncated, _ := r.ReadDir(".", 5)
	if len(cut) != 5 || !truncated {
		t.Fatalf("cut = %d truncated=%v", len(cut), truncated)
	}
	if _, _, err := r.ReadDir("a.go", 10); !errors.Is(err, ErrUnreadable) {
		t.Fatalf("a file listed as a directory = %v", err)
	}
	if _, _, err := r.ReadDir(".git", 10); !errors.Is(err, ErrDenied) {
		t.Fatalf("hidden directory = %v", err)
	}
	if _, _, err := r.ReadDir("../", 10); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("traversal = %v", err)
	}
}

func TestWalkIsOrderedBoundedAndNeverFollowsOrEntersProtectedPlaces(t *testing.T) {
	outside := t.TempDir()
	write(t, outside, "stolen.go", "OUTSIDE")
	dir := t.TempDir()
	for _, f := range []string{"z.go", "a.go", "m/b.go", "m/deep/c.go", "m/deep/deeper/d.go", ".hidden/x.go", "node_modules/pkg/y.go", "id_rsa", "secrets/s.go", "m/.git/config"} {
		write(t, dir, f, "x")
	}
	if err := os.Symlink(outside, filepath.Join(dir, "linkdir")); err != nil {
		t.Skip("symlinks unavailable")
	}
	_ = os.Symlink(filepath.Join(outside, "stolen.go"), filepath.Join(dir, "linkfile.go"))
	_ = syscall.Mkfifo(filepath.Join(dir, "pipe"), 0o644)
	r := open(t, dir)
	collect := func(rel string, opts WalkOptions) ([]string, bool) {
		var seen []string
		_, truncated, err := r.Walk(rel, opts, func(path string, _ int64) error { seen = append(seen, path); return nil })
		if err != nil {
			t.Fatal(err)
		}
		return seen, truncated
	}
	opts := WalkOptions{SkipDirs: map[string]bool{"node_modules": true}}
	got, truncated := collect(".", opts)
	want := []string{"a.go", "m/b.go", "m/deep/c.go", "m/deep/deeper/d.go", "z.go"}
	if !reflect.DeepEqual(got, want) || truncated {
		t.Fatalf("walk = %v truncated=%v, want %v", got, truncated, want)
	}
	for i := 0; i < 10; i++ {
		if again, _ := collect(".", opts); !reflect.DeepEqual(again, got) {
			t.Fatal("the walk order is not deterministic")
		}
	}
	if shallow, _ := collect(".", WalkOptions{MaxDepth: 2, SkipDirs: opts.SkipDirs}); !reflect.DeepEqual(shallow, []string{"a.go", "m/b.go", "z.go"}) {
		t.Fatalf("depth-limited walk = %v", shallow)
	}
	capped, truncated := collect(".", WalkOptions{MaxFiles: 2, SkipDirs: opts.SkipDirs})
	if len(capped) != 2 || !truncated {
		t.Fatalf("capped = %v truncated=%v", capped, truncated)
	}
	if sub, _ := collect("m", opts); !reflect.DeepEqual(sub, []string{"m/b.go", "m/deep/c.go", "m/deep/deeper/d.go"}) {
		t.Fatalf("subtree = %v", sub)
	}
	var stopAfterOne int
	visited, _, err := r.Walk(".", opts, func(string, int64) error { stopAfterOne++; return ErrStop })
	if err != nil || stopAfterOne != 1 || visited != 1 {
		t.Fatalf("ErrStop: visited=%d calls=%d err=%v", visited, stopAfterOne, err)
	}
	boom := errors.New("boom")
	if _, _, err := r.Walk(".", opts, func(string, int64) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("a visit error must abort: %v", err)
	}
	for _, bad := range []string{"..", ".git", "../.."} {
		if _, _, err := r.Walk(bad, opts, func(string, int64) error { return nil }); err == nil {
			t.Errorf("walk of %q was accepted", bad)
		}
	}
	if n, _, _ := r.Walk("a.go", opts, func(string, int64) error { return nil }); n != 0 {
		t.Fatalf("walking a file visited %d", n)
	}
}

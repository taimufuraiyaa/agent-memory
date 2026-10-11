package harnessgit

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func kinds2(entries []StatusEntry) map[string]string {
	out := map[string]string{}
	for _, e := range entries {
		out[e.Path] = e.Kind
	}
	return out
}

func TestParsingGitsStatusFormat(t *testing.T) {
	raw := "# branch.oid 0123456789abcdef0123456789abcdef01234567\x00# branch.head main\x00# branch.upstream origin/main\x00# branch.ab +2 -1\x00" +
		"1 .M N... 100644 100644 100644 aaaaaaaa bbbbbbbb src/app.go\x00" +
		"1 A. N... 000000 100644 100644 00000000 cccccccc new file.txt\x00" +
		"1 D. N... 100644 000000 000000 dddddddd 00000000 gone.txt\x00" +
		"1 .T N... 100644 100644 120000 aaaaaaaa bbbbbbbb type.txt\x00" +
		"2 R. N... 100644 100644 100644 aaaaaaaa bbbbbbbb R100 renamed.txt\x00old name.txt\x00" +
		"u UU N... 100644 100644 100644 100644 a b c conflict.txt\x00" +
		"? untracked.txt\x00? dir/with space.txt\x00! ignored.log\x00" +
		"? .env\x00? id_rsa\x00? deploy/server.pem\x00? .hidden/x\x00" +
		"2 R. N... 100644 100644 100644 aaaaaaaa bbbbbbbb R100 visible.txt\x00.secret-old\x00"
	st := parseStatus([]byte(raw))
	if st.Branch != "main" || st.Head != "0123456789abcdef0123456789abcdef01234567" || st.Upstream != "origin/main" || st.Ahead != 2 || st.Behind != 1 {
		t.Fatalf("branch = %+v", st)
	}
	want := map[string]string{"src/app.go": "modified", "new file.txt": "added", "gone.txt": "deleted", "type.txt": "typechanged", "renamed.txt": "renamed",
		"conflict.txt": "conflicted", "untracked.txt": "untracked", "dir/with space.txt": "untracked"}
	if got := kinds2(st.Entries); !reflect.DeepEqual(got, want) {
		t.Fatalf("entries = %v", got)
	}
	// Paths the harness never shows are counted, not listed: .env, id_rsa, the key, the hidden
	// directory, and the rename from a hidden name.
	if st.Omitted != 5 || st.Truncated {
		t.Fatalf("omitted = %d truncated = %v", st.Omitted, st.Truncated)
	}
	for _, e := range st.Entries {
		if e.Path == "renamed.txt" && (e.From != "old name.txt" || e.Staged != "R" || e.Unstaged != "") {
			t.Fatalf("rename = %+v", e)
		}
		if e.Path == "src/app.go" && (e.Staged != "" || e.Unstaged != "M") {
			t.Fatalf("columns = %+v", e)
		}
	}
	// The initial branch has no commit.
	if init := parseStatus([]byte("# branch.oid (initial)\x00# branch.head main\x00")); init.Head != "" || init.Branch != "main" || len(init.Entries) != 0 {
		t.Fatalf("initial = %+v", init)
	}
	// Garbage and truncated records are skipped, never trusted or fatal.
	if junk := parseStatus([]byte("1 .M short\x002 R. too few\x00u UU\x00? \x00zzz\x00\x00")); len(junk.Entries) != 1 {
		t.Fatalf("junk = %+v", junk)
	}
	// The list is bounded.
	var many strings.Builder
	for i := 0; i < MaxStatusEntries+7; i++ {
		many.WriteString("? f")
		many.WriteString(strings.Repeat("x", i%5))
		many.WriteString("-")
		many.WriteString(strings.Repeat("y", i%7))
		many.WriteString(string(rune('a' + i%26)))
		many.WriteString(strings.Repeat("z", i/26))
		many.WriteString("\x00")
	}
	if big := parseStatus([]byte(many.String())); len(big.Entries) != MaxStatusEntries || !big.Truncated {
		t.Fatalf("big = %d truncated %v", len(big.Entries), big.Truncated)
	}
}

func TestStatusOfARealRepositoryListsKindsAndHidesProtectedNames(t *testing.T) {
	e := newRepo(t)
	e.write("src/app.go", "package app\n\nfunc F() {}\n")
	e.write("new.txt", "x\n")
	e.write(".env", "TOKEN=1\n")
	e.write("id_rsa", "KEY\n")
	e.write("unicode-é.txt", "x\n")
	e.git("rm", "-q", "README.md")
	e.git("add", "new.txt")
	g := e.open()
	st, err := g.Status(bg)
	if err != nil {
		t.Fatal(err)
	}
	got := kinds2(st.Entries)
	if got["src/app.go"] != "modified" || got["new.txt"] != "added" || got["README.md"] != "deleted" || got["unicode-é.txt"] != "untracked" || st.Branch != "main" || st.Head == "" {
		t.Fatalf("status = %+v", st)
	}
	if _, ok := got[".env"]; ok || st.Omitted != 2 {
		t.Fatalf("protected names were listed or not counted: %+v", st)
	}
	for _, entry := range st.Entries {
		if entry.Path == "new.txt" && (entry.Staged != "A") || entry.Path == "src/app.go" && entry.Unstaged != "M" {
			t.Fatalf("columns: %+v", entry)
		}
	}
}

func TestStatusOfAFreshRepositoryWithNoCommits(t *testing.T) {
	gitPath := needGit(t)
	e := &repoEnv{t: t, root: realTemp(t), home: realTemp(t), temp: filepath.Join(realTemp(t), "tmp")}
	e.cfg = Config{SearchPath: []string{filepath.Dir(gitPath)}, Home: e.home, TempRoot: e.temp}
	e.setup = []string{"PATH=" + filepath.Dir(gitPath) + ":/usr/bin:/bin", "HOME=" + e.home, "GIT_CONFIG_NOSYSTEM=1", "LC_ALL=C"}
	e.git("init", "-q", "-b", "trunk")
	e.write("first.txt", "x\n")
	g := e.open()
	st, err := g.Status(bg)
	if err != nil || st.Branch != "trunk" || st.Head != "" || kinds2(st.Entries)["first.txt"] != "untracked" {
		t.Fatalf("%+v %v", st, err)
	}
	if _, err := g.Head(bg); !errors.Is(err, ErrUnborn) {
		t.Fatalf("head = %v", err)
	}
	log, err := g.Log(bg, 5, "")
	if err != nil || !log.Unborn || len(log.Entries) != 0 {
		t.Fatalf("log = %+v %v", log, err)
	}
	// An index with something in it commits as the first, parentless commit.
	if err := g.Add(bg, []string{"first.txt"}); err != nil {
		t.Fatal(err)
	}
	e.git("config", "user.name", "T")
	e.git("config", "user.email", "t@example.com")
	done, err := g.Commit(bg, "the first commit")
	if err != nil || done.Parent != "" || done.Commit == "" || done.Tree == "" {
		t.Fatalf("%+v %v", done, err)
	}
}

func TestDiffShowsStagedAndUnstagedChangesWithoutProtectedFilesOrExternalPrograms(t *testing.T) {
	e := newRepo(t)
	e.write(".env", "TOKEN=old\n")
	e.write("id_rsa", "KEY-old\n")
	e.git("add", "-f", ".env", "id_rsa")
	e.git("commit", "-q", "-m", "tracked secrets, as a mistake")
	e.write("src/app.go", "package app\n\nfunc Changed() {}\n")
	e.write(".env", "TOKEN=SECRETVALUE\n")
	e.write("id_rsa", "KEY-SECRETVALUE\n")
	g := e.open()
	d, err := g.Diff(bg, DiffOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d.Text, "+func Changed() {}") || !strings.Contains(d.Text, "--- a/src/app.go") || d.Paths != 1 || d.Omitted != 2 || d.Truncated {
		t.Fatalf("diff = %+v", d)
	}
	if strings.Contains(d.Text, "SECRETVALUE") || strings.Contains(d.Text, "TOKEN") || strings.Contains(d.Text, "id_rsa") {
		t.Fatalf("a protected file reached the diff:\n%s", d.Text)
	}
	// Staged and unstaged are separate.
	if err := g.Add(bg, []string{"src/app.go"}); err != nil {
		t.Fatal(err)
	}
	unstaged, _ := g.Diff(bg, DiffOptions{})
	staged, _ := g.Diff(bg, DiffOptions{Staged: true})
	if strings.Contains(unstaged.Text, "Changed") || !strings.Contains(staged.Text, "+func Changed() {}") || staged.Paths != 1 || unstaged.Paths != 0 {
		t.Fatalf("unstaged %+v\nstaged %+v", unstaged, staged)
	}
	// One path.
	e.write("README.md", "# project\n\nmore\n")
	only, err := g.Diff(bg, DiffOptions{Path: "README.md"})
	if err != nil || only.Paths != 1 || !strings.Contains(only.Text, "+more") || strings.Contains(only.Text, "app.go") {
		t.Fatalf("%+v %v", only, err)
	}
	// A directory scope, and a change that is binary, which is described and not shown.
	if err := os.WriteFile(filepath.Join(e.root, "src", "blob.bin"), []byte("\x00\x01\x02binary"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.git("add", "src/blob.bin")
	dir, _ := g.Diff(bg, DiffOptions{Staged: true, Path: "src"})
	if !strings.Contains(dir.Text, "Binary files") || strings.Contains(dir.Text, "README") {
		t.Fatalf("scoped diff:\n%s", dir.Text)
	}
	// Refused scopes.
	for _, bad := range []string{"../x", "/etc/passwd", ".env", ".git/config", "-x", ":(top)x", "a\nb", "a\x00b", strings.Repeat("a", 600), "a/../b", "./README.md", `a\b`} {
		if _, err := g.Diff(bg, DiffOptions{Path: bad}); err == nil {
			t.Errorf("Diff accepted the path %q", bad)
		}
	}
}

func TestADiffThatIsTooLargeIsCutAndSaysSo(t *testing.T) {
	e := newRepo(t)
	var b strings.Builder
	for i := 0; i < 20000; i++ {
		b.WriteString("a fairly long line of source text that will be changed in bulk\n")
	}
	e.write("big.txt", b.String())
	e.git("add", "big.txt")
	e.git("commit", "-q", "-m", "big")
	e.write("big.txt", strings.ReplaceAll(b.String(), "fairly", "VERY"))
	d, err := Diff2(e, DiffOptions{})
	if err != nil || !d.Truncated || len(d.Text) > MaxStdout {
		t.Fatalf("truncated=%v len=%d err=%v", d.Truncated, len(d.Text), err)
	}
}

func Diff2(e *repoEnv, opts DiffOptions) (Diff, error) { return e.open().Diff(bg, opts) }

func TestDiffControlCharactersAreRemoved(t *testing.T) {
	e := newRepo(t)
	e.write("note.txt", "clean\n")
	e.git("add", "note.txt")
	e.git("commit", "-q", "-m", "note")
	e.write("note.txt", "red \x1b[31mtext\x1b[0m \x07bell\n")
	d, err := e.open().Diff(bg, DiffOptions{})
	if err != nil || strings.ContainsAny(d.Text, "\x1b\x07") || !strings.Contains(d.Text, "red") {
		t.Fatalf("%q %v", d.Text, err)
	}
}

func TestLogListsRecentCommitsBoundedAndSanitized(t *testing.T) {
	e := newRepo(t)
	for i := 0; i < 4; i++ {
		e.write("n.txt", strings.Repeat("x", i+1))
		e.git("add", "n.txt")
		e.git("commit", "-q", "-m", "change number "+string(rune('0'+i)))
	}
	e.git("commit", "-q", "--allow-empty", "-m", "red \x1b[31msubject\x1b[0m with a bell \x07")
	g := e.open()
	log, err := g.Log(bg, 3, "")
	if err != nil || len(log.Entries) != 3 || log.Unborn {
		t.Fatalf("%+v %v", log, err)
	}
	first := log.Entries[0]
	if !hashRE.MatchString(first.Commit) || first.Author != "Test Person" || !strings.HasPrefix(first.Date, "20") || strings.ContainsAny(first.Subject, "\x1b\x07") ||
		!strings.Contains(first.Subject, "subject") {
		t.Fatalf("first = %+v", first)
	}
	if log.Entries[1].Subject != "change number 3" {
		t.Fatalf("second = %+v", log.Entries[1])
	}
	all, _ := g.Log(bg, MaxLogEntries, "")
	if len(all.Entries) != 6 {
		t.Fatalf("all = %d", len(all.Entries))
	}
	byPath, err := g.Log(bg, 10, "n.txt")
	if err != nil || len(byPath.Entries) != 4 {
		t.Fatalf("by path = %+v %v", byPath, err)
	}
	for _, n := range []int{0, -1, MaxLogEntries + 1} {
		if _, err := g.Log(bg, n, ""); !errors.Is(err, ErrInvalid) {
			t.Errorf("n=%d: %v", n, err)
		}
	}
	if _, err := g.Log(bg, 3, "../x"); err == nil {
		t.Error("a bad log path was accepted")
	}
}

// Commit text is written by whoever made the commit, so it may contain any character Git
// allows, including the ones the log format uses to separate records and fields.
func TestAHostileCommitMessageCannotForgeOrHideLogEntries(t *testing.T) {
	e := newRepo(t)
	forged := strings.Repeat("a", 40)
	e.git("commit", "-q", "--allow-empty", "-m", "real subject\x1e"+forged+"\x1fFake Author\x1f2020-01-01T00:00:00Z\x1fforged subject")
	e.git("commit", "-q", "--allow-empty", "-m", "a subject with a field mark \x1f inside")
	g := e.open()
	log, err := g.Log(bg, 5, "")
	if err != nil || len(log.Entries) != 3 {
		t.Fatalf("%d entries (want the 3 real commits): %+v %v", len(log.Entries), log, err)
	}
	for _, entry := range log.Entries {
		if entry.Commit == forged || entry.Author == "Fake Author" || entry.Subject == "forged subject" {
			t.Fatalf("a commit message forged a log entry: %+v", entry)
		}
	}
	if !strings.Contains(log.Entries[0].Subject, "field mark") || !strings.Contains(log.Entries[1].Subject, "real subject") {
		t.Fatalf("a real commit was cut or hidden: %+v", log.Entries[:2])
	}
}

func TestParseLogReadsOnlyWholeGroupsWithARealHash(t *testing.T) {
	hash := strings.Repeat("c", 40)
	other := strings.Repeat("d", 64)
	group := func(h, author, date, subject string) string {
		return "\x00" + h + "\x00" + author + "\x00" + date + "\x00" + subject + "\n"
	}
	for name, tc := range map[string]struct {
		raw  string
		want []string
	}{
		"nothing":             {"", nil},
		"one":                 {group(hash, "A", "2020-01-01T00:00:00Z", "first"), []string{hash}},
		"two":                 {group(hash, "A", "d", "one") + group(other, "B", "d", "two"), []string{hash, other}},
		"a bad hash":          {group("nothex", "A", "d", "x") + group(hash, "A", "d", "y"), []string{hash}},
		"a short hash":        {group(strings.Repeat("c", 39), "A", "d", "x"), nil},
		"upper case hash":     {group(strings.ToUpper(hash), "A", "d", "x"), nil},
		"cut short":           {group(hash, "A", "d", "one") + "\x00" + other + "\x00B", []string{hash}},
		"no leading marker":   {hash + "\x00A\x00d\x00x\n", nil},
		"a bad group between": {group(hash, "A", "d", "one") + group("bad", "A", "d", "two") + group(other, "B", "d", "three"), []string{hash, other}},
	} {
		var got []string
		for _, e := range parseLog([]byte(tc.raw)) {
			got = append(got, e.Commit)
		}
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("%s: %v, want %v", name, got, tc.want)
		}
	}
	entries := parseLog([]byte(group(hash, "Red \x1b[31mName", "d", "subject \x07 with controls")))
	if len(entries) != 1 || entries[0].Author != "Red Name" || entries[0].Subject != "subject  with controls" {
		t.Fatalf("%+v", entries)
	}
}

func TestAddStagesExactlyTheNamedFilesAndReadsNothingAsAPattern(t *testing.T) {
	e := newRepo(t)
	e.write("a.txt", "a\n")
	e.write("b.txt", "b\n")
	e.write("*.txt", "a file whose name is a pattern\n")
	e.write("-flag.txt", "starts with a dash\n")
	e.write("sub/c.txt", "c\n")
	e.write("with space.txt", "s\n")
	g := e.open()
	if err := g.Add(bg, []string{"*.txt"}); err != nil {
		t.Fatal(err)
	}
	staged, err := g.Staged(bg)
	if err != nil || len(staged) != 1 || staged[0].Path != "*.txt" || staged[0].Status != "A" {
		t.Fatalf("a literal name was treated as a pattern: %+v %v", staged, err)
	}
	if err := g.Add(bg, []string{"with space.txt", "sub/c.txt"}); err != nil {
		t.Fatal(err)
	}
	if staged, _ = g.Staged(bg); len(staged) != 3 {
		t.Fatalf("%+v", staged)
	}
	// A deletion is staged by naming the path.
	e.git("commit", "-q", "-m", "three files")
	if err := os.Remove(filepath.Join(e.root, "a.txt")); err != nil {
		// a.txt was never committed; use a committed file instead
		_ = err
	}
	if err := os.Remove(filepath.Join(e.root, "README.md")); err != nil {
		t.Fatal(err)
	}
	if err := g.Add(bg, []string{"README.md"}); err != nil {
		t.Fatal(err)
	}
	if staged, _ = g.Staged(bg); len(staged) != 1 || staged[0].Status != "D" || staged[0].Path != "README.md" {
		t.Fatalf("a deletion was not staged: %+v", staged)
	}
	// Refusals happen before Git runs, for the whole call.
	many := make([]string, MaxStagePaths+1)
	for i := range many {
		many[i] = "f" + strings.Repeat("x", i)
	}
	for name, paths := range map[string][]string{
		"nothing":        nil,
		"too many":       many,
		"a hidden file":  {"b.txt", ".env"},
		"a dash":         {"-flag.txt"},
		"a magic prefix": {":(top)b.txt"},
		"traversal":      {"../x"},
		"absolute":       {"/etc/hosts"},
		"a newline":      {"a\nb"},
		"inside .git":    {".git/config"},
		"a dot":          {"."},
		"empty":          {""},
		"unclean":        {"sub/../b.txt"},
		"a key":          {"server.pem"},
	} {
		if err := g.Add(bg, paths); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if got, _ := g.Staged(bg); len(got) != 1 {
		t.Fatalf("a refused Add changed the index: %+v", got)
	}
}

func TestANestedRepositoryIsRefusedNotFollowed(t *testing.T) {
	e := newRepo(t)
	inner := filepath.Join(e.root, "vendor-copy")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.name", "I"}, {"config", "user.email", "i@example.com"}} {
		cmd := execIn(inner, e.setup, args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}
	e.write("vendor-copy/x.txt", "x\n")
	g := e.open()
	if !g.Repo().Nested("vendor-copy/x.txt") || g.Repo().Nested("src/app.go") || g.Repo().Nested("README.md") {
		t.Fatal("nested detection is wrong")
	}
	if err := g.Add(bg, []string{"vendor-copy/x.txt"}); !errors.Is(err, ErrSubmodule) {
		t.Fatalf("a path in a nested repository = %v", err)
	}
}

func TestStagedRefusesWhatMustNeverBeCommittedByThisRoute(t *testing.T) {
	e := newRepo(t)
	g := e.open()
	if _, err := g.Staged(bg); !errors.Is(err, ErrNothingStaged) {
		t.Fatalf("empty index = %v", err)
	}
	// A secret someone forced into the index.
	e.write(".env", "TOKEN=1\n")
	e.git("add", "-f", ".env")
	e.write("fine.txt", "ok\n")
	e.git("add", "fine.txt")
	if _, err := g.Staged(bg); !errors.Is(err, ErrProtectedPath) {
		t.Fatalf("a staged secret = %v", err)
	}
	e.git("reset", "-q", ".env")
	if got, err := g.Staged(bg); err != nil || len(got) != 1 {
		t.Fatalf("%+v %v", got, err)
	}
	// A submodule entry (a gitlink) put in the index by other means.
	e.git("update-index", "--add", "--cacheinfo", "160000,"+e.git("rev-parse", "HEAD")+",sub")
	if _, err := g.Staged(bg); !errors.Is(err, ErrSubmodule) {
		t.Fatalf("a staged gitlink = %v", err)
	}
	e.git("rm", "-q", "--cached", "sub")
	// Modes and types are reported.
	if err := os.Chmod(filepath.Join(e.root, "src", "app.go"), 0o755); err != nil {
		t.Fatal(err)
	}
	e.git("add", "src/app.go")
	got, err := g.Staged(bg)
	if err != nil || len(got) != 2 {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestIdentityIsTheConfiguredOneAndIsNeverInvented(t *testing.T) {
	e := newRepo(t)
	g := e.open()
	author, err := g.Identity(bg)
	if err != nil || author.Name != "Test Person" || author.Email != "test@example.com" {
		t.Fatalf("%+v %v", author, err)
	}
	e.git("config", "--unset", "user.name")
	e.git("config", "--unset", "user.email")
	if _, err := g.Identity(bg); !errors.Is(err, ErrNoIdentity) {
		t.Fatalf("no configured identity = %v", err)
	}
	e.write("a.txt", "a\n")
	if err := g.Add(bg, []string{"a.txt"}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Commit(bg, "should not be made"); !errors.Is(err, ErrNoIdentity) {
		t.Fatalf("a commit with no identity = %v", err)
	}
	if n := strings.Count(e.git("log", "--oneline"), "\n") + 1; n != 1 {
		t.Fatalf("a commit was made with an invented identity (%d commits)", n)
	}
}

func TestCommitRecordsExactlyTheIndexWithTheMessageAndVerifiesTheResult(t *testing.T) {
	e := newRepo(t)
	g := e.open()
	e.write("src/app.go", "package app\n\n// changed\n")
	e.write("untracked-not-staged.txt", "left alone\n")
	if err := g.Add(bg, []string{"src/app.go"}); err != nil {
		t.Fatal(err)
	}
	headBefore, _ := g.Head(bg)
	tree, err := g.IndexTree(bg)
	if err != nil {
		t.Fatal(err)
	}
	done, err := g.Commit(bg, "Change app\n\nA body line.\n\tindented")
	if err != nil {
		t.Fatal(err)
	}
	if done.Tree != tree || done.Parent != headBefore || done.Commit == headBefore || done.Commit == "" {
		t.Fatalf("committed = %+v (approved tree %s, parent %s)", done, tree, headBefore)
	}
	if subject := e.git("log", "-1", "--format=%s"); subject != "Change app" {
		t.Fatalf("subject = %q", subject)
	}
	if body := e.git("log", "-1", "--format=%b"); !strings.Contains(body, "A body line.") {
		t.Fatalf("body = %q", body)
	}
	if status := e.git("status", "--porcelain"); status != "?? untracked-not-staged.txt" {
		t.Fatalf("a commit took more than the index: %q", status)
	}
	// Nothing staged: git refuses and so do we, no empty commit is made.
	if _, err := g.Commit(bg, "an empty commit"); err == nil {
		t.Fatal("an empty commit was made")
	}
}

func TestCommitMessagesAreBoundedAndPlain(t *testing.T) {
	for name, tc := range map[string]struct {
		message string
		ok      bool
	}{
		"a plain message":   {"fix: a thing", true},
		"with a body":       {"subject\n\nbody\twith a tab\n", true},
		"unicode":           {"añadir función ✓", true},
		"empty":             {"", false},
		"only space":        {" \n\t ", false},
		"too long":          {strings.Repeat("a", MaxMessageBytes+1), false},
		"exactly the limit": {strings.Repeat("a", MaxMessageBytes), true},
		"an escape":         {"a\x1b[31mb", false},
		"a NUL":             {"a\x00b", false},
		"a carriage return": {"a\rb", false},
		"DEL":               {"a\x7fb", false},
		"invalid UTF-8":     {"a\xffb", false},
	} {
		if got := ValidMessage(tc.message); got != tc.ok {
			t.Errorf("%s: ValidMessage = %v", name, got)
		}
	}
	e := newRepo(t)
	g := e.open()
	e.write("a.txt", "a\n")
	if err := g.Add(bg, []string{"a.txt"}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Commit(bg, "bad\x1b[31m message"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a message with an escape = %v", err)
	}
	if _, err := g.Commit(bg, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("an empty message = %v", err)
	}
}

func TestTheRunnerRefusesWhatIsNotASubcommand(t *testing.T) {
	e := newRepo(t)
	g := e.open()
	for _, args := range [][]string{nil, {}, {"-c", "core.fsmonitor=evil", "status"}, {"--exec-path=/tmp", "status"}, {"-C", "/", "status"}, {"-c", "user.useConfigOnly=true"}, {"-c"}} {
		if _, err := g.Run(bg, DefaultReadTimeout, args...); err == nil {
			t.Errorf("%v was run", args)
		}
	}
	// Git's own failures are reported with bounded text and without the repository's path.
	if _, err := g.Log(bg, 3, "nonexistent-path-xyz"); err != nil {
		t.Fatalf("a log of a missing path is empty, not an error: %v", err)
	}
	if _, err := g.ok(bg, DefaultReadTimeout, "rev-parse", "--verify", "nonexistent-ref"); !errors.Is(err, ErrFailed) || len(err.Error()) > 600 {
		t.Fatalf("%v", err)
	}
}

// A commit can start background maintenance, which would outlive the call and run in a group
// the harness cannot stop. The override keeps it from starting, even when the person's own
// configuration asks for it.
func TestAutomaticMaintenanceNeverFollowsACommit(t *testing.T) {
	e := newRepo(t)
	if err := os.WriteFile(filepath.Join(e.home, ".gitconfig"), []byte("[gc]\n\tauto = 1\n\tautoDetach = false\n[maintenance]\n\tauto = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1800; i++ {
		e.write(fmt.Sprintf("many/f%04d.txt", i), fmt.Sprintf("content %d\n", i))
	}
	g := e.open()
	e.git("-c", "core.hooksPath=/dev/null", "-c", "gc.auto=0", "add", "-A")
	if _, err := g.Commit(bg, "a large commit"); err != nil {
		t.Fatal(err)
	}
	if packs, _ := filepath.Glob(filepath.Join(e.root, ".git", "objects", "pack", "*.pack")); len(packs) != 0 {
		t.Fatalf("a commit started garbage collection: %v", packs)
	}
	// Sanity: with the same global configuration, a plain commit does collect, so the check
	// above would catch a missing override.
	e.write("one-more.txt", "x\n")
	e.git("add", "one-more.txt")
	e.git("-c", "core.hooksPath=/dev/null", "commit", "-q", "-m", "plain commit")
	if packs, _ := filepath.Glob(filepath.Join(e.root, ".git", "objects", "pack", "*.pack")); len(packs) == 0 {
		t.Skip("this Git did not collect after a plain commit with this configuration, so the override cannot be shown to matter here")
	}
}

func TestAddRefusesDirectoriesAndLinksAndAcceptsAGoneFile(t *testing.T) {
	e := newRepo(t)
	e.write("dir/inner.txt", "x\n")
	if err := os.Symlink("README.md", filepath.Join(e.root, "alias.txt")); err != nil {
		t.Fatal(err)
	}
	g := e.open()
	for name, path := range map[string]string{"a directory": "dir", "a link": "alias.txt"} {
		if err := g.Add(bg, []string{path}); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if got, _ := g.Staged(bg); len(got) != 0 {
		t.Fatalf("a refused Add staged something: %+v", got)
	}
	// A gone path is staging a deletion when the file was tracked, and an error from Git when it was not.
	if err := os.Remove(filepath.Join(e.root, "README.md")); err != nil {
		t.Fatal(err)
	}
	if err := g.Add(bg, []string{"README.md"}); err != nil {
		t.Fatalf("staging a deletion: %v", err)
	}
	if err := g.Add(bg, []string{"never-existed.txt"}); !errors.Is(err, ErrFailed) {
		t.Fatalf("a path that never existed: %v", err)
	}
}

func TestADiffOverTooManyFilesIsCutAtThePathBoundAndSaysSo(t *testing.T) {
	e := newRepo(t)
	for i := 0; i < MaxDiffPaths+30; i++ {
		e.write(fmt.Sprintf("bulk/f%03d.txt", i), "before\n")
	}
	e.git("add", "-A")
	e.git("commit", "-q", "-m", "bulk")
	for i := 0; i < MaxDiffPaths+30; i++ {
		e.write(fmt.Sprintf("bulk/f%03d.txt", i), "after\n")
	}
	d, err := e.open().Diff(bg, DiffOptions{})
	if err != nil || d.Paths != MaxDiffPaths || !d.Truncated || !strings.Contains(d.Text, "bulk/f000.txt") || strings.Contains(d.Text, "bulk/f229.txt") {
		t.Fatalf("paths=%d truncated=%v err=%v", d.Paths, d.Truncated, err)
	}
}

func TestAddRefusesMoreThanTheBoundEvenWhenEveryFileExists(t *testing.T) {
	e := newRepo(t)
	var paths []string
	for i := 0; i < MaxStagePaths+1; i++ {
		name := fmt.Sprintf("many%02d.txt", i)
		e.write(name, "x\n")
		paths = append(paths, name)
	}
	g := e.open()
	if err := g.Add(bg, paths); !errors.Is(err, ErrInvalid) {
		t.Fatalf("over the bound = %v", err)
	}
	if err := g.Add(bg, paths[:MaxStagePaths]); err != nil {
		t.Fatalf("at the bound = %v", err)
	}
}

// A commit takes the index and nothing else: a tracked file that was changed but not staged
// stays changed.
func TestCommitNeverTakesAChangeThatWasNotStaged(t *testing.T) {
	e := newRepo(t)
	g := e.open()
	e.write("src/app.go", "package app\n\n// staged change\n")
	e.write("README.md", "# project\n\nan unstaged change to a tracked file\n")
	if err := g.Add(bg, []string{"src/app.go"}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Commit(bg, "only what was staged"); err != nil {
		t.Fatal(err)
	}
	if status := e.git("status", "--porcelain"); status != "M README.md" && status != "M README.md\n" {
		t.Fatalf("a commit took a change that was not staged: %q", status)
	}
	if changed := e.git("show", "--name-only", "--format=", "HEAD"); changed != "src/app.go" {
		t.Fatalf("the commit holds %q", changed)
	}
}

func objectCount(t *testing.T, root string) int {
	t.Helper()
	n := 0
	_ = filepath.WalkDir(filepath.Join(root, ".git", "objects"), func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			n++
		}
		return nil
	})
	return n
}

func TestPathStatesReportOnlyTheNamedPathsAndOnlyWhatHasSomethingToStage(t *testing.T) {
	e := newRepo(t)
	e.write("src/app.go", "package app\n\n// edited\n")
	e.write("fresh.txt", "new\n")
	e.write("untouched.txt", "same\n")
	e.git("add", "untouched.txt")
	e.git("commit", "-q", "-m", "untouched")
	e.write(".gitignore", "ignored.log\n")
	e.write("ignored.log", "x\n")
	if err := os.Remove(filepath.Join(e.root, "README.md")); err != nil {
		t.Fatal(err)
	}
	g := e.open()
	got, err := g.PathStates(bg, []string{"src/app.go", "fresh.txt", "untouched.txt", "ignored.log", "never-existed.txt", "README.md"})
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for p, s := range got {
		kinds[p] = s.Kind
	}
	want := map[string]string{"src/app.go": "modified", "fresh.txt": "new", "README.md": "deleted"}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("states = %v, want %v", kinds, want)
	}
	// Only what was asked about is reported, and a protected path is refused outright.
	only, _ := g.PathStates(bg, []string{"fresh.txt"})
	if len(only) != 1 {
		t.Fatalf("only = %v", only)
	}
	for _, bad := range []string{".env", "../x", "-x", ":(top)x", ""} {
		if _, err := g.PathStates(bg, []string{bad}); err == nil {
			t.Errorf("PathStates accepted %q", bad)
		}
	}
}

// What a commit records is identified without writing anything, and the commit that results
// has the same identity as the index it came from.
func TestTheStagedFingerprintNeedsNoWritesAndMatchesTheCommitItBecomes(t *testing.T) {
	e := newRepo(t)
	g := e.open()
	e.write("src/app.go", "package app\n\n// one\n")
	e.write("new.txt", "n\n")
	if err := g.Add(bg, []string{"src/app.go", "new.txt"}); err != nil {
		t.Fatal(err)
	}
	before := objectCount(t, e.root)
	first, err := g.StagedFingerprint(bg)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := g.StagedFingerprint(bg)
	if first != again || len(first) != 64 || objectCount(t, e.root) != before {
		t.Fatalf("not stable or wrote objects: %s %s (objects %d -> %d)", first, again, before, objectCount(t, e.root))
	}
	e.write("src/app.go", "package app\n\n// two\n")
	if err := g.Add(bg, []string{"src/app.go"}); err != nil {
		t.Fatal(err)
	}
	if changed, _ := g.StagedFingerprint(bg); changed == first {
		t.Fatal("the fingerprint ignores staged content")
	}
	staged, _ := g.StagedFingerprint(bg)
	done, err := g.Commit(bg, "record exactly that")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := g.CommitFingerprint(bg, done.Commit); err != nil || got != staged {
		t.Fatalf("the commit's fingerprint %s differs from the index's %s (%v)", got, staged, err)
	}
	if empty, _ := g.StagedFingerprint(bg); empty == staged {
		t.Fatal("an empty index has the same fingerprint")
	}
	if _, err := g.CommitFingerprint(bg, "HEAD"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a non-hash = %v", err)
	}
}

func TestAFirstCommitHasTheFingerprintOfItsIndexToo(t *testing.T) {
	gitPath := needGit(t)
	e := &repoEnv{t: t, root: realTemp(t), home: realTemp(t), temp: filepath.Join(realTemp(t), "tmp")}
	e.cfg = Config{SearchPath: []string{filepath.Dir(gitPath)}, Home: e.home, TempRoot: e.temp}
	e.setup = []string{"PATH=" + filepath.Dir(gitPath) + ":/usr/bin:/bin", "HOME=" + e.home, "GIT_CONFIG_NOSYSTEM=1", "LC_ALL=C"}
	e.git("init", "-q", "-b", "main")
	e.git("config", "user.name", "T")
	e.git("config", "user.email", "t@example.com")
	e.write("first.txt", "x\n")
	g := e.open()
	if err := g.Add(bg, []string{"first.txt"}); err != nil {
		t.Fatal(err)
	}
	staged, err := g.StagedFingerprint(bg)
	if err != nil {
		t.Fatal(err)
	}
	done, err := g.Commit(bg, "root")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := g.CommitFingerprint(bg, done.Commit); got != staged {
		t.Fatalf("a first commit's fingerprint %s differs from its index's %s", got, staged)
	}
}

func TestBranchAndSubjectAreReadFromTheRepository(t *testing.T) {
	e := newRepo(t)
	g := e.open()
	if b, err := g.Branch(bg); err != nil || b != "main" {
		t.Fatalf("branch = %q %v", b, err)
	}
	head, _ := g.Head(bg)
	if s, err := g.Subject(bg, head); err != nil || s != "initial commit" {
		t.Fatalf("subject = %q %v", s, err)
	}
	e.git("checkout", "-q", "--detach", "HEAD")
	if b, err := g.Branch(bg); err != nil || b != "(detached)" {
		t.Fatalf("detached = %q %v", b, err)
	}
	if _, err := g.Subject(bg, "not-a-hash"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("%v", err)
	}
}

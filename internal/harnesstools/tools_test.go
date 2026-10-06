package harnesstools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
)

const workspace = "agent-memory"

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

type env struct {
	t        *testing.T
	root     string
	outside  string
	provider *Provider
	registry *harness.Registry
	session  *harness.Session
	current  func() string
}

func redact(s string) string {
	return strings.NewReplacer("AKIAIOSFODNN7EXAMPLE", "[REDACTED_SECRET]", "hunter2hunter2", "[REDACTED_SECRET]").Replace(s)
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{t: t, root: t.TempDir(), outside: t.TempDir()}
	e.current = func() string { return e.root }
	write(t, e.root, "main.go", "package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n")
	write(t, e.root, "internal/app/app.go", "package app\n\n// TODO: fix the needle handling\nfunc Run() {}\nfunc needle() {}\n")
	write(t, e.root, "internal/app/app_test.go", "package app\n\nfunc TestNeedle() {}\n")
	write(t, e.root, "docs/readme.md", "# Docs\nThe NEEDLE is here\n")
	write(t, e.root, "vendor/dep/x.go", "package dep // needle\n")
	write(t, e.root, "node_modules/p/y.js", "needle\n")
	write(t, e.root, ".hidden/h.go", "needle\n")
	write(t, e.root, ".env", "DATABASE_PASSWORD=hunter2hunter2\n")
	write(t, e.root, "id_rsa", "needle PRIVATE KEY\n")
	write(t, e.root, "server.pem", "needle CERT\n")
	write(t, e.root, "binary.bin", "needle\x00\x01\x02")
	write(t, e.root, "big.txt", strings.Repeat("needle in a big file\n", 20000)) // ~420 KB
	write(t, e.root, "leaky.go", "package leaky\nconst key = \"AKIAIOSFODNN7EXAMPLE\" // needle\n")
	write(t, e.root, "long.txt", strings.Repeat("x", 1000)+" needle\n")
	write(t, e.root, "crlf.txt", "one\r\ntwo\r\nthree\r\n")
	write(t, e.root, "ctrl.txt", "plain \x1b[31mred\x1b[0m\x07 text\n")
	write(t, e.root, "empty.txt", "")
	write(t, e.outside, "stolen.txt", "needle OUTSIDE-SECRET\n")
	if err := os.Symlink(filepath.Join(e.outside, "stolen.txt"), filepath.Join(e.root, "link-out.txt")); err == nil {
		_ = os.Symlink(e.outside, filepath.Join(e.root, "dir-out"))
	}
	_ = syscall.Mkfifo(filepath.Join(e.root, "pipe"), 0o644)
	e.open(Config{Redact: redact})
	return e
}

func (e *env) open(cfg Config) {
	e.t.Helper()
	cfg.Root = func(ws string) (string, error) {
		if ws != workspace {
			return "", errors.New("unknown workspace")
		}
		return e.current(), nil
	}
	provider, err := NewProvider(cfg)
	if err != nil {
		e.t.Fatal(err)
	}
	e.provider = provider
	e.registry = harness.NewRegistry()
	if err := provider.Register(e.registry); err != nil {
		e.t.Fatal(err)
	}
	session, err := e.registry.OpenSession(context.Background(), ProviderID, harness.Scope{Workspace: workspace, Run: "run-t", Generation: 1})
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { _ = session.Close() })
	e.session = session
}

type result struct {
	prepared harness.Outcome
	outcome  harness.Outcome
	output   string
	action   harness.PreparedAction
}

func (e *env) call(tool string, args any, maxBytes int) result {
	e.t.Helper()
	var raw []byte
	switch typed := args.(type) {
	case string:
		raw = []byte(typed)
	case []byte:
		raw = typed
	default:
		raw, _ = json.Marshal(args)
	}
	envelope, err := e.session.Envelope(harness.CapabilityID(tool), maxBytes)
	if err != nil {
		e.t.Fatal(err)
	}
	action, err := e.session.Prepare(context.Background(), harness.ToolRequest{Envelope: envelope, ToolID: tool, Arguments: raw})
	if err != nil {
		e.t.Fatalf("prepare: %v", err)
	}
	res := result{prepared: action.Outcome, action: action}
	if action.Outcome != harness.OutcomeOK {
		return res
	}
	answer, err := e.session.Invoke(context.Background(), action)
	if err != nil {
		e.t.Fatalf("invoke: %v", err)
	}
	res.outcome, res.output = answer.Outcome, string(answer.Output)
	return res
}

func TestProbeReportsEveryToolAvailableOnlyWhenTheRootOpens(t *testing.T) {
	e := newEnv(t)
	for _, tool := range []string{ToolReadFile, ToolListDir, ToolSearch} {
		if e.session.State(harness.CapabilityID(tool)) != harness.AccessAvailable {
			t.Errorf("%s = %s", tool, e.session.State(harness.CapabilityID(tool)))
		}
	}
	if got := Manifest().Capabilities; len(got) != 3 || Manifest().Kind != harness.KindTool || Manifest().ID != ProviderID {
		t.Fatalf("manifest = %+v", Manifest())
	}
	for name, root := range map[string]string{"missing root": filepath.Join(e.root, "nope"), "a file": filepath.Join(e.root, "main.go")} {
		e.current = func() string { return root }
		session, err := e.registry.OpenSession(context.Background(), ProviderID, harness.Scope{Workspace: workspace, Run: "run-" + strings.ReplaceAll(name, " ", "-"), Generation: 1})
		if err != nil {
			t.Fatal(err)
		}
		if session.State(ToolReadFile) != harness.AccessUnavailable {
			t.Errorf("%s: state = %s", name, session.State(ToolReadFile))
		}
		_ = session.Close()
	}
	if _, err := NewProvider(Config{}); err == nil {
		t.Fatal("a provider without a root resolver was accepted")
	}
}

func TestPrepareValidatesStrictlyAndDoesNoWork(t *testing.T) {
	e := newEnv(t)
	for name, tc := range map[string]struct {
		tool string
		args any
		want harness.Outcome
	}{
		"read ok":              {ToolReadFile, map[string]any{"path": "main.go"}, harness.OutcomeOK},
		"read a missing file":  {ToolReadFile, map[string]any{"path": "nope/missing.go"}, harness.OutcomeOK}, // prepare reads nothing
		"read no path":         {ToolReadFile, map[string]any{}, harness.OutcomeFailed},
		"read the root":        {ToolReadFile, map[string]any{"path": "."}, harness.OutcomeFailed},
		"read traversal":       {ToolReadFile, map[string]any{"path": "../x"}, harness.OutcomeDenied},
		"read deep traversal":  {ToolReadFile, map[string]any{"path": "a/../../x"}, harness.OutcomeDenied},
		"read absolute":        {ToolReadFile, map[string]any{"path": "/etc/passwd"}, harness.OutcomeDenied},
		"read hidden":          {ToolReadFile, map[string]any{"path": ".env"}, harness.OutcomeDenied},
		"read in a hidden dir": {ToolReadFile, map[string]any{"path": ".git/config"}, harness.OutcomeDenied},
		"read a key":           {ToolReadFile, map[string]any{"path": "id_rsa"}, harness.OutcomeDenied},
		"read a pem":           {ToolReadFile, map[string]any{"path": "x/server.pem"}, harness.OutcomeDenied},
		"read NUL path":        {ToolReadFile, map[string]any{"path": "a\x00b"}, harness.OutcomeDenied},
		"read huge path":       {ToolReadFile, map[string]any{"path": strings.Repeat("a", 600)}, harness.OutcomeDenied},
		"read bad start":       {ToolReadFile, map[string]any{"path": "main.go", "start_line": -1}, harness.OutcomeFailed},
		"read too many lines":  {ToolReadFile, map[string]any{"path": "main.go", "max_lines": MaxReadLines + 1}, harness.OutcomeFailed},
		"read negative lines":  {ToolReadFile, map[string]any{"path": "main.go", "max_lines": -5}, harness.OutcomeFailed},
		"read unknown field":   {ToolReadFile, map[string]any{"path": "main.go", "admin": true}, harness.OutcomeFailed},
		"read malformed":       {ToolReadFile, `{"path":`, harness.OutcomeFailed},
		"read trailing data":   {ToolReadFile, `{"path":"main.go"}{"x":1}`, harness.OutcomeFailed},
		"read wrong type":      {ToolReadFile, `{"path":5}`, harness.OutcomeFailed},
		"list default root":    {ToolListDir, map[string]any{}, harness.OutcomeOK},
		"list no arguments":    {ToolListDir, ``, harness.OutcomeOK},
		"list traversal":       {ToolListDir, map[string]any{"path": ".."}, harness.OutcomeDenied},
		"list hidden":          {ToolListDir, map[string]any{"path": ".git"}, harness.OutcomeDenied},
		"list unknown field":   {ToolListDir, map[string]any{"path": ".", "recursive": true}, harness.OutcomeFailed},
		"search ok":            {ToolSearch, map[string]any{"query": "needle"}, harness.OutcomeOK},
		"search empty query":   {ToolSearch, map[string]any{"query": ""}, harness.OutcomeFailed},
		"search no query":      {ToolSearch, map[string]any{}, harness.OutcomeFailed},
		"search long query":    {ToolSearch, map[string]any{"query": strings.Repeat("q", MaxQueryBytes+1)}, harness.OutcomeFailed},
		"search bad regex":     {ToolSearch, map[string]any{"query": "(", "regex": true}, harness.OutcomeFailed},
		"search long glob":     {ToolSearch, map[string]any{"query": "x", "glob": strings.Repeat("g", MaxGlobBytes+1)}, harness.OutcomeFailed},
		"search too many":      {ToolSearch, map[string]any{"query": "x", "max_results": MaxSearchResults + 1}, harness.OutcomeFailed},
		"search negative max":  {ToolSearch, map[string]any{"query": "x", "max_results": -1}, harness.OutcomeFailed},
		"search big context":   {ToolSearch, map[string]any{"query": "x", "context": MaxContextLines + 1}, harness.OutcomeFailed},
		"search traversal":     {ToolSearch, map[string]any{"query": "x", "path": "../.."}, harness.OutcomeDenied},
		"search hidden path":   {ToolSearch, map[string]any{"query": "x", "path": ".hidden"}, harness.OutcomeDenied},
	} {
		if got := e.call(tc.tool, tc.args, 4096); got.prepared != tc.want {
			t.Errorf("%s: prepared as %s, want %s", name, got.prepared, tc.want)
		}
	}
}

func TestADigestBindsTheToolArgumentsAndRootAndSummariesHoldNoContent(t *testing.T) {
	e := newEnv(t)
	a := e.call(ToolReadFile, map[string]any{"path": "main.go"}, 4096)
	same := e.call(ToolReadFile, map[string]any{"max_lines": DefaultReadLines, "start_line": 1, "path": "./main.go"}, 4096)
	if a.action.Digest != same.action.Digest || !strings.HasPrefix(a.action.Digest, "sha256:") {
		t.Fatalf("equivalent calls must share a digest: %q vs %q", a.action.Digest, same.action.Digest)
	}
	other := e.call(ToolReadFile, map[string]any{"path": "main.go", "start_line": 2}, 4096)
	list := e.call(ToolListDir, map[string]any{"path": "internal"}, 4096)
	if other.action.Digest == a.action.Digest || list.action.Digest == a.action.Digest {
		t.Fatal("different calls shared a digest")
	}
	elsewhere := newEnv(t)
	if b := elsewhere.call(ToolReadFile, map[string]any{"path": "main.go"}, 4096); b.action.Digest == a.action.Digest {
		t.Fatal("the same call in another project root shared a digest")
	}
	if strings.Contains(a.action.Summary, "package main") || a.action.Summary != "read main.go" {
		t.Fatalf("summary = %q", a.action.Summary)
	}
}

func TestReadFileReturnsNumberedBoundedRedactedSanitizedLines(t *testing.T) {
	e := newEnv(t)
	got := e.call(ToolReadFile, map[string]any{"path": "main.go"}, 4096)
	if got.outcome != harness.OutcomeOK || !strings.HasPrefix(got.output, "main.go (revision ") || !strings.Contains(got.output, "lines 1-5 of 5)") ||
		!strings.Contains(got.output, "     3\tfunc main() {") {
		t.Fatalf("read = %s %q", got.outcome, got.output)
	}
	window := e.call(ToolReadFile, map[string]any{"path": "main.go", "start_line": 3, "max_lines": 2}, 4096)
	if !strings.Contains(window.output, "lines 3-4 of 5)") || strings.Contains(window.output, "package main") || window.outcome != harness.OutcomePartial ||
		!strings.Contains(window.output, "[more content not shown]") {
		t.Fatalf("window = %s %q", window.outcome, window.output)
	}
	if past := e.call(ToolReadFile, map[string]any{"path": "main.go", "start_line": 99}, 4096); past.outcome != harness.OutcomeOK || strings.Count(past.output, "\n") > 2 {
		t.Fatalf("past the end = %s %q", past.outcome, past.output)
	}
	if empty := e.call(ToolReadFile, map[string]any{"path": "empty.txt"}, 4096); empty.outcome != harness.OutcomeOK || !strings.Contains(empty.output, "of 0)") {
		t.Fatalf("empty = %s %q", empty.outcome, empty.output)
	}
	if crlf := e.call(ToolReadFile, map[string]any{"path": "crlf.txt"}, 4096); strings.Contains(crlf.output, "\r") || !strings.Contains(crlf.output, "     2\ttwo") {
		t.Fatalf("crlf = %q", crlf.output)
	}
	if ctrl := e.call(ToolReadFile, map[string]any{"path": "ctrl.txt"}, 4096); strings.ContainsAny(ctrl.output, "\x1b\x07") || !strings.Contains(ctrl.output, "plain [31mred[0m text") {
		t.Fatalf("control characters survived: %q", ctrl.output)
	}
	if long := e.call(ToolReadFile, map[string]any{"path": "long.txt"}, 4096); !strings.Contains(long.output, "…") || len(long.output) > 500 {
		t.Fatalf("a long line was not capped: %d bytes", len(long.output))
	}
	if leaky := e.call(ToolReadFile, map[string]any{"path": "leaky.go"}, 4096); strings.Contains(leaky.output, "AKIAIOSFODNN7EXAMPLE") || !strings.Contains(leaky.output, "[REDACTED_SECRET]") {
		t.Fatalf("a secret in a file reached the output: %q", leaky.output)
	}
	revA := e.call(ToolReadFile, map[string]any{"path": "main.go"}, 4096).output
	write(t, e.root, "main.go", "package main\n\n// changed\n")
	if revB := e.call(ToolReadFile, map[string]any{"path": "main.go"}, 4096).output; strings.Split(revA, ")")[0] == strings.Split(revB, ")")[0] {
		t.Fatal("the revision did not change with the file")
	}
}

func TestReadFileRefusesEverythingOutsideTheLegitimateSet(t *testing.T) {
	e := newEnv(t)
	for name, path := range map[string]string{"binary": "binary.bin", "missing": "nope.txt", "a directory": "internal", "a fifo": "pipe", "symlink out": "link-out.txt", "through a dir link": "dir-out/stolen.txt"} {
		done := make(chan result, 1)
		go func() { done <- e.call(ToolReadFile, map[string]any{"path": path}, 4096) }()
		select {
		case got := <-done:
			if got.outcome == harness.OutcomeOK || got.outcome == harness.OutcomePartial || strings.Contains(got.output, "OUTSIDE-SECRET") {
				t.Errorf("%s: %s %q", name, got.outcome, got.output)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: the read blocked", name)
		}
	}
	// A file bigger than the read cap is shown in part, never whole.
	big := e.call(ToolReadFile, map[string]any{"path": "big.txt", "max_lines": 5}, 4096)
	if big.outcome != harness.OutcomePartial || !strings.Contains(big.output, "of ") || !strings.Contains(big.output, "+)") {
		t.Fatalf("big = %s %q", big.outcome, big.output)
	}
	// The reply envelope is a hard ceiling.
	small := e.call(ToolReadFile, map[string]any{"path": "internal/app/app.go"}, 120)
	if small.outcome != harness.OutcomePartial || len(small.output) > 120 {
		t.Fatalf("a %d-byte reply under a 120-byte bound: %s", len(small.output), small.outcome)
	}
}

func decodeList(t *testing.T, output string) (entries []listEntry, truncated bool) {
	t.Helper()
	var parsed struct {
		Path      string      `json:"path"`
		Entries   []listEntry `json:"entries"`
		Truncated bool        `json:"truncated"`
	}
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		t.Fatalf("not JSON: %v: %q", err, output)
	}
	return parsed.Entries, parsed.Truncated
}

func TestListDirIsSortedFilteredStructuredAndBounded(t *testing.T) {
	e := newEnv(t)
	got := e.call(ToolListDir, map[string]any{}, 8192)
	entries, truncated := decodeList(t, got.output)
	if got.outcome != harness.OutcomeOK || truncated {
		t.Fatalf("list = %s truncated=%v", got.outcome, truncated)
	}
	names := make([]string, len(entries))
	for i, en := range entries {
		names[i] = en.Name
	}
	if !sort.StringsAreSorted(names) {
		t.Fatalf("not sorted: %v", names)
	}
	for _, hidden := range []string{".env", ".hidden", "id_rsa", "server.pem"} {
		for _, name := range names {
			if name == hidden {
				t.Errorf("%s was listed", hidden)
			}
		}
	}
	kinds := map[string]string{}
	for _, en := range entries {
		kinds[en.Name] = en.Kind
	}
	if kinds["main.go"] != "file" || kinds["internal"] != "dir" || kinds["link-out.txt"] != "other" || kinds["pipe"] != "other" || kinds["dir-out"] != "other" {
		t.Fatalf("kinds = %v", kinds)
	}
	sub, _ := decodeList(t, e.call(ToolListDir, map[string]any{"path": "internal/app"}, 4096).output)
	if len(sub) != 2 || sub[0].Name != "app.go" {
		t.Fatalf("sub = %+v", sub)
	}
	for i := 0; i < 520; i++ {
		write(t, e.root, fmt.Sprintf("many/f%04d.txt", i), "x")
	}
	many := e.call(ToolListDir, map[string]any{"path": "many"}, 1<<20)
	list, cut := decodeList(t, many.output)
	if many.outcome != harness.OutcomePartial || !cut || len(list) != 500 {
		t.Fatalf("many = %s cut=%v n=%d", many.outcome, cut, len(list))
	}
	tight := e.call(ToolListDir, map[string]any{"path": "many"}, 600)
	trimmed, _ := decodeList(t, tight.output)
	if tight.outcome != harness.OutcomePartial || len(tight.output) > 600 || len(trimmed) == 0 || len(trimmed) >= 500 {
		t.Fatalf("envelope bound: %s %d bytes %d entries", tight.outcome, len(tight.output), len(trimmed))
	}
	if file := e.call(ToolListDir, map[string]any{"path": "main.go"}, 4096); file.outcome == harness.OutcomeOK {
		t.Fatal("a file was listed as a directory")
	}
}

type searchResult struct {
	Query        string  `json:"query"`
	Matches      []match `json:"matches"`
	FilesScanned int     `json:"files_scanned"`
	FilesSkipped int     `json:"files_skipped"`
	Truncated    bool    `json:"truncated"`
	Stopped      string  `json:"stopped"`
}

func runSearch(t *testing.T, e *env, args map[string]any, maxBytes int) (searchResult, harness.Outcome, string) {
	t.Helper()
	got := e.call(ToolSearch, args, maxBytes)
	if got.prepared != harness.OutcomeOK {
		t.Fatalf("prepare = %s", got.prepared)
	}
	var parsed searchResult
	if got.output != "" {
		if err := json.Unmarshal([]byte(got.output), &parsed); err != nil {
			t.Fatalf("not JSON: %v: %q", err, got.output)
		}
	}
	return parsed, got.outcome, got.output
}

func paths(matches []match) []string {
	var out []string
	for _, m := range matches {
		out = append(out, fmt.Sprintf("%s:%d", m.Path, m.Line))
	}
	return out
}

func TestSearchFindsMatchesDeterministicallyAndSkipsProtectedPlaces(t *testing.T) {
	e := newEnv(t)
	res, outcome, raw := runSearch(t, e, map[string]any{"query": "needle", "max_results": 100}, 1<<20)
	if outcome != harness.OutcomeOK && outcome != harness.OutcomePartial {
		t.Fatalf("outcome = %s", outcome)
	}
	for _, leaked := range []string{"OUTSIDE-SECRET", "PRIVATE KEY", "CERT", "AKIAIOSFODNN7EXAMPLE", ".hidden", "node_modules", "vendor/", "id_rsa", "server.pem", "link-out", "binary.bin"} {
		if strings.Contains(raw, leaked) {
			t.Errorf("search output contains %q", leaked)
		}
	}
	got := paths(res.Matches)
	want := []string{"internal/app/app.go:3", "internal/app/app.go:5", "leaky.go:2", "long.txt:1"}
	for _, w := range want {
		if !contains(got, w) {
			t.Errorf("missing %s in %v", w, got)
		}
	}
	if !sort.SliceIsSorted(res.Matches, func(i, j int) bool {
		if res.Matches[i].Path != res.Matches[j].Path {
			return res.Matches[i].Path < res.Matches[j].Path
		}
		return res.Matches[i].Line < res.Matches[j].Line
	}) {
		t.Fatalf("matches are not ordered: %v", got)
	}
	for i := 0; i < 10; i++ {
		again, _, _ := runSearch(t, e, map[string]any{"query": "needle", "max_results": 100}, 1<<20)
		if !reflect.DeepEqual(paths(again.Matches), got) {
			t.Fatal("search is not deterministic")
		}
	}
	if res.FilesScanned == 0 || res.FilesSkipped == 0 {
		t.Fatalf("scanned=%d skipped=%d; the big and binary files should be counted as skipped", res.FilesScanned, res.FilesSkipped)
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func TestSearchOptionsCaseRegexContextGlobAndScope(t *testing.T) {
	e := newEnv(t)
	sensitive, _, _ := runSearch(t, e, map[string]any{"query": "NEEDLE", "path": "docs"}, 1<<20)
	insensitive, _, _ := runSearch(t, e, map[string]any{"query": "needle", "ignore_case": true, "path": "docs"}, 1<<20)
	if len(sensitive.Matches) != 1 || sensitive.Matches[0].Path != "docs/readme.md" || len(insensitive.Matches) != 1 {
		t.Fatalf("case: %v %v", paths(sensitive.Matches), paths(insensitive.Matches))
	}
	if miss, _, _ := runSearch(t, e, map[string]any{"query": "NEEDLE", "path": "internal"}, 1<<20); len(miss.Matches) != 0 {
		t.Fatalf("case-sensitive search matched %v", paths(miss.Matches))
	}
	re, _, _ := runSearch(t, e, map[string]any{"query": `func (Run|needle)\(`, "regex": true, "path": "internal"}, 1<<20)
	if got := paths(re.Matches); !reflect.DeepEqual(got, []string{"internal/app/app.go:4", "internal/app/app.go:5"}) {
		t.Fatalf("regex = %v", got)
	}
	ctx, _, _ := runSearch(t, e, map[string]any{"query": "func Run", "path": "internal/app/app.go", "context": 2}, 1<<20)
	if len(ctx.Matches) != 1 || len(ctx.Matches[0].Before) != 2 || len(ctx.Matches[0].After) != 1 || ctx.Matches[0].Before[1] != "// TODO: fix the needle handling" {
		t.Fatalf("context = %+v", ctx.Matches)
	}
	glob, _, _ := runSearch(t, e, map[string]any{"query": "Needle", "ignore_case": true, "glob": "*_test.go"}, 1<<20)
	if got := paths(glob.Matches); !reflect.DeepEqual(got, []string{"internal/app/app_test.go:3"}) {
		t.Fatalf("glob = %v", got)
	}
	single, _, _ := runSearch(t, e, map[string]any{"query": "needle", "path": "internal/app/app.go"}, 1<<20)
	if len(single.Matches) != 2 || single.FilesScanned != 1 {
		t.Fatalf("single file = %+v", single)
	}
	for _, bad := range []string{"main.go/..", "missing"} {
		if got := e.call(ToolSearch, map[string]any{"query": "x", "path": bad}, 4096); got.prepared == harness.OutcomeOK && got.outcome == harness.OutcomeOK && bad == "missing" {
			t.Errorf("a missing path searched OK")
		}
	}
	// RE2 has no catastrophic backtracking: a classic evil pattern returns promptly.
	started := time.Now()
	runSearch(t, e, map[string]any{"query": `(a+)+$`, "regex": true, "path": "long.txt"}, 1<<20)
	if time.Since(started) > 2*time.Second {
		t.Fatalf("an evil regex took %v", time.Since(started))
	}
}

func TestSearchBoundsResultsFilesTimeAndOutputAndRedactsMatches(t *testing.T) {
	e := newEnv(t)
	capped, outcome, _ := runSearch(t, e, map[string]any{"query": "needle", "max_results": 2}, 1<<20)
	if len(capped.Matches) != 2 || !capped.Truncated || outcome != harness.OutcomePartial {
		t.Fatalf("capped = %d truncated=%v %s", len(capped.Matches), capped.Truncated, outcome)
	}
	leaky, _, raw := runSearch(t, e, map[string]any{"query": "AKIA", "path": "leaky.go"}, 1<<20)
	if len(leaky.Matches) != 1 || strings.Contains(raw, "AKIAIOSFODNN7EXAMPLE") {
		t.Fatalf("a secret was returned by search: %s", raw)
	}
	_, outcome, raw = runSearch(t, e, map[string]any{"query": "needle", "max_results": 100}, 400)
	if outcome != harness.OutcomePartial || len(raw) > 400 {
		t.Fatalf("a %d-byte result under a 400-byte bound: %s", len(raw), outcome)
	}
	// A time budget that has already run out stops the walk and says so.
	e.open(Config{Redact: redact, SearchBudget: time.Nanosecond})
	stopped, outcome, _ := runSearch(t, e, map[string]any{"query": "needle"}, 1<<20)
	if stopped.Stopped != "time budget" || outcome != harness.OutcomePartial {
		t.Fatalf("a spent budget did not stop the search: %+v %s", stopped, outcome)
	}
	// More files than the walk cap are reported as truncated.
	e.open(Config{Redact: redact})
	for i := 0; i < MaxSearchFiles+20; i++ {
		write(t, e.root, fmt.Sprintf("flood/f%05d.txt", i), "x")
	}
	flood, outcome, _ := runSearch(t, e, map[string]any{"query": "zzz-no-match", "path": "flood"}, 1<<20)
	if !flood.Truncated || flood.FilesScanned != MaxSearchFiles || outcome != harness.OutcomePartial {
		t.Fatalf("flood: scanned=%d truncated=%v %s", flood.FilesScanned, flood.Truncated, outcome)
	}
}

func TestAPreparedCallIsSingleUseAndGoesStaleIfTheRootMoves(t *testing.T) {
	e := newEnv(t)
	envelope, _ := e.session.Envelope(ToolReadFile, 4096)
	action, err := e.session.Prepare(context.Background(), harness.ToolRequest{Envelope: envelope, ToolID: ToolReadFile, Arguments: []byte(`{"path":"main.go"}`)})
	if err != nil || action.Outcome != harness.OutcomeOK {
		t.Fatal(err)
	}
	if _, err := e.session.Invoke(context.Background(), action); err != nil {
		t.Fatal(err)
	}
	if _, err := e.session.Invoke(context.Background(), action); !errors.Is(err, harness.ErrStale) {
		t.Fatalf("replay = %v", err)
	}
	other := t.TempDir()
	write(t, other, "main.go", "package other\n")
	moved, err := e.session.Prepare(context.Background(), harness.ToolRequest{Envelope: envelope, ToolID: ToolReadFile, Arguments: []byte(`{"path":"main.go"}`)})
	if err != nil || moved.Outcome != harness.OutcomeOK {
		t.Fatal(err)
	}
	e.current = func() string { return other } // the registry now points the workspace elsewhere
	answer, err := e.session.Invoke(context.Background(), moved)
	if err != nil || answer.Outcome != harness.OutcomeStale || len(answer.Output) != 0 {
		t.Fatalf("a re-pointed root = %+v, %v", answer, err)
	}
	e.current = func() string { return e.root }
	// Calling the provider directly with a digest it never issued fails closed.
	s := &session{provider: e.provider, pending: map[string]call{}}
	forged, _ := s.Invoke(context.Background(), harness.PreparedAction{Envelope: envelope, Outcome: harness.OutcomeOK, Digest: "sha256:" + strings.Repeat("0", 64)})
	if forged.Outcome != harness.OutcomeFailed || len(forged.Output) != 0 {
		t.Fatalf("forged = %+v", forged)
	}
}

func TestUnknownToolsAndCapabilityMismatchAreRefusedByThePrepareStep(t *testing.T) {
	e := newEnv(t)
	s := &session{provider: e.provider, pending: map[string]call{}, scope: harness.Scope{Workspace: workspace, Generation: 1}}
	envelope, _ := e.session.Envelope(ToolReadFile, 4096)
	unknown := envelope
	unknown.Capability = "write_file"
	if got, _ := s.Prepare(context.Background(), harness.ToolRequest{Envelope: unknown, ToolID: "write_file"}); got.Outcome != harness.OutcomeUnsupported {
		t.Fatalf("an unknown tool = %s", got.Outcome)
	}
	if got, _ := s.Prepare(context.Background(), harness.ToolRequest{Envelope: envelope, ToolID: ToolListDir, Arguments: []byte(`{}`)}); got.Outcome != harness.OutcomeFailed {
		t.Fatalf("capability and tool disagreeing = %s", got.Outcome)
	}
	for i := 0; i < maxPending; i++ {
		got, _ := s.Prepare(context.Background(), harness.ToolRequest{Envelope: envelope, ToolID: ToolReadFile, Arguments: []byte(fmt.Sprintf(`{"path":"main.go","start_line":%d}`, i+1))})
		if got.Outcome != harness.OutcomeOK {
			t.Fatalf("prepare %d = %s", i, got.Outcome)
		}
	}
	if got, _ := s.Prepare(context.Background(), harness.ToolRequest{Envelope: envelope, ToolID: ToolReadFile, Arguments: []byte(`{"path":"main.go","start_line":999}`)}); got.Outcome != harness.OutcomeFailed {
		t.Fatalf("an unbounded number of pending calls = %s", got.Outcome)
	}
}

func TestThePolicyAllowsOnlyTheReadToolsAndDeniesByDefault(t *testing.T) {
	ok := func(capability harness.CapabilityID) harness.PreparedAction {
		return harness.PreparedAction{Envelope: harness.Envelope{Capability: capability}, Outcome: harness.OutcomeOK, Digest: "sha256:abc12345"}
	}
	policy := ReadOnlyPolicy()
	for _, tool := range []string{ToolReadFile, ToolListDir, ToolSearch} {
		if got := policy.Decide(context.Background(), harnessrunOwner(), ok(harness.CapabilityID(tool))); got != decisionAllow {
			t.Errorf("%s = %v", tool, got)
		}
	}
	for _, tool := range []string{"write_file", "run_command", "git_push", "", "READ_FILE"} {
		if got := policy.Decide(context.Background(), harnessrunOwner(), ok(harness.CapabilityID(tool))); got != decisionDeny {
			t.Errorf("%q = %v", tool, got)
		}
	}
	if (Policy{}).Decide(context.Background(), harnessrunOwner(), ok(ToolReadFile)) != decisionDeny {
		t.Fatal("the zero policy must deny")
	}
	failed := ok(ToolReadFile)
	failed.Outcome = harness.OutcomeDenied
	noDigest := ok(ToolReadFile)
	noDigest.Digest = ""
	if policy.Decide(context.Background(), harnessrunOwner(), failed) != decisionDeny || policy.Decide(context.Background(), harnessrunOwner(), noDigest) != decisionDeny {
		t.Fatal("an unprepared action was allowed")
	}
	custom := NewPolicy(map[harness.CapabilityID]Tier{"edit_file": TierAsk, ToolReadFile: TierAllow, "x": TierDeny})
	if custom.Decide(context.Background(), harnessrunOwner(), ok("edit_file")) != decisionAsk || custom.Decide(context.Background(), harnessrunOwner(), ok("x")) != decisionDeny {
		t.Fatal("tiers were not honored")
	}
	table := map[harness.CapabilityID]Tier{ToolReadFile: TierAllow}
	copied := NewPolicy(table)
	table[ToolReadFile] = TierDeny
	if copied.Decide(context.Background(), harnessrunOwner(), ok(ToolReadFile)) != decisionAllow {
		t.Fatal("the policy aliased the caller's table")
	}
}

func TestSchemasMatchTheArgumentStructsAndTheEnforcedBounds(t *testing.T) {
	structs := map[string]any{ToolReadFile: readArgs{}, ToolListDir: listArgs{}, ToolSearch: searchArgs{}}
	required := map[string][]string{ToolReadFile: {"path"}, ToolListDir: {}, ToolSearch: {"query"}}
	schemas := Schemas()
	if len(schemas) != 3 {
		t.Fatalf("schemas = %d", len(schemas))
	}
	for _, schema := range schemas {
		typ := reflect.TypeOf(structs[schema.Name])
		var fields []string
		for i := 0; i < typ.NumField(); i++ {
			fields = append(fields, strings.Split(typ.Field(i).Tag.Get("json"), ",")[0])
		}
		props := schema.Parameters["properties"].(map[string]any)
		var names []string
		for name := range props {
			names = append(names, name)
		}
		sort.Strings(fields)
		sort.Strings(names)
		if !reflect.DeepEqual(fields, names) {
			t.Errorf("%s: schema properties %v do not match the struct fields %v", schema.Name, names, fields)
		}
		if schema.Parameters["additionalProperties"] != false || !reflect.DeepEqual(schema.Parameters["required"], required[schema.Name]) || schema.Description == "" {
			t.Errorf("%s: %+v", schema.Name, schema.Parameters)
		}
	}
	read := schemas[1].Parameters["properties"].(map[string]any)["max_lines"].(map[string]any)
	search := schemas[2].Parameters["properties"].(map[string]any)
	if read["maximum"] != MaxReadLines || search["max_results"].(map[string]any)["maximum"] != MaxSearchResults ||
		search["context"].(map[string]any)["maximum"] != MaxContextLines || search["query"].(map[string]any)["maxLength"] != MaxQueryBytes {
		t.Fatal("a schema bound differs from the enforced limit")
	}
	encoded, err := json.Marshal(schemas)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"required":null`) {
		t.Fatalf("a schema encodes required as null: %s", encoded)
	}
}

func TestTheProviderItselfRefusesToReplayAPreparedCall(t *testing.T) {
	e := newEnv(t)
	s := &session{provider: e.provider, pending: map[string]call{}, scope: harness.Scope{Workspace: workspace, Generation: 1}}
	envelope, _ := e.session.Envelope(ToolReadFile, 4096)
	action, _ := s.Prepare(context.Background(), harness.ToolRequest{Envelope: envelope, ToolID: ToolReadFile, Arguments: []byte(`{"path":"main.go"}`)})
	if action.Outcome != harness.OutcomeOK {
		t.Fatalf("prepare = %s", action.Outcome)
	}
	first, _ := s.Invoke(context.Background(), action)
	second, _ := s.Invoke(context.Background(), action)
	if first.Outcome != harness.OutcomeOK || len(first.Output) == 0 || second.Outcome != harness.OutcomeFailed || len(second.Output) != 0 {
		t.Fatalf("first=%s second=%s %q", first.Outcome, second.Outcome, second.Output)
	}
}

func TestSearchResultsAreOrderedByFullPathNotByWalkOrder(t *testing.T) {
	e := newEnv(t)
	// "m-x.go" sorts before "m/b.go" as a path string, but the walk enters the directory
	// "m" before it reaches the sibling file "m-x.go".
	write(t, e.root, "m/b.go", "zebra\n")
	write(t, e.root, "m-x.go", "zebra\n")
	write(t, e.root, "m.go", "zebra\n")
	res, _, _ := runSearch(t, e, map[string]any{"query": "zebra"}, 1<<20)
	want := []string{"m-x.go:1", "m.go:1", "m/b.go:1"}
	if got := paths(res.Matches); !reflect.DeepEqual(got, want) {
		t.Fatalf("matches = %v, want %v", got, want)
	}
}

// A link inside the project can name a protected file, so the tools must never follow
// one: not to read it, not to list it, and not to search through it.
func TestToolsNeverFollowALinkToAProtectedTarget(t *testing.T) {
	e := newEnv(t)
	if err := os.Symlink(".env", filepath.Join(e.root, "notes.txt")); err != nil {
		t.Skip("symlinks unavailable")
	}
	if err := os.Mkdir(filepath.Join(e.root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, e.root, ".git/config", "[core]\n\tneedle = 1\n")
	if err := os.Symlink(".git", filepath.Join(e.root, "src")); err != nil {
		t.Fatal(err)
	}
	for name, call := range map[string]result{
		"read a file link":      e.call(ToolReadFile, map[string]any{"path": "notes.txt"}, 4096),
		"read through a dir":    e.call(ToolReadFile, map[string]any{"path": "src/config"}, 4096),
		"list a directory link": e.call(ToolListDir, map[string]any{"path": "src"}, 4096),
		"search a file link":    e.call(ToolSearch, map[string]any{"query": "hunter2", "path": "notes.txt"}, 4096),
	} {
		if strings.Contains(call.output, "hunter2") || strings.Contains(call.output, "core") || (call.prepared == harness.OutcomeOK && call.outcome == harness.OutcomeOK) {
			t.Errorf("%s: reached a protected target: prepared=%s outcome=%s output=%q", name, call.prepared, call.outcome, call.output)
		}
	}
	// A project-wide search must not surface content reached only through a link.
	// (The result echoes the query, so the check is on the matches, not on the text.)
	res := e.call(ToolSearch, map[string]any{"query": "hunter2"}, 1<<16)
	var found struct {
		Matches []map[string]any `json:"matches"`
	}
	if err := json.Unmarshal([]byte(res.output), &found); err != nil || len(found.Matches) != 0 || strings.Contains(res.output, "notes.txt") {
		t.Errorf("search followed a link: %q (%v)", res.output, err)
	}
}

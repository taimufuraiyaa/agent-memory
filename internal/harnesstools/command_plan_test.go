package harnesstools

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessfs"
)

// cmdEnv is a project, a directory of fake toolchains standing in for the search path, and a
// second directory inside the project that a hostile model might hope to be searched.
type cmdEnv struct {
	t        *testing.T
	root     string // real path
	toolDir  string
	cfg      CommandConfig
	project  *harnessfs.Root
	inboxDir string
}

func realTemp(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func newCmdEnv(t *testing.T) *cmdEnv {
	t.Helper()
	e := &cmdEnv{t: t, root: realTemp(t), toolDir: realTemp(t)}
	for _, name := range []string{"go", "gofmt", "cargo", "pytest", "python3", "python", "node", "npm", "make", "ls", "mytool", "bash", "curl", "sh", "git", "perl"} {
		if err := os.WriteFile(filepath.Join(e.toolDir, name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(e.toolDir, "bash"), filepath.Join(e.toolDir, "friendly")); err != nil {
		t.Fatal(err)
	}
	write(t, e.root, "scripts/test.sh", "#!/bin/sh\necho testing\n")
	if err := os.Chmod(filepath.Join(e.root, "scripts/test.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, e.root, "scripts/plain.txt", "not executable\n")
	write(t, e.root, "scripts/noshebang.sh", "echo hi\n")
	if err := os.Chmod(filepath.Join(e.root, "scripts/noshebang.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, e.root, "bin/go", "#!/bin/sh\necho shadow\n")
	if err := os.Chmod(filepath.Join(e.root, "bin/go"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, e.root, "internal/app/app.go", "package app\n")
	write(t, e.root, ".hidden/x", "x")
	e.inboxDir = filepath.Join(e.root, "bin")
	e.cfg = CommandConfig{Enabled: true, SearchPath: []string{e.toolDir}, Home: "/home/tester", MaxOutput: 8 << 10}
	project, err := harnessfs.Open(e.root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = project.Close() })
	e.project = project
	return e
}

func (e *cmdEnv) plan(argv []string, extra ...string) (*commandPlan, harness.Outcome, string) {
	e.t.Helper()
	raw := `{"argv":` + jsonArray(argv)
	for _, kv := range extra {
		raw += "," + kv
	}
	return planCommand(e.project, e.root, e.cfg, []byte(raw+"}"))
}

func jsonArray(items []string) string {
	var b strings.Builder
	b.WriteString("[")
	for i, item := range items {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(quoteJSON(item))
	}
	b.WriteString("]")
	return b.String()
}

func quoteJSON(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x20:
			b.WriteString(`\u00`)
			b.WriteString(string("0123456789abcdef"[r>>4]))
			b.WriteString(string("0123456789abcdef"[r&15]))
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func TestTheCatalogAcceptsOnlyKnownInvocations(t *testing.T) {
	ids := func(program string, args ...string) string {
		if e := matchCatalog(program, args); e != nil {
			return e.id
		}
		return ""
	}
	for name, tc := range map[string]struct {
		program string
		args    []string
		want    string
	}{
		"go test all":         {"go", []string{"test", "./..."}, "go test"},
		"go test with flags":  {"go", []string{"test", "-race", "-count=1", "-run", "TestX|TestY", "-v", "./internal/app"}, "go test"},
		"go test flags after": {"go", []string{"test", "./internal/...", "-short", "-timeout=90s", "-tags", "integration,fast"}, "go test"},
		"go test root":        {"go", []string{"test", "."}, "go test"},
		"go test shuffle":     {"go", []string{"test", "-shuffle=on", "./..."}, "go test"},
		"go test no package":  {"go", []string{"test"}, "go test"},
		"go vet":              {"go", []string{"vet", "./..."}, "go vet"},
		"go build":            {"go", []string{"build", "-race", "./cmd/x"}, "go build"},
		"gofmt list":          {"gofmt", []string{"-l", "."}, "gofmt"},
		"gofmt diff":          {"gofmt", []string{"-d", "-s", "internal/app/app.go"}, "gofmt"},
		"cargo test":          {"cargo", []string{"test", "--workspace", "--no-fail-fast", "-q"}, "cargo test"},
		"cargo test name":     {"cargo", []string{"test", "parser::tests"}, "cargo test"},
		"cargo check pkg":     {"cargo", []string{"check", "-p", "mycrate", "--features=a,b"}, "cargo check"},
		"cargo clippy":        {"cargo", []string{"clippy", "--all-targets"}, "cargo clippy"},
		"pytest":              {"pytest", []string{"-x", "-q", "-k", "not slow", "tests/test_a.py::test_one"}, "pytest"},
		"python -m pytest":    {"python3", []string{"-m", "pytest", "--maxfail=3", "--tb=short", "tests"}, "python -m pytest"},
		"python -m unittest":  {"python3", []string{"-m", "unittest", "-v", "pkg.tests.test_a"}, "python -m unittest"},
		"node --test":         {"node", []string{"--test", "test/a.test.js"}, "node --test"},
		"npm test":            {"npm", []string{"test"}, "npm test"},
		"npm run lint":        {"npm", []string{"run", "lint"}, "npm run"},
		"make test":           {"make", []string{"test"}, "make"},
		"make with jobs":      {"make", []string{"-j", "4", "-k", "build", "test"}, "make"},
		// not cataloged
		"go run":                   {"go", []string{"run", "./cmd/x"}, ""},
		"go generate":              {"go", []string{"generate", "./..."}, ""},
		"go install":               {"go", []string{"install", "./..."}, ""},
		"go test exec":             {"go", []string{"test", "-exec", "sh", "./..."}, ""},
		"go test toolexec":         {"go", []string{"test", "-toolexec=sh", "./..."}, ""},
		"go test overlay":          {"go", []string{"test", "-overlay=/tmp/x.json", "./..."}, ""},
		"go test modfile":          {"go", []string{"test", "-modfile=/tmp/go.mod", "./..."}, ""},
		"go test args":             {"go", []string{"test", "./...", "-args", "-x"}, ""},
		"go test dashdash":         {"go", []string{"test", "--", "./..."}, ""},
		"go test output":           {"go", []string{"test", "-coverprofile=/etc/x", "./..."}, ""},
		"go test remote package":   {"go", []string{"test", "github.com/x/y"}, ""},
		"go test parent package":   {"go", []string{"test", "../x"}, ""},
		"go test absolute package": {"go", []string{"test", "/tmp/x"}, ""},
		"go test dotdot inside":    {"go", []string{"test", "./a/../../x"}, ""},
		"go test count too big":    {"go", []string{"test", "-count=1000", "./..."}, ""},
		"go test count zero":       {"go", []string{"test", "-count=0", "./..."}, ""},
		"go test count text":       {"go", []string{"test", "-count=x", "./..."}, ""},
		"go test bool with value":  {"go", []string{"test", "-v=true", "./..."}, ""},
		"go test missing value":    {"go", []string{"test", "-run"}, ""},
		"go test flag as value":    {"go", []string{"test", "-run", "-v", "./..."}, ""},
		"go test timeout too long": {"go", []string{"test", "-timeout=2h", "./..."}, ""},
		"go test mod mod":          {"go", []string{"test", "-mod=mod", "./..."}, ""},
		"go test control in run":   {"go", []string{"test", "-run", "a\nb", "./..."}, ""},
		"go test empty arg":        {"go", []string{"test", ""}, ""},
		"go test huge run":         {"go", []string{"test", "-run", strings.Repeat("a", 300), "./..."}, ""},
		"go test bad tags":         {"go", []string{"test", "-tags", "a b", "./..."}, ""},
		"go build output":          {"go", []string{"build", "-o", "/tmp/x", "./..."}, ""},
		"gofmt write":              {"gofmt", []string{"-w", "."}, ""},
		"gofmt absolute":           {"gofmt", []string{"-l", "/etc"}, ""},
		"gofmt parent":             {"gofmt", []string{"-l", "../x"}, ""},
		"cargo run":                {"cargo", []string{"run"}, ""},
		"cargo install":            {"cargo", []string{"install", "x"}, ""},
		"cargo test args":          {"cargo", []string{"test", "--", "--nocapture"}, ""},
		"cargo test two names":     {"cargo", []string{"test", "a", "b"}, ""},
		"cargo bad feature":        {"cargo", []string{"check", "--features", "a;b"}, ""},
		"cargo bad package":        {"cargo", []string{"check", "-p", "a/b"}, ""},
		"pytest plugin":            {"pytest", []string{"-p", "evilplugin"}, ""},
		"pytest config":            {"pytest", []string{"-c", "/tmp/x.ini"}, ""},
		"pytest rootdir":           {"pytest", []string{"--rootdir=/"}, ""},
		"pytest absolute":          {"pytest", []string{"/etc/x"}, ""},
		"pytest parent":            {"pytest", []string{"../x"}, ""},
		"python script":            {"python3", []string{"script.py"}, ""},
		"python inline":            {"python3", []string{"-c", "print(1)"}, ""},
		"python -m http":           {"python3", []string{"-m", "http.server"}, ""},
		"node script":              {"node", []string{"script.js"}, ""},
		"node eval":                {"node", []string{"-e", "1"}, ""},
		"node test absolute":       {"node", []string{"--test", "/etc/x"}, ""},
		"npm install":              {"npm", []string{"install"}, ""},
		"npm run arbitrary":        {"npm", []string{"run", "deploy"}, ""},
		"npm test args":            {"npm", []string{"test", "--", "--x"}, ""},
		"npm exec":                 {"npm", []string{"exec", "x"}, ""},
		"make arbitrary target":    {"make", []string{"deploy"}, ""},
		"make file":                {"make", []string{"-f", "/tmp/Makefile", "test"}, ""},
		"make assignment":          {"make", []string{"test", "CC=evil"}, ""},
		"make too many targets":    {"make", []string{"test", "build", "lint", "check"}, ""},
		"unknown program":          {"mytool", []string{"test"}, ""},
		"no arguments":             {"go", nil, ""},
		"only lead partially":      {"python3", []string{"-m"}, ""},
	} {
		if got := ids(tc.program, tc.args...); got != tc.want {
			t.Errorf("%s: matched %q, want %q", name, got, tc.want)
		}
	}
}

func TestAPlannedCommandResolvesWithoutStartingAnything(t *testing.T) {
	e := newCmdEnv(t)
	plan, outcome, reason := e.plan([]string{"go", "test", "-race", "./internal/..."}, `"cwd":"internal"`, `"timeout_seconds":90`)
	if outcome != harness.OutcomeOK || reason != "" {
		t.Fatalf("%s %s", outcome, reason)
	}
	if plan.program != filepath.Join(e.toolDir, "go") || plan.dir != "internal" || plan.realDir != filepath.Join(e.root, "internal") || plan.timeout != 90*time.Second ||
		plan.entry != "go test" || len(plan.escalate) != 0 || plan.profile != "go" || !strings.Contains(plan.identity, plan.program) {
		t.Fatalf("plan = %+v", plan)
	}
	// Defaults: the root, one minute.
	def, _, _ := e.plan([]string{"gofmt", "-l", "."})
	if def.dir != "." || def.realDir != e.root || def.timeout != DefaultCommandTimeout || def.profile != "none" {
		t.Fatalf("defaults = %+v", def)
	}
	// An uncataloged command still plans, with reasons to ask carefully.
	other, outcome, _ := e.plan([]string{"mytool", "--flag", "value"})
	if outcome != harness.OutcomeOK || other.entry != "" || !reflect.DeepEqual(other.escalate, []string{"not_cataloged"}) {
		t.Fatalf("uncataloged = %+v %s", other, outcome)
	}
	// Commands that run a recipe defined in the project say so.
	for _, argv := range [][]string{{"npm", "test"}, {"npm", "run", "build"}, {"make", "test"}} {
		recipe, outcome, _ := e.plan(argv)
		if outcome != harness.OutcomeOK || !reflect.DeepEqual(recipe.escalate, []string{"runs_recipes"}) || recipe.entry == "" {
			t.Errorf("%v: %+v %s", argv, recipe, outcome)
		}
	}
	// An interpreter given code on its command line says that too.
	inline, _, _ := e.plan([]string{"python3", "-c", "print(1)"})
	if !reflect.DeepEqual(inline.escalate, []string{"not_cataloged", "inline_code"}) {
		t.Fatalf("inline = %+v", inline.escalate)
	}
	perl, _, _ := e.plan([]string{"perl", "-e", "1"})
	if !contains(perl.escalate, "inline_code") {
		t.Fatalf("perl = %+v", perl.escalate)
	}
}

func TestDeniedProgramsAreRefusedWhateverTheirSpelling(t *testing.T) {
	e := newCmdEnv(t)
	for _, argv := range [][]string{
		{"bash", "-c", "ls"}, {"sh"}, {"BASH"}, {"Sh"}, {"curl", "https://example.com"}, {"git", "status"}, {"friendly", "-c", "ls"}, // a link to bash
		{"/bin/sh", "-c", "ls"}, {"/usr/bin/curl"}, {"../bash"}, {"./../../bin/sh"}, {"./sub/../sh"}, {"bash5"}, {"zsh-5.9"}, {"env", "X=1", "ls"}, {"xargs"},
		{"sudo", "ls"}, {"rm", "-rf", "."}, {"chmod", "777", "x"}, {"ssh", "host"}, {"nohup", "ls"}, {"osascript", "-e", "x"}, {"docker", "ps"}, {"kill", "1"},
	} {
		plan, outcome, reason := e.plan(argv)
		if outcome != harness.OutcomeDenied && outcome != harness.OutcomeFailed || plan != nil {
			t.Errorf("%v was planned: %v %s", argv, outcome, reason)
		}
	}
	// Denied is the answer for a bare name whether or not it is installed, so what a model
	// learns does not depend on this machine.
	for _, argv := range [][]string{{"bash"}, {"curl"}, {"git"}, {"friendly"}, {"sh"}, {"ssh"}, {"sudo"}, {"rm"}, {"kill"}, {"xargs"}, {"env"}, {"nohup"}, {"docker"}, {"osascript"}, {"BASH"}, {"bash5"}} {
		if _, outcome, reason := e.plan(argv); outcome != harness.OutcomeDenied || reason != "program_denied" {
			t.Errorf("%v: %s %s", argv, outcome, reason)
		}
	}
	// A denied program inside the project is denied too, even though it is a script there.
	write(t, e.root, "scripts/bash", "#!/bin/sh\nexit 0\n")
	if err := os.Chmod(filepath.Join(e.root, "scripts/bash"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, outcome, reason := e.plan([]string{"./scripts/bash"}); outcome != harness.OutcomeDenied || reason != "program_denied" {
		t.Fatalf("a script named bash: %s %s", outcome, reason)
	}
}

func TestAProgramInsideTheProjectIsNeverFoundOnTheSearchPath(t *testing.T) {
	e := newCmdEnv(t)
	link := filepath.Join(realTemp(t), "toolslink")
	if err := os.Symlink(filepath.Join(e.root, "bin"), link); err != nil {
		t.Fatal(err)
	}
	cases := map[string][]string{
		"an entry inside the project":   {e.inboxDir},
		"a link into the project":       {link},
		"a relative entry":              {"bin", "./bin", "../bin"},
		"an empty entry":                {""},
		"a missing entry":               {"/nonexistent/dir"},
		"the project root itself":       {e.root},
		"a project entry before a real": {e.inboxDir, e.toolDir},
	}
	for name, dirs := range cases {
		e.cfg.SearchPath = dirs
		plan, outcome, reason := e.plan([]string{"go", "test", "./..."})
		if name == "a project entry before a real" {
			if outcome != harness.OutcomeOK || plan.program != filepath.Join(e.toolDir, "go") {
				t.Errorf("%s: %+v %s %s", name, plan, outcome, reason)
			}
			continue
		}
		if outcome != harness.OutcomeFailed || reason != "program_not_found" {
			t.Errorf("%s: found something: %+v %s %s", name, plan, outcome, reason)
		}
	}
	// A link in a search-path directory that leads into the project is not followed there either.
	linkedTools := realTemp(t)
	if err := os.Symlink(filepath.Join(e.root, "bin", "go"), filepath.Join(linkedTools, "go")); err != nil {
		t.Fatal(err)
	}
	e.cfg.SearchPath = []string{linkedTools}
	if _, outcome, reason := e.plan([]string{"go", "test"}); outcome != harness.OutcomeFailed || reason != "program_not_found" {
		t.Fatalf("a link to a project file: %s %s", outcome, reason)
	}
}

func TestAProjectScriptNeedsADotSlashAnExecutableBitAndAnInterpreterLine(t *testing.T) {
	e := newCmdEnv(t)
	plan, outcome, reason := e.plan([]string{"./scripts/test.sh", "arg"})
	if outcome != harness.OutcomeOK || !reflect.DeepEqual(plan.escalate, []string{"project_program", "not_cataloged"}) || plan.entry != "" ||
		plan.program != filepath.Join(e.root, "scripts/test.sh") || !strings.Contains(plan.identity, plan.program) {
		t.Fatalf("%+v %s %s", plan, outcome, reason)
	}
	// Its environment does not put the script's own directory on the path.
	for _, kv := range plan.env {
		if strings.HasPrefix(kv, "PATH=") && strings.Contains(kv, e.root) {
			t.Errorf("the project is on the path: %s", kv)
		}
	}
	// A project program named like a toolchain is never mistaken for the cataloged one.
	write(t, e.root, "scripts/go", "#!/bin/sh\nexit 0\n")
	if err := os.Chmod(filepath.Join(e.root, "scripts/go"), 0o755); err != nil {
		t.Fatal(err)
	}
	spoof, outcome, _ := e.plan([]string{"./scripts/go", "test", "./..."})
	if outcome != harness.OutcomeOK || spoof.entry != "" || !contains(spoof.escalate, "project_program") {
		t.Fatalf("a script named go matched the catalog: %+v", spoof)
	}
	big := "#!/bin/sh\n" + strings.Repeat("# padding\n", maxProjectProgram/10+10) // over the bound, but small enough to read
	write(t, e.root, "scripts/big.sh", big)
	if err := os.Chmod(filepath.Join(e.root, "scripts/big.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, e.root, "scripts/binary.sh", "#!/bin/sh\n\x00\x01\x02")
	if err := os.Chmod(filepath.Join(e.root, "scripts/binary.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(e.root, "scripts/test.sh"), filepath.Join(e.root, "scripts/link.sh")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(e.root, "scripts/adir"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		argv   []string
		want   harness.Outcome
		reason string
	}{
		"no dot slash":   {[]string{"scripts/test.sh"}, harness.OutcomeDenied, ""},
		"absolute":       {[]string{"/etc/hosts"}, harness.OutcomeDenied, ""},
		"parent":         {[]string{"./../x.sh"}, harness.OutcomeDenied, ""},
		"parent inside":  {[]string{"./scripts/../scripts/test.sh"}, harness.OutcomeDenied, ""},
		"not executable": {[]string{"./scripts/plain.txt"}, harness.OutcomeFailed, "program_not_found"},
		"no shebang":     {[]string{"./scripts/noshebang.sh"}, harness.OutcomeFailed, "program_not_script"},
		"too large":      {[]string{"./scripts/big.sh"}, harness.OutcomeFailed, "program_not_script"},
		"binary":         {[]string{"./scripts/binary.sh"}, harness.OutcomeFailed, "program_not_script"},
		"a link":         {[]string{"./scripts/link.sh"}, harness.OutcomeDenied, ""},
		"a directory":    {[]string{"./scripts/adir"}, harness.OutcomeFailed, "program_not_found"},
		"missing":        {[]string{"./scripts/nope.sh"}, harness.OutcomeFailed, "program_not_found"},
		"hidden":         {[]string{"./.hidden/x"}, harness.OutcomeDenied, ""},
		"the root":       {[]string{"./"}, harness.OutcomeFailed, ""},
	} {
		if got, outcome, reason := e.plan(tc.argv); got != nil || outcome != tc.want || (tc.reason != "" && reason != tc.reason) {
			t.Errorf("%s: %+v %s %s", name, got, outcome, reason)
		}
	}
}

func TestArgumentsAndTheWorkingDirectoryAreBoundedAndConfined(t *testing.T) {
	e := newCmdEnv(t)
	manyArgs := make([]string, maxArgvItems+1)
	for i := range manyArgs {
		manyArgs[i] = "x"
	}
	manyArgs[0] = "mytool"
	if err := os.Symlink(e.root, filepath.Join(e.root, "rootlink")); err != nil {
		t.Skip("symlinks unavailable")
	}
	for name, tc := range map[string]struct {
		raw  string
		want harness.Outcome
	}{
		"no argv":              {`{}`, harness.OutcomeFailed},
		"empty argv":           {`{"argv":[]}`, harness.OutcomeFailed},
		"an empty program":     {`{"argv":[""]}`, harness.OutcomeFailed},
		"too many items":       {`{"argv":` + jsonArray(manyArgs) + `}`, harness.OutcomeFailed},
		"a huge item":          {`{"argv":["mytool","` + strings.Repeat("a", maxArgvItemBytes+1) + `"]}`, harness.OutcomeFailed},
		"a huge total":         {`{"argv":["mytool"` + strings.Repeat(`,"`+strings.Repeat("a", 1000)+`"`, 9) + `]}`, harness.OutcomeFailed},
		"a newline":            {`{"argv":["mytool","a\nb"]}`, harness.OutcomeFailed},
		"an escape":            {`{"argv":["mytool","a\u001bb"]}`, harness.OutcomeFailed},
		"a NUL":                {`{"argv":["mytool","a\u0000b"]}`, harness.OutcomeFailed},
		"a DEL":                {`{"argv":["mytool","a\u007fb"]}`, harness.OutcomeFailed},
		"a string not array":   {`{"argv":"ls -la"}`, harness.OutcomeFailed},
		"a shell string":       {`{"argv":["ls -la | wc"]}`, harness.OutcomeFailed},
		"a program with args":  {`{"argv":["mytool --flag"]}`, harness.OutcomeFailed},
		"a program with $":     {`{"argv":["$(mytool)"]}`, harness.OutcomeFailed},
		"a program with ;":     {`{"argv":["mytool;ls"]}`, harness.OutcomeFailed},
		"unknown field":        {`{"argv":["mytool"],"shell":true}`, harness.OutcomeFailed},
		"environment field":    {`{"argv":["mytool"],"env":{"A":"1"}}`, harness.OutcomeFailed},
		"stdin field":          {`{"argv":["mytool"],"stdin":"x"}`, harness.OutcomeFailed},
		"malformed":            {`{"argv":[`, harness.OutcomeFailed},
		"trailing data":        {`{"argv":["mytool"]}{}`, harness.OutcomeFailed},
		"negative timeout":     {`{"argv":["mytool"],"timeout_seconds":-1}`, harness.OutcomeFailed},
		"timeout too long":     {`{"argv":["mytool"],"timeout_seconds":601}`, harness.OutcomeFailed},
		"cwd parent":           {`{"argv":["mytool"],"cwd":".."}`, harness.OutcomeDenied},
		"cwd deep parent":      {`{"argv":["mytool"],"cwd":"a/../../x"}`, harness.OutcomeDenied},
		"cwd absolute":         {`{"argv":["mytool"],"cwd":"/tmp"}`, harness.OutcomeDenied},
		"cwd hidden":           {`{"argv":["mytool"],"cwd":".hidden"}`, harness.OutcomeDenied},
		"cwd git":              {`{"argv":["mytool"],"cwd":".git"}`, harness.OutcomeDenied},
		"cwd a link":           {`{"argv":["mytool"],"cwd":"rootlink"}`, harness.OutcomeDenied},
		"cwd missing":          {`{"argv":["mytool"],"cwd":"nope"}`, harness.OutcomeFailed},
		"cwd a file":           {`{"argv":["mytool"],"cwd":"internal/app/app.go"}`, harness.OutcomeFailed},
		"the least that works": {`{"argv":["mytool"]}`, harness.OutcomeOK},
		"a good cwd":           {`{"argv":["mytool"],"cwd":"internal/app"}`, harness.OutcomeOK},
	} {
		if plan, outcome, reason := planCommand(e.project, e.root, e.cfg, []byte(tc.raw)); outcome != tc.want || (tc.want == harness.OutcomeOK) != (plan != nil) {
			t.Errorf("%s: %s %s (plan %v)", name, outcome, reason, plan != nil)
		}
	}
}

func TestTheEnvironmentIsBuiltNeverInherited(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-secret")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "aws-secret")
	t.Setenv("GOFLAGS", "-exec=evil")
	t.Setenv("NODE_OPTIONS", "--require=/tmp/evil.js")
	t.Setenv("LD_PRELOAD", "/tmp/evil.so")
	t.Setenv("BASH_ENV", "/tmp/evil")
	e := newCmdEnv(t)
	byName := func(argv ...string) map[string]string {
		plan, outcome, _ := e.plan(argv)
		if outcome != harness.OutcomeOK {
			t.Fatalf("%v: %s", argv, outcome)
		}
		out := map[string]string{}
		for _, kv := range plan.env {
			k, v, _ := strings.Cut(kv, "=")
			out[k] = v
		}
		return out
	}
	goEnv := byName("go", "test", "./...")
	want := map[string]string{"PATH": e.toolDir + ":/usr/bin:/bin:/usr/sbin:/sbin", "LANG": "en_US.UTF-8", "TERM": "dumb", "NO_COLOR": "1", "CI": "1", "HOME": "/home/tester",
		"GOPROXY": "off", "GOTOOLCHAIN": "local", "GOFLAGS": ""}
	if !reflect.DeepEqual(goEnv, want) {
		t.Fatalf("go environment = %v, want %v", goEnv, want)
	}
	for profile, argv := range map[string][]string{"cargo": {"cargo", "test"}, "node": {"node", "--test"}, "python": {"pytest"}, "none": {"gofmt", "-l", "."}, "uncataloged": {"mytool"}} {
		env := byName(argv...)
		for _, leaked := range []string{"OPENAI_API_KEY", "AWS_SECRET_ACCESS_KEY", "NODE_OPTIONS", "LD_PRELOAD", "BASH_ENV", "USER", "SHELL", "SSH_AUTH_SOCK"} {
			if _, ok := env[leaked]; ok {
				t.Errorf("%s: %s reached the command", profile, leaked)
			}
		}
		if env["GOFLAGS"] != "" && profile != "go" {
			t.Errorf("%s: GOFLAGS leaked: %q", profile, env["GOFLAGS"])
		}
	}
	if byName("cargo", "test")["CARGO_NET_OFFLINE"] != "true" || byName("npm", "test")["npm_config_offline"] != "true" || byName("pytest")["PYTHONDONTWRITEBYTECODE"] != "1" {
		t.Fatal("a toolchain profile is missing its offline or quiet setting")
	}
	// With no home configured and none to find, none is passed.
	e.cfg.Home = ""
	t.Setenv("HOME", "")
	if env := byName("mytool"); env["HOME"] != "" {
		t.Errorf("an empty home was passed: %v", env)
	}
}

func TestTheDigestNamesTheExactExecutableArgumentsDirectoryLimitsAndProject(t *testing.T) {
	e := newCmdEnv(t)
	digest := func(argv []string, extra ...string) string {
		plan, outcome, reason := e.plan(argv, extra...)
		if outcome != harness.OutcomeOK {
			t.Fatalf("%v: %s %s", argv, outcome, reason)
		}
		return plan.digest(e.root)
	}
	base := digest([]string{"go", "test", "./..."})
	if base != digest([]string{"go", "test", "./..."}) || !strings.HasPrefix(base, "sha256:") || len(base) != 71 {
		t.Fatalf("the digest is not stable or well formed: %s", base)
	}
	for name, other := range map[string]string{
		"another argument":  digest([]string{"go", "test", "-race", "./..."}),
		"another package":   digest([]string{"go", "test", "./internal/..."}),
		"another directory": digest([]string{"go", "test", "./..."}, `"cwd":"internal"`),
		"another timeout":   digest([]string{"go", "test", "./..."}, `"timeout_seconds":61`),
		"another program":   digest([]string{"go", "vet", "./..."}),
	} {
		if other == base {
			t.Errorf("the digest ignores %s", name)
		}
	}
	plan, _, _ := e.plan([]string{"go", "test", "./..."})
	if plan.digest(e.root) == plan.digest(filepath.Join(e.root, "elsewhere")) {
		t.Error("the digest ignores the project root")
	}
	// The executable itself: replacing it, or changing it, changes the digest.
	goPath := filepath.Join(e.toolDir, "go")
	if err := os.WriteFile(goPath, []byte("#!/bin/sh\necho a different go\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if digest([]string{"go", "test", "./..."}) == base {
		t.Error("the digest ignores a replaced executable")
	}
	same := digest([]string{"go", "test", "./..."})
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(goPath, later, later); err != nil {
		t.Fatal(err)
	}
	if digest([]string{"go", "test", "./..."}) == same {
		t.Error("the digest ignores a touched executable")
	}
	// A project script is bound by its content.
	first := digest([]string{"./scripts/test.sh"})
	write(t, e.root, "scripts/test.sh", "#!/bin/sh\necho changed\n")
	if err := os.Chmod(filepath.Join(e.root, "scripts/test.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	if digest([]string{"./scripts/test.sh"}) == first {
		t.Error("the digest ignores a changed script")
	}
	// And the environment rules in force.
	p := &commandPlan{argv: []string{"x"}, program: "/x", identity: "i", profile: "go", dir: ".", timeout: time.Second}
	q := *p
	q.profile = "node"
	if p.digest("/r") == q.digest("/r") {
		t.Error("the digest ignores the environment profile")
	}
}

func TestTheDenyListJudgesVersionSuffixesAndPathsByTheirBaseName(t *testing.T) {
	for name, want := range map[string]bool{
		"bash": true, "BASH": true, "bash5": true, "zsh-5.9": true, "sh.exe": false, "/bin/sh": true, "x/y/curl": true, "python3": false, "go": false, "make": false,
		"node": false, "git": true, "git2": true, "shell": false, "ssh-agent": false, "xargs": true, "env": true, "environment": false,
	} {
		if got := deniedName(name); got != want {
			t.Errorf("deniedName(%q) = %v, want %v", name, got, want)
		}
	}
	for _, name := range []string{"python", "python3", "python3.12", "node", "perl", "ruby", "php", "osascript"} {
		if !inlineCode(name, []string{"-c", "x"}) && !inlineCode(name, []string{"-e", "x"}) {
			t.Errorf("%s with inline code was not noticed", name)
		}
	}
	for _, name := range []string{"go", "make", "cargo", "pytest", "ls"} {
		if inlineCode(name, []string{"-e", "x"}) {
			t.Errorf("%s was treated as an interpreter", name)
		}
	}
	if inlineCode("python3", []string{"-m", "pytest"}) || inlineCode("node", []string{"--test"}) {
		t.Error("a module or the test runner was treated as inline code")
	}
}

func TestOnlyExecutableFilesOnTheSearchPathAreFoundAndRelativeEntriesNeverAre(t *testing.T) {
	e := newCmdEnv(t)
	if err := os.WriteFile(filepath.Join(e.toolDir, "noexec"), []byte("#!/bin/sh\nexit 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, outcome, reason := e.plan([]string{"noexec"}); outcome != harness.OutcomeFailed || reason != "program_not_found" {
		t.Fatalf("a non-executable file was found: %s %s", outcome, reason)
	}
	if err := os.Mkdir(filepath.Join(e.toolDir, "adir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, outcome, _ := e.plan([]string{"adir"}); outcome != harness.OutcomeFailed {
		t.Fatalf("a directory was found: %s", outcome)
	}
	// A relative entry is not resolved against wherever the process happens to be, even when
	// that is a directory full of programs outside the project.
	t.Chdir(e.toolDir)
	e.cfg.SearchPath = []string{".", "", "./"}
	if plan, outcome, _ := e.plan([]string{"go", "test"}); outcome != harness.OutcomeFailed || plan != nil {
		t.Fatalf("a relative search-path entry was used: %v %s", plan, outcome)
	}
}

func TestInsideIsExactAboutTheDirectoryItself(t *testing.T) {
	for _, tc := range []struct {
		dir, path string
		want      bool
	}{
		{"/a/b", "/a/b", true}, {"/a/b", "/a/b/c", true}, {"/a/b", "/a/b/c/d", true}, {"/a/b", "/a", false}, {"/a/b", "/a/bc", false}, {"/a/b", "/x", false}, {"/a/b", "/", false}, {"/a/b", "/a/b/../c", false},
	} {
		if got := inside(tc.dir, tc.path); got != tc.want {
			t.Errorf("inside(%s, %s) = %v", tc.dir, tc.path, got)
		}
	}
}

func TestAToolDirectoryThatContainsTheProjectIsStillSearched(t *testing.T) {
	parent := realTemp(t)
	root := filepath.Join(parent, "project")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "go"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	project, err := harnessfs.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer project.Close()
	plan, outcome, reason := planCommand(project, root, CommandConfig{SearchPath: []string{parent}}, []byte(`{"argv":["go","test"]}`))
	if outcome != harness.OutcomeOK || plan.program != filepath.Join(parent, "go") {
		t.Fatalf("%v %s %s", plan, outcome, reason)
	}
}

func TestShellLookingProgramNamesAreRejectedForWhatTheyAre(t *testing.T) {
	e := newCmdEnv(t)
	for _, name := range []string{"ls -la | wc", "mytool --flag", "$(mytool)", "mytool;ls", "`mytool`", "my tool", "mytool&", "-mytool", "*"} {
		if _, outcome, reason := e.plan([]string{name}); outcome != harness.OutcomeFailed || reason != "invalid_arguments" {
			t.Errorf("%q: %s %s", name, outcome, reason)
		}
	}
	// A flag value just under and just over its own bound.
	short, long := strings.Repeat("a", 200), strings.Repeat("a", 201)
	if matchCatalog("go", []string{"test", "-run", short, "./..."}) == nil || matchCatalog("go", []string{"test", "-run", long, "./..."}) != nil {
		t.Error("the -run bound is not exact")
	}
}

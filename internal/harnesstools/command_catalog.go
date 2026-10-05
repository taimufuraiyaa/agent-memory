package harnesstools

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The catalog lists toolchain invocations whose flags and arguments are known, so that an
// ordinary test, build or lint run asks with the ordinary friction. It is deliberately small
// and strict: a flag that is not listed, a flag that runs another program or writes outside
// the project, an argument after "--", or a positional argument that is not a local path or
// a known name all make a command not cataloged, which asks with extra friction instead.

type flagKind int

const (
	flagBool flagKind = iota
	flagInt
	flagText
	flagDuration
	flagEnum
	flagTags
)

type flagSpec struct {
	kind     flagKind
	min, max int
	enum     []string
	pattern  *regexp.Regexp
	maxLen   int
}

type positionalKind int

const (
	posNone positionalKind = iota
	posPackages
	posPaths
	posTestPaths
	posNames
	posTargets
)

type entry struct {
	id       string
	program  string   // the requested program name
	lead     []string // fixed leading arguments
	flags    map[string]flagSpec
	aliases  map[string]string // short flag to canonical
	pos      positionalKind
	maxPos   int
	scripts  []string // for posTargets: the only accepted names
	recipe   bool     // runs a recipe defined in the project, so it asks with extra friction
	profile  string   // environment profile
	describe string
}

var (
	localPackageRE = regexp.MustCompile(`^(\.|\./[A-Za-z0-9_@+./-]*)$`)
	localPathRE    = regexp.MustCompile(`^(\.|[A-Za-z0-9_@+][A-Za-z0-9_@+./-]*|\./[A-Za-z0-9_@+./-]*)$`)
	testPathRE     = regexp.MustCompile(`^(\.|[A-Za-z0-9_@+][A-Za-z0-9_@+./-]*|\./[A-Za-z0-9_@+./-]*)(::[A-Za-z0-9_\[\]-]+)*$`)
	testNameRE     = regexp.MustCompile(`^[A-Za-z0-9_:]{1,100}$`)
	packageNameRE  = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	featureListRE  = regexp.MustCompile(`^[A-Za-z0-9_,-]{1,200}$`)
	tagListRE      = regexp.MustCompile(`^[A-Za-z0-9_,.]{1,200}$`)
	dottedNameRE   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.]{0,150}$`)
)

func boolFlag() flagSpec            { return flagSpec{kind: flagBool} }
func intFlag(min, max int) flagSpec { return flagSpec{kind: flagInt, min: min, max: max} }
func textFlag(max int) flagSpec     { return flagSpec{kind: flagText, maxLen: max} }
func enumFlag(v ...string) flagSpec { return flagSpec{kind: flagEnum, enum: v} }
func patternFlag(re *regexp.Regexp) flagSpec {
	return flagSpec{kind: flagText, pattern: re, maxLen: 200}
}

var catalog = buildCatalog()

func buildCatalog() []entry {
	goTest := map[string]flagSpec{"-v": boolFlag(), "-race": boolFlag(), "-short": boolFlag(), "-failfast": boolFlag(), "-cover": boolFlag(), "-json": boolFlag(),
		"-count": intFlag(1, 100), "-run": textFlag(200), "-skip": textFlag(200), "-timeout": {kind: flagDuration}, "-p": intFlag(1, 64), "-parallel": intFlag(1, 64),
		"-tags": {kind: flagTags}, "-shuffle": {kind: flagText, pattern: regexp.MustCompile(`^(on|off|[0-9]{1,18})$`), maxLen: 20}, "-mod": enumFlag("readonly", "vendor"), "-vet": enumFlag("off")}
	return []entry{
		{id: "go test", program: "go", lead: []string{"test"}, flags: goTest, pos: posPackages, maxPos: 16, profile: "go", describe: "run Go tests"},
		{id: "go vet", program: "go", lead: []string{"vet"}, flags: map[string]flagSpec{"-tags": {kind: flagTags}, "-mod": enumFlag("readonly", "vendor")}, pos: posPackages, maxPos: 16, profile: "go", describe: "run Go vet"},
		{id: "go build", program: "go", lead: []string{"build"}, flags: map[string]flagSpec{"-tags": {kind: flagTags}, "-race": boolFlag(), "-v": boolFlag(), "-mod": enumFlag("readonly", "vendor")}, pos: posPackages, maxPos: 16, profile: "go", describe: "build Go packages without keeping the result"},
		{id: "gofmt", program: "gofmt", flags: map[string]flagSpec{"-l": boolFlag(), "-d": boolFlag(), "-s": boolFlag()}, pos: posPaths, maxPos: 16, profile: "none", describe: "list or diff unformatted Go files"},
		cargoEntry("cargo test", "test", true),
		cargoEntry("cargo check", "check", false),
		cargoEntry("cargo build", "build", false),
		cargoEntry("cargo clippy", "clippy", false),
		{id: "pytest", program: "pytest", flags: pytestFlags(), pos: posTestPaths, maxPos: 16, profile: "python", describe: "run pytest"},
		{id: "python -m pytest", program: "python3", lead: []string{"-m", "pytest"}, flags: pytestFlags(), pos: posTestPaths, maxPos: 16, profile: "python", describe: "run pytest"},
		{id: "python -m unittest", program: "python3", lead: []string{"-m", "unittest"}, flags: map[string]flagSpec{"-v": boolFlag(), "-f": boolFlag(), "-q": boolFlag(), "-k": textFlag(200)}, pos: posNames, maxPos: 16, profile: "python", describe: "run unittest"},
		{id: "node --test", program: "node", lead: []string{"--test"}, flags: map[string]flagSpec{"--test-name-pattern": textFlag(200)}, pos: posPaths, maxPos: 16, profile: "node", describe: "run Node's test runner"},
		{id: "npm test", program: "npm", lead: []string{"test"}, pos: posNone, recipe: true, profile: "node", describe: "run the project's test script"},
		{id: "npm run", program: "npm", lead: []string{"run"}, pos: posTargets, maxPos: 1, scripts: []string{"test", "build", "lint", "check", "typecheck"}, recipe: true, profile: "node", describe: "run a project script"},
		{id: "make", program: "make", flags: map[string]flagSpec{"-j": intFlag(1, 64), "-k": boolFlag()}, pos: posTargets, maxPos: 3, scripts: []string{"test", "build", "lint", "check", "all"}, recipe: true, profile: "none", describe: "run make targets"},
	}
}

func cargoEntry(id, sub string, takesName bool) entry {
	pos := posNone
	if takesName {
		pos = posNames
	}
	return entry{id: id, program: "cargo", lead: []string{sub}, aliases: map[string]string{"-q": "--quiet", "-p": "--package"}, pos: pos, maxPos: 1, profile: "cargo",
		flags: map[string]flagSpec{"--workspace": boolFlag(), "--lib": boolFlag(), "--bins": boolFlag(), "--all-targets": boolFlag(), "--offline": boolFlag(), "--release": boolFlag(),
			"--no-fail-fast": boolFlag(), "--quiet": boolFlag(), "--all-features": boolFlag(), "--locked": boolFlag(), "--package": patternFlag(packageNameRE), "--features": patternFlag(featureListRE)},
		describe: "run Cargo " + sub}
}

func pytestFlags() map[string]flagSpec {
	return map[string]flagSpec{"-x": boolFlag(), "-q": boolFlag(), "-v": boolFlag(), "-k": textFlag(200), "-m": textFlag(200), "--maxfail": intFlag(1, 1000), "--tb": enumFlag("short", "long", "line", "no", "auto")}
}

// matchCatalog decides whether argv (after the program) is exactly a cataloged invocation.
// It returns the entry, or nil when it is not one.
func matchCatalog(program string, args []string) *entry {
	for i := range catalog {
		e := &catalog[i]
		if e.program != program || len(args) < len(e.lead) {
			continue
		}
		match := true
		for j, want := range e.lead {
			if args[j] != want {
				match = false
				break
			}
		}
		if match && e.accepts(args[len(e.lead):]) {
			return e
		}
	}
	return nil
}

func controlFree(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func (e *entry) accepts(rest []string) bool {
	positional := 0
	for i := 0; i < len(rest); i++ {
		token := rest[i]
		if token == "" || !controlFree(token) {
			return false
		}
		if token == "--" {
			return false
		}
		if strings.HasPrefix(token, "-") && token != "-" {
			name, value, hasValue := strings.Cut(token, "=")
			if canonical, ok := e.aliases[name]; ok {
				name = canonical
			}
			spec, ok := e.flags[name]
			if !ok {
				return false
			}
			if spec.kind == flagBool {
				if hasValue {
					return false
				}
				continue
			}
			if !hasValue {
				if i+1 >= len(rest) || strings.HasPrefix(rest[i+1], "-") {
					return false
				}
				i++
				value = rest[i]
			}
			if !spec.valueOK(value) {
				return false
			}
			continue
		}
		positional++
		if positional > e.maxPos || !e.positionalOK(token) {
			return false
		}
	}
	return true
}

func (s flagSpec) valueOK(value string) bool {
	if value == "" || !controlFree(value) || len(value) > 256 {
		return false
	}
	switch s.kind {
	case flagInt:
		n, err := strconv.Atoi(value)
		return err == nil && n >= s.min && n <= s.max
	case flagText:
		if s.maxLen > 0 && len(value) > s.maxLen {
			return false
		}
		return s.pattern == nil || s.pattern.MatchString(value)
	case flagDuration:
		d, err := time.ParseDuration(value)
		return err == nil && d > 0 && d <= 10*time.Minute
	case flagEnum:
		for _, v := range s.enum {
			if value == v {
				return true
			}
		}
		return false
	case flagTags:
		return tagListRE.MatchString(value)
	}
	return false
}

func hasDotDot(token string) bool {
	for _, part := range strings.Split(token, "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

func (e *entry) positionalOK(token string) bool {
	switch e.pos {
	case posPackages:
		base := strings.TrimSuffix(token, "/...")
		return len(token) <= 200 && localPackageRE.MatchString(base) && !hasDotDot(token)
	case posPaths:
		return len(token) <= 200 && localPathRE.MatchString(token) && !hasDotDot(token)
	case posTestPaths:
		return len(token) <= 200 && testPathRE.MatchString(token) && !hasDotDot(token)
	case posNames:
		return testNameRE.MatchString(token) || dottedNameRE.MatchString(token)
	case posTargets:
		for _, name := range e.scripts {
			if token == name {
				return true
			}
		}
	}
	return false
}

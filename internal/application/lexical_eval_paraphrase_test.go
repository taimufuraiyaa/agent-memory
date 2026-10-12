package application

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// evalAnchor names a gold chunk by file path plus a verbatim fragment of the
// chunk text. Resolution happens at run time and must find at least one chunk,
// so a re-chunked corpus fails loudly instead of silently shifting gold.
type evalAnchor struct {
	Path   string
	Anchor string
}

// evalParaphrase is a hand-written query that deliberately avoids the gold
// chunk's distinctive tokens. Gold chunks were located and verified by reading
// the chunk text. Curated holds the explicit keywords an agent would attach to
// the gold memory (used only by the curated exact-term experiment).
type evalParaphrase struct {
	Query   string
	Gold    []evalAnchor
	Curated []string
}

const (
	evalSecurityDoc   = "docs/security.md"
	evalBenchDoc      = "docs/benchmarking.md"
	evalErrorsDoc     = "docs/error-handling-guide.md"
	evalInstallDoc    = "docs/manual-installation.md"
	evalObserveDoc    = "docs/observability.md"
	evalConfigDoc     = "docs/configuration.md"
	evalPluginDoc     = "docs/plugin-development.md"
	evalTerminalDoc   = "docs/terminal-interface.md"
	evalTDDSkill      = ".agents/skills/test-driven-development/SKILL.md"
	evalSimplifySkill = ".agents/skills/code-simplification/SKILL.md"
	evalDebugSkill    = ".agents/skills/debugging-and-error-recovery/SKILL.md"
)

var evalParaphrases = []evalParaphrase{
	{"what steps should I take right away if somebody breaks into the system and I think data was stolen",
		[]evalAnchor{{evalSecurityDoc, "Stop agent-memory processes immediately"}}, []string{"incident", "breach", "response"}},
	{"how do I turn off the scrubbing of sensitive values when debugging",
		[]evalAnchor{{evalSecurityDoc, "AGENT_MEMORY_DISABLE_FILTERING"}}, []string{"scrubbing", "redaction", "secrets"}},
	{"recommended practices for packaging the tool in a docker image with an unprivileged account",
		[]evalAnchor{{evalSecurityDoc, "adduser -D -u 1000"}}, []string{"container", "docker", "nonroot"}},
	{"a table rating how probable and damaging each danger is",
		[]evalAnchor{{evalSecurityDoc, "Likelihood"}}, []string{"risk", "matrix", "threats"}},
	{"which kinds of reports will the maintainers decline to handle",
		[]evalAnchor{{"SECURITY.md", "Social engineering attacks"}}, []string{"scope", "exclusions", "vulnerability"}},
	{"how quickly will maintainers respond after I disclose a flaw privately",
		[]evalAnchor{{"SECURITY.md", "within 48 hours"}}, []string{"disclosure", "response", "timeline"}},
	{"sharing one workstation among several people",
		[]evalAnchor{{"SECURITY.md", "If multiple users share a machine"}}, []string{"multiuser", "permissions", "accounts"}},
	{"what leaves my computer when a hosted vectorization service is used",
		[]evalAnchor{{"SECURITY.md", "Content is sent to OpenAI for embedding"}, {evalSecurityDoc, "Data sent to OpenAI"}}, []string{"openai", "privacy", "embedding"}},
	{"acceptable speed ceilings and memory consumption budgets that code must stay under",
		[]evalAnchor{{evalBenchDoc, "Target: < 5000 ns/op"}}, []string{"performance", "budget", "targets"}},
	{"when should a slowdown be flagged as a problem worth looking into",
		[]evalAnchor{{evalBenchDoc, "Time/op increase"}}, []string{"regression", "thresholds", "monitoring"}},
	{"comparing performance numbers from two code versions statistically",
		[]evalAnchor{{evalBenchDoc, "go install golang.org/x/perf/cmd/benchstat"}}, []string{"benchstat", "comparison", "benchmarks"}},
	{"finding which functions consume the most RAM using the Go profiler",
		[]evalAnchor{{evalBenchDoc, "memprofile"}}, []string{"profiling", "pprof", "heap"}},
	{"why stringifying a failure drops the original cause so callers cannot inspect it",
		[]evalAnchor{{evalErrorsDoc, "%v doesn't preserve"}}, []string{"wrapping", "errors", "chain"}},
	{"keeping credentials out of failure messages shown to users",
		[]evalAnchor{{evalErrorsDoc, "Exposes password"}}, []string{"errors", "secrets", "sanitize"}},
	{"tooling that scans the code for places that hand back failures without adding detail",
		[]evalAnchor{{evalErrorsDoc, "audit-errors.sh"}}, []string{"audit", "errors", "script"}},
	{"predefined shared failure values you can return and compare for common conditions",
		[]evalAnchor{{evalErrorsDoc, "core.ErrInvalidInput"}}, []string{"sentinel", "errors", "core"}},
	{"where to put the inference runtime library on Microsoft's operating system",
		[]evalAnchor{{evalInstallDoc, "onnxruntime.dll"}}, []string{"windows", "runtime", "install"}},
	{"keeping a private copy of the binaries for dependable team rollouts",
		[]evalAnchor{{evalInstallDoc, "Artifactory"}}, []string{"mirror", "artifacts", "offline"}},
	{"getting package downloads to work when outbound traffic goes through a gateway but is not fully isolated",
		[]evalAnchor{{evalInstallDoc, "HTTPS_PROXY"}}, []string{"proxy", "corporate", "network"}},
	{"how to confirm the runtime library was found and vectors are being produced after setup",
		[]evalAnchor{{evalInstallDoc, "onnx_runtime_available"}}, []string{"verify", "health", "install"}},
	{"how much does monitoring slow the application down",
		[]evalAnchor{{evalObserveDoc, "Total overhead"}}, []string{"observability", "overhead", "cost"}},
	{"advice on how many requests to capture for distributed request tracking in live environments",
		[]evalAnchor{{evalObserveDoc, "Sample in production"}}, []string{"tracing", "sampling", "production"}},
	{"options for centralizing application output from many servers",
		[]evalAnchor{{evalObserveDoc, "ELK Stack"}}, []string{"logging", "aggregation", "loki"}},
	{"debugging missing spans in the backend",
		[]evalAnchor{{evalObserveDoc, "TRACING_ENABLED=true"}}, []string{"tracing", "troubleshooting", "otel"}},
	{"which conditions should page the on-call engineer",
		[]evalAnchor{{evalObserveDoc, "Error Rates"}}, []string{"alerts", "paging", "slo"}},
	{"counters for how often vectors are produced and how long that takes",
		[]evalAnchor{{evalObserveDoc, "agent_memory_embedding_total"}}, []string{"metrics", "embedding", "prometheus"}},
	{"you should have written automated checks for anything you cared about; platform upgrades will not guard behavior for you",
		[]evalAnchor{{evalTDDSkill, "Beyonce Rule"}}, []string{"testing", "ownership", "regression"}},
	{"when a defect report comes in do not jump to patching, first create a reproduction that goes red",
		[]evalAnchor{{evalTDDSkill, "do not start by trying to fix it"}}, []string{"bugfix", "tdd", "reproduction"}},
	{"using fakes too heavily yields passing suites while real systems fail",
		[]evalAnchor{{evalTDDSkill, "Over-mocking creates"}}, []string{"mocks", "testing", "doubles"}},
	{"why test code should tolerate duplication in favor of readability",
		[]evalAnchor{{evalTDDSkill, "DAMP (Descriptive And Meaningful Phrases)"}}, []string{"damp", "dry", "readability"}},
	{"check what the function produced rather than verifying the sequence of collaborator invocations",
		[]evalAnchor{{evalTDDSkill, "Tests that verify method call sequences"}}, []string{"state", "interactions", "testing"}},
	{"categorizing automated checks by what infrastructure they touch into small medium and large",
		[]evalAnchor{{evalTDDSkill, "Single process, no I/O"}}, []string{"test-sizes", "resources", "testing"}},
	{"do not delete something until you know the reason it was put there",
		[]evalAnchor{{evalSimplifySkill, "Chesterton's Fence: if you see"}}, []string{"refactoring", "legacy", "caution"}},
	{"very large refactors should be scripted not hand edited",
		[]evalAnchor{{evalSimplifySkill, "Rule of 500"}}, []string{"refactoring", "automation", "codemod"}},
	{"dangers of making code too terse so abstractions lose their names",
		[]evalAnchor{{evalSimplifySkill, "Inlining too aggressively"}}, []string{"over-simplification", "readability", "refactoring"}},
	{"when anything surprising occurs halt new work and retain the output",
		[]evalAnchor{{evalDebugSkill, "STOP adding features"}}, []string{"debugging", "triage", "evidence"}},
	{"address the actual origin instead of masking the visible problem",
		[]evalAnchor{{evalDebugSkill, "Fix the underlying issue, not the symptom"}}, []string{"debugging", "rootcause", "fix"}},
	{"automatically binary-searching commit history for the change that broke things",
		[]evalAnchor{{evalDebugSkill, "git bisect start"}}, []string{"bisect", "regression", "git"}},
	{"failure output coming from outside sources may carry hostile directives and must not be obeyed",
		[]evalAnchor{{evalDebugSkill, "data to analyze, not instructions to follow"}}, []string{"untrusted", "injection", "security"}},
	{"which diagnostic additions are worth leaving in the codebase permanently",
		[]evalAnchor{{evalDebugSkill, "Permanent instrumentation (keep)"}}, []string{"instrumentation", "logging", "debugging"}},
	{"which setting wins when the same option is specified in several places",
		[]evalAnchor{{evalConfigDoc, "defaults < user config"}}, []string{"precedence", "configuration", "overrides"}},
	{"setting up a hosted remote service to produce vectors instead of the local model",
		[]evalAnchor{{evalConfigDoc, "text-embedding-3-small"}, {evalSecurityDoc, "AGENT_MEMORY_EMBEDDING_PROVIDER=openai"}}, []string{"openai", "embedding", "provider"}},
	{"tuning how ranking signals are balanced so they add up to one",
		[]evalAnchor{{evalConfigDoc, "Emphasize semantic similarity"}, {evalConfigDoc, "Scoring weights (must sum"}}, []string{"weights", "ranking", "tuning"}},
	{"making the tool print very detailed internal diagnostics to track down faults",
		[]evalAnchor{{evalConfigDoc, "Enable debug logging"}}, []string{"debug", "logging", "diagnostics"}},
	{"protecting plugin state when several goroutines call it at once",
		[]evalAnchor{{evalPluginDoc, "Use mutexes for shared state"}}, []string{"concurrency", "plugins", "mutex"}},
	{"what an extension must do on teardown so nothing leaks",
		[]evalAnchor{{evalPluginDoc, "Clean up resources in `Shutdown()`"}}, []string{"plugins", "shutdown", "cleanup"}},
	{"how release numbers should change when a module gets incompatible updates",
		[]evalAnchor{{evalPluginDoc, "Increment MAJOR for breaking changes"}}, []string{"semver", "plugins", "versioning"}},
	{"roadmap for swapping out the persistence layer for server-based engines",
		[]evalAnchor{{evalPluginDoc, "PostgreSQL backend"}}, []string{"storage", "plugins", "roadmap"}},
	{"keyboard shortcuts for jumping between the application's screens by number",
		[]evalAnchor{{evalTerminalDoc, "Search all workspace destinations"}}, []string{"keybindings", "tui", "navigation"}},
	{"what to do when the main list appears blank",
		[]evalAnchor{{evalTerminalDoc, "No memories in this workspace yet"}}, []string{"tui", "troubleshooting", "empty"}},
}

var evalParaphraseWord = regexp.MustCompile(`[A-Za-z]{5,}`)

// evalResolveAnchors returns the chunk indexes an anchor list resolves to. It
// fails when an anchor is missing or ambiguous (more than three chunks).
func evalResolveAnchors(t *testing.T, chunks []evalChunk, query string, anchors []evalAnchor) []int {
	t.Helper()
	var idx []int
	for _, a := range anchors {
		var matched []int
		for i, c := range chunks {
			if c.Path == a.Path && strings.Contains(c.Text, a.Anchor) {
				matched = append(matched, i)
			}
		}
		if len(matched) == 0 {
			t.Fatalf("paraphrase %q: anchor %q matched no chunk in %s", query, a.Anchor, a.Path)
		}
		if len(matched) > 3 {
			t.Fatalf("paraphrase %q: anchor %q is ambiguous (%d chunks in %s)", query, a.Anchor, len(matched), a.Path)
		}
		idx = append(idx, matched...)
	}
	return idx
}

// evalBuildParaphraseQueries resolves anchors to chunk IDs. It fails the test
// when an anchor matches nothing, and when a paraphrase repeats a token that is
// rare in the corpus (document frequency <= 3 chunks) and present in its gold
// chunk, because that would reintroduce the lexical leak this family exists to
// remove.
func evalBuildParaphraseQueries(t *testing.T, chunks []evalChunk) []evalQuery {
	t.Helper()
	df := map[string]int{}
	chunkWords := make([]map[string]bool, len(chunks))
	for i, c := range chunks {
		words := map[string]bool{}
		for _, tok := range evalTokens(c.Text) {
			words[strings.ToLower(tok)] = true
		}
		chunkWords[i] = words
		for w := range words {
			df[w]++
		}
	}
	var out []evalQuery
	for _, p := range evalParaphrases {
		goldIdx := evalResolveAnchors(t, chunks, p.Query, p.Gold)
		gold := map[string]bool{}
		for _, i := range goldIdx {
			gold[chunks[i].ID] = true
		}
		for _, word := range evalParaphraseWord.FindAllString(strings.ToLower(p.Query), -1) {
			for _, i := range goldIdx {
				if chunkWords[i][word] && df[word] <= 3 {
					t.Errorf("paraphrase %q reuses rare gold token %q (df=%d)", p.Query, word, df[word])
				}
			}
		}
		out = append(out, evalQuery{Family: "paraphrase", Text: p.Query, Gold: gold})
	}
	if t.Failed() {
		t.FailNow()
	}
	if len(out) < 40 {
		t.Fatalf("paraphrase family too small: %d", len(out))
	}
	return out
}

// evalCuratedKeywords maps a gold chunk ID to the curated keywords attached to
// it, and builds the "tag lookup" queries (two curated terms) that exercise the
// exact-term stream the way an agent searching by its own tags would.
func evalCuratedKeywords(t *testing.T, chunks []evalChunk) (map[string][]string, []evalQuery) {
	t.Helper()
	curated := map[string][]string{}
	var tagQueries []evalQuery
	for _, p := range evalParaphrases {
		gold := map[string]bool{}
		for _, i := range evalResolveAnchors(t, chunks, p.Query, p.Gold) {
			gold[chunks[i].ID] = true
			curated[chunks[i].ID] = append(curated[chunks[i].ID], p.Curated...)
		}
		tagQueries = append(tagQueries, evalQuery{Family: "tag-lookup", Text: fmt.Sprintf("%s %s", p.Curated[0], p.Curated[1]), Gold: gold})
	}
	return curated, tagQueries
}

package application

import (
	"context"
	"database/sql"
	"fmt"
	"hash/fnv"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/embeddings"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/library"
	"github.com/taimufuraiyaa/agent-memory/internal/locator"
	graphretrieval "github.com/taimufuraiyaa/agent-memory/internal/retrieval"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

// TestLexicalComparison compares the current retrieval streams with the BM25
// lexical stream on the repository documentation corpus. It is an evaluation
// harness, not a regression test, so it only runs when explicitly requested:
//
//	AGENT_MEMORY_LEXICAL_EVAL=1 AGENT_MEMORY_LEXICAL_EVAL_REPORT=<file> \
//	  go test ./internal/application -run TestLexicalComparison -v -timeout 30m
func TestLexicalComparison(t *testing.T) {
	if os.Getenv("AGENT_MEMORY_LEXICAL_EVAL") != "1" {
		t.Skip("set AGENT_MEMORY_LEXICAL_EVAL=1 to run the lexical comparison harness")
	}
	ctx := context.Background()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	chunks := evalLoadChunks(t, root)
	queries := evalBuildQueries(chunks)
	if len(chunks) < 200 || len(queries) < 50 {
		t.Fatalf("corpus too small for a comparison: %d chunks, %d queries", len(chunks), len(queries))
	}
	t.Logf("corpus: %d chunks, %d queries", len(chunks), len(queries))

	// The stand-in embedder is used unless a real local model directory is
	// supplied through AGENT_MEMORY_LEXICAL_EVAL_MODEL_DIR.
	var provider embeddings.Provider = newEvalEmbedder(384)
	standIn := true
	if dir := os.Getenv("AGENT_MEMORY_LEXICAL_EVAL_MODEL_DIR"); dir != "" {
		real, err := embeddings.NewProvider(dir)
		if err != nil {
			t.Fatalf("model provider: %v", err)
		}
		provider, standIn = real, false
	}
	dbPath := filepath.Join(t.TempDir(), "eval.db")
	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service := NewMemoryService(store, nil, engine.NewRetrievalEngine(engine.NewVectorSearcher(store, provider)))

	loadStart := time.Now()
	evalLoadMemories(t, ctx, store, provider, chunks)
	loadElapsed := time.Since(loadStart)

	floor := 0.05
	options := func(query string) engine.RetrievalOptions {
		o := engine.RetrievalOptions{Workspace: evalWorkspace, Query: query, TopK: 10, Mode: engine.ModeSearch}
		if standIn {
			o.Policy.MinSemanticScore = &floor
		}
		return o
	}
	hybrid := func(env, weight string) func(string) ([]string, error) {
		return func(query string) ([]string, error) {
			t.Setenv("AGENT_MEMORY_LEXICAL_MODE", env)
			t.Setenv("AGENT_MEMORY_LEXICAL_WEIGHT", weight)
			result, err := service.SearchHybrid(ctx, GraphSearchOptions{Retrieval: options(query), GraphMode: graphretrieval.GraphQueryBasic})
			if err != nil {
				return nil, err
			}
			ids := make([]string, 0, len(result.Hits))
			for _, hit := range result.Hits {
				ids = append(ids, hit.Memory.ID)
			}
			return ids, nil
		}
	}
	arms := []evalArm{
		{Name: "vector", Run: func(query string) ([]string, error) {
			result, err := service.Search(ctx, options(query))
			if err != nil {
				return nil, err
			}
			ids := make([]string, 0, len(result.Hits))
			for _, hit := range result.Hits {
				ids = append(ids, hit.Memory.ID)
			}
			return ids, nil
		}},
		{Name: "exact-term (locator, <=3 terms)", Run: func(query string) ([]string, error) {
			seed := graphTermSeedQuery(query)
			if seed == "" {
				return nil, nil
			}
			result, err := service.SearchTerms(ctx, TermSearchOptions{Workspace: evalWorkspace, Query: seed, Operator: TermOperatorOR, TopK: 10})
			if err != nil {
				return nil, err
			}
			ids := make([]string, 0, len(result.Hits))
			for _, hit := range result.Hits {
				ids = append(ids, hit.Memory.ID)
			}
			return ids, nil
		}},
		{Name: "BM25 only (FTS5)", Run: func(query string) ([]string, error) {
			matches, err := store.SearchMemoryLexical(ctx, sqlite.LexicalQuery{Workspace: evalWorkspace, Text: query, Limit: 10})
			if err != nil {
				return nil, err
			}
			ids := make([]string, 0, len(matches))
			for _, match := range matches {
				ids = append(ids, match.MemoryID)
			}
			return ids, nil
		}},
		{Name: "hybrid current (vector + exact-term, lexical off)", Run: hybrid("off", "")},
		{Name: "hybrid + BM25 candidates only (weight 0)", Run: hybrid("on", "0")},
		{Name: "hybrid + BM25 weighted (weight 0.25)", Run: hybrid("on", "0.25")},
		{Name: "hybrid + BM25 weighted (weight 0.5)", Run: hybrid("on", "0.5")},
	}

	// Warm query and embedding caches so latency compares retrieval work.
	for _, q := range queries {
		for _, arm := range arms {
			_, _ = arm.Run(q.Text)
		}
	}
	memoryResults := evalRunArms(t, arms, queries)

	passageResults := evalPassageArms(t, ctx, dbPath, chunks, queries)
	overhead := evalWriteOverhead(t, ctx, chunks)

	report := evalRenderReport(chunks, queries, loadElapsed, memoryResults, passageResults, overhead, provider.Name(), standIn)
	t.Log("\n" + report)
	if path := os.Getenv("AGENT_MEMORY_LEXICAL_EVAL_REPORT"); path != "" {
		if err := os.WriteFile(path, []byte(report), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

const evalWorkspace = "lexical-eval"

type evalChunk struct {
	ID      string
	Path    string
	Heading string
	Text    string
}

type evalQuery struct {
	Family string
	Text   string
	Gold   map[string]bool
}

type evalArm struct {
	Name string
	Run  func(query string) ([]string, error)
}

type evalFamilyMetrics struct {
	Queries int
	Hit1    int
	Hit5    int
	Hit10   int
	RRSum   float64
}

type evalArmResult struct {
	Name      string
	Overall   evalFamilyMetrics
	ByFamily  map[string]*evalFamilyMetrics
	P50ms     float64
	P95ms     float64
	Errors    int
	Latencies []float64
}

var (
	evalWordPattern       = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
	evalIdentifierPattern = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]{5,}`)
	evalHexPattern        = regexp.MustCompile(`^[0-9a-fA-F]{16,}$`)
)

func evalLoadChunks(t *testing.T, root string) []evalChunk {
	t.Helper()
	var files []string
	for _, dir := range []string{"docs", ".kiro/specs", ".agents/skills"} {
		_ = filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
				return nil
			}
			if strings.Contains(path, "bm25-lexical-candidates") {
				return nil
			}
			files = append(files, path)
			return nil
		})
	}
	for _, name := range []string{"README.md", "SECURITY.md"} {
		if _, err := os.Stat(filepath.Join(root, name)); err == nil {
			files = append(files, filepath.Join(root, name))
		}
	}
	sort.Strings(files)
	var chunks []evalChunk
	seen := map[string]bool{}
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		rel, _ := filepath.Rel(root, file)
		heading, buf, inFence := "", strings.Builder{}, false
		n := 0
		flush := func() {
			text := strings.TrimSpace(buf.String())
			buf.Reset()
			if len(text) < 140 || seen[text] {
				return
			}
			seen[text] = true
			n++
			chunks = append(chunks, evalChunk{ID: fmt.Sprintf("%s#%03d", rel, n), Path: rel, Heading: heading, Text: text})
		}
		for _, line := range strings.Split(string(raw), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "```") {
				inFence = !inFence
				continue
			}
			if !inFence && strings.HasPrefix(trimmed, "#") {
				flush()
				heading = strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
				continue
			}
			if trimmed == "" {
				if buf.Len() >= 420 {
					flush()
				}
				continue
			}
			if buf.Len() > 0 {
				buf.WriteString("\n")
			}
			buf.WriteString(trimmed)
			if buf.Len() >= 1400 {
				flush()
			}
		}
		flush()
	}
	return chunks
}

func evalTokens(text string) []string {
	return evalWordPattern.FindAllString(text, -1)
}

func evalIdentifierLike(token string) bool {
	if len(token) < 6 || evalHexPattern.MatchString(token) {
		return false
	}
	hasDigit, hasUnderscore, hasLowerUpper := false, strings.Contains(token, "_"), false
	for i, r := range token {
		if r >= '0' && r <= '9' {
			hasDigit = true
		}
		if i > 0 && r >= 'A' && r <= 'Z' && token[i-1] >= 'a' && token[i-1] <= 'z' {
			hasLowerUpper = true
		}
	}
	return hasDigit || hasUnderscore || hasLowerUpper
}

func evalBuildQueries(chunks []evalChunk) []evalQuery {
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
	containing := func(tokens []string) map[string]bool {
		gold := map[string]bool{}
		for i, c := range chunks {
			all := true
			for _, tok := range tokens {
				if !chunkWords[i][strings.ToLower(tok)] {
					all = false
					break
				}
			}
			if all {
				gold[c.ID] = true
			}
		}
		return gold
	}
	const perFamily = 150
	pick := func(all []evalQuery) []evalQuery {
		if len(all) <= perFamily {
			return all
		}
		out := make([]evalQuery, 0, perFamily)
		step := float64(len(all)) / float64(perFamily)
		for i := 0; i < perFamily; i++ {
			out = append(out, all[int(float64(i)*step)])
		}
		return out
	}

	var identifiers, keywords, headings []evalQuery
	usedIdent, usedKeywords := map[string]bool{}, map[string]bool{}
	for i, c := range chunks {
		// Identifier family: rare code-like token plus one common context word.
		var idents []string
		for _, tok := range evalIdentifierPattern.FindAllString(c.Text, -1) {
			low := strings.ToLower(tok)
			if evalIdentifierLike(tok) && df[low] >= 1 && df[low] <= 3 && !usedIdent[low] {
				idents = append(idents, tok)
			}
		}
		sort.Strings(idents)
		if len(idents) > 0 {
			tok := idents[0]
			usedIdent[strings.ToLower(tok)] = true
			context, bestDF := "", 0
			for w := range chunkWords[i] {
				if len(w) >= 6 && df[w] > bestDF && df[w] <= 120 && !evalIdentifierLike(w) {
					context, bestDF = w, df[w]
				}
			}
			text := tok
			if context != "" {
				text = context + " " + tok
			}
			identifiers = append(identifiers, evalQuery{Family: "identifier", Text: text, Gold: containing([]string{tok})})
		}
		// Keyword family: three distinctive prose words from the chunk.
		type cand struct {
			w  string
			df int
		}
		var cands []cand
		for w := range chunkWords[i] {
			if len(w) >= 7 && df[w] >= 1 && df[w] <= 6 && !evalIdentifierLike(w) && !strings.ContainsAny(w, "_0123456789") {
				cands = append(cands, cand{w, df[w]})
			}
		}
		if len(cands) >= 3 {
			sort.Slice(cands, func(a, b int) bool {
				if cands[a].df == cands[b].df {
					return cands[a].w < cands[b].w
				}
				return cands[a].df < cands[b].df
			})
			words := []string{cands[0].w, cands[1].w, cands[2].w}
			key := strings.Join(words, " ")
			if !usedKeywords[key] {
				usedKeywords[key] = true
				keywords = append(keywords, evalQuery{Family: "keywords", Text: key, Gold: containing(words)})
			}
		}
	}
	// Heading family: unique, multi-word headings as topical queries.
	headingFiles := map[string]map[string]bool{}
	for _, c := range chunks {
		if c.Heading == "" {
			continue
		}
		key := strings.ToLower(c.Heading)
		if headingFiles[key] == nil {
			headingFiles[key] = map[string]bool{}
		}
		headingFiles[key][c.Path] = true
	}
	doneHeading := map[string]bool{}
	for _, c := range chunks {
		key := strings.ToLower(c.Heading)
		if c.Heading == "" || doneHeading[key] || len(headingFiles[key]) != 1 || len(strings.Fields(c.Heading)) < 3 {
			continue
		}
		doneHeading[key] = true
		gold := map[string]bool{}
		for _, other := range chunks {
			if other.Path == c.Path && strings.EqualFold(other.Heading, c.Heading) {
				gold[other.ID] = true
			}
		}
		headings = append(headings, evalQuery{Family: "heading", Text: c.Heading, Gold: gold})
	}
	var out []evalQuery
	for _, family := range [][]evalQuery{identifiers, keywords, headings} {
		out = append(out, pick(family)...)
	}
	return out
}

func evalLoadMemories(t *testing.T, ctx context.Context, store *sqlite.Store, provider embeddings.Provider, chunks []evalChunk) {
	t.Helper()
	texts := make([]string, len(chunks))
	for i, c := range chunks {
		texts[i] = c.Text
	}
	vectors, err := provider.EmbedBatch(ctx, texts)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for i, c := range chunks {
		keywords, err := locator.Extract(locator.Input{Content: c.Text})
		if err != nil {
			keywords = nil
		}
		memory := core.MemoryEntry{
			ID: c.ID, Workspace: evalWorkspace, Type: core.SemanticMemory, Content: c.Text, Confidence: 0.9,
			StorageTier: core.TierVector, Source: core.MemorySource{Type: core.SourceUserInput},
			Keywords: keywords, CreatedAt: now, UpdatedAt: now,
		}
		if err := store.UpsertMemory(ctx, &memory); err != nil {
			t.Fatalf("load %s: %v", c.ID, err)
		}
		if err := store.UpsertMemoryVector(ctx, c.ID, evalWorkspace, provider.Name(), provider.ModelVersion(), vectors[i]); err != nil {
			t.Fatal(err)
		}
	}
}

// evalRunArms runs every arm for every query with the arm order rotated per
// query, so write-side drift (retrieval logs, access counters, checkpoints)
// affects all arms equally instead of favoring whichever arm runs first.
func evalRunArms(t *testing.T, arms []evalArm, queries []evalQuery) []evalArmResult {
	t.Helper()
	results := make([]evalArmResult, len(arms))
	for i, arm := range arms {
		results[i] = evalArmResult{Name: arm.Name, ByFamily: map[string]*evalFamilyMetrics{}}
	}
	for qi, q := range queries {
		for k := range arms {
			idx := (qi + k) % len(arms)
			start := time.Now()
			ids, err := arms[idx].Run(q.Text)
			results[idx].Latencies = append(results[idx].Latencies, float64(time.Since(start).Microseconds())/1000)
			if err != nil {
				results[idx].Errors++
			}
			evalScore(&results[idx].Overall, results[idx].ByFamily, q, ids)
		}
	}
	for i := range results {
		sort.Float64s(results[i].Latencies)
		results[i].P50ms = evalPercentile(results[i].Latencies, 0.50)
		results[i].P95ms = evalPercentile(results[i].Latencies, 0.95)
	}
	return results
}

func evalScore(overall *evalFamilyMetrics, byFamily map[string]*evalFamilyMetrics, q evalQuery, ids []string) {
	family := byFamily[q.Family]
	if family == nil {
		family = &evalFamilyMetrics{}
		byFamily[q.Family] = family
	}
	rank := 0
	for i, id := range ids {
		if i >= 10 {
			break
		}
		if q.Gold[id] {
			rank = i + 1
			break
		}
	}
	for _, m := range []*evalFamilyMetrics{overall, family} {
		m.Queries++
		if rank == 1 {
			m.Hit1++
		}
		if rank >= 1 && rank <= 5 {
			m.Hit5++
		}
		if rank >= 1 {
			m.Hit10++
			m.RRSum += 1 / float64(rank)
		}
	}
}

func evalPercentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	index := int(math.Ceil(p*float64(len(sorted)))) - 1
	return sorted[max(0, min(index, len(sorted)-1))]
}

// evalPassageArms compares the legacy substring-count scan with BM25 ranking
// through the same authorization-first passage search.
func evalPassageArms(t *testing.T, ctx context.Context, dbPath string, chunks []evalChunk, queries []evalQuery) []evalArmResult {
	t.Helper()
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	conn, err := raw.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatal(err)
	}
	passages := make([]library.Passage, 0, len(chunks))
	byID := map[string]library.Passage{}
	for _, c := range chunks {
		passage := library.Passage{ID: c.ID, EditionID: "edition-eval", SourceAssetID: "asset-eval", StructuralNodeID: "node-" + c.ID, Text: c.Text, Fingerprint: c.ID}
		passages = append(passages, passage)
		byID[c.ID] = passage
		if _, err := conn.ExecContext(ctx, `INSERT INTO source_passages (id, edition_id, source_asset_id, structural_node_id, text, locator_json, fingerprint) VALUES (?, ?, ?, ?, ?, '{}', ?)`,
			passage.ID, passage.EditionID, passage.SourceAssetID, passage.StructuralNodeID, passage.Text, passage.Fingerprint); err != nil {
			t.Fatalf("insert passage: %v", err)
		}
	}
	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	fake := &evalPassageStore{
		resources: []library.LibraryResourcePolicy{{LibraryID: "eval", ResourceType: library.ResourceEdition, ResourceID: "edition-eval",
			Policy: core.AccessPolicy{Version: "p1", Ownership: core.ResourceOwnership{Owner: core.Principal{ID: "org-1", Kind: core.PrincipalOrganization}, Visibility: core.VisibilityOrganization, OrganizationID: "org-1"}}}},
		passages: passages,
	}
	scope := core.AuthorizationScope{
		Principal: core.Principal{ID: "user-1", Kind: core.PrincipalUser}, OrganizationIDs: []string{"org-1"},
		Capabilities: []core.Capability{core.CapabilitySearchSource}, PolicyVersion: "membership-v1",
	}
	runner := func(search *graphretrieval.LexicalPassageSearch) func(string) ([]string, error) {
		return func(query string) ([]string, error) {
			results, err := search.Search(ctx, scope, query, 10)
			if err != nil {
				return nil, err
			}
			ids := make([]string, 0, len(results))
			for _, r := range results {
				ids = append(ids, r.Passage.ID)
			}
			return ids, nil
		}
	}
	ranker := &evalPassageRanker{store: store, byID: byID}
	return evalRunArms(t, []evalArm{
		{Name: "passages: legacy scan (substring counts)", Run: runner(graphretrieval.NewLexicalPassageSearch(fake))},
		{Name: "passages: BM25 (FTS5)", Run: runner(graphretrieval.NewLexicalPassageSearch(fake).WithRanker(ranker))},
	}, queries)
}

type evalPassageStore struct {
	resources []library.LibraryResourcePolicy
	passages  []library.Passage
}

func (s *evalPassageStore) ListLibraryResourcePolicies(context.Context, library.ResourceType) ([]library.LibraryResourcePolicy, error) {
	return s.resources, nil
}

func (s *evalPassageStore) ListPassagesForEditions(context.Context, []string) ([]library.Passage, error) {
	return s.passages, nil
}

type evalPassageRanker struct {
	store *sqlite.Store
	byID  map[string]library.Passage
}

func (r *evalPassageRanker) LexicalIndexAvailable(ctx context.Context) bool {
	return r.store.LexicalIndexAvailable(ctx)
}

func (r *evalPassageRanker) SearchPassagesLexical(ctx context.Context, editionIDs []string, text string, limit int) ([]library.PassageMatch, error) {
	return r.store.SearchPassagesLexical(ctx, editionIDs, text, limit)
}

func (r *evalPassageRanker) GetPassage(_ context.Context, id string) (library.Passage, error) {
	return r.byID[id], nil
}

type evalOverhead struct {
	WithTriggers    time.Duration
	WithoutTriggers time.Duration
	SizeWith        int64
	SizeWithout     int64
	Chunks          int
}

// evalWriteOverhead loads the corpus twice, with and without the lexical
// maintenance triggers, to isolate write cost and storage growth.
func evalWriteOverhead(t *testing.T, ctx context.Context, chunks []evalChunk) evalOverhead {
	t.Helper()
	load := func(dropTriggers bool) (time.Duration, int64) {
		path := filepath.Join(t.TempDir(), "overhead.db")
		store, err := sqlite.Open(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		if dropTriggers {
			rows, err := raw.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'trigger' AND name LIKE 'trg\_%lexical%' ESCAPE '\'`)
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			for rows.Next() {
				var name string
				_ = rows.Scan(&name)
				names = append(names, name)
			}
			_ = rows.Close()
			for _, name := range names {
				if _, err := raw.ExecContext(ctx, `DROP TRIGGER `+name); err != nil {
					t.Fatal(err)
				}
			}
		}
		now := time.Now().UTC()
		start := time.Now()
		for _, c := range chunks {
			memory := core.MemoryEntry{ID: c.ID, Workspace: evalWorkspace, Type: core.SemanticMemory, Content: c.Text, Confidence: 0.9,
				StorageTier: core.TierVector, Source: core.MemorySource{Type: core.SourceUserInput}, CreatedAt: now, UpdatedAt: now}
			if err := store.UpsertMemory(ctx, &memory); err != nil {
				t.Fatal(err)
			}
		}
		elapsed := time.Since(start)
		if _, err := raw.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
			t.Fatal(err)
		}
		_ = store.Close()
		_ = raw.Close()
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		return elapsed, info.Size()
	}
	var o evalOverhead
	o.Chunks = len(chunks)
	o.WithoutTriggers, o.SizeWithout = load(true)
	o.WithTriggers, o.SizeWith = load(false)
	return o
}

func evalRenderReport(chunks []evalChunk, queries []evalQuery, load time.Duration, memory, passages []evalArmResult, o evalOverhead, embedder string, standIn bool) string {
	var b strings.Builder
	families := map[string]int{}
	for _, q := range queries {
		families[q.Family]++
	}
	fmt.Fprintf(&b, "# BM25 lexical comparison report\n\n")
	fmt.Fprintf(&b, "Generated by `TestLexicalComparison` on %s.\n\n", time.Now().UTC().Format("2006-01-02"))
	fmt.Fprintf(&b, "## Setup\n\n")
	fmt.Fprintf(&b, "- Corpus: %d chunks cut from this repository's own Markdown (docs, specs, skills, README, SECURITY), excluding this spec.\n", len(chunks))
	fmt.Fprintf(&b, "- Queries: %d total (identifier %d, keywords %d, heading %d), generated deterministically from the corpus; gold is every chunk that contains the query tokens (identifier/keywords) or sits under the unique heading (heading). Top-k = 10.\n",
		len(queries), families["identifier"], families["keywords"], families["heading"])
	if standIn {
		fmt.Fprintf(&b, "- Embedder: deterministic hashed word/character-trigram stand-in (384 dimensions), **not** a semantic model. Semantic floor lowered to 0.05 for all arms. Vector-arm numbers therefore do not represent MiniLM-class paraphrase quality.\n")
	} else {
		fmt.Fprintf(&b, "- Embedder: real local model provider `%s`; default semantic floor.\n", embedder)
	}
	fmt.Fprintf(&b, "- Exact-term index is populated only by automatic extraction (`locator.Extract` on content); no curated explicit keywords exist in this corpus, so that arm measures the fallback extraction path, not curated keyword recall.\n")
	fmt.Fprintf(&b, "- Query construction favors lexical methods (queries reuse corpus tokens); there is no paraphrase family. Treat absolute numbers as a lexical-component comparison, not an end-to-end quality claim.\n")
	fmt.Fprintf(&b, "- Corpus load (memories, vectors, terms, triggers): %s.\n\n", load.Round(time.Millisecond))
	writeTable := func(title string, results []evalArmResult) {
		fmt.Fprintf(&b, "## %s\n\n", title)
		fmt.Fprintf(&b, "| Arm | R@1 | R@5 | R@10 | MRR@10 | p50 ms | p95 ms | errors |\n|---|---|---|---|---|---|---|---|\n")
		for _, r := range results {
			m := r.Overall
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %.3f | %.2f | %.2f | %d |\n", r.Name, evalPct(m.Hit1, m.Queries), evalPct(m.Hit5, m.Queries), evalPct(m.Hit10, m.Queries), m.RRSum/float64(max(1, m.Queries)), r.P50ms, r.P95ms, r.Errors)
		}
		b.WriteString("\n")
		for _, family := range []string{"identifier", "keywords", "heading"} {
			fmt.Fprintf(&b, "**%s queries**\n\n| Arm | R@1 | R@5 | R@10 | MRR@10 |\n|---|---|---|---|---|\n", family)
			for _, r := range results {
				m := r.ByFamily[family]
				if m == nil {
					continue
				}
				fmt.Fprintf(&b, "| %s | %s | %s | %s | %.3f |\n", r.Name, evalPct(m.Hit1, m.Queries), evalPct(m.Hit5, m.Queries), evalPct(m.Hit10, m.Queries), m.RRSum/float64(max(1, m.Queries)))
			}
			b.WriteString("\n")
		}
	}
	writeTable("Memory retrieval arms", memory)
	writeTable("Passage search arms", passages)
	fmt.Fprintf(&b, "## Write overhead and storage\n\n")
	perWithout := float64(o.WithoutTriggers.Microseconds()) / float64(max(1, o.Chunks)) / 1000
	perWith := float64(o.WithTriggers.Microseconds()) / float64(max(1, o.Chunks)) / 1000
	fmt.Fprintf(&b, "| | total | per memory | database file |\n|---|---|---|---|\n")
	fmt.Fprintf(&b, "| triggers removed | %s | %.3f ms | %.2f MiB |\n", o.WithoutTriggers.Round(time.Millisecond), perWithout, float64(o.SizeWithout)/(1<<20))
	fmt.Fprintf(&b, "| lexical index maintained | %s | %.3f ms | %.2f MiB |\n\n", o.WithTriggers.Round(time.Millisecond), perWith, float64(o.SizeWith)/(1<<20))
	growth := 0.0
	if o.SizeWithout > 0 {
		growth = float64(o.SizeWith-o.SizeWithout) / float64(o.SizeWithout) * 100
	}
	fmt.Fprintf(&b, "Index storage growth on this corpus: %.1f%% (memory rows only, no vectors).\n", growth)
	return b.String()
}

func evalPct(n, total int) string {
	if total == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.1f%%", float64(n)/float64(total)*100)
}

// evalEmbedder is a deterministic hashed bag of words and character trigrams.
type evalEmbedder struct{ dim int }

func newEvalEmbedder(dim int) *evalEmbedder { return &evalEmbedder{dim: dim} }

func (e *evalEmbedder) Name() string         { return "eval-hashed-ngrams" }
func (e *evalEmbedder) ModelVersion() string { return "eval-hashed-ngrams-v1" }
func (e *evalEmbedder) Dimension() int       { return e.dim }

func (e *evalEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	vector := make([]float64, e.dim)
	add := func(feature string, weight float64) {
		h := fnv.New32a()
		_, _ = h.Write([]byte(feature))
		sum := h.Sum32()
		index := int(sum % uint32(e.dim))
		sign := 1.0
		if (sum>>16)&1 == 1 {
			sign = -1
		}
		vector[index] += sign * weight
	}
	for _, token := range evalTokens(strings.ToLower(text)) {
		add("w:"+token, 1)
		padded := "^" + token + "$"
		runes := []rune(padded)
		for i := 0; i+3 <= len(runes); i++ {
			add("g:"+string(runes[i:i+3]), 0.35)
		}
	}
	var norm float64
	for _, v := range vector {
		norm += v * v
	}
	out := make([]float32, e.dim)
	if norm == 0 {
		out[0] = 1
		return out, nil
	}
	norm = math.Sqrt(norm)
	for i, v := range vector {
		out[i] = float32(v / norm)
	}
	return out, nil
}

func (e *evalEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, text := range texts {
		vector, err := e.Embed(ctx, text)
		if err != nil {
			return nil, err
		}
		out[i] = vector
	}
	return out, nil
}

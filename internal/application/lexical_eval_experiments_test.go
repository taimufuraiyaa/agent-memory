package application

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/embeddings"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	graphretrieval "github.com/taimufuraiyaa/agent-memory/internal/retrieval"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

// evalMemoryArms builds the memory retrieval arms for one service/store pair:
// the vector baseline, the exact-term stream, BM25 alone, the current hybrid
// and the BM25-augmented hybrid at every swept weight.
func evalMemoryArms(ctx context.Context, t *testing.T, service *MemoryService, store *sqlite.Store, standIn bool) []evalArm {
	t.Helper()
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
	}
	for _, w := range []string{"0.25", "0.5", "0.75", "1"} {
		arms = append(arms, evalArm{Name: fmt.Sprintf("hybrid + BM25 weighted (weight %s)", w), Run: hybrid("on", w)})
	}
	return arms
}

type evalExperiment struct {
	Title   string
	Results []evalArmResult
}

// evalCuratedExperiment compares the retrieval streams when gold memories carry
// explicit curated keywords against the same corpus with automatic extraction
// only. Two query sets are run on both databases: the hand-written paraphrases
// and "tag lookup" queries made of two curated terms.
func evalCuratedExperiment(t *testing.T, ctx context.Context, provider embeddings.Provider, chunks []evalChunk, vectors [][]float32, paraphrases []evalQuery, standIn bool) []evalExperiment {
	t.Helper()
	curated, tagQueries := evalCuratedKeywords(t, chunks)
	keep := []string{"vector", "exact-term", "BM25 only", "hybrid current", "weight 0.25"}
	filter := func(arms []evalArm) []evalArm {
		var out []evalArm
		for _, arm := range arms {
			for _, k := range keep {
				if strings.Contains(arm.Name, k) {
					out = append(out, arm)
					break
				}
			}
		}
		return out
	}
	var experiments []evalExperiment
	for _, variant := range []struct {
		name    string
		curated map[string][]string
	}{{"automatic extraction only", nil}, {"curated explicit keywords on gold chunks", curated}} {
		store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "curated.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close() })
		service := NewMemoryService(store, nil, engine.NewRetrievalEngine(engine.NewVectorSearcher(store, provider)))
		evalLoadMemories(t, ctx, store, provider, chunks, vectors, variant.curated)
		arms := filter(evalMemoryArms(ctx, t, service, store, standIn))
		for _, set := range []struct {
			name    string
			queries []evalQuery
		}{{"paraphrase queries", paraphrases}, {"tag-lookup queries (two curated terms)", tagQueries}} {
			for _, q := range set.queries {
				for _, arm := range arms {
					_, _ = arm.Run(q.Text) // warm caches
				}
			}
			experiments = append(experiments, evalExperiment{
				Title:   fmt.Sprintf("%s — %s", variant.name, set.name),
				Results: evalRunArms(t, arms, set.queries),
			})
		}
	}
	return experiments
}

type evalBloomRow struct {
	Mode      string
	Set       string
	Queries   int
	MeanUs    float64
	P50Us     float64
	P95Us     float64
	Negative  int
	Maybe     int
	Bypassed  int
	Shortcuts int
}

// evalBloomExperiment measures SearchTerms latency with the Bloom pre-check off,
// in shadow mode (probe, always do the lookup) and in gate mode (probe, skip
// the lookup on a definite miss). Two query sets are used: realistic queries
// whose seed terms usually exist, and terms that cannot exist in the corpus.
func evalBloomExperiment(t *testing.T, ctx context.Context, service *MemoryService, queries []evalQuery) []evalBloomRow {
	t.Helper()
	report, err := service.RebuildTermIndex(ctx, RebuildTermIndexOptions{Workspace: evalWorkspace})
	if err != nil {
		t.Fatalf("rebuild term index: %v", err)
	}
	t.Logf("term index: %d distinct terms, %d bits, %d hashes, est. fpp %.4f", report.DistinctTerms, report.BitCount, report.HashCount, report.EstimatedFPP)
	real := make([]string, 0, len(queries))
	for _, q := range queries {
		if seed := graphTermSeedQuery(q.Text); seed != "" {
			real = append(real, seed)
		}
	}
	var misses []string
	for i := 0; i < 200; i++ {
		misses = append(misses, fmt.Sprintf("zqx%dkvw qzj%dwxp", i, i*7+3))
	}
	sets := []struct {
		name  string
		terms []string
	}{{"realistic seed queries", real}, {"definite-miss queries", misses}}
	modes := []string{"off", "shadow", "gate"}
	const rounds = 5
	var rows []evalBloomRow
	for _, set := range sets {
		samples := map[string][]float64{}
		counts := map[string]*evalBloomRow{}
		for _, m := range modes {
			counts[m] = &evalBloomRow{Mode: m, Set: set.name, Queries: len(set.terms) * rounds}
		}
		for r := 0; r < rounds; r++ {
			for qi, seed := range set.terms {
				for k := range modes {
					mode := modes[(qi+r+k)%len(modes)]
					t.Setenv("AGENT_MEMORY_TERM_BLOOM_MODE", mode)
					start := time.Now()
					result, err := service.SearchTerms(ctx, TermSearchOptions{Workspace: evalWorkspace, Query: seed, Operator: TermOperatorOR, TopK: 10})
					elapsed := float64(time.Since(start).Nanoseconds()) / 1000
					if err != nil {
						t.Fatalf("term search (%s): %v", mode, err)
					}
					samples[mode] = append(samples[mode], elapsed)
					row := counts[mode]
					switch result.Prefilter.Decision {
					case "negative":
						row.Negative++
					case "maybe":
						row.Maybe++
					default:
						row.Bypassed++
					}
					if result.Prefilter.ShortCircuited {
						row.Shortcuts++
					}
				}
			}
		}
		for _, m := range modes {
			s := samples[m]
			sort.Float64s(s)
			var sum float64
			for _, v := range s {
				sum += v
			}
			row := counts[m]
			row.MeanUs = sum / float64(max(1, len(s)))
			row.P50Us = evalPercentile(s, 0.50)
			row.P95Us = evalPercentile(s, 0.95)
			rows = append(rows, *row)
		}
	}
	return rows
}

func evalRenderExtras(embed time.Duration, experiments []evalExperiment, bloom []evalBloomRow) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n## Embedding cost\n\nEmbedding the whole corpus with the configured provider took %s.\n", embed.Round(time.Millisecond))
	fmt.Fprintf(&b, "\n## Curated exact-term experiment\n\n")
	fmt.Fprintf(&b, "Same corpus and vectors, loaded twice: once with automatic term extraction only, once with explicit curated keywords (core.MemoryTerm source `explicit`) added to the gold chunks of the paraphrase family. Exact-term queries use the production seed (first three words of at least three characters) with the OR operator.\n\n")
	for _, e := range experiments {
		fmt.Fprintf(&b, "**%s**\n\n| Arm | R@1 | R@5 | R@10 | MRR@10 | p50 ms | p95 ms |\n|---|---|---|---|---|---|---|\n", e.Title)
		for _, r := range e.Results {
			var m evalFamilyMetrics
			for _, fam := range r.ByFamily {
				m = *fam
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %.3f | %.2f | %.2f |\n", r.Name, evalPct(m.Hit1, m.Queries), evalPct(m.Hit5, m.Queries), evalPct(m.Hit10, m.Queries), m.RRSum/float64(max(1, m.Queries)), r.P50ms, r.P95ms)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "## Term Bloom pre-check cost\n\n")
	fmt.Fprintf(&b, "`SearchTerms` latency per call in microseconds, five rounds, mode order rotated per query. `shadow` probes and always runs the indexed lookup; `gate` skips the lookup on a definite miss.\n\n")
	fmt.Fprintf(&b, "| Query set | Mode | calls | mean µs | p50 µs | p95 µs | negative | maybe | bypassed | short-circuited |\n|---|---|---|---|---|---|---|---|---|---|\n")
	for _, r := range bloom {
		fmt.Fprintf(&b, "| %s | %s | %d | %.1f | %.1f | %.1f | %d | %d | %d | %d |\n", r.Set, r.Mode, r.Queries, r.MeanUs, r.P50Us, r.P95Us, r.Negative, r.Maybe, r.Bypassed, r.Shortcuts)
	}
	b.WriteString("\n")
	return b.String()
}

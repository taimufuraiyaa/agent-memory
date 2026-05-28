package sqlite

import (
	"context"
	"encoding/json"
	"time"
)

type BenchmarkClusterSummary struct {
	ClusterID         string  `json:"cluster_id"`
	ClusterTitle      string  `json:"cluster_title"`
	Cases             int     `json:"cases"`
	Precision         float64 `json:"precision"`
	Recall            float64 `json:"recall"`
	GoldRecall        float64 `json:"gold_recall"`
	KeywordCoverage   float64 `json:"keyword_coverage"`
	NDCG              float64 `json:"ndcg"`
	F1                float64 `json:"f1"`
	TokenEfficiency   float64 `json:"token_efficiency"`
	BaselineTokens    int     `json:"baseline_tokens"`
	ReturnedTokens    int     `json:"returned_tokens"`
	SavedTokens       int     `json:"saved_tokens"`
	CostWithMemory    float64 `json:"cost_with_memory"`
	CostWithoutMemory float64 `json:"cost_without_memory"`
	CostSaved         float64 `json:"cost_saved"`
	CostSavedPct      float64 `json:"cost_saved_pct"`
	CombinedScore     float64 `json:"combined_score"`
	Verdict           string  `json:"verdict"`
}

type BenchmarkRun struct {
	ID                int64                     `json:"id"`
	Workspace         string                    `json:"workspace"`
	RunID             string                    `json:"run_id"`
	SeedCount         int                       `json:"seed_count"`
	CaseCount         int                       `json:"case_count"`
	CaseLimit         int                       `json:"case_limit"`
	TopK              int                       `json:"top_k"`
	Budget            int                       `json:"budget"`
	SeedDurationMs    int                       `json:"seed_duration_ms"`
	OnDurationMs      int                       `json:"on_duration_ms"`
	OffDurationMs     int                       `json:"off_duration_ms"`
	Precision         float64                   `json:"precision"`
	Recall            float64                   `json:"recall"`
	GoldRecall        float64                   `json:"gold_recall"`
	KeywordCoverage   float64                   `json:"keyword_coverage"`
	NDCG              float64                   `json:"ndcg"`
	F1                float64                   `json:"f1"`
	TokenEfficiency   float64                   `json:"token_efficiency"`
	BaselineTokens    int                       `json:"baseline_tokens"`
	ReturnedTokens    int                       `json:"returned_tokens"`
	SavedTokens       int                       `json:"saved_tokens"`
	CostWithMemory    float64                   `json:"cost_with_memory"`
	CostWithoutMemory float64                   `json:"cost_without_memory"`
	CostSaved         float64                   `json:"cost_saved"`
	CostSavedPct      float64                   `json:"cost_saved_pct"`
	CombinedScore     float64                   `json:"combined_score"`
	Verdict           string                    `json:"verdict"`
	OffCases          int                       `json:"off_cases"`
	OffDisabledCount  int                       `json:"off_disabled_count"`
	OffAllDisabled    bool                      `json:"off_all_disabled"`
	OffReturnedTokens int                       `json:"off_returned_tokens"`
	OffBaselineTokens int                       `json:"off_baseline_tokens"`
	OffSavedTokens    int                       `json:"off_saved_tokens"`
	GeneratorManifest map[string]any            `json:"generator_manifest,omitempty"`
	RunManifest       map[string]any            `json:"run_manifest,omitempty"`
	Clusters          []BenchmarkClusterSummary `json:"clusters,omitempty"`
	CreatedAt         string                    `json:"created_at"`
}

func mustJSON(value any, fallback string) string {
	payload, err := json.Marshal(value)
	if err != nil || len(payload) == 0 {
		return fallback
	}
	return string(payload)
}

func decodeJSONObject(raw string) map[string]any {
	if raw == "" {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return map[string]any{}
	}
	return out
}

func decodeBenchmarkClusters(raw string) []BenchmarkClusterSummary {
	if raw == "" {
		return []BenchmarkClusterSummary{}
	}
	var out []BenchmarkClusterSummary
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return []BenchmarkClusterSummary{}
	}
	return out
}

func (s *Store) InsertBenchmarkRun(ctx context.Context, in BenchmarkRun) (BenchmarkRun, error) {
	createdAt := in.CreatedAt
	if createdAt == "" {
		createdAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO benchmark_runs (
  workspace, run_id, seed_count, case_count, case_limit, top_k, budget,
  seed_duration_ms, on_duration_ms, off_duration_ms,
  precision, recall, gold_recall, keyword_coverage, ndcg, f1, token_efficiency,
  baseline_tokens, returned_tokens, saved_tokens,
  cost_with_memory, cost_without_memory, cost_saved, cost_saved_pct,
  combined_score, verdict,
  off_cases, off_disabled_count, off_all_disabled, off_returned_tokens, off_baseline_tokens, off_saved_tokens,
  generator_manifest_json, run_manifest_json, clusters_json, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(workspace, run_id) DO UPDATE SET
  seed_count = excluded.seed_count,
  case_count = excluded.case_count,
  case_limit = excluded.case_limit,
  top_k = excluded.top_k,
  budget = excluded.budget,
  seed_duration_ms = excluded.seed_duration_ms,
  on_duration_ms = excluded.on_duration_ms,
  off_duration_ms = excluded.off_duration_ms,
  precision = excluded.precision,
  recall = excluded.recall,
  gold_recall = excluded.gold_recall,
  keyword_coverage = excluded.keyword_coverage,
  ndcg = excluded.ndcg,
  f1 = excluded.f1,
  token_efficiency = excluded.token_efficiency,
  baseline_tokens = excluded.baseline_tokens,
  returned_tokens = excluded.returned_tokens,
  saved_tokens = excluded.saved_tokens,
  cost_with_memory = excluded.cost_with_memory,
  cost_without_memory = excluded.cost_without_memory,
  cost_saved = excluded.cost_saved,
  cost_saved_pct = excluded.cost_saved_pct,
  combined_score = excluded.combined_score,
  verdict = excluded.verdict,
  off_cases = excluded.off_cases,
  off_disabled_count = excluded.off_disabled_count,
  off_all_disabled = excluded.off_all_disabled,
  off_returned_tokens = excluded.off_returned_tokens,
  off_baseline_tokens = excluded.off_baseline_tokens,
  off_saved_tokens = excluded.off_saved_tokens,
  generator_manifest_json = excluded.generator_manifest_json,
  run_manifest_json = excluded.run_manifest_json,
  clusters_json = excluded.clusters_json,
  created_at = excluded.created_at
`,
		in.Workspace,
		in.RunID,
		in.SeedCount,
		in.CaseCount,
		in.CaseLimit,
		in.TopK,
		in.Budget,
		in.SeedDurationMs,
		in.OnDurationMs,
		in.OffDurationMs,
		in.Precision,
		in.Recall,
		in.GoldRecall,
		in.KeywordCoverage,
		in.NDCG,
		in.F1,
		in.TokenEfficiency,
		in.BaselineTokens,
		in.ReturnedTokens,
		in.SavedTokens,
		in.CostWithMemory,
		in.CostWithoutMemory,
		in.CostSaved,
		in.CostSavedPct,
		in.CombinedScore,
		in.Verdict,
		in.OffCases,
		in.OffDisabledCount,
		boolToInt(in.OffAllDisabled),
		in.OffReturnedTokens,
		in.OffBaselineTokens,
		in.OffSavedTokens,
		mustJSON(in.GeneratorManifest, "{}"),
		mustJSON(in.RunManifest, "{}"),
		mustJSON(in.Clusters, "[]"),
		createdAt,
	)
	if err != nil {
		return BenchmarkRun{}, err
	}

	row := s.db.QueryRowContext(ctx, `
SELECT
  id, workspace, run_id, seed_count, case_count, case_limit, top_k, budget,
  seed_duration_ms, on_duration_ms, off_duration_ms,
  precision, recall, gold_recall, keyword_coverage, ndcg, f1, token_efficiency,
  baseline_tokens, returned_tokens, saved_tokens,
  cost_with_memory, cost_without_memory, cost_saved, cost_saved_pct,
  combined_score, verdict,
  off_cases, off_disabled_count, off_all_disabled, off_returned_tokens, off_baseline_tokens, off_saved_tokens,
  generator_manifest_json, run_manifest_json, clusters_json, created_at
FROM benchmark_runs
WHERE workspace = ? AND run_id = ?`,
		in.Workspace, in.RunID,
	)
	return scanBenchmarkRun(row)
}

func (s *Store) ListBenchmarkRuns(ctx context.Context, workspace string, limit int) ([]BenchmarkRun, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT
  id, workspace, run_id, seed_count, case_count, case_limit, top_k, budget,
  seed_duration_ms, on_duration_ms, off_duration_ms,
  precision, recall, gold_recall, keyword_coverage, ndcg, f1, token_efficiency,
  baseline_tokens, returned_tokens, saved_tokens,
  cost_with_memory, cost_without_memory, cost_saved, cost_saved_pct,
  combined_score, verdict,
  off_cases, off_disabled_count, off_all_disabled, off_returned_tokens, off_baseline_tokens, off_saved_tokens,
  generator_manifest_json, run_manifest_json, clusters_json, created_at
FROM benchmark_runs
WHERE workspace = ?
ORDER BY created_at DESC, id DESC
LIMIT ?`,
		workspace, limit,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := []BenchmarkRun{}
	for rows.Next() {
		run, err := scanBenchmarkRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

type benchmarkScanner interface {
	Scan(dest ...any) error
}

func scanBenchmarkRun(scanner benchmarkScanner) (BenchmarkRun, error) {
	var run BenchmarkRun
	var offAllDisabled int
	var generatorRaw string
	var manifestRaw string
	var clustersRaw string
	err := scanner.Scan(
		&run.ID,
		&run.Workspace,
		&run.RunID,
		&run.SeedCount,
		&run.CaseCount,
		&run.CaseLimit,
		&run.TopK,
		&run.Budget,
		&run.SeedDurationMs,
		&run.OnDurationMs,
		&run.OffDurationMs,
		&run.Precision,
		&run.Recall,
		&run.GoldRecall,
		&run.KeywordCoverage,
		&run.NDCG,
		&run.F1,
		&run.TokenEfficiency,
		&run.BaselineTokens,
		&run.ReturnedTokens,
		&run.SavedTokens,
		&run.CostWithMemory,
		&run.CostWithoutMemory,
		&run.CostSaved,
		&run.CostSavedPct,
		&run.CombinedScore,
		&run.Verdict,
		&run.OffCases,
		&run.OffDisabledCount,
		&offAllDisabled,
		&run.OffReturnedTokens,
		&run.OffBaselineTokens,
		&run.OffSavedTokens,
		&generatorRaw,
		&manifestRaw,
		&clustersRaw,
		&run.CreatedAt,
	)
	if err != nil {
		return BenchmarkRun{}, err
	}
	run.OffAllDisabled = offAllDisabled != 0
	run.GeneratorManifest = decodeJSONObject(generatorRaw)
	run.RunManifest = decodeJSONObject(manifestRaw)
	run.Clusters = decodeBenchmarkClusters(clustersRaw)
	return run, nil
}

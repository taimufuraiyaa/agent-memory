package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// BenchmarkMetrics accumulates per-retrieval instrumentation counters.
// Counters are incremented atomically during retrieval and flushed
// to disk after each recall when AGENT_MEMORY_BENCHMARK_METRICS=1.
type BenchmarkMetrics struct {
	CandidateCount    int64 `json:"candidate_count"`
	VectorSearchCount int64 `json:"vector_search_count"`
	BloomProbeCount   int64 `json:"bloom_probe_count"`
	CacheHitCount     int64 `json:"cache_hit_count"`
}

// BenchmarkMetricsSnapshot is a point-in-time copy for export.
type BenchmarkMetricsSnapshot struct {
	CandidateCount    int64  `json:"candidate_count"`
	VectorSearchCount int64  `json:"vector_search_count"`
	BloomProbeCount   int64  `json:"bloom_probe_count"`
	CacheHitCount     int64  `json:"cache_hit_count"`
	SchemaVersion     string `json:"schema_version"`
}

var (
	benchmarkMetrics   BenchmarkMetrics
	benchmarkMetricsMu sync.Mutex
)

// AddCandidateCount increments the candidate counter.
func AddCandidateCount(n int64) {
	if !benchmarkMetricsEnabled() {
		return
	}
	benchmarkMetricsMu.Lock()
	benchmarkMetrics.CandidateCount += n
	benchmarkMetricsMu.Unlock()
}

// AddVectorSearchCount increments the vector search call counter.
func AddVectorSearchCount(n int64) {
	if !benchmarkMetricsEnabled() {
		return
	}
	benchmarkMetricsMu.Lock()
	benchmarkMetrics.VectorSearchCount += n
	benchmarkMetricsMu.Unlock()
}

// AddBloomProbeCount increments the Bloom filter probe counter.
func AddBloomProbeCount(n int64) {
	if !benchmarkMetricsEnabled() {
		return
	}
	benchmarkMetricsMu.Lock()
	benchmarkMetrics.BloomProbeCount += n
	benchmarkMetricsMu.Unlock()
}

// AddCacheHitCount increments the query-cache hit counter.
func AddCacheHitCount(n int64) {
	if !benchmarkMetricsEnabled() {
		return
	}
	benchmarkMetricsMu.Lock()
	benchmarkMetrics.CacheHitCount += n
	benchmarkMetricsMu.Unlock()
}

// SnapshotBenchmarkMetrics returns a point-in-time copy of all counters.
func SnapshotBenchmarkMetrics() BenchmarkMetricsSnapshot {
	benchmarkMetricsMu.Lock()
	defer benchmarkMetricsMu.Unlock()
	return BenchmarkMetricsSnapshot{
		CandidateCount:    benchmarkMetrics.CandidateCount,
		VectorSearchCount: benchmarkMetrics.VectorSearchCount,
		BloomProbeCount:   benchmarkMetrics.BloomProbeCount,
		CacheHitCount:     benchmarkMetrics.CacheHitCount,
		SchemaVersion:     "benchmark-metrics-v1",
	}
}

// ResetBenchmarkMetrics zeros all counters.
func ResetBenchmarkMetrics() {
	benchmarkMetricsMu.Lock()
	defer benchmarkMetricsMu.Unlock()
	benchmarkMetrics = BenchmarkMetrics{}
}

// FlushBenchmarkMetrics writes the current snapshot to benchmark_metrics.json
// in the working directory. The file is overwritten on each flush.
// If AGENT_MEMORY_BENCHMARK_METRICS is not "1", this is a no-op.
func FlushBenchmarkMetrics() error {
	if !benchmarkMetricsEnabled() {
		return nil
	}
	snapshot := SnapshotBenchmarkMetrics()
	// Determine output path: working directory or AGENT_MEMORY_BENCHMARK_METRICS_FILE
	path := strings.TrimSpace(os.Getenv("AGENT_MEMORY_BENCHMARK_METRICS_FILE"))
	if path == "" {
		cwd, err := os.Getwd()
		if err != nil {
			cwd = "."
		}
		path = filepath.Join(cwd, "benchmark_metrics.json")
	}
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

func benchmarkMetricsEnabled() bool {
	return strings.TrimSpace(os.Getenv("AGENT_MEMORY_BENCHMARK_METRICS")) == "1"
}

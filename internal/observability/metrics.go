// Package observability provides metrics, logging, and tracing for agent-memory.
package observability

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// MetricsRegistry holds all Prometheus metrics for agent-memory.
type MetricsRegistry struct {
	// Write pipeline metrics
	WriteTotal      *prometheus.CounterVec
	WriteDuration   *prometheus.HistogramVec
	WriteErrors     *prometheus.CounterVec
	WriteBytes      *prometheus.HistogramVec
	
	// Write embedding metrics (eager write-time embedding)
	WriteEmbeddingDuration *prometheus.HistogramVec
	WriteEmbeddingErrors   *prometheus.CounterVec
	WriteEmbeddingSuccess  *prometheus.CounterVec
	
	// Retrieval metrics
	RetrievalTotal      *prometheus.CounterVec
	RetrievalDuration   *prometheus.HistogramVec
	RetrievalHits       *prometheus.HistogramVec
	RetrievalErrors     *prometheus.CounterVec
	
	// Storage metrics
	StorageOperations *prometheus.CounterVec
	StorageDuration   *prometheus.HistogramVec
	StorageErrors     *prometheus.CounterVec
	DBConnections     prometheus.Gauge
	DBSize            *prometheus.GaugeVec
	
	// Embedding metrics
	EmbeddingTotal    *prometheus.CounterVec
	EmbeddingDuration *prometheus.HistogramVec
	EmbeddingErrors   *prometheus.CounterVec
	EmbeddingBatchSize *prometheus.HistogramVec
	
	// Token metrics
	TokensUsed       *prometheus.CounterVec
	TokensSaved      *prometheus.CounterVec
	TokenBudgetUsage *prometheus.HistogramVec
	
	// Memory metrics
	MemoryCount       *prometheus.GaugeVec
	MemoryAccessCount *prometheus.CounterVec
	DecayScoreAvg     *prometheus.GaugeVec
	
	// Cache metrics
	CacheHits   *prometheus.CounterVec
	CacheMisses *prometheus.CounterVec
	CacheSize   *prometheus.GaugeVec

	// Lifecycle metrics
	LifecycleDuration *prometheus.HistogramVec

	// Cold tier compression metrics (Task 4.3.3)
	ColdSummarizationTotal    *prometheus.CounterVec
	ColdSummarizationDuration *prometheus.HistogramVec
	ColdCompressionRatio      *prometheus.HistogramVec
	ColdCompressionOrigBytes  *prometheus.HistogramVec

	// API metrics
	HTTPRequestsTotal    *prometheus.CounterVec
	HTTPRequestDuration  *prometheus.HistogramVec
	HTTPRequestSize      *prometheus.HistogramVec
	HTTPResponseSize     *prometheus.HistogramVec
	HTTPRequestsInFlight prometheus.Gauge
}

var (
	registry *MetricsRegistry
	once     sync.Once
)

// GetRegistry returns the global metrics registry, initializing it if needed.
func GetRegistry() *MetricsRegistry {
	once.Do(func() {
		registry = newMetricsRegistry()
	})
	return registry
}

// newMetricsRegistry creates and registers all Prometheus metrics.
func newMetricsRegistry() *MetricsRegistry {
	return &MetricsRegistry{
		// Write pipeline metrics
		WriteTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "agent_memory_write_total",
				Help: "Total number of memory write operations",
			},
			[]string{"workspace", "type", "status"},
		),
		WriteDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "agent_memory_write_duration_seconds",
				Help:    "Duration of memory write operations",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"workspace", "type"},
		),
		WriteErrors: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "agent_memory_write_errors_total",
				Help: "Total number of memory write errors",
			},
			[]string{"workspace", "type", "error_type"},
		),
		WriteBytes: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "agent_memory_write_bytes",
				Help:    "Size of memory write operations in bytes",
				Buckets: []float64{100, 500, 1000, 5000, 10000, 50000, 100000},
			},
			[]string{"workspace", "type"},
		),
		WriteEmbeddingDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "agent_memory_write_embedding_duration_seconds",
				Help:    "Duration of eager write-time embedding operations",
				Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0},
			},
			[]string{"workspace", "provider"},
		),
		WriteEmbeddingErrors: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "agent_memory_write_embedding_errors_total",
				Help: "Total number of eager write-time embedding errors",
			},
			[]string{"workspace", "provider", "error_type"},
		),
		WriteEmbeddingSuccess: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "agent_memory_write_embedding_success_total",
				Help: "Total number of successful eager write-time embedding operations",
			},
			[]string{"workspace", "provider"},
		),
		
		// Retrieval metrics
		RetrievalTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "agent_memory_retrieval_total",
				Help: "Total number of memory retrieval operations",
			},
			[]string{"workspace", "mode", "status"},
		),
		RetrievalDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "agent_memory_retrieval_duration_seconds",
				Help:    "Duration of memory retrieval operations",
				Buckets: []float64{0.01, 0.05, 0.1, 0.5, 1, 2, 5, 10},
			},
			[]string{"workspace", "mode"},
		),
		RetrievalHits: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "agent_memory_retrieval_hits",
				Help:    "Number of hits returned by retrieval",
				Buckets: []float64{0, 1, 5, 10, 20, 50, 100, 200},
			},
			[]string{"workspace", "mode"},
		),
		RetrievalErrors: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "agent_memory_retrieval_errors_total",
				Help: "Total number of retrieval errors",
			},
			[]string{"workspace", "mode", "error_type"},
		),
		
		// Storage metrics
		StorageOperations: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "agent_memory_storage_operations_total",
				Help: "Total number of storage operations",
			},
			[]string{"workspace", "operation", "status"},
		),
		StorageDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "agent_memory_storage_duration_seconds",
				Help:    "Duration of storage operations",
				Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1},
			},
			[]string{"workspace", "operation"},
		),
		StorageErrors: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "agent_memory_storage_errors_total",
				Help: "Total number of storage errors",
			},
			[]string{"workspace", "operation", "error_type"},
		),
		DBConnections: promauto.NewGauge(
			prometheus.GaugeOpts{
				Name: "agent_memory_db_connections",
				Help: "Current number of database connections",
			},
		),
		DBSize: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "agent_memory_db_size_bytes",
				Help: "Database size in bytes",
			},
			[]string{"workspace"},
		),
		
		// Embedding metrics
		EmbeddingTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "agent_memory_embedding_total",
				Help: "Total number of embedding operations",
			},
			[]string{"provider", "status"},
		),
		EmbeddingDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "agent_memory_embedding_duration_seconds",
				Help:    "Duration of embedding operations",
				Buckets: []float64{0.01, 0.05, 0.1, 0.5, 1, 2, 5},
			},
			[]string{"provider"},
		),
		EmbeddingErrors: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "agent_memory_embedding_errors_total",
				Help: "Total number of embedding errors",
			},
			[]string{"provider", "error_type"},
		),
		EmbeddingBatchSize: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "agent_memory_embedding_batch_size",
				Help:    "Batch size for embedding operations",
				Buckets: []float64{1, 5, 10, 20, 50, 100},
			},
			[]string{"provider"},
		),
		
		// Token metrics
		TokensUsed: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "agent_memory_tokens_used_total",
				Help: "Total number of tokens used",
			},
			[]string{"workspace", "operation"},
		),
		TokensSaved: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "agent_memory_tokens_saved_total",
				Help: "Total number of tokens saved",
			},
			[]string{"workspace", "operation"},
		),
		TokenBudgetUsage: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "agent_memory_token_budget_usage_ratio",
				Help:    "Ratio of tokens used vs budget",
				Buckets: []float64{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 1.0},
			},
			[]string{"workspace"},
		),
		
		// Memory metrics
		MemoryCount: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "agent_memory_count",
				Help: "Current number of memories",
			},
			[]string{"workspace", "type", "tier"},
		),
		MemoryAccessCount: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "agent_memory_access_total",
				Help: "Total number of memory accesses",
			},
			[]string{"workspace"},
		),
		DecayScoreAvg: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "agent_memory_decay_score_avg",
				Help: "Average decay score",
			},
			[]string{"workspace"},
		),
		
		// API metrics
		HTTPRequestsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "agent_memory_http_requests_total",
				Help: "Total number of HTTP requests",
			},
			[]string{"method", "path", "status"},
		),
		HTTPRequestDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "agent_memory_http_request_duration_seconds",
				Help:    "Duration of HTTP requests",
				Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 2, 5},
			},
			[]string{"method", "path"},
		),
		HTTPRequestSize: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "agent_memory_http_request_size_bytes",
				Help:    "Size of HTTP requests in bytes",
				Buckets: []float64{100, 1000, 10000, 100000, 1000000},
			},
			[]string{"method", "path"},
		),
		HTTPResponseSize: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "agent_memory_http_response_size_bytes",
				Help:    "Size of HTTP responses in bytes",
				Buckets: []float64{100, 1000, 10000, 100000, 1000000},
			},
			[]string{"method", "path"},
		),
		HTTPRequestsInFlight: promauto.NewGauge(
			prometheus.GaugeOpts{
				Name: "agent_memory_http_requests_in_flight",
				Help: "Current number of HTTP requests being processed",
			}),

		// Cache metrics
		CacheHits: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "agent_memory_cache_hits_total",
				Help: "Total number of query cache hits",
			},
			[]string{"cache_type"},
		),
		CacheMisses: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "agent_memory_cache_misses_total",
				Help: "Total number of query cache misses",
			},
			[]string{"cache_type"},
		),
		CacheSize: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "agent_memory_cache_size_entries",
				Help: "Current number of entries in query cache",
			},
			[]string{"cache_type"},
		),
		LifecycleDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "agent_memory_lifecycle_run_duration_seconds",
				Help:    "Duration of background lifecycle maintenance runs",
				Buckets: []float64{0.1, 0.5, 1.0, 2.5, 5.0, 10.0, 30.0, 60.0},
			},
			[]string{"workspace", "status"},
		),

		// Cold tier compression metrics (Task 4.3.3)
		ColdSummarizationTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "agent_memory_cold_summarization_total",
				Help: "Total number of cold-tier summarization operations",
			},
			[]string{"workspace", "method", "status"},
		),
		ColdSummarizationDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "agent_memory_cold_summarization_duration_seconds",
				Help:    "Duration of cold-tier summarization operations",
				Buckets: []float64{0.05, 0.1, 0.5, 1.0, 2.0, 5.0, 10.0, 30.0},
			},
			[]string{"workspace", "method"},
		),
		ColdCompressionRatio: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "agent_memory_cold_compression_ratio",
				Help:    "Ratio of summary size to original size (lower is better compression)",
				Buckets: []float64{0.05, 0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 1.0},
			},
			[]string{"workspace", "method"},
		),
		ColdCompressionOrigBytes: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "agent_memory_cold_compression_original_bytes",
				Help:    "Original content size in bytes before cold-tier summarization",
				Buckets: []float64{100, 500, 1000, 2000, 5000, 10000, 50000},
			},
			[]string{"workspace"},
		),
	}
}

// Timer is a helper for timing operations.
type Timer struct {
	start time.Time
}

// NewTimer creates a new timer.
func NewTimer() *Timer {
	return &Timer{start: time.Now()}
}

// ObserveDuration observes the duration since the timer was created.
func (t *Timer) ObserveDuration(observer prometheus.Observer) {
	observer.Observe(time.Since(t.start).Seconds())
}

// Duration returns the elapsed time since the timer was created.
func (t *Timer) Duration() time.Duration {
	return time.Since(t.start)
}

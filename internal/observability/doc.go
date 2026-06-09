// Package observability provides metrics, logging, and tracing capabilities for agent-memory.
//
// # Overview
//
// This package provides comprehensive observability through three pillars:
//   - Prometheus metrics for quantitative monitoring
//   - Structured logging for qualitative insights
//   - OpenTelemetry tracing for distributed request tracking
//
// # Metrics
//
// Prometheus metrics are provided via GetRegistry() and cover:
//   - Write pipeline: operations, duration, errors, bytes
//   - Retrieval: operations, duration, hits, errors
//   - Storage: operations, duration, errors, connections, size
//   - Embeddings: operations, duration, errors, batch size
//   - Tokens: used, saved, budget usage
//   - Memory: count, access, decay scores
//   - HTTP API: requests, duration, size, in-flight
//
// Example usage:
//
//	metrics := observability.GetRegistry()
//	timer := observability.NewTimer()
//
//	// Perform operation
//	err := writeMemory(ctx, memory)
//
//	// Record metrics
//	metrics.WriteTotal.WithLabelValues(workspace, memType, status).Inc()
//	timer.ObserveDuration(metrics.WriteDuration.WithLabelValues(workspace, memType))
//
// # Logging
//
// Structured logging is provided via GetLogger() using Go's slog package:
//
//	logger := observability.GetLogger()
//	logger = logger.WithWorkspace(workspace).WithComponent("engine")
//
//	logger.InfoWithFields("memory written", map[string]any{
//	    "memory_id": id,
//	    "type": memType,
//	    "duration": duration,
//	})
//
// Log levels and formats can be configured:
//
//	logger := observability.NewLogger(
//	    observability.LogLevelInfo,
//	    observability.LogFormatJSON,
//	)
//	observability.SetLogger(logger)
//
// # Tracing
//
// OpenTelemetry tracing is provided via InitTracing() and Tracer():
//
//	config := observability.TracingConfig{
//	    Enabled: true,
//	    ServiceName: "agent-memory",
//	    Environment: "production",
//	    SampleRate: 0.1,
//	}
//	shutdown, err := observability.InitTracing(config)
//	defer shutdown(context.Background())
//
//	// Trace an operation
//	ctx, span := observability.StartSpan(ctx, "write-memory")
//	defer span.End()
//
//	observability.SetSpanAttributes(ctx,
//	    observability.WorkspaceAttr(workspace),
//	    observability.MemoryTypeAttr(string(memType)),
//	)
//
// Or use the convenience function:
//
//	err := observability.TraceOperation(ctx, "retrieve-memories",
//	    func(ctx context.Context) error {
//	        return retrieval.Retrieve(ctx, opts)
//	    },
//	    observability.WorkspaceAttr(workspace),
//	    observability.QueryAttr(query),
//	    observability.TopKAttr(topK),
//	)
//
// # Integration
//
// To integrate observability into your code:
//
// 1. Initialize at startup:
//
//	// Metrics are auto-registered
//	metrics := observability.GetRegistry()
//
//	// Configure logging
//	logger := observability.NewLogger(
//	    observability.LogLevelInfo,
//	    observability.LogFormatJSON,
//	)
//	observability.SetLogger(logger)
//
//	// Enable tracing (optional)
//	shutdown, err := observability.InitTracing(tracingConfig)
//	if err != nil {
//	    log.Fatalf("tracing init failed: %v", err)
//	}
//	defer shutdown(context.Background())
//
// 2. Instrument operations:
//
//	func WriteMemory(ctx context.Context, mem core.Memory) error {
//	    // Get logger and metrics
//	    logger := observability.LoggerFromContext(ctx).
//	        WithComponent("writer")
//	    metrics := observability.GetRegistry()
//	    timer := observability.NewTimer()
//
//	    // Start tracing span
//	    ctx, span := observability.StartSpan(ctx, "write-memory")
//	    defer span.End()
//
//	    observability.SetSpanAttributes(ctx,
//	        observability.WorkspaceAttr(mem.Workspace),
//	        observability.MemoryTypeAttr(string(mem.Type)),
//	    )
//
//	    logger.Info("writing memory", "id", mem.ID)
//
//	    // Perform operation
//	    err := store.UpsertMemory(ctx, &mem)
//
//	    // Record metrics
//	    status := "success"
//	    if err != nil {
//	        status = "error"
//	        logger.WithError(err).Error("write failed")
//	        observability.RecordSpanError(ctx, err)
//	        metrics.WriteErrors.WithLabelValues(
//	            mem.Workspace,
//	            string(mem.Type),
//	            "storage_error",
//	        ).Inc()
//	    }
//
//	    metrics.WriteTotal.WithLabelValues(
//	        mem.Workspace,
//	        string(mem.Type),
//	        status,
//	    ).Inc()
//	    timer.ObserveDuration(
//	        metrics.WriteDuration.WithLabelValues(
//	            mem.Workspace,
//	            string(mem.Type),
//	        ),
//	    )
//
//	    return err
//	}
//
// 3. Expose metrics endpoint:
//
//	import "github.com/prometheus/client_golang/prometheus/promhttp"
//
//	http.Handle("/metrics", promhttp.Handler())
//
// # Configuration
//
// Observability can be configured via environment variables:
//
//   - LOG_LEVEL: debug, info, warn, error (default: info)
//   - LOG_FORMAT: text, json (default: text)
//   - TRACING_ENABLED: true, false (default: false)
//   - TRACING_SAMPLE_RATE: 0.0-1.0 (default: 1.0)
//   - METRICS_ENABLED: true, false (default: true)
//
// # Best Practices
//
// 1. Always record operation status (success/error)
// 2. Include workspace context in all operations
// 3. Use timers for duration measurements
// 4. Record errors with error types for categorization
// 5. Use structured logging with consistent field names
// 6. Add relevant attributes to tracing spans
// 7. Keep metric cardinality low (avoid user IDs, etc.)
// 8. Sample traces in high-volume environments
//
// # Performance
//
// Observability has minimal performance impact:
//   - Metrics: ~100ns per increment/observation
//   - Logging: ~1-5µs per log statement
//   - Tracing: ~1-10µs per span (when sampled)
//
// # Production Deployment
//
// For production deployments:
//
// 1. Metrics: Scrape /metrics endpoint with Prometheus
// 2. Logging: Collect logs with structured log aggregator (ELK, Loki, etc.)
// 3. Tracing: Export to OTLP collector, Jaeger, or Zipkin
//
// Replace stdout trace exporter with production exporter:
//
//	import "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
//
//	exporter, err := otlptracegrpc.New(ctx,
//	    otlptracegrpc.WithEndpoint("collector:4317"),
//	    otlptracegrpc.WithInsecure(),
//	)
package observability

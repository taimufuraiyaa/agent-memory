# Observability Guide

## Overview

agent-memory provides comprehensive observability through three pillars:
- **Prometheus Metrics** - Quantitative monitoring and alerting
- **Structured Logging** - Qualitative insights and debugging  
- **OpenTelemetry Tracing** - Distributed request tracking

## Metrics

### Available Metrics

#### Write Pipeline
- `agent_memory_write_total` - Total write operations (workspace, type, status)
- `agent_memory_write_duration_seconds` - Write operation duration
- `agent_memory_write_errors_total` - Write errors by type
- `agent_memory_write_bytes` - Write operation size

#### Retrieval
- `agent_memory_retrieval_total` - Total retrieval operations (workspace, mode, status)
- `agent_memory_retrieval_duration_seconds` - Retrieval duration
- `agent_memory_retrieval_hits` - Number of hits returned
- `agent_memory_retrieval_errors_total` - Retrieval errors by type

#### Storage
- `agent_memory_storage_operations_total` - Storage operations (workspace, operation, status)
- `agent_memory_storage_duration_seconds` - Storage operation duration
- `agent_memory_storage_errors_total` - Storage errors by type
- `agent_memory_db_connections` - Current database connections
- `agent_memory_db_size_bytes` - Database size per workspace

#### Embeddings
- `agent_memory_embedding_total` - Embedding operations (provider, status)
- `agent_memory_embedding_duration_seconds` - Embedding duration
- `agent_memory_embedding_errors_total` - Embedding errors
- `agent_memory_embedding_batch_size` - Batch size distribution

#### Tokens
- `agent_memory_tokens_used_total` - Total tokens used (workspace, operation)
- `agent_memory_tokens_saved_total` - Total tokens saved
- `agent_memory_token_budget_usage_ratio` - Budget usage ratio

#### Memory
- `agent_memory_count` - Memory count (workspace, type, tier)
- `agent_memory_access_total` - Memory access count
- `agent_memory_decay_score_avg` - Average decay score

#### HTTP API
- `agent_memory_http_requests_total` - HTTP requests (method, path, status)
- `agent_memory_http_request_duration_seconds` - Request duration
- `agent_memory_http_request_size_bytes` - Request size
- `agent_memory_http_response_size_bytes` - Response size
- `agent_memory_http_requests_in_flight` - Current in-flight requests

### Usage Example

```go
import "github.com/taimufuraiyaa/agent-memory/internal/observability"

// Get metrics registry
metrics := observability.GetRegistry()

// Record write operation
timer := observability.NewTimer()
err := writeMemory(ctx, memory)

status := "success"
if err != nil {
    status = "error"
    metrics.WriteErrors.WithLabelValues(workspace, memType, "validation_error").Inc()
}

metrics.WriteTotal.WithLabelValues(workspace, memType, status).Inc()
timer.ObserveDuration(metrics.WriteDuration.WithLabelValues(workspace, memType))
metrics.WriteBytes.WithLabelValues(workspace, memType).Observe(float64(len(content)))
```

### Exposing Metrics

Add a metrics endpoint to your HTTP server:

```go
import (
    "net/http"
    "github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
    // ... setup code ...
    
    http.Handle("/metrics", promhttp.Handler())
    http.ListenAndServe(":9090", nil)
}
```

### Prometheus Configuration

Example `prometheus.yml`:

```yaml
global:
  scrape_interval: 15s

scrape_configs:
  - job_name: 'agent-memory'
    static_configs:
      - targets: ['localhost:9090']
```

### Grafana Dashboards

Import the provided Grafana dashboard:
- Dashboard ID: TBD
- Or use `grafana/dashboard.json` in the repository

Key panels:
- Write throughput and latency
- Retrieval performance
- Storage operations
- Token usage and savings
- Error rates by component

## Logging

### Configuration

Configure logging at startup:

```go
import "github.com/taimufuraiyaa/agent-memory/internal/observability"

// Text format (human-readable)
logger := observability.NewLogger(
    observability.LogLevelInfo,
    observability.LogFormatText,
)

// JSON format (for log aggregation)
logger := observability.NewLogger(
    observability.LogLevelInfo,
    observability.LogFormatJSON,
)

// Set as global logger
observability.SetLogger(logger)
```

### Environment Variables

- `LOG_LEVEL`: debug, info, warn, error (default: info)
- `LOG_FORMAT`: text, json (default: text)

### Usage Examples

#### Basic Logging

```go
logger := observability.GetLogger()

logger.Info("memory written", "id", memoryID, "workspace", workspace)
logger.Error("write failed", "error", err.Error())
```

#### Structured Logging

```go
logger.InfoWithFields("retrieval complete", map[string]any{
    "workspace": workspace,
    "query": query,
    "hits": len(hits),
    "duration": duration.String(),
})
```

#### Context-Aware Logging

```go
logger := observability.GetLogger().
    WithWorkspace(workspace).
    WithComponent("retrieval")

logger.Info("starting search", "query", query)
// Logs: level=INFO workspace=my-workspace component=retrieval msg="starting search" query="..."
```

#### Operation Logging

```go
err := logger.LogOperation(ctx, "write-memory", func() error {
    return store.UpsertMemory(ctx, mem)
})
// Logs: operation started, operation completed (with duration), or operation failed
```

### Log Aggregation

For production deployments, aggregate logs with:
- **ELK Stack** (Elasticsearch, Logstash, Kibana)
- **Grafana Loki**
- **Datadog**
- **CloudWatch Logs**

Use JSON format for easier parsing:

```bash
export LOG_FORMAT=json
./agent-memory serve
```

## Tracing

### Configuration

Initialize tracing at startup:

```go
import "github.com/taimufuraiyaa/agent-memory/internal/observability"

config := observability.TracingConfig{
    Enabled:     true,
    ServiceName: "agent-memory",
    Environment: "production",
    SampleRate:  0.1, // Sample 10% of traces
}

shutdown, err := observability.InitTracing(config)
if err != nil {
    log.Fatalf("tracing init failed: %v", err)
}
defer shutdown(context.Background())
```

### Environment Variables

- `TRACING_ENABLED`: true, false (default: false)
- `TRACING_SAMPLE_RATE`: 0.0-1.0 (default: 1.0)
- `OTEL_EXPORTER_OTLP_ENDPOINT`: OTLP collector endpoint

### Usage Examples

#### Manual Span Creation

```go
ctx, span := observability.StartSpan(ctx, "retrieve-memories")
defer span.End()

observability.SetSpanAttributes(ctx,
    observability.WorkspaceAttr(workspace),
    observability.QueryAttr(query),
    observability.TopKAttr(topK),
)

hits, err := retrieval.Retrieve(ctx, opts)
if err != nil {
    observability.RecordSpanError(ctx, err)
    return err
}

observability.SetSpanAttributes(ctx,
    observability.HitCountAttr(len(hits)),
)
```

#### Convenience Function

```go
err := observability.TraceOperation(ctx, "write-memory",
    func(ctx context.Context) error {
        return store.UpsertMemory(ctx, mem)
    },
    observability.WorkspaceAttr(workspace),
    observability.MemoryTypeAttr(string(memType)),
)
```

### Production Deployment

#### OTLP Collector

Replace stdout exporter with OTLP:

```go
import (
    "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
)

exporter, err := otlptracegrpc.New(ctx,
    otlptracegrpc.WithEndpoint("collector:4317"),
    otlptracegrpc.WithInsecure(),
)
```

#### Jaeger

```go
import (
    "go.opentelemetry.io/otel/exporters/jaeger"
)

exporter, err := jaeger.New(jaeger.WithCollectorEndpoint(
    jaeger.WithEndpoint("http://jaeger:14268/api/traces"),
))
```

### Trace Visualization

View traces in:
- **Jaeger UI** - http://jaeger:16686
- **Zipkin** - http://zipkin:9411
- **Grafana Tempo**
- **Datadog APM**

## Best Practices

### Metrics

1. **Keep cardinality low** - Avoid high-cardinality labels (user IDs, memory IDs)
2. **Use consistent labels** - Standardize workspace, type, status
3. **Record both success and failure** - Always include status label
4. **Use histograms for durations** - Not gauges
5. **Use counters for totals** - Not gauges

### Logging

1. **Use structured logging** - Key-value pairs, not string interpolation
2. **Include context** - Workspace, component, operation
3. **Log at appropriate levels**:
   - DEBUG: Detailed diagnostic information
   - INFO: General operational events
   - WARN: Warning conditions
   - ERROR: Error conditions requiring attention
4. **Don't log sensitive data** - PII, secrets, tokens
5. **Log errors with context** - Include error type, operation, inputs

### Tracing

1. **Sample in production** - 1-10% sample rate for high volume
2. **Add relevant attributes** - Workspace, query, sizes
3. **Record errors** - Always call RecordSpanError
4. **Keep span names consistent** - Use operation-entity pattern
5. **Don't create too many spans** - Overhead adds up

## Alerting

### Prometheus Alert Rules

Example `alerts.yml`:

```yaml
groups:
  - name: agent-memory
    interval: 30s
    rules:
      # High error rate
      - alert: HighWriteErrorRate
        expr: rate(agent_memory_write_errors_total[5m]) > 0.1
        labels:
          severity: warning
        annotations:
          summary: "High write error rate"
          description: "Write error rate is {{ $value }} errors/sec"
      
      # Slow retrievals
      - alert: SlowRetrievals
        expr: histogram_quantile(0.95, rate(agent_memory_retrieval_duration_seconds_bucket[5m])) > 2
        labels:
          severity: warning
        annotations:
          summary: "Slow retrieval operations"
          description: "P95 retrieval time is {{ $value }}s"
      
      # Database size growing rapidly
      - alert: DatabaseSizeGrowth
        expr: rate(agent_memory_db_size_bytes[1h]) > 100000000
        labels:
          severity: info
        annotations:
          summary: "Rapid database growth"
          description: "Database growing at {{ $value }} bytes/sec"
```

### Recommended Alerts

1. **Error Rates** - > 5% error rate for 5 minutes
2. **Latency** - P95 > 2s for retrievals
3. **Database Size** - > 10GB or rapid growth
4. **Memory Count** - Unusual spikes or drops
5. **Token Usage** - Budget exceeded frequently

## Performance Impact

Observability overhead:

| Component | Overhead | Notes |
|-----------|----------|-------|
| Metrics | ~100ns | Per increment/observation |
| Logging | ~1-5µs | Per log statement |
| Tracing | ~1-10µs | Per span (when sampled) |

**Total overhead: < 1% for typical workloads**

## Troubleshooting

### Metrics Not Appearing

1. Check metrics endpoint: `curl http://localhost:9090/metrics`
2. Verify Prometheus scraping: Check Prometheus targets UI
3. Check metric labels: Ensure labels match queries

### Logs Not Structured

1. Verify LOG_FORMAT=json environment variable
2. Check logger initialization
3. Use WithFields methods, not string formatting

### Traces Not Showing Up

1. Verify TRACING_ENABLED=true
2. Check sample rate (may be too low)
3. Verify exporter configuration
4. Check collector/backend connectivity

## Examples

See `examples/observability/` for complete examples:
- `simple.go` - Basic metrics and logging
- `advanced.go` - Full observability integration
- `http_middleware.go` - HTTP instrumentation

## References

- [Prometheus Documentation](https://prometheus.io/docs/)
- [Grafana Documentation](https://grafana.com/docs/)
- [OpenTelemetry Go](https://opentelemetry.io/docs/instrumentation/go/)
- [Structured Logging Best Practices](https://go.dev/blog/slog)

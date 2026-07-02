package observability

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"go.opentelemetry.io/otel/trace"
)

const (
	tracerName = "github.com/taimufuraiyaa/agent-memory"
)

// TracingConfig holds tracing configuration.
type TracingConfig struct {
	Enabled     bool
	ServiceName string
	Environment string
	SampleRate  float64
}

// DefaultTracingConfig returns the default tracing configuration.
func DefaultTracingConfig() TracingConfig {
	return TracingConfig{
		Enabled:     false,
		ServiceName: "agent-memory",
		Environment: "development",
		SampleRate:  1.0,
	}
}

// InitTracing initializes OpenTelemetry tracing with stdout exporter.
// In production, this should be replaced with a proper exporter (OTLP, Jaeger, etc.).
func InitTracing(config TracingConfig) (func(context.Context) error, error) {
	if !config.Enabled {
		return func(context.Context) error { return nil }, nil
	}
	
	// Create stdout exporter (for development/testing)
	exporter, err := stdouttrace.New(stdouttrace.WithPrettyPrint())
	if err != nil {
		return nil, fmt.Errorf("failed to create trace exporter: %w", err)
	}
	
	// Create resource with service information
	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(config.ServiceName),
			semconv.DeploymentEnvironment(config.Environment),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}
	
	// Create trace provider
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.TraceIDRatioBased(config.SampleRate)),
	)
	
	// Set global trace provider
	otel.SetTracerProvider(tp)
	
	// Return shutdown function
	return tp.Shutdown, nil
}

// Tracer returns the global tracer.
func Tracer() trace.Tracer {
	return otel.Tracer(tracerName)
}

// StartSpan starts a new span with the given name.
func StartSpan(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	return Tracer().Start(ctx, name, opts...)
}

// SpanFromContext returns the span from the context.
func SpanFromContext(ctx context.Context) trace.Span {
	return trace.SpanFromContext(ctx)
}

// SetSpanAttributes sets attributes on the span in the context.
func SetSpanAttributes(ctx context.Context, attrs ...attribute.KeyValue) {
	SpanFromContext(ctx).SetAttributes(attrs...)
}

// SetSpanStatus sets the status on the span in the context.
func SetSpanStatus(ctx context.Context, code codes.Code, description string) {
	SpanFromContext(ctx).SetStatus(code, description)
}

// RecordSpanError records an error on the span in the context.
func RecordSpanError(ctx context.Context, err error) {
	if err != nil {
		span := SpanFromContext(ctx)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
}

// TraceOperation wraps an operation with tracing.
func TraceOperation(ctx context.Context, name string, fn func(context.Context) error, attrs ...attribute.KeyValue) error {
	ctx, span := StartSpan(ctx, name)
	defer span.End()
	
	if len(attrs) > 0 {
		span.SetAttributes(attrs...)
	}
	
	err := fn(ctx)
	if err != nil {
		RecordSpanError(ctx, err)
		return err
	}
	
	span.SetStatus(codes.Ok, "")
	return nil
}

// Common attribute keys for agent-memory operations
var (
	AttrWorkspace     = attribute.Key("agent_memory.workspace")
	AttrMemoryID      = attribute.Key("agent_memory.memory_id")
	AttrMemoryType    = attribute.Key("agent_memory.memory_type")
	AttrOperation     = attribute.Key("agent_memory.operation")
	AttrQuery         = attribute.Key("agent_memory.query")
	AttrTopK          = attribute.Key("agent_memory.top_k")
	AttrMode          = attribute.Key("agent_memory.mode")
	AttrHitCount      = attribute.Key("agent_memory.hit_count")
	AttrTokenBudget   = attribute.Key("agent_memory.token_budget")
	AttrTokensUsed    = attribute.Key("agent_memory.tokens_used")
	AttrProvider      = attribute.Key("agent_memory.provider")
	AttrBatchSize     = attribute.Key("agent_memory.batch_size")
	AttrStorageTier   = attribute.Key("agent_memory.storage_tier")
)

// Helper functions for common attributes
func WorkspaceAttr(workspace string) attribute.KeyValue {
	return AttrWorkspace.String(workspace)
}

func MemoryIDAttr(id string) attribute.KeyValue {
	return AttrMemoryID.String(id)
}

func MemoryTypeAttr(memType string) attribute.KeyValue {
	return AttrMemoryType.String(memType)
}

func OperationAttr(operation string) attribute.KeyValue {
	return AttrOperation.String(operation)
}

func QueryAttr(query string) attribute.KeyValue {
	return AttrQuery.String(query)
}

func TopKAttr(topK int) attribute.KeyValue {
	return AttrTopK.Int(topK)
}

func ModeAttr(mode string) attribute.KeyValue {
	return AttrMode.String(mode)
}

func HitCountAttr(count int) attribute.KeyValue {
	return AttrHitCount.Int(count)
}

func TokenBudgetAttr(budget int) attribute.KeyValue {
	return AttrTokenBudget.Int(budget)
}

func TokensUsedAttr(used int) attribute.KeyValue {
	return AttrTokensUsed.Int(used)
}

func ProviderAttr(provider string) attribute.KeyValue {
	return AttrProvider.String(provider)
}

func BatchSizeAttr(size int) attribute.KeyValue {
	return AttrBatchSize.Int(size)
}

func StorageTierAttr(tier string) attribute.KeyValue {
	return AttrStorageTier.String(tier)
}

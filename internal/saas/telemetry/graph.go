package telemetry

import (
	"context"

	baseobservability "github.com/taimufuraiyaa/agent-memory/internal/observability"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// RecordGraph accepts only the bounded, content-free graph observation
// contract. Queries, prompts, entity names, report summaries, and arbitrary
// tenant labels have no field through which to enter metrics or traces.
func (o *Observer) RecordGraph(observation baseobservability.GraphObservation) error {
	if o == nil || o.graph == nil {
		return baseobservability.ErrGraphLimitExceeded
	}
	if err := o.graph.Observe(observation); err != nil {
		return err
	}
	_, span := otel.Tracer("agent-memory/graph").Start(context.Background(), "graph."+observation.Stage,
		trace.WithAttributes(
			attribute.String("service.name", o.service),
			attribute.String("agent_memory.graph.stage", observation.Stage),
			attribute.String("agent_memory.graph.mode", observation.Mode),
			attribute.String("agent_memory.graph.route", observation.Route),
			attribute.String("agent_memory.graph.outcome", observation.Outcome),
			attribute.Int64("agent_memory.graph.records", observation.Records),
			attribute.Int64("agent_memory.graph.input_tokens", observation.InputTokens),
			attribute.Int64("agent_memory.cost_microusd", observation.CostMicroUSD),
		))
	if observation.Outcome == "failed" || observation.Outcome == "rejected" {
		span.SetStatus(codes.Error, "graph_operation_failed")
	} else {
		span.SetStatus(codes.Ok, "")
	}
	span.End()
	return nil
}

# Hosted observability contract

The hosted services expose content-free Prometheus metrics and OpenTelemetry
spans. The API serves metrics at `/metrics`; workers and reconcilers serve them
on `AGENT_MEMORY_TELEMETRY_LISTEN_ADDR` (default `:9090`).

## Data boundary

Allowed structured log and trace fields are request ID, trace ID, opaque tenant
ID, service, bounded operation, method, status, outcome, and duration. Metrics
exclude tenant IDs to bound cardinality. Request and response bodies, source
bytes, extracted text, prompts, model output, quotes, filenames, email addresses,
and object keys are prohibited. The telemetry API accepts operational metadata
only, so content cannot be supplied accidentally.

Tenant IDs remain access-controlled operational identifiers. Production log
access follows the operator-access workflow; applications must not use logs as a
customer-content store. Default retention targets are 30 days for logs, 15 days
for traces, and 13 months for aggregate SLO/cost metrics, subject to the approved
jurisdiction policy.

## Coverage

| Component | Signal |
|---|---|
| API | request rate, outcomes, in-flight requests, latency, correlated spans |
| Database | connection and operation outcomes |
| Queue | connection and publication failures |
| Workers | export, validation, extraction, indexing, deletion, and audit jobs |
| Object storage | connection and vault reconciliation outcomes |
| Model gateway | operation outcome and attributed integer micro-dollar cost |
| Cost | cumulative micro-dollar counter, alertable by time-window increase |

The starter dashboard is
`deploy/saas/observability/grafana-dashboard.json`. Prometheus scrape and alert
rules live beside it. Alertmanager routes `page` alerts to the paging router and
lower-severity alerts to the test channel. Deployments must replace those
internal URLs with their managed routing endpoints and execute a synthetic alert
before launch approval.

## Provisional objectives

- Availability: at least 99.9% non-5xx responses over 30 days.
- Latency: API p95 below one second over a rolling ten-minute window.
- Durable jobs: no repeated queue, object-storage, or worker failure for more
  than five minutes.
- Cost: investigate model spend above USD 50 per hour until beta data establishes
  a reviewed ceiling.

These are initial engineering thresholds, not evidence that the production SLO
observation window or paging drill has passed.

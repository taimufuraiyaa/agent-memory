# Skill Revision Lifecycle Runbook

Metrics and alerts are intentionally content-free. Use audit IDs and revision digests from the authorized lifecycle API for investigation; never add skill names, IDs, revision IDs, or content as metric labels.

## Materialization failure

1. Pause promotion for the affected environment.
2. Inspect the failed activation operation and verify the immutable bundle digest.
3. Confirm the active root skill still matches the recorded active digest.
4. Retry with the same idempotency key after correcting infrastructure; roll back only to the recorded last-known-good revision.

## Rollback spike

1. Confirm whether rollbacks are automatic safety actions or authorized manual actions.
2. Compare evaluation, canary, acknowledgement, and execution telemetry for the window.
3. Pause the promotion controller if failures share a policy, evaluator, or materializer version.

## Evaluation failure

1. Check evaluator readiness and environment fingerprint consistency.
2. Keep affected revisions inactive; do not bypass an inconclusive or failed verdict.
3. Retry the same bounded suite only after the evaluator dependency is healthy.

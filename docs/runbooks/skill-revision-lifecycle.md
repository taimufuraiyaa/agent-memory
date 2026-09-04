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

## Evaluator outage

1. Confirm evaluator readiness and the declared environment fingerprint.
2. Leave all affected revisions in draft or testing; an unavailable evaluator is inconclusive and never promotion-eligible.
3. Restore the restricted runner, then replay the same evaluation ID only if its inputs are identical.

## Stuck canary

1. Stop new canary allocation and inspect verified sample counts, acknowledgement coverage, and the activation generation.
2. Disable the canary revision if it is unsafe; otherwise keep the active revision authoritative while telemetry catches up.
3. Never force promotion to clear a canary slot. Resolve the policy evidence or perform an authorized rollback/disable operation.

## Digest mismatch and disablement

1. Treat a root, activation, or immutable-bundle digest mismatch as a hard safety signal.
2. Disable the mismatched revision through the lifecycle API and verify it can no longer resolve or receive canary traffic.
3. Roll back to the recorded last-known-good revision and verify materialization before restoring traffic.

## Feature shutdown

1. Disable recurrence scheduling and automatic-promotion controllers.
2. Stop new canary allocation and restore every affected root skill to its verified last-known-good revision.
3. Keep immutable revision history and audit records readable; do not delete them as part of shutdown.
4. Confirm legacy agents continue loading the restored root skills, then leave lifecycle mutation endpoints disabled until a new accountable release review.

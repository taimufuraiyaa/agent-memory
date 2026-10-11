# Automatic Skill Learning Release Verification

Release requires the permanent `TestAutomaticSkillLearningNaturalClosedLoop` regression plus the repository-wide gates below. The test captures two verified tool results in separate episodes, derives a durable lesson, detects recurrence, builds an immutable revision, evaluates candidate and baseline, enters canary atomically, records acknowledged executions, automatically promotes an explicitly enabled low-risk policy, and recovers from a verified hard safety signal by rolling back to last-known-good.

## Automated evidence

- Fresh and upgraded SQLite migrations, revision-1 import, immutable bundle custody, and shadow-selection parity.
- Authorization, idempotency, generation checks, accountable approval, exact resolution acknowledgement, and safety disablement.
- Local HTTP/CLI, expanded MCP, responsive dashboard, encrypted portable backup, deletion, retention, and tombstone behavior.
- Bounded content-free metrics, routed alerts, evaluator failure handling, canary analysis, materialization restoration, and rollback drills.
- Full Go tests and vet, MCP tests, dashboard tests/typecheck/build, and the embedded-dashboard production smoke gate.

## Accountable enablement

Automatic promotion remains off when no policy exists and whenever `allow_automatic_activation` is false. Enabling it requires a versioned low-risk policy whose accountable product review records thresholds, canary allocation, false-promotion evidence, rollback evidence, isolation evidence, and retention approval. Medium risk requires accountable approval; high risk cannot enable automatic activation.

The local boundary is the versioned skill configuration and acknowledgement flow. It verifies policy, authorization, generation, digest, and safety invariants before a local low-risk policy can activate. Failed or missing evidence keeps automatic promotion disabled.

## Release decision

Do not enable automatic promotion merely because automated tests pass. Review the local skill lifecycle and migration runbooks, retain the bounded audit records, and complete the local policy review before enabling it. Any failed or missing evidence keeps the feature disabled.

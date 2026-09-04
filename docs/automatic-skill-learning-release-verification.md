# Automatic Skill Learning Release Verification

Release requires the permanent `TestAutomaticSkillLearningNaturalClosedLoop` regression plus the repository-wide gates below. The test captures two verified tool results in separate episodes, derives a durable lesson, detects recurrence, builds an immutable revision, evaluates candidate and baseline, enters canary atomically, records acknowledged executions, automatically promotes an explicitly enabled low-risk policy, and recovers from a verified hard safety signal by rolling back to last-known-good.

## Automated evidence

- Fresh and upgraded SQLite migrations, revision-1 import, immutable bundle custody, and shadow-selection parity.
- Authorization, idempotency, generation checks, accountable approval, exact resolution acknowledgement, and safety disablement.
- Standalone HTTP/CLI, expanded MCP, registered-project hosted API, responsive dashboard, portable export, legal hold, deletion, retention, and tombstone behavior.
- Bounded content-free metrics, routed alerts, evaluator failure handling, canary analysis, materialization restoration, and rollback drills.
- Full Go tests and vet, MCP tests, dashboard tests/typecheck/build, and the embedded-dashboard production smoke gate.

## Accountable enablement

Automatic promotion remains off when no policy exists and whenever `allow_automatic_activation` is false. Enabling it requires a versioned low-risk policy whose accountable product review records thresholds, canary allocation, false-promotion evidence, rollback evidence, isolation evidence, and retention approval. Medium risk requires accountable approval; high risk cannot enable automatic activation.

## Release decision

Do not enable automatic promotion merely because automated tests pass. The operator must retain the approved policy record and complete the final product-review checkbox in the production release record. Any failed or missing evidence keeps the feature disabled.

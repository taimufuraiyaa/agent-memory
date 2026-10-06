# Skill Background Orchestrator Contract

Contract version: `skill-orchestrator/v1`

The background orchestrator coordinates existing skill-lifecycle services. Its durable records describe what work may run; they do not contain or replace lifecycle evidence, generated skill content, prompts, model output, credentials, or filesystem paths.

## Durable records

- A workflow binds one verified origin to an immutable input digest, scope, configuration version, policy digest, current stage, and optimistic generation.
- A job binds one workflow stage to an immutable input digest, policy version, schedule, dependency count, retry ceiling, lease fence, and bounded authoritative result references.
- Dependencies identify accepted terminal parent results. They never infer readiness from delivery order.
- Every claim creates an immutable attempt record binding owner, attempt number, fence, lease window, terminal result, and safe failure classification.
- Safety signals bind an authenticated verifier and evidence identifier to an exact skill revision, deduplication digest, policy version, severity, and disposition.
- Configuration is immutable and digest-bound. Automatic low-risk mode is invalid without accountable approval, release-evidence, and signature references.

## Compatibility and safety

Workers claim only the exact contract version they implement. Unknown stages, enum values, versions, invalid timestamps, malformed digests, stale or missing fences, unbounded references, path-like values, multiline content, and arbitrary payloads fail validation.

Only a running job may hold lease fields. Terminal jobs require an ordered completion timestamp and explicit result. Terminal workflows cannot reopen. Repository mutations must additionally compare scope, owner, fence, and workflow generation.

Public records expose stable identifiers and bounded lowercase codes. Protected diagnostics may retain implementation errors, but those errors are not copied into jobs, attempts, metrics, status responses, or dead letters.

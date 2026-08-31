# Skill Orchestrator Chaos Certification

## Purpose

This release gate proves that standalone SQLite and hosted PostgreSQL orchestration converge after crashes and ownership loss without activating an unapproved revision.

## Required matrix

Run the shared repository certification against both runtimes. Each runtime must cover crashes before and after the side-effect boundary of every registered stage, plus renewal loss, duplicate enqueue, stale fencing, database outage, evaluator timeout, cancellation, and worker restart.

Do not replace a missing hosted run with a synthetic observation. A release report requires every case for both runtimes.

## Report contract

Build a `SkillChaosCertificationInput` from the two completed runs and bind it to:

- the immutable release identifier;
- the build SHA-256 digest;
- the migration SHA-256 digest;
- the approved maximum duration for one case;
- the exact observation matrix.

`CertifySkillOrchestratorChaos` sorts the observations canonically and creates an Ed25519-signed `agent-memory/skill-orchestrator-chaos-certificate/v1` report. Certification fails if a case is missing or duplicated, did not converge, produced more than one domain effect, reported any unsafe activation, or exceeded the approved duration.

Verify the certificate with `VerifySkillOrchestratorChaosCertificate` and the release trust key before admission. Store the certificate as immutable release evidence; never store evaluator content, skill payloads, or credentials in it.

## Failure handling

If any case fails:

1. keep automatic activation disabled;
2. retain the failed bounded observation and content-free logs;
3. repair the repository, adapter idempotency, or worker fencing defect;
4. rerun the complete matrix for both runtimes;
5. issue a new certificate bound to the repaired build and migrations.

Never edit, partially reuse, or re-sign a failed report.

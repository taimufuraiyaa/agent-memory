# Skill Orchestrator Security Release Gate

## Boundary

This gate verifies security evidence; it does not perform or self-certify the independent review. Repository tests and disposable deployments remain implementation evidence only.

Automatic activation stays disabled until an independent security owner signs a review for the exact deployed release. The signed statement must validate against `api/evidence/v1/skill-orchestrator-independent-security-review.schema.json` and bind the build, migrations, normalized tenant-isolation receipt, and chaos certificate.

## Required local controls

The release bundle must contain content-free passed evidence for exactly these controls:

1. forced tenant/workspace RLS;
2. API and repository authorization;
3. filesystem and artifact custody;
4. worker process and database privilege;
5. forged identifiers, tokens, and safety signals;
6. concealment and timing behavior;
7. payload and log redaction; and
8. least-privilege evaluation execution.

Each control carries only a passed flag, zero finding count, and SHA-256 digest of its private evidence. Keep tenant IDs, workspace IDs, requests, SQL, timing samples, payloads, tokens, logs, findings, and reviewer identity outside the application database in the immutable private dossier.

## Independent review

The reviewer must assess two tenants and two workspaces on the deployed release, including RLS bypass attempts, identifier substitution, forged credentials and signals, cache/error/timing inference, worker and evaluator credentials, filesystem custody, and redaction. Remediate and retest every finding before signing.

The statement is valid only when:

- its key belongs to the configured independent-security trust set;
- its release and four digests exactly match the gate configuration;
- its completion and expiry are within the approved review-age window; and
- all eight local controls are complete and passed with zero findings.

`EvaluateSkillSecurityGate` returns content-free blockers and never accepts a product-generated signature as independent evidence.

## Failure handling

If any control, binding, freshness check, or signature fails, keep automatic activation disabled. Preserve the failed evidence, remediate, rerun the complete local suite and independent review, then obtain a new signature for the repaired immutable artifacts. Do not edit or reuse an earlier signed statement.

The existing CP2-A independent staging tenant-isolation review remains a separate external program gate and is not closed by these repository fixtures.

# SaaS Threat Model and Security Release Boundary

## Scope and assets

This model covers identity and tenant resolution, PostgreSQL authority, upload
quarantine, immutable source vaults, workers, search projections, the model
gateway, billing, support access, deletion, portable migration, CI artifacts,
and operator credentials. Protected assets are private originals, extracted
text, memories, identity data, credentials, rights provenance, audit evidence,
billing records, deletion state, and encryption material.

## Trust boundaries

1. The edge accepts untrusted clients and supplies only request metadata; the
   API derives the tenant from authenticated membership.
2. API and worker identities have distinct database, queue, and object-store
   capabilities. A job capability names one tenant, source, version, and key.
3. Quarantine is untrusted until signature, checksum, size, malware, and policy
   validation pass. The immutable vault is never a parsing scratch area.
4. Search and vector stores are projections, not authority. Every query includes
   tenant and authorized-resource constraints before ranking.
5. Model providers receive only approved, bounded context through the gateway;
   prompts and responses are excluded from general telemetry.
6. Support sees safe metadata by default. Source access requires independent,
   time-limited elevation and creates an immutable audit event.

## Principal threats and controls

| Area | Threat | Preventive control | Detection and recovery |
|---|---|---|---|
| Tenancy | ID substitution, count/timing inference | Auth-derived context, composite keys, forced RLS, concealment | Two-tenant adversarial suite, anomaly findings, suspension |
| Credentials | Theft, replay, privilege expansion | Verifier-only storage, scoped grants, expiry, rotation | Velocity rules, self-revoke, credential-leak game day |
| Uploads | Malware, polyglots, zip bombs, overwrite | Short grants, exact size/checksum/type, quarantine, immutable promotion | Rejection TTL, reconciliation, quarantine containment |
| Workers | Confused deputy or cross-tenant object read | Single-job capability and service-specific object policy | Capability-negative tests, job/audit correlation |
| Retrieval | Stale/deleted source or cache leak | Authorization before scoring, version filters, projection purge | Parity/deletion tests, rebuild from authority |
| Model gateway | Retention, exfiltration, runaway spend | Provider allowlist, redaction, retention policy, token limits | Content-free usage, circuit breaker, evidence-only fallback |
| Support | Insider browsing or untracked mutation | Safe views, role assignment, two-person elevation, expiry | Immutable operator-view/action audit and access review |
| Billing | Forged/reordered webhook, quota bypass | Idempotent event ledger, provider timestamps, local entitlements | Reconciliation and noisy-neighbor tests |
| Deletion | Partial purge or backup resurrection | Immediate revoke, subsystem confirmations, tombstones | Receipts, retries, restore guard and drills |
| Migration | Corrupt/oversized bundle or silent DB upload | AMPB2 encryption/manifest, explicit source selection, schema/quota checks | Item ledger, reconciliation report, rollback guidance |
| Supply chain | Dependency/image compromise or secret leak | Locked dependencies, scans, minimal non-root images, SBOM/signing gate | CI findings, artifact provenance, rollback |

## Release rule

Repository tests are necessary but do not constitute an independent security
review. Public beta remains blocked until an external penetration and tenant
isolation review is attached to release evidence, all exploitable high/critical
findings are closed, and an accountable security owner signs the report.


# SaaS Implementation Status

Last updated: 2026-08-08.

## Verified

- The hosted API/event conventions are defined in `api/` and checked by
  `make contracts-check`.
- Edge, API, worker, reconciler, and migration entry points compile independently and
  build as non-root container images from shared Go packages.
- Hosted configuration is typed, validated by environment, refuses unsafe
  production-local endpoints, and redacts credentials from its summary.
- Staging and production APIs construct a discovery-backed OIDC verifier that
  validates asymmetric signatures, issuer, audience, expiry, subject, and
  verified email, refreshes rotating JWKS keys, and fails startup closed when
  discovery is unavailable.
- The Kubernetes release path validates four immutable image digests, runs
  migration before workloads, rolls back all three Deployments after rollout
  failure, and optionally atomically emits a mode-`0600`, schema-bound,
  content-free success or failure receipt. The receipt binds cluster context,
  namespace, release, images, timestamps, migration/rollout outcomes,
  Deployment revisions, and rollback state without reading Secret values,
  resource YAML, logs, environments, tokens, or application payloads. Tagged
  hosted releases upload this artifact even when deployment fails. Mocked
  evidence does not close the real staging control.
- A strict machine-readable catalog reconciles exactly to all 57 open external
  controls. The external-evidence CLI hashes in-root non-symlink dossiers,
  requires matching current authorized Ed25519 decisions, rejects unsafe,
  local/mock, missing, duplicate, mismatched, rejected, and expired inputs, and
  emits only content-free counts and sorted control IDs. The runbook, safe empty
  example, and `make saas-external-evidence-check` provide the collection path;
  they do not close any external control without its real dossier.
- `make saas-dev-up` starts PostgreSQL, MinIO, NATS JetStream, migration, API,
  worker, and reconciler workloads. API live and ready probes pass.
- `make saas-local-up` is the explicit persistent local-product entrypoint.
  `make saas-floci-up` swaps only the S3-compatible object boundary to pinned
  Floci 1.6.0 with an isolated volume and idempotent Object Lock bucket setup.
  The local image upgrades OS packages, removes the unused privilege switcher,
  and runs non-root with a read-only root, dropped capabilities, prevented
  privilege escalation, and loopback-only ingress. The complete four-format
  lifecycle passes against both profiles. Floci is a development compatibility
  profile, not least-privilege or launch evidence; the MinIO negative-capability
  gate remains mandatory. Customer traffic enters through the project-owned
  loopback edge on port 58081; direct API port 8080 remains diagnostic-only.
- `make saas-floci-oidc-up` adds a loopback-only provider with discovery,
  JWKS, a fixed synthetic member, and ephemeral overlapping signing keys. The
  API explicitly selects the production discovery authenticator and rejects
  development credentials. The ordinary Floci command removes this optional
  provider and restores development identity. This is local rehearsal, not
  managed-provider or production key-custody evidence.
- `make saas-local-alpha-gate` starts a separate Floci Compose project with
  dynamic loopback ports and isolated volumes, then runs 27 fail-closed checks:
  health, the complete four-format lifecycle, local/Kubernetes/release
  contracts, scratch migrations, retrieval parity, scratch backup/restore,
  runtime hardening, PostgreSQL/NATS/Floci impairment and recovery, a
  content-free edge-to-API telemetry path, API/edge impairment and recovery,
  OIDC unknown-key refresh, key overlap, cached-token outage behavior,
  invalid-token rejection, discovery fail-closed startup and recovery, image
  identities, runtime API/edge trust-secret rotation with old-assertion
  rejection, invalid-configuration fail-closed rollback and state recovery,
  scratch-only independently approved expiring operator access, and
  a two-tenant adversarial retrieval corpus with colliding IDs, unequal sizes,
  warm-path alternation, aggregate timing comparison, and 200 requests at
  concurrency 8, plus credential-abuse detection with independently approved
  repository-backed revocation, a production-HTTP-adapter 503/circuit-open
  model outage with evidence-only fallback, and a distinct fixed-outcome
  source/account deletion receipt. That local run produced zero result/cache
  leaks, zero errors, zero generation calls, a 0.100 ms miss-timing p95 delta,
  and 27.883 ms search p95. The model drill made two bounded upstream attempts
  and then blocked further calls through the open circuit. HIGH/CRITICAL scans
  cover eight images. Each check runs in
  an isolated strict shell so a failed assertion cannot be masked by Bash
  conditional semantics. API readiness now checks PostgreSQL, the
  quarantine bucket, and NATS under a bounded context while liveness remains
  process-only. The gate deletes only its uniquely named databases, containers,
  and volumes. The current package passed with archive SHA-256
  `92f43c5513c2fb516056cae8968e299d0b826157e94de8422c72e5384414478b`;
  the manifest is explicitly classified `local_development` and contains no
  credentials, source bytes, extracted text, user identifiers, OIDC tokens,
  authorization headers, identity fields, JWKS bodies, private keys, or
  environment dumps. Runtime secret, assertion signature, operator, ticket,
  source, database-row, deletion-operation, prompt, evidence-text, and upstream
  response-body fields are also excluded. These operational
  rehearsals remain local development evidence and do not satisfy managed
  secret storage, human operator identity, staging rollback, approved timing
  risk, real provider degradation, external model cost, regional load, elapsed
  deletion windows, independent review, or owner approval.
- The Docker build context is bounded by `.dockerignore`; the validated context
  is under 1 MB rather than the original 1.6 GB workspace transfer.
- The authorization middleware resolves a server-owned request context and
  returns a generic not-found result for unauthorized tenant selection.
- PostgreSQL migration `0001` creates account/control records and tenant-keyed
  workspaces, notes, memories, feedback, sessions, sources, jobs, lineage,
  outbox, audit, and deletion records with forced row-level security.
- Verified account provisioning atomically creates the personal tenant, owner
  membership, onboarding state, private workspace, audit record, and outbox event. Repeated
  provider identities reconcile to the original account and tenant.
- Hosted attestation storage matches first-use, 30-day expiry, renewal,
  policy-change, idempotency, and audit behavior while rejecting calls without
  authenticated account and tenant context.
- Hosted session lifecycle covers login, logout, expiry, verified email change,
  and account recovery with revocation of prior sessions.
- The development API composes the PostgreSQL control, attestation, and memory
  stores behind authenticated tenant middleware; production configuration
  rejects the local identity emulator.
- Memory writes atomically commit tenant-scoped memory, outbox, and content-free
  audit state while enforcing idempotent retries.
- A two-identity HTTP/PostgreSQL test proves signup, attestation, memory write,
  tenant-selection denial, and cross-tenant workspace-ID denial.
- Agent credentials support bounded scopes, one-time secret display, verifier-
  only storage, inspection, expiry, rotation, and revocation. Scoped tokens are
  accepted by the same authorization middleware without role-scope expansion.
- Hosted note create/update, retrieval feedback, and session-end mutations use
  optimistic revision or idempotency controls and transactionally emit outbox
  and content-free audit records. Raw session transcripts are not retained.
- The worker publishes PostgreSQL outbox records to a file-backed NATS
  JetStream with lease-based restart recovery, schema validation, broker-side
  event-ID deduplication, exponential retries, bounded dead-lettering, and
  idempotent consumer checkpoints. Delivery statistics expose backlog age and
  pending, published, dead-letter, and checkpoint counts.
- Workspace and account exports are asynchronous, AES-256-GCM encrypted before
  private object storage, SHA-256 verified, audited, and available through a
  server-mediated 15-minute download window. Bundles include documented memory,
  note, source-policy, source-version, lineage, and attestation metadata while
  excluding source bytes and internal vault keys. Tests cover tampering,
  expiry, and cross-tenant operation-ID substitution.
- Source grants enforce active rights attestation, tenant capability, workspace,
  rights basis, format, checksum, byte, source-count, and concurrency limits.
  Ten-minute object-specific bearer grants stream directly into quarantine and
  are consumed once.
- The worker validates exact size and SHA-256, PDF/EPUB signatures, text
  encoding, and malware-test signatures. Missing scanner input fails closed;
  rejected and expired objects purge. Accepted bytes are AES-256-GCM encrypted
  into tenant/source/version vault keys, then quarantine is removed.
- The reconciler detects missing, orphaned, and multiply referenced vault
  objects. A dedicated MinIO provisioning job creates buckets and three
  service identities: API can only upload quarantine objects and read exports;
  workers can move quarantine/vault/export objects; reconciliation can only
  list vault keys. Negative capability tests prove API vault denial,
  reconciler content-read denial, and API export-write denial. Versioned vault
  writes reject overwrites and retain the original ciphertext.
- Hosted extraction workers decrypt only tenant-prefixed vault objects and port
  the existing PDF, EPUB, Markdown, and text provenance adapters. Parser and
  normalization versions are durable source-version metadata.
- Source publication is one PostgreSQL transaction across versioned structural
  nodes, passages, citation locators, provenance, audit, job completion, source
  state, and indexing outbox events. Failure rollback, replay, and tenant-local
  duplicate-content tests prove no partial authorized corpus becomes visible.
- The rebuildable PostgreSQL full-text projection stores tenant/source/version
  scoped search documents with a GIN index, versioned projection receipts, and
  backlog/stale/ready statistics. Replays are idempotent; rebuild replaces the
  projection; active-version changes purge stale documents; disabled and
  deleting sources are purged before they can be queried.
- The hosted API exposes tenant-isolated source collection, detail, and retry
  operations. Responses translate internal failures into an allowlisted safe
  recovery contract and expose rights basis, attestation policy, parser and
  normalization provenance, encryption version, publication time, and
  retention state without vault keys.
- The API serves an accessible source-custody dashboard at `/dashboard/` with
  all eight product states, semantic progress indicators, safe failure and
  retry actions, and a provenance/retention details drawer. Live browser
  verification exercised loading, details, retry, focus behavior, responsive
  rendering, and a clean browser console.
- Hosted contract and package tests pass with the race detector; targeted vet
  and container topology validation pass.
- Full-text/vector retrieval, score mixing, decay, suppression, feedback,
  citations, context budgets, reviewed memory, and the tenant-aware model
  gateway are implemented with local/hosted parity evidence.
- Audit events use a content-free validated envelope, searchable hash chain,
  immutable archive, anomaly rules, audited containment, least-privilege
  operator access, and a stateful notice/repeat-abuse workflow.
- Retention is registry-driven. Source/account deletion, subsystem receipts,
  scoped holds, backup tombstone replay, restore evidence, and self-service
  privacy views are implemented and failure-injection tested.
- Usage, plans, subscriptions, webhook ordering, quotas, concurrency isolation,
  billing UX, reconciliation, and per-tenant unit-economics calculation are
  implemented without customer content in billing metadata.
- AMPB2 local export and hosted import are encrypted, checksummed, explicit
  about selected source bytes, resumable, idempotent, and reconciliation-backed.
  CLI, MCP, Go SDK, and dashboard modes are explicit; hosted credentials use an
  OS keyring and can revoke themselves.
- Staged rollout controls enforce hashed invitations, country, minimum-age
  confirmation, account/rate/abuse limits, trial/source caps, and audited tenant
  feature/workload flags. Paused uploads fail before custody begins.
- Content-free alpha analytics, failure ownership, launch scorecards, game-day
  evidence, threat-model/runbook contracts, and machine-evaluated private-beta,
  public-beta, and GA evidence windows are implemented. Release decisions now
  also require fresh metrics, recent passed drills, and canonical Ed25519
  approvals verified against an out-of-band owner/control trust bundle; an
  offline-capable CLI hashes reviewed evidence and creates approval artifacts.
- CI now includes frontend, migration/contract, dependency, secret, and image
  scans. All workflow actions are commit-pinned. Tagged hosted releases publish
  minimal non-root, versioned images with SBOM, maximum provenance, and a
  GitHub/Sigstore-signed provenance attestation.
- Hosted API, worker, database, queue, object-storage, model-gateway, job, and
  cost telemetry is content-free and bounded-cardinality. Prometheus and
  Alertmanager configurations and a Grafana launch dashboard pass their native
  validators. Raw error text and object keys are excluded from service logs.
- Production API and worker services support a centrally approved HTTPS model
  route with immutable model/dimension configuration, no redirects, bounded
  response parsing, safe temporary-error classification, and suppression of
  upstream response bodies. The local deterministic provider remains forbidden
  in production.
- Kubernetes base manifests and isolated staging/production overlays define
  separate service accounts, default-deny networking, non-root read-only
  containers, writable bounded temporary volumes, probes, resource limits,
  topology spread, and digest-only images. The release path runs migration
  before workload rollout, health-gates every deployment, and automatically
  invokes rollback; local policy and rollback-contract tests pass.
- A real local hosted lifecycle passes signup, first-use and simulated 30-day
  consent renewal, PDF/EPUB/Markdown/text upload, quarantine, immutable
  encrypted vault promotion, extraction, indexing, cited retrieval, reviewed
  memory creation, encrypted export, verified source deletion, and verified
  account deletion. Each format reaches ready with its expected parser
  provenance and retained-original state. MinIO positive/negative capability
  tests and immutable-object tests pass.
- The repository-wide Go suite, hosted race suite, vet, frontend tests,
  TypeScript checks/build, MCP tests/build, workflow lint, Kubernetes policy,
  release rollback, Prometheus rule, Grafana JSON, and PromQL checks pass.

## In progress

- Phase 0 jurisdiction, counsel, and managed-provider decisions.
- A real tagged staging deployment and provider-specific managed production
  infrastructure; the provider-neutral release automation is ready but no
  cluster or managed-provider credentials are selected in this repository.
- External penetration testing and independent tenant-isolation review.
- Real internal-alpha/private-beta execution, observation windows, staffing,
  production billing reconciliation, and support/notice operations.
- Security, privacy, legal, product, operations, public-beta, and GA approvals.

## Remaining release boundary

Repository-owned implementation now reaches P12. The remaining open checklist
items are external decisions, production infrastructure/provider work, drills
against a deployed environment, elapsed observation windows, independent
reviews, staffing, and accountable-owner approvals. The release-gate tool fails
closed when those evidence samples are missing; tests never self-certify them.
The exact owner, artifact, verification boundary, and current state for every
open criterion are tracked in
[P0-P12 External Evidence Matrix](external-evidence-matrix.md).
The catalog and content-free verifier described in the
[External Evidence Index Runbook](external-evidence-index.md) turn that matrix
into an executable collection queue; they do not close a control without its
real externally retained dossier and current authorized signature.

# P0-P12 External Evidence Matrix

Status captured on 2026-08-08. This matrix is the handoff boundary between the
implemented provider-neutral product and claims that require a real provider,
deployed environment, elapsed observation window, independent reviewer, or
accountable business owner. Repository tests are supporting evidence only; they
cannot replace the external artifacts listed here.

The current workspace has no release image digests, staging kubeconfig, managed
provider configuration, or reachable staging cluster. Every row below remains
open until its evidence is stored in the external immutable evidence system and
the applicable signed release decision references its SHA-256 digest.

The machine-readable source for these 57 control IDs is
[`api/evidence/v1/external-control-catalog.json`](../../api/evidence/v1/external-control-catalog.json).
Use the [signed external-evidence index runbook](external-evidence-index.md) to
assemble and verify dossiers. A complete local fixture proves the verifier, not
the controls in this matrix.

## Evidence rules

- Use content-free identifiers in application tables and release artifacts.
- Store reports, contracts, screenshots, logs, and provider exports outside the
  application database.
- Record artifact SHA-256, accountable owner, observation interval, environment,
  release ID, and immutable image digests.
- Sign owner decisions using the workflow in
  [Signed Release Approvals](release-approvals.md).
- A local emulator, mocked rollback, fixture approval, or checklist edit never
  proves a staging, production, legal, staffing, or independent-review claim.

## Phase 0-5

| Control | Remaining condition | Accountable owner | Required external evidence | Repository support | Current evidence |
|---|---|---|---|---|---|
| P0.1-A | Launch countries, minimum age, support language, and notice jurisdiction approved | Product + counsel | Signed launch-scope decision and jurisdiction memo | `docs/saas/decision-register.md` | Missing |
| P0.1-B | Counsel reviews attestation, retention, notice, deletion, export, and privacy copy | Counsel + privacy | Versioned legal review referencing exact policy/copy digests | Attestation, retention, notice, deletion, and export tests | Missing |
| P0.2-A | Identity, PostgreSQL, object, queue, secrets, observability, billing, email, and model providers selected | Architecture + security + privacy + operations | Signed provider ADRs, regions, DPAs, subprocessors, and service inventory | Typed provider-neutral boundaries | Missing |
| P0.2-B | Exit strategy and export exist for every critical provider | Operations | Per-provider exit runbook plus successful export/restore exercise | Portable export and provider adapters | Missing |
| P0.2-C | Provider content use is limited to approved purpose | Privacy + security | Data-flow review, contract clauses, retention/training settings, and traffic evidence | Content-free telemetry and model gateway policy | Missing |
| CP0-A | Provider and jurisdiction blockers resolved or explicitly deferred | Product + counsel + architecture | Signed decision register with no unowned blocker | `docs/saas/decision-register.md` | Missing |
| CP0-B | Cost envelope and staffing accepted | Product + finance + operations | Approved forecast, beta cap, on-call/support/notice staffing roster | Unit-economics calculator and workload limits | Missing |
| P1.2-A | Staging progressive deployment and rollback demonstrated | Operations | Tagged staging release receipt, image digests, Deployment revisions, health result, and rollback result | `scripts/saas-kubernetes-release.sh staging` now atomically emits schema-validated passed/failed receipts and the tagged workflow uploads them even on failure | No staging cluster, published image digests, or real passed/rollback receipts |
| P1.4-A | Development, staging, and production isolated | Platform security | Provider account/project, namespace, identity, secret, network, and data-store inventory with drift report | `make saas-kubernetes-check` | Provider environments missing |
| P1.4-B | Network tiers, secrets, databases, buckets, queues, and workload identity provisioned by reviewed IaC | Platform + security | Reviewed IaC plan/apply output and resource inventory | Kubernetes overlays and service-specific contracts | Provider IaC missing |
| P1.4-C | Production data stores have no public ingress | Security + operations | Provider firewall/private-endpoint export and independent reachability scan | Default-deny workload policy | Production environment missing |
| CP1-A | Content-free edge-to-API-to-telemetry staging request succeeds | Operations | Trace-linked request receipt from real edge and staging deployment | API telemetry tests | Staging missing |
| CP1-B | Rollback, secret rotation, and operator access demonstrated | Security + operations | Three passed drills with audit/rollout evidence and no customer content | The isolated local-alpha gate now proves runtime API/edge trust-secret rotation, invalid-configuration rollback, and scratch-only independently approved expiring operator access; Kubernetes rollback remains contract-tested | Managed-secret, human-operator, tagged-staging, and owner-approved drills missing |
| CP1-C | No customer feature launches before safe-platform approval | Product | Signed launch-state attestation and external exposure inventory | Admission controls default closed | Owner attestation missing |
| CP2-A | Independent control-plane isolation review finds no cross-tenant path | Independent security reviewer | Signed report covering APIs, RLS, identifiers, caches, errors, and timing | Adversarial integration tests | Independent review missing |
| CP2-B | Identity-provider outage and credential revocation drills pass | Security + operations | Passed staging drill receipts with alert, containment, RTO, and audit references | The isolated local-alpha gate proves OIDC outage behavior plus deterministic credential-abuse detection, independent approval, audited repository-backed revocation, and post-revoke denial | Managed identity/credential inventory, real alert route, staging response, measured RTO, and accountable responders missing |
| CP3-A | Human and agent clients write, search metadata, export, and audit memory in staging | Product + QA | Trace-linked journey receipts for web and scoped agent credential clients | Hosted API E2E tests | Staging journey missing |
| CP3-B | PostgreSQL backup/restore meets provisional RPO/RTO | Operations | Managed backup identifier, restore timestamps, integrity reconciliation, measured RPO/RTO | Tombstone restore guard; isolated local alpha package proves fixture backup/restore and cleanup | Managed database and measured RPO/RTO missing |
| CP4-A | Security approves object policy, worker capabilities, logs, and traces | Security | Signed custody review referencing deployed policies and sampled telemetry | `make saas-object-policy-test` | Independent deployed review missing |
| CP4-B | Privacy approves every retained artifact and TTL | Privacy | Data inventory with purpose, trigger, TTL, deletion method, and policy version | Retention registry coverage | Approval missing |
| CP4-C | PDF, EPUB, Markdown, and text each pass upload-to-ready in staging | QA + operations | Per-format source/version/job/projection receipts and immutable release ID | `make saas-upload-smoke` covers all four formats with local MinIO and Floci profiles; the isolated local alpha package records the complete lifecycle receipt | Staging format run missing |
| CP5-A | Retrieval parity threshold approved and met | Product + engineering | Signed threshold decision plus immutable parity report | `make saas-integration-test` runs parity fixture; the isolated local alpha package records a fresh scratch-database parity receipt | Threshold approval and representative run missing |
| CP5-B | Two-tenant adversarial corpus stays below approved result/count/timing/cache risk | Independent security reviewer | Blind corpus report, statistical timing analysis, cache-key review, and risk acceptance | Tenant-filtered lexical/vector tests plus a local scratch corpus with colliding IDs, unequal sizes, warm-path alternation, zero result/count/cache leaks, and aggregate timing regression metrics | Independent corpus, statistical tolerance, cache-key review, and risk acceptance missing |
| CP5-C | Search p95 and model cost meet target under load | Product + operations | Versioned load report with corpus size, concurrency, region, model, p50/p95/p99, and cost | Bounded telemetry/cost metrics plus a generation-disabled local run of 200 retrievals at concurrency 8 with zero errors and 27.883 ms p95 | Real region/provider/model/cost load run and owner approval missing |

## Phase 6-9

| Control | Remaining condition | Accountable owner | Required external evidence | Repository support | Current evidence |
|---|---|---|---|---|---|
| P6.5-A | Counsel approves launch-country notice workflow and text | Counsel | Signed review of exact notice copy, jurisdiction routing, deadlines, and escalation | Notice state machine and audit tests | Approval missing |
| CP6-A | Notice workflow is staffed and legally approved | Legal operations + support | Coverage roster, response SLA, escalation contacts, tabletop result, and counsel approval | Notice workflow implementation | Staffing and approval missing |
| P7.4-A | Deleted records age out within published backup window | Privacy + operations | Provider retention configuration plus aged-backup test after the full published interval | Backup expiry timestamps and restore guard | Provider and elapsed-time evidence missing |
| CP7-A | Privacy/legal approve retention UI and receipts | Privacy + counsel | Signed review referencing rendered UI/copy and receipt schema digests | Privacy API/UI and deletion receipts | Approval missing |
| CP9-A | Internal users migrate representative libraries successfully | Product + QA | Consented cohort report covering size/format distributions, reconciliation, and failures | AMPB2 import/export tests | Internal cohort run missing |
| CP9-B | Retrieval parity and rollback instructions accepted | Product + engineering + operations | Signed parity report and completed rollback tabletop | Parity fixture and `docs/saas/migration-rollback.md` | Acceptance missing |

## Phase 10-12

| Control | Remaining condition | Accountable owner | Required external evidence | Repository support | Current evidence |
|---|---|---|---|---|---|
| P10.1-A | Internal accounts exercise signup through deletion with real operations | Product + QA + operations | End-to-end alpha journey receipts, support cases, and deletion reconciliation | An isolated automated local alpha journey now produces a content-free receipt without touching the persistent product | Human-operated internal alpha and support evidence missing |
| P10.2-A | Independent penetration and tenant-isolation tests complete | Independent security reviewer | Signed scoped penetration report and retest evidence | Threat model and adversarial tests | Review missing |
| P10.2-B | All exploitable high/critical findings closed | Security | Finding register with remediation commits/releases and independent retest status | CI security gates; all eight local alpha images pass a fresh fixed HIGH/CRITICAL Trivy gate | Independent finding/retest register missing |
| P10.3-A | Required operational game days pass | Operations + security | Passed receipts for database, queue, model, credential, isolation, and deletion scenarios | `readiness.RunDrill` and domain receipts; the 27-check isolated local alpha now covers PostgreSQL, NATS, Floci, OIDC, runtime secret/rollback, operator access, two-tenant isolation/load, credential abuse/revocation, bounded model-provider outage, and fixed-outcome source/account deletion | Selected-provider failures, real alerts/responders, staging projections, measured targets, immutable external receipts, and owner approval missing |
| P10.3-B | SLO and cost alerts page tested owners | Operations + finance | Alert test timestamps, routes, acknowledgements, escalation, and owner roster | Prometheus/Alertmanager rules | Routed test evidence missing |
| CP10-A | Security, privacy, legal, product, and operations approve invited users | Named domain owners | Five current private-beta signed approvals | Private-beta release gate | Approvals missing |
| CP10-B | No severity-one or launch-blocking issue remains | Incident commander + product | Open-incident/finding export and signed blocker review | Incident and finding controls | External register missing |
| CP10-C | Capacity and cost support beta cap | Operations + finance + product | Load/capacity report and approved worst-case unit economics | Quotas and unit-economics calculations | Real capacity data/approval missing |
| P11.1-A | Customer feedback and incident channels staffed | Support + operations | Published channel inventory, coverage roster, escalation test, and response policy | Support escalation runbook | Staffing missing |
| P11.2-A | Production billing reconciles with provider and usage ledger | Finance + engineering | Provider settlement/invoice reconciliation with sampled tenant usage and variance | Billing webhook/ledger reconciler | Production billing period missing |
| P11.3-A | Beta meets provisional SLOs for agreed window | Product + operations | Immutable metric samples covering the agreed observation interval | Release-gate metric evaluator | Elapsed beta window missing |
| P11.3-B | Deletion, notice, anomaly, and support operations meet targets | Privacy + security + support | Receipt/case aggregates and sampled case evidence for the same beta window | Domain receipt and audit systems | Beta operations window missing |
| P11.3-C | No unexplained isolation or audit-integrity signal exists | Security | Closed anomaly report, chain verification, and signed residual-risk review | Audit chain and anomaly engine | Beta signal review missing |
| CP11-A | External signup, legal pages, status, support policy, and security contact ready | Product + counsel + support + security | Live URL/copy digests, ownership, monitoring, and signed launch-asset approvals | Public-beta signed controls | Live assets/approvals missing |
| CP11-B | SLO, deletion, audit, billing, abuse, and cost gates pass | Domain owners | Public-beta gate report covering one shared current window | Release-gate evaluator | Observation evidence missing |
| CP11-C | Public-beta artifacts and readiness approval are signed and current | Product + release authority | Complete immutable approval-directory export and trust bundle | Approval signer/verifier | Signed artifacts missing |
| P12.2-A | GA meets SLO, retention, support, security, and unit-economics thresholds | Product + domain owners | GA scorecard over agreed window with source references and digests | GA metric gate | GA window missing |
| P12.2-B | Restore, deletion, credential, and notice drills repeatedly pass | Operations + security + privacy | Multiple dated drill receipts within GA window | Drill recency gate | Repeated production drills missing |
| P12.2-C | Fresh product, security, privacy, legal, and operations GA decisions exist | Named domain owners | Five unexpired GA approval artifacts from authorized keys | Ed25519 approval verifier | Approvals missing |

## Definition of MVP done

| Control | Remaining condition | Accountable owner | Required external evidence | Current evidence |
|---|---|---|---|---|
| MVP-A | Every MVP requirement has an owner and passing evidence | Program owner | Completed matrix with artifact digests and no missing row | Missing |
| MVP-B | Production-like signup-to-account-deletion journey passes | Product + QA + operations | Trace-linked E2E report covering consent, all formats, query/review, export, renewal, source deletion, and account deletion | Full isolated local alpha lifecycle and content-free manifest pass; production-like staging report missing |
| MVP-C | Cross-tenant adversarial and external penetration reviews pass | Independent security reviewer | CP2-A, CP5-B, and P10.2 evidence with retests | Missing |
| MVP-D | Restore, deletion, provider, queue, credential, and notice drills pass | Operations + domain owners | Current passed drill set referenced by GA report | Local synthetic restore, deletion, provider, queue, and credential rehearsals pass; notice, selected-provider/staging repetitions, external evidence, and owner approval remain missing |
| MVP-E | Billing and worst-case unit economics fit approved limits | Finance + product | Reconciled billing period and signed cost/cap decision | Missing |
| MVP-F | Legal/privacy approve policy, notices, retention, and countries | Counsel + privacy | Current signed legal/privacy package | Missing |
| MVP-G | Operations approve dashboards, on-call, runbooks, capacity, and rollback | Operations | Signed operational readiness package with real drill and deployment evidence | Local operational rehearsals and provider-neutral rollback contracts exist; accountable approval and deployed evidence remain absent |
| MVP-H | No high/critical exploit or unresolved blocker remains | Security + product | Current finding/incident export plus signed release decision | Missing |

## Completion sequence

1. Product and counsel close P0 launch scope; architecture selects providers.
2. Platform provisions isolated staging through reviewed provider IaC.
3. Publish immutable images and run the tagged staging release and rollback.
4. Execute staging journeys, backup restore, format ingestion, migration,
   isolation, alert, and operational drills; store content-free receipts.
5. Commission independent legal, privacy, object-custody, penetration, and
   tenant-isolation reviews; close and retest findings.
6. Run internal alpha and private beta for real observation windows with staffed
   support, notice, incident, and billing operations.
7. Create private-beta, public-beta, and GA signed approval artifacts and run
   `agent-memory-release-gate`; release only when its JSON report is ready.

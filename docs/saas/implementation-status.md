# SaaS Implementation Status

Last updated: 2026-08-10.

Current acceptance snapshot: **351 of 408 items complete (86.0%)**. The 57 open
items are the externally retained deployment, drill, observation-window,
independent-review, staffing, and accountable-approval controls cataloged
below; local tests cannot self-certify them.

- The production path-based external verifier now retains the same captured
  artifact-root descriptor through final catalog, index, trust, and approval-
  set revalidation. It then repeats the complete approval-eligible dossier set
  and caller-visible root identity before returning the report and four source
  digests. A deterministic test replaces the public root after metadata and
  approval checks; finalization fails with no result. The external-evidence CLI
  and final-MVP collector inherit this boundary, while all 57 real controls
  remain external and open.
- Canonical external-evidence verification now captures every approval-eligible
  indexed dossier's root-relative path, regular-file identity, size, and
  modification time before the first dossier hash. Each hash must use its
  captured file, and the complete set plus intermediate directories is checked
  again after the final hash. A deterministic test replaces the first dossier
  after it is verified while the second remains in the pass; the mixed package
  fails closed. Missing approvals still produce valid-unready results without
  opening their dossier. All 57 externally retained controls remain open.
- External-evidence decisions now use one path-based canonical verification
  operation from source loading through dossier hashing. It returns the exact
  catalog, index, trust, and approval-set digests only after revalidating all
  metadata paths and the complete approval directory/member snapshot following
  the dossier pass. The external-evidence CLI and final-MVP collector no longer
  assemble a decision from separate loaders. Deterministic tests add an approval
  and replace the index path during dossier verification; both fail closed.
  The 57 externally retained controls remain open.
- External-evidence dossier hashing now opens one validated non-symlink
  `os.Root` for the complete verification pass. Every dossier is opened relative
  to that captured root, all intermediate directories are checked before and
  after hashing, and the caller-visible root identity is revalidated before a
  report returns. Deterministic tests replace `artifacts/` with an outside
  symlink after validation and replace the artifact root after capture; both
  fail closed. Dossier digests and all 57 external outcomes are unchanged.
- Production external-evidence verification now accepts only the deterministic
  typed semantic SHA-256 of the exact ordered 57-control catalog. Truncation,
  reordering, or changes to approval controls, owner groups, or evidence
  requirements fail before readiness evaluation. The ordinary CLI and final
  MVP collector both use the canonical entry point; generic small-catalog
  verification is unexported test support. The CLI's ready-path test now builds
  all 57 dossiers and approvals rather than proving readiness with one control.
  All 57 real controls remain external and open.
- Local-alpha manifest build and verification now bind all selected receipts to
  one opened, non-symlink `os.Root`, snapshot every selected receipt before
  hashing, repeat that snapshot afterward, and require the caller-visible root
  path to retain the captured directory identity. Deterministic tests reject a
  symlinked root, root replacement between or after receipt reads, and an
  unread receipt replaced mid-traversal. The manifest schema and all 57
  external-control outcomes are unchanged.
- The model gateway now treats an elapsed per-attempt context as authoritative
  even if a provider returns success after the deadline. A deterministic late-
  success regression test replaces the former scheduler-dependent timeout
  escape and covers the shared embedding/generation retry behavior.
- All 42 production SaaS receipt publishers now use one shared descriptor-
  rooted create-only implementation. It rejects symlinked parents, captures and
  verifies one `os.Root` directory identity, creates random mode-`0600` JSON
  temporary files and final hard links relative to that root, syncs the file
  and directory, and cleans both temporary and linked names if the parent path
  changes. Deterministic tests replace the parent after root capture and after
  linking. A repository contract rejects legacy path-following, `CreateTemp`,
  direct-link, and direct-`O_EXCL` publication patterns; no external control is
  self-certified by this local custody improvement.
- Every production path-backed SaaS evidence reader now re-stats the opened
  descriptor and uses non-following `Lstat` on the evidence path after decoding
  or hashing. A follow-up full inventory corrected the initial ten-reader scope
  and hardened 34 additional readers that had non-following path checks but no
  descriptor re-stat or modification-time comparison. A function-level parser
  contract requires both checks and prevents an unrelated safe function from
  masking an unsafe reader in the same file. The contract also scans production
  evidence commands; its follow-up found and hardened the local-alpha manifest
  CLI's metadata/verification JSON loader with exact-length and pre/post
  descriptor/path checks plus a deterministic symlink-to-opened-file test. No
  external control is self-certified by this local hardening.
- The final MVP normalizer now rechecks catalog/index/trust/input descriptor and
  path identity after exact-byte hashing/decoding, and binds approval hashing
  plus strict decoding to one repeated directory/member snapshot. Deterministic
  tests reject post-open source replacement and a newer approval added after
  snapshot. MVP-A through MVP-H remain external pending the real 49-control
  package, immutable receipt, eight owner decisions, and final 57/57 result.
- CP10-A, CP11-C, and P12.2-C approval-export collectors now reconcile the
  manifest, hash files, strictly decode decisions, and verify signatures from
  one exact stable directory/member snapshot. They no longer calculate an
  export digest and then reopen the directory for approvals. Deterministic
  concurrency coverage rejects a newer decision added after snapshot; the
  three controls remain external because authoritative exports and accountable
  approvals are still required.
- The shared release-gate and external-index approval loaders now decode trust,
  catalog, index, and decision JSON from the exact bounded opened-file bytes,
  recheck identity/size/modification time after reading, and require each
  approval directory's sorted membership and member identities to remain one
  coherent snapshot. Dossier hashing and the offline signer's owner-only key
  read also recheck their opened paths. Deterministic tests reject post-open
  replacements and a concurrently added newer decision; the readiness count is
  unchanged because real approvals remain external.
- CP7-A, P10.3-A, and P11.1-A custom input loaders now validate, decode, and
  hash the same bounded regular file descriptor and recheck identity and size
  after reading. Deterministic adversarial tests reject validate-then-open path
  replacement, closing the last three weaker custom-loader paths found by the
  evidence-package audit without changing any external-control outcome.

## Verified

- P0.2-A now has a strict inventory-bound architecture/topology review evidence
  boundary. It requires exact coverage of all eight core components across six
  ownership, custody, capacity, failure-isolation, cost, and incident-response
  domains; reconciles reviewed and independently operated failure-domain
  counts; covers eight fixed data flows and all three enabled/disabled
  integration contract decisions; and derives ten artifact-bound outcomes plus
  joint Architecture/Security/Privacy/Operations review. Local fixtures cannot
  prove physical independence or close P0.2-A; real private diagrams, ADRs,
  facilities, data-flow/contracts, immutable custody, and current accountable
  approval remain external.
- P10.1-A now has a strict release-bound internal-alpha lifecycle evidence
  boundary. It reloads the exact ready staging inventory/plan/change, passed
  release, and ready CP3-A client-journey receipt; reconciles a one-to-100-
  account cohort against positive source-count/byte caps and exact PDF, EPUB,
  Markdown, and text aggregates; requires eleven causal signup-to-deletion
  stages with a real 28-day consent renewal; derives support target and deletion
  readiness; and binds nine checks to the exact cohort, source, support,
  deletion, trace/audit, and Product/QA/Operations artifacts. Local fixtures
  cannot close P10.1-A; the real 28-to-93-day internal staging cohort, immutable
  private evidence, weekly review, and signed accountable approval remain
  external.
- P10.3-A now has a strict release-bound operational game-day evidence
  boundary. It reloads the exact ready staging inventory/plan/change and passed
  release; requires seven fixed database, queue, all-component,
  all-integration-state, credential, isolation, and deletion scenarios; derives
  causal response/recovery durations and approved-target compliance; and
  reconciles 49 scenario plus eight bundle outcomes. Local rehearsals cannot
  close P10.3-A; real staging faults, alerts, responders, private artifacts,
  approved targets, and signed Operations/Security review remain external.
- CP7-A now has a strict content-free Privacy/Counsel review boundary. It
  requires exact closed coverage of four release-rendered privacy surfaces and
  five customer-rights receipt contracts; binds dashboard-build, OpenAPI, and
  schema manifests plus separate signed-review digests; and derives eight fixed
  review outcomes. Complete adverse review remains valid-unready, while
  omissions, substitutions, stale timelines, and readiness contradictions fail
  closed. Local fixtures cannot close CP7-A; real released artifacts, immutable
  private custody, and current Privacy/Counsel signatures remain external.
- CP10-A now has a strict signed private-beta approval-export boundary. It
  hashes exact ready P10.2-B security-closure, P10.3-B alert-routing, CP10-B
  blocker-review, and CP10-C capacity receipts from one staging release;
  derives one evidence-bundle digest with the private supporting-manifest
  digest; reconciles every immutable export entry; and verifies exactly five
  Legal, Operations, Privacy, Product, and Security Ed25519 decisions through
  an independent trust bundle. Every verified decision must bind that same
  bundle. Missing, rejected, or expired decisions remain valid-unready, and
  repository fixtures cannot close CP10-A.
- P0.2-B now has a strict content-free component-recovery and integration-exit
  evidence boundary. It reloads and hashes the exact self-managed inventory,
  requires all eight core components and three external integrations, preserves
  enabled states, and independently reconciles 44 replacement, failover,
  export, and restore exercises against approved duration targets. Disabled
  integrations still require tested disabled-state and exit procedures. The
  aggregate create-only mode-`0600` receipt supports P0.2-B but cannot close it;
  real installation procedures, exercises, immutable custody, and current
  Operations approval remain external.
- P6.5-A and CP6-A now share a strict content-free launch-notice readiness
  boundary. It revalidates and hash-binds the exact ready P0.1 receipt; requires
  one canonical hashed route per approved notice jurisdiction; independently
  derives copy/language, normal/urgent deadline, primary/backup escalation,
  three-domain staffing, and four fixed tabletop-scenario outcomes; and emits a
  create-only mode-`0600` aggregate receipt. P6.5-A and CP6-A remain open for
  real private policy/copy/routing artifacts, an active roster, completed
  tabletop evidence, immutable custody, and separate accountable signatures.
- CP0-A and CP0-B now share a strict content-free program-approval evidence
  boundary. It revalidates and hash-binds exact ready P0.1 and P0.2-C receipts
  to the self-managed inventory; reconciles four fixed ownership, topology,
  integration, and jurisdiction blocker categories; independently derives two
  approved micro-USD cost ceilings and a positive beta cap; and verifies
  primary plus backup coverage for on-call, support, and notice. A full-chain
  test proves the prerequisite binding and the create-only mode-`0600` receipt
  supports, but does not replace, the two separate accountable approvals.
  CP0-A and CP0-B remain open for real private artifacts and current signatures.
- The final MVP boundary now has a non-circular signed-evidence path. A strict
  normalizer reruns the existing external-evidence verifier against the exact
  canonical catalog, requires all 49 non-MVP controls to verify while exactly
  MVP-A through MVP-H remain absent, and derives eight deterministic journey,
  security, drill, economics, legal/privacy, operations, blocker-free, and
  all-foundation gates from verified dossier digests. Its create-only receipt
  is evidence material for eight accountable decisions; MVP-A through MVP-H
  remain open until the real receipt is retained, signed, indexed, and the
  unchanged full 57-control verifier passes.
- P0.2-C now has a strict content-free external-integration data-purpose
  review boundary. It reloads and hashes the exact self-managed staging or
  production inventory, requires matching payment, transactional-email, and
  model enabled states, binds configuration, purpose, contract-or-disabled,
  settings, traffic, exit-plan, and review digests, and derives seven fixed
  settings, allowlist, minimization, and readiness checks. Disabled
  integrations require zero traffic; enabled unsampled integrations remain
  inconclusive; prohibited content, fields, destinations, training, or logging
  fail closed. P0.2-C remains open for the real installed contracts/settings,
  egress exports, immutable private dossier, and Privacy/Security signatures.
- P0.1 now has a strict content-free launch-scope and legal-position boundary.
  It binds exact private decision-register, jurisdiction-memo, policy-manifest,
  legal-review, and risk-register digests; requires positive country, language,
  and notice-jurisdiction coverage plus a recorded minimum age; evaluates six
  fixed legal positions and eight fixed scope/review checks; and derives zero
  blocking or unowned-risk readiness. P0.1-A and P0.1-B remain open for the
  authoritative private artifacts and current Product/Counsel/Privacy
  signatures.
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
- P10.2-B now has a strict independent security-closure boundary. It binds four
  mandatory penetration/isolation/dependency/image assessment sources and a
  content-free finding register to the exact staging platform/release chain,
  derives positive scope coverage, and requires every exploitable or
  inconclusive high/critical finding to be closed with a passed independent
  retest. P10.2-B remains open for the real private assessment exports,
  remediation/retest records, and signed Security release review.
- CP9-A now has a strict representative migration-cohort boundary. It binds a
  current (at most 31-day-old) consented aggregate AMPB2 cohort to the exact
  ready staging platform and passed release, requires positive coverage for all
  four supported formats and three approved size buckets, reconciles selected
  and outcome totals, and derives zero failed, unexplained-loss, and
  duplicate-publication readiness.
  CP9-A remains open for a real internal cohort, immutable private decision and
  report, and signed Product/QA approval.
- CP9-B now has a strict migration parity and rollback-acceptance boundary. It
  revalidates and hashes ready CP9-A and CP5-A receipts, requires one exact
  staging platform/release and dataset, and derives readiness from eight fixed
  local-copy, profile, credential, reconciliation, deletion, continuity,
  remigration, and cross-functional-review tabletop outcomes. CP9-B remains
  open for the real parity report, completed rollback tabletop, immutable
  private dossier, and signed Product/Engineering/Operations acceptance.
- P12.2-A now has a strict retention-aware GA scorecard boundary. It binds an
  approved 28–93 day window to the installed production platform and passed
  release, evaluates exactly thirteen availability, latency, security,
  isolation, deletion, audit, billing, recovery, cost, support, and retention
  metrics, independently derives sample coverage and fixed/dynamic targets,
  and publishes aggregate-only create-only evidence. P12.2-A remains open for
  the real elapsed window, immutable private exports and decisions, and signed
  Product/domain-owner review.
- P12.2-B now has a strict repeated-drill boundary bound to the ready P12.2-A
  receipt and its exact GA window. It requires at least two restore, deletion,
  credential, and notice drills on distinct UTC dates with globally unique IDs
  and artifact digests, derives outcome readiness, and rejects replay or
  outside-window substitution. P12.2-B remains open for real repeated
  production drills, immutable private reports, approved policy, and signed
  Operations/Security/Privacy review.
- P12.2-C now has a strict signed GA approval-export boundary. It reloads and
  hashes ready P12.2-A/P12.2-B receipts, derives one digest covering both,
  reconciles an exact immutable export and independent trust bundle, and
  verifies current authorized product, security, privacy, legal, and operations
  decisions against that shared digest. P12.2-C remains open for the real
  authoritative export, independent trust, current signatures, and named-owner
  GA review.
- CP11-C now has a strict content-free signed-approval export boundary. It
  reloads and hashes ready CP11-A and CP11-B receipts for one production
  release, reconciles every regular JSON artifact against an exact immutable
  export manifest, hashes the separately managed trust bundle, verifies all six
  scoped current Ed25519 decisions, and requires each reviewed-evidence digest
  to bind the correct prerequisite. Symlinks, extra/missing/changed artifacts,
  invalid signatures, unknown controls, substitution, and green contradictions
  fail closed; output excludes owners, keys, evidence references, filenames,
  and signatures. CP11-C remains open for the real authoritative export,
  independent trust bundle, current decisions, and Product/Release Authority
  approval.
- CP3-B now has a strict content-free PostgreSQL restore collector bound to the
  exact ready self-managed inventory, infrastructure plan, and applied-change
  chain. It derives RPO/RTO against the provisional 300/3,600-second targets,
  requires ten fixed reconciliation, audit, tombstone, deleted-data, and
  cleanup checks, preserves failed drills as valid-unready, and publishes a
  create-only mode-`0600` receipt. An isolated pgvector PostgreSQL 17 run
  applied 26 migrations, restored 74 public tables, retained one deletion
  tombstone, and passed the deleted-data guard. This is repository/local proof
  only; CP3-B remains open for a real self-managed backup/PITR drill, immutable
  private dossier, and signed Operations approval.
- CP4-C now has a strict content-free four-format staging collector bound to
  one exact passed release. It requires unique PDF, EPUB, Markdown, and text
  source/version/job/trace identities, hashed full-text and vector projection
  receipts and counts, seven causal outcomes, and deletion of every temporary
  staging source. Failed runs remain valid-unready; unsafe files, copied IDs,
  media mismatches, contradictory state, stale clocks, local/mock evidence, and
  content-bearing fields fail closed. A fresh isolated Floci alpha run passed
  all 27 controls, including `formats=4`, four source deletion receipts, and
  stack cleanup under explicit `local_development` classification. CP4-C
  remains open for a real staging release, immutable private records, and
  signed QA/Operations approval.
- CP4-B now has an exact twelve-class retention inventory with a required
  customer-reviewable purpose, trigger, TTL, deletion method, policy version,
  owner, hold behavior, migration plan, impact, and effective time. The privacy
  API has a typed OpenAPI response and the embedded dashboard shows each
  purpose. A read-only collector binds canonical installed policies to the
  exact ready self-managed inventory/plan/change chain and atomically publishes
  a content-free mode-`0600` receipt. An isolated pgvector PostgreSQL 17 proof
  applied all 27 migrations, found 12 complete active policies and zero
  incomplete rows, and produced the normalized receipt. CP4-B remains open for
  a real installed-platform receipt, immutable private database proof, and
  signed Privacy approval.
- P7.4-A now has a production-only aged-backup evidence boundary rather than
  relying on tombstone timestamps alone. Retention receipts are strictly
  reloaded and re-hashed; the collector binds the exact inventory, plan, ready
  applied change, and installed policy set, then derives the deadline from the
  `backups` policy and requires seven hashed checks for manifest verification,
  later-deleted record presence, deletion receipt, expiry schedule, post-window
  absence, restore unavailability, and cryptographic expiry. Failed checks are
  retained as valid-unready evidence. Local time-shifted tests prove only the
  collector; P7.4-A remains open until a real production backup crosses the
  complete interval and Privacy/Operations sign its immutable dossier.
- CP4-A now has a strict content-free custody-review normalizer bound to the
  exact ready self-managed staging inventory/plan/change chain and passed
  Kubernetes release. Ten fixed checks cover effective policy export; API,
  worker, and reconciler capabilities; vault immutability; quarantine
  promotion/removal; immutable audit custody; source-content absence from
  sampled logs and traces; and restricted telemetry access. Exact byte-digest
  binding, bounded causal/freshness windows, honest valid-unready results,
  create-only mode-`0600` publication, aggregate CLI output, schemas, a
  full file-chain test, Make target, and runbook are verified. CP4-A remains
  open until Security reviews a real staging deployment, the private policy/
  capability/log/trace dossier is retained immutably, and its digest is signed.
- CP2-A now has a separate independent-review evidence boundary rather than
  promoting the local two-tenant rehearsal. A strict normalizer binds six
  fixed control-API, forced-RLS, identifier-substitution, cache-namespace,
  concealment/error, and timing-inference domains to the exact ready staging
  inventory/plan/change chain and passed Kubernetes release. Passed, failed,
  and inconclusive outcomes have consistent bounded finding counts and private
  evidence hashes; unsafe, stale, incomplete, duplicated, contradictory, or
  misbound input fails closed. Schemas, aggregate CLI, create-only mode-`0600`
  publication, a full file-chain test, Make target, runbook, and reusable skill
  are verified. CP2-A remains open until a real independent reviewer assesses
  staging, retains private reports/remediation/retests immutably, and signs the
  exact dossier digest.
- CP1-C now defaults closed in both fresh and upgraded installations: internal
  alpha has signup disabled while retaining invitation requirements for any
  later controlled enablement, and rollback of the corrective migration cannot
  reopen signup. A read-only collector binds the installed five-field launch
  policy observation to the exact staging inventory/plan/ready-change and
  passed-release chain, then atomically publishes a content-free mode-`0600`
  receipt with aggregate CLI output. CP1-C remains open until this runs against
  real staging, the private exposure/policy evidence is retained immutably, and
  Product signs the exact dossier digest.
- CP1-B now has one content-free staging evidence boundary for all three safety
  demonstrations. Rollback receipts are strictly reloadable and must prove the
  canonical API, worker, and reconciler restoration set. A normalizer binds the
  exact staging inventory/plan/ready-change, passed baseline, failed rollback-
  succeeded attempt, and live rollback receipt to seven managed-secret rotation
  checks and seven human operator-access checks. Failed and inconclusive results
  remain valid-unready; schemas, aggregate CLI, atomic mode-`0600` publication,
  full-chain tests, Make target, and runbook exclude identities, secret values,
  commands, audit rows, and customer content. CP1-B remains open until the real
  authorized staging exercises are retained immutably and Security/Operations
  sign the exact dossier digest.
- CP2-B now has a strict content-free identity-safety normalizer bound to the
  exact staging inventory/plan/ready-change and passed-release chain. Fixed
  identity-provider outage and credential-revocation checks cover real alert
  delivery, cached-key continuity, fail-closed trust, deterministic abuse
  detection, independent approval, production-path revocation, post-revoke
  denial, containment, recovery, immutable audit, and content absence. The
  collector derives detection, alert, containment, and RTO durations, binds
  private approved-target digests, preserves failures and target breaches as
  valid-unready, and atomically publishes a mode-`0600` receipt with aggregate
  CLI output. CP2-B remains open until both real managed staging drills and
  immutable private evidence are signed by Security and Operations.
- CP5-A now has a strict content-free representative retrieval-parity
  normalizer bound to the exact staging inventory/plan/ready-change and passed
  release. It binds private threshold-decision and immutable parity-report
  digests, canonicalizes eight dataset/overlap/score/exact-term/feedback/decay/
  suppression/citation outcomes, independently checks the approved and observed
  metrics, and preserves honest failures or metric breaches as valid-unready.
  Full-chain tests, schemas, aggregate CLI, atomic mode-`0600` publication, Make
  target, runbook, and reusable skill are present. CP5-A remains open until a
  representative staging evaluation and Product/Engineering threshold decision
  are retained immutably and signed.
- CP5-B now has a separate independent retrieval-risk normalizer bound to the
  exact staging inventory/plan/ready-change and passed release. It binds private
  blind-corpus, statistical-timing, cache-review, and risk-tolerance digests;
  canonicalizes seven corpus/result/count/timing/cache/warm-path/risk domains;
  and independently checks two-tenant aggregate leak counts plus approved and
  observed timing delta in integer microseconds. Honest findings and breaches
  remain valid-unready. CP5-B remains open until an independent staged review,
  private reports/retests, approved tolerance, and reviewer signature exist.
- CP5-C now has a strict content-free deployed retrieval-load normalizer bound
  to the exact staging inventory/plan/ready-change and passed release. It binds
  private workload-manifest, load-report, model-cost-report, and approved-target
  digests; canonicalizes eight corpus/site/route/concurrency/distribution/p95/
  attribution/cost outcomes; and independently checks ordered integer p50/p95/
  p99 latency, zero errors, positive model calls, the fixed p95-below-800-ms
  target, and approved versus observed integer micro-US-dollar cost. Honest
  failures and breaches remain valid-unready. CP5-C remains open until a real
  installed-site approved-route run, immutable private reports, approved cost
  target, and signed Product/Operations decision exist.
- CP10-C now has a strict private-beta capacity/economics normalizer bound to
  the exact staging platform/release chain and a reloaded ready CP5-C receipt.
  It binds installed launch-policy, entitlement, capacity, economics, and
  approved-decision digests; canonicalizes eight approval/load/measurement/
  headroom/quota/economics/cost outcomes; independently compares planned with
  supported tenant concurrency and retrieval throughput; and recomputes the
  overflow-safe fixed-plus-account-cap-times-variable monthly worst-case cost.
  Honest headroom or cost shortfalls remain valid-unready. CP10-C remains open
  until the real installed assessment and Operations/Finance/Product signatures
  exist; the configurable local $1,000 planning preference is not evidence.
- P10.3-B now has a strict content-free installed alert-routing normalizer bound
  to the exact staging inventory/plan/ready-change and passed release. It binds
  deployed rule/route exports, the owner roster, private synthetic report, and
  approved target decision; enforces the seven fixed API/worker/queue/object/
  model/cost rule severities; and derives causal delivery, escalation,
  acknowledgement, and resolution durations. Honest failures and breaches
  remain valid-unready. P10.3-B remains open until real installed routes and
  drills, immutable private evidence, approved targets, and signed Operations/
  Finance approval exist.
- CP10-B now has a strict content-free incident and launch-blocker review
  normalizer bound to the exact staging inventory/plan/ready-change and passed
  release. It binds current finding/incident exports, the classification policy,
  and private review decision; canonicalizes five export/classification/
  Incident-Commander/Product outcomes; derives total open-item coverage; and
  requires zero severity-one and unresolved launch-blocker signals. Honest
  blockers or incomplete review remain valid-unready. CP10-B remains open until
  current installed exports and the private decision are retained immutably and
  signed by the Incident Commander and Product.
- P11.1-A now has a strict production support-staffing normalizer bound to the
  exact production inventory/plan/ready-change and passed production release.
  It binds the published channel inventory, active coverage roster, response
  policy, approved targets, and private escalation report; independently checks
  primary and backup coverage; canonicalizes two feedback/incident drills and
  six checks; and derives delivery and acknowledgement durations. Honest
  coverage or response failures remain valid-unready. P11.1-A remains open
  until real channels, an active roster, private drills, approved policy, and
  signed Support/Operations approval exist.
- P11.2-A now has a strict production billing-reconciliation normalizer bound
  to the exact payment-enabled production inventory/plan/ready-change and
  passed production release. The release loader now accepts only an explicitly
  requested staging or production target while rollback-pair verification stays
  staging-only. The normalizer binds processor invoice/settlement exports,
  authoritative invoice/usage ledgers, independent usage recomputation,
  webhook ordering, and approved variance targets; requires positive fully
  matched samples; and independently derives invoice, settlement, and usage
  variance. Honest mismatches remain valid-unready. P11.2-A remains open until
  a real closed production period and signed Finance/Engineering dossier exist.
- P11.3-A now has a strict production beta-SLO normalizer bound to the exact
  production inventory/plan/ready-change and passed production release. It
  binds an immutable metric export, reviewed query manifest, approved elapsed
  window, fixed SLO definition, and Product/Operations review; applies the six
  specification-owned availability, latency, and indexing targets in integer
  units; and independently derives sample coverage and target breaches. Honest
  shortfalls remain valid-unready. P11.3-A remains open until a real elapsed
  production window and signed Product/Operations dossier exist.
- P11.3-B now strictly reloads and hashes the ready P11.3-A receipt before
  accepting deletion, rights-notice, anomaly-alert, and support-case aggregates
  for that exact production window. Four fixed domains reconcile due,
  within-target, late, overdue-open, and sampled-case counts and independently
  derive duration and sample shortfalls under nine canonical checks. Honest
  target or coverage failures remain valid-unready. P11.3-B remains open until
  real same-window exports and samples, approved policies, and signed
  Privacy/Security/Support review exist.
- P11.3-C now strictly reloads and hashes both ready P11.3-A and P11.3-B
  receipts before accepting audit-chain, immutable-archive, isolation-signal,
  audit-integrity-signal, anomaly, and residual-risk aggregates for their exact
  production window. It requires a nonempty fully checked audit population,
  independently reconciles every archive and signal classification, and keeps
  honest breaks, gaps, unexplained signals, or open findings valid-unready.
  P11.3-C remains open until real production exports, a closed anomaly dossier,
  complete chain/archive reports, and signed Security review exist.
- CP11-A now has a strict content-free normalizer bound to the exact production
  inventory/plan/ready-change and passed release. It closes the asset manifest
  to seven fixed signup, legal, status, support, and security surfaces; enforces
  specification-owned owner groups; binds immutable URL, copy, monitor, route,
  and owner-decision digests; and independently derives liveness from bounded
  HTTP/probe observations no older than 900 seconds. Honest failures remain
  valid-unready. CP11-A remains open until the real public assets, external
  monitors, private artifacts, accountable decisions, and signed cross-domain
  approval exist.
- CP11-B now strictly reloads and hashes ready billing, beta-SLO,
  beta-operations, and beta-integrity receipts against one production
  platform/release and exact window. It requires positive signup-attempt and
  active-tenant coverage, reconciles every abuse finding classification, and
  independently derives ceiling-rounded per-active-tenant cost before checking
  both total and per-tenant approved ceilings. Honest blockers or overruns stay
  valid-unready. CP11-B remains open until real same-window receipts and
  exports, approved targets, and signed accountable domain-owner review exist.
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
  self-managed production-identity or production key-custody evidence.
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
  `fb5dd1396f0c4379516d6752f0efad48912413d64c9f508253d5bc732db509d8`;
  the manifest is explicitly classified `local_development` and contains no
  credentials, source bytes, extracted text, user identifiers, OIDC tokens,
  authorization headers, identity fields, JWKS bodies, private keys, or
  environment dumps. Runtime secret, assertion signature, operator, ticket,
  source, database-row, deletion-operation, prompt, evidence-text, and upstream
  response-body fields are also excluded. These operational
  rehearsals remain local development evidence and do not satisfy managed
  secret storage, human operator identity, staging rollback, approved timing
  risk, real production-component degradation, external model cost,
  deployment-site load, elapsed
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
- Hosted memory search now enforces authenticated tenant, active workspace, and
  `memory:read` boundaries; uses a trigger-maintained GIN-indexed PostgreSQL
  projection; returns a closed allowlisted result shape with deterministic
  rank/time/ID keyset pagination; and records only result-count/next-page audit
  metadata. Human-session and scoped-agent API parity, cursor binding, strict
  request decoding, audit-failure withholding, dashboard rendering, migration
  structure, OpenAPI contracts, explicit hosted Go SDK routing, and
  keyring-launched MCP workspace search pass repository tests. The launcher
  and MCP server now share canonical API URL and tenant profile variables,
  while the server retains read-only compatibility aliases. The migration
  round-trip, forced-RLS check, locator matching, deleted-row exclusion,
  cross-workspace/cross-tenant isolation, and stable pagination passed against
  a disposable tmpfs-backed `pgvector/pgvector:pg17` runtime.
- A strict staging client-journey collector now binds exactly one human-web and
  one scoped-agent content-free receipt to the exact passed staging release.
  It accepts only fixed authentication, audited memory write/search, audited
  ready-export, and cleanup outcomes with unique canonical request/trace IDs;
  validates post-release, 30-minute, 24-hour-fresh windows; rejects local/mock,
  unsafe, contradictory, duplicate, unknown, and content-bearing inputs; and
  atomically emits a create-only mode-`0600` bundle plus aggregate-only CLI
  report. Valid failures remain unready rather than disappearing as parser
  errors. This collection boundary does not close CP3-A without real staging
  clients, matching private audit/trace exports, immutable retention, and
  signed Product/QA approval.
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

- Product-owner approval of the fully self-managed architecture is recorded;
  the platform ADR, component recovery/exit runbook, and external-integration
  data policy now define the installation contract. P0.2 remains open for exact
  topology inventory, real recovery/traffic evidence, and accountable
  architecture, security, privacy, and operations approval.
- A strict self-managed platform inventory contract now covers Kubernetes,
  identity, PostgreSQL, object storage, queue, secrets, observability, backup,
  and explicit payment/email/model enablement. The validator rejects unsafe
  files, unknown or duplicate components, public private-service ingress,
  undeclared failure domains, and insufficient production redundancy. Its CLI
  and Make target emit only content-free counts and enabled integration kinds.
  A passing inventory is repository support, not proof of a deployed topology.
- A bounded Kubernetes platform preflight collector now reconciles that
  inventory with the live environment namespace, workload identities, secret
  names, network policies, private Service type, immutable images, and ready
  replicas. The receipt binds the exact inventory bytes, and strict reload
  rejects unsafe files, substituted digests, incomplete or reordered checks,
  and readiness contradictions. It emits an atomic private content-free receipt
  on both ready and unready evaluations without reading Secret representations,
  configuration, logs, events, pods, payloads, or raw resources. Real environment
  receipts and independent network/drift review remain open evidence.
- A provider-neutral infrastructure-plan receipt now binds the validated
  inventory to the exact private IaC source bundle and raw plan by SHA-256. Its
  validator requires 21 fixed network, compute, identity, data, secret,
  telemetry, and backup capabilities, rejects unsafe exposure or redundancy,
  and reports replacement/deletion plans as unready. It executes no tool and
  does not replace real apply, drift, installed-resource, or owner-review
  evidence.
- Exact inventory and plan receipt bytes now receive computed SHA-256 identities,
  and the plan binds the inventory digest. A strict post-apply receipt binds both
  artifacts to hashed raw apply, installed-resource inventory, and drift output;
  reconciles all 21 capability results; and treats failed apply, rollback, drift,
  or drift-check states as unready. The validator does not execute IaC or replace
  real private artifacts, independent review, or signed approval.
- A production-only private-authority exposure receipt extends that exact
  evidence chain to a hashed firewall/network-policy export and raw external
  reachability scan. It requires fixed outcomes for PostgreSQL, object storage,
  durable queue, secrets, observability, backup, and Kubernetes control;
  reachable or inconclusive results are valid but unready. The CLI emits only
  aggregate counts. P1.4-C remains open until a real production scan is
  independently performed/reviewed and its dossier receives an authorized
  signature.
- A read-only staging rollback verifier now closes the gap between a failed
  release receipt's rollback boolean and observed restoration. It strictly
  binds the exact passed baseline and later failed/rollback-succeeded receipts,
  rejects no-op image attempts, and queries only the three fixed Deployment
  images, revisions, desired replicas, and ready replicas. Its private atomic
  receipt reports restored, mismatch, not-ready, or unavailable outcomes and
  never initiates rollback. P1.2 and Checkpoint 1 still require an authorized
  real tagged staging fault run, externally retained receipts, and approval.
- A staging edge-to-telemetry collector now binds an exact passed release
  receipt to one generated request/trace challenge through the fixed readiness
  path. The edge blocks internal and metrics routes; API telemetry keeps only a
  1,024-entry, ten-minute content-free exact-observation cache; and the
  collector accepts no arbitrary paths, queries, redirects, or response
  payloads. Its create-only private receipt and aggregate report prove the
  repository collection boundary. CP1-A remains open until a real staging run,
  matching external trace export, immutable dossier, and accountable approval.
- Phase 0 jurisdiction, counsel, self-managed production-topology, and optional
  external-integration decisions.
- A real tagged staging deployment and self-managed production infrastructure;
  the Kubernetes release automation is ready but no staging or production
  cluster inventory and credentials are committed to this repository.
- External penetration testing and independent tenant-isolation review.
- Real internal-alpha/private-beta execution, observation windows, staffing,
  production billing reconciliation, and support/notice operations.
- Security, privacy, legal, product, operations, public-beta, and GA approvals.

## Remaining release boundary

Repository-owned implementation now reaches P12. The remaining open checklist
items are external decisions, self-managed production infrastructure work, drills
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

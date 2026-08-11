# ADR: Self-Managed SaaS Platform

- Status: product direction approved; security, privacy, and operations review pending
- Decision date: 2026-08-09
- Scope: development, staging, and production infrastructure

## Decision

Agent Memory will run its core SaaS platform on infrastructure owned and
operated by the product organization. AWS, Azure, GCP, and equivalent external
cloud platforms are not deployment dependencies.

The internal platform team owns Kubernetes, network boundaries, ingress,
identity, PostgreSQL, object storage, queues, secrets, observability, backups,
restore, and disaster recovery. Payment processing, transactional email, and
model APIs are optional external business integrations. They are disabled until
explicitly configured and approved, sit behind replaceable adapters, and never
become authoritative stores for Agent Memory data.

## Deployment cells

Development, staging, and production are separate administrative domains. They
must not share cluster credentials, OIDC audiences, encryption keys, database
roles, object namespaces, queue subjects, backup repositories, or operator
elevation grants.

Production spans at least two independently operated failure domains within the
approved site. A failure domain must have independent power, host placement,
storage fault boundaries, and network paths sufficient for the failure tests it
claims. Labels alone do not prove independence.

| Capability | Authority | Minimum production shape | Required proof |
|---|---|---|---|
| Compute | Kubernetes control and worker nodes | Quorum control plane and workload spread across failure domains | Node/failure-domain inventory and drain/loss drill |
| Identity | Self-managed OIDC service and internal key custody | Redundant service, MFA and recovery policy, rotating signing keys | Discovery/JWKS inventory, rotation/outage/recovery drill |
| PostgreSQL | Self-managed PostgreSQL authority | Synchronous or policy-approved replicated primary plus tested backups | Failover, point-in-time restore, RPO/RTO reconciliation |
| Object storage | Self-managed S3-compatible storage | Erasure coding or replication across failure domains | Integrity scan, drive/node loss, immutable-vault and restore drill |
| Queue | Self-managed durable event system | Quorum and replicated durable streams | Leader loss, backlog, duplicate delivery, replay reconciliation |
| Secrets | Internally controlled secret and key service | Encrypted authority, scoped workload delivery, audited rotation | Inventory, least privilege, rotation, outage and recovery drill |
| Observability | Self-managed metrics, logs, traces, alerts | Redundant collection and durable content-free operational evidence | Alert-route test, retention proof, dashboard/SLO review |
| Backup | Separate internal recovery boundary | Encrypted, access-separated, retention-enforced copies | Restore, tombstone replay, aged deletion and repository-loss drill |

Exact products remain replaceable implementation choices. Product selection
must not weaken the data, identity, network, recovery, or evidence contracts in
this ADR.

## Data and network contracts

- PostgreSQL and object storage have no public ingress.
- Workloads authenticate with service-specific identities and receive only the
  database, bucket, queue, and secret capabilities they require.
- Customer content is excluded from general logs, metrics, traces, release
  receipts, and infrastructure inventory.
- Authoritative data leaves the self-managed boundary only through an approved
  export or narrowly approved external integration.
- Backups use credentials and an administrative boundary unavailable to normal
  application workloads and database administrators acting alone.
- Every environment has default-deny network policy and explicit ingress and
  egress allowlists.

## Alternatives considered

### External managed cloud services

Rejected by product decision. They reduce operational workload but move
infrastructure control, account authorization, and part of the custody boundary
to an external cloud provider.

### One large binary on one host

Rejected for the SaaS boundary. It is simple to run but cannot independently
scale ingestion and retrieval, cannot isolate service capabilities cleanly, and
turns one process or host failure into total service loss.

### Self-managed service-oriented platform

Selected. It preserves internal custody and replaceability while retaining
separate failure, scaling, and least-privilege boundaries. The trade-off is
substantial internal responsibility for hardware lifecycle, patching, capacity,
key custody, on-call response, and disaster recovery.

## Scaling and failure behavior

Stateless API, worker, and reconciler workloads scale horizontally. PostgreSQL,
object storage, and the queue scale only through reviewed data-system procedures;
automatic workload scaling must not silently change durability or consistency.
During optional model outages, retrieval returns cited evidence without
fabricated synthesis. During authority or audit uncertainty, sensitive writes
fail closed or remain durably queued according to the documented operation
class.

## Rollout

1. Provision isolated development, staging, and production domains through
   reviewed infrastructure code.
2. Inventory identities, networks, stores, queues, secrets, backup locations,
   failure domains, owners, and software versions without customer content.
3. Run tagged staging deployment, rollback, restore, isolation, rotation, alert,
   and lifecycle drills; retain signed immutable evidence.
4. Commission independent security/privacy review and close findings.
5. Admit production traffic only through the private-beta release gate.

## Approval boundary

The product owner approved this direction on 2026-08-09. P0.2-A remains open
until architecture, security, privacy, and operations approve the exact deployed
topology and its evidence dossier. Approval of this ADR does not prove that a
cluster, facility, recovery path, or external integration is ready.

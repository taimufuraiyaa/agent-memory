# Self-Managed Component Recovery and Replacement

This runbook defines the procedure contract for every critical platform
component. Exact commands belong in the installation repository because host,
network, storage, and secret identifiers must not be committed here.

## Universal procedure

For every component, operations must retain a versioned inventory, accountable
owner, dependency map, backup/export location, integrity check, replacement
procedure, rollback condition, and most recent drill receipt. A drill is valid
only when it records start/end time, release and environment, measured RPO/RTO,
reconciliation result, alert/owner acknowledgement, and an immutable evidence
digest without customer content.

1. Declare the impairment and preserve content-free audit, trace, deployment,
   and infrastructure references.
2. Stop unsafe writes or isolate the failed member; do not destroy the last
   recoverable copy.
3. Verify backup/export integrity before promotion or restore.
4. Replace or fail over using a newly scoped identity and reviewed configuration.
5. Reconcile authority, objects, events, projections, tombstones, and audit
   continuity before reopening traffic.
6. Revoke obsolete identities, quarantine old media, and record disposal.
7. Sign and retain the drill dossier outside the application database.

## Component procedures

| Component | Failover or replacement | Export/restore and reconciliation | Rollback/stop condition |
|---|---|---|---|
| Kubernetes | Drain one node/failure domain; replace from pinned host and cluster configuration; restore control-plane quorum from protected state | Compare nodes, namespaces, service accounts, network policies, image digests, workload revisions, and admission policy | Stop if quorum, identity, policy, or workload placement cannot be proven |
| OIDC identity | Keep cached verification within policy; promote healthy redundant service; rotate signing and session keys after compromise | Restore configuration and encrypted key custody; verify issuer/audience, MFA/recovery, JWKS overlap, revocation, and audit | Stop login or fail startup closed if discovery/signature trust is uncertain |
| PostgreSQL | Fence failed primary before promotion; select the most current healthy replica; redirect scoped clients | Restore base backup plus WAL/PITR into an isolated target; replay deletion tombstones; reconcile migrations, tenants, rows, outbox, and audit chain | Stop if split brain, RPO breach, migration mismatch, or deleted data can be served |
| Object storage | Remove failed drive/node only after quorum/durability check; rebuild with a new service identity | Verify versioned inventory and checksums; restore quarantine/vault/export classes separately; reconcile PostgreSQL object references and deletion state | Stop if immutable vault history, encryption metadata, or tenant/object scope is uncertain |
| NATS/queue | Preserve quorum; replace one member at a time; keep consumers paused when authority is uncertain | Export stream/consumer configuration and protected state; replay through idempotent handlers; reconcile outbox, acknowledgements, dead letters, and job state | Stop if duplicates alter business state or an acknowledged event cannot be reconciled |
| Secret/key service | Fail closed for new secret retrieval where cache policy does not permit use; restore redundant authority | Restore encrypted metadata and policy; reissue workload identities; rotate exposed values; prove old values fail | Stop if provenance, access policy, or key version cannot be established |
| Observability | Route to redundant collectors and paging path; preserve local bounded spools | Restore rules, dashboards, routes, retention configuration, and content-free evidence indexes | Stop sensitive operations if required audit evidence cannot be durably retained |
| Backup repository | Isolate suspected corruption; promote an access-separated replica only after integrity validation | Rebuild catalog from immutable manifests; sample restore every data class; replay tombstones before exposure | Stop if integrity, retention, custody separation, or deletion propagation fails |
| Payment processor | Disable paid conversion while preserving existing entitlements conservatively | Export settlement/invoice references and reconcile immutable usage ledger; switch adapter credentials and webhook verification | Stop new charges on signature, duplicate, amount, or settlement mismatch |
| Transactional email | Queue only approved template events; switch adapter without including source content | Export content-free delivery references and suppression state where contract permits | Stop sends on template/version, jurisdiction, recipient, or data-minimization mismatch |
| Model API | Open circuit and return cited retrieval without generated text; switch only to an approved route | No model output is authoritative; retain content-free request/cost references and re-evaluate data policy before a route change | Stop calls on policy, retention/training, response validation, or cost-limit uncertainty |

## Required exercises

- Quarterly: node/failure-domain loss, queue leader loss/backlog, identity
  rotation/outage, secret rotation, alert routing, and model-route outage.
- Before public beta and after material data-layer changes: PostgreSQL failover
  and point-in-time restore, object node/drive loss, backup-repository restore,
  deletion/tombstone reconciliation, and complete lifecycle recovery.
- Before enabling or replacing an external integration: contract/data-flow
  review, credential rotation, outage behavior, reconciliation, and exit export.

P0.2-B remains open until these procedures are adapted to the exact installation
and successful real-environment exercises are signed by operations.

## Content-free evidence normalization

Create a private input from
`docs/saas/component-recovery-exit.example.json`, replacing every placeholder
with the exact inventory identity, digest of the private procedure or exercise
artifact, and reconciled aggregate. The fixed subject set is the eight core
components plus payment, email, and model. Every subject requires replacement,
failover, export, and restore reviews. For a disabled integration, those reviews
prove the disabled-state continuity and exit path; do not mark them not
applicable.

```sh
make saas-recovery-exit-check \
  PLATFORM_INVENTORY=/private/platform-inventory.json \
  RECOVERY_EXIT_INPUT=/private/component-recovery-exit.json \
  RECOVERY_EXIT_RECEIPT=/private/component-recovery-exit-receipt.json
```

The collector rejects symlinks, unknown fields, changed inventory, missing or
duplicate subjects, unreconciled attempts, target/outcome contradictions, and
evidence older than 24 hours. It writes the receipt once with mode `0600` and
prints aggregates only. Exit `0` means ready evidence, `3` means complete but
unready evidence, `2` means invalid CLI usage, and `1` means invalid evidence or
publication failure.

The repository example contains synthetic digests and proves only the schema
and normalization workflow. It must never be indexed as P0.2-B evidence.

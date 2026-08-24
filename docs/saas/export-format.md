# Hosted Export Bundle v1

Hosted exports are asynchronous, tenant-scoped JSON bundles. PostgreSQL stores
the operation state and audit history. The worker serializes the bundle,
encrypts it with AES-256-GCM, computes SHA-256 over the encrypted bytes, and
writes only the encrypted artifact to the private export bucket.

The API never returns a direct object-storage URL. Downloads require a current
authenticated tenant context, the requesting account, `tenant:export`
capability, a ready operation, a matching checksum, and an unexpired 15-minute
download window. Unauthorized, cross-tenant, expired, missing, and tampered
artifacts all use the same not-found response.

Bundle fields:

- `version`, `exported_at`, `tenant_id`, and optional `workspace_id`.
- Authoritative memories with source provenance, keywords, confidence, and
  storage metadata.
- Notes with bodies, properties, versions, and content hashes.
- Source policy metadata and version fingerprints; original source bytes and
  vault object keys are excluded.
- Lineage edges and transformation versions.
- Rights-attestation policy versions, statement digests, accepted statement
  identifiers, and validity timestamps.

Workspace exports filter memories, notes, sources, source versions, and lineage
to that workspace. Account exports include every active tenant-owned record in
those classes. Audit events, credential verifiers, session tokens, billing
secrets, internal object keys, and raw session transcripts are never exported.

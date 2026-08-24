# Agent Memory Portable Bundle 2.0

Status: stable migration contract for local-to-hosted transfer.

## Security envelope

A portable bundle is UTF-8 JSON encrypted with the `AMPB2` authenticated
envelope. The envelope uses Argon2id with a random 16-byte salt and parameters
`t=3`, `m=65536 KiB`, `p=2` to derive a 256-bit key. AES-256-GCM encrypts and
authenticates the JSON, header, salt, and random nonce. Passphrases must contain
at least 12 characters. A reader must reject an unknown magic value, malformed
header, failed authentication, unsupported schema, or manifest mismatch before
publishing any record.

Passphrases are never included in the bundle or persisted by the hosted import
service. Users must transfer them separately from the encrypted file.

## JSON contract

The root object has these required fields:

| Field | Meaning |
| --- | --- |
| `format` | Always `agent-memory-portable`. |
| `version` | Writer schema version; currently `2.0`. |
| `min_reader_version` | Oldest reader allowed to import the bundle. |
| `exported_at` | UTC export time. |
| `tenant_id` | Origin tenant or local installation identifier. It is provenance, not an authorization target. |
| `workspace_id` | Optional origin workspace identifier. Importers publish only into the explicitly selected destination workspace. |
| `memories` | Versioned memory records selected for transfer. |
| `notes` | Selected note records and properties. |
| `sources` | Selected source metadata, rights basis, and lifecycle state. |
| `source_versions` | Source fingerprints, parser/normalization versions, and version metadata. Internal object-store keys are forbidden. |
| `lineage` | Relationships among selected records. |
| `attestations` | Portable policy/receipt provenance; hosted import still requires a currently active destination attestation. |
| `policies` | Policy and retention versions that governed the export. |
| `source_bytes_included` | True only when the user explicitly selected source bytes. |
| `source_objects` | Optional selected source bytes with source ID, filename, media type, byte length, SHA-256, and standard base64. |
| `manifest` | SHA-256 digest and record counts for the portable payload. |

The manifest digest covers the canonical JSON encoding of `memories`, `notes`,
`sources`, `source_versions`, `lineage`, `attestations`, `policies`, and
`source_objects` in that order. Readers must ignore neither a digest mismatch
nor a count mismatch. New optional fields may be added in a compatible minor
revision; a reader must reject a bundle whose `min_reader_version` is newer than
the reader.

## Source-byte consent

Source bytes are excluded by default. A local exporter must show the selected
source list, estimated byte size, destination, and destination retention policy
before setting `source_bytes_included=true`. Every `source_object` must match a
selected `sources` record and its declared size and SHA-256. The importer accepts
PDF, EPUB, Markdown, and plain text only. It performs identity, tenant,
workspace, attestation, quota, schema, encryption, and checksum validation before
publishing memories, notes, or sources.

## Idempotency and reconciliation

The encrypted bundle SHA-256 and caller idempotency key identify an import inside
one tenant. The hosted importer serializes concurrent imports of identical bytes,
uses deterministic per-item idempotency keys, and keeps an item ledger. Retrying
an interrupted request resumes unfinished work and does not duplicate completed
memories, notes, or sources. The final report contains `imported`, `merged`,
`skipped`, and `failed` arrays with item type, external ID, destination ID, and a
content-free reason code.

Source records without explicitly selected bytes are reported as skipped with
`source_bytes_not_selected`. A completed import is immutable from the client's
perspective; importing the same encrypted bytes again returns the original
operation and report.

## Hosted transport

Send the encrypted bytes as the body of `POST /v1/imports` with:

- `Authorization: Bearer ...`
- `X-Agent-Memory-Tenant: <destination tenant>`
- `X-Agent-Memory-Workspace: <destination workspace>`
- `X-Agent-Memory-Bundle-Passphrase: <passphrase>`
- `Idempotency-Key: <16-128 character key>`

The passphrase header is sensitive and must be redacted by ingress, tracing, and
support tooling. Retrieve the content-free report from
`GET /v1/imports/{import_id}`. The encrypted request limit is 250 MiB; plan
source-count, per-source, storage, and concurrency quotas can be lower.

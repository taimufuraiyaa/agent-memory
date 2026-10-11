# Portable Bundle v2

Status: stable encrypted backup contract for local workspaces.

The bundle is a self-contained AMPB2 archive produced and consumed by the local
Agent Memory installation. It is intended for offline backup, restore, and
workspace transfer between local installations. It never leaves the local
installation and contains no credentials.

## Envelope

The encrypted payload contains a versioned `Bundle` manifest, memories, notes,
selected source objects, and skill lifecycle records. The manifest records
counts and SHA-256 digests for every included collection. Import must verify the
manifest before writing any record and must reject mismatched counts, digests,
or unsupported versions.

## Encryption

`EncryptPortable` uses Argon2id to derive an AES-256-GCM key from the caller's
passphrase. Each export generates a fresh salt and nonce. Passphrases are never
stored in the bundle or by the local service. Decryption authenticates the
complete ciphertext before JSON decoding.

## Source-data boundary

Source originals are excluded by default. A caller must explicitly select a
catalogued source file, and the exporter verifies that its current fingerprint
still matches the catalog before including its bytes. This prevents an
unrelated or modified file from entering a backup implicitly.

## Restore safety

Restore is local-only, idempotent, and workspace-scoped. It must preserve the
manifest's integrity checks, avoid overwriting the source database until the
payload is fully authenticated, and report rejected records without silently
converting them into successful writes.

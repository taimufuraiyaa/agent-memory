---
name: verify-external-evidence-index
description: Verify or restore the Agent Memory signed P0-P12 external-evidence collection boundary. Use when changing the 57-control catalog, evidence index, dossier hashing/path safety, Ed25519 approval binding, evidence CLI or Make target, collection runbook, or when assessing whether externally retained release evidence is complete.
---

# Verify External Evidence Index

Keep integrity verification separate from claims that evidence satisfies a
control. Local fixtures can prove verifier behavior only.

## Inspect the boundary

Read these sources before changing behavior:

1. `.kiro/specs/saas-product-platform/requirements.md`, R21 and R74-R78.
2. `.kiro/specs/saas-product-platform/design.md`, external-evidence design.
3. `.kiro/specs/saas-product-platform/tasks.md`, P1.14 and P12.10-P12.14.
4. `docs/saas/external-evidence-index.md` and
   `docs/saas/external-evidence-matrix.md`.
5. `api/evidence/v1/external-control-catalog.json`.
6. `internal/saas/evidenceindex` and
   `cmd/agent-memory-external-evidence`.

## Preserve invariants

- Keep the catalog IDs exactly synchronized with every matrix row. Do not add a
  fake control to make a count pass.
- Route production CLI and final-MVP verification through
  `VerifyCanonicalFiles`. It must own exact-byte catalog/index/trust loading,
  stable approval-set hashing/decoding, signature and dossier verification,
  final source revalidation, and the four returned source digests as one
  logical decision. Do not reconstruct that sequence in callers.
  Require the compiled digest of the deterministic typed 57-control catalog;
  truncation, reordering, or changes to approval controls, owner groups, or
  evidence requirements must fail before readiness evaluation. Keep generic
  small-catalog verification unexported and test-only.
- Decode strict, bounded catalog, index, trust, and approval JSON from the exact
  validated opened-file bytes, then recheck path identity, size, and
  modification time.
- Require approval loading to preserve one directory identity, sorted JSON
  membership, and member identity/size/modification-time snapshot. Concurrent
  additions, removals, or replacements must fail closed and be retried. Repeat
  this snapshot after the complete dossier pass, not only after approval load.
- Open one validated non-symlink `os.Root` for the complete dossier pass.
  Resolve only clean relative paths below `artifacts/` through that descriptor,
  validate every intermediate component before and after hashing, and recheck
  the public root identity before success. Reject component/root replacement,
  symlink path components, and validate-open races.
- Before any dossier hash, snapshot the complete set of approval-eligible
  indexed dossier paths and their regular-file identity, size, and modification
  time. Bind every hash to its captured file and repeat the complete set plus
  intermediate-directory checks after the last hash. An earlier dossier changed
  during a later hash must fail closed. Do not force dossier access for missing,
  rejected, or expired controls; preserve their valid-unready outcome.
- Keep the same artifact-root descriptor open through final catalog, index,
  trust, and approval revalidation. After those checks, repeat the complete
  dossier set and caller-visible root identity before returning. Do not compose
  a production result around a verifier that has already released root custody.
- Hash the opened dossier and require the index digest and durable URI to match
  the newest current authorized Ed25519 decision for gate
  `external_evidence`.
- Require the offline signer to recheck the owner-only key and reviewed dossier
  descriptor/path identity, size, and modification time after reading or
  hashing; post-open replacement must emit no decision.
- Reject local/mock classifications, unknown or duplicate controls, malformed
  entries, unauthorized signatures, mismatches, rejections, and expiration.
- Keep reports content-free: counts and sorted human control IDs only. Never
  emit dossier contents, artifact paths, signatures, public keys, tokens, or
  personal owner data.
- Preserve exit codes: `0` ready, `3` valid but incomplete, `2` invalid CLI
  arguments, and `1` malformed/unsafe or operational failure.
- Never check an external matrix row merely because a local test or fixture is
  ready.

## Verify changes

Run the narrow gates first:

```sh
GOCACHE=/tmp/agent-memory-go-cache go test \
  ./internal/saas/evidenceindex \
  ./cmd/agent-memory-external-evidence \
  ./cmd/agent-memory-release-approval \
  ./internal/contracts -count=1
```

Then run:

```sh
GOCACHE=/tmp/agent-memory-go-cache go test ./...
GOCACHE=/tmp/agent-memory-go-cache go vet ./...
git diff --check
```

For a real package, use the documented Make target with explicit absolute
paths. Treat exit `3` as a collection queue, not a verifier failure. Before
reporting readiness, confirm all 57 dossiers point to real external systems and
the approvals are within their validity windows.

---
name: verify-external-evidence-index
description: Verify or restore the Agent Memory signed P0-P12 external-evidence collection boundary. Use when changing the 57-control catalog, evidence index, dossier hashing/path safety, Ed25519 approval binding, evidence CLI or Make target, collection runbook, or when assessing whether externally retained release evidence is complete.
---

# Verify External Evidence Index

Keep integrity verification separate from claims that evidence satisfies a
control. Local fixtures can prove verifier behavior only.

## Inspect the boundary

Read these sources before changing behavior:

1. `.kiro/specs/saas-product-platform/requirements.md`, R21.
2. `.kiro/specs/saas-product-platform/design.md`, external-evidence design.
3. `.kiro/specs/saas-product-platform/tasks.md`, P1.14.
4. `docs/saas/external-evidence-index.md` and
   `docs/saas/external-evidence-matrix.md`.
5. `api/evidence/v1/external-control-catalog.json`.
6. `internal/saas/evidenceindex` and
   `cmd/agent-memory-external-evidence`.

## Preserve invariants

- Keep the catalog IDs exactly synchronized with every matrix row. Do not add a
  fake control to make a count pass.
- Accept only strict, bounded JSON from regular non-symlink files.
- Resolve only clean relative dossier paths below `artifacts/` under the
  selected root. Reject symlink path components and validate-open races.
- Hash the opened dossier and require the index digest and durable URI to match
  the newest current authorized Ed25519 decision for gate
  `external_evidence`.
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

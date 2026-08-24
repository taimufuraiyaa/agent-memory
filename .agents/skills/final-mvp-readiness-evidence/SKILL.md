---
name: final-mvp-readiness-evidence
description: Verify or extend the Agent Memory non-circular final MVP readiness boundary. Use when changing the 49 foundational controls, MVP-A through MVP-H dependency maps, derived gate digests, schemas, CLI, runbook, or final 57-control handoff.
---

# Final MVP readiness evidence

Preserve the two-stage launch boundary: independently verify the 49 non-MVP
controls, derive the eight final MVP gates, then obtain eight accountable signed
decisions and rerun the unchanged full 57-control verifier. A local fixture or a
ready pre-final receipt never closes MVP-A through MVP-H.

## Read first

- `.kiro/specs/saas-product-platform/requirements.md` R62
- `.kiro/specs/saas-product-platform/design.md`, “Final MVP readiness evidence”
- `.kiro/specs/saas-product-platform/tasks.md` P12.6
- `docs/saas/final-mvp-readiness.md`
- `docs/saas/external-evidence-index.md`
- `internal/saas/mvpreadinessevidence/evidence.go`
- `internal/saas/evidenceindex/index.go`

## Preserve the non-circular boundary

1. Require the exact canonical ordered set of 57 IDs: 49 foundational P0-P12
   controls followed by exactly MVP-A through MVP-H. Reject catalog
   substitution even when the replacement parses.
2. Rerun `evidenceindex.VerifyCanonicalFiles` against the real catalog, index,
   dossier root, independent trust bundle, and signed approval directory. Do
   not consume an unauthenticated report as proof or permit a structurally valid
   substitute catalog.
   Consume its typed catalog/index, foundational report, and exact four source
   digests; do not separately reopen or hash those sources. The shared operation
   must revalidate catalog/index/trust paths and the stable approval
   directory/member snapshot after dossier verification; directory inode alone
   is insufficient to detect additions or replacements. Hash and decode the
   final-MVP input from the same validated opened bytes with post-read path checks.
   It must also snapshot all approval-eligible indexed dossiers before the first
   hash, bind each hash to that file, and repeat the complete dossier set after
   the last hash so the foundational report cannot mix package generations.
   The same artifact-root descriptor must remain open through final metadata and
   approval checks, followed by one last dossier-set and public-root validation.
   Hash all dossiers through one captured non-symlink artifact root, revalidate
   every intermediate component after hashing, and require the public root path
   to retain the captured identity before accepting the foundational report.
3. The ready pre-final state has 49 verified foundations, exactly the eight MVP
   controls missing, and no other missing, rejected, or expired control. A full
   ready 57-control package is invalid input here because it is circular.
4. Preserve absent, rejected, or expired foundational decisions as complete
   valid-unready evidence. Unsafe files, invalid signatures, changed dossiers,
   unknown controls, path races, and aggregate contradictions fail collection.
5. Derive exactly eight fixed gates in order. MVP-A binds all 49; MVP-B journey;
   MVP-C adversarial security; MVP-D drills; MVP-E economics; MVP-F legal/privacy;
   MVP-G operations; MVP-H blocker-free launch.
6. Derive each gate digest from the mapping version plus sorted prerequisite
   control IDs, availability states, and verified dossier SHA-256 values. Never
   accept a supplied gate digest or outcome.
7. Overall readiness requires all 49 foundations and all eight derived gates to
   pass. Supplied expected readiness must match the derivation.
8. Publish create-only mode-`0600` receipts. CLI output remains aggregate-only
   with exits `0` ready foundation, `3` valid-unready, `2` usage, and `1`
   malformed/unsafe/operational failure.

Keep dossier paths, evidence references, owners, people, signatures, public and
private keys, credentials, tenant/customer/provider data, and artifact contents
outside the receipt. Only source digests, fixed IDs/states, aggregate counts,
and derived gate digests cross this boundary.

## Final handoff

After a real exit `0`, retain the receipt immutably. Eight accountable owner
decisions must bind that receipt digest for MVP-A through MVP-H. Add those eight
dossiers and decisions to the index and run the ordinary external-evidence
verifier again. Only its full `57/57` ready result closes the launch boundary.

## Safe change workflow

1. Update R62, design, and P12.6 before changing behavior.
2. Add a failing derivation or real signed-chain test before implementation.
3. Keep the Go canonical IDs, JSON-schema enums, catalog, and matrix exactly
   synchronized; keep every Mermaid label quoted.
4. Update both schemas, example, Make target, runbook, status, and all eight MVP
   matrix rows together.
5. Never check MVP-A through MVP-H from repository fixtures or alter the exact
   57-control external catalog to make a count pass.

## Verification

```sh
go test -race ./internal/saas/evidenceindex ./internal/saas/mvpreadinessevidence ./cmd/agent-memory-mvp-readiness ./internal/contracts -count=1
make contracts-check
make saas-kubernetes-check
make saas-release-script-test
go test ./... -count=1
go vet ./...
find api docs .kiro -name '*.json' -type f -print0 | xargs -0 -n1 jq empty
git diff --check
```

Reconcile 57 catalog IDs, 57 matrix rows, 57 unchecked external acceptance
items, and zero differences between those ID sets before reporting progress.

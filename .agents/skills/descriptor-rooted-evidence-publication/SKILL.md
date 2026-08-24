---
name: descriptor-rooted-evidence-publication
description: Audit and harden Agent Memory create-only JSON receipt publishers against symlinked or replaced parent directories. Use when adding or reviewing evidence publication, atomic receipt creation, temporary files, hard links, or mode-0600 custody guarantees.
---

# Descriptor-rooted evidence publication

A non-following parent check is not sufficient when later temporary creation and
linking resolve the parent path again. An attacker can replace the directory
between calls and redirect an authoritative receipt. Anchor the complete write
transaction to one opened directory descriptor.

## Required invariant

Use `internal/saas/evidencepublish.JSON` for production SaaS receipts. It must:

1. Reject a blank receipt path or invalid temporary prefix.
2. `Lstat` the parent and reject missing, non-directory, or symlink metadata.
3. Open an `os.Root`, open `.` through that root, and require the same directory
   identity as the `Lstat` result.
4. Revalidate the parent path against the opened identity after root capture.
5. Check the destination through the root and refuse any existing entry.
6. Generate a random clean base name and create it root-relative with
   `O_CREATE|O_EXCL` and mode `0600`.
7. Encode indented JSON, sync, and close the temporary descriptor.
8. Revalidate the parent path, then hard-link temporary to final name through
   the same root so publication remains create-only.
9. Revalidate again, sync the opened directory descriptor, and revalidate
   before success.
10. Remove temporary names on every path and remove a linked final name if a
    later validation or directory sync fails.

Do not replace this with `os.Stat(directory)`, `os.CreateTemp(directory, ...)`,
`os.Link(tempPath, receiptPath)`, or direct `os.OpenFile(receiptPath, ...O_EXCL)`.
Those operations independently resolve mutable pathnames.

## Audit

Search production evidence code:

```sh
rg -n 'os\.Stat\(directory\)|os\.CreateTemp\(|os\.Link\(|os\.OpenFile\([^\n]*O_EXCL' \
  internal/saas cmd/agent-memory-* -g '*.go' -g '!**/*_test.go'
```

The search must return no legacy publisher. Also reconcile the number of
production `Publish` functions with packages calling `evidencepublish.JSON` and
inspect any difference rather than assuming naming conventions cover it.

## Deterministic tests

The shared helper tests must prove:

- valid JSON is mode `0600`, synced, and create-only;
- an initially symlinked parent is rejected without redirected output;
- replacing the parent after root capture fails without a temporary or final
  file in either the opened or replacement directory;
- replacing the parent after linking removes the linked receipt through the
  captured root before returning failure.

Use explicit hooks after root capture and after linking. Do not use scheduler
races or sleeps.

## Repository contract and gates

Keep a contract scanning both `internal/saas` and `cmd` for every legacy
path-based publication primitive above. Run helper tests under `-race`, all SaaS
package race tests, full Go tests, vet, contracts, JSON parsing,
Kubernetes/release checks, exact 57-control reconciliation, and
`git diff --check`.

Local publication safety never proves a deployed, legal, review, staffing,
observation-window, drill, or accountable-approval control.

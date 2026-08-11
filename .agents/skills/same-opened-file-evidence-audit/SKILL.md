---
name: same-opened-file-evidence-audit
description: Audit and harden Agent Memory evidence loaders so validation, hashing, and JSON decoding operate on the same opened regular file. Use when adding or reviewing file-backed evidence, manifests, approvals, or receipts for symlink and path-replacement safety.
---

# Same-opened-file evidence audit

Evidence integrity requires the bytes hashed and decoded to come from the same
file object whose type, identity, and size were validated. `Lstat` followed by
`os.ReadFile(path)` is unsafe because the path can be replaced between calls.

## Audit

Search production evidence code first:

```sh
rg -n 'os\.ReadFile|os\.Open\(|os\.Lstat|DisallowUnknownFields' \
  internal/saas cmd/agent-memory-* | rg -v '_test\.go'
```

For each file-backed JSON loader, require this sequence:

1. Reject blank paths.
2. `os.Lstat` and require a positive bounded regular non-symlink file.
3. Open the path once.
4. `file.Stat` and require `os.SameFile(validated, opened)`, regular type, and
   unchanged size and modification time.
5. Read from that descriptor through `io.LimitReader(maximum+1)` and require the
   byte count to equal the opened size and remain within the bound.
6. Decode those same bytes with `DisallowUnknownFields` and reject trailing
   JSON.
7. Re-stat the descriptor with `file.Stat` and the path with `os.Lstat`, then
   require regular type, the same opened identity, size, and modification time.
   Never use `os.Stat(path)` here: replacing the path with a symlink to the
   original inode would follow the link and falsely pass the identity check.
8. Hash the same decoded byte slice, never a second path read.

For a directory-backed approval export, snapshot the directory identity, sorted
JSON membership, and every member's identity, size, and modification time.
Require each opened member to match that snapshot and repeat the full snapshot
before returning. A changed set is an operational retry, never a partial valid
decision view.

Do not split one logical custody decision across two independently safe passes.
If an export digest and decoded approvals describe the same immutable export,
return both from the same snapshot/file-byte traversal. Hashing a directory and
then calling a separate approval loader can bind one generation's digest to a
later generation's valid signatures even when both loaders are individually
race-safe.

If a release decision combines multiple metadata files, an approval directory,
and dossiers, make one path-based operation own the whole decision. Retain each
source snapshot, verify the dossiers, then repeat every metadata path and
approval directory/member check before returning the report and source digests.
For multiple dossiers, capture every dossier selected for hashing before the
first hash, require each open to match its captured identity/size/modification
time, and repeat the complete set after the last hash. Per-file checks alone do
not catch an earlier dossier replaced while a later dossier is processed.
Keep the root descriptor alive through any later metadata/approval finalization,
then repeat the dossier set and public root identity immediately before return;
otherwise root custody ends before the logical decision does.

Keep normalized output content-free and preserve valid-unready evidence.

## Deterministic regression test

Use unexported deterministic hooks at both sensitive boundaries:

```text
decodeStrictRegular(path, target)
  -> decodeStrictRegularWithHook(path, target, afterOpen)
loadApprovalDirectory(path)
  -> loadApprovalDirectoryWithHook(path, afterSnapshot)
```

The file test replaces the path after the descriptor is opened and requires the
post-read identity check to reject it. The directory test adds a newer decision
after the initial snapshot and requires the final membership check to reject
it. These prove post-open substitution and mixed-generation protection without
a flaky scheduler race. Also retain symlink, unknown-field, trailing-data, and
size-bound tests.

Keep a function-level repository contract that parses every production SaaS
package and `agent-memory-*` command source file and requires each path-backed
reader to contain both descriptor and non-following path revalidation,
including modification-time comparisons. It must also reject literal
path-following `os.Stat(path)` checks. File-level text counts are insufficient
because an unrelated safe function can mask an unsafe reader in the same file.

## Boundary

- Never mark an external control complete because the loader or its fixture
  passes.
- Update requirements, design, and mapped tasks before behavior changes.
- Update the control-specific runbook, matrix support text, and skill.
- Run focused race tests, contracts, full tests, vet, JSON parsing,
  Kubernetes/release gates, exact 57-control reconciliation, and
  `git diff --check`.

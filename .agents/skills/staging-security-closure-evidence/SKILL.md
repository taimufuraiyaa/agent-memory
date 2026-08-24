---
name: staging-security-closure-evidence
description: Verify or extend Agent Memory P10.2-B independent staging exploitable-finding closure evidence. Use when changing assessment-source coverage, high/critical lifecycle and retest derivation, schemas, CLI, runbook, or Security handoff.
---

# Independent staging security-closure evidence

## Boundary

P10.2-B requires real independent assessment and remediation evidence against a
staged release. Repository code only normalizes content-free fingerprints,
fixed state, counts, and digests. Never grant scanner, ticketing, deployment,
database, or evidence-store authority; retain finding text, CVEs, affected
packages/images/endpoints, attack details, tickets, reviewer identity, logs,
traces, payloads, and raw reports in private immutable custody.

Bind the input to the exact ready staging inventory, plan, applied change, and
passed release. Require exactly application penetration, tenant isolation,
dependency/supply-chain, and container-image assessment sources. Each source
has positive expected/observed targets and an immutable digest. A passed source
must have complete target coverage; all four must pass for readiness.

Finding fingerprints and evidence digests are SHA-256 and globally unique.
For critical/high severity, `exploitable` and `inconclusive` both block
readiness until state is `closed` and retest is `passed`. An open finding cannot
claim a passed retest, and a blocking finding cannot use `not_required`.
Medium/low or explicitly not-exploitable findings may remain open, but any
inconclusive exploitability classification keeps classification unready.

Preserve partial coverage, unresolved findings, failed retests, and
inconclusive Security review as valid-unready when their checks agree. Reject
missing/duplicate sources, fingerprint replay, impossible lifecycle states,
unsafe files/unknown fields, stale timelines, release substitution, and green
contradictions. Publish create-only mode-`0600`; CLI exits 0/3/2/1.

## Verification

```sh
go test -race ./internal/saas/securityclosureevidence ./cmd/agent-memory-security-closure ./internal/contracts -count=1
make contracts-check
make saas-kubernetes-check
make saas-release-script-test
go test ./internal/saas/evidenceindex ./cmd/agent-memory-external-evidence ./cmd/agent-memory-release-approval -count=1
go test ./... -count=1
go vet ./...
find api/evidence/v1 docs/saas -name '*.json' -print0 | xargs -0 -n1 jq -e . >/dev/null
git diff --check
```

Repository support contributes three P10.7 items. P10.2-B remains external
until the real independent reports, complete scope, remediation/retests, and
signed Security release review exist.

---
name: automatic-skill-production-release-gate
description: Certify or change Agent Memory's automatic-skill production rollout and shutdown boundary. Use when editing the signed release gate, staged-mode evidence, accountable product approval, staging drills, deployment defaults, or final release checklist.
---

# Automatic Skill Production Release Gate

Keep implementation completion separate from production enablement. Repository tests may use ephemeral signing keys, but they never constitute staging evidence or accountable product approval.

## Inspect first

1. Read `.kiro/specs/automatic-skill-background-orchestrator/requirements.md`, then `design.md`, then `tasks.md`.
2. Inspect:
   - `internal/application/skill_orchestrator_release_gate.go`
   - `internal/application/skill_orchestrator_release_gate_test.go`
   - `internal/integration/skill_orchestrator_production_drill_test.go`
   - `internal/integration/skill_orchestrator_release_deployment_test.go`
   - `docs/runbooks/skill-orchestrator-production-release.md`
3. Search Agent Memory for `production-release-gate`, `skill-orchestrator`, and the current release or commit identifier; submit retrieval feedback immediately.

## Required release contract

The gate must fail closed unless all of these are true:

- Release evidence and product approval have independent valid Ed25519 signatures.
- Every staged mode contains a verified signature over the complete immutable configuration; a signed boolean or digest-only assertion is insufficient.
- Both payloads bind the exact release, build, migration, and policy digests.
- Accountable product approval binds the final automatic configuration digest and the exact release-evidence digest.
- Rollout order is exactly `disabled` → `shadow` → `manual` → `canary` → `automatic_low_risk`.
- At least two complete staging iterations cover pause, drain, restore, and shutdown.
- Every drill preserves the active skill digest and never decreases audit history.
- Rollback timing stays within the approved SLO and alert routing is verified.
- Standalone, hosted, chaos, security, capacity, migration, and alert evidence digests are present.
- Product approval covers risk classes, thresholds, canary policy, retry/dead-letter policy, budgets, retention, SLOs, and automatic-low-risk enablement.
- Product approver and release signer satisfy separation of duty and approval is fresh.

Do not accept booleans or references as substitutes for cryptographic verification at the release boundary. Do not put skill content, prompts, credentials, paths, or customer identifiers in evidence.

The external contracts are `api/evidence/v2/skill-orchestrator-configuration-receipt.schema.json`, `skill-orchestrator-production-release-evidence.schema.json`, and `skill-orchestrator-product-approval.schema.json`. Version 1 release and approval payloads fail closed.

## Deployment invariants

- Base Kubernetes `agent-memory-skill-worker` remains at zero replicas.
- `AGENT_MEMORY_SKILL_WORKER_ENABLED` remains `false` in the base deployment.
- Worker and reconciler keep separate least-privilege database roles.
- Automatic mode is enabled only by an approved overlay or backend configuration after the release gate returns `ready: true`.
- Shutdown retains immutable revisions, workflows, attempts, safety signals, configurations, and audit history; legacy agents continue reading the restored active skill.

## Change workflow

1. Write a failing focused test before changing release logic.
2. Implement one narrow slice and run its focused test.
3. Run focused race tests for application and integration release cases.
4. Verify deployment and runbook bindings.
5. Run the full release suite:
   - `GOCACHE=/private/tmp/agent-memory-go-cache go test ./...`
   - `GOCACHE=/private/tmp/agent-memory-go-cache go vet ./...`
   - `make contracts-check`
   - `npm --prefix tools/agent-memory/mcp-server test`
   - `npm --prefix tools/agent-memory/dashboard test`
   - `npm --prefix tools/agent-memory/dashboard run typecheck`
   - `npm --prefix tools/agent-memory/dashboard audit --omit=dev`
   - `make build-with-dashboard`
6. Update Task 33 only after implementation evidence passes.

## Truthful final checkpoint

Leave production-launch checkboxes open when real staging receipts, an approved rollback SLO under ordinary and saturated load, or an accountable product signature are absent. State that automatic execution remains default-off. Never sign or fabricate those external receipts on behalf of the user or product owner.

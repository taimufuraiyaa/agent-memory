---
name: verify-local-alpha-evidence
description: Verify or restore the Agent Memory isolated local-alpha evidence boundary. Use when changing local/Floci Compose profiles, local OIDC identity and key-rotation/outage rehearsal, runtime trust-secret rotation, failed-configuration rollback, scratch operator-access drills, two-tenant isolation/timing evaluation, bounded retrieval load, credential-abuse/revocation drills, model-provider outage handling, deletion lifecycle evidence, edge/API readiness, telemetry rehearsal, lifecycle smoke, scratch database handling, backup/restore drills, image hardening/scans, evidence manifests, or the `saas-local-alpha-gate` Make target.
---

# Verify Local Alpha Evidence

## Preserve the trust boundary

- Read `.kiro/specs/saas-product-platform/requirements.md`, `design.md`, and
  `tasks.md`, especially R13, R14, R16 through R19, and P1.5 through P1.12.
- Treat `.local/evidence/` as development evidence only. Never mark staging,
  production, legal, privacy, staffing, billing, independent-review, or signed
  owner controls complete from a local package.
- Never dump, truncate, restore, or drop the persistent `agent_memory` product
  database. Destructive checks must use the isolated Compose project and
  `agent_memory_alpha_*` scratch databases created by the gate.
- Never capture container environment arrays, credentials, tokens, source
  bytes, extracted text, user identifiers, authorization headers, OIDC tokens,
  synthetic identity fields, JWKS bodies, private keys, or raw SQL rows in
  receipts. Also exclude runtime trust-secret values, assertion signatures,
  operator/ticket/source identifiers, corpus marker text, opaque IDs, and
  invalid-attempt logs. Provider prompts, evidence text, upstream response
  bodies, API keys, credential material, and deletion operation IDs are also
  forbidden.

## Validate changes

1. Run the fast contracts first:

   ```bash
   make saas-local-alpha-gate-test
   docker compose -p agent-memory-alpha-contract \
     -f deploy/saas/compose.yaml \
     -f deploy/saas/compose.floci.yaml \
     -f deploy/saas/compose.oidc.yaml \
     -f deploy/saas/compose.alpha.yaml config --quiet
   ```

2. For behavior changes, run the real isolated gate:

   ```bash
   make saas-local-alpha-gate
   ```

3. Verify the emitted sidecar from `.local/evidence/`:

   ```bash
   cd .local/evidence
   shasum -a 256 -c <run-id>.tar.gz.sha256
   ```

4. Inspect `manifest.json`. Require:
   - schema `agent-memory-local-alpha-evidence-v1`;
   - classification `local_development`;
   - `passed: true`;
   - one sorted check and file entry per receipt;
   - PostgreSQL, NATS, and Floci outage receipts showing fail-closed readiness
     and successful recovery;
   - edge telemetry and API/edge outage receipts showing internal metrics,
     edge liveness, fail-closed upstream readiness, and recovery.
   - OIDC rotation/outage receipts showing unknown-key refresh, old-key
     overlap, cached-token availability, invalid-token rejection,
     discovery fail-closed startup, and recovery without identity material.
   - runtime rotation, configuration rollback, and operator-access receipts
     showing old-assertion rejection, fail-closed invalid replacement,
     last-known-good recovery, independent approval, expiry denial, and audit
     production without their secret or identity material.
   - one two-tenant isolation/load receipt containing only aggregate sample,
     concurrency, error, p95, timing-delta, result/cache-leak, and generation
     counters. Require zero cross-tenant results/cache leaks/errors/generation
     calls and local search p95 below 800 ms.
   - one credential-leak receipt proving three denied events, one critical
     finding, independent approval, repository-backed revocation, post-revoke
     denial, and an audit event without printing any credential or identity.
   - one model-provider-outage receipt proving exactly two bounded HTTP
     attempts, an open circuit on the next request, preserved evidence, and no
     fabricated generation without printing prompt/evidence/upstream content.
   - one deletion-lifecycle receipt containing exactly the fixed four-source
     and one-account outcome. It must be derived from the passed lifecycle
     receipt and contain no deletion operation IDs.
   - the builder and verifier anchor all selected receipts to one opened,
     non-symlink evidence root, snapshot every selected receipt before hashing,
     and repeat that snapshot plus root-path identity validation before
     success. Root or receipt replacement during traversal must fail closed.

5. Confirm no isolated resources remain and the persistent stack is healthy:

   ```bash
   docker ps --filter 'name=agent-memory-alpha-'
   docker volume ls --filter 'name=agent-memory-alpha-'
   curl --fail --silent --show-error http://127.0.0.1:58081/_edge/health/live
   curl --fail --silent --show-error http://127.0.0.1:58081/_edge/health/ready
   ```

6. Run proportional code gates after source changes:

   ```bash
   GOCACHE=/tmp/agent-memory-go-cache go test ./...
   GOCACHE=/tmp/agent-memory-go-cache go vet ./...
   git diff --check
   ```

## Diagnose failures

- Keep the `.incomplete-<run-id>` directory; it is the intended diagnostic
  artifact. Read `INCOMPLETE` and only the failed receipt.
- Keep `run_check` strict: execute each check in a fresh
  `( set -euo pipefail; ... )` subshell and capture its status outside that
  subshell. Calling a check directly from an `if` condition disables Bash
  `errexit` inside the function and can create false passes.
- Re-resolve dynamically published API, edge, and OIDC host ports after a
  container restart. Compose may assign a different host port. Because every
  check runs in a child shell, call `resolve_stack_bindings` again in the parent
  immediately after any check that recreates containers.
- For the discovery-outage startup check, stop both API and OIDC and start the
  already validated API container with `docker start`. A Compose start can
  auto-start the declared OIDC dependency and invalidate the drill.
- If readiness stays successful during an outage, inspect
  `cmd/agent-memory-api/main.go` and `internal/saas/api/handler.go`. Liveness is
  process-only; readiness must check PostgreSQL, the quarantine bucket, and
  NATS under a bounded context and return a generic 503.
- If the edge drill fails, inspect `internal/saas/edge`, the edge service in
  `deploy/saas/compose.yaml`, and only the edge receipt. Edge liveness must stay
  available while `/_edge/health/ready` reflects API availability, and
  `/metrics` must remain unavailable through customer ingress.
- If profile restoration leaves the optional OIDC provider running, restore
  the ordinary Floci profile with orphan removal. The default persistent local
  profile uses development identity; local OIDC is opt-in rehearsal only.
- If the lifecycle signup returns HTTP 403 on a fresh database, verify the
  safe-default migration left `saas_launch_policy.signup_enabled=false` and
  that the smoke explicitly performs its audited, invitation-only transition
  for the singleton `internal_alpha` policy. Never change the migration default
  or bypass launch admission to make the local gate pass.
- If cleanup fails, resolve exact project and database names from `run_id`.
  Never broaden a cleanup target or operate on the default Compose project.
- If manifest verification fails, treat the receipt as mutated. Do not rebuild
  the manifest over changed files; rerun the gate under a new run ID.
- If a stable-root or selected-receipt snapshot check fails, preserve the
  incomplete run for diagnosis and retry only after the producer has stopped
  replacing the evidence directory or its receipts. Never auto-merge two run
  generations into one manifest.

## Completion evidence

Report the run ID, archive SHA-256, 27-check result, cleanup result, restored
ordinary-profile health, and tests executed. Keep all external-evidence
controls open unless their real accountable artifacts exist independently of
this package.

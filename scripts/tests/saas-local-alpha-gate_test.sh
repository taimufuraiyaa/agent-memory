#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
gate="$repo_dir/scripts/saas-local-alpha-gate.sh"
smoke="$repo_dir/scripts/saas-upload-smoke.sh"
makefile="$repo_dir/Makefile"

[[ -x "$gate" ]] || { echo "local alpha gate must be executable" >&2; exit 1; }
bash -n "$gate"
grep -q '^saas-local-alpha-gate:' "$makefile" || { echo "missing saas-local-alpha-gate target" >&2; exit 1; }
grep -q '^saas-local-alpha-gate-test:' "$makefile" || { echo "missing local alpha contract-test target" >&2; exit 1; }

required_contracts=(
  'saas-upload-smoke.sh'
  'saas-local-profiles_test.sh'
  'saas-kubernetes-release_test.sh'
  './evaluation/parity'
  'pg_dump'
  'pg_restore'
  'trivy image'
  'docker inspect'
  'dependency_outage_postgres'
  'dependency_outage_nats'
  'dependency_outage_floci'
  'edge_telemetry'
  'dependency_outage_api_edge'
  'oidc_rotation_outage'
  'runtime_secret_rotation'
  'runtime_configuration_rollback'
  'operator_access'
  'two_tenant_isolation_load'
  'credential_leak_revoke'
  'model_provider_outage'
  'deletion_lifecycle_evidence'
  './evaluation/isolation'
  './evaluation/operations'
  'TestTwoTenantAdversarialAndBoundedRetrievalLoad'
  'TestCredentialLeakDetectionAndRevocation'
  'TestModelProviderOutageFailsSafeWithEvidence'
  'TestOperatorInspectionAndTimeBoundIndependentElevation'
  'old trust assertion rejected'
  'invalid replacement configuration rejected'
  'last known-good configuration restored'
  'identity provider outage'
  'OIDC discovery unavailable'
  'AGENT_MEMORY_SMOKE_AUTH_TOKEN'
  'AGENT_MEMORY_SMOKE_COUNTRY_SECRET="$initial_edge_country_secret"'
  'compose.oidc.yaml'
  'resolve_oidc_url'
  'resolve_api_url'
  'operation="POST:/v1/signup"'
  '/_edge/health/live'
  '/_edge/health/ready'
  'exec -T worker wget'
  'exec -T reconciler wget'
  'http://localhost:9090/metrics'
  'readiness remained successful during'
  'readiness did not recover after'
  'agent-memory-local-evidence'
  'INCOMPLETE'
  'manifest.json'
  'tar -czf'
  'shasum -a 256'
)
for contract in "${required_contracts[@]}"; do
  grep -Fq -- "$contract" "$gate" || { printf 'missing local alpha gate contract: %s\n' "$contract" >&2; exit 1; }
done

isolation_test="$repo_dir/evaluation/isolation/postgres_test.go"
for metric in 'generation_calls=0' 'cross_tenant_results=0' 'cache_leaks=0' 'p95_ms='; do
  grep -Fq -- "$metric" "$isolation_test" || { echo "missing content-free isolation metric: $metric" >&2; exit 1; }
done

for recreating_check in runtime_secret_rotation runtime_configuration_rollback; do
  check_line="$(grep -n "^run_check $recreating_check " "$gate" | cut -d: -f1)"
  [[ -n "$check_line" ]] || { echo "missing recreating check: $recreating_check" >&2; exit 1; }
  next_line="$((check_line + 1))"
  [[ "$(sed -n "${next_line}p" "$gate")" == "resolve_stack_bindings" ]] || {
    echo "$recreating_check must refresh parent-process dynamic bindings after container recreation" >&2
    exit 1
  }
done

grep -Fq -- 'agent_memory_alpha_' "$gate" || { echo "scratch database prefix is required" >&2; exit 1; }
grep -Fq -- 'trap cleanup EXIT' "$gate" || { echo "scratch cleanup trap is required" >&2; exit 1; }
grep -Fq -- '( set -euo pipefail; "$@" )' "$gate" || { echo "checks must execute in a strict fail-closed subshell" >&2; exit 1; }
grep -Fq -- '.Config.User' "$gate" || { echo "hardening receipt must select safe inspect fields" >&2; exit 1; }
if grep -Fq -- '.Config.Env' "$gate"; then
  echo "container environments may contain credentials and must not enter evidence" >&2
  exit 1
fi
if grep -Eq -- "pg_dump.*[[:space:]]-d[[:space:]]+['\"]?agent_memory(['\"])?([[:space:]]|$)" "$gate"; then
  echo "the persistent product database must never be dumped" >&2
  exit 1
fi
if grep -Eq -- '(TRUNCATE|DROP DATABASE)[[:space:]]+agent_memory([;[:space:]]|$)' "$gate"; then
  echo "the persistent product database must never be truncated or dropped" >&2
  exit 1
fi
if grep -Fq 'export=$export_id' "$smoke" || grep -Fq 'account_deletion=$account_deletion_id' "$smoke"; then
  echo 'lifecycle evidence must not print opaque operation identifiers' >&2
  exit 1
fi
grep -Fq 'source_deletion_receipts=4 account_deletion_receipt=present' "$smoke" || {
  echo 'lifecycle evidence must report only bounded deletion outcomes' >&2
  exit 1
}

operations_test="$repo_dir/evaluation/operations/game_day_test.go"
for metric in \
  'credential_leak denied_events=3 findings=1 independent_approval=1 credential_revoked=1 post_revoke_denied=1' \
  'model_provider_outage upstream_attempts=2 circuit_open=1 evidence_preserved=1 fabricated_generation=0' \
  'deletion lifecycle evidence verified: source_receipts=4 account_receipt=present'; do
  grep -Fq -- "$metric" "$operations_test" "$gate" || {
    echo "missing content-free operations metric: $metric" >&2
    exit 1
  }
done

echo "local alpha evidence gate contract verified"

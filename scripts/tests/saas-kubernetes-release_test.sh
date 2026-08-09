#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
test_dir="$(mktemp -d)"
trap 'rm -rf "$test_dir"' EXIT
real_kubectl="$(command -v kubectl)"
mock_kubectl="$test_dir/kubectl"

cat > "$mock_kubectl" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" == "kustomize" ]]; then
  exec "$REAL_KUBECTL" "$@"
fi
printf '%s\n' "$*" >> "$KUBECTL_LOG"
if [[ "$*" == "config current-context" ]]; then
  printf 'staging-context\n'
  exit 0
fi
if [[ "$*" == *"get deployment/"*"jsonpath="* ]]; then
  printf '7'
  exit 0
fi
if [[ "$*" == *"create configmap agent-memory-release"* ]]; then
  printf 'apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: agent-memory-release\n'
fi
if [[ "$*" == *" apply -f "* ]]; then
  manifest="${*: -1}"
  if [[ -f "$manifest" ]] && grep -q '^kind: Job$' "$manifest"; then
    echo "PHASE migration" >> "$KUBECTL_LOG"
  elif [[ -f "$manifest" ]] && grep -q '^kind: Deployment$' "$manifest"; then
    echo "PHASE deployments" >> "$KUBECTL_LOG"
  fi
fi
if [[ "${FAIL_ROLLOUT:-}" == "1" && "$*" == *"rollout status deployment/agent-memory-api --timeout=10m"* && ! -f "$ROLLOUT_FAILED" ]]; then
  touch "$ROLLOUT_FAILED"
  exit 1
fi
if [[ "${FAIL_MIGRATION:-}" == "1" && "$*" == *"wait --for=condition=complete job/agent-memory-migrate"* ]]; then
  exit 1
fi
MOCK
chmod +x "$mock_kubectl"

digest="sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
export AGENT_MEMORY_API_IMAGE="registry.example/agent-memory-api@$digest"
export AGENT_MEMORY_WORKER_IMAGE="registry.example/agent-memory-worker@$digest"
export AGENT_MEMORY_RECONCILER_IMAGE="registry.example/agent-memory-reconciler@$digest"
export AGENT_MEMORY_MIGRATE_IMAGE="registry.example/agent-memory-migrate@$digest"
export AGENT_MEMORY_RELEASE_ID="release-test"
export KUBECTL="$mock_kubectl"
export REAL_KUBECTL="$real_kubectl"
export KUBECTL_LOG="$test_dir/kubectl.log"
export ROLLOUT_FAILED="$test_dir/rollout-failed"
export AGENT_MEMORY_RELEASE_RECEIPT_PATH="$test_dir/success.json"

"$repo_dir/scripts/saas-kubernetes-release.sh" staging >/dev/null
jq -e --arg image "$AGENT_MEMORY_API_IMAGE" '
  .schema == "agent-memory-kubernetes-release-receipt-v1" and
  .environment == "staging" and
  .namespace == "agent-memory-staging" and
  .kubernetes_context == "staging-context" and
  .release_id == "release-test" and
  .outcome == "passed" and
  .migration.outcome == "complete" and
  .rollouts.outcome == "healthy" and
  .rollback == {attempted:false,succeeded:false} and
  .images.api == $image and
  (.deployments | length) == 3 and
  ([.deployments[].revision] | all(. == "7"))
' "$AGENT_MEMORY_RELEASE_RECEIPT_PATH" >/dev/null
[[ "$(find "$AGENT_MEMORY_RELEASE_RECEIPT_PATH" -prune -perm 0600 -print)" == "$AGENT_MEMORY_RELEASE_RECEIPT_PATH" ]]
migration_line="$(grep -n 'PHASE migration' "$KUBECTL_LOG" | cut -d: -f1)"
deployment_line="$(grep -n 'PHASE deployments' "$KUBECTL_LOG" | cut -d: -f1)"
if [[ -z "$migration_line" || -z "$deployment_line" || "$migration_line" -ge "$deployment_line" ]]; then
  echo "migration did not precede deployment rollout" >&2
  exit 1
fi

: > "$KUBECTL_LOG"
export FAIL_ROLLOUT=1
export AGENT_MEMORY_RELEASE_RECEIPT_PATH="$test_dir/failed.json"
if "$repo_dir/scripts/saas-kubernetes-release.sh" staging >/dev/null 2>&1; then
  echo "failed rollout unexpectedly succeeded" >&2
  exit 1
fi
for deployment in agent-memory-api agent-memory-worker agent-memory-reconciler; do
  grep -q "rollout undo deployment/$deployment" "$KUBECTL_LOG"
done
jq -e '
  .outcome == "failed" and
  .migration.outcome == "complete" and
  .rollouts.outcome == "failed" and
  .rollback == {attempted:true,succeeded:true}
' "$AGENT_MEMORY_RELEASE_RECEIPT_PATH" >/dev/null

unset FAIL_ROLLOUT
export FAIL_MIGRATION=1
export AGENT_MEMORY_RELEASE_RECEIPT_PATH="$test_dir/migration-failed.json"
if "$repo_dir/scripts/saas-kubernetes-release.sh" staging >/dev/null 2>&1; then
  echo "failed migration unexpectedly succeeded" >&2
  exit 1
fi
jq -e '
  .outcome == "failed" and
  .migration.outcome == "failed" and
  .rollouts.outcome == "not_started" and
  .rollback == {attempted:false,succeeded:false}
' "$AGENT_MEMORY_RELEASE_RECEIPT_PATH" >/dev/null
unset FAIL_MIGRATION

valid_worker_image="$AGENT_MEMORY_WORKER_IMAGE"
export AGENT_MEMORY_WORKER_IMAGE="mutable:latest"
export AGENT_MEMORY_RELEASE_RECEIPT_PATH="$test_dir/invalid-image.json"
if "$repo_dir/scripts/saas-kubernetes-release.sh" staging >/dev/null 2>"$test_dir/invalid-image.err"; then
  echo "mutable image unexpectedly succeeded" >&2
  exit 1
fi
if [[ ! -f "$AGENT_MEMORY_RELEASE_RECEIPT_PATH" ]]; then
  sed -n '1,80p' "$test_dir/invalid-image.err" >&2
  echo "mutable-image failure did not emit a receipt" >&2
  exit 1
fi
jq -e '.outcome == "failed" and .migration.outcome == "not_started" and .rollback.attempted == false and .images.worker == "unavailable"' \
  "$AGENT_MEMORY_RELEASE_RECEIPT_PATH" >/dev/null
export AGENT_MEMORY_WORKER_IMAGE="$valid_worker_image"

valid_release_id="$AGENT_MEMORY_RELEASE_ID"
export AGENT_MEMORY_RELEASE_ID="invalid release id"
export AGENT_MEMORY_RELEASE_RECEIPT_PATH="$test_dir/invalid-release-id.json"
if "$repo_dir/scripts/saas-kubernetes-release.sh" staging >/dev/null 2>&1; then
  echo "invalid release ID unexpectedly succeeded" >&2
  exit 1
fi
jq -e '.outcome == "failed" and .migration.outcome == "not_started" and .release_id == "invalid"' \
  "$AGENT_MEMORY_RELEASE_RECEIPT_PATH" >/dev/null
export AGENT_MEMORY_RELEASE_ID="$valid_release_id"

printf '{}\n' > "$test_dir/existing.json"
export AGENT_MEMORY_RELEASE_RECEIPT_PATH="$test_dir/existing.json"
if "$repo_dir/scripts/saas-kubernetes-release.sh" staging >/dev/null 2>&1; then
  echo "existing release receipt was overwritten" >&2
  exit 1
fi
printf '{}\n' > "$test_dir/target.json"
ln -s "$test_dir/target.json" "$test_dir/symlink.json"
export AGENT_MEMORY_RELEASE_RECEIPT_PATH="$test_dir/symlink.json"
if "$repo_dir/scripts/saas-kubernetes-release.sh" staging >/dev/null 2>&1; then
  echo "symlink release receipt was accepted" >&2
  exit 1
fi

if grep -Eq 'get secret .*-(o|output)(=|[[:space:]])' "$repo_dir/scripts/saas-kubernetes-release.sh"; then
  echo "release evidence must never retrieve Secret representations" >&2
  exit 1
fi

workflow="$repo_dir/.github/workflows/saas-release.yml"
grep -Fq 'AGENT_MEMORY_RELEASE_RECEIPT_PATH=' "$workflow" || {
  echo "hosted release must configure the staging receipt path" >&2
  exit 1
}
grep -Fq 'actions/upload-artifact@' "$workflow" || {
  echo "hosted release must upload the staging receipt" >&2
  exit 1
}
grep -Fq 'if: always()' "$workflow" || {
  echo "failed staging receipts must still be uploaded" >&2
  exit 1
}
grep -Fq 'if-no-files-found: error' "$workflow" || {
  echo "missing staging receipts must fail the evidence upload" >&2
  exit 1
}

echo "Kubernetes release ordering and rollback tests passed"

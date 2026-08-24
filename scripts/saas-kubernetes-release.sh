#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 1 || ( "$1" != "staging" && "$1" != "production" ) ]]; then
  echo "usage: $0 staging|production" >&2
  exit 2
fi

environment="$1"
namespace="agent-memory-$environment"
repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
base_dir="$repo_dir/deploy/saas/kubernetes/base"
overlay_dir="$repo_dir/deploy/saas/kubernetes/overlays/$environment"
release_id="${AGENT_MEMORY_RELEASE_ID:-manual-$(date -u +%Y%m%dT%H%M%SZ)}"
kubectl_cmd="${KUBECTL:-kubectl}"
receipt_path="${AGENT_MEMORY_RELEASE_RECEIPT_PATH:-}"
started_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
kubernetes_context="unavailable"
migration_outcome="not_started"
rollouts_outcome="not_started"
rollback_attempted=false
rollback_succeeded=false
deployments_applied=false
receipt_enabled=false

require_digest() {
  local variable="$1"
  local image="${!variable:-}"
  if [[ ! "$image" =~ ^[a-zA-Z0-9._:/-]+@sha256:[a-f0-9]{64}$ ]]; then
    echo "$variable must be an immutable image@sha256 digest" >&2
    return 2
  fi
}

prepare_receipt() {
  [[ -n "$receipt_path" ]] || return 0
  local directory
  directory="$(dirname "$receipt_path")"
  if [[ ! -d "$directory" || -L "$directory" || -e "$receipt_path" || -L "$receipt_path" ]]; then
    echo "release receipt destination must be a new path in a non-symlink directory" >&2
    exit 2
  fi
  receipt_enabled=true
}

deployment_metadata() {
  local receipt_outcome="$1"
  local result='[]' deployment revision
  for deployment in agent-memory-api agent-memory-worker agent-memory-reconciler; do
    revision="$({ "$kubectl_cmd" -n "$namespace" get "deployment/$deployment" -o 'jsonpath={.metadata.annotations.deployment\.kubernetes\.io/revision}'; } 2>/dev/null || true)"
    if [[ ! "$revision" =~ ^[1-9][0-9]*$ ]]; then
      if [[ "$receipt_outcome" == "passed" ]]; then
        echo "healthy release is missing Deployment revision metadata" >&2
        return 1
      fi
      revision="unavailable"
    fi
    result="$(jq -cn --argjson current "$result" --arg name "$deployment" --arg revision "$revision" '$current + [{name:$name,revision:$revision}]')"
  done
  printf '%s' "$result"
}

write_receipt() {
  local outcome="$1"
  [[ "$receipt_enabled" == true ]] || return 0
  local directory filename temporary completed_at deployments api_image worker_image reconciler_image migrate_image
  directory="$(dirname "$receipt_path")"
  filename="$(basename "$receipt_path")"
  completed_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  deployments="$(deployment_metadata "$outcome")"
  api_image="$(safe_image "${AGENT_MEMORY_API_IMAGE:-}")"
  worker_image="$(safe_image "${AGENT_MEMORY_WORKER_IMAGE:-}")"
  reconciler_image="$(safe_image "${AGENT_MEMORY_RECONCILER_IMAGE:-}")"
  migrate_image="$(safe_image "${AGENT_MEMORY_MIGRATE_IMAGE:-}")"
  temporary="$(mktemp "$directory/.${filename}.tmp.XXXXXX")"
  chmod 0600 "$temporary"
  if ! jq -n \
    --arg schema "agent-memory-kubernetes-release-receipt-v1" \
    --arg environment "$environment" \
    --arg namespace "$namespace" \
    --arg kubernetes_context "$kubernetes_context" \
    --arg release_id "$release_id" \
    --arg started_at "$started_at" \
    --arg completed_at "$completed_at" \
    --arg outcome "$outcome" \
    --arg api_image "$api_image" \
    --arg worker_image "$worker_image" \
    --arg reconciler_image "$reconciler_image" \
    --arg migrate_image "$migrate_image" \
    --arg migration_outcome "$migration_outcome" \
    --arg rollouts_outcome "$rollouts_outcome" \
    --argjson deployments "$deployments" \
    --argjson rollback_attempted "$rollback_attempted" \
    --argjson rollback_succeeded "$rollback_succeeded" \
    '{schema:$schema,environment:$environment,namespace:$namespace,kubernetes_context:$kubernetes_context,
      release_id:$release_id,started_at:$started_at,completed_at:$completed_at,outcome:$outcome,
      images:{api:$api_image,worker:$worker_image,reconciler:$reconciler_image,migrate:$migrate_image},
      migration:{outcome:$migration_outcome},rollouts:{outcome:$rollouts_outcome},deployments:$deployments,
      rollback:{attempted:$rollback_attempted,succeeded:$rollback_succeeded}}' > "$temporary"; then
    rm -f -- "$temporary"
    return 1
  fi
  if ! validate_receipt "$temporary"; then
    rm -f -- "$temporary"
    echo "release receipt failed its content-free schema contract" >&2
    return 1
  fi
  if ! mv -n -- "$temporary" "$receipt_path" || [[ -e "$temporary" ]]; then
    rm -f -- "$temporary"
    echo "release receipt destination changed before publication" >&2
    return 1
  fi
}

validate_receipt() {
  jq -e '
    . as $receipt |
    keys == ["completed_at","deployments","environment","images","kubernetes_context","migration","namespace","outcome","release_id","rollback","rollouts","schema","started_at"] and
    .schema == "agent-memory-kubernetes-release-receipt-v1" and
    (.environment == "staging" or .environment == "production") and
    .namespace == ("agent-memory-" + .environment) and
    (.kubernetes_context | type == "string" and length > 0 and length <= 253) and
    (.release_id | test("^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$")) and
    (.started_at | fromdateiso8601? != null) and
    (.completed_at | fromdateiso8601? != null) and
    (.outcome == "passed" or .outcome == "failed") and
    (.images | keys == ["api","migrate","reconciler","worker"]) and
    ([.images[]] | all(. == "unavailable" or test("^[A-Za-z0-9._:/-]+@sha256:[a-f0-9]{64}$"))) and
    (.migration | keys == ["outcome"]) and
    (["not_started","failed","complete"] | index($receipt.migration.outcome) != null) and
    (.rollouts | keys == ["outcome"]) and
    (["not_started","failed","healthy"] | index($receipt.rollouts.outcome) != null) and
    (.deployments | type == "array" and length == 3) and
    ([.deployments[].name] == ["agent-memory-api","agent-memory-worker","agent-memory-reconciler"]) and
    ([.deployments[].revision] | all(. == "unavailable" or test("^[1-9][0-9]*$"))) and
    (.rollback | keys == ["attempted","succeeded"]) and
    (.rollback.attempted | type == "boolean") and
    (.rollback.succeeded | type == "boolean") and
    ((.rollback.succeeded | not) or .rollback.attempted) and
    (if .outcome == "passed" then
       .migration.outcome == "complete" and .rollouts.outcome == "healthy" and
       .rollback == {attempted:false,succeeded:false} and
       ([.images[]] | all(. != "unavailable")) and
       ([.deployments[].revision] | all(. != "unavailable"))
     else true end)
  ' "$1" >/dev/null
}

safe_image() {
  local image="$1"
  if [[ "$image" =~ ^[a-zA-Z0-9._:/-]+@sha256:[a-f0-9]{64}$ ]]; then
    printf '%s' "$image"
  else
    printf 'unavailable'
  fi
}

rollback() {
  echo "rollout failed; restoring previous deployment revisions" >&2
  rollback_attempted=true
  rollback_succeeded=true
  for deployment in agent-memory-api agent-memory-worker agent-memory-reconciler; do
    if ! "$kubectl_cmd" -n "$namespace" rollout undo "deployment/$deployment"; then
      rollback_succeeded=false
    fi
  done
  for deployment in agent-memory-api agent-memory-worker agent-memory-reconciler; do
    if ! "$kubectl_cmd" -n "$namespace" rollout status "deployment/$deployment" --timeout=5m; then
      rollback_succeeded=false
    fi
  done
}

on_error() {
  local status=$?
  trap - ERR
  set +e
  if [[ "$deployments_applied" == true ]]; then
    rollback
  fi
  write_receipt failed
  exit "$status"
}

prepare_receipt
trap on_error ERR
command -v jq >/dev/null
if [[ ! "$release_id" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$ ]]; then
  echo "AGENT_MEMORY_RELEASE_ID is invalid" >&2
  release_id="invalid"
  false
fi
for variable in AGENT_MEMORY_API_IMAGE AGENT_MEMORY_WORKER_IMAGE AGENT_MEMORY_RECONCILER_IMAGE AGENT_MEMORY_MIGRATE_IMAGE; do
  require_digest "$variable"
done
command -v "$kubectl_cmd" >/dev/null
resolved_context="$($kubectl_cmd config current-context)"
kubernetes_context="$resolved_context"
[[ -n "$kubernetes_context" && ${#kubernetes_context} -le 253 && "$kubernetes_context" != *$'\n'* ]]
"$repo_dir/scripts/validate-saas-kubernetes.sh"

deployment_manifest="$(mktemp)"
migration_manifest="$(mktemp)"
release_manifest="$(mktemp)"
cleanup() { rm -f "$deployment_manifest" "$migration_manifest" "$release_manifest"; }
trap cleanup EXIT

sed \
  -e "s|agent-memory-api@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa|$AGENT_MEMORY_API_IMAGE|" \
  -e "s|agent-memory-worker@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb|$AGENT_MEMORY_WORKER_IMAGE|" \
  -e "s|agent-memory-reconciler@sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc|$AGENT_MEMORY_RECONCILER_IMAGE|" \
  "$base_dir/deployments.yaml" > "$deployment_manifest"
sed \
  -e "s|agent-memory-migrate@sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd|$AGENT_MEMORY_MIGRATE_IMAGE|" \
  "$base_dir/migration-job.yaml" > "$migration_manifest"

"$kubectl_cmd" apply -f "$overlay_dir/namespace.yaml"
"$kubectl_cmd" -n "$namespace" apply -f "$overlay_dir/runtime-config.yaml"
"$kubectl_cmd" -n "$namespace" apply -f "$base_dir/accounts.yaml"
"$kubectl_cmd" -n "$namespace" apply -f "$base_dir/service.yaml"
"$kubectl_cmd" -n "$namespace" apply -f "$base_dir/network-policy.yaml"

for secret in agent-memory-api-secrets agent-memory-worker-secrets agent-memory-reconciler-secrets agent-memory-migration-secrets; do
  "$kubectl_cmd" -n "$namespace" get secret "$secret" >/dev/null
done

"$kubectl_cmd" -n "$namespace" delete job agent-memory-migrate --ignore-not-found --wait=true
"$kubectl_cmd" -n "$namespace" apply -f "$migration_manifest"
migration_outcome="failed"
"$kubectl_cmd" -n "$namespace" wait --for=condition=complete job/agent-memory-migrate --timeout=10m
migration_outcome="complete"

deployments_applied=true
"$kubectl_cmd" -n "$namespace" apply -f "$deployment_manifest"
rollouts_outcome="failed"
for deployment in agent-memory-api agent-memory-worker agent-memory-reconciler; do
  "$kubectl_cmd" -n "$namespace" rollout status "deployment/$deployment" --timeout=10m
done
rollouts_outcome="healthy"

"$kubectl_cmd" -n "$namespace" create configmap agent-memory-release \
  --from-literal=release-id="$release_id" \
  --from-literal=api-image="$AGENT_MEMORY_API_IMAGE" \
  --from-literal=worker-image="$AGENT_MEMORY_WORKER_IMAGE" \
  --from-literal=reconciler-image="$AGENT_MEMORY_RECONCILER_IMAGE" \
  --from-literal=migrate-image="$AGENT_MEMORY_MIGRATE_IMAGE" \
  --dry-run=client -o yaml > "$release_manifest"
"$kubectl_cmd" -n "$namespace" apply -f "$release_manifest"

write_receipt passed
trap cleanup EXIT
echo "release $release_id is healthy in $namespace"

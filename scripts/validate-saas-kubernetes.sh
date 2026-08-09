#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
base_dir="$repo_dir/deploy/saas/kubernetes"
kubectl_cmd="${KUBECTL:-kubectl}"

for environment in staging production; do
  rendered="$(mktemp)"
  trap 'rm -f "$rendered"' EXIT
  "$kubectl_cmd" kustomize "$base_dir/overlays/$environment" > "$rendered"

  expected_namespace="agent-memory-$environment"
  grep -q "namespace: $expected_namespace" "$rendered"
  grep -q 'name: default-deny' "$rendered"
  grep -q 'readOnlyRootFilesystem: true' "$rendered"
  grep -q 'runAsNonRoot: true' "$rendered"
  grep -q 'automountServiceAccountToken: false' "$rendered"

  if awk '/^[[:space:]]+image:/{print $2}' "$rendered" | grep -Ev '@sha256:[a-f0-9]{64}$' | grep -q .; then
    echo "$environment contains a mutable image reference" >&2
    exit 1
  fi
  if grep -q '^kind: Secret$' "$rendered"; then
    echo "$environment renders committed secret material" >&2
    exit 1
  fi
  if grep -Eq '^  type: (LoadBalancer|NodePort)$' "$rendered"; then
    echo "$environment exposes a workload directly" >&2
    exit 1
  fi
  rm -f "$rendered"
  trap - EXIT
done

accounts="$("$kubectl_cmd" kustomize "$base_dir/overlays/staging" | awk '/^kind: ServiceAccount$/{found=1} found && /^  name: agent-memory-/{print $2; found=0}')"
for account in agent-memory-api agent-memory-worker agent-memory-reconciler agent-memory-migration; do
  grep -qx "$account" <<<"$accounts"
done

echo "Kubernetes workload policy validation passed"

#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
makefile="$repo_dir/Makefile"
compose_file="$repo_dir/deploy/saas/compose.yaml"
floci_file="$repo_dir/deploy/saas/compose.floci.yaml"
oidc_file="$repo_dir/deploy/saas/compose.oidc.yaml"
floci_dockerfile="$repo_dir/deploy/saas/Dockerfile.floci"
s3_init_file="$repo_dir/cmd/agent-memory-s3-init/main.go"

for file in "$makefile" "$compose_file" "$floci_file" "$oidc_file" "$floci_dockerfile" "$s3_init_file"; do
  [[ -f "$file" ]] || { echo "missing local deployment contract: $file" >&2; exit 1; }
done

grep -q '^saas-local-up:' "$makefile" || { echo 'missing saas-local-up target' >&2; exit 1; }
grep -q '^saas-floci-up:' "$makefile" || { echo 'missing saas-floci-up target' >&2; exit 1; }
grep -q '^saas-floci-oidc-up:' "$makefile" || { echo 'missing saas-floci-oidc-up target' >&2; exit 1; }
grep -q '^saas-local-down:' "$makefile" || { echo 'missing saas-local-down target' >&2; exit 1; }
sed -n '/^saas-floci-up:/,/^$/p' "$makefile" | grep -q -- '--remove-orphans' || { echo 'switching back to Floci must remove optional-profile orphans' >&2; exit 1; }

grep -q 'Dockerfile.floci' "$floci_file" || { echo 'Floci must use the hardened local build' >&2; exit 1; }
grep -q 'floci/floci:1\.6\.0@sha256:eab36252ea43a4a73928423f0372219052c5c6f87207f6c4754db14b91d6ed30' "$floci_dockerfile" || { echo 'Floci base image must be digest pinned' >&2; exit 1; }
grep -q 'microdnf upgrade' "$floci_dockerfile" || { echo 'Floci OS packages must be updated' >&2; exit 1; }
grep -q 'microdnf clean all' "$floci_dockerfile" || { echo 'Floci package metadata must be removed' >&2; exit 1; }
grep -q 'rm -f /usr/local/bin/gosu' "$floci_dockerfile" || { echo 'unused vulnerable gosu must be removed' >&2; exit 1; }
grep -q '^USER 1001:0$' "$floci_dockerfile" || { echo 'Floci must run directly as its non-root user' >&2; exit 1; }
if grep -q -- '-compat' "$floci_file"; then
  echo 'vulnerable Floci compat image must not be used' >&2
  exit 1
fi
grep -q 'agent-memory-s3-init' "$floci_file" || { echo 'Floci must use the project-owned S3 initializer' >&2; exit 1; }
grep -q 'http://minio:4566' "$floci_file" || { echo 'workloads must use the Floci S3 endpoint' >&2; exit 1; }
grep -q 'floci-data:/data' "$floci_file" || { echo 'Floci must use isolated persistent storage' >&2; exit 1; }
grep -q 'read_only: true' "$floci_file" || { echo 'Floci and its initializer must use read-only root filesystems' >&2; exit 1; }
grep -q 'no-new-privileges:true' "$floci_file" || { echo 'Floci must prevent privilege escalation' >&2; exit 1; }
grep -q '127.0.0.1:8080:8080' "$compose_file" || { echo 'local API must bind only to loopback' >&2; exit 1; }
grep -q '^  edge:' "$compose_file" || { echo 'local customer ingress must use a separate edge process' >&2; exit 1; }
grep -q '127.0.0.1:58081:8081' "$compose_file" || { echo 'local edge must use its reserved loopback host port' >&2; exit 1; }
grep -q 'AGENT_MEMORY_EDGE_UPSTREAM: http://api:8080' "$compose_file" || { echo 'local edge must use the internal API endpoint' >&2; exit 1; }
grep -Fq '${AGENT_MEMORY_EDGE_COUNTRY_SECRET:-local-edge-country-signing-secret-32}' "$compose_file" || { echo 'API and edge trust secret must support runtime rotation with a safe local default' >&2; exit 1; }
[[ "$(grep -Fc '${AGENT_MEMORY_EDGE_COUNTRY_SECRET:-local-edge-country-signing-secret-32}' "$compose_file")" -eq 2 ]] || { echo 'API and edge must consume the same rotatable trust secret contract' >&2; exit 1; }
grep -q 'http://localhost:8081/_edge/health/ready' "$compose_file" || { echo 'local edge must expose upstream readiness' >&2; exit 1; }
grep -q '^  oidc:' "$oidc_file" || { echo 'optional local OIDC provider is required' >&2; exit 1; }
grep -q 'AGENT_MEMORY_IDENTITY_MODE: oidc' "$oidc_file" || { echo 'OIDC overlay must select explicit API identity mode' >&2; exit 1; }
grep -q 'AGENT_MEMORY_OIDC_ISSUER: http://oidc:8082' "$oidc_file" || { echo 'OIDC overlay must use internal discovery' >&2; exit 1; }
grep -q '127.0.0.1:58082:8082' "$oidc_file" || { echo 'local OIDC provider must use reserved loopback ingress' >&2; exit 1; }
grep -q 'read_only: true' "$oidc_file" || { echo 'local OIDC provider must use a read-only root filesystem' >&2; exit 1; }
grep -q 'no-new-privileges:true' "$oidc_file" || { echo 'local OIDC provider must prevent privilege escalation' >&2; exit 1; }
grep -q 'agent-memory-audit.*objectLock: true' "$s3_init_file" || { echo 'Floci audit bucket must enable Object Lock' >&2; exit 1; }
grep -q '^  floci-volume-init:' "$floci_file" || { echo 'Floci volume ownership initializer is required' >&2; exit 1; }
grep -q '"1001:1001"' "$floci_file" || { echo 'Floci volume must be writable by its non-root runtime' >&2; exit 1; }

if grep -Eq 'floci-data:/data' "$compose_file"; then
  echo 'default MinIO and Floci storage must remain isolated' >&2
  exit 1
fi

render_log="$(mktemp)"
trap 'rm -f -- "$render_log"' EXIT
if ! docker compose -f "$compose_file" -f "$floci_file" config >"$render_log" 2>&1; then
  cat "$render_log" >&2
  exit 1
fi
docker compose -f "$compose_file" -f "$floci_file" -f "$oidc_file" config --quiet
if grep -q 'variable is not set' "$render_log"; then
  cat "$render_log" >&2
  echo 'Compose must not interpolate Floci initializer shell variables' >&2
  exit 1
fi
if grep -Eq '^[[:space:]]*- mode=1777$' "$render_log"; then
  cat "$render_log" >&2
  echo 'tmpfs mode must remain attached to an absolute container mount path' >&2
  exit 1
fi
echo 'local Compose and Floci deployment contracts verified'

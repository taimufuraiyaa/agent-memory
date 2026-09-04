#!/bin/sh
set -eu

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$repository_root"

policy=tools/graphrag-certification/upgrade-policy.yaml
test -s "$policy"
current_version=$(sed -n 's/^current_version: //p' "$policy")
adapter_version=$(sed -n 's/^adapter_version: //p' "$policy")
test -n "$current_version"
test -n "$adapter_version"
grep -Fq "\"graphrag==$current_version\"" tools/graphrag-adapter/pyproject.toml
grep -Fq "version = \"$current_version\"" tools/graphrag-adapter/uv.lock
test -s "tools/graphrag-adapter/wheelhouse/graphrag-$current_version-py3-none-any.whl"
test -s tools/graphrag-adapter/wheelhouse/requirements.txt
grep -A2 -E "^graphrag==$current_version \\\\$" tools/graphrag-adapter/wheelhouse/requirements.txt | grep -Eq -- "--hash=sha256:[0-9a-f]{64}"

if command -v uv >/dev/null 2>&1; then
  (
    cd tools/graphrag-adapter
    uv lock --check
    uv run --frozen --offline pytest
  )
else
  test "${GRAPHRAG_UPGRADE_POLICY_ONLY:-0}" = "1"
  tools/graphrag-adapter/.venv/bin/python -m pytest tools/graphrag-adapter/tests
fi
go test ./internal/contracts ./internal/validation ./internal/saas/graphworker ./internal/saas/graphindex -run 'Graph' -count=1
make graphrag-evaluate

if test "${GRAPHRAG_UPGRADE_POLICY_ONLY:-0}" = "1"; then
  echo "GraphRAG upgrade policy and deterministic compatibility checks passed; release evidence was intentionally not certified."
  exit 0
fi

: "${GRAPHRAG_UPGRADE_REPORT:?signed canary and rollback report path is required}"
: "${GRAPHRAG_UPGRADE_REPORT_SIGNATURE:?report signature path is required}"
: "${GRAPHRAG_UPGRADE_PUBLIC_KEY:?trusted report public key path is required}"
: "${GRAPHRAG_CANDIDATE_IMAGE:?immutable candidate image digest is required}"
: "${COSIGN_PUBLIC_KEY:?candidate image public key path is required}"
case "$GRAPHRAG_CANDIDATE_IMAGE" in *@sha256:*) ;; *) echo "candidate image must be digest-pinned" >&2; exit 1 ;; esac
test -s "$GRAPHRAG_UPGRADE_REPORT"
test -s "$GRAPHRAG_UPGRADE_REPORT_SIGNATURE"
command -v cosign >/dev/null
cosign verify-blob --key "$GRAPHRAG_UPGRADE_PUBLIC_KEY" --signature "$GRAPHRAG_UPGRADE_REPORT_SIGNATURE" "$GRAPHRAG_UPGRADE_REPORT"
cosign verify --key "$COSIGN_PUBLIC_KEY" "$GRAPHRAG_CANDIDATE_IMAGE"
python3 - "$GRAPHRAG_UPGRADE_REPORT" "$current_version" <<'PY'
import json, re, sys
path, current = sys.argv[1:]
with open(path, "rb") as handle:
    report = json.load(handle)
required = {
    "schema", "candidate_version", "prior_image_digest", "candidate_image_digest",
    "sbom_review", "license_review", "vulnerability_review", "schema_golden",
    "determinism", "full_canary", "incremental_canary", "normalized_diff",
    "shadow_evaluation", "deployment_canary", "rollback_image_restored",
    "rollback_active_revision_restored", "approved",
}
if set(report) != required or report["schema"] != "agent-memory-graphrag-upgrade-report/v1":
    raise SystemExit("upgrade report schema is incomplete or contains unknown fields")
for key in required - {"schema", "candidate_version", "prior_image_digest", "candidate_image_digest"}:
    if report[key] is not True:
        raise SystemExit(f"upgrade report control failed: {key}")
digest = re.compile(r"^sha256:[0-9a-f]{64}$")
if not digest.fullmatch(report["prior_image_digest"]) or not digest.fullmatch(report["candidate_image_digest"]) or report["prior_image_digest"] == report["candidate_image_digest"]:
    raise SystemExit("upgrade report image digests are invalid")
if not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+", report["candidate_version"]) or report["candidate_version"] == current:
    raise SystemExit("upgrade report does not describe a version change")
PY

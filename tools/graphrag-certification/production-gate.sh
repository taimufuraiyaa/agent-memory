#!/bin/sh
set -eu

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$repository_root"

for runbook in \
  docs/operations/graphrag.md \
  docs/operations/graphrag-incident.md \
  docs/operations/graphrag-removal.md \
  docs/release/graphrag-production-gate.md
do
  test -s "$runbook"
done

grep -Fq '"graphrag==3.1.2"' tools/graphrag-adapter/pyproject.toml
grep -Fq 'version = "3.1.2"' tools/graphrag-adapter/uv.lock
python3 tools/graphrag-certification/test_production_gate.py
make graphrag-evaluate
make graphrag-chaos-test graphrag-security-test graphrag-recovery-test graphrag-capacity-test
GRAPHRAG_UPGRADE_POLICY_ONLY=1 make graphrag-upgrade-certify

if test "${GRAPHRAG_PRODUCTION_POLICY_ONLY:-0}" = "1"; then
  echo "GraphRAG repository controls pass; external production certification was intentionally not approved."
  exit 0
fi

: "${GRAPHRAG_PRODUCTION_REPORT:?signed production report path is required}"
: "${GRAPHRAG_PRODUCTION_REPORT_SIGNATURE:?production report signature path is required}"
: "${GRAPHRAG_PRODUCTION_PUBLIC_KEY:?trusted production-approval public key path is required}"
test -s "$GRAPHRAG_PRODUCTION_REPORT"
test -s "$GRAPHRAG_PRODUCTION_REPORT_SIGNATURE"
test -s "$GRAPHRAG_PRODUCTION_PUBLIC_KEY"
command -v cosign >/dev/null
cosign verify-blob \
  --key "$GRAPHRAG_PRODUCTION_PUBLIC_KEY" \
  --signature "$GRAPHRAG_PRODUCTION_REPORT_SIGNATURE" \
  "$GRAPHRAG_PRODUCTION_REPORT"
python3 tools/graphrag-certification/production_gate.py \
  "$GRAPHRAG_PRODUCTION_REPORT" \
  "$(git rev-parse HEAD)"

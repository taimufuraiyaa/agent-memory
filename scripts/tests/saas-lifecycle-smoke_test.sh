#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
smoke_script="$repo_dir/scripts/saas-upload-smoke.sh"

bash -n "$smoke_script"

required_contracts=(
  'application/pdf'
  'application/epub+zip'
  'text/markdown'
  'text/plain'
  '/v1/source-queries'
  '/v1/memory-proposals'
  '/v1/exports'
  '/v1/attestations/rights'
  'DELETE "$api_url/v1/sources/'
  'DELETE "$api_url/v1/account"'
  'source_state" != "ready"'
)

for contract in "${required_contracts[@]}"; do
  if ! grep -Fq -- "$contract" "$smoke_script"; then
    printf 'missing lifecycle smoke contract: %s\n' "$contract" >&2
    exit 1
  fi
done

if grep -Fq -- '-c "SELECT set_config' "$smoke_script"; then
  echo "psql variables are not expanded reliably inside -c commands; use stdin SQL" >&2
  exit 1
fi

echo "SaaS lifecycle smoke contract passed"

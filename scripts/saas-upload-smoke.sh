#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
compose_file="$repo_dir/deploy/saas/compose.yaml"
compose_project="${AGENT_MEMORY_SMOKE_COMPOSE_PROJECT:-}"
api_url="${AGENT_MEMORY_SMOKE_API_URL:-http://127.0.0.1:8080}"
auth_token="${AGENT_MEMORY_SMOKE_AUTH_TOKEN:-local-development-bearer}"
email="${AGENT_MEMORY_SMOKE_EMAIL:-member@local.invalid}"
country="${AGENT_MEMORY_SMOKE_COUNTRY:-VN}"
country_secret="${AGENT_MEMORY_SMOKE_COUNTRY_SECRET:-local-edge-country-signing-secret-32}"
invitation="local-smoke-$(date -u +%Y%m%d)"
auth_header="Authorization: Bearer $auth_token"

compose=(docker compose)
if [[ -n "$compose_project" ]]; then
  [[ "$compose_project" =~ ^[a-z0-9][a-z0-9_-]{1,62}$ ]] || { echo "invalid smoke Compose project" >&2; exit 1; }
  compose+=(-p "$compose_project")
fi
compose+=(-f "$compose_file")

for command in curl jq openssl shasum docker zip go; do
  command -v "$command" >/dev/null
done

invite_hash="$(printf '%s' "$invitation" | shasum -a 256 | awk '{print $1}')"
normalized_email="$(printf '%s' "$email" | tr '[:upper:]' '[:lower:]')"
email_hash="$(printf '%s' "$normalized_email" | shasum -a 256 | awk '{print $1}')"
"${compose[@]}" exec -T postgres psql -U agent_memory -d agent_memory -v ON_ERROR_STOP=1 \
  -v invite_hash="$invite_hash" -v email_hash="$email_hash" <<'SQL' >/dev/null
INSERT INTO saas_launch_invitations(
  token_sha256,email_sha256,state,max_uses,reserved_uses,completed_uses,
  expires_at,created_by,created_at
) VALUES(
  :'invite_hash',:'email_hash','active',100,0,0,
  clock_timestamp()+interval '1 hour','local-smoke',clock_timestamp()
) ON CONFLICT(token_sha256) DO UPDATE
SET state='active',expires_at=EXCLUDED.expires_at,max_uses=100;

DO $$
BEGIN
  UPDATE saas_launch_policy
  SET signup_enabled = true,
      invitation_required = true,
      updated_by = 'local-smoke',
      reason_code = 'isolated_local_smoke',
      updated_at = clock_timestamp()
  WHERE singleton = true
    AND phase = 'internal_alpha';
  IF NOT FOUND THEN
    RAISE EXCEPTION 'isolated local smoke requires the internal-alpha launch policy';
  END IF;
END
$$;
SQL

timestamp="$(date +%s)"
signature="$(printf '%s\n%s' "$country" "$timestamp" | openssl dgst -sha256 -hmac "$country_secret" -hex | awk '{print $2}')"
signup_payload="$(jq -cn --arg invitation "$invitation" '{invitation_token:$invitation,age_confirmed:true}')"
signup_response="$(curl --fail-with-body --silent --show-error -X POST "$api_url/v1/signup" \
  -H "$auth_header" -H 'Content-Type: application/json' \
  -H "X-Agent-Memory-Country: $country" \
  -H "X-Agent-Memory-Country-Timestamp: $timestamp" \
  -H "X-Agent-Memory-Country-Signature: $signature" \
  --data "$signup_payload")"
workspace_id="$(jq -er '.data.workspace_id' <<<"$signup_response")"

attestation_response="$(curl --fail-with-body --silent --show-error "$api_url/v1/attestations/rights" -H "$auth_header")"
policy_version="$(jq -er '.data.policy.version' <<<"$attestation_response")"
statement_ids="$(jq -c '[.data.policy.statements[].id]' <<<"$attestation_response")"
attestation_payload="$(jq -cn --arg policy "$policy_version" --argjson statements "$statement_ids" '{policy_version:$policy,accepted_statement_ids:$statements}')"
curl --fail-with-body --silent --show-error -X POST "$api_url/v1/attestations/rights" \
  -H "$auth_header" -H 'Content-Type: application/json' --data "$attestation_payload" >/dev/null

fixture_dir="$(mktemp -d)"
cleanup() {
  rm -rf -- "$fixture_dir"
}
trap cleanup EXIT

portable_db="$fixture_dir/portable.db"
portable_bundle="$fixture_dir/standalone.ampb2"
portable_passphrase='local portable smoke passphrase'
(cd "$repo_dir" && go run ./cmd/agent-memory write \
  --workspace portable-smoke --db "$portable_db" --type semantic \
  --content 'Portable migration smoke memory.' >/dev/null)
printf '%s\n' "$portable_passphrase" | (cd "$repo_dir" && go run ./cmd/agent-memory export \
  --workspace portable-smoke --db "$portable_db" --export-format portable \
  --out "$portable_bundle" --passphrase-stdin >/dev/null)
local_bundle_hash_before="$(shasum -a 256 "$portable_db" | awk '{print $1}')"
portable_import_key="portable-smoke-import-$(date +%s)"
portable_import_response="$(curl --fail-with-body --silent --show-error -X POST "$api_url/v1/imports" \
  -H "$auth_header" -H 'Content-Type: application/octet-stream' \
  -H "X-Agent-Memory-Workspace: $workspace_id" \
  -H "X-Agent-Memory-Bundle-Passphrase: $portable_passphrase" \
  -H "Idempotency-Key: $portable_import_key" \
  --data-binary "@$portable_bundle")"
jq -e '.data.state == "completed" and .data.duplicate == false and (.data.report.imported | length) == 1 and (.data.report.failed | length) == 0' <<<"$portable_import_response" >/dev/null
portable_import_id="$(jq -er '.data.id' <<<"$portable_import_response")"
portable_status_response="$(curl --fail-with-body --silent --show-error "$api_url/v1/imports/$portable_import_id" -H "$auth_header")"
jq -e '.data.state == "completed" and (.data.report.imported | length) == 1' <<<"$portable_status_response" >/dev/null
portable_retry_response="$(curl --fail-with-body --silent --show-error -X POST "$api_url/v1/imports" \
  -H "$auth_header" -H 'Content-Type: application/octet-stream' \
  -H "X-Agent-Memory-Workspace: $workspace_id" \
  -H "X-Agent-Memory-Bundle-Passphrase: $portable_passphrase" \
  -H "Idempotency-Key: $portable_import_key" \
  --data-binary "@$portable_bundle")"
jq -e --arg id "$portable_import_id" '.data.id == $id and .data.state == "completed" and .data.duplicate == true and (.data.report.imported | length) == 1' <<<"$portable_retry_response" >/dev/null
local_bundle_hash_after="$(shasum -a 256 "$portable_db" | awk '{print $1}')"
[[ "$local_bundle_hash_before" == "$local_bundle_hash_after" ]]

create_pdf_fixture() {
  local destination="$1"
  local stream='BT /F1 12 Tf 72 720 Td (Hosted PDF provenance passage.) Tj ET'
  local -a objects=(
    '<< /Type /Catalog /Pages 2 0 R >>'
    '<< /Type /Pages /Kids [3 0 R] /Count 1 >>'
    '<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>'
    "<< /Length ${#stream} >>\nstream\n${stream}\nendstream"
    '<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>'
  )
  local -a offsets=()
  local index xref
  printf '%%PDF-1.4\n' > "$destination"
  for index in "${!objects[@]}"; do
    offsets[$index]="$(wc -c < "$destination" | tr -d ' ')"
    printf '%d 0 obj\n%b\nendobj\n' "$((index + 1))" "${objects[$index]}" >> "$destination"
  done
  xref="$(wc -c < "$destination" | tr -d ' ')"
  printf 'xref\n0 6\n0000000000 65535 f \n' >> "$destination"
  for index in "${!objects[@]}"; do
    printf '%010d 00000 n \n' "${offsets[$index]}" >> "$destination"
  done
  printf 'trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n' "$xref" >> "$destination"
}

create_epub_fixture() {
  local destination="$1"
  local root="$fixture_dir/epub"
  mkdir -p "$root/META-INF" "$root/OEBPS"
  printf '%s' 'application/epub+zip' > "$root/mimetype"
  printf '%s' '<container><rootfiles><rootfile full-path="OEBPS/content.opf"/></rootfiles></container>' > "$root/META-INF/container.xml"
  printf '%s' '<package><metadata><title>Hosted Lifecycle</title><language>en</language><identifier>hosted-lifecycle</identifier></metadata><manifest><item id="chapter" href="chapter.xhtml"/></manifest><spine><itemref idref="chapter"/></spine></package>' > "$root/OEBPS/content.opf"
  printf '%s' '<html><head><title>Lifecycle</title></head><body><h1>Lifecycle</h1><p>Hosted EPUB provenance passage.</p></body></html>' > "$root/OEBPS/chapter.xhtml"
  (
    cd "$root"
    zip -q -X -0 "$destination" mimetype
    zip -q -X -r "$destination" META-INF OEBPS
  )
}

create_pdf_fixture "$fixture_dir/lifecycle.pdf"
create_epub_fixture "$fixture_dir/lifecycle.epub"
printf '%s\n' '# Hosted Markdown lifecycle' '' 'Agent Memory preserves private provenance and cited passages.' > "$fixture_dir/lifecycle.md"
printf '%s\n' 'Hosted plain text keeps durable offset provenance for private retrieval.' > "$fixture_dir/lifecycle.txt"

filenames=("lifecycle.pdf" "lifecycle.epub" "lifecycle.md" "lifecycle.txt")
media_types=("application/pdf" "application/epub+zip" "text/markdown" "text/plain")
parser_versions=("pdf-native-v1" "epub-v1" "markdown-v1" "text-v1")
source_ids=()

for index in "${!filenames[@]}"; do
  source_file="$fixture_dir/${filenames[$index]}"
  media_type="${media_types[$index]}"
  size_bytes="$(wc -c < "$source_file" | tr -d ' ')"
  checksum="$(shasum -a 256 "$source_file" | awk '{print $1}')"
  grant_payload="$(jq -cn \
    --arg workspace "$workspace_id" \
    --arg filename "${filenames[$index]}" \
    --arg media_type "$media_type" \
    --arg checksum "$checksum" \
    --argjson size "$size_bytes" \
    '{workspace_id:$workspace,filename:$filename,media_type:$media_type,size_bytes:$size,checksum_sha256:$checksum,rights_basis:"lawfully_acquired_private_use"}')"
  grant_response="$(curl --fail-with-body --silent --show-error -X POST "$api_url/v1/sources/uploads" \
    -H "$auth_header" -H 'Content-Type: application/json' --data "$grant_payload")"
  upload_path="$(jq -er '.data.upload_path' <<<"$grant_response")"
  source_id="$(jq -er '.data.source_id' <<<"$grant_response")"
  curl --fail-with-body --silent --show-error -X PUT "$api_url$upload_path" \
    -H "Content-Type: $media_type" --data-binary "@$source_file" >/dev/null
  source_ids+=("$source_id")
done

for index in "${!source_ids[@]}"; do
  source_id="${source_ids[$index]}"
  source_state=""
  for _ in $(seq 1 120); do
    source_response="$(curl --fail-with-body --silent --show-error "$api_url/v1/sources/$source_id" -H "$auth_header")"
    source_state="$(jq -er '.data.state' <<<"$source_response")"
    if [[ "$source_state" == "failed" ]]; then
      jq . <<<"$source_response" >&2
      exit 1
    fi
    if [[ "$source_state" == "ready" ]]; then
      jq -e --arg parser "${parser_versions[$index]}" '
        .data.provenance.parser_version == $parser and
        .data.provenance.normalization_version != "" and
        .data.retention_state == "retained_private_vault"
      ' <<<"$source_response" >/dev/null
      break
    fi
    sleep 1
  done
  if [[ "$source_state" != "ready" ]]; then
    jq . <<<"$source_response" >&2
    echo "source did not reach ready before timeout: $source_id" >&2
    exit 1
  fi
done

query_payload="$(jq -cn --arg source "${source_ids[2]}" '{source_ids:[$source],query:"private provenance cited passages",limit:5,context_token_budget:400,generate:false,provider:"local-minilm-scaffold",model:"local-hash-v1"}')"
query_response="$(curl --fail-with-body --silent --show-error -X POST "$api_url/v1/source-queries" \
  -H "$auth_header" -H 'Content-Type: application/json' --data "$query_payload")"
jq -e '.data.answerable == true and (.data.evidence | length) > 0' <<<"$query_response" >/dev/null
evidence_ref="$(jq -c '.data.evidence[0] | {source_id,source_version,passage_id,citation_id}' <<<"$query_response")"

proposal_payload="$(jq -cn --arg workspace "$workspace_id" --argjson evidence "[$evidence_ref]" '{workspace_id:$workspace,memory_type:"semantic",content:"The reviewed source emphasizes private, provenance-grounded recall.",transformation:"interpretation",evidence:$evidence}')"
proposal_response="$(curl --fail-with-body --silent --show-error -X POST "$api_url/v1/memory-proposals" \
  -H "$auth_header" -H 'Content-Type: application/json' --data "$proposal_payload")"
proposal_id="$(jq -er '.data.id' <<<"$proposal_response")"
review_payload='{"content":"The user-reviewed interpretation connects privacy with durable citation provenance.","transformation":"user_edit"}'
curl --fail-with-body --silent --show-error -X PATCH "$api_url/v1/memory-proposals/$proposal_id" \
  -H "$auth_header" -H 'Content-Type: application/json' --data "$review_payload" >/dev/null
review_response="$(curl --fail-with-body --silent --show-error -X POST "$api_url/v1/memory-proposals/$proposal_id/accept" \
  -H "$auth_header" -H 'Content-Type: application/json' --data '{}')"
jq -e '.data.status == "accepted" and .data.memory_id != ""' <<<"$review_response" >/dev/null

export_payload="$(jq -cn --arg workspace "$workspace_id" '{workspace_id:$workspace}')"
export_response="$(curl --fail-with-body --silent --show-error -X POST "$api_url/v1/exports" \
  -H "$auth_header" -H 'Content-Type: application/json' --data "$export_payload")"
export_id="$(jq -er '.data.id' <<<"$export_response")"
export_state=""
for _ in $(seq 1 60); do
  export_response="$(curl --fail-with-body --silent --show-error "$api_url/v1/exports/$export_id" -H "$auth_header")"
  export_state="$(jq -er '.data.state' <<<"$export_response")"
  [[ "$export_state" == "ready" ]] && break
  [[ "$export_state" == "failed" ]] && break
  sleep 1
done
if [[ "$export_state" != "ready" ]]; then
  jq . <<<"$export_response" >&2
  echo "export did not become ready" >&2
  exit 1
fi
export_bundle="$(curl --fail-with-body --silent --show-error "$api_url/v1/exports/$export_id/download" -H "$auth_header")"
jq -e '.manifest.counts.sources >= 4 and .manifest.counts.memories >= 1 and .source_bytes_included == false' <<<"$export_bundle" >/dev/null

identity_response="$(curl --fail-with-body --silent --show-error "$api_url/v1/whoami" -H "$auth_header")"
tenant_id="$(jq -er '.data.tenant_id' <<<"$identity_response")"
account_id="$(jq -er '.data.account_id' <<<"$identity_response")"
"${compose[@]}" exec -T postgres psql -U agent_memory -d agent_memory -v ON_ERROR_STOP=1 \
  -v tenant_id="$tenant_id" -v account_id="$account_id" <<'SQL' >/dev/null
SELECT set_config('app.tenant_id', :'tenant_id', false);
UPDATE saas_attestation_receipts
SET expires_at=clock_timestamp()-interval '1 second'
WHERE tenant_id=:'tenant_id' AND subject_id=:'account_id';
SQL
attestation_response="$(curl --fail-with-body --silent --show-error "$api_url/v1/attestations/rights" -H "$auth_header")"
jq -e '.data.status == "expired" and .data.reason == "expired"' <<<"$attestation_response" >/dev/null
renewal_payload="$(jq -cn --arg policy "$policy_version" --argjson statements "$statement_ids" '{policy_version:$policy,accepted_statement_ids:$statements}')"
renewal_response="$(curl --fail-with-body --silent --show-error -X POST "$api_url/v1/attestations/rights" \
  -H "$auth_header" -H 'Content-Type: application/json' --data "$renewal_payload")"
jq -e '.data.status == "active" and .data.reason == "active"' <<<"$renewal_response" >/dev/null

for source_id in "${source_ids[@]}"; do
  deletion_response="$(curl --fail-with-body --silent --show-error -X DELETE "$api_url/v1/sources/$source_id" \
    -H "$auth_header" -H "Idempotency-Key: lifecycle-source-delete-$source_id")"
  deletion_id="$(jq -er '.data.id' <<<"$deletion_response")"
  deletion_state=""
  for _ in $(seq 1 60); do
    deletion_response="$(curl --fail-with-body --silent --show-error "$api_url/v1/deletions/$deletion_id" -H "$auth_header")"
    deletion_state="$(jq -er '.data.state' <<<"$deletion_response")"
    [[ "$deletion_state" == "completed" ]] && break
    sleep 1
  done
  if [[ "$deletion_state" != "completed" ]]; then
    jq . <<<"$deletion_response" >&2
    echo "source deletion did not complete: $source_id" >&2
    exit 1
  fi
done

source_receipts="$("${compose[@]}" exec -T postgres psql -U agent_memory -d agent_memory -At \
  -v tenant_id="$tenant_id" <<'SQL'
SELECT set_config('app.tenant_id', :'tenant_id', false);
SELECT count(*)
FROM saas_deletion_tombstones
WHERE tenant_id=:'tenant_id' AND target_type='source' AND receipt_sha256<>'';
SQL
)"
[[ "$(tail -n 1 <<<"$source_receipts" | tr -d '[:space:]')" == "4" ]]

account_deletion_response="$(curl --fail-with-body --silent --show-error -X DELETE "$api_url/v1/account" \
  -H "$auth_header" -H "Idempotency-Key: lifecycle-account-delete-$tenant_id")"
account_deletion_id="$(jq -er '.data.id' <<<"$account_deletion_response")"
"${compose[@]}" exec -T postgres psql -U agent_memory -d agent_memory -v ON_ERROR_STOP=1 \
  -v tenant_id="$tenant_id" -v operation_id="$account_deletion_id" <<'SQL' >/dev/null
SELECT set_config('app.tenant_id', :'tenant_id', false);
UPDATE saas_deletion_operations
SET execute_after=clock_timestamp(),next_attempt_at=clock_timestamp()
WHERE tenant_id=:'tenant_id' AND id=:'operation_id';
SQL

account_state=""
account_receipt=""
for _ in $(seq 1 150); do
  account_record="$("${compose[@]}" exec -T postgres psql -U agent_memory -d agent_memory -At \
    -v tenant_id="$tenant_id" -v operation_id="$account_deletion_id" <<'SQL'
SELECT set_config('app.tenant_id', :'tenant_id', false);
SELECT state||'|'||receipt_sha256
FROM saas_deletion_operations
WHERE tenant_id=:'tenant_id' AND id=:'operation_id';
SQL
)"
  account_record="$(tail -n 1 <<<"$account_record")"
  account_state="${account_record%%|*}"
  account_receipt="${account_record#*|}"
  [[ "$account_state" == "completed" && -n "$account_receipt" ]] && break
  sleep 1
done
if [[ "$account_state" != "completed" || -z "$account_receipt" ]]; then
  echo "account deletion did not complete with a receipt: state=$account_state" >&2
  exit 1
fi

echo "full lifecycle smoke passed: formats=${#source_ids[@]} export=ready source_deletion_receipts=4 account_deletion_receipt=present"

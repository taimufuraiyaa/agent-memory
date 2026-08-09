#!/usr/bin/env bash
set -euo pipefail

umask 077

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
compose_file="$repo_dir/deploy/saas/compose.yaml"
floci_file="$repo_dir/deploy/saas/compose.floci.yaml"
oidc_file="$repo_dir/deploy/saas/compose.oidc.yaml"
alpha_file="$repo_dir/deploy/saas/compose.alpha.yaml"
evidence_root="${AGENT_MEMORY_LOCAL_EVIDENCE_DIR:-$repo_dir/.local/evidence}"
started_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
run_stamp="$(date -u +%Y%m%dT%H%M%SZ)"
git_commit="$(git -C "$repo_dir" rev-parse HEAD)"
git_short="${git_commit:0:8}"
random_suffix="$(openssl rand -hex 4)"
run_id="${run_stamp}-${git_short}${random_suffix}"
run_token="$(printf '%s' "${run_stamp}_${git_short}${random_suffix}" | tr '[:upper:]' '[:lower:]')"
compose_project="agent-memory-alpha-${run_token}"
scratch_db="agent_memory_alpha_${run_token}"
restore_db="${scratch_db}_restore"
staging_dir="$evidence_root/.incomplete-$run_id"
final_dir="$evidence_root/$run_id"
receipts_dir="$staging_dir/receipts"
checks_dir="$staging_dir/.checks"
archive_path="$evidence_root/$run_id.tar.gz"
sidecar_path="$archive_path.sha256"
stack_active=false
published=false
api_url=""
edge_url=""
oidc_url=""
postgres_port=""
initial_edge_country_secret="alpha-edge-initial-$(openssl rand -hex 24)"
rotated_edge_country_secret="alpha-edge-rotated-$(openssl rand -hex 24)"
export AGENT_MEMORY_EDGE_COUNTRY_SECRET="$initial_edge_country_secret"

compose=(docker compose -p "$compose_project" -f "$compose_file" -f "$floci_file" -f "$oidc_file" -f "$alpha_file")
base_compose=(docker compose -p "$compose_project" -f "$compose_file")

for command in docker curl jq openssl shasum tar trivy go git; do
  command -v "$command" >/dev/null
done
[[ "$scratch_db" =~ ^agent_memory_alpha_[a-z0-9_]{10,50}$ ]] || { echo "invalid scratch database identity" >&2; exit 1; }
[[ ! -e "$staging_dir" && ! -e "$final_dir" && ! -e "$archive_path" ]] || { echo "local evidence run already exists" >&2; exit 1; }
mkdir -p "$receipts_dir" "$checks_dir"
printf 'local alpha evidence run incomplete: %s\n' "$run_id" > "$staging_dir/INCOMPLETE"

drop_database() {
  local database="$1"
  [[ "$database" =~ ^agent_memory_alpha_[a-z0-9_]{10,58}$ ]] || return 1
  "${compose[@]}" exec -T postgres psql -U agent_memory -d postgres -v ON_ERROR_STOP=1 -v database="$database" <<'SQL'
SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=:'database' AND pid<>pg_backend_pid();
DROP DATABASE IF EXISTS :"database";
SQL
}

cleanup() {
  local exit_status=$?
  set +e
  if [[ "$stack_active" == true ]]; then
    drop_database "$restore_db" >/dev/null 2>&1
    drop_database "$scratch_db" >/dev/null 2>&1
    "${compose[@]}" down -v --remove-orphans >/dev/null 2>&1
  fi
  if [[ "$published" != true ]]; then
    printf 'exit_status=%d\n' "$exit_status" >> "$staging_dir/INCOMPLETE"
    printf 'local alpha gate failed; incomplete diagnostics retained at %s\n' "$staging_dir" >&2
  fi
}
trap cleanup EXIT

run_check() {
  local name="$1"
  shift
  local receipt="receipts/$name.log"
  local check_status
  set +e
  ( set -euo pipefail; "$@" ) > "$staging_dir/$receipt" 2>&1
  check_status=$?
  set -e
  if [[ "$check_status" -eq 0 ]]; then
    jq -cn --arg name "$name" --arg receipt "$receipt" \
      '{name:$name,outcome:"passed",receipt:$receipt}' > "$checks_dir/$name.json"
    printf 'passed: %s\n' "$name"
    return 0
  else
    printf 'failed_check=%s status=%d\n' "$name" "$check_status" >> "$staging_dir/INCOMPLETE"
    printf 'failed: %s (see %s)\n' "$name" "$staging_dir/$receipt" >&2
    return "$check_status"
  fi
}

start_stack() {
  "${compose[@]}" up -d --build --wait
  local api_binding edge_binding oidc_binding postgres_binding
  api_binding="$("${compose[@]}" port api 8080)"
  edge_binding="$("${compose[@]}" port edge 8081)"
  oidc_binding="$("${compose[@]}" port oidc 8082)"
  postgres_binding="$("${compose[@]}" port postgres 5432)"
  [[ "$api_binding" =~ ^127\.0\.0\.1:[0-9]+$ && "$edge_binding" =~ ^127\.0\.0\.1:[0-9]+$ && "$oidc_binding" =~ ^127\.0\.0\.1:[0-9]+$ && "$postgres_binding" =~ ^127\.0\.0\.1:[0-9]+$ ]]
  "${compose[@]}" ps --format json
}

resolve_stack_bindings() {
  local edge_binding postgres_binding
  resolve_api_url
  resolve_oidc_url
  edge_binding="$("${compose[@]}" port edge 8081)"
  postgres_binding="$("${compose[@]}" port postgres 5432)"
  [[ "$edge_binding" =~ ^127\.0\.0\.1:[0-9]+$ && "$postgres_binding" =~ ^127\.0\.0\.1:[0-9]+$ ]]
  edge_url="http://$edge_binding"
  postgres_port="${postgres_binding##*:}"
}

resolve_api_url() {
  local binding
  binding="$("${compose[@]}" port api 8080)"
  [[ "$binding" =~ ^127\.0\.0\.1:[0-9]+$ ]]
  api_url="http://$binding"
}

resolve_oidc_url() {
  local binding
  binding="$("${compose[@]}" port oidc 8082)"
  [[ "$binding" =~ ^127\.0\.0\.1:[0-9]+$ ]]
  oidc_url="http://$binding"
}

check_health() {
  curl --fail --silent --show-error "$api_url/health/live"
  curl --fail --silent --show-error "$api_url/health/ready"
  curl --fail --silent --show-error "$edge_url/_edge/health/live"
  curl --fail --silent --show-error "$edge_url/_edge/health/ready"
}

run_lifecycle() {
  local auth_token
  auth_token="$(fetch_oidc_token)"
  AGENT_MEMORY_SMOKE_API_URL="$edge_url" \
    AGENT_MEMORY_SMOKE_AUTH_TOKEN="$auth_token" \
    AGENT_MEMORY_SMOKE_EMAIL="member@oidc.local.invalid" \
    AGENT_MEMORY_SMOKE_COMPOSE_PROJECT="$compose_project" \
    "$repo_dir/scripts/saas-upload-smoke.sh"
}

verify_deletion_lifecycle_evidence() {
  grep -Fxq \
    'full lifecycle smoke passed: formats=4 export=ready source_deletion_receipts=4 account_deletion_receipt=present' \
    "$staging_dir/receipts/lifecycle.log"
  echo 'deletion lifecycle evidence verified: source_receipts=4 account_receipt=present'
}

fetch_oidc_token() {
  local attempt token
  for attempt in $(seq 1 30); do
    token="$(curl --max-time 2 --fail --silent "$oidc_url/token" 2>/dev/null | jq -er '.token' 2>/dev/null || true)"
    if [[ -n "$token" ]]; then
      printf '%s' "$token"
      return 0
    fi
    sleep 0.25
  done
  echo 'local OIDC token endpoint did not become available' >&2
  return 1
}

identity_status() {
  local token="$1"
  curl --max-time 5 --silent --show-error --output /dev/null --write-out '%{http_code}' \
    -X POST "$edge_url/v1/signup" \
    -H "Authorization: Bearer $token" \
    -H 'Content-Type: application/json' \
    --data '{'
}

require_identity_status() {
  local token="$1"
  local expected="$2"
  local label="$3"
  local status
  status="$(identity_status "$token")"
  if [[ "$status" != "$expected" ]]; then
    printf '%s identity status=%s expected=%s\n' "$label" "$status" "$expected" >&2
    return 1
  fi
}

wait_for_container_health() {
  local service="$1"
  local attempt container_id health
  for attempt in $(seq 1 60); do
    container_id="$("${compose[@]}" ps -q "$service")"
    health="$(docker inspect "$container_id" --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' 2>/dev/null || true)"
    if [[ "$health" == "healthy" ]]; then
      return 0
    fi
    sleep 1
  done
  printf '%s did not become healthy\n' "$service" >&2
  return 1
}

wait_for_api_exit() {
  local attempt container_id state
  for attempt in $(seq 1 30); do
    container_id="$("${compose[@]}" ps -aq api)"
    state="$(docker inspect "$container_id" --format '{{.State.Status}}' 2>/dev/null || true)"
    if [[ "$state" == "exited" ]]; then
      return 0
    fi
    sleep 0.5
  done
  echo 'API did not fail closed with OIDC discovery unavailable' >&2
  return 1
}

run_oidc_rotation_outage() {
  local initial_token rotated_token restarted_token final_token api_container_id
  initial_token="$(fetch_oidc_token)"
  require_identity_status "$initial_token" 400 initial

  curl --max-time 5 --fail --silent --show-error -X POST "$oidc_url/rotate" >/dev/null
  rotated_token="$(fetch_oidc_token)"
  require_identity_status "$rotated_token" 400 rotated
  require_identity_status "$initial_token" 400 overlap

  "${compose[@]}" stop -t 5 oidc
  require_identity_status "$rotated_token" 400 cached
  if ! require_identity_status not-a-token 401 invalid; then
    echo 'invalid token did not fail closed during identity provider outage' >&2
    return 1
  fi
  "${compose[@]}" start oidc
  wait_for_container_health oidc
  resolve_oidc_url
  restarted_token="$(fetch_oidc_token)"
  require_identity_status "$restarted_token" 400 restarted

  "${compose[@]}" stop -t 5 api
  "${compose[@]}" stop -t 5 oidc
  api_container_id="$("${compose[@]}" ps -aq api)"
  [[ "$api_container_id" =~ ^[a-f0-9]{12,64}$ ]]
  docker start "$api_container_id" >/dev/null
  wait_for_api_exit
  "${compose[@]}" start oidc
  wait_for_container_health oidc
  resolve_oidc_url
  docker start "$api_container_id" >/dev/null
  resolve_api_url
  wait_for_recovery api api
  final_token="$(fetch_oidc_token)"
  require_identity_status "$final_token" 400 recovered
  curl --max-time 5 --fail --silent --show-error "$edge_url/_edge/health/ready" >/dev/null
  echo 'OIDC rotation, overlap, cached outage verification, discovery fail-closed startup, and recovery passed'
}

check_edge_telemetry() {
  local request_id="alpha-edge-telemetry-probe"
  local status auth_token
  auth_token="$(fetch_oidc_token)"
  status="$(curl --max-time 5 --silent --show-error --output /dev/null --write-out '%{http_code}' \
    -X POST "$edge_url/v1/signup" \
    -H "X-Request-ID: $request_id" \
    -H "Authorization: Bearer $auth_token" \
    -H 'Content-Type: application/json' \
    --data '{')"
  [[ "$status" == "400" ]]
  status="$(curl --silent --output /dev/null --write-out '%{http_code}' "$edge_url/metrics")"
  [[ "$status" == "404" ]]
  "${compose[@]}" exec -T api wget -q -O - http://localhost:8080/metrics \
    | grep -F 'agent_memory_saas_http_requests_total' \
    | grep -F 'operation="POST:/v1/signup"' >/dev/null
  "${compose[@]}" exec -T worker wget -q -O - http://localhost:9090/metrics \
    | grep -F '# HELP agent_memory_saas_component_operations_total' >/dev/null
  "${compose[@]}" exec -T reconciler wget -q -O - http://localhost:9090/metrics \
    | grep -F '# HELP agent_memory_saas_component_operations_total' >/dev/null
  printf 'edge request observed by API telemetry; internal worker and reconciler metric families available; public metrics status=%s\n' "$status"
}

account_count() {
  "${compose[@]}" exec -T postgres psql -U agent_memory -d agent_memory -Atc 'SELECT count(*) FROM saas_accounts'
}

sign_country_assertion() {
  local secret="$1"
  local timestamp="$2"
  printf 'VN\n%s' "$timestamp" | openssl dgst -sha256 -hmac "$secret" | awk '{print $NF}'
}

direct_signup_status() {
  local token="$1"
  local secret="$2"
  local timestamp signature
  timestamp="$(date -u +%s)"
  signature="$(sign_country_assertion "$secret" "$timestamp")"
  curl --max-time 5 --silent --show-error --output /dev/null --write-out '%{http_code}' \
    -X POST "$api_url/v1/signup" \
    -H "Authorization: Bearer $token" \
    -H 'Content-Type: application/json' \
    -H 'X-Agent-Memory-Country: VN' \
    -H "X-Agent-Memory-Country-Timestamp: $timestamp" \
    -H "X-Agent-Memory-Country-Signature: $signature" \
    --data '{"age_confirmed":true}'
}

authenticated_edge_status() {
  local token="$1"
  curl --max-time 5 --silent --show-error --output /dev/null --write-out '%{http_code}' \
    "$edge_url/v1/whoami" -H "Authorization: Bearer $token"
}

recreate_api_edge() {
  "${compose[@]}" up -d --force-recreate --no-deps --wait --wait-timeout 120 api edge
  resolve_stack_bindings
}

run_runtime_secret_rotation() {
  local token before_count after_count old_status new_status edge_status
  token="$(fetch_oidc_token)"
  before_count="$(account_count)"
  [[ "$before_count" =~ ^[1-9][0-9]*$ ]]

  export AGENT_MEMORY_EDGE_COUNTRY_SECRET="$rotated_edge_country_secret"
  recreate_api_edge >/dev/null

  old_status="$(direct_signup_status "$token" "$initial_edge_country_secret")"
  new_status="$(direct_signup_status "$token" "$rotated_edge_country_secret")"
  edge_status="$(authenticated_edge_status "$token")"
  after_count="$(account_count)"
  [[ "$old_status" == "403" ]]
  [[ "$new_status" == "201" ]]
  [[ "$edge_status" == "200" ]]
  [[ "$after_count" == "$before_count" ]]
  echo 'runtime secret rotated; old trust assertion rejected; authenticated state preserved'
}

run_runtime_configuration_rollback() {
  local token before_count after_count replacement_status=0 exited=false edge_status attempt_log
  token="$(fetch_oidc_token)"
  before_count="$(account_count)"
  attempt_log="$staging_dir/.invalid-replacement.log"

  export AGENT_MEMORY_EDGE_COUNTRY_SECRET="invalid"
  set +e
  "${compose[@]}" up -d --force-recreate --no-deps --wait --wait-timeout 30 api >"$attempt_log" 2>&1
  replacement_status=$?
  set -e
  if wait_for_api_exit >/dev/null 2>&1; then
    exited=true
  fi
  rm -f -- "$attempt_log"

  export AGENT_MEMORY_EDGE_COUNTRY_SECRET="$rotated_edge_country_secret"
  recreate_api_edge >/dev/null
  edge_status="$(authenticated_edge_status "$token")"
  after_count="$(account_count)"

  [[ "$replacement_status" -ne 0 ]]
  [[ "$exited" == true ]]
  [[ "$edge_status" == "200" ]]
  [[ "$after_count" == "$before_count" ]]
  echo 'invalid replacement configuration rejected; last known-good configuration restored; authenticated state preserved'
}

create_scratch_database() {
  "${compose[@]}" exec -T postgres psql -U agent_memory -d postgres -v ON_ERROR_STOP=1 -v database="$scratch_db" <<'SQL'
CREATE DATABASE :"database";
SQL
  "${compose[@]}" run --rm --no-deps \
    -e "AGENT_MEMORY_POSTGRES_URL=postgres://agent_memory:local-development@postgres:5432/$scratch_db?sslmode=disable" \
    migrate
  printf 'scratch database migrated: %s\n' "$scratch_db"
}

run_parity() {
  AGENT_MEMORY_TEST_POSTGRES_URL="postgres://agent_memory:local-development@127.0.0.1:$postgres_port/$scratch_db?sslmode=disable" \
    GOCACHE=/tmp/agent-memory-go-cache \
    go test -count=1 ./evaluation/parity
}

run_backup_restore() {
  local dump_path="$staging_dir/.scratch-backup.dump"
  local source_migrations restored_migrations source_tables restored_tables
  "${compose[@]}" exec -T postgres pg_dump -U agent_memory -d "$scratch_db" -Fc > "$dump_path"
  "${compose[@]}" exec -T postgres psql -U agent_memory -d postgres -v ON_ERROR_STOP=1 -v database="$restore_db" <<'SQL'
CREATE DATABASE :"database";
SQL
  "${compose[@]}" exec -T postgres pg_restore -U agent_memory -d "$restore_db" --exit-on-error < "$dump_path"
  source_migrations="$("${compose[@]}" exec -T postgres psql -U agent_memory -d "$scratch_db" -Atc 'SELECT count(*) FROM saas_schema_migrations')"
  restored_migrations="$("${compose[@]}" exec -T postgres psql -U agent_memory -d "$restore_db" -Atc 'SELECT count(*) FROM saas_schema_migrations')"
  source_tables="$("${compose[@]}" exec -T postgres psql -U agent_memory -d "$scratch_db" -Atc "SELECT count(*) FROM pg_tables WHERE schemaname='public'")"
  restored_tables="$("${compose[@]}" exec -T postgres psql -U agent_memory -d "$restore_db" -Atc "SELECT count(*) FROM pg_tables WHERE schemaname='public'")"
  rm -f -- "$dump_path"
  [[ "$source_migrations" =~ ^[1-9][0-9]*$ && "$source_migrations" == "$restored_migrations" ]]
  [[ "$source_tables" =~ ^[1-9][0-9]*$ && "$source_tables" == "$restored_tables" ]]
  printf 'scratch backup restore passed: migrations=%s tables=%s\n' "$source_migrations" "$source_tables"
}

run_operator_access() {
  AGENT_MEMORY_TEST_POSTGRES_URL="postgres://agent_memory:local-development@127.0.0.1:$postgres_port/$scratch_db?sslmode=disable" \
    GOCACHE=/tmp/agent-memory-go-cache \
    go test -count=1 -run '^TestOperatorInspectionAndTimeBoundIndependentElevation$' ./internal/saas/operator
  echo 'operator access safe inspection, independent approval, expiry denial, and audit verification passed'
}

run_two_tenant_isolation_load() {
  AGENT_MEMORY_TEST_POSTGRES_URL="postgres://agent_memory:local-development@127.0.0.1:$postgres_port/$scratch_db?sslmode=disable" \
    GOCACHE=/tmp/agent-memory-go-cache \
    go test -race -count=1 -v -run '^TestTwoTenantAdversarialAndBoundedRetrievalLoad$' ./evaluation/isolation
}

run_credential_leak_revoke() {
  AGENT_MEMORY_TEST_POSTGRES_URL="postgres://agent_memory:local-development@127.0.0.1:$postgres_port/$scratch_db?sslmode=disable" \
    GOCACHE=/tmp/agent-memory-go-cache \
    go test -race -count=1 -v -run '^TestCredentialLeakDetectionAndRevocation$' ./evaluation/operations
}

run_model_provider_outage() {
  GOCACHE=/tmp/agent-memory-go-cache \
    go test -race -count=1 -v -run '^TestModelProviderOutageFailsSafeWithEvidence$' ./evaluation/operations
}

inspect_hardening() {
  local container_id
  container_id="$("${compose[@]}" ps -q minio)"
  docker inspect "$container_id" --format \
    'user={{.Config.User}} readonly={{.HostConfig.ReadonlyRootfs}} cap_drop={{json .HostConfig.CapDrop}} security_opt={{json .HostConfig.SecurityOpt}}'
  local hardening
  hardening="$(docker inspect "$container_id" --format '{{.Config.User}}|{{.HostConfig.ReadonlyRootfs}}|{{json .HostConfig.CapDrop}}|{{json .HostConfig.SecurityOpt}}')"
  [[ "$hardening" == '1001:0|true|["ALL"]|["no-new-privileges:true"]' ]]
}

wait_for_readiness_failure() {
  local scenario="$1"
  local attempt
  for attempt in $(seq 1 30); do
    if ! curl --max-time 2 --fail --silent --output /dev/null "$api_url/health/ready"; then
      printf 'readiness failed closed during %s after attempt=%d\n' "$scenario" "$attempt"
      return 0
    fi
    sleep 0.25
  done
  printf 'readiness remained successful during %s outage\n' "$scenario" >&2
  return 1
}

wait_for_recovery() {
  local service="$1"
  local scenario="$2"
  local attempt container_id health
  for attempt in $(seq 1 90); do
    container_id="$("${compose[@]}" ps -q "$service")"
    health="$(docker inspect "$container_id" --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' 2>/dev/null || true)"
    if [[ "$health" == "healthy" || "$health" == "running" ]]; then
      if curl --max-time 2 --fail --silent --output /dev/null "$api_url/health/ready"; then
        printf 'readiness recovered after %s attempt=%d dependency_health=%s\n' "$scenario" "$attempt" "$health"
        return 0
      fi
    fi
    sleep 1
  done
  printf 'readiness did not recover after %s outage\n' "$scenario" >&2
  return 1
}

run_dependency_outage() {
  local service="$1"
  local scenario="$2"
  "${compose[@]}" stop -t 5 "$service"
  wait_for_readiness_failure "$scenario"
  "${compose[@]}" start "$service"
  if [[ "$service" == "api" ]]; then
    resolve_api_url
  fi
  wait_for_recovery "$service" "$scenario"
}

wait_for_edge_readiness_failure() {
  local attempt
  for attempt in $(seq 1 30); do
    curl --max-time 2 --fail --silent --output /dev/null "$edge_url/_edge/health/live"
    if ! curl --max-time 2 --fail --silent --output /dev/null "$edge_url/_edge/health/ready"; then
      printf 'edge remained live and failed upstream readiness after attempt=%d\n' "$attempt"
      return 0
    fi
    sleep 0.25
  done
  echo 'edge readiness remained successful during API outage' >&2
  return 1
}

run_edge_api_outage() {
  "${compose[@]}" stop -t 5 api
  wait_for_edge_readiness_failure
  "${compose[@]}" start api
  resolve_api_url
  wait_for_recovery api api
  curl --fail --silent --show-error "$edge_url/_edge/health/ready" >/dev/null
  echo 'edge-to-API readiness recovered'
}

record_image_identities() {
  local image
  for image in \
    agent-memory-floci:1.6.0-hardened \
    agent-memory-saas-s3-init:latest \
    agent-memory-saas-api:latest \
    agent-memory-saas-edge:latest \
    agent-memory-saas-oidc:latest \
    agent-memory-saas-worker:latest \
    agent-memory-saas-reconciler:latest \
    agent-memory-saas-migrate:latest; do
    docker image inspect "$image" --format '{{.RepoTags}} id={{.Id}} repo_digests={{json .RepoDigests}}'
  done
}

scan_images() {
  local image
  for image in \
    agent-memory-floci:1.6.0-hardened \
    agent-memory-saas-s3-init:latest \
    agent-memory-saas-api:latest \
    agent-memory-saas-edge:latest \
    agent-memory-saas-oidc:latest \
    agent-memory-saas-worker:latest \
    agent-memory-saas-reconciler:latest \
    agent-memory-saas-migrate:latest; do
    trivy image --scanners vuln --severity HIGH,CRITICAL --exit-code 1 --ignore-unfixed "$image"
  done
}

cleanup_scratch_databases() {
  drop_database "$restore_db"
  drop_database "$scratch_db"
  printf 'scratch databases removed\n'
}

stop_isolated_stack() {
  "${compose[@]}" down -v --remove-orphans
  printf 'isolated Compose project and volumes removed: %s\n' "$compose_project"
}

stack_active=true
run_check stack_start start_stack
resolve_stack_bindings
run_check api_health check_health
run_check oidc_rotation_outage run_oidc_rotation_outage
resolve_stack_bindings
run_check lifecycle run_lifecycle
run_check deletion_lifecycle_evidence verify_deletion_lifecycle_evidence
run_check runtime_secret_rotation run_runtime_secret_rotation
resolve_stack_bindings
run_check runtime_configuration_rollback run_runtime_configuration_rollback
resolve_stack_bindings
run_check edge_telemetry check_edge_telemetry
run_check local_profile_contract "$repo_dir/scripts/tests/saas-local-profiles_test.sh"
run_check kubernetes_policy_contract "$repo_dir/scripts/validate-saas-kubernetes.sh"
run_check release_rollback_contract "$repo_dir/scripts/tests/saas-kubernetes-release_test.sh"
run_check scratch_database create_scratch_database
run_check retrieval_parity run_parity
run_check backup_restore run_backup_restore
run_check operator_access run_operator_access
run_check two_tenant_isolation_load run_two_tenant_isolation_load
run_check credential_leak_revoke run_credential_leak_revoke
run_check model_provider_outage run_model_provider_outage
run_check runtime_hardening inspect_hardening
run_check dependency_outage_postgres run_dependency_outage postgres postgres
run_check dependency_outage_nats run_dependency_outage nats nats
run_check dependency_outage_floci run_dependency_outage minio floci
run_check dependency_outage_api_edge run_edge_api_outage
run_check image_identities record_image_identities
run_check image_vulnerability_scan scan_images
run_check scratch_cleanup cleanup_scratch_databases
run_check isolated_stack_cleanup stop_isolated_stack
stack_active=false

completed_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
git_dirty=false
[[ -n "$(git -C "$repo_dir" status --porcelain --untracked-files=normal)" ]] && git_dirty=true
checks_json="$(jq -s '.' "$checks_dir"/*.json)"
metadata_path="$staging_dir/.metadata.json"
jq -n \
  --arg run_id "$run_id" \
  --arg profile "floci" \
  --arg git_commit "$git_commit" \
  --argjson git_dirty "$git_dirty" \
  --arg started_at "$started_at" \
  --arg completed_at "$completed_at" \
  --argjson checks "$checks_json" \
  '{run_id:$run_id,profile:$profile,git_commit:$git_commit,git_dirty:$git_dirty,started_at:$started_at,completed_at:$completed_at,checks:$checks}' \
  > "$metadata_path"

go run "$repo_dir/cmd/agent-memory-local-evidence" --root "$staging_dir" --metadata "$metadata_path" > "$staging_dir/manifest.json"
go run "$repo_dir/cmd/agent-memory-local-evidence" --root "$staging_dir" --verify "$staging_dir/manifest.json"
rm -f -- "$metadata_path"
find "$checks_dir" -type f -delete
rmdir "$checks_dir"
rm -f -- "$staging_dir/INCOMPLETE"
temporary_archive="$evidence_root/.incomplete-$run_id.tar.gz"
temporary_sidecar="$temporary_archive.sha256"
tar -czf "$temporary_archive" -C "$staging_dir" .
archive_digest="$(shasum -a 256 "$temporary_archive" | awk '{print $1}')"
printf '%s  %s\n' "$archive_digest" "$(basename "$archive_path")" > "$temporary_sidecar"
mv "$temporary_archive" "$archive_path"
mv "$temporary_sidecar" "$sidecar_path"
mv "$staging_dir" "$final_dir"
published=true
printf 'local alpha evidence passed: %s\narchive: %s\ndigest: %s\n' \
  "$final_dir" "$archive_path" "$archive_digest"

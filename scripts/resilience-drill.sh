#!/usr/bin/env bash
set -euo pipefail

repo="$(cd "$(dirname "$0")/.." && pwd)"
cd "$repo"
run_id="$(date +%s)-$$-${RANDOM}"
compose_project="vpn-resilience-${run_id}"
default_project_volume=vpn-service_postgres-data
utility_image='postgres:18-alpine@sha256:9a8afca54e7861fd90fab5fdf4c42477a6b1cb7d293595148e674e0a3181de15'
preservation_volume_owned=0
preservation_created_at=''
profiles=(--profile core --profile app --profile vpn --profile obs)
export COMPOSE_PROJECT_NAME="$compose_project"
export VPN_SMOKE_FULL_CONTROL_PLANE=1 VPN_SMOKE_TEST_FAILOVER=1 STAGE7_EXTENDED_SMOKE=0 OBSERVABILITY_SMOKE=1
export SMOKE_KEEP_STACK=1 COMPOSE_PROFILES=core,app,vpn,obs
export SMOKE_DISPOSABLE_PROJECT=1
export ACCESS_PUBLIC_IP_RATE_LIMIT=2000 ACCESS_PUBLIC_TOKEN_RATE_LIMIT=2000
export POSTGRES_USER="${POSTGRES_USER:-vpn_local}" POSTGRES_PASSWORD="${POSTGRES_PASSWORD:-local-compose-password}" POSTGRES_DB="${POSTGRES_DB:-vpn_platform}"
export REDIS_PASSWORD="${REDIS_PASSWORD:-local-compose-redis}" KAFKA_PORT="${KAFKA_PORT:-9094}"
export IDENTITY_DB_PASSWORD="${IDENTITY_DB_PASSWORD:-local-compose-identity}" CATALOG_DB_PASSWORD="${CATALOG_DB_PASSWORD:-local-compose-catalog}"
export BILLING_DB_PASSWORD="${BILLING_DB_PASSWORD:-local-compose-billing}" SUBSCRIPTION_DB_PASSWORD="${SUBSCRIPTION_DB_PASSWORD:-local-compose-subscription}"
export ACCESS_DB_PASSWORD="${ACCESS_DB_PASSWORD:-local-compose-access}" PROVISIONING_DB_PASSWORD="${PROVISIONING_DB_PASSWORD:-local-compose-provisioning}"
export NOTIFICATION_DB_PASSWORD="${NOTIFICATION_DB_PASSWORD:-local-compose-notification}" ADMIN_DB_PASSWORD="${ADMIN_DB_PASSWORD:-local-compose-admin}"
export ADMIN_MIGRATOR_DB_PASSWORD="${ADMIN_MIGRATOR_DB_PASSWORD:-local-compose-admin-migrator}"
export ACCESS_CREDENTIAL_KEY_BASE64="${ACCESS_CREDENTIAL_KEY_BASE64:-MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=}"
export ACCESS_TOKEN_HMAC_KEY_BASE64="${ACCESS_TOKEN_HMAC_KEY_BASE64:-ZmVkY2JhOTg3NjU0MzIxMGZlZGNiYTk4NzY1NDMyMTA=}"
export SUBSCRIPTION_PUBLIC_BASE_URL=https://127.0.0.1:8087
export TELEGRAM_WEBHOOK_SECRET="${TELEGRAM_WEBHOOK_SECRET:-local-compose-webhook-secret}" TELEGRAM_BOT_TOKEN="${TELEGRAM_BOT_TOKEN:-local-compose-fake-bot-token}"
export TERMS_URL=https://example.invalid/terms/terms-v1 YOOKASSA_SHOP_ID=test-shop
export YOOKASSA_SECRET_KEY="${YOOKASSA_SECRET_KEY:-local-compose-yookassa-key}" PAYMENT_RETURN_URL=https://example.invalid/payment-return
cache_root="${VPN_PLATFORM_CACHE_ROOT:-$(dirname "$repo")/.cache/vpn-platform}"
export GOCACHE="${GOCACHE:-${cache_root}/cache}"
export GOMODCACHE="${GOMODCACHE:-${cache_root}/mod}"
export GOTMPDIR="${GOTMPDIR:-${cache_root}/tmp}"
mkdir -p "$GOCACHE" "$GOMODCACHE" "$GOTMPDIR"

volume_exists() {
  docker volume inspect "$1" >/dev/null 2>&1
}

initialize_preservation_volume() {
  if ! volume_exists "$default_project_volume"; then
    docker volume create "$default_project_volume" >/dev/null
    preservation_volume_owned=1
    docker run --rm --entrypoint sh -v "${default_project_volume}:/sentinel" "$utility_image" \
      -c "printf '%s' '${run_id}' > /sentinel/.vpn-platform-resilience-preserved"
  fi
  preservation_created_at="$(docker volume inspect --format '{{.CreatedAt}}' "$default_project_volume")"
  [[ -n "$preservation_created_at" ]]
}

verify_preservation_volume() {
  volume_exists "$default_project_volume" || {
    echo 'resilience drill removed the default Compose volume' >&2
    return 1
  }
  local current_created_at
  current_created_at="$(docker volume inspect --format '{{.CreatedAt}}' "$default_project_volume")"
  if [[ "$current_created_at" != "$preservation_created_at" ]]; then
    echo 'resilience drill replaced the default Compose volume' >&2
    return 1
  fi
  if [[ "$preservation_volume_owned" == 1 ]]; then
    local marker
    marker="$(docker run --rm --entrypoint cat -v "${default_project_volume}:/sentinel:ro" "$utility_image" \
      /sentinel/.vpn-platform-resilience-preserved)"
    if [[ "$marker" != "$run_id" ]]; then
      echo 'resilience drill changed data in the preservation-test volume' >&2
      return 1
    fi
  fi
}

cleanup() {
  status=$?
  trap - EXIT
  export SMOKE_KEEP_STACK=0
  cleanup_status=0
  if ! docker compose "${profiles[@]}" down -v --remove-orphans; then
    echo "resilience drill cleanup failed" >&2
    cleanup_status=1
  fi
  for volume in "${compose_project}-go-mod-cache" "${compose_project}-go-build-cache"; do
    if volume_exists "$volume" && ! docker volume rm "$volume" >/dev/null; then
      echo "auxiliary volume cleanup failed: ${volume}" >&2
      cleanup_status=1
    fi
  done
  if [[ -n "$preservation_created_at" ]] && ! verify_preservation_volume; then
    cleanup_status=1
  fi
  if [[ "$preservation_volume_owned" == 1 ]] && volume_exists "$default_project_volume"; then
    if ! docker volume rm "$default_project_volume" >/dev/null; then
      echo 'preservation-test volume cleanup failed' >&2
      cleanup_status=1
    fi
  fi
  if [[ "$cleanup_status" != 0 ]]; then exit "$cleanup_status"; fi
  exit "$status"
}
trap cleanup EXIT

sql() {
  docker compose "${profiles[@]}" exec -T postgres \
    psql --username="$POSTGRES_USER" --dbname "$1" -tAc "$2" | tr -d '[:space:]'
}

wait_until() {
  description="$1"
  shift
  for _ in $(seq 1 120); do
    if "$@" >/dev/null 2>&1; then
      return
    fi
    sleep 0.5
  done
  echo "$description" >&2
  exit 1
}

initialize_preservation_volume
bash scripts/compose-smoke.sh

export LOAD_PROBE_CA_FILE="${repo}/secrets/dev-mtls/ca.crt"
export LOAD_PROBE_DURATION="${RESILIENCE_SOAK_DURATION:-30s}" LOAD_PROBE_RPS="${RESILIENCE_RPS:-20}"
export LOAD_PROBE_CONCURRENCY=8 LOAD_PROBE_MAX_ERROR_RATIO=0.001 LOAD_PROBE_P95_BUDGET_MS=200
export LOAD_PROBE_EXPECTED_STATUSES=404 LOAD_PROBE_URL=https://127.0.0.1:8087/s/resilience-invalid-token
go run -mod=readonly ./tools/loadprobe | tee "tmp/${compose_project}-load-last-report.json"

periods_before="$(sql subscription_service 'SELECT count(*) FROM subscription_periods')"
payment_event_id="$(sql billing_service "SELECT event_id FROM outbox WHERE topic='billing.payment.succeeded.v1' ORDER BY created_at LIMIT 1")"
docker compose "${profiles[@]}" stop kafka
sql billing_service "UPDATE outbox SET state='pending', next_attempt_at=clock_timestamp(), lease_until=NULL, published_at=NULL WHERE event_id='${payment_event_id}'" >/dev/null
sleep 2
docker compose "${profiles[@]}" start kafka
wait_until 'Kafka did not recover' docker compose "${profiles[@]}" exec -T kafka /opt/kafka/bin/kafka-broker-api-versions.sh --bootstrap-server localhost:9092
for _ in $(seq 1 120); do
  [[ "$(sql billing_service "SELECT state FROM outbox WHERE event_id='${payment_event_id}'")" == published ]] && break
  sleep 0.5
done
[[ "$(sql billing_service "SELECT state FROM outbox WHERE event_id='${payment_event_id}'")" == published ]]
[[ "$(sql subscription_service 'SELECT count(*) FROM subscription_periods')" == "$periods_before" ]]

docker compose "${profiles[@]}" stop postgres
live_status="$(curl -sS -o /dev/null -w '%{http_code}' --cacert secrets/dev-mtls/ca.crt https://127.0.0.1:8087/livez)"
ready_status="$(curl -sS -o /dev/null -w '%{http_code}' --max-time 10 --cacert secrets/dev-mtls/ca.crt https://127.0.0.1:8087/readyz)"
[[ "$live_status" == 200 && "$ready_status" == 503 ]]
docker compose "${profiles[@]}" start postgres
wait_until 'PostgreSQL did not recover' docker compose "${profiles[@]}" exec -T postgres pg_isready -U "$POSTGRES_USER" -d "$POSTGRES_DB"
wait_until 'Access did not recover after PostgreSQL outage' docker compose "${profiles[@]}" exec -T access-service /access-service healthcheck

go test -mod=readonly ./services/node-agent/internal/xray -run 'Reload|SystemdManager'
go test -mod=readonly ./services/provisioning/internal/application -run 'Reconcile|Failover'

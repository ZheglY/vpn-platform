#!/usr/bin/env bash
set -euo pipefail

export POSTGRES_USER="${POSTGRES_USER:-vpn_local}"
export POSTGRES_PASSWORD="${POSTGRES_PASSWORD:-local-compose-password}"
export POSTGRES_DB="${POSTGRES_DB:-vpn_platform}"
export REDIS_PASSWORD="${REDIS_PASSWORD:-local-compose-redis}"
export KAFKA_PORT="${KAFKA_PORT:-9094}"
export IDENTITY_DB_PASSWORD="${IDENTITY_DB_PASSWORD:-local-compose-identity}"
export TELEGRAM_WEBHOOK_SECRET="${TELEGRAM_WEBHOOK_SECRET:-local-compose-webhook-secret}"
export TELEGRAM_BOT_TOKEN="${TELEGRAM_BOT_TOKEN:-local-compose-fake-bot-token}"
export FAKE_TELEGRAM_SEND_DELAY="${FAKE_TELEGRAM_SEND_DELAY:-1s}"
export TERMS_URL="${TERMS_URL:-https://example.invalid/terms/terms-v1}"

bash scripts/dev-mtls.sh

cleanup() {
  docker compose --profile core --profile app down -v
}
trap cleanup EXIT

webhook_status() {
  local body="$1"
  curl -sS -o /tmp/vpn-service-webhook-response.json -w "%{http_code}" \
    -H "X-Telegram-Bot-Api-Secret-Token: ${TELEGRAM_WEBHOOK_SECRET}" \
    -H "Content-Type: application/json" \
    --data-binary "$body" \
    http://127.0.0.1:8081/webhooks/telegram
}

scalar_sql() {
  docker compose exec -T postgres psql --username="${POSTGRES_USER}" --dbname identity_service -tAc "$1" | tr -d '[:space:]'
}

wait_redis_processing_key() {
  local update_id="$1"
  local key="telegram:dedupe:processing:${update_id}"
  local exists
  for _ in $(seq 1 100); do
    exists="$(docker compose exec -T -e "REDISCLI_AUTH=${REDIS_PASSWORD}" redis redis-cli --raw EXISTS "$key" | tr -d '[:space:]')"
    if [[ "$exists" == "1" ]]; then
      return 0
    fi
    sleep 0.1
  done
  echo "processing dedupe key was not observed for update_id=${update_id}" >&2
  exit 1
}

docker compose --profile core --profile app up -d --build

containers=(
  vpn-service-postgres-1
  vpn-service-redis-1
  vpn-service-kafka-1
  vpn-service-identity-service-1
  vpn-service-telegram-api-1
  vpn-service-telegram-bot-1
)

for _ in $(seq 1 60); do
  all_healthy=true
  for container in "${containers[@]}"; do
    status="$(docker inspect -f '{{.State.Health.Status}}' "$container")"
    if [[ "$status" != "healthy" ]]; then
      all_healthy=false
    fi
  done
  if [[ "$all_healthy" == "true" ]]; then
    break
  fi
  sleep 2
done

for container in "${containers[@]}"; do
  status="$(docker inspect -f '{{.State.Health.Status}}' "$container")"
  if [[ "$status" != "healthy" ]]; then
    docker compose --profile core --profile app ps
    docker compose --profile core --profile app logs --tail=200
    exit 1
  fi
done

docker compose exec -T kafka /opt/kafka/bin/kafka-topics.sh --bootstrap-server localhost:9094 --list >/dev/null
docker compose exec -T identity-service /identity-service healthcheck >/dev/null

for attempt in $(seq 1 30); do
  if curl -fsS http://127.0.0.1:8081/livez >/tmp/vpn-service-bot-livez.json &&
     curl -fsS http://127.0.0.1:8081/readyz >/tmp/vpn-service-bot-readyz.json &&
     curl -fsS http://127.0.0.1:8081/version >/tmp/vpn-service-bot-version.json; then
    grep -q '"status":"ok"' /tmp/vpn-service-bot-livez.json
    grep -q '"status":"ready"' /tmp/vpn-service-bot-readyz.json
    grep -q '"service":"telegram-bot"' /tmp/vpn-service-bot-version.json
    break
  fi
  if [[ "$attempt" == "30" ]]; then
    docker compose --profile core --profile app ps
    docker compose --profile core --profile app logs --tail=200
    exit 1
  fi
  sleep 2
done

curl -fsS -X POST http://127.0.0.1:8082/reset >/dev/null

start_body='{"update_id":2001,"message":{"message_id":1,"text":"/start","chat":{"id":9001},"from":{"id":4200001,"first_name":"Smoke","language_code":"en"}}}'
webhook_status "$start_body" >/tmp/vpn-service-first-status.txt &
first_pid=$!
wait_redis_processing_key 2001
concurrent_status="$(webhook_status "$start_body")"
wait "$first_pid"
first_status="$(cat /tmp/vpn-service-first-status.txt)"
if [[ "$first_status" != "200" || "$concurrent_status" != "503" ]]; then
  echo "unexpected concurrent webhook statuses: first=$first_status duplicate=$concurrent_status" >&2
  exit 1
fi

completed_replay_status="$(webhook_status "$start_body")"
if [[ "$completed_replay_status" != "200" ]]; then
  echo "completed replay status=$completed_replay_status, want 200" >&2
  exit 1
fi
curl -fsS http://127.0.0.1:8082/messages >/tmp/vpn-service-fake-telegram-messages.json
grep -q '"count":1' /tmp/vpn-service-fake-telegram-messages.json

accept_body='{"update_id":2002,"message":{"message_id":2,"text":"accept","chat":{"id":9001},"from":{"id":4200001,"first_name":"Smoke","language_code":"en"}}}'
accept_status="$(webhook_status "$accept_body")"
if [[ "$accept_status" != "200" ]]; then
  echo "accept webhook status=$accept_status, want 200" >&2
  exit 1
fi
curl -fsS http://127.0.0.1:8082/messages >/tmp/vpn-service-fake-telegram-messages.json
grep -q '"count":2' /tmp/vpn-service-fake-telegram-messages.json

identity_count="$(scalar_sql "SELECT count(*) FROM telegram_identities WHERE telegram_user_id = 4200001")"
consent_count="$(scalar_sql "SELECT count(*) FROM consents c JOIN telegram_identities t ON t.user_id = c.user_id WHERE t.telegram_user_id = 4200001 AND c.document_type = 'terms' AND c.document_version = 'terms-v1'")"
if [[ "$identity_count" != "1" || "$consent_count" != "1" ]]; then
  echo "unexpected database counts: identities=$identity_count consents=$consent_count" >&2
  exit 1
fi

go run ./tools/mtlsprobe/cmd/mtlsprobe PUT https://127.0.0.1:8080/internal/v1/telegram-users/999 secrets/dev-mtls/identity-health.crt secrets/dev-mtls/identity-health.key secrets/dev-mtls/ca.crt 403

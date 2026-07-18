#!/usr/bin/env bash
set -euo pipefail

export POSTGRES_USER="${POSTGRES_USER:-vpn_local}"
export POSTGRES_PASSWORD="${POSTGRES_PASSWORD:-local-compose-password}"
export POSTGRES_DB="${POSTGRES_DB:-vpn_platform}"
export POSTGRES_PORT="${POSTGRES_PORT:-5432}"
export REDIS_PASSWORD="${REDIS_PASSWORD:-local-compose-redis}"
export KAFKA_PORT="${KAFKA_PORT:-9094}"
export IDENTITY_DB_PASSWORD="${IDENTITY_DB_PASSWORD:-local-compose-identity}"
export CATALOG_DB_PASSWORD="${CATALOG_DB_PASSWORD:-local-compose-catalog}"
export BILLING_DB_PASSWORD="${BILLING_DB_PASSWORD:-local-compose-billing}"
export TELEGRAM_WEBHOOK_SECRET="${TELEGRAM_WEBHOOK_SECRET:-local-compose-webhook-secret}"
export TELEGRAM_BOT_TOKEN="${TELEGRAM_BOT_TOKEN:-local-compose-fake-bot-token}"
export FAKE_TELEGRAM_SEND_DELAY="${FAKE_TELEGRAM_SEND_DELAY:-1s}"
export TERMS_URL="${TERMS_URL:-https://example.invalid/terms/terms-v1}"
export YOOKASSA_SHOP_ID="${YOOKASSA_SHOP_ID:-test-shop}"
export YOOKASSA_SECRET_KEY="${YOOKASSA_SECRET_KEY:-local-compose-yookassa-key}"
export PAYMENT_RETURN_URL="${PAYMENT_RETURN_URL:-https://example.invalid/payment-return}"
export COMPOSE_PARALLEL_LIMIT="${COMPOSE_PARALLEL_LIMIT:-2}"
export COMPOSE_BAKE="${COMPOSE_BAKE:-false}"
export COMPOSE_PROFILES="${COMPOSE_PROFILES:-core,app}"

bash scripts/dev-mtls.sh

docker compose --profile core --profile app down -v --remove-orphans

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
  local database="$1"
  local sql="$2"
  docker compose exec -T postgres psql --username="${POSTGRES_USER}" --dbname "$database" -tAc "$sql" | tr -d '[:space:]'
}

yookassa_webhook_status() {
  local body="$1"
  curl -sS -o /tmp/vpn-service-yookassa-response.json -w "%{http_code}" \
    --cacert secrets/dev-mtls/ca.crt \
    -H "Content-Type: application/json" \
    --data-binary "$body" \
    https://127.0.0.1:8084/webhooks/yookassa
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

build_services=(identity-migrate identity-service catalog-service billing-service yookassa-api telegram-api telegram-bot)
for service in "${build_services[@]}"; do
  docker compose --profile core --profile app build "$service"
done

docker compose --profile core --profile app up -d --no-build

containers=(
  vpn-service-postgres-1
  vpn-service-redis-1
  vpn-service-kafka-1
  vpn-service-identity-service-1
  vpn-service-catalog-service-1
  vpn-service-yookassa-api-1
  vpn-service-billing-service-1
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

docker stop vpn-service-telegram-bot-1 vpn-service-billing-service-1 >/dev/null
BILLING_TEST_DATABASE_URL="postgres://billing_app:${BILLING_DB_PASSWORD}@127.0.0.1:${POSTGRES_PORT}/billing_service?sslmode=disable" \
  go test ./services/billing/internal/postgres -run '^TestIntegration' -count=1
docker start vpn-service-billing-service-1 vpn-service-telegram-bot-1 >/dev/null
for _ in $(seq 1 60); do
  billing_status="$(docker inspect -f '{{.State.Health.Status}}' vpn-service-billing-service-1)"
  bot_status="$(docker inspect -f '{{.State.Health.Status}}' vpn-service-telegram-bot-1)"
  if [[ "$billing_status" == "healthy" && "$bot_status" == "healthy" ]]; then
    break
  fi
  sleep 2
done
if [[ "$billing_status" != "healthy" || "$bot_status" != "healthy" ]]; then
  echo "billing services did not recover after integration tests: billing=$billing_status bot=$bot_status" >&2
  exit 1
fi

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

identity_count="$(scalar_sql identity_service "SELECT count(*) FROM telegram_identities WHERE telegram_user_id = 4200001")"
consent_count="$(scalar_sql identity_service "SELECT count(*) FROM consents c JOIN telegram_identities t ON t.user_id = c.user_id WHERE t.telegram_user_id = 4200001 AND c.document_type = 'terms' AND c.document_version = 'terms-v1'")"
if [[ "$identity_count" != "1" || "$consent_count" != "1" ]]; then
  echo "unexpected database counts: identities=$identity_count consents=$consent_count" >&2
  exit 1
fi

curl -fsS -X POST http://127.0.0.1:8085/test/reset >/dev/null
curl -fsS -X POST -H "Content-Type: application/json" --data-binary '{"mode":"ambiguous_after_commit"}' http://127.0.0.1:8085/test/fail-next >/dev/null

buy_body='{"update_id":2003,"message":{"message_id":3,"text":"/buy","chat":{"id":9001},"from":{"id":4200001,"first_name":"Smoke","language_code":"en"}}}'
concurrent_buy_body='{"update_id":2004,"message":{"message_id":4,"text":"/buy","chat":{"id":9001},"from":{"id":4200001,"first_name":"Smoke","language_code":"en"}}}'
webhook_status "$buy_body" >/tmp/vpn-service-first-buy-status.txt &
first_buy_pid=$!
concurrent_buy_status="$(webhook_status "$concurrent_buy_body")"
wait "$first_buy_pid"
buy_status="$(cat /tmp/vpn-service-first-buy-status.txt)"
if [[ "$buy_status" != "200" || "$concurrent_buy_status" != "200" ]]; then
  echo "concurrent buy statuses: first=$buy_status second=$concurrent_buy_status, want 200/200" >&2
  exit 1
fi

provider_payment_id=""
for _ in $(seq 1 80); do
  curl -fsS http://127.0.0.1:8085/test/payments >/tmp/vpn-service-fake-yookassa.json
  provider_payment_id="$(scalar_sql billing_service "SELECT COALESCE(provider_payment_id,'') FROM payments LIMIT 1")"
  if grep -q '"count":1' /tmp/vpn-service-fake-yookassa.json &&
     grep -Eq '"create_attempts":[2-9][0-9]*' /tmp/vpn-service-fake-yookassa.json &&
     [[ -n "$provider_payment_id" ]]; then
    break
  fi
  sleep 0.25
done
if ! grep -q '"count":1' /tmp/vpn-service-fake-yookassa.json ||
   ! grep -Eq '"create_attempts":[2-9][0-9]*' /tmp/vpn-service-fake-yookassa.json ||
   [[ -z "$provider_payment_id" ]]; then
  echo "ambiguous provider create was not reconciled exactly once" >&2
  exit 1
fi

order_count="$(scalar_sql billing_service "SELECT count(*) FROM orders")"
payment_count="$(scalar_sql billing_service "SELECT count(*) FROM payments")"
curl -fsS http://127.0.0.1:8085/test/payments >/tmp/vpn-service-fake-yookassa.json
if [[ "$order_count" != "1" || "$payment_count" != "1" ]] || ! grep -q '"count":1' /tmp/vpn-service-fake-yookassa.json; then
  echo "duplicate buy created extra order or payment state" >&2
  exit 1
fi

curl -fsS -X POST -H "Content-Type: application/json" --data-binary '{"status":"succeeded"}' "http://127.0.0.1:8085/test/payments/${provider_payment_id}/status" >/dev/null
succeeded_webhook='{"type":"notification","event":"payment.succeeded","object":{"id":"'"${provider_payment_id}"'","status":"succeeded","sensitive_ignored":"must-not-persist"}}'
first_yoo_status="$(yookassa_webhook_status "$succeeded_webhook")"
duplicate_yoo_status="$(yookassa_webhook_status "$succeeded_webhook")"
if [[ "$first_yoo_status" != "200" || "$duplicate_yoo_status" != "200" ]]; then
  echo "unexpected YooKassa webhook statuses: first=$first_yoo_status duplicate=$duplicate_yoo_status" >&2
  exit 1
fi

payment_status=""
order_status=""
published_count=""
processed_inbox=""
for _ in $(seq 1 80); do
  payment_status="$(scalar_sql billing_service "SELECT status FROM payments LIMIT 1")"
  order_status="$(scalar_sql billing_service "SELECT status FROM orders LIMIT 1")"
  published_count="$(scalar_sql billing_service "SELECT count(*) FROM outbox WHERE topic='billing.payment.succeeded.v1' AND state='published'")"
  processed_inbox="$(scalar_sql billing_service "SELECT count(*) FROM webhook_inbox WHERE event_type='payment.succeeded' AND state='processed'")"
  if [[ "$payment_status" == "succeeded" && "$order_status" == "paid" && "$published_count" == "1" && "$processed_inbox" == "1" ]]; then
    break
  fi
  sleep 0.25
done
succeeded_inbox_count="$(scalar_sql billing_service "SELECT count(*) FROM webhook_inbox WHERE event_type='payment.succeeded'")"
if [[ "$payment_status" != "succeeded" || "$order_status" != "paid" || "$published_count" != "1" || "$processed_inbox" != "1" || "$succeeded_inbox_count" != "1" ]]; then
  echo "verified payment transition did not converge exactly once: payment=$payment_status order=$order_status published=$published_count processed_inbox=$processed_inbox inbox_count=$succeeded_inbox_count" >&2
  exit 1
fi

out_of_order_webhook='{"type":"notification","event":"payment.canceled","object":{"id":"'"${provider_payment_id}"'","status":"canceled"}}'
if [[ "$(yookassa_webhook_status "$out_of_order_webhook")" != "200" ]]; then
  echo "out-of-order webhook did not return 200" >&2
  exit 1
fi
canceled_inbox_state=""
for _ in $(seq 1 80); do
  canceled_inbox_state="$(scalar_sql billing_service "SELECT state FROM webhook_inbox WHERE event_type='payment.canceled' LIMIT 1")"
  if [[ "$canceled_inbox_state" == "processed" ]]; then
    break
  fi
  sleep 0.25
done
terminal_event_count="$(scalar_sql billing_service "SELECT count(*) FROM outbox")"
payment_status="$(scalar_sql billing_service "SELECT status FROM payments LIMIT 1")"
if [[ "$canceled_inbox_state" != "processed" || "$terminal_event_count" != "1" || "$payment_status" != "succeeded" ]]; then
  echo "out-of-order webhook changed a terminal payment or duplicated its event" >&2
  exit 1
fi

docker compose exec -T kafka /opt/kafka/bin/kafka-console-consumer.sh --bootstrap-server localhost:9092 --topic billing.payment.succeeded.v1 --from-beginning --max-messages 1 --timeout-ms 10000 >/tmp/vpn-service-kafka-event.json 2>/dev/null
grep -q '"event_type":"billing.payment.succeeded.v1"' /tmp/vpn-service-kafka-event.json

go run ./tools/mtlsprobe/cmd/mtlsprobe PUT https://127.0.0.1:8080/internal/v1/telegram-users/999 secrets/dev-mtls/identity-health.crt secrets/dev-mtls/identity-health.key secrets/dev-mtls/ca.crt 403
go run ./tools/mtlsprobe/cmd/mtlsprobe POST https://127.0.0.1:8080/internal/v1/users/00000000-0000-4000-8000-000000000001/consents secrets/dev-mtls/billing-service.crt secrets/dev-mtls/billing-service.key secrets/dev-mtls/ca.crt 403
go run ./tools/mtlsprobe/cmd/mtlsprobe POST https://127.0.0.1:8084/internal/v1/users/00000000-0000-4000-8000-000000000001/orders secrets/dev-mtls/billing-service.crt secrets/dev-mtls/billing-service.key secrets/dev-mtls/ca.crt 403
go run ./tools/mtlsprobe/cmd/mtlsprobe GET https://127.0.0.1:8084/internal/v1/users/00000000-0000-4000-8000-000000000001/orders/00000000-0000-4000-8000-000000000002 secrets/dev-mtls/identity-health.crt secrets/dev-mtls/identity-health.key secrets/dev-mtls/ca.crt 403

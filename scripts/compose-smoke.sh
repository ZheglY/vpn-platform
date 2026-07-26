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
export SUBSCRIPTION_DB_PASSWORD="${SUBSCRIPTION_DB_PASSWORD:-local-compose-subscription}"
export ACCESS_DB_PASSWORD="${ACCESS_DB_PASSWORD:-local-compose-access}"
export PROVISIONING_DB_PASSWORD="${PROVISIONING_DB_PASSWORD:-local-compose-provisioning}"
export NOTIFICATION_DB_PASSWORD="${NOTIFICATION_DB_PASSWORD:-local-compose-notification}"
export ADMIN_DB_PASSWORD="${ADMIN_DB_PASSWORD:-local-compose-admin}"
export ADMIN_MIGRATOR_DB_PASSWORD="${ADMIN_MIGRATOR_DB_PASSWORD:-local-compose-admin-migrator}"
export ACCESS_CREDENTIAL_KEY_BASE64="${ACCESS_CREDENTIAL_KEY_BASE64:-MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=}"
export ACCESS_TOKEN_HMAC_KEY_BASE64="${ACCESS_TOKEN_HMAC_KEY_BASE64:-ZmVkY2JhOTg3NjU0MzIxMGZlZGNiYTk4NzY1NDMyMTA=}"
export SUBSCRIPTION_PUBLIC_BASE_URL="${SUBSCRIPTION_PUBLIC_BASE_URL:-https://127.0.0.1:8087}"
export TELEGRAM_WEBHOOK_SECRET="${TELEGRAM_WEBHOOK_SECRET:-local-compose-webhook-secret}"
export TELEGRAM_BOT_TOKEN="${TELEGRAM_BOT_TOKEN:-local-compose-fake-bot-token}"
export FAKE_TELEGRAM_SEND_DELAY="${FAKE_TELEGRAM_SEND_DELAY:-1s}"
export TERMS_URL="${TERMS_URL:-https://example.invalid/terms/terms-v1}"
export YOOKASSA_SHOP_ID="${YOOKASSA_SHOP_ID:-test-shop}"
export YOOKASSA_SECRET_KEY="${YOOKASSA_SECRET_KEY:-local-compose-yookassa-key}"
export PAYMENT_RETURN_URL="${PAYMENT_RETURN_URL:-https://example.invalid/payment-return}"
export COMPOSE_PARALLEL_LIMIT="${COMPOSE_PARALLEL_LIMIT:-2}"
export COMPOSE_BAKE="${COMPOSE_BAKE:-false}"
compose_project="${COMPOSE_PROJECT_NAME:-vpn-service}"
if [[ ! "$compose_project" =~ ^[a-z0-9][a-z0-9-]*$ ]]; then
  echo 'COMPOSE_PROJECT_NAME must contain only lowercase letters, digits, and hyphens' >&2
  exit 1
fi
compose_container() {
  printf '%s-%s-1' "$compose_project" "$1"
}
vpn_data_network="${compose_project}_vpn-data"
full_vpn="${VPN_SMOKE_FULL_CONTROL_PLANE:-0}"
test_vpn_failover="${VPN_SMOKE_TEST_FAILOVER:-0}"
stage7_extended="${STAGE7_EXTENDED_SMOKE:-0}"
observability="${OBSERVABILITY_SMOKE:-0}"
if [[ "$stage7_extended" == "1" && "$full_vpn" != "1" ]]; then
  echo 'Stage 7 extended smoke requires the full VPN control plane' >&2
  exit 1
fi
if [[ "$test_vpn_failover" == "1" && "$full_vpn" != "1" ]]; then
  echo 'VPN failover smoke requires the full VPN control plane' >&2
  exit 1
fi
profiles=(--profile core --profile app)
profile_names=(core app)
if [[ "$full_vpn" == "1" ]]; then
  profiles+=(--profile vpn)
  profile_names+=(vpn)
fi
if [[ "$observability" == "1" ]]; then
  profiles+=(--profile obs)
  profile_names+=(obs)
  if [[ "$full_vpn" == "1" ]]; then
    export PROMETHEUS_CONFIG_FILE="./deploy/observability/prometheus/prometheus-vpn.yml"
  else
    export PROMETHEUS_CONFIG_FILE="./deploy/observability/prometheus/prometheus.yml"
  fi
fi
export COMPOSE_PROFILES
COMPOSE_PROFILES="$(IFS=,; echo "${profile_names[*]}")"
vpn_client_name="${compose_project}-stage6-client"
vpn_client_image='ghcr.io/xtls/xray-core:26.3.27@sha256:592ec4d11f656db95598d01e76dbcc6e002d67360b96a5436500a938230f52c7'
vpn_client_config="tmp/${compose_project}-stage6-client.json"

bash scripts/dev-mtls.sh
if [[ "$full_vpn" == "1" ]]; then bash scripts/dev-xray.sh; fi

docker compose "${profiles[@]}" down -v --remove-orphans

cleanup() {
  docker rm -f "$vpn_client_name" >/dev/null 2>&1 || true
  rm -f "$vpn_client_config"
  if [[ "${SMOKE_KEEP_STACK:-0}" == "1" ]]; then
    echo 'SMOKE_KEEP_STACK=1: Compose services and volumes were preserved for diagnostics.' >&2
  else
    docker compose "${profiles[@]}" down -v --remove-orphans >/dev/null 2>&1 || true
    if [[ "${SMOKE_DISPOSABLE_PROJECT:-0}" == "1" ]]; then
      docker volume rm "${compose_project}-go-mod-cache" "${compose_project}-go-build-cache" >/dev/null 2>&1 || true
    fi
  fi
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

prometheus_scalar() {
  local query="$1"
  curl -fsS --get --data-urlencode "query=${query}" http://127.0.0.1:9090/api/v1/query |
    node -e 'let b="";process.stdin.on("data",d=>b+=d).on("end",()=>{const r=JSON.parse(b).data.result;process.stdout.write(r.length?r[0].value[1]:"0")})'
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

build_services=(mtls-credentials-init identity-migrate identity-service catalog-service billing-service subscription-service access-service provisioning-migrate notification-service admin-service yookassa-api telegram-api telegram-bot)
if [[ "$full_vpn" == "1" ]]; then build_services+=(provisioning-service node-agent-primary); fi
if [[ "$observability" == "1" ]]; then build_services+=(prometheus grafana otel-collector tempo loki); fi
for service in "${build_services[@]}"; do
  docker compose "${profiles[@]}" build "$service"
done

docker compose "${profiles[@]}" up -d --no-build

containers=(
  "$(compose_container postgres)"
  "$(compose_container redis)"
  "$(compose_container kafka)"
  "$(compose_container identity-service)"
  "$(compose_container catalog-service)"
  "$(compose_container yookassa-api)"
  "$(compose_container billing-service)"
  "$(compose_container subscription-service)"
  "$(compose_container access-service)"
  "$(compose_container notification-service)"
  "$(compose_container admin-service)"
  "$(compose_container telegram-api)"
  "$(compose_container telegram-bot)"
)
if [[ "$full_vpn" == "1" ]]; then
  containers+=(
    "$(compose_container provisioning-service)"
    "$(compose_container node-agent-primary)"
    "$(compose_container node-agent-failover)"
  )
fi
if [[ "$observability" == "1" ]]; then
  containers+=(
    "$(compose_container prometheus)"
    "$(compose_container grafana)"
    "$(compose_container otel-collector)"
    "$(compose_container tempo)"
    "$(compose_container loki)"
  )
fi

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
    docker compose "${profiles[@]}" ps
    docker compose "${profiles[@]}" logs --tail=200
    exit 1
  fi
done

if [[ "$observability" == "1" ]]; then
  expected_targets=8
  if [[ "$full_vpn" == "1" ]]; then expected_targets=11; fi
  healthy_targets=0
  for _ in $(seq 1 30); do
    healthy_targets="$(
      curl -fsS --get --data-urlencode 'query=sum(up{job=~"vpn-control-plane|vpn-provisioning|vpn-nodes"} == 1)' \
        http://127.0.0.1:9090/api/v1/query |
        node -e 'let b="";process.stdin.on("data",d=>b+=d).on("end",()=>{const r=JSON.parse(b).data.result;process.stdout.write(r.length?r[0].value[1]:"0")})'
    )"
    if [[ "$healthy_targets" == "$expected_targets" ]]; then break; fi
    sleep 2
  done
  if [[ "$healthy_targets" != "$expected_targets" ]]; then
    echo "Prometheus has ${healthy_targets} healthy VPN targets, expected ${expected_targets}" >&2
    exit 1
  fi
  expected_pools=7
  expected_message_snapshots=4
  expected_domain_snapshots=3
  if [[ "$full_vpn" == "1" ]]; then
    expected_pools=8
    expected_message_snapshots=5
    expected_domain_snapshots=4
  fi
  for _ in $(seq 1 30); do
    pool_count="$(prometheus_scalar 'count(vpn_platform_postgres_pool_connections{state="max"})')"
    message_snapshots="$(prometheus_scalar 'sum(vpn_platform_message_snapshot_success == 1)')"
    domain_snapshots="$(prometheus_scalar 'sum(vpn_platform_domain_snapshot_success == 1)')"
    billing_snapshots="$(prometheus_scalar 'sum(vpn_billing_metrics_snapshot_success == 1)')"
    subscription_snapshots="$(prometheus_scalar 'sum(vpn_subscription_metrics_snapshot_success == 1)')"
    provisioning_snapshots=0
    healthy_xray=0
    if [[ "$full_vpn" == "1" ]]; then
      provisioning_snapshots="$(prometheus_scalar 'sum(vpn_provisioning_metrics_snapshot_success == 1)')"
      healthy_xray="$(prometheus_scalar 'sum(vpn_node_xray_healthy == 1)')"
    fi
    if [[ "$pool_count" == "$expected_pools" && "$message_snapshots" == "$expected_message_snapshots" && "$domain_snapshots" == "$expected_domain_snapshots" && "$billing_snapshots" == "1" && "$subscription_snapshots" == "1" ]] &&
      [[ "$full_vpn" != "1" || ( "$provisioning_snapshots" == "1" && "$healthy_xray" == "2" ) ]]; then
      break
    fi
    sleep 2
  done
  if [[ "$pool_count" != "$expected_pools" || "$message_snapshots" != "$expected_message_snapshots" || "$domain_snapshots" != "$expected_domain_snapshots" || "$billing_snapshots" != "1" || "$subscription_snapshots" != "1" ]] ||
    [[ "$full_vpn" == "1" && ( "$provisioning_snapshots" != "1" || "$healthy_xray" != "2" ) ]]; then
    echo "Operational metrics incomplete: pools=${pool_count}/${expected_pools} message_snapshots=${message_snapshots}/${expected_message_snapshots} domain_snapshots=${domain_snapshots}/${expected_domain_snapshots} billing=${billing_snapshots} subscription=${subscription_snapshots} provisioning=${provisioning_snapshots} xray=${healthy_xray}" >&2
    exit 1
  fi
  curl -fsS http://127.0.0.1:3000/api/health |
    node -e 'let b="";process.stdin.on("data",d=>b+=d).on("end",()=>{if(JSON.parse(b).database!=="ok")process.exit(1)})'
  curl -fsS 'http://127.0.0.1:3000/api/search?query=VPN%20Platform%20Overview' |
    node -e 'let b="";process.stdin.on("data",d=>b+=d).on("end",()=>{if(!JSON.parse(b).some(x=>x.uid==="vpn-platform-overview"))process.exit(1)})'
  for datasource_uid in tempo loki; do
    curl -fsS "http://127.0.0.1:3000/api/datasources/uid/${datasource_uid}" |
      node -e 'let b="";const want=process.argv[1];process.stdin.on("data",d=>b+=d).on("end",()=>{if(JSON.parse(b).uid!==want)process.exit(1)})' "$datasource_uid"
  done

  trace_id='4bf92f3577b34da6a3ce929d0e0e4736'
  trace_parent="00-${trace_id}-00f067aa0ba902b7-01"
  privacy_sentinel='stage8-private-url-value'
  go run ./tools/mtlsprobe/cmd/mtlsprobe \
    GET "https://127.0.0.1:8080/version?probe=${privacy_sentinel}" \
    secrets/dev-mtls/identity-health.crt secrets/dev-mtls/identity-health.key secrets/dev-mtls/ca.crt \
    200 traceparent "$trace_parent"
  loki_query='%7Bservice_name%3D%22identity-service%22%7D%20%7C%3D%20%22http%20request%22'
  tempo_json=''
  loki_json=''
  for _ in $(seq 1 30); do
    tempo_json="$(curl -fsS "http://127.0.0.1:3000/api/datasources/proxy/uid/tempo/api/traces/${trace_id}" || true)"
    loki_json="$(curl -fsS "http://127.0.0.1:3000/api/datasources/proxy/uid/loki/loki/api/v1/query_range?query=${loki_query}&limit=100&direction=backward" || true)"
    if [[ "$tempo_json" == *"GET /version"* && "$loki_json" == *"$trace_id"* ]]; then break; fi
    sleep 2
  done
  if [[ "$tempo_json" != *"GET /version"* ]]; then
    echo 'known W3C trace did not reach Tempo' >&2
    exit 1
  fi
  if [[ "$loki_json" != *"$trace_id"* || "$loki_json" != *"http request"* ]]; then
    echo 'trace-correlated structured log did not reach Loki' >&2
    exit 1
  fi
  if [[ "$tempo_json" == *"$privacy_sentinel"* || "$loki_json" == *"$privacy_sentinel"* ||
        "$tempo_json" == *'/version?probe='* || "$loki_json" == *'/version?probe='* ]]; then
    echo 'telemetry backend leaked raw URL data' >&2
    exit 1
  fi
fi

if [[ "$full_vpn" != "1" ]]; then
integration_containers=(
  "$(compose_container admin-service)"
  "$(compose_container notification-service)"
  "$(compose_container telegram-bot)"
  "$(compose_container access-service)"
  "$(compose_container subscription-service)"
  "$(compose_container billing-service)"
)
docker stop "${integration_containers[@]}" >/dev/null
BILLING_TEST_DATABASE_URL="postgres://billing_app:${BILLING_DB_PASSWORD}@127.0.0.1:${POSTGRES_PORT}/billing_service?sslmode=disable" \
  go test ./services/billing/internal/postgres -run '^TestIntegration' -count=1
SUBSCRIPTION_TEST_DATABASE_URL="postgres://subscription_app:${SUBSCRIPTION_DB_PASSWORD}@127.0.0.1:${POSTGRES_PORT}/subscription_service?sslmode=disable" \
  go test ./services/subscription/internal/postgres -run '^TestIntegration' -count=1
ACCESS_TEST_DATABASE_URL="postgres://access_app:${ACCESS_DB_PASSWORD}@127.0.0.1:${POSTGRES_PORT}/access_service?sslmode=disable" \
  go test ./services/access/internal/postgres -run '^TestIntegration' -count=1
docker compose "${profiles[@]}" run --rm provisioning-migrate
PROVISIONING_TEST_DATABASE_URL="postgres://provisioning_app:${PROVISIONING_DB_PASSWORD}@127.0.0.1:${POSTGRES_PORT}/provisioning_service?sslmode=disable" \
  go test ./services/provisioning/internal/postgres -run '^TestIntegration' -count=1
ACCESS_TEST_REDIS_ADDR="127.0.0.1:6379" ACCESS_TEST_REDIS_PASSWORD="${REDIS_PASSWORD}" \
  go test ./services/access/internal/ratelimit -run '^TestIntegration' -count=1
NOTIFICATION_TEST_DATABASE_URL="postgres://notification_app:${NOTIFICATION_DB_PASSWORD}@127.0.0.1:${POSTGRES_PORT}/notification_service?sslmode=disable" \
  go test ./services/notification/internal/postgres -run '^TestIntegration' -count=1
ADMIN_TEST_DATABASE_URL="postgres://admin_app:${ADMIN_DB_PASSWORD}@127.0.0.1:${POSTGRES_PORT}/admin_service?sslmode=disable" \
ADMIN_MIGRATOR_TEST_DATABASE_URL="postgres://admin_migrator:${ADMIN_MIGRATOR_DB_PASSWORD}@127.0.0.1:${POSTGRES_PORT}/admin_service?sslmode=disable" \
  go test ./services/admin/internal/postgres -run '^TestIntegration' -count=1
docker start \
  "$(compose_container billing-service)" \
  "$(compose_container subscription-service)" \
  "$(compose_container access-service)" \
  "$(compose_container telegram-bot)" \
  "$(compose_container notification-service)" \
  "$(compose_container admin-service)" >/dev/null
for _ in $(seq 1 60); do
  billing_status="$(docker inspect -f '{{.State.Health.Status}}' "$(compose_container billing-service)")"
  subscription_status="$(docker inspect -f '{{.State.Health.Status}}' "$(compose_container subscription-service)")"
  access_status="$(docker inspect -f '{{.State.Health.Status}}' "$(compose_container access-service)")"
  bot_status="$(docker inspect -f '{{.State.Health.Status}}' "$(compose_container telegram-bot)")"
  notification_status="$(docker inspect -f '{{.State.Health.Status}}' "$(compose_container notification-service)")"
  admin_status="$(docker inspect -f '{{.State.Health.Status}}' "$(compose_container admin-service)")"
  if [[ "$billing_status" == "healthy" && "$subscription_status" == "healthy" && "$access_status" == "healthy" && "$bot_status" == "healthy" && "$notification_status" == "healthy" && "$admin_status" == "healthy" ]]; then
    break
  fi
  sleep 2
done
if [[ "$billing_status" != "healthy" || "$subscription_status" != "healthy" || "$access_status" != "healthy" || "$bot_status" != "healthy" || "$notification_status" != "healthy" || "$admin_status" != "healthy" ]]; then
  echo "services did not recover after integration tests: billing=$billing_status subscription=$subscription_status access=$access_status bot=$bot_status notification=$notification_status admin=$admin_status" >&2
  exit 1
fi
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
    docker compose "${profiles[@]}" ps
    docker compose "${profiles[@]}" logs --tail=200
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

subscription_status=""
subscription_period_count=""
subscription_inbox_count=""
activation_published_count=""
for _ in $(seq 1 80); do
  subscription_status="$(scalar_sql subscription_service "SELECT status FROM subscriptions LIMIT 1")"
  subscription_period_count="$(scalar_sql subscription_service "SELECT count(*) FROM subscription_periods")"
  subscription_inbox_count="$(scalar_sql subscription_service "SELECT count(*) FROM inbox WHERE event_type='billing.payment.succeeded.v1' AND state='processed'")"
  activation_published_count="$(scalar_sql subscription_service "SELECT count(*) FROM outbox WHERE topic='subscription.activated.v1' AND state='published'")"
  if [[ "$subscription_status" == "active" && "$subscription_period_count" == "1" && "$subscription_inbox_count" == "1" && "$activation_published_count" == "1" ]]; then
    break
  fi
  sleep 0.25
done
if [[ "$subscription_status" != "active" || "$subscription_period_count" != "1" || "$subscription_inbox_count" != "1" || "$activation_published_count" != "1" ]]; then
  echo "subscription activation did not converge exactly once" >&2
  exit 1
fi
subscription_user_id="$(scalar_sql subscription_service "SELECT user_id FROM subscriptions LIMIT 1")"
subscription_id="$(scalar_sql subscription_service "SELECT id FROM subscriptions LIMIT 1")"

access_credential_id=""
for _ in $(seq 1 80); do
  access_credential_id="$(scalar_sql access_service "SELECT COALESCE((SELECT id::text FROM access_credentials WHERE subscription_id='${subscription_id}' LIMIT 1),'')")"
  access_provision_published="$(scalar_sql access_service "SELECT count(*) FROM outbox WHERE topic='access.provision.request.v1' AND state='published' AND aggregate_id IN (SELECT id FROM access_credentials WHERE subscription_id='${subscription_id}')")"
  if [[ -n "$access_credential_id" && "$access_provision_published" == "1" ]]; then
    break
  fi
  sleep 0.25
done
if [[ -z "$access_credential_id" || "$access_provision_published" != "1" ]]; then
  echo "access provisioning request was not created exactly once" >&2
  exit 1
fi
if [[ "$full_vpn" == "1" ]]; then
  for _ in $(seq 1 120); do
    access_credential_status="$(scalar_sql access_service "SELECT status FROM access_credentials WHERE id='${access_credential_id}'")"
    provision_outcome_published="$(scalar_sql provisioning_service "SELECT count(*) FROM outbox WHERE topic='access.provision.succeeded.v1' AND state='published' AND aggregate_id='${access_credential_id}'")"
    access_ready_published="$(scalar_sql access_service "SELECT count(*) FROM outbox WHERE topic='access.ready.v1' AND state='published'")"
    if [[ "$access_credential_status" == "active" && "$provision_outcome_published" == "1" && "$access_ready_published" == "1" ]]; then break; fi
    sleep 0.5
  done
  assigned_nodes="$(scalar_sql access_service "SELECT count(*) FROM access_assignment_snapshots WHERE credential_id='${access_credential_id}' AND allocation_revision=1")"
  provisioning_allocations="$(scalar_sql provisioning_service "SELECT count(*) FROM allocations WHERE credential_id='${access_credential_id}' AND state='active'")"
  if [[ "$access_credential_status" != "active" || "$provision_outcome_published" != "1" || "$access_ready_published" != "1" || "$assigned_nodes" != "2" || "$provisioning_allocations" != "2" ]]; then
    docker compose logs --tail=100 provisioning-service access-service node-agent-primary node-agent-failover
    echo "full provisioning did not converge" >&2
    exit 1
  fi
  material_audit_count="$(scalar_sql access_service "SELECT count(*) FROM security_audit_events WHERE credential_id='${access_credential_id}' AND actor_service='provisioning-service' AND action='credential_material.read' AND outcome='succeeded'")"
  if (( material_audit_count < 1 )); then echo 'full provisioning material read was not audited' >&2; exit 1; fi
else
access_operation_id="$(scalar_sql access_service "SELECT id FROM access_operations WHERE kind='provision' LIMIT 1")"
access_command_event_id="$(scalar_sql access_service "SELECT event_id FROM outbox WHERE topic='access.provision.request.v1' LIMIT 1")"
provision_result='{"event_id":"51000000-0000-4000-8000-000000000001","event_type":"access.provision.succeeded.v1","schema_version":1,"occurred_at":"2026-07-18T12:00:05Z","producer":"provisioning-service","correlation_id":"51000000-0000-4000-8000-000000000002","causation_id":"'"${access_command_event_id}"'","aggregate_type":"credential","aggregate_id":"'"${access_credential_id}"'","aggregate_sequence":1,"partition_key":"credential:'"${access_credential_id}"'","data":{"operation_id":"'"${access_operation_id}"'","credential_id":"'"${access_credential_id}"'","applied_revision":1,"status":"active","assigned_node_ids":["51000000-0000-4000-8000-000000000003","51000000-0000-4000-8000-000000000004"],"endpoints":[{"node_id":"51000000-0000-4000-8000-000000000003","role":"primary","address":"vpn.example.invalid","port":443,"server_name":"cdn.example.invalid","reality_public_key":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA","short_id":"0011aabb","spider_x":"/","label":"VPN Primary"},{"node_id":"51000000-0000-4000-8000-000000000004","role":"failover","address":"backup.example.invalid","port":443,"server_name":"www.example.invalid","reality_public_key":"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB","short_id":"2233ccdd","label":"VPN Failover"}],"applied_at":"2026-07-18T12:00:05Z"}}'
printf 'credential:%s|%s\n' "$access_credential_id" "$provision_result" | docker compose exec -T kafka /opt/kafka/bin/kafka-console-producer.sh --bootstrap-server localhost:9092 --topic access.provision.succeeded.v1 --reader-property parse.key=true --reader-property 'key.separator=|'
for _ in $(seq 1 80); do
  access_credential_status="$(scalar_sql access_service "SELECT status FROM access_credentials WHERE id='${access_credential_id}'")"
  access_ready_published="$(scalar_sql access_service "SELECT count(*) FROM outbox WHERE topic='access.ready.v1' AND state='published'")"
  if [[ "$access_credential_status" == "active" && "$access_ready_published" == "1" ]]; then
    break
  fi
  sleep 0.25
done
if [[ "$access_credential_status" != "active" || "$access_ready_published" != "1" ]]; then
  access_provision_inbox="$(scalar_sql access_service "SELECT count(*) FROM inbox WHERE event_type='access.provision.succeeded.v1'")"
  access_dead_letter_reasons="$(scalar_sql access_service "SELECT COALESCE(string_agg(reason_code,','),'') FROM consumer_dead_letters WHERE topic='access.provision.succeeded.v1'")"
  access_operation_status="$(scalar_sql access_service "SELECT status FROM access_operations WHERE id='${access_operation_id}'")"
  docker compose logs --tail=80 access-service
  echo "access provisioning result did not converge: credential=$access_credential_status ready=$access_ready_published inbox=$access_provision_inbox operation=$access_operation_status dead_letters=$access_dead_letter_reasons" >&2
  exit 1
fi
fi

issue_json="$(go run ./tools/mtlsprobe/cmd/mtlsprobe POST "https://127.0.0.1:8087/internal/v1/subscriptions/${subscription_id}/subscription-url/issue" secrets/dev-mtls/telegram-bot.crt secrets/dev-mtls/telegram-bot.key secrets/dev-mtls/ca.crt 200 Idempotency-Key smoke-issue-0001 print-body)"
issued_url="$(printf '%s' "$issue_json" | sed -n 's/.*"subscription_url":"\([^"]*\)".*/\1/p')"
if [[ -z "$issued_url" ]]; then
  echo "access issue endpoint did not return a subscription URL" >&2
  exit 1
fi
go run ./tools/mtlsprobe/cmd/mtlsprobe POST "https://127.0.0.1:8087/internal/v1/subscriptions/${subscription_id}/subscription-url/issue" secrets/dev-mtls/telegram-bot.crt secrets/dev-mtls/telegram-bot.key secrets/dev-mtls/ca.crt 409 Idempotency-Key smoke-issue-0001
profile_status="$(curl -sS -D tmp/stage5-profile-headers.txt -o tmp/stage5-profile-body.txt -w '%{http_code}' --cacert secrets/dev-mtls/ca.crt "$issued_url")"
if [[ "$profile_status" != "200" ]] || ! grep -qi '^cache-control: no-store' tmp/stage5-profile-headers.txt || ! grep -qi '^profile-title: VPN Platform' tmp/stage5-profile-headers.txt || ! grep -q '^vless://' tmp/stage5-profile-body.txt; then
  echo "Happ subscription response was not compatible or no-store" >&2
  exit 1
fi
vpn_credential_uuid=""
if [[ "$full_vpn" == "1" ]]; then
  vpn_credential_uuid="$(sed -n 's#^vless://\([0-9a-fA-F-]\{36\}\)@.*#\1#p' tmp/stage5-profile-body.txt | head -n 1)"
  if [[ -z "$vpn_credential_uuid" ]]; then echo 'Happ profile did not contain a VLESS credential' >&2; exit 1; fi
fi
issued_token="${issued_url##*/}"
if docker compose logs access-service | grep -Fq "$issued_token"; then
  echo "subscription token leaked into access-service logs" >&2
  exit 1
fi
rm -f tmp/stage5-profile-headers.txt tmp/stage5-profile-body.txt

subscription_base="${issued_url%/*}"
for malformed_path in "$subscription_base" "$subscription_base/" "$subscription_base/a/b"; do
  malformed_status="$(curl -sS -D tmp/stage5-malformed-headers.txt -o tmp/stage5-malformed-body.txt -w '%{http_code}' --cacert secrets/dev-mtls/ca.crt "$malformed_path")"
  if [[ "$malformed_status" != "404" ]] || ! grep -qi '^cache-control: no-store' tmp/stage5-malformed-headers.txt || [[ "$(cat tmp/stage5-malformed-body.txt)" != "subscription unavailable" ]]; then
    echo "malformed subscription path did not use the generic no-store 404" >&2
    exit 1
  fi
done
rm -f tmp/stage5-malformed-headers.txt tmp/stage5-malformed-body.txt

if [[ "$stage7_extended" == "1" ]]; then
  if ! bash scripts/stage7-e2e.sh BeforeRevoke "$subscription_user_id" "$subscription_id" "$access_credential_id"; then
    echo "Stage 7 notification job counts (type|status|count):"
    docker compose exec -T postgres psql --username="$POSTGRES_USER" --dbname notification_service -tAc "SELECT notification_type || '|' || status || '|' || count(*) FROM notification_jobs GROUP BY notification_type,status ORDER BY notification_type,status"
    echo "Stage 7 notification inbox counts (event_type|count):"
    docker compose exec -T postgres psql --username="$POSTGRES_USER" --dbname notification_service -tAc "SELECT event_type || '|' || count(*) FROM notification_inbox GROUP BY event_type ORDER BY event_type"
    echo "Stage 7 notification DLQ counts (reason|count):"
    docker compose exec -T postgres psql --username="$POSTGRES_USER" --dbname notification_service -tAc "SELECT reason_code || '|' || count(*) FROM notification_dead_letters GROUP BY reason_code ORDER BY reason_code"
    docker compose logs --tail=120 notification-service telegram-bot
    exit 1
  fi
fi

if [[ "$full_vpn" == "1" ]]; then
  sed -E 's/("id": ")[0-9a-f-]{36}(")/\1'"${vpn_credential_uuid}"'\2/' secrets/dev-xray/smoke-client.json >"$vpn_client_config"
  docker run --rm -v "$(pwd)/${vpn_client_config}:/etc/xray/client.json:ro" "$vpn_client_image" run -test -config /etc/xray/client.json >/dev/null
  docker run -d --name "$vpn_client_name" --network "$vpn_data_network" -p 127.0.0.1:11080:1080 -v "$(pwd)/${vpn_client_config}:/etc/xray/client.json:ro" "$vpn_client_image" run -config /etc/xray/client.json >/dev/null
  vpn_body=''
  for _ in $(seq 1 40); do
    vpn_body="$(curl -sS --socks5-hostname 127.0.0.1:11080 --connect-timeout 2 --max-time 5 http://camouflage.local/ 2>/dev/null || true)"
    if [[ "$vpn_body" == *'local camouflage endpoint'* ]]; then break; fi
    sleep 0.5
  done
  if [[ "$vpn_body" != *'local camouflage endpoint'* ]]; then echo 'full-control-plane VLESS + REALITY request failed' >&2; exit 1; fi

  if [[ "$test_vpn_failover" == "1" ]]; then
    docker compose stop node-agent-primary >/dev/null
    docker rm -f "$vpn_client_name" >/dev/null
    sed -E 's/("id": ")[0-9a-f-]{36}(")/\1'"${vpn_credential_uuid}"'\2/' secrets/dev-xray/smoke-client-failover.json >"$vpn_client_config"
    docker run --rm -v "$(pwd)/${vpn_client_config}:/etc/xray/client.json:ro" "$vpn_client_image" run -test -config /etc/xray/client.json >/dev/null
    docker run -d --name "$vpn_client_name" --network "$vpn_data_network" -p 127.0.0.1:11080:1080 -v "$(pwd)/${vpn_client_config}:/etc/xray/client.json:ro" "$vpn_client_image" run -config /etc/xray/client.json >/dev/null
    failover_body=''
    for _ in $(seq 1 40); do
      failover_body="$(curl -sS --socks5-hostname 127.0.0.1:11080 --connect-timeout 2 --max-time 5 http://camouflage.local/ 2>/dev/null || true)"
      if [[ "$failover_body" == *'local camouflage endpoint'* ]]; then break; fi
      sleep 0.5
    done
    if [[ "$failover_body" != *'local camouflage endpoint'* ]]; then
      echo 'VPN traffic did not survive primary node loss' >&2
      exit 1
    fi
    docker compose start node-agent-primary >/dev/null
    wait_until 'primary node did not recover after failover drill' docker compose exec -T node-agent-primary /node-agent healthcheck
  fi

  go run ./tools/mtlsprobe/cmd/mtlsprobe GET "https://127.0.0.1:18443/internal/v1/credentials/${access_credential_id}" secrets/dev-mtls/identity-health.crt secrets/dev-mtls/identity-health.key secrets/dev-mtls/ca.crt 403

  payment_id="$(scalar_sql billing_service 'SELECT id FROM payments LIMIT 1')"
  order_id="$(scalar_sql billing_service 'SELECT id FROM orders LIMIT 1')"
  amount_minor="$(scalar_sql billing_service "SELECT amount_minor FROM orders WHERE id='${order_id}'")"
  currency="$(scalar_sql billing_service "SELECT currency FROM orders WHERE id='${order_id}'")"
  refunded_at="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"
  refund_id='52000000-0000-4000-8000-000000000001'
  refund_event='{"event_id":"52000000-0000-4000-8000-000000000002","event_type":"billing.refund.succeeded.v1","schema_version":1,"occurred_at":"'"${refunded_at}"'","producer":"billing-service","correlation_id":"52000000-0000-4000-8000-000000000003","causation_id":null,"aggregate_type":"refund","aggregate_id":"'"${refund_id}"'","partition_key":"user:'"${subscription_user_id}"'","data":{"refund_id":"'"${refund_id}"'","payment_id":"'"${payment_id}"'","order_id":"'"${order_id}"'","user_id":"'"${subscription_user_id}"'","amount_minor":'"${amount_minor}"',"currency":"'"${currency}"'","refund_scope":"full","refunded_at":"'"${refunded_at}"'"}}'
  printf 'user:%s|%s\n' "$subscription_user_id" "$refund_event" | docker compose exec -T kafka /opt/kafka/bin/kafka-console-producer.sh --bootstrap-server localhost:9092 --topic billing.refund.succeeded.v1 --reader-property parse.key=true --reader-property 'key.separator=|'

  subscription_revoked=''
  revoke_outcome_published=''
  revoked_allocations=''
  for _ in $(seq 1 180); do
    subscription_revoked="$(scalar_sql subscription_service "SELECT count(*) FROM outbox WHERE topic='subscription.revoked.v1' AND state='published' AND aggregate_id='${subscription_id}'")"
    access_credential_status="$(scalar_sql access_service "SELECT status FROM access_credentials WHERE id='${access_credential_id}'")"
    revoke_outcome_published="$(scalar_sql provisioning_service "SELECT count(*) FROM outbox WHERE topic='access.revoke.succeeded.v1' AND state='published' AND aggregate_id='${access_credential_id}'")"
    revoked_allocations="$(scalar_sql provisioning_service "SELECT count(*) FROM allocations WHERE credential_id='${access_credential_id}' AND state='revoked'")"
    if [[ "$subscription_revoked" == "1" && "$access_credential_status" == "revoked" && "$revoke_outcome_published" == "1" && "$revoked_allocations" == "2" ]]; then break; fi
    sleep 0.5
  done
  if [[ "$subscription_revoked" != "1" || "$access_credential_status" != "revoked" || "$revoke_outcome_published" != "1" || "$revoked_allocations" != "2" ]]; then
    docker compose logs --tail=120 subscription-service access-service provisioning-service node-agent-primary node-agent-failover
    echo 'full revoke did not converge' >&2
    exit 1
  fi
  if curl -sS --socks5-hostname 127.0.0.1:11080 --connect-timeout 3 --max-time 5 http://camouflage.local/ >/dev/null 2>&1; then
    echo 'revoked full-control-plane credential still passed traffic' >&2
    exit 1
  fi
  if [[ "$stage7_extended" == "1" ]]; then
    bash scripts/stage7-e2e.sh AfterRevoke "$subscription_user_id" "$subscription_id" "$access_credential_id"
  fi
  docker rm -f "$vpn_client_name" >/dev/null
  rm -f "$vpn_client_config"
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
go run ./tools/mtlsprobe/cmd/mtlsprobe GET https://127.0.0.1:8084/internal/v1/users/00000000-0000-4000-8000-000000000001/orders/00000000-0000-4000-8000-000000000002 secrets/dev-mtls/subscription-service.crt secrets/dev-mtls/subscription-service.key secrets/dev-mtls/ca.crt 404
go run ./tools/mtlsprobe/cmd/mtlsprobe GET "https://127.0.0.1:8086/internal/v1/users/${subscription_user_id}/subscription" secrets/dev-mtls/telegram-bot.crt secrets/dev-mtls/telegram-bot.key secrets/dev-mtls/ca.crt 200
go run ./tools/mtlsprobe/cmd/mtlsprobe GET "https://127.0.0.1:8086/internal/v1/users/${subscription_user_id}/subscription" secrets/dev-mtls/identity-health.crt secrets/dev-mtls/identity-health.key secrets/dev-mtls/ca.crt 403
if [[ "$full_vpn" != "1" ]]; then
  go run ./tools/mtlsprobe/cmd/mtlsprobe GET "https://127.0.0.1:8087/internal/v1/credentials/${access_credential_id}/provisioning-material" secrets/dev-mtls/provisioning-service.crt secrets/dev-mtls/provisioning-service.key secrets/dev-mtls/ca.crt 200
  go run ./tools/mtlsprobe/cmd/mtlsprobe GET "https://127.0.0.1:8087/internal/v1/credentials/${access_credential_id}/provisioning-material" secrets/dev-mtls/identity-health.crt secrets/dev-mtls/identity-health.key secrets/dev-mtls/ca.crt 403
  material_audit_count="$(scalar_sql access_service "SELECT count(*) FROM security_audit_events WHERE credential_id='${access_credential_id}' AND actor_service='provisioning-service' AND action='credential_material.read' AND outcome='succeeded'")"
  if [[ "$material_audit_count" != "1" ]]; then
    echo "provisioning material read was not audited exactly once" >&2
    exit 1
  fi
fi

if [[ "$observability" == "1" ]]; then
  series_metadata="$(curl -fsS --get --data-urlencode 'match[]={__name__=~"vpn_.*"}' http://127.0.0.1:9090/api/v1/series)"
  for forbidden_metric_value in "$subscription_user_id" "$subscription_id" "$access_credential_id" "$issued_token" "$vpn_credential_uuid"; do
    if [[ -n "$forbidden_metric_value" ]] && grep -Fq "$forbidden_metric_value" <<<"$series_metadata"; then
      echo "Prometheus series metadata leaked a runtime identifier or VPN secret" >&2
      exit 1
    fi
  done
fi

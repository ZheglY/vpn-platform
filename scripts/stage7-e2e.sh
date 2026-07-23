#!/usr/bin/env bash
set -euo pipefail

phase="${1:?phase is required}"
user_id="${2:?user id is required}"
subscription_id="${3:?subscription id is required}"
credential_id="${4:?credential id is required}"

scalar_sql() {
  docker compose exec -T postgres psql --username="${POSTGRES_USER}" --dbname "$1" -tAc "$2" | tr -d '[:space:]'
}

notification_diagnostics() {
  echo "Stage 7 notification job counts (type|status|count):"
  docker compose exec -T postgres psql --username="$POSTGRES_USER" --dbname notification_service -tAc "SELECT notification_type || '|' || status || '|' || count(*) FROM notification_jobs GROUP BY notification_type,status ORDER BY notification_type,status"
  echo "Stage 7 notification inbox counts (event_type|count):"
  docker compose exec -T postgres psql --username="$POSTGRES_USER" --dbname notification_service -tAc "SELECT event_type || '|' || count(*) FROM notification_inbox GROUP BY event_type ORDER BY event_type"
  echo "Stage 7 notification DLQ counts (reason|count):"
  docker compose exec -T postgres psql --username="$POSTGRES_USER" --dbname notification_service -tAc "SELECT reason_code || '|' || count(*) FROM notification_dead_letters GROUP BY reason_code ORDER BY reason_code"
  docker compose logs --tail=120 notification-service telegram-bot
}

wait_sql() {
  local database="$1" sql="$2" expected="$3" attempts="${4:-120}" label="${5:-state}" value=''
  for _ in $(seq 1 "$attempts"); do
    value="$(scalar_sql "$database" "$sql")"
    if [[ "$value" == "$expected" ]]; then return 0; fi
    sleep 0.25
  done
  notification_diagnostics
  echo "Stage 7 state did not converge for ${label}: expected ${expected}, got ${value}" >&2
  exit 1
}

publish_event() {
  local topic="$1" key="$2" payload="$3" encoded
  encoded="$(printf '%s|%s\n' "$key" "$payload" | base64 | tr -d '\r\n')"
  docker compose exec -T -e "KAFKA_RECORD_BASE64=${encoded}" kafka sh -c \
    "printf %s \$KAFKA_RECORD_BASE64 | base64 -d | /opt/kafka/bin/kafka-console-producer.sh --bootstrap-server localhost:9092 --topic ${topic} --reader-property parse.key=true --reader-property key.separator=\\|"
}

telegram_behavior() {
  curl -fsS -X POST -H 'Content-Type: application/json' \
    --data-binary "{\"mode\":\"$1\",\"count\":$2,\"retry_after_seconds\":${3:-0}}" \
    http://127.0.0.1:8082/behavior >/dev/null
}

admin_cli() {
  local certificate="$1" command="$2"
  shift 2
  go run ./services/admin/cmd/admin-cli --base-url https://127.0.0.1:8092 \
    --cert "secrets/dev-mtls/${certificate}.crt" --key "secrets/dev-mtls/${certificate}.key" \
    --ca secrets/dev-mtls/ca.crt --json "$command" "$@"
}

assert_admin_http_failure() {
  local certificate="$1" expected_status="$2" command="$3" stderr_file
  shift 3
  stderr_file="$(mktemp)"
  if admin_cli "$certificate" "$command" "$@" >/dev/null 2>"$stderr_file"; then
    rm -f "$stderr_file"
    echo "admin CLI unexpectedly allowed $command" >&2
    exit 1
  fi
  if ! grep -Eq "^admin-service returned HTTP ${expected_status}$" "$stderr_file"; then
    rm -f "$stderr_file"
    echo "admin CLI failed with an unexpected result for $command" >&2
    exit 1
  fi
  rm -f "$stderr_file"
}

payment_event() {
  local payment_id="$1" event_id="$2" now
  now="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"
  printf '%s' '{"event_id":"'"${event_id}"'","event_type":"billing.payment.succeeded.v1","schema_version":1,"occurred_at":"'"${now}"'","producer":"billing-service","correlation_id":"77000000-0000-4000-8000-000000000099","causation_id":null,"aggregate_type":"payment","aggregate_id":"'"${payment_id}"'","partition_key":"user:'"${user_id}"'","data":{"payment_id":"'"${payment_id}"'","order_id":"77000000-0000-4000-8000-000000000090","user_id":"'"${user_id}"'","plan_id":"basic-monthly","amount_minor":49900,"currency":"RUB","paid_at":"'"${now}"'"}}'
}

if [[ "$phase" == "BeforeRevoke" ]]; then
  wait_sql notification_service "SELECT count(*) FROM notification_jobs WHERE notification_type='payment_confirmed' AND status='delivered'" 1 120 "delivered payment confirmation"
  wait_sql notification_service "SELECT count(*) FROM notification_jobs WHERE notification_type='access_ready' AND status='delivered'" 1 120 "delivered access-ready notification"
  wait_sql notification_service "SELECT count(*) FROM notification_jobs WHERE notification_type='subscription_activated' AND status='suppressed'" 1 120 "suppressed subscription activation"

  messages_before="$(curl -fsS http://127.0.0.1:8082/messages | sed -n 's/.*"count":\([0-9]*\).*/\1/p')"
  payment_key="$(scalar_sql billing_service "SELECT partition_key FROM outbox WHERE topic='billing.payment.succeeded.v1' LIMIT 1")"
  payment_payload="$(scalar_sql billing_service "SELECT payload::text FROM outbox WHERE topic='billing.payment.succeeded.v1' LIMIT 1")"
  publish_event billing.payment.succeeded.v1 "$payment_key" "$payment_payload"
  sleep 2
  [[ "$(scalar_sql notification_service "SELECT count(*) FROM notification_jobs WHERE notification_type='payment_confirmed'")" == 1 ]]
  [[ "$(curl -fsS http://127.0.0.1:8082/messages | sed -n 's/.*"count":\([0-9]*\).*/\1/p')" == "$messages_before" ]]

  telegram_behavior rate_limited 1 2
  publish_event billing.payment.succeeded.v1 "user:${user_id}" "$(payment_event 77000000-0000-4000-8000-000000000022 77000000-0000-4000-8000-000000000002)"
  wait_sql notification_service "SELECT status FROM notification_jobs WHERE business_dedupe_key='payment_confirmed:77000000-0000-4000-8000-000000000022'" retry
  retry_delay="$(scalar_sql notification_service "SELECT extract(epoch FROM (next_attempt_at-updated_at)) FROM notification_jobs WHERE business_dedupe_key='payment_confirmed:77000000-0000-4000-8000-000000000022'")"
  awk "BEGIN { exit !(${retry_delay} >= 1.5) }"
  wait_sql notification_service "SELECT status || ':' || attempts FROM notification_jobs WHERE business_dedupe_key='payment_confirmed:77000000-0000-4000-8000-000000000022'" delivered:2

  telegram_behavior blocked 1 0
  publish_event billing.payment.succeeded.v1 "user:${user_id}" "$(payment_event 77000000-0000-4000-8000-000000000023 77000000-0000-4000-8000-000000000003)"
  wait_sql notification_service "SELECT status || ':' || terminal_reason_code FROM notification_jobs WHERE business_dedupe_key='payment_confirmed:77000000-0000-4000-8000-000000000023'" permanently_failed:telegram_bot_blocked
  failed_id="$(scalar_sql notification_service "SELECT notification_id FROM notification_jobs WHERE business_dedupe_key='payment_confirmed:77000000-0000-4000-8000-000000000023'")"

  admin_cli admin-support-local get-user --user-id "$user_id" >/tmp/vpn-stage7-user.json
  grep -q "\"user_id\":\"${user_id}\"" /tmp/vpn-stage7-user.json
  assert_admin_http_failure admin-support-local 403 retry-notification --notification-id "$failed_id" --reason 'retry after blocked target test' --idempotency-key stage7-retry-0001

  telegram_behavior success 0 0
  admin_cli admin-operations-local retry-notification --notification-id "$failed_id" --reason 'retry after blocked target test' --idempotency-key stage7-retry-0001 >/tmp/vpn-stage7-action.json
  grep -q '"status":"succeeded"' /tmp/vpn-stage7-action.json
  wait_sql notification_service "SELECT status FROM notification_jobs WHERE notification_id='${failed_id}'" delivered
  admin_cli admin-operations-local retry-notification --notification-id "$failed_id" --reason 'retry after blocked target test' --idempotency-key stage7-retry-0001 >/tmp/vpn-stage7-replay.json
  grep -q '"replay":true' /tmp/vpn-stage7-replay.json
  assert_admin_http_failure admin-operations-local 409 retry-notification --notification-id "$failed_id" --reason 'changed request must conflict' --idempotency-key stage7-retry-0001

  admin_cli admin-support-local health >/tmp/vpn-stage7-health.json
  [[ "$(grep -o '"service":' /tmp/vpn-stage7-health.json | wc -l | tr -d ' ')" == 6 ]]
  admin_cli admin-support-local get-provisioning --credential-id "$credential_id" >/tmp/vpn-stage7-provisioning.json
  if grep -Eqi 'subscription_url|vless_client_uuid|vless://|management_url|private_key' /tmp/vpn-stage7-provisioning.json; then exit 1; fi
  go run ./tools/mtlsprobe/cmd/mtlsprobe GET https://127.0.0.1:8092/admin/v1/health secrets/dev-mtls/identity-health.crt secrets/dev-mtls/identity-health.key secrets/dev-mtls/ca.crt 403 >/dev/null
  admin_cli admin-support-local audit --limit 20 >/tmp/vpn-stage7-audit.json
  grep -q operations-local /tmp/vpn-stage7-audit.json
  if grep -Eqi 'subscription_url|vless_client_uuid|vless://|telegram_chat_id|private_key' /tmp/vpn-stage7-audit.json; then exit 1; fi

  telegram_behavior server_error 10 0
  publish_event billing.payment.succeeded.v1 "user:${user_id}" "$(payment_event 77000000-0000-4000-8000-000000000024 77000000-0000-4000-8000-000000000004)"
  wait_sql notification_service "SELECT status FROM notification_jobs WHERE business_dedupe_key='payment_confirmed:77000000-0000-4000-8000-000000000024'" retry
  action_count="$(scalar_sql admin_service 'SELECT count(*) FROM admin_action_requests')"
  docker compose restart notification-service admin-service >/dev/null
  for _ in $(seq 1 60); do
    notification_health="$(docker inspect -f '{{.State.Health.Status}}' vpn-service-notification-service-1)"
    admin_health="$(docker inspect -f '{{.State.Health.Status}}' vpn-service-admin-service-1)"
    if [[ "$notification_health" == healthy && "$admin_health" == healthy ]]; then break; fi
    sleep 1
  done
  [[ "$notification_health" == healthy && "$admin_health" == healthy ]]
  telegram_behavior success 0 0
  wait_sql notification_service "SELECT status FROM notification_jobs WHERE business_dedupe_key='payment_confirmed:77000000-0000-4000-8000-000000000024'" delivered
  [[ "$(scalar_sql admin_service 'SELECT count(*) FROM admin_action_requests')" == "$action_count" ]]
  rm -f /tmp/vpn-stage7-{user,action,replay,health,provisioning,audit}.json
  echo 'Stage 7 pre-revoke notification and admin E2E checks passed.'
  exit 0
fi

wait_sql notification_service "SELECT count(*) FROM notification_jobs WHERE credential_id='${credential_id}' AND notification_type='access_physically_revoked' AND status='suppressed'" 1
wait_sql notification_service "SELECT count(*) FROM notification_jobs WHERE subscription_id='${subscription_id}' AND notification_type='subscription_revoked' AND status='delivered'" 1
wait_sql notification_service "SELECT last_sequence FROM notification_cursors WHERE aggregate_type='access' AND aggregate_id='${credential_id}'" 2
messages_before="$(curl -fsS http://127.0.0.1:8082/messages | sed -n 's/.*"count":\([0-9]*\).*/\1/p')"
now="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"
stale_ready='{"event_id":"77000000-0000-4000-8000-000000000005","event_type":"access.ready.v1","schema_version":1,"occurred_at":"'"${now}"'","producer":"access-service","correlation_id":"77000000-0000-4000-8000-000000000098","causation_id":null,"aggregate_type":"access","aggregate_id":"'"${credential_id}"'","aggregate_sequence":3,"partition_key":"user:'"${user_id}"'","data":{"subscription_id":"'"${subscription_id}"'","credential_id":"'"${credential_id}"'","user_id":"'"${user_id}"'","provisioning_status":"active","ready_at":"'"${now}"'","link_issuance_required":true}}'
publish_event access.ready.v1 "user:${user_id}" "$stale_ready"
wait_sql notification_service "SELECT status || ':' || terminal_reason_code FROM notification_jobs WHERE business_dedupe_key='access_ready:${credential_id}:3'" suppressed:stale_subscription_state
[[ "$(curl -fsS http://127.0.0.1:8082/messages | sed -n 's/.*"count":\([0-9]*\).*/\1/p')" == "$messages_before" ]]
echo 'Stage 7 post-revoke stale-notification E2E checks passed.'

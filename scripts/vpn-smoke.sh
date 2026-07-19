#!/usr/bin/env bash
set -euo pipefail

export POSTGRES_USER="${POSTGRES_USER:-vpn_local}"
export POSTGRES_PASSWORD="${POSTGRES_PASSWORD:-local-compose-password}"
export POSTGRES_DB="${POSTGRES_DB:-vpn_platform}"
export POSTGRES_PORT="${POSTGRES_PORT:-5432}"
export REDIS_PASSWORD="${REDIS_PASSWORD:-local-compose-redis}"
export IDENTITY_DB_PASSWORD="${IDENTITY_DB_PASSWORD:-local-compose-identity}"
export CATALOG_DB_PASSWORD="${CATALOG_DB_PASSWORD:-local-compose-catalog}"
export BILLING_DB_PASSWORD="${BILLING_DB_PASSWORD:-local-compose-billing}"
export SUBSCRIPTION_DB_PASSWORD="${SUBSCRIPTION_DB_PASSWORD:-local-compose-subscription}"
export ACCESS_DB_PASSWORD="${ACCESS_DB_PASSWORD:-local-compose-access}"
export PROVISIONING_DB_PASSWORD="${PROVISIONING_DB_PASSWORD:-local-compose-provisioning}"
export ACCESS_CREDENTIAL_KEY_BASE64="${ACCESS_CREDENTIAL_KEY_BASE64:-MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=}"
export ACCESS_TOKEN_HMAC_KEY_BASE64="${ACCESS_TOKEN_HMAC_KEY_BASE64:-ZmVkY2JhOTg3NjU0MzIxMGZlZGNiYTk4NzY1NDMyMTA=}"
export TELEGRAM_WEBHOOK_SECRET="${TELEGRAM_WEBHOOK_SECRET:-local-compose-webhook-secret}"
export TELEGRAM_BOT_TOKEN="${TELEGRAM_BOT_TOKEN:-local-compose-fake-bot-token}"
export YOOKASSA_SECRET_KEY="${YOOKASSA_SECRET_KEY:-local-compose-yookassa-key}"

profiles=(--profile core --profile app --profile vpn)
client_name=vpn-stage6-client
client_image='ghcr.io/xtls/xray-core:26.3.27@sha256:592ec4d11f656db95598d01e76dbcc6e002d67360b96a5436500a938230f52c7'
credential_id='62000000-0000-4000-8000-000000000090'

cleanup() {
  docker rm -f "$client_name" >/dev/null 2>&1 || true
  docker compose "${profiles[@]}" down -v --remove-orphans >/dev/null 2>&1 || true
}
trap cleanup EXIT

bash scripts/dev-mtls.sh
bash scripts/dev-xray.sh
docker compose "${profiles[@]}" down -v --remove-orphans
docker compose "${profiles[@]}" build provisioning-migrate node-agent-primary
docker compose "${profiles[@]}" up -d postgres camouflage node-agent-primary node-agent-failover

for attempt in $(seq 1 90); do
  postgres="$(docker inspect -f '{{.State.Health.Status}}' vpn-service-postgres-1 2>/dev/null || true)"
  primary="$(docker inspect -f '{{.State.Health.Status}}' vpn-service-node-agent-primary-1 2>/dev/null || true)"
  failover="$(docker inspect -f '{{.State.Health.Status}}' vpn-service-node-agent-failover-1 2>/dev/null || true)"
  if [[ "$postgres" == healthy && "$primary" == healthy && "$failover" == healthy ]]; then break; fi
  if [[ "$attempt" == 90 ]]; then echo 'Stage 6 containers did not become healthy' >&2; exit 1; fi
  sleep 1
done

docker compose "${profiles[@]}" run --rm provisioning-migrate
docker compose "${profiles[@]}" run --rm provisioning-seed
PROVISIONING_TEST_DATABASE_URL="postgres://provisioning_app:${PROVISIONING_DB_PASSWORD}@127.0.0.1:${POSTGRES_PORT}/provisioning_service?sslmode=disable" go test ./services/provisioning/internal/postgres -count=1

probe=(go run ./tools/mtlsprobe/cmd/mtlsprobe PUT "https://127.0.0.1:18443/internal/v1/credentials/${credential_id}" secrets/dev-mtls/provisioning-service.crt secrets/dev-mtls/provisioning-service.key secrets/dev-mtls/ca.crt)
first="$("${probe[@]}" 200 body-file secrets/dev-xray/smoke-present.json print-body)"
go run ./tools/mtlsprobe/cmd/mtlsprobe PUT "https://127.0.0.1:28443/internal/v1/credentials/${credential_id}" secrets/dev-mtls/provisioning-service.crt secrets/dev-mtls/provisioning-service.key secrets/dev-mtls/ca.crt 200 body-file secrets/dev-xray/smoke-present.json
replay="$("${probe[@]}" 200 body-file secrets/dev-xray/smoke-present.json print-body)"
first_revision="$(printf '%s' "$first" | sed -n 's/.*"config_revision":\([0-9]*\).*/\1/p')"
replay_revision="$(printf '%s' "$replay" | sed -n 's/.*"config_revision":\([0-9]*\).*/\1/p')"
first_applied_at="$(printf '%s' "$first" | sed -n 's/.*"applied_at":"\([^"]*\)".*/\1/p')"
replay_applied_at="$(printf '%s' "$replay" | sed -n 's/.*"applied_at":"\([^"]*\)".*/\1/p')"
[[ -n "$first_revision" && "$first_revision" == "$replay_revision" ]]
[[ -n "$first_applied_at" && "$first_applied_at" == "$replay_applied_at" ]]
go run ./tools/mtlsprobe/cmd/mtlsprobe PUT "https://127.0.0.1:18443/internal/v1/credentials/${credential_id}" secrets/dev-mtls/node-health-primary.crt secrets/dev-mtls/node-health-primary.key secrets/dev-mtls/ca.crt 403 body-file secrets/dev-xray/smoke-present.json

docker run --rm -v "$(pwd)/secrets/dev-xray/smoke-client.json:/etc/xray/client.json:ro" "$client_image" run -test -config /etc/xray/client.json >/dev/null
docker run -d --name "$client_name" --network vpn-service_vpn-data -p 127.0.0.1:11080:1080 -v "$(pwd)/secrets/dev-xray/smoke-client.json:/etc/xray/client.json:ro" "$client_image" run -config /etc/xray/client.json >/dev/null
body=''
for attempt in $(seq 1 30); do
  body="$(curl -sS --socks5-hostname 127.0.0.1:11080 --connect-timeout 1 --max-time 2 http://camouflage.local/ 2>/dev/null || true)"
  if [[ "$body" == *'local camouflage endpoint'* ]]; then break; fi
  sleep 0.5
done
[[ "$body" == *'local camouflage endpoint'* ]]

"${probe[@]}" 200 body-file secrets/dev-xray/smoke-absent.json
go run ./tools/mtlsprobe/cmd/mtlsprobe PUT "https://127.0.0.1:28443/internal/v1/credentials/${credential_id}" secrets/dev-mtls/provisioning-service.crt secrets/dev-mtls/provisioning-service.key secrets/dev-mtls/ca.crt 200 body-file secrets/dev-xray/smoke-absent.json
if curl -sS --socks5-hostname 127.0.0.1:11080 --connect-timeout 3 --max-time 5 http://camouflage.local/ >/dev/null 2>&1; then
  echo 'revoked VLESS credential still passed traffic' >&2
  exit 1
fi

echo 'Stage 6 VPN smoke passed: mTLS, capacity SQL, real Xray validation, VLESS + REALITY, replay, and revoke.'

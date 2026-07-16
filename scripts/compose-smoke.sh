#!/usr/bin/env bash
set -euo pipefail

export POSTGRES_USER="${POSTGRES_USER:-vpn_local}"
export POSTGRES_PASSWORD="${POSTGRES_PASSWORD:-local-compose-password}"
export POSTGRES_DB="${POSTGRES_DB:-vpn_platform}"
export REDIS_PASSWORD="${REDIS_PASSWORD:-local-compose-redis}"
export KAFKA_PORT="${KAFKA_PORT:-9094}"

cleanup() {
  docker compose --profile core --profile app down
}
trap cleanup EXIT

docker compose --profile core --profile app up -d --build

containers=(
  vpn-service-postgres-1
  vpn-service-redis-1
  vpn-service-kafka-1
  vpn-service-identity-service-1
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

for _ in $(seq 1 30); do
  if curl -fsS http://127.0.0.1:8080/livez >/tmp/vpn-service-livez.json &&
     curl -fsS http://127.0.0.1:8080/readyz >/tmp/vpn-service-readyz.json &&
     curl -fsS http://127.0.0.1:8080/version >/tmp/vpn-service-version.json; then
    grep -q '"status":"ok"' /tmp/vpn-service-livez.json
    grep -q '"status":"ready"' /tmp/vpn-service-readyz.json
    grep -q '"service":"identity-service"' /tmp/vpn-service-version.json
    exit 0
  fi
  sleep 2
done

docker compose --profile core --profile app ps
docker compose --profile core --profile app logs --tail=200
exit 1

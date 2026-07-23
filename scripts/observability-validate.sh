#!/usr/bin/env bash
set -euo pipefail

prometheus_image='vpn-service/prometheus:local'
grafana_image='vpn-service/grafana:local'
repo="$(cd "$(dirname "$0")/.." && pwd)"

bash scripts/dev-mtls.sh
docker build -f deploy/observability/prometheus/Dockerfile -t "$prometheus_image" .
docker build -f deploy/observability/grafana/Dockerfile -t "$grafana_image" .
docker run --rm --entrypoint /bin/promtool \
  -v "${repo}/deploy/observability/prometheus:/etc/prometheus:ro" \
  -v "${repo}/secrets/dev-mtls/ca.crt:/run/mtls/ca.crt:ro" \
  -v "${repo}/secrets/dev-mtls/observability.crt:/run/mtls/observability.crt:ro" \
  -v "${repo}/secrets/dev-mtls/observability.key:/run/mtls/observability.key:ro" \
  "$prometheus_image" check config /etc/prometheus/prometheus.yml
node -e "JSON.parse(require('fs').readFileSync('deploy/observability/grafana/dashboards/platform-overview.json','utf8'))"

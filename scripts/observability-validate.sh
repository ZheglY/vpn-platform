#!/usr/bin/env bash
set -euo pipefail

prometheus_image='vpn-service/prometheus:local'
grafana_image='vpn-service/grafana:local'
credential_image='vpn-service/credentialstage:local'
repo="$(cd "$(dirname "$0")/.." && pwd)"
credential_volume="vpn-service-observability-validate-$$"

cleanup() {
  docker volume rm -f "$credential_volume" >/dev/null 2>&1 || true
}
trap cleanup EXIT

bash scripts/dev-mtls.sh
docker build -f tools/credentialstage/Dockerfile -t "$credential_image" .
docker build -f deploy/observability/prometheus/Dockerfile -t "$prometheus_image" .
docker build -f deploy/observability/grafana/Dockerfile -t "$grafana_image" .
docker volume create "$credential_volume" >/dev/null
docker run --rm \
  --user 0:0 \
  --network none \
  --read-only \
  --tmpfs /tmp \
  --cap-drop ALL \
  --cap-add CHOWN \
  --cap-add DAC_OVERRIDE \
  --cap-add FOWNER \
  --security-opt no-new-privileges:true \
  -v "${repo}/deploy/local/credentials/mtls-manifest.json:/etc/credentialstage/manifest.json:ro" \
  -v "${repo}/secrets/dev-mtls:/source:ro" \
  -v "${credential_volume}:/runtime/observability" \
  "$credential_image" stage --manifest=/etc/credentialstage/manifest.json --source=/source --destination=/runtime --group=observability
docker run --rm \
  --user 65532:65532 \
  --network none \
  --read-only \
  --cap-drop ALL \
  --security-opt no-new-privileges:true \
  -v "${repo}/deploy/local/credentials/mtls-manifest.json:/etc/credentialstage/manifest.json:ro" \
  -v "${credential_volume}:/runtime/observability:ro" \
  "$credential_image" verify --manifest=/etc/credentialstage/manifest.json --destination=/runtime --group=observability
docker run --rm --entrypoint /bin/promtool \
  -v "${repo}/deploy/observability/prometheus:/etc/prometheus:ro" \
  -v "${credential_volume}:/run/mtls:ro" \
  "$prometheus_image" check config /etc/prometheus/prometheus.yml
docker run --rm --entrypoint /bin/promtool \
  -v "${repo}/deploy/observability/prometheus:/etc/prometheus:ro" \
  -v "${credential_volume}:/run/mtls:ro" \
  "$prometheus_image" check config /etc/prometheus/prometheus-vpn.yml
docker run --rm --entrypoint /bin/promtool \
  -v "${repo}/deploy/observability/prometheus:/etc/prometheus:ro" \
  "$prometheus_image" test rules /etc/prometheus/tests/platform.test.yml /etc/prometheus/tests/operations.test.yml /etc/prometheus/tests/vpn-targets.test.yml
node -e "JSON.parse(require('fs').readFileSync('deploy/observability/grafana/dashboards/platform-overview.json','utf8'))"

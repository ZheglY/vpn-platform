#!/usr/bin/env bash
set -euo pipefail

prometheus_image='vpn-service/prometheus:local'
grafana_image='vpn-service/grafana:local'
collector_image='vpn-service/otel-collector:local'
tempo_image='vpn-service/tempo:local'
loki_image='vpn-service/loki:local'
credential_image='vpn-service/credentialstage:local'
repo="$(cd "$(dirname "$0")/.." && pwd)"
credential_volume="vpn-service-observability-validate-$$"
collector_credential_volume="vpn-service-collector-validate-$$"

cleanup() {
  docker volume rm -f "$credential_volume" >/dev/null 2>&1 || true
  docker volume rm -f "$collector_credential_volume" >/dev/null 2>&1 || true
}
trap cleanup EXIT

node - <<'NODE'
const fs = require('fs');
const vex = JSON.parse(fs.readFileSync('deploy/observability/tempo/tempo.openvex.json', 'utf8'));
const statements = vex.statements || [];
const statement = statements[0] || {};
const products = statement.products || [];
const expectedImpact = "Prometheus 3.5.5 LTS contains the upstream fix: Azure AD OAuth ClientSecret uses the redacting config_util.Secret type. The Tempo build asserts that exact field before compilation; Trivy's linear module range omits the patched 3.5 LTS branch.";
if (
  statements.length !== 1 ||
  statement.vulnerability?.['@id'] !== 'CVE-2026-42151' ||
  products.length !== 1 ||
  products[0]?.['@id'] !== 'pkg:golang/github.com/prometheus/prometheus@v0.305.5' ||
  statement.status !== 'not_affected' ||
  statement.justification !== 'vulnerable_code_not_present' ||
  statement.impact_statement !== expectedImpact
) {
  throw new Error('Tempo OpenVEX statement differs from the reviewed patched-LTS assessment');
}
NODE

bash scripts/dev-mtls.sh
docker build -f tools/credentialstage/Dockerfile -t "$credential_image" .
docker build -f deploy/observability/prometheus/Dockerfile -t "$prometheus_image" .
docker build -f deploy/observability/grafana/Dockerfile -t "$grafana_image" .
docker build -f deploy/observability/otel-collector/Dockerfile -t "$collector_image" .
docker build -f deploy/observability/tempo/Dockerfile -t "$tempo_image" .
docker build -f deploy/observability/loki/Dockerfile -t "$loki_image" .
docker volume create "$credential_volume" >/dev/null
docker volume create "$collector_credential_volume" >/dev/null
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
  -v "${collector_credential_volume}:/runtime/collector" \
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
  -v "${collector_credential_volume}:/runtime/collector" \
  "$credential_image" stage --manifest=/etc/credentialstage/manifest.json --source=/source --destination=/runtime --group=collector
docker run --rm \
  --user 65532:65532 \
  --network none \
  --read-only \
  --cap-drop ALL \
  --security-opt no-new-privileges:true \
  -v "${repo}/deploy/local/credentials/mtls-manifest.json:/etc/credentialstage/manifest.json:ro" \
  -v "${collector_credential_volume}:/runtime/collector:ro" \
  "$credential_image" verify --manifest=/etc/credentialstage/manifest.json --destination=/runtime --group=collector
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
docker run --rm \
  --user 65532:65532 \
  --network none \
  --read-only \
  --cap-drop ALL \
  --security-opt no-new-privileges:true \
  -v "${repo}/deploy/observability/otel-collector/config.yml:/etc/otelcol/config.yml:ro" \
  -v "${collector_credential_volume}:/run/mtls:ro" \
  "$collector_image" validate --config=/etc/otelcol/config.yml
docker run --rm \
  --user 10001:10001 \
  --network none \
  --read-only \
  --tmpfs /tmp \
  --cap-drop ALL \
  --security-opt no-new-privileges:true \
  -v "${repo}/deploy/observability/tempo/tempo.yml:/etc/tempo/tempo.yml:ro" \
  "$tempo_image" -config.file=/etc/tempo/tempo.yml -config.verify=true
docker run --rm \
  --user 10001:10001 \
  --network none \
  --read-only \
  --tmpfs /tmp \
  --cap-drop ALL \
  --security-opt no-new-privileges:true \
  -v "${repo}/deploy/observability/loki/loki.yml:/etc/loki/loki.yml:ro" \
  "$loki_image" -config.file=/etc/loki/loki.yml -verify-config=true
node -e "JSON.parse(require('fs').readFileSync('deploy/observability/grafana/dashboards/platform-overview.json','utf8'))"
node -e "const y=require('js-yaml'); for (const f of ['deploy/observability/grafana/provisioning/datasources/telemetry.yml','deploy/observability/otel-collector/config.yml','deploy/observability/tempo/tempo.yml','deploy/observability/loki/loki.yml']) y.load(require('fs').readFileSync(f,'utf8'));"

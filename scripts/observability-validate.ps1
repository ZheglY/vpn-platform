$ErrorActionPreference = "Stop"

$repo = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$prometheusImage = "vpn-service/prometheus:local"
$grafanaImage = "vpn-service/grafana:local"

& powershell -NoProfile -ExecutionPolicy Bypass -File (Join-Path $PSScriptRoot "dev-mtls.ps1")
if ($LASTEXITCODE -ne 0) {
    exit $LASTEXITCODE
}

docker build -f deploy/observability/prometheus/Dockerfile -t $prometheusImage .
if ($LASTEXITCODE -ne 0) {
    exit $LASTEXITCODE
}

docker build -f deploy/observability/grafana/Dockerfile -t $grafanaImage .
if ($LASTEXITCODE -ne 0) {
    exit $LASTEXITCODE
}

docker run --rm --entrypoint /bin/promtool `
    -v "$repo\deploy\observability\prometheus:/etc/prometheus:ro" `
    -v "$repo\secrets\dev-mtls\ca.crt:/run/mtls/ca.crt:ro" `
    -v "$repo\secrets\dev-mtls\observability.crt:/run/mtls/observability.crt:ro" `
    -v "$repo\secrets\dev-mtls\observability.key:/run/mtls/observability.key:ro" `
    $prometheusImage check config /etc/prometheus/prometheus.yml
if ($LASTEXITCODE -ne 0) {
    exit $LASTEXITCODE
}

node -e "JSON.parse(require('fs').readFileSync('deploy/observability/grafana/dashboards/platform-overview.json','utf8'))"
exit $LASTEXITCODE

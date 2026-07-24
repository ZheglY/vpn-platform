$ErrorActionPreference = "Stop"

$repo = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$prometheusImage = "vpn-service/prometheus:local"
$grafanaImage = "vpn-service/grafana:local"
$credentialImage = "vpn-service/credentialstage:local"
$credentialVolume = "vpn-service-observability-validate-$([guid]::NewGuid().ToString('N'))"

try {
    & powershell -NoProfile -ExecutionPolicy Bypass -File (Join-Path $PSScriptRoot "dev-mtls.ps1")
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

    docker build -f tools/credentialstage/Dockerfile -t $credentialImage .
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
    docker build -f deploy/observability/prometheus/Dockerfile -t $prometheusImage .
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
    docker build -f deploy/observability/grafana/Dockerfile -t $grafanaImage .
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

    docker volume create $credentialVolume | Out-Null
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
    docker run --rm `
        --user "0:0" `
        --network none `
        --read-only `
        --tmpfs /tmp `
        --cap-drop ALL `
        --cap-add CHOWN `
        --cap-add DAC_OVERRIDE `
        --cap-add FOWNER `
        --security-opt no-new-privileges:true `
        -v "$repo\deploy\local\credentials\mtls-manifest.json:/etc/credentialstage/manifest.json:ro" `
        -v "$repo\secrets\dev-mtls:/source:ro" `
        -v "${credentialVolume}:/runtime/observability" `
        $credentialImage stage --manifest=/etc/credentialstage/manifest.json --source=/source --destination=/runtime --group=observability
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
    docker run --rm `
        --user "65532:65532" `
        --network none `
        --read-only `
        --cap-drop ALL `
        --security-opt no-new-privileges:true `
        -v "$repo\deploy\local\credentials\mtls-manifest.json:/etc/credentialstage/manifest.json:ro" `
        -v "${credentialVolume}:/runtime/observability:ro" `
        $credentialImage verify --manifest=/etc/credentialstage/manifest.json --destination=/runtime --group=observability
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

    foreach ($config in @("prometheus.yml", "prometheus-vpn.yml")) {
        docker run --rm --entrypoint /bin/promtool `
            -v "$repo\deploy\observability\prometheus:/etc/prometheus:ro" `
            -v "${credentialVolume}:/run/mtls:ro" `
            $prometheusImage check config "/etc/prometheus/$config"
        if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
    }
    docker run --rm --entrypoint /bin/promtool `
        -v "$repo\deploy\observability\prometheus:/etc/prometheus:ro" `
        $prometheusImage test rules /etc/prometheus/tests/platform.test.yml /etc/prometheus/tests/operations.test.yml /etc/prometheus/tests/vpn-targets.test.yml
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

    node -e "JSON.parse(require('fs').readFileSync('deploy/observability/grafana/dashboards/platform-overview.json','utf8'))"
    exit $LASTEXITCODE
} finally {
    docker volume rm -f $credentialVolume 2>$null | Out-Null
}

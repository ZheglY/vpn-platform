$ErrorActionPreference = "Stop"

$repo = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$prometheusImage = "vpn-service/prometheus:local"
$grafanaImage = "vpn-service/grafana:local"
$collectorImage = "vpn-service/otel-collector:local"
$tempoImage = "vpn-service/tempo:local"
$lokiImage = "vpn-service/loki:local"
$credentialImage = "vpn-service/credentialstage:local"
$alertmanagerImage = "prom/alertmanager:v0.33.1@sha256:9e082985f56f4c8c9f724e18f2288c6708f472e56a5286b8863d080434ea065d"
$credentialVolume = "vpn-service-observability-validate-$([guid]::NewGuid().ToString('N'))"
$collectorCredentialVolume = "vpn-service-collector-validate-$([guid]::NewGuid().ToString('N'))"
$tempoVEXPath = Join-Path $repo "deploy\observability\tempo\tempo.openvex.json"

try {
    $tempoVEX = Get-Content $tempoVEXPath -Raw | ConvertFrom-Json
    $expectedImpact = "Prometheus 3.5.5 LTS contains the upstream fix: Azure AD OAuth ClientSecret uses the redacting config_util.Secret type. The Tempo build asserts that exact field before compilation; Trivy's linear module range omits the patched 3.5 LTS branch."
    if (
        $tempoVEX.statements.Count -ne 1 -or
        $tempoVEX.statements[0].vulnerability.'@id' -ne "CVE-2026-42151" -or
        $tempoVEX.statements[0].products.Count -ne 1 -or
        $tempoVEX.statements[0].products[0].'@id' -ne "pkg:golang/github.com/prometheus/prometheus@v0.305.5" -or
        $tempoVEX.statements[0].status -ne "not_affected" -or
        $tempoVEX.statements[0].justification -ne "vulnerable_code_not_present" -or
        $tempoVEX.statements[0].impact_statement -ne $expectedImpact
    ) {
        throw "Tempo OpenVEX statement differs from the reviewed patched-LTS assessment"
    }

    & powershell -NoProfile -ExecutionPolicy Bypass -File (Join-Path $PSScriptRoot "dev-mtls.ps1")
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

    docker build -f tools/credentialstage/Dockerfile -t $credentialImage .
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
    docker build -f deploy/observability/prometheus/Dockerfile -t $prometheusImage .
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
    docker build -f deploy/observability/grafana/Dockerfile -t $grafanaImage .
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
    docker build -f deploy/observability/otel-collector/Dockerfile -t $collectorImage .
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
    docker build -f deploy/observability/tempo/Dockerfile -t $tempoImage .
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
    docker build -f deploy/observability/loki/Dockerfile -t $lokiImage .
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

    docker volume create $credentialVolume | Out-Null
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
    docker volume create $collectorCredentialVolume | Out-Null
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
        -v "${collectorCredentialVolume}:/runtime/collector" `
        $credentialImage stage --manifest=/etc/credentialstage/manifest.json --source=/source --destination=/runtime --group=observability
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
        -v "${collectorCredentialVolume}:/runtime/collector" `
        $credentialImage stage --manifest=/etc/credentialstage/manifest.json --source=/source --destination=/runtime --group=collector
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
    docker run --rm `
        --user "65532:65532" `
        --network none `
        --read-only `
        --cap-drop ALL `
        --security-opt no-new-privileges:true `
        -v "$repo\deploy\local\credentials\mtls-manifest.json:/etc/credentialstage/manifest.json:ro" `
        -v "${collectorCredentialVolume}:/runtime/collector:ro" `
        $credentialImage verify --manifest=/etc/credentialstage/manifest.json --destination=/runtime --group=collector
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
        $prometheusImage test rules /etc/prometheus/tests/platform.test.yml /etc/prometheus/tests/operations.test.yml /etc/prometheus/tests/vpn-targets.test.yml /etc/prometheus/tests/slo.test.yml
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
    docker run --rm --entrypoint /bin/amtool `
        -v "$repo\deploy\observability\alertmanager:/etc/alertmanager:ro" `
        $alertmanagerImage check-config /etc/alertmanager/alertmanager.yml
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

    docker run --rm `
        --user "65532:65532" `
        --network none `
        --read-only `
        --cap-drop ALL `
        --security-opt no-new-privileges:true `
        -v "$repo\deploy\observability\otel-collector\config.yml:/etc/otelcol/config.yml:ro" `
        -v "${collectorCredentialVolume}:/run/mtls:ro" `
        $collectorImage validate --config=/etc/otelcol/config.yml
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
    docker run --rm `
        --user "10001:10001" `
        --network none `
        --read-only `
        --tmpfs /tmp `
        --cap-drop ALL `
        --security-opt no-new-privileges:true `
        -v "$repo\deploy\observability\tempo\tempo.yml:/etc/tempo/tempo.yml:ro" `
        $tempoImage "-config.file=/etc/tempo/tempo.yml" "-config.verify=true"
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
    docker run --rm `
        --user "10001:10001" `
        --network none `
        --read-only `
        --tmpfs /tmp `
        --cap-drop ALL `
        --security-opt no-new-privileges:true `
        -v "$repo\deploy\observability\loki\loki.yml:/etc/loki/loki.yml:ro" `
        $lokiImage "-config.file=/etc/loki/loki.yml" "-verify-config=true"
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

    node -e "JSON.parse(require('fs').readFileSync('deploy/observability/grafana/dashboards/platform-overview.json','utf8'))"
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
    node -e "const y=require('js-yaml'); for (const f of ['deploy/observability/grafana/provisioning/datasources/telemetry.yml','deploy/observability/otel-collector/config.yml','deploy/observability/tempo/tempo.yml','deploy/observability/loki/loki.yml','deploy/observability/alertmanager/alertmanager.yml']) y.load(require('fs').readFileSync(f,'utf8'));"
    exit $LASTEXITCODE
} finally {
    docker volume rm -f $credentialVolume 2>$null | Out-Null
    docker volume rm -f $collectorCredentialVolume 2>$null | Out-Null
}

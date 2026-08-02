$ErrorActionPreference = "Stop"

$repo = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$runID = [Guid]::NewGuid().ToString("N").Substring(0, 12)
$composeProject = "vpn-resilience-$runID"
$defaultProjectVolume = "vpn-service_postgres-data"
$utilityImage = "postgres:18-alpine@sha256:9a8afca54e7861fd90fab5fdf4c42477a6b1cb7d293595148e674e0a3181de15"
$preservationVolumeOwned = $false
$preservationCreatedAt = ""
$profiles = @("--profile", "core", "--profile", "app", "--profile", "vpn", "--profile", "obs")
$env:COMPOSE_PROJECT_NAME = $composeProject
$env:VPN_SMOKE_FULL_CONTROL_PLANE = "1"
$env:VPN_SMOKE_TEST_FAILOVER = "1"
$env:STAGE7_EXTENDED_SMOKE = "0"
$env:OBSERVABILITY_SMOKE = "1"
$env:SMOKE_KEEP_STACK = "1"
$env:SMOKE_DISPOSABLE_PROJECT = "1"
$env:COMPOSE_PROFILES = "core,app,vpn,obs"
$env:ACCESS_PUBLIC_IP_RATE_LIMIT = "2000"
$env:ACCESS_PUBLIC_TOKEN_RATE_LIMIT = "2000"
$cacheRoot = if (-not [string]::IsNullOrWhiteSpace($env:VPN_PLATFORM_CACHE_ROOT)) {
    $env:VPN_PLATFORM_CACHE_ROOT
} else {
    Join-Path (Split-Path $repo -Parent) ".cache\vpn-platform"
}
if ([string]::IsNullOrWhiteSpace($env:GOCACHE)) { $env:GOCACHE = Join-Path $cacheRoot "cache" }
if ([string]::IsNullOrWhiteSpace($env:GOMODCACHE)) { $env:GOMODCACHE = Join-Path $cacheRoot "mod" }
if ([string]::IsNullOrWhiteSpace($env:GOTMPDIR)) { $env:GOTMPDIR = Join-Path $cacheRoot "tmp" }
New-Item -ItemType Directory -Force -Path $env:GOCACHE, $env:GOMODCACHE, $env:GOTMPDIR | Out-Null

function Test-DockerVolume([string]$name) {
    $previousErrorActionPreference = $ErrorActionPreference
    try {
        $ErrorActionPreference = "SilentlyContinue"
        docker volume inspect $name 2>$null | Out-Null
        $inspectExitCode = $LASTEXITCODE
    } finally {
        $ErrorActionPreference = $previousErrorActionPreference
    }
    return $inspectExitCode -eq 0
}

function Initialize-PreservationVolume {
    if (-not (Test-DockerVolume $defaultProjectVolume)) {
        docker volume create $defaultProjectVolume | Out-Null
        if ($LASTEXITCODE -ne 0) { throw "failed to create preservation-test volume" }
        $script:preservationVolumeOwned = $true
        docker run --rm --entrypoint sh -v "${defaultProjectVolume}:/sentinel" $utilityImage -c "printf '%s' '$runID' > /sentinel/.vpn-platform-resilience-preserved"
        if ($LASTEXITCODE -ne 0) { throw "failed to initialize preservation-test volume" }
    }
    $script:preservationCreatedAt = (& docker volume inspect --format "{{.CreatedAt}}" $defaultProjectVolume).Trim()
    if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($script:preservationCreatedAt)) {
        throw "failed to record preservation-test volume identity"
    }
}

function Test-PreservationVolume {
    if (-not (Test-DockerVolume $defaultProjectVolume)) {
        throw "resilience drill removed the default Compose volume"
    }
    $currentCreatedAt = (& docker volume inspect --format "{{.CreatedAt}}" $defaultProjectVolume).Trim()
    if ($LASTEXITCODE -ne 0 -or $currentCreatedAt -ne $preservationCreatedAt) {
        throw "resilience drill replaced the default Compose volume"
    }
    if ($preservationVolumeOwned) {
        $marker = (& docker run --rm --entrypoint cat -v "${defaultProjectVolume}:/sentinel:ro" $utilityImage /sentinel/.vpn-platform-resilience-preserved).Trim()
        if ($LASTEXITCODE -ne 0 -or $marker -ne $runID) {
            throw "resilience drill changed data in the preservation-test volume"
        }
    }
}

function Invoke-SQL([string]$database, [string]$sql) {
    $value = & docker compose @profiles exec -T postgres psql --username="$env:POSTGRES_USER" --dbname $database -tAc $sql
    if ($LASTEXITCODE -ne 0) { throw "resilience SQL probe failed" }
    return "$value".Trim()
}

function Wait-Command([scriptblock]$probe, [string]$failure) {
    foreach ($attempt in 1..120) {
        $previousErrorActionPreference = $ErrorActionPreference
        try {
            $ErrorActionPreference = "SilentlyContinue"
            & $probe
            $probeExitCode = $LASTEXITCODE
        } finally {
            $ErrorActionPreference = $previousErrorActionPreference
        }
        if ($probeExitCode -eq 0) { return }
        Start-Sleep -Milliseconds 500
    }
    throw $failure
}

Push-Location $repo
try {
    Initialize-PreservationVolume
    & (Join-Path $PSScriptRoot "compose-smoke.ps1")

    $env:LOAD_PROBE_CA_FILE = Join-Path $repo "secrets\dev-mtls\ca.crt"
    $env:LOAD_PROBE_DURATION = if ($env:RESILIENCE_SOAK_DURATION) { $env:RESILIENCE_SOAK_DURATION } else { "30s" }
    $env:LOAD_PROBE_RPS = if ($env:RESILIENCE_RPS) { $env:RESILIENCE_RPS } else { "20" }
    $env:LOAD_PROBE_CONCURRENCY = "8"
    $env:LOAD_PROBE_MAX_ERROR_RATIO = "0.001"
    $env:LOAD_PROBE_P95_BUDGET_MS = "200"
    $env:LOAD_PROBE_EXPECTED_STATUSES = "404"
    $env:LOAD_PROBE_URL = "https://127.0.0.1:8087/s/resilience-invalid-token"
    go run -mod=readonly ./tools/loadprobe | Tee-Object -FilePath (Join-Path $repo "tmp\resilience-load-last-report.json")
    if ($LASTEXITCODE -ne 0) { throw "subscription endpoint load budget failed" }

    $periodsBefore = Invoke-SQL "subscription_service" "SELECT count(*) FROM subscription_periods"
    $paymentEventID = Invoke-SQL "billing_service" "SELECT event_id FROM outbox WHERE topic='billing.payment.succeeded.v1' ORDER BY created_at LIMIT 1"
    docker compose @profiles stop kafka
    if ($LASTEXITCODE -ne 0) { throw "stop Kafka fault injection failed" }
    Invoke-SQL "billing_service" "UPDATE outbox SET state='pending', next_attempt_at=clock_timestamp(), lease_until=NULL, published_at=NULL WHERE event_id='$paymentEventID'" | Out-Null
    Start-Sleep -Seconds 2
    docker compose @profiles start kafka
    if ($LASTEXITCODE -ne 0) { throw "restart Kafka fault injection failed" }
    Wait-Command { docker compose @profiles exec -T kafka /opt/kafka/bin/kafka-broker-api-versions.sh --bootstrap-server localhost:9092 *> $null } "Kafka did not recover"
    foreach ($attempt in 1..120) {
        if ((Invoke-SQL "billing_service" "SELECT state FROM outbox WHERE event_id='$paymentEventID'") -eq "published") { break }
        Start-Sleep -Milliseconds 500
    }
    if ((Invoke-SQL "billing_service" "SELECT state FROM outbox WHERE event_id='$paymentEventID'") -ne "published") {
        throw "outbox did not recover after Kafka outage"
    }
    if ((Invoke-SQL "subscription_service" "SELECT count(*) FROM subscription_periods") -ne $periodsBefore) {
        throw "Kafka recovery duplicated a subscription period"
    }

    docker compose @profiles stop postgres
    if ($LASTEXITCODE -ne 0) { throw "stop PostgreSQL fault injection failed" }
    $liveStatus = & curl.exe -sS -o NUL -w "%{http_code}" --cacert "secrets/dev-mtls/ca.crt" --ssl-no-revoke "https://127.0.0.1:8087/livez"
    $readyStatus = & curl.exe -sS -o NUL -w "%{http_code}" --cacert "secrets/dev-mtls/ca.crt" --ssl-no-revoke --max-time 10 "https://127.0.0.1:8087/readyz"
    if ($liveStatus -ne "200" -or $readyStatus -ne "503") {
        throw "database outage did not preserve liveness and fail readiness"
    }
    docker compose @profiles start postgres
    if ($LASTEXITCODE -ne 0) { throw "restart PostgreSQL fault injection failed" }
    Wait-Command { docker compose @profiles exec -T postgres pg_isready -U $env:POSTGRES_USER -d $env:POSTGRES_DB *> $null } "PostgreSQL did not recover"
    Wait-Command { docker compose @profiles exec -T access-service /access-service healthcheck *> $null } "Access did not recover after PostgreSQL outage"

    docker compose @profiles stop prometheus grafana otel-collector tempo loki
    if ($LASTEXITCODE -ne 0) { throw "stop observability backend fault injection failed" }
    $businessLiveStatus = & curl.exe -sS -o NUL -w "%{http_code}" --cacert "secrets/dev-mtls/ca.crt" --ssl-no-revoke "https://127.0.0.1:8087/livez"
    $businessReadyStatus = & curl.exe -sS -o NUL -w "%{http_code}" --cacert "secrets/dev-mtls/ca.crt" --ssl-no-revoke "https://127.0.0.1:8087/readyz"
    $publicStatus = & curl.exe -sS -o NUL -w "%{http_code}" --cacert "secrets/dev-mtls/ca.crt" --ssl-no-revoke "https://127.0.0.1:8087/s/resilience-observability-outage"
    if ($businessLiveStatus -ne "200" -or $businessReadyStatus -ne "200" -or $publicStatus -ne "404") {
        throw "observability outage interrupted business HTTP processing"
    }
    docker compose @profiles start tempo loki otel-collector prometheus grafana
    if ($LASTEXITCODE -ne 0) { throw "restart observability backend fault injection failed" }

    go test -mod=readonly ./services/node-agent/internal/xray -run "Reload|SystemdManager"
    if ($LASTEXITCODE -ne 0) { throw "Xray reload failure recovery tests failed" }
    go test -mod=readonly ./services/provisioning/internal/application -run "Reconcile|Failover"
    if ($LASTEXITCODE -ne 0) { throw "node loss reconciliation tests failed" }
} finally {
    $cleanupFailures = [System.Collections.Generic.List[string]]::new()
    $env:SMOKE_KEEP_STACK = "0"
    docker compose @profiles down -v --remove-orphans
    if ($LASTEXITCODE -ne 0) { $cleanupFailures.Add("disposable Compose cleanup failed") }
    foreach ($volume in @("${composeProject}-go-mod-cache", "${composeProject}-go-build-cache")) {
        if (Test-DockerVolume $volume) {
            docker volume rm $volume | Out-Null
            if ($LASTEXITCODE -ne 0) { $cleanupFailures.Add("auxiliary volume cleanup failed: $volume") }
        }
    }
    try {
        Test-PreservationVolume
    } catch {
        $cleanupFailures.Add($_.Exception.Message)
    }
    if ($preservationVolumeOwned -and (Test-DockerVolume $defaultProjectVolume)) {
        docker volume rm $defaultProjectVolume | Out-Null
        if ($LASTEXITCODE -ne 0) { $cleanupFailures.Add("preservation-test volume cleanup failed") }
    }
    Pop-Location
    if ($cleanupFailures.Count -ne 0) {
        throw "resilience drill cleanup failed: $($cleanupFailures -join '; ')"
    }
}

$ErrorActionPreference = "Stop"

function Set-DefaultEnv([string]$name, [string]$value) {
    if ([string]::IsNullOrWhiteSpace([Environment]::GetEnvironmentVariable($name))) {
        [Environment]::SetEnvironmentVariable($name, $value, "Process")
    }
}

Set-DefaultEnv "POSTGRES_USER" "vpn_local"
Set-DefaultEnv "POSTGRES_PASSWORD" "local-compose-password"
Set-DefaultEnv "POSTGRES_DB" "vpn_platform"
Set-DefaultEnv "POSTGRES_PORT" "5432"
Set-DefaultEnv "REDIS_PASSWORD" "local-compose-redis"
Set-DefaultEnv "IDENTITY_DB_PASSWORD" "local-compose-identity"
Set-DefaultEnv "CATALOG_DB_PASSWORD" "local-compose-catalog"
Set-DefaultEnv "BILLING_DB_PASSWORD" "local-compose-billing"
Set-DefaultEnv "SUBSCRIPTION_DB_PASSWORD" "local-compose-subscription"
Set-DefaultEnv "ACCESS_DB_PASSWORD" "local-compose-access"
Set-DefaultEnv "PROVISIONING_DB_PASSWORD" "local-compose-provisioning"
Set-DefaultEnv "ACCESS_CREDENTIAL_KEY_BASE64" "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="
Set-DefaultEnv "ACCESS_TOKEN_HMAC_KEY_BASE64" "ZmVkY2JhOTg3NjU0MzIxMGZlZGNiYTk4NzY1NDMyMTA="
Set-DefaultEnv "TELEGRAM_WEBHOOK_SECRET" "local-compose-webhook-secret"
Set-DefaultEnv "TELEGRAM_BOT_TOKEN" "local-compose-fake-bot-token"
Set-DefaultEnv "YOOKASSA_SECRET_KEY" "local-compose-yookassa-key"
Set-DefaultEnv "COMPOSE_PARALLEL_LIMIT" "2"
Set-DefaultEnv "COMPOSE_BAKE" "false"
Set-DefaultEnv "GOCACHE" "D:\Work\Projects\dev\go-work\cache"
Set-DefaultEnv "GOTMPDIR" "D:\Work\Projects\dev\go-work\tmp"
Set-DefaultEnv "TEMP" "D:\Work\Projects\dev\tmp"
Set-DefaultEnv "TMP" "D:\Work\Projects\dev\tmp"

$profiles = @("--profile", "core", "--profile", "app", "--profile", "vpn")
$clientName = "vpn-stage6-client"
$clientImage = "ghcr.io/xtls/xray-core:26.3.27@sha256:592ec4d11f656db95598d01e76dbcc6e002d67360b96a5436500a938230f52c7"
$goImage = "golang:1.26.5-alpine@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2"
$repo = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$credentialID = "62000000-0000-4000-8000-000000000090"

function Invoke-Compose([string[]]$arguments) {
    & docker compose @profiles @arguments
    if ($LASTEXITCODE -ne 0) {
        throw "docker compose failed: $($arguments -join ' ')"
    }
}

function Invoke-GoTool([string[]]$arguments) {
    $mappedArguments = foreach ($argument in $arguments) {
        $argument.Replace("https://127.0.0.1:18443", "https://node-agent-primary.local:18443").Replace("https://127.0.0.1:28443", "https://node-agent-failover.local:28443")
    }
    $result = & docker run --rm `
        --add-host "node-agent-primary.local:host-gateway" `
        --add-host "node-agent-failover.local:host-gateway" `
        -v "$($repo):/src" `
        -v "vpn-service-go-mod-cache:/go/pkg/mod" `
        -v "vpn-service-go-build-cache:/root/.cache/go-build" `
        -w /src `
        $goImage `
        go @mappedArguments
    if ($LASTEXITCODE -ne 0) {
        throw "containerized Go tool failed"
    }
    return $result
}

function Invoke-Desired([int]$port, [string]$bodyFile, [int]$status, [switch]$PrintBody) {
    $arguments = @("run", "./tools/mtlsprobe/cmd/mtlsprobe", "PUT", "https://127.0.0.1:$port/internal/v1/credentials/$credentialID", "secrets/dev-mtls/provisioning-service.crt", "secrets/dev-mtls/provisioning-service.key", "secrets/dev-mtls/ca.crt", "$status", "body-file", $bodyFile)
    if ($PrintBody) {
        $arguments += "print-body"
    }
    $result = Invoke-GoTool $arguments
    return $result
}

try {
    New-Item -ItemType Directory -Force -Path $env:TEMP | Out-Null
    & powershell -NoProfile -ExecutionPolicy Bypass -File scripts/dev-mtls.ps1
    if ($LASTEXITCODE -ne 0) { throw "mTLS generation failed" }
    & powershell -NoProfile -ExecutionPolicy Bypass -File scripts/dev-xray.ps1
    if ($LASTEXITCODE -ne 0) { throw "Xray material generation failed" }

    Invoke-Compose @("down", "-v", "--remove-orphans")
    Invoke-Compose @("build", "provisioning-migrate", "node-agent-primary")
    Invoke-Compose @("up", "-d", "postgres", "camouflage", "node-agent-primary", "node-agent-failover")

    foreach ($attempt in 1..90) {
        $postgres = & docker inspect -f "{{.State.Health.Status}}" "vpn-service-postgres-1" 2>$null
        $primary = & docker inspect -f "{{.State.Health.Status}}" "vpn-service-node-agent-primary-1" 2>$null
        $failover = & docker inspect -f "{{.State.Health.Status}}" "vpn-service-node-agent-failover-1" 2>$null
        if ($postgres -eq "healthy" -and $primary -eq "healthy" -and $failover -eq "healthy") { break }
        if ($attempt -eq 90) { throw "Stage 6 containers did not become healthy" }
        Start-Sleep -Seconds 1
    }

    Invoke-Compose @("run", "--rm", "provisioning-migrate")
    Invoke-Compose @("run", "--rm", "provisioning-seed")
    $previousDatabaseURL = $env:PROVISIONING_TEST_DATABASE_URL
    try {
        $env:PROVISIONING_TEST_DATABASE_URL = "postgres://provisioning_app:$($env:PROVISIONING_DB_PASSWORD)@127.0.0.1:$($env:POSTGRES_PORT)/provisioning_service?sslmode=disable"
        & go test ./services/provisioning/internal/postgres -count=1
        if ($LASTEXITCODE -ne 0) { throw "provisioning PostgreSQL tests failed" }
    } finally {
        $env:PROVISIONING_TEST_DATABASE_URL = $previousDatabaseURL
    }

    $first = (Invoke-Desired 18443 "secrets/dev-xray/smoke-present.json" 200 -PrintBody | Out-String | ConvertFrom-Json)
    Invoke-Desired 28443 "secrets/dev-xray/smoke-present.json" 200 | Out-Null
    $replay = (Invoke-Desired 18443 "secrets/dev-xray/smoke-present.json" 200 -PrintBody | Out-String | ConvertFrom-Json)
    if ($first.config_revision -ne $replay.config_revision -or $first.applied_at -ne $replay.applied_at) {
        throw "node-agent replay was not idempotent"
    }
    Invoke-GoTool @("run", "./tools/mtlsprobe/cmd/mtlsprobe", "PUT", "https://127.0.0.1:18443/internal/v1/credentials/$credentialID", "secrets/dev-mtls/node-health-primary.crt", "secrets/dev-mtls/node-health-primary.key", "secrets/dev-mtls/ca.crt", "403", "body-file", "secrets/dev-xray/smoke-present.json") | Out-Null

    & docker run --rm -v "$((Resolve-Path 'secrets/dev-xray/smoke-client.json').Path):/etc/xray/client.json:ro" $clientImage run -test -config /etc/xray/client.json | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "Xray smoke client configuration is invalid" }
    & docker run -d --name $clientName --network vpn-service_vpn-data -p "127.0.0.1:11080:1080" -v "$((Resolve-Path 'secrets/dev-xray/smoke-client.json').Path):/etc/xray/client.json:ro" $clientImage run -config /etc/xray/client.json | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "start Xray smoke client failed" }
    foreach ($attempt in 1..30) {
        $tcp = Test-NetConnection -ComputerName 127.0.0.1 -Port 11080 -WarningAction SilentlyContinue
        if ($tcp.TcpTestSucceeded) { break }
        if ($attempt -eq 30) { throw "Xray smoke SOCKS listener did not start" }
        Start-Sleep -Milliseconds 500
    }
    $body = & curl.exe -sS --socks5-hostname 127.0.0.1:11080 --connect-timeout 5 --max-time 10 http://camouflage.local/
    if ($LASTEXITCODE -ne 0 -or "$body" -notmatch "local camouflage endpoint") {
        throw "VLESS + REALITY data-plane request failed"
    }

    Invoke-Desired 18443 "secrets/dev-xray/smoke-absent.json" 200 | Out-Null
    Invoke-Desired 28443 "secrets/dev-xray/smoke-absent.json" 200 | Out-Null
    $requestPreference = $ErrorActionPreference
    $ErrorActionPreference = "Continue"
    & curl.exe -sS --socks5-hostname 127.0.0.1:11080 --connect-timeout 3 --max-time 5 http://camouflage.local/ 2>$null | Out-Null
    $revokedExitCode = $LASTEXITCODE
    $ErrorActionPreference = $requestPreference
    if ($revokedExitCode -eq 0) { throw "revoked VLESS credential still passed traffic" }

    Write-Host "Stage 6 VPN smoke passed: mTLS, capacity SQL, real Xray validation, VLESS + REALITY, replay, and revoke."
} catch {
    Write-Warning "Stage 6 VPN smoke failed; safe process logs follow."
	$logPreference = $ErrorActionPreference
	$ErrorActionPreference = "SilentlyContinue"
    foreach ($container in @($clientName, "vpn-service-node-agent-primary-1", "vpn-service-node-agent-failover-1", "vpn-service-camouflage-1")) {
        & docker logs --tail 40 $container 2>$null
    }
	$ErrorActionPreference = $logPreference
    throw
} finally {
	$previousPreference = $ErrorActionPreference
	$ErrorActionPreference = "SilentlyContinue"
    & docker rm -f $clientName 2>$null | Out-Null
    & docker compose @profiles down -v --remove-orphans 2>$null | Out-Null
	$ErrorActionPreference = $previousPreference
}

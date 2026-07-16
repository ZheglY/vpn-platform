$ErrorActionPreference = "Stop"

function Set-DefaultEnv([string]$name, [string]$value) {
    if ([string]::IsNullOrWhiteSpace([Environment]::GetEnvironmentVariable($name))) {
        [Environment]::SetEnvironmentVariable($name, $value, "Process")
    }
}

Set-DefaultEnv "POSTGRES_USER" "vpn_local"
Set-DefaultEnv "POSTGRES_PASSWORD" "local-compose-password"
Set-DefaultEnv "POSTGRES_DB" "vpn_platform"
Set-DefaultEnv "REDIS_PASSWORD" "local-compose-redis"
Set-DefaultEnv "KAFKA_PORT" "9094"
Set-DefaultEnv "IDENTITY_DB_PASSWORD" "local-compose-identity"
Set-DefaultEnv "TELEGRAM_WEBHOOK_SECRET" "local-compose-webhook-secret"
Set-DefaultEnv "TELEGRAM_BOT_TOKEN" "local-compose-fake-bot-token"

try {
    & powershell -NoProfile -ExecutionPolicy Bypass -File scripts/dev-mtls.ps1
    if ($LASTEXITCODE -ne 0) {
        exit $LASTEXITCODE
    }

    docker compose --profile core --profile app up -d --build
    if ($LASTEXITCODE -ne 0) {
        exit $LASTEXITCODE
    }

    $containers = @(
        "vpn-service-postgres-1",
        "vpn-service-redis-1",
        "vpn-service-kafka-1",
        "vpn-service-identity-service-1",
        "vpn-service-telegram-bot-1"
    )
    foreach ($attempt in 1..60) {
        $statuses = @()
        foreach ($container in $containers) {
            $statuses += docker inspect -f "{{.State.Health.Status}}" $container
        }
        if (($statuses | Where-Object { $_ -ne "healthy" }).Count -eq 0) {
            break
        }
        if ($attempt -eq 60) {
            docker compose --profile core --profile app ps
            throw "Compose services did not become healthy: $($statuses -join ', ')"
        }
        Start-Sleep -Seconds 2
    }

    docker compose exec -T kafka /opt/kafka/bin/kafka-topics.sh --bootstrap-server localhost:9094 --list | Out-Null
    docker compose exec -T identity-service /identity-service healthcheck | Out-Null

    $botLive = Invoke-RestMethod -Uri "http://127.0.0.1:8081/livez" -TimeoutSec 10
    $botReady = Invoke-RestMethod -Uri "http://127.0.0.1:8081/readyz" -TimeoutSec 10
    $botVersion = Invoke-RestMethod -Uri "http://127.0.0.1:8081/version" -TimeoutSec 10

    if ($botLive.status -ne "ok" -or $botReady.status -ne "ready" -or $botVersion.service -ne "telegram-bot") {
        throw "unexpected bot smoke response: live=$($botLive.status), ready=$($botReady.status), service=$($botVersion.service)"
    }
} finally {
    docker compose --profile core --profile app down -v
}

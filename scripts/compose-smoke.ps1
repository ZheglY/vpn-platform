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

try {
    docker compose --profile core --profile app up -d --build
    if ($LASTEXITCODE -ne 0) {
        exit $LASTEXITCODE
    }

    $containers = @(
        "vpn-service-postgres-1",
        "vpn-service-redis-1",
        "vpn-service-kafka-1",
        "vpn-service-identity-service-1"
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

    $live = Invoke-RestMethod -Uri "http://127.0.0.1:8080/livez" -TimeoutSec 10
    $ready = Invoke-RestMethod -Uri "http://127.0.0.1:8080/readyz" -TimeoutSec 10
    $version = Invoke-RestMethod -Uri "http://127.0.0.1:8080/version" -TimeoutSec 10

    if ($live.status -ne "ok" -or $ready.status -ne "ready" -or $version.service -ne "identity-service") {
        throw "unexpected smoke response: live=$($live.status), ready=$($ready.status), service=$($version.service)"
    }
} finally {
    docker compose --profile core --profile app down
}

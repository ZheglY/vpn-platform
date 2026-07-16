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
Set-DefaultEnv "FAKE_TELEGRAM_SEND_DELAY" "1s"
Set-DefaultEnv "TERMS_URL" "https://example.invalid/terms/terms-v1"

function Invoke-WebhookStatus([string]$body) {
    $responsePath = Join-Path $env:TEMP "vpn-platform-webhook-response.json"
    $requestPath = Join-Path $env:TEMP "vpn-platform-webhook-request.json"
    try {
        [System.IO.File]::WriteAllText($requestPath, $body, [System.Text.UTF8Encoding]::new($false))
        $status = & curl.exe -sS -o $responsePath -w "%{http_code}" `
            -H "X-Telegram-Bot-Api-Secret-Token: $env:TELEGRAM_WEBHOOK_SECRET" `
            -H "Content-Type: application/json" `
            --data-binary "@$requestPath" `
            "http://127.0.0.1:8081/webhooks/telegram"
        if ($LASTEXITCODE -ne 0) {
            throw "curl webhook request failed with exit code $LASTEXITCODE"
        }
        return [int]$status
    } finally {
        if (Test-Path $responsePath) {
            Remove-Item $responsePath -Force
        }
        if (Test-Path $requestPath) {
            Remove-Item $requestPath -Force
        }
    }
}

function Invoke-ScalarSQL([string]$sql) {
    $value = docker compose exec -T postgres psql --username="$env:POSTGRES_USER" --dbname identity_service -tAc $sql
    if ($LASTEXITCODE -ne 0) {
        exit $LASTEXITCODE
    }
    return "$value".Trim()
}

function Wait-RedisProcessingKey([int64]$updateID) {
    $key = "telegram:dedupe:processing:$updateID"
    foreach ($attempt in 1..100) {
        $exists = docker compose exec -T -e "REDISCLI_AUTH=$env:REDIS_PASSWORD" redis redis-cli --raw EXISTS $key
        if ("$exists".Trim() -eq "1") {
            return
        }
        Start-Sleep -Milliseconds 100
    }
    throw "processing dedupe key was not observed for update_id=$updateID"
}

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
        "vpn-service-telegram-api-1",
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

    Invoke-RestMethod -Uri "http://127.0.0.1:8082/reset" -Method Post -TimeoutSec 10 | Out-Null

    $startBody = '{"update_id":2001,"message":{"message_id":1,"text":"/start","chat":{"id":9001},"from":{"id":4200001,"first_name":"Smoke","language_code":"en"}}}'
    $firstJob = Start-Job -ScriptBlock {
        param([string]$secret, [string]$body)
        $responsePath = Join-Path $env:TEMP "vpn-platform-webhook-first-response.json"
        $requestPath = Join-Path $env:TEMP "vpn-platform-webhook-first-request.json"
        try {
            [System.IO.File]::WriteAllText($requestPath, $body, [System.Text.UTF8Encoding]::new($false))
            $status = & curl.exe -sS -o $responsePath -w "%{http_code}" -H "X-Telegram-Bot-Api-Secret-Token: $secret" -H "Content-Type: application/json" --data-binary "@$requestPath" "http://127.0.0.1:8081/webhooks/telegram"
            if ($LASTEXITCODE -ne 0) {
                throw "curl webhook request failed with exit code $LASTEXITCODE"
            }
            [int]$status
        } finally {
            if (Test-Path $responsePath) {
                Remove-Item $responsePath -Force
            }
            if (Test-Path $requestPath) {
                Remove-Item $requestPath -Force
            }
        }
    } -ArgumentList $env:TELEGRAM_WEBHOOK_SECRET, $startBody
    Wait-RedisProcessingKey 2001
    $concurrentStatus = Invoke-WebhookStatus $startBody
    $firstStatus = Receive-Job -Job $firstJob -Wait
    Remove-Job -Job $firstJob
    if ([int]$firstStatus -ne 200 -or $concurrentStatus -ne 503) {
        throw "unexpected concurrent webhook statuses: first=$firstStatus duplicate=$concurrentStatus"
    }

    $completedReplayStatus = Invoke-WebhookStatus $startBody
    if ($completedReplayStatus -ne 200) {
        throw "completed replay status=$completedReplayStatus, want 200"
    }
    $messages = Invoke-RestMethod -Uri "http://127.0.0.1:8082/messages" -TimeoutSec 10
    if ($messages.count -ne 1) {
        throw "message count after duplicate=$($messages.count), want 1"
    }

    $acceptBody = '{"update_id":2002,"message":{"message_id":2,"text":"accept","chat":{"id":9001},"from":{"id":4200001,"first_name":"Smoke","language_code":"en"}}}'
    $acceptStatus = Invoke-WebhookStatus $acceptBody
    if ($acceptStatus -ne 200) {
        throw "accept webhook status=$acceptStatus, want 200"
    }
    $messages = Invoke-RestMethod -Uri "http://127.0.0.1:8082/messages" -TimeoutSec 10
    if ($messages.count -ne 2) {
        throw "message count after consent=$($messages.count), want 2"
    }

    $identityCount = Invoke-ScalarSQL "SELECT count(*) FROM telegram_identities WHERE telegram_user_id = 4200001"
    $consentCount = Invoke-ScalarSQL "SELECT count(*) FROM consents c JOIN telegram_identities t ON t.user_id = c.user_id WHERE t.telegram_user_id = 4200001 AND c.document_type = 'terms' AND c.document_version = 'terms-v1'"
    if ($identityCount -ne "1" -or $consentCount -ne "1") {
        throw "unexpected database counts: identities=$identityCount consents=$consentCount"
    }

    go run ./tools/mtlsprobe/cmd/mtlsprobe PUT https://127.0.0.1:8080/internal/v1/telegram-users/999 secrets/dev-mtls/identity-health.crt secrets/dev-mtls/identity-health.key secrets/dev-mtls/ca.crt 403
    if ($LASTEXITCODE -ne 0) {
        exit $LASTEXITCODE
    }
} finally {
    docker compose --profile core --profile app down -v
}

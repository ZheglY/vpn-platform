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
Set-DefaultEnv "CATALOG_DB_PASSWORD" "local-compose-catalog"
Set-DefaultEnv "BILLING_DB_PASSWORD" "local-compose-billing"
Set-DefaultEnv "TELEGRAM_WEBHOOK_SECRET" "local-compose-webhook-secret"
Set-DefaultEnv "TELEGRAM_BOT_TOKEN" "local-compose-fake-bot-token"
Set-DefaultEnv "FAKE_TELEGRAM_SEND_DELAY" "1s"
Set-DefaultEnv "TERMS_URL" "https://example.invalid/terms/terms-v1"
Set-DefaultEnv "YOOKASSA_SHOP_ID" "test-shop"
Set-DefaultEnv "YOOKASSA_SECRET_KEY" "local-compose-yookassa-key"
Set-DefaultEnv "PAYMENT_RETURN_URL" "https://example.invalid/payment-return"
Set-DefaultEnv "COMPOSE_PARALLEL_LIMIT" "2"
Set-DefaultEnv "COMPOSE_BAKE" "false"

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

function Invoke-ScalarSQL([string]$database, [string]$sql) {
    $value = docker compose exec -T postgres psql --username="$env:POSTGRES_USER" --dbname $database -tAc $sql
    if ($LASTEXITCODE -ne 0) {
        exit $LASTEXITCODE
    }
    return "$value".Trim()
}

function Invoke-YooKassaWebhookStatus([string]$body) {
    $responsePath = Join-Path $env:TEMP "vpn-platform-yookassa-response.json"
    $requestPath = Join-Path $env:TEMP "vpn-platform-yookassa-request.json"
    try {
        [System.IO.File]::WriteAllText($requestPath, $body, [System.Text.UTF8Encoding]::new($false))
        $status = & curl.exe -sS -o $responsePath -w "%{http_code}" `
            --cacert "secrets/dev-mtls/ca.crt" `
            --ssl-no-revoke `
            -H "Content-Type: application/json" `
            --data-binary "@$requestPath" `
            "https://127.0.0.1:8084/webhooks/yookassa"
        if ($LASTEXITCODE -ne 0) {
            throw "curl YooKassa webhook request failed with exit code $LASTEXITCODE"
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

    $buildServices = @("identity-migrate", "identity-service", "catalog-service", "billing-service", "yookassa-api", "telegram-api", "telegram-bot")
    foreach ($service in $buildServices) {
        docker compose --profile core --profile app build $service
        if ($LASTEXITCODE -ne 0) {
            exit $LASTEXITCODE
        }
    }

    docker compose --profile core --profile app up -d --no-build
    if ($LASTEXITCODE -ne 0) {
        exit $LASTEXITCODE
    }

    $containers = @(
        "vpn-service-postgres-1",
        "vpn-service-redis-1",
        "vpn-service-kafka-1",
        "vpn-service-identity-service-1",
        "vpn-service-catalog-service-1",
        "vpn-service-yookassa-api-1",
        "vpn-service-billing-service-1",
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

    $identityCount = Invoke-ScalarSQL "identity_service" "SELECT count(*) FROM telegram_identities WHERE telegram_user_id = 4200001"
    $consentCount = Invoke-ScalarSQL "identity_service" "SELECT count(*) FROM consents c JOIN telegram_identities t ON t.user_id = c.user_id WHERE t.telegram_user_id = 4200001 AND c.document_type = 'terms' AND c.document_version = 'terms-v1'"
    if ($identityCount -ne "1" -or $consentCount -ne "1") {
        throw "unexpected database counts: identities=$identityCount consents=$consentCount"
    }

    Invoke-RestMethod -Uri "http://127.0.0.1:8085/test/reset" -Method Post -TimeoutSec 10 | Out-Null
    Invoke-RestMethod -Uri "http://127.0.0.1:8085/test/fail-next" -Method Post -ContentType "application/json" -Body '{"mode":"ambiguous_after_commit"}' -TimeoutSec 10 | Out-Null

    $buyBody = '{"update_id":2003,"message":{"message_id":3,"text":"/buy","chat":{"id":9001},"from":{"id":4200001,"first_name":"Smoke","language_code":"en"}}}'
    $buyStatus = Invoke-WebhookStatus $buyBody
    if ($buyStatus -ne 200) {
        throw "buy webhook status=$buyStatus, want 200"
    }

    $providerPaymentID = ""
    $providerState = $null
    foreach ($attempt in 1..80) {
        $providerState = Invoke-RestMethod -Uri "http://127.0.0.1:8085/test/payments" -TimeoutSec 10
        $providerPaymentID = Invoke-ScalarSQL "billing_service" "SELECT COALESCE(provider_payment_id,'') FROM payments LIMIT 1"
        if ($providerState.count -eq 1 -and $providerState.create_attempts -ge 2 -and ![string]::IsNullOrWhiteSpace($providerPaymentID)) {
            break
        }
        Start-Sleep -Milliseconds 250
    }
    if ($providerState.count -ne 1 -or $providerState.create_attempts -lt 2 -or [string]::IsNullOrWhiteSpace($providerPaymentID)) {
        throw "ambiguous provider create was not reconciled exactly once"
    }

    $retryBuyBody = '{"update_id":2004,"message":{"message_id":4,"text":"/buy","chat":{"id":9001},"from":{"id":4200001,"first_name":"Smoke","language_code":"en"}}}'
    if ((Invoke-WebhookStatus $retryBuyBody) -ne 200) {
        throw "retry buy webhook did not return 200"
    }
    $orderCount = Invoke-ScalarSQL "billing_service" "SELECT count(*) FROM orders"
    $paymentCount = Invoke-ScalarSQL "billing_service" "SELECT count(*) FROM payments"
    $providerState = Invoke-RestMethod -Uri "http://127.0.0.1:8085/test/payments" -TimeoutSec 10
    if ($orderCount -ne "1" -or $paymentCount -ne "1" -or $providerState.count -ne 1) {
        throw "duplicate buy created extra state: orders=$orderCount payments=$paymentCount provider=$($providerState.count)"
    }

    Invoke-RestMethod -Uri "http://127.0.0.1:8085/test/payments/$providerPaymentID/status" -Method Post -ContentType "application/json" -Body '{"status":"succeeded"}' -TimeoutSec 10 | Out-Null
    $succeededWebhook = '{"type":"notification","event":"payment.succeeded","object":{"id":"' + $providerPaymentID + '","status":"succeeded","sensitive_ignored":"must-not-persist"}}'
    $firstYooStatus = Invoke-YooKassaWebhookStatus $succeededWebhook
    $duplicateYooStatus = Invoke-YooKassaWebhookStatus $succeededWebhook
    if ($firstYooStatus -ne 200 -or $duplicateYooStatus -ne 200) {
        throw "unexpected YooKassa webhook statuses: first=$firstYooStatus duplicate=$duplicateYooStatus"
    }

    foreach ($attempt in 1..80) {
        $paymentStatus = Invoke-ScalarSQL "billing_service" "SELECT status FROM payments LIMIT 1"
        $orderStatus = Invoke-ScalarSQL "billing_service" "SELECT status FROM orders LIMIT 1"
        $publishedCount = Invoke-ScalarSQL "billing_service" "SELECT count(*) FROM outbox WHERE topic='billing.payment.succeeded.v1' AND state='published'"
        $processedInbox = Invoke-ScalarSQL "billing_service" "SELECT count(*) FROM webhook_inbox WHERE event_type='payment.succeeded' AND state='processed'"
        if ($paymentStatus -eq "succeeded" -and $orderStatus -eq "paid" -and $publishedCount -eq "1" -and $processedInbox -eq "1") {
            break
        }
        Start-Sleep -Milliseconds 250
    }
    $succeededInboxCount = Invoke-ScalarSQL "billing_service" "SELECT count(*) FROM webhook_inbox WHERE event_type='payment.succeeded'"
    if ($paymentStatus -ne "succeeded" -or $orderStatus -ne "paid" -or $publishedCount -ne "1" -or $processedInbox -ne "1" -or $succeededInboxCount -ne "1") {
        throw "verified payment transition did not converge exactly once: payment=$paymentStatus order=$orderStatus published=$publishedCount processed_inbox=$processedInbox inbox_count=$succeededInboxCount"
    }

    $outOfOrderWebhook = '{"type":"notification","event":"payment.canceled","object":{"id":"' + $providerPaymentID + '","status":"canceled"}}'
    if ((Invoke-YooKassaWebhookStatus $outOfOrderWebhook) -ne 200) {
        throw "out-of-order webhook did not return 200"
    }
    foreach ($attempt in 1..80) {
        $canceledInboxState = Invoke-ScalarSQL "billing_service" "SELECT state FROM webhook_inbox WHERE event_type='payment.canceled' LIMIT 1"
        if ($canceledInboxState -eq "processed") {
            break
        }
        Start-Sleep -Milliseconds 250
    }
    $terminalEventCount = Invoke-ScalarSQL "billing_service" "SELECT count(*) FROM outbox"
    $paymentStatus = Invoke-ScalarSQL "billing_service" "SELECT status FROM payments LIMIT 1"
    if ($canceledInboxState -ne "processed" -or $terminalEventCount -ne "1" -or $paymentStatus -ne "succeeded") {
        throw "out-of-order webhook changed a terminal payment or duplicated its event"
    }

    $kafkaStdout = Join-Path $env:TEMP "vpn-platform-kafka-event.json"
    $kafkaStderr = Join-Path $env:TEMP "vpn-platform-kafka-consumer.log"
    try {
        $kafkaProcess = Start-Process -FilePath "docker.exe" -ArgumentList @(
            "compose", "exec", "-T", "kafka",
            "/opt/kafka/bin/kafka-console-consumer.sh",
            "--bootstrap-server", "localhost:9092",
            "--topic", "billing.payment.succeeded.v1",
            "--from-beginning", "--max-messages", "1", "--timeout-ms", "10000"
        ) -NoNewWindow -Wait -PassThru -RedirectStandardOutput $kafkaStdout -RedirectStandardError $kafkaStderr
        $kafkaExitCode = $kafkaProcess.ExitCode
        $kafkaEvent = [System.IO.File]::ReadAllText($kafkaStdout)
    } finally {
        if (Test-Path $kafkaStdout) {
            Remove-Item $kafkaStdout -Force
        }
        if (Test-Path $kafkaStderr) {
            Remove-Item $kafkaStderr -Force
        }
    }
    if ($kafkaExitCode -ne 0 -or "$kafkaEvent" -notmatch 'billing\.payment\.succeeded\.v1') {
        throw "billing payment event was not observable in Kafka: exit=$kafkaExitCode bytes=$($kafkaEvent.Length)"
    }

    go run ./tools/mtlsprobe/cmd/mtlsprobe PUT https://127.0.0.1:8080/internal/v1/telegram-users/999 secrets/dev-mtls/identity-health.crt secrets/dev-mtls/identity-health.key secrets/dev-mtls/ca.crt 403
    if ($LASTEXITCODE -ne 0) {
        exit $LASTEXITCODE
    }
    go run ./tools/mtlsprobe/cmd/mtlsprobe POST https://127.0.0.1:8080/internal/v1/users/00000000-0000-4000-8000-000000000001/consents secrets/dev-mtls/billing-service.crt secrets/dev-mtls/billing-service.key secrets/dev-mtls/ca.crt 403
    if ($LASTEXITCODE -ne 0) {
        exit $LASTEXITCODE
    }
    go run ./tools/mtlsprobe/cmd/mtlsprobe POST https://127.0.0.1:8084/internal/v1/users/00000000-0000-4000-8000-000000000001/orders secrets/dev-mtls/billing-service.crt secrets/dev-mtls/billing-service.key secrets/dev-mtls/ca.crt 403
    if ($LASTEXITCODE -ne 0) {
        exit $LASTEXITCODE
    }
    go run ./tools/mtlsprobe/cmd/mtlsprobe GET https://127.0.0.1:8084/internal/v1/users/00000000-0000-4000-8000-000000000001/orders/00000000-0000-4000-8000-000000000002 secrets/dev-mtls/identity-health.crt secrets/dev-mtls/identity-health.key secrets/dev-mtls/ca.crt 403
    if ($LASTEXITCODE -ne 0) {
        exit $LASTEXITCODE
    }
} finally {
    docker compose --profile core --profile app down -v
}

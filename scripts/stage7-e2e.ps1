param(
    [Parameter(Mandatory = $true)][ValidateSet("BeforeRevoke", "AfterRevoke")][string]$Phase,
    [Parameter(Mandatory = $true)][string]$UserID,
    [Parameter(Mandatory = $true)][string]$SubscriptionID,
    [Parameter(Mandatory = $true)][string]$CredentialID
)

$ErrorActionPreference = "Stop"
$repo = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$composeProject = if (-not [string]::IsNullOrWhiteSpace($env:COMPOSE_PROJECT_NAME)) { $env:COMPOSE_PROJECT_NAME } else { "vpn-service" }
$goModCacheVolume = "${composeProject}-go-mod-cache"
$goBuildCacheVolume = "${composeProject}-go-build-cache"
$goImage = "golang:1.26.5-alpine@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2"

function Invoke-ScalarSQL([string]$database, [string]$sql) {
    $value = docker compose exec -T postgres psql --username="$env:POSTGRES_USER" --dbname $database -tAc $sql
    if ($LASTEXITCODE -ne 0) { throw "Stage 7 SQL probe failed" }
    return "$value".Trim()
}

function Write-NotificationDiagnostics {
    Write-Host "Stage 7 notification job counts (type|status|count):"
    docker compose exec -T postgres psql --username="$env:POSTGRES_USER" --dbname notification_service -tAc "SELECT notification_type || '|' || status || '|' || count(*) FROM notification_jobs GROUP BY notification_type,status ORDER BY notification_type,status"
    Write-Host "Stage 7 notification inbox counts (event_type|count):"
    docker compose exec -T postgres psql --username="$env:POSTGRES_USER" --dbname notification_service -tAc "SELECT event_type || '|' || count(*) FROM notification_inbox GROUP BY event_type ORDER BY event_type"
    Write-Host "Stage 7 notification DLQ counts (reason|count):"
    docker compose exec -T postgres psql --username="$env:POSTGRES_USER" --dbname notification_service -tAc "SELECT reason_code || '|' || count(*) FROM notification_dead_letters GROUP BY reason_code ORDER BY reason_code"
    docker compose logs --tail=120 notification-service telegram-bot
}

function Wait-SQL([string]$database, [string]$sql, [string]$expected, [int]$attempts = 120, [string]$label = "state") {
    foreach ($attempt in 1..$attempts) {
        $value = Invoke-ScalarSQL $database $sql
        if ($value -eq $expected) { return }
        Start-Sleep -Milliseconds 250
    }
    Write-NotificationDiagnostics
    throw "Stage 7 state did not converge for ${label}: expected $expected, got $value"
}

function Publish-Event([string]$topic, [string]$key, [string]$payload) {
    $record = "$key|$payload`n"
    $encoded = [Convert]::ToBase64String([System.Text.UTF8Encoding]::new($false).GetBytes($record))
    $producerCommand = 'printf %s $KAFKA_RECORD_BASE64 | base64 -d | /opt/kafka/bin/kafka-console-producer.sh --bootstrap-server localhost:9092 --topic ' + $topic + ' --reader-property parse.key=true --reader-property key.separator=\|'
    docker compose exec -T -e "KAFKA_RECORD_BASE64=$encoded" kafka sh -c $producerCommand
    if ($LASTEXITCODE -ne 0) { throw "Stage 7 Kafka publish failed for $topic" }
}

function Set-TelegramBehavior([string]$mode, [int]$count, [int]$retryAfter = 0) {
    $body = @{ mode = $mode; count = $count; retry_after_seconds = $retryAfter } | ConvertTo-Json -Compress
    Invoke-RestMethod -Uri "http://127.0.0.1:8082/behavior" -Method Post -ContentType "application/json" -Body $body -TimeoutSec 10 | Out-Null
}

function Invoke-Admin([string]$certificate, [string]$command, [string[]]$arguments) {
    $stderrPath = Join-Path $env:TEMP "vpn-platform-stage7-admin-error.txt"
    try {
        $output = & go run ./services/admin/cmd/admin-cli --base-url https://127.0.0.1:8092 --cert "secrets/dev-mtls/$certificate.crt" --key "secrets/dev-mtls/$certificate.key" --ca secrets/dev-mtls/ca.crt --json $command @arguments 2>$stderrPath
        if ($LASTEXITCODE -ne 0) { throw "admin CLI command failed: $command" }
        return ($output -join "`n") | ConvertFrom-Json
    } finally {
        if (Test-Path $stderrPath) { Remove-Item $stderrPath -Force }
    }
}

function Assert-AdminHTTPFailure([string]$certificate, [int]$expectedStatus, [string]$command, [string[]]$arguments) {
    $stdoutPath = Join-Path $env:TEMP "vpn-platform-stage7-admin-denied-output.txt"
    $stderrPath = Join-Path $env:TEMP "vpn-platform-stage7-admin-denied-error.txt"
    $previousPreference = $ErrorActionPreference
    try {
        $ErrorActionPreference = "Continue"
        & go run ./services/admin/cmd/admin-cli --base-url https://127.0.0.1:8092 --cert "secrets/dev-mtls/$certificate.crt" --key "secrets/dev-mtls/$certificate.key" --ca secrets/dev-mtls/ca.crt --json $command @arguments 1>$stdoutPath 2>$stderrPath
        $exitCode = $LASTEXITCODE
        $errorOutput = if (Test-Path $stderrPath) { Get-Content -Raw $stderrPath } else { "" }
    } finally {
        $ErrorActionPreference = $previousPreference
        if (Test-Path $stdoutPath) { Remove-Item $stdoutPath -Force }
        if (Test-Path $stderrPath) { Remove-Item $stderrPath -Force }
    }
    if ($exitCode -eq 0) { throw "admin CLI unexpectedly allowed $command" }
    if ($errorOutput -notmatch "admin-service returned HTTP $expectedStatus(?:\s|$)") {
        throw "admin CLI failed with an unexpected result for ${command}"
    }
}

function New-PaymentEvent([string]$paymentID, [string]$eventID) {
    $now = (Get-Date).ToUniversalTime()
    return @{
        event_id = $eventID; event_type = "billing.payment.succeeded.v1"; schema_version = 1
        occurred_at = $now.ToString("o"); producer = "billing-service"
        correlation_id = "77000000-0000-4000-8000-000000000099"; causation_id = $null
        aggregate_type = "payment"; aggregate_id = $paymentID
        partition_key = "user:$UserID"
        data = @{
            payment_id = $paymentID; order_id = "77000000-0000-4000-8000-000000000090"
            user_id = $UserID; plan_id = "basic-monthly"; amount_minor = 49900
            currency = "RUB"; paid_at = $now.ToString("o")
        }
    } | ConvertTo-Json -Compress -Depth 6
}

if ($Phase -eq "BeforeRevoke") {
    Wait-SQL "notification_service" "SELECT count(*) FROM notification_jobs WHERE notification_type='payment_confirmed' AND status='delivered'" "1" 120 "delivered payment confirmation"
    Wait-SQL "notification_service" "SELECT count(*) FROM notification_jobs WHERE notification_type='access_ready' AND status='delivered'" "1" 120 "delivered access-ready notification"
    Wait-SQL "notification_service" "SELECT count(*) FROM notification_jobs WHERE notification_type='subscription_activated' AND status='suppressed'" "1" 120 "suppressed subscription activation"

    $messagesBeforeDuplicate = (Invoke-RestMethod -Uri "http://127.0.0.1:8082/messages" -TimeoutSec 10).count
    $paymentKey = Invoke-ScalarSQL "billing_service" "SELECT partition_key FROM outbox WHERE topic='billing.payment.succeeded.v1' LIMIT 1"
    $paymentPayload = Invoke-ScalarSQL "billing_service" "SELECT payload::text FROM outbox WHERE topic='billing.payment.succeeded.v1' LIMIT 1"
    Publish-Event "billing.payment.succeeded.v1" $paymentKey $paymentPayload
    Start-Sleep -Seconds 2
    if ((Invoke-ScalarSQL "notification_service" "SELECT count(*) FROM notification_jobs WHERE notification_type='payment_confirmed'") -ne "1") { throw "duplicate payment event created another notification job" }
    if ((Invoke-RestMethod -Uri "http://127.0.0.1:8082/messages" -TimeoutSec 10).count -ne $messagesBeforeDuplicate) { throw "duplicate payment event produced another Telegram message" }

    Set-TelegramBehavior "rate_limited" 1 2
    Publish-Event "billing.payment.succeeded.v1" "user:$UserID" (New-PaymentEvent "77000000-0000-4000-8000-000000000022" "77000000-0000-4000-8000-000000000002")
    Wait-SQL "notification_service" "SELECT status FROM notification_jobs WHERE business_dedupe_key='payment_confirmed:77000000-0000-4000-8000-000000000022'" "retry"
    $retryDelay = [double](Invoke-ScalarSQL "notification_service" "SELECT extract(epoch FROM (next_attempt_at-updated_at)) FROM notification_jobs WHERE business_dedupe_key='payment_confirmed:77000000-0000-4000-8000-000000000022'")
    if ($retryDelay -lt 1.5) { throw "Telegram Retry-After was not respected" }
    Wait-SQL "notification_service" "SELECT status || ':' || attempts FROM notification_jobs WHERE business_dedupe_key='payment_confirmed:77000000-0000-4000-8000-000000000022'" "delivered:2"

    Set-TelegramBehavior "blocked" 1
    Publish-Event "billing.payment.succeeded.v1" "user:$UserID" (New-PaymentEvent "77000000-0000-4000-8000-000000000023" "77000000-0000-4000-8000-000000000003")
    Wait-SQL "notification_service" "SELECT status || ':' || terminal_reason_code FROM notification_jobs WHERE business_dedupe_key='payment_confirmed:77000000-0000-4000-8000-000000000023'" "permanently_failed:telegram_bot_blocked"
    $failedNotificationID = Invoke-ScalarSQL "notification_service" "SELECT notification_id FROM notification_jobs WHERE business_dedupe_key='payment_confirmed:77000000-0000-4000-8000-000000000023'"

    $supportUser = Invoke-Admin "admin-support-local" "get-user" @("--user-id", $UserID)
    if ($supportUser.user_id -ne $UserID) { throw "read-only admin could not read user status" }
    Assert-AdminHTTPFailure "admin-support-local" 403 "retry-notification" @("--notification-id", $failedNotificationID, "--reason", "retry after blocked target test", "--idempotency-key", "stage7-retry-0001")

    Set-TelegramBehavior "success" 0
    $firstAction = Invoke-Admin "admin-operations-local" "retry-notification" @("--notification-id", $failedNotificationID, "--reason", "retry after blocked target test", "--idempotency-key", "stage7-retry-0001")
    if ($firstAction.status -ne "succeeded" -or $firstAction.replay) { throw "operations retry was not executed exactly once" }
    Wait-SQL "notification_service" "SELECT status FROM notification_jobs WHERE notification_id='$failedNotificationID'" "delivered"
    $replayAction = Invoke-Admin "admin-operations-local" "retry-notification" @("--notification-id", $failedNotificationID, "--reason", "retry after blocked target test", "--idempotency-key", "stage7-retry-0001")
    if (!$replayAction.replay -or $replayAction.action_id -ne $firstAction.action_id) { throw "admin mutation replay was not stable" }
    Assert-AdminHTTPFailure "admin-operations-local" 409 "retry-notification" @("--notification-id", $failedNotificationID, "--reason", "changed request must conflict", "--idempotency-key", "stage7-retry-0001")

    $health = Invoke-Admin "admin-support-local" "health" @()
    if ($health.services.Count -ne 6) { throw "admin health summary is incomplete" }
    $provisioning = Invoke-Admin "admin-support-local" "get-provisioning" @("--credential-id", $CredentialID)
    $provisioningJSON = $provisioning | ConvertTo-Json -Compress -Depth 8
    if ($provisioningJSON -match 'subscription_url|vless_client_uuid|vless://|management_url|private_key') { throw "admin provisioning output exposed secret material" }

    & docker run --rm `
        --add-host "admin-service.local:host-gateway" `
        -v "$($repo):/src" `
        -v "$($goModCacheVolume):/go/pkg/mod" `
        -v "$($goBuildCacheVolume):/root/.cache/go-build" `
        -w /src `
        $goImage `
        go run ./tools/mtlsprobe/cmd/mtlsprobe GET https://admin-service.local:8092/admin/v1/health secrets/dev-mtls/identity-health.crt secrets/dev-mtls/identity-health.key secrets/dev-mtls/ca.crt 403 | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "non-admin SPIFFE certificate was not rejected with 403" }
    $audit = Invoke-Admin "admin-support-local" "audit" @("--limit", "20")
    $auditJSON = $audit | ConvertTo-Json -Compress -Depth 8
    if ($audit.events.Count -lt 2 -or $auditJSON -notmatch 'operations-local' -or $auditJSON -match 'subscription_url|vless_client_uuid|vless://|telegram_chat_id|private_key') { throw "admin audit is incomplete or unsafe" }

    Set-TelegramBehavior "server_error" 10
    Publish-Event "billing.payment.succeeded.v1" "user:$UserID" (New-PaymentEvent "77000000-0000-4000-8000-000000000024" "77000000-0000-4000-8000-000000000004")
    Wait-SQL "notification_service" "SELECT status FROM notification_jobs WHERE business_dedupe_key='payment_confirmed:77000000-0000-4000-8000-000000000024'" "retry"
    $actionCount = Invoke-ScalarSQL "admin_service" "SELECT count(*) FROM admin_action_requests"
    docker compose restart notification-service admin-service | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "Stage 7 service restart failed" }
    foreach ($attempt in 1..60) {
        $notificationHealth = docker inspect -f "{{.State.Health.Status}}" "$composeProject-notification-service-1"
        $adminHealth = docker inspect -f "{{.State.Health.Status}}" "$composeProject-admin-service-1"
        if ($notificationHealth -eq "healthy" -and $adminHealth -eq "healthy") { break }
        Start-Sleep -Seconds 1
    }
    if ($notificationHealth -ne "healthy" -or $adminHealth -ne "healthy") { throw "Stage 7 services did not recover after restart" }
    Set-TelegramBehavior "success" 0
    Wait-SQL "notification_service" "SELECT status FROM notification_jobs WHERE business_dedupe_key='payment_confirmed:77000000-0000-4000-8000-000000000024'" "delivered"
    if ((Invoke-ScalarSQL "admin_service" "SELECT count(*) FROM admin_action_requests") -ne $actionCount) { throw "admin durable actions changed across restart" }

    Write-Host "Stage 7 pre-revoke notification and admin E2E checks passed."
    exit 0
}

Wait-SQL "notification_service" "SELECT count(*) FROM notification_jobs WHERE credential_id='$CredentialID' AND notification_type='access_physically_revoked' AND status='suppressed'" "1"
Wait-SQL "notification_service" "SELECT count(*) FROM notification_jobs WHERE subscription_id='$SubscriptionID' AND notification_type='subscription_revoked' AND status='delivered'" "1"
Wait-SQL "notification_service" "SELECT last_sequence FROM notification_cursors WHERE aggregate_type='access' AND aggregate_id='$CredentialID'" "2"
Wait-SQL "notification_service" "SELECT count(*) FROM notification_jobs WHERE status IN ('pending','retry','delivering')" "0"
$readyText = "<b>VPN access is ready.</b> Send /link to receive your one-time Happ subscription link."
$messagesBeforeStale = Invoke-RestMethod -Uri "http://127.0.0.1:8082/messages" -TimeoutSec 10
$readyMessagesBeforeStale = @($messagesBeforeStale.messages | Where-Object { $_.text -eq $readyText }).Count
$now = (Get-Date).ToUniversalTime().ToString("o")
$staleReady = @{
    event_id = "77000000-0000-4000-8000-000000000005"; event_type = "access.ready.v1"; schema_version = 1
    occurred_at = $now; producer = "access-service"; correlation_id = "77000000-0000-4000-8000-000000000098"; causation_id = $null
    aggregate_type = "access"; aggregate_id = $CredentialID; aggregate_sequence = 3; partition_key = "user:$UserID"
    data = @{ subscription_id = $SubscriptionID; credential_id = $CredentialID; user_id = $UserID; provisioning_status = "active"; ready_at = $now; link_issuance_required = $true }
} | ConvertTo-Json -Compress -Depth 5
Publish-Event "access.ready.v1" "user:$UserID" $staleReady
Wait-SQL "notification_service" "SELECT status || ':' || terminal_reason_code FROM notification_jobs WHERE business_dedupe_key='access_ready:${CredentialID}:3'" "suppressed:stale_subscription_state"
$messagesAfterStale = Invoke-RestMethod -Uri "http://127.0.0.1:8082/messages" -TimeoutSec 10
$readyMessagesAfterStale = @($messagesAfterStale.messages | Where-Object { $_.text -eq $readyText }).Count
if ($readyMessagesAfterStale -ne $readyMessagesBeforeStale) { throw "stale access-ready event was delivered after revoke" }

Write-Host "Stage 7 post-revoke stale-notification E2E checks passed."

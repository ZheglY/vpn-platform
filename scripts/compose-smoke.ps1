$ErrorActionPreference = "Stop"

function Set-DefaultEnv([string]$name, [string]$value) {
    if ([string]::IsNullOrWhiteSpace([Environment]::GetEnvironmentVariable($name))) {
        [Environment]::SetEnvironmentVariable($name, $value, "Process")
    }
}

$repo = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$composeProject = if (-not [string]::IsNullOrWhiteSpace($env:COMPOSE_PROJECT_NAME)) {
    $env:COMPOSE_PROJECT_NAME
} else {
    "vpn-service"
}
if ($composeProject -notmatch '^[a-z0-9][a-z0-9-]*$') {
    throw "COMPOSE_PROJECT_NAME must contain only lowercase letters, digits, and hyphens"
}
$backendNetwork = "${composeProject}_backend"
$vpnDataNetwork = "${composeProject}_vpn-data"
$goModCacheVolume = "${composeProject}-go-mod-cache"
$goBuildCacheVolume = "${composeProject}-go-build-cache"

function Get-ComposeContainerName([string]$service) {
    return "$composeProject-$service-1"
}

$cacheRoot = if (-not [string]::IsNullOrWhiteSpace($env:VPN_PLATFORM_CACHE_ROOT)) {
    $env:VPN_PLATFORM_CACHE_ROOT
} else {
    Join-Path (Split-Path $repo -Parent) ".cache\vpn-platform"
}
$smokeTemp = Join-Path $repo "tmp"
[System.IO.Directory]::CreateDirectory($smokeTemp) | Out-Null
$fallbackTemp = if (-not [string]::IsNullOrWhiteSpace($env:TEMP)) { $env:TEMP } else { Join-Path $repo "tmp" }

Set-DefaultEnv "POSTGRES_USER" "vpn_local"
Set-DefaultEnv "POSTGRES_PASSWORD" "local-compose-password"
Set-DefaultEnv "POSTGRES_DB" "vpn_platform"
Set-DefaultEnv "POSTGRES_PORT" "5432"
Set-DefaultEnv "REDIS_PASSWORD" "local-compose-redis"
Set-DefaultEnv "KAFKA_PORT" "9094"
Set-DefaultEnv "IDENTITY_DB_PASSWORD" "local-compose-identity"
Set-DefaultEnv "CATALOG_DB_PASSWORD" "local-compose-catalog"
Set-DefaultEnv "BILLING_DB_PASSWORD" "local-compose-billing"
Set-DefaultEnv "SUBSCRIPTION_DB_PASSWORD" "local-compose-subscription"
Set-DefaultEnv "ACCESS_DB_PASSWORD" "local-compose-access"
Set-DefaultEnv "PROVISIONING_DB_PASSWORD" "local-compose-provisioning"
Set-DefaultEnv "NOTIFICATION_DB_PASSWORD" "local-compose-notification"
Set-DefaultEnv "ADMIN_DB_PASSWORD" "local-compose-admin"
Set-DefaultEnv "ADMIN_MIGRATOR_DB_PASSWORD" "local-compose-admin-migrator"
Set-DefaultEnv "ACCESS_CREDENTIAL_KEY_BASE64" "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="
Set-DefaultEnv "ACCESS_TOKEN_HMAC_KEY_BASE64" "ZmVkY2JhOTg3NjU0MzIxMGZlZGNiYTk4NzY1NDMyMTA="
Set-DefaultEnv "SUBSCRIPTION_PUBLIC_BASE_URL" "https://127.0.0.1:8087"
Set-DefaultEnv "TELEGRAM_WEBHOOK_SECRET" "local-compose-webhook-secret"
Set-DefaultEnv "TELEGRAM_BOT_TOKEN" "local-compose-fake-bot-token"
Set-DefaultEnv "FAKE_TELEGRAM_SEND_DELAY" "1s"
Set-DefaultEnv "TERMS_URL" "https://example.invalid/terms/terms-v1"
Set-DefaultEnv "YOOKASSA_SHOP_ID" "test-shop"
Set-DefaultEnv "YOOKASSA_SECRET_KEY" "local-compose-yookassa-key"
Set-DefaultEnv "PAYMENT_RETURN_URL" "https://example.invalid/payment-return"
Set-DefaultEnv "COMPOSE_PARALLEL_LIMIT" "2"
Set-DefaultEnv "COMPOSE_BAKE" "false"
Set-DefaultEnv "GOCACHE" (Join-Path $cacheRoot "cache")
Set-DefaultEnv "GOMODCACHE" (Join-Path $cacheRoot "mod")
Set-DefaultEnv "GOTMPDIR" (Join-Path $cacheRoot "tmp")
Set-DefaultEnv "TEMP" $fallbackTemp
Set-DefaultEnv "TMP" $fallbackTemp
$fullVPN = [Environment]::GetEnvironmentVariable("VPN_SMOKE_FULL_CONTROL_PLANE") -eq "1"
$testVPNFailover = [Environment]::GetEnvironmentVariable("VPN_SMOKE_TEST_FAILOVER") -eq "1"
$stage7Extended = [Environment]::GetEnvironmentVariable("STAGE7_EXTENDED_SMOKE") -eq "1"
$observability = [Environment]::GetEnvironmentVariable("OBSERVABILITY_SMOKE") -eq "1"
if ($stage7Extended -and !$fullVPN) { throw "Stage 7 extended smoke requires the full VPN control plane" }
if ($testVPNFailover -and !$fullVPN) { throw "VPN failover smoke requires the full VPN control plane" }
$profiles = @("--profile", "core", "--profile", "app")
if ($fullVPN) { $profiles += @("--profile", "vpn") }
if ($observability) { $profiles += @("--profile", "obs") }
$profileNames = @("core", "app")
if ($fullVPN) { $profileNames += "vpn" }
if ($observability) { $profileNames += "obs" }
[Environment]::SetEnvironmentVariable("COMPOSE_PROFILES", ($profileNames -join ","), "Process")
if ($observability) {
    $prometheusConfig = if ($fullVPN) { "./deploy/observability/prometheus/prometheus-vpn.yml" } else { "./deploy/observability/prometheus/prometheus.yml" }
    [Environment]::SetEnvironmentVariable("PROMETHEUS_CONFIG_FILE", $prometheusConfig, "Process")
}
$vpnClientName = "$composeProject-stage6-client"
$vpnClientImage = "ghcr.io/xtls/xray-core:26.3.27@sha256:592ec4d11f656db95598d01e76dbcc6e002d67360b96a5436500a938230f52c7"

$goImage = "golang:1.26.5-alpine@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2"
$vpnClientConfig = Join-Path $smokeTemp "$composeProject-stage6-client.json"

function Invoke-MTLSProbe([string[]]$arguments) {
    $mappedArguments = foreach ($argument in $arguments) {
        $argument.Replace("https://127.0.0.1:8080", "https://identity-service.local:8080").Replace("https://127.0.0.1:8084", "https://billing-service.local:8084").Replace("https://127.0.0.1:8086", "https://subscription-service.local:8086").Replace("https://127.0.0.1:8087", "https://access-service.local:8087").Replace("https://127.0.0.1:18443", "https://node-agent-primary.local:18443")
    }
    $result = & docker run --rm `
        --add-host "identity-service.local:host-gateway" `
        --add-host "billing-service.local:host-gateway" `
        --add-host "subscription-service.local:host-gateway" `
        --add-host "access-service.local:host-gateway" `
        --add-host "node-agent-primary.local:host-gateway" `
        -v "$($repo):/src" `
        -v "$($goModCacheVolume):/go/pkg/mod" `
        -v "$($goBuildCacheVolume):/root/.cache/go-build" `
        -w /src `
        $goImage `
        go run ./tools/mtlsprobe/cmd/mtlsprobe @mappedArguments
    if ($LASTEXITCODE -ne 0) {
        throw "containerized mTLS probe failed"
    }
    return $result
}

function Invoke-GoIntegrationTest([hashtable]$environment, [string]$package) {
    $environmentArguments = @()
    foreach ($name in $environment.Keys) {
        $environmentArguments += @("-e", "$name=$($environment[$name])")
    }
    & docker run --rm `
        --network $backendNetwork `
        -v "$($repo):/src" `
        -v "$($goModCacheVolume):/go/pkg/mod" `
        -v "$($goBuildCacheVolume):/root/.cache/go-build" `
        -w /src `
        @environmentArguments `
        $goImage `
        go test -mod=readonly $package -run "^TestIntegration" -count=1
    if ($LASTEXITCODE -ne 0) {
        throw "containerized integration test failed: $package"
    }
}

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

function Invoke-PrometheusScalar([string]$promQL) {
    $query = [uri]::EscapeDataString($promQL)
    $result = Invoke-RestMethod -Uri "http://127.0.0.1:9090/api/v1/query?query=$query" -TimeoutSec 10
    if ($result.status -ne "success") {
        throw "Prometheus query failed"
    }
    if (@($result.data.result).Count -eq 0) {
        return 0
    }
    return [int][double]$result.data.result[0].value[1]
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
    foreach ($attempt in 1..300) {
        $exists = docker compose exec -T -e "REDISCLI_AUTH=$env:REDIS_PASSWORD" redis redis-cli --raw EXISTS $key
        if ("$exists".Trim() -eq "1") {
            return
        }
        Start-Sleep -Milliseconds 100
    }
    throw "processing dedupe key was not observed for update_id=$updateID"
}

try {
	New-Item -ItemType Directory -Force -Path $env:GOCACHE, $env:GOTMPDIR, $env:TEMP | Out-Null
    & powershell -NoProfile -ExecutionPolicy Bypass -File scripts/dev-mtls.ps1
    if ($LASTEXITCODE -ne 0) {
        exit $LASTEXITCODE
    }
	if ($fullVPN) {
		& powershell -NoProfile -ExecutionPolicy Bypass -File scripts/dev-xray.ps1
		if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
	}

    docker compose @profiles down -v --remove-orphans
    if ($LASTEXITCODE -ne 0) {
        exit $LASTEXITCODE
    }

    $buildServices = @("mtls-credentials-init", "identity-migrate", "identity-service", "catalog-service", "billing-service", "subscription-service", "access-service", "provisioning-migrate", "notification-service", "admin-service", "yookassa-api", "telegram-api", "telegram-bot")
	if ($fullVPN) { $buildServices += @("provisioning-service", "node-agent-primary") }
    if ($observability) { $buildServices += @("prometheus", "grafana", "otel-collector", "tempo", "loki") }
    foreach ($service in $buildServices) {
        docker compose @profiles build $service
        if ($LASTEXITCODE -ne 0) {
            exit $LASTEXITCODE
        }
    }

    docker compose @profiles up -d --no-build
    if ($LASTEXITCODE -ne 0) {
        exit $LASTEXITCODE
    }

    $containers = @(
        "postgres",
        "redis",
        "kafka",
        "identity-service",
        "catalog-service",
        "yookassa-api",
        "billing-service",
        "subscription-service",
        "access-service",
        "notification-service",
        "admin-service",
        "telegram-api",
        "telegram-bot"
    ) | ForEach-Object { Get-ComposeContainerName $_ }
	if ($fullVPN) {
		$containers += @("provisioning-service", "node-agent-primary", "node-agent-failover") | ForEach-Object { Get-ComposeContainerName $_ }
	}
    if ($observability) {
        $containers += @("prometheus", "grafana", "otel-collector", "tempo", "loki") | ForEach-Object { Get-ComposeContainerName $_ }
    }
    foreach ($attempt in 1..60) {
        $statuses = @()
        foreach ($container in $containers) {
            $statuses += docker inspect -f "{{.State.Health.Status}}" $container
        }
        if (($statuses | Where-Object { $_ -ne "healthy" }).Count -eq 0) {
            break
        }
        if ($attempt -eq 60) {
            docker compose @profiles ps
            throw "Compose services did not become healthy: $($statuses -join ', ')"
        }
        Start-Sleep -Seconds 2
    }

    if ($observability) {
        $expectedTargets = if ($fullVPN) { 11 } else { 8 }
        $healthyTargets = 0
        $query = [uri]::EscapeDataString('sum(up{job=~"vpn-control-plane|vpn-provisioning|vpn-nodes"} == 1)')
        foreach ($attempt in 1..30) {
            $result = Invoke-RestMethod -Uri "http://127.0.0.1:9090/api/v1/query?query=$query" -TimeoutSec 10
            if ($result.status -eq "success" -and $result.data.result.Count -eq 1) {
                $healthyTargets = [int][double]$result.data.result[0].value[1]
            }
            if ($healthyTargets -eq $expectedTargets) {
                break
            }
            Start-Sleep -Seconds 2
        }
        if ($healthyTargets -ne $expectedTargets) {
            throw "Prometheus has $healthyTargets healthy VPN targets, expected $expectedTargets"
        }
        $expectedPools = if ($fullVPN) { 8 } else { 7 }
        $expectedMessageSnapshots = if ($fullVPN) { 5 } else { 4 }
        $expectedDomainSnapshots = if ($fullVPN) { 4 } else { 3 }
        foreach ($attempt in 1..30) {
            $poolCount = Invoke-PrometheusScalar 'count(vpn_platform_postgres_pool_connections{state="max"})'
            $messageSnapshots = Invoke-PrometheusScalar 'sum(vpn_platform_message_snapshot_success == 1)'
            $domainSnapshots = Invoke-PrometheusScalar 'sum(vpn_platform_domain_snapshot_success == 1)'
            $billingSnapshots = Invoke-PrometheusScalar 'sum(vpn_billing_metrics_snapshot_success == 1)'
            $subscriptionSnapshots = Invoke-PrometheusScalar 'sum(vpn_subscription_metrics_snapshot_success == 1)'
            $provisioningSnapshots = if ($fullVPN) { Invoke-PrometheusScalar 'sum(vpn_provisioning_metrics_snapshot_success == 1)' } else { 0 }
            $healthyXray = if ($fullVPN) { Invoke-PrometheusScalar 'sum(vpn_node_xray_healthy == 1)' } else { 0 }
            if ($poolCount -eq $expectedPools -and $messageSnapshots -eq $expectedMessageSnapshots -and $domainSnapshots -eq $expectedDomainSnapshots -and $billingSnapshots -eq 1 -and $subscriptionSnapshots -eq 1 -and (!$fullVPN -or ($provisioningSnapshots -eq 1 -and $healthyXray -eq 2))) {
                break
            }
            Start-Sleep -Seconds 2
        }
        if ($poolCount -ne $expectedPools -or $messageSnapshots -ne $expectedMessageSnapshots -or $domainSnapshots -ne $expectedDomainSnapshots -or $billingSnapshots -ne 1 -or $subscriptionSnapshots -ne 1 -or ($fullVPN -and ($provisioningSnapshots -ne 1 -or $healthyXray -ne 2))) {
            throw "Operational metrics incomplete: pools=$poolCount/$expectedPools message_snapshots=$messageSnapshots/$expectedMessageSnapshots domain_snapshots=$domainSnapshots/$expectedDomainSnapshots billing=$billingSnapshots subscription=$subscriptionSnapshots provisioning=$provisioningSnapshots xray=$healthyXray"
        }
        $grafanaHealth = Invoke-RestMethod -Uri "http://127.0.0.1:3000/api/health" -TimeoutSec 10
        if ($grafanaHealth.database -ne "ok") {
            throw "Grafana database is not healthy"
        }
        $dashboards = Invoke-RestMethod -Uri "http://127.0.0.1:3000/api/search?query=VPN%20Platform%20Overview" -TimeoutSec 10
        if (@($dashboards | Where-Object { $_.uid -eq "vpn-platform-overview" }).Count -ne 1) {
            throw "provisioned VPN Platform dashboard was not found"
        }
        foreach ($dataSourceUID in @("tempo", "loki")) {
            $dataSource = Invoke-RestMethod -Uri "http://127.0.0.1:3000/api/datasources/uid/$dataSourceUID" -TimeoutSec 10
            if ($dataSource.uid -ne $dataSourceUID) {
                throw "provisioned $dataSourceUID data source was not found"
            }
        }

        $traceID = "4bf92f3577b34da6a3ce929d0e0e4736"
        $traceParent = "00-$traceID-00f067aa0ba902b7-01"
        $privacySentinel = "stage8-private-url-value"
        Invoke-MTLSProbe @(
            "GET",
            "https://127.0.0.1:8080/version?probe=$privacySentinel",
            "secrets/dev-mtls/identity-health.crt",
            "secrets/dev-mtls/identity-health.key",
            "secrets/dev-mtls/ca.crt",
            "200",
            "traceparent",
            $traceParent
        ) | Out-Null

        $tempoJSON = ""
        $lokiJSON = ""
        $lokiQuery = [uri]::EscapeDataString('{service_name="identity-service"} |= "http request"')
        foreach ($attempt in 1..30) {
            try {
                $tempoResponse = Invoke-RestMethod -Uri "http://127.0.0.1:3000/api/datasources/proxy/uid/tempo/api/traces/$traceID" -TimeoutSec 10
                $tempoJSON = $tempoResponse | ConvertTo-Json -Compress -Depth 30
            } catch {
                $tempoJSON = ""
            }
            try {
                $lokiResponse = Invoke-RestMethod -Uri "http://127.0.0.1:3000/api/datasources/proxy/uid/loki/loki/api/v1/query_range?query=$lokiQuery&limit=100&direction=backward" -TimeoutSec 10
                $lokiJSON = $lokiResponse | ConvertTo-Json -Compress -Depth 30
            } catch {
                $lokiJSON = ""
            }
            if ($tempoJSON.Contains("GET /version") -and $lokiJSON.Contains($traceID)) {
                break
            }
            Start-Sleep -Seconds 2
        }
        if (!$tempoJSON.Contains("GET /version")) {
            throw "known W3C trace did not reach Tempo"
        }
        if (!$lokiJSON.Contains($traceID) -or !$lokiJSON.Contains("http request")) {
            throw "trace-correlated structured log did not reach Loki"
        }
        foreach ($telemetryJSON in @($tempoJSON, $lokiJSON)) {
            if ($telemetryJSON.Contains($privacySentinel) -or $telemetryJSON.Contains("/version?probe=")) {
                throw "telemetry backend leaked raw URL data"
            }
        }
    }

    if (-not $fullVPN) {
    $integrationContainers = @("admin-service", "notification-service", "telegram-bot", "access-service", "subscription-service", "billing-service") |
        ForEach-Object { Get-ComposeContainerName $_ }
    docker stop @integrationContainers | Out-Null
    if ($LASTEXITCODE -ne 0) {
        exit $LASTEXITCODE
    }
    Invoke-GoIntegrationTest @{
        BILLING_TEST_DATABASE_URL = "postgres://billing_app:$($env:BILLING_DB_PASSWORD)@postgres:5432/billing_service?sslmode=disable"
    } "./services/billing/internal/postgres"
    Invoke-GoIntegrationTest @{
        SUBSCRIPTION_TEST_DATABASE_URL = "postgres://subscription_app:$($env:SUBSCRIPTION_DB_PASSWORD)@postgres:5432/subscription_service?sslmode=disable"
    } "./services/subscription/internal/postgres"
    Invoke-GoIntegrationTest @{
        ACCESS_TEST_DATABASE_URL = "postgres://access_app:$($env:ACCESS_DB_PASSWORD)@postgres:5432/access_service?sslmode=disable"
    } "./services/access/internal/postgres"
    docker compose @profiles run --rm provisioning-migrate
    if ($LASTEXITCODE -ne 0) {
        exit $LASTEXITCODE
    }
    Invoke-GoIntegrationTest @{
        PROVISIONING_TEST_DATABASE_URL = "postgres://provisioning_app:$($env:PROVISIONING_DB_PASSWORD)@postgres:5432/provisioning_service?sslmode=disable"
    } "./services/provisioning/internal/postgres"
    Invoke-GoIntegrationTest @{
        ACCESS_TEST_REDIS_ADDR = "redis:6379"
        ACCESS_TEST_REDIS_PASSWORD = $env:REDIS_PASSWORD
    } "./services/access/internal/ratelimit"
    Invoke-GoIntegrationTest @{
        NOTIFICATION_TEST_DATABASE_URL = "postgres://notification_app:$($env:NOTIFICATION_DB_PASSWORD)@postgres:5432/notification_service?sslmode=disable"
    } "./services/notification/internal/postgres"
    Invoke-GoIntegrationTest @{
        ADMIN_TEST_DATABASE_URL = "postgres://admin_app:$($env:ADMIN_DB_PASSWORD)@postgres:5432/admin_service?sslmode=disable"
        ADMIN_MIGRATOR_TEST_DATABASE_URL = "postgres://admin_migrator:$($env:ADMIN_MIGRATOR_DB_PASSWORD)@postgres:5432/admin_service?sslmode=disable"
    } "./services/admin/internal/postgres"
    [array]::Reverse($integrationContainers)
    docker start @integrationContainers | Out-Null
    if ($LASTEXITCODE -ne 0) {
        exit $LASTEXITCODE
    }
    foreach ($attempt in 1..60) {
        $billingStatus = docker inspect -f "{{.State.Health.Status}}" (Get-ComposeContainerName "billing-service")
        $subscriptionStatus = docker inspect -f "{{.State.Health.Status}}" (Get-ComposeContainerName "subscription-service")
        $accessStatus = docker inspect -f "{{.State.Health.Status}}" (Get-ComposeContainerName "access-service")
        $botStatus = docker inspect -f "{{.State.Health.Status}}" (Get-ComposeContainerName "telegram-bot")
        $notificationStatus = docker inspect -f "{{.State.Health.Status}}" (Get-ComposeContainerName "notification-service")
        $adminStatus = docker inspect -f "{{.State.Health.Status}}" (Get-ComposeContainerName "admin-service")
        if ($billingStatus -eq "healthy" -and $subscriptionStatus -eq "healthy" -and $accessStatus -eq "healthy" -and $botStatus -eq "healthy" -and $notificationStatus -eq "healthy" -and $adminStatus -eq "healthy") {
            break
        }
        if ($attempt -eq 60) {
            throw "Services did not recover after integration tests: billing=$billingStatus subscription=$subscriptionStatus access=$accessStatus bot=$botStatus notification=$notificationStatus admin=$adminStatus"
        }
        Start-Sleep -Seconds 2
    }
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
    $concurrentBuyBody = '{"update_id":2004,"message":{"message_id":4,"text":"/buy","chat":{"id":9001},"from":{"id":4200001,"first_name":"Smoke","language_code":"en"}}}'
    $buyJob = Start-Job -ScriptBlock {
        param([string]$secret, [string]$body)
        $responsePath = Join-Path $env:TEMP "vpn-platform-buy-first-response.json"
        $requestPath = Join-Path $env:TEMP "vpn-platform-buy-first-request.json"
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
    } -ArgumentList $env:TELEGRAM_WEBHOOK_SECRET, $buyBody
    $concurrentBuyStatus = Invoke-WebhookStatus $concurrentBuyBody
    $buyStatus = Receive-Job -Job $buyJob -Wait
    Remove-Job -Job $buyJob
    if ([int]$buyStatus -ne 200 -or $concurrentBuyStatus -ne 200) {
        throw "concurrent buy statuses: first=$buyStatus second=$concurrentBuyStatus, want 200/200"
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

    foreach ($attempt in 1..80) {
        $subscriptionStatus = Invoke-ScalarSQL "subscription_service" "SELECT status FROM subscriptions LIMIT 1"
        $subscriptionPeriodCount = Invoke-ScalarSQL "subscription_service" "SELECT count(*) FROM subscription_periods"
        $subscriptionInboxCount = Invoke-ScalarSQL "subscription_service" "SELECT count(*) FROM inbox WHERE event_type='billing.payment.succeeded.v1' AND state='processed'"
        $activationPublishedCount = Invoke-ScalarSQL "subscription_service" "SELECT count(*) FROM outbox WHERE topic='subscription.activated.v1' AND state='published'"
        if ($subscriptionStatus -eq "active" -and $subscriptionPeriodCount -eq "1" -and $subscriptionInboxCount -eq "1" -and $activationPublishedCount -eq "1") {
            break
        }
        Start-Sleep -Milliseconds 250
    }
    if ($subscriptionStatus -ne "active" -or $subscriptionPeriodCount -ne "1" -or $subscriptionInboxCount -ne "1" -or $activationPublishedCount -ne "1") {
        throw "subscription activation did not converge exactly once: status=$subscriptionStatus periods=$subscriptionPeriodCount inbox=$subscriptionInboxCount activation=$activationPublishedCount"
    }
    $subscriptionUserID = Invoke-ScalarSQL "subscription_service" "SELECT user_id FROM subscriptions LIMIT 1"
    $subscriptionID = Invoke-ScalarSQL "subscription_service" "SELECT id FROM subscriptions LIMIT 1"

    $accessCredentialID = ""
    foreach ($attempt in 1..80) {
        $accessCredentialID = Invoke-ScalarSQL "access_service" "SELECT COALESCE((SELECT id::text FROM access_credentials WHERE subscription_id='$subscriptionID' LIMIT 1),'')"
        $accessProvisionPublished = Invoke-ScalarSQL "access_service" "SELECT count(*) FROM outbox WHERE topic='access.provision.request.v1' AND state='published' AND aggregate_id IN (SELECT id FROM access_credentials WHERE subscription_id='$subscriptionID')"
        if (![string]::IsNullOrWhiteSpace($accessCredentialID) -and $accessProvisionPublished -eq "1") {
            break
        }
        Start-Sleep -Milliseconds 250
    }
    if ([string]::IsNullOrWhiteSpace($accessCredentialID) -or $accessProvisionPublished -ne "1") {
        throw "access provisioning request was not created exactly once"
    }
    if ($fullVPN) {
        foreach ($attempt in 1..120) {
            $accessCredentialStatus = Invoke-ScalarSQL "access_service" "SELECT status FROM access_credentials WHERE id='$accessCredentialID'"
            $provisionOutcomePublished = Invoke-ScalarSQL "provisioning_service" "SELECT count(*) FROM outbox WHERE topic='access.provision.succeeded.v1' AND state='published' AND aggregate_id='$accessCredentialID'"
            $accessReadyPublished = Invoke-ScalarSQL "access_service" "SELECT count(*) FROM outbox WHERE topic='access.ready.v1' AND state='published'"
            if (($accessCredentialStatus -eq "active" -or $accessCredentialStatus -eq "degraded") -and $provisionOutcomePublished -eq "1" -and $accessReadyPublished -eq "1") { break }
            Start-Sleep -Milliseconds 500
        }
        $assignedNodes = Invoke-ScalarSQL "access_service" "SELECT count(*) FROM access_assignment_snapshots WHERE credential_id='$accessCredentialID' AND allocation_revision=1"
        $provisioningAllocations = Invoke-ScalarSQL "provisioning_service" "SELECT count(*) FROM allocations WHERE credential_id='$accessCredentialID' AND state='active'"
        if ($accessCredentialStatus -ne "active" -or $provisionOutcomePublished -ne "1" -or $accessReadyPublished -ne "1" -or $assignedNodes -ne "2" -or $provisioningAllocations -ne "2") {
            docker compose logs --tail=100 provisioning-service access-service node-agent-primary node-agent-failover
            throw "full provisioning did not converge: credential=$accessCredentialStatus outcome=$provisionOutcomePublished ready=$accessReadyPublished assigned=$assignedNodes allocations=$provisioningAllocations"
        }
        $materialAuditCount = Invoke-ScalarSQL "access_service" "SELECT count(*) FROM security_audit_events WHERE credential_id='$accessCredentialID' AND actor_service='provisioning-service' AND action='credential_material.read' AND outcome='succeeded'"
        if ([int]$materialAuditCount -lt 1) { throw "full provisioning material read was not audited" }
    } else {
    $accessOperationID = Invoke-ScalarSQL "access_service" "SELECT id FROM access_operations WHERE kind='provision' LIMIT 1"
    $accessCommandEventID = Invoke-ScalarSQL "access_service" "SELECT event_id FROM outbox WHERE topic='access.provision.request.v1' LIMIT 1"
    $provisionResult = @{
        event_id = "51000000-0000-4000-8000-000000000001"
        event_type = "access.provision.succeeded.v1"
        schema_version = 1
        occurred_at = "2026-07-18T12:00:05Z"
        producer = "provisioning-service"
        correlation_id = "51000000-0000-4000-8000-000000000002"
        causation_id = $accessCommandEventID
        aggregate_type = "credential"
        aggregate_id = $accessCredentialID
        aggregate_sequence = 1
        partition_key = "credential:$accessCredentialID"
        data = @{
            operation_id = $accessOperationID
            credential_id = $accessCredentialID
            applied_revision = 1
            status = "active"
            assigned_node_ids = @("51000000-0000-4000-8000-000000000003", "51000000-0000-4000-8000-000000000004")
            endpoints = @(
                @{ node_id = "51000000-0000-4000-8000-000000000003"; role = "primary"; address = "vpn.example.invalid"; port = 443; server_name = "cdn.example.invalid"; reality_public_key = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"; short_id = "0011aabb"; spider_x = "/"; label = "VPN Primary" },
                @{ node_id = "51000000-0000-4000-8000-000000000004"; role = "failover"; address = "backup.example.invalid"; port = 443; server_name = "www.example.invalid"; reality_public_key = "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"; short_id = "2233ccdd"; label = "VPN Failover" }
            )
            applied_at = "2026-07-18T12:00:05Z"
        }
    } | ConvertTo-Json -Compress -Depth 8
    $kafkaRecord = "credential:$accessCredentialID|$provisionResult`n"
    $kafkaRecordBase64 = [Convert]::ToBase64String([System.Text.UTF8Encoding]::new($false).GetBytes($kafkaRecord))
    docker compose exec -T -e "KAFKA_RECORD_BASE64=$kafkaRecordBase64" kafka sh -c 'printf %s $KAFKA_RECORD_BASE64 | base64 -d | /opt/kafka/bin/kafka-console-producer.sh --bootstrap-server localhost:9092 --topic access.provision.succeeded.v1 --reader-property parse.key=true --reader-property key.separator=\|'
    if ($LASTEXITCODE -ne 0) {
        exit $LASTEXITCODE
    }
    foreach ($attempt in 1..80) {
        $accessCredentialStatus = Invoke-ScalarSQL "access_service" "SELECT status FROM access_credentials WHERE id='$accessCredentialID'"
        $accessReadyPublished = Invoke-ScalarSQL "access_service" "SELECT count(*) FROM outbox WHERE topic='access.ready.v1' AND state='published'"
        if ($accessCredentialStatus -eq "active" -and $accessReadyPublished -eq "1") {
            break
        }
        Start-Sleep -Milliseconds 250
    }
    if ($accessCredentialStatus -ne "active" -or $accessReadyPublished -ne "1") {
        $accessProvisionInbox = Invoke-ScalarSQL "access_service" "SELECT count(*) FROM inbox WHERE event_type='access.provision.succeeded.v1'"
        $accessDeadLetterReasons = Invoke-ScalarSQL "access_service" "SELECT COALESCE(string_agg(reason_code,','),'') FROM consumer_dead_letters WHERE topic='access.provision.succeeded.v1'"
        $accessOperationStatus = Invoke-ScalarSQL "access_service" "SELECT status FROM access_operations WHERE id='$accessOperationID'"
        docker compose logs --tail=80 access-service
        throw "access provisioning result did not converge: credential=$accessCredentialStatus ready=$accessReadyPublished inbox=$accessProvisionInbox operation=$accessOperationStatus dead_letters=$accessDeadLetterReasons"
    }
    }

    $issueJSON = Invoke-MTLSProbe @("POST", "https://127.0.0.1:8087/internal/v1/subscriptions/$subscriptionID/subscription-url/issue", "secrets/dev-mtls/telegram-bot.crt", "secrets/dev-mtls/telegram-bot.key", "secrets/dev-mtls/ca.crt", "200", "Idempotency-Key", "smoke-issue-0001", "print-body")
    $issuedURL = ($issueJSON | ConvertFrom-Json).subscription_url
    if ([string]::IsNullOrWhiteSpace($issuedURL)) {
        throw "access issue endpoint did not return a subscription URL"
    }
    Invoke-MTLSProbe @("POST", "https://127.0.0.1:8087/internal/v1/subscriptions/$subscriptionID/subscription-url/issue", "secrets/dev-mtls/telegram-bot.crt", "secrets/dev-mtls/telegram-bot.key", "secrets/dev-mtls/ca.crt", "409", "Idempotency-Key", "smoke-issue-0001") | Out-Null
    $profileHeaders = Join-Path $smokeTemp "stage5-profile-headers.txt"
    $profileBody = Join-Path $smokeTemp "stage5-profile-body.txt"
    $profileStatus = & curl.exe -sS -D $profileHeaders -o $profileBody -w "%{http_code}" --cacert "secrets/dev-mtls/ca.crt" --ssl-no-revoke $issuedURL
    if ($profileStatus -ne "200" -or !(Select-String -Path $profileHeaders -Pattern '^Cache-Control: no-store' -Quiet) -or !(Select-String -Path $profileHeaders -Pattern '^profile-title: VPN Platform' -Quiet) -or !(Select-String -Path $profileBody -Pattern '^vless://' -Quiet)) {
        throw "Happ subscription response was not compatible or no-store"
    }
	$vpnCredentialUUID = ""
	if ($fullVPN) {
		$profileText = Get-Content -Raw $profileBody
		if ($profileText -notmatch 'vless://([0-9a-fA-F-]{36})@') { throw "Happ profile did not contain a VLESS credential" }
		$vpnCredentialUUID = $Matches[1]
	}
    $issuedToken = $issuedURL.Substring($issuedURL.LastIndexOf('/') + 1)
    $accessLogs = docker compose logs access-service
    if ("$accessLogs".Contains($issuedToken)) {
        throw "subscription token leaked into access-service logs"
    }
    Remove-Item $profileHeaders, $profileBody -Force

    $subscriptionBase = $issuedURL.Substring(0, $issuedURL.LastIndexOf('/'))
    foreach ($malformedPath in @("$subscriptionBase", "$subscriptionBase/", "$subscriptionBase/a/b")) {
        $malformedHeaders = Join-Path $smokeTemp "stage5-malformed-headers.txt"
        $malformedBody = Join-Path $smokeTemp "stage5-malformed-body.txt"
        $malformedStatus = & curl.exe -sS -D $malformedHeaders -o $malformedBody -w "%{http_code}" --cacert "secrets/dev-mtls/ca.crt" --ssl-no-revoke $malformedPath
        if ($malformedStatus -ne "404" -or !(Select-String -Path $malformedHeaders -Pattern '^Cache-Control: no-store' -Quiet) -or (Get-Content -Raw $malformedBody) -ne "subscription unavailable`n") {
            throw "malformed subscription path did not use the generic no-store 404"
        }
        Remove-Item $malformedHeaders, $malformedBody -Force
    }

    if ($stage7Extended) {
        & powershell -NoProfile -ExecutionPolicy Bypass -File scripts/stage7-e2e.ps1 -Phase BeforeRevoke -UserID $subscriptionUserID -SubscriptionID $subscriptionID -CredentialID $accessCredentialID
        if ($LASTEXITCODE -ne 0) {
            $stage7ExitCode = $LASTEXITCODE
            Write-Host "Stage 7 notification job counts (type|status|count):"
            docker compose exec -T postgres psql --username="$env:POSTGRES_USER" --dbname notification_service -tAc "SELECT notification_type || '|' || status || '|' || count(*) FROM notification_jobs GROUP BY notification_type,status ORDER BY notification_type,status"
            Write-Host "Stage 7 notification inbox counts (event_type|count):"
            docker compose exec -T postgres psql --username="$env:POSTGRES_USER" --dbname notification_service -tAc "SELECT event_type || '|' || count(*) FROM notification_inbox GROUP BY event_type ORDER BY event_type"
            Write-Host "Stage 7 notification DLQ counts (reason|count):"
            docker compose exec -T postgres psql --username="$env:POSTGRES_USER" --dbname notification_service -tAc "SELECT reason_code || '|' || count(*) FROM notification_dead_letters GROUP BY reason_code ORDER BY reason_code"
            docker compose logs --tail=120 notification-service telegram-bot
            exit $stage7ExitCode
        }
    }

    if ($fullVPN) {
        $clientConfig = Get-Content -Raw "secrets/dev-xray/smoke-client.json" | ConvertFrom-Json
        $clientConfig.outbounds[0].settings.id = $vpnCredentialUUID
        [System.IO.File]::WriteAllText($vpnClientConfig, ($clientConfig | ConvertTo-Json -Depth 12), [System.Text.UTF8Encoding]::new($false))
        docker run --rm -v "$($vpnClientConfig):/etc/xray/client.json:ro" $vpnClientImage run -test -config /etc/xray/client.json | Out-Null
        if ($LASTEXITCODE -ne 0) { throw "generated Xray client configuration is invalid" }
        docker run -d --name $vpnClientName --network $vpnDataNetwork -p "127.0.0.1:11080:1080" -v "$($vpnClientConfig):/etc/xray/client.json:ro" $vpnClientImage run -config /etc/xray/client.json | Out-Null
        if ($LASTEXITCODE -ne 0) { throw "start Stage 6 Xray client failed" }
        $vpnBody = ""
        foreach ($attempt in 1..40) {
            $vpnBody = & curl.exe -sS --socks5-hostname 127.0.0.1:11080 --connect-timeout 2 --max-time 5 http://camouflage.local/ 2>$null
            if ($LASTEXITCODE -eq 0 -and "$vpnBody" -match "local camouflage endpoint") { break }
            Start-Sleep -Milliseconds 500
        }
        if ("$vpnBody" -notmatch "local camouflage endpoint") { throw "full-control-plane VLESS + REALITY request failed" }

        if ($testVPNFailover) {
            docker compose stop node-agent-primary | Out-Null
            if ($LASTEXITCODE -ne 0) { throw "stop primary node fault injection failed" }
            docker rm -f $vpnClientName | Out-Null
            if ($LASTEXITCODE -ne 0) { throw "stop primary-route Xray client failed" }

            $clientConfig = Get-Content -Raw "secrets/dev-xray/smoke-client-failover.json" | ConvertFrom-Json
            $clientConfig.outbounds[0].settings.id = $vpnCredentialUUID
            [System.IO.File]::WriteAllText($vpnClientConfig, ($clientConfig | ConvertTo-Json -Depth 12), [System.Text.UTF8Encoding]::new($false))
            docker run --rm -v "$($vpnClientConfig):/etc/xray/client.json:ro" $vpnClientImage run -test -config /etc/xray/client.json | Out-Null
            if ($LASTEXITCODE -ne 0) { throw "generated failover Xray client configuration is invalid" }
            docker run -d --name $vpnClientName --network $vpnDataNetwork -p "127.0.0.1:11080:1080" -v "$($vpnClientConfig):/etc/xray/client.json:ro" $vpnClientImage run -config /etc/xray/client.json | Out-Null
            if ($LASTEXITCODE -ne 0) { throw "start failover Xray client failed" }

            $failoverBody = ""
            foreach ($attempt in 1..40) {
                $failoverBody = & curl.exe -sS --socks5-hostname 127.0.0.1:11080 --connect-timeout 2 --max-time 5 http://camouflage.local/ 2>$null
                if ($LASTEXITCODE -eq 0 -and "$failoverBody" -match "local camouflage endpoint") { break }
                Start-Sleep -Milliseconds 500
            }
            if ("$failoverBody" -notmatch "local camouflage endpoint") { throw "VPN traffic did not survive primary node loss" }

            docker compose start node-agent-primary | Out-Null
            if ($LASTEXITCODE -ne 0) { throw "restart primary node fault injection failed" }
            $primaryRecovered = $false
            foreach ($attempt in 1..120) {
                $previousErrorActionPreference = $ErrorActionPreference
                try {
                    $ErrorActionPreference = "SilentlyContinue"
                    docker compose exec -T node-agent-primary /node-agent healthcheck *> $null
                    $healthcheckExitCode = $LASTEXITCODE
                } finally {
                    $ErrorActionPreference = $previousErrorActionPreference
                }
                if ($healthcheckExitCode -eq 0) {
                    $primaryRecovered = $true
                    break
                }
                Start-Sleep -Milliseconds 500
            }
            if (!$primaryRecovered) { throw "primary node did not recover after failover drill" }
        }

        Invoke-MTLSProbe @("GET", "https://127.0.0.1:18443/internal/v1/credentials/$accessCredentialID", "secrets/dev-mtls/identity-health.crt", "secrets/dev-mtls/identity-health.key", "secrets/dev-mtls/ca.crt", "403") | Out-Null

        $paymentID = Invoke-ScalarSQL "billing_service" "SELECT id FROM payments LIMIT 1"
        $orderID = Invoke-ScalarSQL "billing_service" "SELECT id FROM orders LIMIT 1"
        $amountMinor = [int64](Invoke-ScalarSQL "billing_service" "SELECT amount_minor FROM orders WHERE id='$orderID'")
        $currency = Invoke-ScalarSQL "billing_service" "SELECT currency FROM orders WHERE id='$orderID'"
        $refundedAt = (Get-Date).ToUniversalTime().ToString("o")
        $refundID = "52000000-0000-4000-8000-000000000001"
        $refundEvent = @{
            event_id = "52000000-0000-4000-8000-000000000002"; event_type = "billing.refund.succeeded.v1"; schema_version = 1
            occurred_at = $refundedAt; producer = "billing-service"; correlation_id = "52000000-0000-4000-8000-000000000003"; causation_id = $null
            aggregate_type = "refund"; aggregate_id = $refundID; partition_key = "user:$subscriptionUserID"
            data = @{ refund_id = $refundID; payment_id = $paymentID; order_id = $orderID; user_id = $subscriptionUserID; amount_minor = $amountMinor; currency = $currency; refund_scope = "full"; refunded_at = $refundedAt }
        } | ConvertTo-Json -Compress -Depth 6
        $refundRecord = "user:$subscriptionUserID|$refundEvent`n"
        $refundRecordBase64 = [Convert]::ToBase64String([System.Text.UTF8Encoding]::new($false).GetBytes($refundRecord))
        docker compose exec -T -e "KAFKA_RECORD_BASE64=$refundRecordBase64" kafka sh -c 'printf %s $KAFKA_RECORD_BASE64 | base64 -d | /opt/kafka/bin/kafka-console-producer.sh --bootstrap-server localhost:9092 --topic billing.refund.succeeded.v1 --reader-property parse.key=true --reader-property key.separator=\|'
        if ($LASTEXITCODE -ne 0) { throw "publish Stage 6 terminal fact failed" }

        foreach ($attempt in 1..180) {
            $subscriptionRevoked = Invoke-ScalarSQL "subscription_service" "SELECT count(*) FROM outbox WHERE topic='subscription.revoked.v1' AND state='published' AND aggregate_id='$subscriptionID'"
            $accessCredentialStatus = Invoke-ScalarSQL "access_service" "SELECT status FROM access_credentials WHERE id='$accessCredentialID'"
            $revokeOutcomePublished = Invoke-ScalarSQL "provisioning_service" "SELECT count(*) FROM outbox WHERE topic='access.revoke.succeeded.v1' AND state='published' AND aggregate_id='$accessCredentialID'"
            $revokedAllocations = Invoke-ScalarSQL "provisioning_service" "SELECT count(*) FROM allocations WHERE credential_id='$accessCredentialID' AND state='revoked'"
            if ($subscriptionRevoked -eq "1" -and $accessCredentialStatus -eq "revoked" -and $revokeOutcomePublished -eq "1" -and $revokedAllocations -eq "2") { break }
            Start-Sleep -Milliseconds 500
        }
        if ($subscriptionRevoked -ne "1" -or $accessCredentialStatus -ne "revoked" -or $revokeOutcomePublished -ne "1" -or $revokedAllocations -ne "2") {
            docker compose logs --tail=120 subscription-service access-service provisioning-service node-agent-primary node-agent-failover
            throw "full revoke did not converge: subscription=$subscriptionRevoked access=$accessCredentialStatus outcome=$revokeOutcomePublished allocations=$revokedAllocations"
        }
        $requestPreference = $ErrorActionPreference
        $ErrorActionPreference = "Continue"
        & curl.exe -sS --socks5-hostname 127.0.0.1:11080 --connect-timeout 3 --max-time 5 http://camouflage.local/ 2>$null | Out-Null
        $revokedExitCode = $LASTEXITCODE
        $ErrorActionPreference = $requestPreference
        if ($revokedExitCode -eq 0) { throw "revoked full-control-plane credential still passed traffic" }
        if ($stage7Extended) {
            & powershell -NoProfile -ExecutionPolicy Bypass -File scripts/stage7-e2e.ps1 -Phase AfterRevoke -UserID $subscriptionUserID -SubscriptionID $subscriptionID -CredentialID $accessCredentialID
            if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
        }
        Remove-Item $vpnClientConfig -Force
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

    Invoke-MTLSProbe @("PUT", "https://127.0.0.1:8080/internal/v1/telegram-users/999", "secrets/dev-mtls/identity-health.crt", "secrets/dev-mtls/identity-health.key", "secrets/dev-mtls/ca.crt", "403") | Out-Null
    Invoke-MTLSProbe @("POST", "https://127.0.0.1:8080/internal/v1/users/00000000-0000-4000-8000-000000000001/consents", "secrets/dev-mtls/billing-service.crt", "secrets/dev-mtls/billing-service.key", "secrets/dev-mtls/ca.crt", "403") | Out-Null
    Invoke-MTLSProbe @("POST", "https://127.0.0.1:8084/internal/v1/users/00000000-0000-4000-8000-000000000001/orders", "secrets/dev-mtls/billing-service.crt", "secrets/dev-mtls/billing-service.key", "secrets/dev-mtls/ca.crt", "403") | Out-Null
    Invoke-MTLSProbe @("GET", "https://127.0.0.1:8084/internal/v1/users/00000000-0000-4000-8000-000000000001/orders/00000000-0000-4000-8000-000000000002", "secrets/dev-mtls/identity-health.crt", "secrets/dev-mtls/identity-health.key", "secrets/dev-mtls/ca.crt", "403") | Out-Null
    Invoke-MTLSProbe @("GET", "https://127.0.0.1:8084/internal/v1/users/00000000-0000-4000-8000-000000000001/orders/00000000-0000-4000-8000-000000000002", "secrets/dev-mtls/subscription-service.crt", "secrets/dev-mtls/subscription-service.key", "secrets/dev-mtls/ca.crt", "404") | Out-Null
    Invoke-MTLSProbe @("GET", "https://127.0.0.1:8086/internal/v1/users/$subscriptionUserID/subscription", "secrets/dev-mtls/telegram-bot.crt", "secrets/dev-mtls/telegram-bot.key", "secrets/dev-mtls/ca.crt", "200") | Out-Null
    Invoke-MTLSProbe @("GET", "https://127.0.0.1:8086/internal/v1/users/$subscriptionUserID/subscription", "secrets/dev-mtls/identity-health.crt", "secrets/dev-mtls/identity-health.key", "secrets/dev-mtls/ca.crt", "403") | Out-Null
    if (-not $fullVPN) {
        Invoke-MTLSProbe @("GET", "https://127.0.0.1:8087/internal/v1/credentials/$accessCredentialID/provisioning-material", "secrets/dev-mtls/provisioning-service.crt", "secrets/dev-mtls/provisioning-service.key", "secrets/dev-mtls/ca.crt", "200") | Out-Null
        Invoke-MTLSProbe @("GET", "https://127.0.0.1:8087/internal/v1/credentials/$accessCredentialID/provisioning-material", "secrets/dev-mtls/identity-health.crt", "secrets/dev-mtls/identity-health.key", "secrets/dev-mtls/ca.crt", "403") | Out-Null
        $materialAuditCount = Invoke-ScalarSQL "access_service" "SELECT count(*) FROM security_audit_events WHERE credential_id='$accessCredentialID' AND actor_service='provisioning-service' AND action='credential_material.read' AND outcome='succeeded'"
        if ($materialAuditCount -ne "1") { throw "provisioning material read was not audited exactly once" }
    }
    if ($observability) {
        $series = Invoke-RestMethod -Uri 'http://127.0.0.1:9090/api/v1/series?match%5B%5D=%7B__name__%3D~%22vpn_.%2A%22%7D' -TimeoutSec 10
        $seriesJSON = $series | ConvertTo-Json -Compress -Depth 10
        foreach ($forbiddenMetricValue in @($subscriptionUserID, $subscriptionID, $accessCredentialID, $issuedToken, $vpnCredentialUUID)) {
            if (![string]::IsNullOrWhiteSpace($forbiddenMetricValue) -and $seriesJSON.Contains($forbiddenMetricValue)) {
                throw "Prometheus series metadata leaked a runtime identifier or VPN secret"
            }
        }
    }
} finally {
    $cleanupPreference = $ErrorActionPreference
    $ErrorActionPreference = "SilentlyContinue"
    docker rm -f $vpnClientName 2>$null | Out-Null
    if (Test-Path $vpnClientConfig) { Remove-Item $vpnClientConfig -Force }
    if ([Environment]::GetEnvironmentVariable("SMOKE_KEEP_STACK") -eq "1") {
        Write-Warning "SMOKE_KEEP_STACK=1: Compose services and volumes were preserved for diagnostics."
    } else {
        docker compose @profiles down -v --remove-orphans
        if ([Environment]::GetEnvironmentVariable("SMOKE_DISPOSABLE_PROJECT") -eq "1") {
            docker volume rm $goModCacheVolume $goBuildCacheVolume 2>$null | Out-Null
        }
    }
    $ErrorActionPreference = $cleanupPreference
}

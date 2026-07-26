$ErrorActionPreference = "Stop"

$repo = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$repoTmp = Join-Path $repo "tmp"
New-Item -ItemType Directory -Force -Path $repoTmp | Out-Null
$staleDrills = @(Get-ChildItem -LiteralPath $repoTmp -Directory -Filter "backup-drill-*" -ErrorAction Stop)
if ($staleDrills.Count -ne 0) {
    throw "stale backup drill artifacts detected; cleanup is required before key generation"
}
$suffix = [guid]::NewGuid().ToString("N").Substring(0, 10)
$project = "vpn-backup-drill-$suffix"
$artifactDir = Join-Path $repo "tmp\backup-drill-$suffix"
$lastReport = Join-Path $repo "tmp\backup-restore-last-report.json"
$profiles = @("--profile", "core", "--profile", "app", "--profile", "maintenance")
$migrations = @(
    "identity-migrate", "catalog-migrate", "billing-migrate", "subscription-migrate",
    "access-migrate", "provisioning-migrate", "notification-migrate", "admin-migrate"
)
$databases = @(
    @{ name = "identity_service"; owner = "identity_app"; password = "drill-identity" },
    @{ name = "catalog_service"; owner = "catalog_app"; password = "drill-catalog" },
    @{ name = "billing_service"; owner = "billing_app"; password = "drill-billing" },
    @{ name = "subscription_service"; owner = "subscription_app"; password = "drill-subscription" },
    @{ name = "access_service"; owner = "access_app"; password = "drill-access" },
    @{ name = "provisioning_service"; owner = "provisioning_app"; password = "drill-provisioning" },
    @{ name = "notification_service"; owner = "notification_app"; password = "drill-notification" },
    @{ name = "admin_service"; owner = "admin_migrator"; password = "drill-admin-migrator" }
)

function Set-DrillEnv([string]$name, [string]$value) {
    [Environment]::SetEnvironmentVariable($name, $value, "Process")
}

function Invoke-Compose([string[]]$arguments) {
    & docker compose -p $project @profiles @arguments
    if ($LASTEXITCODE -ne 0) {
        throw "backup/restore Compose command failed"
    }
}

function Invoke-ComposeExpectFailure([string[]]$arguments) {
    & docker compose -p $project @profiles @arguments
    if ($LASTEXITCODE -eq 0) {
        throw "backup/restore Compose command unexpectedly succeeded"
    }
}

New-Item -ItemType Directory -Force -Path $artifactDir | Out-Null
Set-DrillEnv "POSTGRES_USER" "drill_admin"
Set-DrillEnv "POSTGRES_PASSWORD" "drill-control-password"
Set-DrillEnv "POSTGRES_DB" "drill_control"
Set-DrillEnv "POSTGRES_PORT" "0"
Set-DrillEnv "REDIS_PASSWORD" "drill-redis"
Set-DrillEnv "KAFKA_PORT" "0"
Set-DrillEnv "IDENTITY_DB_PASSWORD" "drill-identity"
Set-DrillEnv "CATALOG_DB_PASSWORD" "drill-catalog"
Set-DrillEnv "BILLING_DB_PASSWORD" "drill-billing"
Set-DrillEnv "SUBSCRIPTION_DB_PASSWORD" "drill-subscription"
Set-DrillEnv "ACCESS_DB_PASSWORD" "drill-access"
Set-DrillEnv "PROVISIONING_DB_PASSWORD" "drill-provisioning"
Set-DrillEnv "NOTIFICATION_DB_PASSWORD" "drill-notification"
Set-DrillEnv "ADMIN_DB_PASSWORD" "drill-admin"
Set-DrillEnv "ADMIN_MIGRATOR_DB_PASSWORD" "drill-admin-migrator"
Set-DrillEnv "ACCESS_CREDENTIAL_KEY_BASE64" "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="
Set-DrillEnv "ACCESS_TOKEN_HMAC_KEY_BASE64" "ZmVkY2JhOTg3NjU0MzIxMGZlZGNiYTk4NzY1NDMyMTA="
Set-DrillEnv "SUBSCRIPTION_PUBLIC_BASE_URL" "https://example.invalid"
Set-DrillEnv "TELEGRAM_WEBHOOK_SECRET" "drill-webhook"
Set-DrillEnv "TELEGRAM_BOT_TOKEN" "drill-bot"
Set-DrillEnv "TERMS_URL" "https://example.invalid/terms"
Set-DrillEnv "YOOKASSA_SECRET_KEY" "drill-yookassa"
Set-DrillEnv "PAYMENT_RETURN_URL" "https://example.invalid/return"
Set-DrillEnv "BACKUP_ARTIFACT_DIR" $artifactDir
Set-DrillEnv "BACKUP_UID" "65532"
Set-DrillEnv "BACKUP_GID" "65532"
Set-DrillEnv "RESTORE_POSTGRES_USER" "restore_admin"
Set-DrillEnv "RESTORE_POSTGRES_PASSWORD" "restore-control-password"
Set-DrillEnv "RESTORE_POSTGRES_DB" "restore_control"
Set-DrillEnv "COMPOSE_PARALLEL_LIMIT" "2"
Set-DrillEnv "COMPOSE_BAKE" "false"

try {
    Invoke-Compose @("build", "backup-tool")
    Invoke-Compose @("run", "--rm", "--no-deps", "backup-tool", "keygen", "--identity", "/backups/drill.agekey", "--recipient", "/backups/drill.recipient")
    if ($env:BACKUP_DRILL_FAIL_AFTER_KEYGEN -eq "1") {
        throw "forced failure after backup key generation"
    }
    Invoke-Compose @("up", "-d", "--wait", "postgres")
    Invoke-Compose (@("up", "--build") + $migrations)

    foreach ($database in $databases) {
        $sourceURL = "postgres://$($database.owner):$($database.password)@postgres:5432/$($database.name)?sslmode=disable"
        Set-DrillEnv "BACKUP_DATABASE_URL" $sourceURL
        Invoke-Compose @(
            "run", "--rm", "--no-deps", "backup-tool", "backup",
            "--database", $database.name, "--owner", $database.owner,
            "--recipient", "/backups/drill.recipient",
            "--output", "/backups/$($database.name).dump.age",
            "--metadata", "/backups/$($database.name).metadata.json",
            "--inspection", "/backups/$($database.name).source.json"
        )
    }

    $restoreStarted = [DateTimeOffset]::UtcNow
    Invoke-Compose @("up", "-d", "--wait", "restore-postgres")
    Set-DrillEnv "BACKUP_DATABASE_URL" "postgres://identity_app:drill-identity@restore-postgres:5432/identity_service?sslmode=disable"
    Invoke-Compose @("exec", "-T", "-e", "PGPASSWORD=drill-identity", "restore-postgres", "psql", "-U", "identity_app", "-d", "identity_service", "-v", "ON_ERROR_STOP=1", "-c", "CREATE TABLE restore_preflight_sentinel (id integer PRIMARY KEY)")
    Invoke-ComposeExpectFailure @(
        "run", "--rm", "--no-deps", "backup-tool", "restore",
        "--identity", "/backups/drill.agekey",
        "--input", "/backups/identity_service.dump.age",
        "--metadata", "/backups/identity_service.metadata.json"
    )
    $sentinel = & docker compose -p $project @profiles exec -T -e "PGPASSWORD=drill-identity" restore-postgres psql -U identity_app -d identity_service -Atqc "SELECT to_regclass('public.restore_preflight_sentinel') IS NOT NULL"
    if ($LASTEXITCODE -ne 0 -or ("$sentinel").Trim() -ne "t") {
        throw "restore preflight did not preserve the nonempty target"
    }
    Invoke-Compose @("exec", "-T", "-e", "PGPASSWORD=drill-identity", "restore-postgres", "psql", "-U", "identity_app", "-d", "identity_service", "-v", "ON_ERROR_STOP=1", "-c", "DROP TABLE restore_preflight_sentinel")
    foreach ($database in $databases) {
        $targetURL = "postgres://$($database.owner):$($database.password)@restore-postgres:5432/$($database.name)?sslmode=disable"
        Set-DrillEnv "BACKUP_DATABASE_URL" $targetURL
        Invoke-Compose @(
            "run", "--rm", "--no-deps", "backup-tool", "restore",
            "--identity", "/backups/drill.agekey",
            "--input", "/backups/$($database.name).dump.age",
            "--metadata", "/backups/$($database.name).metadata.json"
        )
        Invoke-Compose @(
            "run", "--rm", "--no-deps", "backup-tool", "inspect",
            "--database", $database.name, "--owner", $database.owner,
            "--output", "/backups/$($database.name).target.json"
        )
        Invoke-Compose @(
            "run", "--rm", "--no-deps", "backup-tool", "compare",
            "--source", "/backups/$($database.name).source.json",
            "--target", "/backups/$($database.name).target.json"
        )
    }
    $completedAt = [DateTimeOffset]::UtcNow
    $rtoSeconds = [math]::Round(($completedAt - $restoreStarted).TotalSeconds, 3)
    $metadata = foreach ($database in $databases) {
        Get-Content (Join-Path $artifactDir "$($database.name).metadata.json") -Raw | ConvertFrom-Json
    }
    $oldest = ($metadata | Sort-Object created_at | Select-Object -First 1).created_at
    $rpoSeconds = [math]::Round(($restoreStarted - [DateTimeOffset]::Parse($oldest)).TotalSeconds, 3)
    if ($rpoSeconds -gt 86400) { throw "backup RPO evidence exceeded 24 hours" }
    if ($rtoSeconds -gt 1800) { throw "restore RTO evidence exceeded 30 minutes" }
    $report = [ordered]@{
        format_version = 1
        result = "passed"
        completed_at = $completedAt.ToString("o")
        database_count = $databases.Count
        encrypted_artifacts = $metadata.Count
        integrity = "ciphertext_sha256_exported_snapshot_and_table_row_counts_match"
        ownership = "all_public_relations_match_service_owner"
        rpo_target_seconds = 86400
        observed_rpo_seconds = $rpoSeconds
        rto_target_seconds = 1800
        observed_rto_seconds = $rtoSeconds
    }
    $json = $report | ConvertTo-Json
    Set-Content -LiteralPath (Join-Path $artifactDir "drill-report.json") -Value $json -Encoding utf8
    Set-Content -LiteralPath $lastReport -Value $json -Encoding utf8
    Write-Output $json
} finally {
    $cleanupFailure = $null
    try {
        & docker compose -p $project @profiles down -v --remove-orphans | Out-Null
        if ($LASTEXITCODE -ne 0) {
            throw "backup drill Compose cleanup failed"
        }
    } catch {
        $cleanupFailure = $_
    }
    $resolvedRepoTmp = [System.IO.Path]::GetFullPath((Join-Path $repo "tmp"))
    $resolvedArtifacts = [System.IO.Path]::GetFullPath($artifactDir)
    try {
        if (-not $resolvedArtifacts.StartsWith($resolvedRepoTmp + [System.IO.Path]::DirectorySeparatorChar, [System.StringComparison]::OrdinalIgnoreCase)) {
            throw "backup drill cleanup path escaped repository tmp"
        }
        if (Test-Path -LiteralPath $resolvedArtifacts) {
            Remove-Item -LiteralPath $resolvedArtifacts -Recurse -Force -ErrorAction Stop
        }
        if (Test-Path -LiteralPath $resolvedArtifacts) {
            throw "backup drill artifact cleanup verification failed"
        }
    } catch {
        if ($null -eq $cleanupFailure) {
            $cleanupFailure = $_
        }
    }
    if ($null -ne $cleanupFailure) {
        throw "backup drill cleanup failed"
    }
}

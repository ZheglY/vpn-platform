$ErrorActionPreference = "Stop"

$env:VPN_SMOKE_FULL_CONTROL_PLANE = "1"
$env:STAGE7_EXTENDED_SMOKE = "1"
$env:COMPOSE_PROFILES = "core,app,vpn"

& powershell -NoProfile -ExecutionPolicy Bypass -File (Join-Path $PSScriptRoot "compose-smoke.ps1")
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "Stage 7 smoke passed: durable notifications, Telegram failure handling, admin mTLS/RBAC/idempotency/audit, restart recovery, stale suppression, and Stage 6 VPN lifecycle."

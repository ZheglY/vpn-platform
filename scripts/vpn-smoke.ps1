$ErrorActionPreference = "Stop"

# Full Stage 6 path: access-service -> Kafka -> provisioning-service ->
# subscription-service/access-service mTLS -> node agents -> Xray -> Kafka -> access-service.
$env:VPN_SMOKE_FULL_CONTROL_PLANE = "1"
$env:COMPOSE_PROFILES = "core,app,vpn"

& powershell -NoProfile -ExecutionPolicy Bypass -File (Join-Path $PSScriptRoot "compose-smoke.ps1")
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "Stage 6 VPN smoke passed: access.provision.request.v1 traversed Kafka, both node agents, real Xray, Access readiness, Happ delivery, and revoke."

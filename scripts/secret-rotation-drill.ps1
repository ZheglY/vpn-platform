$ErrorActionPreference = "Stop"

$repo = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$cacheRoot = if (-not [string]::IsNullOrWhiteSpace($env:VPN_PLATFORM_CACHE_ROOT)) {
    $env:VPN_PLATFORM_CACHE_ROOT
} else {
    Join-Path (Split-Path $repo -Parent) ".cache\vpn-platform"
}
if ([string]::IsNullOrWhiteSpace($env:GOCACHE)) { $env:GOCACHE = Join-Path $cacheRoot "cache" }
if ([string]::IsNullOrWhiteSpace($env:GOMODCACHE)) { $env:GOMODCACHE = Join-Path $cacheRoot "mod" }
if ([string]::IsNullOrWhiteSpace($env:GOTMPDIR)) { $env:GOTMPDIR = Join-Path $cacheRoot "tmp" }
New-Item -ItemType Directory -Force -Path $env:GOCACHE, $env:GOMODCACHE, $env:GOTMPDIR | Out-Null

Push-Location $repo
try {
    go run -mod=readonly ./tools/rotationdrill
    if ($LASTEXITCODE -ne 0) { throw "mTLS and REALITY rotation drill failed" }
    go test -mod=readonly ./services/access/internal/credential -run "KeyringSupportsOverlap|TokenHasherKeyring"
    if ($LASTEXITCODE -ne 0) { throw "Access key overlap drill failed" }
    go test -mod=readonly ./services/node-agent/internal/xray -run "SystemdManagerRestoresLastKnownGood"
    if ($LASTEXITCODE -ne 0) { throw "REALITY rollback drill failed" }
    go test -mod=readonly ./services/billing/internal/yookassa ./services/telegram-bot/internal/telegram ./services/notification/internal/telegram
    if ($LASTEXITCODE -ne 0) { throw "provider credential rotation safety tests failed" }
} finally {
    Pop-Location
}

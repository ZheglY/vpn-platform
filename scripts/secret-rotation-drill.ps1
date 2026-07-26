$ErrorActionPreference = "Stop"

$repo = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$cacheRoot = "D:\Work\Projects\dev\.cache"
$tmpRoot = "D:\Work\Projects\dev\.tmp\go"
New-Item -ItemType Directory -Force -Path $cacheRoot, $tmpRoot | Out-Null
$env:GOCACHE = Join-Path $cacheRoot "go-build"
$env:GOMODCACHE = Join-Path $cacheRoot "go-mod"
$env:GOTMPDIR = $tmpRoot

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

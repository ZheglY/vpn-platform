$ErrorActionPreference = "Stop"
$repo = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$composeProject = if (-not [string]::IsNullOrWhiteSpace($env:COMPOSE_PROJECT_NAME)) { $env:COMPOSE_PROJECT_NAME } else { "vpn-service" }
$goImage = "golang:1.26.5-alpine@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2"
docker run --rm `
    -v "$($repo):/src" `
    -v "$($composeProject)-go-mod-cache:/go/pkg/mod" `
    -v "$($composeProject)-go-build-cache:/root/.cache/go-build" `
    -w /src `
    $goImage `
    sh -lc "/usr/local/go/bin/go run ./tools/devxray/cmd/devxray secrets/dev-xray && chown -R 65532:65532 secrets/dev-xray"
exit $LASTEXITCODE

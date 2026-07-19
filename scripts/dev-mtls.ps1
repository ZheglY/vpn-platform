$ErrorActionPreference = "Stop"

$repo = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$goImage = "golang:1.26.5-alpine@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2"
docker run --rm `
    -v "$($repo):/src" `
    -v "vpn-service-go-mod-cache:/go/pkg/mod" `
    -v "vpn-service-go-build-cache:/root/.cache/go-build" `
    -w /src `
    $goImage `
    sh -lc "/usr/local/go/bin/go run ./tools/devmtls/cmd/devmtls secrets/dev-mtls && chown -R 65532:65532 secrets/dev-mtls"
exit $LASTEXITCODE

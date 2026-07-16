$ErrorActionPreference = "Stop"

if (Get-Command gcc -ErrorAction SilentlyContinue) {
    $env:CGO_ENABLED = "1"
    go test -race ./...
    exit $LASTEXITCODE
}

$repo = (Resolve-Path ".").Path
docker run --rm `
    -v "$($repo):/src" `
    -v "vpn-service-go-mod-cache:/go/pkg/mod" `
    -v "vpn-service-go-build-cache:/root/.cache/go-build" `
    -w /src `
    golang:1.26.5 `
    sh -lc "/usr/local/go/bin/go test -race ./..."
exit $LASTEXITCODE

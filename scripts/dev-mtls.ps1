$ErrorActionPreference = "Stop"

$outDir = Join-Path (Resolve-Path ".").Path "secrets/dev-mtls"
go run ./tools/devmtls/cmd/devmtls $outDir
exit $LASTEXITCODE

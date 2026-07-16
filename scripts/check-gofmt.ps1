$ErrorActionPreference = "Stop"

$files = Get-ChildItem -Path . -Recurse -Filter "*.go" -File |
    Where-Object { $_.FullName -notmatch "\\node_modules\\" } |
    ForEach-Object { $_.FullName }

if (-not $files) {
    exit 0
}

$unformatted = & gofmt -l $files
if ($LASTEXITCODE -ne 0) {
    exit $LASTEXITCODE
}
if ($unformatted) {
    Write-Error "Go files are not gofmt-formatted:`n$($unformatted -join "`n")"
    exit 1
}

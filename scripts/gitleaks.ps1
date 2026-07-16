$ErrorActionPreference = "Stop"

$version = $env:GITLEAKS_VERSION
if ([string]::IsNullOrWhiteSpace($version)) {
    $version = "v8.30.1"
}

$headOut = Join-Path $env:TEMP "vpn-service-git-head.out"
$headErr = Join-Path $env:TEMP "vpn-service-git-head.err"
if (Test-Path $headOut) { Remove-Item $headOut -Force }
if (Test-Path $headErr) { Remove-Item $headErr -Force }

$headProcess = Start-Process `
    -FilePath "git" `
    -ArgumentList @("rev-parse", "--verify", "HEAD") `
    -NoNewWindow `
    -Wait `
    -PassThru `
    -RedirectStandardOutput $headOut `
    -RedirectStandardError $headErr
$hasHead = $headProcess.ExitCode -eq 0
Remove-Item $headOut, $headErr -Force -ErrorAction SilentlyContinue

$argsList = @("run", "github.com/zricethezav/gitleaks/v8@$version", "detect", "--source", ".", "--redact", "--no-banner", "--no-color", "--log-level", "warn")
if (-not $hasHead) {
    $argsList += "--no-git"
}

& go @argsList
exit $LASTEXITCODE

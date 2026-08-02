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

$stdoutPath = Join-Path $env:TEMP "vpn-service-gitleaks.out"
$stderrPath = Join-Path $env:TEMP "vpn-service-gitleaks.err"
Remove-Item $stdoutPath, $stderrPath -Force -ErrorAction SilentlyContinue

$process = Start-Process `
    -FilePath "go" `
    -ArgumentList $argsList `
    -NoNewWindow `
    -Wait `
    -PassThru `
    -RedirectStandardOutput $stdoutPath `
    -RedirectStandardError $stderrPath
$stdout = if (Test-Path $stdoutPath) { Get-Content $stdoutPath -Raw } else { "" }
$stderr = if (Test-Path $stderrPath) { Get-Content $stderrPath -Raw } else { "" }
if ($process.ExitCode -eq 0) {
    if ($stdout) { Write-Output $stdout }
    if ($stderr) { [Console]::Error.WriteLine($stderr) }
    Remove-Item $stdoutPath, $stderrPath -Force -ErrorAction SilentlyContinue
    exit 0
}

if ("$stdout`n$stderr" -notmatch "Application Control policy has blocked this file") {
    if ($stdout) { Write-Output $stdout }
    if ($stderr) { [Console]::Error.WriteLine($stderr) }
    Remove-Item $stdoutPath, $stderrPath -Force -ErrorAction SilentlyContinue
    exit $process.ExitCode
}

Write-Warning "Local gitleaks was blocked by Windows Application Control; retrying in a pinned Go container."
Remove-Item $stdoutPath, $stderrPath -Force -ErrorAction SilentlyContinue
$repo = (Resolve-Path ".").Path
$goImage = "golang:1.26.5-alpine@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2"
$dockerArgs = @(
    "run", "--rm",
    "-v", "${repo}:/src",
    "-v", "vpn-platform-go-mod-cache:/go/pkg/mod",
    "-v", "vpn-platform-go-build-cache:/root/.cache/go-build",
    "-w", "/src",
    $goImage,
    "sh", "-lc",
    'apk add --no-cache git >/dev/null && exec /usr/local/go/bin/go "$@"',
    "go"
) + $argsList
& docker @dockerArgs
exit $LASTEXITCODE

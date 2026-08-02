param(
    [switch]$Race
)

$ErrorActionPreference = "Stop"

function Remove-IfExists([string]$path) {
    if (Test-Path $path) {
        Remove-Item $path -Force
    }
}

function Invoke-GoCommand([string[]]$arguments, [string]$stdoutPath, [string]$stderrPath) {
    Remove-IfExists $stdoutPath
    Remove-IfExists $stderrPath

    $process = Start-Process `
        -FilePath "go" `
        -ArgumentList $arguments `
        -NoNewWindow `
        -Wait `
        -PassThru `
        -RedirectStandardOutput $stdoutPath `
        -RedirectStandardError $stderrPath

    return $process.ExitCode
}

function Write-FileIfPresent([string]$path) {
    if (Test-Path $path) {
        Get-Content $path
    }
}

function Read-FileOrEmpty([string]$path) {
    if (Test-Path $path) {
        return Get-Content $path -Raw
    }
    return ""
}

function Invoke-DockerGoTest([string[]]$arguments) {
    $repo = (Resolve-Path ".").Path
    $goCommand = "/usr/local/go/bin/go " + ($arguments -join " ")
    docker run --rm `
        -v "$($repo):/src" `
        -v "vpn-platform-go-mod-cache:/go/pkg/mod" `
        -v "vpn-platform-go-build-cache:/root/.cache/go-build" `
        -w /src `
        golang:1.26.5 `
        sh -lc $goCommand
    exit $LASTEXITCODE
}

$arguments = @("test")
if ($Race) {
    $arguments += "-race"
}
$arguments += "./..."

if ($Race -and -not (Get-Command gcc -ErrorAction SilentlyContinue)) {
    Write-Warning "gcc is not available on PATH; running race tests in Docker."
    Invoke-DockerGoTest $arguments
}

$suffix = if ($Race) { "race" } else { "test" }
$stdoutPath = Join-Path $env:TEMP "vpn-platform-go-$suffix.out"
$stderrPath = Join-Path $env:TEMP "vpn-platform-go-$suffix.err"
$exitCode = Invoke-GoCommand $arguments $stdoutPath $stderrPath

if ($exitCode -eq 0) {
    Write-FileIfPresent $stdoutPath
    Write-FileIfPresent $stderrPath
    Remove-IfExists $stdoutPath
    Remove-IfExists $stderrPath
    exit 0
}

$stdout = Read-FileOrEmpty $stdoutPath
$stderr = Read-FileOrEmpty $stderrPath
$combined = "$stdout`n$stderr"
if ($combined -match "Application Control policy has blocked this file") {
    Write-Warning "Local go test was blocked by Windows Application Control; retrying in Docker."
    Remove-IfExists $stdoutPath
    Remove-IfExists $stderrPath
    Invoke-DockerGoTest $arguments
}

Write-FileIfPresent $stdoutPath
Write-FileIfPresent $stderrPath
Remove-IfExists $stdoutPath
Remove-IfExists $stderrPath
exit $exitCode

$ErrorActionPreference = "Stop"
$previous = [Environment]::GetEnvironmentVariable("OBSERVABILITY_SMOKE")
try {
    [Environment]::SetEnvironmentVariable("OBSERVABILITY_SMOKE", "1", "Process")
    & powershell -NoProfile -ExecutionPolicy Bypass -File (Join-Path $PSScriptRoot "compose-smoke.ps1")
    exit $LASTEXITCODE
} finally {
    [Environment]::SetEnvironmentVariable("OBSERVABILITY_SMOKE", $previous, "Process")
}

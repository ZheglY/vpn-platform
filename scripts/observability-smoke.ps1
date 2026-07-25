$ErrorActionPreference = "Stop"
$previous = [Environment]::GetEnvironmentVariable("OBSERVABILITY_SMOKE")
$previousSampler = [Environment]::GetEnvironmentVariable("OTEL_TRACES_SAMPLER_ARG")
try {
    [Environment]::SetEnvironmentVariable("OBSERVABILITY_SMOKE", "1", "Process")
    [Environment]::SetEnvironmentVariable("OTEL_TRACES_SAMPLER_ARG", "1", "Process")
    & powershell -NoProfile -ExecutionPolicy Bypass -File (Join-Path $PSScriptRoot "compose-smoke.ps1")
    exit $LASTEXITCODE
} finally {
    [Environment]::SetEnvironmentVariable("OBSERVABILITY_SMOKE", $previous, "Process")
    [Environment]::SetEnvironmentVariable("OTEL_TRACES_SAMPLER_ARG", $previousSampler, "Process")
}

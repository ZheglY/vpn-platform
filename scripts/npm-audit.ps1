$ErrorActionPreference = "Continue"

$lastExit = 1
foreach ($attempt in 1..3) {
    npm audit --audit-level=high
    $lastExit = $LASTEXITCODE
    if ($lastExit -eq 0) {
        exit 0
    }
    Write-Warning "npm audit attempt $attempt failed; retrying"
    Start-Sleep -Seconds 5
}

exit $lastExit

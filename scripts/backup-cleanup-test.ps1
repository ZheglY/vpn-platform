$ErrorActionPreference = "Stop"

$repo = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$tmpRoot = Join-Path $repo "tmp"
New-Item -ItemType Directory -Force -Path $tmpRoot | Out-Null
if (@(Get-ChildItem -LiteralPath $tmpRoot -Directory -Filter "backup-drill-*" -ErrorAction Stop).Count -ne 0) {
    throw "backup cleanup test requires a clean tmp directory"
}

$previous = $env:BACKUP_DRILL_FAIL_AFTER_KEYGEN
try {
    $env:BACKUP_DRILL_FAIL_AFTER_KEYGEN = "1"
    & powershell -NoProfile -ExecutionPolicy Bypass -File (Join-Path $PSScriptRoot "backup-restore-drill.ps1")
    if ($LASTEXITCODE -eq 0) {
        throw "forced backup drill failure unexpectedly succeeded"
    }
} finally {
    $env:BACKUP_DRILL_FAIL_AFTER_KEYGEN = $previous
}

if (@(Get-ChildItem -LiteralPath $tmpRoot -Directory -Filter "backup-drill-*" -ErrorAction Stop).Count -ne 0) {
    throw "backup drill left temporary artifacts after forced keygen failure"
}

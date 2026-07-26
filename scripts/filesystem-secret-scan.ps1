$ErrorActionPreference = "Stop"

$repo = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$tmpRoot = Join-Path $repo "tmp"
if (-not (Test-Path -LiteralPath $tmpRoot)) {
    exit 0
}

$findings = @()
foreach ($file in Get-ChildItem -LiteralPath $tmpRoot -File -Recurse -Force -ErrorAction Stop) {
    if ($file.Name -match '\.(agekey|pem|key)$') {
        $findings += $file.FullName
        continue
    }
    if ($file.Length -gt 16MB) {
        continue
    }
    try {
        $body = [System.IO.File]::ReadAllText($file.FullName)
        if ($body -match 'AGE-SECRET-KEY-1' -or
            $body -match '-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----' -or
            $body -match 'vless://') {
            $findings += $file.FullName
        }
    } catch {
        throw "filesystem secret scan could not inspect a temporary artifact"
    }
}
if ($findings.Count -ne 0) {
    [Console]::Error.WriteLine("filesystem secret scan found $($findings.Count) sensitive temporary artifact(s)")
    exit 1
}

$ErrorActionPreference = "Stop"

$govulncheckVersion = $env:GOVULNCHECK_VERSION
if ([string]::IsNullOrWhiteSpace($govulncheckVersion)) {
    $govulncheckVersion = "v1.6.0"
}

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

function Write-WarningTail([string]$path, [string]$prefix) {
    if (Test-Path $path) {
        Get-Content $path -Tail 5 | ForEach-Object {
            Write-Warning "$prefix$_"
        }
    }
}

$directOut = Join-Path $env:TEMP "vpn-service-govulncheck-direct.out"
$directErr = Join-Path $env:TEMP "vpn-service-govulncheck-direct.err"
$directExit = Invoke-GoCommand `
    -Arguments @("run", "golang.org/x/vuln/cmd/govulncheck@$govulncheckVersion", "./...") `
    -StdoutPath $directOut `
    -StderrPath $directErr
if ($directExit -eq 0) {
    Write-FileIfPresent $directOut
    Write-FileIfPresent $directErr
    Remove-IfExists $directOut
    Remove-IfExists $directErr
    exit 0
}

Write-Warning "Direct govulncheck failed with exit code $directExit; retrying with a temporary local copy of the official Go vulnerability database."
Write-WarningTail $directOut "direct govulncheck: "
Write-WarningTail $directErr "direct govulncheck: "
Remove-IfExists $directOut
Remove-IfExists $directErr

$dbRoot = Join-Path $env:TEMP "vpn-service-vulndb"
if (Test-Path $dbRoot) {
    Remove-Item $dbRoot -Recurse -Force
}

New-Item -ItemType Directory -Path (Join-Path $dbRoot "index") | Out-Null
New-Item -ItemType Directory -Path (Join-Path $dbRoot "ID") | Out-Null

function Expand-GzipFile([string]$source, [string]$dest) {
    $inputFile = [System.IO.File]::OpenRead($source)
    try {
        $gzip = [System.IO.Compression.GzipStream]::new($inputFile, [System.IO.Compression.CompressionMode]::Decompress)
        try {
            $outputFile = [System.IO.File]::Create($dest)
            try {
                $gzip.CopyTo($outputFile)
            } finally {
                $outputFile.Dispose()
            }
        } finally {
            $gzip.Dispose()
        }
    } finally {
        $inputFile.Dispose()
    }
}

function Download-GzipJson([string]$endpoint, [string]$dest) {
    $gzipPath = "$dest.gz"
    Invoke-WebRequest -Uri "https://vuln.go.dev/$endpoint.json.gz" -OutFile $gzipPath -UseBasicParsing -TimeoutSec 120
    Expand-GzipFile $gzipPath $dest
    Remove-Item $gzipPath -Force
}

Download-GzipJson "index/db" (Join-Path $dbRoot "index\db.json")
Download-GzipJson "index/modules" (Join-Path $dbRoot "index\modules.json")

$modules = (go list -m all | ForEach-Object { ($_ -split "\s+")[0] }) + @("stdlib", "toolchain") | Sort-Object -Unique
$moduleSet = @{}
foreach ($module in $modules) {
    $moduleSet[$module] = $true
}

$index = Get-Content (Join-Path $dbRoot "index\modules.json") -Raw | ConvertFrom-Json
$ids = New-Object "System.Collections.Generic.HashSet[string]"
foreach ($entry in $index) {
    if ($moduleSet.ContainsKey([string]$entry.path)) {
        foreach ($vulnerability in $entry.vulns) {
            [void]$ids.Add([string]$vulnerability.id)
        }
    }
}

foreach ($id in $ids) {
    Download-GzipJson "ID/$id" (Join-Path $dbRoot "ID\$id.json")
}

$fileURL = "file:///" + (($dbRoot -replace "\\", "/") -replace " ", "%20")
$fallbackOut = Join-Path $env:TEMP "vpn-service-govulncheck-fallback.out"
$fallbackErr = Join-Path $env:TEMP "vpn-service-govulncheck-fallback.err"
$fallbackExit = Invoke-GoCommand `
    -Arguments @("run", "golang.org/x/vuln/cmd/govulncheck@$govulncheckVersion", "-db", $fileURL, "./...") `
    -StdoutPath $fallbackOut `
    -StderrPath $fallbackErr

Write-FileIfPresent $fallbackOut
Write-FileIfPresent $fallbackErr
Remove-IfExists $fallbackOut
Remove-IfExists $fallbackErr
exit $fallbackExit

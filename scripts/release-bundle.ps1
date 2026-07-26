$ErrorActionPreference = "Stop"

$repo = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$cacheRoot = if (-not [string]::IsNullOrWhiteSpace($env:VPN_PLATFORM_CACHE_ROOT)) {
    $env:VPN_PLATFORM_CACHE_ROOT
} else {
    Join-Path (Split-Path $repo -Parent) ".cache\vpn-platform"
}
if ([string]::IsNullOrWhiteSpace($env:GOCACHE)) { $env:GOCACHE = Join-Path $cacheRoot "cache" }
if ([string]::IsNullOrWhiteSpace($env:GOMODCACHE)) { $env:GOMODCACHE = Join-Path $cacheRoot "mod" }
if ([string]::IsNullOrWhiteSpace($env:GOTMPDIR)) { $env:GOTMPDIR = Join-Path $cacheRoot "tmp" }
New-Item -ItemType Directory -Force -Path $env:GOCACHE, $env:GOMODCACHE, $env:GOTMPDIR | Out-Null
$status = & git -C $repo status --porcelain --untracked-files=all
if ($LASTEXITCODE -ne 0) { throw "cannot inspect repository state" }
if ($status) { throw "release bundle requires a clean repository" }

$commit = (& git -C $repo rev-parse HEAD).Trim()
$sourceDate = (& git -C $repo show -s --format=%cI HEAD).Trim()
if ($LASTEXITCODE -ne 0) { throw "cannot inspect source commit" }
$shortCommit = $commit.Substring(0, 12)
$version = if ($env:RELEASE_VERSION) { $env:RELEASE_VERSION } else { "0.0.0-git.$shortCommit" }
$output = if ($env:RELEASE_OUTPUT_DIR) { $env:RELEASE_OUTPUT_DIR } else { Join-Path $repo "tmp\release-$shortCommit" }
$tmpRoot = [System.IO.Path]::GetFullPath((Join-Path $repo "tmp"))
$resolvedOutput = [System.IO.Path]::GetFullPath($output)
if (-not $resolvedOutput.StartsWith($tmpRoot + [System.IO.Path]::DirectorySeparatorChar, [System.StringComparison]::OrdinalIgnoreCase)) {
    throw "release output must be below repository tmp"
}
$output = $resolvedOutput

if (Test-Path -LiteralPath $output) {
    Remove-Item -LiteralPath $output -Recurse -Force
}
New-Item -ItemType Directory -Force -Path $output | Out-Null

Push-Location $repo
try {
    go run -mod=readonly ./tools/releasectl build `
        --inventory deploy/release/images.json `
        --output $output `
        --version $version `
        --commit $commit `
        --source-date $sourceDate
    if ($LASTEXITCODE -ne 0) { throw "release build failed" }

    go run -mod=readonly ./tools/releasectl verify --inventory deploy/release/images.json --output $output
    if ($LASTEXITCODE -ne 0) { throw "release verification failed" }

    Write-Host "Release bundle metadata is ready at $output"
} finally {
    Pop-Location
}

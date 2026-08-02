$ErrorActionPreference = "Stop"

$repo = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$tmpRoot = Join-Path $repo "tmp"
New-Item -ItemType Directory -Force -Path $tmpRoot | Out-Null
$suffix = [guid]::NewGuid().ToString("N").Substring(0, 10)
$artifactDir = Join-Path $tmpRoot "node-hardening-$suffix"
$container = "vpn-node-hardening-$suffix"
$image = "vpn-service/ansible-node-test:local"
New-Item -ItemType Directory -Force -Path $artifactDir | Out-Null

function New-RandomBase64([bool]$urlSafe) {
    $bytes = [byte[]]::new(32)
    $generator = [System.Security.Cryptography.RandomNumberGenerator]::Create()
    try {
        $generator.GetBytes($bytes)
    } finally {
        $generator.Dispose()
    }
    $value = [Convert]::ToBase64String($bytes)
    if ($urlSafe) {
        return $value.TrimEnd("=").Replace("+", "-").Replace("/", "_")
    }
    return $value
}

try {
    $savedGOOS = $env:GOOS
    $savedGOARCH = $env:GOARCH
    $savedCGO = $env:CGO_ENABLED
    try {
        $env:GOOS = "linux"
        $env:GOARCH = "amd64"
        $env:CGO_ENABLED = "0"
        & go build -trimpath -o (Join-Path $artifactDir "node-agent") ./services/node-agent/cmd/node-agent
        if ($LASTEXITCODE -ne 0) { throw "build Linux node-agent fixture failed" }
    } finally {
        $env:GOOS = $savedGOOS
        $env:GOARCH = $savedGOARCH
        $env:CGO_ENABLED = $savedCGO
    }
    $devmtls = Join-Path $artifactDir "devmtls.exe"
    & go build -trimpath -o $devmtls ./tools/devmtls/cmd/devmtls
    if ($LASTEXITCODE -ne 0) { throw "build disposable mTLS generator failed" }
    & $devmtls (Join-Path $artifactDir "mtls") | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "generate disposable mTLS fixture failed" }
    $xrayFixture = @'
#!/bin/sh
set -eu
if [ "${1:-}" = "version" ]; then
  printf '%s\n' 'test-xray'
  exit 0
fi
config=''
test_only=0
while [ "$#" -gt 0 ]; do
  case "$1" in
    -config) config="${2:-}"; shift 2 ;;
    -test) test_only=1; shift ;;
    *) shift ;;
  esac
done
[ -n "$config" ] && [ -r "$config" ]
python3 -m json.tool "$config" >/dev/null
[ ! -e /run/reject-xray-candidate ]
[ "$test_only" -eq 0 ] || exit 0
trap 'exit 0' TERM INT
while :; do sleep 60; done
'@
    [System.IO.File]::WriteAllText((Join-Path $artifactDir "xray"), $xrayFixture.Replace("`r`n", "`n") + "`n", [System.Text.Encoding]::ASCII)
    Set-Content -LiteralPath (Join-Path $artifactDir "reality.key") -Value (New-RandomBase64 $true) -Encoding ascii
    Set-Content -LiteralPath (Join-Path $artifactDir "wireguard.key") -Value (New-RandomBase64 $false) -Encoding ascii
    $variables = [ordered]@{
        vpn_node_test_mode = $true
        vpn_node_manage_services = $true
        vpn_node_environment = "local"
        vpn_node_mtls_namespace = "local"
        vpn_node_wireguard_address = "127.0.0.1/32"
        vpn_node_id = "61000000-0000-4000-8000-000000000001"
        vpn_node_reality_target = "example.invalid:443"
        vpn_node_reality_server_names = @("example.invalid")
        vpn_node_reality_short_ids = @("0011")
        vpn_node_wireguard_peer_public_key = (New-RandomBase64 $false)
        vpn_node_wireguard_peer_endpoint = "192.0.2.1:51820"
        vpn_node_agent_binary_source = "/fixtures/node-agent"
        vpn_node_xray_binary_source = "/fixtures/xray"
        vpn_node_mtls_ca_source = "/fixtures/mtls/ca.crt"
        vpn_node_mtls_cert_source = "/fixtures/mtls/node-agent-primary.crt"
        vpn_node_mtls_key_source = "/fixtures/mtls/node-agent-primary.key"
        vpn_node_reality_private_key_source = "/fixtures/reality.key"
        vpn_node_wireguard_private_key_source = "/fixtures/wireguard.key"
    }
    $variables | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath (Join-Path $artifactDir "vars.json") -Encoding utf8
    & docker build -f deploy/ansible/tests/Dockerfile -t $image .
    if ($LASTEXITCODE -ne 0) { throw "build disposable Ansible host failed" }
    $containerID = & docker run -d --name $container --privileged --cgroupns=host --tmpfs /run --tmpfs /run/lock -v "/sys/fs/cgroup:/sys/fs/cgroup:rw" -e "ANSIBLE_CONFIG=/workspace/deploy/ansible/ansible.cfg" -e "ANSIBLE_ROLES_PATH=/workspace/deploy/ansible/roles" -v "${repo}:/workspace:ro" -v "${artifactDir}:/fixtures:ro" $image
    if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($containerID)) { throw "start disposable Ansible host failed" }
    foreach ($attempt in 1..30) {
        & docker exec $container systemctl show --property=Version | Out-Null
        if ($LASTEXITCODE -eq 0) { break }
        Start-Sleep -Milliseconds 500
    }
    if ($LASTEXITCODE -ne 0) { throw "disposable systemd host did not become ready" }
    & docker exec $container ansible-playbook --syntax-check playbooks/vpn-node.yml --extra-vars "@/fixtures/vars.json"
    if ($LASTEXITCODE -ne 0) { throw "Ansible syntax check failed" }
    & docker exec $container touch /run/reject-xray-candidate
    if ($LASTEXITCODE -ne 0) { throw "stage invalid Xray candidate marker failed" }
    & docker exec $container ansible-playbook playbooks/vpn-node.yml --extra-vars "@/fixtures/vars.json"
    if ($LASTEXITCODE -eq 0) { throw "invalid initial Xray candidate unexpectedly converged" }
    & docker exec $container sh -c "test ! -e /etc/vpn-node/xray/config.json"
    if ($LASTEXITCODE -ne 0) { throw "invalid initial candidate created an Xray config" }
    & docker exec $container systemctl is-active --quiet xray.service
    if ($LASTEXITCODE -eq 0) { throw "Xray started without a valid initial configuration" }
    & docker exec $container rm /run/reject-xray-candidate
    & docker exec $container systemctl stop node-agent.service
    & docker exec $container systemctl reset-failed node-agent.service
    & docker exec $container ansible-playbook playbooks/vpn-node.yml --extra-vars "@/fixtures/vars.json"
    if ($LASTEXITCODE -ne 0) {
        & docker exec $container systemctl status --no-pager node-agent.service
        & docker exec $container journalctl --no-pager -u node-agent.service -n 20
        & docker exec $container systemctl status --no-pager xray.service
        & docker exec $container journalctl --no-pager -u xray.service -n 20
        throw "first Ansible convergence failed"
    }
    $second = & docker exec $container ansible-playbook playbooks/vpn-node.yml --extra-vars "@/fixtures/vars.json"
    if ($LASTEXITCODE -ne 0) { throw "second Ansible convergence failed" }
    if ("$second" -notmatch "changed=0\s+unreachable=0\s+failed=0") {
        throw "Ansible role is not idempotent"
    }
    & docker exec $container sh /workspace/deploy/ansible/tests/assert.sh
    if ($LASTEXITCODE -ne 0) { throw "hardened node assertions failed" }
    $beforeLine = & docker exec $container sha256sum /etc/vpn-node/xray/config.json
    if ($LASTEXITCODE -ne 0) { throw "hash initial Xray config failed" }
    $before = ("$beforeLine" -split "\s+")[0]
    & docker exec $container touch /run/reject-xray-candidate
    & docker exec $container systemctl restart node-agent.service
    Start-Sleep -Seconds 1
    & docker exec $container systemctl is-active --quiet node-agent.service
    if ($LASTEXITCODE -eq 0) { throw "node-agent remained active after an invalid candidate" }
    & docker exec $container systemctl stop node-agent.service
    if ($LASTEXITCODE -ne 0) { throw "stop failed node-agent before last-known-good check failed" }
    & docker exec $container systemctl is-active --quiet xray.service
    if ($LASTEXITCODE -ne 0) { throw "invalid candidate stopped last-known-good Xray" }
    $afterLine = & docker exec $container sha256sum /etc/vpn-node/xray/config.json
    if ($LASTEXITCODE -ne 0) { throw "hash last-known-good Xray config failed" }
    $after = ("$afterLine" -split "\s+")[0]
    if ($before -ne $after) { throw "invalid candidate replaced last-known-good Xray config" }
    & docker exec $container rm /run/reject-xray-candidate
    & docker exec $container systemctl stop node-agent.service
    & docker exec $container systemctl reset-failed node-agent.service
    & docker exec $container systemctl start node-agent.service
    if ($LASTEXITCODE -ne 0) { throw "node-agent did not recover after invalid candidate" }
    & docker restart $container | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "restart disposable host failed" }
    foreach ($attempt in 1..30) {
        & docker exec $container systemctl is-active --quiet node-agent.service
        $nodeActive = $LASTEXITCODE -eq 0
        & docker exec $container systemctl is-active --quiet xray.service
        $xrayActive = $LASTEXITCODE -eq 0
        if ($nodeActive -and $xrayActive) { break }
        Start-Sleep -Milliseconds 500
    }
    if (-not $nodeActive -or -not $xrayActive) { throw "normal host restart did not start node-agent and Xray" }
} finally {
    $cleanupErrors = [System.Collections.Generic.List[string]]::new()
    $containerID = & docker ps -aq --filter "name=^/$container$"
    if (-not [string]::IsNullOrWhiteSpace("$containerID")) {
        & docker rm -f $container | Out-Null
        if ($LASTEXITCODE -ne 0) { $cleanupErrors.Add("remove disposable Ansible host failed") }
    }
    $imageID = & docker images -q $image
    if (-not [string]::IsNullOrWhiteSpace("$imageID")) {
        & docker image rm $image | Out-Null
        if ($LASTEXITCODE -ne 0) { $cleanupErrors.Add("remove disposable Ansible image failed") }
    }
    $resolvedTmp = [System.IO.Path]::GetFullPath($tmpRoot)
    $resolvedArtifacts = [System.IO.Path]::GetFullPath($artifactDir)
    if (-not $resolvedArtifacts.StartsWith($resolvedTmp + [System.IO.Path]::DirectorySeparatorChar, [System.StringComparison]::OrdinalIgnoreCase)) {
        $cleanupErrors.Add("node hardening cleanup path escaped repository tmp")
    } else {
        try {
            if (Test-Path -LiteralPath $resolvedArtifacts) {
                Remove-Item -LiteralPath $resolvedArtifacts -Recurse -Force -ErrorAction Stop
            }
            if (Test-Path -LiteralPath $resolvedArtifacts) {
                $cleanupErrors.Add("node hardening artifacts were not removed")
            }
        } catch {
            $cleanupErrors.Add("remove node hardening artifacts failed: $($_.Exception.Message)")
        }
    }
    if ($cleanupErrors.Count -gt 0) {
        throw ($cleanupErrors -join "; ")
    }
}

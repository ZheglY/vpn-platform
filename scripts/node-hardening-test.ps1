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
    Set-Content -LiteralPath (Join-Path $artifactDir "node-agent") -Value "#!/bin/sh`ntest -r /etc/vpn-node/credentials/node-agent/tls.key`ntest -r /etc/vpn-node/credentials/xray/reality.key`ntest -w /etc/vpn-node/xray`ntrap 'exit 0' TERM INT`nwhile :; do sleep 60; done`n" -Encoding ascii
    Set-Content -LiteralPath (Join-Path $artifactDir "xray") -Value "#!/bin/sh`ntest -r /etc/vpn-node/credentials/xray/reality.key`ntest -r /etc/vpn-node/xray/config.json`ntrap 'exit 0' TERM INT`nwhile :; do sleep 60; done`n" -Encoding ascii
    Set-Content -LiteralPath (Join-Path $artifactDir "ca.pem") -Value "local-disposable-ca" -Encoding ascii
    Set-Content -LiteralPath (Join-Path $artifactDir "tls.crt") -Value "local-disposable-cert" -Encoding ascii
    Set-Content -LiteralPath (Join-Path $artifactDir "tls.key") -Value (New-RandomBase64 $false) -Encoding ascii
    Set-Content -LiteralPath (Join-Path $artifactDir "reality.key") -Value (New-RandomBase64 $true) -Encoding ascii
    Set-Content -LiteralPath (Join-Path $artifactDir "wireguard.key") -Value (New-RandomBase64 $false) -Encoding ascii
    $variables = [ordered]@{
        vpn_node_test_mode = $true
        vpn_node_manage_services = $false
        vpn_node_id = "61000000-0000-4000-8000-000000000001"
        vpn_node_reality_target = "example.invalid:443"
        vpn_node_reality_server_names = @("example.invalid")
        vpn_node_reality_short_ids = @("0011")
        vpn_node_wireguard_peer_public_key = (New-RandomBase64 $false)
        vpn_node_wireguard_peer_endpoint = "192.0.2.1:51820"
        vpn_node_agent_binary_source = "/fixtures/node-agent"
        vpn_node_xray_binary_source = "/fixtures/xray"
        vpn_node_mtls_ca_source = "/fixtures/ca.pem"
        vpn_node_mtls_cert_source = "/fixtures/tls.crt"
        vpn_node_mtls_key_source = "/fixtures/tls.key"
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
    & docker exec $container ansible-playbook playbooks/vpn-node.yml --extra-vars "@/fixtures/vars.json"
    if ($LASTEXITCODE -ne 0) { throw "first Ansible convergence failed" }
    $second = & docker exec $container ansible-playbook playbooks/vpn-node.yml --extra-vars "@/fixtures/vars.json"
    if ($LASTEXITCODE -ne 0) { throw "second Ansible convergence failed" }
    if ("$second" -notmatch "changed=0\s+unreachable=0\s+failed=0") {
        throw "Ansible role is not idempotent"
    }
    & docker exec $container sh /workspace/deploy/ansible/tests/assert.sh
    if ($LASTEXITCODE -ne 0) { throw "hardened node assertions failed" }
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

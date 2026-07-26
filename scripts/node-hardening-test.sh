#!/usr/bin/env bash
set -euo pipefail

repo="$(cd "$(dirname "$0")/.." && pwd)"
suffix="$(date +%s)-$$"
artifact_dir="${repo}/tmp/node-hardening-${suffix}"
container="vpn-node-hardening-${suffix}"
image="vpn-service/ansible-node-test:local"
mkdir -p "$artifact_dir"

cleanup() {
  status=$?
  trap - EXIT
  cleanup_failed=0
  if docker ps -aq --filter "name=^/${container}$" | grep -q .; then
    docker rm -f "$container" >/dev/null 2>&1 || cleanup_failed=1
  fi
  if docker images -q "$image" | grep -q .; then
    docker image rm "$image" >/dev/null 2>&1 || cleanup_failed=1
  fi
  case "$artifact_dir" in
    "$repo"/tmp/node-hardening-*) rm -rf -- "$artifact_dir" || cleanup_failed=1 ;;
    *) cleanup_failed=1 ;;
  esac
  [[ ! -e "$artifact_dir" ]] || cleanup_failed=1
  if (( cleanup_failed != 0 )); then
    echo "node hardening cleanup failed" >&2
    exit 1
  fi
  exit "$status"
}
trap cleanup EXIT

printf '#!/bin/sh\nexit 0\n' >"${artifact_dir}/node-agent"
printf '#!/bin/sh\nexit 0\n' >"${artifact_dir}/xray"
printf 'local-disposable-ca\n' >"${artifact_dir}/ca.pem"
printf 'local-disposable-cert\n' >"${artifact_dir}/tls.crt"
head -c 32 /dev/urandom | base64 >"${artifact_dir}/tls.key"
head -c 32 /dev/urandom | base64 | tr '+/' '-_' | tr -d '=' >"${artifact_dir}/reality.key"
head -c 32 /dev/urandom | base64 >"${artifact_dir}/wireguard.key"
peer_key="$(head -c 32 /dev/urandom | base64)"
cat >"${artifact_dir}/vars.json" <<EOF
{
  "vpn_node_test_mode": true,
  "vpn_node_manage_services": false,
  "vpn_node_id": "61000000-0000-4000-8000-000000000001",
  "vpn_node_reality_target": "example.invalid:443",
  "vpn_node_reality_server_names": ["example.invalid"],
  "vpn_node_reality_short_ids": ["0011"],
  "vpn_node_wireguard_peer_public_key": "${peer_key}",
  "vpn_node_wireguard_peer_endpoint": "192.0.2.1:51820",
  "vpn_node_agent_binary_source": "/fixtures/node-agent",
  "vpn_node_xray_binary_source": "/fixtures/xray",
  "vpn_node_mtls_ca_source": "/fixtures/ca.pem",
  "vpn_node_mtls_cert_source": "/fixtures/tls.crt",
  "vpn_node_mtls_key_source": "/fixtures/tls.key",
  "vpn_node_reality_private_key_source": "/fixtures/reality.key",
  "vpn_node_wireguard_private_key_source": "/fixtures/wireguard.key"
}
EOF

docker build -f deploy/ansible/tests/Dockerfile -t "$image" .
docker run -d --name "$container" --cap-add NET_ADMIN \
  -e ANSIBLE_CONFIG=/workspace/deploy/ansible/ansible.cfg \
  -e ANSIBLE_ROLES_PATH=/workspace/deploy/ansible/roles \
  -v "${repo}:/workspace:ro" -v "${artifact_dir}:/fixtures:ro" "$image" >/dev/null
docker exec "$container" ansible-playbook --syntax-check playbooks/vpn-node.yml --extra-vars @/fixtures/vars.json
docker exec "$container" ansible-playbook playbooks/vpn-node.yml --extra-vars @/fixtures/vars.json
second="$(docker exec "$container" ansible-playbook playbooks/vpn-node.yml --extra-vars @/fixtures/vars.json)"
grep -Eq 'changed=0 +unreachable=0 +failed=0' <<<"$second"
docker exec "$container" sh /workspace/deploy/ansible/tests/assert.sh

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

CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
  -o "${artifact_dir}/node-agent" ./services/node-agent/cmd/node-agent
go run -mod=readonly ./tools/devmtls/cmd/devmtls "${artifact_dir}/mtls" >/dev/null
cat >"${artifact_dir}/xray" <<'EOF'
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
EOF
head -c 32 /dev/urandom | base64 | tr '+/' '-_' | tr -d '=' >"${artifact_dir}/reality.key"
head -c 32 /dev/urandom | base64 >"${artifact_dir}/wireguard.key"
peer_key="$(head -c 32 /dev/urandom | base64)"
cat >"${artifact_dir}/vars.json" <<EOF
{
  "vpn_node_test_mode": true,
  "vpn_node_manage_services": true,
  "vpn_node_environment": "local",
  "vpn_node_mtls_namespace": "local",
  "vpn_node_wireguard_address": "127.0.0.1/32",
  "vpn_node_id": "61000000-0000-4000-8000-000000000001",
  "vpn_node_reality_target": "example.invalid:443",
  "vpn_node_reality_server_names": ["example.invalid"],
  "vpn_node_reality_short_ids": ["0011"],
  "vpn_node_wireguard_peer_public_key": "${peer_key}",
  "vpn_node_wireguard_peer_endpoint": "192.0.2.1:51820",
  "vpn_node_agent_binary_source": "/fixtures/node-agent",
  "vpn_node_xray_binary_source": "/fixtures/xray",
  "vpn_node_mtls_ca_source": "/fixtures/mtls/ca.crt",
  "vpn_node_mtls_cert_source": "/fixtures/mtls/node-agent-primary.crt",
  "vpn_node_mtls_key_source": "/fixtures/mtls/node-agent-primary.key",
  "vpn_node_reality_private_key_source": "/fixtures/reality.key",
  "vpn_node_wireguard_private_key_source": "/fixtures/wireguard.key"
}
EOF

docker build -f deploy/ansible/tests/Dockerfile -t "$image" .
docker run -d --name "$container" --privileged --cgroupns=host \
  --tmpfs /run --tmpfs /run/lock -v /sys/fs/cgroup:/sys/fs/cgroup:rw \
  -e ANSIBLE_CONFIG=/workspace/deploy/ansible/ansible.cfg \
  -e ANSIBLE_ROLES_PATH=/workspace/deploy/ansible/roles \
  -v "${repo}:/workspace:ro" -v "${artifact_dir}:/fixtures:ro" "$image" >/dev/null
for _ in $(seq 1 30); do
  if docker exec "$container" systemctl show --property=Version >/dev/null 2>&1; then break; fi
  sleep 0.5
done
docker exec "$container" systemctl show --property=Version >/dev/null
docker exec "$container" ansible-playbook --syntax-check playbooks/vpn-node.yml --extra-vars @/fixtures/vars.json
docker exec "$container" touch /run/reject-xray-candidate
if docker exec "$container" ansible-playbook playbooks/vpn-node.yml --extra-vars @/fixtures/vars.json; then
  echo "invalid initial Xray candidate unexpectedly converged" >&2
  exit 1
fi
docker exec "$container" test ! -e /etc/vpn-node/xray/config.json
if docker exec "$container" systemctl is-active --quiet xray.service; then
  echo "Xray started without a valid initial configuration" >&2
  exit 1
fi
docker exec "$container" rm /run/reject-xray-candidate
docker exec "$container" systemctl stop node-agent.service
docker exec "$container" systemctl reset-failed node-agent.service
docker exec "$container" ansible-playbook playbooks/vpn-node.yml --extra-vars @/fixtures/vars.json
second="$(docker exec "$container" ansible-playbook playbooks/vpn-node.yml --extra-vars @/fixtures/vars.json)"
grep -Eq 'changed=0 +unreachable=0 +failed=0' <<<"$second"
docker exec "$container" sh /workspace/deploy/ansible/tests/assert.sh
before="$(docker exec "$container" sha256sum /etc/vpn-node/xray/config.json | cut -d' ' -f1)"
docker exec "$container" touch /run/reject-xray-candidate
docker exec "$container" systemctl restart node-agent.service || true
sleep 1
if docker exec "$container" systemctl is-active --quiet node-agent.service; then
  echo "node-agent remained active after an invalid candidate" >&2
  exit 1
fi
docker exec "$container" systemctl stop node-agent.service
docker exec "$container" systemctl is-active --quiet xray.service
after="$(docker exec "$container" sha256sum /etc/vpn-node/xray/config.json | cut -d' ' -f1)"
test "$before" = "$after"
docker exec "$container" rm /run/reject-xray-candidate
docker exec "$container" systemctl stop node-agent.service
docker exec "$container" systemctl reset-failed node-agent.service
docker exec "$container" systemctl start node-agent.service
docker restart "$container" >/dev/null
for _ in $(seq 1 30); do
  if docker exec "$container" systemctl is-active --quiet node-agent.service 2>/dev/null && \
     docker exec "$container" systemctl is-active --quiet xray.service 2>/dev/null; then break; fi
  sleep 0.5
done
docker exec "$container" systemctl is-active --quiet node-agent.service
docker exec "$container" systemctl is-active --quiet xray.service

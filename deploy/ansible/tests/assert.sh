#!/bin/sh
set -eu

test "$(stat -c '%U:%G:%a' /etc/vpn-node/credentials/node-agent/tls.key)" = "root:vpn-node-agent:440"
test "$(stat -c '%U:%G:%a' /etc/vpn-node/credentials/xray/reality.key)" = "root:vpn-xray:440"
test "$(stat -c '%U:%G:%a' /etc/wireguard/wg0.conf)" = "root:root:600"
test "$(stat -c '%U:%G:%a' /etc/vpn-node)" = "root:vpn-xray:750"
test "$(stat -c '%U:%G:%a' /etc/vpn-node/credentials)" = "root:vpn-xray:750"
test "$(stat -c '%U:%G:%a' /etc/vpn-node/credentials/node-agent)" = "root:vpn-node-agent:750"
test "$(stat -c '%U:%G:%a' /etc/vpn-node/xray)" = "vpn-node-agent:vpn-xray:2770"
test "$(id -u vpn-node-agent)" != "$(id -u xray)"
id -Gn vpn-node-agent | grep -qw vpn-xray
id -Gn xray | grep -qw vpn-xray
sudo -u vpn-node-agent -- test -r /etc/vpn-node/credentials/node-agent/tls.key
sudo -u vpn-node-agent -- test -r /etc/vpn-node/credentials/xray/reality.key
sudo -u xray -- test -r /etc/vpn-node/credentials/xray/reality.key
sudo -u vpn-node-agent -- sh -c 'printf "%s\n" "{}" > /etc/vpn-node/xray/config.json'
sudo -u xray -- test -r /etc/vpn-node/xray/config.json
grep -q '^User=vpn-node-agent$' /etc/systemd/system/node-agent.service
grep -q '^Group=vpn-node-agent$' /etc/systemd/system/node-agent.service
grep -q '^SupplementaryGroups=vpn-xray$' /etc/systemd/system/node-agent.service
grep -q '^User=xray$' /etc/systemd/system/xray.service
grep -q '^Group=xray$' /etc/systemd/system/xray.service
grep -q '^SupplementaryGroups=vpn-xray$' /etc/systemd/system/xray.service
grep -q '^CapabilityBoundingSet=CAP_NET_BIND_SERVICE$' /etc/systemd/system/xray.service
grep -q '^CapabilityBoundingSet=$' /etc/systemd/system/node-agent.service
grep -q 'policy drop' /etc/nftables.d/vpn-node.nft
! grep -q 'flush ruleset' /etc/nftables.d/vpn-node.nft
test "$(grep -c 'iifname "wg0".*tcp dport' /etc/nftables.d/vpn-node.nft)" = "2"
grep -q '^XRAY_MANAGER_MODE=systemd$' /etc/vpn-node/node-agent.env
/usr/sbin/visudo -cf /etc/sudoers.d/vpn-node-agent-xray >/dev/null
/usr/bin/systemd-analyze verify /etc/systemd/system/node-agent.service /etc/systemd/system/xray.service
/usr/sbin/nft add table inet preexisting
/usr/sbin/nft -f /etc/nftables.d/vpn-node.nft
/usr/sbin/nft -f /etc/nftables.d/vpn-node.nft
/usr/sbin/nft list table inet preexisting >/dev/null
/usr/bin/systemctl daemon-reload
/usr/bin/systemctl start xray.service node-agent.service
/usr/bin/systemctl is-active --quiet xray.service
/usr/bin/systemctl is-active --quiet node-agent.service

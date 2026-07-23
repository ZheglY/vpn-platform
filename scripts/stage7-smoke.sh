#!/usr/bin/env bash
set -euo pipefail

export VPN_SMOKE_FULL_CONTROL_PLANE=1
export STAGE7_EXTENDED_SMOKE=1
export COMPOSE_PROFILES=core,app,vpn

bash "$(dirname "$0")/compose-smoke.sh"

echo 'Stage 7 smoke passed: durable notifications, Telegram failure handling, admin mTLS/RBAC/idempotency/audit, restart recovery, stale suppression, and Stage 6 VPN lifecycle.'

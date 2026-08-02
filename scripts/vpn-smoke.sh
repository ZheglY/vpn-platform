#!/usr/bin/env bash
set -euo pipefail

# Full Stage 6 path: access-service -> Kafka -> provisioning-service ->
# subscription-service/access-service mTLS -> node agents -> Xray -> Kafka -> access-service.
export VPN_SMOKE_FULL_CONTROL_PLANE=1
export COMPOSE_PROFILES=core,app,vpn

bash "$(dirname "$0")/compose-smoke.sh"

echo 'Stage 6 VPN smoke passed: access.provision.request.v1 traversed Kafka, both node agents, real Xray, Access readiness, Happ delivery, and revoke.'

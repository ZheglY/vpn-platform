#!/usr/bin/env bash
set -euo pipefail

export OBSERVABILITY_SMOKE=1
bash "$(dirname "$0")/compose-smoke.sh"

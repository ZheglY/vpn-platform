#!/usr/bin/env bash
set -euo pipefail

export OBSERVABILITY_SMOKE=1
export OTEL_TRACES_SAMPLER_ARG=1
bash "$(dirname "$0")/compose-smoke.sh"

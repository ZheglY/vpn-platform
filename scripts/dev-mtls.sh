#!/usr/bin/env bash
set -euo pipefail

go run ./tools/devmtls/cmd/devmtls "${1:-secrets/dev-mtls}"

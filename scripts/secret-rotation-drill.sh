#!/usr/bin/env bash
set -euo pipefail

repo="$(cd "$(dirname "$0")/.." && pwd)"
cd "$repo"
go run -mod=readonly ./tools/rotationdrill
go test -mod=readonly ./services/access/internal/credential -run 'KeyringSupportsOverlap|TokenHasherKeyring'
go test -mod=readonly ./services/node-agent/internal/xray -run SystemdManagerRestoresLastKnownGood
go test -mod=readonly ./services/billing/internal/yookassa ./services/telegram-bot/internal/telegram ./services/notification/internal/telegram

#!/usr/bin/env bash
set -euo pipefail

repo="$(cd "$(dirname "$0")/.." && pwd)"
tmp="${repo}/tmp"
[[ -d "$tmp" ]] || exit 0

if find "$tmp" -type f \( -name '*.agekey' -o -name '*.pem' -o -name '*.key' \) -print -quit | grep -q .; then
  echo 'filesystem secret scan found a sensitive temporary artifact' >&2
  exit 1
fi
if rg -l --hidden --glob '!*.dump.age' \
  'AGE-SECRET-KEY-1|-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----|vless://' "$tmp" >/dev/null; then
  echo 'filesystem secret scan found sensitive content in temporary artifacts' >&2
  exit 1
fi

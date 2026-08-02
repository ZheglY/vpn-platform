#!/usr/bin/env bash
set -euo pipefail

repo="$(cd "$(dirname "$0")/.." && pwd)"
mkdir -p "${repo}/tmp"
if compgen -G "${repo}/tmp/backup-drill-*" >/dev/null; then
  echo 'backup cleanup test requires a clean tmp directory' >&2
  exit 1
fi
if BACKUP_DRILL_FAIL_AFTER_KEYGEN=1 bash "${repo}/scripts/backup-restore-drill.sh"; then
  echo 'forced backup drill failure unexpectedly succeeded' >&2
  exit 1
fi
if compgen -G "${repo}/tmp/backup-drill-*" >/dev/null; then
  echo 'backup drill left temporary artifacts after forced keygen failure' >&2
  exit 1
fi

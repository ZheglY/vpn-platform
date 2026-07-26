#!/usr/bin/env bash
set -euo pipefail

repo="$(cd "$(dirname "$0")/.." && pwd)"
mkdir -p "${repo}/tmp"
if compgen -G "${repo}/tmp/backup-drill-*" >/dev/null; then
  echo 'stale backup drill artifacts detected; cleanup is required before key generation' >&2
  exit 1
fi
suffix="$(date +%s)-$$"
project="vpn-backup-drill-${suffix}"
artifact_dir="${repo}/tmp/backup-drill-${suffix}"
last_report="${repo}/tmp/backup-restore-last-report.json"
profiles=(--profile core --profile app --profile maintenance)
migrations=(
  identity-migrate catalog-migrate billing-migrate subscription-migrate
  access-migrate provisioning-migrate notification-migrate admin-migrate
)
databases=(
  'identity_service|identity_app|drill-identity'
  'catalog_service|catalog_app|drill-catalog'
  'billing_service|billing_app|drill-billing'
  'subscription_service|subscription_app|drill-subscription'
  'access_service|access_app|drill-access'
  'provisioning_service|provisioning_app|drill-provisioning'
  'notification_service|notification_app|drill-notification'
  'admin_service|admin_migrator|drill-admin-migrator'
)

mkdir -p "$artifact_dir"
export POSTGRES_USER=drill_admin POSTGRES_PASSWORD=drill-control-password POSTGRES_DB=drill_control POSTGRES_PORT=0
export REDIS_PASSWORD=drill-redis KAFKA_PORT=0
export IDENTITY_DB_PASSWORD=drill-identity CATALOG_DB_PASSWORD=drill-catalog
export BILLING_DB_PASSWORD=drill-billing SUBSCRIPTION_DB_PASSWORD=drill-subscription
export ACCESS_DB_PASSWORD=drill-access PROVISIONING_DB_PASSWORD=drill-provisioning
export NOTIFICATION_DB_PASSWORD=drill-notification ADMIN_DB_PASSWORD=drill-admin
export ADMIN_MIGRATOR_DB_PASSWORD=drill-admin-migrator
export ACCESS_CREDENTIAL_KEY_BASE64=MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=
export ACCESS_TOKEN_HMAC_KEY_BASE64=ZmVkY2JhOTg3NjU0MzIxMGZlZGNiYTk4NzY1NDMyMTA=
export SUBSCRIPTION_PUBLIC_BASE_URL=https://example.invalid
export TELEGRAM_WEBHOOK_SECRET=drill-webhook TELEGRAM_BOT_TOKEN=drill-bot
export TERMS_URL=https://example.invalid/terms YOOKASSA_SECRET_KEY=drill-yookassa
export PAYMENT_RETURN_URL=https://example.invalid/return
export BACKUP_ARTIFACT_DIR="$artifact_dir" BACKUP_UID="$(id -u)" BACKUP_GID="$(id -g)"
export RESTORE_POSTGRES_USER=restore_admin RESTORE_POSTGRES_PASSWORD=restore-control-password RESTORE_POSTGRES_DB=restore_control
export COMPOSE_PARALLEL_LIMIT="${COMPOSE_PARALLEL_LIMIT:-2}" COMPOSE_BAKE=false

compose() {
  docker compose -p "$project" "${profiles[@]}" "$@"
}

cleanup() {
  original_status=$?
  trap - EXIT
  cleanup_status=0
  if ! compose down -v --remove-orphans >/dev/null 2>&1; then
    cleanup_status=1
  fi
  case "$artifact_dir" in
    "$repo"/tmp/backup-drill-*) rm -rf -- "$artifact_dir" || cleanup_status=1 ;;
    *) cleanup_status=1 ;;
  esac
  if [[ -e "$artifact_dir" ]]; then
    cleanup_status=1
  fi
  if (( cleanup_status != 0 )); then
    echo 'backup drill cleanup failed' >&2
    exit 1
  fi
  exit "$original_status"
}
trap cleanup EXIT

compose build backup-tool
compose run --rm --no-deps backup-tool keygen \
  --identity /backups/drill.agekey --recipient /backups/drill.recipient
if [[ "${BACKUP_DRILL_FAIL_AFTER_KEYGEN:-0}" == 1 ]]; then
  echo 'forced failure after backup key generation' >&2
  exit 1
fi
compose up -d --wait postgres
compose up --build "${migrations[@]}"

for entry in "${databases[@]}"; do
  IFS='|' read -r database owner password <<<"$entry"
  export BACKUP_DATABASE_URL="postgres://${owner}:${password}@postgres:5432/${database}?sslmode=disable"
  compose run --rm --no-deps backup-tool backup \
    --database "$database" --owner "$owner" \
    --recipient /backups/drill.recipient \
    --output "/backups/${database}.dump.age" \
    --metadata "/backups/${database}.metadata.json" \
    --inspection "/backups/${database}.source.json"
done

restore_started="$(date +%s)"
compose up -d --wait restore-postgres
export BACKUP_DATABASE_URL='postgres://identity_app:drill-identity@restore-postgres:5432/identity_service?sslmode=disable'
compose exec -T -e PGPASSWORD=drill-identity restore-postgres \
  psql -U identity_app -d identity_service -v ON_ERROR_STOP=1 \
  -c 'CREATE TABLE restore_preflight_sentinel (id integer PRIMARY KEY)'
if compose run --rm --no-deps backup-tool restore \
  --identity /backups/drill.agekey \
  --input /backups/identity_service.dump.age \
  --metadata /backups/identity_service.metadata.json; then
  echo 'restore unexpectedly accepted a nonempty target' >&2
  exit 1
fi
sentinel="$(compose exec -T -e PGPASSWORD=drill-identity restore-postgres \
  psql -U identity_app -d identity_service -Atqc \
  "SELECT to_regclass('public.restore_preflight_sentinel') IS NOT NULL")"
if [[ "$sentinel" != t ]]; then
  echo 'restore preflight did not preserve the nonempty target' >&2
  exit 1
fi
compose exec -T -e PGPASSWORD=drill-identity restore-postgres \
  psql -U identity_app -d identity_service -v ON_ERROR_STOP=1 \
  -c 'DROP TABLE restore_preflight_sentinel'
for entry in "${databases[@]}"; do
  IFS='|' read -r database owner password <<<"$entry"
  export BACKUP_DATABASE_URL="postgres://${owner}:${password}@restore-postgres:5432/${database}?sslmode=disable"
  compose run --rm --no-deps backup-tool restore \
    --identity /backups/drill.agekey \
    --input "/backups/${database}.dump.age" \
    --metadata "/backups/${database}.metadata.json"
  compose run --rm --no-deps backup-tool inspect \
    --database "$database" --owner "$owner" \
    --output "/backups/${database}.target.json"
  compose run --rm --no-deps backup-tool compare \
    --source "/backups/${database}.source.json" \
    --target "/backups/${database}.target.json"
done
completed_at="$(date +%s)"
rto_seconds="$((completed_at - restore_started))"
if (( rto_seconds > 1800 )); then
  echo 'restore RTO evidence exceeded 30 minutes' >&2
  exit 1
fi

node - "$artifact_dir" "$last_report" "$restore_started" "$rto_seconds" <<'NODE'
const fs = require('fs');
const path = require('path');
const [directory, lastReport, restoreStartedRaw, rtoRaw] = process.argv.slice(2);
const metadata = fs.readdirSync(directory)
  .filter(name => name.endsWith('.metadata.json'))
  .map(name => JSON.parse(fs.readFileSync(path.join(directory, name), 'utf8')));
const restoreStarted = Number(restoreStartedRaw);
const oldest = Math.min(...metadata.map(item => Date.parse(item.created_at) / 1000));
const rpo = Math.max(0, restoreStarted - oldest);
if (metadata.length !== 8 || rpo > 86400) {
  throw new Error('backup artifact count or RPO evidence is invalid');
}
const report = {
  format_version: 1,
  result: 'passed',
  completed_at: new Date().toISOString(),
  database_count: 8,
  encrypted_artifacts: metadata.length,
  integrity: 'ciphertext_sha256_exported_snapshot_and_table_row_counts_match',
  ownership: 'all_public_relations_match_service_owner',
  rpo_target_seconds: 86400,
  observed_rpo_seconds: rpo,
  rto_target_seconds: 1800,
  observed_rto_seconds: Number(rtoRaw),
};
const body = JSON.stringify(report, null, 2) + '\n';
fs.writeFileSync(path.join(directory, 'drill-report.json'), body, { mode: 0o600 });
fs.writeFileSync(lastReport, body, { mode: 0o600 });
process.stdout.write(body);
NODE

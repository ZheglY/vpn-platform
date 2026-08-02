#!/usr/bin/env sh
set -eu

: "${NOTIFICATION_DB_PASSWORD:?NOTIFICATION_DB_PASSWORD is required}"
: "${ADMIN_DB_PASSWORD:?ADMIN_DB_PASSWORD is required}"
: "${ADMIN_MIGRATOR_DB_PASSWORD:?ADMIN_MIGRATOR_DB_PASSWORD is required}"

psql -v ON_ERROR_STOP=1 \
  -v notification_password="$NOTIFICATION_DB_PASSWORD" \
  -v admin_password="$ADMIN_DB_PASSWORD" \
  -v admin_migrator_password="$ADMIN_MIGRATOR_DB_PASSWORD" \
  --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<-EOSQL
SELECT format('CREATE ROLE notification_app LOGIN PASSWORD %L', :'notification_password')
WHERE NOT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = 'notification_app')\gexec
SELECT format('ALTER ROLE notification_app WITH PASSWORD %L', :'notification_password')\gexec
SELECT 'CREATE DATABASE notification_service OWNER notification_app'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'notification_service')\gexec
GRANT ALL PRIVILEGES ON DATABASE notification_service TO notification_app;

SELECT format('CREATE ROLE admin_migrator LOGIN PASSWORD %L', :'admin_migrator_password')
WHERE NOT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = 'admin_migrator')\gexec
SELECT format('ALTER ROLE admin_migrator WITH PASSWORD %L', :'admin_migrator_password')\gexec
SELECT format('CREATE ROLE admin_app LOGIN PASSWORD %L', :'admin_password')
WHERE NOT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = 'admin_app')\gexec
SELECT format('ALTER ROLE admin_app WITH PASSWORD %L', :'admin_password')\gexec
SELECT 'CREATE DATABASE admin_service OWNER admin_migrator'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'admin_service')\gexec
GRANT CONNECT ON DATABASE admin_service TO admin_app;
EOSQL

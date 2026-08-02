#!/usr/bin/env sh
set -eu

: "${PROVISIONING_DB_PASSWORD:?PROVISIONING_DB_PASSWORD is required}"

psql -v ON_ERROR_STOP=1 -v provisioning_password="$PROVISIONING_DB_PASSWORD" --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<-EOSQL
SELECT format('CREATE ROLE provisioning_app LOGIN PASSWORD %L', :'provisioning_password')
WHERE NOT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = 'provisioning_app')\gexec
SELECT format('ALTER ROLE provisioning_app WITH PASSWORD %L', :'provisioning_password')\gexec
SELECT 'CREATE DATABASE provisioning_service OWNER provisioning_app'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'provisioning_service')\gexec
GRANT ALL PRIVILEGES ON DATABASE provisioning_service TO provisioning_app;
EOSQL

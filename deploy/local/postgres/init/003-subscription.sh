#!/usr/bin/env sh
set -eu

: "${SUBSCRIPTION_DB_PASSWORD:?SUBSCRIPTION_DB_PASSWORD is required}"

psql -v ON_ERROR_STOP=1 -v subscription_password="$SUBSCRIPTION_DB_PASSWORD" --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<-EOSQL
SELECT format('CREATE ROLE subscription_app LOGIN PASSWORD %L', :'subscription_password')
WHERE NOT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = 'subscription_app')\gexec
SELECT format('ALTER ROLE subscription_app WITH PASSWORD %L', :'subscription_password')\gexec
SELECT 'CREATE DATABASE subscription_service OWNER subscription_app'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'subscription_service')\gexec
GRANT ALL PRIVILEGES ON DATABASE subscription_service TO subscription_app;
EOSQL

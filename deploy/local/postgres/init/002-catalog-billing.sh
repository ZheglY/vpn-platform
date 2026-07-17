#!/usr/bin/env sh
set -eu

: "${CATALOG_DB_PASSWORD:?CATALOG_DB_PASSWORD is required}"
: "${BILLING_DB_PASSWORD:?BILLING_DB_PASSWORD is required}"

psql -v ON_ERROR_STOP=1 -v catalog_password="$CATALOG_DB_PASSWORD" -v billing_password="$BILLING_DB_PASSWORD" --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<-EOSQL
SELECT format('CREATE ROLE catalog_app LOGIN PASSWORD %L', :'catalog_password')
WHERE NOT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = 'catalog_app')\gexec
SELECT format('ALTER ROLE catalog_app WITH PASSWORD %L', :'catalog_password')\gexec
SELECT 'CREATE DATABASE catalog_service OWNER catalog_app'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'catalog_service')\gexec
GRANT ALL PRIVILEGES ON DATABASE catalog_service TO catalog_app;

SELECT format('CREATE ROLE billing_app LOGIN PASSWORD %L', :'billing_password')
WHERE NOT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = 'billing_app')\gexec
SELECT format('ALTER ROLE billing_app WITH PASSWORD %L', :'billing_password')\gexec
SELECT 'CREATE DATABASE billing_service OWNER billing_app'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'billing_service')\gexec
GRANT ALL PRIVILEGES ON DATABASE billing_service TO billing_app;
EOSQL

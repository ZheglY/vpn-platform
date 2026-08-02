#!/usr/bin/env sh
set -eu

: "${ACCESS_DB_PASSWORD:?ACCESS_DB_PASSWORD is required}"

psql -v ON_ERROR_STOP=1 -v access_password="$ACCESS_DB_PASSWORD" --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<-EOSQL
SELECT format('CREATE ROLE access_app LOGIN PASSWORD %L', :'access_password')
WHERE NOT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = 'access_app')\gexec
SELECT format('ALTER ROLE access_app WITH PASSWORD %L', :'access_password')\gexec
SELECT 'CREATE DATABASE access_service OWNER access_app'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'access_service')\gexec
GRANT ALL PRIVILEGES ON DATABASE access_service TO access_app;
EOSQL

#!/usr/bin/env sh
set -eu

: "${IDENTITY_DB_PASSWORD:?IDENTITY_DB_PASSWORD is required}"

psql -v ON_ERROR_STOP=1 -v identity_password="$IDENTITY_DB_PASSWORD" --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<-EOSQL
SELECT format('CREATE ROLE identity_app LOGIN PASSWORD %L', :'identity_password')
WHERE NOT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = 'identity_app')\gexec

SELECT format('ALTER ROLE identity_app WITH PASSWORD %L', :'identity_password')\gexec

SELECT 'CREATE DATABASE identity_service OWNER identity_app'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'identity_service')\gexec

GRANT ALL PRIVILEGES ON DATABASE identity_service TO identity_app;
EOSQL

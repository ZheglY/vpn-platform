-- +goose Up
CREATE TABLE users (
    id uuid PRIMARY KEY,
    status text NOT NULL CHECK (status IN ('active', 'blocked', 'deleted')),
    locale text NULL CHECK (locale IS NULL OR length(locale) <= 32),
    timezone text NULL CHECK (timezone IS NULL OR length(timezone) <= 64),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz NULL
);

CREATE TABLE telegram_identities (
    user_id uuid PRIMARY KEY REFERENCES users(id) ON DELETE RESTRICT,
    telegram_user_id bigint NOT NULL UNIQUE CHECK (telegram_user_id > 0),
    username text NULL CHECK (username IS NULL OR length(username) <= 64),
    display_name text NULL CHECK (display_name IS NULL OR length(display_name) <= 256),
    language_code text NULL CHECK (language_code IS NULL OR length(language_code) <= 16),
    first_seen_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE consents (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    document_type text NOT NULL CHECK (length(document_type) BETWEEN 1 AND 64),
    document_version text NOT NULL CHECK (length(document_version) BETWEEN 1 AND 64),
    source text NOT NULL CHECK (length(source) BETWEEN 1 AND 64),
    accepted_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, document_type, document_version)
);

CREATE INDEX consents_user_id_idx ON consents (user_id);

-- +goose Down
DROP TABLE IF EXISTS consents;
DROP TABLE IF EXISTS telegram_identities;
DROP TABLE IF EXISTS users;

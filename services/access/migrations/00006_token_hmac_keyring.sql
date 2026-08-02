-- +goose Up
ALTER TABLE subscription_tokens
ADD COLUMN token_hmac_key_version integer NOT NULL DEFAULT 1
CHECK (token_hmac_key_version > 0);

-- +goose Down
ALTER TABLE subscription_tokens DROP COLUMN token_hmac_key_version;

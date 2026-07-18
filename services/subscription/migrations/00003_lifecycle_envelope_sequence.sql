-- +goose Up
UPDATE outbox
SET payload = jsonb_set(payload, '{aggregate_sequence}', to_jsonb(aggregate_sequence), true)
WHERE topic IN (
    'subscription.activated.v1',
    'subscription.extended.v1',
    'subscription.expired.v1',
    'subscription.revoked.v1'
);

-- +goose Down
UPDATE outbox
SET payload = payload - 'aggregate_sequence'
WHERE topic IN (
    'subscription.activated.v1',
    'subscription.extended.v1',
    'subscription.expired.v1',
    'subscription.revoked.v1'
);

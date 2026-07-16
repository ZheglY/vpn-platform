# Identity OpenAPI

Stage 2 identity behavior is documented in the root contract at `contracts/http/openapi.yaml`.

The implemented identity-service HTTP surfaces are:

- `PUT /internal/v1/telegram-users/{telegram_id}`
- `GET /internal/v1/users/{user_id}`
- `POST /internal/v1/users/{user_id}/consents`
- `GET /internal/v1/users/{user_id}/consents/{document_type}/{document_version}`

All identity business endpoints require mTLS service identity and strict JSON decoding for request bodies.

# Catalog API

The executable Stage 3 catalog operations are documented in `contracts/http/openapi.yaml`:

- `GET /v1/plans`
- `GET /internal/v1/plans/{plan_id}`

The internal operation requires the allowlisted `billing-service` mTLS identity.

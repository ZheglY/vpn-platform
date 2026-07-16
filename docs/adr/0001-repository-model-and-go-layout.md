# ADR 0001: Repository Model and Go Layout

Status: Accepted

Date: 2026-07-12

## Context

The platform is a portfolio-grade microservice system. A monorepository simplifies local development, CI, contract review, and documentation. The original specification suggested a root `cmd/` directory with service code under `services/<service>/internal/`, but Go `internal` import rules would prevent root commands from importing sibling service internals.

## Decision

Use a monorepository with one root `go.mod` for the first version.

Service binaries live inside each service:

```text
services/<service>/cmd/<binary>/main.go
services/<service>/internal/{domain,application,repository,transport}
services/<service>/migrations
services/<service>/openapi
services/<service>/Dockerfile
```

Shared technical helpers may live in root `internal/platform`, but only for infrastructure primitives such as config parsing, logging setup, HTTP lifecycle, PostgreSQL helpers, Kafka envelope/outbox helpers, observability, and crypto utilities.

`internal/platform` must not contain domain entities or business rules from any service.

## Consequences

- No `go.work` or per-service modules in v1.
- Go `internal` rules protect service implementation packages.
- Cross-service imports of another service's implementation are forbidden by convention, architecture tests, CI, and review.
- A later ADR may introduce multiple modules if repository scale or deployment isolation justifies it.

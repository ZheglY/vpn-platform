# ADR 0003: Database-Per-Service Isolation

Status: Accepted

Date: 2026-07-12

## Context

The platform has separate bounded contexts for identity, catalog, billing, subscription, access, provisioning, notification, and node-agent local state. Cross-service SQL would make service boundaries fictional and block independent deployment.

## Decision

Each service owns its schema, migrations, DB user, and data model.

In local development, one PostgreSQL instance is allowed, but services must use separate logical databases or schemas and separate credentials. Cross-schema joins and direct access to another service's tables are forbidden.

Services communicate through HTTP and Kafka contracts, not shared databases.

## Consequences

- Local Compose can remain lightweight.
- Production databases can be physically separated without changing service code.
- Reporting or admin views must use service APIs or explicitly designed read models, not ad hoc joins.
- Integration tests must run migrations from zero per service.

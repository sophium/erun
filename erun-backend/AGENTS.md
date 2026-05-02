# AGENTS.md

Module-area guidance for `erun-backend`. Follow the repository root `AGENTS.md` first, then apply this file for work in this subtree.

## Area Role

- `erun-backend` contains hosted backend components for ERun.
- Keep API transport behavior in `erun-backend-api`.
- Keep database schema, migration configuration, and schema evolution in `erun-backend-db`.
- Do not put CLI, MCP, or desktop transport logic in this area. Those transports should depend on shared client contracts in `erun-common` when they need backend functionality.

## Module Boundaries

- `erun-backend-api` may depend on `erun-common` for transport-neutral contracts when those contracts are shared with CLI or MCP callers.
- `erun-backend-api` must not import `erun-cli` or `erun-mcp`.
- `erun-backend-db` must not depend on API runtime code. Database schema changes should be usable by migration tooling without starting the API service.
- Keep tenant resolution rules consistent across backend modules: OIDC token issuer identifies the tenant, and backend data access must be scoped to that resolved tenant.

## Identity

- Externally visible identifiers must be UUIDv7 values.
- Keep internal database implementation details out of API-visible IDs.
- Use `UUID` database columns for externally visible IDs in shared schema. PostgreSQL enforces UUID values, while SQLite accepts the PostgreSQL type name through its affinity rules.

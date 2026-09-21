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
- Use PostgreSQL `UUID` database columns for externally visible IDs.

### Login names are organization-scoped

- **A login name is unique per organization, not per instance.** The
  `erun-zitadel` chart sets the instance Domain Policy's
  `user_login_must_be_domain` and `validate_org_domains` true, so the
  erun-shipped Zitadel suffixes a login name with its organization's primary
  domain and an organization cannot claim a domain it does not control. Left
  at Zitadel's own default of false, every tenant's users compete for one
  global namespace, and two tenants wanting the same name is an unavoidable
  collision rather than a policy choice. Covered by
  `erun-devops/k8s/erun-zitadel-chart_test.sh`.
- Zitadel reads those settings when core initialises the **first** instance.
  They shape a freshly provisioned instance and do not rewrite one that
  already exists, so enabling them on a live instance is deliberate operator
  work, not something a deploy performs.
- **A taken login name is a named error, never Zitadel's raw conflict.**
  `zitadel.ErrUsernameTaken` (concretely `*UsernameTakenError`, carrying the
  name the caller asked for) reaches the caller as `409 USERNAME_TAKEN`, from
  identity enrolment and invite acceptance alike. It is matched on Zitadel's
  `Errors.User.AlreadyExists` **message key** and not on the conflict status
  alone: the same endpoint returns a conflict for a colliding email or
  another uniqueness rule, and relabelling those sends the caller to change a
  name that was never the problem.

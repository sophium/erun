---
title: Identity administration
---

# Identity administration

> For the Operator view, see [Administering identity](/collaboration/identity-administration).

`/v1/identity/*` drives the platform's own IdP (Zitadel) Management API server-side, using an org-owner service-account credential the `erun-zitadel` chart provisions on every deployment and never exposes to a browser. It is the console's IdP-identity administration surface (issue #1209): enroll, list, deactivate, and reactivate identities, and read/update the org's login and password policy.

## Restricted to an OPERATIONS tenant

Every endpoint below is refused with `403` unless the caller's resolved tenant is `OPERATIONS` (`tenants.type`) — administering the platform's own IdP is not a `COMPANY` tenant's business. This check runs in addition to, not instead of, the normal `role_permissions` authorization every other endpoint uses (see [erun API protocol · Errors](/agent-reference/api-protocol#errors)).

## Not a generic proxy

Every operation is a named endpoint below, mapped to one specific Zitadel Management API call. There is no pass-through / raw-request endpoint: a generic proxy would inherit the org-owner credential's full authority and could not be reviewed operation by operation.

## Least-privilege decision

Zitadel's built-in `ORG_USER_MANAGER` role would scope user create/list/deactivate/reactivate more narrowly than org-owner. Org policy management (login policy, password complexity) has no built-in role short of org-owner, though. Minting a second, narrower machine-user credential for only the user-CRUD half was considered and rejected for this increment: it would shrink the blast radius for half the surface while adding a second bootstrap-managed credential to operate, and the compensating control — an enumerated, non-proxying endpoint surface (previous section) — already applies uniformly. The single org-owner credential is used for the whole surface; this is a recorded decision, not a default.

## `GET /v1/identity/users`

Lists every identity (human and machine) the platform's IdP knows about.

```jsonc
// 200 response
[
  {
    "id": "387728394274144259",       // the IdP's own user id — this is the `subject` a token from this issuer presents
    "username": "alice",
    "state": "USER_STATE_ACTIVE",     // Zitadel's own USER_STATE_* value, forwarded verbatim
    "email": "alice@example.com",
    "firstName": "Alice",
    "lastName": "Operator"
  }
]
```

`email`/`firstName`/`lastName` are omitted for a machine user (e.g. the platform's own `admin-sa`/`login-client` service accounts, which this list includes).

## `POST /v1/identity/users`

Enrolls a new identity: creates it in the IdP (Zitadel's invite flow — no password is set here, so the enrollee receives an email to complete sign-in) and, only once that succeeds, creates the matching erun user and its `user_external_ids` mapping using the IdP's own returned id as `subject` and the caller's own token issuer as `issuer`.

```jsonc
// request body
{
  "username": "bob",
  "email": "bob@example.com",   // required
  "firstName": "Bob",           // optional
  "lastName": "Operator"        // optional
}

// 201 response — both halves landed
{
  "idpUser": { "id": "387728445393600515", "username": "bob", "state": "USER_STATE_INITIAL" },
  "erunUser": { "userId": "019a…", "username": "bob" }
}

// 201 response — the IdP identity was created, but the erun mapping failed
{
  "idpUser": { "id": "387728445393600515", "username": "bob", "state": "USER_STATE_INITIAL" },
  "error": "identity created in the identity provider but the erun user mapping failed: idp user id 387728445393600515: a user with this username already exists in the target tenant"
}
```

The IdP half is created first, since the erun mapping needs the subject the IdP assigns; a failure there means nothing was created and the response is an error (below), not a `201`. A failure in the erun half after the IdP identity exists is **not** an error response — it is a `201` with `error` set and no `erunUser`, naming the orphaned IdP user id so the operator can retry the mapping (`POST /v1/users` with that id as `subject`) rather than enroll a duplicate identity.

**Error behaviour.**

| Status | Condition | Recovery |
|---|---|---|
| `400` | `username`/`email` empty, or the body is not valid JSON. | Send both fields. |
| `403` | Caller's tenant is not `OPERATIONS`. | Call from an operations-tenant token. |
| Forwarded from Zitadel | The IdP call itself failed (e.g. a username already taken in the IdP). | The response body carries Zitadel's own message; act on it directly. |

## `POST /v1/identity/users/{external_id}/deactivate` and `.../reactivate`

`external_id` is the IdP's own user id (the `id` field from the list/enroll responses above — the same value a token from this issuer presents as `sub`). Deactivating blocks the identity's next sign-in immediately; reactivating reverses it. Both return `204` with an empty body on success.

**Error behaviour.** A Zitadel error is forwarded verbatim with its own status and message — the identity-state text is actionable on its own, for example:

| Status | Body (from Zitadel) | Condition | Recovery |
|---|---|---|---|
| `404` | `User with state initial can only be deleted not deactivated` | The identity has not completed its invite yet (`USER_STATE_INITIAL`). | Wait for the invite to complete, or delete the identity in Zitadel directly — this surface does not delete. |
| `404` | (Zitadel's not-found message) | `external_id` does not name a known identity. | Re-check the id from `GET /v1/identity/users`. |
| `403` | — | Caller's tenant is not `OPERATIONS`. | Call from an operations-tenant token. |

## `GET /v1/identity/org-settings`

Reads the org's current login policy, password complexity policy, and verified domains.

```jsonc
// 200 response
{
  "forceMfa": false,
  "minPasswordLength": 8,
  "passwordRequiresUppercase": true,
  "passwordRequiresLowercase": true,
  "passwordRequiresNumber": true,
  "passwordRequiresSymbol": false,
  "verifiedDomains": ["erun.example.com"]
}
```

`verifiedDomains` is read-only here — verifying a new domain is a DNS/HTTP challenge flow this surface does not drive.

## `PATCH /v1/identity/org-settings`

Applies only the fields present in the body; every other current value is preserved via a server-side read-modify-write against Zitadel (re-sending an unchanged value is skipped rather than sent, since Zitadel answers `400` for a write that carries no real diff). Returns the full settings after the update, same shape as the `GET` above.

```jsonc
// request body — change only forceMfa
{ "forceMfa": true }
```

**Error behaviour.**

| Status | Condition | Recovery |
|---|---|---|
| `400` | Body is not valid JSON. | Send a valid JSON object; every field is optional. |
| `403` | Caller's tenant is not `OPERATIONS`. | Call from an operations-tenant token. |
| Forwarded from Zitadel | The policy write itself failed. | The response body carries Zitadel's own message. |

## Audit

Every request above that reaches its handler (i.e. passed authentication, tenant resolution, and the `OPERATIONS`-tenant check) writes an `audit_events` row through the same request-scoped middleware every other protected endpoint uses — see [Audit log](/agent-reference/audit-log). No route-local audit code was added; registering these endpoints through the shared route registrar was enough for the middleware to see them.

## See also

- [Administering identity](/collaboration/identity-administration) — the Operator view.
- [erun API protocol](/agent-reference/api-protocol) — sign-in, tenant resolution, and the cross-cutting error model these endpoints share.
- [Audit log](/agent-reference/audit-log) — the event shape every mutation above writes.

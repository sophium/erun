---
title: Identity administration
---

# Identity administration

> For the Operator view, see [Administering identity](/collaboration/identity-administration).

`/v1/identity/*` drives the platform's own IdP (Zitadel) Management/Admin API server-side, using an org-owner service-account credential the `erun-zitadel` chart provisions on every deployment and never exposes to a browser. It is the console's IdP-identity administration surface (issue #1209): enroll, list, deactivate, and reactivate identities, read/update the org's login and password policy, and read/update the platform's outbound-mail (SMTP) configuration.

## Restricted to an OPERATIONS tenant

Every endpoint below is refused with `403` unless the caller's resolved tenant is `OPERATIONS` (`tenants.type`) — administering the platform's own IdP is not a `COMPANY` tenant's business. This check runs in addition to, not instead of, the normal `role_permissions` authorization every other endpoint uses (see [erun API protocol · Errors](/agent-reference/api-protocol#errors)).

## Not a generic proxy

Every operation is a named endpoint below, mapped to one specific Zitadel Management API call. There is no pass-through / raw-request endpoint: a generic proxy would inherit the org-owner credential's full authority and could not be reviewed operation by operation.

## Least-privilege decision

Zitadel's built-in `ORG_USER_MANAGER` role would scope user create/list/deactivate/reactivate more narrowly than org-owner. Org policy management (login policy, password complexity) has no built-in role short of org-owner, though. Minting a second, narrower machine-user credential for only the user-CRUD half was considered and rejected for this increment: it would shrink the blast radius for half the surface while adding a second bootstrap-managed credential to operate, and the compensating control — an enumerated, non-proxying endpoint surface (previous section) — already applies uniformly. The single org-owner credential is used for the whole surface; this is a recorded decision, not a default.

## Enrolling into another organization

`POST /v1/identity/users` takes an optional `orgId`. Without it the identity is created in the platform's own organization, as before. With it, the identity is created in **that** organization — the identity boundary another tenant resolves by.

```jsonc
// POST /v1/identity/users
{ "username": "alice", "email": "alice@example.com", "orgId": "388520359030161586" }
```

This is what makes a newly created tenant usable at all. A tenant's first admin arrives through the [per-tenant first-user bootstrap](/agent-reference/api-protocol#tenant-issuers) — the first valid token that resolves to it — and a token resolves to it only when its org claim is that tenant's org. Without `orgId` every enrollment lands in the platform's own org, so a fresh tenant can never receive such a token, never gets a first user, and can never own an environment: created, and inert.

**No erun user row is written for a cross-org enrollment, and that is not a failure.** The caller's tenant is not the new user's, and row-level security would file them under the wrong one. The response reports `mappingDeferred`, so a zero erun user there means "by design" rather than "the mapping failed" — the target tenant's own first-user bootstrap enrolls them, with full access, on their first sign-in.

Zitadel scopes a Management API call by an `x-zitadel-orgid` header, so the same org-owner credential acts in an org it did not create; no additional IdP privilege is involved.

## `POST /v1/identity/orgs`

Creates a Zitadel **organization** — the per-tenant identity boundary an org-scoped issuer resolves tenants by.

```jsonc
// POST /v1/identity/orgs body
{ "name": "validationagent" }

// 201 response
{ "id": "386994597030592700", "name": "validationagent" }
```

Why it exists: a platform's own IdP serves every tenant from one issuer, so tenants are told apart by an org claim (`urn:zitadel:iam:user:resourceowner:id`). Registering a second tenant therefore needs an org for its mapping to point at — and until this endpoint, erun could create the tenant and the issuer mapping and then had nowhere to point them, leaving a hand-made org in Zitadel's own console as the only way through. That is the third of the three gaps in the multi-tenant onboarding path; see [erun API protocol · first-identity bootstrap](/agent-reference/api-protocol#tenant-issuers) and [`PATCH /v1/tenant-issuers`](/agent-reference/api-protocol#patch-v1tenant-issuers).

The returned `id` is what you pass as `orgFieldValue` to [`POST /v1/tenants`](/agent-reference/api-protocol#post-v1tenants), or to `PATCH /v1/tenant-issuers` when converting a single-tenant issuer first.

It creates the org and stops there. It does **not** register an erun tenant, move the caller into the new org, or enrol anyone: those are separate resources with separate gates, and the new org's first erun caller becomes that tenant's admin through the per-tenant first-user bootstrap. The org-owner credential this surface uses stays scoped to the platform's own org.

| Status | Condition | Recovery |
|---|---|---|
| `201` | Org created. | — |
| `400` | `name` is empty or blank. | Send a name. |
| `403` | Caller's tenant is not `OPERATIONS`. | Call from an operations-tenant token. |

## `GET /v1/identity/users`

Lists every identity (human and machine) the platform's IdP knows about, cross-referenced against erun's own `users` table for the caller's tenant so the response distinguishes an enrolled tenant member from an identity that merely exists in the IdP — the fix for a self-registered account (when `allowRegister` was left open, or an account created before it was closed) rendering identically to an actual member.

```jsonc
// 200 response
[
  {
    "id": "387728394274144259",       // the IdP's own user id — this is the `subject` a token from this issuer presents
    "username": "alice",
    "state": "USER_STATE_ACTIVE",     // Zitadel's own USER_STATE_* value, forwarded verbatim
    "email": "alice@example.com",
    "firstName": "Alice",
    "lastName": "Operator",
    "isMachine": false,
    "enrolled": true,                  // true only when this subject also has a row in erun's own users table for this tenant
    "erunUserId": "019a…"              // present only when enrolled is true
  },
  {
    "id": "387728394274144999",
    "username": "stranger",
    "state": "USER_STATE_ACTIVE",
    "email": "stranger@example.com",
    "isMachine": false,
    "enrolled": false                  // exists in the IdP (e.g. self-registered) but is not a tenant member
  },
  {
    "id": "387728394274100001",
    "username": "admin-sa",
    "state": "USER_STATE_ACTIVE",
    "isMachine": true,                 // the platform's own service identity, never a tenant member
    "enrolled": false
  }
]
```

`email`/`firstName`/`lastName` are omitted for a machine user (e.g. the platform's own `admin-sa`/`login-client` service accounts, which this list includes — `isMachine: true` is how a client tells them apart from a human row with an unset email, and is also what the console uses to withhold the deactivate/reactivate control on those two rows).

## `POST /v1/identity/users`

Enrolls a new identity, and creates it differently depending on whether the platform can actually send mail (issue #1168):

- **Mail delivery configured** (see [`GET /v1/identity/smtp-settings`](#smtp-settings-get) below): the identity is created via Zitadel's invite flow — no password is set, so the enrollee receives an email to complete sign-in, and the IdP identity starts `USER_STATE_INITIAL`.
- **Mail delivery not configured** (including when the check itself fails — treated the same as unconfigured, since assuming mail works when it might not risks the same dead end): Zitadel would still create the identity, but the invite email it depends on can never arrive, leaving the account stuck in `USER_STATE_INITIAL` forever. Instead, the identity is created with a random, generated password and its email marked verified (both required together — Zitadel only skips its own initialization email when *both* conditions hold), landing it `USER_STATE_ACTIVE` immediately. The generated password is returned once, in `temporaryPassword`, for the operator to hand to the enrollee out of band; it is never stored or logged.

Only once the IdP half succeeds does this create the matching erun user and its `user_external_ids` mapping, using the IdP's own returned id as `subject` and the caller's own token issuer as `issuer`.

```jsonc
// request body
{
  "username": "bob",
  "email": "bob@example.com",   // required
  "firstName": "Bob",           // optional
  "lastName": "Operator"        // optional
}

// 201 response — mail delivery configured, both halves landed
{
  "idpUser": { "id": "387728445393600515", "username": "bob", "state": "USER_STATE_INITIAL" },
  "erunUser": { "userId": "019a…", "username": "bob" },
  "mailDeliveryConfigured": true
}

// 201 response — mail delivery NOT configured: a temporary password stands in for the invite email
{
  "idpUser": { "id": "387728445393600515", "username": "bob", "state": "USER_STATE_ACTIVE" },
  "erunUser": { "userId": "019a…", "username": "bob" },
  "mailDeliveryConfigured": false,
  "temporaryPassword": "Er7hK2mQ9xL4nP6z!",
  "warning": "This platform's identity provider has no SMTP configured, so no invitation email was sent. Share temporaryPassword with bob directly; they must sign in and change it."
}

// 201 response — the IdP identity was created, but the erun mapping failed
{
  "idpUser": { "id": "387728445393600515", "username": "bob", "state": "USER_STATE_INITIAL" },
  "mailDeliveryConfigured": true,
  "error": "identity created in the identity provider but the erun user mapping failed: idp user id 387728445393600515: a user with this username already exists in the target tenant"
}
```

The IdP half is created first, since the erun mapping needs the subject the IdP assigns; a failure there means nothing was created and the response is an error (below), not a `201`. A failure in the erun half after the IdP identity exists is **not** an error response — it is a `201` with `error` set and no `erunUser`, naming the orphaned IdP user id so the operator can retry the mapping (`POST /v1/users` with that id as `subject`) rather than enroll a duplicate identity. `mailDeliveryConfigured`/`temporaryPassword`/`warning` are reported the same way regardless of which of the two failure/success shapes above applies.

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

## `GET /v1/identity/org-settings` {#org-settings}

Reads the org's current login policy, password complexity policy, and verified domains.

```jsonc
// 200 response
{
  "forceMfa": false,
  "allowRegister": false,           // whether the platform's IdP accepts self-registration (issue #1482)
  "minPasswordLength": 8,
  "passwordRequiresUppercase": true,
  "passwordRequiresLowercase": true,
  "passwordRequiresNumber": true,
  "passwordRequiresSymbol": false,
  "verifiedDomains": ["erun.example.com"]
}
```

`verifiedDomains` is read-only here — verifying a new domain is a DNS/HTTP challenge flow this surface does not drive.

**`allowRegister`.** erun reads this field from Zitadel's own login policy but never wrote it before this endpoint could change it — the whole reported exposure of issue #1482. There is no erun-side default this endpoint imposes: a platform that has never called `PATCH` with `allowRegister` set keeps whatever value its Zitadel org already carries (Zitadel's own instance default is `true`, which is why the exposure went unnoticed). The recommended value for a platform whose access model is invite-only ([`POST /v1/invites`](/agent-reference/api-protocol#invites)) is `false`; erun does not flip it automatically on any existing platform — an operator (or their Agent) sets it explicitly via the `PATCH` below.

## `PATCH /v1/identity/org-settings`

Applies only the fields present in the body; every other current value is preserved via a server-side read-modify-write against Zitadel (re-sending an unchanged value is skipped rather than sent, since Zitadel answers `400` for a write that carries no real diff). Returns the full settings after the update, same shape as the `GET` above.

```jsonc
// request body — close self-registration, leave every other field untouched
{ "allowRegister": false }
```

**Error behaviour.**

| Status | Condition | Recovery |
|---|---|---|
| `400` | Body is not valid JSON. | Send a valid JSON object; every field is optional. |
| `403` | Caller's tenant is not `OPERATIONS`. | Call from an operations-tenant token. |
| Forwarded from Zitadel | The policy write itself failed. | The response body carries Zitadel's own message. |

## `GET /v1/identity/smtp-settings` {#smtp-settings-get}

Reports whether the platform's IdP can send mail at all (issue #1168) — every flow that reaches a person out of band (signup verification, password reset, the invitation flow above) depends on it. Zitadel answers a plain `404` when no SMTP config is active; this translates that into an explicit, checkable field rather than leaving the `404` as the only signal.

```jsonc
// 200 response — configured
{
  "configured": true,
  "config": {
    "host": "smtp.example.com:587",
    "user": "erun",
    "senderAddress": "noreply@example.com",
    "senderName": "Erun Platform",
    "replyToAddress": "",
    "tls": true
  }
}

// 200 response — not configured
{ "configured": false, "config": {} }
```

The password is never included, in either direction — Zitadel does not return it, and this endpoint only ever forwards what Zitadel itself reports.

**Error behaviour.**

| Status | Condition | Recovery |
|---|---|---|
| `403` | Caller's tenant is not `OPERATIONS`. | Call from an operations-tenant token. |
| Forwarded from Zitadel | A transport-level failure reaching Zitadel (not the same as "no config" above, which is `200`). | The response body carries Zitadel's own message. |

## `PATCH /v1/identity/smtp-settings` {#smtp-settings-patch}

Converges the platform's outbound-mail configuration to the request body — provider-agnostic (any SMTP host), declarative (send the full desired state, not a diff). `host` and `senderAddress` are required; `password` is sourced by the caller from wherever it holds the mail provider's credential out of band, and is required the first time (there is nothing yet to leave unchanged), optional afterward (omit it to leave Zitadel's stored password untouched — there is no way to read it back to diff against).

```jsonc
// request body — first-time configuration
{
  "host": "smtp.example.com:587",
  "username": "erun",
  "password": "the-provider-issued-credential",
  "senderAddress": "noreply@example.com",
  "senderName": "Erun Platform",
  "tls": true
}

// 200 response, same shape as GET /v1/identity/smtp-settings above
{ "configured": true, "config": { "host": "smtp.example.com:587", "...": "..." } }
```

Internally this creates-and-activates Zitadel's SMTP config when none exists yet (a freshly created config defaults to inactive and is invisible to the `GET` above until explicitly activated), or read-modify-writes the existing one otherwise — always activating before writing a changed password, since Zitadel's password-update command refuses an inactive config.

**Error behaviour.**

| Status | Condition | Recovery |
|---|---|---|
| `400` | `host`/`senderAddress` empty, the body is not valid JSON, or no config exists yet and `password` was omitted. | Send `host`, `senderAddress`, and (for a first-time configuration) `password`. |
| `403` | Caller's tenant is not `OPERATIONS`. | Call from an operations-tenant token. |
| Forwarded from Zitadel | The config write itself failed. | The response body carries Zitadel's own message. |

## Audit

Every request above that reaches its handler (i.e. passed authentication, tenant resolution, and the `OPERATIONS`-tenant check) writes an `audit_events` row through the same request-scoped middleware every other protected endpoint uses — see [Audit log](/agent-reference/audit-log). No route-local audit code was added; registering these endpoints through the shared route registrar was enough for the middleware to see them.

## See also

- [Administering identity](/collaboration/identity-administration) — the Operator view.
- [erun API protocol](/agent-reference/api-protocol) — sign-in, tenant resolution, and the cross-cutting error model these endpoints share.
- [`POST /v1/invites` and friends](/agent-reference/api-protocol#invites) — the invite-only registration model `allowRegister: false` above is meant to pair with; every tenant type can create its own invites, unlike this OPERATIONS-only surface.
- [Audit log](/agent-reference/audit-log) — the event shape every mutation above writes.

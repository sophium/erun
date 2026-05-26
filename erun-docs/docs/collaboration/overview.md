---
title: Agent collaboration overview
---

# Agent collaboration

Each environment gives one Agent everything it needs to do its own work. But software is rarely built solo — multiple Agents (and their Operators) need to **post comments on each other's work, run reviews, and decide what's ready to merge**. That shared layer is the **erun API**: a single service every Agent and Operator talks to over HTTPS.

<figure className="erun-hero-figure">
  <img src="/img/collaboration-overview.svg" alt="Three actors on the left — Agent A in env feature-a, Agent B in env feature-b, and an Operator on the desktop — connect to a central erun API on the right. Solid arrows show writes and reads to the API; dashed arrows show the API sending merge-queue updates and replies back to the agents." />
</figure>

Every actor — Agent or Operator — talks to the same API and signs in the same way. The API figures out which project (tenant) the request belongs to from the sign-in token, so an Agent automatically stays in its own lane.

## What lives in the API

| Resource | Purpose |
|---|---|
| [Reviews](/collaboration/reviews) | A unit of work-to-be-merged. Carries source branch, target branch, status, and references to the latest builds. |
| [Comments](/collaboration/comments) | Threaded, per-commit-and-line comments on a review. Agents and humans use the same shape. |
| [Builds](/collaboration/builds) | Per-review build results: commit, version, success/failure. Drives review status transitions. |
| Merge queue | A shared queue of `READY` reviews targeting the same branch. `POST /v1/reviews/merge-queue/advance` promotes the next one. |
| Whoami | `GET /v1/whoami` returns the resolved identity for the calling token. |

All paths sit under `/v1/`.

## Sign-in

Every request signs in the same way — Operators and Agents alike. ERun uses standard OIDC tokens (the same protocol you'd use to sign into a corporate SSO): get a token from your tenant's trusted issuer, send it as `Authorization: Bearer <jwt>`, the API resolves which tenant the call belongs to from the token claims.

<figure className="erun-hero-figure">
  <img src="/img/oidc-flow.svg" alt="OIDC sign-in flow. A charcoal 'Caller' (Operator or Agent) at the left exchanges arrows with an OIDC issuer (cyan-stroked box at top centre, labelled Identity Center / Auth0 / Keycloak) — step 1 'request token' goes up, step 2 'signed JWT' comes back. Step 3, a horizontal arrow labelled 'Authorization: Bearer JWT', goes from the Caller to the erun API (cyan-stroked box at right). Step 4, a dashed cyan arrow labelled 'fetch JWKS', goes from the erun API up to the OIDC issuer. Step 5, a downward arrow labelled 'resolve tenant', goes from the erun API down to a light-grey card 'tenant-scoped operations: reviews · comments · builds · merge queue'." />
  <figcaption>One protocol for everyone. The caller fetches a JWT from a trusted issuer; the erun API verifies it against the issuer's JWKS, resolves the tenant from the token claims, and scopes the call.</figcaption>
</figure>

For Agents specifically, the usual pattern is a service-account credential. The Operator doesn't need to know the machinery — the in-pod Agent is provisioned with credentials at deploy time, the desktop's AI panel handles the rest.

For the full protocol spec — tenant-issuer schema, PATCH endpoint, service-account flow, error codes, rate limits, pagination — see **[Agent reference · erun API protocol](/agent-reference/api-protocol)**.

## Why a separate API?

Each environment also has its own [MCP server](/mcp/overview), but that's scoped to one environment — it can answer questions about that environment, not coordinate across many. It has no persistent storage, only the people who have the environment open can reach it, and it doesn't know who anyone is across environments.

The erun API solves the cross-environment problem: a real database, real identities, real permissions, reachable from any environment any Agent has open.

## Typical flow: two agents collaborating

1. **Agent A** completes a change on branch `feature-a` and opens a [review](/collaboration/reviews) targeting `main`.
2. **Agent A** records a successful [build](/collaboration/builds) for the latest commit.
3. **Agent B** lists open reviews and inspects the diff.
4. **Agent B** leaves an inline [comment](/collaboration/comments) on a specific commit + line.
5. **Agent A** reads the comment, pushes a fix, records a new build, and the review transitions to `READY`.
6. The merge queue advances `feature-a`.

Every step is auditable. Every actor is identified. Every transition is constrained by the API's state machine — Agents cannot, for example, mark a review `MERGED` without a corresponding successful build. For the exact request/response shapes and the state-transition rules, follow the resource links above.

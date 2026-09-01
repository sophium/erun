---
title: Administering another tenant
---

# Administering another tenant

Signed in as an Operator on an **OPERATIONS** tenant, the console's **scope selector** lets you administer a **COMPANY** tenant's resources — its environments and its quota today — without switching who you're signed in as.

For the full endpoint spec, see [Agent reference · erun API protocol](/agent-reference/api-protocol#post-v1environments).

## Two different controls, deliberately not merged

The console's sidebar carries two tenant-related controls, and they do unrelated things:

| Control | What it changes | Cost |
|---|---|---|
| **Tenant switcher** (above the nav) | Which **credential** you hold. Your identity resolves to exactly one tenant per token, so reaching a different one means a fresh sign-in. | A full OIDC round trip. |
| **Scope selector** (below the switcher, OPERATIONS only) | Which tenant's rows the panels below it **show and act on**, using the credential you already hold. | Nothing — it's a plain UI selection, applied instantly. |

Picking a tenant in the scope selector never redirects you anywhere and never asks you to sign in again. It defaults to your own tenant — every panel behaves exactly as it always has until you deliberately point it elsewhere.

## Administering environments

The **Environments** panel is the first resource this covers. Pick a tenant in the scope selector, and its Deploy list swaps to that tenant's own environments — each row now names its owning tenant next to its name, so a row never reads as your own tenant's by default once you've widened scope. Registering a new environment still creates it in **your own** tenant regardless of the selected scope; the scope selector changes what you see, not where a write from the register form lands.

## Administering quota

The **Tenants** panel's per-tenant **Set quota** action already let you write another tenant's environment-count cap, per-environment resource ceiling, and aggregate tenant-wide budget — but until now that dialog opened blank, so you had to already know (or guess) the target tenant's current caps before typing new ones, since a quota update always fully replaces the row. The dialog now reads the target tenant's current quota first and starts you from those values, the same as any edit form should. This is a separate control from the scope selector above — it targets whichever tenant's row you clicked, not whichever tenant the selector is pointed at — and the read-only **Quota** panel on your own overview page still shows only your own tenant's caps regardless of scope; widening that panel to the selected scope is a later increment.

## What's audited

Every request you make while scoped to another tenant still carries your own identity — nothing about the scope selector changes who is calling. A write that actually lands in the target tenant (for example registering an environment there directly, via the API) is recorded in a second, explicit audit event naming the target tenant, you as the operator, and your own home tenant, on top of the ordinary per-request event every API call gets. See [Operator in the loop](/collaboration/operator-in-the-loop) for where those events live and how long they're retained.

## What this does not do

- It does not grant any permission you don't already have — a `COMPANY`-tenant Operator never sees the scope selector at all, the same OPERATIONS-only gate the [Users/Tenants/Org settings views](/collaboration/identity-administration) already use.
- It does not let you act *as* the other tenant's own Operators — you're still your own identity, administering another tenant's resources with your own OPERATIONS-tenant reach.
- Resources without a supported cross-tenant capability aren't affected: builds, comments, reviews, releases, and audit events stay scoped to your own tenant regardless of the scope selector's setting (a deliberate boundary, not an oversight — see [Agent reference · erun API protocol](/agent-reference/api-protocol#post-v1environments) for which resources do support it).
- The quota read/write pair above is deliberately not wired to the scope selector — it always targets whichever tenant's row you opened the dialog from, never "whichever tenant the selector currently shows."

## Where next

- [Agent reference · erun API protocol](/agent-reference/api-protocol#post-v1environments) — the `tenantId` parameter's full contract on reads and writes.
- [Agent reference · `GET /v1/quota`](/agent-reference/api-protocol#get-v1quota) and [`PUT /v1/tenants/{tenant_id}/quota`](/agent-reference/api-protocol#put-v1tenantstenant_idquota) — the quota read/write pair's full contract.
- [Managing hosted environments](/collaboration/hosted-environments) — the environment lifecycle this scopes.
- [Administering identity](/collaboration/identity-administration) — the other OPERATIONS-only surfaces, and why enrolling into another tenant's organization needs its own tenant selector.
- [Operator in the loop](/collaboration/operator-in-the-loop) — the audit trail every cross-tenant write lands in.

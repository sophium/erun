---
title: Administering identity
---

# Administering identity

Every hosted erun platform runs its own IdP ([Zitadel](https://zitadel.com)) to sign Operators and Agents in. Before this, enrolling a new colleague meant two consoles: create the human in Zitadel's own admin UI, copy their subject id, then come back to the erun console to link it. The erun console now does both in one action.

For the full route/schema spec, see [Agent reference · Identity administration](/agent-reference/identity-administration).

## What the console offers

Signed in as an Operator on an **OPERATIONS** tenant, the console's **Users** view lets you:

- **Enroll a user.** Give a username and email; the console creates the identity in the platform's IdP (which emails them a sign-in link) and the matching erun user in one action. If the erun-side half fails after the identity provider half succeeds, the console tells you the identity provider id it created so you can retry the mapping rather than enrolling a duplicate.
- **List every identity** the platform's IdP knows about, with its current state.
- **Deactivate** a user, which blocks their next sign-in immediately.
- **Reactivate** a deactivated user.

The **Org settings** view lets you read and change:

- Whether multi-factor authentication is required to sign in.
- The password complexity policy (minimum length, and whether an uppercase letter, lowercase letter, number, or symbol is required).
- The org's verified domains (read-only here — verifying a new domain is a DNS/HTTP challenge flow this view does not drive).

## What still requires Zitadel's own console

This surface is deliberately narrow, not a full admin proxy. Anything beyond enroll/list/deactivate/reactivate and the login/password policy above — role/project configuration inside Zitadel itself, advanced MFA methods, SSO federation, org branding — is still Zitadel's own admin console's job.

## The privilege model

The credential that drives this is Zitadel's **org-owner** service-account token — the most privileged credential the platform's IdP holds, provisioned automatically on every deployment. It never reaches your browser: the console calls erun's own backend, and the backend is the only thing that ever holds it. Every mutation this surface can make is individually audited (see [Operator in the loop](/collaboration/operator-in-the-loop)).

Because that credential is so privileged, this whole surface is restricted to an **OPERATIONS** tenant. A **COMPANY** tenant's Operators do not see these views at all, and the backend refuses the underlying endpoints even if called directly.

## See also

- [Agent reference · Identity administration](/agent-reference/identity-administration) — the full endpoint spec.
- [Hosted platform](/concepts/hosted-platform) — the platform this identity provider belongs to.
- [Operator in the loop](/collaboration/operator-in-the-loop) — the audit trail every mutation here writes to.

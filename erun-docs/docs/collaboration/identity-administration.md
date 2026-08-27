---
title: Administering identity
---

# Administering identity

Every hosted erun platform runs its own IdP ([Zitadel](https://zitadel.com)) to sign Operators and Agents in. Before this, enrolling a new colleague meant two consoles: create the human in Zitadel's own admin UI, copy their subject id, then come back to the erun console to link it. The erun console now does both in one action.

For the full route/schema spec, see [Agent reference · Identity administration](/agent-reference/identity-administration).

## What the console offers

Signed in as an Operator on an **OPERATIONS** tenant, the console's **Users** view lets you:

- **Enroll a user.** Give a username and email; the console creates the identity in the platform's IdP and the matching erun user in one action. If the erun-side half fails after the identity provider half succeeds, the console tells you the identity provider id it created so you can retry the mapping rather than enrolling a duplicate.
  - **When outbound mail is configured** (see below), enrolling emails the new person a sign-in link, the normal invite flow.
  - **When it is not**, there is no mail path for that link to travel, so the backend does not pretend to send one: it generates a temporary password instead and returns it once. This is deliberate — an invite that silently waited on an email that could never arrive would look successful and never resolve. The console shows this password in a dialog right after enrollment, copyable in one click, and says plainly it will not be shown again. Hand it to the new person before closing the dialog — closing it drops the password from the console's own state for good.
- **List every identity** the platform's IdP knows about, with its current state.
- **Deactivate** a user, which blocks their next sign-in immediately. The console asks you to confirm before it takes effect, naming the person and the consequence.
- **Reactivate** a deactivated user.

The **Org settings** view lets you read and change:

- Whether multi-factor authentication is required to sign in.
- **Whether anyone can self-register an account** on the platform's IdP, versus requiring an invite (see [Inviting people](#inviting-people) below). If you have never touched this toggle, your platform keeps whatever value it already had — erun does not flip it for you. For a platform whose access model is invite-only, turning it off here is the recommended, one-time step; nothing about the invite flow below requires it, but leaving self-registration open alongside invites means a stranger can still create an account in your IdP (though not gain any erun access — enrollment, not IdP account creation, is the access boundary).
- The password complexity policy (minimum length, and whether an uppercase letter, lowercase letter, number, or symbol is required).
- The org's verified domains (read-only here — verifying a new domain is a DNS/HTTP challenge flow this view does not drive).

## Outbound mail (SMTP)

Signup verification, password reset, and the invite email above all depend on the platform's IdP actually being able to send mail — and by default, a freshly deployed platform cannot: no mail provider is configured. The console's **Outbound mail** view (next to Users and Org settings, same OPERATIONS-only visibility) reports that state plainly — a badge reading "Not configured" or "Configured" — and lets you set:

- **Host** and **Port** — your provider's SMTP host and port, e.g. `smtp.yourprovider.com` / `587`.
- **Username** and **Password** — the credential your mail provider issued you. Get this from your provider (or wherever your organization stores that kind of credential) before you start; the platform never generates or invents one. Password is required the first time you configure mail; leave it blank on a later edit to keep the one already stored.
- **From address** and **From name** — what recipients see as the sender, e.g. `noreply@yourdomain.com` / `Your Company`.
- **Use TLS** — leave this on unless your provider explicitly tells you otherwise.

Saving drives the same underlying API an Agent would call directly (see [Agent reference · Identity administration](/agent-reference/identity-administration#smtp-settings-get)); a failed save reports your mail provider's own error next to the form, and your entries stay in place so you can fix the value and retry.

There is no way around supplying those values yourself — ERun cannot supply a mail provider on your behalf, and self-service password reset and signup verification simply cannot work without one. Until it's configured, the platform says so plainly rather than staying silent, and inviting a colleague falls back to the temporary-password flow described above rather than pretending an email went out.

**To verify it worked:** once mail delivery is configured, enroll a throwaway test user (see above) — with mail configured, that enrollment emails a real sign-in link to the address you gave it, so receiving it is the proof. If nothing arrives, double check the host/port and credential with your mail provider; the error reported back is whatever your provider's server returned.

## Inviting people {#inviting-people}

Unlike everything above (which is restricted to an **OPERATIONS** tenant), the console's **Invites** view is available to every tenant — a **COMPANY** tenant needs its own way to add members exactly as much as an OPERATIONS one does, now that self-registration is meant to be closed.

Anyone already signed in can:

- **Create an invite.** Optionally pin it to one email address; the console shows you a one-time, copyable link right after creation. Share it however you'd share any link — chat, email, whatever you already use. The link expires after 7 days and works exactly once.
- **See your tenant's outstanding invites** and copy a link again if you need to resend it.
- **Revoke an invite** that should no longer work — it disappears immediately, and the link stops working even if it hasn't expired.

The person you invite opens the link, picks a username and password (and their email, unless you pinned one), and their account is ready immediately — no email round-trip, so it works even on a platform with no outbound mail configured. If something goes wrong partway (their identity-provider account was created but enrolling them into your tenant failed), the page says so plainly and tells them to ask you to finish it, rather than claiming success or failing silently.

An invite to your own tenant is always allowed. Inviting into a **different** tenant is restricted to an OPERATIONS Operator, the same way every other cross-tenant action in this console is — accepting that kind of invite hands out platform-wide access, not just membership in one company's tenant.

## What still requires Zitadel's own console

This surface is deliberately narrow, not a full admin proxy. Anything beyond enroll/list/deactivate/reactivate and the login/password policy above — role/project configuration inside Zitadel itself, advanced MFA methods, SSO federation, org branding — is still Zitadel's own admin console's job.

## The privilege model

The credential that drives this is Zitadel's **org-owner** service-account token — the most privileged credential the platform's IdP holds, provisioned automatically on every deployment. It never reaches your browser: the console calls erun's own backend, and the backend is the only thing that ever holds it. Every mutation this surface can make is individually audited (see [Operator in the loop](/collaboration/operator-in-the-loop)).

Because that credential is so privileged, the Users/Org settings/Outbound mail surface above is restricted to an **OPERATIONS** tenant. A **COMPANY** tenant's Operators do not see those views at all, and the backend refuses the underlying endpoints even if called directly. [Inviting people](#inviting-people) is the one exception — it never touches that credential (it drives erun's own database and, on acceptance, the same Zitadel identity-creation call enrollment already uses), so it is available to every tenant.

## See also

- [Agent reference · Identity administration](/agent-reference/identity-administration) — the full endpoint spec.
- [Agent reference · Invites](/agent-reference/api-protocol#invites) — the invite endpoint spec.
- [Hosted platform](/concepts/hosted-platform) — the platform this identity provider belongs to.
- [Operator in the loop](/collaboration/operator-in-the-loop) — the audit trail every mutation here writes to.

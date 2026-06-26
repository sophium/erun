---
title: Security model
---

# Security model

ERun's security story is one sentence: **identity, isolation, audit — for every action, every Operator, every Agent.** This page is the consolidated view; the details live in other pages and are linked from each section.

## Identity

Every actor (Operator or Agent) presents an OIDC token. The erun API verifies the signature against the tenant's trusted issuers and resolves the `sub` claim to a stable `creator_user_id` recorded in every audit row.

- **Operators** sign in with their organisation's identity provider (Identity Center, Auth0, Keycloak, …) — the same provider they use for SSO.
- **Agents** use a service-account identity — a long-lived client credential exchanged for short-lived JWTs via the OAuth 2.0 client-credentials flow.

There is no anonymous action.

→ [Sign-in (OIDC) full spec](/agent-reference/api-protocol#sign-in-oidc)

## Authorization

Two concentric walls:

| Wall | Scope | Mechanism |
|---|---|---|
| **Tenant scoping** | The erun API filters every request by the token's resolved tenant. Cross-tenant access is impossible from a token of another tenant. | OIDC `tenantClaim` or `allowedSubjects` on the trusted issuer. |
| **Kubernetes RBAC** | Inside an env, every action runs as the runtime pod's ServiceAccount. The Operator's shell and the Agent's MCP calls share that ServiceAccount and its `RoleBinding`. | Standard cluster RBAC — bind narrower roles to restrict what an env can do. |

You don't need to grant an Agent special permissions — it has exactly the permissions a shell in the same pod would have. Restrict at the ServiceAccount level if you need to.

## Isolation

Each env is a separate Kubernetes namespace. The runtime pod and the application services for that env live there together.

- **Namespace isolation** — default-deny NetworkPolicy on the runtime chart blocks cross-namespace ingress. Cross-env traffic requires an explicit opt-in policy.
- **PVC isolation** — workspace + docker daemon PVCs are scoped to one namespace. Dropping the namespace reclaims them.
- **Service-account isolation** — one SA per env, with cluster-bound roles that name the namespace explicitly. An env's ServiceAccount can't read another env's secrets.

→ [Inside an environment](/concepts/runtime-pods)
→ [Networking](/concepts/networking)

## Audit

Three audit layers, all keyed by the resolved `creator_user_id`:

| Layer | What it captures | Retention |
|---|---|---|
| In-environment trace | Every `erun` invocation; every `--dry-run` plan; per-action `docker` / `helm` / `git` commands. | Pod lifetime. |
| MCP per-env events | Every `tools/call` with `argv`, `cwd`, exit code. | 30 days in the pod. |
| erun API events | Every review/comment/build/status transition; security-relevant events table. | Durable. Lifetime of the tenant. |

A `--dry-run` records `result: "dry_run"` and the same `details` it would on a real run — previews are audit-equivalent.

→ [Audit trail event shape](/agent-reference/audit-log#event-shape)
→ [Security events table](/agent-reference/audit-log#security-events)

## Secrets

ERun uses Kubernetes' native `Secret` primitive — there is no ERun-specific secret store. Five sources:

| Source | For |
|---|---|
| Namespace `Secret` objects | Application services |
| OIDC service-account credentials | Agents calling the erun API |
| Host AWS credentials (delivered when an AWS cloud alias is attached to the env) | Managed cloud envs |
| SSH key | IDE attach |
| Registry auth | `docker push` from the pod |

Two enforced rules: **never bake secrets into images; never log secret values.** `--dry-run` redacts secret-like values automatically; the same redaction applies to real-run traces.

→ [Secrets handling](/concepts/runtime-pods#secrets)
→ [`--dry-run` redaction rules](/agent-reference/dry-run-redaction)

## Network

Egress is open by default (envs need to pull images, fetch dependencies, install packages). Ingress is locked down by default to the env's own namespace.

| Direction | Default | Override |
|---|---|---|
| Egress | Allowed | NetworkPolicy with `policyTypes: [Egress]` |
| Ingress (cross-namespace) | Denied by the runtime chart | NetworkPolicy with `namespaceSelector` |
| Ingress (external HTTP/TCP) | Off until you deploy an Ingress / `Service: LoadBalancer` | Helm chart in the env's namespace |

→ [Networking](/concepts/networking)

## What changes between agent envs and runtime envs

Both share the security model. The differences are about *what code is in the env*, not *what controls apply to it*.

- **Agent envs** contain a worktree of source. An Operator + Agent are routinely typing in them. The runtime pod's ServiceAccount typically has broad permissions for builds and deploys.
- **Runtime envs** contain no worktree. The ServiceAccount is typically restricted to the operations the deployed services need (read configmaps, hit the database, etc.). No Operator should be typing in a runtime env — the [hotfix pattern](/concepts/environment-types#hotfix-pattern) is the safer alternative.

A typical hardening pass takes runtime envs and removes everything the agent envs needed for iteration (build tools, broad RBAC).

## Threat model

What ERun explicitly defends against:

- A compromised Agent operating beyond its intended scope — bounded by the runtime pod's ServiceAccount and the tenant's OIDC `allowedSubjects`.
- Lateral movement between envs — denied by namespace NetworkPolicy.
- Unauditable actions — all CLI / MCP / API actions land in the audit trail with `creator_user_id`.
- Secret leakage via logs — `--dry-run` and real-run redaction applies to both.

What it doesn't:

- Compromise of the underlying Kubernetes cluster — that's the cluster admin's problem. ERun trusts the cluster's RBAC and SecurityContext to be correctly configured.
- Compromise of the OIDC issuer — if an attacker can mint valid tokens, they can act as any identity. Mitigation: tenant-issuer JWKS rotation + tenant-level audit on `signin.success` events.
- Compromise of the user's laptop — local config files and SSH keys live there. Mitigation: full-disk encryption + standard laptop hygiene.

A security review of an ERun deployment focuses on the cluster RBAC, the OIDC issuer config, and the audit-trail retention. ERun makes those reviewable; it can't do the review for you.

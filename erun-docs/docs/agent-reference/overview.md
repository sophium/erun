---
title: Agent reference overview
---

# Agent reference

**This section is the spec — for Agents.** Field-by-field schemas, error codes, audit-trail formats, OIDC details, rate limits, build-path resolution algorithms. Everything an Agent needs to operate ERun without guessing.

The rest of the docs is for **Operators**. Operators get the concepts and the workflow; they don't need to read field tables, because the Agent handles those details. If you find yourself reaching for the field reference as an Operator, that's usually a signal the Agent should be doing the work — see [Agent patterns](/collaboration/agent-patterns).

## Inside

- **Concepts** — the platform's mental model. Tenants, environments, types, what's inside the runtime pod, networking, observability, security, conventions, cloud contexts. The Operator sees the high-level summary in the intro; the Concepts pages explain *how* it works.
  - **[Glossary](/concepts/glossary)** — canonical terminology.
  - **[Tenants and environments](/concepts/tenants-and-environments)** — the two organising ideas.
  - **[Environment types](/concepts/environment-types)** — local-agent / remote-agent / runtime.
  - **[Inside an environment](/concepts/runtime-pods)** — what lives in the namespace.
  - **[Networking](/concepts/networking)**, **[Observability](/concepts/observability)**, **[Security](/concepts/security)**, **[Conventions](/concepts/conventions)**, **[Cloud contexts](/concepts/cloud-contexts)**.
- **[MCP protocol + tools](/mcp/overview)** — the typed-tool surface for an environment. Three categories (inspection / action / escape) and the full tool schemas.
- **[Agent patterns](/collaboration/agent-patterns)** — ten patterns Agents converge on: orient first, doctor before raw, skill before hand-writing, build-verify-deploy, etc.
- **erun API**
  - **[API protocol](/agent-reference/api-protocol)** — OIDC sign-in, tenant-issuers, rate limits, pagination.
  - **[Audit log format](/agent-reference/audit-log)** — event shape, retention, security events.
  - **[Reviews](/collaboration/reviews)**, **[Comments](/collaboration/comments)**, **[Builds](/collaboration/builds)** — resource schemas + state machines.
- **Platform spec**
  - **[Conventions spec](/agent-reference/conventions-spec)** — resolution algorithms (project root, Dockerfile, VERSION, command overrides, fingerprint cache).
  - **[Idle-stop policy](/agent-reference/idle-policy)** — eligibility predicate, working-hours semantics, resume mechanics.
- **Configuration spec**
  - **[Configuration](/reference/configuration)** — every per-user / per-project / per-pod config field.
  - **[Build path resolution](/reference/configuration-build-paths)** — the exact algorithm `erun build` / `push` / `deploy` use to resolve project root, env, build context, version, and final image tag.
  - **[Config locations](/reference/config-locations)** — exact filesystem paths per OS.
  - **[Environment variables](/reference/env-vars)** — every `ERUN_*` variable.

## What the Agent is responsible for

Per the Operator/Agent split, an Agent is responsible for the following classes of detail the Operator shouldn't have to think about:

- Picking the right [MCP tool](/mcp/overview#built-in-tools) for the task — inspection before action, action before raw.
- Reading [error responses](/collaboration/reviews#errors) from the erun API and choosing the right retry or correction.
- Looking up [configuration field semantics](/reference/configuration) when scaffolding or editing config.
- Following the [build-path resolution algorithm](/reference/configuration-build-paths) when explaining why a build resolved to a particular image tag.
- Loading the [right skill](/concepts/skills) before writing a service, migration, or ingress — and writing conformant code by hand from the skill's guidance.
- Recording [structured audit events](/agent-reference/audit-log#event-shape) so the Operator can replay the session.
- Respecting the [rate limits](/agent-reference/api-protocol#rate-limits) — backing off when hit, not hammering the API.

## What stays in the Operator's hands

- Approving and merging — every transition to `MERGE` and the merge-queue advance is an Operator action.
- Reviewing [audit-trail events](/collaboration/operator-in-the-loop#the-audit-trail) and security events.
- Tenant-issuer trust changes ([planned](/agent-reference/api-protocol#tenant-issuers) admin-only endpoint).
- Granting Agent autonomy progressions ([Operator maturity](/collaboration/operator-maturity)).

The Operator should not need to memorise the field tables. The Agent should be able to look any of them up without bothering the Operator.

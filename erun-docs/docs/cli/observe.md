---
title: erun observe
---

# `erun observe`

Report an environment's Kubernetes state. Read-only — every call it makes is a `kubectl get`, never anything that can create, change, or delete a cluster object, which is what makes it safe to grant to an orchestrator that must never be handed [`erun exec raw`](/cli/exec).

## Synopsis

```
erun observe [--tenant <t>] [--environment <e>] [--secret <name>=<key>]... [flags]
```

## What it shows

Pods (phase, readiness, restart count, and why a pod isn't ready), the namespace's `ResourceQuota`/`LimitRange` and what's currently consumed against them, `Ingress` hosts and their TLS secret names, and every `Certificate`'s readiness.

When a `Certificate` is not Ready, `observe` walks its `CertificateRequest` → `Order` → `Challenge` chain automatically and reports the `Challenge`'s reason — the field that actually explains a stuck issuance (for example a webhook solver's RBAC denial) — instead of leaving you to run three more `kubectl` commands to find it.

```bash
erun observe --tenant my-tenant --environment prod
erun observe --tenant my-tenant --environment prod --output json
```

## Checking a Secret without reading it

`--secret <name>=<key>` (repeatable) checks whether a Secret exists and carries a given key, without ever reading or printing its value:

```bash
erun observe --tenant my-tenant --environment prod --secret db-credentials=password
```

## Flags

| Flag | Description |
|---|---|
| `--tenant`, `--environment` | Target a specific tenant/environment; default to the current scope. |
| `--secret <name>=<key>` | Check a Secret for a key's presence (repeatable). The value is never read. |
| `--output json` | Emit the full result as JSON. |
| `--dry-run` | Trace the `kubectl get` calls that would run without executing them. |

The full JSON shape (every field, and the exact CertificateRequest → Order → Challenge walk) is specified in [Agent reference · `erun observe`](/agent-reference/cli-flags#erun-observe).

## From an MCP-connected orchestrator

The same read reaches an Agent through the `observe` MCP tool — see [MCP overview § Inspection](/mcp/overview#inspection--read-only).

## Error behaviour

| Failure | Behaviour |
|---|---|
| Tenant/environment can't be resolved. | Errors before any `kubectl` call. |
| `--secret` isn't `name=key`. | Errors before any `kubectl` call, naming the malformed value. |
| The namespace or cluster is unreachable. | Errors naming the failed `kubectl get`. |
| Cert-manager isn't installed in the cluster. | Reports zero certificates rather than erroring. |
| A checked Secret doesn't exist. | Reports `exists: false` — not an error. |

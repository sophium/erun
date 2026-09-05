---
title: erun resize
---

# `erun resize`

Change the runtime pod's and/or the `erun-dind` sidecar's CPU/memory limits and roll them out — without re-running [`erun init`](/cli/init) to change these numbers.

## Synopsis

```
erun resize [--tenant <t>] [--environment <e>] --cpu <cpu> --memory <memory> [flags]
erun resize [--tenant <t>] [--environment <e>] --dind-cpu <cpu> --dind-memory <memory> [flags]
erun resize [--tenant <t>] [--environment <e>] --apply-recommendation [flags]
```

## What it does

Pass `--cpu` and/or `--memory` to change the runtime pod explicitly, `--dind-cpu`/`--dind-memory` to change the `erun-dind` sidecar — the container that actually runs `erun build`/`erun release` — or `--apply-recommendation` to size the runtime pod from its own [standing sizing recommendation](/cli/list#the-sizing-recommendation) instead of retyping a value ERun already computed. The sidecar flags are independent of the runtime-pod flags and can be combined with them, or with `--apply-recommendation`, in the same call — the sidecar has no standing recommendation of its own (see below). A resize whose resolved size already matches the current one is a no-op that says so — it does not roll the pod for nothing.

```bash
erun resize --tenant my-tenant --environment dev --cpu 6 --memory 12Gi
erun resize --tenant my-tenant --environment dev --dind-memory 16Gi
erun resize --tenant my-tenant --environment dev --apply-recommendation
erun resize --tenant my-tenant --environment dev --apply-recommendation --dry-run
```

Raise `--dind-memory` when a multi-arch `erun release`/`erun build --release` OOMs: a full rebuild runs `make check` for both target architectures inside the sidecar, concurrently, and every image build runs there — not in the runtime container.

`--apply-recommendation` needs usage history retained inside the environment's own runtime pod, so run it there (over SSH, or through the environment's own MCP `resize` tool) — a host-side laptop invocation has nothing to read and refuses rather than guessing. It only ever sizes the runtime pod, never the sidecar.

Before resolving a target, the trace prints the standing recommendation's own reasoning — the same `sizing:`/`sizing-evidence:` lines [`erun list`](/cli/list#the-sizing-recommendation) shows under `runtime-pod:` — even when the resolved plan is a no-op. A no-op that reports "already sized" is otherwise a dead end: this is what lets you see *why* nothing is proposed, not just that nothing is.

This moves both containers' own limits — the throttle/OOM ceiling, and the amount each draws from the namespace's resource quota. It does **not** change what the Kubernetes scheduler reserves for either container (a small fixed request, independent of this setting), and it does not touch any PVC — disk sizing is not part of this command.

## Not while someone else is using it

A resize restarts the runtime pod, which would kill any live session inside it — an agent mid-build, a deploy in flight. Before rolling the pod, `resize` checks the environment's activity leases and refuses, naming who holds it, if the environment is not idle:

```
resize refused: this environment is held by orchestrator eng-42, user jane@example.com (lease "exec_job_attach") — a resize restarts the runtime pod and would interrupt that work; pass the override to resize anyway, or wait until it finishes
```

Pass `--override-lease` to roll it anyway. The override is recorded alongside the resize's own lease.

## Flags

| Flag | Description |
|---|---|
| `--tenant`, `--environment` | Target a specific tenant/environment; default to the current scope. |
| `--cpu <cpu>` | Explicit CPU limit for the runtime pod (Kubernetes quantity, e.g. `6`). Omit to leave CPU unchanged unless `--apply-recommendation`. |
| `--memory <memory>` | Explicit memory limit for the runtime pod (Kubernetes quantity, e.g. `12Gi`). Omit to leave memory unchanged unless `--apply-recommendation`. |
| `--dind-cpu <cpu>` | Explicit CPU limit for the `erun-dind` sidecar. Independent of `--cpu`; omit to leave it unchanged. |
| `--dind-memory <memory>` | Explicit memory limit for the `erun-dind` sidecar. Independent of `--memory`; omit to leave it unchanged. |
| `--apply-recommendation` | Size the runtime pod from its own standing sizing recommendation instead of `--cpu`/`--memory`. Never sizes the `erun-dind` sidecar. |
| `--override-lease` | Roll the pod even though the environment is currently held by another worker. |
| `--orchestrator <id>` | The calling orchestrator's own id, recorded on the resize's lease and on the override, if one was needed. |
| `--dry-run` | Resolve and print the plan (current → target per resource, held leases, whether an override was needed) without changing anything. |
| `--output json` | Emit the full result as JSON. |

The full JSON shape and every refusal are specified in [Agent reference · `erun resize`](/agent-reference/cli-flags#erun-resize).

## From the desktop

The Manage dialog → **Runtime** tab's sizing recommendation carries a **Resize to this** action that runs the same operation as `--apply-recommendation`, so applying it is one click rather than retyping the suggested numbers into the resource sliders. Underneath it, the panel shows the same verdict/evidence reasoning the trace prints — including on a no-op, so "Already sized as recommended" always comes with the window and counters it was decided from. See [Runtime pods · What the environment thinks it should be sized as](/concepts/runtime-pods#what-the-environment-thinks-it-should-be-sized-as).

## From an MCP-connected orchestrator

The same operation reaches an Agent through the `resize` MCP tool, scoped to that server's own environment — see [MCP overview · `resize`](/mcp/overview#resize).

## Error behaviour

| Failure | Behaviour |
|---|---|
| Tenant/environment can't be resolved. | Errors before anything is read or changed. |
| None of `--cpu`/`--memory`/`--dind-cpu`/`--dind-memory`/`--apply-recommendation` given. | Errors naming what to pass; nothing is read or changed. |
| Both `--apply-recommendation` and explicit `--cpu`/`--memory` given. | Errors naming the conflict; `--dind-cpu`/`--dind-memory` may still be combined with `--apply-recommendation`. |
| `--apply-recommendation` with no standing recommendation available (no retained usage history, e.g. run from the host rather than in-pod). | Errors saying so, and suggests explicit `--cpu`/`--memory` instead. |
| The resolved targets would exceed the environment's namespace quota once both containers' limits are accounted for. | Errors naming the resource, the overage, and how much is actually available. |
| The environment is held by another worker and `--override-lease` was not passed. | Refuses, naming every holder. |
| The resolved target already matches the current recorded size. | No-op: reports "already sized" and does not deploy. |

---
title: erun usage
---

# `erun usage`

Report an environment's live CPU, memory, and disk usage, read straight from the runtime container's own cgroup accounting — no cluster metrics add-on needed, so it works even where `kubectl top` reports itself unavailable (every local orbstack/k3s-style cluster included).

## Synopsis

```
erun usage [--tenant <t>] [--environment <e>] [--interval <seconds>] [flags]
```

## What it shows

Memory usage against the container's own limit — current usage, the peak high-water mark, and a real out-of-memory kill count, so an agent heading for an OOM kill can notice it before it happens instead of finding out afterwards. CPU utilisation against the container's own quota, sampled over a short interval. Disk usage for the workspace mount.

```bash
erun usage --tenant my-tenant --environment dev
erun usage --tenant my-tenant --environment dev --output json
```

A crossed threshold (memory, memory's peak, or disk usage getting close to full) shows up as a plain-language warning in the output — you don't have to compute the percentages yourself.

Under the warnings, the environment's standing sizing recommendation prints as `sizing:` and `sizing-evidence:` — the same two lines [`erun list`](/cli/list#the-sizing-recommendation) shows, computed from this reading plus whatever history is retained. A reading that trips a memory warning always comes with a raise verdict naming the size that would fix it, so a saturated environment is never reported without the thing to do about it. The recommendation is advisory: acting on it is [`erun resize --apply-recommendation`](/cli/resize).

```bash
erun usage --tenant my-tenant --environment dev
#   Warnings (2):
#     memory is at 98% of its 6144Mi limit (warns at 85%)
#     memory.peak reached 100% of the limit (warns at 95%) -- this environment came close to an OOM kill
#   Sizing recommendation:
#     sizing: memory raise to 9216Mi from 6144Mi (peak 6144Mi of 6144Mi (100%) is within the raise margin, high confidence); ...
#     sizing-evidence: 31h12m observed, 240 samples, 0 restarts, knob=runtimepod, from cgroup memory.peak, ...
```

On an environment that carries the `erun-dind` sidecar, builds run in that container rather than the runtime one — so the CPU and memory above are the runtime container's, and on a building environment they are near-idle by construction, because a build lane spends its time waiting on bounded job calls. The reading reports the sidecar separately rather than leaving you to infer it: the text output prints `erun-dind sidecar (where builds run)` with its own CPU and memory, and the JSON carries the same block as `dind` alongside `excludesBuilds`. When the sidecar's own reading could not be taken, the text says so in a `Note:` line rather than letting the runtime container's figures stand in for the whole environment. Where the sidecar declares no `cpu.max` quota — the common case, since it is deliberately unlimited so a build can use the node — it has no utilisation percentage to report and states that reason instead. See [Runtime pods · Reading the resource figures](/concepts/runtime-pods#reading-the-resource-figures) for why, and `erun observe` for the sidecar's own limits.

The environment's standing [sizing recommendation](/cli/list#the-sizing-recommendation) rides along with the reading. It is derived from this reading together with history the environment's own pod monitor retained, which lives with the environment, so which surfaces show it follows from what can see that evidence rather than from which command was run — the `usage` and `resize` tools over the environment's MCP endpoint, [`erun doctor`](/cli/doctor) and [`erun list`](/cli/list)'s `runtime-pod:` block resolve the same recommendation from the same evidence, so no two of them can disagree. What does vary is how much history is there to reason from: a reading taken from a host has the live counters and no observed window, so the advice those counters alone prove — a raise, after a peak at the limit or a recorded OOM kill — is reported there too, while the shrink direction, which needs a day of quiet evidence, reads as insufficient evidence rather than as agreement that the size is right.

## Flags

| Flag | Description |
|---|---|
| `--tenant`, `--environment` | Target a specific tenant/environment; default to the current scope. |
| `--interval <seconds>` | CPU sample window, default `1`, clamped to `0.1`–`30`. |
| `--output json` | Emit the full result as JSON. |
| `--dry-run` | Trace the `kubectl exec` call that would run without executing it. |

The full JSON shape and the exact unavailability/warning rules are specified in [Agent reference · `erun usage`](/agent-reference/cli-flags#erun-usage).

## From the desktop

The same read is one click away without a terminal: the desktop app's Manage dialog → **Runtime** tab shows **This environment's usage** directly under the resource sliders, refreshed on demand. See [Desktop app · Resources and usage](/desktop/resources-and-usage) and [Runtime pods · Reading the resource figures](/concepts/runtime-pods#reading-the-resource-figures).

## From an MCP-connected orchestrator

The same read reaches an Agent through the `usage` MCP tool — see [MCP overview § Inspection](/mcp/overview#inspection--read-only).

## Error behaviour

| Failure | Behaviour |
|---|---|
| Tenant/environment can't be resolved. | Errors before any `kubectl` call. |
| The namespace, deployment, or cluster is unreachable. | Errors naming the failed `kubectl exec`. |
| cgroup v1, an unlimited limit, or a file that couldn't be read. | Reported as that field's own unavailability, not an error — normal on some clusters. |

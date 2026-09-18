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

On an agent env the CPU and memory lines cover the runtime container alone: `erun build`/`erun release` run in a separate sidecar container (`erun-dind`), not the one they read. Those environments also report **Build CPU** — the build's own cgroup, sampled over its own interval, with the periods it was throttled in:

```
Build CPU: 100.0% of a 4.00-core quota (sampled over 1.0s), throttled 200/200 periods
```

That is the figure that moves during a build; the container's own CPU line stays near zero while a build saturates its cap, so a throttled build is also called out as a warning. Where the build cgroup cannot be read — an image whose sidecar predates it, or a caller without access to the sidecar — `Build CPU` says so rather than leaving the near-zero line above to speak for the build. See [Runtime pods · Reading the resource figures](/concepts/runtime-pods#reading-the-resource-figures) for why the two cgroups are separate.

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

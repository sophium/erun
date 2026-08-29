---
title: Resources and usage
---

# Resources and usage

Below an environment's resource sliders, two readings tell you what the environment itself is doing — separate from what the node underneath it looks like.

- **See the environment's own usage, not just the node's.** Directly under the resource sliders, **This environment's usage** reads the environment's own opinion of itself: CPU utilisation against its quota, memory current and peak against its own cgroup limit with the real OOM-kill count, and disk usage on the workspace mount — refreshed on demand, and it works even on clusters where `kubectl top` can't (no metrics-server required). A field the reading could not measure (an unlimited memory setting, an older cgroup version) says so rather than showing a confident zero. Named warnings call out when memory or disk is close to its limit. See [Runtime pods](/concepts/runtime-pods#reading-the-resource-figures).

### See what an environment is running, and take resources back

Below that, **Running in this environment** reports what the pod is doing right now: how many sessions actually have a live program behind them, and the processes holding memory — Gradle daemons a finished build left resident, the container build cache — grouped by what they are. It is a reading, not a cleanup: nothing is stopped until you click the action beside a group, and your worktree, sessions, and Agent are never touched. The resource figures in the sliders above are a live snapshot of the node, and when the maximum is capped by the node being full rather than by a limit on the environment, the tab says so and points at stopping an environment nobody is using. See [Runtime pods](/concepts/runtime-pods#reading-the-resource-figures).

## Where next

- [Control panel](/desktop/control-panel) — the sidebar's compact usage reading, for comparing environments at a glance.
- [`erun usage`](/cli/usage) — the same reading from the CLI.

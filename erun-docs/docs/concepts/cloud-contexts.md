---
title: Cloud contexts
---

# Cloud contexts

A **cloud context** is a managed Kubernetes cluster ERun starts on demand and stops when it goes idle. Same workflow as local, just hosted somewhere else — your environments live there, your IDE and Agent connect to them exactly as if the cluster were on your laptop.

When to reach for one:

- Your machine doesn't have the capacity for the env you want to run.
- You need to share an env with another Operator or Agent across the network.
- You want overflow capacity for parallel work without buying a beefier laptop.

ERun watches each cloud context in the background, starts it on `erun open`, and stops it when idle — so the cluster doesn't bill twenty-four hours a day.

## Lifecycle

<figure className="erun-hero-figure">
  <img src="/img/cloud-context-lifecycle.svg" alt="Cloud context lifecycle as a state machine. Four cyan-stroked state boxes left to right: stopped (cluster is off) → starting (waking up) → running (ready to use) → stopping (winding down). Forward arrows are labelled 'erun open', 'cluster ready', and 'idle policy fires'. A return curve below loops from stopping back to stopped, labelled 'shutdown complete'. A note at the bottom reads: 'Idle timeout and traffic thresholds are configurable per environment.'" />
  <figcaption>ERun watches each cloud context in the background and reports the current state in `erun list` and in the desktop sidebar.</figcaption>
</figure>

| Status | Meaning |
|---|---|
| `stopped` | The underlying machine is off. Nothing is reachable. |
| `starting` | ERun is waking it up. |
| `running` | Ready to use; you can open environments. |
| `stopping` | Idle timer fired (or someone hit stop) — winding down. |

## Idle stop

For environments backed by a cloud context, ERun watches two activity sources — terminal input and network traffic. When both have been quiet for the configured `idle.timeout` window (default `5m`), and the current time is inside `idle.workinghours` if set, ERun shuts the cluster down. The next `erun open` brings it back.

Both the Agent and the Operator can read the live state via the [MCP `idle` tool](/mcp/overview#idle). The Agent watches this automatically before long-running operations — see [Agent patterns · Idle before sleeping](/collaboration/agent-patterns#3-idle-before-sleeping).

For the exact eligibility predicate, default thresholds, and working-hours semantics, see **[Agent reference · Idle-stop policy](/agent-reference/idle-policy)**.

## Configuring

You typically don't configure cloud contexts yourself. An administrator declares them once per cluster, and your environment references them by alias (something like `MyOrg+123456789012@aws`).

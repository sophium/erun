---
title: Idle-stop policy
---

# Idle-stop policy

The exact predicate ERun uses to decide whether a cloud-backed env is eligible for shutdown. For the Operator-facing view of cloud contexts and idle stop, see [Cloud contexts](/concepts/cloud-contexts).

## Activity sources

ERun watches two activity sources per env, both surfaced via the [MCP `idle` tool](/mcp/overview#idle).

### `last_terminal_input`

| Property | Value |
|---|---|
| Type | RFC3339 UTC timestamp. |
| Updated by | Any of: a keystroke on the in-pod SSH PTY; a successful in-pod `erun` invocation; any non-`idle` MCP `tools/call` against the env. |
| **Not** updated by | the MCP edge's own `idle` polling (would self-perpetuate); file-only edits from a laptop-side IDE that doesn't open an SSH session; passive port-forward traffic without an active session. |
| Initial value | Pod start time (so a freshly-started env is "active"). |
| Persistence | In-memory in the runtime container's MCP server. A pod restart resets to the new start time. |

### `last_network_traffic_window`

| Property | Value |
|---|---|
| Window shape | **Tumbling 60-second window** (not sliding). `started` is the wall-clock instant the window opened; `bytes` accumulates until 60s elapse, at which point a new window opens. |
| Source | Sum of bytes observed at the runtime pod's SSH socket + MCP socket. Loopback traffic counts. |
| Initial state | An empty window with `started: <pod-start-time>` and `bytes: 0`. |

## Predicate evaluation cadence

The idle monitor inside the MCP edge evaluates the predicate every **10 seconds**. State transitions:

- Each evaluation produces an `eligible` boolean.
- ERun maintains a `continuously_eligible_since` timestamp: set to now on the first `eligible=true` after `eligible=false`; cleared the moment `eligible=false` is observed.
- When `(now - continuously_eligible_since) ≥ idle.timeout`, the stop action fires.

## Eligibility predicate

An env is **eligible for stop** when *all three* hold:

```
now() - last_terminal_input > idle.timeout
  AND
last_network_traffic_window.bytes < idle.idletrafficbytes
  AND
now() in working_hours(idle.workinghours, idle.timezone)
```

When the env is continuously eligible for `idle.timeout`, ERun stops the cloud context.

## Configuration

Per-env `EnvConfig.idle.*` (see [Configuration overview](/reference/configuration)):

| Field | Type | Default | Effect |
|---|---|---|---|
| `idle.timeout` | duration | `5m` | How long the env must satisfy the predicate continuously before stop fires. |
| `idle.idletrafficbytes` | int64 | `65536` | Bytes-per-window below which the env is considered network-quiet. |
| `idle.workinghours` | string `HH:MM-HH:MM` | empty (= always) | Window during which idle-stop is allowed to fire. |
| `idle.timezone` | string IANA | host TZ | Time zone for `workinghours` interpretation. |

## Working-hours semantics

When `idle.workinghours` is set:

- The string is parsed as `<start>-<end>` in 24-hour format.
- `<start>` and `<end>` are interpreted in `idle.timezone`.
- If `<end> < <start>` (e.g., `22:00-06:00`), the window wraps midnight.
- If `now()` is **outside** the window, the third clause of the predicate is `false` — the env stays up regardless of terminal/network quiet.

When `idle.workinghours` is empty, the third clause is always `true` (idle-stop can fire at any time).

## Resume mechanics

The next `erun open` against a stopped cloud context follows this numbered algorithm:

1. Resolve the env's `cloudprovideralias` → look up the `CloudContext` record. If status is `running`, skip to step 4.
2. Send the provider's start command (e.g., AWS: `aws ec2 start-instances` against the cluster's ASG node group; GKE / AKS / equivalents: scale autoscaler `minNodes` from 0 to ≥1).
3. Poll the cluster's Kubernetes API every **5 seconds**. Accept the first `200` from `/healthz` or `/readyz`. Cap the poll at **5 minutes**; on cap exceeded → `CLUSTER_UNREACHABLE`.
4. Wait for the runtime pod's phase to reach `Running` and the readiness probe to pass. Cap: **3 minutes**. On cap exceeded → `POD_NOT_READY`.
5. Re-establish the SSH + MCP port-forwards (write the per-env JSON state files described in [Networking spec · Port-forward state files](/agent-reference/networking-spec#port-forward-state-files)).
6. Attach the terminal / IDE per the caller's `--vscode` / `--intellij` / default-shell choice.

State is preserved across stop/start because the PVCs (`/home/erun` and `/var/lib/docker`) survive — only the cluster nodes go away.

Typical end-to-end resume latency: 60–120 seconds on EKS / GKE / AKS, dominated by cluster-API readiness.

### Resume error codes

| Code | Cause | Recovery |
|---|---|---|
| `PROVIDER_START_FAILED` | Cloud-provider start command returned non-zero (quota, IAM, account-level constraint). | Inspect the provider's error message; `aws sso login` if AWS credentials expired. |
| `CLUSTER_UNREACHABLE` | Cluster API didn't respond within 5 minutes. | Check the cluster's autoscaler logs; verify nodes scaled up. |
| `POD_NOT_READY` | Runtime pod failed to reach Ready within 3 minutes. | `kubectl describe pod` + `kubectl logs`; common causes: image-pull failure, init-container failure. |
| `LIMIT_EXCEEDED` | Provider returned a service-quota error. | Lower instance count or request a quota increase. |

## Reading the live state

An Agent that's about to wait on a long operation should check eligibility first:

```jsonc
// MCP
{ "method": "tools/call", "params": { "name": "idle", "arguments": {} } }

// → response
{
  "timeout": "5m0s",
  "working_hours": "09:00-19:00",
  "timezone": "Europe/London",
  "managed_cloud": true,
  "eligible_for_stop": false,
  "activity": {
    "last_terminal_input": "2026-05-25T14:31:02Z",
    "last_network_traffic_window": {
      "started": "2026-05-25T14:30:00Z",
      "bytes": 184320
    },
    "within_working_hours": true
  }
}
```

If `eligible_for_stop: true` and the Agent has work to keep going, it should produce some activity (e.g., a `list` call, a file `touch`) before continuing. See [Agent patterns · Idle before sleeping](/collaboration/agent-patterns#3-idle-before-sleeping).

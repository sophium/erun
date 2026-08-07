---
title: Idle-stop policy
---

# Idle-stop policy

The exact predicate ERun uses to decide whether a cloud-backed env is eligible for shutdown. For the Operator-facing view of cloud contexts and idle stop, see [Cloud contexts](/concepts/cloud-contexts).

## Activity sources

ERun watches four activity sources per env, all surfaced via the [MCP `idle` tool](/mcp/overview#idle). The first two are request-shaped; the last two exist because request-shaped signals cannot describe long work — a detached build or agent run bumps nothing between its first second and its last, so an env under continuous heavy use would otherwise read as untouched.

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

### `resident_process_sample`

| Property | Value |
|---|---|
| Cadence | The in-pod environment monitor samples every **30 seconds**, in every pod — not only cloud-managed ones, because the desktop reads the same signal. |
| Source | The runtime container's own process table. A process counts when its `comm` prefix-matches the build/agent set (`gradle`, `java`, `mvn`, `node`, `npm`, `yarn`, `pnpm`, `cargo`, `rustc`, `python`, `gcc`, `clang`, `cc1`, `cmake`, `ninja`, `make`, `tsc`, `tsserver`, `vite`, `webpack`, `esbuild`, `jest`, `pytest`, `claude`, `codex`, `containerd`, `dockerd`, `buildkit`, `buildctl`) **and** its accumulated CPU time advanced since the previous sample, or it appeared since the previous sample. |
| **Not** counted | Residency alone. An agent parked at a prompt is resident for hours and burns nothing; counting it would pin the env awake permanently, which is the failure the lease TTL also exists to prevent. erun's own binaries are excluded outright — a liveness check that can match the observer is not a check. |
| Initial state | The first sample after a pod start has nothing to compare against and reports idle; it establishes the baseline the next tick compares to. |
| Recorded as | Activity kind `process`, which becomes an idle marker like any other. |

### `activity_leases` {#activity-leases}

An explicit claim held for the lifetime of long work. Unlike the other three sources it names *what* is keeping the env busy.

| Property | Value |
|---|---|
| Taken by | `erun activity lease take --name <what> [--id <id>] [--pid <pid>] [--ttl <duration>]`, or the MCP `activity_lease_take` tool. |
| Released by | `erun activity lease release --id <id>`, or the MCP `activity_lease_release` tool. Releasing an unknown or expired lease succeeds. |
| Identity | `id` defaults to a filename-sanitised `name`. Taking an existing id **renews** it rather than stacking a second lease. |
| Expiry | `ttl` (default `15m`) from the moment of the take or renewal. |
| Lifetime ceiling | `12h` from the original `startedAt`, which no renewal can push past. |
| Liveness reconciliation | When `pid` is set, the lease is reclaimed as soon as that process no longer exists — the same artifact-plus-process-probe shape session reconciliation uses. Checked on every read, so an orphan does not wait out its TTL. |
| Storage | `${XDG_CACHE_HOME}/erun/activity/<tenant>/<environment>/leases/<id>.json`, alongside the per-kind activity records the idle decision already reads. |

Lease JSON:

```jsonc
{
  "id": "gradle-build",
  "name": "gradle-build",
  "pid": 4242,
  "startedAt": "2026-05-25T14:20:00Z",
  "renewedAt": "2026-05-25T14:35:00Z",
  "expiresAt": "2026-05-25T14:50:00Z"
}
```

MCP tool shapes:

| Tool | Input | Output |
|---|---|---|
| `activity_lease_take` | `{tenant?, environment?, name, id?, pid?, ttlSeconds?}` | `{tenant, environment, lease, held[]}` |
| `activity_lease_release` | `{tenant?, environment?, id}` | `{tenant, environment, held[]}` |

Errors: a `take` with no `name` returns `lease name is required`; either tool with no resolvable tenant + environment returns `tenant and environment are required`; a `release` with no `id` returns `lease id is required`. All are tool-call errors, not partial writes.

A [job](/agent-reference/cli-flags#erun-job) is a lease plus an outcome, and holds one for its whole lifetime under the id `job-<job id>` with the supervisor's `pid`. Starting long work through `erun job start` (or the `job_start` MCP tool) therefore defers auto-stop with nothing extra to call, and the reconciliation above is what reclaims the claim if the supervisor dies.

## Predicate evaluation cadence

The idle monitor inside the MCP edge evaluates the predicate every **10 seconds**. State transitions:

- Each evaluation produces an `eligible` boolean.
- ERun maintains a `continuously_eligible_since` timestamp: set to now on the first `eligible=true` after `eligible=false`; cleared the moment `eligible=false` is observed.
- When `(now - continuously_eligible_since) ≥ idle.timeout`, the stop action fires.

## Eligibility predicate

An env is **eligible for stop** when *all four* hold:

```
now() - last_terminal_input > idle.timeout
  AND
last_network_traffic_window.bytes < idle.idletrafficbytes
  AND
no activity lease is held
  AND
now() in working_hours(idle.workinghours, idle.timezone)
```

When the env is continuously eligible for `idle.timeout`, ERun stops the cloud context.

The lease clause is absolute: a held lease makes the env ineligible regardless of how quiet every other source is, and the refusal names the lease (`lease: held by gradle-build`) so an operator reading the status can see why auto-stop is being deferred rather than wondering why an idle-looking env never stops.

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
  },
  "leases": [
    { "id": "agent-run", "name": "agent-run", "pid": 4242, "expiresAt": "2026-05-25T14:50:00Z" }
  ]
}
```

If `eligible_for_stop: true` and the Agent has work to keep going, it should produce some activity (e.g., a `list` call, a file `touch`) before continuing. See [Agent patterns · Idle before sleeping](/collaboration/agent-patterns#3-idle-before-sleeping).

Before **detaching** work rather than merely continuing it, take a lease instead. A poll that happens to keep an env alive is a coincidence the next change can remove; a lease is the env stating what it is doing, which is also what the desktop renders.

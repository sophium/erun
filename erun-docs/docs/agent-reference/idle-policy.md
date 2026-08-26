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
| Updated by | Any of: a keystroke on the in-pod SSH PTY; a successful in-pod `erun` invocation; any MCP `tools/call` against the env that is not an **idle probe** (see below). |
| **Not** updated by | an **idle probe** — a `tools/call` explicitly marked as a diagnostic read that must not perpetuate its own answer. `idle`, `job_status`, `job_output`, `job_await`, `activity_lease_list`, and a tools/list call are always idle probes; the host CLI and desktop mark them so regardless of who's asking or how often (#1227 — polling `erun idle` used to reset the very timer it reported). File-only edits from a laptop-side IDE that doesn't open an SSH session, and passive port-forward traffic without an active session, also don't update it. |
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
| Source | The runtime container's own process table. A process counts when its `comm` prefix-matches the build/agent set (`gradle`, `java`, `mvn`, `node`, `npm`, `yarn`, `pnpm`, `cargo`, `rustc`, `python`, `gcc`, `clang`, `cc1`, `cmake`, `ninja`, `make`, `tsc`, `tsserver`, `vite`, `webpack`, `esbuild`, `jest`, `pytest`, `claude`, `codex`, `containerd`, `dockerd`, `buildkit`, `buildctl`) **and** either it appeared since the previous sample, or its CPU time over the sample interval clears a **rate floor of 5% of one core**. |
| **Not** counted | Residency alone, and CPU below the rate floor. A process parked at a prompt still advances its CPU time by a tick or two per sample from scheduler and terminal-repaint noise — measured at ~0.7% of one core for a parked AI session — so a rule that counted any nonzero delta would clear on every sample and pin the env awake permanently, the same failure the lease TTL exists to prevent. The 5% floor sits well clear of that measured noise while staying far below what a real build step or an agent actually generating a response burns. erun's own binaries are excluded outright — a liveness check that can match the observer is not a check. |
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
| `activity_lease_take` | `{tenant?, environment?, name, id?, pid?, ttlSeconds?, exclusive?, scope?, orchestrator?}` | `{tenant, environment, lease, held[]}` |
| `activity_lease_release` | `{tenant?, environment?, id, exclusive?, scope?}` | `{tenant, environment, held[]}` |

Errors: a `take` with no `name` returns `lease name is required`; either tool with no resolvable tenant + environment returns `tenant and environment are required`; a `release` with no `id` returns `lease id is required`. All are tool-call errors, not partial writes.

A [job](/agent-reference/cli-flags#erun-job) is a lease plus an outcome, and holds one for its whole lifetime under the id `job-<job id>` with the supervisor's `pid`. Starting long work through `erun exec job start` (or the `exec_raw`/`exec_agent` MCP tools) therefore defers auto-stop with nothing extra to call, and the reconciliation above is what reclaims the claim if the supervisor dies.

### Exclusive claims {#exclusive-claims}

Everything above is **presence**: any number of leases can be held on an env at once, and taking one never checks whether anyone else is doing the same thing. That is fine for "is something using this env" — it says nothing about "is it safe for me to start mutating the worktree right now". An **exclusive claim** is the second, stricter mode `activity_lease_take` supports, added for two collisions that presence alone cannot prevent: two agent jobs (or an orchestrator and a job) interleaving `git checkout`/staging/commits in the same worktree, and an agent starting mutating work in an environment an operator is already sitting in over SSH.

| Property | Value |
|---|---|
| Requested by | `activity_lease_take` with `exclusive: true` (CLI: `erun activity lease take --exclusive`). |
| Scope | `scope` (CLI: `--scope`), default `worktree`. Exclusivity is **per scope, never per environment** — a second clone of the same repo in the same pod claims its own scope and is unaffected by another clone's claim. Two takes in different scopes always both succeed. |
| Holder identity | `orchestrator` (CLI: `--orchestrator`) is caller-supplied and recorded verbatim. `tenant`/`user` on the lease's `holder` come from the caller's resolved auth identity over MCP, never from request input — a lease cannot be taken out in someone else's name. |
| Conflict | A second exclusive take in the same scope, by a different holder id, is refused. The error names the current holder (`orchestrator`/`user`/`tenant`, lease `name`, lease `id`) rather than failing opaquely. |
| Version skew | A take against an environment whose MCP edge predates the release that added `exclusive`/`orchestrator` is refused with a diagnostic naming the environment and both remedies, instead of the edge's own raw schema-validation error. See below. |
| Renewal | The same holder id re-taking the same scope renews rather than conflicting — this is how a long job keeps its claim, the same as a plain lease. |
| Operator presence | A **fresh** exclusive claim (not a renewal) is also refused while an SSH session is active in the env — the operator never takes a lease, so their presence is read from the `ssh` activity marker instead. A renewal of a claim the caller already holds skips this check: by the time work is running, its own process is indistinguishable from an operator's on the other markers, so gating renewal on them would make a job refuse itself. This check is deliberately narrower than "any AI session is running" — see the note below. |
| Default TTL | `5m` instead of the plain default's `15m`. An exclusive holder has no PID to probe when it is a remote orchestrator driving over MCP, so the TTL is the only reclaim path for that case; the shorter default matches the orchestrate skill's own polling cadence so a holder that stops renewing lapses promptly. |
| Reclaim | Same rules as a plain lease (expiry, lifetime ceiling, PID liveness when `pid` is set), evaluated against the scope's current holder. |
| Storage | `${XDG_CACHE_HOME}/erun/activity/<tenant>/<environment>/leases/exclusive/<scope>.json` — a separate file per scope, keyed by scope rather than by holder id, which is what makes the take atomic: the file is created with `O_CREATE\|O_EXCL`, so of any number of concurrent claimants only one create can land. |
| Release | `activity_lease_release` with `exclusive: true` and the same `scope`. Only the id that took the claim can release it — releasing by scope name alone, without proving you are the current holder, would let a stale or mistaken release drop a different holder's exclusivity out from under them. A mismatched or already-vacated scope is a no-op success. |

**Version skew:** an environment's MCP edge compiles its tool schema from whatever erun release its runtime image runs. Take `exclusive: true` against an edge older than the release that added exclusive claims, and the go-sdk's own JSON-schema validator rejects `exclusive`/`orchestrator` as unknown properties before the request ever reaches the tool handler — a raw `unexpected additional properties [...]` message that reads exactly like a malformed call, not like a version mismatch, so the caller's natural next move is to doubt their own arguments instead of the edge's version. erun recognizes that one specific failure shape and reframes it as a diagnostic naming the cause and both remedies:

```
<tenant>/<environment>'s edge runs an erun release older than the one that added --exclusive to activity_lease_take;
upgrade the environment (erun pin / erun deploy) to take an exclusive claim there, or omit --exclusive to take a plain presence lease
edge error: <the edge's own schema-validation error, preserved underneath>
```

This only fires for that exact shape — a schema rejection naming `exclusive` or `orchestrator` among the rejected properties, on a call that actually passed `exclusive: true`. Any other failure — an unreachable edge, an auth failure, a genuinely malformed call (e.g. a missing required `name`) — passes through unchanged; it is never misreported as a version mismatch it is not. The recognition is wired into both `erun activity lease take --exclusive` and the raw `erun mcp call --tool activity_lease_take` path, so either way of reaching the edge gets the same diagnostic. Both remedies are real: `erun pin` / `erun deploy` roll the environment's runtime forward to a release that knows the `exclusive` and `orchestrator` properties, and dropping `--exclusive` still takes the plain presence lease documented [above](#activity-leases) — it just cannot coordinate mutating work the way an exclusive claim does.

Why the operator-presence check is SSH-only, not "any busy marker": the resident-process sampler's `process` marker and the codex-wrapper's `codex` marker both fire for *any* invocation of the wrapped `claude`/`codex` binaries — including the one a detached job itself starts. Gating an exclusive take on either would make a job refuse its own renewal, and would make a second legitimate job in a different scope of the same pod refuse against the first job's own resident process — exactly the "exclusivity became a global mutex" outcome this feature exists to avoid. `ssh` has no such ambiguity: no agent job opens an interactive shell into its own pod as part of normal work, so it is the one marker that unambiguously names a human.

Exclusive lease JSON adds three fields to the [shape above](#activity-leases):

```jsonc
{
  "id": "job-fix-1245",
  "name": "job-fix-1245",
  "startedAt": "2026-08-24T12:00:00Z",
  "expiresAt": "2026-08-24T12:05:00Z",
  "scope": "worktree",
  "exclusive": true,
  "holder": { "orchestrator": "petios", "tenant": "erun", "user": "rihards" }
}
```

The [erun-orchestrate skill](/agent-reference/skills-spec#erun-orchestrate) takes this claim before any mutating work in a target environment, and on refusal stops and reports the holder rather than retrying or falling back to working in the tree anyway.

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

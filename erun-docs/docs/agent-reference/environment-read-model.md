---
title: Environment read model
---

# Environment read model

This page owns the **JSON shape** of the two read-only contracts a companion client consumes: the
resolved environment read model, and the structured AI-session status model. It exists because those
shapes are a public, versioned surface that a client which cannot import `erun-common` — a native
Swift client, most concretely — has to hand-write DTOs against. The routes and MCP tools that
*serve* them are owned elsewhere; this page owns what comes back.

Every field below was read from the Go types that produce it, and the `environment` example in the
[MCP tool reference](/mcp/overview#environment) is checked against the real encoder by
`TestMCPOverviewEnvironmentExampleMatchesTheResolvedJSON` in `erun-mcp/mcp_overview_doc_test.go`, so
a renamed or retyped field fails the build rather than silently decoding to a zero in a client.

## Where the contract is served

| Surface | Returns | Reach |
|---|---|---|
| MCP tool `environment` | one [`EnvironmentReadModel`](#the-resolved-read-model) | The environment's own MCP edge. Needs the `erun:read` scope. |
| MCP tool `ai_sessions` | [`AISessionsResult`](#ai-session-status) | The environment's own MCP edge. Needs `erun:read`. |
| `GET /v1/environments/{environment_id}/ai-sessions` | an array of [`AISessionStatus`](#ai-session-status) | The hosted API. See [API protocol · ai-sessions](/agent-reference/api-protocol#ai-sessions-read-endpoint). |
| `erun activity ai-session status --json` | an array of [`AISessionStatus`](#ai-session-status) | Local CLI, inside the environment. |
| `erun list --json` | `ListResult`, whose `tenants[].environments[]` entries are [`ListEnvironmentResult`](#environment-summary) | Local CLI. |

Both MCP tools act on the server's **own** environment: `tenant` and `environment` default to the
server's context and must match it, so a caller cannot use them to read a different environment.
That is a consequence of the contract, not a limitation of the tool — the read model composes
signals (the environment's on-disk erun config, a live `helm`/`kubectl` deploy diagnosis) that only
exist inside the environment itself.

## The resolved read model

`EnvironmentReadModel` is one answered question — *what is this environment doing?* — composed from
an environment summary, its idle-policy eligibility, its cloud-context state, and doctor health.

| Field | Type | Present when |
|---|---|---|
| `tenant` | string | Always. The resolved tenant. |
| `environment` | [object](#environment-summary) | Always. |
| `state` | string enum | Always. One of `running`, `idle`, `deploy-failed`, `stopped`, `unknown`. |
| `cloudContext` | [object](#cloud-context-state) | The environment names a cloud context erun can resolve a status for. Omitted otherwise. |
| `idle` | [object](#idle-status) | Always, from the `environment` tool. |
| `health` | [object](#doctor-health) | A real (non-`preview`) call ran the deploy diagnosis. Omitted under `preview: true`. |

### Lifecycle state resolution

`state` is the field no single underlying read answers on its own, and it is deliberately
**not** a defaulted guess. Every signal that can fail to be observed carries its own observed bit,
so "could not be determined" reads as `unknown` rather than as a confident `stopped` or `running`:

1. If the environment is managed-cloud, read its cloud-context power state.
   - Not observed → `unknown`.
   - `stopped` → `stopped`.
   - `running` → continue to step 2.
   - Anything else (pending, or an unrecognized value) → `unknown`.
2. If the deploy diagnosis ran and reported a release that needs recovery → `deploy-failed`.
3. Else if the idle policy resolved and the environment is eligible to stop → `idle`.
4. Else if *any* signal was observed at all (idle policy, deploy health, or a managed-cloud
   environment) → `running`.
5. Else → `unknown`. Nothing was observed, so reporting `running` would assert the environment is
   alive purely because nobody said otherwise.

Two consequences a client should render deliberately:

- **A managed-cloud environment reads `unknown` from inside its own pod.** Its power state is a live
  AWS reading that `erun-common` never persists, and no AWS credential reaches inside the pod to
  refresh it. Unless something in the same process already refreshed it, `cloudContext` carries the
  environment's config with no `status`, and `state` follows it to `unknown`.
- **`preview: true` removes a signal rather than faking one.** Skipping the deploy diagnosis drops
  step 2 entirely, so an environment that would have read `deploy-failed` can read `running`. Use it
  to avoid a live cluster call, not to obtain a cheaper verdict.

### `environment` — the per-environment summary {#environment-summary}

The same entry `erun list` reports for every environment in a tenant, resolved here for one. The
fields a client reads most are below; the object carries roughly forty, most of them `omitempty`.

| Field | Type | Notes |
|---|---|---|
| `name` | string | |
| `type` | string enum | `local-agent`, `remote-agent`, `runtime`, or `host`. |
| `runtimeVersion` | string | Omitted for an environment that has never deployed. |
| `isDefault` | bool | The tenant's default environment. Omitted when `false`. |
| `isEffective` | bool | The environment an unqualified command actually resolves to. Omitted when `false`. |
| `managedCloud` | bool | Omitted when `false`. See [the `unknown` caveat](#lifecycle-state-resolution). |
| `kubernetesContext` | string | |
| `repoPath`, `localRepoPath` | string | |
| `runtimeVersionLine`, `erunVersion` | object | Present only alongside a `runtimeVersion`; they annotate which release line the number belongs to. |
| `runtimeImageLineMismatch` | object | Present only when the recorded and last-observed runtime images name different release lines. |

`isDefault` means "this is the tenant's default"; `isEffective` means "this is what an unqualified
command resolves to right now". They are separate because a default can be recorded while something
else — an explicit target, a directory match — is in effect.

### `idle` — idle status and activity snapshot {#idle-status}

| Field | Type | Notes |
|---|---|---|
| `policy` | object | Always present. |
| `policy.timeout` | **number** | Nanoseconds, not a duration string: a five-minute timeout is `300000000000`. |
| `policy.workingHours` | string | |
| `policy.timezone` | string | Omitted when unset. |
| `policy.idleTrafficBytes` | number | Always present. |
| `outsideWorkingHours` | bool | |
| `managedCloud` | bool | |
| `stopEligible` | bool | Whether the environment currently qualifies for auto-stop. |
| `stopBlockedReason`, `stopError` | string | Present only when a stop is blocked or failed. |
| `secondsUntilStop` | number | Omitted when there is no countdown. |
| `markers` | array | Always present, empty rather than `null`. One entry per activity source. |
| `activity` | object | Keyed by activity source; omitted when empty. |
| `leases` | array | The work claims currently holding the environment — *what* is deferring auto-stop, not merely that something is. Each carries `id`, `name`, `startedAt`, `expiresAt`, and optionally `pid`, `renewedAt`, `scope`, `exclusive`, `holder`. |
| `stopPendingSince` | string (RFC 3339) | Set while the auto-stop grace period is armed. |
| `secondsUntilForcedStop`, `gracePeriodSeconds` | number | |

The predicate itself, the activity sources, and the working-hours semantics are owned by
[Agent reference · Idle-stop policy](/agent-reference/idle-policy); this table owns only the shape
the values arrive in.

### `cloudContext` — cloud-context state {#cloud-context-state}

| Field | Type | Notes |
|---|---|---|
| `name`, `provider`, `cloudProviderAlias`, `region` | string | The context's own config. |
| `instanceId`, `publicIp`, `instanceType` | string | |
| `kubernetesContext` | string | |
| `status` | string enum | A **live** power reading erun never writes to disk. Absent when nothing refreshed it in this process. |
| `message` | string | Present alongside a status that carries one. |
| `stopProtection` | bool | |
| `stopProtectionKnown` | bool | Whether `stopProtection` was actually read. |

The k3s admin token is a **server secret** and is never part of any response.

### `health` — doctor health {#doctor-health}

| Field | Type | Notes |
|---|---|---|
| `rootConfig` | object | `configPath`, `configStatus` (`ok` \| `missing` \| `corrupted`), `configError`, and the orphaned-alias/context and backup lists. |
| `deploy` | object | See the casing note below. |
| `recommendedRecovery` | object | Omitted when the diagnosis recommends none. |

:::warning[`deploy`'s fields are capitalized]
`DeployDiagnosisResult` carries no JSON tags, so `encoding/json` emits its Go field names verbatim:
`HelmStatus`, `HelmReadError`, `Pods`, `ClusterUnreachable`, `AgentCredentials`. This is the shape
the encoder produces today, not a typo — a client **must** declare these names as written. Every
other object on this page uses lowerCamelCase.
:::

`HelmStatus` and `Pods` are the raw `helm status` / pod output, not a parsed verdict. The parsed
answer is `health.recommendedRecovery` and the `state` field above it. A read that could not tell —
as opposed to one that found nothing to recover — sets `HelmReadError` and `ClusterUnreachable`
rather than reporting the empty string as "no release exists".

## AI-session status

The structured answer to "is the Agent thinking, waiting on me, or gone" — resolved from each
session's **own last reported turn-boundary event**, never from PTY output volume or silence. A
session waiting on the Operator produces no output at all, which is exactly what a finished session
also looks like from outside; only a direct signal separates the two.

The write side is `erun activity ai-session report`, which a tool's own hooks invoke at each turn
boundary. There is no MCP write tool: the natural caller is the hook's own shell command running
inside the pod. Over the hosted API the write is
[`POST /v1/environments/{environment_id}/ai-sessions`](/agent-reference/api-protocol#ai-sessions-endpoint).

### `AISessionStatus`

| Field | Type | Notes |
|---|---|---|
| `sessionId` | string | |
| `tool` | string | Omitted when never reported. Carried forward from an earlier event by a later one that omits it. |
| `state` | string enum | `idle`, `busy`, `awaiting-input`, `exited`, or `oom-killed`. |
| `reason` | string | Always present. A sentence written for a human, explaining the state. |
| `lastActivity` | string (RFC 3339) | **Omitted** for a session that has recorded no activity, rather than rendered as Go's zero instant. |
| `exitCode` | number \| null | Only on an `exited` or `oom-killed` session whose process reported one. |

The MCP tool wraps these in `AISessionsResult`, which echoes the resolved `tenant` and
`environment` alongside a `sessions` array, so an empty list cannot be misread as answering for a
different target than the one requested. An environment with no recorded sessions returns `[]`,
never `null`.

### Event → state transitions

| Reported `event` | Resolved `state` | `reason` |
|---|---|---|
| `turn-start` | `busy` | working |
| `tool-use` | `busy` | working |
| `turn-end` | `awaiting-input` | finished its turn and is waiting for your next message |
| `notify` | `awaiting-input` | is waiting on you: a permission or a question is pending |
| `exit`, `exitReason` `"oom"` | `oom-killed` | the tool's process was killed by an out-of-memory event |
| `exit`, any other reason | `exited` | the exit reason, else `exited with code <n>`, else `exited` |
| *(no event ever reported)* | `idle` | no AI session activity recorded for this id |

### Resolution algorithm

1. Read the session's most recent recorded event. A later report **replaces** the previous one
   outright — only the most recent event decides the state, so there is no history to reconcile.
2. Map that event through the table above. Do **not** consider how long ago it happened: a session
   that reported `turn-end` an hour ago is still `awaiting-input`, because nothing has said
   otherwise since. This is the property that makes `awaiting-input` representable at all — an
   elapsed-time rule would decay it into `idle`, which is the state it exists to be distinguished
   from.
3. A session id with nothing recorded resolves to `idle` with its own reason, not to an error.

Detecting the OOM kill is the reporting side's job (a cgroup `memory.events` read, a `dmesg` scan),
not this model's: `oom-killed` is reported only when the caller that recorded the exit explicitly
said `exitReason: "oom"`.

## Errors

**MCP.** A failed `tools/call` returns a JSON-RPC `error` whose `data.errorCode` carries the machine
code, mirroring the CLI codes. See [MCP · Tool-call error responses](/mcp/overview#tool-call-error-responses).
An unauthenticated or under-scoped caller is refused before either tool runs.

**HTTP.** [`GET /v1/environments/{environment_id}/ai-sessions`](/agent-reference/api-protocol#ai-sessions-read-endpoint)
answers `404` for an environment id that is not the caller's tenant's (row-level security returns
not-found rather than leaking existence), and `500` when the read itself failed. The `POST` beside it
answers `400` for a `sessionId` that is empty or an `event` outside the five recognized values — an
unrecognized event is refused rather than silently resolving to `idle`.

## See also

- [MCP protocol + tools](/mcp/overview) — the tools that serve these shapes.
- [API protocol](/agent-reference/api-protocol) — the HTTP routes that serve them.
- [Idle-stop policy](/agent-reference/idle-policy) — the predicate behind `idle`.
- [Cloud contexts](/concepts/cloud-contexts) — the lifecycle behind `cloudContext.status`.

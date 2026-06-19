---
title: MCP overview
---

# Model Context Protocol (MCP)

MCP is **the typed-tool surface for an environment.** Where shell-level work happens over SSH, MCP carries typed actions — inspection (`idle`, `doctor`, `list`, `version`), operational wrappers around the CLI (`build`, `push`, `deploy`, `release`, `logs`, `open`, `init`, `delete`), and an escape hatch (`raw`). Agents (and any other code that wants structured, auditable access) talk to ERun over MCP; every call lands in the same audit trail the Operator reads.

ERun's conventions reach the Agent through a separate mechanism — [skill bundles](/concepts/skills) deployed into the env, loaded by the Agent's own skill loader. Skills are not MCP tools; they're content the Agent reads to know how to write conformant code. The MCP surface stays focused on inspection + action + escape; "how to scaffold a Go service" lives in the Agent's loaded skill, not behind a tool call.

Every open environment exposes an MCP server in its runtime pod. The desktop app port-forwards it to localhost so any MCP-compatible client — the Claude Code desktop app, the Codex desktop app, custom agents, any other JSON-RPC client — can connect directly.

**Both endpoints accept any client.** SSH and MCP live in the same pod and see the same workspace. The Claude Code and Codex desktop apps typically use both — SSH for shell + filesystem, MCP for structured ERun operations. See [Desktop app](/desktop/overview).

<figure className="erun-hero-figure">
  <img src="/img/mcp-flow.svg" alt="How MCP works. An Agent box on the left exchanges typed JSON-RPC calls with the MCP server in the env's runtime pod. The MCP server box is divided into three labelled category rows. INSPECTION row holds five chips: idle, doctor, list, version, logs. ACTION row holds seven chips: build, push, deploy, release, open, init, delete. ESCAPE row holds raw with a note that it's arbitrary argv, last-resort, every call audited. Below the MCP server, an audit-trail strip captures every call from every category. An Operator pill below the strip is connected by a dashed cyan arrow indicating the Operator can replay any session." />
  <figcaption>Agent calls typed tools over JSON-RPC. Every call is recorded in the audit trail; the Operator can replay any session. That's how Agents earn autonomy without losing the loop.</figcaption>
</figure>

## Endpoint discovery

`erun open` — run directly, or by the desktop app, which keeps the forward fresh by re-running `erun open --no-shell` — writes a small JSON state file per open environment:

```
<UserConfigDir>/erun/portforward/mcp/<tenant>/<environment>.json
```

`UserConfigDir` follows Go's `os.UserConfigDir`:

| OS | Path |
|---|---|
| macOS | `~/Library/Application Support` |
| Linux | `$XDG_CONFIG_HOME` or `~/.config` |
| Windows | `%AppData%` |

The file's `localPort` field is the port to call. For the full state-file shape, see [Networking spec · Port-forward state files](/agent-reference/networking-spec#port-forward-state-files).

## Protocol

JSON-RPC 2.0 over `POST http://127.0.0.1:<port>/mcp` with `Accept: application/json, text/event-stream`.

1. `initialize` (capture the `Mcp-Session-Id` response header).
2. `notifications/initialized` (POST with the session header).
3. `tools/list` or `tools/call` for subsequent requests, always carrying the session id.

### Session lifetime

| Property | Value |
|---|---|
| Session id format | Opaque string (UUID-like, 36 characters). |
| Created by | `initialize` response. |
| Idle timeout | **30 minutes** of no requests carrying the session id. After timeout, the session is evicted; subsequent requests with that id return `404` and must re-`initialize`. |
| Maximum concurrent sessions per pod | **8**. The 9th `initialize` succeeds but evicts the least-recently-used session. |
| Concurrent requests within one session | **2** in-flight max. A 3rd concurrent request returns `429 Too Many Requests` with `Retry-After: 1`. |
| Cross-session isolation | None — sessions are bookkeeping for the protocol, not security boundaries. The MCP server runs as the runtime pod's ServiceAccount; every session sees the same filesystem and RBAC scope. |

### Authentication

The in-pod MCP server is **loopback-only by default**: the chart binds the listener to `127.0.0.1:<port>`. The default-deny `NetworkPolicy` on the env's namespace blocks ingress from outside the namespace. There is no token check on the MCP server itself; clients reach it via the desktop's port-forward, which is bound to the user's own loopback.

To expose MCP cross-namespace or externally (rare), wire an Ingress + token-gated proxy in front of the listener. ERun does not ship that proxy.

### Worked example

The full handshake and a single `tools/call` for `list`, expressed as `curl` calls. Replace `<port>` with the `localPort` from the discovery file.

```bash
# 1. initialize — note the response's Mcp-Session-Id header.
curl -i -X POST http://127.0.0.1:<port>/mcp \
  -H 'Accept: application/json, text/event-stream' \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize",
       "params":{"protocolVersion":"2024-11-05","capabilities":{}}}'
# → HTTP/1.1 200 OK
# → Mcp-Session-Id: 7d4b...
# → {"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05","capabilities":{"tools":{}}}}

# 2. notifications/initialized — no response body expected.
curl -X POST http://127.0.0.1:<port>/mcp \
  -H 'Accept: application/json, text/event-stream' \
  -H 'Content-Type: application/json' \
  -H 'Mcp-Session-Id: 7d4b...' \
  -d '{"jsonrpc":"2.0","method":"notifications/initialized"}'

# 3. tools/call — invoke `list`.
curl -X POST http://127.0.0.1:<port>/mcp \
  -H 'Accept: application/json, text/event-stream' \
  -H 'Content-Type: application/json' \
  -H 'Mcp-Session-Id: 7d4b...' \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/call",
       "params":{"name":"list","arguments":{}}}'
# → {"jsonrpc":"2.0","id":2,"result":{"content":[{"type":"text","text":"{ default_tenant: \"myapp\", ... }"}]}}
```

The response payload is the typed shape from [Structured tool schemas](#structured-tool-schemas), wrapped in MCP's standard `content` envelope. Most clients deserialise it for you.

## Built-in tools

Three categories. The protocol treats them all as MCP tools; the categorisation is about *what they do*. (A fourth category — opinionated code-generation skills — used to live here and has moved off the MCP surface entirely. See [Skills](/concepts/skills) for how Agents pick up project conventions now.)

### Inspection — read-only

| Tool | Purpose |
|---|---|
| `idle` | Resolved idle policy, managed-cloud flag, stop eligibility, current activity snapshot. |
| `doctor` | In-pod health checks (config files, git checkout, SSH keys, docker daemon, workspace PVC). |
| `list` | Same data as the CLI `erun list`, structured. |
| `version` | Build version and commit of the MCP server. |
| `logs` | Tail logs from any container in the env's namespace, with optional filters. |
| `outputs_list` | List the files an agent produced in the pod's outputs directory (`$ERUN_OUTPUTS_DIR`), newest-first. Read-only. |
| `outputs_download` | Read one entry from the outputs directory and return its bytes inline as base64 (a folder as a `tar.gz`); the server is co-located with the files, so it returns the content directly. `preview` returns name/type/size without the bytes. |

### Action — typed wrappers around the CLI

These map 1:1 to the CLI commands of the same name. The MCP wrapper exists so Agents get typed input + output instead of stdout-parsing.

These wrap the [pure command primitives](/concepts/command-primitives): `build` mints a version, `push` publishes a version's image + chart, `deploy` installs a published version by reference. An Agent orchestrating a rollout calls them in that order and threads the version between them — it does **not** use the operator-convenience switches (`build --deploy` / `build --release`). `push` and `deploy` require the version explicitly; MCP paths fail clearly when it's missing rather than building.

| Tool | Wraps | Returns |
|---|---|---|
| `build` | `erun build` | Minted `version`, per-component status (`built` / `cached` / `error`), image tags, fingerprints. |
| `push` | `erun push` | Per-component status, registry URLs, published chart ref. Requires `version`. |
| `deploy` | `erun deploy` | Per-chart rollout status, helm release info. Requires `version`. |
| `release` | `erun release` | Released version, tag, multi-arch confirmation. |
| `open` | `erun open` | Local SSH + MCP ports, status (`opened` / `already_open`). |
| `init` | `erun init` | Created files, deployed namespace. |
| `delete` | `erun delete` | Namespace deleted, local config removed. |

### Escape hatch

| Tool | Purpose |
|---|---|
| `raw` | Run an arbitrary `argv` in the runtime pod. Last-resort escape hatch — see [raw spec](#raw--the-escape-hatch). |

### Tool selection rule

In order of preference: **inspection > action > raw.**

- If a question is "what's the state of X" — reach for an inspection tool.
- If you're invoking a known CLI command — reach for the action wrapper, not `raw`.
- If none of the above apply — use `raw`.

Generating conventional code (a new service, a migration job, an Ingress, …) isn't a tool-call decision — load the relevant [skill](/concepts/skills) and write the files by hand. The skill teaches the convention; the MCP surface stays out of the generation path.

Every call lands in the audit trail with its tool name, so `raw` invocations are immediately distinguishable from typed ones.

## Why typed tools

Anyone running a long-lived Agent in a shared environment wants two things: the Agent should *be able to act* (otherwise it's useless), and the Operator should *see what it did* (otherwise it's unsafe). Typed MCP tools deliver both — structured input/output the Operator can audit, with `raw` available for emergencies. As the Agent earns trust through audit, the Operator can grant more autonomy without losing the loop.

## Structured tool schemas

The structured tools take no arguments unless noted. Outputs are typed JSON.

### `idle`

Resolves the env's idle policy and reports its current activity. Useful for an Agent to decide whether to stop or keep going.

```jsonc
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

### `doctor`

Runs a fixed set of in-pod health checks. Each check returns `ok | warn | fail` plus a one-line detail.

```jsonc
{
  "checks": [
    { "name": "config_files",   "status": "ok",   "detail": "EnvConfig present and valid" },
    { "name": "git_checkout",   "status": "ok",   "detail": "branch=feature-a, clean" },
    { "name": "ssh_keys",       "status": "ok",   "detail": "sshd listening on :22" },
    { "name": "docker_daemon",  "status": "ok",   "detail": "reachable via /var/run/docker.sock" },
    { "name": "workspace_pvc",  "status": "ok",   "detail": "mounted at /home/erun" }
  ]
}
```

A failing check returns `status: "fail"` and a `detail` describing the symptom. Agents should prefer running `doctor` before `raw` when they see unexpected behaviour.

`doctor` also reports why a deploy may have failed (helm release status + runtime pods, read-only) and can recover a failing runtime release. Two boolean inputs request the recovery actions, each mutating the live release: `clearPendingHelm` clears a stuck helm pending-install/upgrade lock, and `rollback` rolls the release back to its last successful revision. They are alternative fixes — requesting both in one call is rejected. See [CLI flag spec · Deploy recovery actions](/agent-reference/cli-flags#deploy-recovery-actions) for the exact commands and when to use each.

### `list`

Same data as the CLI `erun list`, structured. Returns the caller's tenants, envs, and effective target.

```jsonc
{
  "default_tenant": "myapp",
  "current": { "tenant": "myapp", "environment": "local" },
  "tenants": [
    {
      "name": "myapp",
      "default_environment": "local",
      "environments": [
        {
          "name": "local",
          "type": "local-agent",
          "status": "running",
          "kubernetes_context": "docker-desktop",
          "container_registry": "ghcr.io/sophium",
          "runtime_version": "1.0.308"
        }
      ]
    }
  ]
}
```

### `version`

The build version and commit of the MCP server running in the pod.

```jsonc
{
  "build": "1.0.308",
  "commit": "abc123def456",
  "date": "2026-05-20T11:42:00Z"
}
```

### `logs`

Tail logs from a container in the env's namespace. Useful when an Agent is debugging a failed deploy or watching a service.

**Input:**

| Field | Type | Description |
|---|---|---|
| `component` | string | The component / pod name (matches a deployment label). |
| `container` | string (optional) | Container inside the pod. Defaults to the pod's first container. |
| `lines` | integer (optional) | How many trailing lines to return. Default `100`, max `2000`. |
| `since` | string (optional) | RFC3339 timestamp **or** Go-style duration. |
| `previous` | bool (optional) | When `true`, return logs from the previous container instance (crash-loop debugging). |

`since` duration grammar: the subset of [Go's `time.ParseDuration`](https://pkg.go.dev/time#ParseDuration) covering positive durations with units `ns`, `us`, `µs`, `ms`, `s`, `m`, `h`. Examples: `5m`, `1h30m`, `45s`. Negative values and bare numbers are rejected (`INVALID_SINCE`).

**Output:**

```jsonc
{
  "pod": "api-79f5b9c64",
  "container": "api",
  "lines": [
    { "timestamp": "2026-05-25T14:31:02Z", "stream": "stderr", "text": "starting on :8080" },
    { "timestamp": "2026-05-25T14:31:03Z", "stream": "stdout", "text": "GET / 200" }
  ],
  "truncated": false
}
```

### `build`

Trigger a build. Same semantics as the CLI `erun build` — it builds the images and **mints the version** an Agent then threads into `push`/`deploy`. Returns the minted `version` plus typed status per component.

**Input:**

| Field | Type | Description |
|---|---|---|
| `components` | string[] (optional) | Specific components to build. Omitted = build the resolved scope (cwd-based, see [Conventions](/concepts/conventions#how-erun-commands-resolve-scope)). |
| `release` | bool (optional) | Pin a bare release version instead of minting a snapshot. |
| `force` | bool (optional) | Bypass the fingerprint cache. |
| `dry_run` | bool (optional) | Preview without building. |

The MCP `build` tool does **not** expose the `--deploy` convenience switch — an Agent composes the rollout by calling `push` and `deploy` itself with the `version` from this tool's output.

**Output:**

```jsonc
{
  "version": "1.0.0-snapshot-20260525143027",      // the minted version — pass to push/deploy
  "base_version": "1.0.0",
  "results": [
    {
      "component": "api",
      "status": "built",                            // "built" | "cached" | "error"
      "image": "ghcr.io/sophium/api",
      "tag": "1.0.0-snapshot-20260525143027",
      "arches": ["linux/amd64", "linux/arm64"],
      "fingerprint": "fp-a3c7b9d2",
      "duration_ms": 18432
    },
    {
      "component": "ui",
      "status": "cached",
      "image": "ghcr.io/sophium/ui",
      "tag": "1.0.0-snapshot-20260525143027",
      "fingerprint": "fp-b8d1e2f3"
    }
  ],
  "result": "ok"                                   // "ok" | "dry_run" | "error"
}
```

### `deploy`

Install a published version into the env. Same semantics as the CLI `erun deploy` — a pure consume step that never builds or pushes. A **version is required**: supply `version`, or set `current: true` to redeploy the env's recorded version. Omitting both is rejected (`NO_VERSION`) — the MCP path never falls back to building.

**Input:**

| Field | Type | Description |
|---|---|---|
| `version` | string | The published version to install, by reference. Required unless `current` is set. |
| `current` | bool (optional) | Redeploy the env's persisted runtime version. Required unless `version` is set. |
| `components` | string[] (optional) | Subset of the plan to deploy. Omitted = full plan. |
| `force` | bool (optional) | Re-run helm even when the version is unchanged. |
| `timeout` | string (optional) | Override the helm rollout wait, as a Go duration (e.g. `8m0s`). Empty uses the env's `deploy.timeout` or the 5m default. The deploy keeps waiting while an image is still pulling and aborts early on a real container failure — see [rollout wait and monitoring](/agent-reference/cli-flags#rollout-wait-and-pod-monitoring). A malformed value is rejected (`INVALID_ROLLOUT_TIMEOUT`). |
| `dry_run` | bool (optional) | Preview without deploying. |

**Output:**

```jsonc
{
  "results": [
    {
      "component": "api",
      "chart": "api-0.1.0",
      "status": "rolled-out",                       // "rolled-out" | "skipped" | "error"
      "release_revision": 7,
      "image_tag": "1.0.0-snapshot-20260525143027"
    },
    {
      "component": "ui",
      "chart": "ui-0.1.0",
      "status": "skipped",
      "reason": "no source change since last deploy"
    }
  ],
  "result": "ok"
}
```

### Other action tools

`push`, `release`, `open`, `init`, `delete` follow the same shape — typed `arguments` matching their CLI flags, typed `result` payload mirroring the CLI's structured output. The MCP tool name matches the CLI subcommand exactly. Like the CLI, the `push` tool takes a **required** `version` (it publishes a specific version's image + chart and never mints one); omitting it is rejected with `NO_VERSION`. Field-by-field semantics, flag defaults, and per-tool error codes live in the [CLI flag spec](/agent-reference/cli-flags) — every CLI command listed there corresponds 1:1 to the MCP tool of the same name.

### Tool-call error responses

When a `tools/call` fails, the MCP response wraps a typed error. The envelope:

```jsonc
{
  "jsonrpc": "2.0",
  "id": 42,
  "error": {
    "code": -32602,                       // JSON-RPC standard codes for protocol errors
    "message": "Invalid params",
    "data": {
      "errorCode": "BUILD_AGAINST_RUNTIME_ENV",   // ERun-specific machine code
      "message": "erun build is not supported in a runtime env",
      "details": { "env": "prod", "type": "runtime" }
    }
  }
}
```

| JSON-RPC `code` | Meaning |
|---|---|
| `-32700` | Parse error (malformed JSON). |
| `-32600` | Invalid Request. |
| `-32601` | Method not found (unknown tool name). |
| `-32602` | Invalid params (the tool was found but its inputs failed validation). |
| `-32603` | Internal error. |
| `-32000` | Server error (ERun-specific; consult `data.errorCode`). |

`data.errorCode` mirrors the CLI error codes — the canonical list is in [Agent reference · CLI flag spec](/agent-reference/cli-flags). A successful call returns `result` with the per-tool typed output documented above.

## `raw` — the escape hatch

`raw` runs an arbitrary `argv` inside the runtime pod's `erun-devops` container. Reserved for actions the structured tools don't cover.

**Input:**

| Field | Type | Description |
|---|---|---|
| `argv` | string[] | The command and its arguments. First element is the executable. |
| `cwd` | string (optional) | Working directory. Default `/home/erun/git/<repo>`. Must resolve under `/home/erun`. |
| `stdin` | string (optional) | Piped to the process. |
| `timeout_seconds` | integer (optional) | Default `300`. Max `3600`. |

**Output:**

| Field | Type | Description |
|---|---|---|
| `stdout` | string | Captured stdout (up to 1 MiB). |
| `stderr` | string | Captured stderr (up to 1 MiB). |
| `exit_code` | integer | Process exit code. |
| `truncated` | bool | True if either stream was truncated. |

**Constraints:**

- Runs as the pod's ServiceAccount. All env-level RBAC applies — the Agent has the same scope as a shell in the same pod.
- Cannot break out of the container, mount host paths, or escalate privileges. The pod's SecurityContext is the wall.
- Output beyond 1 MiB per stream is truncated; the `truncated` flag is set and a note appears in stderr.
- Every call is recorded in the audit trail with `argv`, `cwd`, exit code, and timestamp. There is no anonymous `raw` call.

**When to use it (and when not):**

- ✅ Inspecting state the structured tools don't surface (a specific log file, a kubectl debug command).
- ✅ Running project-specific scripts (`./scripts/seed-test-data.sh`).
- ❌ Anything an existing tool covers — `list`, `doctor`, `idle` give typed output that's much easier for the Operator to scan.
- ❌ Long-running daemons — `raw` is request/response; it returns when the process exits.

## Skills are not MCP tools

The Agent's path to "add a Go service" / "add a migration job" / "add an Ingress" does not go through MCP. ERun ships those as [skill bundles](/concepts/skills) deployed into the env's runtime image; the Agent's own skill loader picks them up. The Agent reads the relevant SKILL.md, then writes the source + Dockerfile + chart by hand — no `scaffold` tool call, no template generator, no MCP round-trip for code generation.

For the on-disk format, deployment mechanism, and built-in skill catalogue, see [Agent reference · Skills spec](/agent-reference/skills-spec).

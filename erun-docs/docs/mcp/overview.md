---
title: MCP overview
---

# Model Context Protocol (MCP)

MCP is **the typed-tool surface for an environment.** Where shell-level work happens over SSH, MCP carries typed actions — inspection (`idle`, `doctor`, `list`, `version`, `observe`), operational wrappers around the CLI (`build`, `push`, `deploy`, `release`, `expose`, `init`, `delete`, and the `cloud_*`/`context_*`/`platform_*`/`review_*` families), and an escape hatch (`exec_raw`). Agents (and any other code that wants structured, auditable access) talk to ERun over MCP; every call lands in the same audit trail the Operator reads. The [full tool index](#full-tool-index) at the end of this page lists every registered tool.

ERun's conventions reach the Agent through a separate mechanism — [skill bundles](/concepts/skills) deployed into the env, loaded by the Agent's own skill loader. Skills are not MCP tools; they're content the Agent reads to know how to write conformant code. The MCP surface stays focused on inspection + action + escape; "how to scaffold a Go service" lives in the Agent's loaded skill, not behind a tool call.

Every open environment exposes an MCP server in its runtime pod. It runs **inside the runtime container** — the same image the in-pod Agent and an `erun open` shell use — so a tool call executes with the environment's own toolchain, including anything a custom runtime image adds. The desktop app port-forwards it to localhost so any MCP-compatible client — the Claude Code desktop app, the Codex desktop app, custom agents, any other JSON-RPC client — can connect directly.

Because it always runs inside a runtime pod, MCP has no reach into the machine the desktop app itself runs on — a distinct host, even for a builds-here (local-agent) environment. An operation the desktop process alone can perform, such as [restarting the desktop app in place](/cli/app#erun-app-restart) to pick up a rebuild, is therefore a CLI verb with no MCP counterpart: an Agent running as a terminal session on that same machine already has a shell, and reaches it there.

**A server acts only on its own environment.** Many tools take `tenant` and
`environment` arguments, but those are how a caller *states* which environment it
believes it is talking to — not a way to redirect the work. The server runs every
tool in its own pod, against that pod's repo and that pod's `erun` binary, so a
`tenant`/`environment` naming a different environment is **refused** rather than
silently run locally. Omit them to accept the server's own scope, or restate that
scope to assert it. To act on another environment, call that environment's own MCP
edge.

**Both endpoints accept any client.** SSH and MCP live in the same pod and see the same workspace. The Claude Code and Codex desktop apps typically use both — SSH for shell + filesystem, MCP for structured ERun operations. See [Desktop app · Working with an Agent](/desktop/working-with-an-agent).

<figure className="erun-hero-figure">
  <img src="/img/mcp-flow.svg" alt="How MCP works. An Agent box on the left exchanges typed JSON-RPC calls with the MCP server in the env's runtime pod. The MCP server box is divided into three labelled category rows. INSPECTION row holds five chips: idle, doctor, list, version, observe. ACTION row holds seven chips: build, push, deploy, release, expose, init, delete. ESCAPE row holds raw with a note that it's arbitrary argv, last-resort, every call audited. Below the MCP server, an audit-trail strip captures every call from every category. An Operator pill below the strip is connected by a dashed cyan arrow indicating the Operator can replay any session." />
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

An env deployed with a trust anchor requires a **bearer on every request**, including idle probes — the `exec_raw` tool can `kubectl exec`, so the edge is authenticated ahead of any tool running. The anchor is the desktop identity's public key, injected into the pod at deploy time; the matching private key stays on the machine that deployed the env.

| Property | Value |
|---|---|
| Algorithm | EdDSA (Ed25519). Hard-checked, so no `none` / HMAC confusion is possible. |
| `iss` | `file:///etc/erun/mcp-auth/desktopid.pub` — the in-pod path the edge loads its trusted key from. Only the configured issuer is ever trusted, never one named by the token. |
| `aud` | `erun-mcp:<tenant>/<environment>` — a token minted for one env cannot be replayed against another. |
| Lifetime | 5 minutes. Mint per request; do not cache. |
| Failure | `401` with the verification reason. |

An env deployed before key injection (no anchor configured) stays unauthenticated — loopback-only, behind the namespace's default-deny `NetworkPolicy`.

**Don't hand-roll the token.** `erun mcp call` and `erun mcp tools` mint one internally per request; `erun mcp proxy` does the same for a client that speaks MCP itself, relaying its stdio to this endpoint; and `erun mcp token` prints one for a caller driving the protocol directly:

```bash
erun mcp call --tool list --output json          # one typed call, no token handling
erun mcp proxy --tenant myapp --environment local  # stdio MCP server, bearer per request
TOKEN=$(erun mcp token --tenant myapp --environment local)
```

**A bearer must never be written into an MCP client's server config.** The client reads that config once at launch and cannot refresh a header, so the 5-minute lifetime above becomes a hard session limit: every tool for the env fails at once when the token ages out. Configure `erun mcp proxy` as a stdio server instead — the config then names a command, not a credential, and the token is minted per request behind it. See [`erun mcp` · Wiring a laptop-side MCP client](/cli/mcp#wiring-a-laptop-side-mcp-client).

See [`erun mcp`](/cli/mcp#talking-to-an-environments-mcp-edge) for the Operator view and [CLI flags · `erun mcp`](/agent-reference/cli-flags#erun-mcp) for the full contract.

To expose MCP cross-namespace or externally (rare), wire an Ingress in front of the listener; the bearer check above applies there too.

### Worked example

The full handshake and a single `tools/call` for `list`, expressed as `curl` calls — what `erun mcp call` does for you, spelled out. Replace `<port>` with the `localPort` from the discovery file and add `-H "Authorization: Bearer $(erun mcp token)"` to every request against an authenticated env.

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

MCP groups every tool into a family, carried on the wire as `_meta.family` so a client can render the tree without splitting names on `_`. The sections below cover each family; the [full tool index](#full-tool-index) at the end lists every one of them in one place, mechanically kept in sync with the registered surface.

### Inspection — read-only

| Tool | Purpose |
|---|---|
| `idle` | Resolved idle policy, managed-cloud flag, stop eligibility, current activity snapshot, and the activity leases currently holding the env busy. |
| `observe` | The env's Kubernetes state: pods, `ResourceQuota`/`LimitRange` usage, `Ingress` hosts + TLS secret names, and `Certificate` readiness — walking `CertificateRequest` → `Order` → `Challenge` for the failure reason when a certificate isn't Ready. Optionally checks named Secrets for a key's presence without reading their values. Every call is a `kubectl get`; nothing here can mutate the cluster. |
| `usage` | The env's live CPU, memory, and disk usage, read from the runtime container's own cgroup v2 accounting and a statfs of its workspace mount — no metrics-server required, so it works on clusters where `kubectl top` reports unavailable. Memory is reported against the container's own limit with a real OOM-kill count; CPU against its quota over a sample window. A named warning fires when memory, memory's peak, or disk usage cross a fixed threshold. |
| `doctor` | In-pod health checks (config files, git checkout, SSH keys, docker daemon, workspace PVC). |
| `list` | Same data as the CLI `erun list`, structured. |
| `environment` | The environment read model: this environment's `list`-style summary, a resolved lifecycle state (`running`/`idle`/`deploy-failed`/`stopped`/`unknown`), its `idle` status, its cloud-context config, and a `doctor` deploy diagnosis — one call composing what `list`/`idle`/`doctor` already report, rather than three. |
| `version` | Build version and commit of the MCP server. |
| `outputs_list` | List the files an agent produced in the pod's outputs directory (`$ERUN_OUTPUTS_DIR`), newest-first. Read-only. |
| `outputs_download` | Read one entry from the outputs directory and return its bytes inline as base64 (a folder as a `tar.gz`); the server is co-located with the files, so it returns the content directly. On a macOS host an arriving macOS binary carrying no code signature is signed first — the system kills an unsigned one on exec without printing anything — with the host's stable local identity when it has one and ad-hoc otherwise, and the optional `signing: {path, signed, identity, note}` field reports it (`identity` is empty for an ad-hoc signature); a signing failure is reported in `note` and never fails the call. `preview` returns name/type/size without the bytes. |

### Host-served — answered on this host {#host-served}

These tools are answered by `erun mcp proxy` on the operator's machine rather than relayed to the edge, because their subject — a file on the host's filesystem — is something the edge, running in the pod, has no path to. A client sees no difference — each is listed and called like any other tool.

| Tool | Purpose |
|---|---|
| `workspace_sync` | Run one workspace-sync pass for this environment: mirror the pod's git-visible worktree into the host review directory, delete what the pod no longer has, and deliver its cross-built artifacts. `preview` reports what a pass would change without touching the mirror. Refuses by name when the env has no pod worktree, has sync disabled, has no configured local path, or its SSH channel is down. Same pass as [`erun sshd sync`](/cli/sshd). |
| `inputs_upload` | Stream a local file on this host into the pod at an explicit `remotePath`, byte-identical, without the bytes passing through the call's arguments or an Agent's context — the server reads the file and streams it directly. `remotePath` is never defaulted, so a transfer can't silently land somewhere a background process (such as `workspace_sync`'s mirror) reconciles away. `preview` resolves and traces the transfer without sending anything. Refuses by name when the local file is missing, `remotePath` isn't absolute, the channel to the pod is down, or the destination directory isn't writable. Same command as [`erun inputs upload`](/cli/inputs). |

### Action — typed wrappers around the CLI

These map 1:1 to the CLI commands of the same name. The MCP wrapper exists so Agents get typed input + output instead of stdout-parsing.

These wrap the [pure command primitives](/concepts/command-primitives): `build` mints a version, `push` publishes a version's image + chart, `deploy` installs a published version by reference. An Agent orchestrating a rollout calls them in that order and threads the version between them — it does **not** use the operator-convenience switches (`build --deploy` / `build --release`). `push` and `deploy` require the version explicitly; MCP paths fail clearly when it's missing rather than building.

| Tool | Wraps | Returns |
|---|---|---|
| `build` | `erun build` | Minted `version`, per-component status (`built` / `cached` / `error`), image tags, fingerprints. |
| `push` | `erun push` | Per-component status, registry URLs, published chart ref. Requires `version`. |
| `deploy` | `erun deploy` | Per-chart rollout status, helm release info. Requires `version`. |
| `publish` | `erun publish` | Mirrors an already-built version's images from the FROM registry to each TO registry, without building or deploying. Requires `version`. |
| `release` | `erun release` | Released version, tag, multi-arch confirmation, and the read-back that proves the published version resolves. |
| `upgrade` | `erun upgrade` | Redeploys an opted-in, lagging environment to the latest version for its release channel. High blast radius: rolls out a new runtime image and restarts pods. `preview` returns the plan (channel, current → target) without deploying. |
| `pin` | `erun pin` | The resolved plan: every erun version reference for the env, its current value and its new one, plus whether it was applied. Verifies the target is published first. Resolves the checkout to rewrite from `projectRoot`, then the server's own runtime repo path; refuses rather than scan a wider directory when neither is set — there is no cwd to fall back to the way a shell user has. `preview` returns the plan without writing. |
| `expose` | `erun expose` | Resolved public hostname, per-env wildcard record, Host-routing Ingress. Requires a `platform:` block, unless `skipIfUnconfigured` turns that into a no-op. Supports preview (dry-run). |
| `unexpose` | `erun unexpose` | Removes an environment's per-env wildcard DNS record — the DNS-side counterpart to `expose`, run at teardown. Supports preview. |
| `terraform` | `erun terraform` | Runs a hosted platform's per-environment Terraform (`apply`/`plan`/`destroy`). `apply`/`destroy` mutate real cloud and cluster state and require `confirm` to equal the environment name. `preview` returns the resolved commands without executing them. |
| `init` | `erun init` | Created files, deployed namespace. |
| `delete` | `erun delete` | Namespace deleted, local config removed. |
| `contribute_clone` | `erun contribute clone` | Clones the ERun source repository into the environment so contribute-mode tabs can build and run a local checkout. Idempotent. |
| `activity_lease_take` | `erun activity lease take` | The lease held, plus every lease still held on the env. |
| `activity_lease_release` | `erun activity lease release` | Every lease still held on the env. |
| `activity_lease_list` | `erun activity lease list` | Every lease still held on the env. Reading the list also reclaims leases that expired or whose holder process is gone, so it returns what is actually deferring auto-stop. |

Take an activity lease before **detaching** long work in the env — a build, a test suite, an agent run. A detached job makes no calls while it runs, so without a lease the env reads as untouched and auto-stop would kill exactly the work worth protecting; with one, the env reports as busy with the lease's name and the operator can see it. Pass the detached job's `pid` so an abandoned lease is reclaimed, and release it when the work finishes. See [Agent reference · Idle policy](/agent-reference/idle-policy#activity-leases). Work started through the job tools below takes and releases its lease for you.

`activity_lease_take` also accepts `exclusive: true` (plus `scope`, default `worktree`) to claim a scope exclusively rather than merely holding it busy: a second exclusive take in the same scope is refused and told who the current holder is, and a fresh (non-renewal) claim is also refused while an operator's own SSH session is active in the env. Take this before any mutating work — a git checkout, staging, a commit — in a target env; a plain lease says only "something is here", the exclusive claim says "nobody else may mutate this worktree right now". See [Agent reference · Idle policy · Exclusive claims](/agent-reference/idle-policy#exclusive-claims).

One scope is special: `scope: "environment"` means "no other work here at all", not "not this resource". While it is held, **every** job start in that env is refused and told who holds it — ordinary jobs included, not only other exclusive ones — because what a gate contends for is the pod's CPU and memory, which no worktree boundary divides. Prefer `exec_raw`/`exec_agent`'s own `exclusive: true` over taking this claim by hand for a single job; take it directly only when the hold has to span several separate calls, and pass its id to `exec_gate_merge` as `underLeaseId` so your own hold does not refuse you.

### Jobs — long work you come back to {#job-tools}

| Tool | Purpose |
|---|---|
| `exec_agent` | Run an AI agent in the env as a detached job; returns the handle. |
| `exec_job_attach` | Give work you started another way a handle and a lease. |
| `exec_job_status` | One job's state and outcome, or every retained job newest-first. |
| `exec_job_await` | Wait a bounded time (default 30s, max 600s) for a job to finish. |
| `exec_job_output` | Read a page of a job's output, including while it runs. |
| `exec_job_cancel` | Signal a running job's work by its recorded process. |

`job`'s five query verbs moved under `exec` (`job_attach`/`job_status`/`job_await`/`job_output`/`job_cancel` are retired aliases, kept callable for one release). The command-starting tool did not move with them: `job_start`'s capability split across the two tools that actually start a job now — **`exec_raw` with `wait: false`** for a plain command (see [The escape hatch](#raw--the-escape-hatch)), and **`exec_agent`** for an AI tool. `job_start` itself stays registered as a stub: calling it always fails, naming both replacements, so a caller still shaped for the old tool learns what to call instead of seeing a bare "unknown tool". Splitting the capability means each tool's schema only carries the fields its own mode needs, instead of `job_start`'s single schema carrying both a `command` and an `agent`/`prompt` that excluded each other; both new tools also take the optional `tenant`/`environment` pair described above, the same as every other job tool.

**Reach for a backgrounded job instead of a foreground `exec_raw` call for anything you will need to come back to.** A foreground call is request/response: it returns when the process exits, so observing long work through it means re-implementing job bookkeeping in shell — detach the work, redirect it to a log, poll in a loop, invent a sentinel token because the real signal is buffered until exit, and parse that token back out of this envelope. Each of those is a place to get it wrong, and none of them is the interesting problem.

The job tools remove all five:

- **Starting a job detaches for you.** The work gets its own session and its merged stdout + stderr captured to the job's log — no `setsid`, no `nohup`, no redirect.
- **The exit status is captured in the env**, by the supervisor that waited on the work. No sentinel token, and no exit code re-expanded by an intermediate shell.
- **`exec_job_await` is bounded.** It always returns inside the timeout, so no connection is held open for the work's lifetime and a dropped stream is never confused with a dead job. `timedOut` is reported separately from every outcome, so "not finished yet" can never be read as a failure. Call it again to keep waiting.
- **`exec_job_output` is incremental.** Pass the previous read's `nextOffset` back to continue; progress is visible long before the work exits.
- **`exec_job_status` is definite or explicitly `unknown`** — never truncated, and never a success nobody recorded.
- **`exec_job_cancel` targets the pid the record holds**, never a command-line pattern, so it cannot match a process that merely looks like the job or the caller issuing the cancel. It refuses a backgrounded action tool (`build`, `deploy`, `doctor`, and the rest of the [job-envelope tools](#job-envelope) started with `wait: false`): that kind of job runs in-process rather than as a subprocess, so there is nothing to signal.

`exec_raw` (with `wait: false`) and `exec_agent` both take `exclusive: true` for work that needs the env to itself — a full gate run, a build whose result a neighbour would invalidate. While such a job runs, every other job start in that env is refused and told which job holds it. The claim expires without renewal and is reclaimed once its holder is gone, so a crashed job cannot pin an env, and work the holder itself starts runs under the claim rather than being refused by it. Full contract: [Agent reference · Environment exclusivity](/agent-reference/cli-flags#job-exclusivity).

A job also holds an activity lease for its lifetime, so starting one makes the env read as busy and defers auto-stop with nothing extra to call. Finished jobs stay readable for 24 hours, so an orchestrator that reconnects after the work ended still learns the outcome. Full schemas, exit-code contract, retention, and error behaviour: [Agent reference · `erun exec job`](/agent-reference/cli-flags#erun-job).

#### The alive contract {#alive-contract}

`state` alone can only be as fresh as the last read that reconciled it, so a job carries three more fields — `lastAliveAt`, `aliveSeq`, `aliveAgeMs` — written by erun's supervisor on a fixed ~1 second cadence, independent of whether the work itself is producing output. **The caller rule: once `aliveAgeMs` exceeds 5000, stop waiting and treat the job as failed — an `unknown` outcome, never a success and never the tool having errored** — even if `state` has not caught up to say so yet. A silent-but-healthy command (an image pull, a slow test) never trips this, because the beat has nothing to do with the work's own output. Field-by-field semantics and the reasoning behind the 5-second bound: [Agent reference · The alive contract](/agent-reference/cli-flags#alive-contract).

#### Running an agent as a job {#agent-jobs}

An AI tool run non-interactively prints nothing until it exits, so running one as a plain backgrounded command would report `outputBytes: 0` for the whole run while the agent is actively editing files, with `exec_job_status` only ever able to say `running`. `exec_agent` invokes the tool in its streaming mode instead:

```jsonc
// exec_agent {"name": "sweep", "agent": "claude", "prompt": "fix the failing tests"}
```

Two things follow with no change to any other tool's contract:

- **`exec_job_output` returns events while the agent works**, because the job's log is now the tool's event stream.
- **`exec_job_status` carries a `progress` view** — current activity, turns, tools run, and the last thing the agent said — normalized by erun from the tool's own events, so the shape is identical for `claude` and `codex`.

`agent` accepts `claude` or `codex`. Do **not** scrape the agent's private transcript (`~/.claude/projects/…`) or diff the worktree to report progress: that layout is not erun's contract and can change under you, and it cannot work at all for a remote-agent env whose worktree is not host-mounted. Poll `exec_job_status` instead. The full progress schema, the normalized verb set, and the per-tool event mapping are in [Agent reference · Agent jobs](/agent-reference/cli-flags#agent-jobs).

#### The job envelope — backgrounding an action tool {#job-envelope}

`build`, `push`, `deploy`, `publish`, `release`, `upgrade`, `terraform`, `pin`, `init`, `delete`, `expose`, `unexpose`, `doctor`, `exec_write`, `exec_commit`, and `contribute_clone` each take a `wait` input alongside their own fields. `wait: true` (the default this release) runs synchronously and returns the full typed result inline, exactly as before this input existed. `wait: false` starts the same call as a background job and returns `{jobId, state: "running"}` immediately instead — poll `exec_job_status`/`exec_job_await`/`exec_job_output` for the outcome, whose `result` field carries the tool's own typed result verbatim (the same shape it would have returned synchronously) rather than a log to re-parse. This default flips to `false` in a future release, with `wait: true` kept callable for one more release as the compatibility switch — watch the release notes.

Two more inputs come with `wait: false`, and both are ignored when `wait` is true:

- **`startedByJobId`** links the background job to the job the call is being made on behalf of, so that job's own finish check finds it (see [Gate-incomplete](/agent-reference/cli-flags#job-states)). These tools run their work *inside the MCP server's own long-lived process*, which was never itself started as anyone's job, so there is no `ERUN_JOB_ID` here to inherit however deep the nesting is on the calling side — a caller that is itself running as a job and wants this work covered by its own finish check passes its job id explicitly. Nothing is inferred when it is omitted: an unlinked job is recorded as having no parent rather than being attributed to whichever job happens to be running in the environment.
- **`handoff`** marks work deliberately meant to outlive whatever started it (a release, a long deploy), excluding it from that finish check entirely — the same field `exec_raw`/`exec_agent` already take.

A backgrounded action tool also writes a log, so `exec_job_output` serves the work's own trace and output while it runs and after it fails — the record is not just an exit code. `command` stays empty for one of these jobs: the work is a Go call inside this server, not a subprocess, so there is no argv to record; `name`, the log, and `result` are what describe it.

### Cloud — provider aliases {#cloud-tools}

Registers and manages the root-level cloud provider aliases an environment attaches to (`erun cloud set`) for build/push registries, DNS, and docs publishing. See [`erun cloud`](/cli/cloud).

| Tool | Read/Work | Caller | Purpose |
|---|---|---|---|
| `cloud_list` | Read | Agent | List configured root-level cloud provider aliases and their token status. MCP-only — no CLI leaf of its own. |
| `cloud_init_aws` | Work (idempotent) | Agent | Register an AWS SSO provider alias. |
| `cloud_init_cloudflare` | Work (idempotent) | Agent | Register a Cloudflare provider alias from a delegated API token (Zone + DNS edit, plus Pages for docs sites). |
| `cloud_init_erun` | Work (idempotent) | Agent | Register a hosted erun platform alias, discovering its OIDC issuer and CLI client id from the platform's own `/v1/platform` endpoint. |
| `cloud_login` | Work | Agent | Sign in to a configured provider alias (Device Authorization Grant, falling back to Authorization Code + PKCE for `erun`). |
| `cloud_oidc` | Work (idempotent) | Agent | Refresh the OIDC issuer for a configured provider alias. |
| `cloud_set` | Work (idempotent) | Agent | Attach a cloud provider alias to a tenant environment. |
| `cloud_inject_aws_credentials` | Work (idempotent) | **Desktop-only** | Write temporary AWS credentials into the pod (see [Credential tools](#credential-tools-desktop-only), below). |
| `cloud_clear_aws_credentials` | Work (destructive, idempotent) | **Desktop-only** | Remove the injected `erun-host` AWS profile (see [Credential tools](#credential-tools-desktop-only), below). |

All but the two credential tools support `preview`. Every `cloud_*` tool reaches an external system (a cloud SSO endpoint, a registry, the erun platform) and is advertised `openWorld: true`.

### Cloud contexts — managed Kubernetes clusters {#context-tools}

Manages the lifecycle of an erun-managed cloud k3s cluster. See [`erun context`](/cli/context).

| Tool | Read/Work | Purpose |
|---|---|---|
| `context_list` | Read | List managed erun cloud Kubernetes contexts. |
| `context_init` | Work | Bootstrap a managed cloud k3s Kubernetes context. |
| `context_start` | Work (idempotent) | Start a stopped managed cloud context. |
| `context_stop` | Work (destructive, idempotent) | Stop a managed cloud context. |

All four support `preview` and are agent-callable.

### Platform — hosted erun control plane {#platform-tools}

Talks to a hosted erun platform (`erun-backend-api`) over the `erun`-type cloud alias `cloud_init_erun`/`cloud_login` set up. Every call requires that alias to be attached and authenticated first. See [`erun platform`](/cli/platform).

| Tool | Read/Work | Purpose |
|---|---|---|
| `platform_whoami` | Read | Resolve the caller's identity against the platform. |
| `platform_version` | Read | Report the build actually serving the platform's own API — unauthenticated, so it still answers with an expired or missing access token. Compare against the version a route or feature was added in to tell "merged but not deployed" apart from a real bug. |
| `platform_tenant_list` | Read | List tenants visible to the caller — every tenant for an operations-tenant caller, otherwise just the caller's own. |
| `platform_tenant_create` | Work | Register a new tenant. Requires an operations-tenant caller. |
| `platform_tenant_repair-org-mapping` | Work | Repair a tenant already stuck with an unresolvable (issuer, org) mapping — one that lists but that no token can ever authenticate into. Converts `issuer` to org-scoped (if not already) and sets `tenantId`'s own org value. Requires an operations-tenant caller. There is no tenant delete on the platform, so this is the only way back short of direct database access. |
| `platform_identity_org_create` | Work | Create an organization on the platform's own identity provider — the org an org-scoped tenant mapping needs before `platform_tenant_create`'s `orgFieldValue` can produce a mapping any token will ever resolve to. Requires an operations-tenant caller. |
| `platform_user_list` | Read | List a tenant's users. `tenantId` targets another tenant and is honored only for an operations-tenant caller. |
| `platform_user_enroll` | Work | Enrol a user into a tenant. Same `tenantId` scoping as `platform_user_list`; `roleIds` names the roles to grant instead of the platform's default. |
| `platform_env_list` | Read | List the caller's tenant's hosted environments. |
| `platform_env_get` | Read | Fetch one hosted environment by id. |
| `platform_env_register` | Work | Register a hosted environment. For a runtime environment with `runtimeVersion` and a deploy executor configured, this also starts a server-side deploy — poll `platform_env_get` to watch it converge. |
| `platform_env_deploy` | Work | Start a server-side deploy of an already-registered environment. Fails with a conflict if one is already in progress. |
| `platform_env_stop` | Work (destructive, idempotent) | Scale a hosted environment's runtime to zero — the server-side equivalent of `erun stop`. |
| `platform_env_delete` | Work (destructive, idempotent) | Start deleting a hosted environment and tearing down its namespace — the server-side equivalent of `erun delete`. Not recoverable. This call never prompts: pass `confirm=true` to actually delete. The teardown runs in the background; poll `platform_env_get` for `"deleting"` → gone or `"deletion-blocked"`. |
| `platform_context_list` | Read | List the caller's tenant's cloud contexts (managed clusters) on the platform. |
| `platform_context_get` | Read | Fetch one platform-tracked cloud context by id. |
| `platform_context_create` | Work | **Billing warning:** without `planOnly`, this launches a real cloud VM and provisions k3s on it, billing the tenant's cloud account until stopped. `planOnly` asks the platform to resolve and return the bootstrap plan without creating anything — a real API call, distinct from `preview`, which skips the network call entirely. |
| `platform_provision` | Read | Resolve and return the ordered plan for provisioning a hosted environment — tenant, quota, context bootstrap or reuse, namespace, register, deploy — without executing any of it or writing to the database. |

Every `platform_*` tool that isn't `platform_provision` supports `preview` and is advertised `openWorld: true` (it reaches the platform's API over the network). All are agent-callable.

### Reviews — hosted code review and merge queue {#review-tools}

Drives the erun platform's review flow: open a review against a pushed branch, comment, close, and inspect or advance a target branch's merge queue. See [`erun review`](/cli/review).

| Tool | Read/Work | Purpose |
|---|---|---|
| `review_list` | Read | List reviews, narrowed by any combination of `targetBranch`, `sourceBranch`, `status`, `authorUserId`, and `reviewerUserId`. `mine`/`waitingOnMe` resolve to the caller's own user id via `platform_whoami`. |
| `review_show` | Read | Fetch one review together with its comment threads and recorded builds. |
| `review_create` | Work | Open a review. `name` is the eventual squash-merge message and must be unique per tenant. `sourceBranch` must already be pushed (`exec_push`) — the platform can only fetch what has actually landed on the remote. |
| `review_comment` | Work | Comment on a review line, or reply to an existing comment via `parentCommentId`. |
| `review_resolve` | Work (idempotent) | Resolve a comment thread by closing its root comment. `commentId` must be the thread's root — resolving a reply fails, naming the root to retry against. |
| `review_unresolve` | Work (idempotent) | Reopen a comment thread by marking its root comment `OPEN` again. Same root-only restriction as `review_resolve`. |
| `review_close` | Work (idempotent) | Close a review without merging it. |
| `review_record-build` | Work | Record a build against a review — the only way an erun client transitions a review off `OPEN`. A successful build moves it to `READY` (and on to `MERGE` if it was already the merge queue's head); a failed one moves it to `FAILED`. There is no separate tool to set a review's status directly: a `READY` with no build is a different thing entirely (the missed-merge-window requeue). `commitId` must be the full 40-character commit hash the build ran against, and `version` the version it minted — required even when `successful` is `false`, since `release` resolves the version before the build step runs. `gate` records the merge queue's own `GATE` build kind instead: the environment a review's merge queue promoted to `MERGE` reports its own build of the prospective merge this way, and omits `version` since the gate publishes nothing. A successful `gate` build that changes `erun-ui/**` is refused before any network call unless `desktopPlaywrightVerified` is also set — the gate's own build does not run `erun-ui/playwright` (issue #1933), so a green gate build proves nothing about the desktop frontend without that attestation; `projectRoot` overrides which checkout the change is diffed from, defaulting to the runtime repo path. |
| `review_report-merged` | Work | Report a review `MERGED`, for the environment a review's merge queue promoted to `MERGE` once it has fetched the review's target and source (`exec_gate-merge`), gate-built the result, recorded that as a successful `GATE` build (`review_record-build` with `gate` set), and pushed it. The platform verifies rather than trusts this: it checks `buildId` names an already-recorded, successful `GATE` build for this review, then fetches `remoteUrl` to confirm that build's commit is really reachable from the target branch's tip with the parent this review was gated against. Either check failing refuses with 409 `MERGE_NOT_VERIFIED` and leaves the review at `MERGE`. |
| `review_reviewers_list` | Read | List the users assigned to review a review. |
| `review_reviewers_add` | Work (idempotent) | Assign a reviewer, so an Agent can assign a peer Agent (or itself). `userId` must already be enrolled in the caller's own tenant — refused before the network call otherwise. Assigning a reviewer gates no status transition; see [merge queue](/collaboration/merge-queue) for what actually blocks a merge. |
| `review_reviewers_remove` | Work (destructive, idempotent) | Remove a reviewer from a review. |
| `review_queue_list` | Read | List a target branch's merge queue, in queue order. |
| `review_queue_advance` | Work | Advance a target branch's merge queue head to `MERGE`, starting that review's merge-gate build — a real, immediate mutation of shared control-plane state. Fails if the queue is empty or its head is not `READY`, and refuses with the unresolved comment thread count when the head still has open threads (resolve them with `review_resolve`, or use `review_queue_override-advance`). |
| `review_queue_override-advance` | Work | Bypass `review_queue_advance`'s unresolved-thread gate and advance anyway. `reason` is required and is recorded in the platform's audit trail alongside the caller's identity — a deliberate, accountable escape hatch, not a routine way to advance the queue. |

All fifteen support `preview` except the immediate writes (`review_create`, `review_comment`, `review_resolve`, `review_unresolve`, `review_close`, `review_record-build`, `review_report-merged`, `review_reviewers_add`, `review_reviewers_remove`, `review_queue_advance`, `review_queue_override-advance`), which run for real unless `preview` is set. All are agent-callable and `openWorld: true`.

### Idle & auto-stop history {#idle-stop-tools}

`idle` (above, under Inspection) reports the *current* idle state. These three cover the auto-stop record and the pending-warning lifecycle around it. See [Agent reference · Idle policy](/agent-reference/idle-policy).

| Tool | Read/Work | Caller | Purpose |
|---|---|---|---|
| `idle_stop_history` | Read | Agent | Return the last N (cap 10) auto-stop audit entries for the env, newest first, each carrying the per-marker idle/active breakdown captured when the auto-stop grace was armed. |
| `idle_stop_record` | Work (idempotent) | **Desktop-only** | Record a host-driven stop entry (`source=host-manual`). Called by the desktop's Stop button so the History tab can also explain "you clicked Stop" alongside the in-pod monitor's auto-stops. |
| `idle_stop_cancel` | Work (idempotent) | Agent | Dismiss the pending auto-stop grace warning for the env without touching AWS state. No-op when no warning is armed. |

None of the three are on the CLI — `idle_stop_history`/`_record`/`_cancel` are MCP-only wire primitives; `idle` itself wraps `erun idle`.

### Credential tools — desktop-only {#credential-tools-desktop-only}

| Tool | Purpose |
|---|---|
| `cloud_inject_aws_credentials` | Write temporary AWS credentials into the pod's `~/.aws/credentials` under the `erun-host` profile, replacing that profile in place. |
| `cloud_clear_aws_credentials` | Remove the `erun-host` profile from the pod's `~/.aws/credentials`. |

`cloud_inject_aws_credentials` takes the access key, secret, and session token as **tool arguments**, so **do not call it from anything that records its arguments** — an Agent transcript, a session log, an audit trail. It exists for the desktop app's credential refresher, which holds the values in memory. Everything else refreshes an environment's host credentials with [`erun cloud refresh <tenant> <environment>`](/cli/cloud#cloud-refresh), which reads the operator's own AWS profile and streams the credentials to the pod on stdin, so nothing sensitive passes through the caller.

### Working tree — typed mutations, no shell

The mutations an orchestrator performs constantly on an environment's own repository — writing file content, committing, and pushing — used to have no name of their own: doing any of them through `exec_raw` meant composing a heredoc or a `python3 -` script and passing it through a shell, so a backtick or a `$(...)` in the content changed the meaning of the command. None of the three tools below ever interprets its payload as a shell fragment.

| Tool | Purpose |
|---|---|
| `exec_write` | Write `content` to `path` in the runtime repo's working tree, byte-for-byte. `content` is a JSON string field, never composed into a command line, so it round-trips verbatim regardless of what it contains. Refuses if `path` would resolve outside the repo root. Reports the resolved path and byte count written. Set `preview` to trace the write without performing it. |
| `exec_commit` | Stage every change (or, with `paths` set, only those paths) in the runtime repo's working tree and commit it with `message`, taken the same way as `exec_write`'s content. `branch` is the caller's claim about the current branch, verified against `git rev-parse --abbrev-ref HEAD` rather than assumed — a mismatch is refused, loudly, instead of landing the commit on whichever branch HEAD happens to be on. When `paths` is set, the commit is refused just as loudly if the tree has changes outside the declared paths, so an unrelated writer's edits can never be absorbed into it. Reports the branch, commit id, and files committed. Set `preview` to verify the branch and trace the files that would be committed without committing. |
| `exec_push` | Push the runtime repo's working tree's current branch to a remote. `branch` must match the tree's actual current branch, checked the same way as `exec_commit`. A real, immediate mutation of shared remote state — push before opening a review with `review_create`, since the platform can only fetch a branch once it has actually landed there. Set `preview` to verify the branch and trace the push without running it. |
| `exec_merge` | Fetch `targetBranch` from a remote and merge it into the runtime repo's working tree's current branch with an explicit merge commit — never a rebase, since review comments anchor to a commit id and a rewrite would orphan every thread on an open review. A conflicted merge is reported as a distinct, named outcome rather than a generic failure; the worktree is left exactly as git left it, mid-merge, for the caller to resolve or run `git merge --abort`. A real, immediate mutation of the working tree. Set `preview` to trace the fetch and merge without running them. |
| `exec_gate-merge` | Build the prospective merge a merge queue promotion (or batch) gates: fetch `targetBranch` and every entry's `branch` in `sources`, check out a fresh local branch named `targetBranch` at its own current remote tip, then squash-merge each source onto it in turn, each as its own commit carrying its own `message` — one commit per landed source. Passing more than one entry in `sources` batches several unmerged branches into one prospective merge, so the gate that follows tests whether they compile *together*, not just individually; a single entry is the ordinary one-branch gate. For the environment a review's merge queue promotes to `MERGE`: gate-merge, then build against the result, then `review_record-build` with `gate` set and, only on success, `exec_push` and `review_report-merged`. The working tree must already be clean — this checks out a different local branch than whatever the tree is currently on, so uncommitted work there is refused rather than silently carried onto the prospective merge. A source whose squash conflicts is skipped, not fatal: the working tree is reset back to a clean state and the conflict (with its conflicted files) recorded in the result's `skipped` list, and the rest of the batch still gates against the tree as it stood before that attempt; a batch where every source is skipped returns an error. Set `preview` to trace the fetch, checkout, and each squash merge and commit without running them. |

Same commands as [`erun exec write`](/cli/exec#exec-write) / [`erun exec commit`](/cli/exec#exec-commit) / [`erun exec push`](/cli/exec#exec-push) / [`erun exec merge`](/cli/exec#exec-merge) / [`erun exec gate-merge`](/cli/exec#exec-gate-merge). `write`, `commit`, and `diff` (see below) are retired aliases for `exec_write`, `exec_commit`, and `exec_diff`, kept callable for one release (#1186) — new callers should use the `exec_*` names.

### Merge queue gate reporting

| Tool | Purpose |
|---|---|
| `exec_report-commit-status` | Report a commit status on GitHub for `commit` — the last step in the merge queue gate (`exec_gate-merge`, `build`, `review_record-build` with `gate` set): report success once the gate build is green, or failure the moment it is not, naming which gate step failed in `description`. A required status check on the remote's branch protection has nothing to require until this reports it. `commit` should be the review's source branch tip — the pull request's own head commit — never the local prospective squash-merge commit `exec_gate-merge` produces: GitHub only evaluates a required check against a commit reachable from the open pull request, and the squash commit does not exist there until after the gate has already passed and pushed. `context` defaults to `erun/merge-gate` when omitted. Reporting needs a GitHub token (`gh auth login`, or `GITHUB_TOKEN`/`GH_TOKEN`); a missing token refuses before any network call. Set `preview` to trace the request without sending it. |
| `exec_close-pr` | Close `branch`'s open pull request on GitHub, once `review_report-merged` has already succeeded, and record `landingCommit` on it — `exec_gate-merge`'s squash commit is never the branch head GitHub tracks, so GitHub never reconciles a queued merge with its pull request on its own; the commit that actually shipped exists nowhere the pull request can see until this reports it. Safe when `branch` has no open pull request against `targetBranch`: this is a no-op, not an error, since queueing a plain branch with no review is legitimate. Refuses, loudly, when the pull request's current head does not match `gatedCommit` — something pushed to `branch` after the gate fetched it, so the gated content is not what closing would discard. Set `preview` to trace the lookup without closing or commenting on anything. |
| `exec_reconcile-bypass` | Cross-reference GitHub's own bypass ledger for `rulesetId` on `targetBranch` against erun's gate runs and the repository's tags: every push that bypassed `rulesetId` is reported as `RECONCILED` (a `PASSED` gate run built one of the commits it landed), `RELEASE` (a tag points at one of them, so a release published it rather than a gated merge), `UNEXPECTED_ACTOR` (an identity `expectedActors` did not name exercised the bypass), or `UNRECONCILED` (nothing accounts for it). Matching covers every commit the push added, not only its tip — a release push carries three. The after-the-fact accountability check for the bypass the queue's own push structurally needs; `exec_plan-ruleset-bypass` is the other half. `rulesetId` is required and never defaulted; `remoteUrl` defaults to the checkout's `origin`. `since` optionally narrows the GitHub lookup window (`hour`, `day`, `week`, or `month`). The result's `unreconciled` and `unexpectedActors` counts are the loud signal — a caller must not treat either as routine. Set `preview` to trace the GitHub and platform lookups without sending them. |
| `exec_plan-ruleset-bypass` | Resolve the exact ruleset edit that makes one non-human queue identity the only actor holding an `always` bypass on a protected branch, computed from the live ruleset. GitHub's bypass is per-actor and per-ruleset, not per-rule, so adding a required status check on top of a broad `always` grant changes nothing for whoever holds it — see [merge queue § Narrowing who holds the grant](/collaboration/merge-queue#narrowing-the-bypass-grant). Emits two stages that never leave the branch unmergeable — stage 1 grants `queueActor` bypass alongside today's actors, stage 2 demotes every other `always` actor to `pull_request` — plus a rollback payload restoring today's list exactly, and returns the three file paths. `queueActor` is a login when `queueActorType` is `User` (the default), otherwise the numeric actor id. Refuses, naming which, when the queue identity cannot already push, when GitHub does not return the ruleset's bypass actors (it shows them only to a token with write access to the ruleset), or when `targetBranch` is not a branch this ruleset governs. **Never writes to GitHub** — applying the edit stays a deliberate human step. Set `preview` to trace the lookups and the files it would write without sending or writing anything. |

Same commands as [`erun exec report-commit-status`](/cli/exec#exec-report-commit-status) / [`erun exec close-pr`](/cli/exec#exec-close-pr) / [`erun exec reconcile-bypass`](/cli/exec#exec-reconcile-bypass) / [`erun exec plan-ruleset-bypass`](/cli/exec#exec-plan-ruleset-bypass). Unlike the working-tree tools above, none touches the runtime repo's git state — all four only call GitHub's REST API — so this is its own section rather than a row in the working-tree table.

### Proving a deployed plane actually serves what merged

| Tool | Purpose |
|---|---|
| `exec_route-check` | Prove every route erun-backend-api's router registers is actually reachable on the plane `alias` resolves, rather than trusting that merged code is deployed code. Reads the route inventory straight out of erun-backend-api's own source (never a hand-maintained list) and sends a plain GET to each one; first sanity-probes `GET /v1/whoami` and reports the plane unreachable — rather than every route missing — when that alone does not answer. Every probe is a GET regardless of a route's own registered method, since the router reports `405` for a path it knows under a different method, so this never risks creating, updating, or deleting anything on the plane; only the plane's own unmodified `404 page not found` body means a route was never registered at all — a well-formed request for an id that doesn't exist always gets erun-backend-api's own JSON error shape instead, and is reported reachable. The result's `planeReachable`, `missing`, and `errors` fields are the loud signal — a caller must not treat a non-empty `missing` list as routine. `routesDir` overrides the default `erun-backend-api/internal/routes` path resolved from the runtime repo's project root. Set `preview` to trace the resolved plane and route inventory without sending any request. |

Same command as [`erun exec route-check`](/cli/exec#exec-route-check).

### Gate runs — what is being gated now, and what recent gates decided {#gate-runs}

A gate run is the first-class record of one attempt to gate a prospective merge, independent of whether an erun review exists for the change it gates — a repository whose changes arrive as GitHub pull requests, with no erun review at all, is exactly the case this exists for. See [merge queue](/collaboration/merge-queue) for how a review-driven gate fits alongside this.

| Tool | Read/Work | Purpose |
|---|---|---|
| `exec_gate-run_start` | Work | Record the beginning of one gate attempt: `sourceBranch`, `targetBranch`, `sourceCommit`, and the prospective squash-merge commit `mergeCommit`. Returns the new gate run's id. A run with no trackable running phase at all — a squash conflict before any build ever starts — may set `status` directly to `FAILED` or `INCONCLUSIVE` and omit `mergeCommit`. `reviewId` links the run to an erun review, when one exists. Set `preview` to trace the request without sending it. |
| `exec_gate-run_report` | Work | Move `gateRunId` from `RUNNING` to a terminal verdict: `PASSED`, `FAILED`, or `INCONCLUSIVE`. A wrapper that hit its own timeout, or a run interrupted by an environment-specific fault, must report `INCONCLUSIVE` — never `FAILED`, which asserts a real gate step actually produced a red verdict. `failingStep` is required when `status` is `FAILED`. A `FAILED` report is not taken at face value: if `failingStep`/`logRef` (or the file it names) matches one of erun's own known infrastructure-failure signatures, the platform silently upgrades it to `INCONCLUSIVE` before recording it. Reporting against a gate run that already has an outcome is refused (409): a verdict is immutable once reached. Set `preview` to trace the request without sending it. |
| `gate_list` | Read | List gate runs, most recent first, narrowed by any combination of `targetBranch`, `sourceBranch`, and `status`. Each entry names the branch, the prospective merge commit actually tested, the target, and the verdict, and for a `FAILED` one, `failingStep` and `logRef` (where to read it). `RUNNING` means gating right now; `INCONCLUSIVE` means unresolved, not failed. |
| `gate_show` | Read | Fetch one gate run by `gateRunId`. |

Same commands as [`erun exec gate-run start`](/cli/exec#exec-gate-run-start) / [`erun exec gate-run report`](/cli/exec#exec-gate-run-report) / [`erun gate list`](/cli/gate#gate-list) / [`erun gate show`](/cli/gate#gate-show). All four support `preview`; `gate_list` and `gate_show` are read-only. `exec_gate-run_start`/`exec_gate-run_report` are agent-callable only — the environment driving the gate reports its own attempt, never something an operator clicks. `gate_list`/`gate_show` are agent-callable too as this feature's first cut; a console/desktop surface is planned as a follow-up.

### Escape hatch

| Tool | Purpose |
|---|---|
| `exec_raw` | Run an arbitrary `argv` in the runtime pod. Last-resort escape hatch — see [raw spec](#raw--the-escape-hatch). Retired alias: `raw`. |

### Tool selection rule

In order of preference: **inspection > action > working tree > jobs > `exec_raw`.**

- If a question is "what's the state of X" — reach for an inspection tool.
- If you're invoking a known CLI command — reach for the action wrapper, not `exec_raw`.
- If the work is long-running and you will need to know how it ended — reach for a [backgrounded job](#job-tools) (`exec_raw` with `wait: false`, or `exec_agent`), not a foreground `exec_raw` call. A foreground call returns only when the process exits, so using it for lifecycle observation means re-implementing job bookkeeping in shell.
- If none of the above apply — use `exec_raw`.

Generating conventional code (a new service, a migration job, an Ingress, …) isn't a tool-call decision — load the relevant [skill](/concepts/skills) and write the files by hand. The skill teaches the convention; the MCP surface stays out of the generation path.

Every call lands in the audit trail with its tool name, so `exec_raw` invocations are immediately distinguishable from typed ones.

### Full tool index {#full-tool-index}

Every tool the server can register, one row each, grouped by `_meta.family` and matching `erun-common`'s `MCPToolDescriptor` table exactly — a scripted test (`TestMCPOverviewDocumentsEveryTool` in `erun-mcp`) fails the build if a tool is registered here without a row below, or a row below names a tool that isn't registered. Retired aliases (`diff`, `raw`, `write`, `commit`, `workspace_sync`) are omitted; see [Working tree](#working-tree--typed-mutations-no-shell) and [Host-served](#host-served) above for those.

| Family | Tool | CLI equivalent | Read/Work |
|---|---|---|---|
| *(top-level)* | `version` | `erun version` | Read |
| *(top-level)* | `list` | `erun list` | Read |
| *(top-level)* | `environment` | *(MCP-only)* | Read |
| *(top-level)* | `init` | `erun init` | Work |
| *(top-level)* | `build` | `erun build` | Work |
| *(top-level)* | `push` | `erun push` | Work |
| *(top-level)* | `deploy` | `erun deploy` | Work |
| *(top-level)* | `publish` | `erun publish` | Work |
| *(top-level)* | `upgrade` | `erun upgrade` | Work |
| *(top-level)* | `release` | `erun release` | Work |
| *(top-level)* | `pin` | `erun pin` | Work |
| *(top-level)* | `expose` | `erun expose` | Work |
| *(top-level)* | `unexpose` | `erun unexpose` | Work |
| *(top-level)* | `terraform` | `erun terraform` | Work |
| *(top-level)* | `doctor` | `erun doctor` | Work |
| *(top-level)* | `observe` | `erun observe` | Read |
| *(top-level)* | `usage` | `erun usage` | Read |
| *(top-level)* | `resize` | `erun resize` | Work |
| *(top-level)* | `delete` | `erun delete` | Work |
| exec | `exec_diff` | `erun exec diff` | Read |
| exec | `exec_raw` | `erun exec raw` | Work |
| exec | `exec_write` | `erun exec write` | Work |
| exec | `exec_commit` | `erun exec commit` | Work |
| exec | `exec_push` | `erun exec push` | Work |
| exec | `exec_merge` | `erun exec merge` | Work |
| exec | `exec_gate-merge` | `erun exec gate-merge` | Work |
| exec | `exec_report-commit-status` | `erun exec report-commit-status` | Work |
| exec | `exec_close-pr` | `erun exec close-pr` | Work (idempotent) |
| exec | `exec_gate-run_start` | `erun exec gate-run start` | Work |
| exec | `exec_gate-run_report` | `erun exec gate-run report` | Work |
| exec | `exec_reconcile-bypass` | `erun exec reconcile-bypass` | Read |
| exec | `exec_plan-ruleset-bypass` | `erun exec plan-ruleset-bypass` | Read |
| exec | `exec_route-check` | `erun exec route-check` | Read |
| exec | `exec_agent` | *(MCP-only; the CLI covers this as `erun exec job start --agent`)* | Work |
| exec | `exec_job_attach` | `erun exec job attach` | Work |
| exec | `exec_job_status` | `erun exec job status` | Read |
| exec | `exec_job_await` | `erun exec job await` | Read |
| exec | `exec_job_output` | `erun exec job output` | Read |
| exec | `exec_job_cancel` | `erun exec job cancel` | Work |
| exec | `job_start` | *(removed; see `exec_raw` `wait: false` / `exec_agent`)* | Read |
| cloud | `cloud_list` | *(MCP-only)* | Read |
| cloud | `cloud_init_aws` | `erun cloud init aws` | Work |
| cloud | `cloud_init_cloudflare` | `erun cloud init cloudflare` | Work |
| cloud | `cloud_init_erun` | `erun cloud init erun` | Work |
| cloud | `cloud_login` | `erun cloud login` | Work |
| cloud | `cloud_oidc` | `erun cloud oidc` | Work |
| cloud | `cloud_set` | `erun cloud set` | Work |
| cloud | `cloud_inject_aws_credentials` | *(MCP-only, desktop-only)* | Work |
| cloud | `cloud_clear_aws_credentials` | *(MCP-only, desktop-only)* | Work |
| context | `context_list` | `erun context list` | Read |
| context | `context_init` | `erun context init` | Work |
| context | `context_start` | `erun context start` | Work |
| context | `context_stop` | `erun context stop` | Work |
| platform | `platform_whoami` | `erun platform whoami` | Read |
| platform | `platform_version` | `erun platform version` | Read |
| platform | `platform_tenant_list` | `erun platform tenant list` | Read |
| platform | `platform_tenant_create` | `erun platform tenant create` | Work |
| platform | `platform_tenant_repair-org-mapping` | `erun platform tenant repair-org-mapping` | Work |
| platform | `platform_identity_org_create` | `erun platform identity org create` | Work |
| platform | `platform_user_list` | `erun platform user list` | Read |
| platform | `platform_user_enroll` | `erun platform user enroll` | Work |
| platform | `platform_env_list` | `erun platform env list` | Read |
| platform | `platform_env_get` | `erun platform env get` | Read |
| platform | `platform_env_register` | `erun platform env register` | Work |
| platform | `platform_env_deploy` | `erun platform env deploy` | Work |
| platform | `platform_env_stop` | `erun platform env stop` | Work |
| platform | `platform_env_delete` | `erun platform env delete` | Work |
| platform | `platform_context_list` | `erun platform context list` | Read |
| platform | `platform_context_get` | `erun platform context get` | Read |
| platform | `platform_context_create` | `erun platform context create` | Work |
| platform | `platform_provision` | `erun platform provision` | Read |
| review | `review_list` | `erun review list` | Read |
| review | `review_show` | `erun review show` | Read |
| review | `review_create` | `erun review create` | Work |
| review | `review_comment` | `erun review comment` | Work |
| review | `review_resolve` | `erun review resolve` | Work |
| review | `review_unresolve` | `erun review unresolve` | Work |
| review | `review_close` | `erun review close` | Work |
| review | `review_record-build` | `erun review record-build` | Work |
| review | `review_report-merged` | `erun review report-merged` | Work |
| review | `review_reviewers_list` | `erun review reviewers list` | Read |
| review | `review_reviewers_add` | `erun review reviewers add` | Work |
| review | `review_reviewers_remove` | `erun review reviewers remove` | Work |
| review | `review_queue_list` | `erun review queue list` | Read |
| review | `review_queue_advance` | `erun review queue advance` | Work |
| review | `review_queue_override-advance` | `erun review queue override-advance` | Work |
| gate | `gate_list` | `erun gate list` | Read |
| gate | `gate_show` | `erun gate show` | Read |
| idle | `idle` | `erun idle` | Read |
| idle | `idle_stop_history` | *(MCP-only)* | Read |
| idle | `idle_stop_record` | *(MCP-only, desktop-only)* | Work |
| idle | `idle_stop_cancel` | *(MCP-only)* | Work |
| whip | `whip` | `erun whip` | Work |
| activity | `activity_lease_list` | *(MCP-only)* | Read |
| activity | `activity_lease_take` | *(MCP-only)* | Work |
| activity | `activity_lease_release` | *(MCP-only)* | Work |
| activity | `ai_sessions` | *(MCP-only)* | Read |
| outputs | `outputs_list` | `erun outputs list` | Read |
| outputs | `outputs_download` | `erun outputs download` | Read |
| inputs | `inputs_upload` | `erun inputs upload` | Work |
| sshd | `sshd_sync` | `erun sshd sync` | Work |
| contribute | `contribute_clone` | `erun contribute clone` | Work |

77 tools in total. `inputs_upload` and `sshd_sync` are [host-served](#host-served): answered by `erun mcp proxy` on the operator's machine, not relayed to the pod edge.

## Why typed tools

Anyone running a long-lived Agent in a shared environment wants two things: the Agent should *be able to act* (otherwise it's useless), and the Operator should *see what it did* (otherwise it's unsafe). Typed MCP tools deliver both — structured input/output the Operator can audit, with `exec_raw` available for emergencies. As the Agent earns trust through audit, the Operator can grant more autonomy without losing the loop.

## Structured tool schemas

The structured tools take no arguments unless noted. Outputs are typed JSON.

### `idle`

Resolves the env's idle policy and reports its current activity. Useful for an Agent to decide whether to stop or keep going. Asking never counts as activity itself, however often it's polled — see [Agent reference · Idle policy](/agent-reference/idle-policy#last_terminal_input) for the full list of tools this applies to.

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
  },
  "leases": [
    { "id": "agent-run", "name": "agent-run", "pid": 4242, "expiresAt": "2026-05-25T14:50:00Z" }
  ]
}
```

### `ai_sessions`

Resolves the structured status of AI tool sessions in this environment — `idle`, `busy`, `awaiting-input`, `exited`, or `oom-killed` — from each session's own last reported turn-boundary event, never from PTY output volume or silence. A session that finished its turn and is waiting on the Operator produces no output at all, which is exactly what an idle or finished session also looks like from the outside; only a direct signal from the tool distinguishes the two, which is what this tool reports. Pass `session` to resolve one session; omit it to list every session recorded for the environment.

The write side is the CLI verb `erun activity ai-session report`, which a tool's own hooks invoke at each turn boundary (`turn-start`, `tool-use`, `turn-end`, `notify`, `exit`) — there is no MCP write tool for this today, since the natural caller is the hook's own shell command running inside the pod, not a remote client.

```jsonc
// ai_sessions { "session": "abc123" }
{
  "sessions": [
    {
      "sessionId": "abc123",
      "tool": "claude",
      "state": "awaiting-input",
      "reason": "finished its turn and is waiting for your next message",
      "lastActivity": "2026-05-25T14:31:02Z"
    }
  ]
}
```

An `exited` or `oom-killed` session additionally carries `exitCode` when the process reported one. `oom-killed` is reported only when the caller that recorded the exit explicitly said so (`exitReason: "oom"`) — detecting the kill itself (a cgroup `memory.events` read, a `dmesg` scan) is the reporting side's job, not this tool's.

### `whip`

Pushes the pacing nudge into this environment's own AI session (the tab `erun open`'s `--ai` flag reattaches to), on demand rather than waiting for the desktop's own schedule-driven pass. It always acts explicitly — ignoring how recently the session moved — but never bypasses the consecutive-nudge cap: a session that already hit the cap stays capped until it shows fresh activity on its own. Set `preview: true` to resolve the decision without writing anything.

```jsonc
// whip {}
{
  "candidate": {
    "kind": "environment",
    "id": "myapp/dev",
    "name": "myapp/dev",
    "reachable": true,
    "alive": true,
    "lastActiveAt": "2026-05-25T14:31:02Z",
    "nudgeCount": 1
  },
  "decision": 1,
  "reason": "nudge",
  "pushed": true
}
```

`decision` is `0` (none), `1` (nudge), or `2` (cap); `reason` is always one of `not-alive` (no live AI session in this pod to push), `already-capped`, `cap-crossed` (this call just hit the cap), or `nudge`. `pushed` is `false` whenever `preview` was set or the write itself failed (`error` then names why) — never assume a `nudge` decision means the text landed. See [Agent reference · pacing](/agent-reference/skills-spec#periodic-pacing-re-statement-and-crash-auto-resume) for the message text and cap semantics, and [`erun whip`](/cli/whip) for the host-side command that fans this out across every configured environment (and reports every persisted orchestrator as unreachable from this transport, since an MCP server never holds another process's session).

### `observe`

Reads pods, quota/limit usage, ingress routing, and certificate readiness for the env's namespace — read-only, every underlying call is a `kubectl get`. Optional `secrets` input checks named Secret/key pairs for presence without reading their values.

```jsonc
// observe {"secrets": [{"name": "db-credentials", "key": "password"}]}
{
  "tenant": "myapp",
  "environment": "prod",
  "namespace": "myapp-prod",
  "pods": [
    { "name": "web-0", "phase": "Running", "ready": true, "restartCount": 0 }
  ],
  "resourceQuotas": [
    { "name": "erun-quota", "hard": { "limits.cpu": "4" }, "used": { "limits.cpu": "1" } }
  ],
  "limitRanges": [
    { "name": "erun-limits", "limits": [
      { "type": "Container", "default": { "cpu": "1" }, "defaultRequest": { "cpu": "100m" } }
    ] }
  ],
  "ingresses": [
    { "name": "web", "hosts": ["prod.example.com"],
      "tls": [{ "hosts": ["prod.example.com"], "secretName": "web-tls" }] }
  ],
  "certificates": [
    { "name": "wildcard", "ready": false, "reason": "Issuing", "message": "waiting for order to complete",
      "secretName": "wildcard-tls", "dnsNames": ["*.prod.example.com"],
      "orders": [
        { "name": "wildcard-order-1", "state": "pending",
          "challenges": [
            { "name": "wildcard-challenge-1", "type": "DNS-01", "dnsName": "*.prod.example.com",
              "state": "invalid", "reason": "RBAC denied: solvers.acme.cert-manager.io is forbidden: cannot create resource challenges" }
          ] }
      ] }
  ],
  "secrets": [
    { "name": "db-credentials", "key": "password", "exists": true, "hasKey": true }
  ]
}
```

`orders` is populated only for a Certificate that isn't `ready` — a healthy certificate reports no chain to walk. A `secrets` entry always reports `exists`/`hasKey`; a missing Secret reports `exists: false` rather than erroring, and a non-"not found" failure (e.g. an RBAC denial reading the Secret itself) is carried in an `error` field instead of being reported as absence.

### `usage`

Reads CPU quota utilisation, memory against the container's own cgroup limit, and disk usage for the workspace mount, straight from the runtime container's cgroup v2 accounting — no cluster metrics add-on involved, unlike `kubectl top`.

```jsonc
// usage {}
{
  "tenant": "myapp",
  "environment": "prod",
  "cpu": { "quotaCores": 1, "utilizationPercent": 12.4, "intervalSeconds": 1 },
  "memory": { "currentBytes": 413589504, "peakBytes": 1027301376, "limitBytes": 2147483648, "percentOfLimit": 19.3, "oomKills": 0 },
  "disk": [ { "mount": "/home/erun", "totalBytes": 202991730688, "usedBytes": 101495865344, "percentUsed": 50.0 } ],
  "excludesBuilds": true
}
```

Every field reports its own unavailability rather than failing the call: a cluster on cgroup v1 (or with `/sys/fs/cgroup` missing) reports `cpu.unavailable`/`memory.unavailable` with the reason instead of a fabricated zero, and an unlimited `memory.max` reports `memory.unlimited: true` rather than a percentage with no denominator. A `warnings` array appears only when a named threshold is crossed (memory ≥ 85% of its limit, `memory.peak` ≥ 95%, or a watched mount ≥ 90% used) — a heavily-loaded environment might return:

`excludesBuilds` is `true` on every environment whose type carries the `erun-dind` sidecar (all but `runtime` and `host`), omitted otherwise: `cpu`/`memory` above are scoped to the `erun-devops` container alone, and an image build actually runs in `erun-dind` — a separate cgroup this reading has no path to, since its build containers are cgroup siblings rather than descendants. This names that gap rather than let a busy build read as an idle environment; `observe` reports the sidecar's own resource limits.

```jsonc
{
  "memory": { "currentBytes": 1932735283, "limitBytes": 2147483648, "percentOfLimit": 90.0, "peakBytes": 2100000000, "oomKills": 1 },
  "warnings": [
    "memory is at 90% of its 2048Mi limit (warns at 85%)",
    "the cgroup recorded 1 OOM kill(s)"
  ]
}
```

`intervalSeconds` (input, default 1, clamped to 0.1–30) sets the CPU sample window: `usage_usec` is read, the window elapses, then it is read again, so utilisation is a rate over the interval rather than a meaningless cumulative counter.

When retained usage history has accumulated a [standing sizing recommendation](/cli/list#the-sizing-recommendation), it rides along as a `sizing` field — the same verdicts and evidence window `erun list` reports under `runtime-pod:` — so a caller checking on an environment does not need a separate `resize` call just to see it. Omitted when nothing has been observed yet.

### `resize`

Changes the runtime pod's and/or the `erun-dind` sidecar's CPU/memory limits and rolls them out, without re-running `init` to change these numbers. Takes either explicit `cpu`/`memory` for the runtime pod (each optional; naming only one leaves the other unchanged) or `applyRecommendation: true` to size the runtime pod from this environment's own standing sizing recommendation — resolved from usage history retained inside this pod, so the value is never retyped by the caller. `dindCpu`/`dindMemory` size the sidecar instead — the container that actually runs `erun build`/`erun release` — independent of the runtime-pod inputs and combinable with them (or with `applyRecommendation`) in the same call, since the sidecar has no standing recommendation of its own. See [Agent reference · `erun resize`](/agent-reference/cli-flags#erun-resize) for exactly what the recommendation reasons about and the resolution algorithm.

```jsonc
// resize { "applyRecommendation": true, "dindMemory": "24Gi" }
{
  "plan": {
    "tenant": "myapp", "environment": "prod",
    "current": { "cpu": "4", "memory": "8916Mi" },
    "target":  { "cpu": "6", "memory": "8916Mi" },
    "dindCurrent": { "cpu": "4", "memory": "20Gi" },
    "dindTarget":  { "cpu": "4", "memory": "24Gi" },
    "actions": [
      { "resource": "cpu", "from": "4", "to": "6" },
      { "resource": "dind-memory", "from": "20Gi", "to": "24Gi" }
    ],
    "noOp": false
  }
}
```

Before resolving the runtime-pod target, the standing recommendation's own reasoning is traced — one `sizing:` line per resource verdict plus a `sizing-evidence:` line, both readable in the tool's `trace` output — even when the resolved plan turns out to be a no-op, so "already sized" always comes with the window and counters it was decided from.

A resize whose resolved runtime-pod and sidecar targets both equal their current recorded sizes is a no-op (`plan.noOp: true`) and does not deploy. Both containers' own limits move — the throttle/OOM ceiling and the namespace `ResourceQuota` draw those limits count against — but never the scheduler's request for either container (a small fixed value independent of this setting) or any PVC; disk is out of scope until the chart's PVC sizes are values-driven.

Because a resize rolls the pod (`Recreate` strategy) and would kill any live session inside it, it first loads the environment's activity leases (build, deploy, or an agent session — anything currently held) and refuses, naming every holder, unless `overrideLease: true` is passed. The refusal is a plain tool error (see [Tool-call error responses](#tool-call-error-responses)) whose message names each holder:

> resize refused: this environment is held by orchestrator eng-42, user jane@example.com (lease "exec_job_attach") — a resize restarts the runtime pod and would interrupt that work; pass the override to resize anyway, or wait until it finishes

`orchestrator` names the calling orchestrator on the resize's own lease and on the override, if one was needed, so a held-lease refusal names a holder to go ask. Set `preview` to resolve and trace the plan (current → target per resource, held leases, whether an override was used) without changing anything.

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

A failing check returns `status: "fail"` and a `detail` describing the symptom. Agents should prefer running `doctor` before `exec_raw` when they see unexpected behaviour.

`doctor` also reports why a deploy may have failed (helm release status + runtime pods, read-only) and can recover a failing runtime release. Two boolean inputs request the recovery actions, each mutating the live release: `clearPendingHelm` clears a stuck helm pending-install/upgrade lock, and `rollback` rolls the release back to its last successful revision. They are alternative fixes — requesting both in one call is rejected. See [CLI flag spec · Deploy recovery actions](/agent-reference/cli-flags#deploy-recovery-actions) for the exact commands and when to use each.

Two string inputs restore a config from its dated backup before any tenant/env work: `restoreConfigFromBackup` recovers the root erun config, and `restoreEnvConfigFromBackup` recovers the target environment's `config.yaml` (requires explicit `tenant` + `environment`). Each takes a `YYYY-MM-DD` stamp or an absolute path; under `preview` the copy is reported but not performed. See [Config backups](/reference/config-locations#config-backups).

### `list`

Same data as the CLI `erun list`, structured. Returns the caller's tenants, envs, and effective target.

Pass `controlPlanes: true` to additionally check every configured erun-hosted control plane's deployed version, and its linked console's deployed version, against the newest version erun's own registry has actually published — deployed-vs-published, not deployed-vs-main; the result gains a `controlPlaneVersionDrift` field alongside the ordinary list result. This makes real network calls (each plane's own `GET /v1/platform`, each plane's linked console's `GET /version.json`, plus a registry lookup); pass `preview: true` to trace which planes, consoles, and registry lookup would be checked instead of making any call. See [CLI flag spec · Control plane versions](/agent-reference/cli-flags#control-plane-versions) for the full field contract.

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

### `environment`

The environment read model: one call composing what `list`, `idle`, and `doctor` already report into a single resolved answer. `state` is the field the other three cannot give you on their own — `running`, `idle`, `deploy-failed`, `stopped`, or `unknown` — derived from the environment's cloud-context power state (when observed), its deploy health, and its idle-policy eligibility.

`state` is `unknown` whenever a signal it depends on was never observed, rather than a guessed `stopped` or `idle`. This matters most for a managed-cloud environment: its cloud-context power state is a live AWS reading this package never persists to disk, and no AWS credential reaches inside this pod to refresh it — so unless something else already refreshed it in the same process, `cloudContext` carries the environment's cloud-context config with no `status`, and `state` reads `unknown`. Pass `preview: true` to skip the live `helm`/`kubectl` deploy diagnosis (the only part of this call that touches the cluster) and get `health: null` back instead of running it.

```jsonc
{
  "tenant": "myapp",
  "environment": {
    "name": "local",
    "type": "local-agent",
    "runtimeVersion": "1.0.308",
    "managedCloud": false,
    "isDefault": true,
    "isEffective": true
  },
  "state": "running",
  "idle": {
    "policy": { "timeout": "5m0s", "workingHours": "09:00-19:00" },
    "stopEligible": false
  },
  "health": {
    "rootConfig": { "configStatus": "ok" },
    "deploy": { "helmStatus": "STATUS: deployed" }
  }
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

### `build`

Trigger a build. Same semantics as the CLI `erun build` — it builds the images and **mints the version** an Agent then threads into `push`/`deploy`. Returns the minted `version` plus typed status per component.

**Input:**

| Field | Type | Description |
|---|---|---|
| `components` | string[] (optional) | Specific components to build. Omitted = build the resolved scope (cwd-based, see [Conventions](/concepts/conventions#how-erun-commands-resolve-scope)). |
| `release` | bool (optional) | Pin a bare release version instead of minting a snapshot. |
| `force` | bool (optional) | Bypass the fingerprint cache. |
| `dry_run` | bool (optional) | Preview without building. |
| `platforms` | string[] (optional) | Docker `--platform` overrides (e.g. `["linux/amd64"]`) for an environment that can only ever run one architecture; takes precedence over the project's configured `environments.<env>.docker.platforms`. Mutually exclusive with `release`, which always publishes every platform erun supports. See [Multi-architecture](/cli/build#multi-architecture). |

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

### `exec_raw` (backgrounded) / `exec_job_await` / `exec_job_output`

The job record is the shared shape every job tool returns. It is deliberately explicit about what is *not* known: `exitCode` is `null` in `running` and `unknown` — the two states where no outcome was ever observed — and set to the captured code in both `exited` and `abandoned`, so a missing outcome can never be read as a zero one.

```jsonc
// exec_raw {"command": ["./gradlew", "test"], "name": "suite", "wait": false}
{
  "executed": true, "jobId": "suite", "state": "running", "wait": false
}
```

The immediate response only confirms the job started; `exec_job_status` returns the full record:

```jsonc
// exec_job_status {"id": "suite"}
{
  "job": {
    "id": "suite",                 // defaults to name; addresses the job from here on
    "name": "suite",
    "state": "running",            // running | exited | abandoned | unknown
    "command": ["./gradlew", "test"],
    "dir": "/home/erun/git/team",
    "pid": 4242,                   // the supervisor — what liveness is decided by
    "childPid": 4243,              // the work — what exec_job_cancel signals
    "startedAt": "2026-08-07T09:14:02Z",
    "exitCode": null,
    "logPath": "/home/erun/.cache/erun/activity/team/dev/jobs/suite.log",
    "outputBytes": 0,
    "outputLimitBytes": 4194304,
    "leaseId": "job-suite",        // the activity lease held for the job's lifetime
    "lastAliveAt": "2026-08-07T09:14:03Z",
    "aliveSeq": 1,
    "aliveAgeMs": 210               // see [The alive contract](#alive-contract), above
  }
}
```

`exec_job_await` wraps it, and separates "not finished yet" from every outcome:

```jsonc
// exec_job_await {"id": "suite", "timeoutSeconds": 30} — still running
{ "job": { "id": "suite", "state": "running", "exitCode": null, "aliveAgeMs": 340, … },
  "timedOut": true, "waitedSeconds": 30, "timeoutSeconds": 30 }

// exec_job_await {"id": "suite", "timeoutSeconds": 30} — finished, and it failed
{ "job": { "id": "suite", "state": "exited", "exitCode": 42,
           "endedAt": "2026-08-07T09:18:41Z", "outputBytes": 81204, … },
  "timedOut": false, "waitedSeconds": 4, "timeoutSeconds": 30 }

// exec_job_await {"id": "gate", "timeoutSeconds": 30} — the job's own process
// exited, but it left work running behind it in its process group (e.g. a
// gate it backgrounded and returned past); exitCode is captured, same as the
// exited case, but this is never a success whatever it says
{ "job": { "id": "gate", "state": "abandoned", "exitCode": 0,
           "reason": "the job's own process exited, but it left other processes still running in its process group — background work it started and never waited for; nothing further will be reported for that work" },
  "timedOut": false, … }

// a job whose supervisor vanished — never reported as success, and as
// terminal as the exited case above: never re-wait on this expecting a
// different answer
{ "job": { "id": "suite", "state": "unknown", "exitCode": null, "aliveAgeMs": 6120,
           "reason": "job supervisor 4242 is gone without recording an exit status; the runtime pod was most likely replaced" },
  "timedOut": false, … }

// an agent job (kind: agent) whose working tree still had uncommitted
// changes when it ended: exitCode 0, state exited, but never a success —
// the supervisor made a checkpoint commit and pushed it on the agent's
// behalf. See [Agent reference · Working tree checkpoints](/agent-reference/cli-flags#worktree-checkpoints).
{ "job": { "id": "sweep", "state": "exited", "exitCode": 0, "kind": "agent",
           "worktreeDirty": true, "worktreeBranch": "feature/1564-checkpoint",
           "worktreeCommit": "a1b2c3d", "worktreePushed": true, "worktreeRemote": "origin" },
  "timedOut": false, … }
```

`exec_job_output` pages through the merged stdout + stderr:

```jsonc
// exec_job_output {"id": "suite", "offset": 4096}
{ "job": { … }, "offset": 4096, "nextOffset": 69632,
  "output": "…", "hasMore": true, "complete": false }
```

`hasMore` describes this read; `complete` is true only when the job has finished *and* this page reached the end. Whether output was dropped at the cap is `job.outputTruncated`, not either of those. A background action-tool job (see [the job envelope](#job-envelope)) has no output log; read its typed result off `job.result` instead. Field-by-field semantics, the retention window, and error behaviour: [Agent reference · `erun exec job`](/agent-reference/cli-flags#erun-job).

An [agent job](#agent-jobs) carries three more fields on the same record. `progress` is absent until the run emits its first event, so an agent that has not started yet is never reported as an idle one:

```jsonc
// exec_job_status {"id": "sweep"} — an agent run in flight
{ "job": {
    "id": "sweep", "state": "running",
    "kind": "agent", "agentTool": "claude",
    "aliveAgeMs": 450,
    "progress": {
      "tool": "claude",
      "activity": "editing erun-common/mcp_client.go",
      "lastTool": "Edit", "lastTarget": "erun-common/mcp_client.go",
      "turns": 12, "toolsRun": 47, "events": 133,
      "lastMessage": "Rewriting the reconnect path."
    }, … } }
```

### Other action tools

`push`, `publish`, `release`, `upgrade`, `pin`, `expose`, `unexpose`, `terraform`, `doctor`, `observe`, `init`, `delete`, `contribute_clone`, and every `cloud_*`/`context_*`/`platform_*`/`review_*`/`idle_stop_*`/`activity_lease_*` tool follow the same shape — typed `arguments` matching their CLI flags (where a CLI command exists; see the [full tool index](#full-tool-index) for which do not), typed `result` payload mirroring the CLI's structured output where applicable. The MCP tool name matches the CLI subcommand exactly wherever one exists. Like the CLI, the `push` tool takes a **required** `version` (it publishes a specific version's image + chart and never mints one); omitting it is rejected with `NO_VERSION`. Field-by-field semantics, flag defaults, and per-tool error codes live in the [CLI flag spec](/agent-reference/cli-flags) — every CLI command listed there corresponds 1:1 to the MCP tool of the same name.

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

## `exec_raw` — the escape hatch {#raw--the-escape-hatch}

`exec_raw` runs an arbitrary `argv` inside the runtime pod's `erun-devops` container. Reserved for actions the structured tools don't cover. Retired alias: `raw`, kept callable for one release (#1186).

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
- Every call is recorded in the audit trail with `argv`, `cwd`, exit code, and timestamp. There is no anonymous `exec_raw` call.

**When to use it (and when not):**

- ✅ Inspecting state the structured tools don't surface (a specific log file, a kubectl debug command).
- ✅ Running project-specific scripts (`./scripts/seed-test-data.sh`).
- ❌ Anything an existing tool covers — `list`, `doctor`, `idle` give typed output that's much easier for the Operator to scan.
- ❌ Long-running work you need to observe or come back to — `exec_raw` is request/response; it returns when the process exits. Use the [job tools](#job-tools), which detach the work, capture its exit status in the env, and let you poll status and output by handle.
- ❌ Long-running daemons — same reason. Start them as a job and cancel by handle.

## Skills are not MCP tools

The Agent's path to "add a Go service" / "add a migration job" / "add an Ingress" does not go through MCP. ERun ships those as [skill bundles](/concepts/skills) deployed into the env's runtime image; the Agent's own skill loader picks them up. The Agent reads the relevant SKILL.md, then writes the source + Dockerfile + chart by hand — no `scaffold` tool call, no template generator, no MCP round-trip for code generation.

For the on-disk format, deployment mechanism, and built-in skill catalogue, see [Agent reference · Skills spec](/agent-reference/skills-spec).

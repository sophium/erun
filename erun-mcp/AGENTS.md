# AGENTS.md

Module-specific guidance for `erun-mcp`. Follow the repository root `AGENTS.md` first, then apply this file.

`erun-mcp` is the MCP transport — the `emcp` executable and its HTTP MCP server. It follows the shared Go conventions in `erun-common/AGENTS.md` (code organization, dependency wiring, visibility, naming, Go safety) and adds the MCP-specific rules below.

## Module Role And Boundaries

- `erun-mcp` owns MCP transport concerns: server startup, HTTP handler wiring, SDK integration, tool registration, and the `cmd/emcp` executable.
- Keep MCP-specific configuration, flag parsing, and transport wiring in `erun-mcp`, not in `erun-cli` or `erun-common`.
- `erun-cli` and `erun-mcp` must not import each other.
- `erun-mcp` should reach backend functionality through transport-neutral clients and contracts in `erun-common`, not by importing backend API packages directly.
- Design MCP-facing handlers as non-interactive operations with explicit inputs and structured outputs. MCP-exposed paths should receive all required input explicitly and fail clearly when input is missing.
- MCP is a programmatic orchestration layer, not an operator at a terminal: it composes the pure primitives and threads the version, rather than relying on operator-convenience switches. The `push`/`deploy` tools require an explicit version and fail clearly without one (an agent captures it from the `build` tool's result); they never build or synthesize a version. See root `AGENTS.md` § "Command primitives vs orchestration".
- Action-oriented MCP endpoints should provide a preview or plan path so callers can inspect the resolved work before execution. Preview behavior should avoid side effects and return the concrete actions that would run.
- By default, new commands should be implemented in both transports: CLI and MCP. Keep the MCP layer thin; shared planning and execution belong in `erun-common`.

## The WebSocket Attach Edge Is Not An MCP Tool

`attach.go` registers a second HTTP surface beside the JSON-RPC MCP path (`GET <mcpPath>/attach/{session}`), on the same `*http.ServeMux`, behind bearer-token auth (`wsAttachAuthHTTPMiddleware`, a route-scoped variant of `authHTTPMiddleware` — see below) — but it is not a registered MCP tool and never will be: the MCP transport is call-and-result JSON-RPC (`mcp.NewStreamableHTTPHandler`), and a live PTY needs a persistent duplex byte stream, which a tool call cannot carry. This is erun#1106's own decision, recorded on the issue: the handler execs `eruncommon.RemoteAppSessionAttachLines`'s script as a **local subprocess of this pod** and bridges its PTY to the socket — never a Kubernetes API call, which is the entire reason this lives in the per-env `erun-mcp` edge rather than a platform-side gateway holding `pods/exec`.

- Auth resolves identity the same way the JSON-RPC path does, but the `erun:attach` capability is checked again explicitly in `attachHTTPHandler`, before the WebSocket `Upgrade` call — never after. A caller without it gets a plain HTTP 403 and no subprocess ever starts.
- **A browser client authenticates via WebSocket subprotocol, never a header.** `authHTTPMiddleware` reads the bearer token from the `Authorization` header, but a browser's `WebSocket` constructor exposes no way to set that header on the handshake at all — its only credential-bearing surface is the subprotocol list (`new WebSocket(url, [protocols])`, joined into one `Sec-WebSocket-Protocol` header per RFC 6455). `wsAttachAuthHTTPMiddleware` (`auth.go`) is this route's own resolver: it tries the `Authorization` header first (unchanged for a CLI/mobile caller that can set headers), then falls back to `attachSubprotocolBearerToken`, which accepts exactly a two-entry offer `[attachAuthSubprotocol, token]`. `attachUpgrader.Subprotocols` echoes back the scheme name (never the token) on a successful handshake, satisfying RFC 6455's negotiation requirement. This fallback is scoped to the attach route alone — `authHTTPMiddleware` (the JSON-RPC path) still only reads the header, so this wider acceptance cannot leak into a route a plain HTTP client relies on.
- Do not wrap this path in `trafficMeteringMiddleware`: its `byteCountingResponseWriter` does not implement `http.Hijacker`, and the WebSocket upgrade needs to hijack the connection. Record environment activity by calling `recordRuntimeActivity` directly from the handler instead of composing the shared `activityHTTPMiddleware`.
- Wire protocol: binary frames carry raw PTY bytes in both directions. Text frames carry JSON control messages — client→server `{"type":"resize","cols":N,"rows":N}`, server→client `{"type":"outcome","outcome":"<eruncommon.AISessionAttachOutcome>"}` sent exactly once, immediately before the server closes the socket. This is how an evicted or ended client learns *why* the stream stopped instead of reading a silent close as indistinguishable from a network stall; keep the "unknown must not render as a definite value" property (root AGENTS.md) when touching this path — a signal-killed or unreaped subprocess must resolve to `AISessionAttachOutcomeUnknown`, never a guessed `ended`.
- `dtach` needs a real controlling terminal, not a pipe, so the subprocess runs under a PTY (`creack/pty`) even though this handler's own caller is a WebSocket, not a terminal. Killing the local subprocess only detaches this attach client — the session's own master (and, once #1107 exists, whatever it is running) keeps running for the next attach; the process-group kill exists so an orphaned `dtach -A` client cannot linger holding the PTY slave open.
- This surface has its first real caller as of erun#1692: `erun-console`'s `src/mcp/attachClient.ts` (see `erun-console/AGENTS.md`), which is exactly why the subprotocol fallback above exists — a bare header-only resolver would have left the console permanently unable to reach this edge no matter how the console-side code was written. It still has no CLI verb, and `erun-docs/docs/agent-reference/api-protocol.md` now specs the wire protocol under the mcp-token endpoint's section.
- **The first real browser-driven attach against a real edge (erun#2042 follow-up, `erun-console/playwright/tests/mcp-attach-session.spec.ts`) found this handler assumed its session directory already existed.** `runAttachSession` builds `eruncommon.RemoteAppSessionAttachLines`'s script and runs it, but that script's own `dtach -A` needs `eruncommon.RemoteAppSessionSocketDir` to already exist — true for the CLI shell path (`open.go`'s `remoteShellLaunchLines` `mkdir -p`s it first) but not for this edge, and not guaranteed by anything in the runtime image's boot path either: `session-prune.sh` explicitly no-ops when the directory is absent ("nothing created yet: no sessions have ever run in this container") rather than creating it. So a freshly deployed or restarted pod that has never had a CLI-driven session (`erun open --ai`, a linked orchestrator) run inside it had no session directory at all — an operator's very first attach straight from the console (reachable with no CLI in between) hit `dtach: ...: No such file or directory` piped into the client's own byte stream as raw shell stderr, and a misdiagnosed `"taken-over"` outcome (the owner-file write failing open reads identically to a real rival claiming the socket — exactly the "unknown must not render as a definite value" property this file calls out above). `runAttachSession` now creates the directory itself (`os.MkdirAll(filepath.Dir(socket), 0o700)`) before building or running the script; `TestAttachCreatesSessionDirectoryOnAFreshPod` (`attach_test.go`) is the regression test, proven against a real `dtach`/PTY round trip with the directory intentionally absent beforehand.

## A Browser Caller Needs Two Cross-Origin Fixes, Not One

`server.go`'s `corsMiddleware` reflects the caller's `Origin` back in `Access-Control-Allow-Origin` so a browser script may *read* a cross-origin response — necessary for the hosted console calling an environment's exposed MCP hostname from a different origin (`erun-console/AGENTS.md`), since a PaaS instance's console hostname is runtime config this binary cannot bake in as a fixed allowlist. That header alone was believed sufficient until erun-console's first real cross-origin browser round trip (`erun-console/playwright/tests/mcp-operate-scope.spec.ts`, erun#1107/#763/#2035) actually ran it and got a 403 on every tool call: `"cross-origin request detected from Sec-Fetch-Site header"`.

The MCP go-sdk (v1.4.1+) installs Go's stdlib `net/http.CrossOriginProtection` by default whenever `mcp.StreamableHTTPOptions.CrossOriginProtection` is left nil — a same-origin CSRF guard that runs *inside* `NewStreamableHTTPHandler`'s own handler, checking the `Sec-Fetch-Site` request header before the request reaches anything `corsMiddleware` wrote. `corsMiddleware`'s headers govern the *response*; this guard rejects the request outright, earlier in the chain, with no knowledge of what `corsMiddleware` reflected. Left at its default it 403'd every legitimate cross-origin call this edge exists to serve — meaning neither the console's admin-scope `version` smoke test nor `OperateToolForm` (erun#2024/#2026/#2035) had ever actually been proven to work from a real browser at a different origin than the edge, exactly the disclosed gap `erun-console/AGENTS.md` used to name and exactly the case a hosted deployment always is.

`crossOriginProtectionForAuthenticatedEdge` exempts this edge's one served path from that guard via `AddInsecureBypassPattern`. This is deliberate, not a shortcut around a check that still matters here: CSRF protection defends against a forged request riding a browser's *ambient* credentials (a cookie the browser attaches automatically); this edge has none. Every request must already carry a bearer token in the `Authorization` header (`authHTTPMiddleware`, checked before this handler ever runs), which a cross-site page cannot forge without already knowing the secret it protects — at which point origin was never doing protective work the bearer check wasn't already doing. If a future change ever moves this edge to cookie-based auth, this bypass would need to be revisited alongside it.

## Desktop Restart Is A CLI Verb, Not An MCP Tool

`erun app restart` (issue #1341) is another single-transport exception, for the same structural reason as host AWS credentials below: `emcp` always runs inside a runtime pod for a resolved tenant/environment, on a different host and network namespace than the operator's desktop process — that is true even for a "local-agent" (builds-here) environment, whose runtime still lands in a cluster (a local k3d/k3s cluster included) rather than sharing the desktop's own loopback interface. The desktop's restart control endpoint (`erun-ui/restart_control.go`) binds to `127.0.0.1` on the operator's own machine, so no `emcp` instance — local-agent, remote-agent, or runtime — can ever reach it, regardless of how the tool is wired. The orchestrator that needs to trigger a desktop restart is, by the nature of the bug this fixes, always a process already running on the operator's own machine (that is what let it reach for `launchctl submit` in the first place), so it already has a shell and can call the CLI verb directly. Do not add an MCP tool for this: there is no host it could run on that would make the loopback call succeed.

## Host AWS Credentials Are A CLI Verb, Not An MCP Tool

`cloud_inject_aws_credentials` / `cloud_clear_aws_credentials` are the **in-pod half** of host credential delivery: `emcp` runs inside the runtime container, so it can write `~/.aws/credentials` but has no access to the operator's own AWS SSO profile, which lives only on their machine. The refresh itself is therefore a CLI verb (`erun cloud refresh <tenant> <environment>`) and deliberately has no MCP counterpart — the root `AGENTS.md` both-transports default does not apply when one transport structurally cannot originate the operation.

Keep the inject tool working (the desktop's credential refresher calls it), but treat its argument-carried credentials as a hazard: its `Description:` must keep telling callers not to invoke it from anything that records arguments, and must keep pointing at the CLI verb. Do not add new tools that take credential material as tool input. When a secret has to reach the pod, stream it on stdin behind a constant script (`erun-common/cloud_host_credentials.go`, `remoteSSHKeySeedScript` in `erun-common/open.go`) or deliver it as a Secret (`applyCloudflareCredentialsSecret`).

## MCP Tool Descriptions

The MCP server's tool `Description:` and parameter `jsonschema:"…"` strings are part of the product's public surface — the only place a reader (human or LLM) learns what a tool does before calling it. The shared quality bar and review methodology for tool descriptions and CLI help live in `erun-cli/AGENTS.md` § "CLI Help And MCP Tool Descriptions"; apply it to MCP descriptions too. The key cross-transport rule: the MCP tool `Description:` and the CLI `Short:` + `Long:` for the same operation must reflect the same ground-truth behaviour — if they diverge in meaning, fix both.

## Diagnosing A Deployed Runtime Via MCP

When investigating what is happening inside a deployed runtime pod (in-pod config files, log files, env vars, process state), prefer the per-environment `erun-mcp` endpoint over asking the user to SSH in. The desktop keeps a local port-forward open to each remote env's MCP edge for as long as that env is open; talking JSON-RPC to that port is the fastest way to gather on-pod evidence without a context switch.

- Find the local port at `<UserConfigDir>/erun/portforward/mcp/<tenant>/<environment>.json` — `localPort` is the port to call. `UserConfigDir` follows Go's `os.UserConfigDir`: `~/Library/Application Support` on macOS, `$XDG_CONFIG_HOME` or `~/.config` on Linux, `%AppData%` on Windows. If the JSON file is missing, the env is not open in the desktop and there is no endpoint to query; ask the user to open it first.
- Speak JSON-RPC 2.0 over `POST http://127.0.0.1:<port>/mcp` with `Accept: application/json, text/event-stream`. Send `initialize` first, capture the `Mcp-Session-Id` response header, send a `notifications/initialized` POST carrying that header, then call `tools/list` (for discovery) or `tools/call`. The session id must be on every subsequent request.
- Prefer the structured tools when they cover the question: `idle` returns the resolved `policy`, `managedCloud`, `stopEligible`, `blockedReason`, `markers`, and `activity` snapshot without recording activity; `doctor`, `list`, and `version` are similarly structured. Use `raw` only for state these tools do not expose.
- `raw` runs an arbitrary `argv` from the runtime repo root and returns `{stdout, stderr, executed, workingDirectory, trace}`. Pass `command` as an `argv` array, not a shell string; reach for `["sh","-c","…"]` only when you need shell features. Typical inspections: `cat ~/.config/erun/<tenant>/<env>/config.yaml`, `env | grep ^ERUN_`, `tail ~/.erun/<tenant>/<env>/idle-monitor.log`, `ls -al`, `ps auxf`.
- Scope: `raw` executes inside the `erun-devops` runtime container — the same image, toolchain, env vars (including the `ERUN_CLOUD_*` set), and filesystem an `erun open` shell gets, so what `raw` reports is what the environment actually has. An env customised via `erun-build-env` is therefore fully inspectable and operable through its own MCP edge.
- Treat this endpoint as a diagnostic shortcut, not a substitute for tests. If a code path is reachable from a `--dry-run` trace or a `go test` subprocess, the test belongs there. Use MCP when the question is "what does the running pod actually have on disk or in memory right now?".

### Verifying in-pod fixes before re-running the user-visible flow

When iterating on plumbing that lives inside the runtime pod — contribute-mode toolchain, an updated runtime image, a clone freshness fix, a new env var the chart was supposed to wire — confirm the pod state via MCP **before** asking the user to rebuild the desktop binary, click a launcher, or trigger a redeploy. The desktop-side cycle is slow (re-build erun-app, re-open env, re-click); the MCP probe is one HTTP round-trip and tells you whether the fix is even in the pod yet. Skip the round trip if the pod already has the expected state but the user-facing flow still fails — that points the investigation at the desktop or at the chart, not at the runtime image.

Useful contribute-mode probes (substitute `<port>` from the JSON state file and `<session>` from `Mcp-Session-Id`):

```sh
# 1. Webkit + libsoup dev libraries the Wails build needs (webkit2_41 path).
curl -s -X POST http://127.0.0.1:<port>/mcp \
  -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
  -H "Mcp-Session-Id: <session>" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"raw","arguments":{"command":["sh","-c","pkg-config --modversion webkit2gtk-4.1 libsoup-3.0"]}}}'
# Both versions print → runtime image is current. "Package not found" → rebuild + redeploy.

# 2. Yarn at the pinned version.
... "command":["yarn","--version"] ...
# 1.22.22 → ok. Empty / not found → runtime image is stale.

# 3. Contribute clone HEAD (does the pod have the latest fix the user is testing?).
... "command":["git","-C","/home/erun/git/erun","log","-1","--pretty=%h %s"] ...
# Compare against the branch HEAD on github.com. Behind → the in-tab fix is in the source you
# pushed, but the pod hasn't pulled yet. Send `cd ~/git/erun && git pull` to the contribute
# ERun tab (or via raw with sh -c).

# 4. Is the headless contribute-app process actually running?
... "command":["sh","-c","pgrep -fa 'erun-app --headless' || echo not running"] ...

# 5. Is the contribute-app port bound inside the pod?
... "command":["sh","-c","ss -tlnp | grep :17550 || echo port not bound"] ...
```

When the user reports "still broken after your fix": run these probes first, then act on the answer. Two common patterns:

- Pod toolchain is missing the package my fix expected → I shipped the Dockerfile change but the user hasn't rebuilt + redeployed the runtime image yet. Action: ask the user to run the image rebuild + deploy, or build it on their behalf if authorized.
- Clone HEAD is behind → I shipped a `build.sh` / `run.sh` fix but the pod's `~/git/erun` still has the old script. The `erun contribute clone` command always lands on the repository's default branch; while iterating on an unmerged feature branch the clone needs to be advanced manually. Action: `raw command=["sh","-c","cd /home/erun/git/erun && git fetch origin && git checkout <branch> && git pull --ff-only"]`, then ask the user to retry the click. After the PR is merged, a `git pull` on main is enough.

If the probes all show the expected state but the user-visible click still fails, the bug is in the desktop layer or in how the desktop talks to the pod (port-forward, browser open, etc.), not in the in-pod fix.

## Validation

- Run `go test ./...` from this module after Go changes.

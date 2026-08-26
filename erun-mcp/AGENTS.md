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

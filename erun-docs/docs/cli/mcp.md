---
title: erun mcp
---

# `erun mcp`

Run ERun as a Model Context Protocol (MCP) server over HTTP. This is the entry point used by IDEs, Agents, and the desktop app's port-forward to the in-pod MCP container.

## Synopsis

```
erun mcp [TENANT] [ENVIRONMENT] [flags]
erun mcp call  --tool <name> [--args '<json>'] [--tenant T --environment E]
erun mcp tools [--tenant T --environment E]
erun mcp token [--tenant T --environment E]
erun mcp proxy [--tenant T --environment E]
```

The `mcp` command is a launcher for the `emcp` executable that ships in the `erun-mcp` package. It establishes the runtime context (tenant + environment) and then hands off to `emcp`.

## What it exposes

A typed tool surface — inspection (`idle`, `doctor`, `list`, …), action wrappers around the CLI (`build`, `deploy`, …), and an escape hatch (`raw`). Operators don't usually call MCP directly; it's the integration point for Agents (Claude Code, Codex) and IDEs. (Code-generation guidance — how to write a service, a migration, an ingress — lives in [skills](/collaboration/skills) deployed to the env, not on the MCP surface.)

For the protocol, the full tool list with schemas, the handshake, and worked examples, see [MCP overview](/mcp/overview).

## Usage modes

You rarely run `erun mcp` directly. Three normal entry points:

1. **The default — in the runtime pod.** The runtime container (`erun-devops`) serves the env's MCP edge on `ERUN_MCP_PORT` (default `17000`), so every tool call runs with the environment's own toolchain. The in-pod Agent (`claude`, `codex`) reaches it at loopback; the desktop app port-forwards it for any laptop-side client.
2. **Laptop-side client.** Point an MCP-aware tool at [`erun mcp proxy`](#wiring-a-laptop-side-mcp-client), which bridges the client's stdio to the env's edge. See [Troubleshooting · Connecting a laptop-side Agent client](/reference/troubleshooting#connecting-a-laptop-side-agent-client) for when this is the right call.
3. **Direct launch (for local development of MCP itself).**

   ```bash
   erun mcp my-tenant local --port 17000
   ```

## Talking to an environment's MCP edge

The subcommands go the other direction: instead of serving MCP, they *call* the MCP edge an open environment already exposes. Use them when you want one typed operation against an env from a script, an orchestrating Agent, or your own shell — without configuring an MCP client.

```bash
erun mcp tools                                  # what can this environment do?
erun mcp call --tool version                    # one typed call, result on stdout
erun mcp call --tool raw --args '{"command":["git","status"]}'
erun mcp call --tool list --output json         # structured result for a script
erun mcp token                                  # a bearer, for a client that speaks MCP itself
```

Each one resolves the target the same way `erun deploy` and `erun open` do — the current scope, or `--tenant` / `--environment` to name another env — and reaches it on that env's local MCP port (the port `erun list` reports).

**Tokens are not your problem.** `call` and `tools` mint a short-lived bearer for the target environment immediately before each request, so a long-running tool call can't fail halfway through because a token aged out, and there is nothing to store or refresh. Reach for `token` only when you're driving the protocol yourself; mint a fresh one per request rather than caching it.

Being able to reach the edge depends on the port-forward `erun open` establishes, and on the environment trusting this machine's identity — the same identity the desktop app injects when it deploys an env.

## What a token is allowed to do {#capability-authorization}

Authentication answers *which tenant* is calling. It does not answer *what that caller may do* — and the edge needs both, because `raw` can run arbitrary commands in the pod and `deploy`, `delete` and `context_*` all mutate.

Every tool requires one of two capabilities:

| Capability | Tools |
| --- | --- |
| `erun:read` | Observation that cannot change anything: `version`, `list`, `idle`, `idle_stop_history`, `context_list`, `cloud_list`, `diff`, `observe`, `outputs_list`, `outputs_download`, `job_status`, `job_output`, `job_await` |
| `erun:admin` | Everything else, including remote execution and every mutating tool |

`erun:admin` implies `erun:read`, so an admin token never carries both.

A caller sees only the tools it may call. `tools/list` is filtered to the caller's capabilities, so a disallowed tool is *unknown* rather than forbidden — the answer to "what can I do here" is the same as the answer to "what am I allowed to do". Each surviving handler re-checks at call time as well, so a tool reached by any other route still refuses. Both decisions are audited with the resolved tenant, user and tool.

Two deliberate defaults:

- **A token that says nothing about capabilities is an admin.** That is the desktop's model — one operator who is the tenant admin — and it is what keeps this gate from locking out tokens minted before capabilities existed. Narrowing a caller is opt-in.
- **A tool nobody has classified requires admin.** The read set is an allowlist, so a newly added tool is unreachable to a read-only caller until someone decides it is safe, rather than silently reachable.

A token that carries roles, none of them erun's, grants *nothing* — treating an unrelated role as admin would make it a privilege escalation. Such a caller is authenticated but not authorized, and the edge answers `403` rather than `401`: retrying with the same credentials will not help, and the fix is a role rather than a fresh login.

Capabilities are read from either shape an issuer produces — a space-delimited OAuth `scope` claim, or a `roles` array of the kind project roles arrive in. For a hosted deployment, model them as IdP project roles and grant the subset a tenant may use at the org level, so a role never granted to an org cannot appear in its users' tokens.

An edge deployed without a trust anchor is the loopback-only case that predates authentication, and keeps its existing surface: turning authentication off does not turn authorization on.

## Wiring a laptop-side MCP client {#wiring-a-laptop-side-mcp-client}

`erun mcp proxy` is the same edge again, for a client that speaks MCP itself. It reads JSON-RPC on stdin, relays each message to the environment's edge with a bearer minted for that one request, and writes the reply back on stdout. Configure the client to launch it as a stdio server:

```json
{
  "mcpServers": {
    "acme-dev": {
      "type": "stdio",
      "command": "erun",
      "args": ["mcp", "proxy", "--tenant", "acme", "--environment", "dev"]
    }
  }
}
```

**Configure the command, never a token.** An MCP client reads its server config once when it starts and has no way to refresh a header afterwards, so a bearer written into that config expires partway through the session and every tool for that environment fails at the same moment. Nothing in the config above is a credential, so a session keeps working for as long as it runs — however long that is.

If the port-forward is down, the proxy answers each request with a JSON-RPC error telling you to run `erun open`, and keeps serving; the client shows the message and recovers on its own once the forward is back. stdout carries JSON-RPC and nothing else — every diagnostic goes to stderr, where the client's own log picks it up.

An environment's edge can also forget the session mid-run — it restarts inside a still-running pod, or the session simply ages out. The proxy handles that itself: it re-runs the handshake and retries the request once, so the client sees its reply and nothing else, and the re-handshake is noted on stderr. Only an edge that will not accept a new session surfaces an error to the client.

## Error behaviour

| Failure | Behaviour |
|---|---|
| `emcp` binary not found. | Errors with "build or install it first". |
| Port already in use. | The server fails to bind and exits with the bind error. |
| Tenant / environment not resolvable. | Errors before launching `emcp`. |
| `call` without `--tool`. | Errors with `--tool is required`; exit code 1. Run `erun mcp tools` to list them. |
| `call --args` is not a JSON object. | Errors with `--args must be a JSON object` before resolving the target; exit code 1. |
| Nothing listening on the env's MCP port. | Errors with `MCP endpoint is not reachable`, naming the endpoint, and tells you to run `erun open <tenant> <env>` to bring the port-forward up; exit code 1. |
| The env rejects the bearer. | Errors with `MCP endpoint rejected the bearer token` and points at redeploying the env from the desktop app so it trusts this machine's identity; exit code 1. |
| The tool itself reports an error. | Prints the tool's own message (`MCP tool <name> reported an error: …`); exit code 1. |
| The tool does not exist. | Prints the edge's JSON-RPC error, including its code; exit code 1. |
| No desktop identity on this machine. | Errors with the path it looked at and tells you to open an environment from the desktop app once so the identity exists; exit code 1. A fresh key is never generated, because no deployed env would trust it. |
| `proxy` cannot reach the edge, or the edge rejects the bearer. | The failing request is answered with a JSON-RPC error carrying the same recovery text as the table rows above, and the relay keeps serving the next message. The command itself stays running and exits 0 when the client closes stdin. |

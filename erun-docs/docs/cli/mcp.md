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
```

The `mcp` command is a launcher for the `emcp` executable that ships in the `erun-mcp` package. It establishes the runtime context (tenant + environment) and then hands off to `emcp`.

## What it exposes

A typed tool surface — inspection (`idle`, `doctor`, `list`, …), action wrappers around the CLI (`build`, `deploy`, …), and an escape hatch (`raw`). Operators don't usually call MCP directly; it's the integration point for Agents (Claude Code, Codex) and IDEs. (Code-generation guidance — how to write a service, a migration, an ingress — lives in [skills](/collaboration/skills) deployed to the env, not on the MCP surface.)

For the protocol, the full tool list with schemas, the handshake, and worked examples, see [MCP overview](/mcp/overview).

## Usage modes

You rarely run `erun mcp` directly. Three normal entry points:

1. **The default — in the runtime pod.** The runtime container (`erun-devops`) serves the env's MCP edge on `ERUN_MCP_PORT` (default `17000`), so every tool call runs with the environment's own toolchain. The in-pod Agent (`claude`, `codex`) reaches it at loopback; the desktop app port-forwards it for any laptop-side client.
2. **Laptop-side client (troubleshooting).** Point an MCP-aware tool at the local port the desktop app maintains at `<UserConfigDir>/erun/portforward/mcp/<tenant>/<environment>.json`. See [Troubleshooting · Connecting a laptop-side Agent client](/reference/troubleshooting#connecting-a-laptop-side-agent-client) for when this is the right call.
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

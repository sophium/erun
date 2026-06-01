---
title: erun mcp
---

# `erun mcp`

Run ERun as a Model Context Protocol (MCP) server over HTTP. This is the entry point used by IDEs, Agents, and the desktop app's port-forward to the in-pod MCP container.

## Synopsis

```
erun mcp [TENANT] [ENVIRONMENT] [flags]
```

The `mcp` command is a launcher for the `emcp` executable that ships in the `erun-mcp` package. It establishes the runtime context (tenant + environment) and then hands off to `emcp`.

## What it exposes

A typed tool surface — inspection (`idle`, `doctor`, `list`, …), action wrappers around the CLI (`build`, `deploy`, …), and an escape hatch (`raw`). Operators don't usually call MCP directly; it's the integration point for Agents (Claude Code, Codex) and IDEs. (Code-generation guidance — how to write a service, a migration, an ingress — lives in [skills](/collaboration/skills) deployed to the env, not on the MCP surface.)

For the protocol, the full tool list with schemas, the handshake, and worked examples, see [MCP overview](/mcp/overview).

## Usage modes

You rarely run `erun mcp` directly. Three normal entry points:

1. **The default — in the runtime pod.** The chart starts an `erun-mcp` container that exposes the server on `ERUN_MCP_PORT` (default `17000`). The in-pod Agent (`claude`, `codex`) reaches it at loopback; the desktop app port-forwards it for any laptop-side client.
2. **Laptop-side client (troubleshooting).** Point an MCP-aware tool at the local port the desktop app maintains at `<UserConfigDir>/erun/portforward/mcp/<tenant>/<environment>.json`. See [Troubleshooting · Connecting a laptop-side Agent client](/reference/troubleshooting#connecting-a-laptop-side-agent-client) for when this is the right call.
3. **Direct launch (for local development of MCP itself).**

   ```bash
   erun mcp my-tenant local --port 17000
   ```

## Error behaviour

| Failure | Behaviour |
|---|---|
| `emcp` binary not found. | Errors with "build or install it first". |
| Port already in use. | The server fails to bind and exits with the bind error. |
| Tenant / environment not resolvable. | Errors before launching `emcp`. |

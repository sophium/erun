---
title: erun mcp
---

# `erun mcp`

Run ERun as a Model Context Protocol (MCP) server over HTTP. This is the entry point used by IDEs, AI assistants, and the desktop app's port-forward to the in-pod MCP container.

## Synopsis

```
erun mcp [TENANT] [ENVIRONMENT] [flags]
```

The `mcp` command is a launcher for the `emcp` executable that ships in the `erun-mcp` package. It establishes the runtime context (tenant + environment) and then hands off to `emcp`.

## What it exposes

The MCP server speaks JSON-RPC 2.0 over `POST /mcp` with `Accept: application/json, text/event-stream`. After the standard `initialize` + `notifications/initialized` handshake, callers can use:

| Tool | Purpose |
|---|---|
| `idle` | Resolved idle policy, managed-cloud flag, stop eligibility, activity snapshot. |
| `doctor` | In-pod health checks (config, git, SSH keys). |
| `list` | Same data as `erun list`, structured. |
| `version` | Build version and commit. |
| `raw` | Run an arbitrary `argv` in the runtime repo root. Last-resort escape hatch. |

See [MCP overview](/mcp/overview) for the full protocol details and example calls.

## Usage modes

Most users don't run `erun mcp` directly. The two normal entry points are:

1. **Inside the runtime pod**: the chart starts an `erun-mcp` container that exposes the server on `ERUN_MCP_PORT` (default `17000`). The desktop app port-forwards to it.
2. **From an IDE / AI agent on your laptop**: configure your tool to call the local port the desktop app maintains at `<UserConfigDir>/erun/portforward/mcp/<tenant>/<environment>.json`.

For local development or testing the server, you can invoke it directly:

```bash
erun mcp my-tenant local --port 17000
```

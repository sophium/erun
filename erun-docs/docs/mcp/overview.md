---
title: MCP overview
---

# Model Context Protocol (MCP)

Every open environment exposes an MCP server in its runtime pod. The desktop app forwards a local port to that server so AI tools and IDEs can call it directly.

## Endpoint discovery

The desktop writes a small JSON file per open environment:

```
<UserConfigDir>/erun/portforward/mcp/<tenant>/<environment>.json
```

`UserConfigDir` follows Go's `os.UserConfigDir`:

| OS | Path |
|---|---|
| macOS | `~/Library/Application Support` |
| Linux | `$XDG_CONFIG_HOME` or `~/.config` |
| Windows | `%AppData%` |

The file's `localPort` field is the port to call.

## Protocol

JSON-RPC 2.0 over `POST http://127.0.0.1:<port>/mcp` with `Accept: application/json, text/event-stream`.

1. `initialize` (capture the `Mcp-Session-Id` response header).
2. `notifications/initialized` (POST with the session header).
3. `tools/list` or `tools/call` for subsequent requests, always carrying the session id.

## Built-in tools

| Tool | Purpose |
|---|---|
| `idle` | Resolved idle policy, managed-cloud flag, stop eligibility, current activity snapshot. |
| `doctor` | In-pod health checks (config files, git checkout, SSH keys). |
| `list` | Same data as the CLI `erun list`, structured. |
| `version` | Build version and commit. |
| `raw` | Run an arbitrary `argv` in the runtime pod (last-resort escape hatch). |

Prefer the structured tools when they cover the question — they give typed output. Reach for `raw` only for state the others don't expose.

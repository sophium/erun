---
title: erun app
---

# `erun app`

Launch the ERun [desktop app](/desktop/overview). Starts the app detached and returns immediately, so you can launch it from a terminal without tying up the shell.

## Synopsis

```
erun app [flags]
```

## Flags

| Flag | Description |
|---|---|
| `--headless` | Run the desktop backend without a window and serve the frontend over HTTP. |
| `--port` | HTTP listen port for `--headless` (defaults to the desktop binary's default). |

Without `--headless`, no flags are forwarded — the app opens its normal window.

## Examples

```bash
erun app
erun app --headless --port 34115
```

## Error behaviour

| Failure | Behaviour |
|---|---|
| `erun-app` binary / bundle not found. | Errors with "build or install it first". |

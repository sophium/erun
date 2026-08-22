---
title: erun inputs
---

# `erun inputs`

Place a file from this machine inside an environment's runtime pod. This is the inverse of [`erun outputs`](/cli/outputs) — `outputs` pulls deliverables *out* of the pod, `inputs` pushes a file *in*.

Reach for it when the environment's own tooling needs to consume something only your machine can produce — evidence pulled from an account only your host is authenticated against, a credential file, a fixture — and there's no other way to get it there.

## `erun inputs upload`

```
erun inputs upload <local-path> <remote-path> [--tenant <t>] [--environment <e>] [flags]
```

Streams `<local-path>` into the pod at `<remote-path>`, byte-identical. `<remote-path>` is the full absolute destination inside the pod, including the file name — there's no default location, so a transfer can never land somewhere unexpected. The destination directory is created if it doesn't already exist.

```bash
erun inputs upload ./evidence.xlsx /home/erun/.erun/outputs/evidence.xlsx
erun inputs upload ./creds.json /home/erun/.erun/outputs/creds.json --environment prod
```

After the transfer, the command compares the local and remote checksums and fails if they don't match. On success it prints the size and SHA-256.

## Dry run

`--dry-run` resolves the pod, traces the exact `kubectl exec` that would run, and shows the transfer script — without sending anything.

## From an MCP-connected orchestrator

An Agent already driving the environment's MCP tools reaches the same transfer through the `inputs_upload` tool that [`erun mcp proxy`](/cli/mcp) serves locally — see [MCP overview](/mcp/overview).

## Error behaviour

| Failure | Behaviour |
|---|---|
| Tenant/environment can't be resolved. | Errors before any pod contact with the resolution failure. |
| Local file doesn't exist, or is a directory. | Errors before any pod contact. |
| `<remote-path>` isn't absolute, or contains `..`. | Errors before any pod contact. |
| The channel to the pod is down. | Errors naming the transport failure. |
| Destination directory isn't writable. | Errors naming the directory. |
| Local and remote checksums disagree after transfer. | Errors with both checksums; the file may be corrupt. |

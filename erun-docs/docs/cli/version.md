---
title: erun version
---

# `erun version`

Print the CLI's build version and commit.

## Synopsis

```
erun version
```

## Output

```
erun 1.0.76 (commit abc1234, built 2026-05-24T10:42:15Z)
latest stable: 1.0.80
latest snapshot: 1.0.81-snapshot-20260530120000
```

The first line is baked into the binary at build time via `-ldflags "-X main.Version=..."` and matches `erun-devops/VERSION` at build time. The `latest stable` / `latest snapshot` lines come from a best-effort lookup against the configured runtime registry (a ~5-second call; failures are skipped silently). Pass `--no-registry` to skip the lookup and print only the build line.

## Flags

| Flag | Description |
|---|---|
| `--no-registry` | Skip the remote registry lookup (offline-safe). |

## Comparing versions

When troubleshooting, it's helpful to compare:

- The CLI version (`erun version` on your laptop).
- The runtime pod version (`erun version` after `erun open`, or the environment's runtime version shown by `erun list`).
- The chart's `appVersion` (`helm list -n <tenant>-<env>`).

A mismatch between any of these usually points at a stale pod (re-deploy with `erun deploy --force`) or an outdated CLI install (re-install via Homebrew or your package manager).

## Error behaviour

| Failure | Behaviour |
|---|---|
| Registry unreachable / lookup times out. | The `latest …` lines are omitted; the build line still prints. Use `--no-registry` to skip the lookup entirely. |

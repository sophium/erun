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
```

The version string is baked into the binary at build time via `-ldflags "-X main.Version=..."`. It matches the version in `erun-devops/VERSION` at the time of the build.

## Comparing versions

When troubleshooting, it's helpful to compare:

- The CLI version (`erun version` on your laptop).
- The runtime pod version (`erun version` after `erun open`, or `runtime version:` in `erun list`).
- The chart's `appVersion` (`helm list -n <tenant>-<env>`).

A mismatch between any of these usually points at a stale pod (re-deploy with `erun deploy --force`) or an outdated CLI install (re-install via Homebrew or your package manager).

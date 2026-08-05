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
erun 1.0.80 (commit abc1234, built 2026-05-24T10:42:15Z)
latest stable: 1.0.80
latest snapshot: 1.0.81-snapshot-20260530120000
```

The `erun` line is always **erun's own build version** — baked into the binary at build time via `-ldflags` — so it is directly comparable with the `latest stable` line below it. It never changes with your working directory. The `latest stable` / `latest snapshot` lines come from a best-effort lookup against the configured runtime registry (a ~5-second call; failures are skipped silently). Pass `--no-registry` to skip the lookup and print only the version lines.

Run it inside a project that carries a `VERSION` file and the **project's** version prints on its own labelled line, alongside erun's:

```
erun 1.0.80
project myapp 1.0.76.rc.f2a14b2ad
latest stable: 1.0.80
```

The two are separate facts: `erun` is the tool, `project` is the thing you're building with it. The label is the name of the directory holding the resolved `VERSION` file (see [Build path resolution](/reference/configuration-build-paths) for how that file is found).

`--output json` keeps them in distinct fields, so a script can never confuse one for the other:

```json
{
  "erun": { "version": "1.0.80", "commit": "abc1234" },
  "project": { "name": "myapp", "version": "1.0.76.rc.f2a14b2ad", "versionFile": "/home/you/git/myapp/VERSION" },
  "latestStable": "1.0.80"
}
```

`project` is absent when no project `VERSION` resolves.

## Flags

| Flag | Description |
|---|---|
| `--no-registry` | Skip the remote registry lookup (offline-safe). |
| `--output json` | Emit the structured result (separate `erun` and `project` fields) instead of the human lines. |

## Comparing versions

When troubleshooting, it's helpful to compare:

- The CLI version (`erun version` on your laptop) — the `erun` line.
- The runtime pod version (`erun version` after `erun open`, or the environment's runtime version shown by `erun list`) — again the `erun` line, which is the pod's runtime build regardless of which project directory you happen to be standing in.
- The chart's `appVersion` (`helm list -n <tenant>-<env>`).

A mismatch between any of these usually points at a stale pod (re-deploy with `erun deploy --force`) or an outdated CLI install (re-install via Homebrew or your package manager). The `project` line takes no part in this comparison — it tracks your application's releases, not erun's.

## Error behaviour

| Failure | Behaviour |
|---|---|
| Registry unreachable / lookup times out. | The `latest …` lines are omitted; the build line still prints. Use `--no-registry` to skip the lookup entirely. |

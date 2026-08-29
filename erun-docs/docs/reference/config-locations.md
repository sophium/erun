---
title: Config file locations
---

# Config file locations

ERun's configuration lives in a small number of well-known files.

## Per-user (your machine)

```
$XDG_CONFIG_HOME/erun/                    # or ~/.config/erun on Linux/macOS
├── config.yaml                           # global defaults (default tenant, default env)
├── config.yaml.<YYYY-MM-DD>.bak          # daily backups of the global config (last 5 kept)
└── <tenant>/
    ├── tenant.yaml                       # tenant config
    └── <environment>/
        ├── config.yaml                   # env config (kube context, registry, runtime)
        ├── config.yaml.<YYYY-MM-DD>.bak  # daily backups of this env's config (last 5 kept)
        └── (workspace caches, idle logs, …)
```

### Config backups {#config-backups}

Both the global config and each environment's `config.yaml` are snapshotted before they are overwritten. The first save on any given day copies the previous file to `<name>.<YYYY-MM-DD>.bak` next to it; later saves the same day are no-ops, and only the five most recent dailies are kept. Backups carry no secrets the live file does not.

To roll a file back, run [`erun doctor`](/cli/doctor): `--restore-config-from-backup <date>` for the global config, or `--restore-env-config-from-backup <date>` (with an explicit tenant and environment) for one environment's config — useful when a setting was changed or corrupted, such as an environment type that resolved to the wrong value.

Alongside the config tree, ERun keeps per-environment **state** under `~/.erun` (always the home directory, not the XDG dir):

```
~/.erun/<tenant>/<environment>/
└── trace.log                             # rolling full-detail trace of every env-scoped command
~/.erun/timing/
└── <command>-<timestamp>.json            # one step-timing record per build/release/push/deploy run
```

### Trace log {#trace-log}

Every environment-scoped command (`open`, `doctor`, `deploy`, a scoped `upgrade`, and the MCP action tools inside the pod) appends its complete trace — every action and decision, at full detail regardless of `-v`/`-vv` — to `trace.log`, each line stamped with an RFC3339 timestamp. The file rotates to `trace.log.1` once it passes 5 MB, so the pair never grows beyond ~10 MB per environment. `--dry-run` previews are not written. A remote environment accumulates two of these files — the host one (commands run from your machine: open, deploy, doctor, upgrade) and the in-pod one (Agent MCP actions and in-pod CLI runs). The [desktop's Diagnostics console](/desktop/diagnostics-console) merges both into one timeline by the per-line timestamps, marking in-pod lines `[pod]`, so diagnostics for a failure exist even when nobody was watching.

### Step timing {#step-timing}

`build`, `release`, `push`, and `deploy` each measure themselves as a tree of named steps — the whole run at the root, with children for each image build (further split per architecture), each chart publish, each release stage, and each deploy target — and report it two ways on completion, on both success and failure:

1. A duration-ordered table on the normal feedback channel (so it also lands in `trace.log` for an environment-scoped `deploy`), one line per step, indented by nesting, each line reading `<name> [<duration>]`. Within a step, children are sorted by duration descending — the dominant cost is always the first child line — except that two children within 100ms of each other sort by name instead, so the order is the same on every run rather than flipping on scheduler jitter between two near-identical costs. A step whose children's durations don't add up to its own carries a synthetic `(unaccounted) [<duration>]` line for the gap (a sub-step nothing here names); a step whose children ran concurrently and so overlap carries `(ran concurrently, overlap) [<duration>]` instead of a misleading negative number. An image build step is annotated `(cache hit)` or `(cache miss: <reason>)` — see [Agent reference · Fingerprint cache](/agent-reference/conventions-spec#fingerprint-cache) for what a hit means and why a miss happens for the reason given. A failed step is annotated `(failed)` and the row ends with `— <error>`.
2. A JSON document at `~/.erun/timing/<command>-<timestamp>.json` (`<timestamp>` is the run's start time, `20060102T150405.000000000Z`, so a directory listing sorts oldest to newest and two runs of the same command never collide), so two runs — a normal one and a 22x-slower one — can be diffed by tooling instead of compared by eye across logs:

   ```json5
   {
     "command": "release",             // build | release | push | deploy
     "startedAt": "2026-08-22T14:05:12.345596947Z",
     "durationSeconds": 2713.4,
     "duration": "45m13.4s",           // Go's time.Duration.String()
     "failed": false,
     "error": "",                      // set only when failed is true
     "steps": [
       {
         "name": "frs-docs",           // image name, chart name ("chart <name>"),
                                        // release stage name, or "<tenant>/<env> <version>" for deploy
         "durationSeconds": 664.2,
         "duration": "11m4.2s",
         "failed": false,
         "cacheHit": false,            // omitted entirely for a non-image step
         "cacheMissReason": "fingerprint image is missing for platforms [linux/amd64, linux/arm64]",
         "unaccountedSeconds": 0,      // present only when the gap crosses the 100ms floor
         "overlapSeconds": 0,          // present only when children ran concurrently
         "steps": [
           { "name": "linux/amd64", "durationSeconds": 340.1, "duration": "5m40.1s", "cacheHit": false, "cacheMissReason": "…" },
           { "name": "linux/arm64", "durationSeconds": 324.1, "duration": "5m24.1s", "cacheHit": false, "cacheMissReason": "…" }
         ]
       }
     ]
   }
   ```

   Unlike the printed table, a JSON record's `steps` stay in insertion order rather than duration-sorted, so the same step lands at the same array position across two runs even when a regression changed which one took longest.

`~/.erun/timing/` is not tenant/environment-scoped like `trace.log`: `build`, `release`, and `push` are pure primitives that often run with no deploy target at all, so a location keyed to tenant+environment would leave most of their runs with no record. All four commands write here regardless of whether they're also inside an environment-scoped `trace.log`.

`--dry-run` does no work and skips step timing entirely, the same as it skips the `==> Building` / `==> Releasing` / `==> Pushing` / `==> Deploying` markers.

The XDG base dir follows the OS conventions:

| OS | Path |
|---|---|
| macOS | `~/Library/Application Support` (via `os.UserConfigDir`) |
| Linux | `$XDG_CONFIG_HOME` if set, else `~/.config` |
| Windows | `%AppData%` |

## Per-project (committed in the repo)

```
<repo>/.erun/
└── config.yaml                           # project-level config (per-env registry, k8s deploy plans)
```

This file is checked into the repository so that everyone on the team uses the same registry and deployment topology for shared environments.

## Per-pod (inside the runtime container)

```
/home/erun/                               # persisted on a PVC
├── git/<repo-name>/                      # your project checkout
├── .config/erun/<tenant>/<env>/          # the env's config, mirrored from your machine
├── .erun/<tenant>/<env>/bootstrap.yaml   # remote-init marker
└── .erun/<tenant>/<env>/trace.log        # rolling trace of in-pod erun commands
```

The runtime container reads the same `~/.config/erun/...` layout as your laptop, just inside the pod.

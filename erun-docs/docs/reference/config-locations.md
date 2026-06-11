---
title: Config file locations
---

# Config file locations

ERun's configuration lives in a small number of well-known files.

## Per-user (your machine)

```
$XDG_CONFIG_HOME/erun/                    # or ~/.config/erun on Linux/macOS
├── config.yaml                           # global defaults (default tenant, default env)
└── <tenant>/
    ├── tenant.yaml                       # tenant config
    └── <environment>/
        ├── config.yaml                   # env config (kube context, registry, runtime)
        └── (workspace caches, idle logs, …)
```

Alongside the config tree, ERun keeps per-environment **state** under `~/.erun` (always the home directory, not the XDG dir):

```
~/.erun/<tenant>/<environment>/
└── trace.log                             # rolling full-detail trace of every env-scoped command
```

### Trace log {#trace-log}

Every environment-scoped command (`open`, `doctor`, `deploy`, a scoped `upgrade`, and the MCP action tools inside the pod) appends its complete trace — every action and decision, at full detail regardless of `-v`/`-vv` — to `trace.log`, each line stamped with an RFC3339 timestamp. The file rotates to `trace.log.1` once it passes 5 MB, so the pair never grows beyond ~10 MB per environment. `--dry-run` previews are not written. The [desktop's Diagnostics console](/desktop/overview#diagnostics-console) reads this file (the in-pod copy for remote environments), so diagnostics for a failure exist even when nobody was watching.

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

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
└── .erun/<tenant>/<env>/bootstrap.yaml   # remote-init marker
```

The runtime container reads the same `~/.config/erun/...` layout as your laptop, just inside the pod.

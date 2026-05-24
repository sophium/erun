---
title: Local vs non-local
---

# Local vs non-local environments

The environment named `local` is your development sandbox. Every other name is a **non-local** environment and behaves differently in ways worth knowing about.

## Behavioral split

| | local | non-local |
|---|---|---|
| Image version tag | `X.Y.Z-snapshot-<UTC-timestamp>` (unique per build) | bare `X.Y.Z` from `VERSION` file |
| `erun push` rebuilds first? | yes | no — push only |
| `erun deploy` rebuilds components? | yes | no |
| `erun build` source | ERun-native multi-arch Docker builds with fingerprint cache | delegates to `<projectRoot>/build.sh` |
| Auto-pick `kubectl current-context` | yes | no — must be configured |
| `--snapshot` flag honored | yes | ignored |
| Per-env `values.<env>.yaml` scaffolded | no | yes |

## Why the split exists

Non-local environments use **stable release tags**. The contract is that `<image>:1.0.42` means a specific built artifact — if `erun push` silently rebuilt that tag from whatever happens to be in your local Docker cache, you'd be mutating a release.

The `local` environment exists precisely so developers can iterate with safe, unique snapshot tags. There you can rebuild and push freely; the tag includes a timestamp, so no two builds collide.

## The `local` decision in code

The check is a simple string compare on the environment **name** (case-insensitive). Nothing else is consulted — not whether the env is marked `Remote`, what its Kubernetes context is, or where it deploys to.

```go
const DefaultEnvironment = "local"

func isLocalEnvironment(environment string) bool {
    return strings.EqualFold(strings.TrimSpace(environment), DefaultEnvironment)
}
```

So an environment named `Local` or `local` is treated as local; everything else is non-local.

## Practical workflow

For local iteration:

```bash
erun open my-tenant local
erun build      # rebuilds with fresh snapshot tag
erun push       # rebuilds + pushes the snapshot
erun deploy     # promotes the snapshot into the cluster
```

For non-local environments (where you've usually SSH'd into a remote runtime pod):

```bash
erun build      # runs ./build.sh — produces images with the stable VERSION tag
erun push       # docker push (no rebuild)
erun deploy     # rolls out the pushed image
```

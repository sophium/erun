---
title: Development vs promotion environments
---

# Development vs promotion environments

ERun environments come in two kinds, distinguished by what they're for:

- A **development environment** — where iteration happens. Snapshot tags, automatic rebuilds, easy overwrites. Optimized for the rapid build / test / break / fix loop.
- A **promotion environment** — everything that isn't a development environment. Stable release tags, explicit promotion steps, no silent rebuilds. Optimized for "this artifact is what we said it is."

"Development" describes the **workflow**, not the location. A development environment can run on your laptop (Docker Desktop, kind, k3d), on a managed cloud cluster, or anywhere ERun can talk to. What makes it a development environment is that you iterate in it.

## How an environment is designated

Today, an environment becomes a development environment by being named `local`. The name `local` is the current marker: when ERun's build, push, deploy, and version-tagging code sees that name, it switches into the development-workflow mode. Every other name is treated as a promotion environment.

This means each tenant has at most one development environment (one per tenant can be named `local`), but an organization can have many — one per tenant.

```yaml
# Inside ~/.config/erun/<tenant>/<env>/config.yaml
# An env named "local" is treated as the development environment.
```

The name is a convention, not a constraint on what the environment can do. A `local` environment is just an environment; the name flips the development-workflow behaviors on.

## Behavioral split

| | Development env (`local`) | Promotion env (any other name) |
|---|---|---|
| Image version tag | `X.Y.Z-snapshot-<UTC-timestamp>` (unique per build) | bare `X.Y.Z` from `VERSION` file |
| `erun push` rebuilds first? | yes | no — push only |
| `erun deploy` rebuilds components? | yes | no |
| `erun build` source | ERun-native multi-arch Docker builds with fingerprint cache | delegates to `<projectRoot>/build.sh` |
| Auto-pick `kubectl current-context` | yes | no — must be configured |
| `--snapshot` flag honored | yes | ignored |
| Per-env `values.<env>.yaml` scaffolded | no | yes |

## Why the split exists

Promotion environments use **stable release tags**. The contract is that `<image>:1.0.42` means a specific built artifact — if `erun push` to a promotion env silently rebuilt that tag from whatever happens to be in your local Docker cache, you'd be mutating a release.

Development environments exist precisely so you can iterate with safe, unique snapshot tags. The tag includes a UTC timestamp, so no two builds collide; overwriting is the expected behavior.

## The designation in code

The current check is a string compare on the environment name (case-insensitive, whitespace-trimmed):

```go
const DefaultEnvironment = "local"

func isLocalEnvironment(environment string) bool {
    return strings.EqualFold(strings.TrimSpace(environment), DefaultEnvironment)
}
```

Nothing else is consulted — not whether the env is marked `Remote`, what its Kubernetes context is, or where it deploys to. So today, any environment named `Local`, `local`, or `LOCAL` is the development environment; everything else is a promotion environment.

The conceptual model is broader than the current implementation: a development environment is whichever environment you're iterating in. The implementation just uses the name as the marker.

## Practical workflow

For iteration in your development environment:

```bash
erun open my-tenant local
erun build      # rebuilds with a fresh snapshot tag
erun push       # rebuilds + pushes the snapshot
erun deploy     # promotes the snapshot into the cluster
```

For promotion environments (where you've usually SSH'd into a remote runtime pod):

```bash
erun build      # runs ./build.sh — produces images with the stable VERSION tag
erun push       # docker push (no rebuild)
erun deploy     # rolls out the pushed image
```

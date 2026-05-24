---
title: Local vs non-local environments
---

# Local vs non-local environments

ERun environments split into two behaviors:

- **Local** — the iteration sandbox. Snapshot image tags, automatic rebuild on push, easy overwrite. Optimized for the rapid build / test / break / fix loop.
- **Non-local** — everything else. Stable release tags from the `VERSION` file, no silent rebuild, explicit promotion. Optimized for "this artifact is what we said it is."

The terms describe **workflow**, not location. A local-mode environment can run on your laptop (Docker Desktop, kind, k3d), on a managed cloud cluster, or anywhere ERun can talk to. What makes it "local" is that you iterate in it.

## How an environment becomes "local"

Today, an environment gets the local-mode behavior by being named `local` (case-insensitive). The name is the marker the code checks. Every other name is treated as non-local.

```
~/.config/erun/<tenant>/local/config.yaml          ← this env runs in local mode
~/.config/erun/<tenant>/anything-else/config.yaml  ← non-local
```

Each tenant has at most one `local` environment (one per tenant), but an organization can have many — one per tenant. Common organizational naming like `dev`, `integration`, `staging`, `prod` is unaffected: those are non-local, and they behave like non-local should.

The conceptual model is broader than the name check: a local environment is whichever environment you're iterating in. The implementation just uses the name as the marker. Some teams may want a different env name to trigger local-mode behavior; today, the way to do that is to name that env `local`.

## Behavioral split

| | Local (env named `local`) | Non-local (any other name) |
|---|---|---|
| Image version tag | `X.Y.Z-snapshot-<UTC-timestamp>` (unique per build) | bare `X.Y.Z` from `VERSION` file |
| `erun push` rebuilds first? | yes | no — push only |
| `erun deploy` rebuilds components? | yes | no |
| `erun build` source | ERun-native multi-arch Docker builds with fingerprint cache | delegates to `<projectRoot>/build.sh` |
| Auto-pick `kubectl current-context` | yes | no — must be configured |
| `--snapshot` flag honored | yes | ignored |
| Per-env `values.<env>.yaml` scaffolded | no | yes |

## Why the split exists

Non-local environments use **stable release tags**. The contract is that `<image>:1.0.42` means a specific built artifact — if `erun push` to a non-local env silently rebuilt that tag from whatever happens to be in your local Docker cache, you'd be mutating a release.

Local environments exist precisely so you can iterate with safe, unique snapshot tags. The tag includes a UTC timestamp, so no two builds collide; overwriting is the expected behavior.

## The check in code

The current implementation is a string compare on the environment name (case-insensitive, whitespace-trimmed):

```go
const DefaultEnvironment = "local"

func isLocalEnvironment(environment string) bool {
    return strings.EqualFold(strings.TrimSpace(environment), DefaultEnvironment)
}
```

Nothing else is consulted — not whether the env is marked `Remote`, what its Kubernetes context is, or where it deploys to. So the name `local` is what flips the iteration-mode behaviors on; everything else is non-local.

## Practical workflow

In your local environment:

```bash
erun open my-tenant local
erun build      # rebuilds with a fresh snapshot tag
erun push       # rebuilds + pushes the snapshot
erun deploy     # promotes the snapshot into the cluster
```

In a non-local environment (often SSH'd into the runtime pod):

```bash
erun build      # runs ./build.sh — produces images with the stable VERSION tag
erun push       # docker push (no rebuild)
erun deploy     # rolls out the pushed image
```

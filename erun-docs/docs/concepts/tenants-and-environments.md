---
title: Tenants and environments
---

# Tenants and environments

ERun's configuration is organized around two ideas:

- A **tenant** is a project or workstream. It maps to a git repository and owns one or more environments.
- An **environment** is a named runtime target within a tenant: `local`, `dev`, `staging`, `<your-feature-branch>`, and so on. Each environment lives in its own Kubernetes namespace with its own container registry, runtime configuration, home volume, docker daemon, and MCP endpoint.

Environments are independent. A single machine can host many of them in parallel — one per feature branch, one per agent, one per teammate sharing the same cluster — and they don't see each other's state. The limit is your CPU and memory, not anything ERun imposes.

## Configuration layout

```
~/.config/erun/
├── config.yaml                  # global defaults
└── <tenant>/
    ├── tenant.yaml              # tenant config (project root, default env, cloud providers)
    └── <environment>/
        └── config.yaml          # per-environment config (kube context, registry, runtime version)

<repo>/.erun/
└── config.yaml                  # project-level config (committed; per-env registries, deploy plans)
```

## "Local" is the iteration mode

One environment per tenant can be named `local` (case-insensitive). The name `local` flips on the iteration-mode behaviors:

- **Snapshot tags** (`X.Y.Z-snapshot-<UTC-timestamp>`) safe to overwrite on every iteration.
- **Auto-rebuild on `erun push`** so iteration is one command.
- **Auto-context-pick** — `kubectl current-context` is used when no Kubernetes context is configured.

Every other environment is **non-local**: stable semver tags from `VERSION`, no auto-rebuild on push, explicit context. The name doesn't matter beyond that — `dev`, `integration`, `staging`, `prod`, `<feature-branch>` are all non-local, and they all behave like non-local environments should.

See [Local vs non-local](/concepts/local-vs-non-local) for the full behavioral split.

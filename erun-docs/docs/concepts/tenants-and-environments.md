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

## Development environments

One environment per tenant can be designated as the **development environment** — the place where iteration happens. Conceptually, "development" is a workflow, not a location: a development environment can run locally on Docker Desktop, or on a managed cloud cluster, or anywhere ERun can talk to.

Today the marker for this designation is the environment name `local` (case-insensitive). An environment named `local` gets:

- **Snapshot tags** (`X.Y.Z-snapshot-<UTC-timestamp>`) safe to overwrite on every iteration.
- **Auto-rebuild on `erun push`** so iteration is one command.
- **Auto-context-pick** — `kubectl current-context` is used when no Kubernetes context is configured.

Every other environment is a **promotion environment**: stable semver tags from `VERSION`, no auto-rebuild on push, explicit context. See [Development vs promotion environments](/concepts/local-vs-non-local) for the full split.

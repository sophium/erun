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

## The `local` environment is special

The environment named `local` (case-insensitive) is treated as your development sandbox. It is the only environment where:

- `erun build` and `erun push` produce **snapshot** image tags (`X.Y.Z-snapshot-<UTC-timestamp>`) that are safe to overwrite.
- `erun push` automatically rebuilds the image before pushing.
- ERun auto-fills the Kubernetes context from `kubectl config current-context` when none is configured.

Every other environment is **non-local**: it uses stable semver tags from the `VERSION` file, and `erun build` delegates to the project's `build.sh` script. See [Local vs non-local](/concepts/local-vs-non-local) for the full split.

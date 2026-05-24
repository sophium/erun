---
title: Container registries
---

# Container registries

Each environment has a **container registry** — the host/namespace that `erun build`, `erun push`, and `erun deploy` use to tag and publish images.

## Where the value comes from

The registry is resolved in priority order:

1. The environment's own `EnvConfig.ContainerRegistry` (stored under `~/.config/erun/<tenant>/<env>/config.yaml`).
2. The project's `.erun/config.yaml` `environments.<env>.containerregistry`.
3. The project's top-level `containerregistry` in `.erun/config.yaml`.
4. The built-in default: `ghcr.io/sophium`.

You can set or change the registry from the desktop app's environment edit modal, or by passing `--container-registry <host>` to `erun init`.

## Multi-architecture builds

Every `erun build`, `erun build --release`, and `erun deploy` produces both `linux/amd64` and `linux/arm64`. There is no single-arch code path — that avoids developer-machine builds that work locally but fail on a foreign-arch cluster. The cluster runs binfmt/qemu so the foreign arch builds inside the dind sidecar.

## Authentication

- **ghcr.io** — `gh auth login` (and a `write:packages` token scope) covers it.
- **AWS ECR** — the cluster's IRSA role grants the runtime pod permission to push; for local `docker push` you use a profile via `aws ecr get-login-password`.
- **Other registries** — `docker login` once; credentials are persisted at `~/.docker/config.json`.

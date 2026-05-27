---
title: Container registries
---

# Container registries

Each environment has a **container registry** — the host/namespace that `erun build`, `erun push`, and `erun deploy` use to tag and publish images.

## Where the value comes from

<figure className="erun-hero-figure">
  <img src="/img/registry-resolution.svg" alt="Four cyan-stroked boxes in a row showing the four priority levels of container registry resolution. P1 Per-env override (EnvConfig.containerregistry, per-user config). P2 Project per-env (environments.&lt;env&gt;.containerregistry in .erun/config.yaml). P3 Project default (containerregistry top-level in .erun/config.yaml). P4 Built-in default (ghcr.io/sophium, charcoal box, always available). Arrows between each pair labelled 'if unset'. A strapline reads: 'Override at the highest level you need — usually per-env for ECR, project-wide for ghcr.'" />
  <figcaption>Highest priority wins. ERun falls through to the next level until a value is set.</figcaption>
</figure>

You can set or change the registry from the desktop app's environment edit modal, or by passing `--container-registry <host>` to `erun init`.

## Multi-architecture builds

Every `erun build`, `erun build --release`, and `erun deploy` produces both `linux/amd64` and `linux/arm64`. There is no single-arch code path — that avoids developer-machine builds that work locally but fail on a foreign-arch cluster. The cluster runs binfmt/qemu so the foreign arch builds inside the dind sidecar.

## Authentication

- **ghcr.io** — `gh auth login` (and a `write:packages` token scope) covers it.
- **AWS ECR** — the cluster's IRSA role grants the runtime pod permission to push; for local `docker push` you use a profile via `aws ecr get-login-password`.
- **Other registries** — `docker login` once; credentials are persisted at `~/.docker/config.json`.

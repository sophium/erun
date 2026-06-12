---
name: erun-build-env
description: Create a custom build environment by extending ERun's published runtime image with the project's own toolchain, then pointing the environment at the result. Use when the user says "init build environment", "init erun build environment", "create a custom build environment", "customize the runtime image", or any similar request to add tools to the image an environment's runtime pod runs.
---

# Custom build environment

Use this skill when the project needs tools the stock ERun runtime image
doesn't ship (a language toolchain, a CLI, system packages). The motion:
write a Dockerfile that extends the published `erun-devops` image, build
and push it with `erun build`, and set it as the environment's runtime
image. The next deploy/open rolls it out.

## Step 1 — determine the runtime version and registry

The custom image must extend the same version the environment runs.

```sh
if [ -n "${ERUN_TENANT:-}" ]; then
    # Inside a deployed env: the running version is the one to extend.
    runtime_version=$(erun version | head -n 1 | tr -d '[:space:]' | sed 's/^v//')
else
    # On a laptop: read the env's pinned runtimeversion.
    grep 'runtimeversion' ~/.config/erun/<tenant>/<environment>/config.yaml
    # or inspect the per-env block of:
    erun list
fi
```

The registry defaults to `ghcr.io/sophium`; if the env config carries a
`runtimeregistry` or `containerregistry`, use that instead.

## Step 2 — write the starter Dockerfile

Place it at `<module>/docker/<image-name>/Dockerfile` inside the project
(one path segment between the project root and `docker/`, one between
`docker/` and the Dockerfile) so `erun build` picks it up with the whole
project as build context. The directory name becomes the image name —
pick something like `<tenant>-runtime`.

```dockerfile
FROM ghcr.io/sophium/erun-devops:<runtime-version>

# Add the project's toolchain here. Examples:
# RUN apt-get update && apt-get install -y --no-install-recommends \
#         <packages> \
#     && rm -rf /var/lib/apt/lists/*
# RUN curl -fsSL <tool-installer-url> | sh
```

Guide the user on what to add: keep layers small, pin versions, clean
package caches in the same `RUN`. Commit the Dockerfile with `git` so the
team shares the toolchain.

## Step 3 — build and push

```sh
erun build
```

`erun build` runs the Docker build for both `linux/amd64` and
`linux/arm64` and pushes the result to the env's registry. Add `--dry-run`
first to preview the exact commands.

## Step 4 — point the environment at the image

Either persist it through init:

```sh
erun init <tenant> <environment> --runtime-image <image-name>
```

or edit the env config (`~/.config/erun/<tenant>/<environment>/config.yaml`)
and set the `runtimeimage` field. A full reference
(`ghcr.io/acme/acme-runtime:1.2.3`) is used verbatim; a bare name
(`acme-runtime`) resolves to `<registry>/<name>:<runtime version>`.

On deploy, the image rides into the published `erun-devops` chart as
`imageOverrides.erun-devops` — the chart stays canonical, only the
runtime container's image changes. The next `erun deploy` or `erun open`
rolls the custom image out.

## Important

- Always extend `erun-devops`; do not replace it with an unrelated base —
  the entrypoint, agents, and in-pod tooling live in that image.
- Keep the `FROM` version aligned with the env's `runtimeversion`. After
  an `erun upgrade`, rebuild the custom image against the new version.

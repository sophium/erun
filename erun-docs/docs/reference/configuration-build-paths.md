---
title: Build path resolution
---

# Build path resolution

When you run `erun build`, `erun push`, or `erun deploy` locally, ERun has to resolve four things: which **project root** to operate against, which **environment** to assume, which **build directory** is the Docker context, and which **version** to tag the image with. This page documents how each is resolved.

This is reference material — for everyday use you don't need to think about it. See [Configuration overview](/reference/configuration) for the field-level reference.

## 1. Project root

1. If `--project-root` is supplied (internal flag, used by tests), use that.
2. Otherwise, walk up from the current working directory. At each level, check for a `.git` directory or file. The first match's containing directory is the project root.
3. If the walk reaches the filesystem root with no match, the project root is **empty** and dependent resolutions degrade (the container registry falls through to the built-in default; the command typically aborts with `NOT_IN_GIT_REPO`).

| Error code | Cause |
|---|---|
| `NOT_IN_GIT_REPO` | cwd is not inside a git repository. |
| `PROJECT_ROOT_INVALID` | `--project-root` was supplied but the path doesn't exist or contains no `.git`. |

## 2. Environment

1. If `--environment` is supplied, use that.
2. Otherwise, enumerate every environment of every tenant and compare the resolved project root from step 1 against each env's `localRepoPath`. An env matches when the project root is **at or below** its `localRepoPath` (so a nested working directory still resolves); the **longest** matching path wins. Envs with no `localRepoPath` are skipped. A tie at the same longest path **across different tenants** is ambiguous and resolves to no match. On a unique match, pick that tenant's `defaultenvironment`.
3. If no tenant matches and the cwd is in a git repo, fall back to the global `ERunConfig.default_tenant` and that tenant's `defaultenvironment`.
4. If still unresolved and the caller is interactive (TTY), prompt for tenant + env.
5. Otherwise abort with `ENVIRONMENT_NOT_RESOLVED`.

| Error code | Cause |
|---|---|
| `ENVIRONMENT_NOT_RESOLVED` | None of the resolution steps produced an env, and no TTY is available for a prompt. |
| `TENANT_NOT_CONFIGURED` | The resolved tenant has no `~/.config/erun/<tenant>/tenant.yaml`. |

## 3. Build context directory

A Dockerfile is in the **standard layout** iff its absolute path matches:

```
^<projectRoot>/[^/]+/docker/[^/]+/Dockerfile$
```

Exactly one path segment between `<projectRoot>` and `docker/`, and exactly one between `docker/` and `Dockerfile`. `<tenant>-devops/docker/<component>/Dockerfile` is the canonical case.

- **Standard layout match:** Docker context = `<projectRoot>`. `COPY` paths can reference anything in the project tree (e.g., `COPY erun-cli /src/erun-cli` from a Dockerfile under `<tenant>-devops/docker/erun-devops/`).
- **No match (flat layout):** Docker context = the directory containing the Dockerfile. `COPY` paths outside that directory are not available.

The image name is `filepath.Base(buildDir)` — the directory containing the Dockerfile.

| Error code | Cause |
|---|---|
| `NO_BUILDABLE_CONTEXT` | The walk found no `Dockerfile` under any `<tenant>-devops/docker/<component>/` and the cwd is not itself a buildable context. |

## 4. Version

ERun walks a sequence of candidate `VERSION` files and uses the first one it finds:

1. `<buildDir>/VERSION` (the image's pinned version).
2. Compute the next-up directory:
   - If `<buildDir>` matches `<...>/docker/<image>/`, hop up two levels (skip `docker/` and `<image>/`).
   - Otherwise hop up one.
3. From that directory, walk up to the project root. At each level, check for `VERSION`. First match wins.

Contents are the bare version string (`1.0.76`). Trailing newlines are stripped.

For an **agent env**, the version is then transformed into a snapshot: `1.0.76` becomes `1.0.76-snapshot-20260525143027` (UTC timestamp). For a **runtime env**, the version is used as-is.

| Error code | Cause |
|---|---|
| `NO_VERSION_FILE` | The walk reached the project root with no `VERSION` found anywhere. |
| `INVALID_VERSION_FILE` | A `VERSION` file was found but contains non-semver content or extra non-whitespace lines. |

## 5. Final image tag

```
<registry>/<image-name>:<version>
```

- `<registry>` — the `build`-marked registry from the [container registries](/reference/configuration#container-registries) list.
- `<image-name>` — `filepath.Base(buildDir)` — the directory name where the Dockerfile lives.
- `<version>` — the resolved version, snapshot-stamped for agent envs.

### Example

A Dockerfile at `/repo/petios-devops/docker/petios-devops/Dockerfile`, in an env whose `EnvConfig.containerregistry` is `020362606330.dkr.ecr.eu-west-2.amazonaws.com`, with a `VERSION` file at `/repo/petios-devops/docker/petios-devops/VERSION` reading `1.0.308`, in a runtime env, produces:

```
020362606330.dkr.ecr.eu-west-2.amazonaws.com/petios-devops:1.0.308
```

In an agent env the tag becomes `…/petios-devops:1.0.308-snapshot-<UTC-timestamp>` and `--release` strips the snapshot suffix.

---
title: erun build
---

# `erun build`

Build the project's container image(s). `erun build` runs **only in agent envs** — runtime envs receive deploys of already-built artifacts. See [Environment types](/concepts/environment-types).

## Synopsis

```
erun build [flags]
```

## Where builds happen

In an agent env, `erun build` resolves the current Docker build context (from the current working directory), produces both `linux/amd64` and `linux/arm64` images via the local Docker daemon plus binfmt, and tags them so subsequent builds promote from cache instead of rebuilding. See [Fingerprint cache](/agent-reference/conventions-spec#fingerprint-cache) for the cache-promotion algorithm.

The image tag in an agent env is a snapshot built from the nearest `VERSION` file plus a timestamp; see [Build path resolution](/reference/configuration-build-paths) for the exact tag format and VERSION-walking rule. To produce a stable release tag (for promotion to a runtime env), use `erun build --release`.

Running `erun build` against a runtime env **exits non-zero with a clear message**, rather than silently succeeding or producing an unexpected artefact:

```
error: erun build is only meaningful in an agent env.
The active env 'prod' is a runtime env (no worktree, no builds).
To roll out an already-built version: erun deploy --tenant <t> --environment prod --version <v>
```

This makes an accidental build against the wrong env immediately visible — the Operator (or Agent) sees the misconfiguration the moment they try.

## Flags

| Flag | Description |
|---|---|
| `--deploy` | After a successful build, run `erun deploy` for the same environment. |
| `--release` | Run `erun release` first and publish the release-tagged images. |
| `--force` | Delete and recreate conflicting release tags when combined with `--release`. |
| `--dry-run` | Resolve and print every `docker build` / `docker tag` / `docker push` command without executing. |

Advanced flags (`--no-incremental`, `--version`) and the full build lifecycle (binfmt verification, fingerprint resolution, per-arch build → manifest list) are on [Agent reference · CLI flag spec · `erun build`](/agent-reference/cli-flags#erun-build).

## Examples

In an agent env:

```bash
erun build              # rebuild current Docker context with a fresh snapshot tag
erun build --dry-run    # see exactly what would run
erun build --deploy     # build then deploy in one shot
erun build --release    # produce a stable release tag for promotion to a runtime env
```

To get a built artifact into a runtime env, run `erun deploy` against that env — it picks up the already-built version from the registry. See [`erun deploy`](/cli/deploy).

## Multi-architecture

Every build produces both `linux/amd64` and `linux/arm64`. There is no single-platform code path — a single-arch artifact built locally cannot be deployed to a cluster of a different architecture, and arch-specific Dockerfile bugs should fail at developer-machine build time, not at remote deploy time.

The local Docker daemon must have binfmt installed for the foreign arch. The runtime chart's `binfmt` init container installs this automatically inside the cluster; for local builds you may need to run `docker run --privileged --rm tonistiigi/binfmt --install all` once on your host.

## `--dry-run` output

A `--dry-run` invocation streams the same `audit:` and `trace:` lines a real run would, with secrets redacted:

```
$ erun build --dry-run
audit: erun build --dry-run
trace: resolving build scope
trace:   project root      = /Users/you/code/myapp
trace:   tenant            = my-tenant
trace:   environment       = local (agent env)
trace:   build context     = /Users/you/code/myapp
trace:   dockerfile        = my-tenant-devops/docker/myapp-api/Dockerfile
trace:   version source    = my-tenant-devops/docker/myapp-api/VERSION
trace:   version           = 1.0.77
trace:   snapshot suffix   = -snapshot-20260525143027
trace:   registry          = ghcr.io/sophium  (from .erun/config.yaml)
trace: would run:
trace:   docker buildx build \
trace:     --platform linux/amd64,linux/arm64 \
trace:     -t ghcr.io/sophium/myapp-api:1.0.77-snapshot-20260525143027 \
trace:     -f my-tenant-devops/docker/myapp-api/Dockerfile \
trace:     --build-arg GITHUB_TOKEN=<redacted> \
trace:     /Users/you/code/myapp
trace:   docker tag ghcr.io/sophium/myapp-api:1.0.77-snapshot-20260525143027 \
trace:              ghcr.io/sophium/myapp-api:fp-a3c7b9d2-amd64
trace:   docker tag ghcr.io/sophium/myapp-api:1.0.77-snapshot-20260525143027 \
trace:              ghcr.io/sophium/myapp-api:fp-a3c7b9d2-arm64
result: dry_run (no side effect)
```

The real-run trace is identical byte-for-byte except `result: ok` (or `result: error`) at the end. Redaction follows the rules in [Agent reference · Dry-run redaction](/agent-reference/dry-run-redaction).

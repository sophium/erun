---
title: Conventions spec
---

# Conventions spec

The resolution algorithms behind ERun's conventions. For the Operator-facing overview, see [Conventions](/concepts/conventions). For the build-path resolution end-to-end, see [Build path resolution](/reference/configuration-build-paths).

## Project root resolution

The project root is the directory containing `.git`. The resolution algorithm:

1. If `--project-root <path>` is supplied (internal flag, used by tests), use that. Validate that the directory exists and contains a `.git` directory or file.
2. Otherwise, starting at the cwd, walk up the directory hierarchy. At each level, check for `.git`. The first directory containing it is the project root.
3. If the walk reaches the filesystem root with no match, the project root is **empty** and dependent resolutions degrade (the container registry falls through to the built-in default; build context cannot resolve; the command aborts).

Failure mode:

| Outcome | `code` | Recovery |
|---|---|---|
| Not in a git repo. | `NOT_IN_GIT_REPO` | Run `git init`, or pass `--project-root` to point at an existing repo. |
| `--project-root` supplied but the path doesn't exist or contains no `.git`. | `PROJECT_ROOT_INVALID` | Correct the path. |

## Component naming

A component is the unit of build + deploy. Every appearance of a component in the project tree, the deploy plan, the registry, and the env's Kubernetes namespace shares the same string. A skill writing a new component, or a build process resolving an existing one, derives all of the locations below from a single source name.

### Validation

The component name must match:

```
^[a-z][a-z0-9-]*$
```

That is: ASCII lowercase letters, digits, and hyphens; first character must be a letter. The constraint matches Kubernetes' DNS-1123 label rules (Deployment names, Service names, label values), which the component name lands in.

| `code` | Cause |
|---|---|
| `INVALID_COMPONENT_NAME` | Name fails the regex. |
| `COMPONENT_NAME_COLLISION` | A directory with this name already exists under `<tenant>-devops/docker/` or `<tenant>-devops/k8s/`. The skill writing the component aborts unless it was invoked with an explicit overwrite hint. |
| `RESERVED_NAME` | The name `<tenant>-devops` is reserved for the runtime-pod chart (see below). |

### Usage sites

The component name appears identically in every location below:

| Site | Path / value |
|---|---|
| Source module | `<projectRoot>/<component>/` (top-level) or `<projectRoot>/<module>/<component>/` (nested) |
| Docker build context | `<projectRoot>/<tenant>-devops/docker/<component>/Dockerfile` |
| VERSION file (optional per-component override) | `<projectRoot>/<tenant>-devops/docker/<component>/VERSION` |
| Helm chart | `<projectRoot>/<tenant>-devops/k8s/<component>/Chart.yaml` |
| Per-env values overlay | `<projectRoot>/<tenant>-devops/k8s/<component>/values.<env>.yaml` |
| Deploy plan | An entry in `ProjectConfig.environments.<env>.k8s.deployments[]` |
| Image reference | `<registry>/<component>:<version>` |
| Kubernetes resources | `Deployment.metadata.name = <component>`, `Service.metadata.name = <component>`, pod label `app: <component>` |

### Tenant prefix

Convention (not enforced): prefix every component with the tenant name.

```
erun-cli, erun-mcp, erun-backend-api, erun-backend-postgres, erun-devops
petios-frontend, petios-api, petios-devops
```

The convention buys:

1. **Registry-level uniqueness.** Multiple tenants pushing to one registry don't collide on image names.
2. **Self-describing identity in shared listings.** `kubectl get pods --all-namespaces`, deploy plans, audit trails — `petios-api` is unambiguously the api of petios.
3. **Coexistence with the runtime-pod chart.** That chart's component name is `<tenant>-devops` (see Reserved names below). Tenant-prefixed application components sit next to it in `k8s/` without ambiguity.

The language skills (`go-service`, `node-service`, `python-service`, `java-service`) apply the prefix automatically when generating a component from a short role name the Operator gave. An Operator who says "add a Go service called `api`" against tenant `hello-erun` ends up with `hello-erun-api`, not bare `api`. Skills can be overridden via project skills (see [Skills spec](/agent-reference/skills-spec)) if a project prefers a different convention.

### Reserved names

| Name | Reservation |
|---|---|
| `<tenant>-devops` | The runtime-pod chart's component name. Every env deploys it — from the repo-local chart at `<tenant>-devops/k8s/<tenant>-devops/` when the project carries one, otherwise from the published `erun-devops` chart (see [`erun deploy`](/cli/deploy#where-the-runtime-chart-comes-from)). Bears the runtime image and the env's `erun-devops` / `erun-mcp` / `erun-dind` containers. Application components must not collide with this name. |

### `Service` DNS implications

The Kubernetes Service named `<component>` is reachable in-cluster as:

```
<component>.<tenant>-<env>.svc.cluster.local
```

— see [Networking spec · DNS resolution](/agent-reference/networking-spec#dns-resolution). The component-naming regex above is what guarantees that hostname is a valid DNS-1123 label sequence.

## Multi-stage Dockerfile expectation

ERun expects Dockerfiles to use the multi-stage builder pattern — a builder stage that provisions toolchain, runs the tests, and produces an artefact, then a runtime stage that ships only the artefact.

Minimal skeleton:

```dockerfile
# Stage 1: builder — provisions toolchain, runs tests, produces the artefact.
FROM golang:1.26.0 AS builder
WORKDIR /src
COPY . .
# Tests that don't depend on a deployed artefact run here. A failure fails
# the build, so no image is produced from a red test run.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go test ./...
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags "-s -w" -o /out/app ./cmd/app

# Stage 2: runtime — thin, no build tools.
FROM alpine:3.20
COPY --from=builder --chmod=0755 /out/app /usr/local/bin/app
ENTRYPOINT ["app"]
```

Why ERun expects this:

- **Runtime images stay small** — no compilers, no source, no test deps in production.
- **Build cache stays effective** — BuildKit `--mount=type=cache` persists across builds in the runtime pod's dind PVC.
- **Security separation** — production images don't ship a build toolchain.

Single-stage Dockerfiles are not rejected, but the multi-arch and cache benefits don't apply.

### Tests run in the builder stage

ERun adds no separate test phase — `erun build` is `docker build`, so tests run where the Dockerfile puts them. The recommendation, **for any stack**, is to run the project's test command in the builder stage, as a `RUN` step before the artefact is produced — `go test ./...`, `npm test`, `pytest`, `mvn test`, `cargo test`, `dotnet test`, whatever the toolchain uses. The Go skeleton above is only an example; the rule is language-independent.

Run every test that **does not depend on a deployed artefact** there:

- **Unit tests**, and
- **integration tests that run in-process or against fixtures the build can stand up itself** (an embedded database, a stub server, a temp filesystem) — anything that needs no live cluster, no running service, and no network to a deployed dependency.

Because the test step is part of `docker build`, a failing test fails the build and no image is tagged — so a successful build is always a tested build, which is what marks a [review](/collaboration/reviews) `READY`. Keep the test step in the builder stage (never the runtime stage) so test dependencies stay out of the shipped image, and reuse the build cache (e.g. BuildKit `--mount=type=cache`) across the test and compile steps so downloads and intermediate objects are shared.

Whether the test step runs before or after the compile step is toolchain-specific, and doesn't matter to the gate. `go test` compiles and runs the tests on its own, so it can precede the `go build` that produces the shipped binary (as in the skeleton above); a toolchain that exercises a compiled artefact would build first, then test. The only requirement is that the test command is a `RUN` in the builder stage, so a non-zero exit aborts the build before the runtime stage is reached.

Tests that **do** require a running deployment — end-to-end checks against live services — cannot run in the builder stage. They run against a deployed environment after [`deploy`](/cli/deploy), not during build.

## Docker build context resolution

A Dockerfile is in the **standard layout** iff its absolute path matches:

```
^<projectRoot>/[^/]+/docker/[^/]+/Dockerfile$
```

That is: exactly one path segment between `<projectRoot>` and `docker/`, exactly one segment between `docker/` and `Dockerfile`. (`<tenant>-devops/docker/<component>/Dockerfile` is the canonical case.)

Algorithm:

1. Compute the Dockerfile's path relative to the project root.
2. Match against the regex above. If it matches:
   - **Standard layout.** Docker context = `<projectRoot>`. Image name = the directory name immediately under `docker/`. The Dockerfile can `COPY` from anywhere in the project tree.
3. Otherwise:
   - **Flat layout.** Docker context = the directory containing the Dockerfile. Image name = that directory's basename. `COPY` paths outside the Dockerfile's directory are not available.

The standard layout (`<module>/docker/<image>/Dockerfile`) is ERun's convention for project Dockerfiles; the flat layout is the fallback for hand-built contexts.

## Multi-architecture build contract

Every `erun build` produces both `linux/amd64` and `linux/arm64`. The build-graph order:

1. Verify the local Docker daemon advertises both target platforms. The check inspects `docker buildx ls` for builders supporting both. On miss → `BINFMT_MISSING` with a hint to run `docker run --privileged --rm tonistiigi/binfmt --install all`.
2. For each image, invoke `docker buildx build --platform linux/amd64,linux/arm64 …` — BuildKit drives the per-arch builds in parallel, sharing the build cache.
3. After build completes, two per-arch tags exist locally: `<image>:<version>-<arch>`.
4. For release-tagged builds (`erun build --release`), additionally push the per-arch tags and create a manifest list with `docker manifest create <image>:<version> --amend <image>:<version>-amd64 --amend <image>:<version>-arm64` followed by `docker manifest push`.
5. Snapshot builds skip step 4 (no manifest list; the per-arch tags stay local).

A partial-arch failure aborts the whole build for that image — there is no single-arch fallback. The contract is that an image either has both architectures or has not been built.

## VERSION file walking order

When ERun resolves the version for a build, it walks a sequence of candidate `VERSION` files in this order. The first file found wins:

1. `<buildDir>/VERSION` — the image's own pinned version.
2. Compute the next-up directory:
   - If `<buildDir>` matches `<...>/docker/<image>/` (the standard-layout Dockerfile container), hop up two levels — skipping the `docker/` parent and the `<image>/` directory.
   - Otherwise hop up one level.
3. From that directory, walk up toward the project root one level at a time. At each level, check for a `VERSION` file. Stop at the first hit, or when the project root is reached and exhausted.

Contents are the bare version string (e.g., `1.0.76`). Trailing newlines are stripped. A file containing anything else (multiple lines, surrounding whitespace beyond a trailing newline, version-incompatible characters) is rejected with `INVALID_VERSION_FILE`.

For an **agent env**, the version is transformed to `<semver>-snapshot-<UTC-timestamp>`. For a **runtime env**, it's used as-is (release tag).

The full algorithm from project-root resolution through final image-tag construction lives in [Build path resolution](/reference/configuration-build-paths).

## Command override resolution

Any `erun` command can be overridden by a matching `<command>.sh` script.

| Drop this file | Overrides |
|---|---|
| `build.sh` | `erun build` |
| `push.sh` | `erun push` |
| `deploy.sh` | `erun deploy` |
| `release.sh` | `erun release` |
| `<command>.sh` | the matching `erun <command>` |

ERun looks for the script in this order:

1. `<projectRoot>/<command>.sh` — the top-level project script.
2. Otherwise, the first nested `*/<command>.sh` it finds during a walk, **skipping `docker/` and `linux/` artifact subtrees** (those are inside image build contexts, not module roots).

The override contract:

- The script runs with the resolved env's environment variables in scope.
- ERun's built-in command logic is bypassed entirely.
- Exit code determines success or failure.
- The script is responsible for tagging images, pushing, helm calls, migrations — whatever the built-in would have done.

Override applicability by env type:

- **Agent envs** consult overrides for any command. `build.sh`, `push.sh`, `release.sh` are agent-env-only by definition (runtime envs don't build / push / release).
- **Runtime envs** consult overrides only for commands meaningful there — primarily `deploy.sh`.

The trade-off: ERun's audit trail and `--dry-run` previews show what the *override script* prints; they can't introspect inside it. Use overrides sparingly.

## Fingerprint cache

Every Docker build computes a **content fingerprint** over the Dockerfile and the files it consumes. The fingerprint drives whether the image is rebuilt or promoted from a cached copy.

### Fingerprint computation

1. Parse the Dockerfile to enumerate every `COPY` directive. For each `COPY <src>... <dst>`, expand `<src>` against the build context (standard layout: `<projectRoot>`; flat layout: the Dockerfile's directory).
2. For each expanded source path (file or directory), walk its tree in deterministic order: a depth-first, alphabetically-sorted enumeration. Respect `.dockerignore` at the build context root and at each subtree's root.
3. Build the input buffer by concatenating, for each visited file: its **relative path** (a stable identifier), a null byte (`\x00`), the file's **bytes**, and a null byte. For directories, only the path is included (no contents).
4. Append the Dockerfile's bytes to the buffer.
5. Compute `SHA-256` over the buffer. The first 8 hex bytes (16 chars) form the fingerprint string written as `fp-<hash>`.

Build args (`--build-arg KEY=VALUE`) are **not** part of the fingerprint — they affect runtime behaviour but not the image content layers. A build whose only change is a `--build-arg` value still cache-promotes (and may produce a stale image; this is the documented limitation).

### Cache state

Each successful build writes the fingerprint to `<projectRoot>/.erun/config.yaml` under `environments.<env>.docker.fingerprints.<image>`. The file is committed to the repo so fresh clones can promote without rebuilding.

### Build-time algorithm

1. Compute the local fingerprint per the rule above.
2. Read the configured fingerprint from `<projectRoot>/.erun/config.yaml`.
3. If local == configured: pull `<registry>/<image>:fp-<configured>-<arch>` from the registry. On hit, re-tag locally as the current build's intended tag. **Skip the `docker build` invocation.** Emit `result: cached`.
4. If local != configured (or the pull misses): run `docker buildx build` per the [multi-architecture contract](#multi-architecture-build-contract). Update `<projectRoot>/.erun/config.yaml` with the new fingerprint. Emit `result: built`.

`--no-incremental` on `erun build` skips steps 1–3 and rebuilds every image unconditionally.

`--force` on `erun build --release` additionally deletes the prior `fp-<hash>-<arch>` tags in the registry before pushing the new ones — recovery path for a known-bad release that needs to be overwritten.

## Helm Job pattern for one-shots

ERun deploys two kinds of workload through helm charts:

- **Pods** (Deployments / StatefulSets) for long-running processes.
- **Jobs** for one-shot deployment operations targeting external resources (database migrations, CDN uploads, Lambda updates, CloudFront invalidations, terraform applies).

The conventional one-shot Job chart:

```yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: <component>-deploy
  annotations:
    helm.sh/hook: post-install,post-upgrade
    helm.sh/hook-delete-policy: before-hook-creation,hook-succeeded
    helm.sh/hook-weight: "10"
spec:
  backoffLimit: 2
  ttlSecondsAfterFinished: 600
  template:
    spec:
      restartPolicy: Never
      serviceAccountName: <component>-deployer
      containers:
        - name: deploy
          image: <registry>/<component>:<version>
          env: [ /* credentials, target IDs */ ]
```

Why this pattern:

- **One source of truth for "deploy this change"** — the Job's container image is built and versioned exactly like a service. The deploy step is `erun deploy <component>`, same shape as anything else.
- **Idempotent and auditable** — every deploy creates a fresh Job (helm hooks make this automatic). The Job's logs are the audit trail.
- **No long-running pods serving nothing** — for things like CDN uploads, you don't want a pod sitting around between deploys.

Examples in the erun repo:

- `erun-devops/k8s/erun-backend-db/` — a migration Job that runs Atlas migrations against the env's postgres.
- `erun-devops/k8s/erun-docs/` — a Cloudflare Pages deploy Job that runs `wrangler pages deploy /site/`.

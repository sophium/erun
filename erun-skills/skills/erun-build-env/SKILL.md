---
name: erun-build-env
description: Create a custom build environment by extending ERun's published runtime image with the project's own toolchain, then pointing the environment at the result, and maintain, repair, or upgrade an existing custom build environment in place by re-pinning it to the target runtime version and filling any gaps against this skill's contract. Use when the user says "init build environment", "init erun build environment", "create a custom build environment", "customize the runtime image", "upgrade the build environment", "upgrade the custom runtime image", "repair the build environment", "reconcile the <tenant>-devops module", "bump the runtime image to <version>", "maintain the build environment", or any similar request to add tools to, or keep current, the image an environment's runtime pod runs.
---

# Custom build environment

Use this skill when the project needs tools the stock ERun runtime image
doesn't ship (a language toolchain, a CLI, system packages). The motion:
create a `<tenant>-devops` module that holds a Dockerfile extending the
published `erun-devops` image, give it a `VERSION`, build and push it with
`erun build`, and set it as the environment's runtime image. The next
deploy/open rolls it out.

Throughout, `<tenant>` is the env's tenant (e.g. tenant `acme` →
`acme-devops`). The worked paths below use `acme`.

## Step 1 — determine the runtime version and registry

The custom image must extend the same version the environment runs.

```sh
if [ -n "${ERUN_TENANT:-}" ]; then
    # Inside a deployed env: the running version is the one to extend. The first
    # line is "erun <version> (<commit> built <date>)" — take the 2nd field;
    # --no-registry skips the remote version lookup.
    runtime_version=$(erun version --no-registry | head -n 1 | awk '{print $2}')
else
    # On a laptop: pull the value out of the env's "runtimeversion: <ver>" line
    # (or inspect the per-env block of `erun list`).
    runtime_version=$(grep 'runtimeversion' ~/.config/erun/<tenant>/<environment>/config.yaml | awk '{print $2}')
fi
```

The registry defaults to `ghcr.io/sophium`; if the env config carries a
`runtimeregistry` or `containerregistry`, use that instead.

## Step 2 — create the `<tenant>-devops` module and Dockerfile

The custom runtime image **replaces** the stock `erun-devops` image (it rides
into the published chart as `imageOverrides.erun-devops` on deploy), so its
module and image follow a fixed shape:

- The outer module directory name **must end in `-devops`** — `erun build`
  discovers the runtime build module by that suffix. Name it `<tenant>-devops`.
- The inner `docker/<image-name>/` directory name becomes the **image name**.
  Name it `<tenant>-devops` too, so the image that replaces the runtime is
  unmistakably named for it.

Result (tenant `acme`):

```
acme-devops/
├── VERSION                              # created in Step 3
└── docker/
    └── acme-devops/
        └── Dockerfile
```

Write the Dockerfile at `<tenant>-devops/docker/<tenant>-devops/Dockerfile`:

```dockerfile
FROM ghcr.io/sophium/erun-devops:<runtime-version>

# Add the project's toolchain here. Examples:
# RUN apt-get update && apt-get install -y --no-install-recommends \
#         <packages> \
#     && rm -rf /var/lib/apt/lists/*
# RUN curl -fsSL <tool-installer-url> | sh
```

Guide the user on what to add: keep layers small, pin versions, clean
package caches in the same `RUN`. Commit the module with `git` so the team
shares the toolchain.

## Step 3 — add the module's VERSION file

`erun build` mints the image version from a `VERSION` file and fails with
`version file not found for current module` without one. Create a plain
`VERSION` at the **module root** (`<tenant>-devops/VERSION`) holding a semver:

```sh
printf '1.0.0\n' > acme-devops/VERSION
```

`erun build` stamps the image as `<version>-snapshot-<timestamp>` from this
base (a plain `--release` build uses the bare version). Bump it when you cut
a versioned runtime image. Commit it alongside the Dockerfile.

## Step 4 — build and push

```sh
erun build
```

Run it from the project root (or from inside `<tenant>-devops/`). `erun build`
runs the Docker build for both `linux/amd64` and `linux/arm64` and pushes the
result to the env's registry. Add `--dry-run` first to preview the exact
commands — a clean dry-run resolves the image to
`<registry>/<tenant>-devops:<version>-snapshot…` for both architectures.

## Step 5 — point the environment at the image

Either persist it through init:

```sh
erun init <tenant> <environment> --runtime-image <tenant>-devops
```

or edit the env config (`~/.config/erun/<tenant>/<environment>/config.yaml`)
and set the `runtimeimage` field. A full reference
(`ghcr.io/acme/acme-devops:1.2.3`) is used verbatim; a bare name
(`acme-devops`) resolves to `<registry>/<name>:<runtime version>`.

On deploy, the image rides into the published `erun-devops` chart as
`imageOverrides.erun-devops` — the chart stays canonical, only the
runtime container's image changes. The next `erun deploy` or `erun open`
rolls the custom image out.

## Step 6 — customise the runtime pod shape (optional)

The image customises **what's installed**; some needs are **pod-shape** and a
Dockerfile can't express them — a sidecar container, an extra volume/mount, extra
env, or the cluster RBAC a sidecar needs. For those, wrap the published
`erun-devops` chart in a `<tenant>-devops` umbrella — **reference it, never fork
it**. Skip this step unless you have such a need; image-only via `imageOverrides`
(Step 5) is the default and adds no chart.

Create `<tenant>-devops/k8s/<tenant>-devops/` — the dir name, the `Chart.yaml`
`name:`, and the helm release are all `<tenant>-devops` (the runtime release
name):

```yaml
# acme-devops/k8s/acme-devops/Chart.yaml
apiVersion: v2
name: acme-devops
version: 0.1.0
dependencies:
  - name: erun-devops
    version: "1.0.0"                 # the runtime version from Step 1
    repository: "oci://ghcr.io/sophium/charts"
```

Supply the pod shape through the published chart's extension hooks, nested under
the `erun-devops` subchart key, in a **per-env** `values.<env>.yaml` (one for
every env this env deploys to — a missing one fails `erun deploy`):

```yaml
# acme-devops/k8s/acme-devops/values.prod.yaml
erun-devops:
  extraContainers:                   # sidecars added to the runtime pod
    - name: cache
      image: redis:7
  extraVolumes:
    - name: scratch
      emptyDir: {}
  extraVolumeMounts:                 # mounted on the erun-devops container
    - name: scratch
      mountPath: /scratch
  extraEnv:                          # extra env on the erun-devops container
    - name: FOO
      value: bar
  extraRules:                        # extra cluster RBAC for the pod's SA
    - apiGroups: [""]
      resources: ["nodes"]
      verbs: ["get", "list"]
```

`erun deploy` picks this up as the runtime chart (it matches the runtime release
name), `helm dependency build`s it, and **re-scopes every runtime value it sets
— tenant, ports, cloud context, MCP auth, and the `imageOverrides.erun-devops`
image from Step 5 — under the `erun-devops.` subchart key**, so the wrapped
runtime is wired exactly as the published chart would be. Track `Chart.lock`
(committed) and gitignore `charts/*.tgz`, as the platform umbrellas do.

## Maintenance, repair & upgrade

This skill also maintains a `<tenant>-devops` module a prior run already
produced. If the module exists, do **not** stop — reconcile it in place. Preview
the diff before writing; touch only version pins and genuine gaps, never the
project's own Dockerfile toolchain layers.

**Detect.** Look for `<tenant>-devops/` with a Dockerfile and `VERSION`. Present →
maintenance mode; absent → the scaffold flow above.

**Repair** against this skill's contract, without clobbering project content:

- Missing `VERSION` at the module root → add one (Step 3).
- Wrong module/dir naming → the outer dir and the `docker/<image>/` dir must both
  be `<tenant>-devops` (Step 2); rename to match so `erun build` discovers it.
- Dockerfile not `FROM ghcr.io/sophium/erun-devops:…` → repoint the base (Step 2);
  keep the project's added toolchain layers as-is.
- If the Step 6 umbrella is present, backfill a missing per-env
  `values.<env>.yaml`, a committed `Chart.lock`, or the gitignored `charts/*.tgz`.

**Upgrade** to one erun version across every pin — the env's current
`runtimeversion` (it moves with `erun upgrade`) or an explicit target (Step 1):

- Re-pin the Dockerfile `FROM ghcr.io/sophium/erun-devops:<version>`.
- Re-pin the module `VERSION` to that version.
- If the Step 6 umbrella is present, re-pin its `erun-devops` dependency
  `version:` in `Chart.yaml` to the same version, then `helm dependency update` to
  regenerate `Chart.lock` for the new version (`helm dependency build` errors on a
  stale lock); commit the updated lock.
- `erun build` to rebuild and push both arches, then confirm the env's
  `runtimeimage` still points at the module (Step 5).

Bump every pin together — the whole module rides one erun version. Re-running is
safe: it edits in place and only moves version pins and fills gaps.

**Clean up.** If the module was renamed or relocated (a stray `<old>-devops/`, or a
second `docker/<oldname>/` under it), remove the superseded copy after previewing so
`erun build` discovers exactly one runtime module. Leave the project's toolchain
layers alone, and don't prune pushed images — an unreferenced tag in the registry is
the operator's to remove, not this skill's; note it rather than deleting it.

## Important

- Always extend `erun-devops`; do not replace it with an unrelated base —
  the entrypoint, agents, and in-pod tooling live in that image.
- Keep the `FROM` version aligned with the env's `runtimeversion`. After
  an `erun upgrade`, rebuild the custom image against the new version.
- The optional runtime umbrella (Step 6) **references** the published
  `erun-devops` chart as a subchart; never fork its templates. A knob you need
  that the chart doesn't expose is a change to `erun-devops`, not a copy here.

---
title: CLI flag spec
---

# CLI flag spec

> For the Operator-facing workflow per command, see the [CLI section](/cli/overview).

Every `erun` flag, by command. Type, default, validation, and where the resolved value persists. Operator-facing pages show only the flags an Operator types day-to-day; this page is the complete contract.

Common flags inherited from the root command apply to every subcommand:

| Flag | Type | Default | Effect |
|---|---|---|---|
| `--output` | enum `text` \| `json` | `text` | Output mode. `text` is the human-readable stream. `json` emits a single structured result object on stdout for orchestrators (the human stream is suppressed or sent to stderr). See [Structured output](#structured-output-flag). |
| `--dry-run` | bool | `false` | Resolve and print the trace without performing side effects. Implies trace verbosity. |
| `-v` / `--verbose` | bool | `false` | Stream external tool output (`helm --debug`, `kubectl --v=4`, …). |
| `-vv` | bool | `false` | `-v` plus per-command `trace:` lines for every action + decision. |
| `--time` | bool | `false` | Print elapsed wall time at the end. |
| `--help` / `-h` | bool | `false` | Print command help and exit `0`. |

### Structured output (`--output json`) {#structured-output-flag}

`--output json` is the orchestration handoff. Every command accepts it; each emits the typed result documented under its section below. The result for [`erun build`](#erun-build) is the one orchestrators most depend on:

```jsonc
{
  "version": "1.0.81-snapshot-20260616120000",   // the minted version — the content identity
  "baseVersion": "1.0.81",                        // the bare VERSION base, before the snapshot suffix
  "images": [                                     // every image built/tagged at this version
    { "image": "ghcr.io/sophium/erun-devops", "tag": "1.0.81-snapshot-20260616120000",
      "arches": ["linux/amd64", "linux/arm64"], "status": "built" }
  ]
}
```

An orchestrator (the desktop app, a script, an Agent over MCP) runs `erun build --output json`, captures `version`, then threads that exact value into [`erun push --version <version>`](#erun-push) and [`erun deploy --version <version>`](#erun-deploy). This is why `push`/`deploy` require an explicit version and the convenience switches (`build --deploy` / `build --release`) are reserved for an Operator at the terminal — programmatic callers compose the primitives and pass the version themselves.

## `erun init`

### Common flags

See [`erun init`](/cli/init) — `--tenant`, `--environment`, `--kubernetes-context`, `--container-registry`, `--runtime-image`, `--set-default-tenant`, `-y` / `--yes`.

### Advanced flags

| Flag | Type | Default | Validation | Persists to |
|---|---|---|---|---|
| `--project-root <path>` | string (absolute path) | `<cwd>`'s git repo root (`git rev-parse --show-toplevel`) | Must be an existing directory; must contain a `.git/` directory or `.git` file. | The new env's `EnvConfig.localRepoPath` (every env type records it; #549). |
| `--remote` | bool | `false` | Conflicts with a `--type` whose value disagrees (e.g. `--type=local-agent --remote`). | Deprecated alias for `--type=remote-agent`: sets `EnvConfig.type = remote-agent`. Init then writes the in-pod bootstrap marker. |
| `--no-git` | bool | `false` | Only meaningful with `--remote` / `--type=remote-agent`. | Skips the in-pod `git clone` step. |
| `--version <version>` | string (semver) | The CLI's built-in `ERUN_VERSION`. | Must satisfy `^[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.-]+)?$`. | `EnvConfig.runtimeversion`. |
| `--runtime-image <ref>` | string | unset (the published `<registry>/erun-devops:<version>` image). | A full OCI reference (registry path and/or tag present) is used verbatim; a bare name resolves to `<registry>/<name>:<runtime version>` at deploy time. | `EnvConfig.runtimeimage`; applied as `imageOverrides.erun-devops` on every published-chart deploy. |
| `--bootstrap` | bool | `false` | — | **Deprecated, ignored.** Prints a deprecation warning; `init` no longer scaffolds a `<tenant>-devops/` module — envs deploy the published `erun-devops` chart. |
| `--runtime-cpu <value>` | Kubernetes quantity | `4` | Must match the Kubernetes `Quantity` grammar (`m`, plain integer, decimal). | `EnvConfig.runtimepod.cpu`. |
| `--runtime-memory <value>` | Kubernetes quantity | `8916Mi` | Must match the Kubernetes `Quantity` grammar (`Ki`, `Mi`, `Gi`, …). | `EnvConfig.runtimepod.memory`. |
| `--codecommit-ssh-key-id <id>` | string (`APKA…` shape) | unset | Must start with `APKA`; must be a valid IAM key id (length 21). | Stored in the in-pod bootstrap marker (`bootstrap.yaml` → `codecommitSshKeyId`). |
| `--confirm-environment` | bool | `false` | — | Equivalent to `-y` for the env-overwrite confirmation only. |

### Side effects

`erun init` writes these files in this order:

1. `~/.config/erun/<tenant>/tenant.yaml` (creating `~/.config/erun/<tenant>/` if missing).
2. `~/.config/erun/<tenant>/<env>/config.yaml`.
3. `<projectroot>/.erun/config.yaml`. Existing values are preserved; new defaults are merged.
4. Helm-installs the runtime chart into the namespace `<tenant>-<environment>` — the repo-local chart when the project has one, otherwise the published `oci://<registry>/charts/erun-devops` chart pinned to the runtime version (see [`erun deploy`](/cli/deploy#where-the-runtime-chart-comes-from)).
5. With `--remote`: writes the in-pod marker at `/home/erun/.erun/<tenant>/<env>/bootstrap.yaml`.

### `erun init` lifecycle algorithm

1. Parse flags; resolve effective tenant + env (see [Configuration · Resolution order](/reference/configuration#effective-tenant--environment-for-a-cli-command)).
2. Validate `--kubernetes-context` against `~/.kube/config`. On miss, abort with the available context list.
3. Resolve `--project-root` (defaults to `git rev-parse --show-toplevel`). On miss, abort with `not in a git repository`.
4. If the tenant/env already exists, prompt unless `-y` / `--confirm-environment`. Aborting on `n` is the safe default.
5. Resolve the runtime chart — repo-local when the project carries one, the published `oci://<registry>/charts/erun-devops` otherwise — and `helm upgrade --install` it into `<tenant>-<environment>`.
6. With `--remote`: open SSH and write the in-pod bootstrap marker.
7. Update default-tenant pointer if `--set-default-tenant`.
8. Exit `0`.

### Error codes

| Code | Cause | Exit code |
|---|---|---|
| `NOT_IN_GIT_REPO` | `--project-root` unset and cwd is not in a git repo. | `1` |
| `KUBE_CONTEXT_MISSING` | `--kubernetes-context` is not present in `~/.kube/config`. | `1` |
| `HELM_INSTALL_FAILED` | Runtime chart install failed; the per-user config is written but the in-pod marker is not. | `2` |
| `REGISTRY_UNREACHABLE` | `--container-registry` is set but DNS/network failed. (Warning, not abort.) | `0` (with warning) |

---

## `erun open`

### Common flags

`--tenant`, `--environment`, `--no-shell`, `--vscode`, `--intellij`.

### Advanced flags

| Flag | Type | Default | Validation | Persists to |
|---|---|---|---|---|
| `--no-alias-prompt` | bool | `false` | Only meaningful with `--no-shell`. | None (interactive choice only). |
| `--version <version>` | string (semver) | `EnvConfig.runtimeversion` or the CLI built-in. | Same as `erun init --version`. | `EnvConfig.runtimeversion` for this run only (not persisted). |
| `--runtime-image <ref>` | string | `EnvConfig.runtimeimage` (unset → the published image). | Same reference rules as `erun init --runtime-image`. Applies only to envs deploying the published chart (rides in as `imageOverrides.erun-devops`); envs with a repo-local chart ignore it. | Run-only override (not persisted). |

`erun open` is a pure primitive: it brings up the runtime pod for the env's recorded version (installing the published chart by reference) and attaches a shell. It does **not** build, push, or mint a version — there is no build branch on env `type`. The retired `--snapshot`/`--no-snapshot` pair has no replacement flag. Rolling out a new version is the caller's job: the desktop app composes [`build`](#erun-build) → [`push`](#erun-push) → [`deploy`](#erun-deploy) around the open, threading the version it captured from `build --output json`.

### `erun open` lifecycle algorithm

1. Parse flags; resolve effective tenant + env.
2. Load `EnvConfig` (Kubernetes context, container registry, runtime version, type). `open` installs the published chart for the recorded runtime version by reference; it never builds or pushes.
3. If `EnvConfig.cloudprovideralias` is set, look up the cloud context. If `stopped`, send the provider-specific start command. Poll the cluster API every `5s` until reachable or 5 minutes elapse (then abort `CLUSTER_UNREACHABLE`).
4. Render the runtime chart with the effective `EnvConfig` values; run `helm upgrade --install <env>-runtime <chart>` into `<tenant>-<env>`.
5. Wait for the runtime pod's SSH server to be reachable on the in-pod port (`EnvConfig.sshd.port`, default `22`). Readiness probe is a TCP connect + banner-line read, retried every `2s` with a `60s` cap.
6. Establish local port-forwards. `erun open` starts a detached `kubectl port-forward` per channel and records it at `<UserConfigDir>/erun/portforward/{mcp,sshd,api}/<tenant>/<env>.json` with `{tenant, environment, kubernetesContext, namespace, localPort, logPath, processId}` — see [Networking spec · Port-forward state files](/agent-reference/networking-spec#port-forward-state-files).
7. Attach a terminal (default), print kubectl/cwd switching commands (`--no-shell`), or launch the IDE (`--vscode`/`--intellij`).
8. Exit `0` when the terminal exits.

### Error codes

| Code | Cause | Exit code |
|---|---|---|
| `TENANT_NOT_CONFIGURED` | Resolved tenant has no `~/.config/erun/<tenant>/tenant.yaml`. | `1` |
| `KUBE_CONTEXT_MISSING` | `EnvConfig.kubernetescontext` is absent from `~/.kube/config`. | `1` |
| `CLUSTER_UNREACHABLE` | Cluster API does not respond after 5 minutes. | `2` |
| `CLOUD_START_FAILED` | Cloud-provider start command returned an error or the context entered a terminal failure state. | `2` |
| `HELM_UPGRADE_FAILED` | `helm upgrade --install` returned non-zero. The release is in helm's failure state; consult `helm history`. | `2` |
| `SSH_READY_TIMEOUT` | SSH server did not come up within the `60s` readiness window. | `2` |
| `IDE_LAUNCHER_MISSING` | `--vscode` / `--intellij` requested but the launcher binary isn't on `PATH`. Falls back to printing SSH details; exit `0`. | `0` |

---

## `erun build`

`erun build` is the **version-minting** primitive: it builds the images and stamps the version that `push`/`deploy` later consume. By default it mints a snapshot (`<base>-snapshot-<UTC-timestamp>`); `--release`, an explicit `--version`, or a version carried by the build directory pins the bare version instead. `build` never decides snapshot-vs-stable from the environment type.

### Common flags

`--deploy`, `--release`, `--force`, `--dry-run`, `--output`.

`--deploy` and `--release` are **operator-convenience switches** that compose downstream primitives (`--deploy` → push + deploy; `--release` → the release flow). Programmatic callers do not use them: they run `erun build --output json`, capture `version`, and call `push`/`deploy` themselves. See [Structured output](#structured-output-flag).

### Advanced flags

| Flag | Type | Default | Validation | Notes |
|---|---|---|---|---|
| `--no-incremental` | bool | `false` | — | Disables the fingerprint cache. Every Docker context rebuilds. |
| `--version <version>` | string (semver) | Resolved per [Build path resolution · VERSION walking](/reference/configuration-build-paths). | Same as `erun init --version`. Conflicts with `--release` (which resolves the version itself). | Pins a bare version for this build instead of minting a snapshot. |

### `--output json` result

`erun build --output json` prints the [structured output](#structured-output-flag) object: `{version, baseVersion, images}`. `version` is the minted content identity an orchestrator threads into `push`/`deploy`.

### `erun build` lifecycle algorithm

1. Parse flags; resolve effective tenant + env. Refuse with `BUILD_AGAINST_RUNTIME_ENV` if env type is `runtime` (a runtime env has no source to build).
2. Resolve project root, build scope, Dockerfile, build context, VERSION per [Build path resolution](/reference/configuration-build-paths).
3. **Mint the version.** Default: append the snapshot suffix `-snapshot-<UTC-timestamp>` to the resolved base. With `--release` / `--version` / a build-dir version: use the bare version. Compute the per-image content fingerprint.
4. For each resolved image:
   a. If fingerprint matches the registry copy and `--no-incremental` / `--force` is not set: promote the registry copy locally; skip the build.
   b. Otherwise: invoke `docker buildx build --platform linux/amd64,linux/arm64 -t <registry>/<image>:<version> -f <Dockerfile> <context>` with the resolved `--build-arg` set.
   c. Tag the result `<registry>/<image>:fp-<fingerprint>-<arch>` for each architecture.
5. Emit the minted version (and, with `--output json`, the structured result).
6. If `--deploy`: compose push + deploy at the minted version (operator-convenience shortcut). If `--release`: chain to `erun release`, which builds, then reuses `push` to publish the release-tagged variants and chart. Programmatic callers skip both and orchestrate the primitives themselves.
7. Exit `0`.

### Multi-arch verification

Before any `docker build` call, the binary inspects `docker buildx ls` for builders advertising `linux/amd64` *and* `linux/arm64`. If either platform is missing, abort with `BINFMT_MISSING` and print:

```
binfmt for <arch> not installed. Run:
  docker run --privileged --rm tonistiigi/binfmt --install all
```

### Error codes

| Code | Cause | Exit code |
|---|---|---|
| `BUILD_AGAINST_RUNTIME_ENV` | `erun build` called against an env where `EnvConfig.type == "runtime"`. | `1` |
| `NO_PROJECT_ROOT` | cwd is not inside a project root. | `1` |
| `NO_BUILDABLE_CONTEXT` | Walked up from cwd and found no `<tenant>-devops/docker/<image>/` directory. | `1` |
| `BINFMT_MISSING` | Local docker daemon cannot produce one of the target platforms. | `2` |
| `BUILD_FAILED` | `docker buildx build` returned non-zero. | `2` |

---

## `erun push`

### Version (required)

| Flag | Type | Required | Notes |
|---|---|---|---|
| `--version <version>` | string (semver, snapshot or bare) | **Yes** | The version to publish (the same flag `deploy` uses, for consistency across commands). `push` does not mint a version — it builds each image from source at this version (promoting unchanged images from the fingerprint cache), pushes the per-arch tags, assembles the multi-arch manifest list, then publishes the runtime helm chart. Missing → `NO_VERSION` (exit 1). |

### What push publishes

For the supplied `--version`, `push` always builds each image from its source context (never a prebuilt bare tag), pushes per-arch tags, assembles the manifest list, then runs `helm package` + `helm push` to `oci://<registry>/charts` and verifies with a `helm pull` round-trip. Image and chart are published together at the same version. There is no environment-type branch. [`erun release`](#erun-release) reuses this step for all its publishing.

### Common flags

`--force`, `--dry-run`, `--output`.

### Authentication retry semantics

When `docker push` returns one of these registry-side error strings, `erun push` retries automatically:

| Registry response (substring match, case-insensitive) | Retry |
|---|---|
| `unauthorized` | Re-runs `docker login <registry>` interactively (TTY required). |
| `denied` | Same. |
| `insufficient_scope` | Same. |
| `does not match expected scopes` (GHCR-specific) | Invokes `gh auth refresh -s write:packages,read:packages` and retries. |
| `permission_denied` (GHCR-specific) | Same as above. |

If no TTY is attached, the retry skips the login prompt and surfaces the original error.

### Error codes

| Code | Cause | Exit code |
|---|---|---|
| `NO_VERSION` | No `<version>` argument. `push` publishes a specific version; it does not mint one. | `1` |
| `NO_BUILDABLE_CONTEXT` | No `<tenant>-devops/docker/<image>/` build context found to build the version from. | `1` |
| `REGISTRY_AUTH_FAILED` | All retry attempts failed (or no TTY for the interactive login). | `2` |
| `MANIFEST_LIST_ASSEMBLY_FAILED` | Per-arch tags pushed but `docker manifest create` failed. | `2` |
| `CHART_PUSH_FAILED` | Images pushed but `helm push` or the `helm pull` verification of the runtime chart failed — the version is not yet deployable. | `2` |

---

## `erun deploy`

`erun deploy` is a **pure consume** primitive: it helm-installs an already-published version by reference. It never builds, pushes, or publishes — a version is required input, not something it mints.

### Common flags

`--version`, `--current`, `--components`, `--force`, `--dry-run`, `--output`. Subcommand: `erun deploy <component>`.

### Version selection — `--version` / `--current` (required)

`erun deploy` and `erun upgrade` are *consume* operations: the resolved version names a content identity to install, not a label to stamp on a fresh build. **A version is required** — exactly one of:

| Flag | Type | Meaning |
|---|---|---|
| `--version <v>` | string (semver, snapshot or bare) | Install version `<v>` by reference. |
| `--current` | bool | Install the env's persisted runtime version (`EnvConfig.runtimeversion`) — redeploy what it already runs. Errors if the env has no recorded version yet. |

Passing **neither** is an error (`NO_VERSION`): `deploy requires a version — pass --version <v> or --current`; exit 1; nothing runs. `deploy` does not build, so there is no fallback to "produce a version from the working tree" — that path no longer exists. The desktop app and other orchestrators always pass `--version`, threading the value captured from [`erun build --output json`](#erun-build).

Once the version is resolved, `deploy`:

1. Resolves **no** docker builds and pushes **nothing** (so it can never overwrite the published version). It runs `helm upgrade --install` pinned to the version.
2. Verifies each image the chart references at the version exists. For every `image:` ref the chart resolves at `AppVersion == <v>`: registry-less refs (the app images, which take their registry from `--set containerRegistry`) are qualified with the deploy registry; refs pinned at a different version (infra/base images such as dind and binfmt) are skipped. The check is `docker manifest inspect` (then a local `docker image inspect` fallback); in `--dry-run` it is traced and the network call is skipped. A registry error that is not a definitive "absent" does not block the deploy.
3. Errors during resolution, before `helm upgrade`, when an image at the version is absent both locally and in the registry: `deploy --version <v>: image <ref> is not present locally or in the registry; deploy installs an existing version and does not build it — run erun build/push to create it first`.

The dry-run trace names the decision per spec: `deploy: version <v> pinned; installing the published image, no local build`. Every env installs by reference from the published `oci://…/charts/erun-devops` chart (or the repo-local chart when the project carries one).

### `--components` value set

The `--components` flag's accepted values are derived from `<tenant>-devops/k8s/<component>/Chart.yaml` discovery at command-resolve time. For the erun repository itself, the registered set is `{erun-backend-postgres, erun-backend-db, erun-backend-api}`. Each tenant project ships its own set.

### Deploy-plan resolution

The deploy plan comes from `ProjectConfig.environments.<env>.k8s.deployments[]` (see [Configuration · `environments.<env>.k8s.deployments[]`](/reference/configuration#per-project-config)). When the field is absent, `erun deploy` falls back to ordering by chart dependency declarations; on a tie, alphabetical by component name.

### Skip-helm semantics

`erun deploy` skips the `helm upgrade --install` for a component when:

1. The resolved version equals the version the env already runs (no rollout would change anything), and
2. The chart directory (`<tenant>-devops/k8s/<component>/`) has no diff against the last successful deploy's snapshot, and
3. `--force` is not set.

The skip emits `result: skipped (no change)` in the trace. `deploy` never pushes, so there is no `docker push` to skip.

### Immutable-selector recovery

A Kubernetes `Deployment.spec.selector` is immutable: helm cannot patch a release whose installed selector differs from the chart's rendered selector, and aborts the upgrade with `Deployment.apps "<name>" is invalid: spec.selector: … field is immutable`. This happens when an environment was first installed under a chart that rendered a different selector than the one now being applied (e.g. a pre-cutover per-tenant chart that labelled pods `app: <release>` versus a chart that hardcoded `app: erun-devops`, or vice-versa).

`erun deploy` detects this specific failure and recovers automatically, in `erun-common` so both CLI and MCP flows get it:

1. It parses the offending Deployment name from helm's error and deletes **only** that Deployment (`kubectl delete deployment <name> --namespace <ns> [--context <ctx>] --ignore-not-found`). The release's PVCs (`<release>-home`, `<release>-docker`, `<release>-worktree`), ServiceAccount, and RBAC are separate objects and are **not** touched, so build cache and `/home/erun` survive.
2. It retries the `helm upgrade --install` **once**. With the Deployment gone, helm creates it fresh with the new selector.

The recovery is bounded to a single retry (the delete removes the conflict, so the retry cannot hit the same error) and fires only for an immutable `spec.selector` change — an unrelated immutable-field error is not caught and never triggers a delete. It runs only in real execution, not `--dry-run` (the conflict is a helm side-effect failure, not a pre-action decision). The trace names the decision on the audit channel: `deploy: Deployment <name> selector is immutable and changed; deleting it (PVCs preserved) and retrying the upgrade`; the literal `kubectl delete` is logged at `-vv`. If the retried upgrade fails for any other reason, that error surfaces as `HELM_UPGRADE_FAILED`.

### Error codes

| Code | Cause | Exit code |
|---|---|---|
| `NO_VERSION` | Neither `--version` nor `--current` given. `deploy` does not mint a version, so there is nothing to install. | `1` |
| `NO_CURRENT_VERSION` | `--current` given but the env has no recorded runtime version yet. Deploy a specific `--version` once to seed it. | `1` |
| `CLUSTER_UNREACHABLE` | Same as `erun open`. | `2` |
| `MISSING_IMAGE_IN_REGISTRY` | A chart references `<registry>/<component>:<version>` that does not exist (and was never built/pushed). | `1` |
| `MISSING_CHART_IN_REGISTRY` | The runtime chart was not published at the requested version (`helm pull` failed). Push the version first — push publishes image and chart together. | `1` |
| `HELM_UPGRADE_FAILED` | A step in the plan failed; later steps are not executed. | `2` |

---

## `erun doctor`

### Flags

| Flag | Type | Default | Effect |
|---|---|---|---|
| `--dry-run` | bool | `false` | Run the inspection; print the recovery plan; do not execute it. |
| `-y` | bool | `false` | Auto-approve every offered recovery action. |
| `--clear-pending-helm` | bool | `false` | Run the clear-pending-helm recovery without prompting (see [Deploy recovery actions](#deploy-recovery-actions)). |
| `--rollback` | bool | `false` | Run the rollback recovery without prompting (see [Deploy recovery actions](#deploy-recovery-actions)). |
| `--sync-config` | bool | `false` | In-pod only. Reconcile the on-disk env config with the helm-injected `ERUN_*` env vars (injected env wins): rewrite the projected keys (`type`, `kubernetescontext`, `cloudprovideralias`, `managedcloud`, cloud provider/context blocks, `idle`, `runtimeregistry`, `containerregistries`, `disablebuildscript`), preserving every unprojected key. Reports per-key drift as `missing` / `wrong` / `legacy`; under `--dry-run` the file writes are traced but not performed. Short-circuits the remote-init flow. |

### Check catalogue

Each check returns one of `ok`, `missing`, `error` (parse failure, permission denied), or `skip` (not applicable in this context).

#### Local-host checks (run when `ERUN_REPO_REMOTE` is not `true`)

| Check id | What it inspects | Recovery if missing |
|---|---|---|
| `config.tenant` | `~/.config/erun/<tenant>/tenant.yaml` exists and parses. | Suggests `erun init <tenant>`. |
| `config.environment` | `~/.config/erun/<tenant>/<env>/config.yaml` exists and parses. | Suggests `erun init <tenant> <env>`. |
| `config.project` | `<projectroot>/.erun/config.yaml` exists. | Suggests `erun init`. |
| `cluster.kube_context` | `EnvConfig.kubernetescontext` is in `~/.kube/config`. | Lists available contexts. |
| `cluster.runtime_pod` | A pod matching the runtime-chart's labels is `Running` in `<tenant>-<env>`. | Suggests `erun open`. |
| `workspace.project_root` | `<projectroot>` exists and is a git repo. | No automatic recovery. |

#### In-pod checks (run when `ERUN_REPO_REMOTE=true`)

| Check id | What it inspects | Recovery if missing |
|---|---|---|
| `bootstrap.marker` | `/home/erun/.erun/<tenant>/<env>/bootstrap.yaml` exists and parses. | Suggests re-running `erun init --remote` from the host. |
| `workspace.project_root` | The in-pod project root exists. | Offers to `git clone` from the marker's recorded remote. |
| `workspace.git_checkout` | The checkout's HEAD is on the marker's recorded branch. | Offers to `git checkout` the branch. |
| `ssh.keypair` | `~/.ssh/id_ed25519` and `.pub` exist. | Offers `ssh-keygen -t ed25519 -f ~/.ssh/id_ed25519 -N ''`. |
| `ssh.codecommit_key` | When the marker recorded a CodeCommit host: `~/.ssh/id_rsa` (RSA, not ed25519) is registered with the IAM user. | Offers to generate and upload via `aws iam upload-ssh-public-key`. |

### Deploy recovery actions {#deploy-recovery-actions}

After the read-only deploy diagnosis (helm release status + runtime pods), `doctor` can run two recovery actions that **mutate the live release**. They are **alternative** fixes for different failure modes, not additive steps — clearing a pending lock leaves the release at its last deployed revision, so a rollback run straight after would step back a further revision. `--clear-pending-helm` and `--rollback` are therefore mutually exclusive; passing both aborts with `--clear-pending-helm and --rollback are alternative recoveries; pass only one` (exit 1, nothing runs).

Gating: each action runs non-interactively with its flag. With **no flag**, `doctor` inspects the helm status and prompts for the **single recommended** action — `pending-install`/`pending-upgrade`/`pending-rollback` → clear pending; a present-but-unhealthy release (`failed`, `superseded`, …) → rollback; a healthy (`status: deployed`), missing (`not found`), or unreadable release → no destructive prompt at all. It never offers both at once. Under `--dry-run` the exact command is traced and nothing runs.

| Action | Flag | Command run | Use when |
|---|---|---|---|
| Clear pending helm release | `--clear-pending-helm` | `kubectl [--context <ctx>] --namespace <ns> delete secrets,configmaps -l 'owner=helm,name=<release>,status in (pending-install,pending-upgrade,pending-rollback)' --ignore-not-found` | A deploy died mid-upgrade and left the release locked in a pending state, so the next `erun deploy` refuses to start. |
| Roll back to last successful revision | `--rollback` | `helm rollback <release> --namespace <ns> [--kube-context <ctx>] --wait --timeout <deploy-wait>` | The current revision is bad or never converged and a previous revision was healthy. |

`<release>` is the runtime release name for the tenant; `<ns>` and `<ctx>` are the resolved env namespace and kube-context. To rebuild and roll out fresh images instead of recovering the existing release, re-run [`erun deploy --force`](/cli/deploy) — the desktop's failed-deploy card surfaces that as its **Force rebuild & redeploy** button.

Both actions are also exposed on the [MCP `doctor` tool](/mcp/overview#doctor) via the `clearPendingHelm` and `rollback` boolean inputs.

### Exit codes

| Code | Meaning |
|---|---|
| `0` | All checks `ok`, or every `missing` check was recovered. |
| `1` | At least one check `missing` and recovery declined (or `--dry-run`). |
| `2` | At least one check `error` (parse failure, permission denied). Inspect the trace to find which. |

---

## `erun outputs`

`erun outputs` lists and downloads files an agent produced in an environment's runtime pod outputs directory (`$ERUN_OUTPUTS_DIR`, default `/home/erun/.erun/outputs`). Both subcommands resolve the pod from tenant/environment scope and read it over `kubectl exec`; the MCP `outputs_list`/`outputs_download` tools cover the same operations for in-pod callers (which read the filesystem directly).

### `erun outputs list`

| Flag | Type | Default | Effect |
|---|---|---|---|
| `--tenant <t>` | string | current scope | Target tenant. |
| `--environment <e>` | string | current scope | Target environment; requires `--tenant`. |
| `--path <dir>` | absolute path | `$ERUN_OUTPUTS_DIR` → `/home/erun/.erun/outputs` | Pod directory to list. Must be absolute and free of `..`. |
| `--limit <n>` | int | `0` (all) | Cap on entries returned, newest-first. |

Lists one directory one level deep over `kubectl exec … find <dir> -maxdepth 1`, sorted newest-first by mtime. A missing directory yields an empty result, not an error. `--output json` emits `{dir, entries:[{name,path,size,modTime,isDir}], total, truncated}`.

### `erun outputs download`

| Flag | Type | Default | Effect |
|---|---|---|---|
| `<name>` (arg) | string | **required** | Entry to download, a single path segment under the directory. A name with directory components is reduced to its base segment; `.`/`..`/empty are rejected. |
| `--tenant` / `--environment` / `--path` | — | — | As for `list`. |
| `--dest <local-path>` | path | current directory | Local file or directory to write to. (`--dest`, not `--output`, which is the global mode flag.) |
| `--force` | bool | `false` | Overwrite an existing local destination. |

A file streams as base64; a folder streams as a `tar.gz` archive (saved as `<name>.tar.gz`). The payload is SHA-256'd and capped at 100 MB (`MaxRuntimeOutputBytes`) — a larger file errors before transfer. `--output json` emits `{name, dest, size, sha256, isArchive, archiveFormat}`. Both subcommands support `--dry-run` (traces the `kubectl exec` argv + script and the planned destination; no I/O).

---

## `erun release`

`erun release` orchestrates **build → push → git-tag**: it builds the release-tagged images, then reuses [`erun push`](#erun-push) to publish the multi-arch image manifest **and** the runtime chart at the release version, then creates the commit + tag. It has no chart-publishing step of its own. See [Release version policy](/agent-reference/release-policy) for the version-pattern rules and the publishing contract; the `erun release` flag set is just `--dry-run` and `--output`, and is documented on the [Operator page](/cli/release).

---

## `erun delete`

### Flags

| Flag | Type | Default | Effect |
|---|---|---|---|
| `-y` | bool | `false` | Skip the destructive-action confirmation prompt. |
| `--dry-run` | bool | `false` | Print what would be removed; perform no side effect. |

### What is removed

1. The Kubernetes namespace `<tenant>-<env>` (cascades to every Deployment, PVC, Service, ConfigMap, Secret inside).
2. The per-user env config directory `~/.config/erun/<tenant>/<env>/`.
3. If the deleted env was the tenant's `defaultenvironment`: clears the pointer (next `erun open` against the tenant prompts for a new default).

The local port-forward state files under `<UserConfigDir>/erun/portforward/{mcp,sshd,api}/<tenant>/<env>.json` are **not** removed; a later env with the same name overwrites them (see [Networking spec · Port-forward state files](/agent-reference/networking-spec#port-forward-state-files)).

### Error codes

| Code | Cause | Exit code |
|---|---|---|
| `NAMESPACE_NOT_FOUND` | The Kubernetes namespace does not exist. Treated as a successful no-op; exits `0`. | `0` |
| `KUBE_DELETE_FAILED` | Namespace deletion returned an error from the API server. Local config is **not** removed. | `2` |

---

## `erun mcp`

See [MCP overview](/mcp/overview) for the protocol and the tool list. The launcher flag set is:

| Flag | Type | Default | Effect |
|---|---|---|---|
| `--port <n>` | int | `EnvConfig.mcpport` (default `17000`). | The HTTP listener port. |
| `--host <addr>` | string | `127.0.0.1` | The bind address. The in-pod default is loopback-only. |

---

## See also

- [CLI overview](/cli/overview) — the Operator-facing summary.
- [Configuration](/reference/configuration) — where each persisted flag value lands.
- [Command primitives](/concepts/command-primitives) — the Operator-facing model of pure primitives, version-as-identity, and orchestration vs convenience switches.
- [Build path resolution](/reference/configuration-build-paths) — the algorithm `erun build` and `push` use to resolve scope and the version `build` mints.
- [Dry-run redaction](/agent-reference/dry-run-redaction) — what `--dry-run` rewrites in the trace.
- [Skills](/concepts/skills) — how Agents pick up project conventions for code generation (replaces the removed `erun add` command).

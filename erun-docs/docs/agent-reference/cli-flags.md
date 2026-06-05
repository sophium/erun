---
title: CLI flag spec
---

# CLI flag spec

> For the Operator-facing workflow per command, see the [CLI section](/cli/overview).

Every `erun` flag, by command. Type, default, validation, and where the resolved value persists. Operator-facing pages show only the flags an Operator types day-to-day; this page is the complete contract.

Common flags inherited from the root command apply to every subcommand:

| Flag | Type | Default | Effect |
|---|---|---|---|
| `--dry-run` | bool | `false` | Resolve and print the trace without performing side effects. Implies trace verbosity. |
| `-v` / `--verbose` | bool | `false` | Stream external tool output (`helm --debug`, `kubectl --v=4`, …). |
| `-vv` | bool | `false` | `-v` plus per-command `trace:` lines for every action + decision. |
| `--time` | bool | `false` | Print elapsed wall time at the end. |
| `--help` / `-h` | bool | `false` | Print command help and exit `0`. |

## `erun init`

### Common flags

See [`erun init`](/cli/init) — `--tenant`, `--environment`, `--kubernetes-context`, `--container-registry`, `--bootstrap`, `--set-default-tenant`, `-y` / `--yes`.

### Advanced flags

| Flag | Type | Default | Validation | Persists to |
|---|---|---|---|---|
| `--project-root <path>` | string (absolute path) | `<cwd>`'s git repo root (`git rev-parse --show-toplevel`) | Must be an existing directory; must contain a `.git/` directory or `.git` file. | `TenantConfig.projectroot`. |
| `--remote` | bool | `false` | — | Sets `EnvConfig.remote = true`. With `--remote`, init runs inside the runtime pod (writes the bootstrap marker). |
| `--no-git` | bool | `false` | Only meaningful with `--remote`. | Skips the in-pod `git clone` step. |
| `--version <version>` | string (semver) | The CLI's built-in `ERUN_VERSION`. | Must satisfy `^[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.-]+)?$`. | `EnvConfig.runtimeversion`. |
| `--runtime-image <repo>` | string | Built-in default (`ghcr.io/sophium/erun-devops`). | Must be a valid OCI image reference (host[/path]…[:tag] without the tag — the tag comes from `--version`). | `EnvConfig.runtimeregistry` resolution. |
| `--runtime-cpu <value>` | Kubernetes quantity | `4` | Must match the Kubernetes `Quantity` grammar (`m`, plain integer, decimal). | `EnvConfig.runtimepod.cpu`. |
| `--runtime-memory <value>` | Kubernetes quantity | `8916Mi` | Must match the Kubernetes `Quantity` grammar (`Ki`, `Mi`, `Gi`, …). | `EnvConfig.runtimepod.memory`. |
| `--codecommit-ssh-key-id <id>` | string (`APKA…` shape) | unset | Must start with `APKA`; must be a valid IAM key id (length 21). | Stored in the in-pod bootstrap marker (`bootstrap.yaml` → `codecommitSshKeyId`). |
| `--confirm-environment` | bool | `false` | — | Equivalent to `-y` for the env-overwrite confirmation only. |

### Side effects

`erun init` writes these files in this order:

1. `~/.config/erun/<tenant>/tenant.yaml` (creating `~/.config/erun/<tenant>/` if missing).
2. `~/.config/erun/<tenant>/<env>/config.yaml`.
3. `<projectroot>/.erun/config.yaml`. Existing values are preserved; new defaults are merged.
4. With `--bootstrap`: scaffolds `<projectroot>/<tenant>-devops/` (Dockerfile, build.sh, helm chart skeleton).
5. Helm-installs the runtime chart into the namespace `<tenant>-<environment>`.
6. With `--remote`: writes the in-pod marker at `/home/erun/.erun/<tenant>/<env>/bootstrap.yaml`.

### `erun init` lifecycle algorithm

1. Parse flags; resolve effective tenant + env (see [Configuration · Resolution order](/reference/configuration#effective-tenant--environment-for-a-cli-command)).
2. Validate `--kubernetes-context` against `~/.kube/config`. On miss, abort with the available context list.
3. Resolve `--project-root` (defaults to `git rev-parse --show-toplevel`). On miss, abort with `not in a git repository`.
4. If the tenant/env already exists, prompt unless `-y` / `--confirm-environment`. Aborting on `n` is the safe default.
5. With `--bootstrap`: render the `<tenant>-devops/` skeleton from the canonical template. Abort if any target file exists (no silent overwrite).
6. Render and `helm upgrade --install` the runtime chart into `<tenant>-<environment>`.
7. With `--remote`: open SSH and write the in-pod bootstrap marker.
8. Update default-tenant pointer if `--set-default-tenant`.
9. Exit `0`.

### Error codes

| Code | Cause | Exit code |
|---|---|---|
| `NOT_IN_GIT_REPO` | `--project-root` unset and cwd is not in a git repo. | `1` |
| `KUBE_CONTEXT_MISSING` | `--kubernetes-context` is not present in `~/.kube/config`. | `1` |
| `BOOTSTRAP_CONFLICT` | `<projectroot>/<tenant>-devops/` exists and `--bootstrap` was passed. | `1` |
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
| `--runtime-image <repo>` | string | `EnvConfig.runtimeregistry` resolution. | Same as `erun init --runtime-image`. | Run-only override. |
| `--snapshot` / `--no-snapshot` | tri-state bool (`true` / `false` / unset) | `EnvConfig.snapshot` (defaults to `nil`). | Only applies to agent envs. Ignored against runtime envs (warning logged). | `EnvConfig.snapshot` for this run. |

### `erun open` lifecycle algorithm

1. Parse flags; resolve effective tenant + env.
2. Load `EnvConfig` (Kubernetes context, container registry, runtime version, snapshot flag).
3. If `EnvConfig.cloudprovideralias` is set, look up the cloud context. If `stopped`, send the provider-specific start command. Poll the cluster API every `5s` until reachable or 5 minutes elapse (then abort `CLUSTER_UNREACHABLE`).
4. Render the runtime chart with the effective `EnvConfig` values; run `helm upgrade --install <env>-runtime <chart>` into `<tenant>-<env>`.
5. Wait for the runtime pod's SSH server to be reachable on the in-pod port (`EnvConfig.sshd.port`, default `22`). Readiness probe is a TCP connect + banner-line read, retried every `2s` with a `60s` cap.
6. Establish local port-forwards. The desktop's port allocator writes `<UserConfigDir>/erun/portforward/{mcp,ssh,api}/<tenant>/<env>.json` with `{localPort, podPort, pid}`.
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

### Common flags

`--deploy`, `--release`, `--force`, `--dry-run`.

### Advanced flags

| Flag | Type | Default | Validation | Notes |
|---|---|---|---|---|
| `--no-incremental` | bool | `false` | — | Disables the fingerprint cache. Every Docker context rebuilds. |
| `--version <version>` | string (semver) | Resolved per [Build path resolution · VERSION walking](/reference/configuration-build-paths). | Same as `erun init --version`. | Overrides the resolved VERSION for this build only. |

### `erun build` lifecycle algorithm

1. Parse flags; resolve effective tenant + env. Refuse with `BUILD_AGAINST_RUNTIME_ENV` if env type is `runtime`.
2. Resolve project root, build scope, Dockerfile, build context, VERSION per [Build path resolution](/reference/configuration-build-paths).
3. Compute the snapshot suffix `-snapshot-<UTC-timestamp>` and the per-image content fingerprint.
4. For each resolved image:
   a. If fingerprint matches the registry copy and `--no-incremental` / `--force` is not set: promote the registry copy locally; skip the build.
   b. Otherwise: invoke `docker buildx build --platform linux/amd64,linux/arm64 -t <registry>/<image>:<version>-snapshot-<ts> -f <Dockerfile> <context>` with the resolved `--build-arg` set.
   c. Tag the result `<registry>/<image>:fp-<fingerprint>-<arch>` for each architecture.
5. If `--deploy`: chain to `erun deploy`.
6. If `--release`: chain to `erun release` and push release-tagged variants.
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

### Common flags

`--force`, `--dry-run`.

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
| `NO_IMAGE_TO_PUSH` | Build context resolved but no built image was found locally. | `1` |
| `REGISTRY_AUTH_FAILED` | All retry attempts failed (or no TTY for the interactive login). | `2` |
| `MANIFEST_LIST_ASSEMBLY_FAILED` | Per-arch tags pushed but `docker manifest create` failed. | `2` |

---

## `erun deploy`

### Common flags

`--components`, `--version`, `--force`, `--dry-run`. Subcommand: `erun deploy <component>`.

### `--components` value set

The `--components` flag's accepted values are derived from `<tenant>-devops/k8s/<component>/Chart.yaml` discovery at command-resolve time. For the erun repository itself, the registered set is `{erun-backend-postgres, erun-backend-db, erun-backend-api}`. Each tenant project ships its own set.

### Deploy-plan resolution

The deploy plan comes from `ProjectConfig.environments.<env>.k8s.deployments[]` (see [Configuration · `environments.<env>.k8s.deployments[]`](/reference/configuration#per-project-config)). When the field is absent, `erun deploy` falls back to ordering by chart dependency declarations; on a tie, alphabetical by component name.

### Skip-helm semantics

`erun deploy` skips both `docker push` and `helm upgrade --install` for a component when:

1. Every image the chart references promoted from the fingerprint cache (no rebuild happened), and
2. The chart directory (`<tenant>-devops/k8s/<component>/`) has no diff against the last successful deploy's snapshot, and
3. `--force` is not set.

The skip emits `result: skipped (no change)` in the trace.

### Error codes

| Code | Cause | Exit code |
|---|---|---|
| `CLUSTER_UNREACHABLE` | Same as `erun open`. | `2` |
| `MISSING_IMAGE_IN_REGISTRY` | A chart references `<registry>/<component>:<version>` that does not exist. | `1` |
| `HELM_UPGRADE_FAILED` | A step in the plan failed; later steps are not executed. | `2` |
| `STALE_FINGERPRINT_CACHE` | Cache mismatch detected; build/push runs as if not cached. (Warning, not abort.) | `0` (with warning) |

---

## `erun doctor`

### Flags

| Flag | Type | Default | Effect |
|---|---|---|---|
| `--dry-run` | bool | `false` | Run the inspection; print the recovery plan; do not execute it. |
| `-y` | bool | `false` | Auto-approve every offered recovery action. |
| `--clear-pending-helm` | bool | `false` | Run the clear-pending-helm recovery without prompting (see [Deploy recovery actions](#deploy-recovery-actions)). |
| `--rollback` | bool | `false` | Run the rollback recovery without prompting (see [Deploy recovery actions](#deploy-recovery-actions)). |

### Check catalogue

Each check returns one of `ok`, `missing`, `error` (parse failure, permission denied), or `skip` (not applicable in this context).

#### Local-host checks (run when `ERUN_REPO_REMOTE` is not `true`)

| Check id | What it inspects | Recovery if missing |
|---|---|---|
| `config.tenant` | `~/.config/erun/<tenant>/tenant.yaml` exists and parses. | Suggests `erun init <tenant>`. |
| `config.environment` | `~/.config/erun/<tenant>/<env>/config.yaml` exists and parses. | Suggests `erun init <tenant> <env>`. |
| `config.project` | `<projectroot>/.erun/config.yaml` exists. | Suggests `erun init --bootstrap`. |
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

## `erun release`

See [Release version policy](/agent-reference/release-policy) for the version-pattern rules and the release-tag publishing contract; the `erun release` flag set is just `--dry-run` and is documented on the [Operator page](/cli/release).

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
3. The local port-forward marker files under `<UserConfigDir>/erun/portforward/{mcp,ssh,api}/<tenant>/<env>.json`.
4. If the deleted env was the tenant's `defaultenvironment`: clears the pointer (next `erun open` against the tenant prompts for a new default).

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
- [Build path resolution](/reference/configuration-build-paths) — the algorithm `erun build`, `push`, `deploy` use to resolve scope and version.
- [Dry-run redaction](/agent-reference/dry-run-redaction) — what `--dry-run` rewrites in the trace.
- [Skills](/concepts/skills) — how Agents pick up project conventions for code generation (replaces the removed `erun add` command).

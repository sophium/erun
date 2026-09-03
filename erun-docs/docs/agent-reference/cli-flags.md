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
| `--type <type>` | enum (`local-agent`, `remote-agent`, `runtime`, `host`) | unset. A **new** env then resolves to `local-agent` (or `remote-agent` when `--remote` is given); an **existing** env keeps `EnvConfig.type`. | Must be one of the four values. Conflicts with a `--remote` whose value disagrees (`host` also conflicts, since it resolves `RemoteWorktree() == false` the same as `local-agent`). | `EnvConfig.type`. On an existing env this is a [retype](#init-existing-env), permitted between any two types in either direction. `host` creates or retypes to an env with **no cluster contact at all** — see the host branch noted in the lifecycle algorithm below, which skips steps 2, 5, 6, 7, 8 entirely. |
| `--remote` | bool | `false` | Conflicts with a `--type` whose value disagrees (e.g. `--type=local-agent --remote`). | Deprecated alias for `--type=remote-agent`: sets `EnvConfig.type = remote-agent`. Init then writes the in-pod bootstrap marker. |
| `--no-git` | bool | `false` | Only meaningful with `--remote` / `--type=remote-agent`. | Skips the in-pod `git clone` step. |
| `--version <version>` | string (semver) | A **new** env takes the CLI's built-in `ERUN_VERSION`; an **existing** env keeps `EnvConfig.runtimeversion` (the built-in fills in only when the env records none). | Must satisfy `^[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.-]+)?$`. | `EnvConfig.runtimeversion`. The transport's own version is a fallback, not a request — an `init` about something else never repins a running env; move a version with [`erun deploy --version`](#erun-deploy). |
| `--runtime-image <ref>` | string | unset — deploy then [defaults the image](#deploy-runtime-image-default) from the chart (a `<tenant>-devops` umbrella → its own image; the shared `erun-devops` chart → no override). | A full OCI reference (registry path and/or tag present) is used verbatim; a bare name resolves to `<registry>/<name>:<runtime version>` at deploy time. | `EnvConfig.runtimeimage`; applied as `imageOverrides.erun-devops` on every published-chart deploy. |
| `--runtime-registry <host>` | string (registry host, optional org path) | unset — the [runtime chart search](#deploy-runtime-chart-search) then resolves ERun's artifacts from the env's `deploy`-marked registry, widening to the runtime image's registry when the chart is not there. | Recorded verbatim (trimmed); no scheme, no `charts/` suffix. | `EnvConfig.runtimeregistry`, which the chart search and the in-pod `RUNTIME_REGISTRY` projection both honour first. Init's own runtime deploy sees it in the same run, so it is also the recovery path for an env that cannot complete a deploy. It is the only writer that replaces the field: a deploy records the registry its chart search resolved at, but only when the field is empty or already agrees — a value set here survives a deploy that resolved elsewhere, which traces `deploy: the env's runtime registry <recorded> stands; the runtime chart resolved from <resolved> instead (…)` rather than overwriting it. |
| `--bootstrap` | bool | `false` | — | **Deprecated, ignored.** Prints a deprecation warning; `init` no longer scaffolds a `<tenant>-devops/` module — envs deploy the published `erun-devops` chart. |
| `--runtime-cpu <value>` | Kubernetes quantity | A **new** env takes `4`; an **existing** env keeps `EnvConfig.runtimepod.cpu`. | Must match the Kubernetes `Quantity` grammar (`m`, plain integer, decimal). | `EnvConfig.runtimepod.cpu`. Supplied alone it merges — naming only the CPU leaves the recorded memory where it was. |
| `--runtime-memory <value>` | Kubernetes quantity | A **new** env takes `8916Mi`; an **existing** env keeps `EnvConfig.runtimepod.memory`. | Must match the Kubernetes `Quantity` grammar (`Ki`, `Mi`, `Gi`, …). | `EnvConfig.runtimepod.memory`. Merges with `--runtime-cpu` the same way. |
| `--dind-cpu <value>` | Kubernetes quantity | A **new** env takes `4`; an **existing** env keeps `EnvConfig.runtimedindpod.cpu`. | Must match the Kubernetes `Quantity` grammar (`m`, plain integer, decimal). | `EnvConfig.runtimedindpod.cpu` — the `erun-dind` sidecar's own limit, independent of `--runtime-cpu`. Supplied alone it merges — naming only the CPU leaves the recorded memory where it was. |
| `--dind-memory <value>` | Kubernetes quantity | A **new** env takes `20Gi`; an **existing** env keeps `EnvConfig.runtimedindpod.memory`. | Must match the Kubernetes `Quantity` grammar (`Ki`, `Mi`, `Gi`, …). | `EnvConfig.runtimedindpod.memory`. Merges with `--dind-cpu` the same way. Raise this when a multi-arch `erun release`/`erun build --release` OOMs inside the sidecar — every image build runs there, not in the runtime container. |
| `--codecommit-ssh-key-id <id>` | string (`APKA…` shape) | unset | Must start with `APKA`; must be a valid IAM key id (length 21). | Stored in the in-pod bootstrap marker (`bootstrap.yaml` → `codecommitSshKeyId`). |
| `--confirm-environment` | bool | `false` | — | Equivalent to `-y` for the env-overwrite confirmation only. |
| `--platform-account` | bool | `false` | — | Makes the env a **cluster platform account**: `EnvConfig.platformaccount = true`, which threads `--set platformAccount=true` at deploy so the runtime chart binds the env's ServiceAccount to the built-in `cluster-admin` (a `<release>-platform` `ClusterRoleBinding`). Lets in-pod platform Terraform (the [cluster edge](/agent-reference/skills-spec#erun-enable-hosting-edge)) and component installs manage cluster-scoped resources. The first deploy that adds the binding must run from an admin-capable context (the API server's escalation check). |
| `--components <a,b,…>` | list of strings | unset — nothing changes. | Any string; not validated against a chart universe at init time (that check happens at deploy time). | `EnvConfig.deploy.components`, the [saved deploy selection](/reference/configuration#envconfig) — the same field `erun deploy --components` overrides per run but never persists. An explicit empty value (`--components ''`), distinct from omitting the flag, clears a saved selection and returns the env to its repo [`k8s.deployments`](#components-value-set) plan; there is no other command that resets it. |
| `--erun-registry` | bool | `false` | Conflicts with `--container-registry` and with `--cluster-registry` — pick one. | Seeds the env's registry list with a single static entry, `registry.erunpaas.com/<tenant>` (`eruncommon.HostedRegistryReference`), marked `build`+`deploy` — the same shape `--container-registry` produces, just pointed at erun's hosted registry instead of an operator-named host. `(Planned.)` end to end: the flag, `BootstrapInitParams.ErunRegistry`, and the resolved registry entry all work today, but `registry.erunpaas.com` itself is not yet a live, reachable registry (see [Container registries · Hosted registry](/deployment/registries#hosted-registry)) — the DNS, TLS certificate, and the platform's zot deployment are not cut over. The env's deploy registry then holds only this project's images, so pair it with `--runtime-registry ghcr.io/sophium` (see the row above) to keep resolving erun's own runtime chart. |

### Re-initializing an existing environment {#init-existing-env}

When the env config already exists, `init` reconciles it against the invocation instead of creating it. Two questions are answered separately, and conflating them is what made settings vanish before: **what type is this invocation asking for**, and **which settings did it supply**.

A setting is applied because it was supplied, not because of the type the invocation resolved to. Every field below describes the runtime pod, which an env of any type has, so `erun init <tenant> <env> --image-pull-secret X` lands on a `remote-agent` or `runtime` env without restating `--type`.

| Input | Supplied | Omitted |
|---|---|---|
| `--version` | Sets `EnvConfig.runtimeversion`. Trace: `init: runtime version set to <v>` (or `… already <v>`). | Keeps it. Trace: `init: runtime version not given; keeping <v>`. The transport's built-in version fills in **only** when the env records none, tracing `init: env records no runtime version; adopting <v>`. |
| `--runtime-image` | Sets `EnvConfig.runtimeimage` — **tagless**, even when the flag value carries a tag (a trailing `:<tag>`/digest is stripped before persisting). [Deploy resolution](/reference/configuration#advanced-image-overrides) already pins a tagless reference to the env's own runtime version on every deploy; persisting the tag is what leaves a stale pin behind after the env's version moves on, so init never records one. | Keeps it. Trace: `init: runtime image not given; keeping <ref>`. |
| `--runtime-registry` | Sets `EnvConfig.runtimeregistry`. | Keeps it. |
| `--image-pull-secret` | Replaces `EnvConfig.imagepullsecrets` with the trimmed, de-duplicated list. | Keeps the recorded list. |
| `--runtime-cpu` / `--runtime-memory` | Merges onto `EnvConfig.runtimepod`: the limit named is set, the other is kept. Trace: `init: runtime pod resources set to cpu=<c> memory=<m>`. | Keeps both. Trace: `init: runtime pod resources not given; keeping cpu=<c> memory=<m>`. |
| `--dind-cpu` / `--dind-memory` | Merges onto `EnvConfig.runtimedindpod`, independently of `--runtime-cpu`/`--runtime-memory`. Trace: `init: erun-dind sidecar resources set to cpu=<c> memory=<m>`. | Keeps both. Trace: `init: erun-dind sidecar resources not given; keeping cpu=<c> memory=<m>`. |
| `--type` / `--remote` | Retypes the env — see below. | **Never** retypes. Trace: `init: --type not given; keeping env type "<t>"`. |
| `--components` | Replaces `EnvConfig.deploy.components` outright, including with an empty list when the value is explicitly empty (`--components ''`) — that clears a saved selection and returns deploy to the repo plan. Trace: `init: deploy components set to <a,b,…>` (or `… (cleared — deploy now follows the repo k8s.deployments plan)`). | Keeps the recorded selection. Trace: `init: deploy components not given; keeping <a,b,…>` (silent when there was none). |

The `--type` default is the asymmetry that matters: a new env with no `--type` resolves to `local-agent`, but that fallback is a default, not a request, so an existing env is not moved by it.

**Retyping.** A named `--type` that differs from `EnvConfig.type` changes it, in either direction and between any two of the four types — including `runtime` → `remote-agent`, which is what makes a runtime env orchestratable by the desktop. Trace: `init: env type "<from>" -> "<to>"` (or `init: env type already "<t>"` when they match). The rest of the run then does the work the named type implies: retyping to `remote-agent` or `runtime` runs the same runtime deploy and in-pod checkout a fresh `--type=<t>` init would; retyping to `host` runs no deploy at all and skips the cluster/cloud reconcile in [Side effects](#side-effects) below entirely.

Retyping **to** `local-agent` or `host` is the one case that needs more than the field: a `local-agent` worktree is hostPath-mounted, and a `host` env *is* the directory with no pod to mount it into — either way, the path a remote/runtime env carries in `EnvConfig.localRepoPath` names an in-pod directory or is empty, not a usable host path. The retype re-resolves the host project root (`--project-root`, else the cwd's git root) and records it (`init: local repo path set to <path>`); when neither answers, it fails and writes nothing — `LOCAL_AGENT_RETYPE_NEEDS_REPO_PATH` for `--type=local-agent`, `HOST_RETYPE_NEEDS_REPO_PATH` for `--type=host` (same cause, worded for what the type actually does with the path — "mount" vs "use").

Settings are reconciled before `init`'s own runtime deploy, so that deploy carries them: a re-init that adds `--image-pull-secret` deploys with the secret, and one that omits `--runtime-cpu` deploys at the env's recorded limits rather than the defaults.

An env created by this same run skips the reconcile entirely — it was written from these params moments ago, so there is nothing to reconcile and no trace lines are emitted.

### Side effects

`erun init` writes these files in this order:

1. `~/.config/erun/<tenant>/tenant.yaml` (creating `~/.config/erun/<tenant>/` if missing).
2. `~/.config/erun/<tenant>/<env>/config.yaml`.
3. `<projectroot>/.erun/config.yaml`. Existing values are preserved; new defaults are merged.
4. Helm-installs the runtime chart into the namespace `<tenant>-<environment>` — the repo-local chart when the project has one, otherwise the published `oci://<registry>/charts/erun-devops` chart pinned to the runtime version (see [`erun deploy`](/cli/deploy#where-the-runtime-chart-comes-from)).
5. With `--remote`: writes the in-pod marker at `/home/erun/.erun/<tenant>/<env>/bootstrap.yaml`.

### `erun init` lifecycle algorithm

1. Parse flags; resolve effective tenant + env (see [Configuration · Resolution order](/reference/configuration#effective-tenant--environment-for-a-cli-command)).
2. Validate `--kubernetes-context` against `~/.kube/config`. On miss, abort with the available context list. **Skipped for `--type=host`** — a host env never resolves a kubernetes context at all, at this or any other step; that is the whole point of the type.
3. Resolve `--project-root` (defaults to `git rev-parse --show-toplevel`). On miss, abort with `not in a git repository` (`local-agent`, `host`) or proceed sourceless (`remote-agent`, `runtime`, whose worktree is not a host path).
4. If the tenant/env already exists, prompt unless `-y` / `--confirm-environment`. Aborting on `n` is the safe default. Runs for `--type=host` too — the confirmation is local and interactive, not cluster-touching.
5. With `--remote`/`--type=remote-agent` (and `--type=runtime`): resolve a ghcr.io credential from the machine running `init` itself (a docker config entry, a gh session, or `GH_TOKEN`/`GITHUB_TOKEN`) for every registry the env is configured to build to or deploy from. When one resolves, mint (or refresh) a `kubernetes.io/dockerconfigjson` Secret named `<tenant>-devops-registry-credential` via `kubectl apply -f -` and persist its name to `EnvConfig.registrycredentialsecretname`, so step 6's chart install mounts it. Resolves to nothing (no error) when the host itself has no credential to give. **Never runs for `--type=host`.**
6. Resolve the runtime chart — repo-local when the project carries one, the published `oci://<registry>/charts/erun-devops` otherwise — and `helm upgrade --install` it into `<tenant>-<environment>`, threading `registryCredentialSecretName` when step 5 minted one. **Never runs for `--type=host`** — it has no pod for any chart to render into, so it records `name`, `type`, and `localRepoPath` and stops.
7. With `--remote`/`--type=remote-agent` (and `--type=runtime`): verify the pod can authenticate to any ghcr.io registry it is configured to build to or deploy from — a docker config entry, a gh session, or `GH_TOKEN`/`GITHUB_TOKEN`, checked directly in the pod. The Secret step 5 minted is what usually makes this resolve on a freshly created environment; abort if none resolves regardless — the pod is left deployed (init is safe to re-run once authenticated). **Never runs for `--type=host`.**
8. With `--remote`: open SSH and write the in-pod bootstrap marker. **Never runs for `--type=host`.**
9. Update default-tenant pointer if `--set-default-tenant`.
10. Exit `0`.

### Error codes

| Code | Cause | Exit code |
|---|---|---|
| `NOT_IN_GIT_REPO` | `--project-root` unset and cwd is not in a git repo. | `1` |
| `LOCAL_AGENT_RETYPE_NEEDS_REPO_PATH` | `--type=local-agent` on an existing env, with no `--project-root` and no git repo at the cwd, so there is no host path to mount as the worktree. Nothing is written. Message: ``cannot change <tenant>/<env> to type local-agent: it needs a host repo path to mount — run init from the project directory or pass --project-root``. | `1` |
| `HOST_RETYPE_NEEDS_REPO_PATH` | `--type=host` on a new or existing env, with no `--project-root` and no git repo at the cwd, so there is no directory to record. Nothing is written. Message (existing env): ``cannot change <tenant>/<env> to type host: it needs a host directory to use — run init from the project directory or pass --project-root``; a new env's message says "cannot create" in place of "cannot change". | `1` |
| `KUBE_CONTEXT_MISSING` | `--kubernetes-context` is not present in `~/.kube/config`. | `1` |
| `HELM_INSTALL_FAILED` | Runtime chart install failed; the per-user config is written but the in-pod marker is not. | `2` |
| `REGISTRY_UNREACHABLE` | `--container-registry` is set but DNS/network failed. (Warning, not abort.) | `0` (with warning) |
| `REGISTRY_CREDENTIAL_MISSING` | The pod init just deployed has no ghcr.io credential for a registry it is configured to build to or deploy from (no docker config entry, no gh session, no `GH_TOKEN`/`GITHUB_TOKEN`), and the machine running `init` had none to provision either. The pod is left deployed; authenticate it (`erun open`) and re-run `erun init` to confirm. | `1` |

---

## `erun open`

### Common flags

`--tenant`, `--environment`, `--no-shell`, `--deploy`, `--vscode`, `--intellij`, `--reconnect`.

### Advanced flags

| Flag | Type | Default | Validation | Persists to |
|---|---|---|---|---|
| `--deploy` | bool | `false` | Operator-convenience switch. | None. |
| `--reconnect` | bool | `false` | Declares the run a machine-initiated reattach rather than an Operator open. Suppresses everything in `open` that *starts* something: the cloud-context start in step 3, and both halves of the wake in step 5 (the `EnvConfig.stopped` clear and the scale-to-one). A stopped runtime aborts with `RUNTIME_STOPPED`. Composes with every other flag. | None — and, by design, prevents the `EnvConfig.stopped` write a plain `open` performs. |
| `--no-alias-prompt` | bool | `false` | Only meaningful with `--no-shell`. | None (interactive choice only). |
| `--version <version>` | string (semver) | `EnvConfig.runtimeversion` or the CLI built-in. | Same as `erun init --version`. Implies `--deploy` (pinning a version is only meaningful if it rolls out). | `EnvConfig.runtimeversion` for this run only (not persisted). |
| `--runtime-image <ref>` | string | `EnvConfig.runtimeimage` (unset → the published image). | Same reference rules as `erun init --runtime-image`. Applies only to envs deploying the published chart (rides in as `imageOverrides.erun-devops`); envs with a repo-local chart ignore it. Implies `--deploy`. | Run-only override (not persisted). |

`erun open` is a pure primitive: it verifies the runtime is **already deployed**, best-effort port-forwards SSH/MCP/API for laptop-side tooling, and attaches a shell to the in-pod session. By default it does **not** build, push, mint a version, or deploy — there is no build branch on env `type`, and no `helm upgrade`. The retired `--snapshot`/`--no-snapshot` pair has no replacement flag. Rolling out a version is the caller's job: the desktop app composes [`build`](#erun-build) → [`push`](#erun-push) → [`deploy`](#erun-deploy) around the open, threading the version it captured from `build --output json`. `--deploy` is the **operator-convenience switch** that deploys before opening (builds-here envs build → push → deploy; runtime/remote envs install the recorded/`--version` published chart by reference); a `--version`/`--runtime-image` override implies it. Programmatic callers never use `--deploy` — they compose the primitives themselves.

### `erun open` lifecycle algorithm

1. Parse flags; resolve effective tenant + env. `--version`/`--runtime-image` set the effective deploy flag.
2. Load `EnvConfig` (Kubernetes context, container registry, runtime version, type). By default `open` neither builds, pushes, nor deploys; it expects the runtime to already exist.
3. If `EnvConfig.cloudprovideralias` is set, look up the cloud context. If `stopped`, send the provider-specific start command. Poll the cluster API every `5s` until reachable or 5 minutes elapse (then abort `CLUSTER_UNREACHABLE`). **`--reconnect` skips this step entirely** — the same reason [`erun stop`](#erun-stop) skips it: starting the machine an Operator (or an idle policy) just stopped is not a decision a reattach gets to make. The reconnect then fails against the unreachable cluster, which is the honest outcome. For an already-running context the step is a no-op either way.
4. **If `--deploy` (or an implied deploy):** deploy the runtime first — a builds-here env composes build → push → deploy; a runtime/remote env runs `helm upgrade --install <env>-runtime <chart>` into `<tenant>-<env>` for the recorded/`--version` published chart by reference (requires a resolvable version, else `RUNTIME_VERSION_REQUIRED`). Default (no `--deploy`): a trace line records that `open` is a pure primitive that is not deploying, then `open` verifies the runtime deployment exists in `<tenant>-<env>` and aborts with `RUNTIME_NOT_DEPLOYED` if it does not. This deployment-presence check is the authoritative "is the runtime up" signal — reachability is **not** inferred from a later port-forward timeout, so a forward that merely can't bind is never misreported as an undeployed environment.
5. **Wake the runtime if it is stopped.** Read the runtime Deployment's `spec.replicas`. If it is `0`, `kubectl scale deployment/<tenant>-devops --replicas=1`, then `kubectl wait --for=condition=Available --timeout 2m0s`. This runs before any port-forward because `kubectl port-forward deployment/…` cannot attach to zero replicas. A Deployment already asking for `>= 1` replica gets no scale call. A Deployment that is absent, or a replica count that cannot be read, is treated as running and the open proceeds — the wake must never be more fragile than the open it precedes. Independently, if `EnvConfig.stopped` is set it is cleared **before** step 4, so a `--deploy` run renders `replicas: 1` instead of re-applying the recorded stop.

   **`--reconnect` inverts this step.** Waking is what an Operator opening the environment means; it is not what a supervisor re-establishing a dropped session means, and the two are indistinguishable from inside `open` unless the caller says which it is. A `--reconnect` run therefore makes no scale call and no `EnvConfig.stopped` clear at all: a stopped Deployment aborts with `RUNTIME_STOPPED` and a running one proceeds through the rest of the algorithm unchanged. This is what makes a stop durable against reconnects — [`erun stop`](#erun-stop) drops every attached session, so a reconnect that woke would undo the stop that caused it, erasing the recorded intent on the way, on a loop for as long as anything is attached.
6. **Refresh the host AWS credentials** if `EnvConfig.cloudprovideralias` names an AWS alias — the same write [`erun cloud refresh`](#erun-cloud-refresh) performs. This runs after the wake because the credentials are streamed into the running pod: a stopped environment has nothing to write to. **Best-effort**: a failure (usually a lapsed SSO session) is traced as a warning and `open` continues, so the environment keeps whatever credentials it already had and the session still opens.
7. Wait for the runtime pod's SSH server to be reachable on the in-pod port (`EnvConfig.sshd.port`, default `22`). Readiness probe is a TCP connect + banner-line read, retried every `2s` with a `60s` cap.
8. Establish local port-forwards (**best-effort**). `erun open` starts a detached `kubectl port-forward` per channel (MCP, SSH, API) and records each at `<UserConfigDir>/erun/portforward/{mcp,sshd,api}/<tenant>/<env>.json` with `{tenant, environment, kubernetesContext, namespace, localPort, logPath, processId}` — see [Networking spec · Port-forward state files](/agent-reference/networking-spec#port-forward-state-files). These forwards back **laptop-side tooling only** (the desktop app's panels, `erun api`, `erun mcp`); the shell/AI session itself runs in-pod via `kubectl exec` and does not use them, so a forward that cannot bind is logged as a warning and skipped rather than aborting `open`.
9. Attach a terminal (default), print kubectl/cwd switching commands (`--no-shell`), or launch the IDE (`--vscode`/`--intellij`).
10. Exit `0` when the terminal exits.

### Error codes

| Code | Cause | Exit code |
|---|---|---|
| `TENANT_NOT_CONFIGURED` | Resolved tenant has no `~/.config/erun/<tenant>/tenant.yaml`. | `1` |
| `HOST_ENV_NO_SHELL` | The environment is a [host env](/concepts/environment-types#host) — no pod and no cluster to open a kubectl-exec shell into. Checked before every other step (before `KUBE_CONTEXT_MISSING`, before any port-forward). Message names the worktree directory to open directly instead. | `1` |
| `KUBE_CONTEXT_MISSING` | `EnvConfig.kubernetescontext` is absent from `~/.kube/config`. | `1` |
| `CLUSTER_UNREACHABLE` | Cluster API does not respond after 5 minutes. | `2` |
| `CLOUD_START_FAILED` | Cloud-provider start command returned an error or the context entered a terminal failure state. | `2` |
| `RUNTIME_VERSION_REQUIRED` | `--deploy` for an env with no local chart and no resolvable runtime version (none recorded, none passed via `--version`). Run `erun deploy --version <v>` or persist a version. | `1` |
| `HELM_UPGRADE_FAILED` | `helm upgrade --install` returned non-zero (only on the `--deploy` path). The release is in helm's failure state; consult `helm history`. | `2` |
| `RUNTIME_NOT_DEPLOYED` | Pure `open` (no `--deploy`) but the runtime deployment is absent in `<tenant>-<env>`. Detected before the port-forwards so the message is actionable: run `erun deploy` or `erun open --deploy`. | `1` |
| `RUNTIME_STOPPED` | `--reconnect` against a Deployment scaled to zero. Deliberate: a reattach does not start an environment. Nothing is scaled and `EnvConfig.stopped` is left set; the message names the plain `erun open <tenant> <env>` that starts it. | `1` |
| `SSH_READY_TIMEOUT` | The runtime is deployed but its SSH server did not become reachable within the `60s` readiness window. (A genuinely undeployed runtime is caught earlier as `RUNTIME_NOT_DEPLOYED`.) | `2` |
| `IDE_LAUNCHER_MISSING` | `--vscode` / `--intellij` requested but the launcher binary isn't on `PATH`. Falls back to printing SSH details; exit `0`. | `0` |

---

## `erun build`

`erun build` is the **version-minting** primitive: it builds the images and stamps the version that `push`/`deploy` later consume. By default it mints a snapshot (`<base>-snapshot-<UTC-timestamp>`); `--release`, an explicit `--version`, or a version carried by the build directory pins the bare version instead. `build` never decides snapshot-vs-stable from the environment type.

### Common flags

`--deploy`, `--release`, `--force`, `--dry-run`, `--output`.

`--jobs`/`-j` sets how many images build at once: `0` (default) resolves a conservative degree from the host, `1` is strictly sequential, `N` is explicit. `ERUN_BUILD_JOBS` sets the same value by environment, and is the deterministic seam for tests — pin it rather than inheriting the runner's core count.

Scheduling honours the `FROM` graph: independent images share a **wave**, and an image that `FROM`s a sibling waits for it. With more than one worker the wave plan is emitted as a trace line before any build, followed by every image's decision lines in dependency order, then the builds themselves with each image's output buffered and flushed in wave order — so output is deterministic at any degree and the dry-run contract is unaffected. At `--jobs 1` the decision lines stay interleaved with each image's own output, exactly as before. `push`, `release`, and `build --deploy` are always sequential. A `FROM` cycle fails with an error naming the images rather than deadlocking.

`--deploy` and `--release` are **operator-convenience switches** that compose downstream primitives (`--deploy` → push + deploy; `--release` → the release flow). Programmatic callers do not use them: they run `erun build --output json`, capture `version`, and call `push`/`deploy` themselves. See [Structured output](#structured-output-flag).

### Advanced flags

| Flag | Type | Default | Validation | Notes |
|---|---|---|---|---|
| `--no-incremental` | bool | `false` | — | Disables the fingerprint cache. Every Docker context rebuilds. |
| `--version <version>` | string (semver) | Resolved per [Build path resolution · VERSION walking](/reference/configuration-build-paths). | Same as `erun init --version`. Conflicts with `--release` (which resolves the version itself). | Pins a bare version for this build instead of minting a snapshot. |
| `--platform <platform>` | string[] (repeatable) | Resolved per [Multi-architecture build contract](/agent-reference/conventions-spec#multi-architecture-build-contract). | Rejected together with `--release` (`release build cannot be combined with an explicit --platform override: a release always publishes every platform erun supports`). | Overrides the docker `--platform` targets for this build/push, e.g. `linux/amd64`. Absent, falls back to the project's configured `environments.<env>.docker.platforms`, then the default multi-arch pair. |
| `--component <name>` | string | Auto-selects the lone [`components:`](/reference/configuration#components-block) entry when the project declares exactly one; empty otherwise. | Must name a declared `components:` entry when the project declares any. Fails naming the declared choices when omitted and more than one entry is declared. | Selects which `components:` root (`docker`/`dockercontext`/`version`) this build resolves, for a monorepo of independent deployables that do not share one `docker`/`k8s` root. Unused (falls through to `paths:`/convention) when the project declares no `components:` map. |

### `--output json` result

`erun build --output json` prints the [structured output](#structured-output-flag) object: `{version, baseVersion, images}`. `version` is the minted content identity an orchestrator threads into `push`/`deploy`.

### `erun build` lifecycle algorithm

1. Parse flags; resolve effective tenant + env. Refuse with `BUILD_AGAINST_RUNTIME_ENV` if env type is `runtime` (a runtime env has no source to build).
2. Resolve project root, build scope, Dockerfile, build context, VERSION per [Build path resolution](/reference/configuration-build-paths).
3. **Mint the version.** Default: append the snapshot suffix `-snapshot-<UTC-timestamp>` to the resolved base. With `--release` / `--version` / a build-dir version: use the bare version. Compute the per-image content fingerprint.
4. For each resolved image:
   a. If fingerprint matches the registry copy and `--no-incremental` / `--force` is not set: promote the registry copy locally; skip the build.
   b. Otherwise: for each platform in the [resolved platform list](/agent-reference/conventions-spec#multi-architecture-build-contract) (both by default), invoke `docker build --platform <platform> -t <registry>/<image>:<version>-<arch> -f <Dockerfile> <context>` with the resolved `--build-arg` set.
   c. Tag the result `<registry>/<image>:fp-<fingerprint>-<arch>` for each targeted architecture.
   d. **`ERUN_VERSION` for a base built by this same run.** Images are ordered so a `FROM <registry>/<base>:${ERUN_VERSION}` wrapper builds after its base. A build that does not push tags only per-arch (`…:<version>-<arch>`) — the arch-less `…:<version>` is a manifest list [`push`](#erun-push) mints in the registry, so it exists neither locally nor remotely for an unpublished version. So when the wrapper's base is one of this run's own unpublished images, its `ERUN_VERSION` build arg is `<version>-<arch>` for the architecture being built, resolving the base from the local daemon at the matching arch (an arch-less local tag would be last-arch-wins, and therefore single-arch). This is what lets `erun build --version <v>` validate a whole release locally, dependent images included, before any git ref moves. A local snapshot base follows the same rule at `<base>-snapshot-<arch>`. A `--release` wrapper keeps the plain `<version>`: its base is pushed earlier in the same run, so it resolves from the published multi-arch manifest. No plain-`<version>` local tag is ever created, so a local build can never be mistaken for a published manifest or pushed in place of one.
5. Emit the minted version (and, with `--output json`, the structured result).
6. If `--deploy`: compose push + deploy at the minted version (operator-convenience shortcut). If `--release`: run the `erun release` flow, which builds and reuses `push` to publish the release-tagged variants and chart, verifies they resolve, and only then moves the git refs. Programmatic callers skip both and orchestrate the primitives themselves.
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
| `COMPONENT_NOT_DECLARED` | `--component <name>` names an entry the project's [`components:`](/reference/configuration#components-block) map does not declare (or the project declares no `components:` map at all). | `1` |
| `AMBIGUOUS_COMPONENT_SELECTION` | No `--component` and the project declares more than one `components:` entry. | `1` |

---

## `erun push`

### Version (required, unless `--build`)

| Flag | Type | Required | Notes |
|---|---|---|---|
| `--version <version>` | string (semver, snapshot or bare) | **Yes**, unless `--build` is set | The version to publish (the same flag `deploy` uses, for consistency across commands). `push` does not mint a version — it builds each image from source at this version (promoting unchanged images from the fingerprint cache), pushes the per-arch tags, assembles the multi-arch manifest list, then publishes each component's helm chart. Missing (and no `--build`) → `NO_VERSION` (exit 1). |
| `--build` | bool (default `false`) | — | **Operator-only convenience switch** (CLI top-level `erun push` only). Builds the current source first — the same pure build `erun build` runs, minting a snapshot version — then pushes that exact minted version. Equivalent to `erun build && erun push --version <minted>`. Mutually exclusive with `--version` (the version is whatever build mints); passing both → exit 1, `push --build builds and pushes the version it mints; do not also pass --version`. `--force` propagates to the build step. **Not exposed over MCP**: the `push` tool keeps `version` required, because programmatic callers compose `build` → `push` themselves and thread the minted version (see [Command primitives](/concepts/command-primitives)). |

### What push publishes

For the supplied `--version`, `push` always builds each image from its source context (never a prebuilt bare tag), pushes per-arch tags, assembles the manifest list, then publishes every Helm chart discovered under the project's `k8s/*` directories — a **directory scan**, not a lookup keyed to same-named images, so image-less charts (a tenant's own `frs-backend-api`, `frs-powerdns`, … wrappers) publish too. For each: `helm dependency build` (umbrella charts that vendor published subcharts) + `helm package` + `helm push` to `oci://<registry>/charts` at `--version`, verified with a `helm pull` round-trip. Chart publishing is decoupled from the image push: a **version-pinned base** (`erun-powerdns`, `erun-backend-postgres`, `erun-zitadel`, `erun-zitadel-login`) keeps its image at the upstream pin and is not re-pushed at `--version`, but its chart still publishes at `--version` so platform deploys resolve it. [`erun build`](#erun-build) packages the same charts locally (validate + `--output json`) without publishing. Charts publish under `/charts`, separate from the same-named image repo so a chart never collides with its image at the same ref. There is no environment-type branch. [`erun release`](#erun-release) reuses this step for all its publishing.

### Chart verification retry semantics

The `helm pull` round-trip reads back an artifact `push` itself just wrote, so a registry that has not finished propagating the new tag is a race, not a verdict — GHCR in particular mints the pull token before the tag is listed and answers the first fetch `403: denied`. Verification therefore retries up to **4 attempts** with a linear backoff of **500ms, 1s, 1.5s** (≈3s worst case) when the failed read's output matches a transient class, case-insensitive:

| Class | Matched substrings |
|---|---|
| Authorization / propagation | `401`, `403`, `404`, `denied`, `unauthorized`, `not found`, `manifest unknown` |
| Transport | `timeout`, `timed out`, `temporary failure`, `connection reset`, `connection refused`, `eof`, `no such host`, `tls handshake` |
| Server-side | `service unavailable`, `too many requests`, `500 `, `502`, `503` |

Any other failure is treated as final and fails on the first attempt. A read that never succeeds still fails the push after the last attempt — the retry bounds a race, it does not swallow a persistent failure.

### Common flags

`--force`, `--dry-run`, `--output`, `--component` (same selector and validation as [`erun build`'s `--component`](#erun-build) — see [`components:`](/reference/configuration#components-block)).

### Upfront registry-credential check

Before building anything, `erun push` (and `erun release`, which reuses `push`'s publish stage) checks whether any credential resolves for a ghcr.io registry it would push to — a docker config entry, a gh session, or `GH_TOKEN`/`GITHUB_TOKEN`. GHCR never accepts an anonymous push, so no credential at all is a certain failure, not an ambiguous one; refusing here turns a multi-arch build spent for nothing into an immediate, actionable error naming the missing credential and the `gh auth login`/`docker login` commands to fix it.

### Authentication retry semantics

When `docker push` returns one of these registry-side error strings, `erun push` retries automatically:

| Registry response (substring match, case-insensitive) | Retry |
|---|---|
| `unauthorized` | Re-runs `docker login <registry>` interactively (TTY required). |
| `denied` | Same. |
| `insufficient_scope` | Same. |
| `does not match expected scopes` (GHCR-specific) | Invokes `gh auth refresh -s write:packages,read:packages` and retries. Requires an interactive browser login (see gating below). |
| `permission_denied` (GHCR-specific) | Same as above. |

If no TTY is attached, the generic `docker login` retry skips the login prompt and surfaces the original error.

The GHCR scope refresh has a stricter gate: it drives `gh`'s interactive browser device-code flow, so `erun push` never launches it when there is no browser or no operator at the prompt. It is skipped when **either**:

- the process runs inside the chart-injected runtime pod (`ERUN_TENANT` and `ERUN_ENVIRONMENT` set) — headless, no browser, even though the desktop terminal is a PTY-backed pod shell; or
- `stdin` is not an interactive terminal (MCP, CI, pipes).

When the refresh is skipped, `erun push` does **not** hang on a device-code prompt. It fails with an actionable error naming the missing `write:packages` scope and the exact commands to run from a host shell with a browser:

```
gh auth refresh -h github.com -u <owner> -s write:packages,read:packages
gh auth token -u <owner> -h github.com | docker login ghcr.io -u <owner> --password-stdin
```

### Error codes

| Code | Cause | Exit code |
|---|---|---|
| `NO_VERSION` | No `<version>` argument. `push` publishes a specific version; it does not mint one. | `1` |
| `NO_BUILDABLE_CONTEXT` | No `<tenant>-devops/docker/<image>/` build context found to build the version from. | `1` |
| `REGISTRY_CREDENTIAL_MISSING` | No credential resolves for a ghcr.io registry to push to at all (no docker config entry, no gh session, no `GH_TOKEN`/`GITHUB_TOKEN`). Refused before any build. | `1` |
| `REGISTRY_AUTH_FAILED` | All retry attempts failed (or no TTY for the interactive login). | `2` |
| `MANIFEST_LIST_ASSEMBLY_FAILED` | Per-arch tags pushed but `docker manifest create` failed. | `2` |
| `CHART_PUSH_FAILED` | Images pushed but a chart's `helm push`, or its `helm pull` verification after every retry, failed — the version is not yet deployable. Charts publish one at a time, so the error names the split explicitly (`published:` / `failed:` / `not attempted:`) and states the recovery: re-run `erun push --version <version>`, which republishes idempotently. | `2` |
| `COMPONENT_NOT_DECLARED` | `--component <name>` names an entry the project's `components:` map does not declare. | `1` |
| `AMBIGUOUS_COMPONENT_SELECTION` | No `--component` and the project declares more than one `components:` entry. | `1` |

---

## `erun deploy`

`erun deploy` is a **pure consume** primitive: it helm-installs an already-published version by reference. It never builds, pushes, or publishes — a version is required input, not something it mints.

### Common flags

`--version`, `--runtime-image`, `--runtime-chart`, `--current`, `--components`, `--force`, `--rollout-timeout`, `--mcp-auth-public-key`, `--no-mcp-auth`, `--dry-run`, `--output`. Subcommand: `erun deploy <component>`.

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

### Subchart value forwarding for wrapped umbrellas {#deploy-subchart-forwarding}

A tenant that publishes its own artifacts ships **umbrella** charts — the runtime `<tenant>-devops` and each `<tenant>-<component>` — that wrap the canonical `erun-<base>` chart as a subchart (dependency name `erun-<base>`, no alias; the `erun-build-env` / `erun-blueprint-platform` pattern). helm does **not** pass top-level `--set` values into subchart scope, so a by-reference deploy of such a chart would leave the wrapped subchart's `{{ required }}` `tenant`/`environment` unset (`tenant is required` at render). Deploy closes that gap for any chart it installs by reference whose name is tenant-prefixed (not the canonical `erun-<base>`):

1. **Re-scopes the threaded `--set`s** under the subchart key `erun-<base>` (`--set-string erun-backend-api.tenant=<t>`, …), so every value erun resolves at deploy time — `tenant`/`environment`, ports, cloud context, MCP auth, `imageOverrides`, registry — reaches the wrapped subchart exactly as it would a chart installed directly. A canonical `erun-<base>` chart installed directly (the `erun` product tenant, or an explicitly selected `erun-*` chart) is **not** re-scoped — its top-level `--set`s already reach it.
2. **Applies the chart's bundled `values.<env>.yaml`.** Before the rollout, deploy runs `helm pull <ref> --version <v> --untar --untardir <tmp>` and adds `-f <tmp>/<chart>/values.<env>.yaml`, forwarding the tenant's own authored per-env subchart values (pod-shape: `extraContainers`/`extraVolumes`/`extraEnv`/`extraRules`, and any overrides authored under the subchart key). This is the by-reference analogue of a worktree deploy's local `values.<env>.yaml`. The file is `-f`'d **before** any config-dir overlay (`~/.config/erun/<tenant>/<env>/values.yaml`), and the re-scoped `--set`s win over both — so erun-resolved values are authoritative and a key authored in the bundled file that erun also threads (e.g. `api.oidcAllowedIssuers`) is owned by erun, not the file.

The dry-run trace shows the `helm pull … --untar` line before the `helm upgrade` line; the temp dir is removed after the rollout. Local (worktree) deploys are unchanged: a local runtime umbrella re-scopes via its Chart.yaml `erun-devops` dependency and `-f`s its worktree `values.<env>.yaml`; a local component umbrella `-f`s its worktree `values.<env>.yaml` (which is why authoring the nested subchart values there is still required for the worktree path).

### Runtime chart search order {#deploy-runtime-chart-search}

An env with no repo-local runtime chart and no stated chart ([`--runtime-chart`](#deploy-runtime-chart) / `EnvConfig.runtimechart`) resolves the chart by probing coordinates in order. Let `R` be the chart registry — `EnvConfig.runtimeregistry` when set, else the runtime image's registry for a `--cluster-registry` env, else the env's `deploy`-marked registry, else the project's configured registry, else `ghcr.io/sophium` — and `P` the platform registry, the registry prefix of `EnvConfig.runtimeimage` when it names one, else `ghcr.io/sophium`.

| # | Coordinate | Probed when | Trace on a miss |
|---|---|---|---|
| 1 | `oci://R/charts/<tenant>-devops` | `RuntimeReleaseName(tenant) != erun-devops` (skipped for the `erun` product tenant) | `deploy: runtime chart <tenant>-devops <v> not found in R (the tenant's own umbrella)` |
| 2 | `oci://R/charts/erun-devops` | always | `deploy: runtime chart erun-devops <v> not found in R (the shared platform chart)` |
| 3 | `oci://P/charts/erun-devops` | `P != ""` and `P != R` | `deploy: runtime chart erun-devops <v> not found in P (the shared platform chart in the runtime image's registry \| in erun's own registry)` |

Each probe is the same authenticated registry read `push` writes with; the first coordinate that publishes `<v>` installs, tracing `deploy: runtime chart <chart> <v> found in <registry> (<reason>)`. Rung 3 exists because ERun publishes `charts/erun-devops` only beside the runtime image it releases: an env whose `deploy` registry is its own ECR (or the in-cluster `erun-registry`) has the platform chart at no version there, and a search that stopped at rung 2 left it undeployable at every version.

When no coordinate is confirmed published at `<v>`, the search **refuses** rather than install rung 2 unconfirmed: `charts/erun-devops` is versioned on ERun's own release line, so pairing it with another project's version is a coordinate that can never exist, and installing it anyway was the failure mode this refusal replaced. Each probe answer is one of two kinds, and the refusal distinguishes them rather than treating a registry it couldn't read as a "no": **confirmed absent** (the registry answered and the version was not in the chart's published tags) or **could not determine** (the read itself failed — an unreadable or unauthenticated registry, a network error). The search traces each rung's answer as it goes (`deploy: runtime chart <chart> <v> not found in <registry> (<reason>)` for a confirmed miss, `deploy: runtime chart <chart> <v> could not be confirmed in <registry> (<reason>): <error>` for an inconclusive one), then a final `deploy: no runtime chart candidate confirmed at <v>; refusing to guess` before returning the error, so the dry-run trace shows the stopping decision even though the deploy never reaches a `helm` command. The error message enumerates every coordinate probed and its answer, plus the three ways out: [`erun init --runtime-registry <host>`](#erun-init) to record where ERun's artifacts live, `erun push --version <v>` from the project that owns a `<tenant>-devops` umbrella, or [`--runtime-chart`](#deploy-runtime-chart) / `EnvConfig.runtimechart` to name the chart outright — the last of which also lets a deploy proceed on an env whose search cannot itself confirm a coordinate, since a `--runtime-chart`/`EnvConfig.runtimechart` value supersedes the search's answer entirely.

The registry the search **resolved at** — not `R`, where it started — is what a successful deploy (or `open`) memoizes as `EnvConfig.runtimeregistry`, so the next search short-circuits there. The write is fill-or-confirm only, and resolution traces which of the two applies whenever the resolved registry differs from `R`:

| `EnvConfig.runtimeregistry` before | Deploy records | Trace |
|---|---|---|
| empty | the registry the chart resolved at | `deploy: recording runtime registry <resolved>, where the runtime chart resolved, rather than R, where the search started` |
| set (so `R` = it) and the chart resolved at `R` | the same value (no change) | none — nothing decided |
| set (so `R` = it) and the chart resolved at `P` | the value already there, unchanged | ``deploy: the env's runtime registry <recorded> stands; the runtime chart resolved from <resolved> instead (`erun init <tenant> <env> --runtime-registry <resolved>` changes it)`` |

A deploy that did not search — a repo-local chart, a chart stated via `--runtime-chart` / `EnvConfig.runtimechart`, or a component-only rollout — records the registry it pulled the runtime image from, which is the provenance `--current` re-addresses.

### Runtime image override — `--runtime-image` {#deploy-runtime-image}

`--runtime-image <ref>` installs the runtime running the given image via the canonical published `erun-devops` chart (the image rides in as `imageOverrides.erun-devops`), pinned to `--version` — **even when the env carries a repo-local `<tenant>-devops` chart**, which the override deliberately bypasses. This lets an operator bootstrap an environment on the canonical ERun base image (or any external image) before the env's own `<tenant>-devops` image has been built and pushed, then switch to the tenant image once it exists. It mirrors [`erun open --runtime-image`](#erun-open) but on the pure `deploy` primitive (it does not imply anything beyond the deploy).

| Flag | Type | Default | Validation | Persists to |
|---|---|---|---|---|
| `--runtime-image <ref>` | string (OCI image ref) | unset → the env's resolved runtime image (repo-local chart, `EnvConfig.runtimeimage`, or the [tenant-umbrella default](#deploy-runtime-image-default)). | Same reference rules as [`erun init --runtime-image`](#erun-init): a full reference (registry path and/or tag present) is used verbatim; a bare/tagless reference is qualified against the env's registry and pinned to `<version>` (never `:latest`). | `EnvConfig.runtimeimage`, so a later `open`/redeploy addresses the same image. |

The dry-run trace names the decision: `deploy: bypassing the repo-local runtime chart for the runtime image override <ref>; using published chart <chart> version <v>`, followed by `deploy: runtime image override <ref>:<v> (imageOverrides.erun-devops)`. The desktop runtime dialog threads this flag automatically when the operator picks the ERun-base entry in the version picker (#697); picking the env's own `<tenant>-devops` image deploys the env's own chart with no override.

#### Default runtime image for a tenant umbrella {#deploy-runtime-image-default}

With **no** `--runtime-image` and **no** `EnvConfig.runtimeimage`, deploy resolves `imageOverrides.erun-devops` from the published chart it is installing:

- Deploying the tenant's own `charts/<tenant>-devops` umbrella (rung 1 of the [runtime chart search](#deploy-runtime-chart-search)) → deploy **defaults the image to the umbrella's own** `<registry>/<tenant>-devops:<version>`. `erun push` publishes the umbrella and its `<tenant>-devops` image together on the tenant version line, so the chart's identity names the image; building and pushing it is sufficient. Trace: `deploy: defaulting runtime image to the <tenant>-devops chart's own image <ref> (imageOverrides.erun-devops)`, re-scoped into the deploy as `--set-string erun-devops.imageOverrides.erun-devops=<ref>`.
- Deploying the shared `charts/erun-devops` chart (no tenant umbrella published) → **no override** is set; the chart's own default image runs. An image-only build env therefore still points at its image through `runtimeimage`.

An explicit `runtimeimage` (or `--runtime-image`) always wins over this default — so a tenant that publishes its own umbrella can still pin a *different* image (e.g. a hotfix build) by setting the field, which traces the `runtime image override` line above rather than the `defaulting` line. This default is why a `<tenant>-devops` umbrella deploy runs the tenant's own image without any `runtimeimage`, instead of silently falling back to a stock `erun-devops:<tenant-version>` the tenant line never published (which would `ImagePullBackOff`).

### Runtime chart override -- `--runtime-chart` {#deploy-runtime-chart}

`--runtime-chart <ref>` names the runtime chart as its own deploy coordinate instead of deriving it from `--version`. ERun has four coordinates in play -- chart repository, chart version, image repository, image version -- and `--version` normally collapses all four, which is correct whenever [`erun push`](#erun-push) published the chart and image as a pair. It is wrong the moment they ship on different release lines: a project whose `<tenant>-devops` image is versioned on the project's own line (`9.9.9-snapshot-<ts>`) has no chart at that version and never will, so the published-chart lookup resolves nothing and the deploy fails `FetchReference … not found`. With this flag the operator states the chart (repository, and optionally version) while `--version` keeps stamping the env's runtime version and tagging the image.

| Flag | Type | Default | Validation | Persists to |
|---|---|---|---|---|
| `--runtime-chart <ref>` | string (OCI chart ref, optional `:<version>` suffix) | unset → the [runtime chart search](#deploy-runtime-chart-search) at `--version`. | An `oci://` scheme is added when absent. The version is split from the **last path segment only**, so a registry port (`registry.example:5000/charts/erun-devops`) is not read as a version. No version → the chart resolves at `--version`. | Nothing — run-only, deliberately not persisted, so an env's recorded state never implies a chart it was not deployed with. |

The override applies to the **runtime release only**; component charts continue to resolve at `--version`. It composes with [`--runtime-image`](#deploy-runtime-image) and with `EnvConfig.runtimeimage`, which is how each artifact ends up on its own line — the dry-run then carries both decisions:

```
deploy: runtime image override registry.example/acme/team-devops:9.9.9-snapshot-20260101010101 (imageOverrides.erun-devops)
deploy: runtime chart override oci://ghcr.io/sophium/charts/erun-devops version 1.2.3
helm upgrade --install … oci://ghcr.io/sophium/charts/erun-devops --version 1.2.3 …
```

### `--components` value set and selection precedence {#components-value-set}

`erun deploy` is **opt-in only**: it deploys exactly the resolved selection and nothing else — no chart deploys "by default" beyond that selection. Validation of `--components` depends on the deploy path:

- **Local path** (a repo with source): the selectable universe is every chart directory discovered under `<tenant>-devops/k8s/` **plus** the runtime aliases `<tenant>-devops` and `erun-devops` (the runtime resolves to a repo-local `<tenant>-devops`/`erun-devops` chart, or the published `erun-devops` chart when neither exists — the same dual-lookup [`erun open`](#erun-open) uses). A name matching no discovered chart and no runtime alias is rejected before any deploy runs with `unknown deploy component "<name>"; valid components for this environment are: <sorted universe>`, so typos and stale saved entries surface immediately.
- **Sourceless path** (a remote/runtime env, installing by reference from the registry): there are no local charts to validate against, and a tenant may publish its own component charts beyond the fixed platform set, so the selection is **trusted** — any name installs `oci://<registry>/charts/<name> --version <v>`. A name whose chart was never published at the version surfaces at deploy time as `MISSING_CHART_IN_REGISTRY` (an actionable "that version has no published chart" error), not an up-front rejection.

The selection resolves by **precedence** — the first non-empty tier wins entirely; tiers do not merge:

1. `--components <a,b,…>` — the explicit one-shot selection for this run.
2. `EnvConfig.deploy.components` — the environment's saved per-machine default (`erun init --components <a,b,…>`, or the desktop app's Runtime-tab checklist; see [Configuration · `deploy.components`](/reference/configuration#envconfig)).
3. `ProjectConfig.environments.<env>.k8s.deployments[]` — the repo deployment plan.
4. Empty (none of the above name anything) → the runtime chart alone, which bootstraps or heals the environment.

A chart deploys **iff** its component name is in the resolved selection. The runtime deploys only when the selection names a runtime alias, or when the selection is empty (tier 4) — an explicit selection that omits the runtime deploys the named components without it. `erun-powerdns` is the platform's authoritative DNS singleton; it runs the gpgsql backend against `erun-backend-postgres`, so sequence it after postgres in the plan. `erun-zitadel` is the platform's hosted IdP singleton, sequenced after postgres for the same reason (its own `zitadel` database on the shared instance); it renders one pod carrying both Zitadel core and the separate Login V2 container, a Service, and one Ingress routing `/ui/v2/login` to login and everything else to core, and it refuses to render without `zitadel.masterkeySecretName` naming an existing Secret and an auth host resolvable from the [`platform:` block](/reference/configuration#platform-block). The dry-run trace names the tier: `deploy: component selection source <tier>; deploying the runtime chart alone` (empty selection) or `deploy: component selection source <tier>; components <a, b, …>`.

Tiers never merge, so a saved tier-2 selection can permanently shadow a richer tier-3 plan — the same divergence a `--components` flag run once and never cleared can leave behind. Whenever the resolved source is tier 2 and the repo plan (tier 3) names components the saved set omits, a trace line names the gap at normal verbosity — `deploy: saved components shadow the repo plan; plan also names <a, b, …>` — and a **plain deploy (no `--components`) then refuses to run** rather than silently rolling out the stale subset: code cannot tell a saved selection that simply predates a plan addition from one an operator narrowed on purpose forever, so it does not guess either way. The refusal names both ways out: adopt the addition with `erun init --components <the saved set plus what the plan names>`, or return to the plan outright with `erun init --components ''` (an explicit empty value, not an omitted flag), which clears the saved selection; nothing else exists today that resets it. Passing `--components` explicitly for that one run (tier 1) bypasses the guard entirely, exactly as it bypasses the saved selection itself — an intentional one-shot narrowing is never blocked by it, and it never widens what actually deploys beyond what was named.

### MCP-auth stickiness and the downgrade guard {#deploy-mcp-auth}

`erun deploy` renders the chart from scratch (no `helm --reuse-values`), so any `mcpAuth.*` value it does not set falls back to the chart default — off. Because the edge's `raw` tool executes commands in the pod, an omission is a privilege downgrade, not a cosmetic one. The setting is therefore resolved from the environment, with an explicit opt-out:

| Input | Resolution |
|---|---|
| `--mcp-auth-public-key <path>` (MCP `deploy` `mcp_auth_public_key` input) | Trust that key. The path is persisted to `EnvConfig.mcpauthpublickeypath` at the point the deploy applies the key — after the `<release>-mcp-auth` Secret apply, before the `helm upgrade` — so a rollout that fails afterwards still leaves the environment naming the key its release trusts. Trace: `deploy: mcp auth: recording the public key <path> on <tenant>/<environment>`, emitted only when the recorded value would change. |
| Neither flag, `EnvConfig.mcpauthpublickeypath` set | Rethread the recorded key. Trace: `deploy: mcp auth: rethreading the env's recorded public key <path>`. |
| `--no-mcp-auth` (MCP `deploy` `no_mcp_auth` input) | Resolve **no** authentication and clear `mcpauthpublickeypath` — the clear is written after the unauthenticated release has rolled out, since only then has the edge actually stopped trusting the key. Trace: `deploy: mcp auth disabled by request; …`. |

There is one signing mechanism — a `file://`-issued key — used by two callers: the desktop passes its own key via `--mcp-auth-public-key`, and a hosted environment's server-side deploy Job passes the backend's own MCP-signing public key (`mcptoken.Signer`) the same way, automatically, so the console's minted tokens verify with no Operator action. Both write the same `mcpAuth.*` chart values; only the key's origin differs.

**Downgrade guard.** When the resolved plan has authentication off and `--no-mcp-auth` was not given, deploy reads the live release's values (`helm get values <release> -o json`) and fails at resolution if `mcpAuth.enabled` is true — the case of an environment that enabled authentication before the key was recorded. Error code `MCP_AUTH_DOWNGRADE_REFUSED`; the message names what the release trusts, resolved from the same read plus the release's own Secret:

1. `mcpAuth.issuer` is an OIDC (`https://`) issuer — only possible on a legacy or hand-configured release, since erun has no supported way to write one — the message names that issuer and says so, pointing at `--mcp-auth-public-key` to switch the release onto the key-based path instead. Trace: `deploy: mcp auth: release <release> authenticates against the OIDC issuer <issuer>; no local key is involved`.
2. Otherwise the release trusts a desktop key, so deploy reads it out of `mcpAuth.secretName` (defaulting to `<release>-mcp-auth`) with `kubectl get secret <name> -o jsonpath={.data.desktopid\.pub}` and compares it byte-for-byte with this host's desktop identity public key (`<user config dir>/ERun/desktopid.pub`):
   - **Match** → the message names that path, and its `sha256`, as the key to pass to `--mcp-auth-public-key` — the match is also what says re-supplying it keeps the edge's existing trust rather than rotating it. Trace: `deploy: mcp auth: release <release> trusts this host's desktop identity key <path>`.
   - **No match** → the message names the Secret and the key's `sha256`, and says it is not this host's desktop identity key.
   - **Secret unreadable** → the message names the Secret alone. Trace: `deploy: mcp auth: secret <name> could not be read`.

   The `--no-mcp-auth` opt-out is named in every case. The read runs **only** on that path (an authenticated deploy pays nothing) and a release that cannot be read imposes no constraint, so an unreachable cluster never blocks a deploy. `ERUN_MCP_AUTH_LIVE_PROBE_OVERRIDE` is the integration-suite seam that answers the read without a cluster; it is a test seam, not a production knob.

The guard is scoped to explicit deploy requests (`erun deploy`, `erun upgrade`, `erun publish`, `open --deploy`, `build --deploy`). `erun open`'s heal-redeploy rethreads a recorded key but is never blocked by the guard — it must still be able to hand over a shell.

### In-pod guard for `local-agent` environments {#deploy-in-pod-local-agent}

The erun config store inside a runtime pod is a projection of the `ERUN_*` env vars the chart injects (see [`erun doctor --sync-config`](#erun-doctor)), not the host config that defines the environment. For a `local-agent` environment that projection is missing everything that shapes the env — the hostPath worktree, the local port range, the pod resource limits, and the runtime registry — so an in-pod resolve silently substitutes defaults and the rollout reshapes the environment (and cuts the MCP channel that issued it).

`erun deploy` refuses that combination at resolution: error code `IN_POD_LOCAL_AGENT_RUNTIME_DEPLOY`, naming the host command to run instead. The guard fires only when all of the following hold, so nothing else regresses:

- The environment's `type` is `local-agent`.
- The process is inside an erun runtime pod, identified by the chart-set `ERUN_TENANT` + `ERUN_ENVIRONMENT` pair, and they name **this** environment. (A kubeconfig or an `in-cluster` context is not the signal — both exist off-pod.)
- The resolved specs include the runtime chart (`<tenant>-devops`). A component-only selection is unaffected.

A `remote-agent` environment owns its worktree inside the pod, so it keeps deploying itself in-pod.

### Deploy-plan resolution

The deploy plan comes from `ProjectConfig.environments.<env>.k8s.deployments[]` (see [Configuration · `environments.<env>.k8s.deployments[]`](/reference/configuration#per-project-config)). It serves two roles: **selection tier 3** above — its charts are the deploy selection when no `--components` and no saved `deploy.components` apply — and the **ordering** source: steps deploy in listed order, and a list within a step deploys in parallel. When the field is absent, `erun deploy` falls back to ordering by chart dependency declarations; on a tie, alphabetical by component name.

### Skip-helm semantics

`erun deploy` skips the `helm upgrade --install` for a component when:

1. The resolved version equals the version the env already runs (no rollout would change anything), and
2. The chart directory (`<tenant>-devops/k8s/<component>/`) has no diff against the last successful deploy's snapshot, and
3. `--force` is not set.

The skip emits `result: skipped (no change)` in the trace. `deploy` never pushes, so there is no `docker push` to skip.

### Rollout wait and pod monitoring

`erun deploy` runs `helm upgrade --install --wait --wait-for-jobs --timeout <t>` and, concurrently, polls the release's pods to fail fast on a real failure while staying patient through a slow image pull.

**Timeout resolution** (`<t>`), highest precedence first:

| Source | Value |
|---|---|
| `--rollout-timeout <dur>` flag (MCP `deploy` `timeout` input) | Per-deploy override. Go duration (e.g. `8m`, `90s`); a malformed or non-positive value aborts before any rollout: `invalid rollout timeout "<v>"`, exit 1. |
| `EnvConfig.deploy.timeout` | Per-environment default (see [Configuration · `EnvConfig`](/reference/configuration#envconfig)). A malformed value aborts at spec resolution: `invalid environment deploy timeout "<v>"`, exit 1. |
| Built-in default | `5m0s`. |

The resolved value is the `helm --timeout` argument (visible in the dry-run helm command line) and the `waiting for helm rollout (timeout <t>)...` real-run line. It is the upper bound on how long a still-progressing rollout waits; it does **not** apply to the rollback path (`helm rollback --wait --timeout 2m0s`) or the shell-launch wait, which carry their own fixed timeout.

**Pod monitor.** While helm waits, erun polls `kubectl get pods -o json` for the release (filtered by the `meta.helm.sh/release-name` annotation) every 2s (`ERUN_DEPLOY_POD_WATCH_INTERVAL`, default `2s`, floor `100ms`). It classifies each init + main container state and decides between *keep waiting* and *abort early*:

- **Keep waiting (image still pulling).** A container in `ImagePullBackOff` or `ErrImagePull` whose message is **not** a permanent rejection is treated as a pull in progress — a large image on a slow or rate-limited registry legitimately cycles `Pulling → ErrImagePull → ImagePullBackOff → retry`. The watcher does not abort; it keeps waiting up to `<t>` and prints `pod <p>: <c> Pulling image (<reason>)` status lines so the wait is visible. `helm --timeout` is the only bound on this case.
- **Abort early (real failure).** When a container reaches a state that will not recover, the watcher sends `SIGINT` (then `SIGKILL` after 2s) to the helm process and the deploy fails immediately with `deploy failed early: pod <p> container <c> <reason>: <message>` rather than waiting out `<t>`. The terminal states are:
  - `InvalidImageName`, `CreateContainerConfigError`, `CreateContainerError`, `RunContainerError`, `ContainerCannotRun` — config/runtime errors that no wait fixes.
  - `CrashLoopBackOff` once `restartCount ≥ 2` (a single transient init crash is tolerated). The last terminated message is surfaced.
  - A **permanent** image-pull rejection: an `ImagePullBackOff`/`ErrImagePull` message containing `manifest unknown`, `not found`, `repository does not exist`, `pull access denied`, `unauthorized`, `authentication required`, `forbidden`, `denied`, `invalid reference format`, or `no such image` — a missing tag, absent repository, or bad credentials that retrying will never resolve. (Transient causes — timeouts, DNS blips, connection resets, TLS handshake failures — are deliberately **not** treated as permanent, so a briefly unreachable registry keeps waiting.)

The monitor is best-effort: a transient `kubectl get pods` error is ignored (it never aborts a deploy helm would otherwise drive to success). In `--dry-run` the watcher action is traced (`deploy: watching pods in <ns> …`) and the `kubectl get pods -o json` command is shown, but no polling happens.

### Immutable-selector recovery

A Kubernetes `Deployment.spec.selector` is immutable: helm cannot patch a release whose installed selector differs from the chart's rendered selector, and aborts the upgrade with `Deployment.apps "<name>" is invalid: spec.selector: … field is immutable`. This happens when an environment was first installed under a chart that rendered a different selector than the one now being applied (e.g. a pre-cutover per-tenant chart that labelled pods `app: <release>` versus a chart that hardcoded `app: erun-devops`, or vice-versa).

`erun deploy` detects this specific failure and recovers automatically, in `erun-common` so both CLI and MCP flows get it:

1. It parses the offending Deployment name from helm's error and deletes **only** that Deployment (`kubectl delete deployment <name> --namespace <ns> [--context <ctx>] --ignore-not-found`). The release's PVCs (`<release>-home`, `<release>-docker`, `<release>-worktree`), ServiceAccount, and RBAC are separate objects and are **not** touched, so build cache and `/home/erun` survive.
2. It retries the `helm upgrade --install` **once**. With the Deployment gone, helm creates it fresh with the new selector.

The recovery is bounded to a single retry (the delete removes the conflict, so the retry cannot hit the same error) and fires only for an immutable `spec.selector` change — an unrelated immutable-field error is not caught and never triggers a delete. It runs only in real execution, not `--dry-run` (the conflict is a helm side-effect failure, not a pre-action decision). The trace names the decision on the audit channel: `deploy: Deployment <name> selector is immutable and changed; deleting it (PVCs preserved) and retrying the upgrade`; the literal `kubectl delete` is logged at `-vv`. If the retried upgrade fails for any other reason, that error surfaces as `HELM_UPGRADE_FAILED`.

### Error codes

| Code | Cause | Exit code |
|---|---|---|
| `HOST_ENV_NO_DEPLOY` | The environment is a [host env](/concepts/environment-types#host) — no pod and no cluster to deploy to. Checked before spec resolution, so nothing is built or resolved first. | `1` |
| `NO_VERSION` | Neither `--version` nor `--current` given. `deploy` does not mint a version, so there is nothing to install. | `1` |
| `NO_CURRENT_VERSION` | `--current` given but the env has no recorded runtime version yet. Deploy a specific `--version` once to seed it. | `1` |
| `CLUSTER_UNREACHABLE` | Same as `erun open`. | `2` |
| `MISSING_IMAGE_IN_REGISTRY` | A chart references `<registry>/<component>:<version>` that does not exist (and was never built/pushed). | `1` |
| `RUNTIME_CHART_NOT_CONFIRMED` | The [runtime chart search](#deploy-runtime-chart-search) confirmed no coordinate published at the requested version — refused before any `helm` command runs. The message names each coordinate probed and whether it was confirmed absent or could not be read; see that section for the full contract. | `1` |
| `MISSING_CHART_IN_REGISTRY` | A chart resolution *did* confirm a coordinate (a component chart, always trusted on the sourceless path; or a runtime chart the search resolved) but the `helm pull` for it failed anyway — a tag evicted between the probe and the pull, or a genuinely unpublished component chart version. The message names each coordinate: record where ERun's artifacts live (`erun init --runtime-registry`), push the version from the project that owns the chart, or name the chart with `--runtime-chart`. For a component chart, push the version first — push publishes image and chart together. | `1` |
| `HELM_UPGRADE_FAILED` | A step in the plan failed (or helm's own `--timeout` elapsed while the rollout was still not ready); later steps are not executed. | `2` |
| `ROLLOUT_CONTAINER_FAILED` | The [pod monitor](#rollout-wait-and-pod-monitoring) observed a terminal container failure (crash loop, config/runtime error, or a permanent image-pull rejection) and aborted the rollout early instead of waiting out the timeout. The message names the pod, container, and reason. | `2` |
| `INVALID_ROLLOUT_TIMEOUT` | `--rollout-timeout` or `EnvConfig.deploy.timeout` is not a positive Go duration. Nothing runs. | `1` |
| `MCP_AUTH_DOWNGRADE_REFUSED` | The live release has `mcpAuth.enabled=true` but the resolved plan has no authentication, and `--no-mcp-auth` was not given. Nothing runs. See [MCP-auth stickiness](#deploy-mcp-auth). | `1` |
| `IN_POD_LOCAL_AGENT_RUNTIME_DEPLOY` | A `local-agent` environment's runtime chart was deployed from inside that environment's own runtime pod, where the config store is not authoritative. Nothing runs. See [In-pod guard](#deploy-in-pod-local-agent). | `1` |

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
| `--restore-env-config-from-backup` | string | `""` | Restore the target environment's `config.yaml` from a dated backup (`YYYY-MM-DD`) or an absolute path, before any tenant/env work so a corrupted env config can be recovered first. Requires explicit `<tenant> <environment>` args. Under `--dry-run` the copy is traced but not performed. Errors: missing explicit tenant+environment → `--restore-env-config-from-backup needs an explicit tenant and environment` (exit 1); no matching backup → `no env config backup matches "<date>" for <tenant>/<env>` (exit 1). The MCP `doctor` tool exposes the same operation as the `restoreEnvConfigFromBackup` input. |
| `--repair-workspace-sync` | bool | `false` | For a remote-agent env with `sshd.workspacesync.enabled`, diagnose and repair the host mirror's SSH provisioning **without a helm redeploy**: resolve/persist the SSH public key, write the local `~/.ssh/config` alias, install the pod's `authorized_keys` through the runtime container, and ensure the SSH port-forward. When SSH still can't reach the pod afterwards, it names `erun sshd init` as the remaining step (the redeploy this repair deliberately skips). Under `--dry-run` every action is traced and nothing runs; when it is the only action requested, `doctor` stops after it (no deploy diagnosis or prune prompts). Host-side provisioning, so it is CLI-only — the MCP `doctor` tool does not expose it, mirroring `erun sshd init`. |

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

### Git push access check

Gating: runs only when `EnvConfig.RemoteWorktree()` is true (`remote-agent` or `runtime` env types) — the project checkout lives inside the pod for those types, so a credential gap there is invisible to anything running on the operator's own machine. A `local-agent` or `host` env is skipped entirely: its checkout lives on the operator's machine, which already carries their own git/gh credentials.

The check execs a single read-only shell script into the runtime pod (`kubectl exec deployment/<release> -- /bin/sh -lc '<script>'`) that: resolves `origin` via `git remote get-url origin`; runs `git ls-remote --exit-code <remote> HEAD` to test an anonymous fetch; runs `gh auth status -h <host>` (never `gh auth login`, `gh auth refresh`, or `gh auth switch`) to test whether a `gh` session is configured for the remote's host; checks `GH_TOKEN`/`GITHUB_TOKEN`; and, for an SSH remote with none of the above, runs `ssh -o BatchMode=yes -T git@<host>` and greps its stderr for `successfully authenticated`. Every one of these is read-only and side-effect-free, so the check can never itself start gh's interactive device-code/browser flow, which cannot complete headlessly — there is no browser, and no human at a prompt, inside an agent pod.

Report shape (printed under `== Git push access ==`): `Remote` (the resolved `origin` URL, or the section is omitted entirely when there is no checkout or no `origin`), `Fetch` (`ok` or `FAILED`), `Push` (`credential configured (...)` or `NO CREDENTIAL — <remedy>`). The remedy text always names the same non-interactive-safe fix: authenticate once from an interactive shell opened with `erun open <tenant> <environment>` (`gh auth login -h <host>`, or `gh auth login -h <host> --with-token < token-file` to skip gh's browser flow entirely), and never attempt it from an unattended agent run. Provisioning this credential is deliberately manual — the same model as `~/.aws/credentials`' `erun-host` profile, but for a full `gh`/git identity rather than a narrowly-scoped registry token, so erun does not copy a live operator credential into a shared, ephemeral pod on the environment's behalf.

Reading this check requires the runtime pod, same as host AWS credentials: when the pod isn't reachable, `doctor` reports `could not read` for it instead of aborting and continues the rest of the run.

Exposed identically on both transports: the CLI's plain-text report and the [MCP `doctor` tool](/mcp/overview#doctor)'s output carry the same `== Git push access ==` section, both driven by the same `eruncommon.InspectGitPushAccess`.

### Deploy recovery actions {#deploy-recovery-actions}

After the read-only deploy diagnosis (helm release status + runtime pods), `doctor` can run two recovery actions that **mutate the live release**. They are **alternative** fixes for different failure modes, not additive steps — clearing a pending lock leaves the release at its last deployed revision, so a rollback run straight after would step back a further revision. `--clear-pending-helm` and `--rollback` are therefore mutually exclusive; passing both aborts with `--clear-pending-helm and --rollback are alternative recoveries; pass only one` (exit 1, nothing runs).

Gating: each action runs non-interactively with its flag. With **no flag**, `doctor` inspects the helm status and prompts for the **single recommended** action — `pending-install`/`pending-upgrade`/`pending-rollback` → clear pending; a present-but-unhealthy release (`failed`, `superseded`, …) → rollback; a healthy (`status: deployed`), missing (`not found`), or unreadable release → no destructive prompt at all. It never offers both at once. Under `--dry-run` the exact command is traced and nothing runs.

| Action | Flag | Command run | Use when |
|---|---|---|---|
| Clear pending helm release | `--clear-pending-helm` | `kubectl [--context <ctx>] --namespace <ns> delete secrets,configmaps -l 'owner=helm,name=<release>,status in (pending-install,pending-upgrade,pending-rollback)' --ignore-not-found` | A deploy died mid-upgrade and left the release locked in a pending state, so the next `erun deploy` refuses to start. |
| Roll back to last successful revision | `--rollback` | `helm rollback <release> --namespace <ns> [--kube-context <ctx>] --wait --timeout 2m0s` | The current revision is bad or never converged and a previous revision was healthy. |

`<release>` is the runtime release name for the tenant; `<ns>` and `<ctx>` are the resolved env namespace and kube-context. To rebuild and roll out fresh images instead of recovering the existing release, re-run [`erun deploy --force`](/cli/deploy) — the desktop's failed-deploy card surfaces that as its **Force rebuild & redeploy** button.

Both actions are also exposed on the [MCP `doctor` tool](/mcp/overview#doctor) via the `clearPendingHelm` and `rollback` boolean inputs.

### Exit codes

| Code | Meaning |
|---|---|
| `0` | All checks `ok`, or every `missing` check was recovered. |
| `1` | At least one check `missing` and recovery declined (or `--dry-run`). |
| `2` | At least one check `error` (parse failure, permission denied). Inspect the trace to find which. |

---

## `erun list` {#erun-list}

For the Operator view, see [`erun list`](/cli/list). With no flags, `list` prints the full listing (every configured tenant and environment) and never mutates state.

### Version drift across a tenant {#version-drift}

Pass `--tenant` to switch the command into a distinct read: erun-version drift across that one tenant's own environments, instead of the full listing.

| Flag | Type | Default | Effect |
|---|---|---|---|
| `--tenant <name>` | string | none | Switches the command to a version-drift report for this tenant. Errors `tenant "<name>" not found` if the tenant has no config. |
| `--gate-environment <name>` | string | none | Requires `--tenant`. Names the environment driving that tenant's merge-queue gate (erun has no stored concept of which environment gates a tenant's merges — see [merge-queue § the gate](/collaboration/merge-queue#the-gate) — so the caller states it). Errors `--gate-environment requires --tenant` when passed alone, or `gate environment "<name>" not found in tenant "<tenant>"` when the named environment doesn't exist in the tenant. |

The MCP `list` tool takes the same two inputs as `versionDriftTenant`/`gateEnvironment`, alongside its existing `verbosity`; when `versionDriftTenant` is set, the structured result carries an additional `versionDrift` field beside the ordinary list result rather than replacing it (`ListToolResult`, `erun-mcp/list.go`) — the CLI's own `--output json` for this mode instead emits the version-drift report alone.

Each environment's version comes from the same `ResolveErunVersion` config-only resolution the full listing's `runtime-version:` line uses (see [Release lines](/cli/list#release-lines)) — nil (rendered `version=none`) whenever it can't be read from config alone, e.g. a deploy that never recorded a resolved runtime image.

```
Version drift for tenant erun:
  max version: 1.0.247
  environments:
    - build version="1.0.246" [behind max]
    - code4 version="1.0.247"
  gate:
    environment: build
    version: 1.0.246
    behind: yes -- outdated relative to code4
```

`max version` is the newest version observed among the tenant's *own* environments, not the newest erun has ever published — that's `erun version`'s/`erun upgrade`'s registry-latest concern. `[behind max]` marks an environment whose version parses lower than the max; an unparseable or missing version is shown bare (never guessed at) and excluded from the max computation. The `gate:` block only prints when `--gate-environment` is given, and `behind:` has three readings: `no` (the gate carries the max, or ties it), `yes -- outdated relative to <envs>` (naming every environment running a newer version), and `unknown (gate's own erun version could not be resolved from config)` when the gate's own version can't be read — reported explicitly rather than folded into a silent `no`, since a gate older than the code it gates can pass a change that would fail on current code.

### Control plane versions {#control-plane-versions}

Pass `--control-planes` to switch the command into a third distinct read: every configured erun-hosted control plane's deployed version, compared against the newest version erun's own registry has actually published — deployed-vs-published, not deployed-vs-main. Cannot be combined with `--tenant`/`--gate-environment`.

| Flag | Type | Default | Effect |
|---|---|---|---|
| `--control-planes` | bool | `false` | Switches the command to a control-plane version report instead of the full listing. Errors `--control-planes cannot be combined with --tenant/--gate-environment` if either is also set. |
| `--dry-run` | bool | `false` | Only meaningful alongside `--control-planes`. Traces which control planes and which registry lookup would be checked, without making either network call, and prints `Dry run: control plane version check planned; see trace for the planes and registry lookup that would be probed.` |

The MCP `list` tool exposes the same behavior as `controlPlanes` (bool) and `preview` (bool, the MCP-side equivalent of `--dry-run`); when `controlPlanes` is set, the structured result carries an additional `controlPlaneVersionDrift` field (`eruncommon.ControlPlaneVersionDrift`) beside the ordinary list result (`ListToolResult`, `erun-mcp/list.go`) — the CLI's own `--output json` for this mode instead emits the control-plane version report alone.

Every configured cloud-provider alias with `provider: erun` is treated as a control plane. For each one, the command calls that plane's own unauthenticated `GET /v1/platform` to read its deployed `version`; a plane that does not answer (network failure, non-2xx) is reported `reachable: false` with `unreachableReason` set, and never gets a `behind`/`ahead` verdict — an unreachable plane is never reported current. The published baseline comes from the same registry lookup `erun pin`/`erun upgrade` already use (`ResolveDefaultRuntimeRegistryVersions`, erun's own `ghcr.io/sophium/erun-devops` image tags) rather than a hand-maintained list, so it can never drift from what erun has actually shipped.

```
$ erun list --control-planes
published version: 1.0.247
Control planes:
  - erun+api.erunpaas.com@erun api-url="https://api.erunpaas.com" reachable=yes version="1.0.245" [behind published -- roll it]
```

`behind` is set only when both the plane's deployed version and the registry's published latest stable parse as plain three-part semver, and the deployed version orders strictly *below* the published one — routine drift, the plane simply hasn't been rolled onto an already-published release yet. `ahead` is the opposite order: the plane is running something the registry has never published at all, reported distinctly because it is a more alarming condition than routine drift (an unpublished build reached a live plane some other way), never folded into `behind`. Neither is set when the registry lookup itself failed (`publishedVersionError`, printed as `published version: unresolved (<reason>)`) or either version fails to parse as plain semver — absent evidence is reported explicitly rather than guessed at.

### Exit codes

| Code | Meaning |
|---|---|
| `0` | Full listing, version-drift report, or control-plane version report resolved (including when a plane is unreachable or behind/ahead — those are findings in the report, not command failures). |
| `1` | `--gate-environment` without `--tenant`; unknown `--tenant`; unknown `--gate-environment`; `--control-planes` combined with `--tenant`/`--gate-environment`. |

---

## `erun observe` {#erun-observe}

Reports an environment's Kubernetes state, read-only: every underlying call is `kubectl [--context <ctx>] --namespace <ns> get <resource> [name] -o json`, never anything that mutates. Same operation as the MCP `observe` tool (see [MCP overview § `observe`](/mcp/overview#observe)).

### Flags

| Flag | Type | Default | Effect |
|---|---|---|---|
| `--tenant <t>` | string | current scope | Target tenant. |
| `--environment <e>` | string | current scope | Target environment; requires `--tenant`. |
| `--secret <name>=<key>` | string, repeatable | none | Check Secret `<name>` for key `<key>`'s presence. Malformed (missing `=`, empty name, or empty key) aborts with `--secret must be name=key, got "<value>"` (exit 1) before any `kubectl` call. |

### Resolution and output shape

Resolves tenant/environment/namespace the same way every other typed command does (`ResolveOpen`), then issues, in order: `get pods`, `get resourcequota`, `get limitrange`, `get ingress`, `get certificates.cert-manager.io`, then one `get secret <name>` per `--secret` check. `--output json` emits:

```jsonc
{
  "tenant": "myapp", "environment": "prod", "namespace": "myapp-prod",
  "pods": [ { "name": "web-0", "phase": "Running", "ready": true, "restartCount": 0, "reason": "" } ],
  "resourceQuotas": [ { "name": "erun-quota", "hard": { "limits.cpu": "4" }, "used": { "limits.cpu": "1" } } ],
  "limitRanges": [ { "name": "erun-limits", "limits": [
    { "type": "Container", "max": {}, "min": {}, "default": { "cpu": "1" }, "defaultRequest": { "cpu": "100m" } }
  ] } ],
  "ingresses": [ { "name": "web", "hosts": ["prod.example.com"],
    "tls": [ { "hosts": ["prod.example.com"], "secretName": "web-tls" } ] } ],
  "certificates": [ { "name": "wildcard", "ready": false, "reason": "Issuing", "message": "…",
    "secretName": "wildcard-tls", "dnsNames": ["*.prod.example.com"], "orders": [ /* see below */ ] } ],
  "secrets": [ { "name": "db-credentials", "key": "password", "exists": true, "hasKey": true, "error": "" } ]
}
```

`reason` on a pod is the container's `waiting`/`terminated` reason if present, else the `PodScheduled=False` reason (a pod never admitted to a node has no container status to read a reason from), else the `Ready=False` condition's reason. `secrets` is omitted entirely when no `--secret` was given.

### The Certificate → CertificateRequest → Order → Challenge walk {#certificate-failure-chain}

`certificates[].orders` is populated only when that Certificate's `status.conditions[type=Ready]` is not `True`. The walk, run once against a fresh listing of each resource kind in the namespace (not once per certificate):

1. List `certificaterequests.cert-manager.io`; filter to the ones labelled `cert-manager.io/certificate-name=<certificate>`; take the one with the latest `metadata.creationTimestamp` (a Certificate can be reissued, leaving stale requests behind — only the newest one's chain is live). None matching → `orders` is empty.
2. List `orders.acme.cert-manager.io`; keep the ones whose `ownerReferences` include `{kind: CertificateRequest, name: <request from step 1>}`.
3. For each such Order, list `challenges.acme.cert-manager.io` and keep the ones owned (`ownerReferences`) by that Order.
4. Each reported order carries `state`/`reason` from `status`; each challenge carries `type`/`dnsName` from `spec` and `state`/`reason` from `status` — `reason` is the field that explains a stuck issuance (e.g. a webhook solver's RBAC denial), which is otherwise three separate `kubectl get` calls away.

A cluster with no cert-manager CRDs installed (`kubectl` reports "the server doesn't have a resource type" / "no matches for kind") reports `certificates: []` rather than erroring — a cluster simply has no certificates to walk.

### Secret presence checks

Each `--secret <name>=<key>` becomes one `kubectl get secret <name> -o json`, read only for its key names (`data`/`stringData`), never a value:

| Outcome | `exists` | `hasKey` | `error` |
|---|---|---|---|
| Secret and key both present. | `true` | `true` | `""` |
| Secret present, key absent. | `true` | `false` | `""` |
| Secret not found. | `false` | `false` | `""` |
| Any other failure (e.g. RBAC denial reading the Secret). | `false` | `false` | the kubectl error, so a permission problem is never reported indistinguishably from "does not exist" |

### Error behaviour

| Failure | Behaviour |
|---|---|
| Tenant/environment can't be resolved. | Errors before any `kubectl` call. |
| `--secret` isn't `name=key`. | Errors before any `kubectl` call, naming the malformed value. |
| `get pods` / `resourcequota` / `limitrange` / `ingress` fails (namespace or cluster unreachable). | Errors naming the failed call; nothing is reported. |
| `get certificates.cert-manager.io` fails because the CRD isn't installed. | `certificates: []`; not an error. |
| `get certificates.cert-manager.io` fails for another reason. | Errors naming the failed call. |

---

## `erun usage` {#erun-usage}

Reports an environment's live CPU, memory, and disk usage, read from the runtime container's own cgroup v2 accounting and a statfs of its workspace mount. Same operation as the MCP `usage` tool (see [MCP overview § `usage`](/mcp/overview#usage)).

**No metrics-server is required.** Unlike `kubectl top` (which reports `error: Metrics API not available` on any cluster without the metrics-server add-on — every local orbstack/k3s-style cluster included), the underlying `kubectl exec`s a fixed diagnostic script into the runtime pod's `erun-devops` container and reads `/sys/fs/cgroup` and `df` directly. Nothing here can mutate the cluster.

### Flags

| Flag | Type | Default | Effect |
|---|---|---|---|
| `--tenant <t>` | string | current scope | Target tenant. |
| `--environment <e>` | string | current scope | Target environment; requires `--tenant`. |
| `--interval <seconds>` | float | `1` | CPU sample window, clamped to `[0.1, 30]`. `cpu.stat`'s `usage_usec` is read, the window elapses, then it is read again, so utilisation is a rate over the interval rather than a meaningless cumulative counter. |

### Resolution and output shape

Resolves tenant/environment/namespace the same way every other typed command does (`ResolveOpen`), then runs one `kubectl exec -c erun-devops deployment/<tenant>-devops -- /bin/sh -lc <script>` against the resolved namespace. `--output json` emits:

```jsonc
{
  "tenant": "myapp", "environment": "prod",
  "cpu": { "quotaCores": 1, "utilizationPercent": 12.4, "intervalSeconds": 1 },
  "memory": { "currentBytes": 413589504, "peakBytes": 1027301376, "limitBytes": 2147483648, "percentOfLimit": 19.3, "oomKills": 0 },
  "disk": [ { "mount": "/home/erun", "totalBytes": 202991730688, "usedBytes": 101495865344, "percentUsed": 50.0 } ],
  "warnings": [],
  "excludesBuilds": true
}
```

`cpu.quotaCores` is `cpu.max`'s quota ÷ period; `memory.percentOfLimit` is `memory.current` ÷ `memory.max`; `disk[].percentUsed` is `df`'s used ÷ total for the watched mount (the runtime chart's `HOME`, `/home/erun`, is the only mount watched today). `warnings` is omitted (empty) unless a threshold below is crossed.

`excludesBuilds` is `true` whenever the environment's type carries the `erun-dind` sidecar (every type except `runtime` and `host` — `EnvironmentType.UsesDindSidecar`), omitted (false) otherwise. `cpu`/`memory` above are read from the `erun-devops` container's own cgroup alone; an image build (`erun build`/`erun release`) actually runs in `erun-dind`, a separate cgroup whose build containers are cgroup siblings rather than descendants of this one, so there is no path from inside `erun-devops` to read them. `excludesBuilds` names that gap explicitly rather than let a busy build read as an idle environment — the same disclosure the desktop's Runtime tab caption makes (`usageExcludesBuilds` in `erun-ui/frontend/src/components/app/Sidebar.helpers.ts`) and the non-JSON output states as a `Note:` line. [`erun observe`](/agent-reference/cli-flags#erun-observe) reports the sidecar's own resource limits.

### Unavailability, not failure {#usage-unavailability}

Every field group reports its own unavailability rather than failing the whole call — cgroup v1, an unlimited limit, and a file the exec script could not read are all normal on some clusters, not errors:

| Condition | Reported as |
|---|---|
| `/sys/fs/cgroup` is not `cgroup2fs` (cgroup v1, or absent). | `cpu.unavailable` and `memory.unavailable` name the reason; every other CPU/memory field stays zero. |
| `cpu.max`'s quota is `max` (unlimited) or the file could not be read. | `cpu.unavailable` names the reason — there is no quota to measure utilisation against. |
| `memory.max` is `max` (unlimited). | `memory.unlimited: true`; `memory.limitBytes`/`percentOfLimit` stay zero rather than a fabricated percentage. |
| `memory.current` could not be read. | `memory.unavailable` names the reason. |
| `df` reported nothing for the watched mount. | that entry's `disk[].unavailable` names the reason. |

`memory.oomKills` comes from `memory.events`' `oom_kill` counter — a real kill count, not a guess made after the fact.

### Named warning thresholds {#usage-thresholds}

A reading nobody acts on is decoration, so `warnings` fires a plain-language entry (not a code) when:

| Threshold | Reasoning |
|---|---|
| `memory.percentOfLimit` ≥ 85%. | A container this close to its limit is one build step away from an OOM kill. |
| `memory.peak` ÷ `memory.limitBytes` ≥ 95%. | `memory.peak` is a high-water mark, so a near-limit peak matters even after current usage drops back down. |
| any `disk[].percentUsed` ≥ 90%. | Disk fills silently — no kernel counter tracks "close calls" the way `memory.peak` does for RAM — so the warning threshold sits ahead of the failure rather than reacting to it. |
| `memory.oomKills` > 0. | Always reported: a kill already happened. |

### Error behaviour

| Failure | Behaviour |
|---|---|
| Tenant/environment can't be resolved. | Errors before any `kubectl` call. |
| The namespace, deployment, or cluster is unreachable. | Errors naming the failed `kubectl exec`. |

---

## `erun resize` {#erun-resize}

Changes the runtime pod's and/or the `erun-dind` sidecar's CPU/memory limits and rolls them out through the same deploy composition `erun deploy` uses (`ResolveCurrentDeploySpecs`/`RunDeploySpecs`), so it reuses the existing rollout mechanism rather than inventing a second one. Same operation as the MCP `resize` tool (see [MCP overview § `resize`](/mcp/overview#resize)).

### Flags

| Flag | Type | Default | Effect |
|---|---|---|---|
| `--tenant <t>` | string | current scope | Target tenant. |
| `--environment <e>` | string | current scope | Target environment; requires `--tenant`. |
| `--cpu <cpu>` | string (Kubernetes CPU quantity) | unset | Explicit target CPU limit for the runtime pod. Merged onto the current value — naming only `--cpu` leaves memory unchanged. |
| `--memory <memory>` | string (Kubernetes memory quantity) | unset | Explicit target memory limit for the runtime pod. Merged onto the current value the same way. |
| `--dind-cpu <cpu>` | string (Kubernetes CPU quantity) | unset | Explicit target CPU limit for the `erun-dind` sidecar. Merged onto the current value, independent of `--cpu`/`--memory` — may be combined with them in one call. |
| `--dind-memory <memory>` | string (Kubernetes memory quantity) | unset | Explicit target memory limit for the `erun-dind` sidecar. Merged onto the current value the same way. |
| `--apply-recommendation` | bool | `false` | Size the runtime pod from `RecommendRuntimeSizing`'s per-resource verdicts (see [`erun list` § the sizing recommendation](/cli/list#the-sizing-recommendation)) instead of `--cpu`/`--memory`. Mutually exclusive with them. Never sizes the `erun-dind` sidecar — see step 2 below for why. |
| `--override-lease` | bool | `false` | Proceed even though `LoadEnvironmentActivityLeases` reports a held lease. |
| `--orchestrator <id>` | string | `""` | Recorded as `EnvironmentActivityLeaseHolder.Orchestrator` on the resize's own exclusive lease. |
| `--dry-run` | bool | `false` | Resolve and trace the plan; performs no write. |

### Resolution algorithm

1. Resolve tenant/environment/`EnvConfig` (`ResolveOpen`, the same resolver every other typed command uses).
2. Resolve the target `RuntimePodResources` for the runtime pod:
   - `--apply-recommendation`: load the standing recommendation the same way `erun list` does (`LoadRuntimeUsageHistory` + `RecommendRuntimeSizing`, scoped to **this process's own** cache directory — see [Idle policy § activity leases](/agent-reference/idle-policy#activity-leases) for why that is in-pod-only for a remote/runtime environment). For each verdict whose `action` is `raise` or `lower`, adopt its `suggested` value; a verdict of `hold`/`insufficient-evidence` leaves that resource unchanged. No recommendation available → error. `RecommendRuntimeSizing` derives its verdicts from cgroup counters read out of the container it runs inside — the `erun-devops` container, not the `erun-dind` sidecar next to it in the same pod — so there is no standing recommendation for the sidecar today; covering it would mean exec'ing into it specifically and retaining a second usage history.
   - Explicit `--cpu`/`--memory`: merge onto the current `EnvConfig.runtimepod`, matching `erun init`'s own merge semantics for the same field.
3. Resolve the target `RuntimePodResources` for the `erun-dind` sidecar, independently: explicit `--dind-cpu`/`--dind-memory` merge onto the current `EnvConfig.runtimedindpod` the same way. Both targets normalize and validate (`ValidateRuntimePodResources`/`ValidateRuntimeDindPodResources`), then validate together against `EnvConfig.namespacequota` if one is set: a `ResourceQuota` counts every container in the pod, so the runtime pod's target CPU/memory plus the sidecar's own *resolved* target (not a fixed constant — a `--dind-cpu`/`--dind-memory` in the same call moves it) must not exceed the quota. A violation errors naming the resource, the sidecar's share, and how much is actually available to the runtime container.
4. If both resolved targets equal their current recorded values, stop: report a no-op, no lease check, no deploy.
5. Load every currently held activity lease for the environment (`LoadEnvironmentActivityLeases` — plain and exclusive alike, the same predicate the desktop's own AI-session spawn uses to decide occupancy). Any result and `--override-lease` unset → refuse, naming every holder (`EnvironmentActivityLeaseHolder.String()` plus the lease's `name`). An override is traced explicitly when used.
6. `--dry-run` stops here, after tracing the per-resource `current -> target` lines (`cpu`/`memory` for the runtime pod, `dind-cpu`/`dind-memory` for the sidecar) and the occupancy decision.
7. Take an exclusive lease (scope `worktree`, name `resize`) for the duration of the write — this is what a *second, concurrent* resize call collides with, distinct from step 5's occupancy check against other workers. Persist `EnvConfig.runtimepod`/`EnvConfig.runtimedindpod` to the resolved targets, then run the same deploy composition `erun deploy` uses with no explicit version override (redeploys the environment's own recorded `runtimeversion`), so the runtime chart's `--set-string runtime.resources.limits.cpu/memory` and `runtime.dind.resources.limits.cpu/memory` rerender with the new values and the `Recreate`-strategy Deployment rolls exactly once. Release the lease when the deploy finishes (or fails).

### What moves and what doesn't

| Quantity | Affected by `resize`? |
|---|---|
| Runtime container's `resources.limits.cpu`/`.memory` (the throttle/OOM ceiling) | Yes, via `--cpu`/`--memory` or `--apply-recommendation`. |
| `erun-dind` sidecar's `resources.limits.cpu`/`.memory` | Yes, via `--dind-cpu`/`--dind-memory` — independent of the runtime pod's own limits and combinable with them in one call. |
| The namespace `ResourceQuota`'s draw from this environment | Yes, indirectly: quota accounting is limits-based and counts both containers. |
| Runtime container's and sidecar's `resources.requests` (what the scheduler reserves) | No — pinned to small fixed defaults (`DefaultLimitRangeDefaultRequestCPU`/`Memory`, `DefaultRuntimeDindRequestCPU`/`Memory`) independent of this command. |
| Any PVC (home, docker state, worktree) | No — PVC sizes are chart literals today, not values-driven. |

### Error behaviour

| Failure | Behaviour |
|---|---|
| Tenant/environment can't be resolved. | Errors before any read or write. |
| None of `--cpu`/`--memory`/`--dind-cpu`/`--dind-memory`/`--apply-recommendation` given. | Errors naming what to pass instead. |
| Both `--apply-recommendation` and explicit `--cpu`/`--memory` given. | Errors naming the conflict. `--dind-cpu`/`--dind-memory` may still be combined with `--apply-recommendation` in the same call — they size a different resource with no recommendation of its own. |
| `--apply-recommendation` with no retained history for this environment. | Errors, and names the explicit-values fallback. |
| Resolved runtime-pod target plus the sidecar's resolved target would exceed `EnvConfig.namespacequota`. | Errors naming the resource, the sidecar's share, and the remainder actually available. |
| Another holder's lease is present (`LoadEnvironmentActivityLeases` non-empty) and `--override-lease` is unset. | Errors naming every holder (`orchestrator`, `user`, lease `name`). |
| A second resize is already running (`TakeEnvironmentActivityLease` with `Exclusive: true` conflicts). | Errors naming that holder. |
| The deploy step fails (chart resolution, helm rollout). | Errors as `erun deploy` would for the same failure; `EnvConfig.runtimepod`/`runtimedindpod` have already been persisted to the new values at this point, since deploy is what makes them live and a retry should redeploy the same target rather than resolve a stale one. |
| Both resolved targets equal their current recorded values. | No-op: reports "already sized" (naming all four current values), takes no lease, and does not deploy. |

---

## `erun whip` {#erun-whip}

Pushes the pacing nudge into every reachable target: every configured environment's own AI session (over that environment's MCP `whip` tool — see [MCP overview § `whip`](/mcp/overview#whip)) plus every persisted orchestrator (`ERunConfig.orchestrators`). Same population-agnostic decide/report core (`eruncommon.DecideWhip`/`WhipReport`) the desktop's automatic pacing reconciler and the MCP tool both use.

### Flags

| Flag | Type | Default | Effect |
|---|---|---|---|
| `--tenant <t>` | string | unset | Whip only this environment; requires `--environment`. |
| `--environment <e>` | string | unset | Whip only this environment; requires `--tenant`. |
| `--dry-run` | bool | `false` | Still calls each reachable environment's `whip` tool, with `preview: true`, so the report reflects a real decision without ever writing into the session. |
| `--json` | bool | `false` | Emit the full `WhipReport` as JSON. |

### Resolution algorithm

1. If both `--tenant`/`--environment` are given, the target list is that one pair. If neither is given, list every environment across every configured tenant (`ListTenantConfigs` + `ListEnvConfigs`) — never the ambient current-directory default a bare `resolveOpen` would resolve to. Passing only one of the two errors.
2. For each environment target: resolve its MCP edge the same way `erun idle`/`erun exec` do (a local port-forward state file `erun open` maintains while the environment is open). An edge that cannot be resolved, or whose call fails, resolves to `{decision: none, reason: "not-alive"}` — not a command failure. A resolved edge is called with `whip {preview: <ctx.DryRun>}`; the decoded `eruncommon.WhipResult` is used verbatim.
3. Load `~/.erun/config.yaml`'s `Orchestrators` list and turn each into a candidate via `eruncommon.ListWhipOrchestratorCandidates` (always `Reachable: false`), then `eruncommon.DecideWhip` against the resolved `WhipConfig` with `explicit: true`. Every orchestrator therefore always resolves to `{decision: none, reason: "unreachable-from-transport"}` from this transport.
4. Render one line per result (`candidate.id`/name, decision, reason, and the write error if any), or the full `WhipReport` JSON with `--json`.

### `WhipReport` shape

```jsonc
{
  "dryRun": false,
  "results": [
    {
      "candidate": { "kind": "environment", "id": "myapp/dev", "name": "myapp/dev", "reachable": true, "alive": true, "lastActiveAt": "...", "nudgeCount": 1, "capped": false },
      "decision": 1,        // 0 none, 1 nudge, 2 cap
      "reason": "nudge",    // not-alive | unreachable-from-transport | fresh | already-capped | cap-crossed | nudge
      "pushed": true
    },
    {
      "candidate": { "kind": "orchestrator", "id": "eng-1", "name": "Eng One", "reachable": false, "alive": false, "nudgeCount": 0, "capped": false },
      "decision": 0,
      "reason": "unreachable-from-transport",
      "pushed": false
    }
  ]
}
```

### Configuration: `ERunConfig.whip` {#whip-config}

`~/.erun/config.yaml`'s optional `whip` section (`eruncommon.WhipConfigOverride`) overrides the pacing defaults every surface reads through `eruncommon.ResolveWhipConfig`:

| Key | Type | Unset behaviour |
|---|---|---|
| `message` | string | `eruncommon.DefaultWhipMessage` (the built-in pacing text) |
| `staleafterseconds` | int | `eruncommon.DefaultWhipStaleAfter` (600 = 10 minutes) |
| `maxnudges` | int | `eruncommon.DefaultWhipMaxNudges` (6) |
| `autoenabled` | bool | `true` — gates only the *automatic*, schedule-driven pass; an explicit whip (this command, the MCP tool's default, or a future in-app row action) always ignores it |

Every field is a pointer in the on-disk override so "unset" (keep the default) is distinguishable from an explicit zero/false. The desktop's automatic reconciler re-reads this section once per tick (no rebuild or restart needed); this command and the MCP tool read it fresh on every invocation.

### Error behaviour

| Failure | Behaviour |
|---|---|
| Only one of `--tenant`/`--environment` given. | Errors naming the conflict; nothing is read or pushed. |
| An environment's MCP edge cannot be resolved or called. | That target resolves to `{decision: none, reason: "not-alive", error: "<the underlying error>"}`; the command still exits 0. |
| A persisted orchestrator. | Always `{decision: none, reason: "unreachable-from-transport"}`; the command still exits 0. |
| No environments and no orchestrators configured. | Exits 0 with `results: []` (or omitted under `--json` if empty). |

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

## `erun inputs`

`erun inputs upload` is the inverse of `erun outputs download`: it streams a file from this host into an environment's runtime pod over `kubectl exec -i` (stdin), never through argv or a base64 blob in a tool argument. It has no in-pod MCP counterpart — the edge runs in the pod and has no path back to the operator's filesystem — but an MCP-connected orchestrator reaches the same transfer through the `inputs_upload` local tool `erun mcp proxy` serves (see [MCP overview § Host-served](/mcp/overview#host-served)).

### `erun inputs upload`

| Flag | Type | Default | Effect |
|---|---|---|---|
| `<local-path>` (arg) | path | **required** | File on this host to upload; must exist and not be a directory. |
| `<remote-path>` (arg) | absolute path | **required, never defaulted** | Full destination inside the pod, including the file name. Must be absolute and free of `..`. Not defaulted deliberately: a transfer can never silently land somewhere a background process (e.g. the workspace-sync mirror) reconciles away. |
| `--tenant <t>` | string | current scope | Target tenant. |
| `--environment <e>` | string | current scope | Target environment; requires `--tenant`. |

The remote script creates the destination directory if missing, writes to a same-directory temp file, and renames into place — so a killed transfer never leaves a partial file visible at the final path — then reports the written size and SHA-256. The command errors if that checksum (or size) disagrees with what was sent. `--output json` emits `{remotePath, bytes, sha256}`. `--dry-run` traces the `kubectl exec` argv and the upload script without sending anything (the local file must still exist to resolve its size).

---

## `erun cloud refresh`

`erun cloud refresh TENANT ENVIRONMENT` re-injects the operator's short-lived AWS credentials into an environment's runtime pod. It is the non-leaking counterpart to the `cloud_inject_aws_credentials` MCP tool: the credential values are never inputs, so an Agent or a script can call it without writing a secret into a transcript.

| Argument / flag | Type | Default | Effect |
|---|---|---|---|
| `TENANT` (arg) | string | **required** | Target tenant. No default-scope fallback — the target is always explicit. |
| `ENVIRONMENT` (arg) | string | **required** | Target environment. |
| `--dry-run` | bool | `false` | Resolve the plan, trace the deployment wait, the `kubectl exec` argv, and the write script, and exit without exporting credentials or touching the pod. |
| `--output` | `text` \| `json` | `text` | Global mode flag. |

Algorithm:

1. Resolve the environment. Read `EnvConfig.cloudprovideralias`; abort `no AWS cloud provider alias` when empty.
2. Resolve the alias in the root config; abort when it is not configured, or when its `provider` is not `aws`.
3. Resolve the AWS region (managed cloud context → kubeconfig context name → the alias's `ssoregion` → the region in an ECR registry host). An unresolved region is traced as `<unresolved>` and simply omitted from the written profile; it is not an error.
4. `kubectl wait --for=condition=Available deployment/<tenant>-devops` (2 minute timeout).
5. Trace the `kubectl exec -i … /bin/sh -lc <script>` argv and the script body. Under `--dry-run`, stop here.
6. `aws configure export-credentials --format process --profile <alias profile>` to mint the credentials. A failure names `erun cloud login --alias <alias>`, the usual cause being a lapsed SSO session.
7. Render the `[erun-host]` profile block (access key, secret, session token, resolved region, and an `x_erun_expiration` marker `doctor` reads back) and stream it to the pod on the exec's **stdin**. The script drops any existing `[erun-host]` section before appending, so a repeat refresh overwrites in place; every other profile in `~/.aws/credentials` is preserved, and the file is left `0600`.

The credential material never appears in an argument, a trace line, or a golden file. The write script is a constant — it carries the profile name, not the values — so tracing it in full is safe.

| Failure | Behaviour |
|---|---|
| Environment carries no AWS alias. | Exit 1 before any cluster call; names `erun cloud set <tenant> <env> --alias <alias>`. |
| Alias not configured in the root config. | Exit 1, `cloud provider alias … is not configured`. |
| Alias is a Cloudflare alias. | Exit 1, `host credential refresh applies to AWS aliases only`. |
| Credential export fails (expired SSO). | Exit 1; the error names `erun cloud login --alias <alias>`. The pod's existing profile is untouched. |
| Runtime deployment not Available. | Exit 1 at the wait; nothing is written. |

`erun open` runs the same refresh for any environment with an AWS alias, after its deployment-presence check and after the wake that follows it (the credentials are written into the running pod, so there is nothing to write to until it is up). There it is **best-effort**: a failure is traced as a warning and the session still opens, because a lapsed SSO session degrades the environment but is not a reason to withhold the shell. `erun deploy` deliberately does **not** refresh — it is a pure primitive driven by orchestrators and `erun release`, often with no operator present and against environments nobody is about to use; the credentials file lives on the home PVC and survives the pod replacement a deploy causes, so a deploy invalidates nothing that a refresh would fix.

---

## `erun release`

`erun release` orchestrates **build → push → git-tag**, in that order: it stamps the version and creates the commit and a local tag, builds the release-tagged images, reuses [`erun push`](#erun-push) to publish the multi-arch image manifest **and** the runtime chart at the release version, re-resolves each published manifest, and only then pushes the tag and branches. It has no chart-publishing step of its own. The ordering is the contract — a release that exits `0` means the announced version is deployable, and one that cannot publish fails with nothing public. See [Release version policy](/agent-reference/release-policy) for the version-pattern rules and the publishing contract; the `erun release` flag set is just `--dry-run` and `--output`, and is documented on the [Operator page](/cli/release).

---

## `erun stop`

`erun stop` scales an environment's runtime Deployment to zero, returning the runtime container's
resource limits **and** its unlimited `dind` sidecar's real consumption to the node. It is the
counterpart to `erun open`, which is the only thing that starts an environment again. There is
deliberately **no MCP `stop` tool**: the env's MCP edge runs inside the runtime container, so
stopping over MCP would kill the caller mid-call. Lifecycle is host-side, as it always has been for
`deploy` and `open`.

### Flags

| Flag | Type | Default | Effect |
|---|---|---|---|
| `--tenant <name>` | string | current scope | Target tenant. |
| `--environment <name>` | string | current scope | Target environment. |
| `--dry-run` | bool | `false` | Trace every action and decision; perform no side effect. |
| `--output json` | string | `text` | Emit the structured result on stdout (see below). |

### `erun stop` lifecycle algorithm

1. Resolve the target the same way `erun open` does (positional args, then `--tenant`/`--environment`, then the current scope). A missing Kubernetes context aborts.
2. **No cloud-context preflight.** Unlike every other cluster-touching command, `stop` never starts a stopped [cloud context](/concepts/cloud-contexts) to reach the cluster.
3. Read the runtime Deployment's `spec.replicas` / `status.readyReplicas`. An absent Deployment aborts with `RUNTIME_NOT_DEPLOYED`.
4. If the Deployment is already at `0` replicas, skip to step 8 — steps 5–7 are the "this run actually reclaims capacity" path.
5. **List the attached desktop sessions.** `kubectl exec deployment/<tenant>-devops` runs the same in-pod session heartbeat probe the desktop app polls, and the ids it reports are traced and returned as `endedSessions`. They live in the pod, so the stop ends them; naming them makes that a stated consequence rather than tabs mysteriously going dark. An unreadable probe is traced and the stop continues — it is reporting, not a precondition.
6. `kubectl scale deployment/<tenant>-devops --replicas=0`.
7. **Confirm the stop took effect.** Re-read `spec.replicas`. Anything other than `0` aborts with `STOP_NOT_APPLIED` *before* the config write, so `EnvConfig.stopped` never claims a stop the cluster did not keep and the command never reports success for a stop that did not happen. Skipped under `--dry-run`, which traces the check instead.
8. If `EnvConfig.stopped` is not already `true`, set it. This is the durable half: a bare scale patch is drift that the next `helm upgrade` reverts, so `deploy` renders the chart's `stopped` value from this field and reconciles `replicas` declaratively.
9. Emit `==> Stopped <tenant>/<env>` and exit `0`.

### Durability and the interaction with `deploy`

| Sequence | Result |
|---|---|
| `stop` → `open` | The environment starts. `open` clears `EnvConfig.stopped` and scales the Deployment back to `1`. |
| `stop` → `open --reconnect` | The environment **stays stopped**. The reconnect aborts with `RUNTIME_STOPPED`; nothing is scaled and `EnvConfig.stopped` stays set. This is the sequence the desktop app produces on its own — a stop drops every attached session and each tab respawns `open` — so it is what makes a stop hold for an environment somebody has open. |
| `stop` → `deploy` | The environment **stays stopped**. `deploy` threads `--set stopped=true`, so the chart renders `replicas: 0`. `deploy` installs a version; it does not decide whether the environment should be running. |
| `stop` → `deploy` → `open` | The environment starts, on the version `deploy` installed. |

Every automatic stop inherits the same protection, because it is `open` that refuses rather than `stop` that defends. An idle-stop that scales an environment down records the same `EnvConfig.stopped` and drops the same sessions, and the reconnects it triggers decline for the same reason. The [idle stop](/agent-reference/idle-policy) that ships today stops the whole [cloud context](/concepts/cloud-contexts) rather than one Deployment, and it is covered by the same flag one layer up: `--reconnect` skips `open`'s cloud-context start, so a reattach cannot restart the machine an idle policy just stopped.

Making `deploy` a wake would have been unreliable in one specific way: `deploy` skips the helm call
when the released version already matches, so a wake-on-deploy would fire or not depending on
whether anything changed. Reconciling the recorded intent instead is consistent whether the helm
call runs or is skipped.

### `--output json` result

```json
{
  "tenant": "my-tenant",
  "environment": "rihards-dev",
  "namespace": "my-tenant-rihards-dev",
  "release": "my-tenant-devops",
  "kubernetesContext": "my-cluster",
  "stopped": true,
  "alreadyStopped": false,
  "endedSessions": ["open-0", "ai"]
}
```

`alreadyStopped` distinguishes the no-op from the run that actually reclaimed capacity. `endedSessions` lists the desktop terminal sessions that were living in the pod and went down with it, omitted when there were none or when the run was a no-op.

### What survives

The `/home/erun` PVC (workspace, agent config, outputs, credentials), the docker-state PVC (image
store and build cache), and a `local-agent` env's hostPath worktree are all untouched, so waking is
a pod start rather than a cold rebuild. In-pod processes are not: a stop ends whatever was running.

### Error codes

| Code | Cause | Exit code |
|---|---|---|
| `ENVIRONMENT_NOT_FOUND` | Resolved tenant/environment has no config. | `1` |
| `KUBE_CONTEXT_MISSING` | `EnvConfig.kubernetescontext` is empty or absent from `~/.kube/config`. | `1` |
| `RUNTIME_NOT_DEPLOYED` | The runtime Deployment does not exist in `<tenant>-<env>`. Nothing is holding capacity, and nothing is changed. | `1` |
| `KUBE_SCALE_FAILED` | `kubectl scale` returned non-zero. `EnvConfig.stopped` is **not** recorded, so the config never claims a stop that did not happen. | `1` |
| `STOP_NOT_APPLIED` | The scale returned zero but the confirming re-read found the Deployment still asking for replicas. `EnvConfig.stopped` is **not** recorded and the command does **not** report success — a stop that silently did not take effect is the one failure the Operator cannot see for themselves. | `1` |

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

## `erun job` {#erun-job}

`erun job` starts long work in an environment and answers what happened to it. The work always runs in the environment, wherever the command is typed: inside the environment the verbs act on its store directly, and from anywhere else they act through the environment's MCP edge, which needs the port-forward `erun open` establishes. The `job_*` [MCP tools](/mcp/overview#job-tools) are that same surface reached directly, over the same shared implementation. Paths are the environment's — `--dir` and the reported log path resolve inside it, and a pid names a process in the environment.

Use it instead of hand-rolling detachment, a log redirect, a polling loop, a sentinel token, and an exit-code parse around [`erun exec raw`](/cli/exec) / the `raw` MCP tool. Three properties are the reason it exists:

- **The outcome is captured inside the environment**, by the supervisor process that waited on the work. It is never derived from a token in the log and never passes through an intermediate shell, so `$?` cannot be expanded in the wrong place.
- **Liveness is a probe of a recorded pid**, never a match against a command line. A pattern can match the polling shell itself, or the shell issuing a cancel.
- **A status is definite or explicitly `unknown`**, never silently partial.

### Job states

| State | Meaning | `exitCode` |
|---|---|---|
| `running` | The recorded process is alive and no outcome has been observed. | `null` |
| `exited` | The work finished and the supervisor captured its status. `-1` means it was terminated by a signal, which `signal` names. | integer |
| `abandoned` | The job's own process exited and the supervisor captured its exit status, but it left something it spawned still running in its process group — background work started and never waited for, e.g. a gate a job backgrounded and then exited past. `reason` describes it. Never a success, whatever `exitCode` says. | integer |
| `gate-incomplete` | The job's own process exited, but a job *it started* (via `job start`, e.g. a gate run through `agent-gate.sh`) had not reached a verdict even after the supervisor waited for it (see below). `reason` names the still-running job(s). Never a success, whatever `exitCode` says. | integer |
| `unknown` | The record outlived whatever was meant to finish it — the supervisor is gone without recording an outcome (most often because the runtime pod was replaced), or an attached job's tracked process is gone. `reason` says which. | `null` |

The demotion to `unknown` happens on the next read and is persisted, so every later read gives the same answer. An `unknown` job is never a success: `job await` exits `125` for it, distinct from both `0` and a failure.

`abandoned` sits between the two: like `exited`, the supervisor did observe the process end and recorded a real `exitCode` for it; like `unknown`, it is never a success — even an `exitCode` of `0` is not one, because something the job started is still running and nothing will ever report on it again. Detection happens once, right after the supervisor reaps the job's own process, by checking whether its process group still has a live member; that check is POSIX-only, so on Windows a job that backgrounds work this way still reads back as a plain `exited`. `job status`/`job await` render it as `abandoned <exitCode>: <name> (<reason>)`, distinct from both `exited <exitCode>: <name>` and `unknown: <name> (<reason>)`.

Every state line names why it ended when there is anything recorded to say, `exited` included: `exited <exitCode>: <name> (signal <signal>)` when the work was signalled (the signal *is* the reason), `exited <exitCode>: <name> (<reason>)` otherwise when the record carries one, and the bare `exited <exitCode>: <name>` only when it carries neither. `job await`'s own failure message follows the same rule — `job "<id>" exited <exitCode> (signal <signal>)` or `job "<id>" exited <exitCode>: <reason>`. A job whose work could not be started at all (`failed to start: …`) is the case this matters most for: its exit code is `-1` and the reason is the entire answer.

`gate-incomplete` is `abandoned`'s sibling for a different shape of leftover work: not a process in the job's own process group, but a *sibling job record* — one started through `job start` from inside this job's own work, most commonly an agent running its `make check` gate through `agent-gate.sh` (which detaches the gate as its own job precisely so it survives the caller). That detachment is also what makes `abandoned`'s process-group check blind to it: the child job runs in its own session on purpose. Every job's own process carries `ERUN_JOB_ID` naming the job it is running as, so a nested `job start` reached from inside it — directly, or through anything it spawns — records that value as its own `startedByJobId`.

That inheritance only holds for a plain nested subprocess (a Bash tool calling the erun CLI directly). A start forwarded through the environment's MCP edge instead crosses into that server's own long-lived process, which was never itself started as anyone's job and so has no `ERUN_JOB_ID` to inherit, however deep the logical nesting is on the calling side — `exec_agent`/`exec_raw` (with `wait: false`) and every [job-envelope tool](/mcp/overview#job-envelope) (`build`, `deploy`, `doctor`, and the rest, with `wait: false`) accept an explicit `startedByJobId` field for exactly this case. The erun CLI's own off-environment `job start` fills it in automatically from its own `ERUN_JOB_ID` when forwarding; a caller reaching the MCP tools directly sets it itself if it wants the linkage.

Nothing is guessed when it is absent. A caller driving an environment from outside any job at all is genuinely parentless, and attributing its work to whichever job happens to be running there would be a definite answer to an unknown question — so an unlinked job records no parent, and its outcome is simply nobody's finish check to wait on.

`startedJobFailed` names the *latest* attempt under a given `--name`, not every failure that name has ever seen: `agent-gate.sh` folds the working tree and command into the generated `--id`, so an agent that fixes what a gate found and reruns it gets a fresh id under the same `--name`, and the earlier failing attempt's record is never replaced (only reusing an id outright does that). A later attempt under the same name supersedes an earlier failure once it starts, whether or not it has finished yet, so a stale failure from a fixed-and-rerun gate never haunts the parent's own outcome once the current attempt has gone green.

When a job's process ends, the supervisor checks the job store for any non-[handoff](#job-handoff) job naming it as `startedByJobId` that has not finished, and **waits for it** rather than deciding the outcome on the spot — polling the started job's record until it finishes or a generous cap elapses (24 hours by default; tunable per process via `ERUN_JOB_GATE_INCOMPLETE_WAIT_CAP`, a `time.ParseDuration` string). Nothing is holding a connection or an orchestrator's turn open on the other end of this wait — the supervisor is already a detached background process — so waiting out even the longest gate costs nothing the way holding an interactive `job await` call open would.

- If the wait ends because the cap elapses with the started job still running, the outcome is `gate-incomplete`, even at `exitCode: 0`. This is what turns "an agent ended its turn while the gate it started was still running" from a silent, unnoticed success into a state an orchestrator can poll for.
- If the wait ends because the started job finished, its own outcome is folded into this job's record instead: `state` stays whatever this job's own process produced (usually `exited`), and `startedJobFailed` names the started job if — and only if — it did **not** succeed. `succeeded` is `false` whenever `startedJobFailed` is set, regardless of this job's own `exitCode`. This is the common case in practice: a gate that runs long but eventually passes or fails is waited out and reported truthfully, rather than ever surfacing a misleading intermediate `gate-incomplete` a caller would have to separately chase down.

`job status`/`job await` render `gate-incomplete` as `gate-incomplete <exitCode>: <name> (<reason>)`, and append `, <startedJobFailed text>` to an otherwise-`exited`/`abandoned` line when `startedJobFailed` is set.

### Bounded reinvocation for an agent job {#job-reinvocation}

`gate-incomplete`/`startedJobFailed` tell an orchestrator polling from outside the truth, but a one-shot `--agent` run's own process has already exited by the time either is recorded — nothing wakes it to act on what it started. For an agent job specifically (never a plain command job, and never for a plain nonzero exit with no started work involved), erun closes that gap itself: before finalizing `gate-incomplete` or `startedJobFailed`, it resumes the same tool session with the concrete outcome, giving the agent a real turn to fix it, verify it, or explain why it cannot be resolved.

This is a **resumption of the same conversation**, not a fresh, context-free retry: `claude -p --resume <session-id>` / `codex exec resume <thread-id>` carry the tool's own prior context forward, verified live for Claude (a fact told in one process was correctly recalled from a wholly separate resumed process). It only runs when a session id was actually captured from the tool's own event stream (`session_id` on every Claude stream-json event, `thread_id` on Codex's `thread.started` event) — an agent job with no captured session id gets the plain, unresumed outcome exactly as before this existed.

| Field | Type | Meaning |
|---|---|---|
| `reinvocationCount` | integer | How many bounded follow-up turns already ran for this job. `0` for a command job and for an agent job that never needed one. |

Two independent caps bound the chain, both on the job's own record so neither can be extended by anything a reinvoked turn itself starts:

- A fixed count, `EnvironmentJobMaxReinvocations` (default `2`), overridable per supervisor process via `ERUN_JOB_MAX_REINVOCATIONS`.
- A wall-clock budget across the whole chain, `EnvironmentJobReinvocationBudget` (default `30m`), overridable via `ERUN_JOB_REINVOCATION_BUDGET` (a `time.ParseDuration` string), struck once before the first turn rather than reset per turn.

Once either cap is reached, the job finalizes exactly as it would without this feature — `gate-incomplete` or `startedJobFailed`, `succeeded: false` — except `reason` now says so explicitly (`"... (already resumed N time(s) without a clean outcome; the reinvocation bound is exhausted)"`), distinct from a job that never got a reinvocation at all. `job status` also appends `, resumed N/M time(s)` to the rendered line for any job with `reinvocationCount > 0`, running or finished.

### Deliberate handoff: `--handoff` {#job-handoff}

Not every job a job starts is meant to be waited for. `job start --handoff` marks the new job as deliberately outliving whatever starts it — a release, a long render, anything an agent kicks off on purpose before ending its own turn. A handoff job is excluded from its parent's finish check entirely: it is never counted toward `gate-incomplete`, and its own eventual outcome (success or failure) is never folded into `startedJobFailed`. Without `--handoff`, *every* nested `job start` defaults into the wait-then-report behavior above, which is correct for a gate but wrong for work genuinely meant to keep running past the caller's own turn.

### The alive contract {#alive-contract}

`state` alone answers "did the work finish", but it can only be as fresh as the last thing that read and reconciled the record — a pid-liveness check runs only when something calls `status`/`await`/`output`. A supervisor can also die between reads (a `SIGTERM`'d container, an OOM kill) with nothing to say so from the inside until the next reconcile. To close that gap every job record carries three more fields, written by the supervisor on a fixed cadence independent of the work's own output:

| Field | Type | Meaning |
|---|---|---|
| `lastAliveAt` | RFC3339 timestamp | The supervisor's own clock timestamp at its last beat, stamped every ~1 second (`EnvironmentJobAliveHeartbeatInterval`) for as long as the supervisor runs — an image pull or a silent test suite beats exactly as often as a chatty one. |
| `aliveSeq` | integer | A monotonic counter bumped on every beat, so a caller can distinguish "still beating" from "the same timestamp read twice" at second resolution. |
| `aliveAgeMs` | integer or `null` | Computed fresh on every read as `now − lastAliveAt`, **in the reader's own process, using the same clock `lastAliveAt` was stamped with** — never a caller subtracting its own wall clock from a pod timestamp, which a few seconds of skew would turn into a false failure against a 5-second bound. `null` only when the job has never beaten: an attached job (no supervisor loop exists for it) or one whose supervisor has not registered its first beat yet. |

**The caller rule:** once `aliveAgeMs` exceeds `5000`, stop waiting and treat the job as failed — report it as an `unknown` outcome, never as a success and never as the tool itself having errored — even if `state` still reads `running`. 1 second of beat cadence against a 5 second bound is 5× headroom for poll jitter and scheduling delay, not slack for the beat itself to run late by design. A silent-but-healthy command never trips this: the beat has nothing to do with `outputBytes`.

In practice the two signals — `state` and `aliveAgeMs` — usually agree, because the same pid-liveness check that would demote `state` to `unknown` also stops finding a live supervisor at the same moment beats stop landing. `aliveAgeMs` matters when a caller cannot afford to wait for the next reconcile, or when a reused pid could otherwise pass a liveness probe without actually being the job's supervisor: verified end to end by killing a real supervisor process with `SIGKILL` and confirming `aliveAgeMs` exceeds `5000` within ~6 seconds of the kill, with `state` landing on `unknown` in the same window.

### `erun job start`

`erun job start [flags] -- <command> [args...]`

| Flag | Type | Default | Effect |
|---|---|---|---|
| `--tenant <t>` / `--environment <e>` | string | **required** | Target environment. |
| `--name <what>` | string | **required** | What the work is. Shown wherever the environment reports as busy. |
| `--id <id>` | string | the `--name` | Handle to address the job by, filename-sanitised. |
| `--dir <path>` | path | the caller's resolution | Working directory for the work. |
| `--agent <tool>` | string | none | Run an AI tool instead of a command; the trailing arguments are the prompt. `claude` or `codex`. See [Agent jobs](#agent-jobs). |
| `--max-output-bytes <n>` | int64 | `4194304` (4 MiB) | Cap on captured output. |
| `--lease-ttl <duration>` | duration | `15m` | Activity lease TTL; the supervisor renews at TTL/3 (minimum 5s) for as long as the work runs, and at 2s intervals for an agent job so the lease's name can carry the current activity. |
| `--handoff` | bool | `false` | Mark this job as deliberately meant to outlive whatever starts it, excluding it from that job's own finish check. See [Deliberate handoff](#job-handoff). |
| `--dry-run` | bool | `false` | Trace the supervisor argv, the log path, and the lease; start nothing. |

erun spawns a supervisor in **its own session**, so the work survives this call returning, the caller exiting, and the transport dropping — nothing needs wrapping in `setsid`, `nohup`, or a redirect. The work itself runs in its own process group, which is what lets [`cancel`](#erun-job-cancel) reach it without touching the supervisor.

The supervisor registers the job before starting the work, so `start` returns a handle that always resolves — including for work that fails to `exec`, which lands as an `exited` job whose `reason` says `failed to start: …`.

Re-using the id of a job that is **still running** is refused (two supervisors writing one record would make every later answer ambiguous). Re-using a **finished** id replaces it, and traces that it did.

`--output json` emits the job record.

### Agent jobs {#agent-jobs}

An AI tool run non-interactively prints **nothing** until it exits, so a multi-hour agent job started as a plain command reports `outputBytes: 0` for its whole life while it is actively editing files. `--agent` makes the run a job *kind* instead of an opaque command: erun builds the tool's streaming invocation, and the supervisor folds the resulting event stream into one normalized progress view.

| `--agent` | Command erun runs | Stream folded |
|---|---|---|
| `claude` | `claude -p <prompt> --output-format stream-json --verbose` | `assistant` events (text blocks and `tool_use` blocks), `result`. |
| `codex` | `codex exec --json <prompt>` | `turn.started` / `turn.completed` / `turn.failed`, `item.started` / `item.updated` / `item.completed` (`agent_message`, `reasoning`, `command_execution`, `file_change`, `mcp_tool_call`, `web_search`), `error`. |

`--agent` and a trailing command are mutually exclusive: passing both fails with `an agent job runs the tool's own streaming invocation; pass a prompt, not a command`. An unsupported tool fails with `unsupported agent tool "<name>": expected one of claude, codex`.

The job record then carries three extra fields:

| Field | Meaning |
|---|---|
| `kind` | `command` (default) or `agent`. |
| `agentTool` | The tool the run invokes; empty for a command job. |
| `progress` | The normalized view below. Absent until the run emits its first event, so an agent that has not started is never reported as an idle one. |

```jsonc
"progress": {
  "tool": "claude",
  "activity": "editing erun-common/mcp_client.go", // last tool + its target; cleared once the run reports a result
  "lastTool": "Edit",                              // erun's tool vocabulary, not the vendor's event name
  "lastTarget": "erun-common/mcp_client.go",
  "turns": 12,                                     // distinct assistant turns
  "toolsRun": 47,                                  // completed tool calls
  "events": 133,                                   // stream events folded, so a caller can tell the stream is alive
  "lastMessage": "Rewriting the reconnect path.",  // most recent thing the agent said
  "result": "",                                    // the run's own closing summary; never a substitute for exitCode
  "error": ""                                      // the last error the stream reported
}
```

`activity` is normalized from the tool's own vocabulary into a fixed verb set — `editing`, `reading`, `running`, `searching`, `fetching`, `delegating to`, `thinking`, `calling`, and `using <tool> on` for anything else — so a codex `file_change` item and a claude `Edit` tool call both read `editing <path>`. Every free-text field is truncated to 240 characters. **This normalization is the contract**; the raw vendor events are only in the job log, and a vendor reshaping its stream changes what erun parses, not what a caller reads.

The supervisor folds the stream every 2 seconds and rewrites the record only when progress moved, so polling `job status` is cheap. It also renames the job's activity lease to `<name>: <progress summary>`, which is what lets the desktop show `editing erun-common/mcp_client.go` where it would otherwise show only that something is running — see [Idle policy · activity_leases](/agent-reference/idle-policy#activity-leases).

`job output` needs no special handling for an agent job: the log is the event stream, so the existing incremental read returns events while the agent works.

### Working tree checkpoints {#worktree-checkpoints}

An agent job's exit status says nothing about the state of the working tree it ran in — a clean `exited 0` and a tree with a thousand uncommitted lines are otherwise indistinguishable. When an [agent job](#agent-jobs) (`kind: agent`) with a `--dir` finishes, the supervisor checks that directory's git state once, before the record is written, and folds what it found into seven more fields:

| Field | Type | Meaning |
|---|---|---|
| `worktreeDirty` | bool | `true` only when `--dir` was a git working tree with uncommitted changes (tracked or untracked, respecting `.gitignore`) at the moment the job ended. `false` — meaning absent from the record — covers every other case identically: a command job (never checked), an agent job with no `--dir` or a `--dir` outside a git repo, and an agent job that left its tree clean. |
| `worktreeBranch` | string | The branch HEAD pointed at when the check ran; the literal `"HEAD"` when detached. Empty when `worktreeDirty` is `false`. |
| `worktreeDetached` | bool | `true` when HEAD was not on a branch at all. |
| `worktreeCommit` | string | The checkpoint commit the supervisor made, empty when it made none. |
| `worktreePushed` | bool | Whether `worktreeCommit` reached `worktreeRemote`. A commit that exists only in the working tree is exactly as exposed to a lost pod as the uncommitted changes it was meant to save. |
| `worktreeRemote` | string | The remote `worktreeCommit` was pushed to (`origin`), empty when nothing was pushed. |
| `worktreeReason` | string | Why no checkpoint was made, or why one was made but not pushed. Empty when `worktreeDirty` is `false`, or when a commit was made and pushed cleanly. |

When the tree is dirty, the supervisor does not just report — it makes a machine-authored checkpoint commit (message: `WIP: checkpoint by the erun job supervisor`, explicitly inviting the reader to rewrite or squash it) and pushes it to `origin`, because the agent that would otherwise do this by hand is already gone by the time anyone reads the record. It only does this where committing is actually safe:

| Condition | What happens |
|---|---|
| HEAD is detached (`worktreeBranch: "HEAD"`) | No commit: it would be unreachable the moment HEAD moves. `worktreeDetached: true`, `worktreeReason` explains it. |
| A merge, rebase, cherry-pick, or revert is in progress | No commit: committing over the operation's own state could corrupt it. |
| The branch is one this job treats as protected — the remote's actual default branch (`origin/HEAD`) when it can be read, else `main`/`master`/`develop` | No commit: refused rather than depositing a WIP commit directly on a protected branch. |
| None of the above, and the commit itself fails | `worktreeCommit` empty, `worktreeReason` names the failure. |
| The commit succeeds but the push fails | `worktreeCommit` set, `worktreePushed: false`, `worktreeReason` names the push failure — the commit still exists locally, but "only as safe as this one working tree". |
| The commit succeeds and the push succeeds | `worktreeCommit` and `worktreeRemote` set, `worktreePushed: true`, `worktreeReason` empty. |

A dirty working tree is never a success, whatever `exitCode` says: `job.Succeeded()`'s single definition folds `!worktreeDirty` in alongside `state == exited && exitCode == 0`, the same way it already folds in "nothing left running behind it" for `abandoned`/`gate-incomplete`. `job status`/`job await`'s text line appends `working tree was dirty; checkpointed as <sha> and pushed to <remote>` (or `but not pushed: <reason>`, or `and left uncommitted: <reason>`) to whatever state line it would otherwise render — including a plain `exited 0` line, since a checkpointed-and-pushed tree is still worth an orchestrator's attention even though nothing was lost.

### `erun job attach`

Registers work erun did **not** start, so it is visible and holds an activity lease.

| Flag | Type | Default | Effect |
|---|---|---|---|
| `--tenant` / `--environment` | string | **required** | Target environment. |
| `--name <what>` | string | **required** | What the work is. |
| `--id <id>` | string | the `--name` | Handle. Re-attaching the same id renews the lease rather than restarting the job. |
| `--pid <n>` | int | **required** | Process to track. |
| `--log <path>` | path | none | File the work already writes to, so `job output` can serve it. |
| `--lease-ttl <duration>` | duration | `15m` | Lease TTL. |
| `--dry-run` | bool | `false` | Resolve and trace without recording. |

An attached job resolves against the named pid and nothing else. It reads `running` while that process lives and `unknown` once it is gone; it can **never** report an exit status, because nothing erun ran was waiting on the process to observe one. Start work through `job start` when the outcome matters.

### `erun job status`

| Flag | Type | Default | Effect |
|---|---|---|---|
| `--tenant` / `--environment` | string | **required** | Target environment. |
| `--id <id>` | string | none | Report one job; omit for every retained job, newest first. |

`--output json` emits the job record (with `--id`) or the array (without).

For an [agent job](#agent-jobs) the text line and the record both carry the progress view, so `status` answers what the agent is doing rather than only that it is running:

```
running: sweep, pid 4243, last beat 210ms ago, agent claude, editing erun-common/mcp_client.go, 12 turns, 47 tools, 91204 bytes of output
```

The `last beat <n>ms ago` segment is the text rendering of `aliveAgeMs` (see [The alive contract](#alive-contract)) for a running job — present whenever the record has beaten at least once.

An agent job that has not emitted yet reads `agent claude, no events yet` — distinct from an idle one, and the honest answer while the tool is still starting.

### `erun job await` {#erun-job-await}

| Flag | Type | Default | Effect |
|---|---|---|---|
| `--tenant` / `--environment` | string | **required** | Target environment. |
| `--id <id>` | string | **required** | Job to wait for. |
| `--timeout <duration>` | duration | `30s` | Bounded wait. Must be `> 0` and `≤ 10m`; a larger value is an error, not a clamp. |

The call returns inside the timeout either way, so no connection is held open for the work's lifetime. The record is re-read every 250 ms; nothing streams. Call it again to keep waiting.

**Exit codes are the contract** — a timeout is a different event from a failure, and neither is inferred from the other:

| Exit code | Meaning |
|---|---|
| `0` | The job reached `exited` with code `0`, `startedJobFailed` is empty, and, for an agent job, `worktreeDirty` is `false`. |
| `1` | The job reached `exited` with a non-zero code, its state is `abandoned` or `gate-incomplete` (work it started — a process it left running, or a job it started whose wait was exhausted — outlived it), `startedJobFailed` names a job it started and waited for that did not succeed (see [gate-incomplete](#erun-job) / [Deliberate handoff](#job-handoff)), or `worktreeDirty` is `true` — see [Working tree checkpoints](#worktree-checkpoints). Any of these is never a success, even at a captured `exitCode` of `0`. `job.exitCode` carries the captured code either way; the message text says which case it is. |
| `124` | This one call's own bounded wait elapsed with the job still running — never a verdict on the job itself. Matches `timeout(1)`. A job that can run longer than the 10-minute cap (a full test-suite gate routinely does) is not a case this command refuses to cover: call it again, at up to the cap, each time it reports `124`, rather than asking for one longer wait. |
| `125` | The job's state is `unknown` — no outcome was ever recorded. |

`--output json` emits `{job, timedOut, waitedSeconds, timeoutSeconds}`. `timedOut` is `true` only for the `124` case, so a caller reading the payload does not have to infer it from the exit code either.

### `erun job output`

| Flag | Type | Default | Effect |
|---|---|---|---|
| `--tenant` / `--environment` | string | **required** | Target environment. |
| `--id <id>` | string | **required** | Job whose output to read. |
| `--offset <n>` | int64 | `0` | Byte offset to read from. Pass the previous read's `nextOffset` to continue rather than repeat. An offset past the end of the log is an error. |
| `--max-bytes <n>` | int64 | `65536` | Most bytes to return in this read. |

stdout and stderr are **merged** into one log in write order, and served as the file stands — so progress is readable long before the work exits. The bytes go to stdout; `next offset: <n> (more: <bool>, complete: <bool>)` goes to stderr so it never corrupts the payload. `--output json` emits `{job, offset, nextOffset, output, hasMore, complete}`.

`complete` is true only when the job has finished **and** this page reached the end of its output. `hasMore` only describes this read; the job's own `outputTruncated` is what says output was dropped at the cap.

### `erun job cancel` {#erun-job-cancel}

| Flag | Type | Default | Effect |
|---|---|---|---|
| `--tenant` / `--environment` | string | **required** | Target environment. |
| `--id <id>` | string | **required** | Job to cancel. |
| `--signal <name>` | `TERM` \| `INT` \| `HUP` \| `KILL` | `TERM` | Signal to send. |
| `--dry-run` | bool | `false` | Trace the target without signalling. |

The signal goes to the **process group of the pid the record holds**, so a cancel can only reach the work it names — not a process that merely looks like it, and not the shell issuing the cancel. Two guards make the latter impossible: signalling erun's own pid is refused, and so is signalling erun's own process group.

The job's supervisor is deliberately **not** signalled, so it survives to record the outcome; the cancelled job then reads back as a normal `exited` job carrying `signal`. Cancelling a job that already finished is not an error — it reports `signalled: false`.

On Windows there are no signals: every name maps to a `taskkill /F /T` of the recorded pid, and `signal` is never populated on the resulting record.

### Storage, retention, and output bounding {#job-storage}

| Property | Value |
|---|---|
| Record | `${XDG_CACHE_HOME}/erun/activity/<tenant>/<environment>/jobs/<id>.json`, written atomically (temp + rename) so a concurrent status read can never see a half-written record. |
| Log | `…/jobs/<id>.log`. |
| Durability | The same store the [activity leases](/agent-reference/idle-policy#activity-leases) use. Inside a runtime pod that path is on the home PVC, so records survive pod replacement — deliberately not container-local `/tmp`, which every deploy strands. |
| Output cap | `--max-output-bytes` (default 4 MiB) per job. Past it the log stops growing, the record sets `outputTruncated: true` (immediately, not at exit), and `job status` says `(truncated at the output cap)`. The **head** is kept: the outcome never comes from the log, so a bounded log costs detail, never the result. |
| Retention | A finished job stays readable for **24 hours** after it ended, and the newest **50** finished records per environment are kept. Running jobs are never pruned. An orchestrator reconnecting after the work ended can therefore still learn the outcome; reaping at exit would recreate the problem this surface closes. |
| Pruning | Happens on read and on write, alongside the same reconcile-on-read pass that demotes a stranded record to `unknown`. Pruning removes the record and its log together. |

### The lease a job holds

A job holds an activity lease named after the job for its whole lifetime, with `id` = `job-<job id>` and `pid` = the supervisor's. Starting a job therefore makes the environment report as busy and defers auto-stop, with the caller arranging nothing — see [Idle policy · activity_leases](/agent-reference/idle-policy#activity-leases) for the lease's own semantics. If the supervisor is killed outright the lease is reclaimed by the same pid probe that reclaims any abandoned holder.

### Error behaviour

| Failure | Behaviour |
|---|---|
| No `--tenant` / `--environment`. | Exit 1, `tenant and environment are required`. |
| `start` with no `--name`. | Exit 1, `job name is required` — a job that names no work would report the environment busy without saying why. |
| `start` with no command after `--`. | Exit 1 from argument validation. |
| `start --agent <unsupported>`. | Exit 1, `unsupported agent tool "<name>": expected one of claude, codex`. Nothing is spawned. |
| `start --agent` with an empty prompt. | Exit 1, `an agent job needs a prompt to run`. |
| `start` re-using a running id. | Exit 1, `job "<id>" is already running (pid <n>); pass a different id or cancel it first`. Nothing is spawned. |
| The supervisor never registers the job within 10s. | Exit 1, naming the supervisor pid. No handle is returned. |
| `attach` with no `--pid`. | Exit 1, `a pid to track is required; an attached job has nothing else to reconcile against`. |
| Unknown job id on `status` / `await` / `output` / `cancel`. | Exit 1, `no job "<id>" in <tenant>/<environment>`. |
| `await --timeout` above the ceiling or `≤ 0`. | Exit 1 before waiting, naming the `10m0s` ceiling. |
| `output --offset` past the end of the log. | Exit 1, naming the offset and the log size. |
| `cancel --signal` outside `TERM`/`INT`/`HUP`/`KILL`. | Exit 1, `unsupported signal`. |
| `cancel` targeting a process that already exited. | Success; the record reconciles on the next read. |

---

## `erun mcp`

See [MCP overview](/mcp/overview) for the protocol and the tool list. The launcher flag set is:

| Flag | Type | Default | Effect |
|---|---|---|---|
| `--port <n>` | int | `EnvConfig.mcpport` (default `17000`). | The HTTP listener port. |
| `--host <addr>` | string | `127.0.0.1` | The bind address. The in-pod default is loopback-only. |
| `--path <p>` | string | `/mcp` | The HTTP path the endpoint is served from. |

### `erun mcp call` / `tools` / `token` / `proxy` — client side

These call an environment's MCP edge rather than serving one; they are the supported way for a script or an orchestrating Agent to reach an env without configuring an MCP client, and the reason no MCP *tool* exists for this (a tool that calls another env's edge would invert the transport). All four share the target flags:

| Flag | Type | Default | Effect |
|---|---|---|---|
| `--tenant <t>` | string | The current scope. | Target a specific tenant. |
| `--environment <e>` | string | The tenant's default env. | Target a specific environment; requires `--tenant`. |
| `--dry-run` | bool | `false` | Resolve the endpoint and trace the request without sending it. |
| `--output json` | string | `text` | Emit the structured result on stdout. |

`call` adds:

| Flag | Type | Default | Effect |
|---|---|---|---|
| `--tool <name>` | string | *(required)* | The tool to invoke. |
| `--args <json>` | string | `{}` | The tool's arguments as a JSON object. |

Resolution and request contract:

1. The target resolves through the same path as `deploy` / `open` (explicit flags, else the current runtime directory).
2. The endpoint is `http://127.0.0.1:<localPort>/mcp`, where `localPort` is the env's MCP port — the value `erun list` reports and `erun open` forwards.
3. A bearer is minted **per HTTP request**, immediately before it is sent: EdDSA (Ed25519) over the desktop identity, `iss=file:///etc/erun/mcp-auth/desktopid.pub`, `aud=erun-mcp:<tenant>/<environment>`, 5-minute expiry. No client-side timeout is imposed on the call, so a tool that runs for minutes is not cut short and cannot fail on an aged-out token.
4. The handshake is the standard one — `initialize`, `notifications/initialized`, then `tools/call` or `tools/list` — propagating `Mcp-Session-Id` and accepting both plain-JSON and SSE-framed replies.

#### `proxy` — stdio bridge

`proxy` takes only the shared target flags. It is a transport adapter, not an operation: an MCP client launches it as a stdio server and it relays that client's own JSON-RPC to the env's edge. It exists because a client reads its server config once at launch and cannot refresh a header, so a bearer written into that config turns the 5-minute token lifetime into a hard session limit — every tool for the env failing at the same moment. Configuring a *command* rather than a credential removes the class of failure: nothing in the client's config expires.

| Property | Contract |
|---|---|
| Input framing | Newline-delimited JSON-RPC on **stdin**, one message per line. A final message without a trailing newline is still relayed. Blank lines are ignored. Relay is sequential — one message in flight at a time. |
| Output framing | One JSON-RPC message per line on **stdout**, compacted to a single line even when the edge pretty-printed it. **stdout carries JSON-RPC and nothing else**; the audit line, trace output, and every diagnostic go to stderr. |
| Notifications | A message with no `id` (or `"id": null`) gets no stdout line at all — including when the relay fails, since there is no id to answer against. The failure still reaches stderr. |
| Session | The `Mcp-Session-Id` from the first reply is captured and sent on every subsequent request; the client never sees it. |
| Bearer | Minted per relayed request, same claims as row 3 above. The proxy never writes a token to disk and never reuses one past its life. |
| Handshake | Not performed by the proxy — the client's own `initialize` / `notifications/initialized` are relayed like any other message. |
| Termination | Exits `0` when stdin closes. |

Failure mapping. Every one of these is answered as a JSON-RPC error on the failing request's own `id`, with code `-32603`, and the relay keeps serving the next message — the edge can come back mid-session:

| Condition | `error.message` |
|---|---|
| Nothing listening on the local MCP port | `MCP endpoint is not reachable: <endpoint> (…); run erun open <tenant> <env> so the local MCP port-forward is up` |
| Edge answered `401`/`403` | `MCP endpoint rejected the bearer token: <endpoint> (HTTP 401); <tenant>/<env> was deployed without this machine's MCP public key, so redeploy it from the ERun desktop app` |
| Edge accepted a request but returned no body | `the MCP edge at <endpoint> accepted the request but returned no reply` — the client fails visibly instead of waiting on a reply that is not coming. |
| No desktop identity on this machine | The minting error, naming the path it looked at. |

The same text is written to stderr on every failure, including the notification case that has no reply to carry it.

```json
// a Claude Code / Codex stdio server entry
{ "mcpServers": { "acme-dev": { "type": "stdio", "command": "erun",
  "args": ["mcp", "proxy", "--tenant", "acme", "--environment", "dev"] } } }
```

`--output json` shapes:

```json
// erun mcp call --output json
{ "tool": "version", "text": "{\"version\":\"1.0.80\"}", "structured": { "version": "1.0.80" }, "isError": false }

// erun mcp tools --output json
{ "tools": [ { "name": "raw", "description": "…", "inputSchema": { "type": "object", "properties": {} } } ] }

// erun mcp token --output json
{ "tenant": "myapp", "environment": "local", "endpoint": "http://127.0.0.1:17000/mcp",
  "issuer": "file:///etc/erun/mcp-auth/desktopid.pub", "audience": "erun-mcp:myapp/local",
  "expiresAt": "2026-05-24T10:47:15Z", "token": "eyJhbGciOiJFZERTQSI…" }
```

`text` is every text content block of the tool result joined by newlines; `structured` is the tool's `structuredContent`, absent when the tool returns none. In text mode `call` prints `text`, or the structured payload when the tool returned no text.

Error codes:

| Code | Cause | Exit code |
|---|---|---|
| `--tool is required` | `call` invoked without `--tool`. | `1` |
| `--args must be a JSON object` | `--args` did not parse as a JSON object. Raised before the target resolves. | `1` |
| `MCP endpoint is not reachable` | The dial to the local MCP port failed — normally a missing port-forward. Recovery: `erun open <tenant> <env>`. | `1` |
| `MCP endpoint rejected the bearer token` | The edge answered `401`/`403`: it does not trust this machine's identity. Recovery: redeploy the env from the desktop app so the current public key is injected. | `1` |
| `MCP tool <name> reported an error` | The tool set `isError`; the message is the tool's own. | `1` |
| `MCP tools/call failed` | A JSON-RPC-level error (unknown tool, invalid arguments), reported with the protocol code. | `1` |
| `no desktop identity at <path>` | No private key on this machine. A key is never generated here, because a fresh identity signs tokens no deployed env trusts. Recovery: open an env from the desktop app once. | `1` |

---

## See also

- [CLI overview](/cli/overview) — the Operator-facing summary.
- [Configuration](/reference/configuration) — where each persisted flag value lands.
- [Command primitives](/concepts/command-primitives) — the Operator-facing model of pure primitives, version-as-identity, and orchestration vs convenience switches.
- [Build path resolution](/reference/configuration-build-paths) — the algorithm `erun build` and `push` use to resolve scope and the version `build` mints.
- [Dry-run redaction](/agent-reference/dry-run-redaction) — what `--dry-run` rewrites in the trace.
- [Skills](/concepts/skills) — how Agents pick up project conventions for code generation (replaces the removed `erun add` command).

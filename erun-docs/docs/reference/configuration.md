---
title: Configuration overview
---

# Configuration overview

ERun's configuration lives in three layers. Each layer holds different kinds of settings and is consulted at different points in the lifecycle.

<figure className="erun-hero-figure">
  <img src="/img/config-layers.svg" alt="Three configuration layer cards side by side. PER-USER (cyan-stroked) at ~/.config/erun/, edited by you / erun init / desktop, read every command, holds ERunConfig · TenantConfig · EnvConfig. PER-PROJECT (cyan-stroked) at &lt;repo&gt;/.erun/config.yaml, edited by team in PRs, read every build/push/deploy, holds ProjectConfig. PER-POD ENV VARS (charcoal) set by helm at deploy, derived automatically, read by erun in the runtime pod, examples ERUN_TENANT and ERUN_NAMESPACE." />
  <figcaption>At deploy time the helm chart derives the per-pod environment variables from the per-user and per-project layers.</figcaption>
</figure>

For exact file paths see [Config locations](/reference/config-locations). For the in-pod env var list see [Environment variables](/reference/env-vars). For how `erun build` resolves project root / context / version, see [Build path resolution](/reference/configuration-build-paths).

---

## Per-user config

### `ERunConfig` (`~/.config/erun/config.yaml`)

Global defaults that apply across all tenants.

| Field | Type | Used by | Effect |
|---|---|---|---|
| `default_tenant` | string | `erun` (no args), `erun open`, `erun list` | Tenant used when no tenant argument is supplied. |
| `cloudproviders[]` | list | `erun init`, `erun open`, `erun cloud` | Known cloud provider identities (alias, provider, account, profile, SSO/OIDC settings). Each cloud-bound env references one by alias. |
| `cloudcontexts[]` | list | `erun open`, `erun deploy`, `erun cloud` | Known managed cloud clusters. Each binds a cluster to a provider, region, and instance type/size. |
| `runtimeregistry.namespace` | string | `erun version`, runtime image pulls | Overrides where ERun looks for `erun-devops` and related runtime images. Useful for internal mirrors (Harbor, ECR, Artifactory). |
| `runtimeregistry.repository` | string | same as above | Overrides the repository name in the namespace. |
| `runtimeregistry.baseurl` | string | same as above | Registry HTTP endpoint. Defaults differ for Docker Hub vs GHCR. |
| `runtimeregistry.tokenurl` | string | same as above | GHCR token endpoint. Only used on the GHCR flow. |

### `TenantConfig` (`~/.config/erun/<tenant>/tenant.yaml`)

One per tenant.

| Field | Type | Used by | Effect |
|---|---|---|---|
| `name` | string | All commands | Tenant identifier. |
| `projectroot` | string | build path resolution (cwd→tenant match), `erun init` (env default) | Absolute path the tenant lives at on this machine. Used to match the current working directory to a tenant when commands run without `--tenant`. Also the default copied into a new env's `repopath` at `erun init`. ([Planned move to env-level.](#planned-changes)) |
| `defaultenvironment` | string | `erun` (no args), `erun open`, `erun list` | Environment used when none is supplied. |
| `api_url` | string | `erun open` (API port forward) | Backend API base URL for this tenant. |
| `cloudprovideraliases[]` | list of strings | `erun init`, `erun open` | Cloud provider aliases the tenant is allowed to use. |
| `primarycloudprovideralias` | string | `erun open` (suggesting cloud bindings) | Default cloud provider alias for new envs in this tenant. |
| `remote` | bool | build path resolution | When true, ERun knows the tenant's `projectroot` describes a *remote* machine. ([Planned removal.](#planned-changes)) |
| `snapshot` | `*bool` | (none) | ([Planned removal — does not belong on the tenant.](#planned-changes)) |

### `EnvConfig` (`~/.config/erun/<tenant>/<env>/config.yaml`) {#envconfig}

One per environment. This is the most-edited file.

| Field | Type | Used by | Effect |
|---|---|---|---|
| `name` | string | All commands | Environment identifier. |
| `type` | string (enum) | `erun build`, `erun open`, `erun deploy`, helm chart (`worktreeStorage`) | `local-agent`, `remote-agent`, or `runtime`. The canonical signal for what this env is for. When set, it takes precedence over the legacy `remote` and `snapshot` fields. See [Environment types](/concepts/environment-types). |
| `localRepoPath` | string | helm chart (`worktreeHostPath` for `local-agent` envs), `erun deploy` (project-config load, registry resolution) | Absolute path to the repo on the local machine. Only meaningful for `local-agent` envs; left empty for `remote-agent` and `runtime`. |
| `repopath` | string | (legacy) helm chart (`worktreeHostPath`), `erun deploy` (project-config load, registry resolution) | Legacy path field. Kept for backward compatibility with envs created before `localRepoPath` existed; new envs use `localRepoPath` instead. ([Planned removal.](#planned-changes)) |
| `kubernetescontext` | string | `erun open`, `erun deploy`, `erun list` | Kubernetes context to deploy/open against. Special value `in-cluster` is set inside the runtime pod. |
| `containerregistry` | string | `erun build`, `erun push`, `erun deploy`, build image tag resolution | Highest-precedence registry for images this env produces or pulls. Becomes the `<registry>` portion of the image tag. |
| `cloudprovideralias` | string | `erun open`, `erun deploy`, idle-stop, audit labels | Which cloud provider identity backs this env. Resolves to the `cloudproviders[]` entry. |
| `managedcloud` | bool | helm chart (`ERUN_CLOUD_ENVIRONMENT`) | When true, marks the env as running on a managed cloud context (enables idle-stop, cloud-credential refresh, etc.). |
| `runtimeversion` | string | `erun open`, `erun deploy`, chart appVersion | Pins the version of the runtime image used by this env. |
| `runtimeregistry` | string | `erun open`, `erun deploy` | Overrides the registry the runtime image is pulled from (per-env). |
| `autoupgrade` | bool | [`erun upgrade`](/cli/upgrade), desktop Upgrade all | When true, this env joins the Upgrade-all set: `erun upgrade` redeploys it to the latest version for its channel when `runtimeversion` lags. |
| `upgradechannel` | string (enum) | [`erun upgrade`](/cli/upgrade) | Release channel an upgrade targets: `stable` (semver releases) or `snapshot` (latest snapshot build). Orthogonal to `type`. When unset, defaults from `type` — runtime → `stable`, agent → `snapshot`. |
| `runtimepod.cpu` | string | helm chart (`runtime.resources.limits.cpu`) | CPU limit for the runtime pod (e.g. `4`, `500m`). |
| `runtimepod.memory` | string | helm chart (`runtime.resources.limits.memory`) | Memory limit (e.g. `8916Mi`, `2Gi`). |
| `sshd.enabled` | bool | `erun open`, chart (SSH port-forward setup) | Whether the in-pod SSH server is exposed via port forward. Needed for IDE attach. |
| `sshd.localport` | int | `erun open` | Local port the desktop binds for the SSH forward. `0` = auto-allocate. |
| `sshd.publickeypath` | string | `erun open --vscode`, `erun open --intellij`, SSH key sync | Path to the SSH public key authorized for the env. |
| `sshd.workspacesync.enabled` | bool | desktop workspace-sync poller | Mirror a local folder into the runtime workspace. |
| `sshd.workspacesync.localpath` | string | desktop workspace-sync poller | The local folder to mirror. |
| `idle.timeout` | duration (e.g. `5m0s`) | chart (`ERUN_IDLE_TIMEOUT`), in-pod idle monitor | How long the env must be quiet before idle-stop fires. |
| `idle.workinghours` | string (`HH:MM-HH:MM`) | chart (`ERUN_IDLE_WORKING_HOURS`), idle monitor | Window during which idle-stop is allowed to fire. |
| `idle.timezone` | string | chart (`ERUN_IDLE_TIMEZONE`), idle monitor | Time zone for `workinghours`. |
| `idle.idletrafficbytes` | int64 | chart (`ERUN_IDLE_TRAFFIC_BYTES`), idle monitor | Bytes/window below which the env is considered network-quiet. |
| `claude.usemantle` | `*bool` | chart (`CLAUDE_CODE_USE_MANTLE`) | Route Claude through Mantle. |
| `claude.usebedrock` | `*bool` | chart (`CLAUDE_CODE_USE_BEDROCK`) | Route Claude through AWS Bedrock. |
| `claude.models[]` | list | chart (`ERUN_CLAUDE_AVAILABLE_MODELS`) | Allow-list of Claude models for in-pod tools. |
| `claude.maxoutputtokens` | `*int` | chart (`CLAUDE_CODE_MAX_OUTPUT_TOKENS`) | Max output tokens per Claude response. |
| `claude.effort` | `*string` | desktop AI launcher (`claude --effort` / `claude --settings`) | Effort level for the env's Claude AI tab, one of `low`, `medium`, `high`, `xhigh`, `max`, `ultracode`. Unset or invalid → `ultracode`. The five `--effort` levels launch as `claude --effort <level>`; `ultracode` is not an `--effort` value — it launches as `claude --settings '{"ultracode":true}'` and enables xhigh effort plus standing multi-agent workflow orchestration. Only the default Claude launch is affected; a non-`claude` `aitool` or a Claude launch the Operator wrote with explicit flags is left untouched. Saving a change from the desktop reopens the env's open AI tabs; the session resumes via `--continue`. |
| `claude.defaultmodel` | `*string` | desktop AI launcher (`claude --model`) | Model the env's Claude AI tab starts on. Applied only while it is one of the env's available models (`claude.models[]`, or the default available set when that list is empty); otherwise no `--model` is passed. Model names are opaque tokens to ERun — resolving one (e.g. `fable`) to a concrete model is Claude's concern. Same verbatim-launch carve-out and save-reopen behaviour as `claude.effort`. |
| `claude.verbosedebug` | bool | desktop AI launcher (`claude --verbose --debug`) | Launch the env's Claude AI tab with Claude's own verbose + debug diagnostics streaming into the tab. Absent means off. Same verbatim-launch carve-out and save-reopen behaviour as `claude.effort`. |
| `aitool` | string | desktop AI launcher, runtime entrypoint | Which Agent is the default for this env (`claude`, `codex`, …). |
| `remote` | bool | helm chart (worktree storage selection), build path resolution | When true, the runtime pod uses a PVC-backed checkout; when false, the host project root is mounted via `hostPath`. ([Planned removal — subsumed by `type`.](#planned-changes)) |
| `snapshot` | `*bool` | `erun build`, `erun push`, `erun deploy`, `erun open` | Marks this env as agent-mode: when `true`, builds happen here and produce snapshot-tagged artefacts; when `false`, the env only receives deploys. ([Planned removal — subsumed by `type`.](#planned-changes)) |
| `localportrangestart` | int | desktop port allocator | Base port for this env's local forwards (MCP, API, SSH). |
| `autostart` | `*bool` | desktop sidebar open | `nil` = ask, `true` = always start linked cloud context on open, `false` = never. |
| `remotehostcredentials` | bool | helm chart (cloud credentials passthrough) | Mount the host's cloud credentials into the runtime pod (for managed cloud envs). |

---

## Per-project config

### `ProjectConfig` (`<repo>/.erun/config.yaml`)

Committed to the repo, applies to anyone who checks it out.

| Field | Type | Used by | Effect |
|---|---|---|---|
| `containerregistry` | string | `erun build`, `erun push`, `erun deploy`, build image tag resolution | Project-wide fallback registry. Lower precedence than `EnvConfig.containerregistry`. |
| `environments` | map | per-env settings (below) | Map of `<env-name> → ProjectEnvironmentConfig`. |
| `environments.<env>.containerregistry` | string | `erun build`, `erun push`, `erun deploy` | Per-env registry override. Higher precedence than the top-level project registry, lower than `EnvConfig.containerregistry`. |
| `environments.<env>.docker.fingerprints` | map | `erun build`, `erun build --release` | Per-image content fingerprints from the last published build. Drives the [fingerprint cache](/agent-reference/conventions-spec#fingerprint-cache). |
| `environments.<env>.k8s.deployments[]` | ordered list | `erun deploy` | The ordered deploy plan for this env. Each step is either a single component name or a list of names deployed in parallel. |
| `release.mainbranch` | string | `erun release` | Main branch name (default `main`). |
| `release.developbranch` | string | `erun release` | Develop branch name (default `develop`). |

---

## Per-pod env vars

The helm chart writes these into the runtime pod at deploy time. They're derived from the per-user and per-project layers above; you don't edit them directly. Full list at [Environment variables](/reference/env-vars).

---

## Resolution order

Some values can be set in multiple layers. When that happens, ERun consults them in a fixed order.

### Container registry (for the image tag)

1. `EnvConfig.containerregistry` (per-user, per-env).
2. `ProjectConfig.environments.<env>.containerregistry` (per-project, per-env).
3. `ProjectConfig.containerregistry` (per-project, top-level).
4. Built-in default (`ghcr.io/sophium`).

### Kubernetes context

1. `EnvConfig.kubernetescontext` (per-env explicit).
2. For `local` envs only: `kubectl config current-context` if `EnvConfig.kubernetescontext` is empty.
3. Inside the runtime pod: always `in-cluster`.

### Effective tenant + environment for a CLI command

1. Explicit `--tenant` / `--environment` flags.
2. Current working directory's git repo, matched against each tenant's `projectroot`.
3. `ERunConfig.default_tenant` and that tenant's `defaultenvironment`.
4. Interactive prompt (TTY only).
5. Error otherwise.

### Runtime version

1. `EnvConfig.runtimeversion` (explicit pin).
2. The CLI's own built-in build version (the `erun version` value).

For Docker build context / version resolution, see [Build path resolution](/reference/configuration-build-paths).

---

## How to inspect your effective config

| What you want | Where to find it |
|---|---|
| Default tenant + env, every tenant's envs, current effective target | `erun list` |
| The resolved registry/context/runtime version for an env | `erun list` (per-env block) |
| The exact config inside a running runtime pod | MCP `list` tool, or `erun list` inside the pod |
| Open the per-env settings UI | Desktop app → click the env in the sidebar → edit modal |
| Audit who/what last changed a value | The audit trail (CLI `audit:` lines) and git history of `<repo>/.erun/config.yaml` |

---

## Planned changes

The migration from the three legacy fields (`TenantConfig.projectroot`, `EnvConfig.remote`, `EnvConfig.snapshot`) to one explicit `EnvConfig.type` has begun:

- ✅ `EnvConfig.type` is writable today via `erun init --type` or by editing the YAML directly.
- ✅ When `type` is set on an env, downstream commands (`erun build`, `erun open`, `erun deploy`) and the helm chart wiring (`worktreeStorage=host|pvc|none`) branch on it instead of the legacy fields.
- ✅ `EnvConfig.localRepoPath` is the new name for the local-host worktree path; only `local-agent` envs populate it.
- ⏳ Legacy fields stay readable for one release. Envs that have no `type` set fall back to deriving it from `remote` and `snapshot` per the truth table below.
- ⏳ A follow-up release will drop the legacy fields and the deprecated `--remote` / `remote: true` aliases.

### `EnvConfig.type` truth table

| Value | Worktree storage | Build behaviour | Legacy fallback (when `type` unset) |
|---|---|---|---|
| `local-agent` | `hostPath` mount of `localRepoPath` | snapshot tags, builds in-pod | `Remote: false` (the historical default) |
| `remote-agent` | PVC checkout cloned from git | snapshot tags, builds in-pod | `Remote: true` + `snapshot: true` |
| `runtime` | None | release tags only, no builds | `Remote: true` + `snapshot: false` |

See [Environment types](/concepts/environment-types) for what each value means in practice.

### Field-level moves

| Today | Planned | Why |
|---|---|---|
| `TenantConfig.projectroot` | Removed; cwd-to-tenant matching iterates over envs' `localRepoPath`. | A tenant can host both local and remote envs; the path lives on the env, not the tenant. |
| `EnvConfig.repopath` | Replaced by `EnvConfig.localRepoPath`. | Scoped explicitly: the local-machine path mounted into the pod. For PVC envs the field is unset — the repo lives inside the pod at a fixed convention. |
| `TenantConfig.remote` | Removed. | Subsumed by per-env `type`. |
| `TenantConfig.snapshot` | Removed. | Doesn't belong on the tenant; subsumed by per-env `type`. |
| `EnvConfig.remote` | Removed. | Subsumed by `type` (`local-agent` ↔ false; `remote-agent` and `runtime` ↔ true). |
| `EnvConfig.snapshot` | Removed. | Subsumed by `type` (`local-agent` and `remote-agent` ↔ true; `runtime` ↔ false). |

During the migration, ERun reads both shapes. Setting only `type` is sufficient; legacy fields stay supported for one release before being removed.

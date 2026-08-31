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
| `cloudproviders[]` | list | `erun init`, `erun open`, `erun cloud` | Known cloud provider identities (alias, provider, account, profile, SSO/OIDC settings). `provider` is `aws` or `cloudflare`. AWS entries carry the SSO profile and OIDC issuer; Cloudflare entries carry an account ID and a token reference into the local secret store (the scoped API token itself is never stored in this file). Each cloud-bound env references entries by alias. |
| `cloudcontexts[]` | list | `erun open`, `erun deploy`, `erun cloud` | Known managed cloud clusters. Each binds a cluster to a provider, region, and instance type/size. |
| `runtimeregistry.namespace` | string | `erun version`, runtime image pulls | Overrides where ERun looks for `erun-devops` and related runtime images. Useful for internal mirrors (Harbor, ECR, Artifactory). |
| `runtimeregistry.repository` | string | same as above | Overrides the repository name in the namespace. |
| `runtimeregistry.baseurl` | string | same as above | Registry HTTP endpoint. Defaults differ for Docker Hub vs GHCR. |
| `runtimeregistry.tokenurl` | string | same as above | GHCR token endpoint. Only used on the GHCR flow. |
| `execution.modes.<operation>` | string | Operations with a library alternative (see [Execution modes](#execution-modes)) | `"library"` switches that operation from its CLI subprocess to an equivalent Go library call; anything else (including unset) keeps the subprocess. |

### `TenantConfig` (`~/.config/erun/<tenant>/tenant.yaml`)

One per tenant.

| Field | Type | Used by | Effect |
|---|---|---|---|
| `name` | string | All commands | Tenant identifier. |
| `defaultenvironment` | string | `erun` (no args), `erun open`, `erun list` | Environment used when none is supplied. |
| `api_url` | string | `erun open` (API port forward) | Backend API base URL for this tenant. |
| `cloudprovideraliases[]` | list of strings | `erun init`, `erun open` | Cloud provider aliases the tenant is allowed to use. |
| `primarycloudprovideralias` | string | `erun open` (suggesting cloud bindings) | Default cloud provider alias for new envs in this tenant. |

### `EnvConfig` (`~/.config/erun/<tenant>/<env>/config.yaml`) {#envconfig}

One per environment. This is the most-edited file.

| Field | Type | Used by | Effect |
|---|---|---|---|
| `name` | string | All commands | Environment identifier. |
| `type` | string (enum) | `erun build`, `erun open`, `erun deploy`, helm chart (`worktreeStorage`, `ERUN_ENV_TYPE`) | `local-agent`, `remote-agent`, `runtime`, or `host`. The canonical — and only — signal for what this env is for. Configs written before `type` existed are migrated to a concrete value on read (see [Legacy migration](#planned-changes)). See [Environment types](/concepts/environment-types). |
| `localRepoPath` | string | helm chart (`worktreeHostPath` for `local-agent` envs), `erun deploy` (project-config load, registry resolution) | Absolute path to the repo on the local machine. Meaningful for `local-agent` and `host` envs (for `host` it names the directory the env *is*, with no pod to mount it into); left empty for `remote-agent` and `runtime`. |
| `repopath` | string | (legacy, read-only) | Removed struct field. A `repopath:` in an older config is migrated into `localRepoPath` on read and dropped on the next save; new configs only carry `localRepoPath`. ([Migration done.](#planned-changes)) |
| `kubernetescontext` | string | `erun open`, `erun deploy`, `erun list` | Kubernetes context to deploy/open against. Special value `in-cluster` is set inside the runtime pod. |
| `containerregistries` | list | `erun build`, `erun push`, `erun deploy`, build image tag resolution | Per-env marked registry list, set for `remote-agent`/`runtime` envs whose project config is not on the local machine (`local-agent` envs resolve the list from the project's `.erun/config.yaml` instead). Each entry is `{registry, roles}` where roles ⊆ `build`/`from`/`to`/`deploy`. See [Container registries](#container-registries). |
| `cloudprovideralias` | string | `erun open`, `erun deploy`, idle-stop, audit labels | Which AWS cloud provider identity backs this env. Resolves to a `cloudproviders[]` entry. |
| `cloudprovideraliases` | map (provider → alias) | `erun open`, `erun deploy` | Additional cloud aliases attached to this env, keyed by provider type (e.g. `cloudflare`). AWS stays in the scalar `cloudprovideralias` above for backward compatibility; non-AWS providers live here. An env holds at most one alias per provider type, so a runtime pod can receive both an AWS identity and a Cloudflare token at once. A Cloudflare alias injects `CLOUDFLARE_API_TOKEN` / `CLOUDFLARE_ACCOUNT_ID` for in-pod tooling. |
| `managedcloud` | bool | helm chart (`ERUN_CLOUD_ENVIRONMENT`) | When true, marks the env as running on a managed cloud context (enables idle-stop, cloud-credential refresh, etc.). |
| `runtimeversion` | string | `erun open`, `erun deploy`, chart appVersion | Pins the version of the runtime image used by this env. |
| `runtimeregistry` | string | `erun open`, `erun deploy` | Per-env override for where the environment resolves ERun's own artifacts: it is the first registry the [runtime chart search](/cli/deploy#where-the-runtime-chart-comes-from) probes, and it is projected into the pod as `RUNTIME_REGISTRY` for in-pod platform image resolution. Set it with `erun init --runtime-registry <host>` when the env's `deploy`-marked registry holds only your project's own images — ERun publishes `charts/erun-devops` beside the runtime image it releases, never beside your application images. A successful deploy or `open` records the registry the runtime chart actually resolved from here, so the next search starts there — but only to fill the field in or confirm it: a value you set is never replaced by a deploy that resolved somewhere else, which is traced instead. |
| `runtimeimage` | string | `erun open`, `erun deploy` | Points the env's runtime pod at a custom image instead of the published `<registry>/erun-devops:<version>` default. A full reference (`ghcr.io/acme/acme-runtime:1.2.3`) is used verbatim; a bare name resolves to `<registry>/<name>:<runtime version>`. Set by `erun init --runtime-image`; carried to the published chart as `imageOverrides.erun-devops` on every deploy (see [Advanced chart values](#advanced-chart-values)). **Optional for a tenant that publishes its own `charts/<tenant>-devops` umbrella** — that deploy defaults the image to the umbrella's own `<tenant>-devops` image, so set `runtimeimage` only to pin a different image; an image-only env riding the shared `charts/erun-devops` chart still needs it. A value that resolves to the stock `erun-devops` image is **ignored on an umbrella deploy** (see [Advanced chart values](#advanced-image-overrides)). |
| `runtimerunningimage` | string | `erun deploy` (healed on every deploy of the runtime chart) | Display-only memo of the runtime image a deploy last actually resolved for this env's pod — the same value that lands on the release as `imageOverrides.erun-devops`, or the chart's own stock default when no override applied. Never read back to influence a deploy's own image choice (`runtimeimage` above is the field deploy reads for that); it exists only so a reader can tell which release line `runtimeversion`'s number belongs to — ERun's own, or a tenant's own `<tenant>-devops` line, which can differ even between two environments of the same tenant. `erun list` renders it beside `runtime-version` as `<version> (<line> line, <registry>/<image>)`, flags the case where the release name and the image disagree, and reads `line undetermined` when no deploy under this field has run yet (never guessed from the tenant or release name). See [`erun list` · release lines](/cli/list#release-lines). |
| `runtimechart` | string | `erun deploy`, `erun pin` | Names the runtime chart this env rides, as an OCI reference that may carry its own version (`oci://ghcr.io/sophium/charts/erun-devops:1.0.178`). Set it when the runtime **image** is versioned on the project’s own release line rather than ERun’s: the chart is ERun’s artifact and exists only at ERun’s versions, so deriving both from `runtimeversion` names a chart that was never published and the deploy fails `FetchReference … not found`. With the chart stated here, `runtimeversion` keeps stamping the env and tagging the image, and each artifact is named on the line it ships on — see [`erun deploy` · naming the chart and the image separately](/cli/deploy#runtime-chart-coordinate). Omit the `:<version>` suffix to name only a different registry, keeping the paired version. Empty (the default) keeps the published lookup at the runtime version, which is right whenever [`erun push`](/cli/push) published chart and image together. Overridable for one run with [`erun deploy --runtime-chart`](/cli/deploy), which is not persisted. When it names ERun's own stock `erun-devops` chart at an explicit version, [`erun pin`](/cli/pin) moves that version alongside `runtimeversion` on a re-pin; a chart naming a tenant's own line is left alone. Editable in the desktop app under an environment’s Runtime tab (**Runtime chart**). |
| `mcpauthpublickeypath` | string | `erun deploy`, `erun init`, `erun open` | Path to the PEM public key the env's `erun-mcp` edge verifies bearer tokens against — the desktop `file://` path. Recorded by a deploy that enabled authentication (`erun deploy --mcp-auth-public-key <path>`, `erun init --mcp-auth-public-key <path>`) at the moment it applies the key to the cluster rather than after the rollout completes — so a rollout that fails afterwards still leaves the env naming the key its release trusts — and rethreaded by every later deploy of the runtime chart, so a plain version bump keeps the edge authenticated instead of falling back to the chart default. Cleared only by `erun deploy --no-mcp-auth`, once that unauthenticated release has rolled out. Empty means the env never had desktop MCP authentication. See [`erun deploy` · MCP edge authentication is sticky](/cli/deploy#mcp-auth-sticky). |
| `imagepullsecrets` | list of strings | `erun deploy`, helm chart | Names Kubernetes `dockerconfigjson` secrets the runtime pod authenticates image pulls with — needed when the runtime image is a **private** registry package (e.g. a private `<tenant>-devops` umbrella image; ERun's own `erun-devops`/`erun-dind` images are public, so a default env needs none). Threaded to the chart as `imagePullSecrets[i].name`, re-scoped under the `erun-devops` subchart key for an umbrella. Empty (the default) leaves the pod pulling anonymously, so public-image envs are unaffected. Each named secret is re-minted from a host-resolved credential on every `erun deploy` when one is available (ECR via the AWS CLI, anything else via your local docker session) — see [Advanced chart values](#advanced-image-pull-secrets). For a `ghcr.io`-hosted runtime image specifically, `erun deploy` no longer needs an entry here set up front at all: it auto-provisions and appends one on its own when a credential resolves, and refuses the deploy outright when none does and the image can't be confirmed public — see [Private image pull secrets](#advanced-image-pull-secrets). |
| `mountsource` | bool | `erun deploy` (runtime worktree storage + clone wiring), helm chart | Runtime-only opt-in for real-time patching: when true (together with `repourl`), the runtime pod gets a PVC-backed worktree it clones from `repourl` at the deployed release tag on first boot. A no-op without `repourl`, and ignored for agent envs (which already carry source). Default false keeps a runtime env sourceless (deploy-by-reference). Set from the desktop's Runtime tab → **Mount source code**. See [Environment types → Hotfix pattern](/concepts/environment-types#hotfix-pattern). |
| `repourl` | string | `erun deploy`, helm chart (`ERUN_REPO_URL`) | Git remote the runtime pod clones when `mountsource` is set. Cloned into the in-pod worktree and checked out at `v<runtimeversion>` (the deployed release tag). |
| `autoupgrade` | bool | [`erun upgrade`](/cli/upgrade), desktop Upgrade all | When true, this env joins the Upgrade-all set: `erun upgrade` redeploys it to the latest version for its channel when `runtimeversion` lags. |
| `disablebuildscript` | bool | [`erun build`](/cli/build), `erun build --deploy` | When true, erun ignores any project `build.sh` for this env and resolves docker/release builds directly; if there is no docker build context either, the build errors with no buildable context. Default false. |
| `platformaccount` | bool | `erun deploy`, helm chart | When true, designates the env's runtime ServiceAccount as the cluster's **erun platform admin**: deploy threads `--set platformAccount=true` and the runtime chart binds the SA to the built-in `cluster-admin` ClusterRole (a `<release>-platform` ClusterRoleBinding). This is the grant that lets in-pod `erun terraform apply` of the [cluster edge](/agent-reference/skills-spec#erun-enable-hosting-edge) and platform component installs (cert-manager, Traefik, PowerDNS) create the cluster-scoped resources they require — namespaces, CRDs, cluster RBAC, webhooks. Default false leaves the SA with namespaced admin only. Set with `erun init --platform-account`. The first deploy that creates the binding must run from an admin-capable context — the API server's privilege-escalation check requires the creator already hold `cluster-admin`, so an in-pod self-deploy cannot bootstrap it. |
| `upgradechannel` | string (enum) | [`erun upgrade`](/cli/upgrade) | Release channel an upgrade targets: `stable` (semver releases) or `snapshot` (latest snapshot build, or the stable release once one is published on top of it — see [`erun upgrade`](/cli/upgrade#what-opted-in-means)). Orthogonal to `type`. When unset, defaults from `type` — runtime → `stable`, agent → `snapshot`. |
| `stopped` | bool | [`erun stop`](/cli/stop), [`erun open`](/cli/open), `erun deploy`, helm chart (`stopped`) | Records that the Operator stopped this environment. `erun stop` sets it and scales the runtime Deployment to zero; `erun open` clears it and scales back up. `deploy` renders the chart's `stopped` value from it, so the chart emits `replicas: 0` and a `helm upgrade` reconciles the stop instead of silently restarting the pod — a bare scale patch alone would be drift the next upgrade reverts. `deploy` therefore never wakes an environment; opening it does. Clearing the field is an Operator gesture: `erun open --reconnect`, the form a supervisor uses to re-establish a dropped session, leaves it set (see [`erun open`](/agent-reference/cli-flags#erun-open)), so a stop is not erased by the reconnects the stop itself triggers. Default false. |
| `runtimepod.cpu` | string | helm chart (`runtime.resources.limits.cpu`) | CPU limit for the runtime pod (e.g. `4`, `500m`). |
| `runtimepod.memory` | string | helm chart (`runtime.resources.limits.memory`) | Memory limit (e.g. `8916Mi`, `2Gi`). |
| `sshd.enabled` | bool | `erun open`, chart (SSH port-forward setup) | Whether the in-pod SSH server is exposed via port forward. Needed for IDE attach. |
| `sshd.localport` | int | `erun open` | Local port the desktop binds for the SSH forward. `0` = auto-allocate. |
| `sshd.publickeypath` | string | `erun open --vscode`, `erun open --intellij`, SSH key sync | Path to the SSH public key authorized for the env. |
| `sshd.workspacesync.enabled` | bool | desktop workspace-sync poller | Mirror a local folder into the runtime workspace. |
| `sshd.workspacesync.localpath` | string | desktop workspace-sync poller | The local folder to mirror. |
| `deploy.timeout` | duration (e.g. `5m0s`) | `erun deploy`, `erun upgrade` (helm `--timeout`) | Per-env helm rollout wait. How long `deploy` waits for the rollout to become ready before helm times out; the [pod monitor](/agent-reference/cli-flags#rollout-wait-and-pod-monitoring) keeps waiting up to this bound while an image is still pulling and aborts earlier on a real failure. Unset → `5m0s`. Overridden per-deploy by `--rollout-timeout` / the MCP `deploy` `timeout` input. A malformed value fails the deploy. |
| `deploy.components` | list | `erun deploy` | Per-machine saved deploy selection: the charts `erun deploy` rolls out for this env by default (chart directory names under `<tenant>-devops/k8s/`, plus the runtime release name `<tenant>-devops`). Set it with `erun init --components <a,b,…>`, or from the desktop app's Runtime-tab checklist (inside the Version-to-deploy picker, gated until you pick a version, "Set as default"). It is a **published-version view, the same for every env type**: once you pick a version it offers the component charts actually published at that version (plus the runtime) — the version, not the env's local source, decides which charts exist, so a version that never published a chart doesn't list it, and a local-agent env shows the same published components as a runtime env rather than its local working-tree chart directories. (Deploying local working-tree charts by name stays available to an operator via the CLI.) Deploy is opt-in: `--components` overrides this per run; when both are empty, deploy falls back to the project's [`k8s.deployments`](#per-project-config) plan, then to the runtime chart alone. See [selection precedence](/agent-reference/cli-flags#components-value-set). Empty → no saved selection. Because a saved selection wins over the plan permanently and tiers never merge, a plain deploy (no `--components`) sourced from the saved set **refuses** whenever the plan names something the saved set omits, tracing `deploy: saved components shadow the repo plan; plan also names <a, b, …>` and naming the fix: adopt the addition with `erun init --components <a,b,…>`, or clear the saved selection with `erun init --components ''` to return to the plan outright. `--components` passed explicitly for that one run bypasses the refusal (and the saved selection) entirely. |
| `idle.timeout` | duration (e.g. `5m0s`) | chart (`ERUN_IDLE_TIMEOUT`), in-pod idle monitor | How long the env must be quiet before idle-stop fires. |
| `idle.workinghours` | string (`HH:MM-HH:MM`) | chart (`ERUN_IDLE_WORKING_HOURS`), idle monitor | Window during which idle-stop is allowed to fire. |
| `idle.timezone` | string | chart (`ERUN_IDLE_TIMEZONE`), idle monitor | Time zone for `workinghours`. |
| `idle.idletrafficbytes` | int64 | chart (`ERUN_IDLE_TRAFFIC_BYTES`), idle monitor | Bytes/window below which the env is considered network-quiet. |
| `claude.usemantle` | `*bool` | chart (`CLAUDE_CODE_USE_MANTLE`) | Route Claude through Mantle. |
| `claude.usebedrock` | `*bool` | chart (`CLAUDE_CODE_USE_BEDROCK`) | Route Claude through AWS Bedrock. |
| `claude.models[]` | list | chart (`ERUN_CLAUDE_AVAILABLE_MODELS`) | Allow-list of Claude models for in-pod tools. |
| `claude.maxoutputtokens` | `*int` | chart (`CLAUDE_CODE_MAX_OUTPUT_TOKENS`) | Max output tokens per Claude response. |
| `claude.effort` | `*string` | desktop AI launcher (`claude --effort` / `claude --settings`) | Effort level for the env's Claude AI tab, one of `low`, `medium`, `high`, `xhigh`, `max`, `ultracode`. Unset or invalid → `ultracode`. The five `--effort` levels launch as `claude --effort <level>`; `ultracode` is not an `--effort` value — it launches as `claude --settings '{"ultracode":true}'` and enables xhigh effort plus standing multi-agent workflow orchestration. Only the default Claude launch is affected; a non-`claude` `aitool` or a Claude launch the Operator wrote with explicit flags is left untouched. Saving a change from the desktop reopens the env's open AI tabs; the session resumes via `--continue`. |
| `claude.defaultmodel` | `*string` | desktop AI launcher (`claude --model`) | Model the env's Claude AI tab starts on. Applied while it is one of the env's available models (`claude.models[]`, or the default available set when that list is empty); when unset — or set to a model no longer in that set — the launch falls back to the first available model (`opus` by default) rather than passing no `--model`, so a managed session never defers to Claude Code's own default model (Fable), which erun's pod auth does not serve. Model names are opaque tokens to ERun — resolving one (e.g. `fable`) to a concrete model is Claude's concern. `fable` stays strictly opt-in: it is never in the default available set and launches only when the Operator both lists it under `claude.models[]` and selects it here. The resolved model is also mirrored into `CLAUDE_CODE_SUBAGENT_MODEL` on the launch, so subagents spawned inside that session run on the env's model instead of Claude Code's separate subagent default; it is left unset only when no available model is a usable token. Same verbatim-launch carve-out and save-reopen behaviour as `claude.effort`. |
| `claude.verbosedebug` | bool | desktop AI launcher (`claude --verbose --debug`) | Launch the env's Claude AI tab with Claude's own verbose + debug diagnostics streaming into the tab. Absent means off. Same verbatim-launch carve-out and save-reopen behaviour as `claude.effort`. |
| `aitool` | string | desktop AI launcher, runtime entrypoint | Which Agent is the default for this env (`claude`, `codex`, …). |
| `localportrangestart` | int | desktop port allocator | Base port for this env's local forwards (MCP, API, SSH). |
| `autostart` | `*bool` | desktop sidebar open | `nil` = ask, `true` = always start linked cloud context on open, `false` = never. |
| `remotehostcredentials` | bool | — (deprecated no-op) | **Deprecated.** Host AWS credential delivery now follows AWS-alias attachment: attaching an AWS cloud alias to an env delivers its credentials into the runtime pod (the alias association *is* the opt-in — "act on my behalf here"), so no separate toggle is needed. Retained only so existing configs still parse; setting it has no effect. |

### What a `host` env leaves unset {#host-env-fields}

A `host` env (see [Environment types → host](/concepts/environment-types#host)) has no pod and no cluster at all, so `erun init --type host` records only `name`, `type`, and `localRepoPath` — every other field above stays empty rather than being filled with a value that would never be read. Concretely: `kubernetescontext`, `containerregistries`, `cloudprovideralias(es)`, `managedcloud`, `runtimeversion`, `runtimeregistry`, `runtimeimage`, `runtimerunningimage`, `runtimechart`, `mcpauthpublickeypath`, `imagepullsecrets`, `mountsource`/`repourl`, `platformaccount`, `stopped`, `runtimepod.*`, `sshd.*`, and `idle.*` are all pod/cluster-shaped and meaningless for host — `erun deploy`, `erun pin`, and `erun terraform` refuse a host env outright rather than resolving a plan from them. `upgradechannel` resolves to `snapshot` by its type default (host, like the two agent types, iterates on snapshot builds) but is moot in practice: `erun upgrade` skips a host env even if `autoupgrade` is set, the same way it skips a not-opted-in env, since there is no runtime version to redeploy. The desktop's Init dialog hides the fields above entirely when `Host` is selected, so this is enforced at the point of entry, not just documented after the fact.

The four `claude.*` rows above (`usemantle`, `usebedrock`, `models[]`, `maxoutputtokens`) are the Claude values erun manages itself. The runtime chart accepts further `claude.*` values that erun never sets — pin a model, point Bedrock at a VPC endpoint, tune prompt caching — via the env's values overlay; see [Advanced chart values](#advanced-chart-values).

The env's Claude AI tab also launches with **Remote Control** enabled by default: the managed launch appends `--remote-control <tenant>/<env>` after the effort/model/verbose flags, so the session is drivable from the Claude iOS app under that name. It is omitted when `claude.usemantle` or `claude.usebedrock` is set — Remote Control pairs through the claude.ai account relay, which those gateway auth modes can't authenticate — and follows the same verbatim-launch carve-out as `claude.effort` (a non-`claude` `aitool`, or a Claude launch written with explicit flags, is left untouched). An environment name that isn't a plain token (alphanumerics, `.`, `-`, `_`, starting with an alphanumeric) falls back to the unnamed `--remote-control`, leaving Claude Code to name the session from the pod hostname. For the Operator view, see [Desktop app · Working with an Agent](/desktop/working-with-an-agent).

---

## Per-project config

### `ProjectConfig` (`<repo>/.erun/config.yaml`)

Committed to the repo, applies to anyone who checks it out.

| Field | Type | Used by | Effect |
|---|---|---|---|
| `containerregistries` | list | `erun build`, `erun push`, `erun deploy`, build image tag resolution | Project-wide marked registry list. Each entry is `{registry, roles}` with roles ⊆ `build`/`from`/`to`/`deploy`. See [Container registries](#container-registries). |
| `environments` | map | per-env settings (below) | Map of `<env-name> → ProjectEnvironmentConfig`. |
| `environments.<env>.containerregistries` | list | `erun build`, `erun push`, `erun deploy` | Per-env marked registry list override. Higher precedence than the top-level project list. |
| `environments.<env>.docker.fingerprints` | map | `erun build`, `erun build --release` | Per-image content fingerprints from the last published build. Drives the [fingerprint cache](/agent-reference/conventions-spec#fingerprint-cache). |
| `environments.<env>.docker.platforms` | list | `erun build`, `erun push` | Pins the `docker --platform` targets for a non-release build/push in this env (e.g. `[linux/amd64]`), for a cluster that can only ever run one architecture. `--platform` on the command line overrides it for one invocation. Never applies to `erun build --release` / `erun release`, which always build every platform erun supports. See [Multi-architecture](/cli/build#multi-architecture). |
| `environments.<env>.k8s.deployments[]` | ordered list | `erun deploy` | The ordered deploy plan for this env. Each step is either a single component name or a list of names deployed in parallel. |
| `release.mainbranch` | string | `erun release` | Main branch name (default `main`). |
| `release.developbranch` | string | `erun release` | Develop branch name (default `develop`). |
| `platform` | map | `erun deploy` (PowerDNS), `erun expose` | Per-instance platform deployment config. Absent for non-platform projects. See [`platform:` block](#platform-block). |
| `paths` | map | `erun build`, `erun push`, `erun deploy`, `erun terraform` | Overrides where erun discovers the devops assets — the `docker/` and `k8s/` folders, the `terraform-<tenant>` root, and the `VERSION` file. Absent → the conventional layout. See [`paths:` block](#paths-block). |

#### `paths:` block {#paths-block}

The `paths:` block relocates the devops assets erun otherwise discovers by convention: the `docker/` build contexts and `k8s/` Helm charts under `<tenant>-devops/`, the per-environment Terraform roots at `terraform-<tenant>/` (or `<tenant>-devops/terraform-<tenant>/`), and the `VERSION` file. It is project-global (not per-environment). Reach for it when a repo does not nest these under a `<tenant>-devops/` module — for example a devops repo whose root carries `docker/`, `k8s/`, `terraform-<tenant>/`, and `VERSION` as top-level siblings.

Every field is optional; an unset field keeps the conventional location. A configured path resolves relative to the project root (an absolute path is used as-is). The override **relocates** the canonical folders, it does not rename them: the `docker/` and `k8s/` directories keep those exact names (erun's build and deploy machinery keys off the folder name), so a configured `docker`/`k8s` path must end in a `docker`/`k8s` segment. One field, `dockercontext`, is not a path but a mode selector for how the Docker build context is chosen.

| Field | Type | Default | Effect |
|---|---|---|---|
| `docker` | string | `<tenant>-devops/docker` | Directory (named `docker`) whose subdirectories are the per-component build contexts (`<docker>/<component>/Dockerfile`). Read by `erun build` / `erun push`. |
| `dockercontext` | `repo-root` \| `component` | positional heuristic | Selects the Docker build **context** root, overriding the positional heuristic (see [Build context directory](/reference/configuration-build-paths#3-build-context-directory)). `repo-root` → context is the project root (so a Dockerfile can `COPY` from anywhere in the repo); `component` → context is the component build dir. Unset keeps the heuristic. Read by `erun build` / `erun push` / `erun release`. |
| `k8s` | string | `<tenant>-devops/k8s` | Directory (named `k8s`) whose subdirectories are the per-component Helm charts (`<k8s>/<component>/Chart.yaml`). Read by `erun deploy`, and by `erun build` for chart packaging. |
| `terraform` | string | `terraform-<tenant>` or `<tenant>-devops/terraform-<tenant>` | Base directory under which the per-environment Terraform roots live; erun still appends `/<environment>`. Read by `erun terraform`, which by convention checks `terraform-<tenant>/` then `<tenant>-devops/terraform-<tenant>/` (the same `-devops` discovery as `docker`/`k8s`) before this override is needed. |
| `version` | string | walk up from the build dir to the project root | Path to the `VERSION` file that mints the build version. A directory resolves to `<dir>/VERSION`. Read by `erun build` / `erun push` / `erun release`. |

```yaml
# <repo>/.erun/config.yaml — a devops repo that holds the folders at its root
paths:
  docker: docker
  k8s: k8s
  terraform: terraform-frs
  version: VERSION
```

**Error behaviour.** A configured override that does not resolve fails the command (exit code 1) rather than silently falling back to convention:

- `paths.docker` / `paths.k8s` pointing at a directory that is missing, not named `docker`/`k8s`, or holding no build contexts / charts → `configured docker path "<p>" (.erun/config.yaml paths.docker) is not a docker build module: …` (and the `k8s` analogue).
- `paths.terraform` with no `<base>/<environment>/` directory → `no Terraform root at <dir> … the .erun/config.yaml paths.terraform base "<p>" must contain a <env>/ dir …`.
- `paths.version` pointing at a missing file → `configured version file <p> (.erun/config.yaml paths.version) not found`.
- `paths.dockercontext` set to anything other than `repo-root` or `component` → `invalid docker context "<v>" (.erun/config.yaml paths.dockercontext): expected "repo-root" or "component"`.

#### `platform:` block {#platform-block}

The `platform:` block configures an **erunpaas platform deployment** — an installation that runs the global singletons (the PowerDNS nameserver, the hosted IdP) and exposes tenant services under a delegated zone. ERun's platform is generic, installable software: any vendor deploys it under their **own** names, so every value is configuration — nothing (not even the base domain) is hardcoded. `erun deploy` threads these as `platform.*` helm values (the `erun-powerdns` and `erun-zitadel` charts read them); [`erun expose`](/cli/expose) reads them to resolve service hostnames.

The whole block is optional. An empty block means the project runs no platform deployment. Once any field is set the block is "in use" and is validated at deploy/expose time — a malformed block fails fast.

| Field | Type | Required | Effect / default |
|---|---|---|---|
| `basedomain` | string | yes (when block in use) | The registered domain this deployment serves, e.g. `erunpaas.com`. Everything else derives from it. Must be a valid domain name. |
| `env` | string | no | The dedicated platform environment that owns the singletons (PowerDNS, the DNS-01 broker), e.g. `frs-prod`. Must be a DNS-safe `<tenant>-<env>` namespace label. |
| `serviceszone` | string | no | The child zone delegated to this deployment's PowerDNS, under which tenant services are exposed. Default `services.<basedomain>`. Must be a valid domain at or under `basedomain`. |
| `authoritativeip` | string | no | The public IP this deployment's authoritative nameserver answers on (the glue-record target for `serviceszone`). Must parse as an IP when set. |
| `nameservers` | list | no | The NS hostnames the parent zone delegates `serviceszone` to. Default `[ns1.<basedomain>, ns2.<basedomain>]`. |
| `authhost` | string | no | The hosted-IdP host, served from the apex zone (not `serviceszone`). Default `auth.<basedomain>`. Must be a valid domain at or under `basedomain`. The `erun-zitadel` chart issues tokens for this origin, and a platform deploy adds `https://<authhost>` to the API's `ERUN_OIDC_ALLOWED_ISSUERS` so the control plane trusts its own IdP. |
| `acmeemail` | string | no | The account email for this deployment's Let's Encrypt registration (LE rate limits are per registered domain, so each deployment uses its own account). |
| `caaissuer` | string | no | CA domain the services zone authorizes via apex `CAA` records (`issue` + `issuewild`), e.g. `letsencrypt.org`. Empty (default) writes no CAA — any CA may issue. Opt-in because it must match the CA the cluster edge's ACME server uses; a mismatched CAA blocks issuance. When set, the `erun-powerdns` zone-bootstrap also gives per-env empty-non-terminal names a definitive CAA answer instead of an ambiguous `NODATA`. |
| `apiurl` | string | no | This deployment's own API base URL, e.g. `https://api.frs-prod.services.erunpaas.com`. Served unauthenticated at [`GET /v1/platform`](/agent-reference/api-protocol#platform-endpoint) so a client can discover it; an unset value renders as an empty string, never an error. |
| `consoleurl` | string | no | This deployment's hosted web console URL. Same discovery contract as `apiurl`. |
| `brand` | string | no | This deployment's display name, if set. Same discovery contract as `apiurl`. |
| `docsurl` | string | no | The documentation site this deployment's own surfaces link to, e.g. `https://docs.erunpaas.com`. Default `https://docs.<basedomain>`. Must be an absolute `http`/`https` URL when set; a trailing slash is normalized away. Same discovery contract as `apiurl`. |
| `tagline` | string | no | The one-line pitch this deployment's signed-out landing page leads with. Unset leaves the client's bundled product tagline in place. Same discovery contract as `apiurl`. |
| `logourl` | string | no | Absolute URL of this deployment's logo, e.g. `https://erunpaas.com/logo.svg`. It is a URL rather than a path the platform serves, because one built console image serves every instance and carries no brand asset. Must be an absolute `http`/`https` URL when set; unset — or a URL the browser cannot load — leaves the client's generic mark. Same discovery contract as `apiurl`. |

```yaml
# <repo>/.erun/config.yaml
platform:
  basedomain: erunpaas.com
  env: frs-prod
  authoritativeip: 203.0.113.10
  caaissuer: letsencrypt.org   # optional; authorizes only this CA on the zone
  apiurl: https://api.frs-prod.services.erunpaas.com     # optional; served at GET /v1/platform
  consoleurl: https://console.frs-prod.services.erunpaas.com # optional; served at GET /v1/platform
  brand: Acme                                            # optional; served at GET /v1/platform
  tagline: Ship it, prove it.                            # optional; else the bundled product tagline
  logourl: https://acme.example/logo.svg                 # optional; else a generic mark
  # serviceszone, authhost, docsurl, and nameservers default from basedomain:
  #   serviceszone: services.erunpaas.com
  #   authhost:     auth.erunpaas.com
  #   docsurl:      https://docs.erunpaas.com
  #   nameservers:  [ns1.erunpaas.com, ns2.erunpaas.com]
```

A second vendor installs the same artifacts under their own names — e.g. `basedomain: kppaas.com`, `env: kp-prod` — with no code changes.

**Error behaviour.** The block is validated when `erun deploy`/`erun expose` resolves the plan, before any chart work, and a malformed value fails the command (exit code 1) rather than being threaded onward:

- `basedomain` unset while any other field is set → `platform config: basedomain is required when a platform block is set`.
- `basedomain` / `serviceszone` / `authhost` not a valid domain, or a host not at or under `basedomain` → `platform config: serviceszone "<v>" must be "<basedomain>" or a subdomain of it`.
- `authoritativeip` that does not parse as an IP → `platform config: authoritativeip "<v>" is not a valid IP address`.
- `env` that is not a DNS-safe namespace label → `platform config: env "<v>" must be a DNS-safe namespace label (lowercase letters, digits, and hyphens)`.
- `docsurl` / `logourl` that is not an absolute `http`/`https` URL → `platform config: logourl "<v>" must be an absolute URL including the scheme and host, for example "https://<basedomain>/logo.svg"`. These values are rendered by a browser as a link and an image, so a bare host or a relative path would become a dead link the page itself cannot explain — the message names the shape expected and an example under this deployment's own domain.

---

## Per-pod env vars

The helm chart writes these into the runtime pod at deploy time. They're derived from the per-user and per-project layers above; you don't edit them directly. Full list at [Environment variables](/reference/env-vars).

---

## Advanced chart values (Operator escape hatches) {#advanced-chart-values}

The runtime chart accepts more values than erun manages. At deploy time erun passes two layers to `helm upgrade --install`:

1. The env's values overlay — `values.<env>.yaml` in the runtime chart directory (`<tenant>-devops/k8s/<tenant>-devops/values.<env>.yaml`). It is passed with `-f` and is required: deploy aborts with `values file not found for environment "<env>"` when it is missing. Environments that deploy the [published `erun-devops` chart](/cli/deploy#where-the-runtime-chart-comes-from) have no local chart directory; for them the overlay lives next to the env's config at `<UserConfigDir>/erun/<tenant>/<environment>/values.yaml` (e.g. `~/.config/erun/<tenant>/<environment>/values.yaml` on Linux) and is optional — when absent, the chart defaults plus erun's `--set` list fully describe the deploy.
2. erun's own `--set`/`--set-string` list, derived from `EnvConfig` and the resolved plan.

Helm gives `--set` precedence over `-f`, so for every key erun manages the overlay can never win. The keys below are exactly the ones erun's `--set` list never includes — for them the `values.<env>.yaml` overlay is authoritative, which makes it the supported escape hatch for behaviour erun doesn't model.

### `claude.*` model and Bedrock tuning {#advanced-claude-values}

Each value renders as an env var on the runtime container, and the pod's entrypoint relays it into the Agent's `~/.claude/settings.json`. Both steps are AWS-gated: the chart renders this env block only when the env's cloud provider is `aws` (`cloudContext.provider`), and the entrypoint relay runs only when Bedrock configuration is active — an AWS provider, or `CLAUDE_CODE_USE_BEDROCK` / `CLAUDE_CODE_USE_MANTLE` set, with a resolvable region.

| Chart value | Env var | Default | Effect |
|---|---|---|---|
| `claude.model` | `ANTHROPIC_MODEL` | unset | Pin the primary model Claude uses. |
| `claude.defaultOpusModel` | `ANTHROPIC_DEFAULT_OPUS_MODEL` | unset | Pin the model ID the `opus` alias resolves to (Bedrock model IDs, `anthropic.`-prefixed). |
| `claude.defaultSonnetModel` | `ANTHROPIC_DEFAULT_SONNET_MODEL` | unset | Pin the model ID the `sonnet` alias resolves to. |
| `claude.defaultHaikuModel` | `ANTHROPIC_DEFAULT_HAIKU_MODEL` | unset | Pin the model ID the `haiku` alias resolves to. |
| `claude.bedrockBaseURL` | `ANTHROPIC_BEDROCK_BASE_URL` | unset | Route Bedrock traffic through a VPC endpoint or gateway instead of the public endpoint. |
| `claude.mantleBaseURL` | `ANTHROPIC_BEDROCK_MANTLE_BASE_URL` | unset | Base URL for the Bedrock Mantle gateway. |
| `claude.bedrockServiceTier` | `ANTHROPIC_BEDROCK_SERVICE_TIER` | unset | Bedrock service tier: `default`, `flex`, or `priority`. |
| `claude.skipMantleAuth` | `CLAUDE_CODE_SKIP_MANTLE_AUTH` | unset | Skip Mantle's own auth step (set `1` when the gateway handles auth). |
| `claude.disablePromptCaching` | `DISABLE_PROMPT_CACHING` | unset | Turn prompt caching off (set `1`). |
| `claude.enablePromptCaching1H` | `ENABLE_PROMPT_CACHING_1H` | unset | Use the 1-hour prompt-cache TTL on Bedrock (set `1`). |
| `claude.maxThinkingTokens` | `MAX_THINKING_TOKENS` | `1024` | Thinking-token budget per response. |
| `claude.smallFastModelAWSRegion` | `ANTHROPIC_SMALL_FAST_MODEL_AWS_REGION` | `cloudContext.region` | AWS region for the small/fast helper model, when it differs from the env's region. Like `AWS_REGION`, the env var is emitted only when a region resolves — see [Environment variables](/reference/env-vars). |

The `claude.*` keys erun does manage — `claude.useBedrock`, `claude.useMantle`, `claude.availableModels`, `claude.maxOutputTokens` — come from the [`EnvConfig` fields above](#envconfig) and are always `--set`; an overlay value for them is ignored.

### Runtime pod resource requests {#advanced-runtime-requests}

erun manages only the runtime pod's resource **limits**: `EnvConfig.runtimepod.cpu` / `.memory` (set with `erun init --runtime-cpu` / `--runtime-memory` or the desktop's env settings) are always `--set` as `runtime.resources.limits.{cpu,memory}`. The requests are overlay-only:

| Chart value | Default | Effect |
|---|---|---|
| `runtime.resources.requests.cpu` | `0.25` | CPU request for the runtime pod. |
| `runtime.resources.requests.memory` | `1024Mi` | Memory request for the runtime pod. |

Request overrides are invisible to `erun open`'s redeploy drift detection — it compares the deployed limits against `EnvConfig.runtimepod` and ignores requests — so changing a request in the overlay takes effect on the next deploy, not automatically on the next open.

### Runtime image override {#advanced-image-overrides}

`imageOverrides.erun-devops` is a supported public value of the runtime chart: it replaces the image the `erun-devops` container runs while keeping the rest of the chart canonical. The supported way to set it is the [`EnvConfig.runtimeimage`](#envconfig) field (`erun init --runtime-image`), which erun passes as `--set-string imageOverrides.erun-devops=<image>` on every deploy of the published chart — the intended path for custom toolchain images built `FROM` the published `erun-devops` image (the `erun-build-env` [skill](/concepts/skills) walks through it).

When `runtimeimage` is **unset**, erun defaults the override from the chart it is deploying: a deploy of the tenant's own published `charts/<tenant>-devops` umbrella defaults `imageOverrides.erun-devops` to that umbrella's own image, `<registry>/<tenant>-devops:<version>`. [`erun push`](/cli/push) publishes the umbrella and its `<tenant>-devops` image together at one version, so the chart's identity names the image — building and pushing it is enough for the deploy to run it, with no `runtimeimage` to set and no silent fall-back to a stock `erun-devops:<version>` the tenant's version line never published. A deploy of the shared `charts/erun-devops` chart carries no such signal: with `runtimeimage` unset it passes no override (the chart's own default image runs, and the overlay may set the value directly), so an image-only build env still points at its image through `runtimeimage`.

An explicit `runtimeimage` normally wins over that default — the operator's choice. Two cases are treated as stale leftovers rather than a deliberate current choice, and are **ignored** in favour of the default above:

- The shared-chart-to-umbrella migration: on a `<tenant>-devops` umbrella deploy, a `runtimeimage` that resolves to the stock `erun-devops` image (any registry) is a leftover from when the env rode the shared chart — the tenant's version line never publishes `erun-devops`, so honouring it would pin a tag that 404s (ImagePullBackOff). Trace: `deploy: ignoring stale runtimeimage <image> on the <tenant>-devops umbrella deploy …`.
- A `runtimeimage` that names the **very same image** this deploy's own line would already resolve unaided (the umbrella's own image, above), just at some other tag: that pin is provably redundant, so its tag can only be a stale one left behind by an earlier deploy, and the current version's tag is used instead. Trace: `deploy: ignoring stale runtimeimage <image> (this deploy's own line already publishes <default-image>; the saved pin just names an older tag); defaulting to <default-image>`.

A non-stock `runtimeimage` naming a genuinely different image (a real custom image, or a hotfix build) still wins as before, and the shared `charts/erun-devops` chart still honours a stock `erun-devops` value (there it is correct). Because both stale cases exist, `erun init --runtime-image` records the value **tagless** — the deploy path above already pins a tagless reference to the env's own runtime version, so recording a tag is what creates the rot in the first place.

#### Runtime image and version are one coordinate {#advanced-runtime-image-line}

`runtimeimage` and `runtimeversion` are resolved together: the pair names both *which* image the pod runs and *which release line* its version number belongs to — erun's own, or a tenant's own `<tenant>-devops` line. A version number alone can't tell the two apart (the same number is valid on both lines), but the image name always can, so every deploy that resolves a concrete runtime image now heals `runtimeimage` to match it in the same write as `runtimeversion` — self-maintaining and tagless, the same shape `erun init --runtime-image` already records. This closes the gap where a deploy that moved an environment onto its own image line (by building and pushing that image, or by the chart-level staleness healing above) updated the version but left `runtimeimage` still naming the old line.

Before installing, `erun deploy` also compares the image it is about to run against `runtimerunningimage` — the last image a deploy actually confirmed running for this environment. A resolved image on a different release line than the environment's last confirmed deploy is refused **before the rollout**, unless this call explicitly names the new line itself (`--runtime-image` or `--runtime-chart`): the runtime chart's `Recreate` strategy tears the running pod down before the replacement is scheduled, and the wrong image can otherwise resolve to a real, existing tag — erun's own stock image genuinely exists at almost every version number a tenant's own line also uses, so checking that the tag exists is not enough. A pairing this check cannot classify (most commonly, an environment with no prior confirmed deploy yet) is never refused — see [Troubleshooting · runtime image release line mismatch](/reference/troubleshooting#runtime-image-line-mismatch) for the refusal message and the fix, and [`erun doctor`](/cli/doctor) for the same check run offline against config alone.

### Private image pull secrets {#advanced-image-pull-secrets}

The runtime pod pulls its image anonymously by default, which is correct for ERun's **public** `erun-devops`/`erun-dind` images. When the runtime image is a **private** registry package — most commonly a private `<tenant>-devops` umbrella image — the pod needs a pull credential. Set [`EnvConfig.imagepullsecrets`](#envconfig) (via `erun init --image-pull-secret`) to the name(s) of the `dockerconfigjson` secret(s) the pod should pull with; `erun deploy` threads them to the chart as `imagePullSecrets[i].name` (re-scoped under the `erun-devops` subchart key for an umbrella), and the runtime pod authenticates its pulls with them. Leaving the list empty threads nothing, so public-image envs are byte-for-byte unchanged.

Before every rollout, `erun deploy` also re-mints each named secret's content from credentials it resolves for **every registry in play** — the deploy registry (`containerRegistry`) plus each [image override](#advanced-image-overrides)'s own registry, when it names a different one — on whichever machine runs the deploy. Each registry resolves independently, using the same resolution [version listing](/deployment/registries#discovering-versions-to-deploy) uses: the AWS CLI for an ECR host, otherwise your local docker session. This is what keeps a runtime image pinned to a different registry than the chart's own (built in ECR, deployed via a chart pulled from ghcr, say) pullable at all — resolving only the deploy registry's credential left the pod with none for the image's own registry. It also keeps an ECR-hosted image pullable past its authorization token's twelve-hour expiry without an operator noticing the rot and recreating the secret by hand. When no credential resolves for a given registry (no AWS CLI or docker session available where deploy runs), that registry's existing coverage in the secret is left exactly as it was — the other registries' credentials still refresh, and the first deploy to name a pull secret still needs one to already exist for any registry erun cannot resolve a credential for. `erun doctor` flags a `runtimeimage`/`runtimeregistry` mismatch by name from config alone, so this split surfaces even with the pod down — see [Troubleshooting](/reference/troubleshooting#runtime-image-registry-mismatch).

#### The runtime image is checked automatically, before the rollout {#advanced-image-pull-secrets-preflight}

Everything above is opt-in — an Operator has to notice a private image and name a secret for it. The one image `erun deploy` resolves for itself (the runtime image, `imageOverrides.erun-devops`) doesn't need that: when it's a `ghcr.io` package, `deploy` checks whether it can actually be pulled *before* touching the cluster, on the same terms `erun push`/`erun release` already check for anonymous pullability. This closes a real gap — an environment that had ridden the public stock `erun-devops` image for months worked fine with no pull secret configured, and only found out it needed one the moment it moved onto its own private `<tenant>-devops` line, by then already mid-rollout.

- **A host credential resolves** (the same resolution `imagepullsecrets` above uses): `erun deploy` auto-provisions and attaches a secret named `<tenant>-devops-image-pull`, appended to `imagepullsecrets` for that run — no `--image-pull-secret` needed. The dry-run trace names it: `image pull: <image> has a resolvable ghcr.io credential; attaching auto-provisioned secret <tenant>-devops-image-pull`.
- **No credential resolves, and the image is confirmed anonymously pullable:** nothing changes — a public-image env with no credential configured keeps deploying exactly as before.
- **No credential resolves, and the image is private (or its pullability can't be determined at all — a registry that can't be reached is never assumed public):** `erun deploy` **refuses before the rollout**, naming the image and the missing credential, rather than letting the runtime chart's `Recreate` strategy tear down the running pod for a replacement that can't be pulled. See [Troubleshooting · image is not anonymously pullable](/reference/troubleshooting#image-not-anonymously-pullable) for the fix.

This check is scoped to `ghcr.io`; a runtime image on ECR or another registry still relies on `imagepullsecrets` and `runtimeregistry`/`runtimeimage` alignment as described above. `--dry-run` always shows the decision (or, when a real run's live pullability check can't run offline, that it will run one), so this is never invisible.

### Turning the MCP edge off {#advanced-mcp-enabled}

The runtime container serves the environment's MCP edge on `mcpPort`, which is why an MCP tool call runs with the environment's own toolchain (see [Inside an environment](/concepts/runtime-pods)). The chart value `mcpEnabled` gates it and defaults to `true`; set `mcpEnabled: false` in the env's values overlay to run the pod with no edge at all — no listener, no advertised `mcp` container port. Everything else about the pod is unchanged, and an Agent or desktop client then has no MCP endpoint for that env.

### Runtime pod shape extensions {#advanced-pod-shape}

The runtime chart exposes five additive, no-op-by-default extension points for build environments that need pod shape the [image override](#advanced-image-overrides) cannot express — a sidecar, an extra volume/mount, extra env, or the cluster RBAC a sidecar needs:

| Value | Merges into |
|---|---|
| `extraContainers` | the pod's `containers` list (sidecars) |
| `extraVolumes` | the pod's `volumes` list |
| `extraVolumeMounts` | the `erun-devops` container's `volumeMounts` |
| `extraEnv` | the `erun-devops` container's `env` |
| `extraRules` | an extra ClusterRole (`<release>-extra`) bound to the runtime ServiceAccount, for cluster-scoped RBAC a sidecar needs; namespaced access already comes from the built-in admin binding |

Set them through the env's values overlay. Deployed as the published chart directly, they sit at the top level. When a `<tenant>-devops` umbrella **wraps** the published chart as a subchart (the `erun-build-env` [skill](/concepts/skills) Step 6), nest them under the `erun-devops` key — and `erun deploy` **re-scopes every runtime value it sets** (tenant, ports, cloud context, MCP auth, and the `imageOverrides.erun-devops` image) under that same `erun-devops.` subchart key, so the wrapped runtime is wired exactly as the published chart would be. A plain (non-wrapped) runtime keeps top-level values.

An example overlay:

```yaml
# <tenant>-devops/k8s/<tenant>-devops/values.prod.yaml
claude:
  model: anthropic.claude-opus-4-8
  bedrockServiceTier: priority
runtime:
  resources:
    requests:
      cpu: "1"
      memory: 2048Mi
```

---

## Resolution order

Some values can be set in multiple layers. When that happens, ERun consults them in a fixed order.

### Container registries

A project declares a **list** of registries, each marked with the roles it plays. The list resolves per environment, then individual registries are selected by role.

**Resolving the list for an environment:**

1. `EnvConfig.containerregistries` (per-user, per-env — set for `remote-agent`/`runtime` envs).
2. `ProjectConfig.environments.<env>.containerregistries` (per-project, per-env override).
3. `ProjectConfig.containerregistries` (per-project, top-level).
4. Built-in default seed: a single `ghcr.io/sophium` entry marked `build` + `deploy`.

**Roles** (each entry carries any subset):

| Role | Meaning | Count |
|---|---|---|
| `build` | `erun build`/`erun push` push target; the `<registry>` of the build image tag. | ≤ 1 |
| `from` | Copy source on deploy. | ≤ 1 |
| `to` | Copy destination(s) on deploy. | ≥ 0 |
| `deploy` | Registry the cluster pulls from (rendered as `containerRegistry` in the chart). | ≥ 1 (required) |

**Role rules** (validated when the list is resolved for build or deploy):

- At most one `build`, at most one `from`; at least one `deploy`.
- `from` and `to` are set together and must name different registries.
- More than one `deploy` → the first wins.

A `deploy` registry need not also carry `build` or `to`: the image it serves may be published there externally (e.g. a runtime env that pulls a released image), which erun does not police at config time.

**Behaviour:**

- **Build** pushes to the `build` registry. No `build` registry → the environment cannot build (`erun build` aborts: `environment "<env>" has no build registry`; exit code 1).
- **Deploy** copies each image the cluster needs (the runtime image and any locally-built component) from `from` to every `to` with `docker buildx imagetools create` (manifest-aware), then the cluster pulls from the `deploy` registry. The copy runs only when both `from` and `to` are set.

**Migration:** a legacy single `containerregistry: X` scalar (project or env config) is read once as a one-entry list `[{registry: X, roles: [build, deploy]}]` and rewritten in the list shape on the next save.

### Deploy chart source {#deploy-chart-source}

`erun deploy` installs charts **by reference from the published registry** — the runtime chart (`oci://<registry>/charts/erun-devops` + `imageOverrides.erun-devops`) and each selected platform component (`oci://<registry>/charts/erun-<component>`), threading `tenant`/`environment` and the env's config as top-level `--set`. A **runtime env needs no local source**: its worktree is `none`, and components deploy by reference (release-named `<tenant>-<component>`, in default-rank order), so the deploy runs from anywhere — the operator's machine or the control plane. When the env's repo *is* local (an agent env, or an in-pod checkout for real-time patching) and carries a chart for a selected component, that local chart is used instead — the optional patch path.

Component charts are published per release (each `erun-<component>` chart rides `erun push`), but only from the release each was added — so which charts a version offers depends on the version. The desktop's Components checklist reflects this: it probes the registry for the charts published at the selected deploy version (`charts/erun-<component>`, distinct from the runtime *image* tags the version picker lists) and offers only those. A version that never published a component's chart doesn't list it; selecting one anyway (via `--components`) fails the deploy with a chart-not-found error.

### Kubernetes context

1. `EnvConfig.kubernetescontext` (per-env explicit).
2. For `local` envs only: `kubectl config current-context` if `EnvConfig.kubernetescontext` is empty.
3. Inside the runtime pod: always `in-cluster`.

### Effective tenant + environment for a CLI command

1. Explicit `--tenant` / `--environment` flags.
2. Current working directory, matched against each tenant's environments' `localRepoPath` (longest match wins; an ambiguous tie across tenants resolves to no match).
3. `ERunConfig.default_tenant` and that tenant's `defaultenvironment`.
4. Interactive prompt (TTY only).
5. Error otherwise.

### Runtime version

1. `EnvConfig.runtimeversion` (explicit pin).
2. The CLI's own built-in build version (the `erun version` value).

For Docker build context / version resolution, see [Build path resolution](/reference/configuration-build-paths).

---

## Execution modes {#execution-modes}

A handful of operations can run either as a subprocess shelling out to a CLI (`aws`, and more tools over time) or through an equivalent Go library call. Both paths trace the identical CLI-equivalent command for `--dry-run`/audit purposes, and produce the same result — the switch only changes what actually executes.

`execution.modes` in `~/.config/erun/config.yaml` is a map from operation name to mode:

```yaml
execution:
  modes:
    aws-sts: library
```

- Every operation defaults to `subprocess` when unset (or set to anything other than `library`), so upgrading erun never silently changes what runs.
- The switch takes effect immediately — no rebuild or release needed.
- `erun doctor` reports the resolved mode for every operation that has a library alternative; see [`erun doctor`](/cli/doctor#what-it-checks).
- Today's promoted operations:

  | Operation | What it covers | What stays on subprocess |
  |---|---|---|
  | `aws-sts` | `aws sts get-caller-identity`, used to resolve the caller's AWS identity and check whether a stored AWS session is still active. | Every other `aws` operation (`sso login`/`logout`, `configure set`, `configure export-credentials`) — these drive a real browser SSO flow or write the shared `~/.aws` ini files, neither of which the switch touches. |
  | `aws-sts-web-identity-token` | `aws sts get-web-identity-token`, used by `cloud oidc` to mint the OIDC bearer token that federates an AWS-provider alias with erun. | The federation-enable recovery (`aws iam enable-outbound-web-identity-federation`) this operation retries through on failure — still subprocess-only. |

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

## Migration and planned changes {#planned-changes}

ERun has moved from the legacy `remote` + `snapshot` field pair to one explicit `EnvConfig.type`. `type` is the single signal for the env's *shape* — its worktree storage and chart wiring:

- ✅ `EnvConfig.type` is written by `erun init --type`, the desktop env settings, or by editing the YAML directly.
- ✅ The helm chart wiring (`worktreeStorage=host|pvc|none`, `ERUN_ENV_TYPE`) branches on `type`. The delivery commands (`erun build`, `erun push`, `erun deploy`, `erun open`) are [pure primitives](/concepts/command-primitives) and do **not** branch on `type` — `build` mints a version, `push` publishes it, `deploy` installs it by reference, `open` opens a shell. The caller (the desktop app, or an Operator) decides which primitives to run for a given env.
- ✅ `EnvConfig.localRepoPath` is the local-host project path the env was created against. `erun init` seeds it for **every** env type (#549) — it is the single source for cwd→tenant matching, the `erun open` repo path, and the deploy worktree repo name. For `local-agent` envs it is also the hostPath mounted into the pod; for `host` envs it *is* the environment, with no pod to mount it into; `remote-agent` / `runtime` envs use a PVC worktree, so the value names the in-pod worktree (by its basename) but is never mounted. For `local-agent` and `host` envs it can be **retargeted after creation** from the desktop's env settings (General tab → Repository path, with a native Browse picker); the change applies on Save (and, for `local-agent`, takes effect on the next deploy — a `host` env has no deploy to take effect on). For `remote-agent` / `runtime` envs the field stays read-only there — their repo is not a local host path.
- ✅ `EnvConfig.remote`, `EnvConfig.snapshot`, and the matching `TenantConfig` fields are **removed**. A config written before `type` existed is migrated on read: ERun parses the legacy `remote`/`snapshot` keys, derives `type` per the table below, and discards them. No action is needed — re-saving an env (e.g. from the desktop) persists the resolved `type`.
- ✅ `EnvConfig.repopath` is removed — migrated into `localRepoPath` on read, dropped on the next save.
- ✅ `TenantConfig.projectroot` is removed (see [Field-level moves](#field-level-moves)). Working-directory→tenant matching now compares the cwd against each tenant's environments' `localRepoPath`; a config written with `projectroot` still loads — the key is ignored on read and dropped on the next save.

### Legacy `remote`/`snapshot` → `type` migration {#envconfig-type-truth-table}

A config with no `type` is migrated from the retired keys on read. `snapshot` absent is read the same as `snapshot: false`:

| `remote` | `snapshot` | Migrated `type` | Worktree storage | Build behaviour |
|---|---|---|---|---|
| `false` | `true` | `local-agent` | `hostPath` mount of `localRepoPath` | snapshot tags, builds in-pod |
| `true` | `true` | `remote-agent` | PVC checkout cloned from git | snapshot tags, builds in-pod |
| `true` | `false` / absent | `runtime` | none | release tags only, no builds |
| `false` | `false` / absent | (unresolved) | `hostPath` (fallback) | — |

See [Environment types](/concepts/environment-types) for what each value means in practice.

### Field-level moves {#field-level-moves}

| Field | Status | Why |
|---|---|---|
| `EnvConfig.remote` | ✅ Removed | Subsumed by `type` (`local-agent` ↔ false; `remote-agent` / `runtime` ↔ true). Legacy YAML migrated on read. |
| `EnvConfig.snapshot` | ✅ Removed | Subsumed by `type` (`local-agent` / `remote-agent` ↔ true; `runtime` ↔ false). Legacy YAML migrated on read. |
| `TenantConfig.remote` / `TenantConfig.snapshot` | ✅ Removed | Belonged on the env, not the tenant; subsumed by per-env `type`. |
| `TenantConfig.projectroot` | ✅ Removed; cwd→tenant matching iterates over envs' `localRepoPath` (longest match wins; ambiguous ties resolve to no match). Legacy YAML ignored on read and dropped on save. | A tenant can host both local and remote envs; the path lives on the env, not the tenant. |
| `EnvConfig.repopath` | ✅ Removed; migrated into `EnvConfig.localRepoPath` on read and dropped on save. | Scoped explicitly: the local-machine path mounted into the pod. For PVC envs the field is unset — the repo lives inside the pod at a fixed convention. |

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
| `type` | string (enum) | `erun build`, `erun open`, `erun deploy`, helm chart (`worktreeStorage`, `ERUN_ENV_TYPE`) | `local-agent`, `remote-agent`, or `runtime`. The canonical — and only — signal for what this env is for. Configs written before `type` existed are migrated to a concrete value on read (see [Legacy migration](#planned-changes)). See [Environment types](/concepts/environment-types). |
| `localRepoPath` | string | helm chart (`worktreeHostPath` for `local-agent` envs), `erun deploy` (project-config load, registry resolution) | Absolute path to the repo on the local machine. Only meaningful for `local-agent` envs; left empty for `remote-agent` and `runtime`. |
| `repopath` | string | (legacy, read-only) | Removed struct field. A `repopath:` in an older config is migrated into `localRepoPath` on read and dropped on the next save; new configs only carry `localRepoPath`. ([Migration done.](#planned-changes)) |
| `kubernetescontext` | string | `erun open`, `erun deploy`, `erun list` | Kubernetes context to deploy/open against. Special value `in-cluster` is set inside the runtime pod. |
| `containerregistries` | list | `erun build`, `erun push`, `erun deploy`, build image tag resolution | Per-env marked registry list, set for `remote-agent`/`runtime` envs whose project config is not on the local machine (`local-agent` envs resolve the list from the project's `.erun/config.yaml` instead). Each entry is `{registry, roles}` where roles ⊆ `build`/`from`/`to`/`deploy`. See [Container registries](#container-registries). |
| `cloudprovideralias` | string | `erun open`, `erun deploy`, idle-stop, audit labels | Which AWS cloud provider identity backs this env. Resolves to a `cloudproviders[]` entry. |
| `cloudprovideraliases` | map (provider → alias) | `erun open`, `erun deploy` | Additional cloud aliases attached to this env, keyed by provider type (e.g. `cloudflare`). AWS stays in the scalar `cloudprovideralias` above for backward compatibility; non-AWS providers live here. An env holds at most one alias per provider type, so a runtime pod can receive both an AWS identity and a Cloudflare token at once. A Cloudflare alias injects `CLOUDFLARE_API_TOKEN` / `CLOUDFLARE_ACCOUNT_ID` for in-pod tooling. |
| `managedcloud` | bool | helm chart (`ERUN_CLOUD_ENVIRONMENT`) | When true, marks the env as running on a managed cloud context (enables idle-stop, cloud-credential refresh, etc.). |
| `runtimeversion` | string | `erun open`, `erun deploy`, chart appVersion | Pins the version of the runtime image used by this env. |
| `runtimeregistry` | string | `erun open`, `erun deploy` | Overrides the registry the runtime image is pulled from (per-env). |
| `runtimeimage` | string | `erun open`, `erun deploy` | Points the env's runtime pod at a custom image instead of the published `<registry>/erun-devops:<version>` default. A full reference (`ghcr.io/acme/acme-runtime:1.2.3`) is used verbatim; a bare name resolves to `<registry>/<name>:<runtime version>`. Set by `erun init --runtime-image`; carried to the published chart as `imageOverrides.erun-devops` on every deploy (see [Advanced chart values](#advanced-chart-values)). |
| `autoupgrade` | bool | [`erun upgrade`](/cli/upgrade), desktop Upgrade all | When true, this env joins the Upgrade-all set: `erun upgrade` redeploys it to the latest version for its channel when `runtimeversion` lags. |
| `disablebuildscript` | bool | [`erun build`](/cli/build), `erun build --deploy` | When true, erun ignores any project `build.sh` for this env and resolves docker/release builds directly; if there is no docker build context either, the build errors with no buildable context. Default false. |
| `upgradechannel` | string (enum) | [`erun upgrade`](/cli/upgrade) | Release channel an upgrade targets: `stable` (semver releases) or `snapshot` (latest snapshot build, or the stable release once one is published on top of it — see [`erun upgrade`](/cli/upgrade#what-opted-in-means)). Orthogonal to `type`. When unset, defaults from `type` — runtime → `stable`, agent → `snapshot`. |
| `runtimepod.cpu` | string | helm chart (`runtime.resources.limits.cpu`) | CPU limit for the runtime pod (e.g. `4`, `500m`). |
| `runtimepod.memory` | string | helm chart (`runtime.resources.limits.memory`) | Memory limit (e.g. `8916Mi`, `2Gi`). |
| `sshd.enabled` | bool | `erun open`, chart (SSH port-forward setup) | Whether the in-pod SSH server is exposed via port forward. Needed for IDE attach. |
| `sshd.localport` | int | `erun open` | Local port the desktop binds for the SSH forward. `0` = auto-allocate. |
| `sshd.publickeypath` | string | `erun open --vscode`, `erun open --intellij`, SSH key sync | Path to the SSH public key authorized for the env. |
| `sshd.workspacesync.enabled` | bool | desktop workspace-sync poller | Mirror a local folder into the runtime workspace. |
| `sshd.workspacesync.localpath` | string | desktop workspace-sync poller | The local folder to mirror. |
| `deploy.timeout` | duration (e.g. `5m0s`) | `erun deploy`, `erun upgrade` (helm `--timeout`) | Per-env helm rollout wait. How long `deploy` waits for the rollout to become ready before helm times out; the [pod monitor](/agent-reference/cli-flags#rollout-wait-and-pod-monitoring) keeps waiting up to this bound while an image is still pulling and aborts earlier on a real failure. Unset → `5m0s`. Overridden per-deploy by `--rollout-timeout` / the MCP `deploy` `timeout` input. A malformed value fails the deploy. |
| `deploy.components` | list | `erun deploy` | Per-machine saved deploy selection: the charts `erun deploy` rolls out for this env by default (chart directory names under `<tenant>-devops/k8s/`, plus the runtime release name `<tenant>-devops`). The desktop app's Runtime-tab checklist writes it via "Set as default". Deploy is opt-in: `--components` overrides this per run; when both are empty, deploy falls back to the project's [`k8s.deployments`](#per-project-config) plan, then to the runtime chart alone. See [selection precedence](/agent-reference/cli-flags#components-value-set). Empty → no saved selection. |
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

The four `claude.*` rows above (`usemantle`, `usebedrock`, `models[]`, `maxoutputtokens`) are the Claude values erun manages itself. The runtime chart accepts further `claude.*` values that erun never sets — pin a model, point Bedrock at a VPC endpoint, tune prompt caching — via the env's values overlay; see [Advanced chart values](#advanced-chart-values).

The env's Claude AI tab also launches with **Remote Control** enabled by default: the managed launch appends `--remote-control <tenant>/<env>` after the effort/model/verbose flags, so the session is drivable from the Claude iOS app under that name. It is omitted when `claude.usemantle` or `claude.usebedrock` is set — Remote Control pairs through the claude.ai account relay, which those gateway auth modes can't authenticate — and follows the same verbatim-launch carve-out as `claude.effort` (a non-`claude` `aitool`, or a Claude launch written with explicit flags, is left untouched). An environment name that isn't a plain token (alphanumerics, `.`, `-`, `_`, starting with an alphanumeric) falls back to the unnamed `--remote-control`, leaving Claude Code to name the session from the pod hostname. For the Operator view, see [Desktop app](/desktop/overview).

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
| `environments.<env>.k8s.deployments[]` | ordered list | `erun deploy` | The ordered deploy plan for this env. Each step is either a single component name or a list of names deployed in parallel. |
| `release.mainbranch` | string | `erun release` | Main branch name (default `main`). |
| `release.developbranch` | string | `erun release` | Develop branch name (default `develop`). |
| `platform` | map | `erun deploy` (PowerDNS), `erun expose` | Per-instance platform deployment config. Absent for non-platform projects. See [`platform:` block](#platform-block). |

#### `platform:` block {#platform-block}

The `platform:` block configures an **erunpaas platform deployment** — an installation that runs the global DNS singleton (PowerDNS) and exposes tenant services under a delegated zone. ERun's platform is generic, installable software: any vendor deploys it under their **own** names, so every value is configuration — nothing (not even the base domain) is hardcoded. `erun deploy` threads these as `platform.*` helm values (only the `erun-powerdns` chart reads them today); [`erun expose`](/cli/expose) reads them to resolve service hostnames.

The whole block is optional. An empty block means the project runs no platform deployment. Once any field is set the block is "in use" and is validated at deploy/expose time — a malformed block fails fast.

| Field | Type | Required | Effect / default |
|---|---|---|---|
| `basedomain` | string | yes (when block in use) | The registered domain this deployment serves, e.g. `erunpaas.com`. Everything else derives from it. Must be a valid domain name. |
| `env` | string | no | The dedicated platform environment that owns the singletons (PowerDNS, the DNS-01 broker), e.g. `frs-prod`. Must be a DNS-safe `<tenant>-<env>` namespace label. |
| `serviceszone` | string | no | The child zone delegated to this deployment's PowerDNS, under which tenant services are exposed. Default `services.<basedomain>`. Must be a valid domain at or under `basedomain`. |
| `authoritativeip` | string | no | The public IP this deployment's authoritative nameserver answers on (the glue-record target for `serviceszone`). Must parse as an IP when set. |
| `nameservers` | list | no | The NS hostnames the parent zone delegates `serviceszone` to. Default `[ns1.<basedomain>, ns2.<basedomain>]`. |
| `authhost` | string | no | The hosted-IdP host, served from the apex zone (not `serviceszone`). Default `auth.<basedomain>`. Must be a valid domain at or under `basedomain`. |
| `acmeemail` | string | no | The account email for this deployment's Let's Encrypt registration (LE rate limits are per registered domain, so each deployment uses its own account). |

```yaml
# <repo>/.erun/config.yaml
platform:
  basedomain: erunpaas.com
  env: frs-prod
  authoritativeip: 203.0.113.10
  # serviceszone, authhost, and nameservers default from basedomain:
  #   serviceszone: services.erunpaas.com
  #   authhost:     auth.erunpaas.com
  #   nameservers:  [ns1.erunpaas.com, ns2.erunpaas.com]
```

A second vendor installs the same artifacts under their own names — e.g. `basedomain: kppaas.com`, `env: kp-prod` — with no code changes.

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
| `claude.smallFastModelAWSRegion` | `ANTHROPIC_SMALL_FAST_MODEL_AWS_REGION` | `cloudContext.region` | AWS region for the small/fast helper model, when it differs from the env's region. |

The `claude.*` keys erun does manage — `claude.useBedrock`, `claude.useMantle`, `claude.availableModels`, `claude.maxOutputTokens` — come from the [`EnvConfig` fields above](#envconfig) and are always `--set`; an overlay value for them is ignored.

### Runtime pod resource requests {#advanced-runtime-requests}

erun manages only the runtime pod's resource **limits**: `EnvConfig.runtimepod.cpu` / `.memory` (set with `erun init --runtime-cpu` / `--runtime-memory` or the desktop's env settings) are always `--set` as `runtime.resources.limits.{cpu,memory}`. The requests are overlay-only:

| Chart value | Default | Effect |
|---|---|---|
| `runtime.resources.requests.cpu` | `0.25` | CPU request for the runtime pod. |
| `runtime.resources.requests.memory` | `1024Mi` | Memory request for the runtime pod. |

Request overrides are invisible to `erun open`'s redeploy drift detection — it compares the deployed limits against `EnvConfig.runtimepod` and ignores requests — so changing a request in the overlay takes effect on the next deploy, not automatically on the next open.

### Runtime image override {#advanced-image-overrides}

`imageOverrides.erun-devops` is a supported public value of the runtime chart: it replaces the image the `erun-devops` container runs while keeping the rest of the chart canonical. The supported way to set it is the [`EnvConfig.runtimeimage`](#envconfig) field (`erun init --runtime-image`), which erun passes as `--set-string imageOverrides.erun-devops=<image>` on every deploy of the published chart — the intended path for custom toolchain images built `FROM` the published `erun-devops` image (the `erun-build-env` [skill](/concepts/skills) walks through it). When `runtimeimage` is unset, erun passes no override and the overlay may set the value directly.

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
- ✅ `EnvConfig.localRepoPath` is the local-host project path the env was created against. `erun init` seeds it for **every** env type (#549) — it is the single source for cwd→tenant matching, the `erun open` repo path, and the deploy worktree repo name. For `local-agent` envs it is also the hostPath mounted into the pod; `remote-agent` / `runtime` envs use a PVC worktree, so the value names the in-pod worktree (by its basename) but is never mounted. For `local-agent` envs it can be **retargeted after creation** from the desktop's env settings (General tab → Repository path, with a native Browse picker); the change applies on Save and takes effect on the next deploy. For `remote-agent` / `runtime` envs the field stays read-only there — their repo is not a local host path.
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

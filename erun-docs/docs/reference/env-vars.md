---
title: Environment variables
---

# Environment variables

ERun reads a small number of `ERUN_*` variables, mostly when running inside a runtime pod.

## In-pod variables (set by the helm chart)

| Variable | Type | Default | Purpose | Source |
|---|---|---|---|---|
| `ERUN_REPO_PATH` | absolute path | `/home/erun/git/<repo>` | Project checkout inside the pod. In-pod `erun` resolves the project root from it (so `erun terraform` and the MCP repo-path find the tree); a sourceless runtime env's release tree is symlinked here with no `.git`, so the host's git-repo walk can't apply. | Helm chart (`worktreeHostPath` template). |
| `ERUN_OUTPUTS_DIR` | absolute path | `/home/erun/.erun/outputs` | Canonical agent outputs directory: where agents/skills write deliverables that [`erun outputs`](/cli/outputs) lists and downloads. On the home PVC, so it persists across pod restarts. | Helm chart (literal on the runtime + MCP containers); created by the image and the entrypoint. |
| `ERUN_REPO_REMOTE` | bool literal `true`/`false` | unset on host; `true` in pod | Marks the pod as a runtime pod. Used by `IsInRuntimeEnvironment`. | Helm chart, only when env type is `remote-agent` or `runtime`. |
| `ERUN_REPO_URL` | git remote URL | unset unless mounting source | Git remote the runtime pod clones into `ERUN_REPO_PATH` on first boot, for a runtime env that opted into a mutable source worktree. The entrypoint clones only into an empty worktree, so live edits survive restarts. | Helm chart, only when [`EnvConfig.mountsource`](/reference/configuration#envconfig) is set with a `repourl`. |
| `ERUN_REPO_REF` | git ref (release tag) | unset unless mounting source | Ref checked out after the clone — the deployed release tag `v<version>`. Best-effort: an unresolvable ref leaves the clone on its default branch. | Helm chart (`v` + the deployed version). |
| `ERUN_ENV_TYPE` | enum `local-agent`/`remote-agent`/`runtime` | (set in pod) | The env's resolved type. The pod entrypoint writes it into the in-pod `EnvConfig.type`, so in-pod `erun` resolves the same type the laptop did. | Helm chart (the inverse of the `worktreeStorage` mapping). |
| `ERUN_TENANT` | string | (required) | Tenant name. | Deploy plan (the tenant under which the chart runs); not a field on `EnvConfig`. |
| `ERUN_ENVIRONMENT` | string | (required) | Environment name. | `EnvConfig.name`. |
| `ERUN_KUBERNETES_CONTEXT` | string | `in-cluster` | Always `in-cluster` inside the pod. | Helm chart literal. |
| `ERUN_NAMESPACE` | string | `<tenant>-<env>` | Pod's Kubernetes namespace. | Downward API (`metadata.namespace`). |
| `ERUN_MCP_PORT` | int (1024–65535) | `17000` | MCP server listener. | Allocated by the deploy plan from `EnvConfig.localportrangestart`; passed to the chart via `--set mcpPort`. |
| `ERUN_SSHD_PORT` | int (1024–65535) | `22` | In-pod SSH server. | Hardcoded by the chart at 22 inside the pod. `EnvConfig.sshd.localport` controls the host-side forward port, not the in-pod port. |
| `ERUN_IDLE_TIMEOUT` | duration (Go `time.ParseDuration` grammar) | `5m` | See [`EnvConfig.idle.timeout`](/reference/configuration#envconfig). | `EnvConfig.idle.timeout`. |
| `ERUN_IDLE_WORKING_HOURS` | string `HH:MM-HH:MM` | unset | Window during which idle-stop may fire. | `EnvConfig.idle.workinghours`. |
| `ERUN_IDLE_TIMEZONE` | IANA TZ | host TZ | TZ for `WORKING_HOURS`. | `EnvConfig.idle.timezone`. |
| `ERUN_IDLE_TRAFFIC_BYTES` | int64 | `65536` | Below-threshold quiet bytes. | `EnvConfig.idle.idletrafficbytes`. |
| `ERUN_CLOUD_ENVIRONMENT` | string | unset | Cloud-context alias; presence signals managed cloud. | `EnvConfig.cloudprovideralias` resolution. |
| `ERUN_CLOUD_CONTEXT_NAME` | string | unset | Cluster id. | Cloud-context lookup. |
| `ERUN_CLOUD_PROVIDER` | enum (`aws`, `gcp`, `azure`, `onprem`) | unset | Provider kind. | Cloud-context lookup. |
| `ERUN_CLOUD_PROVIDER_ALIAS` | string | unset | Provider alias (admin-defined). | Cloud-context lookup. |
| `ERUN_CLOUD_REGION` | string | unset | Cloud region (e.g. `eu-west-2`). | Cloud-context lookup. |
| `ERUN_CLOUD_INSTANCE_ID` | string | unset | Provider-specific instance id (EC2 InstanceId, GCE name, etc.). | Cloud-context lookup. |
| `AWS_PROFILE` | string | unset | `erun-host` on an AWS environment that carries a cloud alias, selecting the profile ERun writes the operator's short-lived credentials into (`erun cloud refresh`, `erun open`, the desktop refresher). Absent otherwise. | `cloudContext.useHostCredentials`, set by deploy from `EnvConfig.cloudprovideralias`. |
| `AWS_REGION` | string | unset | Default AWS region for every SDK and CLI call in the pod, on an AWS environment. **Emitted only when a region resolves** — an empty `AWS_REGION` would override the region the pod's own AWS profile carries instead of falling back to it, so "no region resolved" means the variable is absent. Resolution order: managed cloud context → kubeconfig context name → the alias's Identity Center region → the region in an ECR registry host. | `cloudContext.region`, threaded by deploy only when non-empty. |
| `ANTHROPIC_SMALL_FAST_MODEL_AWS_REGION` | string | `AWS_REGION` | Region for Claude's small/fast helper model. Follows the same omit-when-unresolved rule as `AWS_REGION`. | `claude.smallFastModelAWSRegion`, defaulting to `cloudContext.region`. |
| `ERUN_RUNTIME_REGISTRY` | string | unset | Registry erun resolves runtime image refs / runtime versions against. When unset the in-pod config omits it and resolution falls back to `ghcr.io/sophium`. | `EnvConfig.runtimeregistry` via the deploy spec (`--set-string runtimeRegistry`). |
| `ERUN_CONTAINER_REGISTRIES` | JSON | unset | The env's marked registry list (`[{"registry":"…","roles":["build","deploy"]}]`), so in-pod build/push role resolution works on remote/runtime pods whose list lives only on the env config rather than in a repo `.erun/config.yaml`. When unset the in-pod config omits it. | `EnvConfig.containerregistries` via the deploy spec (`--set-json containerRegistries`). |
| `ERUN_DISABLE_BUILD_SCRIPT` | bool | `false` | Disable `build.sh` discovery for in-pod (remote-agent) builds. Always written (`true` and `false`) when the chart sets it, so `erun doctor --sync-config` can reconcile a flip; an older chart that does not set it yields `false`. | `EnvConfig.disablebuildscript` via the deploy spec (`--set disableBuildScript`). |
| `CLAUDE_CODE_USE_MANTLE` | bool | unset | Route Claude through Mantle. | `EnvConfig.claude.usemantle`. |
| `CLAUDE_CODE_USE_BEDROCK` | bool | unset | Route Claude through AWS Bedrock. | `EnvConfig.claude.usebedrock`. |
| `CLAUDE_CODE_MAX_OUTPUT_TOKENS` | int | unset | Max tokens per Claude response. | `EnvConfig.claude.maxoutputtokens`. |
| `ERUN_CLAUDE_AVAILABLE_MODELS` | comma-separated strings | unset | Allow-list of Claude model identifiers. When a gateway is configured this is the gateway catalog's model ids, so the models the operator maintains once at erun level are what each environment's AI tab offers. | `EnvConfig.claude.models[]`, or the root config's gateway catalog. |
| `ANTHROPIC_API_KEY` | string | unset | Read directly by Claude Code. Set only via Kubernetes Secret reference in the runtime chart's values. Set to an **empty string** when a gateway is configured: an API key left unset lets Claude Code fall back to a direct provider, which would bypass the gateway the operator asked for. | External Secret, or the gateway catalog. |
| `ANTHROPIC_BASE_URL` | URL | unset | Override the Claude Code API endpoint. Emitted only when the erun-level gateway catalog names a base URL, and then for **every environment regardless of cloud provider** — an operator's gateway replaces the model provider wherever the env runs, which is why this is not gated the way the Bedrock/Mantle variables are. | Root config `openrouter.baseurl`. |
| `ANTHROPIC_AUTH_TOKEN` | string | unset | The gateway credential, which Claude Code sends as `Authorization: Bearer`. Its value comes from the Secret `erun-claude-gateway`, which `erun deploy` writes into the environment's namespace from the credential in ERun's own operator secret store — so no token reaches config, helm argv, or a launch command. | Root config `openrouter.authtokenref`, or this machine's own Claude Code credential when nothing is saved **and those settings already point at the same `openrouter.baseurl`**. |
| `CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY` | bool | unset | Ask the gateway for its model list — `GET /v1/models?limit=1000` — and add the returned names to the `/model` picker alongside the built-in entries. Claude Code keeps only entries whose **id contains `claude` or `anthropic`**, matched case-insensitively, and ignores the rest, so a gateway serving other vendors' models under its own ids (for example `deepseek/…`) will not list them here. Those models still run: a session reaches them through the model the environment selects, and the catalog's models are what the tab offers. Set whenever a gateway is configured. | Root config `openrouter`. |
| `CLAUDE_CODE_MAX_CONTEXT_TOKENS` | int | unset | The context window Claude Code should assume. A gateway model id carries none of its own, so without this Claude Code assumes one that may differ from the model's real window. Set pod-wide from the catalog's default model, and per launch from the model the environment selected. | Root config `openrouter.models[].context`. |

Each `EnvConfig.*` reference is fully spec'd in [Configuration · EnvConfig](/reference/configuration#envconfig).

## Orchestrator-session variables

A host-side orchestrator session has no pod, so none of the variables above apply
to it. The desktop app sets a smaller, disjoint set on the session process at
launch, and the shared orchestrator contract reads its scope from them.

| Variable | Type | Default | Purpose | Source |
|---|---|---|---|---|
| `ERUN_ORCHESTRATOR_ID` | string | unset | **The session's identity.** The orchestrator's own id, which keys into the `orchestrators:` list in erun's `config.yaml`: matching this id is how the session resolves which environments are its own, and it names the session's `RESUME-NOTE.<id>.md`. An empty value means a transient (Investigate) session with no id and no linked environments. | Orchestrator spawn. |
| `ERUN_ORCHESTRATOR_LAUNCH` | UUID | unset | Per-launch nonce. The session's own hooks stamp it onto the conversation id they report, so a restart's hand-off attaches to the launch that asked for it rather than to any session carrying the same orchestrator id. | Orchestrator spawn, minted once per launch. |
| `ERUN_OUTPUTS_DIR` | absolute path | unset | Host directory this session's deliverables are written to, so the outputs convention an in-pod agent follows still has a target with no pod. See the in-pod table above for the same variable inside a runtime pod. | Orchestrator spawn; omitted for a transient session, which has no id and so no directory of its own. |
| `ERUN_UI_SESSION` | bool literal `1` | unset | Internal. Marks the process as one the desktop app started, so the app can tell its own session from a shell the Operator opened by hand. Do not depend on this. | Desktop app, for every session it launches (orchestrator and in-app shell alike). |
| `ERUN_DEV_BIN_DIR` | absolute path | `erun-cli/bin` | Internal to the development wrapper `erun-cli/run.sh`, which reads it as the directory it builds `erun` and `erun-app` into. An Operator sets it to a directory outside the checkout so that invoking `erun` does not write into a worktree — which a host-side orchestrator treats as a read-only review directory. Do not depend on this; it is not part of the session contract. | `erun-cli/run.sh` (read from the environment, never set by erun). |

One variable outside the `ERUN_*` namespace is set here too:
`CLAUDE_CODE_SUBAGENT_MODEL` (string, `opus`), which pins the model the
session's subagents run on. The in-pod Claude variables above are unrelated —
those come from `EnvConfig.claude.*` and describe a pod, not a session.

Read scope from `ERUN_ORCHESTRATOR_ID`, never from memory or disk: the id is the
only thing that ties a session to its `orchestrators:` entry.

## CLI-side variables

| Variable | Type | Default | Purpose |
|---|---|---|---|
| `ERUN_IDLE_PROBE` | bool literal `true` | unset | Hint that the CLI is being invoked by the desktop's idle prober. When set, suppresses interactive output. |
| `ERUN_FORCE_TTY` | bool literal `1` | unset | Internal test seam. Reports stdout as a terminal to a piped run, so an interactive path can be exercised without a TTY. Do not depend on this. |
| `ERUN_LOCAL_SHELL_OVERRIDE` | bool literal `1` | unset | Internal test seam. Launches the Local tab's shell as a genuine interactive POSIX shell with a pinned prompt and no rc files, so a terminal-content test is not at the mercy of the Operator's own `$SHELL` dotfiles. Do not depend on this. |
| Docker / Helm / kubectl standard variables | various | per tool | Honoured as documented by each tool (e.g. `DOCKER_HOST`, `KUBECONFIG`, `HELM_NAMESPACE`). |

## Variables NOT read by ERun

The following look ERun-related but are not consumed:

- `ERUN_VERSION` — compiled into the binary at build time (`-ldflags -X main.Version=…`). Not read from the environment.
- `ERUN_HOME` — there is no such variable; per-user config lives under `<config-root>/`, the platform's own config directory (see [Config locations](/reference/config-locations)), and per-environment state always lives under `~/.erun/`.

A variable not in either table above is ignored.

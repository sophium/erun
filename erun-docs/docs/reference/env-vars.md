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
| `ERUN_RUNTIME_REGISTRY` | string | unset | Registry erun resolves runtime image refs / runtime versions against. When unset the in-pod config omits it and resolution falls back to `ghcr.io/sophium`. | `EnvConfig.runtimeregistry` via the deploy spec (`--set-string runtimeRegistry`). |
| `ERUN_CONTAINER_REGISTRIES` | JSON | unset | The env's marked registry list (`[{"registry":"…","roles":["build","deploy"]}]`), so in-pod build/push role resolution works on remote/runtime pods whose list lives only on the env config rather than in a repo `.erun/config.yaml`. When unset the in-pod config omits it. | `EnvConfig.containerregistries` via the deploy spec (`--set-json containerRegistries`). |
| `ERUN_DISABLE_BUILD_SCRIPT` | bool | `false` | Disable `build.sh` discovery for in-pod (remote-agent) builds. Always written (`true` and `false`) when the chart sets it, so `erun doctor --sync-config` can reconcile a flip; an older chart that does not set it yields `false`. | `EnvConfig.disablebuildscript` via the deploy spec (`--set disableBuildScript`). |
| `CLAUDE_CODE_USE_MANTLE` | bool | unset | Route Claude through Mantle. | `EnvConfig.claude.usemantle`. |
| `CLAUDE_CODE_USE_BEDROCK` | bool | unset | Route Claude through AWS Bedrock. | `EnvConfig.claude.usebedrock`. |
| `CLAUDE_CODE_MAX_OUTPUT_TOKENS` | int | unset | Max tokens per Claude response. | `EnvConfig.claude.maxoutputtokens`. |
| `ERUN_CLAUDE_AVAILABLE_MODELS` | comma-separated strings | unset | Allow-list of Claude model identifiers. | `EnvConfig.claude.models[]`. |
| `ANTHROPIC_API_KEY` | string | unset | Read directly by Claude Code. Set only via Kubernetes Secret reference in the runtime chart's values. | External Secret. |
| `ANTHROPIC_BASE_URL` | URL | unset | Override the Claude Code API endpoint. | Per-env chart values. |

Each `EnvConfig.*` reference is fully spec'd in [Configuration · EnvConfig](/reference/configuration#envconfig).

## CLI-side variables

| Variable | Type | Default | Purpose |
|---|---|---|---|
| `ERUN_IDLE_PROBE` | bool literal `true` | unset | Hint that the CLI is being invoked by the desktop's idle prober. When set, suppresses interactive output. |
| Docker / Helm / kubectl standard variables | various | per tool | Honoured as documented by each tool (e.g. `DOCKER_HOST`, `KUBECONFIG`, `HELM_NAMESPACE`). |

## Variables NOT read by ERun

The following look ERun-related but are not consumed:

- `ERUN_VERSION` — compiled into the binary at build time (`-ldflags -X main.Version=…`). Not read from the environment.
- `ERUN_HOME` — there is no such variable; per-user config lives under `~/.config/erun/` (or the OS-equivalent path; see [Config locations](/reference/config-locations)).

A variable not in either table above is ignored.

---
title: Environment variables
---

# Environment variables

ERun reads a small number of `ERUN_*` variables, mostly when running inside a runtime pod.

## In-pod variables (set by the helm chart)

| Variable | Purpose |
|---|---|
| `ERUN_REPO_PATH` | Absolute path to the project checkout inside the pod (`/home/erun/git/<repo>`). |
| `ERUN_REPO_REMOTE` | `true` when running inside a runtime pod; used by `IsInRuntimeEnvironment`. |
| `ERUN_TENANT` | Tenant name. |
| `ERUN_ENVIRONMENT` | Environment name. |
| `ERUN_KUBERNETES_CONTEXT` | Always `in-cluster` inside the pod. |
| `ERUN_NAMESPACE` | Pod's Kubernetes namespace (from the downward API). |
| `ERUN_MCP_PORT` | Port the in-pod MCP server listens on. |
| `ERUN_SSHD_PORT` | Port the in-pod SSH server listens on. |
| `ERUN_IDLE_*` | Idle policy: `TIMEOUT`, `WORKING_HOURS`, `TIMEZONE`, `TRAFFIC_BYTES`. |
| `ERUN_CLOUD_*` | Linked cloud context: `ENVIRONMENT`, `CONTEXT_NAME`, `PROVIDER`, `PROVIDER_ALIAS`, `REGION`, `INSTANCE_ID`. |
| `CLAUDE_CODE_*`, `ANTHROPIC_*` | AI tooling configuration. |

## CLI-side variables

| Variable | Purpose |
|---|---|
| `ERUN_IDLE_PROBE` | Hint that the CLI is being invoked by the desktop's idle prober (used to suppress some output). |
| Docker / Helm / kubectl standard variables | Honored as documented by their respective tools. |

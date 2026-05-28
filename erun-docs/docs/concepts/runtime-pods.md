---
title: Inside an environment
---

# Inside an environment

When you open an environment, ERun creates (or reuses) a **dedicated Kubernetes namespace** for it. Everything that belongs to the env lives in that namespace — both ERun's own developer-access surface and the application services your project deploys.

<figure className="erun-hero-figure">
  <img src="/img/inside-environment.svg" alt="Inside one Kubernetes namespace. At the top, an Operator pill and an Agent pill connect via dashed arrows labelled SSH and MCP into a runtime pod that sits inside the namespace card. The runtime pod holds three charcoal containers — erun-devops (shell + erun + docker), erun-mcp (MCP server for agents), erun-dind (docker daemon sidecar). Below the runtime pod, still inside the same namespace, four application service boxes — frontend, api, db, queue — plus a '+ more' note. A strapline reads: 'One namespace = one full functioning copy of the project. Drop the namespace, everything goes with it.'" />
  <figcaption>One namespace = one full functioning copy of the project. The runtime pod is the shared surface for Operator (SSH) and Agent (MCP); the application services live alongside it.</figcaption>
</figure>

The namespace is the unit of isolation. Two envs of the same tenant run in two different namespaces; they can't see each other's pods, secrets, PVCs, or services. The point isn't just to host ERun's own pod — it's to **deploy a whole functioning copy of your project** inside the namespace without affecting any other env.

## What lives in the namespace

A typical env namespace holds two kinds of workload, side by side:

### 1. The ERun runtime pod (developer access)

One pod with three containers:

| Container | Role |
|---|---|
| `erun-devops` | Main shell + tools (`erun`, `docker`, `kubectl`, `helm`, `gh`, …). **Also ships the Agent CLI** — `claude`, `codex`, or whichever tool the env is configured for — pre-wired against the in-pod MCP loopback. The default Agent in the env runs inside this container. |
| `erun-mcp` | MCP server for Agents. Exposes structured tools (`idle`, `doctor`, `list`, `version`, `build`, `deploy`, `raw`, …). Reached at loopback by the in-pod Agent and via port-forward from laptop-side clients. |
| `erun-dind` | Docker daemon sidecar. Backs `/var/run/docker.sock` for the shell container's `docker` invocations. |

The three share two persistent volume claims: `/home/erun` (workspace + config) and `/var/lib/docker` (the daemon's image store, so builds stay cache-warm across pod restarts).

This pod is the **shared surface** for Operator and Agent. Two endpoints on the same pod, both accepting any client:

- **SSH** — a remote shell + filesystem surface. Operators attach via VS Code Remote-SSH, IntelliJ Gateway, Cursor, terminal, anything else that speaks SSH. **Claude Code and Codex desktop apps also attach here** — they open the env as a remote workspace like any other SSH-aware tool.
- **MCP** — a typed-tool surface (`idle`, `doctor`, `list`, `version`, `build`, `deploy`, `raw`, …). Used by Agents for structured calls and audit-friendly operations. Same MCP clients (Claude Code, Codex, custom Agents) typically use SSH and MCP together — SSH for file edits, MCP for ERun operations.

Both endpoints see the same `/home/erun` workspace, the same docker daemon, the same audit trail. **No parallel worlds — Operator and Agent are in the same environment.**

### 2. The application services you deploy

The components declared in `.erun/config.yaml`'s deploy plan — backend pods, frontends, databases, queues, ingresses, the migration jobs, whatever your project ships. Built from `<tenant>-devops/docker/*/`, rolled out via `<tenant>-devops/k8s/*/` charts.

These run in the same namespace as the runtime pod, with their own services and PVCs.

## Why a per-env namespace

- **Full functioning environment per env.** An agent can build, deploy, and exercise the entire system in their env without touching anyone else's. A feature-branch env can be a complete, runnable copy of prod.
- **One-command teardown.** `erun delete` drops the namespace; everything in it goes with it. PVCs reclaimed, services removed, no orphans.
- **Per-env RBAC.** The runtime pod's ServiceAccount is the single authorization surface for everything the agent does inside the env.
- **Parallelism without crosstalk.** N envs = N namespaces, hosted on the same cluster, each isolated from the others.

## Why a single pod for the developer surface

The three developer containers live in one pod, not three:

- File edits visible in `erun-devops` are immediately visible to the daemon in `erun-dind`.
- The MCP container inspects the same `/home/erun` filesystem the shell sees.
- One ServiceAccount, one RBAC scope, one audit surface.

## Idle / auto-stop

Cloud-backed envs participate in an idle policy — see [Cloud contexts](/concepts/cloud-contexts).

## Secrets

ERun doesn't ship its own secret-management layer — it uses Kubernetes' native primitives. Where do secrets come from?

| Source | Used by | How |
|---|---|---|
| **Kubernetes `Secret` objects in the env's namespace** | Application services | Helm charts in `<tenant>-devops/k8s/<component>/` reference them via `envFrom: secretRef:` or volume mounts. Create them with `kubectl create secret` or template them in your chart. |
| **OIDC service-account credentials** | Agents calling the erun API | Stored as a `Secret` in the env's namespace; mounted into the Agent's container at deploy time. See [Sign-in](/agent-reference/api-protocol#sign-in-oidc). |
| **Cloud credentials on the host** | The runtime pod (managed-cloud envs only) | When the env opts in via the desktop's env settings, the host's `~/.aws`, `~/.config/gcloud`, etc. are mounted into the pod read-only. |
| **SSH key** | IDE attach over SSH | A locally-stored public key, injected by the helm chart into the runtime pod. Path configured per env in the desktop. |
| **Registry auth** | `docker push` from inside the pod | Persisted at `~/.docker/config.json` in the pod's PVC. `erun push` reruns `docker login` interactively on a 401. |

Two rules ERun enforces:

1. **Never bake secrets into images.** Images are mutable history once published; secrets in layers leak forever. Use `Secret` references at deploy time, or `EnvConfig`-driven environment variables.
2. **Never log secret values.** `--dry-run` [redaction](/agent-reference/dry-run-redaction) applies to the live trace too — the rule is "if it looks like a secret, replace the value".

For cloud-native secret stores (AWS Secrets Manager, GCP Secret Manager, Vault) the pattern is the same as in production: an in-cluster sidecar fetches the secret and materialises it as a Kubernetes `Secret`, which your chart then references. ERun has no opinion on which sidecar you use.

## How many envs can run at once

The hard limit is your machine's CPU + memory (for local clusters) or the cloud context's instance type (for managed clusters). In practice, the runtime pod fits in ~4 CPU and ~9 GiB; a typical app stack adds 2–8 GiB per env. So:

- **16 GiB laptop** — 1–2 envs running side by side comfortably.
- **32 GiB laptop** — 3–4 envs.
- **64 GiB laptop** — 6–8 envs, or more if you trim per-env runtime-pod sizing to fit your workload.

Lower the runtime pod's CPU / memory per env from the desktop's env settings if you want more concurrency on a constrained machine. For the field names and defaults, see [Configuration · `EnvConfig`](/reference/configuration#envconfig). For genuinely heavy parallel work, point envs at a managed cloud context — see [Cloud contexts](/concepts/cloud-contexts).

---
title: Environment types
---

# Environment types

Every ERun environment is one of **three types**, determined by where its project worktree lives. Operator and Agent collaborate in agent envs (`local-agent` or `remote-agent`); runtime envs serve deployed services.

<figure className="erun-hero-figure">
  <img src="/img/env-type-decision.svg" alt="Decision diagram for picking an environment type. A charcoal box at the top asks 'What is this env for?'. Three branching arrows labelled 'iterate · machine', 'iterate · cloud', and 'serve only' lead to three cyan-stroked leaf boxes: LOCAL-AGENT (develop on your machine, cluster on laptop with Docker Desktop / OrbStack / k3d, worktree mounted from host, snapshot-tagged builds); REMOTE-AGENT (develop in the cloud, cluster doesn't share your filesystem with EKS / GKE / AKS, worktree cloned to PVC, snapshot-tagged builds); RUNTIME (serve deployed services, no development, no worktree, no builds, receives erun deploy artefacts)." />
  <figcaption>One question — three outcomes. Pick the leaf that matches what the env is *for*.</figcaption>
</figure>

<figure className="erun-hero-figure">
  <img src="/img/env-types.svg" alt="Three environment types side by side, each in its own light-grey Kubernetes namespace card. LOCAL-AGENT card: charcoal pill 'your machine' at top, a 'hostPath' arrow down, cyan-stroked 'runtime pod + worktree' box. REMOTE-AGENT card: 'git' charcoal pill, 'clone' arrow, 'runtime pod + worktree (PVC)'. RUNTIME card: 'registry' charcoal pill, 'pull image' arrow, 'deployed services (no worktree)'. Each card has a label at the bottom: 'cluster on your laptop · Docker Desktop · OrbStack · k3d'; 'cluster doesn't share your filesystem · EKS · GKE · AKS'; 'no development happens here · dev · test · dr · prod'." />
  <figcaption>Each env is a Kubernetes namespace. Where the worktree comes from — and whether there's a worktree at all — determines the type.</figcaption>
</figure>

| Type | Worktree source | Used for |
|---|---|---|
| **local-agent** | Mounted from the local machine's filesystem (Kubernetes `hostPath`) | An operator or agent developing on their own machine. |
| **remote-agent** | Cloned into the pod, persisted on the env's PVC | An operator or agent developing on a remote cluster where the local machine plays no role. |
| **runtime** | None — no worktree at all | Running deployed services. No development happens here; the env receives released artifacts and serves them. |

The three types form a spectrum from "developer iterates here" (local-agent and remote-agent) to "production serves here" (runtime).

## local-agent

The classic developer experience. The Kubernetes pod sees your local machine's working directory live; edits in your editor are immediately visible to the agent inside the pod. Builds happen here and produce snapshot-tagged artifacts.

Typical names: `local`, `<operator>-develop`, `<operator>-hotfix`.

Used when the cluster runs on your machine (Docker Desktop, kind, k3d) or shares a filesystem with your machine.

## remote-agent

Same iteration model as local-agent — builds happen here, snapshot tags, agent develops in the pod — but the project is cloned from git into the pod's PVC instead of mounted from the local machine. You attach via SSH (any IDE) or MCP (Codex, Claude Code) just like a local-agent env.

Typical names: `<operator>-develop` against a cloud cluster, or any agent env on a cluster whose nodes don't share your local filesystem.

## runtime

Where services run. No worktree, no development. Receives versioned artifacts via `erun deploy` and serves them.

Typical names: `dev`, `test`, `dr`, `prod`.

You don't develop in a runtime env. To make a change, you:

1. Develop the change in a local-agent or remote-agent env.
2. Build a new artifact (locked to a version).
3. Deploy that artifact to the runtime env.

## What concretely changes per type

The type describes the **environment** — where its worktree comes from and what it's for. It does **not** change what the `build` / `push` / `deploy` commands do: those are [pure primitives](/concepts/command-primitives) with no environment-type branch. What differs per type is whether the env has source to build from at all, and who supplies the commands (an Operator at the terminal, or the desktop app orchestrating them).

| Behavior | local-agent | remote-agent | runtime |
|---|---|---|---|
| Worktree in pod | Mounted from host | Cloned to PVC | None |
| Source to build from | Yes | Yes | None — nothing for `build` to act on |
| Typical loop | iterate: build → push → deploy a snapshot | iterate: build → push → deploy a snapshot | consume: `deploy --version` a published version |
| Editor / IDE attach | Yes (SSH + MCP) | Yes (SSH + MCP) | Not the normal pattern |
| Per-env helm overlay | Defaults are fine | Defaults are fine | Yes — each runtime env wants its own |

A runtime env has no worktree, so there's no source for `build` to act on and no reason to build there — you deploy a version into it that was built and pushed elsewhere. An agent env has source, so it's where the iterate loop runs. The desktop app reads the env's type to decide *which* primitives to run on the Operator's behalf, but the primitives themselves stay the same everywhere (see [Command primitives](/concepts/command-primitives)).

The exact snapshot tag format and how `erun build` resolves it lives at [Build path resolution](/reference/configuration-build-paths).

## Hotfix pattern

Patching a runtime env "in real time" isn't supported — runtime envs have no editable source. The pattern is to spin up a local-agent env beside it, on the same cluster:

<figure className="erun-hero-figure">
  <img src="/img/hotfix-pattern.svg" alt="Hotfix pattern. Inside one Kubernetes cluster, two cyan-stroked environment cards sit side by side. The left card is labelled LOCAL-AGENT ENV with name erun-prod-local, worktree ~/code/erun-prod, branch hotfix/urgent, and Operator + Agent pills working on edit · build · push. The right card is labelled RUNTIME ENV with name erun-prod and two service mini-boxes inside: backend-api :1.0.76 and postgres. A cyan arrow between the two cards is labelled 'erun deploy v1.0.77'. The strapline reads: 'Same cluster, two envs, two roles — erun-prod-local develops; erun-prod serves.'" />
  <figcaption>Two envs share the cluster. The local-agent env is where Operator + Agent edit and build; one `erun deploy` rolls the result into the runtime env beside it.</figcaption>
</figure>

To hotfix `erun-backend-api` in `erun-prod`:

1. Create `erun-prod-local` — a local-agent env targeting the same Kubernetes context as `erun-prod`, with the `erun-prod` worktree mounted from your local machine.
2. Open it. Operator + Agent have a normal development surface — editor, terminal, build tools — attached to a pod *inside the prod cluster*.
3. Make the change; build the new version.
4. Deploy to `erun-prod`:
   ```bash
   erun deploy erun-backend-api --tenant erun --environment prod --version 1.0.77
   ```
5. `erun-prod-local` stays as your active development surface against prod.

## Mapping to configuration

Set `EnvConfig.type` to one of `local-agent`, `remote-agent`, or `runtime`. When `type` is set it is the source of truth for the env's shape — its worktree storage (`worktreeStorage=host|pvc|none`) and the `ERUN_ENV_TYPE` the helm chart wires in. The `build` / `push` / `deploy` commands do **not** branch on it; the caller (the desktop app, or an Operator) decides which primitives to run for a given env.

The retired `EnvConfig.remote` and `EnvConfig.snapshot` fields no longer exist. A config written before `type` existed is migrated on read — ERun derives `type` from the old `remote`/`snapshot` keys per the [legacy migration table](/reference/configuration#envconfig-type-truth-table) and discards them. New envs created by `erun init --type` set `type` directly.

```bash
# Local-agent env (default if neither flag is given):
erun init team local --type local-agent

# Remote-agent env (replaces the older --remote flag):
erun init team dev --type remote-agent --no-git

# Runtime env (worktree-less; serves deployed artifacts):
erun init team prod --type runtime --no-git
```

`--remote` is preserved as a deprecated alias for `--type=remote-agent`; passing both flags with conflicting values is an error.

The desktop app's **New environment** dialog exposes the same choice as an **Environment type** field. Picking `Local agent` also reveals a **Local repo path** input (with a native folder picker) — that's the host directory mounted into the agent pod as the worktree, equivalent to the CLI's `--project-root`.

Inside a runtime pod, the on-disk env config is a projection of the env's configuration, written from the helm-injected `ERUN_*` env vars. It carries the build/deploy-relevant fields the pod acts on — `runtimeregistry`, `containerregistries`, and `disablebuildscript` (a `remote-agent` pod builds in-pod and needs the build/push registry list; a `runtime` pod only deploys and needs the deploy/runtime registry). If a pod's config drifts from the deploy spec, [`erun doctor --sync-config`](/cli/doctor) reconciles it in place (injected env wins), preserving the keys the env does not carry.

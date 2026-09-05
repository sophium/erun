---
title: Environment types
---

# Environment types

Every ERun environment is one of **four types**, determined by where its project worktree lives. Operator and Agent collaborate in agent envs (`local-agent` or `remote-agent`); runtime envs serve deployed services; a host env is a directory on the Operator's own machine with no pod and no cluster at all.

<figure className="erun-hero-figure">
  <img src="/img/env-type-decision.svg" alt="Decision diagram for picking an environment type. A charcoal box at the top asks 'What is this env for?'. Three branching arrows labelled 'iterate · machine', 'iterate · cloud', and 'serve only' lead to three cyan-stroked leaf boxes: LOCAL-AGENT (develop on your machine, cluster on laptop with Docker Desktop / OrbStack / k3d, worktree mounted from host, snapshot-tagged builds); REMOTE-AGENT (develop in the cloud, cluster doesn't share your filesystem with EKS / GKE / AKS, worktree cloned to PVC, snapshot-tagged builds); RUNTIME (serve deployed services, no development, no worktree, no builds, receives erun deploy artefacts)." />
  <figcaption>One question — three of the four outcomes. A host env sits outside this decision entirely: it exists only when the work genuinely cannot run in a pod at all — see <a href="#host">host</a> below.</figcaption>
</figure>

<figure className="erun-hero-figure">
  <img src="/img/env-types.svg" alt="Three environment types side by side, each in its own light-grey Kubernetes namespace card. LOCAL-AGENT card: charcoal pill 'your machine' at top, a 'hostPath' arrow down, cyan-stroked 'runtime pod + worktree' box. REMOTE-AGENT card: 'git' charcoal pill, 'clone' arrow, 'runtime pod + worktree (PVC)'. RUNTIME card: 'registry' charcoal pill, 'pull image' arrow, 'deployed services (no worktree)'. Each card has a label at the bottom: 'cluster on your laptop · Docker Desktop · OrbStack · k3d'; 'cluster doesn't share your filesystem · EKS · GKE · AKS'; 'no development happens here · dev · test · dr · prod'." />
  <figcaption>Each of these three is a Kubernetes namespace. A host env isn't — it has no namespace, no pod, and no cluster at all, which is exactly why it doesn't fit this picture.</figcaption>
</figure>

| Type | Worktree source | Used for |
|---|---|---|
| **local-agent** | Mounted from the local machine's filesystem (Kubernetes `hostPath`) | An operator or agent developing on their own machine. |
| **remote-agent** | Cloned into the pod, persisted on the env's PVC | An operator or agent developing on a remote cluster where the local machine plays no role. |
| **runtime** | None — no worktree at all | Running deployed services. No development happens here; the env receives released artifacts and serves them. |
| **host** | The local machine's filesystem directly — no pod to mount it into | Work that cannot run in a pod at all: desktop-app builds needing a GUI toolchain, tasks needing host-wide credentials (keychain, code-signing identity). |

The first three types form a spectrum from "developer iterates here" (local-agent and remote-agent) to "production serves here" (runtime). Host is not on that spectrum — it is the one type with no pod, so none of build/push/deploy's cluster-facing behaviour applies to it at all.

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

An orchestrator can still link a runtime env — but only with the **runtime** role (see [Desktop app · Orchestrators](/desktop/orchestrators)), which means operating it directly (deploy, pin, observe) rather than developing in it or reviewing it. There is no worktree and no in-pod Agent, so a runtime env linked with any other role is refused; the runtime env's own `type` and the orchestrator's `runtime` role are independent fields that happen to share a spelling, not the same thing.

## host

A directory on the Operator's own machine, with **no pod and no cluster at all**. It exists for work that a pod genuinely cannot do: building the ERun desktop app itself (its GUI toolchain isn't in the runtime image), and tasks that need host-wide credentials — the OS login keychain, a code-signing identity, a macOS privacy grant. Both of these already happened by hand before this type existed; a host env just gives them a declared shape instead of an unwritten exception.

**Host is not local-agent.** They both name a directory on your machine, and that similarity is exactly the trap: a local-agent env's worktree is that directory *hostPath-mounted into a pod*, which is where its agent actually runs. A host env has no pod at all — it doesn't deploy, doesn't get a kubernetes context, doesn't open a shell through `erun open` (that directory is already where you are; open it directly), and never resolves a runtime version. `erun deploy`, `erun pin`, and `erun terraform` all refuse a host env by name rather than resolving a plan that has nothing to act on.

An orchestrator can link a host env the same way it links a local-agent one: since there is no pod to sync a worktree from, the review directory and the worktree are the same path, and the Agent working there is the operator's own checkout — the orchestrator doesn't edit it directly, same as any other linked env.

Typical names: `<operator>-desktop-build`, `<operator>-macos-sign`.

## What concretely changes per type

The type describes the **environment** — where its worktree comes from and what it's for. It does **not** change what the `build` / `push` / `deploy` commands do for the three pod-based types: those are [pure primitives](/concepts/command-primitives) with no environment-type branch among them. What differs per type is whether the env has source to build from at all, and who supplies the commands (an Operator at the terminal, or the desktop app orchestrating them). Host is the one exception to "no environment-type branch": since it has no pod at all, `deploy`/`push`/`pin`/`terraform` refuse it outright rather than resolving a plan — that's a precondition ("does this env have a pod to act on"), not a policy decision about which primitives to run.

| Behavior | local-agent | remote-agent | runtime | host |
|---|---|---|---|---|
| Worktree in pod | Mounted from host | Cloned to PVC | None | No pod at all — the worktree just *is* the local directory |
| Source to build from | Yes | Yes | None — nothing for `build` to act on | Yes |
| Typical loop | iterate: build → push → deploy a snapshot | iterate: build → push → deploy a snapshot | consume: `deploy --version` a published version | build locally (e.g. the desktop app); never deploy |
| Editor / IDE attach | Yes (SSH + MCP) | Yes (SSH + MCP) | Not the normal pattern | Yes — it's already a local directory, open it directly |
| Per-env helm overlay | Defaults are fine | Defaults are fine | Yes — each runtime env wants its own | N/A — no helm chart ever renders for it |

A runtime env has no worktree by default, so there's no source for `build` to act on and no reason to build there — you deploy a version into it that was built and pushed elsewhere. (You can opt one into a mutable source worktree for live patching; see [Hotfix pattern](#hotfix-pattern).) An agent env has source, so it's where the iterate loop runs. The desktop app reads the env's type to decide *which* primitives to run on the Operator's behalf, but the primitives themselves stay the same everywhere (see [Command primitives](/concepts/command-primitives)).

The exact snapshot tag format and how `erun build` resolves it lives at [Build path resolution](/reference/configuration-build-paths).

## Hotfix pattern

A runtime env has no editable source by default. You *can* opt one into a mutable source worktree — the desktop's env settings (Runtime tab → **Mount source code**, backed by [`EnvConfig.mountsource`](/reference/configuration#envconfig) + `repourl`) clone the repository into the pod at the deployed release, checked out for live edits — when you genuinely need to patch the running release in place. The lower-risk pattern, and the default, is to spin up a local-agent env beside it, on the same cluster:

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

## Baked release artifacts

Mount source (above) is for *editable* source. When a runtime env instead needs *read-only* artifacts present — a platform Terraform tree, seed data, fixtures the deployed services expect — a tenant's `<tenant>-devops` image can **bake** them into the image and have them appear in the pod's git folder.

The one rule: bake them into **`/opt/erun/release/`**, never under `/home/erun`. A runtime pod mounts a persistent PVC over `/home/erun`, which shadows anything baked beneath it — so a `COPY` into `/home/erun/git/<repo>/…` silently never appears. Artifacts baked into `/opt/erun/release` (outside that PVC) survive, and on boot a sourceless runtime env symlinks its git folder (`~/git/<repo>`) at `/opt/erun/release`. The result shows through as `~/git/<repo>/…`, always matching the deployed image — no copy step, nothing to keep in sync. `erun terraform` and other repo-rooted commands then resolve the tree there. Unlike a mounted worktree, this content is read-only image state: to change it, rebuild and redeploy the image.

## Mapping to configuration

Set `EnvConfig.type` to one of `local-agent`, `remote-agent`, `runtime`, or `host`. When `type` is set it is the source of truth for the env's shape — its worktree storage (`worktreeStorage=host|pvc|none`; a host env never renders a chart at all, so it has no `worktreeStorage` value to speak of) and the `ERUN_ENV_TYPE` the helm chart wires in for the three pod-based types. The `build` / `push` / `deploy` commands do **not** branch on type among the three pod-based types; the caller (the desktop app, or an Operator) decides which primitives to run for a given env. A host env is never that caller's target for `push`/`deploy`/`pin`/`terraform` at all — see [host](#host) above.

The retired `EnvConfig.remote` and `EnvConfig.snapshot` fields no longer exist. A config written before `type` existed is migrated on read — ERun derives `type` from the old `remote`/`snapshot` keys per the [legacy migration table](/reference/configuration#envconfig-type-truth-table) and discards them. New envs created by `erun init --type` set `type` directly.

```bash
# Local-agent env (default if neither flag is given):
erun init team local --type local-agent

# Remote-agent env (replaces the older --remote flag):
erun init team dev --type remote-agent --no-git

# Runtime env (worktree-less; serves deployed artifacts):
erun init team prod --type runtime --no-git

# Host env (no pod, no cluster; names a directory on this machine):
erun init team desktop-build --type host --project-root ~/code/erun
```

`--remote` is preserved as a deprecated alias for `--type=remote-agent`; passing both flags with conflicting values is an error.

The desktop app's **New environment** dialog exposes the same choice as an **Environment type** field, including `Host`. Picking `Local agent` or `Host` reveals a **Local repo path** input (with a native folder picker) — for local-agent that's the host directory mounted into the agent pod as the worktree; for host it's the directory the env *is*, with no pod to mount it into. Either way it's equivalent to the CLI's `--project-root`. Picking `Host` also hides the kubernetes-context, runtime-pod, and container-registry fields — none of them apply to a type with no pod.

The type is editable after creation from either surface. In the desktop, the **Environment type** field on an existing env's **Manage → General** tab is a selector. From the CLI, re-run `init` with the type you want (`erun init team dev --type remote-agent`) — it moves the env between any two types and does the work the new type implies, and omitting `--type` leaves the env's type alone (see [`erun init` · Re-running on an existing environment](/cli/init#re-running-init-on-an-existing-environment)). Changing the type alters build and deploy behaviour and reconfigures the worktree, so reach for it to fix a wrong type, not as a routine toggle.

Inside a runtime pod, the on-disk env config is a projection of the env's configuration, written from the helm-injected `ERUN_*` env vars. It carries the build/deploy-relevant fields the pod acts on — `runtimeregistry`, `containerregistries`, and `disablebuildscript` (a `remote-agent` pod builds in-pod and needs the build/push registry list; a `runtime` pod only deploys and needs the deploy/runtime registry). If a pod's config drifts from the deploy spec, [`erun doctor --sync-config`](/cli/doctor) reconciles it in place (injected env wins), preserving the keys the env does not carry.

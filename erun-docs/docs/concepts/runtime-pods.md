---
title: Inside an environment
---

# Inside an environment

When you open an environment, ERun creates (or reuses) a **dedicated Kubernetes namespace** for it. Everything that belongs to the env lives in that namespace — both ERun's own developer-access surface and the application services your project deploys.

<figure className="erun-hero-figure">
  <img src="/img/inside-environment.svg" alt="Inside one Kubernetes namespace. At the top, an Operator pill and an Agent pill connect via dashed arrows labelled SSH and MCP into a runtime pod that sits inside the namespace card. The runtime pod holds two charcoal containers — erun-devops (shell + erun + docker + MCP server), erun-dind (docker daemon sidecar). Below the runtime pod, still inside the same namespace, four application service boxes — frontend, api, db, queue — plus a '+ more' note. A strapline reads: 'One namespace = one full functioning copy of the project. Drop the namespace, everything goes with it.'" />
  <figcaption>One namespace = one full functioning copy of the project. The runtime pod is the shared surface for Operator (SSH) and Agent (MCP); the application services live alongside it.</figcaption>
</figure>

The namespace is the unit of isolation. Two envs of the same tenant run in two different namespaces; they can't see each other's pods, secrets, PVCs, or services. The point isn't just to host ERun's own pod — it's to **deploy a whole functioning copy of your project** inside the namespace without affecting any other env.

## What lives in the namespace

A typical env namespace holds two kinds of workload, side by side:

### 1. The ERun runtime pod (developer access)

One pod with two containers:

| Container | Role |
|---|---|
| `erun-devops` | Main shell + tools (`erun`, `docker`, `kubectl`, `helm`, `gh`, …). **Also ships the Agent CLI** — `claude`, `codex`, or whichever tool the env is configured for — pre-wired against the in-pod MCP loopback. The default Agent in the env runs inside this container. **And it serves the env's MCP edge** — structured tools (`idle`, `doctor`, `list`, `version`, `build`, `deploy`, `raw`, …) on `ERUN_MCP_PORT`, reached at loopback by the in-pod Agent and via port-forward from laptop-side clients. |
| `erun-dind` | Docker daemon sidecar. Backs `/var/run/docker.sock` for the shell container's `docker` invocations. |

Because the MCP edge runs in this container, an MCP tool call executes with exactly the toolchain the env is built with. Add Java or a compose plugin to the runtime image and MCP-driven `raw` and `build` see it, the same as an `erun open` shell does.

Both share two persistent volume claims: `/home/erun` (workspace + config) and `/var/lib/docker` (the daemon's image store, so builds stay cache-warm across pod restarts). The home PVC also holds the **agent outputs directory** (`$ERUN_OUTPUTS_DIR`, default `/home/erun/.erun/outputs`) — where agents and skills drop deliverables you pull out with [`erun outputs`](/cli/outputs); because it's on the PVC, those files survive pod restarts.

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

The developer containers live in one pod, not two:

- File edits visible in `erun-devops` are immediately visible to the daemon in `erun-dind`.
- MCP tools run inside `erun-devops`, so they see the same filesystem, toolchain, and docker daemon the shell sees.
- One ServiceAccount, one RBAC scope, one audit surface.

## Stopping and starting an environment

An environment is not always running. Most environments are idle most of the time, and an idle one
still reserves everything it was given — the runtime container's CPU and memory limits, plus
whatever `erun-dind` is really consuming, which has no limit at all. [`erun stop`](/cli/stop) scales
the runtime to zero so all of it goes back to the node; **opening the environment starts it again**,
and [`erun open`](/cli/open) waits for the pod before it forwards anything.

Both PVCs survive a stop, so starting is a pod start rather than a cold rebuild: the workspace,
the agent config, the outputs directory, the image store and the build cache are all still there.
What does not survive is whatever was running in the pod — stop an environment because nobody is
using it, not to pause work in progress.

The desktop shows a stopped environment as **stopped**, not as broken: a hollow indicator on the
environment's row rather than the warning triangle a failed deploy gets. Stopping is also an action
there, on the environment's Runtime tab beside Deploy — which is where you notice the problem,
because the resource sliders on that tab are computed from what the node's pods currently reserve.

## Reading the resource figures

The CPU and memory figures on the Runtime tab are **one reading of the node right now**, not a
ceiling on what an environment supports. Two things move underneath them: a node's allocatable
capacity changes as its own reservations change, and the free figure depends on what every other
pod on that node currently holds. So the number you see is a snapshot, and the tab says which node
it came from.

Two cases the number alone cannot explain, so the tab spells them out:

- **The maximum equals what this environment already has.** An environment can always keep what it
  is already running with, so when the node has nothing left the slider's maximum is floored at the
  current value. That reads like a product limit but means the opposite — the node is fully
  committed. The remedy is to [stop an environment](/cli/stop) nobody is using on that node, after
  which the figure rises.
- **Some usage is not counted.** The reading prefers a container's declared limits, falls back to
  its measured usage when the cluster reports metrics, and says how many containers it could not
  account for at all when neither is available. The `erun-dind` sidecar declares no limits, so on a
  cluster without metrics its real consumption — Testcontainers, the build cache — is invisible to
  the reading and the tab warns that the true usage is higher than shown.

## What the environment thinks it should be sized as

The figures above describe the node. The environment also has an opinion about *itself*: every
environment accumulates a standing recommendation — raise memory, drop memory, raise CPU, or leave
it alone — from its own container's cgroup counters, and [`erun list`](/cli/list#the-sizing-recommendation)
prints it under `runtime-pod:`. Nothing is applied automatically; resizing means a deploy, and that
is your call to time.

It matters because sizing is otherwise set once and never revisited, and both ways of being wrong
are live. Under-provisioning shows up as a killed agent. Over-provisioning shows up as nothing at
all — it just quietly holds capacity that the free figure above then reports as unavailable to
everyone else on the node.

## What is holding the environment's resources

A build leaves things running. Gradle keeps its daemons alive for the next build, Testcontainers
leaves JVMs resident, the container build cache grows. That is fine while you are working and
wasteful afterwards — and until now nothing showed it, so a heavy environment had no explanation.

The Runtime tab reports what the pod is running, directly under the resource sliders: how many
[sessions](/desktop/overview) actually have a live program behind them, and the processes holding
memory grouped by what they are. It is read-only by default — you see what is there before anything
is stopped — and the groups that are safe to reclaim carry an action:

| Group | Action | What it does |
|---|---|---|
| Gradle daemons / Java processes | **Stop build daemons** | `gradle --stop`, then terminates any Gradle daemon JVMs left behind. |
| Container build processes | **Prune build cache** | Prunes the build cache and dangling images. |

Neither touches your worktree, a running session, or the Agent. Agent processes are shown without an
action for exactly that reason: they are your work, not a leftover.

A session's running state is **observed in the pod** — its socket exists *and* a live program sits
behind it — rather than inferred from how recently it printed something. An Agent waiting on a
compile is silent but running; a dropped connection is quiet but finished. Inferring from output
gets both backwards, which is how a pane could show a stalled indicator beside a truthful "still
running" count. The session sockets themselves live and die with the pod, and a replaced pod clears
any left behind at boot, so a leftover socket is never presented as a running session.

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

Those numbers are about envs running *at once*, not envs you have. Configured envs cost nothing;
only running ones reserve capacity. So the usual way past the limit is not a bigger machine — it is
[`erun stop`](/cli/stop) on the envs nobody is using, which hands their CPU and memory straight back
to the ones that are. Opening a stopped env starts it again.

Lower the runtime pod's CPU / memory per env from the desktop's env settings if you want more concurrency on a constrained machine. For the field names and defaults, see [Configuration · `EnvConfig`](/reference/configuration#envconfig). For genuinely heavy parallel work, point envs at a managed cloud context — see [Cloud contexts](/concepts/cloud-contexts).

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

Both share two persistent volume claims: `/home/erun` (workspace + config, and the go build cache, which is bounded — see [below](#what-is-holding-the-environments-resources)) and `/var/lib/docker` (the daemon's image store, so builds stay cache-warm across pod restarts). The home PVC also holds the **agent outputs directory** (`$ERUN_OUTPUTS_DIR`, default `/home/erun/.erun/outputs`) — where agents and skills drop deliverables you pull out with [`erun outputs`](/cli/outputs); because it's on the PVC, those files survive pod restarts.

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
still reserves everything it was given — the runtime container's CPU and memory limits, plus the
`erun-dind` sidecar's own limits (sized independently — see [`erun resize`](/cli/resize)).
[`erun stop`](/cli/stop) scales the runtime to zero so all of it goes back to the node; **opening the
environment starts it again**, and [`erun open`](/cli/open) waits for the pod before it forwards
anything.

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
  account for at all when neither is available. The runtime pod's own two containers (`erun-devops`,
  `erun-dind`) always declare limits — [`erun resize`](/cli/resize) is what moves them — so this gap
  is about the application services deployed alongside them: any of those that declares no limit of
  its own is invisible to the reading on a cluster without metrics, and the tab warns that the true
  usage is higher than shown.

That is the node's answer to "how full is the machine". The environment's own answer to "how close
am I to my own limits" is a different reading — CPU against its own quota, memory current and peak
against its own cgroup limit with a real OOM-kill count, disk on the workspace mount — and it needs
no cluster metrics add-on at all, so it works on the same metrics-server-less clusters where the
node reading above falls back to declared limits. **This environment's usage**, directly below the
resource sliders on the Runtime tab, is the direct route to it; [`erun usage`](/cli/usage) gives the
same reading from a terminal or an MCP-connected orchestrator.

**On an agent env, that reading excludes the environment's own builds.** An agent env's runtime pod
carries a second container, `erun-dind`, and every `erun build`/`erun release` actually runs there —
not in the `erun-devops` container the reading above measures. `erun-dind`'s build containers are a
separate cgroup the `erun-devops` container has no path to read, so a build that is genuinely
saturating the sidecar can still show as an idle environment here. Rather than leave that
unexplained, both **This environment's usage** and `erun usage` say so directly whenever the
environment carries the sidecar (every type except runtime and host); [`erun resize`](/cli/resize)
is what sizes the sidecar independently, and its own limits show up under [`erun observe`](/cli/observe).

## What the environment thinks it should be sized as

The figures above describe the node. The environment also has an opinion about *itself*: every
environment accumulates a standing recommendation — raise memory, drop memory, raise CPU, or leave
it alone — from its own container's cgroup counters, and [`erun list`](/cli/list#the-sizing-recommendation)
prints it under `runtime-pod:`. Nothing is applied automatically: [`erun resize`](/cli/resize) (or the
Runtime tab's Resize action) is what acts on it, and it refuses to roll the pod out from under a
build, a deploy, or an agent session already using the environment unless you explicitly override
that.

It matters because sizing is otherwise set once and never revisited, and both ways of being wrong
are live. Under-provisioning shows up as a killed agent. Over-provisioning shows up as nothing at
all — it just quietly holds capacity that the free figure above then reports as unavailable to
everyone else on the node.

The `erun-dind` sidecar has no standing recommendation of its own — it is a different container
with a different cgroup, and nothing in the environment reads it — so its build CPU cap is the one
figure below that is a rule rather than an observation.

### Sizing the build CPU cap {#sizing-the-build-cpu-cap}

Every image build runs in the `erun-dind` sidecar, so its CPU limit is the ceiling on how much of a
node a single `erun build` can use. The default is `12`, and it is derived rather than fixed: the
node's CPUs divided across the build-capable environments erun expects to be building on it at
once, floored at `4`.

Two things about that rule are easy to get backwards.

**A CPU limit is a ceiling, not a reservation.** Kubernetes schedules on requests — erun pins those
to a small fixed value — so the sum of every environment's limit on a node is allowed to exceed the
node. What the kernel then does is share the node fairly between whatever is actually running,
which is a better outcome than each build being held under a quota too small to use the node even
when it has the node to itself. A build capped at a fraction of an idle node does not go faster
because the node is free: it spends its wall clock throttled, which is what a CPU-pressure figure
near 100% alongside a load average far below the core count means. Sizing the cap to a "safe" small
number is the failure, not the cautious choice.

**Limits do not reserve, so co-tenants do contend.** The corollary is that four environments on one
node each sized for the whole node will contend for it when they all build at once, and that
contention is real CPU pressure rather than quota throttling. That is the trade the divisor makes,
and it is why the number takes a co-tenant count rather than always assuming one: an environment on
a node it shares with several other build-capable environments wants a smaller cap than one alone
on a node. The floor of `4` bounds the other end — below it a build is throttled on any node — and
it is also what an environment whose node size erun has never established falls back to.

Move either end with [`erun resize --dind-cpu`](/cli/resize), which rolls the sidecar onto the new
limit; `erun init --dind-cpu` sets it for a new environment. Because a resize restarts the runtime
pod, it refuses while the environment is held by a build, a deploy or an agent session unless you
override that.

## What is holding the environment's resources

A build leaves things running. Gradle keeps its daemons alive for the next build, Testcontainers
leaves JVMs resident, the container build cache grows. That is fine while you are working and
wasteful afterwards — and until now nothing showed it, so a heavy environment had no explanation.

The Runtime tab reports what the pod is running, directly under the resource sliders: how many
[sessions](/desktop/resources-and-usage) actually have a live program behind them, and the processes holding
memory grouped by what they are. It is read-only by default — you see what is there before anything
is stopped — and the groups that are safe to reclaim carry an action:

| Group | Action | What it does |
|---|---|---|
| Gradle daemons / Java processes | **Stop build daemons** | `gradle --stop`, then terminates any Gradle daemon JVMs left behind. |
| Container build processes | **Prune build cache** | Prunes the build cache and dangling images. |

Neither touches your worktree, a running session, or the Agent. Agent processes are shown without an
action for exactly that reason: they are your work, not a leftover.

A cache that only ever grows is also how a node's remaining space disappears without any one
environment appearing to hold it. So each environment's build cache is bounded to a share of its own
docker volume — 80% by default, leaving the rest to the images and containers a cache prune cannot
reclaim — and it is reclaimed down to that bound automatically once the bound is reached. At 70% the
environment says so, a tenth of the volume below the bound, so the growth is visible while the remedy
is still ahead of you rather than alongside it.

The bound is per environment on purpose. Each environment's docker sidecar owns its own volume, so a
share of that volume is a limit no single environment can exceed on the others' behalf. An unbounded
cache is exactly what that costs: the disk-headroom guard that prunes when the node runs low frees
*the node's* space, so every other environment's next build repays its layers from cold.

The **go build cache** is the other half of that, and it lives on the home volume rather than the
docker one. It has a bound of its own because the go command's own rule is not one: go evicts entries
nothing has touched in five days, which is a rule about *recency* with no ceiling, and the five-day
working set of a multi-module repository built per GOARCH, with and without `-race`, reaches tens of
gigabytes — enough that four environments sharing a node held about 45 GB each and filled it.

Each environment's is capped at 16 GiB. Reaching the cap evicts the **least recently used** entries
down to it rather than clearing the cache: what goes is the bulk nothing has touched since it was
written, and what stays is the working set the next build asks for. Clearing it would bound it too,
and would make every build afterwards cold — on this repository a warm build of one module takes
about half a second against about fifty seconds from an empty cache. The cap is checked when the pod
starts and every half hour after, so an environment already over it comes back under it without
waiting for the next build.

Set it per environment with `cacheTrim.goBuildMaxGi` in the runtime chart's values; `0` leaves the
cache unbounded. Size it above what your builds actually reuse rather than at what looks tidy: a cap
below the working set turns every build cold, which is the cost the bound exists to avoid. The home
claim's own `storage:` request is not part of this — the storage class these claims land on is
node-local and enforces no quota, so that number is a declaration rather than a ceiling, and the go
build cache's cap is what actually keeps home from filling a node.

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

The hard limit is your machine's CPU + memory (for local clusters) or the cloud context's instance type (for managed clusters). In practice, the runtime pod is sized for ~4 CPU and ~16 GiB — that is the default because an agent runs the full `make check-gate` inside that container, the same gate an image build runs in the `erun-dind` sidecar. An env that only serves an app needs far less; trim it with [`erun resize`](/cli/resize) and a typical app stack adds 2–8 GiB per env. So:

- **16 GiB laptop** — 1 env at the default sizing, 2 if you trim the ones that do not run gates.
- **32 GiB laptop** — 2 envs at the default sizing.
- **64 GiB laptop** — 4 envs, or more if you trim per-env runtime-pod sizing to fit your workload.

Those numbers are about envs running *at once*, not envs you have. Configured envs cost nothing;
only running ones reserve capacity. So the usual way past the limit is not a bigger machine — it is
[`erun stop`](/cli/stop) on the envs nobody is using, which hands their CPU and memory straight back
to the ones that are. Opening a stopped env starts it again.

Lower the runtime pod's CPU / memory per env from the desktop's env settings if you want more concurrency on a constrained machine. For the field names and defaults, see [Configuration · `EnvConfig`](/reference/configuration#envconfig). For genuinely heavy parallel work, point envs at a managed cloud context — see [Cloud contexts](/concepts/cloud-contexts).

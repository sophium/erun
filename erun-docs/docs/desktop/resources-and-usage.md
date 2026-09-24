---
title: Resources and usage
---

# Resources and usage

Below an environment's resource sliders, two readings tell you what the environment itself is doing — separate from what the node underneath it looks like.

- **See the environment's own usage, not just the node's.** Directly under the resource sliders, **This environment's usage** reads the environment's own opinion of itself: CPU utilisation against its quota, memory current and peak against its own cgroup limit with the real OOM-kill count, and disk usage on the workspace mount — refreshed on demand, and it works even on clusters where `kubectl top` can't (no metrics-server required). A field the reading could not measure (an unlimited memory setting, an older cgroup version) says so rather than showing a confident zero. Named warnings call out when memory or disk is close to its limit. See [Runtime pods](/concepts/runtime-pods#reading-the-resource-figures).
- **A ceiling is named as one, and the reservation is stated beside it.** The Memory row reads `25.0% of 2048Mi limit · 1024Mi requested`: a cgroup limit is what the container may grow to under node pressure and reserves nothing at scheduling time, so `of 2048Mi` alone would read as an environment holding 2GiB. The request is what the scheduler actually admits the pod on — it decides whether the environment can be placed at all, and on every environment erun deploys it is a small fraction of the ceiling, because `deploy` sets limits only. It is read from the live pod spec (no cgroup file records a request); a pod spec that could not be read leaves the reservation unstated rather than showing a requested zero. The same figures are on the Runtime tab and in [`erun usage`](/cli/usage).
- **On a build-capable environment, the reading covers two containers — because the work is in the second one.** The CPU and memory rows are the runtime container's, and on an environment that builds they are near-idle by construction: a build lane spends its time waiting on bounded job calls, so those figures cannot tell a healthy build from a wedged one. Every image build runs in the `erun-dind` sidecar instead, and the panel reports it as its own **Builds** block, named so the two cannot be confused. That sidecar usually declares no CPU quota — it is deliberately unlimited so a build can use the node — so it has no utilisation percentage to report; the panel states its cumulative CPU-seconds instead, which is a real measurement with no ceiling to be a fraction of. A sidecar reading that could not be taken renders as absent, never as an idle zero, and the caption keeps saying which rows exclude builds either way.
- **See whether Stop will do anything before you press it.** The tab reads the environment's runtime Deployment and says what it found: running (with how many replicas are ready), stopped, or not yet deployed. Stop is offered only when the runtime actually wants pods. On one that is already stopped — or one that was never deployed — the control is disabled with the reason on screen, because a stop there is a correct no-op that would otherwise read as a broken button. When the cluster cannot be read at all, the tab says so and leaves Stop usable rather than blocking the only control you have. The helper text under the button also names the platform components a stop will *not* touch, so a pod that outlives the stop is not mistaken for a failed one — see [`erun stop` · What a stop does not touch](/cli/stop#what-a-stop-does-not-touch).

### See what an environment is running, and take resources back

Below that, **Running in this environment** reports what the pod is doing right now: how many sessions actually have a live program behind them, and the processes holding memory — Gradle daemons a finished build left resident, the container build cache — grouped by what they are. It is a reading, not a cleanup: nothing is stopped until you click the action beside a group, and your worktree, sessions, and Agent are never touched. The resource figures in the sliders above are a live snapshot of the node, and when the maximum is capped by the node being full rather than by a limit on the environment, the tab says so and points at stopping an environment nobody is using. See [Runtime pods](/concepts/runtime-pods#reading-the-resource-figures).

### What an environment's memory limit does under contention {#memory-limit-contention}

A limit is containment for the container that holds it. It is not a reservation, and it is not a
promise that the node underneath can back it — so once a node runs short, the number you size an
environment to is also part of what decides that environment's fate. Two numbers are doing two
different jobs, and only one of them is the one you pick:

| | What it decides |
|---|---|
| The request — 0.25 CPU / 1 GiB per container, 0.5 CPU / 2 GiB for the pod | Whether the environment can be placed on a node at all, and where it ranks when a node has to shed pods |
| The limit — the CPU and memory you size | How far a container may grow before the kernel stops it, and how much memory it can be holding when a node-level killer looks |

**Every environment pod is Burstable, at priority 0.** Kubernetes grants the `Guaranteed` class only
when every container's requests *equal* its limits, and erun's requests are a small fixed value
while its limits are the thing you choose — so no environment pod ever qualifies. Nothing sets a
priority class on one either. Every environment therefore shares one eviction rank, with nothing to
break a tie in its favour.

**A bigger limit is a wider target, not a safer environment.** When a node comes under memory
pressure, Kubernetes reclaims by evicting the pods whose usage *exceeds their requests* first,
ranked by priority and then by how far past the request they are; pods using less than they
requested go last. An environment doing real work in its runtime container is far past that 1 GiB
request, and every environment is at priority 0 — so it is in the first group, and it stays there
for as long as it is busy. What the limit changes is not the ranking but how much memory the
environment is *able* to hold when the ranking is applied. Two environments sized at 27 GiB and
13.5 GiB are equally evictable in principle; only the first can become the environment holding
27 GiB. Node OOM scores do not come from the limit either — a container's adjustment is derived from
its request and the size of the node, so it is identical for every environment on that node however
each is sized.

**A container that reaches its own limit dies on an otherwise idle node.** That kill is containment
working, not the node running out: the kernel stops the container at its own limit regardless of how
much memory the node has free, and independently of every other environment. It is the failure the
**This environment's usage** reading reports as its OOM-kill count above — and it is why a lowered
limit is not a safe direction either. The sizing recommendation exists precisely because a runtime
container sized below the work an agent runs is killed mid-run, taking unpushed work with it; see
[What the environment thinks it should be sized as](/concepts/runtime-pods#what-the-environment-thinks-it-should-be-sized-as).

**On a build-capable environment, the sidecar's ceiling does not contain a build.** Every image
build runs in the `erun-dind` sidecar, but the build steps themselves run in containers that escape
the sidecar's own memory cgroup — so the sidecar's limit is capacity guidance rather than a
guaranteed ceiling on a build. A build can therefore grow the node without ever tripping its own
limit, which leaves the decision to the node-level killer described above. Its CPU cap *is* a real
ceiling, which is why [sizing the build CPU cap](/concepts/runtime-pods#sizing-the-build-cpu-cap) is
a rule rather than an observation.

**What bounds the sum is the namespace, not any container.** A limit bounds one container; a node's
oversubscription is bounded by a Kubernetes `ResourceQuota` on the environment's namespace, which
[`erun observe`](/cli/observe) reports alongside its current consumption. A locally-created
environment carries none — nothing in `erun init` sets one — and the hosted platform applies one per
tenant environment instead (see [Quotas](/concepts/hosted-platform#quotas)). To make a node go
further, the levers are [stopping](/cli/stop) environments nobody is using and running fewer per
node — not shrinking a limit that is sized for the work the environment actually does.

## Where next

- [Control panel](/desktop/control-panel) — the sidebar's compact usage reading, for comparing environments at a glance.
- [`erun usage`](/cli/usage) — the same reading from the CLI.

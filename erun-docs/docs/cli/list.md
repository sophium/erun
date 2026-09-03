---
title: erun list
---

# `erun list`

List all configured tenants and environments, the effective target for the current directory, and configured cloud providers. `list` is read-only and never mutates state. (To list managed cloud *contexts*, use [`erun context list`](/cli/context).)

## Synopsis

```
erun list [flags]
```

## Output

Sections print in order — configuration location, defaults, the effective target for the current directory, configured cloud providers, every tenant and its environments, then any orchestrators:

```
Configuration:
  directory: /Users/you/Library/Application Support/erun
Defaults:
  tenant: my-tenant
  environment: local
Current Directory:
  path: /Users/you/code/my-project
  repo: my-project
  configured tenant: my-tenant
  effective target: my-tenant/local
  kubernetes context: docker-desktop
  type: local-agent
  snapshot: enabled
  repo path: /Users/you/code/my-project
Cloud Providers:
  none
Tenants:
  - my-tenant (default)
    ...
Orchestrators:
  none
```

The full per-env field set (local port allocations, API URL, SSH details, …) prints under each tenant; the example abbreviates. See [Configuration](/reference/configuration) for what each value means.

Each orchestrator lists its linked environments beside what that orchestrator uses each one for: `role=code`, `role=build`, `role=runtime`, or `role=undeclared` when nothing has been set. `role` and an environment's own `type` are independent fields shown in different places — a runtime-*type* environment linked with the runtime *role* shows `type: runtime` under its tenant entry and `role=runtime` under the orchestrator entry, and the two mean different things even though they share a spelling.

## Release lines {#release-lines}

`runtime-version:` is a bare number, and a number alone can't say which release it belongs to: a
tenant can publish its own `<tenant>-devops` image and version it on its own line, so two
environments of the same tenant can genuinely be running different lines from each other. `erun
list` names the line beside the number:

```
runtime-version: 1.0.84 (frs line, ghcr.io/sophium/frs-devops)
runtime-version: 1.0.203 (erun line, ghcr.io/sophium/erun-devops — release name frs-devops disagrees with the image)
runtime-version: 1.0.226 (line undetermined — no resolved runtime image recorded; redeploy to record it)
```

The middle line is the case worth double-checking: a `frs-devops` release running the stock
`erun-devops` image is legitimate, but it means that environment moves on ERun's own release line,
not the tenant's — comparing its number against another environment's tenant-line number is
comparing two different things. The last line means exactly what it says: nothing has recorded which
image this environment's pod actually runs yet, so `erun list` says so rather than guessing from the
tenant's name.

## The sizing recommendation

`runtime-pod:` prints the size an environment was *given*. Under it, when ERun has watched the
environment long enough to have an opinion, two more lines print what that size should be — derived
from what the environment has actually done, not from what anyone guessed when they created it:

```
runtime-pod: cpu=12 memory=23552Mi
sizing: memory lower to 18432Mi from 23552Mi (peak 12153Mi of 23552Mi (52%), no oom kills, keeping 1.5x headroom, low confidence); cpu lower to 10 from 12 (busiest interval 4.567 of 12, 0.00% of scheduling periods throttled (0 of 376556), keeping 2x headroom, low confidence)
sizing-evidence: 31h12m observed, 240 samples, 1 restarts, knob=runtimepod, from cgroup memory.peak, cgroup memory.events oom_kill, cgroup cpu.stat usage_usec/nr_throttled (not loadavg)
```

The lines are advisory: ERun never resizes an environment on its own. Acting on one is
[`erun resize --apply-recommendation`](/cli/resize), which applies the computed value without
retyping it and restarts the pod — a decision for you to make and time.

### Where the figures come from

The runtime container's own cgroup counters, read from inside the container. **No metrics-server is
required** — that is deliberate, because `kubectl top` answers "Metrics API not available" on local
clusters, which are the ones you iterate in all day. The counters are sampled on the same tick the
[idle monitor](/agent-reference/idle-policy) already runs, and retained per environment.

### What each signal says

| Signal | Direction | Why |
|---|---|---|
| `memory.events` `oom_kill` above zero | **raise memory**, high confidence | Something was already killed. One kill is enough. The suggestion is sized from the limit that proved too small, not from the observed peak — the allocation that triggered the kill was refused, so it never reached `memory.peak`. |
| Observed memory peak at 90% of the limit or more | **raise memory**, high confidence | Sampling means the true peak is at least the peak observed. An environment already this close has plausibly gone further between two reads. |
| Observed memory peak below about two-thirds of the limit, over a long quiet window, no kills | **lower memory**, low confidence | The suggestion keeps 1.5× the observed peak. |
| 5% or more of scheduling periods throttled | **raise CPU**, high confidence | `nr_throttled`/`nr_periods` is real starvation: the container wanted CPU and the quota refused it. |
| Any throttling below that threshold | **hold CPU** | Tolerable, but not unused — the quota does bind sometimes, so this is not grounds to shrink. |
| No throttling at all, over a long quiet window, busiest interval below half the quota | **lower CPU**, low confidence | The suggestion keeps 2× the busiest interval measured. |

`insufficient-evidence` is a distinct answer from `hold`. `hold` means ERun looked and the size is
right; `insufficient-evidence` means it has not watched long enough, or the counter was unavailable,
and the line says which.

### Why raising and shrinking are not symmetric

Being wrong in the two directions does not cost the same. An over-provisioned environment quietly
consumes cluster capacity. An under-provisioned one kills a running agent — the failure behind
*"was killed (exit 137) — likely out of memory"*. So ERun raises on modest evidence and shrinks only
on a long, quiet window, never below 1.5× the peak it actually observed, and never at better than low
confidence. A quiet window is an argument from silence, and it is labelled as one.

A raise is also bounded by what the environment's namespace quota can admit, where one is
configured: a `ResourceQuota` counts every container in the pod, so the `erun-dind` sidecar's own
limit is spent before the runtime container gets anything, and a suggestion above the remainder is a
size Kubernetes would refuse to schedule. Raising the quota is a separate decision from resizing the
pod, so ERun clamps and says it clamped rather than silently recommending both.

### Two limits worth knowing

Without these, the output reads wrong:

- **`memory.peak` resets when the container restarts.** It is a high-water mark for the current
  container lifetime, not for the environment. This is exactly why ERun retains a history instead of
  reading the counter live — `sizing-evidence` reports how many restarts the history spans, and the
  peak it quotes survived them.
- **PSI is unavailable on some kernels.** `memory.pressure` and `cpu.pressure` are simply absent in
  the runtime container's cgroup on some hosts, so nothing here depends on pressure stall
  information. `nr_throttled`/`nr_periods` is the CPU-starvation signal that is actually available.

And one thing the figures are *not*: a host loadavg. A loadavg counts the machine's runnable queue,
so a 12-core environment sitting at a load of 10 looks saturated while `cpu.stat` reports not one
throttled period — which is why the evidence line names its counters and says `not loadavg`.

### When the lines do not print

The history is written by the container that produced it, so it exists on that environment's own
runtime pod. Running `erun list` from your laptop shows no sizing lines for a remote environment —
there is no history there to read. Ask the environment (over
[MCP](/mcp/overview) or an [`erun open`](/cli/open) shell) and it answers about itself; the MCP
`list` tool carries the same recommendation as a structured `sizing` field on each environment.

A newly created environment prints nothing either, until its monitor has taken a sample.

## Version drift across a tenant {#version-drift}

Pass `--tenant` to switch `list` from the full listing into a focused check: which erun version every environment in that tenant is running, and the newest version observed among them.

```bash
erun list --tenant my-tenant
```

```
Version drift for tenant my-tenant:
  max version: 1.0.247
  environments:
    - build version="1.0.246" [behind max]
    - code4 version="1.0.247"
```

Add `--gate-environment` to name the environment that drives that tenant's merge-queue gate — erun doesn't track this on its own, so you say which one it is — and the report additionally flags whether that environment is running an older erun version than any environment it gates:

```bash
erun list --tenant my-tenant --gate-environment build
```

```
Version drift for tenant my-tenant:
  max version: 1.0.247
  environments:
    - build version="1.0.246" [behind max]
    - code4 version="1.0.247"
  gate:
    environment: build
    version: 1.0.246
    behind: yes -- outdated relative to code4
```

A gate older than the code it gates can pass a change that would fail on current code — this is what catches that before a bad merge slips through. See [collaboration › merge queue](/collaboration/merge-queue#the-gate) for what the gate does. For the exact flag contract and JSON shape, see [Agent reference › CLI flags](/agent-reference/cli-flags#version-drift).

Like the rest of `list`, this report exits `0` on its own — `list` reports, it doesn't gate. Add `--fail-on-drift` to make that one invocation exit non-zero when an environment is behind the tenant's max, or the named gate environment is itself behind (or its version couldn't be resolved), so a script or a schedule can act on it:

```bash
erun list --tenant my-tenant --fail-on-drift
```

## Control plane versions {#control-plane-versions}

`--tenant` catches drift *between* your own environments. It has no baseline for a different, easy-to-miss gap: a control plane can simply never get rolled onto a release that has already shipped, and nothing about its own reported version says whether that release exists. Pass `--control-planes` for that check instead: every erun-hosted control plane you've configured (a cloud provider alias with `provider: erun`, e.g. the one `erun cloud init erun` creates), compared against the newest version erun's own registry has actually published.

```bash
erun list --control-planes
```

```
published version: 1.0.247
Control planes:
  - erun+api.erunpaas.com@erun api-url="https://api.erunpaas.com" reachable=yes version="1.0.245" [behind published -- roll it]
    console: url="https://console.erunpaas.com" reachable=yes version="1.0.245" [behind published -- roll it]
```

Each plane is checked with its own unauthenticated `GET /v1/platform` — the same call `erun cloud init erun` uses to discover a plane in the first place — so a plane that doesn't answer prints `reachable=no reason="..."` instead of a version; an unreachable plane is never reported current. `[behind published -- roll it]` means the plane is running a real, older release than what's published; `[ahead of published -- running an unpublished version]` means the opposite and more unusual case — the plane is running something the registry has never published at all, which is worth investigating on its own rather than "just roll it".

That same `GET /v1/platform` response also names the plane's linked console (`consoleUrl` — a plane and its console are always deployed together, never configured as a separate alias), so each reachable plane's console is checked the same way, against the same published baseline, and printed nested under it as a `console:` line — a plane can be current while its console lags behind, or vice versa, and before this there was no way to tell. A plane whose response carries no `consoleUrl` prints no `console:` line at all, rather than guessing.

This makes real network calls (each plane and console, plus erun's registry), so add `--dry-run` to preview which planes, consoles, and registry lookup would be checked without making any call.

This report also exits `0` on its own, same as `--tenant`'s above. Add `--fail-on-drift` to make that one invocation exit non-zero when a plane or its console is behind or ahead of published, a plane or console is unreachable, or the published baseline itself couldn't be resolved — none of those confirm a plane and its console are running what erun actually published:

```bash
erun list --control-planes --fail-on-drift
```

`--fail-on-drift` never fires under `--dry-run`: nothing was actually probed, so there is nothing to fail on.

## Common usages

```bash
erun list                         # full listing
erun list | grep -i "tenant"      # quick scan of names
erun list | grep "effective"      # what ERun targets right now
```

`erun list` is what every troubleshooting flow should start with — it tells you which environment ERun considers "effective" right now and what its resolved config looks like.

## Error behaviour

| Failure | Behaviour |
|---|---|
| No config yet. | Prints the sections with `none` placeholders; not an error. |
| Current directory isn't a configured project. | `effective target: none` (or `unavailable (…)` with the reason); the rest still prints. |
| Config file unreadable. | Errors with the read failure; nothing is printed. |
| `--gate-environment` passed without `--tenant`. | Errors `--gate-environment requires --tenant`; nothing is printed. |
| `--tenant` names a tenant with no config. | Errors `tenant "<name>" not found`. |
| `--gate-environment` names an environment not in that tenant. | Errors `gate environment "<name>" not found in tenant "<tenant>"`. |
| `--control-planes` combined with `--tenant`/`--gate-environment`. | Errors `--control-planes cannot be combined with --tenant/--gate-environment`; nothing is printed. |
| `--control-planes` and a configured plane or its linked console is unreachable, or the registry lookup fails. | Not an error — printed as a finding (`reachable=no reason="..."`, or `published version: unresolved (...)`); exit code stays `0` unless `--fail-on-drift` is set. |
| `--fail-on-drift` passed without `--tenant` or `--control-planes`. | Errors `--fail-on-drift requires --tenant or --control-planes`; nothing is printed. |
| `--fail-on-drift` set and the report finds drift (an environment behind max, a behind gate, an unreachable/behind/ahead plane or console, or an unresolved published baseline). | The full report still prints, then the command exits non-zero naming what it found. Never fires under `--dry-run` — nothing was probed, so there is nothing to fail on. |

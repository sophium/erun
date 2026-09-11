---
name: erun-orchestrate
description: Operate as a host-side erun orchestrator that drives and reviews work across agent environments without editing their code locally. Use when asked to "orchestrate erun environments", "drive the remote agents", "coordinate work across environments", "review what the agents changed", "review changes across envs", "run the built app to verify", or "delegate this to the environment's agent".
---

# erun-orchestrate

Coordinate authorized work across linked agent environments from the host.
Develop in the pods, review on the host, and verify the delivered result.
Repository-wide engineering, contribution, interaction, and approval rules remain
in the target repository's AGENTS.md; direct in-pod Operators do not need an
orchestrator to work with their Agent.

Carry the requested outcome through its authorized steps without repeatedly
asking about routine implementation choices. A task does not automatically
authorize publishing, merging, releasing, resizing, or cross-environment changes
beyond its scope. Respect an explicitly shorter requested flow; surface genuine
missing authority or external blockers with the evidence and required action.

## Scope and configuration

- Read the `orchestrators:` entry matching `ERUN_ORCHESTRATOR_ID`, including
  linked environments, roles, types, and review paths. Scope comes from config,
  not populated directories or naming conventions. A linked empty mirror may
  simply await sync; an undeclared role is not permission to guess.
- Remote-agent review directories are one-way pod mirrors; local-agent directories
  are the pod-mounted worktrees themselves. Both are read-only to the orchestrator.
  Host edits are either overwritten by sync or collide with the owning Agent.
- Mirrors are read/delivery surfaces, not build directories. Sync deletes files
  absent in the pod. Read authoritative pod diffs and received artifacts there;
  keep orchestration tools and build outputs outside every review directory.
- Treat host and pod configuration as different state stores. Diagnose host
  identity/lifecycle on the host; pod calls answer only about pod state.
  Read-only mirrors cannot establish live runtime version: use the MCP version tool.

## Roles and workflow

1. Resolve scope, role, environment type, and host/pod paths.
2. Send implementation and focused iteration to a **code** environment.
3. Send pushed-branch regression gates, gate fixes, and releases to a **build**
   environment with warm caches; it is not the feature-authoring lane.
4. Operate **runtime** environments through erun/platform lifecycle operations.
   They have no worktree or in-pod Agent to delegate code work to.
5. Review each environment's authoritative diff read-only; send corrections back
   through that environment, never patch its mirror.
6. Have the pod cross-build host-native artifacts into its outputs directory;
   receive them through mirror artifacts or explicit download, then run them on
   the matching host OS/architecture. A Linux pod cannot verify a foreign-OS binary.
   Native desktop GUI compilation is the explicit host-build exception below.

Keep reviews and paths scoped to their environment. Locate durable Terraform
state/provider data before running an operation; running the same command on the
other machine may address an empty, unrelated state.

## Environment channels

- Reach pod work through its erun MCP, not kubectl exec, SSH, or direct Helm.
  Use the host erun CLI for lifecycle and authoritative environment shape.
- A bound forwarding port is not proof of a working MCP channel. Test end to end.
  Job status/await, idle, and MCP calls/tools already self-heal one unreachable hop;
  channel exit 126 is not the delegated job's result.
- A manual reattach uses `erun open <tenant> <environment> --reconnect`, not bare
  open: reconnect must not wake an intentionally stopped environment.
  Starting a stopped environment is a separate authorized host lifecycle action;
  its absent MCP cannot start itself.
- For environment-shaping deploys, inspect the live release and resolved plan
  against host config. A local-agent pod has only chart-injected shape fields,
  so self-deploying from its defaults can silently replace host configuration.
- Treat transient channel loss/re-registration as recovery work, not a terminal
  job outcome. Investigate a persistently unavailable channel within scope;
  do not bypass the access boundary.

## Claiming issues

GitHub assignee cannot distinguish lanes sharing credentials. Use the orchestrator
ID's `wip:<id>` label, not its display name.

- Select open issues without any `wip:` label. Confirm open, add your label,
  then immediately re-read **state and labels before dispatch**.
- Labels are not compare-and-swap. If simultaneous claims appear, the
  lower-sorting orchestrator ID removes its own label and yields. If the issue
  closed, release your label and choose other authorized work.
- Do not take a live holder's issue or retry-loop on its claim. Reclaim only with
  evidence the holder is no longer running. Create the matching label when
  provisioning a new orchestrator ID.
- Release at completion, abandonment, or handback, not while work is in flight.
  Returning later requires the full fresh-claim sequence. Remove and report your
  own stale label on a closed issue; the open-issue query will not find it.

## Working in a pod

### Ownership and capacity

- Claim exclusive worktree ownership before checkout, staging, commit, cache
  pruning, or HEAD movement. Respect refusals and report the named holder;
  never clear a seemingly stale live claim manually. Reclamation belongs to leases.
- **Heavy gates claim the entire environment**, before dispatch through terminal
  verdict. Use an exclusive `environment` scope; another clone/worktree in the
  same pod does not isolate CPU/memory or make a concurrent probe safe.
  Worktree-scoped claims are only sufficient for unrelated lightweight work when
  no environment-wide claim exists.
- Keep the claim through multi-process workflows and renew it before expiry;
  pass its ID through supported under-lease paths instead of conflicting with it.
  Release on every terminal path. Detached-job presence leases prevent idle stop
  but do not replace the required exclusive gate claim.
- Lease PIDs belong to the environment, not the host. Do not confuse expiration
  and supervisor reconciliation with a permanent lock.
- Check actual capacity and `erun usage` before/during heavy work. Limits do not
  reserve capacity, and BuildKit memory enforcement has a documented gap in the
  DevOps guide. A second tree is not another environment's resources.
- Resizing restarts the pod and requires appropriate authority; respect worker
  refusals. Inspect recommendations rather than retyping resource values blindly.

### Dispatch, waits, and evidence

- Use tracked detached jobs for long work and bounded job awaits/status checks.
  Give delegated Agents the explicit waiting and reporting contract in the brief.
  Do not use background shell promises or handwritten polling to infer completion.
- Require a terminal recorded outcome and final evidence. Automatic reinvocation
  may recover some incomplete agent jobs, but it is bounded and not guaranteed.
  Neither a promise to report later nor wrapper exit zero proves completion.
- Preserve stdout **and stderr** from dispatch and failures. Transfer scripts
  without expansion by intermediate wrappers. Preserve the underlying exit status
  across logging/filtering; use the appropriate shell's pipeline-status support,
  not an assumed bash variable in another shell.
- A wait timeout (including 124 or Make's wrapper exit 2) means the wait ended.
  Read the underlying job's own status/log before assigning any verdict.
  Unknown terminal evidence is inconclusive, not passed or failed.
- Prefer short bounded waits that survive channel recycling. Recheck job state,
  channel, HEAD/cleanliness, and resource use at least every few minutes, more
  promptly for fast lanes; a fixed sweep cadence must not delay completed work.
  Do useful independent review/preparation while jobs run without invading a gate.
- Verify actual commits after coherent chunks and inspect artifacts yourself.
  Turn counts, narration, leases, and another Agent's “pass” are not saved changes
  or proof that the requested property was verified. Take over unfinished work only
  after resolving ownership safely.

## Review and recovery

- Check the user-facing outcome as well as gates: correct error wording about the
  wrong identity/state still traps the Operator. Apply the target repo's UX rules.
- Compare candidate fixes to a matching baseline and establish cause independently.
  Matching failure counts alone prove neither determinism nor product fault.
- Test negative claims with an appropriate probe before reporting “missing” or
  “cannot verify”. Name the attempted verification, narrower substitute, and gap;
  do not misrepresent host or native-UI gaps as covered by pod tests.
- Check outcome-bearing resources, not readiness promises alone. Select conditions
  by type rather than index; liveness must exclude the observer and exited zombies.
- After interruption, inspect actual applied state before retrying. If a plan
  replaces a healthy resource because of stale state/taint, resolve that discrepancy
  under the operation's approval rules; do not destroy it reflexively.
- Kill patterns must exclude unrelated sessions, paths, and arguments.
- Prefer typed operations with scoped authorization and useful failure output.
  Recover their logs and distinguish missing setup, tool defects, and deliberately
  unavailable credentials. Repeated raw workarounds identify a capability gap;
  implement it only when in scope, otherwise report the required expansion.
- Keep public/tenant addresses in their respective committed deployment/DNS owners;
  a manual live change is a diagnostic probe, not a reproducible delivered fix.

## Fixing erun itself

When an authorized task includes repairing erun, author in an erun pod checkout,
validate, then publish/roll out only to the extent authorized. Generic contribution
and refactoring rules stay in the repository root, not this workflow.

- Use shared `erun release` orchestration for a release. It publishes and verifies
  images/charts **before pushing the public tag**; do not reconstruct that ordering
  with ad hoc tag pushes or command convenience switches.
- Keep the build environment exclusive and warm. Verify registry credentials first.
  If publication fails after a local stamp/tag, inspect retry state and preserve
  the intended version; do not automatically reset the checkout or delete tags.
- Check node architecture and emulation cost when choosing build capacity.
  A multiarch release still needs every required platform, not a native-only shortcut.
- Deploy the published version using the owning host configuration, accounting for
  tenant runtime images/umbrellas, then verify the live MCP version and original flow.
- Release cadence/coalescing is separate from merge gating; use the merge-queue
  skill's authorized handoff and the API guide's explicitly proposed cadence policy.

## Rebuilding and restarting erun itself

- Replace a CLI binary safely while retaining rollback. For desktop restarts use
  `erun app restart --orchestrator "$ERUN_ORCHESTRATOR_ID"`, not a custom persistent
  relauncher that may resurrect the app after every quit.
- Before restart, write `RESUME-NOTE.$ERUN_ORCHESTRATOR_ID.md` in the orchestrator
  workspace: requested task, delivered work, in-flight job IDs, first verification,
  and expensive facts. Do not overwrite another orchestrator's note.
- On resume, read your note, inspect existing jobs rather than duplicating them,
  and continue the authorized task. Re-discover channels after registration settles.
- Follow the tool's configured source pointer; verify commit before build and the
  actual running process/version afterward. A restart-shaped message is not proof:
  inspect process identity/start time.
- When the native GUI toolchain requires a host build, use a copy outside every
  mirror/mounted review tree with separate outputs; code is still authored in-pod.
  Keep the previous executable for recovery.

## Completion

Report delivered outcome, observed verification, remaining gaps/blockers, relevant
job/commit/release identities, and assumptions made. Verify issue/PR state after
authorized publication rather than trusting closing references. Do not promise a
later report without an actual follow-up mechanism.

Improve canonical guidance at its source, not an installed baked copy or private
memory. Keep it short: invariant, exception, and verification, not incident history.

# AGENTS.md

Repository guidance for humans and coding agents working in this repo. This file holds only repository-wide, cross-cutting rules; module-specific guidance lives in each module's own `AGENTS.md`, linked from the Modules list below.

- Follow this file for the whole repository.
- **Always read every applicable `AGENTS.md` before touching files in its subtree, on every task, even when you have read it before in another conversation or earlier in this one.** "Applicable" means this root file plus the `AGENTS.md` of every directory that is an ancestor of, or contains, the files you are about to read, edit, run, or test. Read each one end-to-end and apply its guidance to the work; do not rely on memory or summaries.
- When in doubt, list `AGENTS.md` files with `find . -name AGENTS.md -not -path '*/node_modules/*'` and read each one whose path is a prefix of, or descendant of, the area you are about to change. Skipping this step has caused real bugs (e.g. shell-syntax assumptions that broke zsh on macOS).
- A child `AGENTS.md` is additional, not a replacement: parent rules still apply unless the child explicitly overrides them.
- If you add a new module-guidance `AGENTS.md` anywhere in the tree, add it to the Modules list below so future readers find it without searching.
- Each module-guidance `AGENTS.md` directory ships a `CLAUDE.md` symlink pointing to its `AGENTS.md` so Claude Code auto-loads the local guidance when launched from any subtree. Treat both names as the same file; only edit `AGENTS.md` directly.
- **"Documentation" in agent-facing instructions means `AGENTS.md`** (this file plus every applicable subtree `AGENTS.md`). When the user says "read documentation", re-read the relevant `AGENTS.md` files end-to-end; do not substitute `erun-docs/` for that step. When the user says "update documentation", edit the nearest applicable `AGENTS.md`. `erun-docs/` is the public product documentation site — a separate concern with its own update workflow, owned by the `erun-docs/AGENTS.md` rules. Read it only when its content is the load-bearing reference for an investigation; do not treat it as the source of repo workflow or engineering guidance.
- Do not add new documentation files unless the user explicitly asks for them; add repository instructions to `AGENTS.md` instead.
- Capture repository guidance, conventions, and best practices in the appropriate `AGENTS.md` — the shared, tool-agnostic source of truth every human and agent reads. Do not use a coding assistant's own private memory or notes feature (for example Claude Code's memory) to persist repo guidance or best practices; that hides them from everyone not using that tool. When you learn something worth keeping as repo guidance, propose adding it to the relevant `AGENTS.md` instead.
- Keep `AGENTS.md` focused on repository workflow and engineering guidance; do not document app behavior, command semantics, or end-user functionality in it.
- Do not modify `README.md` unless the user explicitly asks for a README change.

## Modules

Each module owns its own `AGENTS.md` with the details for working in it; read the relevant one before touching that subtree.

- `erun-cli` — CLI utility (the `erun` binary). See `erun-cli/AGENTS.md`.
- `erun-common` — shared, transport-agnostic core types and logic; the canonical home for the shared Go conventions. See `erun-common/AGENTS.md`.
- `erun-mcp` — MCP server module (the `emcp` executable). See `erun-mcp/AGENTS.md`.
- `erun-backend` — backend service area containing the API and database migration modules. See `erun-backend/AGENTS.md` (plus `erun-backend/erun-backend-api/AGENTS.md` and `erun-backend/erun-backend-db/AGENTS.md`).
- `erun-devops` — runtime Docker images, Linux packaging, and Kubernetes chart assets used by build, open, deploy, and release flows. See `erun-devops/AGENTS.md`.
- `erun-kit` — shared, transport-agnostic frontend foundation (design tokens, shadcn primitives, generic widgets, and eventually shared models/slices) consumed by both `erun-ui/frontend` and `erun-console` via Yarn workspaces; the frontend-side analogue of `erun-common`. See `erun-kit/AGENTS.md`.
- `erun-ui` — desktop app built with Wails (Go backend + TypeScript/Yarn frontend). See `erun-ui/AGENTS.md` (plus `erun-ui/playwright/AGENTS.md`).
- `erun-console` — hosted web console SPA (Vite + React + TypeScript/Yarn); separate from the desktop `erun-ui`, talks to `erun-backend-api` directly over HTTP. See `erun-console/AGENTS.md` (plus `erun-console/playwright/AGENTS.md` for the real-Zitadel OIDC sign-in e2e).
- `erun-docs` — public product documentation site (Docusaurus 3.x), published to Cloudflare Pages via a k8s Job under `erun-devops/k8s/erun-docs/`. See `erun-docs/AGENTS.md`.
- `erun-integration` — cross-module integration test harness; runs the compiled `erun` binary with `--dry-run` against per-command goldens and gates merged coverage. See `erun-integration/AGENTS.md`.
- `erun-skills` — canonical source for Claude Code / Codex SKILL.md files and the Claude Code plugin manifest; consumed by both the runtime image (in-pod install) and the marketplace at the repo root (laptop install). See `erun-skills/AGENTS.md`.

## Contributing

- Erun GitHub repository: `https://github.com/sophium/erun`
- Use this repository to extend ERun functionality.
- Start by creating or confirming the GitHub issue that tracks the work.
- Branch from `main` using the issue-linked naming rules defined below.
- Implement the change and run the relevant validation before publishing.
- Push the branch and open a pull request back into `main`.
- After a pull request is accepted, switch the local checkout back to the branch the PR targeted, usually `main`.
- **Once a branch's PR has merged, that branch is dead — never push to it again.** The merge queue lands the PR as a squash commit under a new SHA, so a later push to the old branch updates a ref nothing reads: git and `gh` both report success, and the commit never reaches `main` (erun#2007). If trailing work turns up after merge — even minutes later — start a fresh branch from the updated `main` instead of resuming the old one. Before pushing to a branch you did not just create in this session, confirm its PR/review is still open.
- When the PR is intended to close the issue, include `Closes #<issue-number>` in the PR body.
- A pushed branch or an open PR does not close the issue by itself. The issue closes after the PR is merged or if it is closed manually.
- If the user asks for `push, accept`, treat that as completing the full publish flow rather than stopping after the branch push.
- If the user asks to `close`, always treat that as the repository publish flow in this repo: push the branch, open the PR, merge it with squash unless they asked otherwise, close the PR via merge, and close the linked issue.
- Do not interpret `close` as a request to end or archive the conversation in this repository.
- Publishing needs an authenticated `gh`. If `gh auth status` reports no host, do not stop and hand the publish flow back to the user. Drive the GitHub OAuth device flow yourself: `POST https://github.com/login/device/code` with the GitHub CLI client id `178c6fc778ccc68e1d6a` and scope `repo read:org gist workflow write:packages` (`write:packages` is what publishing to ghcr needs — a token minted without it can clone, branch and PR, then fail at the release push), surface the returned `user_code` together with `https://github.com/login/device`, then poll `POST https://github.com/login/oauth/access_token` (grant `urn:ietf:params:oauth:grant-type:device_code`) until it returns an `access_token`. Store it with `gh auth login --with-token`, then continue the publish flow without further user input.
- The device-code entry in the user's browser is the only auth step that requires the user, and it requires only that — not a pasted token, and not an interactive `gh auth login` in a TTY. `gh auth login` (including `--web`) hangs in this harness because it has no controlling terminal; the curl-driven device flow does not, so prefer it.
- Never fabricate a token, scrape application secrets for one, or report the publish flow as blocked on authentication before attempting the device flow.
- Stay within the current PR for the whole body of related work. When additional bugs, gaps, or improvements surface while working on a branch, add them to the same PR rather than filing a separate issue or opening a new branch. Do not propose splitting work into multiple PRs, and do not ask whether to split — assume the answer is "no" unless the user explicitly says otherwise. Update the PR title and body to reflect the broader scope when the diff grows.
- One body of work, one PR. The PR may link to multiple issues (`Closes #A` / `Closes #B`), but the unit of review is the PR, not the issue.

## Architecture And Module Boundaries

Internal ownership and per-module conventions live in each module's `AGENTS.md`. This is the cross-cutting dependency graph that binds them:

- Shared, transport-agnostic domain logic lives in `erun-common`; transport-specific code lives in the transport modules (`erun-cli`, `erun-mcp`, `erun-ui`). Shared logic may move downward into `erun-common`; transport-specific logic must not move upward into shared packages.
- `erun-cli` and `erun-mcp` must not import each other. Both reach backend functionality through transport-neutral clients and contracts in `erun-common`, not by importing backend API packages directly.
- `erun-ui` is an additive desktop transport, not a replacement for shared domain logic. It may depend on `erun-common` and may launch the installed `erun` executable as a child process, but it must not import `erun-cli` packages. Keep shared tenant and environment resolution in `erun-common`.
- Keep `erun-common` usable as a standalone library: transport-agnostic, with no dependency on Cobra, the MCP SDK, or module-specific orchestration.
- By default, implement new commands in both transports (CLI and MCP) over shared `erun-common` logic; treat a single-transport command as the exception that needs a clear reason.

### Command primitives vs orchestration

- `build`, `push`, `deploy`, and `open` are **pure primitives** that each do exactly one thing and contain **no environment-type or env-name decision logic**: `build` builds container images and mints the version; `push` publishes images (and, for the runtime image, the runtime chart) at a version; `deploy` helm-installs a published version by reference; `open` opens a shell. A primitive must not decide *what else* to run based on the environment — that is policy, and policy belongs to the caller.
- **The version is a content identity minted only by `build`.** `push` and `deploy` require an explicit version; running them without one (and without an orchestration switch) is an error, not a trigger to build. The CLI may prompt; MCP-exposed paths must fail clearly when the version is missing.
- **Orchestration is explicit and opt-in.** A command may grow a switch that runs prerequisite primitives first (`build --deploy`, `open --deploy`), but the default is pure. These switches are **operator convenience only**. Programmatic orchestration layers — the `erun-ui` desktop app, scripts, agents driving MCP — must **not** use the convenience switches; they compose the pure primitives themselves and thread the version (capture it from `build`'s `--output json` result) so the policy ("for this env type, the operator's action means build→push→deploy") lives in the caller, never in the command.
- **Chart publishing belongs to `push`** (it publishes the runtime image and chart together at a version); `release` orchestrates build → push → git-tag and reuses `push` for all publishing. `deploy` never builds, pushes, or publishes.
- **`release` is the one orchestrator whose ordering is a contract, not a convenience.** It publishes *before* it tags: the local stamp/commit/tag, then the build+publish of every image and chart the version resolves, then the read-back that proves they resolve, and only then the tag push, packaging-checksum sync, version bump, branch push, and GitHub release. `erun release` and `erun build --release` run the same execution so the two cannot drift. See § "Release Rules" for the invariants this ordering exists to hold.

## Answering The Operator's Questions

- The operator's questions are never rhetorical. When the operator asks a question — "how is it done if it is not done?", "why X?", "is Y finished?", "nothing changed?", or pushes back with a question instead of a statement — the question is a genuine request for an answer. Answer it directly and completely as the first thing your reply does, before anything else.
- A question is not authorization to act. Do not respond to a question by launching a workflow, editing files, running commands, or "helpfully" starting the work the question hints at. Answer first; then wait for an explicit instruction to proceed, or ask what the operator wants. Acting on a question instead of answering it is a defect.
- Answer honestly, including when the honest answer is "it is not done", "I was wrong", or "that does not work yet". Do not reframe a question as already-handled, do not bury the answer under a wall of planned actions, and do not substitute activity for an answer. If the operator had to ask, the prior reply was probably overclaiming — treat the question as a signal to correct the record, not to push forward.
- A question that exposes a contradiction (something claimed done that visibly is not) takes priority over momentum. Stop, state the real situation plainly, and only resume work once the operator has confirmed the direction.
- Self-check before sending a reply to a question: the first sentence must state the answer. If the reply opens with anything that is *not* the answer — announcing what you read or are about to do ("Reading …", "Let me …"), agreeing or framing first ("You're right", "Good question", "Understood"), restating the question, a meta-apology about your own conduct ("that's my mistake, I'll …"), or an unsolicited "want me to do X?" offer the operator did not ask for — delete that opening and lead with the answer. Answer, then stop; propose next steps only when asked. Repeated re-asking of the same question means the earlier replies buried the answer — treat that as the defect to fix, not a prompt to explain yourself further.

## Working Rules

- Start each non-trivial change by identifying the smallest coherent outcome that would satisfy the request, the modules likely affected, and the validation scope needed for confidence.
- When asking the user to confirm a plan, show a plan only for the intended change, not for discovery by itself. Describe the actual changes to make: the behavior that will change, the user-visible outcome, the likely files or modules to edit, and the validation that will prove the change. Keep investigation or discovery steps out of the plan unless they materially affect the proposed implementation.
- Prefer fast, evidence-driven iteration. Use existing behavior, failing symptoms, tests, and screenshots as the source of truth, then tighten the implementation around the observed problem.
- Keep work centered on the current user goal. Avoid opportunistic cleanup, broad redesign, or unrelated polish unless it directly reduces risk for the requested change.
- When a problem crosses module boundaries, solve it at the lowest shared layer that owns the behavior, then keep transport-specific code focused on adaptation and presentation.
- Make high-impact behavior explicit before executing it. Favor plans, previews, confirmations, and reversible steps when work can delete data, mutate remote systems, publish artifacts, or affect shared environments.
- Record state that describes what has already been applied to a cluster when it is applied, not only when the whole operation succeeds. A run that fails after the change landed still leaves the environment carrying it, so a record withheld until success leaves the environment unable to name what it is running — precisely when an operator needs it to, since recovery usually means re-supplying that value. The exception is a value a failed run turns into a phantom, such as one minted locally that was never published; that stays a post-success write, and the comment beside it should say which of the two it is.
- Preserve momentum by choosing the simplest defensible design that fits the repository. Add structure only when it clarifies ownership, reduces repeated logic, or prevents a real class of mistakes.
- Treat repeated user corrections as signal that the interaction model is wrong, not just the implementation detail. Revisit the flow and simplify it around what the user is trying to accomplish.
- Treat every change that affects a user-triggered code path as a UX-affecting change, including backend wiring, lifecycle refactors, event-handler edits, persistence work, and frontend logic that does not directly edit a component. Before considering such a change complete, walk through the user-facing sequence it produces and verify the user can see what is happening, recover from what fails, and confirm what succeeded. Setting state without a corresponding visible affordance, or surfacing a status that is cleared by the next lifecycle step before the user can register it, both count as gaps that block the change. For desktop work, follow the impact-review checklist in `erun-ui/AGENTS.md` § "UX Impact Review Checklist".
- Treat every feature addition or feature-changing PR as a docs-affecting change, and include the `erun-docs` plan in the same approval step as the rest of the change. The plan must name each affected page, identify its audience (Operator-facing vs Agent reference — see `erun-docs/AGENTS.md` § "Audience: Operator vs Agent" and § "Companion pages") and the validation (`cd erun-docs && yarn build` clean, anchor and canonical-terminology sweeps). If no docs update is needed, the plan must say so with a one-line reason. Undocumented behaviour is not part of the contract (see `erun-docs/AGENTS.md` § "Spec discipline"), so missing docs are a defect, not optional follow-up — they land in the same PR as the feature, never as a separate "docs PR." Pure bug fixes that preserve behaviour, refactors with no public-surface change, and internal cleanup are exempt; when unsure whether a change qualifies as exempt, treat it as feature work and include the doc plan.
- Avoid duplicating investigation. Once a cause is established, update the relevant shared guidance, tests, or abstractions so future work can start from that knowledge.
- For high-impact operations, prefer designs that can expose an explicit resolved plan, support dry-run execution, and emit traceable command details.
- New or materially changed action-oriented commands should support `--dry-run` (CLI) or a preview/plan path (MCP) by default unless there is a strong reason they cannot. The preview should resolve the intended work and show the concrete actions that would execute, without performing side effects.
- Do not treat summary notes as a sufficient dry-run for imperative operations when the real execution plan can be shown. Prefer the actual commands, file writes, or concrete operation steps, with secrets redacted only where necessary.
- Keep external dependencies pinned and explicit. Make dependency changes easy to review and avoid hidden runtime coupling where practical.
- CLI prompts are acceptable in interactive flows, but MCP-exposed paths should receive all required input explicitly and fail clearly when input is missing.
- Prefer deterministic command behavior so tool calls are safe to run repeatedly and concurrently.
- Prefer safety and clarity over micro-optimizations.
- For documentation-only guidance changes, do not change app behavior. Update the nearest applicable `AGENTS.md` and validate by reviewing the edited guidance for consistency.
- Fix lint findings by correcting the code; never disable, suppress, downgrade, or threshold-bump a lint rule.
- **Fixing pre-existing issues is mandatory, not optional.** A failing test, lint finding, or other violation you encounter is yours to investigate and fix, even when your change did not cause it. "It was already broken" / "not caused by me" / "out of scope" / "pre-existing" is never a reason to skip, ignore, suppress, defer, or bypass it — no `--no-verify`, no `//nolint`, no `t.Skip`, no `test.fixme`, no commenting-out, no threshold bump, and no reclassifying it as someone else's problem. A flaky test — one that passes only sometimes — is a failing test too: make it deterministic (wait on observable conditions, never wall-clock sleeps or retries-until-green), never skip it, mark it `fixme`, or wave it through as "pre-existing flakiness". The pre-commit hook lints the whole module, so touching any file in a module makes **every** pre-existing finding in that module yours to resolve. Fix it in the same PR — that is the default and the expectation, not a fallback. Deferring to a tracking issue is a narrow exception, permitted only when the fix genuinely cannot land in this PR (it needs a design decision the user must make, or it is a distinct large effort the user has been told about and explicitly agreed to defer); even then you file and link the issue *before* proceeding, and you never present the deferral as routine. A green commit hook, a green module lint, and a green `make integration-test` are the floor, not an aspiration. Surfacing a failure as "pre-existing" or "out of scope" without either fixing it or clearing an explicit deferral is the exact defect this rule exists to prevent — treat the temptation to punt as a signal to fix.
- **Identical failure counts across repeated runs do not prove a failure is real.** A deterministic environmental fault (a missing directory, a stale cache, a fixture leaking state — see `erun-ui/AGENTS.md`'s shared-baseline-row leak for an example) reproduces the exact same count on every run just as reliably as a genuine bug does. Use a matching count as corroboration only after independently confirming the cause; treating it as the discriminator between "this is a real, reproducible bug" and "this environment breaks the same way every time" runs backwards and can cost hours chasing the wrong fix or reopening an already-fixed issue.
- Clarify design and trade-offs in prose conversation; don't batch shaping decisions into rigid question-forms.
- When delegating analysis to sub-agents, use a capable model — not a lightweight locator model — for substantive reasoning.
- Once the user authorizes a body of work (e.g. "do it all" / "carry on"), carry it through to completion across commits and PRs without re-asking permission between increments; surface only genuine blockers. This does not license unrequested expansion — still get plan sign-off before multi-module or public-surface diffs the user did not ask for.
- **A generated type's "never null" is a promise about the source struct, not about what actually crosses the wire.** A Go field with no `omitempty` and a slice type still marshals a nil slice as JSON `null`, and a bindings generator has no way to encode "unless the server took a shortcut" into the TypeScript type it emits. Any consumer that range-iterates or maps over such a field unconditionally will throw on that `null`, and if the throw happens during boot/render before an error boundary's fallback UI can help, the crash can hide a working affordance underneath it (see `erun-ui/frontend/src/app/bootThunks.ts`'s `normalizeBootTenants`, added after exactly this happened to a first-run empty state). Validate boundary-crossing data defensively (`Array.isArray`, a real null check) at the point it enters application state, everywhere the field is one control-flow path away from an unconditional iteration — do not rely on the generated type's nullability annotation alone.

## Smooth, Seamless, No Dead Ends (Mandatory)

**Anything less than a smooth, seamless end-to-end experience is a failure.**
Not a nice-to-have missed, not polish deferred — a failure, on the same footing
as a wrong result. Basic is never enough. A change is finished when the
experience is right, not when the code path works.

**Seamless means the user never has to do the product's work for it.** They
should not have to guess what state they are in, refresh a panel by hand to see
the outcome of something they just did, re-enter a value the product already
holds, leave for a terminal to finish a task they started in the app, or notice
the seam between two subsystems at all. Anywhere the user has to think about the
product's internals to make progress, that is a defect to be filed and fixed,
not the cost of doing business.

**Smooth means the passage of time is handled, not ignored.** Anything that can
take more than a moment shows that it is working, keeps the surface responsive
while it does, and lands its result without a jolt. An action that leaves the
app — an SSO sign-in that opens a browser, a deploy, a build — is the case that
matters most: a frozen screen during it reads as broken, and so does a screen
that snaps to a new state with no explanation of what happened.

**Every state a user can reach must offer a way forward.** A message that names
a problem must also name — and where it can, carry — the action that resolves
it. A surface with no next action is a defect of the same severity as a crash:
the user is stopped, and the product has told them nothing they can do about it.

Three distinct failures, all of them dead ends:

1. **Advice that cannot work.** An error whose suggested remedy does not apply
   to the actual state. Diagnose precisely enough to be right, or say plainly
   what is unknown — never emit a confident remedy for a cause that was not
   checked.
2. **An action that succeeds and changes nothing on screen.** If the surface
   still shows the failure after the remedy succeeded, the user learns that the
   product is broken. Whatever failed must be retried and its new result shown.
   A button that appears to do nothing is worse than no button.
3. **A capability that exists with no way in.** If the CLI or the API can do it
   and the user's surface cannot, that surface has a dead end wherever that
   capability is the answer. "Use the CLI" is not a resolution inside a GUI.
   `erun-integration`'s desktop-surface gate (`erun-integration/AGENTS.md` §
   "Desktop-surface gate") enforces exactly this failure mode for the desktop
   app: a registered CLI command or MCP tool with no reference in
   `erun-ui/frontend/src` fails the gate unless it is declared agent-facing
   (`erun-common/mcp_tools.go`'s `AgentFacing` field, or
   `erun-cli/cmd/command_tree.go`'s `cliOnlyAgentFacingCommands` for a
   CLI-only command).

**Distinguish causes before writing copy.** "Unauthorized" from the platform API
is not one condition. *Session expired*, *identity never enrolled*, *tenant
never connected*, and *permission genuinely refused* are four different user
situations with four different next actions; collapsing them into one sentence
guarantees the sentence is wrong for most of the people who read it.

**Onboarding is where this is judged first.** The user who is not yet connected
to the platform, or whose identity is not yet enrolled in a tenant, is the one
most likely to be lost and least able to help themselves. Getting from "not set
up" to "working" must be possible from inside the product, guided, and short.
Where a step genuinely requires an administrator, the product still owns the
handoff: say exactly who must do what, show the exact values they need, and make
those values copyable — never leave the user to reconstruct them by hand.

## One Agent Job Is One Run (Mandatory)

**An in-pod agent job is one non-interactive run. There is no "later."** No
scheduler wakes it back up, no monitor watches it, and nothing notifies
anyone when a backgrounded process it started finishes. When the job's own
process exits, the run is over — for good — whatever is still executing
underneath it. This is the same dead-end failure mode as § "Smooth, Seamless,
No Dead Ends" above (silence that reads as success), applied to the one
surface an agent fully controls: its own final turn.

- **Run gates in the foreground, with an explicit timeout.** No `&`, no
  `nohup`, no background shell task, no "I'll report back when it finishes."
  If a gate needs to outlive one command, start it as a nested detached job
  (e.g. `erun job start`) and block on it — `job await` / `job status` — in
  the same run, not a promise to check later.
- **A result not in the final message does not exist.** The orchestrator
  reads the job's recorded outcome, not the agent's intentions. Work reported
  only as a plan to check back on it is work nobody will ever see reported.
- **Never end a turn asking a question or offering an option.** Nobody is
  there to answer. A final message that waits on a reply is a dead end
  exactly like a UI screen with no next action: the run is stopped, and nothing
  will ever unstick it.
- This has already cost real work in this repo: agents that backgrounded a
  gate and ended their turn reported `exitCode: 0` while the work sat
  unfinished, uncommitted, or unrecovered, and the orchestrator believed it
  because nothing in the job's own status said otherwise (erun#1374).

## Long Gates Detach Themselves Inside An Agent Pod

`make check` is a thin front door (`scripts/agent-gate.sh`) around the real
gate, `check-gate`. Outside an agent pod — a human's terminal, CI, a plain
`docker build` — it execs `check-gate` directly and behavior is unchanged.
Inside a coding agent's own pod (`ERUN_ENV_TYPE` `local-agent` or
`remote-agent`) it instead detaches `check-gate` through `erun exec job
start` and blocks on `erun exec job await` for a bounded window, because an
ordinary foreground `make check` run (20-40 minutes) outruns a coding agent's
own foreground window and gets auto-backgrounded into a bare task handle —
exactly the case § "One Agent Job Is One Run" above warns about, except here
the agent never chose to background anything. The fix has to be structural,
not a prompt reminder: two lanes given opposite instructions ("foreground
only, never poll" vs. the exact `job start`/`job await` invocations to use)
both fell into the same turn-per-poll loop anyway, because nothing rejected
the ordinary foreground invocation either agent actually typed. Whatever the
caller does — wait once, or re-run the same command after a timeout — the
cost is a small, bounded number of calls, and the job's real exit code and
full captured output still reach the caller once it finishes.

Apply the same pattern to any other command whose normal run time can exceed
an agent's foreground window (see `scripts/agent-gate.sh`'s own header
comment for the exact mechanics). Run `scripts/agent-gate_test.sh` directly
after touching it — like `erun-devops/docker/erun-devops/entrypoint_test.sh`,
it asserts process/argv behavior against a stubbed `erun` and is not wired
into `make check`.

`scripts/parallel-gate.sh`'s `width` mode is the single place that derives
how wide a parallel fan-out (`LINT_PARALLELISM`, `HELM_CHART_TEST_PARALLELISM`)
may run on this environment, from the real CPU quota and a memory ceiling
rather than `nproc` alone — see its own header comment for why `nproc` (which
reads the CPU affinity mask, not the CFS quota) isn't sufficient on its own.
Run `scripts/parallel-gate_test.sh` directly after touching it, same
not-wired-into-`make check` reasoning as `agent-gate_test.sh` above.

The criterion for wrapping a command this way is "long enough that a harness
will background it", never the target's name — `make check` was simply the
first one found this way. `erun-ui/playwright/run.sh` (longer than `make
check` itself; see `erun-ui/playwright/AGENTS.md` § "Headless Launch") and
`make integration-test` (routinely run standalone per § "Integration Test
Gate" below, and the single longest component of `check-gate`) are wired
through the same wrapper for that reason. `erun release` / `erun build
--release` were checked and are not: the merge queue already runs a release
as a detached job in an agent env (see § "Integration Test Gate" below), and
nothing tells an agent to run either directly as routine, foreground,
per-change validation the way `make check`, `make integration-test`, and the
playwright suite are. Re-check this list when a new command earns that same
routine-and-long status.

**A timeout waiting on one of these detached jobs is not the gate's verdict.** `job await`'s own bounded window elapsing reports only that the wait ended — exit 124 from `agent-gate.sh`'s wrapper, or exit 2 from other timeout paths — while the underlying job can still be running, or can already have finished green. Treat either as "unknown, check again" (the same distinction `GateRunStatus` draws between `INCONCLUSIVE` and a real `FAILED` verdict — see `erun-backend/erun-backend-api/AGENTS.md` § "Gate Runs") and re-query the job's actual status/output before reading it as red. Misreading a timeout as a failure has already produced a wrongly-failed green PR.

## Code Comments

- Keep comments terse and abstract: explain the application behavior and intent behind the code — the "why" — not the mechanics a reader can see in the code itself.
- Comment only non-obvious logic. Do not narrate routine or self-evident changes, and do not add a comment for every edit.
- Do not put issue IDs (`#123`, `issue #123`, `See #123`) in comments — provenance lives in the git history (`git blame`), not the source. This rule overrides matching the surrounding comment style: much of the existing codebase (for example `erun-common/ai_launch.go` and the `open` integration scenarios) still carries issue IDs in comments, but those are known pre-existing violations, not precedent. Never add an issue ID because neighbouring comments have one, and strip the ID (keeping any explanatory text) from comments you edit.
- The comment rules above apply to new and edited comments regardless of how the surrounding code is written. When the local style contradicts this guidance, the guidance wins; a pervasive in-repo pattern is not a licence to extend it.
- Prune stale comments as you touch the code; a comment that no longer matches what the code does is worse than none.
- The no-issue-ID rule above is checked mechanically, not just by review: `scripts/check-issue-references.mjs` (TypeScript comments under `erun-kit/src`, `erun-ui/frontend/src`, `erun-console/src`) and `erun-integration/issue_reference_test.go` (Go comments) both gate `main`. Catch a violation locally instead of in that gate: enable `.githooks/pre-commit` once per clone (`git config core.hooksPath .githooks`, see README "Enable git hooks") — it now runs the TypeScript-side check automatically on `git commit` whenever staged files fall under those three roots — or run `node scripts/check-issue-references.mjs erun-kit/src erun-ui/frontend/src erun-console/src` directly before pushing a change that touches frontend comments.

## End-to-End Verification Gate (Mandatory)

When a user reports a bug they observed, or when a change touches behaviour that ships in a deployed artifact, the agent verifies the fix end-to-end in the live target — itself, not by handing steps back to the user. Test suites (unit, integration, UI-harness) give code-level confidence; they do not replace running the originally failing flow against the deployed artifact and watching it succeed.

The gate has three parts:

1. **Roll the change into the actual target.** Whatever the change ships through (image, chart, binary, config), drive that path until the running target carries the change. Cached/promoted artifact pipelines must be bypassed when needed so the new content really lands, not a stale equivalent. When an unrelated external precondition blocks the standard rollout, find an equivalent narrower path (direct patch, manual deploy step, etc.) so the target picks up the fix and the gate can proceed; don't stop at "rollout failed, blocked."

2. **Reproduce the original failure verbatim from the same vantage point.** Run the same command, from the same place, with the same inputs the user did. If the failure was inside a sandboxed runtime, exercise it from inside that runtime. If it was a layer talking to another layer, exercise it through that same interface. Use the project's already-documented diagnostic surfaces to reach the deployed state; spin up the minimum harness needed when none exists. Watching it succeed is the gate; watching a related path succeed is not.

3. **Be honest about what you didn't verify.** Some surfaces can't be driven without a human (interactive UI rendering, OS-level integration, real keystroke timing). Name those explicitly, point at the closest test-harness signal that covers them, and don't pad the verified-list with checks that did not happen against the actual deployed artifact.

Exceptions are narrow: changes with no live-target surface (pure refactors that don't change observable behaviour) are validated by the existing suites alone. Anything that crosses a deployed boundary triggers the full gate.

Probe artifacts the agent leaves behind during verification (injected files, manual patches that diverge from the source of truth, helper processes) are the agent's mess to clean up before declaring done, so the user's environment returns to a clean state.

## Run `make fast-check` Before Pushing

`make fast-check` is a fast, local subset of `check-gate` — golangci-lint, the tracker-reference gate, and prettier formatting — that runs in seconds to under a minute, not the 9-10 minutes a full `make check`/`check-gate` cycle costs. Agents are what push in this repository (see § "Contributing" above), so an agent pushing a branch without having run `fast-check` first is choosing to find out about a lint finding, a tracker reference in a comment, or an unformatted file ten minutes later in the merge gate instead of immediately. Run `make fast-check` before every push; it is cheap enough that there is no reason not to.

`fast-check` is **not** a substitute for `make check` / `make check-gate`, and passing it does not mean the branch is ready to merge — it runs no tests, no build, and no integration suite. Still run the full gate (or push and let the merge queue's own gate run it) before merging; `fast-check` only shortens the feedback loop for the narrow class of failure it covers.

## Integration Test Gate (Mandatory)

- `make integration-test` must be green on `main` at all times. Do not merge a PR that leaves any scenario red, including scenarios that were already failing before your branch — if you discover a preexisting red, either fix it in the same PR or open a tracking issue and a follow-up PR before merging anything else that touches the suite. "Some tests were already broken" is not a license to add more.
- This repository intentionally has no hosted CI (#521): GitHub-hosted runners are ephemeral, which defeats erun's daemon-centric build caching, and erun provides its own build system and merge queue instead. Both halves of that queue are state in `erun-backend-api`, not a dedicated Job: promoting a review to `MERGE` is the environment's own cue to build the prospective merge of the review's source onto its *current* target and gate it with a real `erun build` itself, pushing only on green and reporting the outcome — `MERGED` is accepted only once the platform verifies the reported commit against the real repository, so it means a merge actually happened and a build actually passed, not a caller's assertion. An accepted (now actually-merged) review then enqueues a release, which the environment that earns it runs the same way. **This is no longer aspirational** (correcting the prior "nothing runs it against this repository's own PRs today" claim here): the client-side plumbing (`erun exec gate-merge`, `erun review record-build --gate`, `erun review report-merged`, and the `erun-merge-queue-drive` skill chaining them — see `erun-docs/docs/collaboration/merge-queue.md` § "The gate") is what actually landed dozens of this repository's own merges and two releases, verified server-side by `acceptMerged` on every one. What remains is a distinct, narrower gap tracked in #1912: GitHub's own branch-protection ruleset on `main` still has no way to tell the queue's own legitimate direct push apart from a human bypassing review outright — both currently authenticate as the same shared credential and print the identical "Bypassed rule violations" warning, so that warning carries no signal today. Both halves of the fix now exist in code — `erun exec plan-ruleset-bypass` resolves the exact two-stage ruleset edit that narrows the bypass grant to one non-human identity (and refuses when its preconditions do not hold), and `erun exec reconcile-bypass --expected-actor` accounts for every bypass afterwards — but applying the edit is a repository-settings change an operator performs, so the gap stays open until they do. See `erun-backend/erun-backend-api/AGENTS.md` § "Merge Queue" for the recorded plan and the corrections behind it. `erun exec report-commit-status` exists but is a separate mechanism aimed at a plain-GitHub-PR-flow repo requiring a status on a pull request's own head commit before its merge button unlocks — it has never been wired into this repository's own gate-merge/push flow, has never run against a real merge, and does not by itself close #1912's gap; nothing in the narrowing plan depends on it, and no `required_status_checks` rule may be added before a producer is proven end to end. Run the gate locally before merging (`make check`) in the meantime, and every erun-driven build anywhere else re-runs it via the image's test stage (see `erun-devops/AGENTS.md` § "Build Workflow"). Do not reintroduce GitHub Actions workflows for build or test gating.
- Run `go test ./...` (or `make integration-test`) under `erun-integration/` before pushing any change that touches `erun-cli`, `erun-common`, the runtime entrypoint, the chart deploy plumbing, or any integration golden. If a scenario is red on your machine but you believe it is "platform-dependent" or "flaky", that is a defect to fix (see the host-OS pinning guidance in `erun-integration/AGENTS.md`), not a reason to ship. `make integration-test` is itself wired through `scripts/agent-gate.sh` (see § "Long Gates Detach Themselves Inside An Agent Pod" above) since it is routinely run standalone and is long enough on its own to hit the same foreground-timeout failure; `make check-gate` depends on the underlying `integration-test-gate` target directly so a `make check` run never nests one detached job inside another.
- The integration suite runs the compiled binary as a subprocess against per-command `--dry-run` goldens. The contract for `--dry-run` is therefore a hard public-surface boundary: every action and every decision the command would take must appear as a trace line, regardless of whether downstream input resolution succeeds. Treat a missing trace as a bug, not a documentation gap.
- Coverage is gated on `erun-cli` + `erun-common` only, and the integration suite is the single source of truth for that coverage — unit tests in those packages do not count toward the gate, so write the integration scenario for new `erun-cli`/`erun-common` behavior and delete unit tests that overlap one. Other modules (`erun-mcp`, `erun-backend`, `erun-ui`) are validated by their own per-module suites.
- Do not skip integration scenarios to make the suite green. If a regression is discovered, leave the failing scenario in place so the gate fails until the regression is fixed; that is the suite working as designed.
- See `erun-integration/AGENTS.md` for harness layout, scenario shape, fixture patterns, normalization rules, the coverage threshold and scope, and stub-injection guidance.
- **The gate's `erun build` cannot verify the desktop app (#1933), and #1937 answers the toolchain half of the fix: yes, it can run there.** `make check`'s `test` stage had no Wails/webkit toolchain, so `erun-ui/playwright` never ran inside a `GATE` build even though `erun-ui/AGENTS.md` makes that suite mandatory for every desktop change — a green `GATE` build proved nothing about `erun-ui/**` until this was caught: four desktop PRs (#1911, #1925, #1926, #1927) merged through the gate with their own new Playwright specs never executed anywhere, and a real run against `main` afterward found `27` failures, including a regression in #1901's own spec that the gate had reported green. **Verified empirically, not assumed:** the `erun-devops` test stage now installs the same Wails/webkit CGO toolchain and Playwright Chromium runtime deps the final image installs (`build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev libsoup-3.0-dev`, then `install-deps chromium`) — the packages install cleanly on the exact `golang:1.26.0` base the test stage already uses, `go build -tags "desktop,production,webkit2_41"` succeeds there, the compiled binary runs `--headless` with zero display (no Xvfb — `runHeadless` never calls `wails.Run`), and a `make check-gate` run against this exact Dockerfile change passed — so there is no platform blocker to running the suite inside the gate. `make test-playwright` (root Makefile) builds that headless `erun-app` and runs the suite against it from the same stage.
- **#1937 also root-caused, and structurally fixed, one whole class of the suite's nondeterminism: the shared seeded baseline rows leak real state across spec files.** `SEED_ENV_ALPHA`/`SEED_ENV_BETA` are the two seeded rows nearly every spec touches, and the worker-scoped headless backend (`erun-ui/playwright/fixtures/workerBackend.ts`) that serves them lives for the whole worker's lifetime, not one spec file — so `App.envActivity`/`App.envUsage` (in-memory maps keyed by tenant/env, `erun-ui/environment_activity.go` and `environment_usage.go`) keep a real observation (including its own `observedAt` timestamp, stamped even for a never-deployed env's routine "not running" reading) from whichever spec ran first, and every later spec in that worker inherits it — first found as the #1901 hover-card layout spec's "zone 2" race (fixed there by switching to fresh, pristine envs; `erun-ui/playwright/tests/sidebar-hovercard-layout.spec.ts`), but the mechanism is general, not specific to that one spec. The fix lands at the fixture layer rather than by auditing every spec that touches the baseline rows: `App.ResetEnvironmentActivityObservations`/`App.ResetEnvironmentUsageObservations` clear both maps, and `erun-ui/playwright/fixtures/erunApp.ts`'s `app` fixture — the dependency nearly every spec pulls in — calls both over `/__erun_invoke` before the app ever boots, so every test's initial read model starts from "never yet observed" regardless of what an earlier spec in the same worker left behind. A second, independent race in the same #1901 spec — reading hover-card geometry immediately after Radix's `PopoverContent` becomes visible, racing its own ~150ms entrance transform — is fixed on the separate, not-yet-merged `feature/1901-hover-card-layout-fix` branch; it is not duplicated here.
- **Re-verified after the fixture-isolation fix landed, still not yet a `check-gate` prerequisite.** The suite had 27 failing specs and non-deterministic results under parallel load (two full runs on the same commit: 27 vs 24 failures, only 3 files red in both) before the shared-baseline-row fix above. That fix (originally landed as `bug/playwright-fixture-isolation`, ported into this branch) has now been run against this exact commit via `erun exec job` in this agent's own pod (the `erun-devops` final image, which already carried the toolchain for in-pod contribute-mode builds): `make test-playwright`, `502 passed (502)`, zero failures, zero flakes. That is one clean run, not the repeated-run track record `erun-ui/playwright/AGENTS.md`'s "No flaky tests" rule calls for before trusting a suite this large under parallel load; the disciplined next step is more repeated runs, including inside the actual `erun-devops` Dockerfile `test` stage container (not just the final image) rather than assuming the final-image result transfers. `check-gate` does not depend on `test-playwright` yet. Until it does, `erun review record-build --gate` (`erun-common/desktop_coverage_gate.go`) keeps refusing a successful call whose commit changes `erun-ui/**` unless `--desktop-playwright-verified` attests the suite was actually run against it — the #1933 fail-closed stopgap stays the enforcement mechanism, not a replacement. See `erun-docs/docs/collaboration/merge-queue.md` § "The gate" and § "The desktop coverage gap".

## Release Rules

- **A release that exits 0 means the version is deployable, and a release that cannot publish fails before anything is public.** These two invariants set the stage order, and nothing may reorder around them:
  - The release stages split at the publish. Everything before it (`sync-remote`, `release` — the chart/packaging stamp, the commit, the *local* tag) is recoverable by re-running; everything after it (`push-release-tag`, `sync-packaging-checksums`, `post-release-version-bump`, `sync-develop`, `push`, the linux `release.sh` scripts that create the GitHub release) is outward-facing. A new stage goes in the group that matches its reversibility — never "wherever it happens to work".
  - The publish is not assumed to have landed. `verify-publication` re-resolves every published image's multi-arch manifest, and each chart is read back as it publishes; a version whose artifacts cannot be resolved never reaches its tag.
  - The version bump is a post-publish step for a reason. It moves `VERSION` past the version being released, so a bump ahead of the publish strands that version: a plain `erun build` then mints the *next* one, and `push --version <released>` cannot assemble a manifest from per-arch tags nothing built. Keeping the bump after the publish means a failed release leaves `VERSION` on the version it was releasing, and re-running retries the same version.
  - A release refuses to run when its resolved images are not all covered by a build it will publish. Announcing a version the registry never receives is the failure this exists to prevent; do not soften it into a warning.
  - **The base branch is not assumed to stand still.** `sync-remote` establishes fast-forwardability once, at the start; the release then re-reads the branch immediately before the build and refuses a move it can still see cheaply, and its final push rebases onto a branch that moved during the build and retries, bounded. Neither half may be dropped in favour of "do not merge while a release is in flight" — that is guidance for humans, and the tool must not depend on it being followed. A knowable blocker belongs before the spend, and a knowable recovery belongs where the alternative is a registry the repository disagrees with.
- **A detached release can be interrupted by its own pod dying, and the recovery is designed around that (#1051).** A long release is the build most likely to fill the node's disk (a multi-arch, many-image build), and filling the node's disk is what gets a pod evicted — so the release most likely to need resuming is also the one that caused its own interruption. Three things follow:
  - `ensureReleaseDiskHeadroom` (`erun-common/release_disk_headroom.go`) prunes reclaimable docker build cache and refuses up front when the docker root's free space is observably below a floor (20 GiB default, `ERUN_RELEASE_MIN_DISK_HEADROOM_BYTES` to tune it) — the same "known failure caught before the spend" shape as the registry-permission and images-not-covered preflights above. An inconclusive read (the daemon's root is on a different container's filesystem, e.g. the erun-dind sidecar) is not a refusal; it proceeds exactly as before this preflight existed.
  - Before rebuilding anything, the release reports what the registry already has at the target version (`reportAlreadyPublishedReleaseArtifacts` in `erun-common/release_publish.go`) — a single, non-retried probe, not a resumability rework: it does not change what actually gets rebuilt (the fingerprint-based promote-and-skip inside `Publish` still decides that), it only tells the operator up front rather than leaving them to infer progress from how fast the rebuild finishes.
  - A local release tag that exists but not at HEAD is refused (as it always was — it must not tag a commit it did not build), but when it is unpushed *and* origin's branch history has never incorporated it, the refusal now names the diagnosis and the exact remedy (`git tag -d <tag>`) instead of leaving the operator to work out by hand that the tag is an interrupted run's leftover rather than a real collision. Reclaiming it automatically was judged too aggressive for a git tag inside a release flow.
  - A job's own terminal state distinguishes "the runtime pod was replaced" from "the same pod is still running but the supervisor process is gone" (`EnvironmentJob.UnknownReasonKind`, `erun-common/job.go`) by comparing the pod hostname recorded at job start against the hostname reconciling the record — a Kubernetes pod's hostname is its pod name, unique per pod instance, so a mismatch is certain, not a guess.
- Treat release work as repository-wide. When changing release behavior, validate `erun-common`, `erun-cli`, and `erun-mcp`, not just the module where the code change landed.
- When release, launcher, or desktop packaging behavior changes affect the desktop app, validate `erun-ui` too and keep package-manager metadata aligned with the desktop build outputs.
- Keep stable release automation responsible for all repository metadata that must move with the release, including versioned charts, package-manager metadata, and generated release references; when that metadata references GitHub archive assets, update both version fields and checksums instead of rewriting only URLs.
- Treat release-time Docker images as dependency graphs, not isolated targets. If a release image depends on local base images, publish those local dependencies before publishing the dependent image.
- `erun build --release` (and `erun release`, which reuses the same build) always produces both `linux/amd64` and `linux/arm64`, with no override: a released artifact is published for anyone and must be deployable on any cluster, and arch-specific Dockerfile bugs in a release must fail at build time, not at a stranger's remote deploy. Non-release `erun build`/`erun push` default to the same multi-arch pair, but may be narrowed to one platform for an environment whose own cluster can only ever run it and therefore never pays to build or promote the platform it can't — either per invocation (`erun build --platform linux/amd64`, repeatable) or pinned permanently for an environment via `.erun/config.yaml`'s `environments.<env>.docker.platforms`. Combining `--platform` with `--release` is refused outright rather than silently narrowing a released artifact. `erun push` (including the push step `release` runs) assembles the multi-arch manifest list — a list of one entry when the build targeted one platform — and publishes the runtime chart alongside the runtime image; `deploy` only installs a published version and never builds. Non-release `erun build` stops after the per-arch local builds.
- Multi-architecture builds must verify daemon capability explicitly. Fail with a direct error when the local Docker daemon cannot produce all required target platforms (e.g. binfmt is missing for the foreign arch), rather than letting the per-platform `docker build` fail with a confusing message.
- Keep the runtime deployment and release-build environment aligned. If the runtime pod performs release builds through dind, ensure the deployment installs the required binfmt or emulator support before the daemon is used for multi-arch builds.
- Prefer pinned versions for release-critical infrastructure images such as binfmt helpers, dind, and runtime base images so release behavior stays reproducible.
- When release automation pushes tags or branches mid-flow, add the follow-up verification needed for later steps. Do not assume remote state, package archives, or checksums are available without checking.
- Add regression tests for each release failure mode that was fixed. When a release bug is caused by ordering, missing metadata, or missing platform support, encode that contract in tests so the next release cannot regress silently.
- When a change affects generated runtime charts or embedded templates, test both the shared template source and the concrete runtime chart when practical. Treat them as one contract.

### Release cadence policy (#1985, design recorded — not yet implemented)

- **The trigger already exists; the drain does not.** Every merge already enqueues a release: `ReviewService.acceptMerged` calls `ReleaseTrigger.TriggerRelease` (`erun-backend/erun-backend-api/internal/service/reviews.go:451`), which is `ReleaseService.Enqueue` — one idempotent `queued` row per distinct commit (`UNIQUE (tenant_id, commit_id)` on `releases`). But `ReleaseRepository.ClaimNext`, the half that would take the oldest `queued` row and mark it running, has no caller anywhere in the codebase: no HTTP route registers it, no CLI/MCP command wraps it, and no skill exists in the `erun-merge-queue-drive` shape to drive it. A queued release simply sits until a human notices and runs `erun release` by hand — exactly the "reactive, operator-judgement-driven" cadence this issue was opened to replace. `ReleaseRepository.ClaimWindow.Cooldown` — a per-tenant minimum spacing between consecutive releases — already exists in the repository layer too, unused for the same reason.
- **Release cost is measured from this session's own evidence, not the `~/.erun/timing/release-*.json` records the issue asked for** (none exist on the environment used to write this policy — `~/.erun/timing/` here holds only `build-*`/`deploy-*` records, both sub-millisecond no-ops on this pod; the releases this repository actually cut ran from a different environment/credential context, and nothing here can read its timing directory). The available evidence is the issue's own operator-observed measurement: four releases (1.0.244–247) cut in one day, hand-driven, ~20–30 minutes wall-clock each. Reading `erun-common/release.go`/`release_publish.go`'s stage list against that number narrows *where* the time goes without needing a fresh record: the stages before and after `publish` (`sync-remote`, the stamp/commit/local-tag, `push-release-tag`, `sync-packaging-checksums`, `post-release-version-bump`, `sync-develop`, `push`) are git plumbing — seconds each. `publish` (real multi-arch Docker builds and pushes for every image and chart the version resolves) and `verify-publication` (a registry manifest read-back, network-wait rather than CPU) are structurally the only stages that can plausibly account for 20–30 minutes; `erun-common/timing.go`'s per-run JSON tree already breaks a real run down to prove this precisely, so the next release run anywhere should keep its record and this estimate should be replaced with a real one rather than repeated as fact.
- **Commit accumulation between releases is measured directly from tags.** The last 13 releases before this one (`v1.0.235`…`v1.0.247`) each shipped 5–22 commits since the previous tag (median 7, mean ~9), spaced a median 154 minutes (mean 178 minutes) apart by tag timestamp. `main` sits 16 commits past `v1.0.247` as of this policy being written, with nothing queued or running (`git tag --list 'v1.0.248*'` is empty) — consistent with the issue's "roughly 12 commits" observation, grown further while this design work was itself unreleased. Separately, `erun-backend-db/AGENTS.md`'s `#1956`/`#1970` retention designs already measured this repository's broader activity over 2026-08-27→2026-09-02: 243 PRs merged (~35/day average, 56/day peak) against 43 releases (~6/day) — i.e., the de-facto historical cadence was already **one release per ~6–9 merges**, matching the per-tag commit count above almost exactly. The existing cadence is a batch policy already, just an unwritten one enforced by an operator's judgement instead of a threshold.
- **Per-merge cadence is refuted by the same numbers, not merely undesirable.** `ClaimNext`'s claim predicate allows only one `running` release per tenant at a time — releases are serialized, not parallel. At 35–56 merges/day and ~20–30 minutes per release, a strict one-release-per-merge policy needs 700–1,680 minutes (11.7–28 hours) of serial release execution per day just to keep up, on a tenant that already spends most of a day's wall-clock on other work. Either releases would need to run concurrently (breaking the single-in-flight invariant the queue's own partial unique index enforces, and racing `publish`'s registry writes) or each release would need to be far cheaper (nothing in this design changes what `publish` actually builds) — neither is in scope, so per-merge is measurement-refuted, not a matter of preference.
- **A pure timer is redundant with what the queue already gives idempotently, and a pure batch-count needs a coalescing behavior the schema doesn't have.** A fixed-interval drain (e.g. hourly) that finds nothing `queued` costs nothing, thanks to `Enqueue`'s per-commit idempotency — so a timer is safe to run even when quiet. But `ClaimNext` claims the *oldest* queued row, one commit at a time; if merges outpace the timer, a drain still runs one release per queued row in FIFO order rather than folding N queued commits into one release of the newest — the exact coalescing the historical "~1 release per 6-9 merges" cadence achieved informally. Today's schema and `ClaimNext` query have no "supersede the older queued rows for this tenant" behavior; adding one (claim the *newest* `queued` row per tenant, and mark any now-superseded `queued` rows for the same tenant as resolved by that release rather than running them individually) is what turns "batch-threshold" from a policy statement into something `ClaimNext` can actually execute.
- **Recommended policy: drain on whichever comes first — a commit-count threshold or a cooldown timer — always coalescing to the newest queued commit, until the automation below exists.** Concretely: drain when either 5–10 commits have accumulated since the last release (matching the observed 5–22/median-7 historical range) or `ClaimWindow.Cooldown` (recommend 30–60 minutes) has elapsed since the last release, whichever is sooner; claiming always takes the newest queued commit for the tenant, not the oldest, so N merges since the last drain cost one release, not N. Until a release-queue-drive mechanism exists (see below), this is a directive for whoever triggers `erun release` today — operator or agent — not a claim that it runs unattended.
- **The gate environment's own version is part of this policy, not a detail left to notice.** This session's own hazard: the `build`-shaped environment that gates every merge (the `erun-merge-queue-drive` promotion flow, `erun-backend-api/AGENTS.md` § "Merge Queue") was still running `1.0.246` while every environment it had gated was already built at `1.0.247` — the tool doing the gating was one release behind the code it was validating, so a regression introduced between those two versions in erun's own build/gate logic could pass its own gate undetected. `erun release` never deploys anything — confirmed by reading `erun-common/release.go`: no `Deploy` call exists anywhere in the release execution path, consistent with root `AGENTS.md` § "Command primitives vs orchestration" (`release` orchestrates build→push→tag, never deploy). So nothing rolls any environment onto a newly published version automatically, ever, including the gate environment — and "let each environment decide when to update" (the correct default for ordinary tenant environments) is the wrong policy for the one environment whose own currency is a precondition for every gate it runs on everyone else's behalf. **This policy states explicitly what the merge-queue design left unstated: whichever environment(s) perform the merge-queue gate must be redeployed to the version a drain just published as an immediate, unconditional step of that same drain — never a step an operator remembers separately, and never bundled into ordinary per-environment update discretion.**
- **What is missing to automate any of this, in order:** (1) a release-queue-drive mechanism — an HTTP route or CLI/MCP command wrapping `ClaimNext` with the newest-commit/supersede behavior above, plus a skill in the `erun-merge-queue-drive` shape to drive it, including the gate-environment redeploy step from the previous bullet as part of the same run; (2) `erun-backend-api/AGENTS.md`'s `#1969` design ("An agent environment cannot provision a platform cloud alias") — recorded but not implemented — which is what would let an agent environment run that drainer unattended rather than a human running `erun release` by hand each time. Until both land, this section's threshold-or-cooldown drain plus mandatory gate-environment redeploy is the policy to follow manually.

## Kubernetes RBAC For Server-Side Executors

- **A role that lets helm install is not a role that lets helm wait.** `helm --wait` decides a Deployment is ready by walking Deployment → ReplicaSet → Pods. A role with full `apps/deployments` and no `apps/replicasets` installs fine and then times out on a healthy release, reporting a timeout rather than the Forbidden it swallowed. When adding or auditing a role that runs helm, enumerate the object graph helm traverses, not the objects the chart declares. (#1080, #1083)
- **`kubectl auth can-i <verb> <resource>/<subresource>` gives false positives.** It answered `yes` for `patch deployments/scale` against a role granting only `deployments`, while the live call was Forbidden. Use `kubectl auth can-i --list --as=<sa> -n <ns>` and trust a real impersonated call over either. RBAC treats a subresource as a distinct resource. (#1080)
- **A test that pins a role's rule list only confirms the rules somebody already thought of.** Two consecutive releases shipped a broken provisioner role while such a test passed. Prefer a test that exercises the operation and asserts it succeeds. (#1081, #1083)

## Refactoring Rules

- Treat refactoring as behavior-preserving by default.
- Do not change user-visible output, help text, error text, prompts, logging, defaults, or flags unless the user explicitly asks for that functional change.
- Before and after a refactor, compare observable behavior with `main` and add or update regression tests for any behavior that must remain unchanged.
- Before moving code, identify the ownership boundary, the public callers that must stay stable, and the smallest validation set that can prove behavior did not change.
- During a large-file refactor, move one coherent responsibility at a time and validate after meaningful slices instead of batching unrelated moves into one hard-to-review change.
- For transport-facing refactors, keep method names, argument shapes, return shapes, event names, and generated-binding contracts stable unless the user explicitly asks for a contract change.
- Keep moved code in the same package or module when the move is organizational only. Change package boundaries only when the new boundary is part of the intended design.
- Preserve dependency direction. Shared logic may move downward into `erun-common`; transport-specific logic must not move upward into shared packages.
- Prefer package-private moved symbols unless an existing external caller needs them. Moving code is not a reason to export it.
- After refactoring shared code or moving logic across module boundaries, run validation in all modules: `erun-cli`, `erun-common`, and `erun-mcp`. Use each module's local validation commands; this includes `go test ./...` and linting where the module defines lint configuration.
- Include `erun-ui` in that validation set when the refactor changes desktop wiring, shared code consumed by the desktop app, or package-manager and launcher integration.
- After refactoring, explicitly look for unused code left behind by the move or simplification and remove it. Do not leave dead wrappers, compatibility helpers, or transport-specific glue in place just because tests still reference it.
- When a shared interface in `erun-common` already matches the needed contract, use it directly instead of creating a duplicate local interface with the same methods.
- After extracting shared code, remove test-only production shims where possible and move meaningful coverage to the module that now owns the behavior.
- Prefer deleting obsolete pass-through helpers over keeping rename layers. If a command now calls a shared service directly, remove the old wrapper unless it still provides real CLI-specific behavior.

## Branching Strategy

- Create a GitHub issue before starting implementation work.
- Sync `main` before cutting the branch. Run `git checkout main && git pull --ff-only origin main` first so the new branch starts from the current remote tip, never a stale local `main`.
- Branch from `main`.
- Use `feature/<issue-number>-<short-kebab-case-description>` for new functionality.
- Use `bug/<issue-number>-<short-kebab-case-description>` for bug fixes.
- Include the issue number in the branch name for traceability, for example `feature/12-add-mcp-server-entrypoint`.
- **Never branch from another open PR's branch head to pick up its unmerged work.** The merge queue lands a PR as a squash commit under a brand-new SHA (root `AGENTS.md` § "Integration Test Gate" / `erun-docs/docs/collaboration/merge-queue.md` § "The gate"); a branch forked from the old head still carries that same work under its original SHAs, so the moment the dependency merges, the dependent branch conflicts with itself against `main` — silently, with no warning from git, `gh`, or erun (erun#2007). If work genuinely depends on something unmerged, wait for it to merge and branch from `main` afterward; if it cannot wait, rebase the dependent branch onto `main` as soon as the dependency lands and drop the commits `main` already carries under the new SHA.

## Pull Request Titles

- Use a clean human title that describes the change directly.
- Do not add agent markers such as `[codex]` unless the repository explicitly asks for them.
- Prefer sentence-style titles such as `Add HTTP MCP server entrypoint`.

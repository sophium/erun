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
- Clarify design and trade-offs in prose conversation; don't batch shaping decisions into rigid question-forms.
- When delegating analysis to sub-agents, use a capable model — not a lightweight locator model — for substantive reasoning.
- Once the user authorizes a body of work (e.g. "do it all" / "carry on"), carry it through to completion across commits and PRs without re-asking permission between increments; surface only genuine blockers. This does not license unrequested expansion — still get plan sign-off before multi-module or public-surface diffs the user did not ask for.

## Code Comments

- Keep comments terse and abstract: explain the application behavior and intent behind the code — the "why" — not the mechanics a reader can see in the code itself.
- Comment only non-obvious logic. Do not narrate routine or self-evident changes, and do not add a comment for every edit.
- Do not put issue IDs (`#123`, `issue #123`, `See #123`) in comments — provenance lives in the git history (`git blame`), not the source. This rule overrides matching the surrounding comment style: much of the existing codebase (for example `erun-common/ai_launch.go` and the `open` integration scenarios) still carries issue IDs in comments, but those are known pre-existing violations, not precedent. Never add an issue ID because neighbouring comments have one, and strip the ID (keeping any explanatory text) from comments you edit.
- The comment rules above apply to new and edited comments regardless of how the surrounding code is written. When the local style contradicts this guidance, the guidance wins; a pervasive in-repo pattern is not a licence to extend it.
- Prune stale comments as you touch the code; a comment that no longer matches what the code does is worse than none.

## End-to-End Verification Gate (Mandatory)

When a user reports a bug they observed, or when a change touches behaviour that ships in a deployed artifact, the agent verifies the fix end-to-end in the live target — itself, not by handing steps back to the user. Test suites (unit, integration, UI-harness) give code-level confidence; they do not replace running the originally failing flow against the deployed artifact and watching it succeed.

The gate has three parts:

1. **Roll the change into the actual target.** Whatever the change ships through (image, chart, binary, config), drive that path until the running target carries the change. Cached/promoted artifact pipelines must be bypassed when needed so the new content really lands, not a stale equivalent. When an unrelated external precondition blocks the standard rollout, find an equivalent narrower path (direct patch, manual deploy step, etc.) so the target picks up the fix and the gate can proceed; don't stop at "rollout failed, blocked."

2. **Reproduce the original failure verbatim from the same vantage point.** Run the same command, from the same place, with the same inputs the user did. If the failure was inside a sandboxed runtime, exercise it from inside that runtime. If it was a layer talking to another layer, exercise it through that same interface. Use the project's already-documented diagnostic surfaces to reach the deployed state; spin up the minimum harness needed when none exists. Watching it succeed is the gate; watching a related path succeed is not.

3. **Be honest about what you didn't verify.** Some surfaces can't be driven without a human (interactive UI rendering, OS-level integration, real keystroke timing). Name those explicitly, point at the closest test-harness signal that covers them, and don't pad the verified-list with checks that did not happen against the actual deployed artifact.

Exceptions are narrow: changes with no live-target surface (pure refactors that don't change observable behaviour) are validated by the existing suites alone. Anything that crosses a deployed boundary triggers the full gate.

Probe artifacts the agent leaves behind during verification (injected files, manual patches that diverge from the source of truth, helper processes) are the agent's mess to clean up before declaring done, so the user's environment returns to a clean state.

## Integration Test Gate (Mandatory)

- `make integration-test` must be green on `main` at all times. Do not merge a PR that leaves any scenario red, including scenarios that were already failing before your branch — if you discover a preexisting red, either fix it in the same PR or open a tracking issue and a follow-up PR before merging anything else that touches the suite. "Some tests were already broken" is not a license to add more.
- This repository intentionally has no hosted CI (#521): GitHub-hosted runners are ephemeral, which defeats erun's daemon-centric build caching, and erun provides its own build system and merge queue instead. The trigger half of that queue now exists in `erun-backend-api` — an accepted review enqueues a release, and a per-tenant serial queue runs `erun release` as a Job in an agent env with warm caches. It is opt-in per control plane and is not yet a required status check, so the gate is still enforced where it always was: run it locally before merging, and every erun-driven build anywhere re-runs it via the image's test stage (see `erun-devops/AGENTS.md` § "Build Workflow"). Do not reintroduce GitHub Actions workflows for build or test gating.
- Run `go test ./...` (or `make integration-test`) under `erun-integration/` before pushing any change that touches `erun-cli`, `erun-common`, the runtime entrypoint, the chart deploy plumbing, or any integration golden. If a scenario is red on your machine but you believe it is "platform-dependent" or "flaky", that is a defect to fix (see the host-OS pinning guidance in `erun-integration/AGENTS.md`), not a reason to ship.
- The integration suite runs the compiled binary as a subprocess against per-command `--dry-run` goldens. The contract for `--dry-run` is therefore a hard public-surface boundary: every action and every decision the command would take must appear as a trace line, regardless of whether downstream input resolution succeeds. Treat a missing trace as a bug, not a documentation gap.
- Coverage is gated on `erun-cli` + `erun-common` only, and the integration suite is the single source of truth for that coverage — unit tests in those packages do not count toward the gate, so write the integration scenario for new `erun-cli`/`erun-common` behavior and delete unit tests that overlap one. Other modules (`erun-mcp`, `erun-backend`, `erun-ui`) are validated by their own per-module suites.
- Do not skip integration scenarios to make the suite green. If a regression is discovered, leave the failing scenario in place so the gate fails until the regression is fixed; that is the suite working as designed.
- See `erun-integration/AGENTS.md` for harness layout, scenario shape, fixture patterns, normalization rules, the coverage threshold and scope, and stub-injection guidance.

## Release Rules

- **A release that exits 0 means the version is deployable, and a release that cannot publish fails before anything is public.** These two invariants set the stage order, and nothing may reorder around them:
  - The release stages split at the publish. Everything before it (`sync-remote`, `release` — the chart/packaging stamp, the commit, the *local* tag) is recoverable by re-running; everything after it (`push-release-tag`, `sync-packaging-checksums`, `post-release-version-bump`, `sync-develop`, `push`, the linux `release.sh` scripts that create the GitHub release) is outward-facing. A new stage goes in the group that matches its reversibility — never "wherever it happens to work".
  - The publish is not assumed to have landed. `verify-publication` re-resolves every published image's multi-arch manifest, and each chart is read back as it publishes; a version whose artifacts cannot be resolved never reaches its tag.
  - The version bump is a post-publish step for a reason. It moves `VERSION` past the version being released, so a bump ahead of the publish strands that version: a plain `erun build` then mints the *next* one, and `push --version <released>` cannot assemble a manifest from per-arch tags nothing built. Keeping the bump after the publish means a failed release leaves `VERSION` on the version it was releasing, and re-running retries the same version.
  - A release refuses to run when its resolved images are not all covered by a build it will publish. Announcing a version the registry never receives is the failure this exists to prevent; do not soften it into a warning.
  - **The base branch is not assumed to stand still.** `sync-remote` establishes fast-forwardability once, at the start; the release then re-reads the branch immediately before the build and refuses a move it can still see cheaply, and its final push rebases onto a branch that moved during the build and retries, bounded. Neither half may be dropped in favour of "do not merge while a release is in flight" — that is guidance for humans, and the tool must not depend on it being followed. A knowable blocker belongs before the spend, and a knowable recovery belongs where the alternative is a registry the repository disagrees with.
- Treat release work as repository-wide. When changing release behavior, validate `erun-common`, `erun-cli`, and `erun-mcp`, not just the module where the code change landed.
- When release, launcher, or desktop packaging behavior changes affect the desktop app, validate `erun-ui` too and keep package-manager metadata aligned with the desktop build outputs.
- Keep stable release automation responsible for all repository metadata that must move with the release, including versioned charts, package-manager metadata, and generated release references; when that metadata references GitHub archive assets, update both version fields and checksums instead of rewriting only URLs.
- Treat release-time Docker images as dependency graphs, not isolated targets. If a release image depends on local base images, publish those local dependencies before publishing the dependent image.
- Every Docker build — `erun build` and `erun build --release` — produces both `linux/amd64` and `linux/arm64`. There is no single-platform code path. A single-arch artifact built locally cannot be deployed to a cluster of a different architecture, and arch-specific Dockerfile bugs should fail at developer-machine build time, not at remote deploy time. `erun push` (including the push step `release` runs) assembles the multi-arch manifest list and publishes the runtime chart alongside the runtime image; `deploy` only installs a published version and never builds. Non-release `erun build` stops after the per-arch local builds.
- Multi-architecture builds must verify daemon capability explicitly. Fail with a direct error when the local Docker daemon cannot produce all required target platforms (e.g. binfmt is missing for the foreign arch), rather than letting the per-platform `docker build` fail with a confusing message.
- Keep the runtime deployment and release-build environment aligned. If the runtime pod performs release builds through dind, ensure the deployment installs the required binfmt or emulator support before the daemon is used for multi-arch builds.
- Prefer pinned versions for release-critical infrastructure images such as binfmt helpers, dind, and runtime base images so release behavior stays reproducible.
- When release automation pushes tags or branches mid-flow, add the follow-up verification needed for later steps. Do not assume remote state, package archives, or checksums are available without checking.
- Add regression tests for each release failure mode that was fixed. When a release bug is caused by ordering, missing metadata, or missing platform support, encode that contract in tests so the next release cannot regress silently.
- When a change affects generated runtime charts or embedded templates, test both the shared template source and the concrete runtime chart when practical. Treat them as one contract.

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

## Pull Request Titles

- Use a clean human title that describes the change directly.
- Do not add agent markers such as `[codex]` unless the repository explicitly asks for them.
- Prefer sentence-style titles such as `Add HTTP MCP server entrypoint`.

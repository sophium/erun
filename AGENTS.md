# AGENTS.md

Repository-wide guidance for humans and coding agents. Module-specific guidance
lives in each module's own `AGENTS.md`.

## Guidance Scope

- Read this file and every applicable child `AGENTS.md` end-to-end before
  reading, editing, running, or testing files in its subtree, on every task.
  Child guidance adds to this file unless it explicitly overrides it.
- When scope is unclear, find all `AGENTS.md` files and read those in or above
  the affected area. If a new module guide is added, list it below and provide
  a `CLAUDE.md` symlink beside it. Edit only `AGENTS.md`.
- In agent-facing requests, "documentation" means the applicable `AGENTS.md`
  files. `erun-docs/` is the separate public documentation site and follows its
  own guidance.
- Put shared repository conventions in the nearest applicable `AGENTS.md`, not
  in private assistant memory. Keep these files about engineering workflow, not
  product behavior or end-user reference material.
- Do not add documentation files or modify `README.md` unless explicitly asked.

When maintaining guidance, preserve each unique requirement in its owning guide.
Replace incident narratives and implementation inventories with the invariant,
its important exceptions, and a source/test reference. Distinguish implemented
contracts from proposals; do not silently discard unresolved decisions. Update
incoming references when moving a rule. Host-orchestrator policy belongs in its
provisioned instructions and `erun-orchestrate`, not this repository-wide guide;
operators may interact directly with in-pod agents without an orchestrator.

## Modules

- `erun-cli` — CLI (`erun`). See `erun-cli/AGENTS.md`.
- `erun-common` — shared transport-neutral Go logic. See
  `erun-common/AGENTS.md`.
- `erun-mcp` — MCP server (`emcp`). See `erun-mcp/AGENTS.md`.
- `erun-backend` — API and database modules. See `erun-backend/AGENTS.md`,
  `erun-backend/erun-backend-api/AGENTS.md`, and
  `erun-backend/erun-backend-db/AGENTS.md`.
- `erun-devops` — images, packaging, and Kubernetes assets. See
  `erun-devops/AGENTS.md`.
- `erun-kit` — shared frontend foundation. See `erun-kit/AGENTS.md`.
- `erun-ui` — Wails desktop app. See `erun-ui/AGENTS.md` and
  `erun-ui/playwright/AGENTS.md`.
- `erun-console` — hosted React SPA. See `erun-console/AGENTS.md` and
  `erun-console/playwright/AGENTS.md`.
- `erun-docs` — public Docusaurus site. See `erun-docs/AGENTS.md`.
- `erun-integration` — cross-module CLI integration tests. See
  `erun-integration/AGENTS.md`.
- `erun-skills` — canonical Codex and Claude skills. See
  `erun-skills/AGENTS.md`.

## Contributing

- Track work in `https://github.com/sophium/erun`: create or confirm the GitHub
  issue before implementation, branch from current `main`, implement and validate,
  then push the branch and open a PR targeting `main`.
- Keep related work in the same PR, including bugs or gaps found while working.
  Do not split it or ask to split unless the user requests it. Update the PR title
  and body to match the final scope; one PR may close multiple issues.
- Put `Closes #<issue-number>` in the PR body for each issue the merge should
  close. A branch push or open PR alone does not close an issue.
- Enable the repository's `.githooks` pre-commit hooks in each clone; keep the
  comment-reference and formatting checks active rather than bypassing them.
- After merge, return the local checkout to the PR's target branch, usually
  `main`. Never push to a merged branch again; trailing work needs a fresh branch
  from updated `main`. Before pushing an existing branch, verify its PR/review
  is still open.
- `push, accept` and `close` authorize the full publish flow: push, open the PR,
  squash-merge unless told otherwise, close linked issues, and return to the
  target branch. In this repository, `close` never means end or archive the
  conversation.
- If publishing lacks authentication, initiate a supported device-login flow,
  show the operator the verification URL and code, and resume after approval.
  Attempt that flow before reporting authentication as a blocker. Never ask for
  a pasted token, fabricate credentials, or scrape secrets.

### Branching Strategy

- Fast-forward local `main` to the current `origin/main` before cutting a branch.
  Use `feature/<issue-number>-<short-kebab-case-description>` for functionality
  and `bug/<issue-number>-<short-kebab-case-description>` for fixes.
- Do not base work on another open PR's head: squash merges replace its commit
  identities and can leave the dependent branch conflicting with already-landed
  work. Wait for the dependency to merge, then branch from updated `main`. If work
  cannot wait, rebase the dependent branch onto `main` as soon as the dependency
  lands, dropping commits already incorporated by the squash merge.

### Pull Request Titles

- Use a direct, sentence-style title describing the change, such as
  `Add HTTP MCP server entrypoint`. Omit agent markers such as `[codex]` unless
  explicitly required by the repository.

## Architecture And Module Boundaries

- Shared transport-neutral domain logic belongs in `erun-common`;
  transport-specific adaptation belongs in `erun-cli`, `erun-mcp`, or
  `erun-ui`. The CLI and MCP modules must not import each other or backend API
  packages directly.
- Keep `erun-common` independent of Cobra, MCP SDKs, and transport orchestration.
  New commands normally expose CLI and MCP transports over shared logic.

### Command primitives vs orchestration

- `build`, `push`, `deploy`, and `open` are pure primitives. Only `build` mints
  a version; `push` and `deploy` require one. Environment policy and multi-step
  orchestration belong to callers.
- Convenience orchestration switches are for interactive operators only.
  Programmatic callers compose primitives and pass the version explicitly.
- `push` publishes images and runtime charts; `deploy` only installs a
  published version. `release` is the contracted build/publish/tag
  orchestrator and must verify publication before exposing release metadata.

## Working Rules

- Before a non-trivial change, present the smallest coherent outcome, affected
  modules, user-visible result, documentation impact, and validation needed;
  obtain confirmation before multi-module or public-surface changes.
- Answer operator questions directly in the first sentence. A question is not
  authorization to act. Correct contradictions plainly before resuming work.
- Prefer evidence from reproduced behavior, tests, and visible results. Keep
  changes focused and solve cross-module problems at the lowest owning layer.
- Make destructive, remote, publishing, and shared-environment actions explicit
  before execution. Prefer reversible operations and concrete dry-run or
  preview output for imperative commands.
- New or materially changed action commands should support CLI `--dry-run` or
  an MCP preview path. Missing required MCP input must fail clearly.
- Preserve deterministic, repeatable behavior. Pin external and
  release-critical dependencies.
- Record successfully applied external state when it lands, even if a later
  step fails. Do not record an unpublished, locally minted version as deployed.
  Recovery must be able to identify what actually changed.
- Validate data at trust and transport boundaries before using it. Generated
  types are not runtime validation; normalize nullable collections before
  storing or iterating them. Preserve unknown state instead of guessing a
  definite outcome.
- Attribute failures using a clean, comparable baseline and an established
  cause. Identical failure counts can reproduce an environmental fault just as
  reliably as a product defect; counts alone do not establish causality.
- Fix every test, lint, formatting, or gate failure encountered. Do not skip,
  suppress, weaken, or dismiss failures as pre-existing or flaky. Defer only
  when a required design decision or genuinely separate large effort has been
  explicitly accepted and tracked.
- Once a body of work is authorized, carry it through completion without
  seeking approval between routine increments. Stop only for genuine blockers
  or scope-expanding decisions.

## Smooth, Seamless, No Dead Ends

- A user-triggered path is complete only when the user can see progress, know
  the outcome, recover from failure, and continue without understanding
  subsystem boundaries.
- Every reachable error or empty state must provide an accurate next action.
  Distinguish causes before writing remedies; never prescribe an unchecked fix.
- Successful actions must refresh the affected surface. Do not require users to
  re-enter known values, refresh manually, or leave a GUI for capabilities the
  GUI should expose.
- Treat onboarding and administrator handoffs as product flows: identify who
  must act, what they must do, and provide exact copyable values.
- For desktop changes, perform the impact review required by
  `erun-ui/AGENTS.md`.

## Documentation

- Feature and behavior changes include public documentation in the same plan
  and PR. Name affected `erun-docs` pages, audience, and validation. State why
  no docs change is needed for behavior-preserving fixes or refactors.

## End-to-End Verification Gate

- Verify user-reported bugs and deployed behavior end-to-end in the real target:
  deploy the changed artifact, repeat the original flow from the same vantage
  point, report anything that could not be exercised, and remove probe artifacts.

## Integration Test Gate

- Run the validation required by every affected module. Refactors of shared
  code or moves across module boundaries validate `erun-common`, `erun-cli`, and
  `erun-mcp`, including each module's tests and configured lint checks. Include
  `erun-ui` when desktop wiring, consumed shared code, package-manager integration,
  or launcher integration changes.
- Run `make fast-check` before every push. It does not replace the full gate.
- Keep `make integration-test` green for changes affecting CLI/common behavior,
  runtime entrypoints, chart deployment, or integration goldens. Never skip a
  scenario, and preserve complete `--dry-run` traces.
- Run the full `make check` gate before merging. This repository uses its own
  build and merge queue; do not add GitHub Actions for build or test gating.
- Structural tests that read sibling modules, directory trees, or generated
  artifacts outside their compiled inputs must run uncached (`go test -count=1`)
  in their module's gate. Add that wiring with the test; a cached pass cannot
  establish that changed external inputs were checked.
- Guidance-only changes require consistency, ownership, reference, and formatting
  checks. They do not require deploying or rebuilding unchanged app behavior;
  changes to embedded instruction templates also run their provisioning tests.

## Release Rules

- A successful release means every versioned artifact is deployable. Refuse
  incomplete builds and verify images and charts from their published source
  before pushing the release tag or other outward-facing metadata.
- Keep recoverable local preparation before publication and outward-facing
  changes after verification. Version bumps occur only after publication so a
  failed run can retry the same version.
- Account for the base branch moving during a release: check before expensive
  work and reconcile safely before the final push.
- Release builds cover `linux/amd64` and `linux/arm64`; non-release builds may
  narrow platforms explicitly. Verify daemon support before multi-architecture
  work and publish local base images before dependent images.
- Treat release changes as repository-wide. Validate `erun-common`, `erun-cli`,
  and `erun-mcp`, plus desktop packaging and runtime chart contracts when
  affected. Add regression coverage for every fixed release failure mode.
- Keep packaging versions and archive checksums consistent with published
  artifacts. Chart changes validate both shared templates and concrete tenant
  charts, not just one side of the contract.

## Code Comments

- Comments explain non-obvious intent, not mechanics. Keep them terse, remove
  stale comments, and do not add issue IDs or issue-tracker URLs to source
  comments. Remove such references from comments you edit while retaining the
  explanation; neighboring violations are not precedent. Provenance belongs in
  git history and PR descriptions.

## Refactoring Rules

- Preserve behavior unless the user explicitly requests a functional change:
  output, help and error text, prompts, logging, defaults, and flags stay stable.
  For transport-facing code, also preserve method names, argument and return
  shapes, event names, and generated-binding contracts.
- Before moving code, identify its owner, public callers that must stay stable,
  and the smallest validation set that proves behavior is preserved. Compare
  observable behavior with `main` before and after; add or update regression
  coverage for the contracts that must remain unchanged.
- Move one coherent responsibility at a time and validate after meaningful
  slices. Keep organizational moves within the same package or module unless
  changing that boundary is part of the intended design.
- Preserve dependency direction: shared logic may move into `erun-common`;
  transport-specific logic must not. Keep moved symbols package-private unless
  an existing external caller requires them to be exported.
- Reuse matching shared interfaces from `erun-common` directly rather than
  duplicating them locally. Move meaningful coverage to the module that now owns
  the behavior and remove test-only production shims where possible.
- Explicitly search for and remove unused code after the move, including dead
  wrappers, compatibility helpers, transport glue, and rename layers. Tests
  referencing an obsolete helper are not a reason to keep it; retain a wrapper
  only when it still provides real behavior.
- Complete the affected-module validation under "Integration Test Gate" above.

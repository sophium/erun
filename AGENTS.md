# AGENTS.md

Repository guidance for humans and coding agents working in this repo.

- Follow this file for the whole repository.
- **Always read every applicable `AGENTS.md` before touching files in its subtree, on every task, even when you have read it before in another conversation or earlier in this one.** "Applicable" means this root file plus the `AGENTS.md` of every directory that is an ancestor of, or contains, the files you are about to read, edit, run, or test. Read each one end-to-end and apply its guidance to the work; do not rely on memory or summaries.
- When in doubt, list `AGENTS.md` files with `find . -name AGENTS.md -not -path '*/node_modules/*'` and read each one whose path is a prefix of, or descendant of, the area you are about to change. Skipping this step has caused real bugs (e.g. shell-syntax assumptions that broke zsh on macOS).
- Current `AGENTS.md` files in this repo:
  - `AGENTS.md` (this file) — repository-wide rules.
  - `erun-ui/AGENTS.md` — desktop-module guidance, including macOS + Windows targets and Wails frontend rules.
  - `erun-ui/playwright/AGENTS.md` — Playwright end-to-end UI test suite that drives `erun-app --headless`.
  - `erun-devops/AGENTS.md` — runtime-image, chart, build-cache, and release-workflow guidance.
  - `erun-docs/AGENTS.md` — product documentation site (Docusaurus) and the Cloudflare Pages deploy contract.
  - `erun-integration/AGENTS.md` — integration-test harness layout, scenario shape, and the coverage gate.
  - `erun-backend/AGENTS.md` plus `erun-backend/erun-backend-api/AGENTS.md` and `erun-backend/erun-backend-db/AGENTS.md` — hosted-backend, API, and Atlas-migration guidance.
  - `erun-skills/AGENTS.md` — canonical skill source for the Claude Code / Codex SKILL.md files plus the plugin manifest. Consumed by both the runtime image (vendored into `/etc/erun/skills/`) and the Claude Code marketplace at the repo root.
- A child `AGENTS.md` is additional, not a replacement: parent rules still apply unless the child explicitly overrides them.
- If you add a new `AGENTS.md` anywhere in the tree, also list it in the bullet above so future readers find it without searching.

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
- Publishing needs an authenticated `gh`. If `gh auth status` reports no host, do not stop and hand the publish flow back to the user. Drive the GitHub OAuth device flow yourself: `POST https://github.com/login/device/code` with the GitHub CLI client id `178c6fc778ccc68e1d6a` and scope `repo read:org gist workflow`, surface the returned `user_code` together with `https://github.com/login/device`, then poll `POST https://github.com/login/oauth/access_token` (grant `urn:ietf:params:oauth:grant-type:device_code`) until it returns an `access_token`. Store it with `gh auth login --with-token`, then continue the publish flow without further user input.
- The device-code entry in the user's browser is the only auth step that requires the user, and it requires only that — not a pasted token, and not an interactive `gh auth login` in a TTY. `gh auth login` (including `--web`) hangs in this harness because it has no controlling terminal; the curl-driven device flow does not, so prefer it.
- Never fabricate a token, scrape application secrets for one, or report the publish flow as blocked on authentication before attempting the device flow.
- Stay within the current PR for the whole body of related work. When additional bugs, gaps, or improvements surface while working on a branch, add them to the same PR rather than filing a separate issue or opening a new branch. Do not propose splitting work into multiple PRs, and do not ask whether to split — assume the answer is "no" unless the user explicitly says otherwise. Update the PR title and body to reflect the broader scope when the diff grows.
- One body of work, one PR. The PR may link to multiple issues (`Closes #A` / `Closes #B`), but the unit of review is the PR, not the issue.

## Project Structure

- `erun-cli` - CLI utility
- `erun-common` - shared common module
- `erun-mcp` - MCP server module
- `erun-backend` - backend service area containing the API and database migration modules
- `erun-devops` - runtime Docker images, Linux packaging, and Kubernetes chart assets used by build, open, deploy, and release flows
- `erun-ui` - desktop app module built with Wails, using a Go backend and a TypeScript/Yarn frontend
- `erun-docs` - public product documentation site (Docusaurus 3.x), published to Cloudflare Pages via a k8s Job under `erun-devops/k8s/erun-docs/`
- `erun-integration` - cross-module integration test harness; runs the compiled `erun` binary with `--dry-run` against per-command goldens and gates merged coverage
- `erun-skills` - canonical source for Claude Code / Codex SKILL.md files and the Claude Code plugin manifest; consumed by both the runtime image (in-pod install) and the marketplace at the repo root (laptop install)

## Module Boundaries

- Keep CLI-private implementation in `erun-cli/internal`.
- Treat `internal` as a deliberate module boundary, not a staging area for future shared code.
- Move code into `erun-common` only when it is genuinely shared across modules and remains transport-agnostic.
- Do not move code into `erun-common` just because it is reused once; prefer a specific shared package only when a stable cross-module abstraction exists.
- Keep `erun-common` small and focused on reusable core types and logic, not module-specific orchestration.
- `erun-cli` and `erun-mcp` must not import each other.
- `erun-cli` may depend on `erun-common`, but its `mcp` command is only a launcher for the `emcp` executable and must not embed MCP server logic.
- `erun-ui` is an additive desktop transport for the same solution, not a replacement for shared domain logic. Keep shared tenant and environment resolution in `erun-common`, not in the desktop module.
- `erun-ui` owns desktop-specific concerns: Wails startup, native window integration, frontend assets, PTY-backed terminal sessions, and package-manager-facing desktop build outputs.
- `erun-ui` may depend on `erun-common`, and it may launch the installed `erun` executable as a child process for interactive terminal sessions, but it must not import `erun-cli` packages.
- `erun-mcp` owns MCP transport concerns: server startup, HTTP handler wiring, SDK integration, tool registration, and the `cmd/emcp` executable.
- Keep MCP-specific configuration, flag parsing, and transport wiring in `erun-mcp`, not in `erun-cli` or `erun-common`.
- `erun-backend-api` owns hosted backend HTTP API concerns: request authentication, tenant resolution from OIDC claims, API routing, and server-side transport contracts.
- `erun-backend-db` owns backend database schema and migration concerns. Manage schema changes through Atlas migrations in that module instead of embedding schema setup in API startup code.
- `erun-cli` and `erun-mcp` should reach backend functionality through transport-neutral clients and contracts in `erun-common`, not by importing backend API packages directly.
- Keep `erun-common` usable as a standalone library for third parties. Shared code placed there must be transport-agnostic and should not depend on Cobra, the MCP SDK, or module-specific orchestration.
- When sharing operation contracts across modules, prefer transport-neutral names such as plan, request, result, or input/output. Do not put MCP-only wrapper types in `erun-common` unless they are intentionally generic library contracts.
- Prefer reusing a shared struct over creating a transport-local duplicate with the same shape. When one shared struct is the canonical contract for both CLI and MCP, transport-specific annotations such as `json` tags are acceptable in `erun-common` to avoid structure duplication.
- By default, new commands should be implemented in both transports: CLI and MCP. Treat a command as shared work unless there is a clear repository-specific reason for it to exist in only one transport.

## Preferred Direction

- Prioritize maintainability and clarity over performance optimizations by default.
- Prefer established repository patterns over introducing new command, config, testing, or documentation styles. Extend the existing shape first and only add a new pattern when the current one is clearly inadequate.
- Organize shared command logic by command name when practical. If `build`, `open`, `init`, or `deploy` is shared, prefer files and types that mirror that command shape across `erun-common`, `erun-cli/cmd`, and `erun-mcp`.
- Add new code directly to the file or module that owns the behavior. Do not use a large file, facade, or transport entrypoint as a temporary staging area.
- Keep files organized around cohesive responsibilities: contracts, planning, execution, discovery, formatting, persistence, and transport adaptation should not be mixed just because they belong to the same command.
- When a command has multiple responsibilities, split by stable behavior boundaries rather than by incidental implementation details.
- Keep public entrypoints thin. They should adapt inputs, call focused logic, and render or return results instead of accumulating domain behavior.
- Treat large source files as a signal to clarify ownership, not as a goal to reduce line counts mechanically.
- Move related code together only when it forms a stable responsibility with a clear name and a clear caller. Do not create temporary holding files or vague utility buckets.
- Preserve public behavior during organization work. Keep output text, defaults, flags, JSON shapes, errors, ordering, and side effects unchanged unless the user explicitly asks for a behavior change.
- Prefer moving complete contracts, workflow steps, or pure helpers over moving isolated lines. A moved unit should be understandable without reading the old large file first.
- Keep boundary files as facades only when they are real composition or transport boundaries. A facade should wire dependencies, enforce the public contract, and delegate to focused owners.
- Put behavior beside the state it owns. If a workflow owns busy flags, request state, retries, timers, or persistence, keep the state transitions in that workflow rather than scattering them across callers.
- Separate composition from operations. Files that construct applications, commands, transports, or runtimes should not also own read models, config mutation, process/session lifecycle, or domain workflows.
- Keep transport contracts separate from workflow execution. JSON, CLI, or MCP-facing contract types may live together, but should not be mixed with long-running operations, process management, or domain conversion logic.
- Keep read-model assembly separate from state mutation. Listing, status aggregation, version suggestions, and display conversion should not be mixed with save/delete/start/stop workflows.
- Keep helper modules behavior-specific and dependency-light. Prefer pure helpers for normalization, formatting, classification, selection, and ordering.
- After moving code, remove obsolete wrappers, stale comments, unused helpers, and test-only production shims.
- Keep CLI and MCP layers thin. Flags, prompts, terminal rendering, MCP schemas, and transport setup belong in the transport modules; shared planning and execution logic belongs in `erun-common`.
- Do not make one transport invoke the other for shared behavior. If CLI and MCP need the same operation logic, extract it into `erun-common` so third parties can use it directly as a library.
- Keep trace and preview policy shared, but keep rendering transport-specific. `erun-common` may own plans, command specs, and feedback rules; CLI owns terminal trace formatting and MCP owns structured tool output.
- When the same status or resolved-plan data must be shown in both CLI and MCP, extract the transport-neutral result assembly into `erun-common`. Let CLI format it for humans and MCP return it as structured output.
- Prefer immutable value-style inputs and resolved plans over mutating shared state in place.
- Prefer explicit runtime structs over package globals.
- Keep mutable state local to one CLI execution or one MCP tool invocation.
- Default to local execution and local integrations. Any remote or hosted transport should be additive, not the baseline behavior.
- Prefer dependency injection in tests instead of replacing globals.
- Prefer pure functions with no side effects for core logic.
- Keep config and domain types simple and easy to copy safely.
- Keep business logic reusable so the CLI and MCP layers can share it.
- Design MCP-facing handlers as non-interactive operations with explicit inputs and structured outputs.
- Keep tenant DevOps runtime scaffolding shared. When `init` creates project-local runtime assets, prefer generating the tenant-specific `<tenant>-devops` module from shared templates in `erun-common` so CLI and MCP flows stay aligned.
- Assume tenant-specific DevOps modules use the shared `erun` runtime image as their base. Prefer thin tenant wrappers that extend the canonical runtime image over duplicating Dockerfiles, entrypoints, prompt scripts, or tool installation logic per tenant module.
- Keep generated runtime asset identity explicit. Prefer rendering stable, intentional names into generated assets over deriving runtime identity indirectly from release metadata when the generated module already knows what it is.
- Treat runtime startup code and deployment templates as one contract. If runtime initialization depends on specific context values, pass them explicitly through deployment inputs instead of relying on ambient process state or cwd detection inside the container.
- Keep transport entrypoints responsible for wiring required runtime initialization values into shared deployment plans. Deployment templates should declare required startup inputs, and shared execution should pass them concretely so the same contract holds across CLI and MCP flows.

## Dependency Wiring

- Apply KISS to dependency wiring. Do not introduce abstractions or injection layers unless they solve an immediate problem in the current code.
- Do not pass a dependency into a function unless that function actually uses it in its own body. Passing it through to another function does not count as usage.
- Prefer wiring concrete dependencies at the boundary and then passing only the specific values needed by the next function.
- If a function only needs already-built subcommands, handlers, or services, pass those directly instead of the larger set of dependencies used to construct them.
- Prefer direct use of an existing concrete function such as `common.FindProjectRoot` when it is only needed once. Do not create a local alias just to forward it.
- If a dependency value is used multiple times in the same function, binding it to a local is acceptable when that improves readability.
- Keep default wiring local to the real composition boundary, usually `Execute()` or the transport entrypoint, rather than spreading default-resolution helpers throughout production code.
- In CLI command constructors, keep inline `RunE` closures thin. Use them for Cobra argument adaptation and flag binding, but move real command/application logic into named package functions.
- If a command already has meaningful application logic such as resolving shared results and rendering them, prefer a named `run...Command` or equivalent helper over leaving that logic inline in the Cobra definition.
- Test-only convenience wiring helpers are acceptable, but keep them in `_test.go` files and name them clearly as test helpers so they do not read like production APIs.

## Visibility

- Default functions, types, and variables to package-private. Export only when they are actually used outside the package today.
- Do not keep functions exported only for tests in the same package. Lower them and let same-package tests call them directly.
- When refactoring removes the last external use of an exported symbol, lower it unless there is a clear current external caller that still needs it.

## Naming

- Do not use a `Service` suffix in local variable names when a more direct noun exists. Prefer names such as `deployer`, `builder`, `opener`, or `bootstrapper` over names such as `deployService`.
- Use `Service` in type names only when the abstraction is genuinely a stable service concept in the domain. Do not add the suffix by default.
- Do not call small input structs `Request` when they are just direct function inputs with a small number of fields.
- For function input structs with fewer than 5 top-level fields, prefer a `Params` suffix over `Request`.
- Reserve `Request` and `Response` naming for transport-facing contracts or shapes that are meaningfully request/response objects rather than simple local parameters.

## Go Safety Notes

- Go is memory-safe by default, but practical failures still come from shared mutable state, data races, resource leaks, and `unsafe`.
- Copying is a good default only for plain value data. Slices, maps, pointers, channels, and structs containing them still share underlying state unless explicitly cloned.
- Favor clear ownership over incidental sharing. If callers must not mutate returned data, return a copy.

## Working Rules

- Start each non-trivial change by identifying the smallest coherent outcome that would satisfy the request, the modules likely affected, and the validation scope needed for confidence.
- When asking the user to confirm a plan, show a plan only for the intended change, not for discovery by itself. Describe the actual changes to make: the behavior that will change, the user-visible outcome, the likely files or modules to edit, and the validation that will prove the change. Keep investigation or discovery steps out of the plan unless they materially affect the proposed implementation.
- Prefer fast, evidence-driven iteration. Use existing behavior, failing symptoms, tests, and screenshots as the source of truth, then tighten the implementation around the observed problem.
- Keep work centered on the current user goal. Avoid opportunistic cleanup, broad redesign, or unrelated polish unless it directly reduces risk for the requested change.
- When a problem crosses module boundaries, solve it at the lowest shared layer that owns the behavior, then keep transport-specific code focused on adaptation and presentation.
- Make high-impact behavior explicit before executing it. Favor plans, previews, confirmations, and reversible steps when work can delete data, mutate remote systems, publish artifacts, or affect shared environments.
- Preserve momentum by choosing the simplest defensible design that fits the repository. Add structure only when it clarifies ownership, reduces repeated logic, or prevents a real class of mistakes.
- Treat repeated user corrections as signal that the interaction model is wrong, not just the implementation detail. Revisit the flow and simplify it around what the user is trying to accomplish.
- Treat every change that affects a user-triggered code path as a UX-affecting change, including backend wiring, lifecycle refactors, event-handler edits, persistence work, and frontend logic that does not directly edit a component. Before considering such a change complete, walk through the user-facing sequence it produces and verify the user can see what is happening, recover from what fails, and confirm what succeeded. Setting state without a corresponding visible affordance, or surfacing a status that is cleared by the next lifecycle step before the user can register it, both count as gaps that block the change. For desktop work, follow the impact-review checklist in `erun-ui/AGENTS.md` § "UX Impact Review Checklist".
- Treat every feature addition or feature-changing PR as a docs-affecting change, and include the `erun-docs` plan in the same approval step as the rest of the change. The plan must name each affected page, identify its audience (Operator-facing vs Agent reference — see `erun-docs/AGENTS.md` § "Audience: Operator vs Agent" and § "Companion pages") and the validation (`cd erun-docs && yarn build` clean, anchor and canonical-terminology sweeps). If no docs update is needed, the plan must say so with a one-line reason. Undocumented behaviour is not part of the contract (see `erun-docs/AGENTS.md` § "Spec discipline"), so missing docs are a defect, not optional follow-up — they land in the same PR as the feature, never as a separate "docs PR." Pure bug fixes that preserve behaviour, refactors with no public-surface change, and internal cleanup are exempt; when unsure whether a change qualifies as exempt, treat it as feature work and include the doc plan.
- Avoid duplicating investigation. Once a cause is established, update the relevant shared guidance, tests, or abstractions so future work can start from that knowledge.
- Treat execution state as scoped to one CLI run or one MCP request, not shared process state.
- Avoid adding new package-level mutable variables.
- Keep side effects at the boundaries: CLI I/O, MCP transport, filesystem, network, and process execution.
- For high-impact operations, prefer designs that can expose an explicit resolved plan, support dry-run execution, and emit traceable command details.
- New or materially changed action-oriented CLI commands should support `--dry-run` by default unless there is a strong reason they cannot. Dry-run should resolve the intended work and print the concrete actions that would execute, without performing side effects.
- Do not treat summary notes as a sufficient dry-run for imperative operations when the real execution plan can be shown. Prefer the actual commands, file writes, or concrete operation steps, with secrets redacted only where necessary.
- Action-oriented MCP endpoints should likewise provide a preview or plan path so callers can inspect the resolved work before execution. Preview behavior should avoid side effects and return the concrete actions that would run.
- Keep external dependencies pinned and explicit. Make dependency changes easy to review and avoid hidden runtime coupling where practical.
- When optimizing Dockerfiles, prefer simple, reviewable layer ordering and cache boundaries over clever or fragile build tricks.
- Keep tests isolated and do not add `t.Parallel()` around code that mutates globals.
- CLI prompts are acceptable in interactive flows, but MCP-exposed paths should receive all required input explicitly and fail clearly when input is missing.
- Prefer deterministic command behavior so tool calls are safe to run repeatedly and concurrently.
- Prefer safety and clarity over micro-optimizations.
- For documentation-only guidance changes, do not change app behavior. Update the nearest applicable `AGENTS.md` and validate by reviewing the edited guidance for consistency.
- Do not add new documentation files unless the user explicitly asks for them; add repository instructions to `AGENTS.md` instead.
- Keep `AGENTS.md` focused on repository workflow and engineering guidance; do not document app behavior, command semantics, or end-user functionality in it.
- Do not modify `README.md` unless the user explicitly asks for a README change.
- **"Documentation" in agent-facing instructions means `AGENTS.md`** (this file plus every applicable subtree `AGENTS.md`). When the user says "read documentation", re-read the relevant `AGENTS.md` files end-to-end; do not substitute `erun-docs/` for that step. When the user says "update documentation", edit the nearest applicable `AGENTS.md`. `erun-docs/` is the public product documentation site — a separate concern with its own update workflow, owned by the `erun-docs/AGENTS.md` rules. Read it only when its content is the load-bearing reference for an investigation; do not treat it as the source of repo workflow or engineering guidance.
- Each `AGENTS.md` directory ships a `CLAUDE.md` symlink pointing to its `AGENTS.md` so Claude Code auto-loads the local guidance when launched from any subtree. Treat both names as the same file; only edit `AGENTS.md` directly.

## Diagnosing A Deployed Runtime Via MCP

When investigating what is happening inside a deployed runtime pod (in-pod config files, log files, env vars, process state), prefer the per-environment `erun-mcp` endpoint over asking the user to SSH in. The desktop keeps a local port-forward open to each remote env's `erun-mcp` container for as long as that env is open; talking JSON-RPC to that port is the fastest way to gather on-pod evidence without a context switch.

- Find the local port at `<UserConfigDir>/erun/portforward/mcp/<tenant>/<environment>.json` — `localPort` is the port to call. `UserConfigDir` follows Go's `os.UserConfigDir`: `~/Library/Application Support` on macOS, `$XDG_CONFIG_HOME` or `~/.config` on Linux, `%AppData%` on Windows. If the JSON file is missing, the env is not open in the desktop and there is no endpoint to query; ask the user to open it first.
- Speak JSON-RPC 2.0 over `POST http://127.0.0.1:<port>/mcp` with `Accept: application/json, text/event-stream`. Send `initialize` first, capture the `Mcp-Session-Id` response header, send a `notifications/initialized` POST carrying that header, then call `tools/list` (for discovery) or `tools/call`. The session id must be on every subsequent request.
- Prefer the structured tools when they cover the question: `idle` returns the resolved `policy`, `managedCloud`, `stopEligible`, `blockedReason`, `markers`, and `activity` snapshot without recording activity; `doctor`, `list`, and `version` are similarly structured. Use `raw` only for state these tools do not expose.
- `raw` runs an arbitrary `argv` from the runtime repo root and returns `{stdout, stderr, executed, workingDirectory, trace}`. Pass `command` as an `argv` array, not a shell string; reach for `["sh","-c","…"]` only when you need shell features. Typical inspections: `cat ~/.config/erun/<tenant>/<env>/config.yaml`, `env | grep ^ERUN_`, `tail ~/.erun/<tenant>/<env>/idle-monitor.log`, `ls -al`, `ps auxf`.
- Scope: `raw` executes inside the `erun-mcp` container, which receives only the env vars the chart wires for that container — the `ERUN_CLOUD_*` set lives on the sibling `erun-devops` container. To inspect devops-container state, call `kubectl exec -n <namespace> <pod> -c erun-devops -- …` through `raw` (the MCP container has in-cluster RBAC to its own namespace). Both containers share the `/home/erun` PVC, so files under `~/`, `~/.config/erun`, and `~/.erun` are visible from either side.
- Treat this endpoint as a diagnostic shortcut, not a substitute for tests. If a code path is reachable from a `--dry-run` trace or a `go test` subprocess, the test belongs there. Use MCP when the question is "what does the running pod actually have on disk or in memory right now?".

### Verifying in-pod fixes before re-running the user-visible flow

When iterating on plumbing that lives inside the runtime pod — contribute-mode toolchain, an updated runtime image, a clone freshness fix, a new env var the chart was supposed to wire — confirm the pod state via MCP **before** asking the user to rebuild the desktop binary, click a launcher, or trigger a redeploy. The desktop-side cycle is slow (re-build erun-app, re-open env, re-click); the MCP probe is one HTTP round-trip and tells you whether the fix is even in the pod yet. Skip the round trip if the pod already has the expected state but the user-facing flow still fails — that points the investigation at the desktop or at the chart, not at the runtime image.

Useful contribute-mode probes (substitute `<port>` from the JSON state file and `<session>` from `Mcp-Session-Id`):

```sh
# 1. Webkit + libsoup dev libraries the Wails build needs (webkit2_41 path).
curl -s -X POST http://127.0.0.1:<port>/mcp \
  -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
  -H "Mcp-Session-Id: <session>" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"raw","arguments":{"command":["sh","-c","pkg-config --modversion webkit2gtk-4.1 libsoup-3.0"]}}}'
# Both versions print → runtime image is current. "Package not found" → rebuild + redeploy.

# 2. Yarn at the pinned version.
... "command":["yarn","--version"] ...
# 1.22.22 → ok. Empty / not found → runtime image is stale.

# 3. Contribute clone HEAD (does the pod have the latest fix the user is testing?).
... "command":["git","-C","/home/erun/git/erun","log","-1","--pretty=%h %s"] ...
# Compare against the branch HEAD on github.com. Behind → the in-tab fix is in the source you
# pushed, but the pod hasn't pulled yet. Send `cd ~/git/erun && git pull` to the contribute
# ERun tab (or via raw with sh -c).

# 4. Is the headless contribute-app process actually running?
... "command":["sh","-c","pgrep -fa 'erun-app --headless' || echo not running"] ...

# 5. Is the contribute-app port bound inside the pod?
... "command":["sh","-c","ss -tlnp | grep :17550 || echo port not bound"] ...
```

When the user reports "still broken after your fix": run these probes first, then act on the answer. Two common patterns:

- Pod toolchain is missing the package my fix expected → I shipped the Dockerfile change but the user hasn't rebuilt + redeployed the runtime image yet. Action: ask the user to run the image rebuild + deploy, or build it on their behalf if authorized.
- Clone HEAD is behind → I shipped a `build.sh` / `run.sh` fix but the pod's `~/git/erun` still has the old script. The `erun contribute clone` command always lands on the repository's default branch; while iterating on an unmerged feature branch the clone needs to be advanced manually. Action: `raw command=["sh","-c","cd /home/erun/git/erun && git fetch origin && git checkout <branch> && git pull --ff-only"]`, then ask the user to retry the click. After the PR is merged, a `git pull` on main is enough.

If the probes all show the expected state but the user-visible click still fails, the bug is in the desktop layer or in how the desktop talks to the pod (port-forward, browser open, etc.), not in the in-pod fix.

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
- This repository intentionally has no hosted CI (#521): GitHub-hosted runners are ephemeral, which defeats erun's daemon-centric build caching, and erun will provide its own build system and merge queue. Until that lands, the gate is enforced where it always was — run it locally before merging, and every erun-driven build anywhere re-runs it via the image's test stage (see `erun-devops/AGENTS.md` § Build Workflow). Do not reintroduce GitHub Actions workflows for build or test gating.
- Run `go test ./...` (or `make integration-test`) under `erun-integration/` before pushing any change that touches `erun-cli`, `erun-common`, the runtime entrypoint, the chart deploy plumbing, or any integration golden. If a scenario is red on your machine but you believe it is "platform-dependent" or "flaky", that is a defect to fix (see the host-OS pinning guidance in `erun-integration/AGENTS.md`), not a reason to ship.
- Any change touching `erun-cli`, `erun-common`, the runtime entrypoint, or the chart deploy plumbing must keep `make integration-test` passing. The target builds the `erun` binary with coverage instrumentation, runs the suite under `erun-integration/`, merges counters, and fails when total statement coverage of `erun-cli` + `erun-common` drops below the configured threshold (`COVERAGE_THRESHOLD`; the default is pinned in `erun-integration/scripts/integration-test.sh` and is raised in lockstep with coverage-raising changes).
- The integration suite runs the compiled binary as a subprocess against per-command `--dry-run` goldens. The contract for `--dry-run` is therefore a hard public-surface boundary: every action and every decision the command would take must appear as a trace line, regardless of whether downstream input resolution succeeds. Treat a missing trace as a bug, not a documentation gap.
- Integration scenarios default to `--dry-run`. `erun` is meant to be fully auditable: if a plan or decision cannot be proven correct from a dry-run trace, that is a defect in the dry-run contract, not a license to bring in stubs. Stub binaries (`ERUN_<NAME>_BIN`, scripted `kubectl`/`helm`/`docker`/`git`/`aws` replacements) are legitimate only in the two patterns `erun-integration/AGENTS.md` § "Stubbing rules" defines — deterministic decision input for a dry-run branch, and real-run scenarios for code whose behavior only exists outside dry-run (subprocess launcher bodies, retry loops, persistence, prompt handling). Reaching for a stub to paper over a missing trace stays forbidden: add the trace, gate the side effect behind `if !ctx.DryRun`, then write the scenario against the dry-run output.
- When you add or change a command, add or update integration scenarios in `erun-integration/` for every flag combination that influences the resolved plan. Use `UPDATE_GOLDEN=1 go test ./erun-integration/...` to regenerate `testdata/<command>/<scenario>.txt`, then re-run without the flag to lock the snapshot in.
- Do not skip integration scenarios to make the suite green. If a regression is discovered, leave the failing scenario in place so the gate fails until the regression is fixed; that is the suite working as designed.
- Coverage is gated on `erun-cli` + `erun-common` only. Other modules (`erun-mcp`, `erun-backend`, `erun-ui`) are validated by their own per-module test suites and do not count toward this gate. To extend coverage scope, edit `erun-integration/internal/erun.CoverPkgs` and the script's threshold logic together.
- Test `erun-cli` and `erun-common` behavior with integration scenarios. The integration suite is the single source of truth for coverage on those modules: it exercises the compiled binary end-to-end and is the only signal the gate enforces. Unit tests in those packages do not contribute to the gate, so any coverage they appear to provide is invisible at merge time.
- When you add or change behavior in `erun-cli` or `erun-common`, write the integration scenario. If a code branch looks unreachable from the binary subprocess (e.g. an error wrapped opaquely, a pure parser with no production caller), the defect is almost always in the production path — wrap the error, expose the trace, or remove the dead branch — not in the test strategy. Fix the production code so the branch becomes reachable, then write the scenario.
- When a unit test in `erun-cli` or `erun-common` overlaps with an existing integration scenario, delete the unit test. Keeping both forks the source of truth, hides drift, and inflates per-module coverage numbers without moving the gate.
- See `erun-integration/AGENTS.md` for harness layout, scenario shape, fixture patterns, normalization rules, and stub-injection guidance.

## Release Rules

- Treat release work as repository-wide. When changing release behavior, validate `erun-common`, `erun-cli`, and `erun-mcp`, not just the module where the code change landed.
- When release, launcher, or desktop packaging behavior changes affect the desktop app, validate `erun-ui` too and keep package-manager metadata aligned with the desktop build outputs.
- Keep stable release automation responsible for all repository metadata that must move with the release, including versioned charts, package-manager metadata, and generated release references; when that metadata references GitHub archive assets, update both version fields and checksums instead of rewriting only URLs.
- Treat release-time Docker images as dependency graphs, not isolated targets. If a release image depends on local base images, publish those local dependencies before publishing the dependent image.
- Every Docker build — `erun build`, `erun build --release`, and `erun deploy` — produces both `linux/amd64` and `linux/arm64`. There is no single-platform code path. A single-arch artifact built locally cannot be deployed to a cluster of a different architecture, and arch-specific Dockerfile bugs should fail at developer-machine build time, not at remote deploy time. Release-tagged builds additionally push and assemble the multi-arch manifest list; non-release `erun build` stops after the per-arch local builds.
- Multi-architecture builds must verify daemon capability explicitly. Fail with a direct error when the local Docker daemon cannot produce all required target platforms (e.g. binfmt is missing for the foreign arch), rather than letting the per-platform `docker build` fail with a confusing message.
- Keep the runtime deployment and release-build environment aligned. If the runtime pod performs release builds through dind, ensure the deployment installs the required binfmt or emulator support before the daemon is used for multi-arch builds.
- Prefer pinned versions for release-critical infrastructure images such as binfmt helpers, dind, and runtime base images so release behavior stays reproducible.
- When release automation pushes tags or branches mid-flow, add the follow-up verification needed for later steps. Do not assume remote state, package archives, or checksums are available without checking.
- Add regression tests for each release failure mode that was fixed. When a release bug is caused by ordering, missing metadata, or missing platform support, encode that contract in tests so the next release cannot regress silently.
- When a change affects generated runtime charts or embedded templates, test both the shared template source and the concrete runtime chart when practical. Treat them as one contract.

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

## CLI Help And MCP Tool Descriptions

`erun --help` (every Cobra command's `Short:` / `Long:` / `Example:` and every flag usage string) and the MCP server's tool `Description:` + parameter `jsonschema:"…"` strings are the only place a reader — human or LLM — can learn how to use a command and what it will do before running it. Treat them as part of the product's public surface, not as cosmetic strings.

Quality bar:

- Help is useful, not exhaustive. A reader should come away knowing what the command is for, when to reach for it, and — where it matters — where it sits in the larger workflow. Conveying that context is the job; enumerating internals is not. Detail that does not change how someone uses the command (every file written, every intermediate git/helm/kubectl call) is noise — leave it out.
- Orient the reader in the flow when a command is part of one. erun's release/deploy commands form a pipeline — build → release → push → deploy. Good help says where a command sits in that flow and what its convenience flags fold in (e.g. `build --release` resolves and stamps the release version before building; `build --deploy` pushes the result and rolls it out — so one command runs steps you would otherwise run by hand), not the git tags or helm upgrades underneath.
- Every command must be help-discoverable. `erun <cmd> --help` must print usable help even for subcommands that need `DisableFlagParsing`; intercept `--help` before forwarding the rest to the underlying command. A command whose `--help` does not render is a bug.
- A leaf command's `Short:` names the operation and the role it plays, not the topic, and stays to one line. "Delete an environment" is the topic; "Delete a tenant environment and tear down its remote runtime" is the operation. Keep consequences out of the `Short:`.
- High-blast-radius commands — anything that mutates remote k8s, AWS resources, the project's git state, or destructive local state (`init`, `deploy`, `delete`, `open`, `release`, `doctor`, `context init`, `cloud init aws`, `build --release --deploy`, and equivalents) — carry a short `Long:` that states only the consequences a reader must weigh before running: irreversible data loss, money spent, remote or shared state changed, a browser/SSO login that will pop. State them plainly in a line or two; do not turn the `Long:` into an exhaustive side-effect log.
- Every command whose flag combinations meaningfully change behaviour needs an `Example:` block with at least one realistic invocation.
- The CLI `Short:` + `Long:` and the MCP tool `Description:` for the same operation must reflect the same ground-truth behaviour. If they diverge in meaning, one of them is wrong — fix both, not just one.

Methodology when reviewing or editing help / descriptions:

- Establish ground truth first. Trace the command's execution path end-to-end and write down what it actually does — operations performed, side effects, files written, network calls, prompts, output, error paths. Only then read the existing `Short:` / `Long:` / MCP `Description:` and score each against that ground truth. Reviewing docs against impressions of the code, or against the sibling transport's docs, is not the review — it produces false positives.
- Score descriptions by accuracy, not length, and by usefulness, not completeness. A longer description that misframes the operation is worse than a short one that gets the noun right. "Run Docker build operations" reduces an orchestrator to a docker proxy; replacing the CLI's `Short:` with that wording makes the help worse, not better. Judge whether a reader learns what the command is for and when to use it — not whether every behaviour is listed.
- Do not blanket-lift one transport's description into the other. Each must be rewritten against the ground truth.
- When reporting on help-text quality, quote the literal user-visible text (the `--help` block, the MCP description) and reason about what a reader can infer from it. Skip file:line citations — they do not change whether the help works. Reserve source references for places where something must be changed.

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

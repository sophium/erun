# AGENTS.md

Repository guidance for humans and coding agents working in this repo.

- Follow this file for the whole repository.
- When working inside a subdirectory that has its own `AGENTS.md`, follow the child file as additional, more specific guidance for that subtree.
- Submodules may define their own `AGENTS.md` files with more specific guidance. See `erun-ui/AGENTS.md` for desktop-module guidance.
- See `erun-devops/AGENTS.md` for runtime-image, chart, build-cache, and release-workflow guidance in the DevOps module.

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

## Project Structure

- `erun-cli` - CLI utility
- `erun-common` - shared common module
- `erun-mcp` - MCP server module
- `erun-devops` - runtime Docker images, Linux packaging, and Kubernetes chart assets used by build, open, deploy, and release flows
- `erun-ui` - desktop app module built with Wails, using a Go backend and a TypeScript/Yarn frontend

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
- Prefer fast, evidence-driven iteration. Use existing behavior, failing symptoms, tests, and screenshots as the source of truth, then tighten the implementation around the observed problem.
- Keep work centered on the current user goal. Avoid opportunistic cleanup, broad redesign, or unrelated polish unless it directly reduces risk for the requested change.
- When a problem crosses module boundaries, solve it at the lowest shared layer that owns the behavior, then keep transport-specific code focused on adaptation and presentation.
- Make high-impact behavior explicit before executing it. Favor plans, previews, confirmations, and reversible steps when work can delete data, mutate remote systems, publish artifacts, or affect shared environments.
- Preserve momentum by choosing the simplest defensible design that fits the repository. Add structure only when it clarifies ownership, reduces repeated logic, or prevents a real class of mistakes.
- Treat repeated user corrections as signal that the interaction model is wrong, not just the implementation detail. Revisit the flow and simplify it around what the user is trying to accomplish.
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
- Do not add new documentation files unless the user explicitly asks for them; add repository instructions to `AGENTS.md` instead.
- Keep `AGENTS.md` focused on repository workflow and engineering guidance; do not document app behavior, command semantics, or end-user functionality in it.
- Do not modify `README.md` unless the user explicitly asks for a README change.

## Release Rules

- Treat release work as repository-wide. When changing release behavior, validate `erun-common`, `erun-cli`, and `erun-mcp`, not just the module where the code change landed.
- When release, launcher, or desktop packaging behavior changes affect the desktop app, validate `erun-ui` too and keep package-manager metadata aligned with the desktop build outputs.
- Keep stable release automation responsible for all repository metadata that must move with the release, including versioned charts, package-manager metadata, and generated release references; when that metadata references GitHub archive assets, update both version fields and checksums instead of rewriting only URLs.
- Treat release-time Docker images as dependency graphs, not isolated targets. If a release image depends on local base images, publish those local dependencies before publishing the dependent image.
- Release-tagged runtime images must be published for both `linux/amd64` and `linux/arm64`. Do not rely on the local daemon default platform for stable releases.
- Multi-architecture release builds must verify builder capability explicitly. Fail with a direct error when the selected buildx builder does not report all required target platforms.
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
- Branch from `main`.
- Use `feature/<issue-number>-<short-kebab-case-description>` for new functionality.
- Use `bug/<issue-number>-<short-kebab-case-description>` for bug fixes.
- Include the issue number in the branch name for traceability, for example `feature/12-add-mcp-server-entrypoint`.

## Pull Request Titles

- Use a clean human title that describes the change directly.
- Do not add agent markers such as `[codex]` unless the repository explicitly asks for them.
- Prefer sentence-style titles such as `Add HTTP MCP server entrypoint`.

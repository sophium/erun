# AGENTS.md

Module-specific guidance for `erun-common`. Follow the repository root `AGENTS.md` first, then apply this file.

`erun-common` holds the shared, transport-agnostic core types and logic for the whole solution, and it is the **canonical home for the shared Go conventions** used across the Go modules (`erun-cli`, `erun-common`, `erun-mcp`). `erun-cli/AGENTS.md` and `erun-mcp/AGENTS.md` inherit everything in this file and add only their transport-specific rules.

## Module Role And Boundaries

- Keep `erun-common` small and focused on reusable core types and logic, not module-specific orchestration.
- Move code into `erun-common` only when it is genuinely shared across modules and remains transport-agnostic.
- Do not move code into `erun-common` just because it is reused once; prefer a specific shared package only when a stable cross-module abstraction exists.
- Keep `erun-common` usable as a standalone library for third parties. Shared code placed there must be transport-agnostic and should not depend on Cobra, the MCP SDK, or module-specific orchestration.
- When sharing operation contracts across modules, prefer transport-neutral names such as plan, request, result, or input/output. Do not put MCP-only wrapper types in `erun-common` unless they are intentionally generic library contracts.
- Prefer reusing a shared struct over creating a transport-local duplicate with the same shape. When one shared struct is the canonical contract for both CLI and MCP, transport-specific annotations such as `json` tags are acceptable in `erun-common` to avoid structure duplication.

## Preferred Direction

- Prioritize maintainability and clarity over performance optimizations by default.
- Prefer established repository patterns over introducing new command, config, testing, or documentation styles. Extend the existing shape first and only add a new pattern when the current one is clearly inadequate.
- Organize shared command logic by command name when practical. If `build`, `open`, `init`, or `deploy` is shared, prefer files and types that mirror that command shape across `erun-common`, `erun-cli/cmd`, and `erun-mcp`.
- Keep `build`/`push`/`deploy`/`open` as pure primitives in the shared layer: their resolution and execution must not branch on environment type or env name. `build` mints the version (a snapshot unless `--release`/override); `push`/`deploy` take the version as explicit input and never synthesize one; chart publishing rides with `push`. Env-type decisions are the caller's policy — keep them out of `erun-common`. See root `AGENTS.md` § "Command primitives vs orchestration".
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
- **Never put a process name erun ships into a path that lands in another process's command line.** Session sockets lived at `/tmp/erun-app/<...>.dtach`, so the `dtach` command line holding an operator's terminal contained `erun-app` — and `pkill -f erun-app`, the natural way to free the desktop binary before a rebuild, killed those terminals and their agent sessions. `eruncommon.DesktopAppName` is the name to keep out of such paths; `TestSessionSocketPathCannotCollideWithTheDesktopBinary` pins the property rather than the literal path, because the literal is what drifts.
- **When a tool erun wraps only reports on exit, wrap its streaming mode and normalize the events — do not expose the vendor's shape.** A detached `claude -p` job sat at zero captured bytes for its whole life, so every orchestrator hand-rolled a scraper over the tool's private transcript. `erun-common/job_agent.go` is the pattern: erun builds the streaming invocation, folds the events into one tool-agnostic view (`AgentJobProgress`), and only the per-tool parsers know a vendor's event names. A vendor reshaping its stream must change what erun parses, never what a caller reads.
- **A long-running supervisor has exactly one writer of its record.** When a periodic poll and the work's own milestones both persist state, route both through one mutex-guarded owner (`jobRecorder`) — two independent writers of the same file will eventually let a progress tick overwrite the outcome the wait just captured.

## Dependency Wiring

- Apply KISS to dependency wiring. Do not introduce abstractions or injection layers unless they solve an immediate problem in the current code.
- Do not pass a dependency into a function unless that function actually uses it in its own body. Passing it through to another function does not count as usage.
- Prefer wiring concrete dependencies at the boundary and then passing only the specific values needed by the next function.
- If a function only needs already-built subcommands, handlers, or services, pass those directly instead of the larger set of dependencies used to construct them.
- Prefer direct use of an existing concrete function such as `common.FindProjectRoot` when it is only needed once. Do not create a local alias just to forward it.
- If a dependency value is used multiple times in the same function, binding it to a local is acceptable when that improves readability.
- Keep default wiring local to the real composition boundary, usually `Execute()` or the transport entrypoint, rather than spreading default-resolution helpers throughout production code.
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

## Validation

- Run `go test ./...` from this module after Go changes.
- `erun-common` behavior is gated end-to-end by the integration suite, not by unit tests (root `AGENTS.md` § "Integration Test Gate"; `erun-integration/AGENTS.md`). Add or update integration scenarios for new `erun-common` behavior; a unit test that overlaps an integration scenario should be deleted, not kept.

# AGENTS.md

Repository guidance for humans and coding agents working in this repo.

## Contributing

- Erun GitHub repository: `https://github.com/sophium/erun`
- Use this repository to extend ERun functionality.
- Start by creating issue, create branch and PR once conclusion, that functionality is needed is reached

## Project Structure

- `erun-cli` - CLI utility
- `erun-common` - shared common module
- `erun-mcp` - MCP server module

## Module Boundaries

- Keep CLI-private implementation in `erun-cli/internal`.
- Treat `internal` as a deliberate module boundary, not a staging area for future shared code.
- Move code into `erun-common` only when it is genuinely shared across modules and remains transport-agnostic.
- Do not move code into `erun-common` just because it is reused once; prefer a specific shared package only when a stable cross-module abstraction exists.
- Keep `erun-common` small and focused on reusable core types and logic, not module-specific orchestration.

## Preferred Direction

- Prefer explicit runtime structs over package globals.
- Keep mutable state local to one CLI execution or one MCP tool invocation.
- Default to local execution and local integrations. Any remote or hosted transport should be additive, not the baseline behavior.
- Prefer dependency injection in tests instead of replacing globals.
- Prefer pure functions with no side effects for core logic.
- Keep config and domain types simple and easy to copy safely.
- Keep business logic reusable so the CLI and MCP layers can share it.
- Design MCP-facing handlers as non-interactive operations with explicit inputs and structured outputs.

## Go Safety Notes

- Go is memory-safe by default, but practical failures still come from shared mutable state, data races, resource leaks, and `unsafe`.
- Copying is a good default only for plain value data. Slices, maps, pointers, channels, and structs containing them still share underlying state unless explicitly cloned.
- Favor clear ownership over incidental sharing. If callers must not mutate returned data, return a copy.

## Working Rules

- Treat execution state as scoped to one CLI run or one MCP request, not shared process state.
- Avoid adding new package-level mutable variables.
- Keep side effects at the boundaries: CLI I/O, MCP transport, filesystem, network, and process execution.
- Keep tests isolated and do not add `t.Parallel()` around code that mutates globals.
- CLI prompts are acceptable in interactive flows, but MCP-exposed paths should receive all required input explicitly and fail clearly when input is missing.
- Prefer deterministic command behavior so tool calls are safe to run repeatedly and concurrently.
- Prefer safety and clarity over micro-optimizations.
- Do not modify `README.md` unless the user explicitly asks for a README change.

## Refactoring Rules

- Treat refactoring as behavior-preserving by default.
- Do not change user-visible output, help text, error text, prompts, logging, defaults, or flags unless the user explicitly asks for that functional change.
- Before and after a refactor, compare observable behavior with `main` and add or update regression tests for any behavior that must remain unchanged.

## Branching Strategy

- Create a GitHub issue before starting implementation work.
- Branch from `main`.
- Use `feature/<issue-number>-<short-kebab-case-description>` for new functionality.
- Use `bug/<issue-number>-<short-kebab-case-description>` for bug fixes.
- Include the issue number in the branch name for traceability, for example `feature/12-add-mcp-server-entrypoint`.
- Open pull requests back into `main` and reference the issue in the PR body, for example `Closes #12`.
- Treat `push, accept` as a request to complete the full publish flow: push the branch, create the pull request, and leave the PR in a non-draft accepted/ready-for-review state. Do not stop after the branch push alone.

## Pull Request Titles

- Use a clean human title that describes the change directly.
- Do not add agent markers such as `[codex]` unless the repository explicitly asks for them.
- Prefer sentence-style titles such as `Add HTTP MCP server entrypoint`.

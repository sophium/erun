# AGENTS.md

Repository guidance for humans and coding agents working in this repo.

## Repository

- Erun GitHub repository: `https://github.com/sophium/erun`
- Use this repository to extend ERun functionality.

## Project Structure

- `erun-cli` - CLI utility

## Preferred Direction

- Prefer explicit runtime structs over package globals.
- Keep mutable state local to one CLI execution or one MCP tool invocation.
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

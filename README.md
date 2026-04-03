# ERun

ERun is a Go-based developer tool for working with project tenants and environments.

The repository currently contains:

- a CLI in `erun-cli/`
- explicit project initialization through `erun init`
- a simple MCP server exposed through `erun mcp`
- YAML-backed config stored in the platform XDG config directory

The CLI remains the primary interface. MCP is an additional transport that exposes the same capabilities as tools.

## Current Commands

- `erun init`
- `erun version`
- `erun mcp`

`erun init` bootstraps configuration for the current project or for an explicit tenant/project root.

`erun mcp` runs a stdio MCP server and currently exposes:

- `version`
- `init`

## Configuration

ERun stores configuration under the XDG config directory in an `erun/` root.

The current model has three layers:

- global ERun config
- tenant config
- environment config

At the moment the default environment is `dev`.

## Development

Install prerequisites:

```sh
brew install go
brew install golangci-lint
git config core.hooksPath .githooks
```

Run locally from the repo root:

```sh
./scripts/run-erun.sh
```

Or from the Go module:

```sh
cd erun-cli
./run.sh
```

Run tests:

```sh
cd erun-cli
go test ./...
go test -race ./...
```

## Design Notes

- Keep business logic reusable below the CLI layer so the same operations can back MCP tools.
- Prefer explicit command execution over hidden global initialization.
- Keep mutable state scoped to one CLI run or one MCP request.
- Prefer pure core logic and keep side effects at the boundaries.

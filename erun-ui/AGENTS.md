# AGENTS.md

Module-specific guidance for `erun-ui`. Follow the repository root `AGENTS.md` first, then apply this file for work in this subtree.

## Module Role

- `erun-ui` is the desktop app transport for ERun.
- Keep shared tenant, environment, and project-resolution logic in `erun-common`. Do not duplicate shared planning or config resolution in the desktop module.
- `erun-ui` must not import `erun-cli`. When the desktop app needs an interactive shell, launch the installed `erun` executable as a child process instead of linking CLI packages directly.

## Frontend And Backend Split

- Keep Wails startup, native window integration, PTY management, process execution, and session lifecycle in Go.
- Keep layout, interaction behavior, DOM state, and terminal presentation in the frontend source tree.
- Keep terminal session ownership in Go. The frontend should attach to sessions by ID, render buffered output, and send input, but it should not start shells on its own.
- Prefer small transport-facing Go methods with JSON-safe structs over leaking backend internals into the frontend contract.

## Frontend Workflow

- Use Yarn for dependency management and frontend builds. Do not introduce `npm` or `pnpm` lockfiles unless the user explicitly asks for a toolchain change.
- Edit frontend source files, not generated bundles or generated Wails bindings. Regenerate generated artifacts instead of hand-editing them.
- Keep styling intentional and native-desktop oriented. Prefer precise layout and spacing adjustments in CSS over adding more Wails or DOM complexity.

## Build And Packaging

- Keep the module build script as the canonical local and release-facing desktop build entrypoint.
- Preserve the installed binary name `erun-app` unless the repository explicitly changes launcher and package-manager integration too. `erun app`, Homebrew, and Scoop wiring depend on that artifact name.
- Keep macOS and Windows packaging assumptions aligned across the module build scripts and package-manager metadata.
- When changing Darwin CGO or deployment-target behavior, align compile and link settings together so local builds, package-manager builds, and Wails builds do not drift.

## Validation

- Run `go test ./...` for Go/backend changes.
- Run `yarn build` and `go test ./...` for frontend changes.
- Run `./build.sh <target>` when changing desktop packaging, Wails wiring, CGO settings, or generated asset embedding.

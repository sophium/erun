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
- Use the pinned shadcn CLI from `erun-ui/frontend/package.json`. Do not use `shadcn@latest` or `yarn dlx shadcn@latest`; run shadcn commands through `yarn shadcn ...` from `erun-ui/frontend`.
- Keep generated shadcn files aligned with the pinned CLI. After adding or updating shadcn components, run `yarn shadcn:check` from `erun-ui/frontend`; it regenerates the installed components and fails if committed generated files differ.
- Edit frontend source files, not generated bundles or generated Wails bindings. Regenerate generated artifacts instead of hand-editing them.
- Keep styling intentional and native-desktop oriented. Prefer precise layout and spacing adjustments in CSS over adding more Wails or DOM complexity.

## Frontend Styling

- Use Tailwind utilities as the default for component-owned layout, spacing, color, typography, hover/focus/disabled state, and responsive behavior.
- Keep shadcn-generated files under `erun-ui/frontend/src/components/ui`, `erun-ui/frontend/src/lib/utils.ts`, `erun-ui/frontend/src/styles/theme.css`, `components.json`, `package.json`, and `yarn.lock` aligned with the pinned shadcn CLI. Do not hand-edit generated shadcn output unless the change is intentionally local and `yarn shadcn:check` still passes.
- Keep `src/styles/theme.css` shadcn-compatible. Put app-owned Tailwind theme extensions in separate app CSS files, then import them from `src/styles/index.css`.
- Keep global CSS small and reserved for true globals: root sizing/reset rules, xterm internals, Wails drag or resize state hooks, pseudo-elements that would be awkward in markup, and runtime CSS variables that are set from controller state.
- Prefer shadcn primitives and variants for buttons, inputs, dialogs, tabs, popovers, tooltips, labels, and checkboxes before adding custom local controls.
- Preserve CSS variables for runtime-sized panels and computed values such as sidebar width, review width, file-list width, tree depth, and diff content width.
- Use semantic Tailwind tokens such as `bg-background`, `text-foreground`, `border-border`, `bg-sidebar`, and app-owned tokens from app theme files instead of repeating raw color values in component markup.
- Avoid reintroducing broad semantic CSS class files for ordinary component styling. If a selector is only used by one React component and does not require a true global rule, keep the styling beside that component in `className`.
- For frontend styling changes, run `yarn build`, `yarn shadcn:check`, and `go test ./...` from the relevant module paths unless the change is documentation-only.

## Build And Packaging

- Keep the module build script as the canonical local and release-facing desktop build entrypoint.
- Preserve the installed binary name `erun-app` unless the repository explicitly changes launcher and package-manager integration too. `erun app`, Homebrew, and Scoop wiring depend on that artifact name.
- Keep macOS and Windows packaging assumptions aligned across the module build scripts and package-manager metadata.
- When changing Darwin CGO or deployment-target behavior, align compile and link settings together so local builds, package-manager builds, and Wails builds do not drift.

## Validation

- Run `go test ./...` for Go/backend changes.
- Run `yarn build` and `go test ./...` for frontend changes.
- Run `./build.sh <target>` when changing desktop packaging, Wails wiring, CGO settings, or generated asset embedding.

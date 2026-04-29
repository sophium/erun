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
- Edit source files, not generated artifacts such as bindings, bundles, generated models, generated clients, or generated component output. Regenerate artifacts through the repository's generator command and inspect the generated diff instead of hand-editing the output.
- Do not patch generated files manually, even temporarily to satisfy a compiler, type checker, import, or test. When generated output is missing or stale, first change the source contract that owns it, then run the appropriate generator.
- If the required generator is unavailable or failing, stop and report the generator problem instead of hand-writing generated output. For Wails frontend bindings, this means changing the exported Go method or type first, then running `wails generate module` from `erun-ui`.
- Keep styling intentional and native-desktop oriented. Prefer precise layout and spacing adjustments in CSS over adding more Wails or DOM complexity.

## Frontend Code Organization

- Keep React-facing controllers, hooks, and app services as thin public facades. They should adapt component events, call focused modules, update state, and emit changes rather than accumulating workflow logic.
- Add new frontend code directly to the module that owns it. Do not use a facade, component, hook, or large file as a temporary staging area.
- Put app-owned data structures in `model/` when they are shared across frontend modules or express a workflow contract. Use one exported type or interface per file, and re-export them from a local `index.ts` when that keeps imports readable.
- Do not move generated Wails types or shared UI types from `@/types` into local model folders.
- Keep model files free of behavior. Put formatting, parsing, normalization, validation, and classification logic in focused helper modules named for the domain they serve.
- Put grouped mutable bookkeeping into focused classes when maps, sets, buffers, timers, or subscriptions represent one lifecycle concept.
- Put DOM/layout interactions in focused action modules when they can operate on app state, DOM references, and explicit callbacks.
- Keep Wails calls near the workflow that owns them unless a backend adapter is being introduced intentionally.
- Keep storage persistence close to the state transition that owns the persisted value.
- Name workflow modules for user-visible flows or domain concepts, such as environment, tenant, global configuration, review, terminal, layout, cloud context, or package management.

## Frontend Component Discovery

- Before adding a new frontend control or composing a custom interaction, search for an existing local component and usage pattern with `rg` in `erun-ui/frontend/src/components` and `erun-ui/frontend/src/app`. Search by user-facing label, role, primitive name, and behavior, such as `Popover`, `Command`, `VersionField`, `choicesOpen`, `dropdown`, `select`, or the closest visible label.
- Prefer reusing or extracting from an existing app-owned component when the behavior already exists. For example, editable picker behavior should be compared against `VersionField` before using browser-native `datalist` or building a separate one-off control.
- Check both shadcn primitives in `erun-ui/frontend/src/components/ui` and app-owned wrappers in `erun-ui/frontend/src/components/app` before choosing an implementation. Use primitives directly only when no app-owned wrapper already expresses the same interaction.
- Before adding hover, focus, tooltip, popover, toast, badge-detail, or other secondary information UI, search for `Tooltip`, `IconTooltip`, `Popover`, `Dialog`, existing status badges, and nearby titlebar/sidebar patterns. Do not use the browser-native `title` attribute for meaningful product UI, diagnostics, errors, guidance, or multi-line content.
- If a native browser control is used for non-trivial behavior, verify it renders and behaves correctly in the Wails WebView target. Do not assume browser-native affordances such as `datalist` are visible or usable in the desktop runtime without checking the rendered surface.
- When a new reusable interaction is still needed after discovery, make it an app-owned component with explicit props rather than duplicating markup inline across dialogs.

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

## Professional UX

- Start UI work by naming the user task, the decision or recovery the screen must support, and the states the user needs to distinguish. Do this before choosing controls or wiring data. If the task is diagnostic, identify what "not working" means, what blocks action, what failed after action, and what the user can do next.
- Treat UX guidance as acceptance criteria for UI work, not as optional polish after data flow works. A change that technically saves data but uses the wrong control, asks users to type values the app already knows, hides relevant state, or makes recovery unclear is incomplete.
- Do not treat backend data exposure as a complete UI change. After data is available, design how the user will discover it, understand it, recover from it, and verify whether their action succeeded.
- Before implementing a UI surface, decide whether the information belongs inline, in a badge, in a panel, in a popover, in a dialog, or in a notification based on urgency, persistence, and actionability. Persistent blockers and recoverable errors should be visible where the user acts; transient supplemental details may use hover/focus surfaces.
- Treat the referenced UX standards in this section as mandatory source material, not background links or inspiration. Before implementing a non-trivial UI change, explicitly apply the relevant linked guidance: Nielsen Norman heuristics for interaction quality and status/recovery behavior, WCAG for accessibility, and Material guidance for empty states and dialogs when those patterns are involved.
- Before considering a UI change complete, do a heuristic pass against the linked references: visibility of system status, match with user language, consistency and standards, error prevention, recognition over recall, user control, accessible operation, and component-appropriate behavior. If a control, label, status color, disabled state, or action would violate those principles, fix it even if no local bullet names that exact problem.
- For reviews and implementation notes on UI work, mention the specific linked UX principle or standard that drove the component choice when the decision is not obvious. Examples: "visibility of system status requires an inline blocker", "recognition over recall requires a named recovery action", or "WCAG keyboard access requires a focusable trigger."
- Check the UI against the actual user question that triggered the work. If the user asks "where can I see the error?", the finished UI must make the error or the reason there is no error visible without requiring inference from implementation details.
- Choose the component that matches the data model before implementing save behavior. Known option sets must use selectors, dropdowns, segmented controls, toggles, or equivalent constrained controls rather than free-text inputs. Free-text fields are reserved for values users genuinely author, such as names or paths.
- Model user-facing settings around the domain objects users actually manage. Do not expose implementation details such as hidden local profiles, process state, transport-specific names, generated IDs, or implementation caches as primary concepts unless the user must explicitly choose them.
- Use familiar operational patterns: lists or tables for collections, badges for status, icon buttons for compact actions, forms for editable details, and explicit primary/secondary actions for side effects.
- Make object state visible where users act on it. Collection rows should show the object name, relevant metadata, current status, and the most likely action without requiring users to open a detail view.
- Diagnostic state must explain the current outcome directly where the user is looking. If an operation is blocked, show the blocking reason as a first-class line, not only as a raw marker or nested detail. If an operation failed after being attempted, show the latest actionable error separately from blockers.
- Tooltips are acceptable only for short supplemental details or compact status diagnostics. They must use the app tooltip/popover primitives, be keyboard reachable when attached to non-button elements, provide an accessible label, and remain readable for long values through wrapping or escalation to a popover/dialog. Native `title` is only acceptable for non-essential truncation hints.
- Keep state, color, and available actions semantically consistent. A success/ready state should look successful and should not present an action that implies the opposite state; warning/error/expired states should clearly show that attention is needed and offer the relevant recovery action.
- Empty states must not look like disabled inputs or editable fields. Use plain text or a purpose-built empty-state surface, and reserve bordered input styling for controls that accept input.
- Labels and messages must use user-language, not implementation-language. Prefer terms that describe the object or action the user understands, and keep internal provider, CLI, SDK, profile, session, or transport details out of the primary UI.
- Status refresh, login, deploy, delete, and other side-effecting actions must be explicit user actions. Do not run them implicitly when opening, rendering, or refreshing a settings view unless that behavior is clearly named by the control.
- Design destructive, publishing, login, and external side-effect flows with clear action boundaries: users should understand what will happen before the action starts, be able to cancel before commitment, and see completion or failure status afterward.
- Keep forms focused on values the user can know and intentionally provide. Do not ask users to invent derived values; compute aliases, IDs, labels, summaries, and status from authoritative data after the relevant operation completes. When a value is selected from configured repository or application state, load that state into the form and present it as choices.
- Keep settings saves scoped to edited fields. Saving one setting must not remove or rewrite unrelated configuration.
- Preserve accessibility basics in every UI change: semantic buttons and labels, keyboard-reachable controls, visible focus, sufficient contrast, non-color-only status communication, and error text associated with the relevant control or action.
- Validate UI work with an actual rendered surface, using a Wails runtime or a browser harness with Wails mocks when the plain Vite page cannot run standalone. Include the relevant visual state in validation: empty, populated, loading, error, disabled, and narrow viewport when the change can affect those states.

References:
- Nielsen Norman Group, "10 Usability Heuristics for User Interface Design": https://www.nngroup.com/articles/ten-usability-heuristics/
- W3C, "Web Content Accessibility Guidelines (WCAG) 2.2": https://www.w3.org/TR/WCAG22/
- Material Design, "Empty states": https://m1.material.io/patterns/empty-states.html
- Material Design, "Dialogs": https://m1.material.io/components/dialogs.html

## Build And Packaging

- Keep the module build script as the canonical local and release-facing desktop build entrypoint.
- Preserve the installed binary name `erun-app` unless the repository explicitly changes launcher and package-manager integration too. `erun app`, Homebrew, and Scoop wiring depend on that artifact name.
- Keep macOS and Windows packaging assumptions aligned across the module build scripts and package-manager metadata.
- When changing Darwin CGO or deployment-target behavior, align compile and link settings together so local builds, package-manager builds, and Wails builds do not drift.

## Validation

- Run `go test ./...` for Go/backend changes.
- Run `yarn build` and `go test ./...` for frontend changes.
- Run `./build.sh <target>` when changing desktop packaging, Wails wiring, CGO settings, or generated asset embedding.

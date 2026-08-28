# AGENTS.md

Module-specific guidance for `erun-kit`. Follow the repository root `AGENTS.md` first, then apply this file for work in this subtree.

## Module role

- `erun-kit` is the shared frontend foundation for ERun's two React apps — the desktop (`erun-ui/frontend`) and the hosted console (`erun-console`). It plays the same role on the frontend side that `erun-common` plays on the Go side: the transport-agnostic core both transports depend on, never the other way around (see root `AGENTS.md` § "Command primitives vs orchestration" for the Go-side analogue of this boundary).
- **The kit is transport-agnostic, with no exceptions.** Nothing here may import Wails bindings, `fetch`, a base query, or a Redux store. Widgets take props and emit callbacks; nothing here reaches into an app's state. A widget that needs `useAppSelector`/`useAppDispatch` or a `wailsjs` import is not ready to move here.
- Consumed via Yarn workspaces (root `package.json`), resolved by both apps as the `erun-kit` package. Both apps also register a `@kit/*` path alias (tsconfig `paths` + Vite `resolve.alias`) pointing at `erun-kit/src` — this is **not** for importing the kit's public API (use the bare `erun-kit` specifier for that); it exists because the kit's own shadcn-managed primitives resolve each other and `cn` through `@kit/...`, and that alias has to resolve identically regardless of which app's Vite/tsc instance is processing the file (a bundler alias is global to its own config, not scoped to the file that uses it — an app's own `@/*` alias would otherwise silently point kit-internal imports at the wrong tree). Hand-written kit code (the tier-2 widgets) uses plain relative imports instead, since nothing regenerates them and there's no reason to add alias surface for files under full manual control.

## What belongs here

- **Tier 1 — tokens and primitives.** `src/styles/theme.css` (the shadcn/Tailwind 4 token scale, light and dark) and `src/lib/utils.ts` (`cn`), plus the shadcn primitives under `src/components/ui/`.
- **Tier 2 — generic widgets.** App-agnostic components with zero app-state imports: `StatusBadge` (+ helpers), `EmptyState`, `IconTooltip`, `FieldLabel`, `SelectField`, `EditableComboField` (+ helpers), `ErrorBoundary`, `ResizeHandle`, `FileIcon`.
- **Tier 3 — erun-domain widgets**, moved as the console actually needs them rather than speculatively: `VersionField`, `KubernetesContextSelect`, `ContainerRegistriesField`. These encode erun concepts, so a second implementation would be a second opinion about erun, not just a duplicated control. None have moved yet — the console has no caller for them.
- **Tier 4 — shared models and RTK Query endpoint definitions.** `src/models/platformConfig.ts` is the `GET /v1/config` wire model (`Tenant`, `Environment`, `CloudContext`, `TenantConfigView`) plus its lenient parsers — this is erun-backend-api's own contract, not a console-only concept, so any transport that talks to the hosted platform API shares it. `src/api/platformConfigEndpoints.ts` is the one RTK Query endpoint definition (`buildPlatformConfigEndpoints`) parameterized over a `BaseQuery` rather than a concrete one, so it injects into any app's own `createApi` instance: erun-console's `platformApi` (`src/app/api/platformApi.ts`) wires it to a real `httpBaseQuery`; erun-ui/frontend's own suite (`src/app/api/platformConfigEndpoints.test.ts`) wires the identical factory to its real `wailsQueryFn`, proving the same definition produces the same model over both transports.
- Of erun#1211's phase-7 list (tenants, environments, selection, notifications, request counters, collaboration state), only the piece above has moved. The rest stayed put deliberately: the desktop's `tenantsSlice`/`selectionSlice`/`requestCountersSlice` hold desktop-only shapes (`UITenant`, Wails-bound selection, PTY-flow counters) with no console equivalent to share, and console's own environments/provisioning/identity/mcp reads became RTK Query endpoints and slices local to `erun-console/src/app/` rather than kit widgets, since nothing about their state is shared with the desktop today. Move a slice here only when a second real transport needs the same shape — not ahead of that caller.

## What does not belong here

- Anything desktop-only: `TerminalPane`, `TerminalTabStrip`, `Titlebar.*`, `ReviewPanel.*`, `DiffList`, `ManageDialog*`, `OrchestratorDialog`, `DebugPanel`, `ReconnectDialog`, and their slices/thunks. These depend on Wails, a PTY, or a pod.
- Anything console-only: `ConfigView`, `ProvisionPanel`, `MCPAccessPanel`, `EnvironmentsPanel`, `UsersPanel`, `OrgSettingsPanel`, and the app shell (`shell/AppShell`, `ConsoleSidebar`, `ConsoleHeader`, `PreShellScreens`, `CenteredCard`, `BrandMark`, `sections.ts`, `theme.ts`) — the shell composes kit primitives with console-specific navigation and IA, it is not itself a generic widget.
- A base query, a `fetch` call, or a store. If a widget needs one, it isn't a widget — it's app wiring, and it stays in the app.

## Adding a widget

1. Confirm it has zero app-state imports (no `useAppSelector`/`useAppDispatch`/`@/app/*`) in its current home before moving it — that's what makes tier 2 safe to share as-is.
2. `git mv` it into `src/components/`, rewrite its internal imports to plain relative paths (not `@/...` — see "Module role" above for why), and re-export it from `src/index.ts`.
3. Update the moving app's imports to `from 'erun-kit'`, run `yarn typecheck && yarn lint && yarn format:check && yarn build` in the app, and add the widget to the harness (`harness/Harness.tsx`) so it renders in every state the harness covers.
4. Run this module's own gates (below) before the app's.

## Adding or updating a shadcn primitive

- Run `yarn shadcn ...` from this directory, not from either app — the primitives, `components.json`, and `src/styles/theme.css` live here now.
- After adding or updating a primitive, run `yarn shadcn:check`; it regenerates the pinned set and fails the build if the committed output differs from what the pinned CLI produces.
- Do not hand-edit generated primitive output under `src/components/ui/`. If a primitive needs to change, change it through the CLI and re-run `shadcn:check`.
- **Exception: `dialog.tsx` carries three deliberate, permanent classes that stock shadcn does not emit** — `grid-cols-1` on the content, and `min-w-0` on the header and footer. Stock `DialogContent` is `display: grid` with no explicit `grid-template-columns`, so the browser sizes the implicit single column to the max-content width of its widest descendant: one unbroken string (a long file path, a UUID) with no `min-w-0` above it drags that column, and every sibling grid item with it, past the card's own clamped box, while the card's background and border still paint at the correct width. These are **not** hand-edits to live with: `scripts/reapply-dialog-clamp.mjs` reapplies them after `shadcn add --overwrite`, and `shadcn:check` runs it between regeneration and the diff, so the check stays green and still catches every other drift. The script asserts each stock string is present before replacing it, so if upstream changes this primitive it fails loudly rather than masking the change — re-derive the clamp against the new output and update the script, never delete it.

## Validation

Run all of these from `erun-kit/` for any change to this module:

```bash
yarn typecheck      # tsc --noEmit
yarn lint            # eslint .
yarn format:check    # prettier --check .
yarn build           # vite build of harness/ — the kit's own demo app
yarn shadcn:check    # regenerate the pinned primitives, diff against committed output
```

- `yarn build` builds `harness/`, a small Vite app that renders every widget in the states it's given (default, disabled, empty, with/without an action, every `StatusBadge` tone) and a light/dark toggle. It is the kit's own reviewable artifact — run `yarn dev` to preview it locally. It is not how the two apps consume the kit; they import `erun-kit`'s TypeScript source directly (see "Module role"), so nothing here needs a published build output.
- After any change, also validate every app that consumes the changed surface: `erun-ui/frontend` (`yarn typecheck && yarn lint && yarn format:check && yarn build`, plus `./playwright/run.sh` specs covering the changed widget — see `erun-ui/playwright/AGENTS.md`) and `erun-console` (`yarn typecheck && yarn lint && yarn format:check && yarn build && yarn test`) if it has adopted the changed surface.
- The desktop's Playwright suite is the regression gate that proves a kit change is byte- and behaviour-identical for its existing consumer: it must stay green with **no spec edits**. A spec edited to keep it passing after a kit change is a regression in the kit, not a test update.

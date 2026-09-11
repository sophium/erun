# AGENTS.md

Shared frontend guidance. Follow root `AGENTS.md`. Desktop and console also
apply the shared workflow and UX sections here.

## Module role

- Own transport-neutral tokens, primitives, widgets, and genuinely shared models.
  Never import Wails, fetch, a concrete base query, an app store, or app hooks.
  Widgets take props and emit callbacks; endpoint factories accept a base query.
- Apps consume the TypeScript source through Yarn workspaces and the bare
  `erun-kit` public export. Both apps must resolve `@kit/*` identically for
  generated primitive imports. Hand-written kit code uses relative imports.

## What belongs here

- Tokens in `src/styles/theme.css`, `cn`, and generated shadcn primitives.
- Generic state-free widgets, including status, empty state, field, tooltip,
  resizing, and error-boundary components.
- Domain widgets/models only when a second real transport needs the same shape.
  `platformConfig.ts` and `buildPlatformConfigEndpoints` are shared already;
  desktop selection/PTY state and console-local workflows are not.

## What does not belong here

App shells, transport clients/stores, terminal lifecycles, desktop review controls,
and console provisioning/identity panels stay in their owning apps.

## Adding a widget

1. Verify it has no app-state/transport imports, move it as one responsibility,
   use relative internal imports, and export it through `src/index.ts`.
2. Change consumer imports to `erun-kit`; add all relevant states to
   `harness/Harness.tsx`.
3. Validate kit and every affected consumer. Organizational sharing must preserve
   existing rendered behavior; do not edit desktop specs merely to hide a regression.

## Adding or updating a shadcn primitive

- Run the pinned `yarn shadcn` CLI here, not in an app. Never hand-edit generated
  primitives. Real regeneration may change dependencies and root `yarn.lock`;
  review those changes.
- `yarn shadcn:check` regenerates in a scratch workspace, not this workspace's
  node_modules/lockfile. Keep that isolation; the CLI's install can prune packages.
- Preserve `scripts/reapply-dialog-clamp.mjs`: after regeneration it adds
  `grid-cols-1` to dialog content and `min-w-0` to header/footer. These prevent
  unbroken content from widening an implicit grid beyond the dialog.
  Fail when upstream shapes change; re-derive the patch, never suppress the check.

## Shared frontend workflow

- Use Yarn, strict TypeScript, ESLint, and Prettier; keep their checked-in
  configurations authoritative instead of copying numeric limits into guidance.
- Edit source contracts and regenerate bindings/models/primitives. If the generator
  fails, fix or report it; never hand-write generated output to satisfy a check.
- Discover existing kit widgets and app wrappers before creating controls.
  Reuse the closest interaction or extract a prop-driven component.
- Use Tailwind utilities and semantic theme tokens for component styling. Keep
  global CSS for resets, integration hooks, and runtime CSS variables; place app
  theme extensions in app CSS, not the generated theme.
- Apps must include kit sources in Tailwind scanning and resolve the class-based
  `.dark` theme consistently: saved preference first, OS preference otherwise.
- Keep workflow/state owners separate from view adapters and pure model types.
  Persist values at the owning transition; preserve timers, retries, focus,
  notifications, and busy-state behavior during refactors.

## Professional UX

- Before non-trivial UI work, record the user task, decision/recovery, chosen
  control, relevant states, and the UX principles supporting that choice.
  Apply this to backend/lifecycle changes with visible effects too.
- Use known-option controls for known values; free text is for authored values.
  Compute derived values and load configured choices. Save only edited fields.
- Present user/domain concepts rather than provider, transport, cache, or process
  internals. Show object state and the likely action where the user acts.
- Distinguish blockers from attempted failures, and render actionable reasons
  inline. Supplemental tooltips must be accessible and bounded/wrapping; native
  `title` is only for non-essential truncation hints.
- Make effects explicit: rendering or opening settings must not silently login,
  deploy, delete, or publish. Preserve cancellation before commitment and visible
  progress/result afterward.
- Use semantic labels/buttons, keyboard access, visible focus, sufficient contrast,
  non-color-only status, and field-associated errors. Empty states are not inputs.
- Validate rendered empty, populated, loading, error, disabled, and narrow states
  where affected; verify the original user question is visibly answered.
- For non-trivial changes, read/apply the relevant standards and name the principle
  behind non-obvious decisions, not merely the fact a reference was consulted:
  [Nielsen heuristics](https://www.nngroup.com/articles/ten-usability-heuristics/),
  [WCAG 2.2](https://www.w3.org/TR/WCAG22/),
  [Material empty states](https://m1.material.io/patterns/empty-states.html), and
  [Material dialogs](https://m1.material.io/components/dialogs.html).

## UX Impact Review Checklist

Record in the PR (or small-change commit body):

1. Affected user-triggered paths, or explicitly no user-triggered path.
2. User-visible sequence, including what renders each state change.
3. Any state mutation without a visible affordance; remove or surface it.
4. Persistent progress visible outside dialogs that close during work.
5. Recognizable success/failure and an actionable recovery without reading raw logs.
6. Comparable existing flow and consistency with its feedback.
7. Each of the ten Nielsen heuristics: applies/pass, issue, or explicitly inapplicable.
8. Owning app's test coverage in the same PR. Stage config-shaped state rather
   than skip it; disclose live-infrastructure gaps and cover the nearest observable
   invariant plus the underlying branch in its owning suite.

## Design-Language Decision Record

- Use kit `StatusBadge` and domain-named mappings to its five tones:
  success, warning, destructive, in-progress, muted.
- Use `InlineAlert` (`role="alert"`) for attempted failures and
  `PermissionNotice` (`role="status"`) for restricted access. Expected blocks
  are status, not faults; avoid bespoke unlabelled feedback.
- Render outcomes beside controls that remain visible. For outcomes arriving
  after navigation, use one-off notifications plus a durable activity list,
  not an unrelated toast vocabulary.
- Confirm destructive/access-revoking actions: Cancel first, action second,
  controls disabled and action spinning in flight. Unrecoverable deletion
  additionally requires the object's name.
- Use real dialog/drawer primitives with focus trap/restore and narrow transition
  announcements; never focus controls inside `aria-hidden` content.
- Keep shared activity and condition indicators consistent across row types.
  Departures from these decisions require an explicit reason in the UX note.

### Permission degradation

- Gate on effective capabilities, not role names. Centralize mapping from the
  shared capability contract to app surfaces; do not invent per-component tests.
- Do not issue forbidden reads or present unusable actions. Keep the missing
  access visible somewhere, and preserve independently accessible panels.
- Distinguish no objects, no filter matches, and no permission. A restricted
  list must never look empty. Tenant-type eligibility is a separate constraint.

## Validation

- Code changes: `yarn typecheck && yarn lint && yarn format:check && yarn build &&
yarn test && yarn shadcn:check` from this module.
- `yarn build` produces the review harness, not a package apps must build/import.
  Preview with `yarn dev`; check light/dark and affected widget states.
- Root `test-frontend` gates the first five commands; shadcn drift is a separate
  local check. Validate affected consumers with their five frontend commands and
  their required browser tests.
- Guidance-only changes follow root validation scope.

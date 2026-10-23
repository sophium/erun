# AGENTS.md

Real-browser console tests. Follow root and `erun-console/AGENTS.md`.
This is a separate Yarn package, not part of the app's Vitest/tsc collection.
Suites that need a deployed environment (OIDC sign-in against the deployment's
IdP, WebSocket attach over an exposed edge) are not here: they live in
`erun-devops/e2e/console` and run through `erun e2e`.

## Role

The package holds one suite, `tests/landing-no-horizontal-scroll.spec.ts`: the
real-engine CSS layout of the signed-out front door, a boundary jsdom cannot
see. It needs no backend, so it runs in the console image's `test` stage
(`erun-devops/docker/erun-console/Dockerfile`) on every build, together with
this package's `typecheck`, `lint` and `format:check`.

A suite that would need a local API, Postgres or emcp does not belong here: the
API, MCP and database behaviour is proven by those modules' own Go suites, and
anything that needs a deployment is an `erun e2e` suite.

## Run

- `playwright.config.ts` owns the server: its `webServer` runs `vite preview`
  over `erun-console/dist` on `127.0.0.1:5179` with `--strictPort` and
  `reuseExistingServer: false`, so an occupied port fails the run instead of
  being measured, and Playwright stops the server when the run ends.
- Build the bundle first (`yarn build` in `erun-console`); the preview serves
  whatever `dist/` holds.
- Install with `yarn install` and `yarn install-browsers`, then `yarn test`
  (`yarn test:headed` for a visible browser).

## Conventions

- Keep one worker, no full parallelism, and zero retries. Wait on observable
  URLs/elements/HTTP state, never wall-clock sleeps or retries-until-green.
- Nothing here is opt-in or skipped: the config provides everything a spec
  needs. Do not add an environment-variable gate.
- Specs are thin UI drivers asserting what an operator sees, not only endpoint
  internals. Shared UX and safety rules still apply.

## The landing-layout e2e

- Provision no backend. The signed-out landing page renders from bundled
  defaults, and platform discovery failing is its documented fallback path, so
  this suite serves the built bundle statically. Keep it that way: it needs
  nothing but the console's own bundle, which is what lets the console's build
  run it.
- Assert `documentElement.scrollWidth - clientWidth`, the measurement the bug
  was reported with. jsdom implements no layout — `scrollWidth` is always 0 —
  so neither the Vitest suite nor a geometry sweep can substitute for this.
- `overflow: clip` clips painting, not layout: a clipped element's bounding box
  still overflows its parent. Assert the scroll region, never a bounding-box
  sweep, for this defect class.
- Keep the bleed assertions. A fix that deletes the decorative element, or
  shrinks it back inside its parent's box, must fail rather than pass as "no
  more overflow".

# erun-ui playwright

End-to-end UI tests for the erun-app desktop frontend, driving the headless
HTTP+SSE bridge that `erun-app --headless` exposes.

## One-time setup

```sh
yarn install
yarn install-browsers
```

## Common commands

| Command                | What it does                                          |
| ---------------------- | ----------------------------------------------------- |
| `yarn test`            | Headless run (default). Same as `yarn test:headless`. |
| `yarn test:headless`   | Headless run (explicit alias).                        |
| `yarn test:headed`     | Same suite, with a visible browser window.            |
| `yarn test:ui`         | Playwright's interactive UI runner.                   |
| `yarn test:debug`      | Pause-on-step debugger.                               |
| `yarn report`          | Open the most recent HTML report.                     |
| `yarn install-browsers`| Re-install bundled Chromium (one-time).               |

## Notes

- `playwright.config.ts` spawns `../bin/erun-app --headless --port 34123` as
  its `webServer`. Make sure the desktop binary is up to date by running
  `./build.sh` from `erun-ui/` first.
- Tests run with `fullyParallel: false` and `workers: 1` because the
  headless backend is a singleton process and tests share session state.
- POM classes live under `pages/`. Each component surface (sidebar,
  titlebar, dialogs, panels, drawer) has its own class; specs talk to those
  through the fixture in `fixtures/erunApp.ts`.

# AGENTS.md

Module-specific guidance for `erun-ui`. Follow the repository root `AGENTS.md` first, then apply this file for work in this subtree.

## Module Role

- `erun-ui` is the desktop app transport for ERun.
- Keep shared tenant, environment, and project-resolution logic in `erun-common`. Do not duplicate shared planning or config resolution in the desktop module.
- `erun-ui` must not import `erun-cli`. When the desktop app needs an interactive shell, launch the installed `erun` executable as a child process instead of linking CLI packages directly.
- The desktop is an orchestration layer over the pure command primitives, not an operator at a terminal. It must **not** use the operator-convenience switches (`build --deploy`, `open --deploy`); it composes `build` → `push <version>` → `deploy <version>` itself, capturing the version build minted from `erun build --output json` and threading it forward. The per-env-type policy ("an Operator's deploy on an agent env means build→push→deploy; on a runtime env it means deploy a chosen/`--current` version") lives here in the desktop, computed from `EnvConfig` type, never pushed down into the commands. See root `AGENTS.md` § "Command primitives vs orchestration".

## Frontend And Backend Split

- Keep Wails startup, native window integration, PTY management, process execution, and session lifecycle in Go.
- The desktop is linked `-H windowsgui` and owns no console, so every console child it spawns on Windows (`erun`, `kubectl`, `helm`, `git`, `ssh`, `tar`, ...) otherwise gets a brand-new console window that flashes and vanishes. Any non-PTY `exec.Command`/`exec.CommandContext` in this module must call `eruncommon.HideConsoleWindow(cmd)` before running — it is a no-op off Windows. PTY-backed sessions (ConPTY/`creack/pty`) attach to their own pseudo-console and are exempt. This applies transitively: a child the desktop launches windowless whose own children are console apps must suppress them too (see the IDE launcher in `erun-cli/cmd/open_ide.go`).
- Desktop terminal sessions are one per pod per tab ID. Reattach takes over the existing dtach session rather than mirroring it.
- Diagnostics and error capture must be always-on and bounded — never gated behind an opt-in the user discovers only after an error has happened.
- Automatic agent spawning is resource-bounded by `investigation_bounds.go`: input floor, one spawn per event, duplicate-signature exclusion, cooldown, population cap, and lifetime. Reserve the slot atomically with the cap check; derive liveness from the session registry.
- Register spawned agents as attached environment jobs with activity leases (`AttachEnvironmentJob`) so status and busy indicators account for them.
- Test report/state paths belong on the owning struct, defaulted at construction and overridden per test; never write shared host locations.
- Keep layout, interaction behavior, DOM state, and terminal presentation in the frontend source tree.
- Keep terminal session ownership in Go. The frontend should attach to sessions by ID, render buffered output, and send input, but it should not start shells on its own.
- Prefer small transport-facing Go methods with JSON-safe structs over leaking backend internals into the frontend contract.
- Keep Wails-exported Go methods as transport-facing facades. They should validate UI inputs, call focused backend workflow logic, and return JSON-safe results.
- Keep desktop backend UI contracts together when they are generated into frontend bindings. Do not mix JSON-facing structs with process, PTY, or cloud lifecycle behavior in the same owner.
- Put backend workflow behavior beside the lifecycle state it owns, such as idle status, session management, pasted images, cloud context actions, or window state.
- Keep Wails event payloads stable unless the frontend contract is intentionally changing and generated bindings are refreshed.
- Keep `app.go` focused on app composition: dependency defaults, `App` construction, Wails startup/shutdown hooks, and top-level lifecycle wiring.
- Put terminal session startup, reuse, resizing, input, output streaming, activity recording, close logic, and session keys in terminal/session-owned backend files.
- Put config editing and conversion in config-owned backend files. Separate global or tenant config handlers from environment config handlers when they mutate different store objects or depend on different lifecycle state.
- Put state/read-model assembly in read-model-owned backend files. This includes initial UI state, version suggestions, Kubernetes context listing, endpoint formatting, and build details.
- Keep pasted-image handling near terminal/session ownership when it depends on the active terminal selection.
- Keep desktop backend moves package-local unless a real shared abstraction is being introduced. Organizational splits inside `erun-ui` should stay in package `main`.

## Command Completion And State-Refresh Wiring

- The desktop runs CLI work through two distinct PTY lifecycles, and they have different completion semantics. Dedicated PTYs spawned by `StartSession`/`StartAISession` exit when the underlying process ends, so `handleTerminalExit` and `streamSession` hooks fire normally. Commands piped into the shared Local shell via `runErunCommandInLocal` (`StartInitSession`, `StartDeploySession`, `StartSSHDInitSession`, `StartDoctorSession`) do NOT produce a PTY exit when the underlying `erun <cmd>` finishes — the shell stays at a prompt for the next command.
- Do not gate state-refresh logic (sidebar reload, env open, dependent tab spawn, activity finalization) on `handleTerminalExit` for piped-into-shell commands. The exit branch is unreachable under the shared-shell model and the user sees stale UI when the command succeeds. This has already caused a real regression where `erun init` completed successfully but the new environment never appeared in the sidebar.
- For state-change signals from piped CLI commands, use the trace-line contract that `feedActivityTraceFromTerminal` and `newActivityTraceLineHandler` already parse for the activity queue (`==> Deploying ...`, `==> Deployed ...`, `==> Skipping ...`, etc.). Treat the structured lines as a public API: the CLI must emit them on every code path that should signal state change, integration goldens must lock them in, and the desktop must parse them deterministically.
- When adding a new desktop-observed signal (e.g. init success), emit a stable structured trace line from the CLI command in `erun-common`/`erun-cli`, add the matcher in `activity_queue_app.go`, fire an explicit Wails event for the frontend to listen to, and regenerate integration goldens with `UPDATE_GOLDEN=1`. Put the reactive frontend logic in the controller wired through that event, not in `handleTerminalExit`.
- Remove `TerminalSessionRegistry.trackXSession` methods, the matching field in `TerminalExitSelections`, and the corresponding branch in `handleTerminalExit` when their last caller disappears. A registry tracking method with no callers means the exit handler branch is unreachable, which masks a missing state-refresh path under the new execution model.
- `ensureDefaultEnvTabs`, `spawnERunTabPassive`, `StartSession`, and `StartAISession` require the env config to exist on disk. Do not invoke them eagerly from `activateLocalAfterCommand` for flows that create the env from inside the PTY (`erun init`, first-time `erun deploy`, unconfigured `erun sshd init`). Spawn dependent tabs only after the success signal arrives. Do not wrap these spawns in empty `catch {}` blocks — silent swallowing of "env not found yet" errors hides this ordering bug.
- When refactoring command execution to switch between dedicated-PTY and shared-shell models, manually validate the full happy path in the desktop app: the command runs to completion in the new model, the sidebar reflects the new state, all expected tabs appear, and the terminal returns to a usable post-command state. Integration goldens cover the CLI's dry-run output, not desktop UI reactions to real-run completion.
- The env-create path branches on whether `erun init` already deployed the runtime, keyed on `environmentTypeIsRemoteWorktree(env.type)` after the create-time reload (`handleEnvironmentInitialized`):
  - **Remote-worktree envs (remote-agent / runtime):** `erun init` deploys the runtime itself — carrying the desktop's `--mcp-auth-public-key` and the resolved cluster registry — and waits for it to be Available before emitting `==> Initialized`. So the handler OPENS the env directly; it composes no deploy. `open` stays a pure primitive (it does not deploy). Do **not** compose a second deploy here: `erun init` already deployed the runtime, and a re-render (adding mcp-auth + the cluster registry) rolls the pod init just created — the double-deploy defect.
  - **Local-agent (builds-here) envs:** `erun init` does not deploy (no in-pod build), so after `==> Initialized` the handler composes the single build→push→deploy (`startInitialDeploySelection` → `maybeStartDeployOrchestration`), records the env as pending-open, and opens its tabs only on the matching `environment-deployed` signal (emitted from `finishDeployByTenantEnv` on a succeeded/skipped deploy). Do **not** open a local-agent env's tabs before that signal — they would spawn against a runtime that does not exist and fail with an MCP port-forward timeout.
- Resolve orchestrator conversations attached → tracked → anchor through `resolveOrchestratorConversation` on restore, start, crash respawn, and restart. The deterministic anchor is a first-launch fallback, not proof of the live conversation.
- Pair conversation tracking's writer and reader in the same change. The desktop creates a per-launch nonce and accepts a hook's observation only when its echoed nonce matches the durable launch entry; stale/replaced sessions must not control resumes.
- Surface non-default conversation resolution and failed explicit attachments to the operator. Preserve the manage dialog's Conversation recovery control.
- Session liveness comes from the pod's dtach socket plus live program (`RemoteAppSessionHeartbeatScript`), not stream silence. `session_heartbeat.go` owns authoritative busy-latch release and rendered counts. Silence may latch busy on, but may decide off only without an authoritative observation. Keep the stale-PTY reconciler distinct.
- Pod status reads that must work when the env is unhealthy go over `deps.execRuntimePod` (kubectl exec), not `deps.runPodRaw` (the MCP edge). The edge needs a port-forward and a healthy in-pod server, which is exactly what is missing when a session looks stale.
- Mirror the pod's worktree, not its index: subtract `git ls-files -dz` entries from indexed listings. A fetch failure must not suppress deletion reconciliation.
- Every `syncWorkspaceOnce` pass emits one bounded diagnostic with inputs, counts, and errors, never per-file logs.
- The shared runtime "ensure" (`ensureEnvRuntimeOnce`) is now a thin reconnect, not a deploy preflight: it rebinds the MCP/API forwarders against the already-deployed runtime. A failed ensure must be **surfaced**, not swallowed — set env-status failed + post an actionable notification — and must **not** stamp the dedup TTL on failure (so the next tab open retries). Discarding the error and TTL-stamping on failure (the pre-#644 behaviour) masks an undeployed env behind a downstream port-forward timeout.

## Runtime Usage Accuracy

- Runtime usage samples the runtime container's cgroup, not the dind sidecar doing builds. A low reading is not evidence of idle build capacity; inspect both resource domains.

## Frontend Workflow

- Use Yarn for dependency management and frontend builds. Do not introduce `npm` or `pnpm` lockfiles unless the user explicitly asks for a toolchain change.
- The shadcn primitives, `components.json`, `lib/utils.ts`, and `styles/theme.css` live in `erun-kit`, not here — see `erun-kit/AGENTS.md`. Run shadcn commands and `yarn shadcn:check` from `erun-kit`, never from `erun-ui/frontend`.
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
- Workflow classes should receive explicit dependencies and callbacks rather than importing component state, mutating global singletons, or reaching back through broad controller references.
- Keep frontend workflow moves behavior-preserving: preserve timers, busy flags, notifications, emitted state updates, retry state, and focus/resize side effects.
- Put terminal output buffering, display filtering, and session bookkeeping in terminal-owned modules. Keep controllers responsible only for connecting terminal events to app state.
- Put review diff navigation and DOM viewport selection in review-owned modules. Keep generic diff parsing and formatting in diff helper modules.

## Frontend Component Discovery

Follow the shared discovery rules in `erun-kit/AGENTS.md`; also inspect desktop
wrappers and `VersionField` before inventing editable pickers. Verify native
browser controls in the Wails WebView, not only Chromium.

## Frontend Styling

Follow kit's shared styling rules. Keep desktop CSS for Wails drag/resize hooks,
xterm internals, and runtime-sized panel variables; app theme extensions stay local.
Do not add layout-affecting padding/borders to the element passed to
`Terminal.open`: FitAddon does not subtract that padding. Put it on a wrapper.
Styling changes run frontend build and Go tests; primitive changes also run kit drift checks.

## Professional UX

Apply `erun-kit/AGENTS.md` §§ "Professional UX" and "Design-Language Decision Record".
Reach the hosted API through `eruncommon.PlatformClient`, not desktop-local HTTP.
Map capabilities to surfaces in the Go read model; components render the resolved
per-panel outcome rather than recomputing permissions.

## UX Impact Review Checklist

Apply the shared checklist in `erun-kit/AGENTS.md` to every user-triggered path.
Desktop item 8 requires an accompanying Playwright spec. Config-shaped state is
stageable; only genuinely live/native dependencies justify a disclosed gap.
Check especially that shared-shell completion refreshes the sidebar, newly
selected environments exist in the rendered model, and progress survives modal
closure instead of flashing then disappearing.

## Design-Language Decision Record

Shared status, notification, permission, and confirmation decisions live in
`erun-kit/AGENTS.md`. Desktop reference implementations include
`ActivityQueueDrawer` for dialog focus/transition announcements and the shared
sidebar busy/condition glyphs.

### The keyboard model the review surface still owes

New review interactions need keyboard equivalents for file/hunk navigation,
reply, resolve/unresolve, and starting review. Extend the existing
`reviewKeyboardShortcuts.ts` / `reviewDiffKeyboardNav.ts` owners rather than
assuming focusability alone supplies keyboard operation. Do not describe the
review surface as having no key handlers.

### What to preserve

Keep dialog focus restoration, narrow live announcements, and consistent sidebar
activity/status indicators. Expected blocks are status notices; attempted failures
are alerts.

## Build And Packaging

- Keep the module build script as the canonical local and release-facing desktop build entrypoint.
- Preserve the installed binary name `erun-app` unless the repository explicitly changes launcher and package-manager integration too. `erun app`, Homebrew, and Scoop wiring depend on that artifact name.
- Keep macOS and Windows packaging assumptions aligned across the module build scripts and package-manager metadata.
- When changing Darwin CGO or deployment-target behavior, align compile and link settings together so local builds, package-manager builds, and Wails builds do not drift.
- Sign macOS builds with the stable identity owned by `codesign.sh`, never ad-hoc: changing identity/hash can invalidate TCC grants silently. Reuse the existing keychain identity, report its name, sign bundle and bare binary, and preserve the script's explicit diagnostic/recovery on failure.
- Re-sign through `codesign.sh` / `eruncommon.SignHostArtifact`. Keep identity name, keychain path, and password aligned with common and `TestLocalSigningIdentityMatchesTheSharedContract`.
- Preserve the platform constraints tested by `codesign_script_test.go`:
  - Use the same non-empty PKCS#12 password at export/import.
  - Append the keychain idempotently to the user's search list, preserve other entries, and leave it available for later artifact signing.
  - Do not use `security find-identity -v` as a self-signed-identity presence check; keychain-file presence determines reuse.
- Report each signing step and its stderr; do not hide identity-chain failures.

## Validation

- Go/backend changes: `go test -count=1 ./...`. Root `test-erun-ui` and lint gate
  this module independently; sibling-module test commands cannot reach it.
- Frontend changes: `yarn typecheck && yarn lint && yarn format:check && yarn build &&
  yarn test` in `frontend/`; root `test-frontend` runs the same gates.
- Observable interaction/lifecycle changes also run `./playwright/run.sh`
  (force `--build` after backend edits). See the coverage contract below.
- Packaging, Wails, CGO, or asset embedding changes run `./build.sh <target>`.
  Align compile/link deployment targets and package-manager metadata.
- `--skip-lint` is a visible per-invocation local-iteration option, never a CI or
  final-validation bypass and never inherited through an environment variable.
- Guidance-only changes follow root scope; embedded instructions run provisioning tests.

## Lint, Format, Typecheck

Checked-in ESLint, Prettier, and TypeScript configs are authoritative; fix code,
not rules. Use `yarn lint:fix` and `yarn format` for mechanical fixes.
Playwright has its own package/configs and adds its test-specific lint rules.

## End-to-end UI tests

- Observable frontend or backend-to-frontend changes require Playwright assertions
  in the same PR. Type checks and Go tests cannot prove rendered behavior.
  Exemptions are zero-observable-surface changes and must be stated in the PR.
- `./playwright/run.sh` is the canonical runner; see its guide for flags, fixtures,
  area selection, and isolation. It reuses the binary unless `--build` is passed.
- Stage tenant/environment/config-shaped cases. For genuinely live-only branches,
  cover the nearest observable invariant, explain the gap in the spec, and name
  the Go test owning the inaccessible branch.
- Root `test-playwright` is wired into `check-gate`; no manual attestation replaces
  it. Build-selected areas are not proof that the full suite ran.
- Headless HTTP/SSE tests do not exercise Wails native windows, menus, WebView
  lifecycle, or signing. Windows cross-compilation in the gate proves compile/link
  only; macOS native validation requires an Apple SDK host.
- Changes to real create → deploy → open → MCP flow also run the opt-in k3d mode
  under `playwright/AGENTS.md`; it is separate from the inert default suite.

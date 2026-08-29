---
title: Skills spec
---

# Skills spec

> For the Operator view, see [Skills](/concepts/skills).

A **skill** is a directory containing a `SKILL.md` plus optional reference content. ERun owns one canonical source of skills (`erun-skills/skills/<name>/` in the `sophium/erun` repository) and publishes them through two paths: baked into the runtime image for in-pod use, and via a Claude Code plugin marketplace for laptop use. The Agent — Claude Code, Codex, or whatever the env's `aitool` selects — discovers them through its own skill-loading convention.

This page specifies: the on-disk format, the per-tool discovery paths, the deployment mechanism the runtime chart uses, the marketplace distribution contract, the layering rules, and the built-in skill catalogue.

## SKILL.md format

A skill bundle is a directory:

```
<skill-name>/
├── SKILL.md           # required; the entrypoint the Agent reads
├── references/        # optional; longer-form reference content the SKILL.md cites
│   └── ...
├── examples/          # optional; worked examples the Agent can use as starting points
│   └── ...
└── scripts/           # optional; helper scripts the Agent can invoke as part of applying the skill
    └── ...
```

`SKILL.md` has YAML frontmatter followed by a markdown body:

```markdown
---
name: go-service
description: Write a conformant Go HTTP service with the ERun multi-stage Dockerfile and helm chart layout. Use when the Operator asks to add a Go service, gRPC service, or background worker written in Go.
---

# Go service

When you're adding a Go service to an ERun project, follow this layout.

## Source module

Live at `<projectRoot>/<name>/`. Layout:
…

## Dockerfile

Live at `<projectRoot>/<tenant>-devops/docker/<name>/Dockerfile`. Multi-stage…

…
```

| Field | Required | Validation |
|---|---|---|
| `name` | yes | Matches `^[a-z][a-z0-9-]*$`. Must equal the parent directory name. Uniquely identifies the skill. |
| `description` | yes | One sentence; under 200 characters. This is what the Agent reads to decide whether the skill applies — phrase it as **when** to use the skill, not **what** the skill contains. |

The body is plain markdown — the same content the Agent would read as instructions. Sections, lists, code blocks, links to reference files all work.

## Per-tool discovery paths

The `erun-devops` container's entrypoint copies each skill into the conventional location for the env's configured Agent on every env start. The Agent picks them up automatically — no extra flag or config needed.

| Agent | Discovery path inside the runtime pod |
|---|---|
| `claude` | `~/.claude/skills/<skill-name>/SKILL.md` |
| `codex` | `~/.codex/skills/<skill-name>/SKILL.md` |

Both paths are installed in parallel from the same `/etc/erun/skills/<name>/` source baked into the image. A generic canonical path under `~/.config/agent-skills/` is (Planned.) for future tools.

## Source module

Skills live in exactly one place in the source tree: `erun-skills/skills/<skill-name>/` in `sophium/erun`. Both the runtime image and the plugin marketplace vendor from this directory. Editing a skill is a single-file change in that module; the two distribution paths pick it up automatically.

Module guidance: [`erun-skills/AGENTS.md`](https://github.com/sophium/erun/blob/main/erun-skills/AGENTS.md).

## Deployment mechanism

Two paths deliver the skill set to the Agent:

### In-pod (runtime image)

The runtime Dockerfile vendors the whole tree with one line:

```dockerfile
COPY --chmod=0644 erun-skills/skills /etc/erun/skills
```

On every entrypoint run, `initialize_claude_config` and `initialize_codex_config` run `skills-install.sh` over every subdirectory under `/etc/erun/skills/`, installing each skill into `~/.claude/skills/<name>/` and `~/.codex/skills/<name>/`. Supporting files (templates, helper scripts) inside the skill directory ship with the skill automatically.

The install both **installs a skill when absent and refreshes it when the baked copy changed**, so a rebuilt image's updated skill reaches existing envs — while **preserving in-pod edits**. Provenance is tracked per skill by recording the baked `SKILL.md` hash in a `.erun-skill-baked-sha256` marker: a copy whose `SKILL.md` still matches its marker is unmodified since erun installed it and is refreshed to the baked version, while one that differs was edited in-pod and is left untouched (a legacy copy with no marker is treated as unmodified and adopted on the first refresh). So an un-edited skill tracks the image across upgrades, and a skill you edit inside a running env survives both pod restarts and image rebuilds.

### Host orchestrator (desktop)

The desktop app installs the same canonical skills into the host's `~/.claude/skills/<name>/` for host-side orchestrator sessions, using the identical marker-based install-or-refresh — so a host orchestrator tracks the latest skill on each launch while preserving any host-side edits.

The source it installs from resolves in this order, first match wins:

1. `ERUN_SKILLS_DIR`, if set — taken **verbatim, with no fallback**, so pointing it at an empty directory installs nothing rather than silently resolving something else.
2. The `erun-skills/skills` directory of the checkout the desktop binary was built from, stamped into the binary by `erun-ui/build.sh` / `build.ps1`. This is what keeps a desktop that runs from outside its checkout — the usual case, since the built bundle is copied elsewhere to run — installing the skills its own build ships.
3. `erun-skills/skills` found by walking up (max 8 levels) from the running executable, for a binary that was built without the stamp but sits inside a checkout.

If none resolves, the orchestrator still launches and the skills already installed are left alone — but the condition is **reported, not silent**: a warning notification naming the checkout that was expected, where the executable looked, and the two recoveries (set `ERUN_SKILLS_DIR`, or rebuild with `erun-ui/build.sh` / `build.ps1`) is posted once per desktop run, and every occurrence is logged. A build that silently stopped refreshing skills is indistinguishable from one where the skill had not changed. A desktop installed from a package manager carries no checkout, so `ERUN_SKILLS_DIR` is its only source.

The desktop also writes a `SessionStart` hook into the shared orchestrators workspace's `.claude/settings.json` that **injects the operating contract** — it prints the workspace `CLAUDE.md`, and then the orchestrator's own `CLAUDE.<id>.md` role file if it has one, to the session on every session boundary (Claude Code's `SessionStart` fires with source `startup`, `resume`, `clear`, and `compact`, and all four are re-injected) — so the contract and the standing role are always already in context rather than a [`erun-orchestrate`](#erun-orchestrate) skill the model is merely asked to load and could skip, and neither one silently drops out of context after a `/clear` or a compaction. The hook prints the files directly (plain stdout, so no `additionalContext` size cap), falling back to a short directive if the shared `CLAUDE.md` is ever missing.

Guidance is two layers, injected shared-then-specific so the ordering is the precedence rule. `CLAUDE.md` — described above — is the one shared contract every orchestrator obeys; erun rewrites it on every launch, so an edit there is discarded on the next one. `CLAUDE.<id>.md`, in the same shared orchestrators workspace, is this orchestrator's own standing role: erun seeds it once, with a short comment explaining the convention, and never rewrites it afterward — the deliberate inverse of the shared file. The hook prints it immediately after `CLAUDE.md`, so anything in it can add to the contract or override a line of it. `<id>` is the orchestrator's internal id, not its display name — an orchestrator the sidebar shows as `erun-admin` can carry id `erun-issues`, so its role file is `CLAUDE.erun-issues.md` — which is why the desktop's orchestrator management dialog resolves and opens both files by id rather than asking the operator to know the filename convention (see [Desktop app · Orchestrators](/desktop/orchestrators) for the Operator-facing view).

#### Which conversation a launch resumes {#orchestrator-conversation-resolution}

> For the Operator view, see [Desktop app · The conversation an orchestrator comes back to](/desktop/orchestrators#orchestrator-conversations).

Every launch of an orchestrator resumes a named conversation rather than `--continue`'s most-recent one, which in a shared workspace collapses every orchestrator onto one session. The name is resolved from three sources, in this order.

**The anchor (derived).** `uuid5(6f7e9c2a-1b3d-4e5f-8a9b-0c1d2e3f4a5b, <orchestrator id>)`. A pure function of the id, so it is identical on every launch and on every machine, needs nothing on disk, and cannot be written by another session. It answers *which conversation is this orchestrator's by convention* — and only that. A transient (Investigate) session has no id, so it has no anchor and starts unpinned.

**The tracked conversation (live).** The harness does not always adopt the id it is asked to resume; a launch that asks for the anchor can end up writing to a conversation of its own, after which the anchor's transcript stops growing while the work accumulates elsewhere. Only the session knows which one it is writing to, so the session reports it:

- The desktop mints a **launch nonce** (a v4 UUID) per launch, exports it as `ERUN_ORCHESTRATOR_LAUNCH` beside `ERUN_ORCHESTRATOR_ID`, and writes it into its own durable open-set record (`orchestrator-open.json`, `launchId` per entry) as the session is spawned.
- A hook installed on `SessionStart`, `UserPromptSubmit`, `PreToolUse`, `PostToolUse` and `Stop` reads `session_id` from the hook's own stdin JSON and writes `{"conversationId":…,"launchId":…,"atUnix":…}` to `<UserConfigDir>/ERun/orchestrator-live/<id>.json`. Session boundaries *and* turn boundaries, because the id can change mid-run; bare shell, so it works with erun off `PATH`; every failure path is `|| true`, since a hook that could wedge a session costs more than a missed record.
- A record counts only while `launchId` equals the `launchId` on that orchestrator's open-set entry. That makes the two halves independent: the desktop writes the nonce, the session writes the conversation, and neither is authoritative alone. A session that never saw this launch's nonce cannot claim the orchestrator's conversation however it came by the id, and a store whose writer is removed stops counting on the very next launch — the two failure modes of the record this replaces.

**The attachment (operator).** A conversation the Operator attached through the manage dialog. Durable on the open-set entry (`attachedConversationId`), so a later launch honours it instead of recomputing the anchor, and cleared by asking for the default back. It outranks both of the above.

Resolution is: attachment if usable → tracked record if confirmed → anchor. A candidate ahead of the anchor must clear two checks — its transcript is still on disk, and no *other* configured orchestrator has a claim on it (that orchestrator's anchor, its attachment, or its own tracked record) — because the cost of the wrong answer is another orchestrator's history presented as this one's. Recency is never an input: the transcript directory holds conversations belonging to several orchestrators and to none, so "the newest file" is usually somebody else's.

**Nothing falls through silently.** A resolution that is not the plain answer carries an operator-facing notice — surfaced beside the orchestrator list on a restore, and as a notification on an ordinary start:

| Outcome | Notice |
|---|---|
| Tracked conversation resumed | Names the conversation resumed and the anchor it beat. It is the good outcome; the two ids disagree, and only the Operator can tell whether the winner holds the work they expect. |
| `launchId` missing or from another launch | Resumes the anchor, names the unconfirmed conversation, and says nothing has confirmed it since. |
| Tracked transcript no longer on disk | Resumes the anchor and says the transcript is gone. |
| Tracked conversation claimed by another orchestrator | Resumes the anchor and names the orchestrator that owns it. |
| Attachment unusable (either of the two checks above) | Resumes the anchor and says the Operator's own choice could not be honoured. |

The restart hand-off (`orchestrator-restore/<id>.json`) records the conversation the running session reports being on under its launch, not the id it was spawned with — a restart is the one path that must reach the session that asked for it. The crash respawn keeps the same nonce, since it is the same launch continuing.

**Listing and attaching.** `ListOrchestratorConversations(<id>)` reports what one orchestrator could resume, and is the surface that makes a wrong resume correctable rather than terminal:

| Field | Meaning |
|---|---|
| `resuming` / `resumingSource` | The conversation a launch resolves right now, and which of `attached` / `tracked` / `derived` produced it. |
| `attached` | The Operator's standing choice, empty when there is none. |
| `notice` | The same notice the resolution would surface, so the picker explains the state it is showing. |
| `conversations[]` | Per conversation: `conversationId`, `folder`, `lastWrittenUnix`, `sizeBytes`, `excerpt`, `role`, `resuming`. |
| `omittedNotMine` / `omittedForCap` | How many rows were withheld because another orchestrator claims them, and how many older ones the 60-row cap dropped. Stated, never silently trimmed. |
| `transcriptsRoot` | `~/.claude/projects`, the directory the rows were read from. |

`role` is one of `attached`, `live` (tracked and confirmed), `stranded` (recorded as live by a launch nothing can confirm — the row most likely to hold unreachable work), `derived`, or `unowned`. Enumeration globs `~/.claude/projects/*/*.jsonl` across **every** project directory, since a conversation id is globally unique and an orchestrator's own conversation may have been started elsewhere. `folder` comes from the transcript's own recorded `cwd`, never decoded from the project directory name — the harness encodes a path by replacing each separator with `-`, which no decode can round-trip for a path that already contained one. `folder` and `excerpt` are read from the first 128 KiB only, so listing a directory of multi-megabyte transcripts stays cheap; the anchor is always a row even before its first write.

`AttachOrchestratorConversation(<id>, <conversationId>, cols, rows)` records the choice and restarts the orchestrator in that conversation; `DetachOrchestratorConversation(<id>, cols, rows)` clears it and restarts on whatever resolves. Attaching a conversation another orchestrator claims is **refused**, with the owner named — recovering a mis-attached orchestrator is what this exists for, and crossing two orchestrators is what it exists to prevent.

#### Periodic pacing re-statement and crash auto-resume

> For the Operator view, see [Workflow · Level 3](/collaboration/workflow#level-3).

The `SessionStart` hook above closes the gap at session *boundaries* (`startup`, `resume`, `clear`, `compact`). It re-states nothing in the (often long) stretch between two boundaries, so the desktop's session-heartbeat poller — the same 15-second tick that reconciles each orchestrator's busy/shell-activity reports — also re-states the pacing contract directly into a live session whose own report has gone stale, and separately relaunches a session whose process exited from a failure.

**Staleness read.** Each orchestrator's turn-boundary activity report (the same file the busy-spinner reconciler reads, `{"busy":…,"atUnix":…}`) is read again here, independent of that reconciler's own busy/idle staleness bounds: a report not refreshed for **10 minutes** (`orchestratorPacingStaleAfter`) reads as quiet regardless of whether its last write said busy or idle. The reference point is the later of the report's own write time and the session's launch time, so a session with no report yet (its first turn hasn't reached a boundary) is not nudged before it has had ten minutes to.

**Nudge.** For each live, non-transient orchestrator whose report is stale, the desktop writes this text into the session's pty, then — after a short settle, as a **separate** write — a bare carriage return to submit it:

> Keep pacing yourself, on connection errors wait and resume, do not exit this loop. If the assigned task is already complete and verified, say so in one line and stop.

A background shell reported running for that orchestrator (`orchestrator-shell-activity`) no longer holds the nudge back. It used to: the reasoning was that a shell left running was evidence the orchestrator meant to be quiet, but a background shell is a fact about the *shell*, not about the turn behind it — a long-running build is the single most likely thing still running when a turn dies mid-response, so the old rule suppressed the nudge exactly when it was needed most. The shell-activity report is unchanged and still drives the shell indicator; pacing simply stopped reading it.

Each nudge appears in the pane as a dim marker line naming the attempt count and the **measured** quiet period — never the ten-minute constant — so a session quiet for twenty minutes reads as twenty minutes, not as the contract having been honoured on time:

```
── pacing nudge N/6 sent — no activity report for 19m42s ──
```

If the orchestrator's last report said `busy: true` and was never followed by an idle one, the marker names that too, since a report stuck on "busy" for a full staleness period usually means the harness never reached its own turn boundary (a dropped connection, a crash) rather than a turn that finished and simply stopped reporting:

```
── pacing nudge N/6 sent — no activity report for 19m42s — last report said mid-turn, so the turn may have died without one ──
```

**Bounds.**

- One nudge per orchestrator per tick, and never for a transient (Investigate) session — it has no `id` and its own bounded lifecycle belongs to the investigation registry, not this one.
- A cap of **6** consecutive un-answered nudges (`orchestratorPacingMaxNudges`) — about an hour at the 10-minute interval. Crossing the cap posts a warning notification and a distinct pane marker instead of a 7th nudge, and the cap latches (no repeat notification on later ticks) until it is rearmed by either:
  - a fresh turn-boundary report whose own timestamp is later than the last nudge **and** says `busy: true` — evidence the session resumed on its own, or
  - real operator input sent into that same pane (`SendSessionInput`) — evidence the operator is now at the keyboard.

**Every decision is logged, not just the ones that nudge.** The reconciler names the reason it did or didn't nudge — alive-but-fresh, not alive, already capped, crossing the cap, or nudging — in the desktop's durable app log, once per transition rather than once per 15-second tick. A stalled pane that never nudges (because the session itself is no longer alive) is therefore distinguishable from a healthy, ordinarily quiet one by reading that log, instead of both looking identical from outside.

**This pass is neither the only way to trigger it nor fixed at these values.** The message, the ten-minute threshold, and the cap of 6 are `~/.erun/config.yaml`'s `whip` section (`eruncommon.WhipConfigOverride`) — unset by default, so an install that configures nothing keeps exactly the text and bounds above. [`erun whip`](/cli/whip) is the manual, visible counterpart: an operator-triggered pass over the same decide/report core (`eruncommon.DecideWhip`) that pushes every environment's own AI session (over its `whip` MCP tool) plus every persisted orchestrator, reporting each target's outcome by name — pushed, or skipped and why — rather than only reporting that it ran. A session nudged this way sees the exact same text as the automatic pass; there is nothing in the message itself that distinguishes an automatic re-statement from an operator-triggered one, so treat any pacing nudge as erun restating a contract you already agreed to, not as a message from your operator.

**Auto-resume after a crash.** An orchestrator's managed session carries a respawn closure (the same `tryReconnect` mechanism environment tabs use to recover a dropped pod session), gated so it only ever fires for a genuine failure:

- **A clean exit never respawns.** `Wait()` returning `nil` (`terminalSessionExitReason` answers `""`) means the operator quit the TUI from inside the pane, not a crash.
- **A torn-down registration never respawns**, even carrying a real failure reason: the closure refuses unless the orchestrator's config-map entry and its managed-terminal registration are both still exactly what they were when the session was spawned. `StopOrchestrator` deletes both under the same lock a Stop takes, so Stop always refuses its own respawn — including a respawn already in flight when Stop is called, since the same check runs again after the relaunch attempt returns.
- **A transient (Investigate) session never respawns** — it carries no respawn closure at all; its lifecycle is the investigation registry's to end.

When it does fire, the closure relaunches through the same launch path a fresh start uses, resuming whatever conversation the crashed session last reported under this launch's nonce rather than only the id it was spawned with (see [Which conversation a launch resumes](#orchestrator-conversation-resolution)), and hands the relaunched session a resume prompt distinct from the restart-hand-off one above (it names no return note, since nothing wrote one for an involuntary crash):

> This session's process just exited unexpectedly and was relaunched automatically. Resume the conversation exactly where it left off and carry any in-progress task through to its verified end without waiting to be asked.

The crash-fallback shell command itself was also closed: `buildOrchestratorLaunch`'s primary launch already resumes a pinned session id (`--resume <id>` if the conversation exists, `--session-id <id>` to create it) with a `||`-chained (PowerShell: `$LASTEXITCODE`-checked) shell-level fallback for the same single invocation. That fallback now retries the identical pinned invocation rather than ever dropping to an unpinned `claude`, since an unpinned session is what the live-conversation recorder (above) would then report as this orchestrator's conversation — silently swapping it onto an amnesiac one on the very first in-shell retry. Only a transient/legacy launch (no pinned id) still falls back to plain `claude`.

### Laptop (plugin marketplace)

The repo-root file `.claude-plugin/marketplace.json` publishes `erun-skills/` as the `erun-tools` plugin via a `git-subdir` source. Users add the marketplace and install:

```bash
/plugin marketplace add sophium/erun
/plugin install erun-tools@sophium/erun
```

Skills become invocable as `/erun-tools:<skill-name>` (e.g. `/erun-tools:erun-file-issue`). See [Marketplace distribution](#marketplace) below for the full schema and update flow.

### Layering (Planned.)

Tenant-level skills (`<projectRoot>/<tenant>-devops/skills/<name>/`) and project-level skills (`<projectRoot>/.erun/skills/<name>/`) are reserved as future layers above the in-pod baked set. The current implementation ships only the runtime-image baked set; tenant and project layers are not yet mounted.

## Marketplace distribution {#marketplace}

The plugin is published from `sophium/erun` itself — the repo *is* the marketplace. No second repository to maintain.

### `.claude-plugin/marketplace.json`

At the repo root. Schema (current, as published):

```json
{
  "$schema": "https://anthropic.com/claude-code/marketplace.schema.json",
  "name": "sophium/erun",
  "description": "ERun skills for Claude Code …",
  "owner": { "name": "Sophium", "email": "maintainers@sophium.com" },
  "plugins": [
    {
      "name": "erun-tools",
      "description": "…",
      "category": "development",
      "source": {
        "source": "git-subdir",
        "url": "https://github.com/sophium/erun.git",
        "path": "erun-skills",
        "ref": "main",
        "sha": "<commit-sha>"
      },
      "homepage": "https://github.com/sophium/erun/tree/main/erun-skills"
    }
  ]
}
```

Fields:

| Field | Required | Notes |
|---|---|---|
| `name` (marketplace) | yes | `sophium/erun`. Used by `/plugin marketplace add` and as the suffix in `/plugin install <plugin>@<marketplace>`. |
| `owner` | recommended | Display info for the catalogue UI. |
| `plugins[].name` | yes | `erun-tools`. Used as the namespace prefix for skill invocation (`/erun-tools:<skill>`). |
| `plugins[].source.source` | yes | `git-subdir` so the plugin can live in a subdirectory of the marketplace repo. |
| `plugins[].source.url` | yes | HTTPS git URL — must be cloneable without credentials for public marketplaces. |
| `plugins[].source.path` | yes | `erun-skills`. The plugin root inside the marketplace repo. |
| `plugins[].source.ref` | yes | Branch (`main`) used to resolve the SHA on update. |
| `plugins[].source.sha` | yes | Pinned commit hash. Users only see updates when this changes. |
| `plugins[].homepage` | recommended | Browseable URL for the plugin source. |

### `erun-skills/.claude-plugin/plugin.json`

The plugin manifest. Skills are auto-discovered from `erun-skills/skills/`; they do not need to be enumerated.

```json
{
  "name": "erun-tools",
  "version": "1.0.0",
  "description": "…",
  "author": { "name": "Sophium", "email": "maintainers@sophium.com" },
  "homepage": "https://github.com/sophium/erun/tree/main/erun-skills",
  "repository": "https://github.com/sophium/erun",
  "license": "MIT"
}
```

### Update flow

1. Edit a skill in `erun-skills/skills/<name>/SKILL.md`.
2. Commit and merge to `main`.
3. The release flow bumps `source.sha` in `.claude-plugin/marketplace.json` to the merge commit. (Until release automation handles this end-to-end, the bump lands in the same PR as the skill edit.)
4. Users see the update on next `/plugin marketplace update sophium/erun`.

Auto-update for third-party marketplaces is disabled by default in Claude Code; users either opt in via the `/plugin` UI or run `/plugin marketplace update` manually.

### Install commands

```bash
/plugin marketplace add sophium/erun                            # add once
/plugin install erun-tools@sophium/erun                         # install plugin
/reload-plugins                                                 # pick up mid-session
/plugin marketplace update sophium/erun                         # refresh catalogue
/plugin uninstall erun-tools@sophium/erun                       # remove
```

### Error behaviour

| Failure | What the user sees | Recovery |
|---|---|---|
| Marketplace repo unreachable (network, auth) | `/plugin marketplace add` fails with the upstream git clone error. | Check network / `gh auth status`; retry. |
| `marketplace.json` malformed JSON | `/plugin marketplace add` fails with the parse error and the offending line. | File an issue against `sophium/erun`. |
| `source.sha` no longer reachable (history rewritten) | Install fails with a "commit not found" error. | Re-run `/plugin marketplace update sophium/erun` to fetch the latest SHA. |
| Skill name collision with an existing user-installed skill of the same name | Claude Code namespaces the plugin-shipped skill as `/erun-tools:<name>`, so collisions are not possible at the invocation layer. | n/a. |
| Plugin install succeeds but no skills appear | `/reload-plugins` may be needed. If still missing, the plugin manifest may have rejected the install — check `/plugin` UI for an error entry. | Run `/plugin` and inspect the **Errors** tab. |

### Codex distribution (Planned.)

Codex CLI does not have an analogous plugin marketplace yet. Inside a deployed env, Codex receives the same skills via the runtime-image baked install — no extra step. For laptop Codex use, copy `erun-skills/skills/<name>/SKILL.md` into `~/.codex/skills/<name>/` manually until upstream Codex ships plugin support.

## Layering rules

| Conflict | Resolution |
|---|---|
| A project skill has the same `name` as a built-in. | Project skill wins. The built-in is hidden in this env. |
| A tenant skill has the same `name` as a built-in. | Tenant skill wins; project skill (if also present) wins over tenant. |
| Two skills in the same layer have the same name. | Sort lexicographically by source path; later wins. (This is a misconfiguration — flag in `erun doctor`.) |
| A skill's `name` frontmatter doesn't match its directory. | The skill is **skipped**; `erun doctor` reports `SKILL_NAME_MISMATCH`. |

## Built-in skill catalogue

The current v1 set, shipped both in the runtime image (`/etc/erun/skills/`) and via the plugin marketplace. Skills come in two semantic kinds:

- **Blueprint skills** — package ERun's accumulated best practices for building complex industry-strength solutions.
- **Workflow skills** — let users participate in ERun's processes (report problems, share improvements back so other users benefit).

### `erun-file-issue`

| Field | Value |
|---|---|
| Kind | Workflow — participate in ERun's issue-reporting process. |
| Source | `erun-skills/skills/erun-file-issue/SKILL.md` |
| Description | "Register or file a bug or feature request for the ERun project itself on GitHub." |
| Triggers | "file an erun bug", "file an erun feature", "register erun bug", "register erun feature", "open an erun issue" |
| Inputs | Issue title; what-happened / what-expected / reproduction (or feature goal + acceptance criteria) |
| Outputs | `gh issue create --repo sophium/erun --label bug` (or `--label enhancement`) invocation with a templated body. Body adapts to context: inside an env it includes `${ERUN_TENANT}`, `${ERUN_ENVIRONMENT}`, and the `ERUN_*` env dump; on a laptop it omits those. |
| Error behaviour | `gh` not installed or unauthenticated → surfaces the `gh auth status` hint and stops. Title or body missing → re-prompts the user. |

### `erun-contribute`

| Field | Value |
|---|---|
| Kind | Workflow — lets users share improvements back to the platform so other users benefit. |
| Source | `erun-skills/skills/erun-contribute/SKILL.md` |
| Description | "Contribute a change to the ERun platform itself — create a new GitHub issue against sophium/erun that captures the work, clone the repo, implement the change following its AGENTS.md rules, and submit a pull request back." |
| Triggers | "contribute to erun", "make a change to erun", "work on erun", "land a fix in erun", "submit a PR to erun", "propose an improvement to erun" |
| Inputs | One- or two-sentence description of the change; sentence-style title; issue type (`bug` / `feature` / `enhancement`); short kebab-case description for the branch name (defaulted from the title) |
| Outputs | A newly-filed issue against `sophium/erun` (Step 1), then a cloned repo at `~/git/erun`, branch `feature/<n>-…` or `bug/<n>-…`, code change, `make integration-test` run, push, PR via `gh pr create --repo sophium/erun --base main` with `Closes #<n>` in the body |
| Error behaviour | `gh issue create` fails (auth, network, label not allowed) → stops; does not proceed to clone without an issue number to anchor the PR. `make integration-test` fails → does not push; surfaces the failure. PR title contains an agent marker (`[claude]`, `[codex]`) → re-prompts the user per `AGENTS.md` § "Pull Request Titles". |

Semantic: `erun-contribute` is **initiator-driven** — the same person who runs it both files the issue and ships the PR. For reporting a problem without intent to follow up, use `erun-file-issue` instead. For picking up an issue someone else filed, no skill applies; the user clones, branches, implements, and PRs directly.

Key contract: the skill **explicitly reads** the cloned repo's `AGENTS.md` and every applicable subtree `AGENTS.md` each time it fires. Claude Code does not auto-reload `CLAUDE.md` after a `cd` mid-session, so this read step is binding.

### `erun-blueprint-agents`

| Field | Value |
|---|---|
| Kind | Blueprint — packages ERun's orientation for a tenant repo (environment model, core commands, artifact locations, the one-version pinning contract, skill pointers). |
| Source | `erun-skills/skills/erun-blueprint-agents/SKILL.md` + `templates/` |
| Description | "Blueprint the repo-root agent-guidance file for an erun tenant project — a canonical AGENTS.md plus a CLAUDE.md symlink (one file, one source of truth) pre-populated with orientation on working in the erun environment — the tenant/environment model, the core erun commands (build, deploy, terraform apply, list, doctor, open), where the deploy artifacts live and the one-version pinning contract, and pointers to the other skills. Idempotent — reconciles a missing or broken symlink and never clobbers hand-authored guidance." |
| Triggers | "scaffold root AGENTS.md", "add erun agent guidance to this repo", "orient this tenant repo for agents", "create the repo-root CLAUDE.md", "set up AGENTS.md for this erun project" |
| Inputs | The repo root (default: current working directory); the tenant + environment to name in the guidance — resolved from `${ERUN_TENANT}`/`${ERUN_ENVIRONMENT}` in-pod or `erun list` on a laptop, else left as generic `<tenant>`/`<env>` pattern text. |
| Outputs | A repo-root canonical `AGENTS.md` (rendered from `templates/AGENTS.md`, with `<tenant>`/`<env>` substituted where resolved) plus a `CLAUDE.md` **same-directory relative symlink** to it (git mode `120000`; blob content is the bare filename `AGENTS.md`) — matching erun's own repo convention that `AGENTS.md` is canonical and `CLAUDE.md` points at it, not the reverse. The content covers the tenant/environment model (agent env vs runtime env, working inside the pod via `erun open`, the `runtimeversion` pin), the core commands (`erun list`/`open`/`build`/`deploy`/`terraform apply`/`doctor`), the deploy-artifact locations (`terraform-<tenant>/<env>/`, `<tenant>-devops/k8s/<tenant>-<component>/`, `<tenant>-devops/docker/<tenant>-devops/Dockerfile`), the one-version pinning contract (the Terraform module `?ref`, each Helm umbrella `Chart.yaml` `version:`, the build-env Dockerfile `FROM`, and the env `runtimeversion`, all bumped together), and pointers to the other skills. Both files are committed to git — never written to `${ERUN_OUTPUTS_DIR}`. The other blueprint skills (`erun-blueprint-api`/`-rls-db`/`-docs`/`-platform`, `erun-build-env`) point at this skill so any scaffold path yields root guidance. The generated file documents the Windows symlink caveat (a symlink-less Windows checkout materializes `CLAUDE.md` as plain text containing `AGENTS.md`; read `AGENTS.md` directly there). Idempotent on re-run: a correct `AGENTS.md` + `CLAUDE.md -> AGENTS.md` symlink is left untouched, and a missing/broken symlink over a present canonical file is recreated. |
| Error behaviour | A hand-authored (regular, non-symlink) `AGENTS.md`/`CLAUDE.md` already at the root → not a stop; never overwrite — report it and offer to fold the erun orientation in with the user's confirmation. Tenant/env unresolvable → write the file with generic `<tenant>`/`<env>` pattern text (still valid). `ln -s` unavailable on a Windows shell without symlink support → the canonical `AGENTS.md` still works standalone; create the symlink via git on a platform that supports it. Not inside a git repository → write the files and surface that they must be committed once the repo is initialized. |

Do not confuse this skill with a **reusable agent** — a different artifact kind, specced at [Reusable agents spec](/agent-reference/agents-spec). This skill scaffolds a repo's `AGENTS.md` guidance file; a reusable agent (e.g. `erun-builder`, `erun-reviewer`) is a standing subagent role.

### `erun-blueprint-rls-db`

| Field | Value |
|---|---|
| Kind | Blueprint — packages ERun's accumulated best practices for multi-tenant PostgreSQL. |
| Source | `erun-skills/skills/erun-blueprint-rls-db/SKILL.md` + `templates/` |
| Description | "Build a multi-tenant PostgreSQL database module following ERun's blueprint — row-level security, Atlas migrations, UUIDv7 surrogate keys, shared timestamp trigger, separate erun_tenant / erun_operations PostgreSQL roles, and the canonical tenant/issuer/user bootstrap that erun-backend-db captures — and maintain, repair, and upgrade a module it previously produced by detecting existing artifacts and entering maintenance mode instead of stopping, filling blueprint gaps without clobbering the project's own tables or committed migrations, and re-pinning the module's own version axes — the PostgreSQL major and Atlas toolchain — to their targets (it has no erun-version coupling)." |
| Triggers | "build a multi-tenant postgres database", "create a tenant-scoped postgres schema with row-level security", "set up multi-tenant postgres migrations", "I need an erun-backend-db-shaped module", "build a multi-tenant rls db", "upgrade the multi-tenant postgres module", "repair the rls db module", "reconcile the tenant database schema to the blueprint", "bump the db module to \<version\>", "maintain the erun-backend-db-shaped module" |
| Inputs | Module name; target directory; list of tenant-owned tables; PostgreSQL major version (default 18) |
| Outputs | `<module>/atlas.hcl`, `<module>/schema/{tables,indexes,triggers,rls,fks}/*.sql`, `<module>/schema/roles.sql`, `<module>/migrations/default/`, `<module>/AGENTS.md`. Bootstrap tables (`tenants`, `tenant_issuers`, `users`, `user_external_ids`) plus one tables/indexes/triggers/rls set per user-supplied table. On an existing module (an `atlas.hcl` plus a `schema/` tree) it enters maintenance mode instead of scaffolding: previews the plan, reconciles gaps against the current `erun-backend-db` blueprint (missing bootstrap tables, `roles.sql`, `rls/context.sql`, timestamp triggers, RLS `ENABLE`/`FORCE` + `_tenant_policy`/`_operations_policy` pairs, `atlas.hcl` `src` order), and re-pins the module's own version axes (the PostgreSQL major and Atlas toolchain) to their targets — never clobbering the project's own tables and never rewriting a committed migration (drift is corrected with a new forward `atlas migrate diff`). Cleanup removes only superseded scaffolding, never dropping a table or deleting a committed migration — a schema removal belongs in a reviewed forward migration, not a cleanup pass. |
| Error behaviour | Target dir already has `atlas.hcl` → not a stop; enter maintenance mode and reconcile gaps + re-pin in place, previewing before writing and never clobbering the project's tables or committed migrations. PostgreSQL \< 18 detected → stop (native `uuidv7()` unavailable). `atlas` not installed → skip validate, surface install hint, continue. User-supplied table name collides with bootstrap names → stop and ask user to rename. |

### `erun-blueprint-api`

| Field | Value |
|---|---|
| Kind | Blueprint — packages ERun's accumulated best practices for multi-tenant HTTP APIs. |
| Source | `erun-skills/skills/erun-blueprint-api/SKILL.md` + `templates/` |
| Description | "Build or maintain a multi-tenant Go HTTP API service following ERun's blueprint — OIDC bearer authentication, tenant resolution from the token issuer, layered model / repository / service / routes structure, transaction-scoped PostgreSQL security context, identity resolution cache, and audit logging — and reconcile, repair, and upgrade a previously scaffolded service in place by realigning it to the current blueprint and refreshing the service's own dependency pins, without clobbering the project's own business logic (it is a standalone Go module with no erun-version coupling). Captures the patterns that erun-backend-api packages." |
| Triggers | "build a multi-tenant http api", "build a multi-tenant backend api", "create an erun-backend-api-shaped service", "I need a multi-tenant Go api with oidc auth and tenant rls", "upgrade the multi-tenant api", "repair the erun-backend-api-shaped service", "reconcile the api to the blueprint", "bump the api to \<version\>", "maintain the multi-tenant api" |
| Inputs | Module name; Go module path; target directory; OIDC issuers; initial entities (optional) |
| Outputs | `<module>/go.mod`, `<module>/cmd/<module>/main.go`, `<module>/server.go`, `<module>/auth.go`, `<module>/oidc.go`, `<module>/identity_cache.go`, `<module>/api_path.go`, `<module>/audit.go`, `<module>/internal/{model,repository,routes}/...`, `<module>/AGENTS.md`. Includes a working `GET /v1/whoami` endpoint; entity routes are produced per user-supplied entity. Its own Step 6 then composes [`erun-blueprint-service`](#erun-blueprint-service) to add the `<tenant>-devops/docker/<module>/Dockerfile` and `<tenant>-devops/k8s/<module>/` chart this skill's source-only output has no deploy artifacts for — without it, `erun build`/`erun deploy` have nothing to find. On an existing service (a `go.mod`/`server.go`/`internal/repository/tx.go` present) it enters maintenance mode instead of scaffolding: previews the plan, restores structural drift against the current blueprint (a missing layer, OIDC/authentication, authorization or audit middleware, tenant-from-issuer resolution, the `TxManager.WithTx` RLS security-context wiring, or the identity-resolution cache), and refreshes the service's own dependency `require` pins and `go` toolchain line, then re-proves with `go mod tidy` / `go build` / `go test`; it never clobbers the project's own domain entities or business logic. Cleanup removes only superseded generated files (preview-first), never the project's own code. |
| Error behaviour | Target dir already has `go.mod` → not a stop; enter maintenance mode and reconcile against the blueprint in place — fill structural drift and refresh the service's own dependency pins — without clobbering the project's own content. Empty OIDC issuer list → stop. Database side (matching `erun-backend-db`-shaped schema) missing → surface and offer to run `erun-blueprint-rls-db` first. `go build` fails after generation → surface compiler output; most common cause is module path mismatch. |

### `erun-blueprint-service`

| Field | Value |
|---|---|
| Kind | Blueprint — packages ERun's component-naming and multi-stage-Dockerfile conventions for a deployable service. |
| Source | `erun-skills/skills/erun-blueprint-service/SKILL.md` + `templates/` |
| Description | "Add a custom service's deploy artifacts — a multi-stage Dockerfile, a Helm chart, and per-env values overlays — in the exact layout `erun build` and `erun deploy` discover by convention (`<tenant>-devops/docker/<component>/`, `<tenant>-devops/k8s/<component>/`), so a hand-written or generated service becomes a component erun can build and ship without anyone reverse-engineering the convention. Also maintains, repairs, and upgrades deploy artifacts it previously produced in place, without clobbering the service's own source or hand-authored chart templates." |
| Triggers | "add deploy artifacts for this service", "scaffold a Dockerfile and helm chart for \<component\>", "make this service deployable with erun", "wire up build and deploy for \<component\>", "add a component chart", "this service has no Dockerfile/chart yet", "upgrade the \<component\> chart", "repair the \<component\> deploy wiring", "reconcile \<component\>'s deploy artifacts" |
| Inputs | Tenant; component name (validated against `^[a-z][a-z0-9-]*$`, tenant-prefix recommended); the component's source location and language/toolchain; container port + health-check path (default `8080`/`/healthz`); the envs to generate `values.<env>.yaml` for (`local` always required); whether the component must be publicly reachable. |
| Outputs | `<tenant>-devops/docker/<component>/Dockerfile` (multi-stage: builder runs tests then builds, thin non-root runtime stage — Go skeleton shipped, swap the builder stage for another toolchain); `<tenant>-devops/k8s/<component>/{Chart.yaml,values.local.yaml,values.<env>.yaml,templates/service.yaml}` — a `Deployment` + `Service` named **literally** `<component>` (not tenant-templated the way erun's own published multi-tenant component charts are, since this chart is authored once for one tenant's own component), with the image defaulting to `<containerRegistry>/<component>:<Chart.AppVersion>` overridable via the same `imageOverrides.<component>` mechanism `erun deploy` already threads, and readiness/liveness probes on the configured health-check path. Before writing, checks whether `*/k8s/<component>/Chart.yaml` already resolves anywhere else in the tree (`componentHelmChartCandidate`'s matching is not scoped to the target `<tenant>-devops/`) and stops rather than creating a second chart erun would later refuse to disambiguate. Because `erun push` rewrites `Chart.yaml` `version`/`appVersion` to the resolved build version on every publish (`overrideHelmChartVersion`), the shipped placeholders need no hand-maintenance. Public reachability is not chart-side: pairing the tenant-prefixed component name with the literal Service name is what makes `erun expose <tenant> <env> <service>` (which targets the tenant-scoped Service `<tenant>-<service>`) resolve to the Service this chart already renders, with no separate Ingress in the chart itself. On an existing component (a Dockerfile or chart already at the conventional path) it enters maintenance mode instead of scaffolding: previews the plan, fills gaps against this skill's contract (a missing `values.<env>.yaml`, a `Deployment`/`Service` not literally named `<component>`, a missing probe, a runtime stage that isn't thin/non-root) without touching the Dockerfile's builder-stage toolchain commands or a hand-authored chart template, and re-validates with `helm lint`/`helm template`. There is no erun-version coupling to re-pin — this is the component's own release line, re-stamped by `erun push` as above. |
| Error behaviour | Component name fails `^[a-z][a-z0-9-]*$` → ask the user to rename (`INVALID_COMPONENT_NAME`). Name collides with a chart elsewhere in the tree → stop before writing; rename or reuse the existing chart. Name is the reserved `<tenant>-devops` → refuse (that's the runtime-pod chart's, owned by `erun-build-env`). Dockerfile/chart already exist → not a stop; enter maintenance mode and reconcile gaps in place, previewing first, never clobbering the service's own code or hand-authored templates. `erun deploy` fails `values file not found for environment "<env>"` → create the missing `values.<env>.yaml` (comment-only is valid); `values.local.yaml` is required too. `erun deploy <component>` fails `multiple Helm charts found for component "<component>"` → a second same-named chart was added by hand after scaffolding; rename one. `helm lint`/`helm template` unavailable locally → skip validation and say so; the runtime image ships `helm`. `erun expose` resolves but the Ingress 503s → the targeted Service `<tenant>-<service>` doesn't match what the chart rendered, usually a component scaffolded without the tenant prefix; rename to add the prefix, or pass the exact post-prefix role as `<service>`. A one-shot Job (migration/cron) requested instead of a long-running service → out of scope for the shipped `templates/chart/templates/service.yaml` shape; point at [Conventions spec · Helm Job pattern for one-shots](/agent-reference/conventions-spec#helm-job-pattern-for-one-shots) and hand-write the Job chart. |
| Compose | `erun-blueprint-api` produces a service's source only and has no deploy artifacts of its own; its Step 6 applies this skill to close that gap. Any other service-authoring skill or hand-written service can compose it the same way. |

### `erun-blueprint-docs`

| Field | Value |
|---|---|
| Kind | Blueprint — packages ERun's docs-site pattern: a Docusaurus 3.x site published to Cloudflare Pages by a Kubernetes hook Job, the shape `erun-docs` captures. |
| Source | `erun-skills/skills/erun-blueprint-docs/SKILL.md` + `templates/` |
| Description | "Scaffold a product documentation site following ERun's blueprint — a Docusaurus 3.x site published to Cloudflare Pages through a Kubernetes Job, the exact shape erun-docs captures — and also maintain, repair, and upgrade an already-scaffolded docs site in place, reconciling it with the current blueprint and re-pinning its versions without clobbering the project's own content pages." |
| Triggers | "set up product docs site", "scaffold a docusaurus docs site", "build erun-docs-shaped documentation", "create a docs site deployed to cloudflare pages", "add a documentation site for this project", "upgrade the docs site", "repair the docs deploy wiring", "reconcile the docusaurus site with the blueprint", "bump the docs site to \<version\>", "maintain the docs site" |
| Inputs | Module name (default `<concern>-docs`); target repo root; site title + tagline + production URL; Cloudflare Pages project name + branch alias; GitHub org/repo for `editUrl` |
| Outputs | `<module>/` Docusaurus site (`docusaurus.config.ts` with `onBrokenLinks: throw`, `sidebars.ts`, `docs/`, `src/css`, `static/img`, `package.json`, `tsconfig.json`); `erun-devops/docker/<module>/{Dockerfile,entrypoint.sh}` (two-stage build → pinned wrangler); `erun-devops/k8s/<module>/{Chart.yaml,values.local.yaml,values.prod.yaml,templates/docs.yaml}` (ServiceAccount + `post-install,post-upgrade` hook Job that runs `wrangler pages deploy`). Both `values.local.yaml` (agent env, `docs.enabled: false`) and `values.prod.yaml` ship, because `erun deploy` requires a per-chart `values.<env>.yaml` for every env — including the `<tenant>-local` agent env the desktop deploys — with no fallback. On an existing site (a `<module>/docusaurus.config.ts` or the deploy plumbing present) it enters maintenance mode instead of scaffolding: previews the diff, reconciles the deploy wiring against the current `erun-docs` blueprint (a missing `values.<env>.yaml` — especially `values.local.yaml` — `Chart.yaml`/`templates/docs.yaml`/`entrypoint.sh`, `onBrokenLinks: 'throw'` turned off, a Git-connected Pages project, drifted plumbing), and re-pins two axes separately — the erun release (`ERUN_VERSION`, the `Chart.yaml` `version`/`appVersion`) to the target, and the docs toolchain (`node`/`wrangler` tags, `@docusaurus/*` pins) to current — before re-proving with `yarn install`/`yarn build` — never clobbering the operator's `docs/` pages, `sidebars.ts`, or `src/css/custom.css`. Cleanup removes only superseded deploy-wiring, never the operator's content; a stale Cloudflare Pages project is the operator's to remove. |
| Error behaviour | Target dir already has `<module>/docusaurus.config.ts` → not a stop; enter maintenance mode and reconcile the deploy wiring against the blueprint + re-pin versions in place, preserving the existing `docs/` content. `yarn build` fails on a broken link → fix the link, do not disable `onBrokenLinks`. `npx create-docusaurus` offline → fall back to bundled `templates/`. No Cloudflare alias (or its token lacks `Pages:Edit`) → scaffold still succeeds; the publish Secret and Direct-Upload Pages project are provisioned automatically from a Cloudflare alias, so surface that the first `erun deploy` Job fails until an alias whose token has `Pages:Edit` is attached (custom domain + DNS stay manual). User asks for a Git-connected Pages project → stop (Direct Upload only; a Git connection double-deploys). `erun deploy` fails `values file not found for environment "<env>"` → the chart is missing `values.<env>.yaml`; create it (an empty/comment-only file is valid), remembering the agent env needs `values.local.yaml`. |

### `erun-blueprint-platform`

| Field | Value |
|---|---|
| Kind | Blueprint — packages ERun's accumulated best practices for hosted-platform deploy wiring. |
| Source | `erun-skills/skills/erun-blueprint-platform/SKILL.md` |
| Description | "Blueprint the deploy artifacts for a hosted erun platform — a per-env Terraform tree (terraform-\<tenant\>/) whose modules wrap erun's published Terraform modules, and the per-env Helm values overlays plus thin umbrella charts that reference erun's published OCI charts — all version-pinned to the erun release the environment runs; also maintains, repairs, and upgrades an existing terraform-\<tenant\>/ tree and its \<tenant\>-\<component\> umbrellas in place, re-pinning every erun reference to the target version and filling gaps against this contract." |
| Triggers | "blueprint the platform", "scaffold the platform terraform", "set up the platform helm charts and terraform", "create the terraform-\<tenant\> structure", "blueprint erun platform deploy", "set up platform deploy artifacts", "upgrade the platform terraform", "repair the platform charts", "reconcile the terraform-\<tenant\> tree", "bump the platform to \<version\>", "maintain the platform deploy artifacts" |
| Inputs | The env's tenant + short env name; the erun version to pin to (`erun version` in-pod, or the env's `runtimeversion`); the platform values (`base_domain`, `services_zone`, `acme_email`); the container registry (env `containerregistry`, default `ghcr.io/sophium`). |
| Outputs | `terraform-<tenant>/{common.tf, variables.tf, .gitignore}` (canonical providers + shared vars), `terraform-<tenant>/modules/terraform-<tenant>-cluster-edge/` (wraps erun's `terraform-erun-cluster-edge` by `?ref=v<version>`), and per env a `terraform-<tenant>/<env>/` folder whose `common.tf`/`variables.tf` are **symlinks** to the root and that adds the env's services via its own `main.tf` + `<env>.tfvars`; plus — **optional, the patch/override path** (a runtime env deploys the published components by reference from config — see [Deploy chart source](/reference/configuration#deploy-chart-source) — so no umbrella is needed for a normal deploy) — per platform component, a thin umbrella `<tenant>-devops/k8s/<tenant>-<component>/Chart.yaml` (directory name, chart `name:`, and Helm release all `<tenant>-<component>`, e.g. `acme-docs`) depending on erun's published `erun-<component>` OCI chart, with a **per-chart `values.<env>.yaml` for every env it deploys to — including `values.local.yaml`**. `erun deploy <tenant> <env>` reads `<tenant>-<component>/values.<env>.yaml` from each chart dir (required, no fallback, no config-dir overlay) and keys the component name off the directory, and the desktop deploys the `<tenant>-local` agent env, so a missing `values.local.yaml` fails the deploy. Each umbrella's resolved dependency is tracked as `Chart.lock` (committed) with `charts/*.tgz` gitignored (`**/charts/*.tgz`); `erun deploy` runs `helm dependency build` before install, rebuilding `charts/` from `Chart.lock`, so the tgz is never committed (vendor it only for an air-gapped install). No `run.tf`, no per-env shell scripts — [`erun terraform apply`](/cli/terraform) owns the apply workflow. This skill wraps only the erun platform's own component charts; it never emits a runtime `erun-devops`/`<tenant>-devops` umbrella **from here** — the runtime chart is [`erun-build-env`](#erun-build-env)'s. A tenant that ships these component charts runs them on its own version line, so `erun-build-env` must also publish a `<tenant>-devops` chart at the tenant version (its Step 6, required for such a tenant, and what `erun deploy` demands when a deploy includes tenant components); a bootstrap/erun-only env with no components of its own may still ride the shared `erun-devops` chart via `imageOverrides`. On an existing tree (a `terraform-<tenant>/` or any `<tenant>-<component>` umbrella present) it enters maintenance mode instead of stopping: previews the diff/plan, then reconciles in place — re-pins every erun reference on both sides (each tenant module's `?ref=v<version>` and each umbrella `Chart.yaml` dependency `version:`) to one target, fills contract gaps (absent `common.tf`/`variables.tf` symlinks, a missing per-env `values.<env>.yaml` including `values.local.yaml`, a missing `**/charts/*.tgz` gitignore entry, an uncommitted `Chart.lock`), and refreshes derived artifacts (`helm dependency update` to regenerate the committed `Chart.lock`, then `erun terraform apply`) — never clobbering the project's own tfvars, values overrides, or extra tenant modules, and confirming the tenant first on a loose match. Cleanup removes only what the reconcile supersedes — a dropped umbrella dir or a stale/mis-named component release the new set replaces — preview-first, and never `helm uninstall`s a stateful release (postgres / a data PVC) or drops data as a side effect (it stops and flags instead). |
| Error behaviour | `terraform-<tenant>/` already exists → not a stop; enter maintenance mode and reconcile the tree in place (re-pin every erun reference to the target version, fill contract gaps, refresh derived artifacts) after previewing the diff, offering to add a new `<env>/` folder if that's the ask and confirming the tenant only on a loose match. erun version unresolvable → stop and ask (never default to `main` for production wiring). `?ref=v<version>` doesn't resolve on `terraform init` → pin to a released `vX.Y.Z`. `helm dependency build` 404s → that version's chart isn't published; pin to a pushed version. `erun deploy` fails `values file not found for environment "<env>"` → the umbrella chart lacks `values.<env>.yaml`; create it (empty/comment-only is valid), including `values.local.yaml` for the agent env. `erun deploy` fails `tenant is required` (or `environment is required`) → the wrapped subchart reads those in its own scope; author them nested under the dependency name in the umbrella's `values.<env>.yaml`. A by-reference deploy re-scopes deploy's `--set`s under the subchart key and `helm pull`s that file to apply it, so this now surfaces only on a worktree deploy whose `values.<env>.yaml` omits the nested keys. A component that can't run in an env (`erun-powerdns` needs `:53`/hostNetwork + a private-image pull secret; `erun-zitadel` needs a public auth host, its cert, and an existing masterkey Secret) → omit it from that env's `--components`, don't force it. `erun-zitadel` render fails `zitadel.masterkeySecretName is required` → create the 32-character masterkey Secret out of band and name it in the umbrella's `values.<env>.yaml`; never generate one into the chart or the repo. `erun-powerdns` CrashLoops binding `:53` → it bound `0.0.0.0`, which collides with the node's systemd-resolved `127.0.0.53:53` stub; set `erun-powerdns.powerdns.localAddress` in the umbrella's `values.<env>.yaml` to the node's interface IP (empty binds the node IP by default on current erun; the override is honored on every version) rather than hand-patching the live Deployment. Operator asks to put the Cloudflare token in `<env>.tfvars` → refuse; it is injected as `TF_VAR_cloudflare_api_token` at apply time. An `erun-devops`/`<tenant>-devops` umbrella under `<tenant>-devops/k8s/` → that is the runtime chart (owned by `erun-build-env`), legitimate and required once the tenant ships its own components; don't create or edit it from this skill, only remove a stray one this skill created by mistake. |

### `erun-build-env`

| Field | Value |
|---|---|
| Kind | Workflow — extend the environment's runtime image through ERun's supported extension path. |
| Source | `erun-skills/skills/erun-build-env/SKILL.md` |
| Description | "Create a custom build environment by extending ERun's published runtime image with the project's own toolchain, then pointing the environment at the result, and maintain, repair, or upgrade an existing custom build environment in place by re-pinning it to the target runtime version and filling any gaps against this skill's contract." |
| Triggers | "init build environment", "init erun build environment", "create a custom build environment", "customize the runtime image", "upgrade the build environment", "upgrade the custom runtime image", "repair the build environment", "reconcile the \<tenant\>-devops module", "bump the runtime image to \<version\>", "maintain the build environment" |
| Surfaced by | `erun build` — and the build it runs for `--release` / `--deploy`, plus the MCP `build` tool — prints a one-line advisory recommending this skill whenever it runs in a project with no `<tenant>-devops` build module (the module that would hold the custom runtime image). The advisory fires regardless of whether the build itself succeeds. |
| Inputs | The tooling to add (packages, toolchains, CLIs); the target tenant + environment. The module and image names are fixed by convention: `<tenant>-devops` for both (see Outputs). |
| Outputs | A `<tenant>-devops` module (outer directory name **must end in `-devops`** — `erun build` discovers the runtime build module by that suffix) containing a starter Dockerfile at `<tenant>-devops/docker/<tenant>-devops/Dockerfile` (inner directory name becomes the image name) with `FROM <registry>/erun-devops:<runtime-version>` (version read from `erun version` in-pod, or the env's `runtimeversion` / `erun list` on a laptop); a `VERSION` file at the module root (`<tenant>-devops/VERSION`, e.g. `1.0.0`) — `erun build` mints the image version from it; an `erun build` run that builds both architectures and pushes to the env's registry; the env's [`runtimeimage`](/reference/configuration#envconfig) field set to `<tenant>-devops` via `erun init --runtime-image <ref>` or a direct config edit. On the next deploy/open the image rides into the published chart as `imageOverrides.erun-devops` ([Advanced chart values](/reference/configuration#advanced-chart-values)). For files a sourceless runtime env must carry on disk (a platform Terraform tree, seed data, fixtures), the skill directs baking them into **`/opt/erun/release/`** — never under `/home/erun`, which the runtime pod's home PVC shadows — laid out relative to the repo root; on a runtime env the entrypoint symlinks the git folder (`~/git/<tenant>`) at `/opt/erun/release`, so `COPY … /opt/erun/release/<tenant>-devops/terraform-<tenant>/` surfaces at `~/git/<tenant>/<tenant>-devops/terraform-<tenant>/` where `erun terraform` resolves it. A `<tenant>-devops/k8s/<tenant>-devops/` umbrella chart depending on the published `erun-devops` chart — **required** once the tenant ships its own component charts (so the runtime deploys on the tenant's own version line, which `erun deploy` demands when a deploy includes tenant components), optional otherwise for pod shape the image can't express, and published by `erun push`/`erun release` at the tenant version — supplying `extraContainers`/`extraVolumes`/`extraVolumeMounts`/`extraEnv`/`extraRules` nested under the `erun-devops` subchart key in per-env `values.<env>.yaml`; `erun deploy` installs it as the runtime chart, `helm dependency build`s it, and re-scopes every runtime value (incl. the image override) under the subchart key ([pod shape extensions](/reference/configuration#advanced-pod-shape)). Because `erun push` publishes the umbrella and its `<tenant>-devops` image together, deploy **defaults `imageOverrides.erun-devops` to the umbrella's own image** ([default runtime image](/agent-reference/cli-flags#deploy-runtime-image-default)), so once the umbrella is published `runtimeimage` is optional — set it only to pin a different image; an image-only env riding the shared `erun-devops` chart still needs it. On an existing `<tenant>-devops` module it enters maintenance mode instead of re-scaffolding: previews the diff, then re-pins to one target runtime version (`FROM ghcr.io/sophium/erun-devops:<version>`, the module `VERSION`, and any Step 6 umbrella `erun-devops` dependency `version:`), fills contract gaps (a missing `VERSION`, wrong `<tenant>-devops` module/image naming, a `FROM` that isn't `erun-devops`, or under the umbrella a missing per-env `values.<env>.yaml`/`Chart.lock`/`charts/*.tgz` gitignore), then rebuilds and pushes both arches — never clobbering the project's own toolchain layers. Cleanup removes a renamed/relocated old module (preview-first) but never prunes pushed images — those are the operator's. For a project that can't nest the module at the conventional repo-root `<tenant>-devops/docker/<tenant>-devops/` depth, the skill also points at the `.erun/config.yaml` `paths:` escape hatch — [`paths.docker`](/reference/configuration#paths-block) to relocate discovery and [`paths.dockercontext: repo-root`](/reference/configuration-build-paths#3-build-context-directory) so a deeper Dockerfile's repo-relative `COPY`s still resolve. |
| Error behaviour | Runtime version unresolvable (no `runtimeversion` in the env config and not inside a pod) → asks the Operator before writing the Dockerfile. Existing `<tenant>-devops` module (Dockerfile + `VERSION` present) → not a stop; enter maintenance mode and reconcile in place — re-pin the `FROM`/`VERSION` (and any Step 6 umbrella dependency) to the target runtime version and fill contract gaps, previewing the diff first and keeping the project's own toolchain layers. Module directory not ending in `-devops`, or no `VERSION` file at the module root → `erun build` fails (`dockerfile not found in current directory` / `version file not found for current module`); the skill's Steps 2–3 produce the layout that avoids both. `erun build` fails (e.g. `BINFMT_MISSING`, registry push rejected) → surfaces the build error and does not touch the env config. Base image other than `erun-devops` requested → refuses; the entrypoint, the Agent tooling, and the in-pod `erun` live in that image. |

### `erun-browser-session-rest`

| Field | Value |
|---|---|
| Kind | Workflow — authenticated REST against a host that blocks API tokens, via a reused browser session. |
| Source | `erun-skills/skills/erun-browser-session-rest/SKILL.md` + `save-session.mjs` + `request.mjs` |
| Description | "Make authenticated REST calls to a host whose org blocks API tokens and admin-gates OAuth, by reusing a saved browser login session (Playwright storageState)." |
| Triggers | "authenticated REST via a browser session", "call an API that blocks API tokens", "reuse my browser login for API calls", "hit the `<host>` API without a token" |
| Inputs | Host base URL (`ERUN_REST_BASE_URL` / `--base`); login URL (`ERUN_REST_LOGIN_URL` / `--login`); session-file path (`ERUN_REST_SESSION` / `--session`, default `./session.json`); per call: HTTP method, path, optional JSON body. No host, credentials, or IdP are baked in. |
| Outputs | `save-session.mjs` opens a real browser for manual login (SSO + MFA) and writes a Playwright `storageState` session file (cookies only — never a password). `request.mjs` makes the authenticated call, prints the response body to stdout, and rolls the session forward (re-saves it) so refreshed cookies persist. |
| Error behaviour | Missing base/login URL → usage error, exit 2. Session file missing/unreadable → "run save-session.mjs first", exit 2. HTTP error status → response body to stdout, status to stderr, exit 1. Expired session (401 / login redirect) → re-run `save-session.mjs`. Requires Node 18+ and Playwright (`npx playwright install chromium`). |
| Security | The session file holds live session cookies — treat it as a secret, keep it out of git, keep it short-lived. The login is intentionally manual; the skill never stores a plaintext password. This is a fallback — prefer an API token or an approved OAuth app whenever the host allows one. |

### `erun-enable-hosting-edge`

| Field | Value |
|---|---|
| Kind | Workflow — applies the public hosting edge to a cluster through ERun's published Terraform module. |
| Source | `erun-skills/skills/erun-enable-hosting-edge/SKILL.md` |
| Description | "Stand up the public hosting edge for an erun cluster — a Traefik ingress controller, cert-manager, and a namespaced DNS-01 Issuer that issues wildcard TLS for the services zone — by applying the terraform-erun-cluster-edge module, and maintain, repair, and upgrade that edge afterwards by re-pinning the module ?ref to the env's erun version and re-applying to reconcile drift." |
| Triggers | "enable the hosting edge", "enable public hosting", "set up TLS ingress for erun", "apply the cluster edge", "set up cert-manager and traefik", "issue wildcard TLS for the services zone", "upgrade the hosting edge", "repair the cluster edge", "reconcile cert-manager and traefik", "bump the cluster edge to \<version\>", "maintain the public hosting edge" |
| Inputs | The env's `CLOUDFLARE_API_TOKEN` (injected by a Cloudflare alias) passed as `TF_VAR_cloudflare_api_token`; the services zone (`platform.serviceszone`) and ACME email (`platform.acmeemail`); the erun version the module `?ref` pins to (`erun version`, else `main` off-pod). The apply runs in-pod as the runtime ServiceAccount and creates cluster-scoped resources (namespaces, CRDs), so the env must be a **[platform account](/reference/configuration#envconfig)** (`platformaccount: true`, `erun init --platform-account`) — otherwise the SA has namespaced admin only and the apply is denied. |
| Outputs | A Terraform root (in a temp dir) that references `terraform-erun-cluster-edge` from erun's GitHub by `?ref=v<version>` and applies it: a Traefik ingress controller, cert-manager + CRDs, a namespaced DNS-01 `Issuer` (`erun-cloudflare`, in the env namespace), and a wildcard `Certificate` for `*.<services-zone>`. Idempotent — re-running reconciles. Maintenance is the same re-apply: when the edge already exists (`kubectl get issuer -n <issuer-namespace> erun-cloudflare` succeeds) it re-pins the module `?ref` to the env's erun version and re-applies to reconcile drift, previewing with `terraform plan` first — no separate scaffold artifacts, no clobbering operator-owned cluster content. There are no local artifacts to clean; tearing the edge down is a deliberate `erun terraform destroy`, never a maintenance side effect. The platform's own wildcard Issuer's DNS-01 solver has three modes via `dns01_provider`: `cloudflare` (default), `powerdns-rfc2136` (DNS UPDATE + TSIG direct to the self-hosted PowerDNS — single-tenant platform cluster only), and `powerdns-broker`: a per-tenant namespaced Issuer whose challenges route through a per-cluster cert-manager webhook shim to the [DNS-01 broker](/agent-reference/api-protocol#dns01-broker), which authorizes each challenge against the env's own subzone. Installing that webhook shim is a separate switch, `install_dns01_webhook` — it defaults to matching `dns01_provider == "powerdns-broker"` (back-compat) but can be set independently, so a platform can keep its own wildcard on `cloudflare`/`powerdns-rfc2136` while still installing the shim for per-tenant brokered Issuers (the ones `erun expose` provisions). Once installed, per env it mints a DNS-01 token ([`POST …/dns01-token`](/agent-reference/api-protocol#dns01-token-endpoint)) landed as the Issuer's token Secret; it needs no Cloudflare token or TSIG key. A separate opt-in switch, `install_coredns_forward` (default `false`, so an already-applied cluster's DNS behavior never changes on a module upgrade), declares a `coredns-custom` ConfigMap server block that forwards `base_domain_name` to `coredns_forward_upstreams` (default the public resolvers `1.1.1.1`/`1.0.0.1`/`8.8.8.8`, overridable for an air-gapped or policy-constrained cluster) — k3s's bundled CoreDNS already mounts and imports that ConfigMap, so declaring the block needs no change to the CoreDNS Deployment. This makes in-cluster resolution of the platform's own published names (which the HTTP-01 self-check depends on, at issuance and every unattended renewal) independent of whatever DNS the node happens to fall through to. |
| Error behaviour | No `CLOUDFLARE_API_TOKEN` → stops, points at `erun cloud init cloudflare` + `erun cloud set … --alias <name>@cloudflare`. `terraform`/`kubectl` missing, or `kubectl` not pointed at a reachable cluster → stops. `apply` fails with `namespaces is forbidden` / `customresourcedefinitions … is forbidden` for the runtime SA → the env is not a platform account; set `platformaccount: true` (`erun init --platform-account`) and redeploy from an admin-capable context so the chart binds the SA to `cluster-admin`, then re-apply. Issuer/Certificate stalls → `kubectl describe` the ACME order/challenge; usual causes are a token missing `Zone:Read`+`DNS:Edit` or the services zone not yet delegated to Cloudflare. A self-check failure specifically (`dial tcp: lookup <host> ...: no such host` in the challenge, while the name resolves fine from outside the cluster) points at the node's resolver rather than the zone — confirm with an in-cluster `nslookup` and apply `install_coredns_forward=true` with `base_domain_name` set. `install_coredns_forward = true` with `base_domain_name` unset → rejected at plan time by a Terraform precondition. A cluster already carrying a hand-applied `coredns-custom` ConfigMap needs it reconciled once (`terraform import`, or delete the hand-applied copy) before the first apply with `install_coredns_forward = true`, because the module creates that object when nothing else has. It owns only its own key inside it (server-side apply under its own field manager), so importing an existing ConfigMap keeps every other key through subsequent applies, and a destroy removes just the module's key. Set `manage_coredns_custom_configmap = false` on a cluster where something else owns that object's lifecycle and the module will manage its key without ever creating or deleting the ConfigMap. The forward also refuses to apply when CoreDNS's Corefile does not `import /etc/coredns/custom/*.server`, rather than writing an entry nothing will read, and validates each `coredns_forward_upstreams` entry at plan time — a malformed one would otherwise write an invalid server block that only fails at CoreDNS's next restart. While validating a fresh zone, `-var acme_server=<staging>` avoids Let's Encrypt production rate limits. |

### `erun-setup-k3s-cluster`

| Field | Value |
|---|---|
| Kind | Workflow — provisions a durable local cluster on Windows for erun to build and deploy to. |
| Source | `erun-skills/skills/erun-setup-k3s-cluster/SKILL.md` |
| Description | "Stand up a durable local Kubernetes cluster on Windows that erun builds and deploys to — real k3s running inside WSL2 (with an in-cluster image registry and a WSL-hosted Docker engine, no Docker Desktop), wired to an erun local-agent environment — and maintain, repair, or tear it down afterwards." |
| Triggers | "set up a local erun cluster on Windows", "set up k3s for erun", "create a local k3s cluster", "install k3s on Windows for erun", "run erun locally on Windows", "wire erun to a local cluster", "give me a local cluster to deploy erun to", "repair the local k3s cluster", "tear down the local erun cluster" |
| Inputs | Windows 11 22H2+ (WSL mirrored networking + `hostAddressLoopback`); `kubectl`, `helm`, and the `docker` **client** on Windows (`scoop install kubectl helm docker`); the erun CLI on PATH; the target tenant + environment name (env named `local` by convention). No Docker Desktop — the Docker daemon runs inside the distro. The registry (`localhost:5000`), kube-context (`erun-k3s`), and Docker endpoint (`tcp://127.0.0.1:2375`) are fixed by the skill. |
| Outputs | A WSL2 Ubuntu distro (installed via `wsl --install`, or the WSL MSI from GitHub releases when the inbox stub misbehaves) with systemd enabled and `%USERPROFILE%\.wslconfig` set to `networkingMode=mirrored` **and** `[experimental] hostAddressLoopback=true` — both required so `localhost` resolves host↔WSL in each direction (mirrored alone leaves the Hyper-V firewall blocking host→WSL). Inside the distro: `iptables` installed and a `dev-kmsg.service` that provides `/dev/kmsg` (both needed by k3s on WSL2); a k3s server (`--write-kubeconfig-mode=644`) with `/etc/rancher/k3s/registries.yaml` mirroring `localhost:5000` over HTTP; an in-cluster registry (`registry:2` as a `kube-system` Deployment on **`hostNetwork`** with `REGISTRY_HTTP_ADDR=0.0.0.0:5000` and a `hostPath` volume, dropped into `/var/lib/rancher/k3s/server/manifests/` so k3s reconciles it — hostNetwork binds the node's `:5000` directly, avoiding the CNI portmap/`hostPort` path that is unreliable on WSL2); a `docker.io` daemon exposed on `unix://` + `tcp://127.0.0.1:2375` via a systemd drop-in, with binfmt registered (`tonistiigi/binfmt`) for erun's mandatory multi-arch (`linux/amd64`+`linux/arm64`) builds. On Windows: `DOCKER_HOST=tcp://127.0.0.1:2375` (User env); an `erun-k3s` context written to `%USERPROFILE%\.kube\config` from `/etc/rancher/k3s/k3s.yaml` (server `https://127.0.0.1:6443`) via a rename regex that handles **both** `  name: default` and k3s's `- name: default`; an erun `local-agent` environment created with `erun init <tenant> local --type local-agent --kubernetes-context erun-k3s --container-registry localhost:5000` run **inside the project** (writes `containerregistries: [{registry: localhost:5000, roles: [build, deploy]}]` to the project `.erun/config.yaml` and `kubernetescontext: erun-k3s` to `%LOCALAPPDATA%\erun\<tenant>\local\config.yaml`); and an `erun-k3s-boot` logon Scheduled Task that boots the distro so systemd restarts k3s + registry + dockerd across reboots. Validated end-to-end on Windows 11 24H2 (k3s v1.36, Ubuntu 26.04): `kubectl get nodes` Ready from Windows, `docker push localhost:5000/...` from Windows then a k3s pod pulling that image to completion, and `erun deploy <tenant> local --version <v> --dry-run` resolving `kubectl --context erun-k3s`, `--kube-context erun-k3s`, and `containerRegistry=localhost:5000` / `oci://localhost:5000/charts/erun-devops`. |
| Error behaviour | Windows older than 11 22H2 (no mirrored networking/`hostAddressLoopback`) → stop; the host↔WSL `localhost` wiring cannot work. `dism`/`wsl --install` returns `Error 740` → the shell has Administrators-group membership but only Medium integrity; run the elevated step from a Scheduled Task as `NT AUTHORITY\SYSTEM` `-RunLevel Highest` (use `msiexec` for the WSL MSI; the inbox `wsl --install` stub needs an interactive session and fails as SYSTEM). Inbox `wsl.exe` only reprints "…is not installed" → install the WSL MSI directly. `.wslconfig` missing `hostAddressLoopback=true` → `kubectl` gets `error: EOF` / registry unreachable from Windows though the distro is fine internally; add it and `wsl --shutdown`. `kubectl` → `Please enter Username` → the kubeconfig rename missed k3s's `- name: default`, so the context's user reference has no matching user; re-run the rename regex. k3s pull `http: server gave HTTP response to HTTPS client` → the `registries.yaml` mirror is missing; re-write it and `systemctl restart k3s`. Registry `:5000` not listening on the host / `iptables` absent → the CNI `hostPort` path is unreliable on WSL2; keep the registry on `hostNetwork` (don't switch back to `hostPort`). `Invoke-WebRequest http://localhost:5000/...` times out → a .NET HttpClient quirk over the WSL loopback; use `curl.exe --noproxy '*'` (docker push/pull over the same address work). `erun deploy` pushes to `ghcr.io/...` instead of `localhost:5000` → the project's `.erun/config.yaml` `local` env pins another registry; add the `containerregistries` override. First command after a cold start fails but later ones succeed → the distro was booting and k3s wasn't Ready; wait for `kubectl --context erun-k3s get nodes` Ready. Teardown (`k3s-uninstall.sh` + unregister the task, optionally `wsl --unregister`) is destructive and operator-initiated, never a maintenance side effect. |

### `erun-orchestrate`

| Field | Value |
|---|---|
| Kind | Workflow — operate as a host-side orchestrator driving and reviewing work across agent environments. |
| Source | `erun-skills/skills/erun-orchestrate/SKILL.md` |
| Description | "Operate as a host-side erun orchestrator that drives and reviews work across agent environments without editing their code locally." |
| Triggers | "orchestrate erun environments", "drive the remote agents", "coordinate work across environments", "review what the agents changed", "review changes across envs", "run the built app to verify", "delegate this to the environment's agent" |
| Inputs | The orchestrator's own configuration — its `ERUN_ORCHESTRATOR_ID` and the `orchestrators:` entry in erun's config store listing its linked `tenant/environment/directory` review windows (authoritative for scope; `erun list` only enumerates what exists) — plus, per linked env, its `type` and its host review directory. A `remote-agent` env's directory is the workspace-sync mirror and its `localrepopath` is the pod-side path where its agent edits; a `local-agent` env's directory *is* its worktree on this machine (its `localrepopath`), which the pod mounts at a different in-pod path, so the host path is never passed into the pod. |
| Outputs | No files. The orchestrator develops changes in the environment's pod (never on the host), driving the env through its **erun MCP** (its `raw`/`build`/`deploy`/`diff`/`outputs_*` tools — never `kubectl`; task the in-pod AI agent through the MCP if the env has one); reviews each env's review directory read-only; and to run an executable on the host has the env cross-build it for the host arch into the pod outputs dir, then runs it to verify. For a `remote-agent` env the directory is a plain one-way mirror with no local git, so the authoritative diff comes from the pod via the MCP's `diff` and artifacts arrive under the mirror's `.erun-outputs/`. For a `local-agent` env the directory is the real worktree the pod builds from, so a host `git diff` there is already authoritative, and artifacts are pulled with `outputs_download` since there is no mirror. A mirror is a read and delivery surface, not a place to build — a sync pass deletes whatever the pod's listing does not contain, so a build tree there is dismantled while the build is still running — and the one host build the skill sanctions (the desktop, whose GUI toolchain is absent from the pod image) happens from a copy taken outside every review directory, with its outputs kept out of the source tree, built from the checkout the build tool itself reads rather than the working directory, and with the previous binary kept beside the new one so a bad build can be put back. Rebuilding erun's own desktop means restarting it, and the restart is bracketed by a **return note**: before triggering it the orchestrator writes `RESUME-NOTE.$ERUN_ORCHESTRATOR_ID.md` in its working directory, holding the task in the operator's words, what is already delivered, what is still in flight (each detached job named by its id, so the resumed session polls it rather than restarting it), what to verify first, and the facts that were expensive to derive — the conversation does not survive the restart and the file does. The id is in the file name because every orchestrator on a host shares one working directory: a note addressed to the directory alone is one any orchestrator can read as its own or overwrite, and the desktop's resume prompt names this exact file so a woken session cannot pick up a neighbour's agenda. The restart itself is triggered with [`erun app restart`](/cli/app) rather than a hand-rolled relauncher: it resolves and verifies the running desktop before touching anything and reports refused/failed/restarted plainly, so a target that no longer resolves is refused outright instead of silently doing nothing, and the after-the-fact confirmation below is the second half of the check rather than the whole of it. On resume it reads its own note and acts on it unprompted instead of assuming the restart handed its instructions back, confirms the rebuilt code is live in the *running* process rather than trusting a clean build log, and treats every environment channel as dropping and re-registering across the restart, so a tool missing right after one is a reconnect in progress rather than a capability to record as absent. Long work is detached and polled rather than held open in one call, and held under an [activity lease](/agent-reference/idle-policy#activity-leases) for its lifetime so the env reports as busy and auto-stop leaves it alone. Each roughly-five-minute pass also re-reads the environment's own [live resource usage](/cli/usage) alongside the job's state, the channel, and the tree's HEAD — the one staleness check that ends the run outright rather than merely misinforming it — and a threshold crossing is acted on — applying the standing recommendation directly with [`erun resize --apply-recommendation`](/cli/resize), or stopping the run — rather than only reported after an OOM kill has already happened. A resize restarts the pod, so it is refused while another worker holds the environment, the same shape as the exclusive worktree lease below: report the holder, don't override reflexively. Before any mutating work in a target environment — a git checkout, staging, a commit — the skill takes the [exclusive worktree lease](/agent-reference/idle-policy#exclusive-claims) first, scoped to the tree it is about to touch (a separate clone of the same repo in the same pod claims its own scope and is unaffected); on refusal it stops and reports the named holder rather than retry-looping or falling back to working in the tree anyway. Before dispatching a lane onto an issue at all, the skill claims it with a `wip:$ERUN_ORCHESTRATOR_ID` label — keyed on the orchestrator id, never the display name, since the id is the only identity a session can resolve about itself — selecting only from issues with no `wip:` label present, refusing to start one that already carries another orchestrator's claim the same way it refuses a held exclusive worktree lease, re-reading the issue's `state` and labels together after adding its own because the write is not compare-and-swap and neither is the issue's own openness, yielding to the lower-sorting orchestrator id on a concurrent double-claim, and dropping its own label and taking different work if that re-read instead finds the issue already closed. That re-read sits immediately before dispatch, not only at selection time, because the gap in between is where the issue's state can move; a lane returning to an issue it previously released re-runs the whole claim from scratch rather than treating the old claim as still standing, since a faithfully-released claim is exactly what leaves that window open for someone else to finish and close the issue first. A `wip:` label discovered on an issue that has since closed — invisible to the `--state open` selection query, so nothing else surfaces it — gets dropped and reported rather than silently cleared. The skill releases its label once the work is merged, abandoned, or handed back rather than leaving it to park the issue for every later lane. Between restarts, the desktop backs the skill's own pacing rule structurally rather than replacing it: a session whose activity report goes stale gets the pacing contract retyped into its pane, and a session whose process exits from a real failure (never a clean exit or the operator's own Stop) is relaunched into the same conversation with a prompt to carry on — see [Periodic pacing re-statement and crash auto-resume](#periodic-pacing-re-statement-and-crash-auto-resume) above. | The skill frames the orchestrator as a self-directing, self-improving agent: a task authorizes the whole flow it implies through verification, release, and redeploy, so ambiguity is resolved by taking the recommended option (listed as assumptions on completion) rather than by asking, and friction met while operating erun is fixed at the source with the lesson written back into the shared guidance — the skill itself included — rather than into a per-tool private note. A check that looks unreachable is treated as a hypothesis rather than an outcome: a GUI-only surface is reached by rebuilding and restarting the tool, a state no fixture produces is looked for in what is already running (the orchestrator's own session included), a failure is never attributed to the environment or to pre-existing state without running the same thing on the unchanged base, and a dead read path is repaired rather than worked around. A gap may still be reported, but it has to name the mechanisms tried. Platform bugs are filed via `erun-file-issue`. |
| Error behaviour | A dropped or stale local MCP port-forward is not the orchestrator's to hand-supervise for `job status`/`job await`/`idle`/`mcp call`/`mcp tools`: each already retries once through `erun open --reconnect` before surfacing the failure, and exits on a distinct code (126) if that retry doesn't recover it — a channel-unreachable exit is never a job's own outcome and must not be read as "finished". Hand-rolled supervision (a raw MCP call, or any command that doesn't yet self-heal) must reattach with `erun open --reconnect`, never a bare `erun open`, which would silently start an environment the operator deliberately stopped and clear the recorded stop; a bound port also proves nothing by itself — prove the tunnel by getting an actual answer through it. An env whose MCP edge is unreachable while its neighbours answer may simply be [stopped](/cli/stop) — its runtime is scaled to zero, so there is no container for the edge to run in. The skill directs the orchestrator to wake it from the host with `erun open --tenant T --environment E --no-shell` and resume over MCP; `erun deploy` does not wake a stopped env, and there is deliberately no MCP `stop`/`start` tool. Editing a review directory is never the path to a change: in a `remote-agent` mirror the edit is a no-op (the next sync reconciles the mirror against the pod's file listing, deleting what the pod does not have and refetching what differs), and in a `local-agent` worktree it *does* reach the pod and collides with the in-pod agent that owns the tree — the skill directs all env access through the env's erun MCP instead, never `kubectl`. If the orchestrator session has no env MCP wired in, that is an erun gap to flag (the desktop must inject each linked env's MCP endpoint + a signed bearer), not a cue to fall back to `kubectl`. A missing `remote-agent` mirror (workspace sync not enabled) → review is unavailable for that env until sync is enabled; linking a `local-agent` env whose repository path is absent is rejected at link time rather than yielding an empty review window. Destructive cross-env actions (deploy, delete) get a heads-up as they run — a notification issued while proceeding, not a gate the orchestrator waits on. A desktop restart-and-resume is refused rather than mis-delivered: the desktop records which conversation asked for the restart and the environment set that conversation was wired to, and if the conversation can no longer be identified or the orchestrator's environments changed in the meantime, the orchestrator reopens **idle** and the reason — naming that orchestrator's own `RESUME-NOTE.<id>.md` — is surfaced beside the orchestrator list instead of a stale conversation being told to carry on in a scope it has never seen. Each restart is staged in its own per-orchestrator slot, so one restart never displaces another's; a launch reopens one orchestrator, and any other orchestrator that restarted mid-task is named in the same notice, with its return note, rather than dropped silently. That is why the return note, not the resume prompt, is the contract — a resume that wakes the session with nothing to act on is a platform defect to file (`erun-file-issue`), and the note is what makes the work recoverable either way. |

### `erun-merge`

| Field | Value |
|---|---|
| Kind | Workflow — takes a finished change from "done" to a review sitting at `READY` on the erun platform, the same rungs the desktop's diff-panel Merge action drives. |
| Source | `erun-skills/skills/erun-merge/SKILL.md` |
| Description | "Take the current branch from \"the work is done\" to a review sitting at READY on the erun platform — resolve or accept a target branch, merge it in, commit and push, open or reuse the review, build and record the result. Stops at READY/FAILED and never advances the merge queue." |
| Triggers | "merge this branch", "land this change", "merge onto main", "advance the merge queue for this branch", "run erun-merge" |
| Inputs | `<targetBranch>`, optional — given, it is the target; omitted, resolved from `erun exec diff --json --scope all`'s `reviewBase.branch` (the same fork-point detection the desktop diff panel uses), stated before acting. A commit message from the operator only if the working tree has uncommitted changes to fold in. A review name only when opening a fresh review and the branch's latest commit subject doesn't already describe it. |
| Outputs | The target branch merged into the current branch with an explicit merge commit (`erun exec merge`, never a rebase); the branch pushed (`erun exec commit`/`erun exec push`); a review opened or reused for the source→target pair (`erun review list` then `erun review create`); the pushed commit built (`erun build --release`) and the result recorded against the review (`erun review record-build`), which is what actually moves it to `READY` or `FAILED` — there is no separate status-setting call. Reports the review id, the build outcome, and the resulting status, then stops. |
| Error behaviour | `erun` not on PATH → stops before touching git, naming `erun cloud init erun` / `erun cloud login` as laptop setup. No erun-type cloud alias configured → surfaces `erun review list`'s own error naming `erun cloud init erun --api-url <url>`; never guesses a URL. A conflicted merge → stops, names every conflicted file, leaves the worktree mid-merge for the operator to resolve or `git merge --abort`; never resolves a conflict itself. A failed `erun build --release` → records the failure against the review (`--failed`, moving it to `FAILED`) rather than retrying on its own; re-running the skill re-runs the build. |

Never advances the merge queue (`erun review queue advance`) or its override (`erun review queue override-advance`) — both are a separate operator decision once a `READY` review's threads are resolved; see [Review loop topology](/collaboration/review-loop-topology)'s `READY, all resolved` row and [Merge queue](/collaboration/merge-queue) for the gate mechanics this skill deliberately does not restate. Every rung checks the state it would produce before acting, so re-running after a partial failure resumes rather than repeating a merge, a push, or opening a second review for the same pair.

### `erun-review`

| Field | Value |
|---|---|
| Kind | Workflow — the reviewer-side counterpart to `erun-merge`: reads a review's diff, leaves line-anchored comments for what should block the merge, and pushes a proposal branch where it has a concrete fix, the same rungs the `erun-reviewer` reusable agent drives. |
| Source | `erun-skills/skills/erun-review/SKILL.md` |
| Description | "Review someone else's branch on the erun platform — read the diff, leave line-anchored comments only for what should actually block the merge, and where there is a concrete fix, push it as a proposal branch the author can take. Stops at \"reported\"; never advances the merge queue, overrides it, closes the review, or resolves a thread it did not open." |
| Triggers | "review this branch", "review the change", "leave review comments", "review PR", "run erun-review" |
| Inputs | `<reviewId>`, optional — given, it is the review; omitted, resolved from `erun review list --source-branch <currentBranch>` when exactly one open review matches. |
| Outputs | Existing comment threads read first (`erun review show`) so no point already made is re-raised; own `OPEN` threads with a reply that genuinely addresses the point resolved (`erun review resolve`); the source fetched and diffed against the target (`git fetch` + `erun exec raw git diff`) without checking out the source branch; line-anchored comments posted (`erun review comment`, body on stdin, paced against the write-endpoint budget) only for findings that should block the merge, with anything advisory folded into one summary comment or dropped; where a finding has a concrete fix, a `proposal/<reviewId>/<slug>` branch pushed from the review's head (never the source branch) and named in a comment with the exact command to take it; the worktree restored to the branch it started on, or the left-checked-out state named plainly if it can't be. Reports the review id, every thread opened or resolved, every proposal branch pushed, and what wasn't reviewed and why, then stops. |
| Error behaviour | `erun` not on PATH → stops before any platform call, naming laptop setup. No erun-type cloud alias configured → surfaces `erun review show`'s own error naming `erun cloud init erun --api-url <url>`; never guesses one. Zero or more than one open review matching the current branch when `<reviewId>` is omitted → stops, asks for it explicitly. A dirty worktree before pushing a proposal → stops and reports the uncommitted state rather than stashing, discarding, or checking out over it. A write call failing mid-batch → stops and reports exactly which comments posted and which didn't; comments are immutable, so there is no retry-in-place. |

The single policy this skill exists to enforce: **a thread is for something that should stop the merge.** Every open thread blocks `erun review queue advance`'s `409` gate until its own root author resolves it or an operator burns an audited `override-advance` — see [Merge queue § The unresolved-thread check](/collaboration/merge-queue#the-unresolved-thread-check). Volume is not thoroughness here; it is a denial of service on the author, so anything advisory goes in one summary comment or isn't raised at all. This skill never advances the queue, never overrides it, never closes the review, and never resolves a thread it did not open — see [Review loop topology § The reviewer must come back](/collaboration/review-loop-topology#the-reviewer-must-come-back) for why only the thread's own author can unblock it.

### `erun-merge-queue-drive`

| Field | Value |
|---|---|
| Kind | Workflow — the execution half of the state-based merge queue: once a review is promoted to `MERGE`, this drives the actual fetch → gate-build → push → report sequence the platform expects "whichever environment gets promoted" to perform itself. **Not automatic**: nothing polls for a `MERGE` promotion and invokes this on its own today — an operator or agent invokes it explicitly against a review that `erun review queue advance`/`override-advance` already promoted. |
| Source | `erun-skills/skills/erun-merge-queue-drive/SKILL.md` |
| Description | "Drive a review's merge-queue gate to completion once it has already been promoted to MERGE — fetch its target and source, build the prospective squash merge, gate it with a real `erun build` (never --release), and push and report MERGED only on green. Stops at MERGED/FAILED and never advances, overrides, or promotes the queue itself." |
| Triggers | "drive the merge queue", "run the merge gate", "gate this promoted review", "build and push the merge queue head" |
| Inputs | `<reviewId>`, required — the review a promotion already targeted. Refuses if the review is not currently `MERGE`. |
| Outputs | The target and source fetched and squash-merged onto a fresh local checkout of the target (`erun exec gate-merge`); the result gated with `erun build` (never `--release`, since the gate publishes nothing); the outcome recorded as a `GATE`-kind build (`erun review record-build --gate`) — successful or failed, this is what actually moves the review to `FAILED` on a bad gate; on success only, the target branch pushed (`erun exec push`) and the review reported `MERGED` (`erun review report-merged`), which the platform verifies against the pushed commit and the target branch's real tip rather than trusting. Reports the review id, source and target branches, the gate build's id and commit, and the resulting status, then stops. |
| Error behaviour | `erun` not on PATH → stops before touching git. The named review is not `MERGE` → stops, naming `erun review queue advance` as the missing prerequisite. The source branch is gone from origin → stops before any mutation. A squash conflict or a failed `erun build` → records a failed `GATE` build against the best available commit and stops; never fabricates a commit to report against, and never resolves a conflict itself. A push refused after a successful gate build (commonly a non-fast-forward) → stops naming it as an anomaly — the queue's own one-merge-in-flight-per-branch invariant means nothing else should have moved the target — rather than retrying blindly. A `report-merged` refusal (`409 MERGE_NOT_VERIFIED`) → surfaces the platform's own reason rather than retrying. |

Never advances, overrides, or promotes the merge queue itself — see [Merge queue](/collaboration/merge-queue) for the gate mechanics and the three conditions `MERGED` is verified against, which this skill's own rungs exist to satisfy honestly rather than assert. Every rung checks the review's actual state before acting, so re-running after a stop at the gate-merge or build step resumes rather than repeating a failed-build report; a stop after the `GATE` build was recorded successful but before push/report needs a human decision (see the skill body's own "Resuming after a partial failure" section) rather than a blind re-run, which would record a second, redundant `GATE` build.

### Catalogue evolution

The catalogue is open — new skills land in `erun-skills/skills/` and ship through both distribution paths automatically. Each skill's `description` is what the Agent matches on, so additions don't require coordinated client changes.

## Adding a custom skill

1. Create `<projectRoot>/.erun/skills/<skill-name>/SKILL.md` with the frontmatter above plus your guidance body.
2. Commit it. Anyone who opens an env on this project picks it up on the next `erun open`.
3. `erun doctor` validates skill bundles on startup — frontmatter parse failures, name mismatches, and missing `SKILL.md` show up as `skill.<name>` check failures.

## Inspecting deployed skills

The MCP `doctor` tool reports the resolved skill set per env:

```jsonc
{
  "checks": [
    {
      "name": "skills",
      "status": "ok",
      "detail": "10 built-in + 2 project skills loaded",
      "skills": [
        { "name": "go-service",  "source": "builtin"  },
        { "name": "house-style", "source": "project"  }
      ]
    }
  ]
}
```

A `raw` listing also works: `ls -la /etc/erun/skills/ ~/.claude/skills/ ~/.codex/skills/`.

## See also

- [Skills](/concepts/skills) — Operator-facing summary.
- [Marketplace distribution](#marketplace) — the plugin manifest and update flow.
- [Conventions](/concepts/conventions) — what the skills teach.
- [Conventions spec](/agent-reference/conventions-spec) — the underlying layout the skills target.

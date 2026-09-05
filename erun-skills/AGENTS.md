# AGENTS.md

Module-area guidance for `erun-skills`. Follow the repository root `AGENTS.md` first.

## Module role

- `erun-skills` is the canonical source of truth for ERun skills (Claude Code / Codex SKILL.md files), ERun's reusable agent definitions (Claude Code subagent `.md` files), and for the Claude Code plugin manifest that distributes both.
- Two consumers vendor from this module:
  - The runtime image bakes `skills/` into `/etc/erun/skills/` (`erun-devops/docker/erun-devops/Dockerfile` `COPY erun-skills/skills /etc/erun/skills`) and `agents/` into `/etc/erun/agents/` (`COPY erun-skills/agents /etc/erun/agents`). The pod entrypoint installs each skill into `~/.claude/skills/<name>/` and `~/.codex/skills/<name>/`, and each agent into `~/.claude/agents/<name>.md` (Codex has no reusable-agent equivalent yet), refreshing an unmodified copy when the image's changed and preserving an in-pod edit — see Editing skills/agents below.
  - The Claude Code marketplace at the repo root (`.claude-plugin/marketplace.json`) publishes this directory as the `erun-tools` plugin via `git-subdir`, so laptop Claude Code can install the same skills via `/plugin install erun-tools@sophium/erun`. A plugin's own `agents/` directory is one of Claude Code's built-in subagent discovery locations, so the same install picks up `erun-builder`/`erun-reviewer` with no extra manifest entry.
- Do not duplicate skill or agent content anywhere else in the repo. Every skill body lives in exactly one `SKILL.md` under `erun-skills/skills/<name>/`; every reusable agent lives in exactly one `<name>.md` under `erun-skills/agents/`.

## Layout

```
erun-skills/
├── AGENTS.md
├── CLAUDE.md                                # symlink → AGENTS.md (repo convention)
├── .claude-plugin/
│   └── plugin.json                          # plugin manifest consumed by Claude Code
├── skills/
│   └── <skill-name>/
│       ├── SKILL.md                         # required; frontmatter + body
│       └── (optional supporting files)      # templates, scripts, reference docs
└── agents/
    └── <agent-name>.md                      # frontmatter + system-prompt body; one file, no bundle
```

- Skills live in their own subdirectory under `skills/`. The directory name is the skill name (e.g. `erun-file-issue`).
- Supporting files (templates, helper scripts) sit alongside `SKILL.md` in the same skill directory. They ship in both consumers automatically.
- Reusable agents are flat files directly under `agents/`, matching Claude Code's own subagent convention (one `.md` file per agent, no directory). See [`agent-reference/agents-spec.md`](https://github.com/sophium/erun/blob/main/erun-docs/docs/agent-reference/agents-spec.md) for the full on-disk format, discovery paths, and the built-in catalogue.
- `plugin.json` declares plugin metadata. Skills and agents are both auto-discovered from their own directories; do not enumerate either in `plugin.json`.

## Naming

- Skill names use `erun-<concern>` in kebab-case. Examples: `erun-file-issue`, `erun-contribute`, `erun-blueprint-rls-db`.
- The skill directory name, the `name:` in the `SKILL.md` frontmatter, and the namespaced invocation (`/erun-tools:<name>`) must all match.
- Do not prefix with the plugin name; the plugin namespace is applied automatically at install time.
- Reusable agents use the same `erun-<concern>` convention (e.g. `erun-builder`, `erun-reviewer`) and share the same namespace as skills — a new agent's name must not collide with an existing skill's, or vice versa. `erun-blueprint-agents` (a skill, scaffolds a repo's `AGENTS.md`) is the one existing name easiest to confuse with the reusable-agent artifact kind itself; keep the two apart in naming and in the docs.

## `SKILL.md` / agent frontmatter rules

- Required: `name:` and `description:`. The `description` is what Claude matches against the user's phrasing, so list the canonical trigger phrases verbatim (e.g. "file erun bug", "open erun issue") for a skill, or the delegation condition for a reusable agent (e.g. "use when running the builder side of the review loop").
- Keep `description` to one sentence + an explicit list of trigger phrases (skills) or delegation condition (agents). Vague descriptions lose to specific ones in skill/agent selection.
- Do not set `disable-model-invocation: true` on a skill unless it is explicitly user-invoke-only.

## Skill body rules

- **Be context-aware.** Skills run both inside a deployed env (where `${ERUN_TENANT}`, `${ERUN_ENVIRONMENT}`, `${ERUN_OUTPUTS_DIR}`, in-pod tools like `erun version`, and the runtime MCP endpoint exist) and on a developer laptop (where none of that does). Gate pod-only fragments behind a check such as `[ -n "${ERUN_TENANT:-}" ]`.
- **Write deliverables to the outputs dir.** When a skill produces a file the operator will want off the pod (a report, generated artifact, build output, log bundle) and it does not belong in the git worktree, write it under `${ERUN_OUTPUTS_DIR}` so `erun outputs list`/`download` can pull it out. Gate on it the same way: `[ -n "${ERUN_OUTPUTS_DIR:-}" ] && cp report.md "${ERUN_OUTPUTS_DIR}/"`. Files that belong in the repo still go to git.
- Reference tools that are reliably present in both contexts (`gh`, `git`, `curl`) or check before use. Do not assume `kubectl`, `helm`, or `erun` are on PATH on a laptop.
- Trigger the skill on **user intent**, not on individual mechanical steps. One workflow → one skill, not three sub-skills the user has to chain.
- Keep skill bodies short. The body is loaded only when the skill fires; long bodies are fine when needed, but every line should be doing work — no "best practices" filler.
- Two skill kinds: **Blueprint** (packages best practices plus a blueprint for building something) and **Workflow** (drives an ERun process, e.g. `erun-file-issue`, `erun-contribute`). Frame Blueprint skills as best-practices-plus-blueprints — never as "scaffolding" or "templates".
- The `erun-contribute` workflow creates a **new** issue and PR (create-issue → implement → PR); it is not for working an already-open ticket.

## Editing skills and agents

- Editing any `SKILL.md` or agent `.md` is a content change in both consumers:
  - Runtime image: triggers a rebuild of `erun-devops` via the standard fingerprint check. No `.erun/config.yaml` fingerprint bump needed; `erun-devops` isn't pinned by base fingerprint.
  - Marketplace: users see the update when the release flow bumps `source.sha` in `/.claude-plugin/marketplace.json`. Bump the SHA in the same commit that publishes the change.
- Do not edit the baked copy at `/etc/erun/skills/<name>/SKILL.md` or `/etc/erun/agents/<name>.md` inside a runtime container. That copy is overwritten on every image rebuild. Edit the source here.
- The entrypoint installs a baked skill/agent when absent and **refreshes** it when the image's copy changed, but **preserves in-pod edits**: an installed `~/.claude/skills/<name>/SKILL.md` that no longer matches the baked hash recorded in its `.erun-skill-baked-sha256` marker was edited in the pod and is left untouched (see `erun-devops/docker/erun-devops/skills-install.sh`). Agents follow the identical policy over a single file instead of a directory: an installed `~/.claude/agents/<name>.md` is checked against a sidecar `<name>.md.erun-agent-baked-sha256` marker (see `erun-devops/docker/erun-devops/agents-install.sh`). So in-pod edits survive restarts and upgrades, while an un-edited skill or agent tracks the image — this is what lets a change here actually reach existing envs on their next boot with a rebuilt image. (One edited in-pod *before* this marker mechanism existed has no marker to prove it was edited, so it is refreshed to the baked version once, on the first boot after the change ships.)

## Validation

- `python3 -c "import json; json.load(open('erun-skills/.claude-plugin/plugin.json'))"` — manifest is valid JSON.
- `python3 -c "import json; json.load(open('.claude-plugin/marketplace.json'))"` — marketplace manifest is valid JSON.
- Build the runtime image (`erun build` per `erun-devops/AGENTS.md`) and confirm `ls /etc/erun/skills/` and `ls /etc/erun/agents/` in the resulting container show every skill directory in `skills/` and every agent file in `agents/`.
- For each skill, exercise its trigger phrasing in a real Claude Code / Codex session and confirm the skill fires and the body produces the intended actions. For each agent, confirm Claude Code's Agent tool lists it and delegating to it by name runs the expected role.
- `sh erun-devops/docker/erun-devops/skills-install_test.sh` and `sh erun-devops/docker/erun-devops/agents-install_test.sh` — run directly (not part of `make check`; see `erun-devops/AGENTS.md`) to lock the install/refresh/preserve/legacy behaviour.

## Docs contract

- Every skill added or changed updates `erun-docs/docs/agent-reference/skills-spec.md` § "Built-in skill catalogue" with: canonical name, `description` frontmatter verbatim, trigger phrases, inputs, outputs, error behaviour. This is the Agent-reference single source of truth for the skill catalogue.
- Every reusable agent added or changed updates `erun-docs/docs/agent-reference/agents-spec.md` § "Built-in agent catalogue" with: canonical name, role, what it watches for, what it does, what it never does.
- The Operator-facing summary in `erun-docs/docs/concepts/skills.md` lists each v1 skill with a one-sentence "what it does". Do not duplicate trigger phrases or schemas there; link to the Agent reference instead.
- Per root `AGENTS.md` § "Working Rules": every feature PR that adds or changes a skill or a reusable agent includes the planned `erun-docs` edits in the same approval step, in the same PR.

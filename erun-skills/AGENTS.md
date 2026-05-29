# AGENTS.md

Module-area guidance for `erun-skills`. Follow the repository root `AGENTS.md` first.

## Module role

- `erun-skills` is the canonical source of truth for ERun skills (Claude Code / Codex SKILL.md files) and for the Claude Code plugin manifest that distributes them.
- Two consumers vendor from this module:
  - The runtime image bakes `skills/` into `/etc/erun/skills/` (`erun-devops/docker/erun-devops/Dockerfile` `COPY erun-skills/skills /etc/erun/skills`). The pod entrypoint installs each skill into `~/.claude/skills/<name>/` and `~/.codex/skills/<name>/` only when the destination doesn't exist.
  - The Claude Code marketplace at the repo root (`.claude-plugin/marketplace.json`) publishes this directory as the `erun-tools` plugin via `git-subdir`, so laptop Claude Code can install the same skills via `/plugin install erun-tools@sophium/erun`.
- Do not duplicate skill content anywhere else in the repo. Every skill body lives in exactly one `SKILL.md` under `erun-skills/skills/<name>/`.

## Layout

```
erun-skills/
├── AGENTS.md
├── CLAUDE.md                                # symlink → AGENTS.md (repo convention)
├── .claude-plugin/
│   └── plugin.json                          # plugin manifest consumed by Claude Code
└── skills/
    └── <skill-name>/
        ├── SKILL.md                         # required; frontmatter + body
        └── (optional supporting files)      # templates, scripts, reference docs
```

- Skills live in their own subdirectory under `skills/`. The directory name is the skill name (e.g. `erun-file-issue`).
- Supporting files (templates, helper scripts) sit alongside `SKILL.md` in the same skill directory. They ship in both consumers automatically.
- `plugin.json` declares plugin metadata. Skills are auto-discovered from `skills/`; do not enumerate them in `plugin.json`.

## Naming

- Skill names use `erun-<concern>` in kebab-case. Examples: `erun-file-issue`, `erun-contribute`, `erun-scaffold-rls-db`.
- The skill directory name, the `name:` in the `SKILL.md` frontmatter, and the namespaced invocation (`/erun-tools:<name>`) must all match.
- Do not prefix with the plugin name; the plugin namespace is applied automatically at install time.

## `SKILL.md` frontmatter rules

- Required: `name:` and `description:`. The `description` is what Claude matches against the user's phrasing, so list the canonical trigger phrases verbatim (e.g. "file erun bug", "open erun issue").
- Keep `description` to one sentence + an explicit list of trigger phrases. Vague descriptions lose to specific ones in skill selection.
- Do not set `disable-model-invocation: true` unless the skill is explicitly user-invoke-only.

## Skill body rules

- **Be context-aware.** Skills run both inside a deployed env (where `${ERUN_TENANT}`, `${ERUN_ENVIRONMENT}`, in-pod tools like `erun version`, and the runtime MCP endpoint exist) and on a developer laptop (where none of that does). Gate pod-only fragments behind a check such as `[ -n "${ERUN_TENANT:-}" ]`.
- Reference tools that are reliably present in both contexts (`gh`, `git`, `curl`) or check before use. Do not assume `kubectl`, `helm`, or `erun-real` are on PATH on a laptop.
- Trigger the skill on **user intent**, not on individual mechanical steps. One workflow → one skill, not three sub-skills the user has to chain.
- Keep skill bodies short. The body is loaded only when the skill fires; long bodies are fine when needed, but every line should be doing work — no "best practices" filler.

## Editing skills

- Editing any `SKILL.md` is a content change in both consumers:
  - Runtime image: triggers a rebuild of `erun-devops` via the standard fingerprint check. No `.erun/config.yaml` fingerprint bump needed; `erun-devops` isn't pinned by base fingerprint.
  - Marketplace: users see the update when the release flow bumps `source.sha` in `/.claude-plugin/marketplace.json`. Bump the SHA in the same commit that publishes the skill change.
- Do not edit the baked copy at `/etc/erun/skills/<name>/SKILL.md` inside a runtime container. That copy is overwritten on every image rebuild. Edit the source here.
- The entrypoint's `[ ! -e ]` guard means user edits to `~/.claude/skills/<name>/SKILL.md` inside a running pod survive pod restarts. That's intentional; do not change it.

## Validation

- `python3 -c "import json; json.load(open('erun-skills/.claude-plugin/plugin.json'))"` — manifest is valid JSON.
- `python3 -c "import json; json.load(open('.claude-plugin/marketplace.json'))"` — marketplace manifest is valid JSON.
- Build the runtime image (`erun build` per `erun-devops/AGENTS.md`) and confirm `ls /etc/erun/skills/` in the resulting container shows every skill directory in `skills/`.
- For each skill, exercise its trigger phrasing in a real Claude Code / Codex session and confirm the skill fires and the body produces the intended actions.

## Docs contract

- Every skill added or changed updates `erun-docs/docs/agent-reference/skills-spec.md` § "Built-in skill catalogue" with: canonical name, `description` frontmatter verbatim, trigger phrases, inputs, outputs, error behaviour. This is the Agent-reference single source of truth for the skill catalogue.
- The Operator-facing summary in `erun-docs/docs/concepts/skills.md` lists each v1 skill with a one-sentence "what it does". Do not duplicate trigger phrases or schemas there; link to the Agent reference instead.
- Per root `AGENTS.md` § "Working Rules": every feature PR that adds or changes a skill includes the planned `erun-docs` edits in the same approval step, in the same PR.

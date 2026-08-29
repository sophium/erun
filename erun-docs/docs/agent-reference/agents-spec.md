---
title: Reusable agents spec
---

# Reusable agents spec

> For the topology these two shipped agents implement, see [Review loop topology](/collaboration/review-loop-topology). For the companion artifact kind, see [Skills spec](/agent-reference/skills-spec) — a **skill** is guidance an Agent loads on demand; a **reusable agent** is a standing role with its own name that an Agent tool can delegate to as a subagent.

A **reusable agent** is a named role — frontmatter plus a system-prompt body — that Claude Code's Agent tool can invoke as a subagent. ERun owns one canonical source (`erun-skills/agents/<name>.md` in the `sophium/erun` repository) and publishes it through the same two paths skills already use: baked into the runtime image for in-pod use, and via the same Claude Code plugin marketplace for laptop use.

**Naming caution:** this is not [`erun-blueprint-agents`](/agent-reference/skills-spec#erun-blueprint-agents) — that is a *skill* for scaffolding a repo-root `AGENTS.md` guidance file. This page specs a different artifact kind: a standing subagent role. Keep the two apart in the docs and in naming; neither name may collide with the other's namespace.

## On-disk format

Unlike a skill (a directory bundle), a reusable agent is **one Markdown file**, matching Claude Code's own subagent convention exactly — there is no supporting-files bundle to carry, so a directory would add nesting with nothing to put in it:

```
erun-skills/agents/
├── erun-builder.md
└── erun-reviewer.md
```

Each file has YAML frontmatter followed by the system prompt as its body:

```markdown
---
name: erun-builder
description: Standing role for an environment where features get built — takes work to READY, evaluates review proposals, executes the merge. Use when the operator asks to run the builder side of the review loop.
---

You are the builder in ERun's review loop...
```

| Field | Required | Validation |
|---|---|---|
| `name` | yes | Matches `^[a-z][a-z0-9-]*$`. Must equal the file's basename (without `.md`). Uniquely identifies the agent. |
| `description` | yes | When Claude should delegate to this subagent — phrased as **when** to use it, matching Claude Code's own subagent frontmatter contract. |

`tools`, `model`, and the other optional fields Claude Code's subagent format accepts are valid but not required; ERun's built-in agents below leave them unset (inherit) unless the catalogue entry says otherwise.

## Per-tool discovery paths

| Agent tool | Discovery path inside the runtime pod |
|---|---|
| `claude` | `~/.claude/agents/<name>.md` |
| `codex` | No reusable-agent equivalent today. **(Planned.)** |

Unlike skills, this is a **one-tool** table today: Codex CLI has no documented subagent-delegation mechanism to install into. A `codex` env still gets the baked `erun-skills/agents/` source (it ships in the same image layer), but nothing installs from it until Codex ships an equivalent.

## Source module

Reusable agents live in exactly one place in the source tree: `erun-skills/agents/<name>.md` in `sophium/erun`. Both the runtime image and the plugin marketplace vendor from this directory, the same way they already vendor `erun-skills/skills/`. Editing an agent is a single-file change; the two distribution paths pick it up automatically.

Module guidance: [`erun-skills/AGENTS.md`](https://github.com/sophium/erun/blob/main/erun-skills/AGENTS.md).

## Deployment mechanism

### In-pod (runtime image)

The runtime Dockerfile vendors the tree with one line, the same shape as skills:

```dockerfile
COPY --chmod=0644 erun-skills/agents /etc/erun/agents
```

On every entrypoint run, `initialize_claude_config` runs `erun-install-agents` (baked to `/usr/local/bin/erun-install-agents`, mirroring `erun-install-skills`'s `install_or_refresh_skill` shape but over flat `<name>.md` files instead of skill directories) over every file under `/etc/erun/agents/`, installing each into `~/.claude/agents/<name>.md`.

The install both **installs an agent when absent and refreshes it when the baked copy changed**, while **preserving in-pod edits** — identical policy to skills, adapted for a single file: provenance is tracked in a sidecar marker (`~/.claude/agents/<name>.md.erun-agent-baked-sha256`) holding the baked file's hash. An installed copy whose content still matches its marker is unmodified since erun installed it and is refreshed to the baked version; one that differs was edited in-pod and is left untouched (a legacy copy with no marker is treated as unmodified and adopted on the first refresh) — see [Skills spec § Deployment mechanism](/agent-reference/skills-spec#deployment-mechanism) for the identical policy skills already ship.

### Laptop (plugin marketplace)

`.claude-plugin/marketplace.json` at the repo root already publishes `erun-skills/` as the `erun-tools` plugin via `git-subdir` (see [Skills spec § Marketplace distribution](/agent-reference/skills-spec#marketplace)). Claude Code's plugin loader discovers a plugin's own `agents/` directory automatically, at the lowest of its three lookup priorities (project `.claude/agents/` beats user `~/.claude/agents/` beats a plugin's `agents/`) — so `erun-skills/agents/` needs no new marketplace wiring: installing `erun-tools@sophium/erun` already carries `erun-builder` and `erun-reviewer` alongside every skill.

## Naming

- Agent names use `erun-<concern>` in kebab-case, the same convention skills use. Examples: `erun-builder`, `erun-reviewer`.
- The file's basename (without `.md`) and the `name:` frontmatter must match.
- A reusable agent's name must not collide with any skill's name in the same namespace — the two loaders are independent, so nothing enforces this automatically; check the [skill catalogue](/agent-reference/skills-spec#built-in-skill-catalogue) by hand before naming a new agent.

## Built-in agent catalogue

The two agents this topology names, both shipped in the runtime image (`/etc/erun/agents/`) and via the plugin marketplace.

### `erun-builder`

| Field | Value |
|---|---|
| Role | Standing role for an environment where features get built. |
| Watches for | Assigned work; its own reviews' comment threads. |
| Does | Implements the assigned work in its own environment. Takes it to `READY` with `/erun-merge` **(Planned.**, [#1516](https://github.com/sophium/erun/issues/1516)**)** rather than hand-rolling the commit/push/open-review sequence. Reads its reviews' threads; for each proposal branch, fetches it, judges it on merit, and merges the ones it accepts — it is not obliged to take a proposal, and replies with why when it declines. Never resolves a thread it did not open; it replies and lets the reviewer close. Once every thread on its review is resolved, runs `erun review queue advance` and lets the gate mint the release. |
| Never does | Call `erun review queue override-advance` as routine — that is a deliberate, separately-authorized escape hatch, not a way to skip a slow reviewer. Resolve a thread it did not open. |

### `erun-reviewer`

| Field | Value |
|---|---|
| Role | Standing role for an environment that reviews. |
| Watches for | `READY` reviews it is a reviewer on. |
| Does | Runs `/erun-review` **(Planned.**, [#1518](https://github.com/sophium/erun/issues/1518)**)**: reads the diff, posts line-anchored comments, and — where it has a concrete fix — pushes a proposal branch the author can take. Returns to reviews it has already commented on, reads the builder's replies, and resolves its own threads once addressed. Opens threads sparingly — every open thread blocks the merge. |
| Never does | Advance the merge queue. Call `override-advance`. Resolve a thread it did not open (it can only resolve its own). |

Both agents pick up their work through `erun review list --waiting-on-me` (the reviewer filter) and the reviews the builder itself opened; assigning a reviewer to a review from any erun client is **(Planned.**, [#1515](https://github.com/sophium/erun/issues/1515)**)**, so populating `--waiting-on-me`'s result today needs direct API access.

## Docs contract

- Every reusable agent added or changed updates this page's catalogue: canonical name, `description` frontmatter verbatim, what it watches for, what it does, what it never does.
- Per root `AGENTS.md` § "Working Rules": every feature PR that adds or changes a reusable agent includes the planned `erun-docs` edits in the same approval step, in the same PR.

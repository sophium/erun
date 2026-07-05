---
name: erun-blueprint-agents
description: Blueprint the repo-root agent-guidance file for an erun tenant project — a canonical AGENTS.md plus a CLAUDE.md symlink (one file, one source of truth) pre-populated with orientation on working in the erun environment — the tenant/environment model, the core erun commands (build, deploy, terraform apply, list, doctor, open), where the deploy artifacts live and the one-version pinning contract, and pointers to the other skills. Idempotent — reconciles a missing or broken symlink and never clobbers hand-authored guidance. Use when the user says "scaffold root AGENTS.md", "add erun agent guidance to this repo", "orient this tenant repo for agents", "create the repo-root CLAUDE.md", "set up AGENTS.md for this erun project", or any similar request to give a tenant repo day-one agent orientation.
---

# Blueprint the repo-root agent guidance

Give an erun tenant repo a repo-root guidance file so any coding agent — or
human — landing in it gets oriented on the erun environment instead of finding
nothing. Produce one canonical `AGENTS.md` at the repository root and a
`CLAUDE.md` symlink pointing to it, so both `AGENTS.md`-readers (Codex) and
`CLAUDE.md`-readers (Claude Code) resolve to the same content.

This skill packages ERun's orientation for a tenant repo — the environment
model, the core commands, where the deploy artifacts live, the one-version
pinning contract, and which skill to reach for. It complements the other
blueprints: `erun-blueprint-platform`, `erun-build-env`, and the module
blueprints emit their own artifacts and each assumes this root file exists (their
module `AGENTS.md` opens with "Follow your repository root `AGENTS.md` first").

## When to use

Trigger on user phrasings such as:

- "scaffold root AGENTS.md" / "create the repo-root CLAUDE.md"
- "add erun agent guidance to this repo" / "orient this tenant repo for agents"
- "set up AGENTS.md for this erun project"

Also apply it as a finishing step from the other blueprint skills: if the
repository root has no `AGENTS.md`/`CLAUDE.md`, scaffold it here.

## What gets produced

```
<repo-root>/
├── AGENTS.md                 # canonical — the guidance content
└── CLAUDE.md  ->  AGENTS.md  # same-directory relative symlink (git mode 120000)
```

The content ships alongside this `SKILL.md` at `templates/AGENTS.md`. Both files
are **committed to git** and shared with the team — never written to
`${ERUN_OUTPUTS_DIR}` (that directory is only for off-pod deliverables).

## Step 1 — resolve the tenant and environment (best effort)

Fill the `<tenant>`/`<env>` placeholders when they are in scope; otherwise leave
the generic pattern text — the guidance is valid either way.

```sh
if [ -n "${ERUN_TENANT:-}" ]; then
    tenant="${ERUN_TENANT}"
    env="${ERUN_ENVIRONMENT:-}"
else
    # On a laptop: `erun list` shows the tenant/env pairs configured for this repo.
    tenant=""   # fill from `erun list` or ask the user
    env=""
fi
```

## Step 2 — write the canonical root file (only if absent)

Check the repo root before writing — never clobber hand-authored guidance:

- If a **regular** (non-symlink) `AGENTS.md` **or** `CLAUDE.md` already exists at
  the root, stop and report it. Offer to fold the erun orientation in only with
  the user's confirmation; do not overwrite.
- Otherwise copy `templates/AGENTS.md` to the repo-root `AGENTS.md`,
  substituting `<tenant>`/`<env>` where resolved.

## Step 3 — create the CLAUDE.md symlink

`AGENTS.md` is canonical; `CLAUDE.md` is a **same-directory relative** symlink to
it — matching erun's own repo convention (edit `AGENTS.md`, never `CLAUDE.md`):

```sh
ln -s AGENTS.md CLAUDE.md   # relative target — never an absolute path, never a copy
```

If a correct `CLAUDE.md -> AGENTS.md` symlink is already present, leave it. If a
`CLAUDE.md` symlink points elsewhere (or is a stray copy that is not
hand-authored content), reconcile it to point at `AGENTS.md`.

## Step 4 — commit

```sh
git add AGENTS.md CLAUDE.md
git commit -m "Add repo-root agent guidance (AGENTS.md + CLAUDE.md symlink)"
```

Commit so the whole team — and every future agent — shares the orientation.

## Maintenance & idempotency

Safe to re-run. On a repo that already carries the file:

- Correct canonical `AGENTS.md` + `CLAUDE.md -> AGENTS.md` symlink present →
  leave it untouched.
- Symlink missing or broken, canonical file present → recreate the symlink
  (Step 3).
- A hand-authored `AGENTS.md`/`CLAUDE.md` present → never clobber; offer to fold
  the erun orientation in with confirmation.

## Windows caveat

`AGENTS.md` is the canonical **regular** file, so it is always readable. On a
Windows checkout without symlink support (`core.symlinks=true` / Developer Mode
off), git materializes `CLAUDE.md` as a plain text file containing the word
`AGENTS.md` rather than a real link — read `AGENTS.md` directly there. The
generated file states this in its own "Agent-guidance files" section.

## Error behaviour

| Failure mode | Recovery |
|---|---|
| A hand-authored `AGENTS.md`/`CLAUDE.md` already exists at the root | Not a stop; do not overwrite. Report it and offer to fold the erun orientation in with the user's confirmation. |
| Tenant/env can't be resolved (not in a pod, no `erun list` match) | Write the file with the generic `<tenant>`/`<env>` pattern text; the guidance is still valid. |
| `ln -s` unavailable (Windows shell without symlink support) | Note the Windows caveat; the canonical `AGENTS.md` still works standalone. Create the symlink via git on a platform that supports it. |
| Not inside a git repository | Write the files; surface that they must be committed once the repo is initialized. |

## Important

- `AGENTS.md` is canonical; `CLAUDE.md` is the symlink. This matches erun's own
  repo convention — do not invert it.
- The symlink target is **relative and same-directory** (`AGENTS.md`), never an
  absolute path and never a copied duplicate.
- Both files are repo content — commit them to git; never write them to
  `${ERUN_OUTPUTS_DIR}`.
- Never clobber a hand-authored root guidance file. Skip, or fold in only with
  confirmation.

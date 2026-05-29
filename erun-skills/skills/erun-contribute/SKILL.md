---
name: erun-contribute
description: Contribute a change to the ERun platform itself — create a new GitHub issue against sophium/erun that captures the work, clone the repo, implement the change following its AGENTS.md rules, and submit a pull request back. Use when the user says "contribute to erun", "make a change to erun", "work on erun", "land a fix in erun", "submit a PR to erun", "propose an improvement to erun", or any phrasing that asks to land a code change in the ERun platform.
---

# Contribute to ERun

Use this skill when the user wants to land a code change in the ERun
platform itself (`sophium/erun`) rather than in the tenant repository.
The skill owns the full motion: **create the GitHub issue → clone →
read AGENTS.md → branch → implement → validate → PR**. Issue creation
is Step 1, not a precondition.

## When to use

Trigger on user phrasings such as:

- "contribute to erun" / "I want to work on erun"
- "make a change to erun" / "propose an improvement to erun"
- "land a fix in erun" / "submit a PR to erun"
- any phrasing that asks for a code change against `sophium/erun`,
  whether the user has thought of it as a bug or as a feature.

Do **not** use this skill when the user already has an issue number and
just wants to pick up an existing ticket — that is a different,
post-issue workflow (clone, branch, implement, PR) that does not need
this skill. This skill is for the case where the work is initiator-driven:
the user has the idea, the skill files it, then ships it.

For reporting a problem with no intent to follow up with a PR, use
`erun-file-issue` instead.

## Inputs to collect

Ask the user once, then proceed:

1. **What's changing** — one or two sentences describing the change.
   This becomes both the issue body and the PR summary.
2. **Title** — sentence-style, under ~70 characters. Used as the issue
   title and as the PR title.
3. **Issue type** — `bug` or `feature` (or `enhancement`). Determines
   the issue label and the branch prefix.
4. **Short kebab-case description** — used in the branch name. Default
   to a slug derived from the title.

Do not invent these. Ask once, then proceed.

## Step 1 — create the issue

```sh
case "${issue_type}" in
    bug)
        label=bug
        body_template="## What happens

<symptom>

## What you expected

<expected>

## Proposed fix

<the change about to be shipped>"
        ;;
    feature|enhancement)
        label=enhancement
        body_template="## Goal

<one paragraph: what the change accomplishes>

## Acceptance criteria

- [bulleted list]

## Proposed approach

<a sentence or two: how the change will be implemented>"
        ;;
    *)
        echo "issue_type must be 'bug', 'feature', or 'enhancement'" >&2
        exit 1
        ;;
esac

issue_url=$(gh issue create --repo sophium/erun \
    --label "${label}" \
    --title "${title}" \
    --body "${body_template}")

issue_number=$(basename "${issue_url}")
echo "Filed issue: ${issue_url}"
```

The issue body should describe the *intended* change, not a
hypothetical request — this is the contributor's own work, anchored to
an issue for tracking. Fill the body from the inputs collected above;
do not leave placeholder text.

If `gh issue create` fails (auth, network, label not allowed), surface
the error and stop. Do not proceed to clone + implement without an
issue number to anchor the PR.

## Step 2 — clone the repository

Pick a clone location that does not collide with the user's current
checkout. Default to `${HOME}/git/erun`. If a clone already exists
there with the same remote, fast-forward it; otherwise clone fresh:

```sh
clone_dir="${HOME}/git/erun"
if [ -d "${clone_dir}/.git" ] && \
   git -C "${clone_dir}" remote get-url origin 2>/dev/null | grep -q 'sophium/erun'; then
    git -C "${clone_dir}" fetch origin main
    git -C "${clone_dir}" checkout main
    git -C "${clone_dir}" merge --ff-only origin/main
else
    rm -rf "${clone_dir}"
    gh repo clone sophium/erun "${clone_dir}"
fi
cd "${clone_dir}"
```

## Step 3 — read the ERun repository's AGENTS.md files (mandatory)

**Claude Code does not auto-reload `CLAUDE.md` after a `cd` mid-session.**
You must explicitly read the `AGENTS.md` files of the cloned repository
*as agent reads* before touching any code. Do this every time you invoke
this skill, even if you have read these files in a prior session — the
rules change.

Required reads (every time, end-to-end):

1. `${clone_dir}/AGENTS.md` — repo-wide rules.
2. Every `AGENTS.md` whose directory is an ancestor of, or contains,
   the files you are about to read, edit, run, or test. Discover them
   with:
   ```sh
   find "${clone_dir}" -name AGENTS.md -not -path '*/node_modules/*'
   ```
   Then `Read` each one that applies to your task subtree.

Apply the rules. They are binding for this work, not advisory.

In particular:

- Branching strategy and PR conventions live in the root `AGENTS.md`
  § "Branching Strategy" and § "Pull Request Titles".
- Module-boundary, naming, and visibility rules apply to every change.
- The integration test gate in `AGENTS.md` § "Integration Test Gate" is
  mandatory for any touch to `erun-cli`, `erun-common`, the runtime
  entrypoint, or chart deploy plumbing.
- For desktop work, read `erun-ui/AGENTS.md` § "UX Impact Review
  Checklist".
- For backend work, read `erun-backend/AGENTS.md` plus the relevant
  `erun-backend-api/AGENTS.md` or `erun-backend-db/AGENTS.md`.
- Per root `AGENTS.md` § "Working Rules": every feature PR must include
  planned `erun-docs` changes (or a one-line justification of why none
  are needed) in the same PR.

## Step 4 — branch

Use the issue-linked naming rules from `AGENTS.md` § "Branching
Strategy":

```sh
case "${issue_type}" in
    bug)              branch_prefix=bug ;;
    feature|enhancement) branch_prefix=feature ;;
esac
branch="${branch_prefix}/${issue_number}-${short_desc}"
git -C "${clone_dir}" checkout -b "${branch}"
```

## Step 5 — implement

Make the change. Hold these rules in mind (lifted from root
`AGENTS.md`):

- One body of work, one PR. Don't split. If unrelated bugs or gaps
  surface while working, fold them into this PR rather than opening a
  new branch.
- Don't introduce abstractions, fallbacks, or backwards-compatibility
  shims beyond what the task requires.
- Action-oriented CLI commands should support `--dry-run`; new commands
  should ship in both CLI and MCP transports unless there is a clear
  repo-specific reason for one only.
- Visibility default is package-private; don't export by reflex.

## Step 6 — validate

The validation set depends on what you touched. As a floor:

- `cd "${clone_dir}" && go test ./...` for any Go change.
- `cd "${clone_dir}" && make integration-test` if you touched
  `erun-cli`, `erun-common`, the runtime entrypoint, or chart deploy
  plumbing. The integration suite includes a coverage gate; it must not
  regress below the configured threshold.
- `cd "${clone_dir}/erun-docs" && yarn install && yarn build` if you
  touched any docs page (`onBrokenLinks: 'throw'` will fail on broken
  links).
- Module-specific suites per the relevant subtree `AGENTS.md`.

Do not skip a failing test to make the gate green. If a preexisting red
exists on `main`, surface it; do not extend the body of work to fix it
unless the user agrees.

## Step 7 — commit

Follow the repo's commit style (one-line subject, body when context
matters). Read `git log --oneline -20` if you are unsure of the local
style. Do not add agent markers like `[codex]` or `[claude]` to commit
messages.

```sh
git -C "${clone_dir}" add <files>
git -C "${clone_dir}" commit -m "<sentence-style subject>" \
    -m "$(cat <<'EOF'
<body explaining the why, not the what>
EOF
)"
```

## Step 8 — push and open the PR

```sh
git -C "${clone_dir}" push -u origin "${branch}"

gh pr create --repo sophium/erun --base main --title "${title}" --body "$(cat <<EOF
## Summary

<1-3 bullets>

## Test plan

- [bulleted list of validation runs]

Closes #${issue_number}
EOF
)"
```

The PR title rule from `AGENTS.md` § "Pull Request Titles" is binding:
clean human title, no agent markers, no `[codex]` / `[claude]`
prefixes. Keep the title under ~70 characters; details go in the body.

## Step 9 — return to the original working directory

Leave the user where they were before the skill fired:

```sh
cd "${ORIGINAL_PWD:-$HOME}"
```

If you were operating inside a tenant repository when the skill was
invoked, the user is now back in their tenant repo and can continue
their primary work.

## Important

- Do not push directly to `main` and do not force-push to shared
  branches.
- Do not skip the `AGENTS.md` reads in Step 3. They are binding even
  when you have seen them before; rules change.
- Do not propose splitting this work into multiple PRs. If scope creeps,
  ask the user before splitting; default is one PR.
- Do not interpret "close" as ending the conversation. In ERun, "close"
  means the full publish flow: push, PR, merge with squash, close
  issue.
- Do not file the issue and then drop the work. The contribute skill is
  for initiator-driven work: the same person who files the issue ships
  the PR. If the user wants to file an issue without following through,
  use `erun-file-issue` instead.

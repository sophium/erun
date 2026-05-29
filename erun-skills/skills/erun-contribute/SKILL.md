---
name: erun-contribute
description: Clone the ERun repository, follow its AGENTS.md rules to implement a change, and submit a pull request back to sophium/erun. Use when the user says "contribute to erun", "make a change to erun", "work on erun", "fix erun bug <id>", "implement erun feature <id>", "land a fix in erun", or any phrasing that asks to develop on the ERun platform itself (not the tenant repository).
---

# Contribute to ERun

Use this skill when the user wants to make a change to the ERun platform
itself (`sophium/erun`) rather than to the tenant repository being worked on.
This skill drives the full clone → develop → PR flow and binds you to the
ERun repository's `AGENTS.md` rules for the duration.

## When to use

Trigger on user phrasings such as:

- "contribute to erun" / "I want to work on erun"
- "make a change to erun" / "land a fix in erun"
- "fix erun bug <number>" / "implement erun feature <number>"
- any phrasing that asks for a code change in `sophium/erun`, not in the
  current tenant repository.

If the user is reporting a problem and hasn't filed an issue yet, run the
`erun-file-issue` skill first to get an issue number, then resume this flow.

## Inputs

- **Issue number** — required. The skill expects an existing
  `https://github.com/sophium/erun/issues/<N>`. If the user doesn't have one,
  file it first via `erun-file-issue`.
- **Issue type** — `bug` or `feature`. Determines the branch prefix.
- **Short kebab description** — used in the branch name, e.g.
  `add-mcp-server-entrypoint`.

## Step 1 — confirm the issue

```sh
gh issue view <N> --repo sophium/erun
```

If the issue does not exist or is closed without merge, stop and surface that.
Do not invent an issue number.

## Step 2 — clone the repository

Pick a clone location that does not collide with the user's current
checkout. Default to `${HOME}/git/erun` (inside an ERun env this matches the
in-pod convention; on a laptop it's a reasonable default). If a clone
already exists there with the same remote, fast-forward it instead of
re-cloning:

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
*as agent reads*, before touching any code. Do this every time you invoke
this skill, even if you have read these files in a prior session.

Required reads (every time, end-to-end):

1. `${clone_dir}/AGENTS.md` — repo-wide rules.
2. Every `AGENTS.md` whose directory is an ancestor of, or contains, the
   files you are about to read, edit, run, or test. Discover them with:
   ```sh
   find "${clone_dir}" -name AGENTS.md -not -path '*/node_modules/*'
   ```
   Then `Read` each one that applies to your task subtree.

Apply the rules. They are binding for this work, not advisory. In particular:

- Branching strategy and PR conventions live in the root `AGENTS.md`
  § "Branching Strategy" and § "Pull Request Titles".
- Module-boundary, naming, and visibility rules apply to every change.
- The integration test gate in `AGENTS.md` § "Integration Test Gate" is
  mandatory for any touch to `erun-cli`, `erun-common`, the runtime
  entrypoint, or chart deploy plumbing.
- For desktop work, read `erun-ui/AGENTS.md` § "UX Impact Review Checklist".
- For backend work, read `erun-backend/AGENTS.md` plus the relevant
  `erun-backend-api/AGENTS.md` or `erun-backend-db/AGENTS.md`.
- Per root `AGENTS.md` § "Working Rules": every feature PR must include
  planned `erun-docs` changes (or a one-line justification of why none are
  needed) in the same PR.

## Step 4 — branch

Use the issue-linked naming rules from `AGENTS.md` § "Branching Strategy":

```sh
case "${issue_type}" in
    bug)     branch="bug/${issue_number}-${short_desc}" ;;
    feature) branch="feature/${issue_number}-${short_desc}" ;;
    *) echo "issue_type must be 'bug' or 'feature'" >&2; exit 1 ;;
esac
git -C "${clone_dir}" checkout -b "${branch}"
```

## Step 5 — implement

Make the change. Hold these rules in mind:

- One body of work, one PR. Don't split. If unrelated bugs or gaps surface
  while working, fold them into this PR rather than opening a new branch.
- Don't introduce abstractions, fallbacks, or backwards-compatibility shims
  beyond what the task requires (root `AGENTS.md` § "Doing tasks").
- Action-oriented CLI commands should support `--dry-run`; new commands
  should ship in both CLI and MCP transports unless there is a clear
  repo-specific reason for one only.
- Visibility default is package-private; do not export by reflex.

## Step 6 — validate

The validation set depends on what you touched. As a floor:

- `cd "${clone_dir}" && go test ./...` for any Go change.
- `cd "${clone_dir}" && make integration-test` if you touched `erun-cli`,
  `erun-common`, the runtime entrypoint, or chart deploy plumbing. Coverage
  threshold (`COVERAGE_THRESHOLD`, default 90%) must hold.
- `cd "${clone_dir}/erun-docs" && yarn install && yarn build` if you
  touched any docs page (`onBrokenLinks: 'throw'` will fail on broken
  links).
- Module-specific suites per the relevant subtree `AGENTS.md`.

Do not skip a failing test to make the gate green. If a preexisting red
exists on `main`, surface it; do not extend the body of work to fix it
unless the user agrees.

## Step 7 — commit

Follow the repo's commit style (one-line subject, body when context matters).
Read `git log --oneline -20` if you are unsure of the local style. Do not
add agent markers like `[codex]` or `[claude]` to commit messages.

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

gh pr create --repo sophium/erun --base main --title "<sentence-style title>" --body "$(cat <<EOF
## Summary

<1-3 bullets>

## Test plan

- [bulleted list of validation runs]

Closes #${issue_number}
EOF
)"
```

The PR title rule from `AGENTS.md` § "Pull Request Titles" is binding: clean
human title that describes the change directly, no agent markers, no
`[codex]` / `[claude]` prefixes. Keep the title under ~70 characters; details
go in the body.

## Step 9 — return to the original working directory

Leave the user where they were before the skill fired:

```sh
cd "${ORIGINAL_PWD:-$HOME}"
```

If you were operating inside a tenant repository when the skill was invoked,
the user is now back in their tenant repo and can continue their primary
work.

## Important

- Do not push directly to `main` and do not force-push to shared branches.
- Do not skip the `AGENTS.md` reads in Step 3. They are binding even when
  you have seen them before; rules change.
- Do not propose splitting this work into multiple PRs. If scope creeps,
  ask the user before splitting; default is one PR.
- Do not interpret "close" as ending the conversation. In ERun, "close"
  means the full publish flow: push, PR, merge with squash, close issue.

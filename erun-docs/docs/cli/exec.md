---
title: erun exec
---

# `erun exec`

Repository helpers that run from the project root. Six subcommands: `diff` (a structured git diff), `raw` (run an arbitrary command), `write` (write file content), `commit` (commit every change), `push` (push a branch to a remote), and `merge` (merge a branch into the current one).

## Synopsis

```
erun exec diff [flags]
erun exec raw COMMAND [ARG...] [flags]
erun exec write PATH [flags]
erun exec commit BRANCH [PATH...] [flags]
erun exec push BRANCH [flags]
erun exec merge TARGET_BRANCH [flags]
```

## Subcommands

### `exec diff`

Shows the current git diff, including untracked files. Outputs raw diff text by default, or structured JSON with `--json` (files, hunks, lines, a directory tree, and the review base + commits).

| Flag | Description |
|---|---|
| `--scope` | `current` (default, worktree), `all` (since the review base), or `commit`. |
| `--selected-commit` | Oldest commit to include when `--scope=commit`. |
| `--json` | Emit the structured diff instead of raw text. |

### `exec raw`

Runs an arbitrary command from the project root. The command runs **directly, not through a shell** — wrap it in `sh -c "…"` if you need shell features. `--dry-run` traces the command (with secret-looking arguments redacted) without running it. Use `--` to pass flags through to the wrapped command rather than to `erun`.

### `exec write`

Writes stdin to PATH inside the project working tree, byte-for-byte, creating parent directories as needed. Content is read from stdin, never a flag value, so nothing in it is ever composed into a shell command — a backtick, a `$(...)`, or a trailing newline in the content lands exactly as given. The write is refused if PATH would resolve outside the project root. `--dry-run` resolves the path and traces the write without performing it. Reports the resolved path and byte count written; add `--output json` for a structured result.

### `exec commit`

Stages every change in the project working tree (`git add -A`) and commits it with a message read verbatim from stdin — same shell-avoidance property as `write`. BRANCH must match the working tree's actual current branch: the commit is **refused, loudly**, when it does not, rather than landing on whatever branch HEAD happens to be on.

Pass one or more PATH arguments to stage and commit only those paths instead of every change — the common case right after `exec write`. A scoped commit is also refused, loudly, if the tree has changes outside the declared paths, so an unrelated writer's edits (a half-finished job, a stray earlier edit) can never be absorbed into a commit that did not ask for them.

`--dry-run` verifies the branch and traces the files that would be committed, without staging or committing. Reports the branch, commit id, and files committed; add `--output json` for a structured result.

### `exec push`

Pushes the project working tree's current branch to a remote (`origin` by default; override with `--remote`). BRANCH must match the working tree's actual current branch — refused, loudly, on mismatch, the same discipline as `commit`. A real, immediate mutation of shared remote state: it's the step that lands a branch somewhere a [review](/cli/review) (or another reviewer) can actually fetch it from — before this command, the only way to push a branch was `erun exec raw git push`.

`--dry-run` verifies the branch and traces the push without running it. Reports the branch, remote, and pushed commit id; add `--output json` for a structured result.

### `exec merge` {#exec-merge}

Fetches TARGET_BRANCH from a remote (`origin` by default; override with `--remote`) and merges it into the project working tree's current branch with an explicit merge commit — **never a rebase**: review comments anchor to a commit id, and a rewrite would orphan every thread on an open review.

A conflicted merge is reported as a distinct, named outcome rather than a generic failure. The worktree is left exactly as git left it, mid-merge — resolve the conflicted files and commit, or run `git merge --abort` to back out, before doing anything else with it.

`--dry-run` traces the fetch and merge without running them. Reports the branch, target branch, remote, and merged commit id; add `--output json` for a structured result.

## Examples

```bash
erun exec diff --scope all
erun exec raw go test ./...
erun exec raw --dry-run -- kubectl get pods --all-namespaces
erun exec write values.yaml < new-values.yaml
echo 'fix the values typo' | erun exec commit main
echo 'fix the values typo' | erun exec commit main values.yaml
erun exec push feature/add-widget
erun exec merge main
```

## Error behaviour

| Failure | Behaviour |
|---|---|
| Not inside a git project. | Errors with "cannot find git project"; nothing runs. |
| `--selected-commit` without `--scope=commit`. | Errors before running git. |
| Wrapped command exits non-zero (`raw`). | Its exit code and stderr propagate; `erun` adds nothing. |
| PATH resolves outside the project root (`write`). | Refuses with `path "..." is outside the working tree "..."`; nothing is written. |
| PATH traverses a symlink between the project root and the target (`write`). | Refuses with `path "..." traverses "...", which is a symlink; writing through a symlink is refused`; nothing is written, even if the symlink's target is itself outside the project root. |
| BRANCH does not match the current branch (`commit`). | Refuses with `refusing to commit: working tree is on branch "X", not the declared "Y"`; nothing is staged. |
| Nothing changed to commit (`commit`). | Refuses with `nothing to commit: the working tree has no changes`. |
| Tree has changes outside the declared PATHs (`commit`). | Refuses with `refusing to commit: the working tree has changes outside the declared paths: ...`; nothing is staged. |
| A PATH argument is blank (`commit`). | Refuses with `path entries must not be blank` rather than falling back to committing everything. |
| BRANCH does not match the current branch (`push`). | Refuses with `refusing to push: working tree is on branch "X", not the declared "Y"`; nothing is pushed. |
| The remote rejects the push (`push`), e.g. a non-fast-forward. | Git's own stderr surfaces verbatim; nothing about the local branch changes. |
| The merge conflicts (`merge`). | Refuses naming every conflicted file and pointing at `git merge --abort`; the worktree is left mid-merge for the caller to resolve. |
| The fetch or merge fails for another reason (`merge`), e.g. an unknown branch. | Git's own stderr surfaces verbatim; the worktree is unchanged. |

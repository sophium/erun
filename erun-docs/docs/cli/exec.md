---
title: erun exec
---

# `erun exec`

Repository helpers that run from the project root. Nine subcommands: `diff` (a structured git diff), `raw` (run an arbitrary command), `write` (write file content), `commit` (commit every change), `push` (push a branch to a remote), `merge` (merge a branch into the current one), `gate-merge` (build the prospective squash merge a merge queue promotion gates), `report-commit-status` (report a GitHub commit status for a merge queue gate result), and `close-pr` (close the GitHub pull request a merge queue gate actually shipped).

## Synopsis

```
erun exec diff [flags]
erun exec raw COMMAND [ARG...] [flags]
erun exec write PATH [flags]
erun exec commit BRANCH [PATH...] [flags]
erun exec push BRANCH [flags]
erun exec merge TARGET_BRANCH [flags]
erun exec gate-merge SOURCE_BRANCH --target TARGET_BRANCH [flags]
erun exec report-commit-status COMMIT --state STATE --description DESCRIPTION --remote-url URL [flags]
erun exec close-pr BRANCH --target TARGET_BRANCH --remote-url URL --gated-commit SHA --landing-commit SHA [flags]
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

### `exec gate-merge` {#exec-gate-merge}

Fetches `--target` and SOURCE_BRANCH from a remote (`origin` by default; override with `--remote`), checks out a fresh local branch named `--target` at its own current remote tip, and squash-merges SOURCE_BRANCH onto it as one commit — the message read verbatim from stdin, same shell-avoidance property as `write`/`commit`.

This is the git half of gating a [merge queue](/collaboration/merge-queue) promotion: the environment a review's merge queue promotes to `MERGE` runs `gate-merge`, then `erun build` against the result, then `erun review record-build --gate` and, only on success, `erun exec push` and `erun review report-merged`.

The working tree must already be clean: unlike `merge`, this checks out a **different** local branch than whatever the tree is currently on, so uncommitted work there is refused rather than silently carried onto the prospective merge or lost.

A conflicted squash is reported as a distinct, named outcome, the same as `merge`. The worktree is left exactly as git left it, mid-conflict — resolve the conflicted files and commit, or run `git merge --abort` to back out.

`--dry-run` traces the fetch, checkout, squash merge, and commit without running them. Reports the target branch, source branch, remote, source commit, and squash merge commit id; add `--output json` for a structured result.

### `exec report-commit-status` {#exec-report-commit-status}

Reports a commit status on GitHub for COMMIT — the last step in the merge queue gate: report `--state success` once the gate build is green, or `--state failure` the moment it is not, naming which gate step failed in `--description`. A required status check on the remote's branch protection has nothing to require until this reports it.

COMMIT should be the review's source branch tip — the pull request's own head commit — never the local prospective squash-merge commit `gate-merge` produces: GitHub only evaluates a required check against a commit reachable from the open pull request, and the squash commit does not exist there until after the gate has already passed and pushed.

`--remote-url` names the github.com remote to report against, in any form git accepts. `--context` names the status check a required-status-checks rule points at (defaults to `erun/merge-gate`); `--target-url` is an optional link a reader clicks through to (e.g. a build log). Reporting needs a GitHub token — `gh auth login`, or `GITHUB_TOKEN`/`GH_TOKEN` in the environment.

`--dry-run` traces the request without sending it.

### `exec close-pr` {#exec-close-pr}

Closes BRANCH's open pull request on GitHub and records `--landing-commit` on it. This runs after [`erun review report-merged`](/cli/review#review-report-merged) has already succeeded: `gate-merge`'s squash commit is never the branch head GitHub tracks, so GitHub never reconciles a queued merge with its pull request on its own, and the commit that actually shipped exists nowhere the pull request can see.

Safe when BRANCH has no open pull request against `--target`: this is a no-op, not an error, since queueing a plain branch with no review is legitimate.

Refuses, loudly, when the pull request's current head does not match `--gated-commit` — something pushed to BRANCH after the gate fetched it, so the gated content is not what closing would discard.

`--remote-url` names the github.com remote the pull request lives on. `--gated-commit` is BRANCH's tip at the moment `gate-merge` fetched and tested it (its own reported `sourceCommit`). `--landing-commit` is the commit that actually landed on `--target` — recorded in a comment on the pull request before it is closed. Closing needs a GitHub token — `gh auth login`, or `GITHUB_TOKEN`/`GH_TOKEN` in the environment.

`--dry-run` traces the lookup without closing or commenting on anything.

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
echo 'Add widget' | erun exec gate-merge feature/add-widget --target main
erun exec report-commit-status $(git rev-parse HEAD) --state success \
  --description 'gate build passed' --remote-url https://github.com/org/repo.git
erun exec close-pr feature/add-widget --target main \
  --remote-url https://github.com/org/repo.git \
  --gated-commit $(git rev-parse origin/feature/add-widget) --landing-commit $(git rev-parse HEAD)
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
| `--target` is missing (`gate-merge`). | Errors with `--target is required` before touching git. |
| The working tree has uncommitted changes (`gate-merge`). | Refuses with `refusing to gate-merge: the working tree has uncommitted changes`; nothing is fetched or checked out. |
| The squash merge conflicts (`gate-merge`). | Refuses naming every conflicted file and pointing at `git merge --abort`; the worktree is left mid-conflict for the caller to resolve. |
| The fetch, checkout, squash merge, or commit fails for another reason (`gate-merge`), e.g. an unknown branch. | Git's own stderr surfaces verbatim; the worktree is left in whatever state that step produced. |
| `--state`, `--description`, or `--remote-url` is missing, or `--state` is not one of `success`/`failure`/`error`/`pending` (`report-commit-status`). | Refuses with a message naming the missing or invalid field; nothing is reported, even under `--dry-run`. |
| `--remote-url` is not a recognized github.com remote (`report-commit-status`). | Refuses with `remote-url "..." is not a recognized github.com remote`. |
| No GitHub token is available (`report-commit-status`). | Refuses with `no GitHub token available to report a commit status; run 'gh auth login' or set GITHUB_TOKEN`; never reaches the network. |
| GitHub rejects the request (`report-commit-status`), e.g. insufficient scope. | GitHub's own response body surfaces verbatim. |
| `--target`, `--remote-url`, `--gated-commit`, or `--landing-commit` is missing (`close-pr`). | Refuses with a message naming that branch, target branch, gated commit, and landing commit are all required; nothing is looked up, even under `--dry-run`. |
| `--remote-url` is not a recognized github.com remote (`close-pr`). | Refuses with `remote-url "..." is not a recognized github.com remote`. |
| BRANCH has no open pull request against `--target` (`close-pr`). | Not an error: reports nothing found and stops: a queued plain branch with no review is legitimate. |
| The pull request's current head does not match `--gated-commit` (`close-pr`). | Refuses with `refusing to close pull request #N for BRANCH: its head is X, not Y — the commit the gate actually tested. ...`; nothing is commented or closed. |
| No GitHub token is available (`close-pr`). | Refuses with `no GitHub token available to close a pull request; run 'gh auth login' or set GITHUB_TOKEN`; never reaches the network. |
| GitHub rejects a request (`close-pr`), e.g. insufficient scope. | GitHub's own response body surfaces verbatim. |

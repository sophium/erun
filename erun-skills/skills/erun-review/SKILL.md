---
name: erun-review
description: Review someone else's branch on the erun platform — read the diff, leave line-anchored comments only for what should actually block the merge, and where there is a concrete fix, push it as a proposal branch the author can take. Stops at "reported"; never advances the merge queue, overrides it, closes the review, or resolves a thread it did not open. Use when the user says "review this branch", "review the change", "leave review comments", "review PR", "run erun-review", or any similar request to review a branch on erun.
---

# Review a branch: /erun-review \<reviewId\>

Reviewer-side counterpart to [`/erun-merge`](../erun-merge/SKILL.md): `/erun-merge`
gets a change *to* `READY`; this is what happens to it there. It reads the
diff, comments on what actually matters, and — where it has a concrete fix —
pushes a proposal branch. `<reviewId>` is optional — given, it is the review;
omitted, this skill resolves it from the current branch (see rung 1).

See [Review loop topology](https://github.com/sophium/erun/blob/main/erun-docs/docs/collaboration/review-loop-topology.md)
for the builder/reviewer roles this skill plays one half of.

## The single most important thing: a thread is for what should stop the merge

**Every open thread blocks the merge queue.** `erun review queue advance`
refuses with `409 Conflict` while any root comment on the head review is
`OPEN` — naming the count, not the content. An agent that posts twenty
nitpick threads and stops has **denied service on the author**: nothing
merges until every one of them is resolved (only the thread's own root
author can do that) or an operator burns an audited, identity-recorded
`override-advance`.

So the policy is not "raise everything you notice." It is:

- **Open a thread only for something that should stop the merge** — a
  correctness bug, a broken contract, a violated hard constraint, a missing
  test for behavior that matters. If you would block the merge over it were
  you the one deciding, it earns a thread. If you would not, it doesn't.
- **Everything advisory goes in one summary comment, or isn't raised at
  all.** Style preferences, alternate phrasings, "consider renaming this" —
  batch them into a single reply-free thread at the end, or skip them. Never
  one thread per nit.
- **When in doubt, it doesn't earn a thread.** Under-raising costs a missed
  nitpick. Over-raising costs the author's ability to merge at all. These are
  not symmetric costs.
- **Volume is not thoroughness here — it is harm.** A review that opens ten
  threads is not more careful than one that opens two; it is ten times more
  likely to leave the author stuck behind a thread nobody comes back to
  resolve.

Everything below exists in service of that policy. Do not let a later rung's
mechanics ("post line-anchored comments") read as license to relitigate it.

## Before doing anything: can this even run here?

```sh
command -v erun >/dev/null 2>&1 || {
  echo "erun is not on PATH. On a laptop: install erun, then 'erun cloud init erun --api-url <url>' and 'erun cloud login --alias <alias>' to connect to the platform this review lives on."
  exit 1
}
```

There is no partial version of this skill — reviewing without the platform
client means no threads, no proposal branch, nothing durable. If no
erun-type cloud alias is configured, the first platform call below
(`erun review show`) fails cleanly on its own and names the fix; do not
guess a URL or an alias.

## The rungs

### 1. Resolve the review

```sh
review_id="${1:-}"
if [ -z "${review_id}" ]; then
  branch=$(git rev-parse --abbrev-ref HEAD)
  review_id=$(erun review list --source-branch "${branch}" --output json \
    | jq -r '[.[] | select(.status != "CLOSED" and .status != "MERGED")] | if length == 1 then .[0].reviewId else "" end')
  if [ -z "${review_id}" ]; then
    echo "Could not resolve exactly one open review for source branch ${branch}. Pass one explicitly: /erun-review <reviewId>"
    exit 1
  fi
fi
detail=$(erun review show "${review_id}" --output json)
source=$(echo "${detail}" | jq -r .review.sourceBranch)
target=$(echo "${detail}" | jq -r .review.targetBranch)
echo "Reviewing ${review_id}: ${source} -> ${target}"
```

State the resolved review, its source, and its target before doing anything
else — same discipline as `/erun-merge`'s target-resolution rung.

### 2. Read every existing thread first

`detail.comments` (from the `erun review show` call above) is every comment
on this review, both open and resolved. Read all of it — root comments and
their replies — before forming any opinion of your own:

```sh
echo "${detail}" | jq -r '.comments[] | "\(.commentId) parent=\(.parentCommentId // "-") status=\(.status) \(.filePath):\(.line): \(.body)"'
```

**Never raise a point an existing thread already makes**, whether that
thread is still `OPEN` or already resolved — a resolved thread's point
already landed once; reopening it under a new comment id is the same waste
as never having read it. This is what makes a re-run over the same review
additive instead of repetitive.

**Resolve your own threads that got addressed.** Filter comments to roots
(`parentCommentId` empty) authored by you
(`self=$(erun platform whoami --output json | jq -r .userId)`) that are
still `OPEN` and have at least one reply. Read the reply. If it genuinely
addresses the point — not merely acknowledges it — resolve it:

```sh
erun review resolve "${review_id}" "${comment_id}"
```

The author accepting the point is the event that earns a resolve, never the
fact that you pushed a proposal in step 6 below. If the reply doesn't
actually address it, leave the thread open and say why in a reply of your
own. **Only resolve a thread whose root comment id you can see was created
by `self` above** — resolving a thread you did not open is always out of
scope, no matter how obviously stale it looks.

### 3. Fetch and diff the branch actually under review

```sh
git fetch origin "${source}" "${target}"
commit=$(git rev-parse "origin/${source}")
erun exec raw git diff "origin/${target}...origin/${source}"
```

This only fetches — it does not check out `origin/${source}`, so whatever
branch your own worktree started on is untouched by this rung. `${commit}`
(the full 40-lowercase-hex hash `origin/${source}` resolved to) is the anchor
every comment in step 5 uses; if the review moves and you run this skill
again, re-fetch and recompute `${commit}` rather than reusing the old value
— an anchor is only valid against the commit it was actually taken from.

### 4. Decide what actually warrants a thread

Apply the policy from the top of this file. For each candidate finding, ask:
would this block the merge if you were the one deciding? If yes, it's a
thread. If no, it goes into one summary comment posted once at the end of
step 5, or not at all.

### 5. Post line-anchored comments — paced

```sh
echo "<finding, wrapped so it reads as a standalone thread>" \
  | erun review comment "${review_id}" --commit "${commit}" --file "${path}" --line "${line}"
```

- The body is piped on stdin, never interpolated into a shell argument or a
  `-m`-style flag — the same discipline `erun exec commit` uses for its own
  message.
- Comments are **immutable** (no edit endpoint) and capped at **8 KiB**
  UTF-8. Get each one right before sending it; there is no revise.
- `--line` must exist in `--file` at `--commit` for the comment to make
  sense — the API does not reject an out-of-range line for you today (there
  is no `422` on this path), so this is on you, not a backstop you can rely
  on.
- **Pace the writes.** Comment/build/resolve calls share a write-endpoint
  budget (documented target: 60 req/min per token — see
  `erun-docs/docs/agent-reference/api-protocol.md#rate-limits`; not yet
  enforced with a `429`, but a large batch should still behave as if it
  were). Post findings in small batches with a short pause between calls
  rather than firing every comment at once. If a call in the batch fails,
  **stop and report exactly which comments posted and which did not** —
  there is no rollback and no retry-until-green; a partial batch is a fact
  to report, not a failure to paper over.

### 6. Propose a concrete fix — as a branch, never the source branch

Where a finding has a concrete fix, push it as a proposal rather than only
describing it in prose.

**Never push to `${source}`.** The reviewer proposes; the author decides.
Pushing onto the branch under review would rewrite the author's own work and
orphan every existing comment's anchor. `erun exec push` enforces a related
but distinct guard — it refuses a branch that is not the worktree's actual
current branch — so this is a stated policy, not only a mechanical one.

```sh
start_branch=$(git rev-parse --abbrev-ref HEAD)
if [ -n "$(git status --porcelain)" ]; then
  echo "Worktree is dirty on ${start_branch}; not touching it. Uncommitted changes:"
  git status --porcelain
  # Stop here — do not stash, discard, or check out over someone's in-progress work.
else
  slug="<short-kebab-case-description>"
  proposal="proposal/${review_id}/${slug}"
  git checkout -b "${proposal}" "origin/${source}"
  # ... apply the fix ...
  echo "<commit message>" | erun exec commit "${proposal}"
  erun exec push "${proposal}"
  git checkout "${start_branch}"
fi
```

Then comment on the review naming the branch and the exact command to take
it:

```sh
echo "Pushed a proposal: ${proposal}. Take it with:
git fetch origin ${proposal} && git merge origin/${proposal}" \
  | erun review comment "${review_id}" --commit "${commit}" --file "${path}" --line "${line}"
```

You are proposing, not deciding — the author judges it on merit and may
decline it.

**Do not strand the environment.** `git checkout "${start_branch}"` above
restores exactly what was checked out before this rung ran. If that restore
itself cannot complete (a real conflict, not just this skill's own doing),
say so plainly in the report: name the branch the worktree is actually left
on and what uncommitted state, if any, sits on top of it. Silence about
where the worktree ended up is not an acceptable outcome.

### 7. Report and stop

State plainly: the review id and the source/target branches reviewed; every
thread opened (file, line, one-line summary) and every thread resolved this
run; every proposal branch pushed and the command to take it; anything you
chose not to review and why; and the branch your own worktree ends this run
on. Then stop.

## What this skill refuses outright, always

- **Pushing to the review's source branch.** Proposals go to
  `proposal/<reviewId>/<slug>`, never onto the branch under review.
- **Advancing the merge queue** (`erun review queue advance`) or
  **overriding its gate** (`erun review queue override-advance`) — both are
  the builder's or an operator's call, never the reviewer's.
- **Closing the review** (`erun review close`).
- **Resolving a thread it did not open** — even one that plainly looks
  stale or addressed. Only that thread's own root author can close it, and
  this skill only ever acts as itself.
- **Re-raising a point an existing thread already makes**, open or
  resolved.
- **Stranding the environment** without saying so — either it restores the
  branch it started on, or it names exactly what it left checked out.

## Resuming after a partial failure

Every rung reads state before acting: step 2 re-derives "already raised"
from the review's current comments, step 5 reports exactly what posted
before a paced batch stopped, and step 6 checks for a dirty tree before
touching the worktree. Re-running `/erun-review <reviewId>` after any
failure picks up from what the review actually shows now, not from an
assumption about what a previous run finished.

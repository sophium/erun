---
title: Merge queue
---

# Merge queue

The merge queue is the only path to `MERGED` — it is what makes two independently-green reviews that break the target branch together impossible, and it carries the one audited escape hatch in the product. It also has a lot of surface: a shape, a gate, a comment-thread check, three clients that can advance it, and a wedge-recovery path. This page is the single account of all of it; [Reviews](/collaboration/reviews) stays the wire-level endpoint reference, [`erun review`](/cli/review) the CLI reference, and the [desktop reviews tab](/desktop/reviews) the app reference — each links here for the mechanics rather than repeating them.

For the standing builder/reviewer roles that drive a review through this queue, see [Review loop topology](/collaboration/review-loop-topology). Getting a reviewer into that role in the first place — assigning or removing one from any client — is [`erun review reviewers`](/cli/review#review-reviewers) (also `review_reviewers_*` over MCP); see [Reviews § Author, reviewers, and discovery](/collaboration/reviews#author-reviewers-and-discovery) for the resource itself.

## Why it exists

Two reviews can each be green on their own and still break the target branch when both land — the second one was only ever tested against a target branch snapshot that the first hadn't touched yet. The merge queue closes that gap by serialising `READY` reviews per target branch: the second review promoted onto a branch is always gated against whatever the first one just landed, never against a stale snapshot.

## Shape of the queue

The queue is **shared per target branch**, not global. Every `READY` review for a given `targetBranch` waits in a single FIFO — `GET /v1/reviews/merge-queue?targetBranch=main` lists it in order. A review that has been promoted (status `MERGE`) has already left that waiting line; only one review per target branch may be `MERGE` at a time, so the review currently being gated and the reviews still waiting are always disjoint sets.

## The gate {#the-gate}

Promoting the head of the queue does real work, not a status flip — but the platform is not the one doing that work. The environment that gets promoted to `MERGE` is expected to fetch its target and source branches itself, build the prospective squash merge of the source onto the *current* target, gate that build with a real build (`erun build`), and push only if it passes: the same workspace, daemon, and warm caches it already has, rather than a separate Job standing up a cold one. `MERGE` is still reached only by promotion (`PATCH .../status` asserting `MERGE` directly is always refused).

`MERGED`, though, is not privileged to a particular caller — any caller may report it, because the platform verifies it rather than trusting who sent it. Before accepting a `PATCH .../status` with `{"status": "MERGED", "buildId": "...", "remoteUrl": "..."}`, it checks all three of:

1. **The build is real.** `buildId` names a `GATE`-kind build already recorded against this exact review, and it succeeded — a caller cannot assert `MERGED` off a build that failed, belongs to a different review, or doesn't exist.
2. **The commit is really there.** Fetching `remoteUrl`, the platform confirms the build's `commitId` is genuinely reachable from the tip of the review's target branch — not just a commit the caller says it made.
3. **It was built on the right base.** The target tip this review was gated against — the merge commit of whichever review most recently reached `MERGED` on the same target branch (or, for the first merge through the queue on a branch, nothing to compare against yet) — has to still be a real ancestor of the reported commit. This tolerates unrelated commits landing directly on the branch in between (a release's own commits, for instance) without treating them as evidence the merge was built on the wrong base; it still refuses a merge whose history never really passed through the gated tip at all — computed against a target that had already moved on and then force-pushed into place — even though the commit it produced is genuinely on the branch.

All three have to hold, or the transition is refused with `409 Conflict` and code `MERGE_NOT_VERIFIED` (see [Reviews § Machine error codes](/collaboration/reviews#machine-error-codes)) — nothing about the review changes. This is a strictly stronger guarantee than trusting a privileged caller: it is a fact about the repository, checkable by fetching the same remote yourself, not a claim believed because of who reported it.

**One drive at a time per environment, enforced rather than assumed.** The gate rewrites the environment's one shared worktree onto the target branch, so two drives in flight there do not merely slow each other down — they corrupt each other's accounting. It has happened: one batch reported pushing a commit that belonged to the other batch's tree, and two pull requests were closed against work that had never landed, because `git rev-parse HEAD` answers whichever drive touched the tree last. A drive therefore claims the environment exclusively for its whole window (`erun activity lease take --exclusive --scope environment`) before reading any review, and `erun exec gate-merge` refuses while anything else holds that claim — a drive that skipped the claim still cannot reach the worktree. The same claim refuses every `erun exec job start` in that environment, which is what keeps a probe or a second gate job from being scheduled beside the gate's build and invalidating its verdict (see root `AGENTS.md` § "A Gate Holds The Environment To Itself" for the measurement: the same gate ran green in ~7 minutes alone and 17 minutes with two reds beside a second batch, both reds on tests that pass standalone). It is a lease, not a lock: it expires without renewal and is reclaimed once its holder is gone, so an interrupted drive cannot pin the environment.

The gate's build is recorded as a [`GATE`-kind build](/collaboration/builds#merge-queue) via the ordinary `POST /builds` route: it publishes nothing, so it carries no `version`, and a failed one carries `failureDetail` in the gate's own words. A successful gate's build becomes the review's `lastMergedBuildId` once `MERGED` is accepted.

The client tooling for this side is [`erun exec gate-merge`](/cli/exec#exec-gate-merge) (fetch every `--source` and the target, then squash-merge each source onto a fresh checkout of the target in turn — repeat `--source` to batch several promoted reviews' branches into one prospective merge, testing whether they compile *together*; a source that conflicts is skipped and recorded rather than failing the whole batch, and a single `--source` is the ordinary one-review gate), [`erun review record-build --gate`](/cli/review#review-record-build) (record the `GATE` build, successful or failed), and [`erun review report-merged`](/cli/review#review-report-merged) (report `MERGED` once the push actually lands — refused with `MERGE_NOT_VERIFIED` otherwise). The `erun-merge-queue-drive` skill chains all three for one or more reviews a promotion already targeted; nothing polls for a promotion and runs it automatically today, so it is invoked explicitly, the same way `advance`/`override-advance` below are.

A queued merge lands a squash commit whose SHA is never the source branch's head, so GitHub cannot reconcile it with the branch's own open pull request the way it reconciles an ordinary `git push` or a `gh pr merge` — the PR stays open forever with no link to what actually shipped. [`erun exec close-pr`](/cli/exec#exec-close-pr) is the follow-up step the `erun-merge-queue-drive` skill runs right after a successful `report-merged`: it finds the source branch's open pull request (a no-op, not an error, when there is none — a queued plain branch is legitimate), refuses loudly if the pull request's head has moved since the gate fetched it, and otherwise comments the landing commit on it and closes it.

A repository merged through a plain GitHub pull request instead of an erun review — never calling `MERGE`/`MERGED` at all — can still require this same gate build via GitHub's own branch protection: [`erun exec report-commit-status`](/cli/exec#exec-report-commit-status) turns the gate build's outcome into a GitHub commit status on the pull request's head commit, which a required-status-checks rule can then require before GitHub allows the merge. This is a separate mechanism from the `MERGE`/`MERGED` verification above — it never touches an erun review at all — but reuses the same gate build a `gate-merge` + real build already produced.

## Watching the gate {#watching-the-gate}

Everything above happens somewhere with no name of its own by default: a gate build is just a job in whichever environment ran it, and a repository merged through a plain pull request (the previous paragraph) has no review at all to look at. [`erun gate list`](/cli/gate#gate-list) is the queue view that answers "what is being gated right now, what is waiting, and what did the last gates decide" without knowing any job id, whether or not an erun review exists for the change: each entry names the branch, the prospective merge commit actually tested, the target, and the verdict — `RUNNING`, `PASSED`, `FAILED`, or `INCONCLUSIVE`.

`INCONCLUSIVE` is not a failure — it means the gate never reached a real verdict at all: a wrapper that hit its own timeout cap, or a run an environment-specific fault (a network blip, a pod eviction) interrupted mid-flight. Treat it as unresolved and worth re-driving, not as a red gate. A `FAILED` entry always names `failingStep` (which gate step actually produced the red verdict) and, when available, `logRef` (where to read it).

A gate run is reported independently of a review's own `GATE` build — `erun exec gate-run start`/`erun exec gate-run report` (also `exec_gate-run_start`/`exec_gate-run_report` over MCP) are the two calls that make an attempt visible, whether or not `reviewId` is set. `erun gate list`/`erun gate show` are the CLI view; the desktop's tenant dashboard (see [Reviews § Gates tab](/desktop/reviews#gates-tab)) and the hosted console's own Gate runs section show the identical queue. See [MCP overview § Gate runs](/mcp/overview#gate-runs) for the full tool spec.

### The desktop app is covered too {#desktop-coverage-gap}

The gate's `erun build` verifies the desktop app the same way it verifies every other module: `erun-devops`'s test stage runs `make check`, and `make check` runs `erun-ui/playwright` as a real `check-gate` prerequisite, so a green `GATE` build against a commit touching `erun-ui/**` means that suite actually ran and passed against that exact commit — not narration, an executed gate. `erun review record-build --gate` no longer takes a desktop-coverage attestation flag; there is nothing left to attest that the build itself doesn't already prove.

This closes what was previously a fail-closed stopgap (issue #1933): the test stage originally had no Wails/webkit toolchain, so the suite couldn't run inside a gate build at all, and a `--desktop-playwright-verified` flag stood in as a manual attestation until the suite was fast, deterministic, and wired into `check-gate` for real. See root `AGENTS.md` § "Integration Test Gate" for the stabilization work and the repeated-run evidence that justified flipping it on.

## Reconciling a bypass {#reconciling-a-bypass}

The queue's own push (`erun exec push` at the end of `The gate` above) is a direct push to the target branch, never a GitHub PR merge — so on a repository whose branch protection requires pull requests, the queue's push structurally needs a ruleset bypass every time. Two separate things follow: **who** may hold that bypass grant, and **what** each exercise of it is accounted for by.

### Narrowing who holds the grant {#narrowing-the-bypass-grant}

Adding a required status check does not narrow anything on its own: GitHub's ruleset bypass is **per-actor and per-ruleset, not per-rule**, so an actor with `bypass_mode: "always"` skips every rule in the ruleset — `required_status_checks` included — whether or not anything ever reported a status. The enforcement that matters is that exactly one nameable, non-human identity can bypass at all.

[`erun exec plan-ruleset-bypass`](/cli/exec#exec-plan-ruleset-bypass) (also `exec_plan-ruleset-bypass` over MCP) resolves that edit from the ruleset as it actually is, and refuses up front on the preconditions that make it safe: the queue identity must already be able to push, GitHub must be showing the ruleset's bypass actors (it returns them only to a token with write access to the ruleset — planning without them would emit an edit that silently drops every actor already there), and `--target-branch` must be a branch this ruleset really governs. It emits two stages plus a rollback and never writes to GitHub itself:

- **Stage 1** grants the queue identity an `always` bypass *alongside* today's actors. Both paths stay open, so the queue can be proven under the new identity before anything is taken away.
- **Stage 2** demotes every other `always` actor to `pull_request` — an emergency lever that still requires opening a pull request, rather than removing a human's escape hatch outright.
- **Rollback** is today's bypass list, exactly as read, so one `PUT` puts it back.

Order matters in one direction only: stage 2 before a real gated merge has run under the new identity is what leaves a branch with no working way in. Verification is per-identity and GitHub answers it directly — `gh api repos/<owner>/<repo>/rulesets/<id> --jq .current_user_can_bypass` returns `always`, `pull_requests_only`, or `never` **for the token that asked**, so running it as each identity is the check that the edit did what it looked like it did.

Two choices the plan deliberately leaves to the operator, because getting them wrong is what breaks a repository:

- **A `bypass_mode` of `exempt` must never be used here.** An exempt actor's push skips enforcement *without being recorded as a bypass*, so it never appears in the ledger the reconciliation below reads — the push becomes invisible rather than accountable. Stage 2 demotes an existing `exempt` actor for the same reason.
- **`erun release` pushes to the same protected branch** (its own tag, packaging-checksum sync, and version-stamp commits) and is a different actor from the gate's push unless it authenticates as the same identity. Whether release shares the queue identity or gets its own grant has to be decided *before* stage 2; leaving it unstated breaks the next release.

### Checking what each bypass landed {#checking-each-bypass}

[`erun exec reconcile-bypass`](/cli/exec#exec-reconcile-bypass) (also `exec_reconcile-bypass` over MCP) reads GitHub's own rule-suites ledger for a ruleset and target branch and accounts for every push that used a bypass:

| Verdict | What it means |
|---|---|
| `RECONCILED` | A `PASSED` gate run built one of the commits this push landed. |
| `RELEASE` | A tag in the repository points at one of them — a release stamps, tags and then pushes, so its own commits were never gated as a merge. |
| `UNEXPECTED_ACTOR` | An identity `--expected-actor` did not name exercised the bypass, whatever the content turned out to be. |
| `UNRECONCILED` | Nothing accounts for what landed. |

A push is matched against **every commit it added** (`before_sha..after_sha`), not only its tip: a release push carries three commits and a batched merge more than one, so tip-only matching would report every release as unaccounted for. Naming `--expected-actor` is what makes the narrowing above observable rather than merely configured — a gated merge pushed by the wrong identity is still a finding.

The command exits non-zero after printing the full report when anything is unaccounted for or any unnamed identity bypassed. Run it on a schedule against the repository's own protected branch and alert on a non-zero exit.

## The unresolved-thread check {#the-unresolved-thread-check}

Before promoting the head review, the queue checks its comment threads. If any thread is still `OPEN` (its root comment unresolved), advancing refuses with `409 Conflict` and a structured body — the one place on this API that uses its own bespoke shape instead of the standard `{code, message, details}` envelope, naming the count and the review so a caller can act on it instead of parsing a sentence:

```jsonc
{
  "error": "unresolved_threads",
  "message": "review rev_01H... has 3 unresolved comment thread(s); resolve them before advancing the merge queue",
  "reviewId": "rev_01H...",
  "unresolvedThreads": 3
}
```

Clearing it takes one of two things: resolve the threads, or use [`override-advance`](#overriding-the-gate). Resolving a thread is itself restricted — **only that thread's own root-comment author can close it** (see [Comments § Open / closed](/collaboration/comments#comment-status)). The builder that opened the review cannot resolve a reviewer's thread no matter how completely it addressed the point; if the reviewer never comes back, the review is stuck behind that thread short of an override. This is deliberate, not an oversight — see [Review loop topology § The reviewer must come back](/collaboration/review-loop-topology#the-reviewer-must-come-back) for why the loop is designed around it.

## Advancing it {#advancing-it}

All three clients do the same thing: promote the queue's current head to `MERGE`. The response in every case is the *promoted* review, not the merged one — the promoted environment is expected to build, push, and report the gate itself (see [The gate](#the-gate)); poll for the terminal `MERGED` or `FAILED` outcome.

### From the API

```
POST /v1/reviews/merge-queue/advance
Content-Type: application/json

{ "targetBranch": "main" }
```

See [Reviews § Endpoints](/collaboration/reviews#endpoints) for the full request/response shape.

### From the CLI

```bash
erun review queue advance --target-branch main
```

See [`erun review queue advance`](/cli/review#review-queue-list--review-queue-advance).

### From the desktop

Tenant dashboard → a review's detail → **Merge queue** tab → **Advance queue**, behind a confirm step. The action is replaced by the missing-access affordance when your account can't use it. See [Desktop reviews § Merge queue and comment threads](/desktop/reviews#merge-queue-and-comment-threads).

## Overriding the gate {#overriding-the-gate}

`override-advance` promotes the head exactly as `advance` does, but skips the unresolved-thread check:

```
POST /v1/reviews/merge-queue/override-advance
Content-Type: application/json

{ "targetBranch": "main", "reason": "hotfix, reviewers unavailable" }
```

`reason` is required — blank or missing is refused with `400 Bad Request` before anything is promoted — and is recorded in the [audit trail](/agent-reference/audit-log) alongside the caller's identity, as an `API`-type event whose `apiPath` is `/v1/reviews/merge-queue/override-advance`. This is the one legitimate escape from the thread gate: deliberate (a distinct call, not a flag on `advance`), accountable (reason + caller both durably recorded), and separately authorized — a tenant can grant `advance` without granting `override-advance`, since they're different API paths.

- **CLI:** `erun review queue override-advance --target-branch main --reason "hotfix, reviewers unavailable"` — see [`erun review queue override-advance`](/cli/review#review-queue-override-advance).
- **Desktop:** same **Advance queue** action, available only to an account with the separate override permission; it bypasses the thread-count refusal by asking for a reason inline.

## When the gate wedges {#when-the-gate-wedges}

If a `MERGE` review's gate never reaches a terminal state — an operator-diagnosed stuck run — requeuing it needs a direct API call today:

```
PATCH /v1/reviews/{reviewId}/status
Content-Type: application/json

{ "status": "READY" }
```

Omitting `buildId` on a `READY` transition is what marks this as the missed-merge-window path rather than a build result: the review moves back to `READY` and rejoins its target branch's queue **at the tail**, not the head — it does not get promoted again immediately. There is no CLI flag or desktop button for this yet; until one exists, an operator (or an Agent with API access) makes this call directly.

## Failure table {#failure-table}

| Refusal | HTTP status | Body | How to unblock |
|---|---|---|---|
| No `READY` review waiting for that target branch (queue empty) | `404 Not Found` | `{code: "EMPTY_QUEUE", message}` (no `details`) | Wait for a review to reach `READY` — its build succeeded — then advance again. |
| Another review is already `MERGE` for that target branch | `404 Not Found` | `{code: "NOT_FOUND", message}` (no `details`) | Wait for it to reach `MERGED`/`FAILED`, or see [When the gate wedges](#when-the-gate-wedges) if it looks stuck. |
| The head review has an unresolved comment thread | `409 Conflict` | structured — `{error, message, reviewId, unresolvedThreads}`, shown [above](#the-unresolved-thread-check) | Resolve the thread (its root author only), or [`override-advance`](#overriding-the-gate). |

The first two rows both 404, but are distinguishable now: [Reviews · Machine error codes](/collaboration/reviews#machine-error-codes) names `EMPTY_QUEUE` for the first case (nothing `READY` waiting), while the second — another review already merging — falls through to the generic `NOT_FOUND` code every 404 gets by default, since that case has no business-specific code of its own.

## See also

- [Reviews](/collaboration/reviews) — the review resource, its status lifecycle, and the merge-queue endpoints' wire contract.
- [Comments](/collaboration/comments) — thread status and the root-author-only close rule the unresolved-thread check depends on.
- [Builds](/collaboration/builds) — the `GATE` build kind the gate writes.
- [`erun gate`](/cli/gate) — the CLI client for gate-run visibility.
- [MCP overview § Gate runs](/mcp/overview#gate-runs) — the `gate_list`/`gate_show`/`exec_gate-run-*` tool spec.
- [`erun review`](/cli/review) — the CLI client.
- [Desktop reviews](/desktop/reviews) — the desktop client.
- [Review loop topology](/collaboration/review-loop-topology) — the builder/reviewer roles that drive a review through this queue.
- [Workflow](/collaboration/workflow) — where `MERGE`/`MERGED` sit in the larger `PR`/`QA`/`DONE` mapping.

---
title: Merge queue
---

# Merge queue

The merge queue is the only path to `MERGED` — it is what makes two independently-green reviews that break the target branch together impossible, and it carries the one audited escape hatch in the product. It also has a lot of surface: a shape, a gate, a comment-thread check, three clients that can advance it, and a wedge-recovery path. This page is the single account of all of it; [Reviews](/collaboration/reviews) stays the wire-level endpoint reference, [`erun review`](/cli/review) the CLI reference, and the [desktop reviews tab](/desktop/reviews) the app reference — each links here for the mechanics rather than repeating them.

For the standing builder/reviewer roles that drive a review through this queue, see [Review loop topology](/collaboration/review-loop-topology). Getting a reviewer into that role in the first place — assigning or removing one from any client — is [`erun review reviewers`](/cli/review#review-reviewers) (also `review_reviewers_*` over MCP); see [Reviews § Author, reviewers, and discovery](/collaboration/reviews#author-reviewers-and-discovery) for the resource itself.

## Why it exists

Two reviews can each be green on their own and still break the target branch when both land — the second one was only ever tested against a target branch snapshot that the first hadn't touched yet. The merge queue closes that gap by serialising `READY` reviews per target branch: the second review promoted onto a branch is always gated against whatever the first one just landed, never against a stale snapshot.

## Shape of the queue

The queue is **shared per repository and target branch**, not global — and not per target branch alone. A tenant may serve more than one repository, and two of them both have a `main`: keyed on the branch alone, one repository's queued work would show up as another's and a promotion would gate a branch that need not exist in the checkout driving it. `GET /v1/reviews/merge-queue?repository=<remote>&targetBranch=main` lists one repository's queue in order; `erun review queue list` derives `--repository` from the checkout it runs in.

Every `READY` review for that pair waits in a single FIFO. A review that has been promoted (status `MERGE`) has already left that waiting line; only one review per repository and target branch may be `MERGE` at a time, so the review currently being gated and the reviews still waiting are always disjoint sets.

A review created before the platform recorded a repository carries none. That is the **absence of an answer, not a second answer**: it is not a repository of its own, so it neither makes a queue ambiguous nor resolves one. One named repository beside any number of such rows is still one repository's queue, and promoting it promotes the row at its head like any other — refusing the whole queue because some of its rows predate repository identity is what once stranded exactly the work a tenant most needed advanced.

The refusal is for a queue that really is several repositories' — `MERGE_QUEUE_AMBIGUOUS` when no repository was named and the queue holds more than one repository's **named** reviews — because promoting "the head" there would gate whichever repository's review happened to sort first, a branch that need not exist in the checkout driving the gate. Naming one is how a mixed queue is resolved.

A row that names no repository is in none of those queues, and the refusal says so: `details.unrecordedRepositoryReviewIds` lists the waiting rows it cannot attribute, by id. They matter to the reader because nothing about naming a repository reaches them — a promotion naming one takes that repository's own head — so a queue holding several named repositories plus such rows leaves those rows waiting until they can be attributed or reconciled (see [Reconciling a review that landed elsewhere](#landed-elsewhere), which adopts the repository the report names for exactly this kind of row).

## The gate {#the-gate}

Promoting the head of the queue does real work, not a status flip — but the platform is not the one doing that work. The environment that gets promoted to `MERGE` is expected to fetch its target and source branches itself, build the prospective squash merge of the source onto the *current* target, gate that build with a real build (`erun build`), and push only if it passes: the same workspace, daemon, and warm caches it already has, rather than a separate Job standing up a cold one. `MERGE` is still reached only by promotion (`PATCH .../status` asserting `MERGE` directly is always refused).

`MERGED`, though, is not privileged to a particular caller — any caller may report it, because the platform verifies it rather than trusting who sent it. The reported `remoteUrl` must name the review's own repository — canonicalized first, so the SSH remote an SSH checkout reports is the HTTPS identity the review recorded. A remote for a different repository is refused rather than used to verify, and a review that recorded no repository adopts the one the report names. Before accepting a `PATCH .../status` with `{"status": "MERGED", "buildId": "...", "remoteUrl": "..."}`, it checks all three of:

1. **The build is real.** `buildId` names a `GATE`-kind build already recorded against this exact review, and it succeeded — a caller cannot assert `MERGED` off a build that failed, belongs to a different review, or doesn't exist.
2. **The commit is really there.** Fetching `remoteUrl`, the platform confirms the build's `commitId` is genuinely reachable from the tip of the review's target branch — not just a commit the caller says it made. The fetch holds no credential, so an SSH `remoteUrl` is read over the same host's HTTPS rather than through an SSH agent the platform does not have; `git remote get-url origin` is a usable value either way.
3. **It was built on the right base.** The target tip this review was gated against — the merge commit of whichever review most recently reached `MERGED` on the same target branch *in the same repository* (or, for the first merge through the queue on a branch, nothing to compare against yet) — has to still be a real ancestor of the reported commit. The repository here is the one `remoteUrl` names, which for a review that recorded none is the identity it is adopting: a tenant serving two repositories that both have a `main` does not let either one's merge commit anchor the other's, and a row carrying no repository is never measured against "every repository at once". This tolerates unrelated commits landing directly on the branch in between (a release's own commits, for instance) without treating them as evidence the merge was built on the wrong base; it still refuses a merge whose history never really passed through the gated tip at all — computed against a target that had already moved on and then force-pushed into place — even though the commit it produced is genuinely on the branch.

All three have to hold, or the transition is refused with `409 Conflict` and code `MERGE_NOT_VERIFIED` (see [Reviews § Machine error codes](/collaboration/reviews#machine-error-codes)) — nothing about the review changes. This is a strictly stronger guarantee than trusting a privileged caller: it is a fact about the repository, checkable by fetching the same remote yourself, not a claim believed because of who reported it.

These three are the conditions for a review sitting at `MERGE`. A review at any other status is not a queued merge and is verified differently, against the target branch's own history — see [Reconciling a review that landed elsewhere](#landed-elsewhere) below.

**One drive at a time per environment, enforced rather than assumed.** The gate rewrites the environment's one shared worktree onto the target branch, so two drives in flight there do not merely slow each other down — they corrupt each other's accounting. It has happened: one batch reported pushing a commit that belonged to the other batch's tree, and two pull requests were closed against work that had never landed, because `git rev-parse HEAD` answers whichever drive touched the tree last. A drive therefore claims the environment exclusively for its whole window (`erun activity lease take --exclusive --scope environment`) before reading any review, and `erun exec gate-merge` refuses while anything else holds that claim — a drive that skipped the claim still cannot reach the worktree. The refusal fires on either scope that covers the worktree being rewritten: the `environment` scope above, and the `worktree` scope an exclusive take defaults to when its caller names none, so a drive that took its claim without `--scope` is protected too. The same `environment` claim refuses every `erun exec job start` in that environment, which keeps a probe or a second gate job from invalidating the verdict; the same gate measured about 7 minutes alone and 17 minutes with two false reds beside a second batch. It is a lease, not a lock: it expires without renewal and is reclaimed once its holder is gone, so an interrupted drive cannot pin the environment.

The gate's build is recorded as a [`GATE`-kind build](/collaboration/builds#merge-queue) via the ordinary `POST /builds` route: it publishes nothing, so it carries no `version`, and a failed one carries `failureDetail` in the gate's own words.

The `RECORDED` build that puts a review into this queue in the first place is the same kind of assertion, one rung earlier: `OPEN` → `READY` runs a plain `erun build` against the pushed commit and records the version it mints. It too publishes nothing, so the version is metadata rather than a claim that a deployable artifact exists at that string — the artifact that ships is cut *after* merge by the release the accepted review enqueues, which mints its own. A `--release` at that step instead publishes a two-architecture `-pr.<sha>` image and chart set per pull request that nothing consumes, and never consults the environment's `docker.platforms` pin; see [Builds § Triggering builds](/collaboration/builds#triggering-builds). A successful gate's build becomes the review's `lastMergedBuildId` once `MERGED` is accepted — which is also what anchors the *next* merge's condition 3 above, so a merge accepted without a gate build (see [Reconciling a review that landed elsewhere](#landed-elsewhere)) deliberately records none and leaves that anchor where it was.

The client tooling for this side is [`erun exec gate-merge`](/cli/exec#exec-gate-merge) (fetch every `--source` and the target, then squash-merge each source onto a fresh checkout of the target in turn — repeat `--source` to batch several promoted reviews' branches into one prospective merge, testing whether they compile *together*; a source that conflicts is skipped and recorded rather than failing the whole batch, and a single `--source` is the ordinary one-review gate), [`erun build --gate`](/cli/build) against that checkout (see [The gate build actually executes](#the-gate-build-actually-executes)), [`erun review record-build --gate`](/cli/review#review-record-build) (record the `GATE` build, successful or failed), and [`erun review report-merged`](/cli/review#review-report-merged) (report `MERGED` once the push actually lands — refused with `MERGE_NOT_VERIFIED` otherwise). The `erun-merge-queue-drive` skill chains all three for one or more reviews a promotion already targeted; nothing polls for a promotion and runs it automatically today, so it is invoked explicitly, the same way `advance`/`override-advance` below are. The squash commit each source lands as keeps that branch's own `Closes #N` and `Reproduces:` / `Regression-Test:` trailers beneath the review name, so an issue declared closed by the branch it fixed is closed on the target the queue lands it, and a later reader can still tell a fixed issue from an open one.

A queued merge lands a squash commit whose SHA is never the source branch's head, so GitHub cannot reconcile it with the branch's own open pull request the way it reconciles an ordinary `git push` or a `gh pr merge` — the PR stays open forever with no link to what actually shipped. [`erun exec close-pr`](/cli/exec#exec-close-pr) is the follow-up step the `erun-merge-queue-drive` skill runs right after a successful `report-merged`: it finds the source branch's open pull request (a no-op, not an error, when there is none — a queued plain branch is legitimate), refuses loudly if the pull request's head has moved since the gate fetched it, and otherwise comments the landing commit on it and closes it.

A repository merged through a plain GitHub pull request instead of an erun review — never calling `MERGE`/`MERGED` at all — can still require this same gate build via GitHub's own branch protection: [`erun exec report-commit-status`](/cli/exec#exec-report-commit-status) turns the gate build's outcome into a GitHub commit status on the pull request's head commit, which a required-status-checks rule can then require before GitHub allows the merge. This is a separate mechanism from the `MERGE`/`MERGED` verification above — it never touches an erun review at all — but reuses the same gate build a `gate-merge` + real build already produced.

## What runs where: the build/platform split {#capability-split}

The gate's steps do not all need the same credentials, and that difference decides which machine can run a drive.

**A build needs no platform credentials.** `erun build` mints its version, builds, and publishes without resolving an erun platform alias at all. With none configured it skips *reporting* its outcome to the platform and carries on — the skip is deliberate, and nothing about the build's own result depends on it.

**Every `erun review` call does.** `erun review list`, `create`, `record-build`, `report-merged`, and the [`erun exec gate-run`](/cli/exec#exec-gate-run-start) family all resolve a configured erun platform cloud alias first, and abort **before any network call** when there is none. They exit with code **127**, not `1` — a distinct code precisely so a script reading only the exit status can tell "this machine cannot reach the platform" apart from "it tried and failed". See [`erun review` § Error behaviour](/cli/review#error-behaviour).

That distinction matters because an **agent environment can hold a platform alias, but cannot sign itself in to get one**:

- `erun cloud init erun --api-url <url>` succeeds unattended — it reads the platform's public `GET /v1/platform` and writes the alias. It performs no sign-in, so the alias it writes has no session behind it.
- `erun cloud login` does not. It completes through an OIDC **Device Authorization Grant** or **Authorization Code + PKCE**, and both need a human at a browser: the device grant requires someone to open the verification URL and approve it, and the PKCE flow's loopback redirect reuses an already-authenticated browser session. No retry, timeout, or piped answer substitutes for that person, so an unattended environment cannot sign *itself* in no matter how long it tries.

The session therefore has to arrive from outside, and it does — by the same route the environment's registry credential takes. **`erun init` resolves the invoking machine's own signed-in erun alias and provisions it into the environment it creates.** The runtime pod mounts it read-only and seeds its cloud config from it at boot, so the environment's `erun` resolves the alias with no interactive step of its own, and it survives pod recreation. An environment created this way can drive the whole queue itself.

Two consequences are worth stating plainly:

- **Which identity it carries depends on whether the platform would mint one.** Where it would, the environment holds a machine identity of its own, scoped to `TenantAgent`, and its calls are attributable to it. Where it would not — a tenant on an identity provider erun does not administer, a host with no signed-in erun alias to ask with — it holds the delegating Operator's alias, so every call is attributed to whoever ran `init` and two environments provisioned from one host are indistinguishable in the audit trail. `erun init` reports which one an environment got. See [Service-account flow for Agents](/agent-reference/api-protocol#service-account-flow-for-agents) for the boundary and [#2684](https://github.com/sophium/erun/issues/2684) for the revocation and migration work still open.
- **It covers only what `erun init` provisioned, from a host that could ask.** An environment created before this existed, or by a host that could not provision for it, keeps what it had — and nothing inside the pod repairs that. Re-running `erun init` from a signed-in host is the fix.

Where an environment holds no alias, a gate drive is a **credentialed-host operation**, run by an orchestrator or operator machine that has `erun cloud login` done and can reach the environment's worktree. The environment contributes the workspace, the daemon, and the warm caches the build runs in — not the record of what it built. Concretely:

| Step | Runs on |
|---|---|
| `erun-merge`: resolve the target, `erun exec merge`, commit, push | The environment |
| `erun-merge`: the already-merged review check, `erun review create`, `erun review record-build` | Either — wherever a usable alias is |
| `erun-merge`: the build whose version that `record-build` carries | Either — the environment has the warm caches, and the build itself needs no alias |
| `erun-merge-queue-drive`: every rung, including resolving each review and reporting `MERGED` | Either — a provisioned environment drives its own queue; otherwise a credentialed host |

Both skills say so instead of discovering it mid-run. `erun-merge` and `erun-merge-queue-drive` probe for a usable alias before they touch git or take the environment claim, and stop there with this split named rather than proceeding into a call that cannot succeed. `erun-merge-queue-drive` stops **before** its exclusive environment claim in particular, so a drive that could never record anything does not reserve the environment and refuse the gate job that could actually run.

One exception on the build side: a project whose configured container registry is the platform-hosted `registry.erunpaas.com` authenticates that push with the operator's own platform bearer token, so the image *push* needs the alias. A registry the tenant runs itself does not.

## Reconciling a review that landed elsewhere {#landed-elsewhere}

Not every change lands through this queue. When a branch is merged on GitHub by **squash merge**, the platform's review row for it could never reach `MERGED`: a squash merge makes none of the branch's own commits ancestors of the target — that is what squashing means — and no `GATE` build was ever recorded for it, because it landed through GitHub rather than the queue. Both of `report-merged`'s queue conditions are therefore unsatisfiable, no matter how long the review sits there.

The only remaining exit used to be [`review close`](/cli/review#review-close), which renders landed work as `CLOSED` — indistinguishable from abandoned, and so a worse signal than leaving it `OPEN`. The result was that such reviews stayed `OPEN` forever: one tenant measured 63 open reviews of which 44 were already on `main`, and the count grew by one for every change that landed this way.

`report-merged` now reconciles these too, and it still verifies rather than believes. Omit `--build-id`, and the platform fetches `--remote-url` and asks the branch's ancestry first: a branch that landed by merge commit or by fast-forward — including one the target was fast-forwarded *onto*, so both refs name the same commit — is confirmed directly, because its tip is in the target's history. A branch sitting exactly on the target's tip is confirmed the same way rather than refused for having no change set of its own: to the two refs, a fast-forward landing and a branch that never committed anything are one shape.

For everything else it compares the **change set**: everything the review's source branch adds, relative to where it diverged from the target, must already be present in the target branch's history. The comparison is of the change set and not of the commit graph, so it holds even when the target advanced under the squash (the ordinary case). A branch whose history is unrelated to the target's, or whose change set no commit on the target carries, names no landing and is refused.

```bash
erun review report-merged 018f... --remote-url https://github.com/org/repo.git
```

- **CLI:** [`erun review report-merged`](/cli/review#review-report-merged) with `--build-id` omitted.
- **MCP:** `review_report-merged` with `buildId` omitted.

A reconciled merge records no `lastMergedBuildId` and triggers no release: there was no build, and the landing it reports already happened elsewhere and published whatever it published. It also does not disturb the queue — the next promotion's own gate still anchors on the last *queue-driven* merge on that branch, so reconciling any number of squash-landed reviews leaves subsequent merges verifying exactly as before.

`CLOSED` reviews are never reconciled: closing is a decision already made and this does not reopen it. A review sitting at `MERGE` is the queue's and still goes through the `GATE`-build path above.

## Watching the gate {#watching-the-gate}

Everything above happens somewhere with no name of its own by default: a gate build is just a job in whichever environment ran it, and a repository merged through a plain pull request (the previous paragraph) has no review at all to look at. [`erun gate list`](/cli/gate#gate-list) is the queue view that answers "what is being gated right now, what is waiting, and what did the last gates decide" without knowing any job id, whether or not an erun review exists for the change: each entry names the branch, the prospective merge commit actually tested, the target, and the verdict — `RUNNING`, `PASSED`, `FAILED`, or `INCONCLUSIVE`.

`INCONCLUSIVE` is not a failure — it means the gate never reached a real verdict at all: a wrapper that hit its own timeout cap, or a run an environment-specific fault (a network blip, a pod eviction) interrupted mid-flight. Treat it as unresolved and worth re-driving, not as a red gate. A `FAILED` entry always names `failingStep` (which gate step actually produced the red verdict) and, when available, `logRef` (where to read it).

A gate run is reported independently of a review's own `GATE` build — `erun exec gate-run start`/`erun exec gate-run report` (also `exec_gate-run_start`/`exec_gate-run_report` over MCP) are the two calls that make an attempt visible, whether or not `reviewId` is set. `erun gate list`/`erun gate show` are the CLI view; the desktop's tenant dashboard (see [Reviews § Gates tab](/desktop/reviews#gates-tab)) and the hosted console's own Gate runs section show the identical queue. See [MCP overview § Gate runs](/mcp/overview#gate-runs) for the full tool spec.

### The gate build actually executes {#the-gate-build-actually-executes}

`erun build --gate` executes the Dockerfile's `test` stage — the one that runs `make check` — instead of accepting a replay of it. Two caches can otherwise stand in for that stage, and only the first is a good trade. erun's own fingerprint cache is refused for such a Dockerfile outright: a fingerprint proves the inputs are unchanged, not that the gate ever ran against them. BuildKit's layer cache sits *underneath* that guard, and when a tree is byte-identical to a previous build it serves the whole stage from its own layers: the build finishes in seconds at zero CPU, `make check` never executes, and the exit code is 0 — indistinguishable from a gate that spent minutes on the tree. `--gate` closes that second door narrowly, with `--no-cache-filter` on that stage alone, so a gate costs the gate and not a cold rebuild of the shared cache that every other stage still uses.

A replayed gate stage is **refused** by every `erun build`, `--gate` or not. A run that watched BuildKit replay a Dockerfile's whole test stage built images without running the project's gate, and no intent on the caller's part changes what the run did; the exit status has to say so, because the exit status is all `erun review record-build --gate` reads. The refusal names the replayed image and the way out: re-run with `--gate`, which executes just that stage.

### The desktop app is covered too {#desktop-coverage-gap}

The gate's `erun build --gate` verifies the desktop app the same way it verifies every other module: `erun-devops`'s test stage runs `make check`, and `make check` runs `erun-ui/playwright` as a real `check-gate` prerequisite, so a green `GATE` build against a commit touching `erun-ui/**` means that suite actually ran and passed against that exact commit — not narration, an executed gate. `erun review record-build --gate` no longer takes a desktop-coverage attestation flag; there is nothing left to attest that the build itself doesn't already prove.

The former manual desktop attestation is no longer used. The gate runs the headless desktop suite with its build dependencies; it does not replace verification of native OS behavior such as macOS GUI integration.

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

Only one review may be at `MERGE` per target branch at a time (`headOfMergeQueue` refuses to promote a second one), so a review that lands at `MERGE` and never reaches a terminal state wedges the whole queue for that branch — nothing else can be promoted until it clears. Two ways this happens in practice:

- An operator-diagnosed stuck gate run: the environment promoted to `MERGE` never reports back `MERGED`/`FAILED` (crashed, evicted, hung).
- A batched `erun exec gate-merge --source A --source B` pushes both branches' commits in one go, but the merge queue still promotes and verifies its members one review at a time. If a member that was never promoted to `MERGE` has its commit land this way, no later `report-merged` for it can ever succeed (there is no `MERGE` review to report), which permanently breaks `gatedTargetTip`'s parent check for whichever review gets promoted next — that review reaches `MERGE`, gate-builds and pushes correctly, and still refuses at `report-merged` with `MERGE_NOT_VERIFIED` because its build's parent no longer matches the platform's own record of the target tip.

`PATCH /v1/reviews/{reviewId}/status` with a bare `{ "status": "READY" }` (no `buildId`) is what recovers it — the same missed-merge-window transition, whichever way the wedge happened:

```
PATCH /v1/reviews/{reviewId}/status
Content-Type: application/json

{ "status": "READY" }
```

Omitting `buildId` on a `READY` transition is what marks this as the missed-merge-window path rather than a build result: the review moves back to `READY` and rejoins its target branch's queue **at the tail**, not the head — it does not get promoted again immediately. Refused with `409 Conflict` and `REVIEW_NOT_MERGING` from any status other than `MERGE`, naming the status the review actually holds — the review was resolved by id, so reporting it as missing would describe something the caller can see.

- **CLI:** `erun review requeue REVIEW_ID` — see [`erun review requeue`](/cli/review#review-requeue). Fetches the review first, so a review that is not at `MERGE` is refused before the write, naming its actual status; the server's own refusal names it too.
- **MCP:** `review_requeue` — same behaviour, `reviewId` the only input.
- Neither takes a reason: unlike [`override-advance`](#overriding-the-gate), this transition bypasses no safety gate, so there is nothing to make accountable.

## Failure table {#failure-table}

| Refusal | HTTP status | Body | How to unblock |
|---|---|---|---|
| No `READY` review waiting for that target branch (queue empty) | `404 Not Found` | `{code: "EMPTY_QUEUE", message}` (no `details`) | Wait for a review to reach `READY` — its build succeeded — then advance again. |
| Another review is already `MERGE` for that target branch | `409 Conflict` | `{code: "MERGE_QUEUE_OCCUPIED", message, details}` — `details` names `targetBranch`, `reviewId`, `name`, `sourceBranch` | Wait for that review to reach `MERGED`/`FAILED`, or `review requeue` it back to `READY` to free the slot — see [When the gate wedges](#when-the-gate-wedges) if it looks stuck. |
| The head review has an unresolved comment thread | `409 Conflict` | structured — `{error, message, reviewId, unresolvedThreads}`, shown [above](#the-unresolved-thread-check) | Resolve the thread (its root author only), or [`override-advance`](#overriding-the-gate). |
| No repository was named and the queue holds more than one repository's reviews | `409 Conflict` | `{code: "MERGE_QUEUE_AMBIGUOUS", message, details}` — `details` names `targetBranch`, the `repositories`, and `unrecordedRepositoryReviewIds` (the waiting rows recording none, empty when there are none) | Name one: `erun review queue advance --repository <remote> --target-branch <branch>`. Rows listed in `unrecordedRepositoryReviewIds` belong to none of the named repositories, so naming one does not reach them — that is what the field is telling you. |
| A `READY` transition with no `buildId` on a review that is not at `MERGE` (the requeue path) | `409 Conflict` | `{code: "REVIEW_NOT_MERGING", message, details}` — `details` names `reviewId` and the `status` the review actually holds | Requeue only recovers a review stuck at `MERGE`; nothing to do for any other status, whose own path applies. |

Only the empty-queue case is a `404`. Every other advance refusal is a `409` that names what is actually in the way, so a reader is never sent after a missing resource: [Reviews · Machine error codes](/collaboration/reviews#machine-error-codes) names `EMPTY_QUEUE` for the empty queue, `MERGE_QUEUE_OCCUPIED` for an occupied slot, `MERGE_QUEUE_AMBIGUOUS` for a queue that is several repositories', and `REVIEW_NOT_MERGING` for a requeue from the wrong status.

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

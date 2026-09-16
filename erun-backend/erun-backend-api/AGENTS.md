# AGENTS.md

Module-specific guidance for `erun-backend-api`. Follow the repository root and `erun-backend/AGENTS.md` first.

## Module Role

- `erun-backend-api` is the Go HTTP API module for hosted ERun backend functionality.
- API endpoints must require an OIDC bearer token unless the endpoint is explicitly infrastructure-only, such as the `/healthz` health check.
- The token `iss` claim determines the tenant. Resolve the tenant before invoking endpoint behavior, then pass tenant identity explicitly through request-scoped context.
- Keep endpoint handlers thin: authenticate, adapt HTTP inputs, call focused workflow or persistence code, and return JSON-safe responses.
- Do not let CLI or MCP import this module directly. Shared clients, request contracts, and result contracts used by CLI and MCP belong in `erun-common`.

## Layer Layout

- Keep each API layer in its own directory under `internal/`.
- Put DB-mapped entities in `internal/model/`.
- Put SQL persistence code in `internal/repository/`.
- Put workflow orchestration in `internal/service/` only when a workflow has real logic beyond calling one repository method.
- Put HTTP route registration, request parsing, and response writing in `internal/routes/`.
- Keep `server.go` as the composition boundary. It should construct repositories, optional services, routes, and middleware, then wire them together.
- Do not create layer directories as empty abstractions. Add a service file only when a service owns behavior.
- Keep imports directional: routes may import repositories or services and model; services may import repositories and model; repositories may import model; model must not import API layers.

## Model Entities

- Use one table-mapped `internal/model` language through repositories, services,
  and routes; no duplicate layer DTO/entity copies.
- Map rows with Bun. Explicit write-column lists, scan-only tags, and database
  defaults/triggers own the write set. Ignore caller IDs, tenant IDs, timestamps,
  authors, and derived fields even if JSON decoding populated them.
- Do not copy a model merely to strip output-only fields. Return models directly
  when their shape fits; use route-local params for genuine partial/path inputs.
  Comment non-obvious derived/read-only fields and omit optional derived output
  where not populated.

## Repository Layer

- Own SQL-visible CRUD/find methods returning model values. Use Get by public ID
  and List with a small local filter rather than multiplying ListBy methods.
  Do not add parent/security IDs only to duplicate route nesting or RLS.
- Use Bun `?` placeholders, not driver-specific `$1`, including security SQL.
  Avoid implicit ORM associations/scoping for tenant-sensitive behavior.
- Transaction wiring reads authenticated context and sets LOCAL role, tenant,
  and user where ownership applies. Use transaction-local settings so pooled
  connections cannot leak identity. Missing context is an internal wiring error.
- Keep IdentityRepository limited to identity resolution/bootstrap; ordinary
  user CRUD belongs in UserRepository. No SQLite fallback.

## Service Layer

- Add services for real workflows, multi-repository coordination, transitions,
  ownership, or transactional writes, not pass-through CRUD.
- Routes may use a repository and service independently. Keep services
  HTTP-neutral and use model values; focused server-derived preparation is valid.

## Routes Layer

- Own path/query/body adaptation, registration, status, and JSON. Protected routes
  rely on middleware auth; do not repeat user-auth checks in every handler.
- Keep request structs local unless a real shared transport contract owns them.
- Classify every route in `routeroles.Routes` and the operator-surface audit.
  A genuinely internal route needs an explicit reason; missing UI is not itself
  evidence it should be exempt. See integration's route gates.

## Reviews And Builds

- Review `name` is the squash merge message.
- Reviews must have both `targetBranch` and `sourceBranch`.
- Review status values include `OPEN`, `CLOSED`, `FAILED`, `READY`, `MERGE`, and `MERGED`; do not remove existing statuses when adding workflow states.
- Successful builds should move `OPEN` or `FAILED` reviews into the per-target-branch merge queue as `READY`.
- A `READY` review becomes `MERGE` only when it is advanced as the next item for that target branch. The promoted environment is expected to build the prospective merge onto the *current* target itself — the property that catches two reviews that are each green alone but broken together before the target branch moves.
- If a `MERGE` review misses its merge window without failing, move it back to `READY` at the end of the same target branch queue.
- Failed builds for queued or merging reviews should move the review to `FAILED` and remove it from the queue.
- `MERGE` is never a caller-reported status — `PATCH /v1/reviews/{review_id}/status` refuses it; only `AdvanceMergeQueue`/`OverrideAdvanceMergeQueue` promoting the queue head reach it. `MERGED` *is* reachable from `PATCH .../status`, from any caller, because acceptance is verified rather than privileged (see "Merge Queue" below): the platform checks the reported `buildId` names an already-recorded, successful `GATE` build for this review, and fetches `remoteUrl` to confirm that build's commit is really on the target branch with the parent this review was gated against. `builds.kind = 'GATE'` marks that build; it carries no version because the gate publishes nothing.
- `CLOSED` reviews must not appear in the merge queue.
- Review list endpoints should support filtering by target branch, source branch, status, author, and reviewer, composable with each other.
- `Review.AuthorUserID` is a read-only, database-defaulted field (`erun_current_user_id()`). Never add it to a repository `Create`/`Update` column list — a client-supplied value in the request body must be ignored, the same as tenant ID and timestamps.
- Reviewer assignment is a separate resource (`ReviewReviewerRepository`, table `review_reviewers`), not a field on `Review`. Adding a reviewer does not gate any review status transition.

### Builds Without A Review (erun#1954)

- Reuse Build with optional ReviewID. An unattached RECORDED build must identify
  tenant/environment/commit/version; environment deletion preserves history by
  setting the reference NULL.
- The unattached POST clears body reviewId and refuses GATE. Review-linked builds
  use the review route; GATE requires a review in service and SQL too.
- Tenant-wide build lists use keyset pagination; per-review lists are bounded by
  the review. Retention is a separate DB policy, not an implicit list-side prune.

## Authentication

- Validate bearer tokens before resolving tenant state.
- Treat missing, malformed, or unverifiable bearer tokens as unauthorized.
- Treat an unknown issuer as unauthorized because it cannot be mapped to an ERun tenant.
- Resolve the ERun user from the token issuer and subject before protected route code runs.
- Store tenant ID, ERun user ID, external issuer, and external user ID in request-scoped security context.
- Avoid package-level mutable auth configuration. Pass verifiers and tenant resolvers through explicit API construction.
- Prefer a single identity resolver when database-backed auth is used, because empty-database bootstrap must resolve or create tenant, issuer, user, roles, and permissions atomically.
- If there are no tenants, the first valid authenticated identity may create the initial `OPERATIONS` tenant and first ERun user. That user must receive both predefined roles: `ReadAll` and `WriteAll` — the platform's own genesis bootstrap, which has no other user yet to administer even the platform's own tenant registration.
- Once any tenant exists, unknown or unregistered issuers are unauthorized, and an unknown external subject is unauthorized for a tenant that already has a user. The one implicit-creation case beyond empty-database bootstrap is per-tenant first-user bootstrap: when a token resolves to a tenant that has zero users, enrol that subject as the tenant's first user. What that user receives depends on the tenant's type (`IdentityRepository.insertTenantFirstUserAccess`): an `OPERATIONS` tenant's first user gets `ReadAll`/`WriteAll` too, the same reach the genesis bootstrap grants, because this tenant's own root-resolution capabilities (registering another tenant, administering the platform's IdP, the one-time bootstrap-name repair) live only behind that wildcard and no other user exists yet in the tenant to grant it later; a `COMPANY` tenant's first user gets `TenantAdmin` instead — full administration of that tenant, without platform-operator reach. This is how a newly-provisioned tenant gets its first admin; for an org-scoped issuer it means the first valid caller in a new org becomes that tenant's admin. Do not create users implicitly in any other case.
- `ReadAll`/`WriteAll` at enrolment is otherwise **not** the default. `UserRepository.Create` (the shared enrolment path behind `POST /v1/users`, `POST /v1/identity/users`, and invite acceptance) grants only the roles named in a caller-supplied `roleIds` when given. Otherwise, the tenant's first user gets `TenantAdmin` (mirroring the sign-in bootstrap bullet above for that path) — granting a role is itself permission-gated, so a first user enrolled with nothing could never be granted anything — and every later enrollment defaults to `TenantUser` rather than the zero-role default this repository shipped briefly: an invited colleague can read the tenant and drive reviews/comments/builds/the merge queue and existing environments immediately, not sit fully capability-less (unable even to read `GET /v1/whoami`) until someone remembers to grant a role by hand. See `internal/routeroles` and the "Predefined Roles" section below for what `TenantUser`/`TenantAdmin` actually grant.
- Operations tenants are system tenants. PostgreSQL RLS allows them to access tenant-owned rows across tenants by setting the transaction role to `erun_operations`, but API authorization must still require assigned roles and permissions.
- Normal tenant transactions must use PostgreSQL role `erun_tenant`; operations tenant transactions must use PostgreSQL role `erun_operations`.

## Authorization

- Permissions are stored in `role_permissions` as either exact API method/path pairs or regex method/path patterns.
- Permission matching must use the canonical API path template set by route registration, such as `/v1/reviews/{review_id}`, not the concrete request URL.
- Keep broad predefined roles pattern-based: `ReadAll` covers all read-style methods across all API paths, and `WriteAll` covers all write-style methods across all API paths.
- **`TenantUser`/`TenantAdmin` are the narrower predefined roles, built from exact routes, never a hand-authored pattern.** `internal/routeroles` (`Routes`, a `map["METHOD /path"]Class`) is the single source of truth for which registered route each grants: `TenantUserClass` (both `TenantUser` and `TenantAdmin` grant it — reading the tenant, driving reviews/comments/builds/the merge queue, operating environments that already exist), `TenantAdminOnly` (only `TenantAdmin` — creating/deleting environments, registering contexts, managing users/invites/roles/org-adjacent settings), or `OperationsOnly` (neither — reachable only through `ReadAll`/`WriteAll`, even from inside an `OPERATIONS` tenant, so `TenantAdmin` stays a genuinely lesser position than the platform operator there). `routeroles.TenantUserPermissions()`/`TenantAdminPermissions()` derive each role's exact `role_permissions` grants directly from that map. `internal/repository/predefined_roles.go`'s `ensureNarrowerRolesExist` creates/re-grants both roles for a tenant lazily and idempotently — at every first-user bootstrap and every `RoleRepository.List` read — so an already-bootstrapped tenant (or one that predates a later route reclassification) picks up the current grant set the next time anything calls it, with no migration backfill.
- **Every registered route must be classified.** `erun-integration`'s role-classification gate (`erun-integration/AGENTS.md` § "Role-classification gate") fails when a route registered in `internal/routes` has no entry in `routeroles.Routes` — the same "classify every route or fail" discipline as its desktop-surface gate, so a route added later cannot silently land inside or outside `TenantUser`/`TenantAdmin`.
- Route handlers should not check role names directly. Authorization middleware should compute access from the authenticated user's assigned roles and permissions before the route handler runs.
- **The caller's effective permission set is reported, not left to be guessed.** `GET /v1/whoami` carries `capabilities`: every registered route this caller would be let through to. It is resolved by the same authorizer that enforces access (`PermissionAuthorizer.PermittedRoutes`, one query and one matcher shared with `Authorize`), over the handler's own route catalog. A second implementation of the decision is a second place for it to be wrong — a client rendering from a capability set that disagrees with enforcement is worse than one with no set at all, because it teaches an operator to expect the wrong thing.
- The candidate set is the routes the handler actually registered (`routeCatalog`), recorded as they register and read per request, so a route added anywhere reaches the answer without a second list to maintain.
- `capabilities` is deliberately not `omitempty`. An empty set means "this caller may do nothing" and a missing field means "this platform cannot say" — a client treats the second as licence to attempt the call, so collapsing them would hide surfaces a caller can use. For the same reason a capability set that cannot be resolved fails the whoami read rather than answering without one.
- **The operations gate stays keyed on tenant type, not roles (recorded decision).** Every user of an `OPERATIONS` tenant inherits that tenant's cross-tenant `erun_operations` reach regardless of which roles they hold, because the gate checks `tenants.type`, not a permission. Moving it to a role-based check is a deliberate, separate security change (narrower blast radius per OPERATIONS user) rather than something to fold into ordinary role-assignment work — it needs its own design pass on what an "operations-scoped" permission means and how it interacts with `erun_operations`'s RLS bypass.

## Identity Resolution Cache

- Authentication middleware may cache issuer, external subject, tenant, and ERun user resolution results with a bounded TTL.
- Cache both successful and failed external identity lookups.
- Failed external identity lookup caching is required so repeated requests with invalid external IDs do not repeatedly hit the database.
- Keep negative-cache TTL short enough that newly created users become usable without a long delay.
- Keep positive-cache TTL bounded so tenant issuer and user mapping changes converge without restarting the API.
- Key identity caches by issuer and external subject. Do not cache only by subject because subjects are issuer-scoped.
- Also key on the resolved org claim value for org-scoped (shared) issuers. Under a shared issuer the same `(issuer, subject)` resolves to different tenants per org, so an `(issuer, subject)`-only key would serve one tenant's resolved RLS context to another. Derive the org before the cache lookup (its claim name lives on `issuers.org_field_key`) and include it in the key; single-tenant issuers resolve org to empty and keep `(issuer, subject)` behavior.
- Do not cache raw bearer tokens as identity keys.
- Do not let identity cache decisions bypass token verification. Verify the bearer token first, then use claims to look up cached tenant/user resolution.
- Cache entries must be safe for concurrent requests and must not use package-level mutable globals. Pass cache instances through API construction.
- Expose cache TTLs as explicit configuration when they become runtime-tunable.

## Audit Logging

- Authentication or request middleware should write audit events for successfully authorized API requests.
- Do not make individual route handlers responsible for routine audit logging.
- Write audit events only after token verification, tenant resolution, user resolution, and endpoint authorization have succeeded.
- API audit events must include tenant ID, ERun user ID, external issuer, external user ID, type `API`, API method, canonical API path, and event time.
- The audit API path must be the same canonical route template used by `role_permissions.api_path`, such as `/v1/reviews/{review_id}`, not a concrete URL containing IDs or query strings.
- Use the canonical route path and method observed by middleware as the audit source. Do not let routes hand-write those values for normal request auditing.
- Register protected routes through the shared route registration helper so the canonical API path is available to authentication, authorization, and audit middleware.
- Audit logging should use the same request-scoped security context that repository transaction wiring uses.
- Audit writes should target PostgreSQL, matching `erun-backend-db` schema guidance.
- Future CLI and MCP audit callers must set type `CLI` or `MCP` and populate `cli_command` or `mcp_tool` respectively. Store parameter payloads as serialized text, preferably compact JSON for structured input.
- MCP-backed platform calls set `Context.MCPTool`; PlatformClient forwards the audit header and middleware records type MCP/tool name. This reclassifies existing API events, not all in-pod tool activity. CLI classification and auditing tools that never call this API remain separate, unimplemented work.
- Treat audit logging failure policy as an explicit API configuration decision. Default request auditing should prefer failing closed only when the endpoint requires audit durability; otherwise log/report the audit failure without hiding authorization failures as unrelated route errors.
- Do not log audit events for requests rejected before authorization, such as missing tokens, invalid tokens, unknown issuers, unknown external users, or denied permissions.

## IDs

- Use UUIDv7 for all externally visible API IDs.
- Keep externally visible ID generation in database defaults. Create routes and repository create methods must not accept or generate those IDs in application code.

## Server-Side Executors

- Work that needs the erun toolchain and does not already have a warm daemon and checkout to run against — env-deploy, stop, and delete — runs as a Kubernetes Job in the tenant's runtime image, never embedded in the API. `internal/jobexec` owns the shared half — creating the Job, watching it to a terminal outcome, and reading the run's own account of a failure off the pod before the TTL reaps it. A new executor supplies only its Job shape and its command; do not write a second launcher.
- Key a Job and its durable workflow by the **attempt**, never by the resource. A name derived from something already terminal makes a retry re-read the previous outcome instead of running — the replay bug behind #1020.
- Derive the attempt suffix from a hash of the whole id, not a slice of it. A UUIDv7's leading characters are its timestamp, so two ids minted milliseconds apart share them and collapse onto one Job name.
- A Job that sets `command` replaces the image's entrypoint, so nothing the entrypoint would have exported is set. Pass what the run needs explicitly (`ERUN_REPO_PATH` is the one that has bitten).
- Record a failure in the run's own words. A reason that says only that a Job exited names nothing an operator can act on.
- Merge gates and releases run in the owning environment's warm checkout/daemon, not cold API-created Jobs. Do not restore removed mergeexec/releaseexec placement configuration.

## Release Queue

- The queue releases what has already been accepted. It does not approve, and it must not grow merge-decision logic.
- `ReleaseService` (`internal/service/releases.go`) owns only `Enqueue`/`Get`: recording a trigger exactly once per `(tenant, commit)`, requeuing a failed one as a new attempt, and answering an already-released commit with the row that already exists. Minting a second version for one merge commit is the worst failure this feature can have.
- **Nothing claims or runs a queued release yet.** `internal/repository/releases.go`'s `ClaimNext`/`RecordOutcome`/`ExpireStale` still hold the serialisation contracts (one running row per tenant via a partial unique index, a cooldown, stale-release expiry) and are exercised directly against Postgres in `release_queue_sql_e2e_test.go`, but no service or route calls them — the Job that used to (`internal/releaseexec`) is gone, and its environment-driven replacement (whichever environment earns the release runs `erun release` itself and reports the build, the same shift `MERGED` made — see "Merge Queue" below) is not wired up. A trigger reliably records `queued`; draining the queue is future work.

### Release cadence policy (#1985, design recorded — not yet implemented)

- Enqueue is idempotent per tenant/commit; the drainer is still unwired.
  ClaimNext's oldest-row/cooldown behavior does not coalesce a batch.
- Proposed drain: newest queued commit, superseding older queued rows, when
  5–10 commits accumulate OR a 30–60 minute cooldown elapses. Preserve one running
  release per tenant, stale-attempt expiry, and per-commit idempotency.
- These are provisional operating thresholds, not measured SLA values. Use real
  per-stage timing before retuning; do not assume one release per merge is affordable.
- A drain must update its gate environment to the published version in the same
  workflow. Release itself never deploys; ordinary tenant upgrades stay discretionary.
- Missing pieces: newest-commit/coalescing claim semantics, an environment-driven
  drainer with outcome reporting and gate-env rollout, and unattended credentials
  described below. Until then, manual policy lives in the merge-queue skill.

## Merge Queue

- The merge queue is what makes `MERGED` mean something happened, not something a caller asserted — but the guarantee is now a fact checked about the repository, not a claim believed because of who reported it. Promoting a review to `MERGE` (`ReviewService.AdvanceMergeQueue`) is the cue for whichever environment gets promoted to fetch the review's target and source, build the prospective squash merge locally, gate it with a real `erun build`, and push only on green — using its own already-warm workspace and daemon, not a Job standing up a cold one.
- `MERGED` is reached by `PATCH /v1/reviews/{review_id}/status` — from any caller — via `ReviewService.acceptMerged`, never by a privileged internal path. Before accepting it, `acceptMerged` checks all three: `verifyGateBuild` confirms `buildId` names an already-recorded, successful `GATE` build against this exact review; `verifyRepositoryState` fetches the caller-supplied `remoteUrl` (`internal/gitverify.RemoteVerifier`, real `go-git`, no `git` binary needed in this container) to confirm the build's commit is really reachable from the target branch's tip, and that `gatedTargetTip` — the merge commit of whichever review most recently reached `MERGED` on the same target branch, or nothing to compare against for the first merge through the queue on a branch — is really its *ancestor*. Any of the three failing refuses with `*MergeNotVerifiedError` (409, `MERGE_NOT_VERIFIED`) and leaves the review at `MERGE`.
- Verify gatedTargetTip ancestry, not immediate-parent equality: release metadata commits may intervene. Preserve refusal for replaced/unrelated target history and acceptance with intervening commits (`internal/gitverify` tests).
- The gate is `erun build`, never `erun release`. The gate publishes nothing — releasing stays exactly where #1030 put it, after the merge lands, triggered off the merge commit the gate produced. `ReviewService` calls `ReleaseTrigger.TriggerRelease` directly on a successful `acceptMerged` — no cycle, because `ReleaseService` does not depend back on `ReviewService` the way the old Job-dispatching `MergeQueueService` had to avoid one.
- A failed `GATE` build does not need this verification: reporting one through the ordinary `POST /builds` route reaches `ReviewService.MarkBuildResult`, which (being kind-agnostic) already moves a `MERGE` review straight to `FAILED` exactly as a failed `RECORDED` build would. Only a *successful* `GATE` build's `MERGED` claim needs the separate, verified `PATCH .../status` call.
- One merge in flight per `(tenant, target_branch)` falls out of the existing invariant `AdvanceMergeQueue` already enforced: it refuses to promote while a `MERGE` review is active on that branch. Because the head is always gated against the *current* target, re-testing after each landed merge is automatic rather than a separate invalidation pass — and it is what makes reading `gatedTargetTip` at verification time equivalent to reading it at promotion time: nothing else can move a target branch's recorded tip while this review is the one holding `MERGE` on it.
- A build reported for one review can promote a *different* review to `MERGE`. Every call site that can produce a promotion has to observe the promoted review, not assume it is the one it was already looking at.
- Unattended agent platform credentials remain blocked by the design below; do not imply a fresh pod can self-authenticate.

### What a hand-run merge-queue script should hand off to erun vs keep as operator policy (#1987)

Caller scheduling, batch choice/order, log capture, and failure triage belong in
`erun-skills/skills/erun-merge-queue-drive/SKILL.md`. Shared batch execution and
infrastructure classification belong in `erun-common/AGENTS.md`.

The API owns these distinct records: one gate_run per batch attempt; an individual
review's GATE build is separate. Reporting N reviews MERGED from one batch build
is still an unresolved verification design, not implied by batch support.

## Gate Runs

- A gate run (`GateRunService`, `GET`/`POST /v1/gate-runs`, `GET`/`PATCH /v1/gate-runs/{gate_run_id}`) is the first-class record of one attempt to gate a prospective merge, independent of whether an erun review exists for the change (erun#1931). A review-driven merge's `GATE` build is one producer of a gate run (the environment driving `erun-merge-queue-drive` reports both); a repository whose changes arrive as GitHub pull requests, with no erun review at all, is the other — the reason this is a table of its own rather than an extension of `reviews`/`builds`: forcing every gated branch to open a review would conflate a code-review workflow (assignments, comment threads) with a CI-verification concern that has no need for either, and `builds.successful` is a plain boolean with no way to represent "never reached a verdict" without a review's own FAILED transition firing on it.
- `GateRunStatus` is `RUNNING`, `PASSED`, `FAILED`, or `INCONCLUSIVE`. **A verdict must be distinguishable from a non-verdict**: a wrapper that hits its own timeout cap, or a run interrupted by an environment-specific fault (a network blip, a pod eviction), reports `INCONCLUSIVE`, never `FAILED` — `FAILED` asserts a real verdict was reached and a real gate step produced it. `Start` may be called already carrying a terminal status (`MergeCommit` empty, `FailingStep` set) for a run that never had a trackable "running" phase at all, such as a squash conflict before any build starts. This distinction used to depend entirely on driver judgment call by call — `INCONCLUSIVE` existed in the enum with nothing that ever set it automatically. `erun-common/gate_run_failure_classifier.go` now closes that: `RunGateRunStart`/`RunGateRunReport` reclassify a caller-reported `FAILED` as `INCONCLUSIVE` automatically when `FailingStep`/`LogRef` names one of erun's own known infrastructure signatures (see "What a hand-run merge-queue script should hand off to erun" above).
- A `FAILED` report must always carry `FailingStep` (which gate step produced the red verdict); `LogRef` is an optional pointer to where to read it. Neither is required for `INCONCLUSIVE`, since the whole point is that the gate itself never reached a real verdict to name a step for.
- `ReportOutcome` refuses reporting against a gate run that is not `RUNNING` (`*GateRunAlreadyDecidedError`, 409): a verdict is immutable once reached, so a caller cannot silently overwrite one gate run's outcome with another.
- `ReviewID` is nullable and, when set, links a gate run to the review it gates — but nothing here writes to `reviews`/`builds`, and nothing there writes to `gate_runs`; the two are independent records a caller may report to either or both of, not one driving the other.
- `GET /v1/gate-runs`/`GET /v1/gate-runs/{gate_run_id}` are classified normally now (erun#1932): a real operator surface reads them in both `erun-console` (`src/gateRuns/`) and `erun-ui/frontend` (the tenant dashboard's Gates tab). They are no longer in `route_audit.go`'s `InternalAPIRoutes` — that entry was a deliberate, temporary scope decision for #1931's CLI-first delivery, not a claim they needed no operator surface forever. `POST /v1/gate-runs` and `PATCH /v1/gate-runs/{gate_run_id}` are internal permanently: the environment driving the gate reports its own attempt, the same self-report shape as `POST /v1/reviews/{review_id}/builds`.

### GitHub branch protection cannot tell the queue's push from a bypass (#1912)

Ruleset identity migration and its approval/verification sequence belong in
`erun-skills/skills/erun-merge-queue-drive/SKILL.md`, not the API engineering guide.
`reconcile-bypass` and `plan-ruleset-bypass` are implemented; live credential and
ruleset changes remain explicit operations work. Gate-run records provide
evidence, not a substitute for GitHub-side enforcement.

### An agent environment cannot provision a platform cloud alias (#1969, design recorded; not yet implemented)

- A fresh agent needs unattended outbound platform auth; device/PKCE login needs
  a human. Proposed: a tenant Zitadel machine user with client_credentials through
  existing issuer-generic OIDC verification, not a new signing trust anchor.
- Provision the identity/role idempotently server-side with the first agent-capable
  environment. Deliver client ID/secret via the existing Kubernetes Secret channel;
  do not require the pod to authenticate interactively to create its own access.
- Use a purpose-built role, not broad TenantUser: review read, nested build report,
  gate-run create/update, and review status update only. Extend scopes only after
  identifying a real additional caller.
- Pod startup/config reconciliation must establish the alias automatically;
  common's login/token resolution must mint refreshable short-lived tokens through
  the new grant. Until both exist this is design, not a fix.
- GitHub queue identity and platform machine identity are two trust domains.
  Name them coherently for attribution but never share the literal secret.
  Release participation and hosted-orchestrator attribution remain separate decisions.

## Cloud-Provider-Alias Storage: A Nil-Cipher Route Must Refuse, Not Vanish (erun#2042 follow-up)

Always register the alias route and return an actionable 501 when its encryption
dependency is absent, like the nil MCP signer. Do not hide optional configuration
failures as 404s. This does not provision `ERUN_SECRETS_KEY`: deployment still
needs a real 32-byte key delivered as a Secret. Keep both configured-success and
unconfigured-refusal tests.

## Validation

- Run `go test ./...` from this module after Go changes.
- The queue gates are opt-in and live in this module's root package. `ERUN_E2E_RELEASE_DATABASE_URL` runs the release queue's SQL contracts (`ClaimNext`/`RecordOutcome`/`ExpireStale`/idempotency) against a migrated PostgreSQL (`release_queue_sql_e2e_test.go`); `ERUN_E2E_REVIEWS_DATABASE_URL` runs the `builds.kind`/version/failure_detail contract and tenant isolation against a migrated PostgreSQL (`internal/repository/reviews_e2e_test.go`); `ERUN_E2E_MERGE_DATABASE_URL` and `ERUN_E2E_RELEASE_DATABASE_URL` each additionally run an HTTP-level end-to-end gate (`merge_queue_e2e_test.go`, `release_queue_e2e_test.go`) — no cluster, no DBOS, since neither queue dispatches a Job anymore. The merge queue's gate drives a real local (`file://`) git remote through the exact fetch/merge/push steps an environment performs: `TestMergeQueueRefusesAMergeReportedAgainstAStaleTarget` proves a merge built against a target tip that is no longer current gets refused even once it is genuinely pushed to the branch (a force-push standing in for a buggy or malicious reporter, since a well-behaved fast-forward-only push cannot manufacture that state), and `TestMergeQueueAcceptsAMergeReportedAfterUnrelatedCommitsLandInBetween` (erun#2250) proves the opposite direction — a review reported after the release flow's own direct `[skip ci]` pushes landed in between is still accepted, because `gatedTargetTip` only has to be an ancestor of the reported commit, not its immediate parent.
- `internal/gitverify` has its own unit tests against real local git repositories (`verifier_test.go`) — no cluster or database, the same style `internal/mergeexec/job_test.go` used before this package replaced it. `TestRemoteVerifierIsAncestor*` cover `IsAncestor` directly: a direct ancestor, an ancestor separated by unrelated commits in between, a commit compared against itself, an unrelated commit that never led to the descendant at all (both directions), and a malformed hash.
- The environment delete state machine has the same shape of opt-in gate: `ERUN_E2E_ENVIRONMENT_DATABASE_URL` runs `ClaimDelete`/`MarkDeleteBlocked`/`Count`/`ListByStatuses`'s SQL contracts against a migrated PostgreSQL (`environment_delete_e2e_test.go`).
- The capability contract has its own opt-in gate: `ERUN_E2E_PERMISSIONS_DATABASE_URL` runs the property the whole contract rests on — for a given role set, every route the capability answer claims is one `Authorize` permits, and every route it omits is one `Authorize` refuses — against a migrated PostgreSQL (`internal/repository/permissions_e2e_test.go`), across pattern rules, exact rules, a deliberately narrow anchored pattern, and a caller with no permissions at all.
- The gate-run failure classifier (see "Gate Runs" above) has its own opt-in HTTP-level gate: `ERUN_E2E_GATE_RUN_DATABASE_URL` runs `gate_run_classifier_e2e_test.go` against a migrated PostgreSQL and a real handler — no dry-run, no mocked classifier. It drives `eruncommon.RunGateRunStart`/`RunGateRunReport` (the exact entry points the CLI and MCP tools call) with each of `GateRunInconclusiveSignatures()`'s known infrastructure signatures and confirms the persisted, read-back status is `INCONCLUSIVE`; drives a genuine, unmatched failure and confirms it stays `FAILED`; confirms `RunGateRunList`/its `status` filter let a caller tell the two apart (a caller filtering on `status=failed` must not see the classified run); and confirms `RunReviewRecordBuild --gate --failed` refuses a known-signature `--failure-detail` outright while still recording a genuine one. This closes the "verified end to end" claim in the "What a hand-run merge-queue script should hand off to erun" bullet above with a permanent, repeatable test — that verification had previously only ever been a manual, uncommitted session.
- The mandatory unit-level refusal tests for `acceptMerged`'s three verification conditions live in `internal/service/reviews_test.go` (`TestAcceptMergedRefusesWhenCommitIsNotOnTheTargetBranch`, `TestAcceptMergedRefusesWhenGatedTipIsNotAnAncestorOfTheReportedCommit`, `TestAcceptMergedRefusesWithNoSuccessfulGateBuildRecorded`), each driven by a fake `MergeVerifier` so the condition under test is isolated from the other two. `TestAcceptMergedSucceedsWhenParentIsTheGatedTip` and `TestAcceptMergedSucceedsWhenUnrelatedCommitsLandedBetweenGatingAndReporting` are the corresponding acceptance cases for condition 2's ancestry check (erun#2250): the ordinary exact-parent-match case, and the case with unrelated commits landing in between.

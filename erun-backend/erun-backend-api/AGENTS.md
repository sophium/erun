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
- **A path parameter that names an externally visible id is validated once, at
  registration, never per handler.** `routes.WithUUIDPathIDs`
  (`internal/routes/path_ids.go`) wraps every route registered through
  `ProtectedRouteRegistrar`, so a segment that cannot be a UUID is answered
  `400 INVALID_PATH_ID` — naming the parameter and the value — before the
  handler runs. Without it a malformed id travelled to the database, which
  rejected it at parse time, and the route layer's generic fallback reported a
  typo as a server fault: it tells the caller the platform is broken when their
  own id is mistyped, and it puts every such typo into the platform's 5xx rate.
  The classification is an **exception list, not an allow-list**, matching the
  "all externally visible IDs are UUIDv7" rule below: every `{...}` segment is
  treated as an id unless it is named in `nonUUIDPathParams` (today only
  `{alias}`, a credential's own chosen name, and `{external_id}`, the IdP's
  subject identifier), so a route added later is covered without its author
  opting in. Only the spelling is judged, not the version or existence — a
  well-formed id that names nothing is still a `404`. The guard sits inside the
  auth wrapper, so an unauthenticated caller gets `401` for every id shape and
  parsing is not observable before authorization. `normalizeNoRows`' own
  `invalid_text_representation` mapping stays as the second line of defence for
  a non-route caller. Rotation: `internal/routes/path_ids_test.go` covers the
  guard, `malformed_path_id_e2e_test.go` the real handler against a real
  migrated PostgreSQL (opt-in, `ERUN_E2E_MERGE_DATABASE_URL`). Registering a
  route directly on the mux instead of through the registrar bypasses it.
- Keep request structs local unless a real shared transport contract owns them.
- Classify every route in `routeroles.Routes` and the operator-surface audit.
  A genuinely internal route needs an explicit reason; missing UI is not itself
  evidence it should be exempt. See integration's route gates.

## Reviews And Builds

- Review `name` is the squash merge message. It is unique within a repository among reviews that can still reach `MERGED` or did reach it; a `CLOSED` review's name is free to reuse.
- **A review records the repository its branches belong to** (`Review.Repository`), canonicalized through `erun-common`'s `RepositoryIdentity` in `PrepareCreate` rather than trusted as sent — an SSH remote and its HTTPS form must answer one identity, or a review created from an SSH checkout would be invisible to the HTTPS remote the merge-queue environment reads. `POST /v1/reviews` refuses (`400 INVALID_REPOSITORY`) a value that names no repository. Absent is allowed and recorded as none: it is what a review of a repository with no nameable remote is, and it is what every review created before the column existed is. `ReviewService.UpdateStatus` sets it from a `MERGED` report's `remoteUrl` when the review records none, which is the only moment the platform holds it.
- **The merge queue is keyed by `(repository, targetBranch)`, never by the target branch alone.** A tenant may serve more than one repository and they share branch names, so every queue query takes a repository filter and an empty one means "every repository's queue", which is what a target branch alone has always meant. A promotion naming none is refused (`409 MERGE_QUEUE_AMBIGUOUS`) when that queue holds more than one repository's reviews (`ReviewService.refuseAmbiguousQueue` over `QueuedRepositories`) rather than promoting a head that names no single repository — the one-repository case, including the single queue unrecorded reviews share, still promotes as it always did.
- **A review that records no repository is unknown, never a repository of its own.** Reviews created before the platform recorded a repository carry none, and counting that absence as a distinct repository made a tenant's own queue read as two and refuse to advance (`QueuedRepositories` now reports named repositories and unrecorded rows separately; only `len(Named) >= 2` refuses). The refusal names the unattributable rows by id (`details.unrecordedRepositoryReviewIds`), because naming a repository does not reach them — they are in none of the named queues — and an operator told only the repositories would have to infer which rows are stuck. `markBuildSucceeded` treats an ambiguity on the promotion it attempts as not-this-build's-failure, like an occupied slot or a gated head: the build is already recorded and returning the refusal fails a report that succeeded.
- **`reviews_status_build_link_check` does not require a build for `MERGED`.** Reconciliation (a branch that landed without the queue) records no `last_merged_build_id` by design, so requiring one made every such report a `23514` and a bare 500 against a real database. `FAILED`/`READY`/`MERGE` still name their build; a queue-driven `MERGED` report's GATE build is required in `ReviewService.acceptMerged`, the only place the build row can be read.
- A `MERGED` report's `remoteUrl` must name the review's own repository (`resolveReportedRepository`); a remote for a different one is refused with `MERGE_NOT_VERIFIED` rather than used to verify a commit that landed somewhere else. `gatedTargetTip` is read from that same repository, so one repository's landing is never the anchor for another's — **and it is read from the identity the report names, never from the review's own `repository` column.** An empty repository filter means "every repository" to `FindLastMergedReview`, and a row created before the platform recorded one carries exactly that, so anchoring on the column resolved whichever repository had merged onto a same-named target branch most recently: it refused a real landing because a stranger's commit is not one of its ancestors, and for a repository whose own history had been rewritten it defeated the force-push check by finding some other repository's commit where that repository's own tip belonged. The identity is threaded into `gatedTargetTip` as a parameter (`verifyRepositoryState`) rather than read off a review, because the two are different questions and passing the wrong one is silent. `acceptMerged` records the adopted identity and logs it, so what the row ends up recording is what verification actually established. Coverage: `cross_repository_verification_e2e_test.go` (two real `file://` remotes sharing one target branch name, `ERUN_E2E_MERGE_DATABASE_URL`), `TestAcceptMergedDoesNotAnchorAnUnrecordedReviewOnAnotherRepositorysMerge`.
- Reviews must have both `targetBranch` and `sourceBranch`.
- Review status values include `OPEN`, `CLOSED`, `FAILED`, `READY`, `MERGE`, and `MERGED`; do not remove existing statuses when adding workflow states.
- Successful builds should move `OPEN` or `FAILED` reviews into their own repository's per-target-branch merge queue as `READY`.
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
- **`TenantUser`/`TenantAdmin`/`TenantAgent` are the narrower predefined roles, built from exact routes, never a hand-authored pattern.** `internal/routeroles` (`Routes`, a `map["METHOD /path"]Roles`) is the single source of truth for which registered route each grants. `Roles` is a **membership set**, one bit per role, not one class per route: `TenantAgent` is a strict subset of TenantUser's reach, which no single-valued constant can express without over-granting the machine role or narrowing TenantUser for every tenant already holding it. Composed values name the sets that matter: `TenantUserClass` (TenantUser and TenantAdmin, not TenantAgent — reading the tenant, driving reviews/comments/builds/the merge queue, operating environments that already exist), `TenantAdminOnly` (TenantAdmin alone — creating/deleting environments, registering contexts, managing users/invites/roles/org-adjacent settings), `TenantAgentClass` (all three — the environment-run gate and self-report flow), and `OperationsOnly` (no narrower role at all — reachable only through `ReadAll`/`WriteAll`, even from inside an `OPERATIONS` tenant, so `TenantAdmin` stays a genuinely lesser position than the platform operator there). `routeroles.TenantUserPermissions()`/`TenantAdminPermissions()`/`TenantAgentPermissions()` derive each role's exact `role_permissions` grants directly from that map. `internal/repository/predefined_roles.go`'s `ensureNarrowerRolesExist` creates and reconciles all three roles for a tenant lazily and idempotently — at every first-user bootstrap and every `RoleRepository.List` read, in both directions — so an already-bootstrapped tenant (or one that predates a later route reclassification) picks up the current grant set the next time anything calls it, with no migration backfill.
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
- **`MERGED` has two verification stories, and which one applies is decided by the review's own status, never by the caller's claim.** A review holding `MERGE` goes through `acceptMerged`; any other review goes through `reconcileMerged` (see "Landed without the queue" below). Both are reached by the same `PATCH /v1/reviews/{review_id}/status`, from any caller, never by a privileged internal path — and neither trusts its caller.
- `acceptMerged`, for a review at `MERGE`, checks all three: `verifyGateBuild` confirms `buildId` names an already-recorded, successful `GATE` build against this exact review; `verifyRepositoryState` fetches the caller-supplied `remoteUrl` (`internal/gitverify.RemoteVerifier`, real `go-git`, no `git` binary needed in this container) to confirm the build's commit is really reachable from the target branch's tip, and that `gatedTargetTip` — the merge commit of whichever review most recently reached `MERGED` on the same target branch *of the repository the report names*, or nothing to compare against for the first merge through the queue on that repository's branch — is really its *ancestor*. Any of the three failing refuses with `*MergeNotVerifiedError` (409, `MERGE_NOT_VERIFIED`) and leaves the review at `MERGE`.
- Verify gatedTargetTip ancestry, not immediate-parent equality: release metadata commits may intervene. Preserve refusal for replaced/unrelated target history and acceptance with intervening commits (`internal/gitverify` tests).
- **The verification fetch is credential-less, so the form of `remoteUrl` is the platform's problem, not the caller's.** `internal/gitverify`'s `fetchableRemoteURL` rewrites an SSH remote — scp-like `git@host:owner/repo.git`, `ssh://host/owner/repo.git`, which is what `git remote get-url origin` returns on an SSH checkout — to the same host's HTTPS before fetching, because the platform only reads and a public repository reads that way with no key. A URL already readable anonymously (https, http, git, file, a local path) is left exactly as given. An SSH remote on a port of its own has no HTTPS equivalent and is refused up front, naming the form the platform needs, rather than being sent to a fetch that fails on `SSH_AUTH_SOCK`. A fetch that still fails says the platform could not read the remote **and that the merge's landing was not judged** — by the time it runs the branch is already pushed, so the message must not read as a verdict that the merge did not happen. `erun-common`'s `RepositoryIdentity` performs the same SSH rewrite for identity — a different question, whose answer must agree with fetchability — coverage: `TestRepositoryIdentityFoldsEverySpellingOfOneRepository`, `TestRepositoryIdentityAgreesAcrossTheFormsAClientMayHold`. Coverage: `TestFetchableRemoteURL` (the rewrite), `TestRemoteVerifierFetchesAnSSHRemoteOverHTTPS` (the fetch-level outcome, over a real local repository), `TestRemoteVerifierNamesTheRequiredFormForAnSSHRemoteItCannotRewrite`, and `TestRemoteVerifierSeparatesAFailedReadFromAFailedMerge`.
- The gate is `erun build`, never `erun release`. The gate publishes nothing — releasing stays exactly where #1030 put it, after the merge lands, triggered off the merge commit the gate produced. `ReviewService` calls `ReleaseTrigger.TriggerRelease` directly on a successful `acceptMerged` — no cycle, because `ReleaseService` does not depend back on `ReviewService` the way the old Job-dispatching `MergeQueueService` had to avoid one.
- A failed `GATE` build does not need this verification: reporting one through the ordinary `POST /builds` route reaches `ReviewService.MarkBuildResult`, which (being kind-agnostic) already moves a `MERGE` review straight to `FAILED` exactly as a failed `RECORDED` build would. Only a *successful* `GATE` build's `MERGED` claim needs the separate, verified `PATCH .../status` call.
- One merge in flight per `(tenant, repository, target_branch)` falls out of the existing invariant `AdvanceMergeQueue` already enforced: it refuses to promote while a `MERGE` review is active on that queue. Because the head is always gated against the *current* target, re-testing after each landed merge is automatic rather than a separate invalidation pass — and it is what makes reading `gatedTargetTip` at verification time equivalent to reading it at promotion time: nothing else can move a target branch's recorded tip while this review is the one holding `MERGE` on it.
- A build reported for one review can promote a *different* review to `MERGE`. Every call site that can produce a promotion has to observe the promoted review, not assume it is the one it was already looking at.
- Unattended agent platform credentials remain blocked by the design below; do not imply a fresh pod can self-authenticate.

### Landed without the queue (erun#2575)

- **A review whose work landed by GitHub squash merge could never reach `MERGED`.** The branch's own commits are deliberately not ancestors of the target, and no `GATE` build was ever recorded for it, so neither of `acceptMerged`'s conditions can ever hold however long it sits there. `review close` renders landed work as `CLOSED` — abandoned — which is worse than leaving it `OPEN`, so such reviews sat `OPEN` forever and the operator-facing open count grew by one for every change that landed this way (measured: 63 OPEN, 44 of them already on `main`).
- `reconcileMerged` is the second verification story, and it verifies rather than believes: `gitverify.ContainsChanges` fetches both branches from `remoteURL` and confirms that everything the source branch adds, relative to where it diverged from the target, is present in the target's history. Ancestry is asked first and answers every ordinary landing on its own — a merge commit, a fast-forward, and a fast-forward of the target *onto* the branch tip, where both refs name the same commit and a commit is its own ancestor. That last shape is the reason the equal-tip case is **contained, not refused**: it is that landing, it is the shape the sanctioned no-queue path leaves behind, and the refs cannot tell it apart from a branch that never committed anything — which the change-set comparison cannot either, and which is accepted anyway the moment it trails the target instead of sitting exactly on it. So a refusal there bought no protection against a landing that did not happen while refusing the landing that did. Only then is the change set compared, as a **fingerprint** (every added, removed and modified path with the blob it moved to or from, `changeFingerprint`, order-independent), not as a commit graph — so it survives the target advancing under the squash, which is the ordinary case. Only a branch that shares no single merge base, or whose change set is carried by no commit on the target, names no landing and is refused.
- `reconcileMerged` refuses `CLOSED` (terminal) and `MERGE` (the queue's, which must go through `acceptMerged`). It records **no `lastMergedBuildId`** and triggers **no release**: there was no build, and the landing it reports already happened elsewhere and published whatever it published.
- `FindLastMergedReview` therefore filters on `last_merged_build_id IS NOT NULL`. It is what `gatedTargetTip` anchors the next queue-driven merge on; returning a build-less merge would resolve an empty build id and fail every subsequent `report-merged` on that branch.
- The client surface is `report-merged` with `--build-id` omitted — a review at `MERGE` still requires it, any other review does not. Both refusals stay `409 MERGE_NOT_VERIFIED`, so a caller still cannot tell a lie about the repository, only state which review it is talking about.
- `reconcileMerged`'s tests live in `internal/service/reviews_test.go` (`TestReconcileMergedAcceptsALandedBranchFromAnyOpenStatus`, `TestReconcileMergedRefusesWhenTheBranchIsNotInTheTarget`, `TestReconcileMergedRefusesWithNoVerifier`, `TestReconcileMergedRefusesAClosedReview`), and the git check's own against real local repositories in `internal/gitverify/verifier_test.go` (`TestRemoteVerifierContainsChangesFindsASquashLandedBranch`, which also asserts the branch tip is *not* an ancestor of the squash commit — the half of the reproduction that shows the old check could never hold — plus the unrelated-commits-in-between, ordinary-landing, and refusal cases).

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
`reconcile-bypass` and `plan-ruleset-bypass` are implemented, and so is the
push-side half: a push GitHub admits by bypassing a ruleset reports that in
erun's own output, naming the rules stepped over, because the successful-push
path used to discard the remote line carrying it. Live credential and ruleset
changes remain explicit operations work. Gate-run records provide evidence, not
a substitute for GitHub-side enforcement.

### An agent environment cannot sign itself in to a platform alias (#1969)

- **Delegated provisioning is implemented.** A fresh agent needs unattended
  outbound platform auth, and device/PKCE login needs a human, so the session
  arrives the way the registry credential does: `erun init` runs on a host that
  *is* signed in, resolves that host's own erun alias, and mints
  `<tenant>-devops-platform-alias` for the environment it creates
  (`platform_alias_secret.go`, `provisionPlatformAliasSecret`). The runtime chart
  mounts it read-only and the entrypoint's `sync_platform_alias` seeds the pod's
  cloud config from it at boot, so it survives pod recreation. Provisioning is a
  no-op — never an error — when the host has no erun alias, several ambiguous
  ones, or no stored session, since most installs never attach the hosted platform.
- **The alias is the operator's own identity, not a machine identity.** That is a
  deliberate limitation, not an oversight: it means an environment's platform calls
  are attributed to whoever ran `init`, and two environments provisioned from one
  host are indistinguishable in the audit trail. Say so rather than letting it read
  as a per-environment credential.
- **Implemented: the machine role's authorization model.** `routeroles.Routes`
  classifies a route by a *membership set* (`Roles`, one bit per narrower role),
  not one class per route, because the machine role is a strict subset of
  `TenantUserClass` and no single-valued class can express that — a fourth
  constant could only have made it a peer, or narrowed TenantUser for every
  tenant already holding it. `TenantAgentPermissions()` derives the subset:
  `GET /v1/whoami` and the review reads (`erun review show`'s review, comments
  and builds fetches), the review-nested `GATE` build report, gate-run
  create/update, the `MERGED` status report, and `POST /v1/builds` — the
  unattached self-report `erun build` actually makes, which the original
  subset missed and which would otherwise 403 the environment's own call. It
  deliberately excludes `GET /v1/builds` (the tenant-wide history has no
  environment caller), opening or promoting reviews, comment/reviewer writes,
  and every release route. `ensureNarrowerRolesExist` seeds `TenantAgent` for
  every tenant alongside TenantUser/TenantAdmin, lazily and reconciled in both
  directions, so an operator can grant it today. **Reconciliation replaces a
  derived role's grants outright, in both permission forms** — a route
  reclassified *out* of one of these roles has to actually stop being granted
  to a tenant seeded before the reclassification, which inserts alone
  (`ON CONFLICT DO NOTHING`) never did. That is why the removal predicate
  cannot be a bare `(api_method, api_path) NOT IN (...)`: a pattern row stores
  both as NULL, so that comparison is NULL rather than TRUE and the row
  survives, and the model has no pattern members to have named it. Coverage:
  `internal/routeroles/route_roles_test.go` (the subset invariant and the
  closed route list), `internal/repository/roles_e2e_test.go` (the seeded
  grants, and both reconciliation removals — an exact pair, and the pattern
  row), and this module root's `role_policy_e2e_test.go`
  (`ERUN_E2E_ROLES_DATABASE_URL`, `ERUN_E2E_PERMISSIONS_DATABASE_URL`: a
  TenantAgent holder permitted exactly its set against every real registered
  route, and refused the rest).
- **Implemented: the machine identity, and the privileged path that mints it.**
  `MachineIdentityService` (`internal/service/machine_identity.go`) provisions
  or returns one environment's own identity: it asks the tenant's own IdP for
  an application under a login name *derived from the environment's id*
  (`MachineIdentityLoginName`), enrols the `(issuer, subject)` pair as an erun
  user, and converges the `TenantAgent` grant. Every step is idempotent, and
  derivation rather than storage is the whole mechanism — nothing links an
  environment to its identity but that arithmetic, so a second call can only
  find what the first created. The route is
  `POST /v1/environments/{environment_id}/machine-identity`, classified
  `TenantUserClass`: the credential is strictly weaker than the mcp-token that
  class already mints, and the caller is provisioning an environment that
  already exists in their own tenant.
  - **Who may grant it: a new privileged internal path, and never an operations
    caller.** The enrolment and the role grant the identity needs are both
    `TenantAdminOnly` as routes, while the session provisioning runs under is an
    ordinary operator's delegated alias — so on any tenant whose operator is not
    its genesis user (the common case) those routes could never be called. The
    service therefore performs both itself, with the API's own authority, behind
    a route whose own classification *is* the authorization. An operations
    caller was the alternative and is rejected: it would put platform staff in
    the middle of every tenant's own provisioning, and `OperationsOnly` names a
    platform-operator position rather than a tenant's act.
  - **It refuses rather than guesses where it cannot mint.** A tenant resolving
    by no org-scoped issuer, or by several, is refused (`409
    MACHINE_IDENTITY_UNAVAILABLE`, naming which); an unconfigured control plane
    answers `501 MACHINE_IDENTITY_UNCONFIGURED` at the route, never a 404. The
    identity is created in the tenant's *own* organization — minting it in
    anyone else's is the attribution collapse this feature exists to remove.
  - **Delivery reuses the existing channel**: `erun-common`'s
    `provisionMachineIdentitySecret` writes the same
    `<tenant>-devops-platform-alias` Secret the runtime chart already mounts,
    carrying a `clientsecretref` in place of a `refreshtokenref`, and
    `resolveERunAccessToken` picks the grant that reference implies. It
    exchanges the credential for a token *before* writing anything, and falls
    back to the delegating alias — traced, never silent — when any step cannot
    be completed, so an environment never trades a working identity for a
    broken one. Coverage: `internal/service/machine_identity_test.go`,
    `internal/zitadel/machine_test.go` (the Management API binding, against a
    fake), `internal/routes/machine_identity_test.go`, and
    `machine_identity_e2e_test.go` (`ERUN_E2E_MACHINE_IDENTITY_DATABASE_URL`:
    two provisioning calls leave one user row, one mapping and one grant).
  - **The issuer-generic token half is the verified go/no-go, and its artifact
    is `machine_identity_token_e2e_test.go`** (same
    `ERUN_E2E_MACHINE_IDENTITY_DATABASE_URL`, against a real migrated
    PostgreSQL). It drives a token minted by the real `client_credentials`
    grant (`CloudProviderBearerToken` asks for
    `urn:zitadel:iam:user:resourceowner`, the claim the shipped Zitadel
    org-scoped mapping names) through the real bearer verifier and the real
    `IdentityRepository.ResolveTenantByIssuer`, and pins both directions of the
    one question source cannot settle: honoured, the token resolves its tenant;
    refused by the issuer (the BYO case, and what a Zitadel that stopped
    honouring the scope for machine users would produce), the scope is dropped
    on one retry and the token is refused with the named
    `security.ErrTenantUnresolved` rather than resolving to a wrong tenant. The
    same file drives the environment's own routine `POST /v1/builds`
    self-report through the real handler as the identity an environment now
    holds, with its `GET /v1/builds` refusal as the negative control; the
    integration suite's `TestMachineIdentityBuildSelfReport`
    (`erun-integration/machine_identity_test.go`) covers the client half with a
    real `erun build` and a stub platform that refuses that route to any bearer
    but the machine token. What a stub cannot establish is whether Zitadel's own
    token endpoint honours that scope on a `client_credentials` grant — that
    remains a live-instance question.
  - **Two things remain open, both tracked in #2684.** Revocation and the
    migration of an environment already carrying the operator's credential are
    separate work — `EnvConfig.PlatformAliasSecretName` is a single scalar, so
    an environment is on one identity or the other and never both, and
    re-running `erun init` is the switch. And the Management API binding
    (`internal/zitadel/machine.go`) is exercised only against a fake: the
    issuer-generic *token* half is the verified go/no-go, the provisioning
    calls are not verified against a live instance.
- GitHub queue identity and platform machine identity are two trust domains.
  Name them coherently for attribution but never share the literal secret.
  Release participation and hosted-orchestrator attribution remain separate decisions.
- **An environment with no provisioned alias keeps the split: it builds, a
  credentialed host records.** `erun build` needs no platform alias (with none
  configured it skips reporting its outcome); every `erun review` call aborts
  before any network call and exits `127`. The merge skills check for a usable
  alias and stop there rather than walking into a call that cannot succeed — see
  `erun-docs/docs/collaboration/merge-queue.md` § "What runs where: the
  build/platform split". An environment `erun init` provisioned from a signed-in
  host clears that check and drives the queue itself.

## Cloud-Provider-Alias Storage: A Nil-Cipher Route Must Refuse, Not Vanish (erun#2042 follow-up)

Always register the alias route and return an actionable 501 when its encryption
dependency is absent, like the nil MCP signer. Do not hide optional configuration
failures as 404s. This does not provision `ERUN_SECRETS_KEY`: deployment still
needs a real 32-byte key delivered as a Secret. Keep both configured-success and
unconfigured-refusal tests.

## Validation

- Run `go test ./...` from this module after Go changes.
- The queue gates are opt-in and live in this module's root package. `ERUN_E2E_RELEASE_DATABASE_URL` runs the release queue's SQL contracts (`ClaimNext`/`RecordOutcome`/`ExpireStale`/idempotency) against a migrated PostgreSQL (`release_queue_sql_e2e_test.go`); `ERUN_E2E_REVIEWS_DATABASE_URL` runs the `builds.kind`/version/failure_detail contract and tenant isolation against a migrated PostgreSQL (`internal/repository/reviews_e2e_test.go`); `ERUN_E2E_MERGE_DATABASE_URL` and `ERUN_E2E_RELEASE_DATABASE_URL` each additionally run an HTTP-level end-to-end gate (`merge_queue_e2e_test.go`, `release_queue_e2e_test.go`) — no cluster, no DBOS, since neither queue dispatches a Job anymore. The merge queue's gate drives a real local (`file://`) git remote through the exact fetch/merge/push steps an environment performs: `TestMergeQueueRefusesAMergeReportedAgainstAStaleTarget` proves a merge built against a target tip that is no longer current gets refused even once it is genuinely pushed to the branch (a force-push standing in for a buggy or malicious reporter, since a well-behaved fast-forward-only push cannot manufacture that state), and `TestMergeQueueAcceptsAMergeReportedAfterUnrelatedCommitsLandInBetween` (erun#2250) proves the opposite direction — a review reported after the release flow's own direct `[skip ci]` pushes landed in between is still accepted, because `gatedTargetTip` only has to be an ancestor of the reported commit, not its immediate parent. `cross_repository_verification_e2e_test.go` (`ERUN_E2E_MERGE_DATABASE_URL`) covers which repository that anchor is drawn from: two remotes sharing one target branch name, with `TestReportMergedDoesNotAnchorALegacyReviewOnAnotherRepositorysMergeCommit` proving a row recording none is anchored on the repository its report names and not on a stranger's merge commit, and `TestReportMergedVerifiesARecordedReviewAgainstItsOwnRepositoryOnly` proving a row that records one is still refused for a different one.
- `internal/gitverify` has its own unit tests against real local git repositories (`verifier_test.go`) — no cluster or database, the same style `internal/mergeexec/job_test.go` used before this package replaced it. `TestRemoteVerifierIsAncestor*` cover `IsAncestor` directly: a direct ancestor, an ancestor separated by unrelated commits in between, a commit compared against itself, an unrelated commit that never led to the descendant at all (both directions), and a malformed hash.
- The environment delete state machine has the same shape of opt-in gate: `ERUN_E2E_ENVIRONMENT_DATABASE_URL` runs `ClaimDelete`/`MarkDeleteBlocked`/`Count`/`ListByStatuses`'s SQL contracts against a migrated PostgreSQL (`environment_delete_e2e_test.go`).
- The capability contract has its own opt-in gate: `ERUN_E2E_PERMISSIONS_DATABASE_URL` runs the property the whole contract rests on — for a given role set, every route the capability answer claims is one `Authorize` permits, and every route it omits is one `Authorize` refuses — against a migrated PostgreSQL (`internal/repository/permissions_e2e_test.go`), across pattern rules, exact rules, a deliberately narrow anchored pattern, and a caller with no permissions at all.
- **The derived-role proofs are opt-in on `ERUN_E2E_ROLES_DATABASE_URL`/`ERUN_E2E_PERMISSIONS_DATABASE_URL`, and no gate target sets either** — so `go test ./...` reports a clean `ok` for a package whose role-contract assertions never executed, the `NOT RUN:` caveat root AGENTS.md describes. They need a real migrated PostgreSQL, and the test-stage image `make check-gate` runs in carries neither a docker daemon nor the atlas CLI, so like `test-retention`/`test-schema-drift`/`test-postgres-restart` they are run by hand or via `erun exec job` in an agent env rather than by a gate target. Run them before merging a change to `internal/routeroles`, the derived-role reconciliation in `internal/repository/predefined_roles.go`, or any route's classification. The names that matter: `TestRoleReconciliationRemovesAGrantNoLongerInTheDerivedSet` and `TestRoleReconciliationRemovesAPatternGrantTheDerivedSetCannotName` (both removals a seeded role must actually take), `TestSeededTenantAgentGrantsAreTheDerivedSubsetOfTenantUser` (the strict containment), and this module root's `TestTenantAgentDrivesTheGateAndNothingElse` and `TestWriteAllHolderRetainsAccessAfterRolloutOfNarrowerRoles` (the machine role permitted exactly its set and refused the rest; an existing WriteAll holder unaffected).
- The gate-run failure classifier (see "Gate Runs" above) has its own opt-in HTTP-level gate: `ERUN_E2E_GATE_RUN_DATABASE_URL` runs `gate_run_classifier_e2e_test.go` against a migrated PostgreSQL and a real handler — no dry-run, no mocked classifier. It drives `eruncommon.RunGateRunStart`/`RunGateRunReport` (the exact entry points the CLI and MCP tools call) with each of `GateRunInconclusiveSignatures()`'s known infrastructure signatures and confirms the persisted, read-back status is `INCONCLUSIVE`; drives a genuine, unmatched failure and confirms it stays `FAILED`; confirms `RunGateRunList`/its `status` filter let a caller tell the two apart (a caller filtering on `status=failed` must not see the classified run); and confirms `RunReviewRecordBuild --gate --failed` refuses a known-signature `--failure-detail` outright while still recording a genuine one. This closes the "verified end to end" claim in the "What a hand-run merge-queue script should hand off to erun" bullet above with a permanent, repeatable test — that verification had previously only ever been a manual, uncommitted session.
- The mandatory unit-level refusal tests for `acceptMerged`'s three verification conditions live in `internal/service/reviews_test.go` (`TestAcceptMergedRefusesWhenCommitIsNotOnTheTargetBranch`, `TestAcceptMergedRefusesWhenGatedTipIsNotAnAncestorOfTheReportedCommit`, `TestAcceptMergedRefusesWithNoSuccessfulGateBuildRecorded`), each driven by a fake `MergeVerifier` so the condition under test is isolated from the other two. `TestAcceptMergedSucceedsWhenParentIsTheGatedTip` and `TestAcceptMergedSucceedsWhenUnrelatedCommitsLandedBetweenGatingAndReporting` are the corresponding acceptance cases for condition 2's ancestry check (erun#2250): the ordinary exact-parent-match case, and the case with unrelated commits landing in between.

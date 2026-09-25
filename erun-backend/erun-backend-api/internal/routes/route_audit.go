package routes

// InternalAPIRoutes declares API routes that need no operator entry point in
// erun-ui/frontend or erun-console, using the same explicit-marker
// discipline eruncommon.MCPToolDescriptor's AgentFacing field and
// erun-cli/cmd/command_tree.go's cliOnlyAgentFacingCommands already use:
// silence must never be how a route opts out of the desktop-surface gate
// (erun-integration/AGENTS.md § "Desktop-surface gate"). Keyed by
// "<METHOD> <canonical path template>", exactly as passed to
// ProtectedRouteRegistrar or registered directly on the mux.
var InternalAPIRoutes = map[string]bool{
	// Unauthenticated platform-discovery bootstrap: a client fetches this
	// before it holds a token, to learn the issuer and console/CLI client
	// ids to authenticate with. Infrastructure a client needs in order to
	// function at all, not an operator-invoked action -- the same class
	// erun-backend-api/AGENTS.md's Authentication section already carves out
	// for `/healthz`.
	"GET /v1/platform": true,
	// Reports a build result (RECORDED, an environment's own `erun build`, or
	// GATE, merge-queue verification) that the environment running the build
	// already ran on its own -- never something an operator clicks to
	// trigger. The environment promoted to MERGE reports its own gate build
	// through this same route as part of the merge-queue mechanics (see
	// erun-backend-api/AGENTS.md's "Merge Queue"); an operator's part of that
	// flow (advancing the queue, closing a review) already has a real
	// desktop surface, which is what actually needs one.
	"POST /v1/reviews/{review_id}/builds": true,
	// Same self-report shape as the route above, for a build with no review
	// attached -- an ordinary `erun build` run in an environment (erun#1954).
	// GET /v1/builds is not listed here: it has a real operator surface (the
	// desktop tenant dashboard's Builds tab, extended to include unattached
	// builds alongside review builds -- see erun-ui/tenant_dashboard.go).
	"POST /v1/builds": true,
	// Starts (POST) or reports the outcome of (PATCH) a gate run -- the
	// environment driving the gate reports its own attempt, never something
	// an operator clicks, the same self-report shape as the build-result
	// route above.
	"POST /v1/gate-runs":                true,
	"PATCH /v1/gate-runs/{gate_run_id}": true,
	// GET /v1/gate-runs and GET /v1/gate-runs/{gate_run_id} used to be listed
	// here too, as a deliberate, temporary scope decision for the CLI-first
	// delivery of gate-run visibility. They are classified normally now that
	// a real operator surface exists in both erun-console (src/gateRuns/)
	// and erun-ui/frontend (the tenant dashboard's Gates tab).
	// The environment's own AI-tool hooks report their turn-boundary status
	// (busy/idle/awaiting-input) here -- never something an operator clicks,
	// the same self-report shape as the build-result route above. The
	// matching GET has a real operator surface (erun-console's environments
	// panel), so it is not listed here.
	"POST /v1/environments/{environment_id}/ai-sessions": true,
	// The environment's own reporter appends to the sequenced event log --
	// never something an operator clicks, the same self-report shape as the
	// ai-session route above.
	"POST /v1/environments/{environment_id}/events": true,
	// Provisions an environment's own platform identity. Driven by the same
	// client-side moment the platform credential itself is delivered in: the
	// provisioning client (`erun init`, and the deploy retrofit) calls this
	// with the operator's own session, then writes what it returns into the
	// environment's Kubernetes Secret -- the channel the runtime chart already
	// mounts. It is not an operator-clicked action, and there is nothing for a
	// dialog to decide: the identity is a function of the environment, and
	// calling it twice returns the same one.
	"POST /v1/environments/{environment_id}/machine-identity": true,
	// The read half of that log is a resume position, not a report: a client
	// reconnecting to a stream hands back the cursor it last reached and gets
	// the backlog it missed. Its operator-facing form is a live-updating view
	// this change does not build (that is client work, and the console has no
	// live-update consumer of any kind yet), so it is recorded here as a
	// transport endpoint driven by a client's own reconnect loop rather than
	// left unclassified -- but it is not exempted as "nobody has needed a
	// surface": a console live view is the reader it is ultimately for.
	"GET /v1/events": true,
	// The environment-scoped jobs read is what a caller that knows only its
	// own environment id asks -- an in-pod agent or orchestrator checking
	// what else is running alongside it before claiming more work. It is not
	// a second operator view of the queue: the tenant-wide GET /v1/jobs
	// returns exactly these rows and narrows to one environment with its own
	// environmentId filter, which is what the console's Jobs section renders.
	// The nested route earns its place by being reachable from the side that
	// has no tenant-scoped job ids to filter by, not by giving an operator a
	// capability the flat route does not already have.
	"GET /v1/environments/{environment_id}/jobs": true,
	// The definition upload is performed by the machine that authored the
	// settings, through `erun platform env push` (and `erun platform env
	// register --definition`, which writes revision 1 as part of adopting the
	// row). Nothing an operator clicks triggers it: the desktop's hosted-marker
	// panel is deliberately read-only, because driving an upload from the
	// config watcher is the write->event->write loop this repository has
	// already shipped once (erun-ui/AGENTS.md). The matching GET is not listed
	// here -- it has a real desktop surface, bound through
	// apiRouteWailsBindings below.
	"PUT /v1/environments/{environment_id}/definition": true,
	// A one-time, operations-only repair action for a platform whose own
	// OPERATIONS tenant bootstrapped under the legacy "operations" name before
	// its ERUN_TENANT was read at bootstrap (see erun-backend-api/AGENTS.md's
	// bootstrap-name comment). Every platform bootstraps exactly once, so this
	// is invoked at most once per already-affected platform ever, by whoever
	// operates it directly against the API -- not a recurring operator
	// workflow like tenant registration or issuer administration, which is
	// what earns those a console surface. There is no ongoing UI affordance to
	// design here, only a break-glass call.
	"PATCH /v1/tenants/reconcile-bootstrap-name": true,
	// erun platform provision's own preview: it renders a plan for a
	// standalone new-cluster bootstrap (Context block) that the desktop's
	// environment form has never exposed a control for. The desktop's own
	// register-preview action used to call this route too (with only the
	// name/type/kubernetesContext fields, never the Context block), but that
	// meant the plan it rendered couldn't express a contextId or
	// runtimeVersion the way a real register call could -- a preview that
	// cannot model what submit does. It now previews through
	// POST /v1/environments with preview:true instead (the exact route and
	// body Register submits), so this route stays a real, exercised CLI-only
	// preview with no separate desktop surface to keep in sync.
	"POST /v1/provision": true,
}

// KnownUnsurfacedRoutes is a record of known gaps, not a design decision: it
// started as the 33 routes that had no operator entry point in either
// erun-ui/frontend/src or erun-console/src the day the desktop-surface gate
// was taught to see API-only capabilities, kept here so that day's already-red
// gate could still be adopted without either declaring most of them
// permanently internal (most need a real surface, not an exemption) or
// weakening the gate itself. It is the opposite claim from InternalAPIRoutes
// above: an entry here asserts nothing about whether the route deserves a
// surface, only that it does not have one yet.
//
// This map is the tracker of record for that remaining work, and its entries
// are the whole answer to "is this still open?": every one of them is work
// nobody has done yet -- not a settled decision, and not deferred to a
// document or an issue that can move on without it. Nothing here cites a
// tracking issue, deliberately: root AGENTS.md § "Code Comments" keeps
// tracker references out of source (enforced by
// erun-integration/issue_reference_test.go), and the one indirect pointer
// this map used to carry -- a document naming the issue that tracked it --
// was followed to a closed issue while entries remained here, which taught a
// reader nothing about whether the rest was abandoned or forgotten. What
// keeps the list honest is the shrink-only enforcement
// (desktopsurface.FindStaleBaselineEntries, erun-integration/AGENTS.md
// § "Desktop-surface gate" § "Baseline for pre-existing gaps"): a route that
// gains a real reference in either tree must be removed from this map in the
// same change, or the gate fails. That check reads the routes the API
// registers, so it catches an entry whose gap has since been closed, but not
// one whose route no longer exists at all: remove an entry here by hand when
// the route itself goes. Closing one out means giving it that surface, or
// moving it to InternalAPIRoutes above with the reason it needs none.
//
// What remains needs a surface too large for a single change: erun's own
// hosted release view (the "GET /v1/releases" family entries, plus the
// single build's detail route that reads the same way -- no console/desktop
// UI exists anywhere for the release system itself, unlike
// reviews/builds/merge-queue which already have one), tenant-issuer
// administration and the usage-event log (both need a new admin surface
// designed, not just a fetch wired up), and the DNS-01 token mint (needs
// `erun expose` itself redesigned to call it, not a bare button).
var KnownUnsurfacedRoutes = map[string]bool{
	// Creating an org on the platform's own IdP is what makes a second tenant
	// possible: an org-scoped issuer resolves tenants by the org claim, so a new
	// tenant needs an org for its mapping to point at. That makes it plainly
	// operator-facing, not internal — it belongs beside the console's tenant
	// registration, whose dialog already takes the org-scoped issuer fields. It
	// is recorded here rather than exempted because it needs a real surface
	// designed with the rest of identity administration, not a bare button.
	"POST /v1/identity/orgs":                             true,
	"GET /v1/releases":                                   true,
	"GET /v1/releases/{release_id}":                      true,
	"GET /v1/reviews/{review_id}/builds/{build_id}":      true,
	"GET /v1/reviews/{review_id}/releases":               true,
	"GET /v1/usage-events":                               true,
	"POST /v1/environments/{environment_id}/dns01-token": true,
	"POST /v1/releases":                                  true,
}

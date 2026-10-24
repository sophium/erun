package routes

// InternalAPIRoutes declares API routes that need no operator entry point in
// erun-ui/frontend or erun-console, using the same explicit-marker
// discipline mcptools.MCPToolDescriptor's AgentFacing field and
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
	// Reports a build result that the environment running the build already
	// ran on its own -- never something an operator clicks to trigger. The
	// environment promoted to MERGE reports its build of the prepared merge
	// commit through this same route as part of the merge-queue mechanics (see
	// erun-backend-api/AGENTS.md's "Merge Queue"); an operator's part of that
	// flow (advancing the queue, closing a review) already has a real
	// desktop surface, which is what actually needs one.
	"POST /v1/reviews/{review_id}/builds": true,
	// Same self-report shape as the route above, for a build with no review
	// attached -- an ordinary `erun build` run in an environment.
	// GET /v1/builds is not listed here: it has a real operator surface (the
	// desktop tenant dashboard's Builds tab, extended to include unattached
	// builds alongside review builds -- see erun-ui/tenant_dashboard.go).
	"POST /v1/builds": true,
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
	// the console does not have yet, so it is recorded here as a
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
	// The definition upload is not listed: the desktop owns it (config
	// watcher plus the hosted-marker panel's recovery control), bound through
	// apiRouteWailsBindings below. Its write->event->write loop is closed by
	// an origin filter and a definition digest (erun-ui/AGENTS.md
	// § "Hosted definition transfer").
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
	// environment form never exposes. The desktop previews a register through
	// POST /v1/environments with preview:true (the exact route and body
	// Register submits), so this stays a CLI-only preview.
	"POST /v1/provision": true,
}

// KnownUnsurfacedRoutes is a record of known gaps, not a design decision:
// routes with no operator entry point in either erun-ui/frontend/src or
// erun-console/src that need a real surface rather than an exemption. It is
// the opposite claim from InternalAPIRoutes
// above: an entry here asserts nothing about whether the route deserves a
// surface, only that it does not have one yet.
//
// This map is the tracker of record for that remaining work: every entry is
// work nobody has done yet, not a settled decision. What keeps the list
// honest is the shrink-only enforcement
// (desktopsurface.FindStaleBaselineEntries, erun-integration/AGENTS.md
// § "Desktop-surface gate" § "Baseline for pre-existing gaps"): a route that
// gains a real reference in either tree must be removed from this map in the
// same change, or the gate fails. That check reads the routes the API
// registers, so it catches an entry whose gap has since been closed, but not
// one whose route was deleted: remove an entry here by hand when
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

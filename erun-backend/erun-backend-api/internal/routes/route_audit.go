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
}

// KnownUnsurfacedRoutes is a record of known gaps, not a design decision: it
// started as the 33 routes that had no operator entry point in either
// erun-ui/frontend/src or erun-console/src the day the desktop-surface gate
// was taught to see API-only capabilities, kept here so that day's already-red
// gate could still be adopted without either declaring most of them
// permanently internal (most need a real surface, not an exemption) or
// weakening the gate itself. It is the opposite claim from InternalAPIRoutes
// above: an entry here asserts nothing about whether the route deserves a
// surface, only that it does not have one yet. See erun-integration/AGENTS.md
// § "Desktop-surface gate" § "Baseline for pre-existing gaps" for the
// tracking issue and the shrink-only enforcement
// (desktopsurface.FindStaleBaselineEntries): a route that gains a real
// reference in either tree must be removed from this map in the same change,
// or the gate fails. What remains today needs a surface too large for a
// single change: erun's own hosted release view (the four "GET /v1/releases"
// family entries -- no console/desktop UI exists anywhere for the release
// system itself, unlike reviews/builds/merge-queue which already have one),
// tenant-issuer administration and the usage-event log (both need a new
// admin surface designed, not just a fetch wired up), and the DNS-01 token
// mint (needs `erun expose` itself redesigned to call it, not a bare button).
var KnownUnsurfacedRoutes = map[string]bool{
	// The onboarding request/approve queue's backend half. The desktop
	// (sidebar status icon, request dialog) and console (operator queue)
	// surfaces for these six routes are a separate, later change — landing
	// them together with a real operator entry point is what removes this
	// entry, not a design decision that they need none.
	"POST /v1/invite-requests":                             true,
	"GET /v1/invite-requests/mine":                         true,
	"GET /v1/invite-requests":                              true,
	"POST /v1/invite-requests/{invite_request_id}/approve": true,
	"POST /v1/invite-requests/{invite_request_id}/decline": true,
	"PATCH /v1/config/invite-request-rate-limit":           true,
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
	"GET /v1/tenant-issuers":                             true,
	"GET /v1/usage-events":                               true,
	"PATCH /v1/tenant-issuers":                           true,
	"POST /v1/environments/{environment_id}/dns01-token": true,
	"POST /v1/releases":                                  true,
}

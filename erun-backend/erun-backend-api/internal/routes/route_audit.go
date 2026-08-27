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
}

// KnownUnsurfacedRoutes is a record of known gaps, not a design decision: it
// holds exactly the routes that had no operator entry point in either
// erun-ui/frontend/src or erun-console/src the day the desktop-surface gate
// was taught to see API-only capabilities, kept here so that day's already-red
// gate could still be adopted without either declaring 33 routes permanently
// internal (most need a real surface, not an exemption) or weakening the gate
// itself. It is the opposite claim from InternalAPIRoutes above: an entry here
// asserts nothing about whether the route deserves a surface, only that it
// does not have one yet. See erun-integration/AGENTS.md § "Desktop-surface
// gate" § "Baseline for pre-existing gaps" for the tracking issue and the
// shrink-only enforcement (desktopsurface.FindStaleBaselineEntries): a route
// that gains a real reference in either tree must be removed from this map in
// the same change, or the gate fails.
var KnownUnsurfacedRoutes = map[string]bool{
	"DELETE /v1/reviews/{review_id}/reviewers/{user_id}": true,
	"DELETE /v1/users/{user_id}/roles/{role_id}":         true,
	"GET /v1/quota":                                              true,
	"GET /v1/releases":                                           true,
	"GET /v1/releases/{release_id}":                              true,
	"GET /v1/reviews/{review_id}/builds":                         true,
	"GET /v1/reviews/{review_id}/builds/{build_id}":              true,
	"GET /v1/reviews/{review_id}/comments":                       true,
	"GET /v1/reviews/{review_id}/releases":                       true,
	"GET /v1/reviews/{review_id}/reviewers":                      true,
	"GET /v1/roles":                                              true,
	"GET /v1/tenant-issuers":                                     true,
	"GET /v1/usage-events":                                       true,
	"GET /v1/users":                                              true,
	"GET /v1/users/{user_id}/roles":                              true,
	"PATCH /v1/reviews/{review_id}/comments/{comment_id}/status": true,
	"PATCH /v1/reviews/{review_id}/status":                       true,
	"PATCH /v1/tenant-issuers":                                   true,
	"POST /v1/environments/{environment_id}/dns01-token":         true,
	"POST /v1/environments/{environment_id}/stop":                true,
	"POST /v1/provision":                                         true,
	"POST /v1/releases":                                          true,
	"POST /v1/reviews/merge-queue/advance":                       true,
	"POST /v1/reviews/merge-queue/override-advance":              true,
	"POST /v1/reviews/{review_id}/builds":                        true,
	"POST /v1/reviews/{review_id}/comments":                      true,
	"POST /v1/reviews/{review_id}/reviewers":                     true,
	"POST /v1/roles":                                             true,
	"POST /v1/users":                                             true,
	"POST /v1/users/{user_id}/roles":                             true,
	"PUT /v1/tenants/{tenant_id}/quota":                          true,
}

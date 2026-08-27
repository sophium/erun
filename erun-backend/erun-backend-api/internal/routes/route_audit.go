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

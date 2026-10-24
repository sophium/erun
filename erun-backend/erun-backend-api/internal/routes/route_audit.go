package routes

// InternalAPIRoutes declares API routes that need no operator surface, keyed by "<METHOD> <path template>".
// Silence must never be how a route opts out of the desktop-surface gate.
var InternalAPIRoutes = map[string]bool{
	// Unauthenticated discovery a client needs before it holds a token.
	"GET /v1/platform": true,
	// Self-reported by the environment that ran the build.
	"POST /v1/reviews/{review_id}/builds": true,
	// Self-reported build with no review attached.
	"POST /v1/builds": true,
	// Self-reported by the environment's AI-tool hooks.
	"POST /v1/environments/{environment_id}/ai-sessions": true,
	// Self-reported by the environment's event reporter.
	"POST /v1/environments/{environment_id}/events": true,
	// Called by the provisioning client (`erun init`, deploy retrofit); idempotent, nothing for a dialog to decide.
	"POST /v1/environments/{environment_id}/machine-identity": true,
	// Transport resume cursor for a client's reconnect loop; a console live view is its eventual reader.
	"GET /v1/events": true,
	// For a caller that knows only its environment id; operators use GET /v1/jobs.
	"GET /v1/environments/{environment_id}/jobs": true,
	// The definition upload is absent: the desktop owns it through apiRouteWailsBindings.
	// One-time, operations-only break-glass repair for a legacy "operations" bootstrap name.
	"PATCH /v1/tenants/reconcile-bootstrap-name": true,
	// CLI-only preview of a standalone new-cluster bootstrap; the desktop previews via POST /v1/environments.
	"POST /v1/provision": true,
}

// KnownUnsurfacedRoutes records routes that still need an operator surface; it is a tracker, not an exemption.
// It is shrink-only: the gate fails when a listed route gains a surface, but entries for deleted routes
// must be removed by hand.
var KnownUnsurfacedRoutes = map[string]bool{
	// Operator-facing; needs a surface designed with the rest of identity administration.
	"POST /v1/identity/orgs":                             true,
	"GET /v1/releases":                                   true,
	"GET /v1/releases/{release_id}":                      true,
	"GET /v1/reviews/{review_id}/builds/{build_id}":      true,
	"GET /v1/reviews/{review_id}/releases":               true,
	"GET /v1/usage-events":                               true,
	"POST /v1/environments/{environment_id}/dns01-token": true,
	"POST /v1/releases":                                  true,
}

package model

// UnexposeParams are the inputs to removing an env's wildcard DNS record; the
// Ingress goes away with the env's namespace.
type UnexposeParams struct {
	Tenant      string
	Environment string
	ProjectRoot string
	// SkipIfUnconfigured makes a missing platform block a no-op, for the delete Job.
	SkipIfUnconfigured bool
	// ServicesZone/PlatformNamespace replace ProjectRoot's platform block, for
	// the sourceless delete Job.
	ServicesZone      string
	PlatformNamespace string
	ErunAlias         string
}

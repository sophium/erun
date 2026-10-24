package model

// E2ETarget scopes an erun e2e run to a deployed environment.
type E2ETarget struct {
	Tenant            string
	Environment       string
	Namespace         string
	KubernetesContext string
}

// E2EParams is one erun e2e run.
type E2EParams struct {
	Plan    E2EPlan
	Target  E2ETarget
	BaseURL string
	Version string
	// TargetNamed: only an explicitly named target is trusted to be what a
	// --base-url serves, so only it supplies that URL's expected version.
	TargetNamed bool
}

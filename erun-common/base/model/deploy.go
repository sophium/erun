package model

type KubernetesDeploymentCheckParams struct {
	Name               string
	Namespace          string
	KubernetesContext  string
	ExpectedRepoPath   string
	ExpectedSSHD       *bool
	ExpectedMCPPort    int
	ExpectedSSHPort    int
	ExpectedRuntimePod RuntimePodResources
}

type DeployTarget struct {
	Tenant          string
	Environment     string
	RepoPath        string
	VersionOverride string
	// Components, when non-empty, fully determines what deploys, overriding the saved set and plan.
	// Unknown names error at resolve.
	Components []string
	// Force re-runs the helm upgrade even when the release already matches the version.
	Force bool
	// RolloutTimeout overrides deploy.timeout and DefaultHelmDeploymentTimeout for this deploy only.
	RolloutTimeout string
	// NamespaceQuotaOverride overrides the env's namespace ceiling for this deploy only.
	NamespaceQuotaOverride NamespaceResourceQuota
	// MCPAuthPublicKeyPath empty rethreads the recorded key, so auth survives a version bump.
	MCPAuthPublicKeyPath string
	// DisableMCPAuth is the only way to turn auth off, so no deploy downgrades an edge by omission.
	DisableMCPAuth bool
	// RuntimeImageOverride lets an env run another image before its own <tenant>-devops image exists.
	RuntimeImageOverride string
	// RuntimeChartOverride is an OCI chart reference, optionally versioned, stated by the operator.
	RuntimeChartOverride string
}

type KubernetesDeployContext struct {
	Dir           string
	ComponentName string
	ChartPath     string
}

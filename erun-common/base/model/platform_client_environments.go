package model

// PlatformCreateEnvironmentParams is the environment-registration input.
type PlatformCreateEnvironmentParams struct {
	Name              string `json:"name"`
	Type              string `json:"type"`
	ContextID         string `json:"contextId,omitempty"`
	KubernetesContext string `json:"kubernetesContext,omitempty"`
	RuntimeVersion    string `json:"runtimeVersion,omitempty"`
	// Adopt records an existing environment without provisioning or deploying it;
	// it requires KubernetesContext and forbids RuntimeVersion and ContextID.
	Adopt bool `json:"adopt,omitempty"`
	// TenantID is honoured only for operations-tenant callers (others get 403).
	TenantID string `json:"tenantId,omitempty"`
}

// PlatformDeployEnvironmentParams.Version empty deploys the pinned RuntimeVersion.
type PlatformDeployEnvironmentParams struct {
	Version string `json:"version,omitempty"`
}

// PlatformCreateContextParams registers a cluster; Preview only returns the plan.
type PlatformCreateContextParams struct {
	Name               string `json:"name"`
	CloudProviderAlias string `json:"cloudProviderAlias"`
	Region             string `json:"region"`
	InstanceType       string `json:"instanceType,omitempty"`
	DiskType           string `json:"diskType,omitempty"`
	DiskSizeGB         int    `json:"diskSizeGb,omitempty"`
	Preview            bool   `json:"preview,omitempty"`
}

// PlatformCreateContextResult has a nil Context for a preview.
type PlatformCreateContextResult struct {
	Context *PlatformContext `json:"context,omitempty"`
	Plan    []string         `json:"plan"`
}

// PlatformProvisionParams sets exactly one of Context or KubernetesContext.
type PlatformProvisionParams struct {
	Environment       PlatformProvisionEnvironment `json:"environment"`
	Context           *PlatformProvisionContext    `json:"context,omitempty"`
	KubernetesContext string                       `json:"kubernetesContext,omitempty"`
}

type PlatformProvisionEnvironment struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type PlatformProvisionContext struct {
	Name               string `json:"name"`
	CloudProviderAlias string `json:"cloudProviderAlias"`
	Region             string `json:"region"`
	InstanceType       string `json:"instanceType,omitempty"`
	DiskType           string `json:"diskType,omitempty"`
	DiskSizeGB         int    `json:"diskSizeGb,omitempty"`
}

// PlatformProvisionResult reports a quota block in QuotaOk instead of failing.
type PlatformProvisionResult struct {
	Plan    []string `json:"plan"`
	QuotaOk bool     `json:"quotaOk"`
}

package settings

type ERunConfig struct {
	DefaultTenant  string                `json:"defaultTenant"`
	CloudProviders []CloudProviderStatus `json:"cloudProviders,omitempty"`
	CloudContexts  []CloudContextStatus  `json:"cloudContexts,omitempty"`
	// OpenRouter is one root-level catalog every environment selects from.
	OpenRouter *OpenRouterConfig `json:"openRouter,omitempty"`
	// OpenRouterDefaults pre-fills the catalog from this machine's Claude Code; it
	// is not persisted.
	OpenRouterDefaults *HostGatewayDefaults `json:"openRouterDefaults,omitempty"`
}

// OpenRouterModel carries the context window, which a gateway id lacks.
type OpenRouterModel struct {
	ID      string `json:"id"`
	Context int    `json:"context,omitempty"`
	// RequiresReasoningEcho must round-trip: the editor writes the whole catalog
	// back, so a dropped field would be erased.
	RequiresReasoningEcho bool `json:"requiresReasoningEcho,omitempty"`
}

// OpenRouterConfig references the credential as a Secret, never a value.
type OpenRouterConfig struct {
	BaseURL string `json:"baseUrl,omitempty"`
	// AuthTokenRef is a secret store ref; the token never enters config.
	AuthTokenRef string            `json:"authTokenRef,omitempty"`
	DefaultModel string            `json:"defaultModel,omitempty"`
	Models       []OpenRouterModel `json:"models,omitempty"`
}

type TenantConfig struct {
	Name                      string                `json:"name"`
	DefaultEnvironment        string                `json:"defaultEnvironment"`
	APIURL                    string                `json:"apiUrl"`
	CloudProviderAliases      []string              `json:"cloudProviderAliases,omitempty"`
	PrimaryCloudProviderAlias string                `json:"primaryCloudProviderAlias,omitempty"`
	CloudProviders            []CloudProviderStatus `json:"cloudProviders,omitempty"`
}

type CloudProviderStatus struct {
	Alias         string `json:"alias"`
	Provider      string `json:"provider"`
	Username      string `json:"username,omitempty"`
	AccountID     string `json:"accountId,omitempty"`
	Profile       string `json:"profile,omitempty"`
	OIDCIssuerURL string `json:"oidcIssuerUrl,omitempty"`
	Status        string `json:"status"`
	Message       string `json:"message,omitempty"`
}

type CloudProviderBearerToken struct {
	Alias    string              `json:"alias"`
	Issuer   string              `json:"issuer,omitempty"`
	Token    string              `json:"token"`
	Provider CloudProviderStatus `json:"provider"`
}

type CloudContextStatus struct {
	Name                string `json:"name"`
	Provider            string `json:"provider"`
	CloudProviderAlias  string `json:"cloudProviderAlias"`
	Region              string `json:"region"`
	InstanceID          string `json:"instanceId,omitempty"`
	PublicIP            string `json:"publicIp,omitempty"`
	InstanceType        string `json:"instanceType"`
	DiskType            string `json:"diskType"`
	DiskSizeGB          int    `json:"diskSizeGb"`
	KubernetesContext   string `json:"kubernetesContext"`
	Status              string `json:"status"`
	Message             string `json:"message,omitempty"`
	StopProtection      bool   `json:"stopProtection,omitempty"`
	StopProtectionKnown bool   `json:"stopProtectionKnown,omitempty"`
}

type AWSCloudAliasInput struct {
	Alias         string `json:"alias,omitempty"`
	Username      string `json:"username,omitempty"`
	AccountID     string `json:"accountId,omitempty"`
	Profile       string `json:"profile,omitempty"`
	SSORegion     string `json:"ssoRegion,omitempty"`
	SSOStartURL   string `json:"ssoStartUrl,omitempty"`
	OIDCIssuerURL string `json:"oidcIssuerUrl,omitempty"`
}

// EnvironmentCloudAlias is one provider-type slot, so AWS and Cloudflare
// aliases can coexist.
type EnvironmentCloudAlias struct {
	Provider string   `json:"provider"`
	Alias    string   `json:"alias"`
	Options  []string `json:"options"`
}

type CloudContextInitInput struct {
	Name               string `json:"name,omitempty"`
	CloudProviderAlias string `json:"cloudProviderAlias"`
	Region             string `json:"region"`
	InstanceType       string `json:"instanceType"`
	DiskType           string `json:"diskType"`
	DiskSizeGB         int    `json:"diskSizeGb"`
}

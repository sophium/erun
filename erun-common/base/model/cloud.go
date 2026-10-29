package model

type CloudProviderConfig struct {
	Alias         string `json:"alias" yaml:"alias"`
	Provider      string `json:"provider" yaml:"provider"`
	Username      string `json:"username,omitempty" yaml:"username,omitempty"`
	AccountID     string `json:"accountId,omitempty" yaml:"accountid,omitempty"`
	Profile       string `json:"profile,omitempty" yaml:"profile,omitempty"`
	SSORegion     string `json:"ssoRegion,omitempty" yaml:"ssoregion,omitempty"`
	SSOStartURL   string `json:"ssoStartUrl,omitempty" yaml:"ssostarturl,omitempty"`
	OIDCIssuerURL string `json:"oidcIssuerUrl,omitempty" yaml:"oidcissuerurl,omitempty"`

	// Cloudflare is set only for CloudProviderCloudflare; the token is stored as a secret-store reference.
	Cloudflare *CloudflareProviderConfig `json:"cloudflare,omitempty" yaml:"cloudflare,omitempty"`

	// ERun is set only for CloudProviderERun; the refresh token is stored as a secret-store reference.
	ERun *ERunProviderConfig `json:"erun,omitempty" yaml:"erun,omitempty"`
}

// CloudflareProviderConfig is a Cloudflare alias identity; only TokenRef is persisted, never the token.
type CloudflareProviderConfig struct {
	AccountID string `json:"accountId,omitempty" yaml:"accountid,omitempty"`
	TokenName string `json:"tokenName,omitempty" yaml:"tokenname,omitempty"`
	TokenRef  string `json:"tokenRef,omitempty" yaml:"tokenref,omitempty"`
}

type CloudProviderStatus struct {
	CloudProviderConfig `json:",inline" yaml:",inline"`
	Status              string `json:"status" yaml:"status"`
	Message             string `json:"message,omitempty" yaml:"message,omitempty"`
}

type InitAWSCloudProviderParams struct {
	Profile       string
	Username      string
	AccountID     string
	RoleName      string
	Region        string
	SSORegion     string
	SSOStartURL   string
	OIDCIssuerURL string
	SkipLogin     bool
}

type CloudLoginParams struct {
	Alias string
	Force bool
	// Scopes are added to "openid offline_access"; reserved scopes such as
	// urn:zitadel:iam:user:resourceowner are often not advertised in discovery.
	Scopes []string
	// Flow selects the OIDC grant for an erun-hosted alias; other providers ignore it.
	Flow string
}

type CloudBearerParams struct {
	Alias    string
	Audience string
}

type SetEnvironmentCloudAliasParams struct {
	Tenant      string
	Environment string
	Alias       string
}

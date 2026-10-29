package model

// InitCloudflareCloudProviderParams adds a Cloudflare alias; APIToken needs Zone + DNS edit
// plus any scopes it will use.
type InitCloudflareCloudProviderParams struct {
	AccountID string
	TokenName string
	APIToken  string
}

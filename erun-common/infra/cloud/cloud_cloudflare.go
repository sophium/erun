package cloud

// CloudflareTokenInfo is the used subset of Cloudflare's tokens/verify response.
type CloudflareTokenInfo struct {
	ID     string
	Status string
}

// CloudflareAccount is an account a token can act on.
type CloudflareAccount struct {
	ID   string
	Name string
}

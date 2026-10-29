package cloud

import (
	"time"
)

// OIDCDiscovery is the subset of the OIDC discovery document the device and PKCE flows need.
type OIDCDiscovery struct {
	Issuer                      string `json:"issuer"`
	AuthorizationEndpoint       string `json:"authorization_endpoint"`
	TokenEndpoint               string `json:"token_endpoint"`
	DeviceAuthorizationEndpoint string `json:"device_authorization_endpoint,omitempty"`
}

// ERunDeviceAuthorization is a device authorization grant's initial response (RFC 8628 3.2).
type ERunDeviceAuthorization struct {
	DeviceCode              string
	UserCode                string
	VerificationURI         string
	VerificationURIComplete string
	ExpiresIn               time.Duration
	Interval                time.Duration
}

// ERunTokens is a successful OIDC token response.
type ERunTokens struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    time.Duration
}

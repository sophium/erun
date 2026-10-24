package config

import (
	"strings"
)

// ERunProviderConfig is the erun-hosted-platform identity for a cloud alias:
// the API this alias authenticates against, the OIDC client it authenticates
// as, and a reference to the stored credential (never the credential itself,
// which lives only in the secret store).
//
// Exactly one of RefreshTokenRef and ClientSecretRef is set, and which one
// says what kind of identity the alias is. A refresh-token reference is a
// signed-in person's session, delegated to an environment by `erun init` on
// the host they signed in on; a client-secret reference is a machine identity
// erun provisioned for one environment, which presents itself through the
// client_credentials grant and belongs to no person at all. The two are not
// interchangeable in either direction: a machine identity can never act as its
// operator, and a signed-in session is not something a pod should be holding.
type ERunProviderConfig struct {
	APIURL          string `json:"apiUrl,omitempty" yaml:"apiurl,omitempty"`
	ClientID        string `json:"clientId,omitempty" yaml:"clientid,omitempty"`
	RefreshTokenRef string `json:"refreshTokenRef,omitempty" yaml:"refreshtokenref,omitempty"`
	ClientSecretRef string `json:"clientSecretRef,omitempty" yaml:"clientsecretref,omitempty"`
}

// IsMachineIdentity reports whether this alias authenticates as a provisioned
// machine identity rather than a delegated operator session.
func (c ERunProviderConfig) IsMachineIdentity() bool {
	return strings.TrimSpace(c.RefreshTokenRef) == "" && strings.TrimSpace(c.ClientSecretRef) != ""
}

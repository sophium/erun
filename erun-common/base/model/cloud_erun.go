package model

import (
	"strings"
)

// ERunProviderConfig is a platform alias identity. Exactly one of RefreshTokenRef (a
// person's delegated session) and ClientSecretRef (a provisioned machine identity) is set.
type ERunProviderConfig struct {
	APIURL          string `json:"apiUrl,omitempty" yaml:"apiurl,omitempty"`
	ClientID        string `json:"clientId,omitempty" yaml:"clientid,omitempty"`
	RefreshTokenRef string `json:"refreshTokenRef,omitempty" yaml:"refreshtokenref,omitempty"`
	ClientSecretRef string `json:"clientSecretRef,omitempty" yaml:"clientsecretref,omitempty"`
}

func (c ERunProviderConfig) IsMachineIdentity() bool {
	return strings.TrimSpace(c.RefreshTokenRef) == "" && strings.TrimSpace(c.ClientSecretRef) != ""
}

// InitERunCloudProviderParams carries the API base URL; everything else is discovered from the platform.
type InitERunCloudProviderParams struct {
	APIURL string
}

package settings

import (
	"fmt"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/command/ai"
	"github.com/sophium/erun/erun-common/infra/cloud/secretstore"
)

const (
	gatewayCredentialSourceSaved = "saved"
	// gatewayCredentialSourceHost reuses this machine's Claude Code key, only for
	// the same gateway.
	gatewayCredentialSourceHost = "host"
)

// GatewayCredentialStatus identifies the credential a deploy delivers
// without revealing it.
type GatewayCredentialStatus struct {
	Ref string `json:"ref"`
	// Source is empty when no credential exists and a deploy cannot authenticate.
	Source string `json:"source,omitempty"`
	Hint   string `json:"hint,omitempty"`
	// HostEndpoint lets a non-reusable host key say which gateway it belongs to.
	HostEndpoint string `json:"hostEndpoint,omitempty"`
}

// LoadGatewayCredentialStatus reports which credential a deploy would deliver.
func (s *Service) LoadGatewayCredentialStatus() (GatewayCredentialStatus, error) {
	catalog, err := s.gatewayCatalog()
	if err != nil {
		return GatewayCredentialStatus{}, err
	}
	return gatewayCredentialStatus(catalog), nil
}

// SaveGatewayCredential stores a gateway credential in the operator secret
// store, keeping it out of config, chart values, and traces.
func (s *Service) SaveGatewayCredential(token string) (GatewayCredentialStatus, error) {
	trimmed := strings.TrimSpace(token)
	if trimmed == "" {
		return GatewayCredentialStatus{}, fmt.Errorf("a gateway credential is required")
	}
	catalog, err := s.gatewayCatalog()
	if err != nil {
		return GatewayCredentialStatus{}, err
	}
	store, err := secretstore.DefaultCloudSecretStore()
	if err != nil {
		return GatewayCredentialStatus{}, fmt.Errorf("resolve cloud secret store: %w", err)
	}
	if err := store.SaveCloudSecret(gatewayCredentialRef(catalog), trimmed); err != nil {
		return GatewayCredentialStatus{}, fmt.Errorf("save gateway credential: %w", err)
	}
	return gatewayCredentialStatus(catalog), nil
}

// ClearGatewayCredential removes a saved credential, falling back to the host key.
func (s *Service) ClearGatewayCredential() (GatewayCredentialStatus, error) {
	catalog, err := s.gatewayCatalog()
	if err != nil {
		return GatewayCredentialStatus{}, err
	}
	store, err := secretstore.DefaultCloudSecretStore()
	if err != nil {
		return GatewayCredentialStatus{}, fmt.Errorf("resolve cloud secret store: %w", err)
	}
	if err := store.DeleteCloudSecret(gatewayCredentialRef(catalog)); err != nil {
		return GatewayCredentialStatus{}, fmt.Errorf("clear gateway credential: %w", err)
	}
	return gatewayCredentialStatus(catalog), nil
}

func (s *Service) gatewayCatalog() (*configpkg.OpenRouterConfig, error) {
	config, _, err := s.d.Store.LoadERunConfig()
	if err != nil {
		return nil, err
	}
	return config.OpenRouter, nil
}

func gatewayCredentialRef(catalog *configpkg.OpenRouterConfig) string {
	if ref := catalog.AuthTokenRefName(); ref != "" {
		return ref
	}
	return configpkg.DefaultOpenRouterAuthTokenRef
}

// gatewayCredentialStatus must mirror resolveGatewayCredential's precedence so
// the catalog never reports what a deploy will not do.
func gatewayCredentialStatus(catalog *configpkg.OpenRouterConfig) GatewayCredentialStatus {
	status := GatewayCredentialStatus{Ref: gatewayCredentialRef(catalog)}
	if saved, ok := savedGatewayCredential(status.Ref); ok {
		status.Source = gatewayCredentialSourceSaved
		status.Hint = credentialHint(saved)
		return status
	}
	status.HostEndpoint = ai.HostClaudeGatewayEndpoint()
	if token, ok := ai.HostClaudeGatewayCredentialFor(catalog.Endpoint()); ok {
		status.Source = gatewayCredentialSourceHost
		status.Hint = credentialHint(token)
	}
	return status
}

// savedGatewayCredential reports "none" for an unreadable store so a broken
// store cannot stop the catalog opening.
func savedGatewayCredential(ref string) (string, bool) {
	store, err := secretstore.DefaultCloudSecretStore()
	if err != nil {
		return "", false
	}
	saved, err := store.LoadCloudSecret(ref)
	if err != nil {
		return "", false
	}
	value := strings.TrimSpace(saved)
	return value, value != ""
}

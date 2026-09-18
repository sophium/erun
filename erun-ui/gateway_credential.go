package main

import (
	"fmt"
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"
)

// Sources a gateway credential can come from, as reported to the catalog.
const (
	// gatewayCredentialSourceSaved is a value the operator saved in erun's own
	// operator secret store.
	gatewayCredentialSourceSaved = "saved"
	// gatewayCredentialSourceHost is the key this machine's own Claude Code
	// authenticates with, reused because it is already pointed at this same
	// gateway.
	gatewayCredentialSourceHost = "host"
)

// uiGatewayCredentialStatus is what the catalog shows about the credential a
// deploy will deliver: where it comes from, and which value, identified rather
// than revealed.
type uiGatewayCredentialStatus struct {
	// Ref is the operator secret store ref the credential lives under.
	Ref string `json:"ref"`
	// Source is gatewayCredentialSourceSaved, gatewayCredentialSourceHost, or
	// empty when neither exists and a deploy cannot authenticate.
	Source string `json:"source,omitempty"`
	// Hint identifies the value without revealing it.
	Hint string `json:"hint,omitempty"`
	// HostEndpoint is the gateway this machine's own Claude Code is pointed at,
	// when it is pointed at one. It is reported so a key that will not be reused
	// can say which endpoint it belongs to, rather than reading as no key at all
	// while the operator can see it in their own settings.
	HostEndpoint string `json:"hostEndpoint,omitempty"`
}

// LoadGatewayCredentialStatus reports which credential a deploy would deliver.
func (a *App) LoadGatewayCredentialStatus() (uiGatewayCredentialStatus, error) {
	catalog, err := a.gatewayCatalog()
	if err != nil {
		return uiGatewayCredentialStatus{}, err
	}
	return gatewayCredentialStatus(catalog), nil
}

// SaveGatewayCredential stores an explicit gateway credential, overriding the
// key this machine's Claude Code uses.
//
// It goes to erun's own operator secret store — the same place the Cloudflare
// token lives — so the value stays out of config.yaml, out of every chart
// value, and out of the deploy trace.
func (a *App) SaveGatewayCredential(token string) (uiGatewayCredentialStatus, error) {
	trimmed := strings.TrimSpace(token)
	if trimmed == "" {
		return uiGatewayCredentialStatus{}, fmt.Errorf("a gateway credential is required")
	}
	catalog, err := a.gatewayCatalog()
	if err != nil {
		return uiGatewayCredentialStatus{}, err
	}
	store, err := eruncommon.DefaultCloudSecretStore()
	if err != nil {
		return uiGatewayCredentialStatus{}, fmt.Errorf("resolve cloud secret store: %w", err)
	}
	if err := store.SaveCloudSecret(gatewayCredentialRef(catalog), trimmed); err != nil {
		return uiGatewayCredentialStatus{}, fmt.Errorf("save gateway credential: %w", err)
	}
	return gatewayCredentialStatus(catalog), nil
}

// ClearGatewayCredential removes a saved credential, returning the catalog to
// this machine's own Claude Code key rather than leaving the gateway
// unauthenticated.
func (a *App) ClearGatewayCredential() (uiGatewayCredentialStatus, error) {
	catalog, err := a.gatewayCatalog()
	if err != nil {
		return uiGatewayCredentialStatus{}, err
	}
	store, err := eruncommon.DefaultCloudSecretStore()
	if err != nil {
		return uiGatewayCredentialStatus{}, fmt.Errorf("resolve cloud secret store: %w", err)
	}
	if err := store.DeleteCloudSecret(gatewayCredentialRef(catalog)); err != nil {
		return uiGatewayCredentialStatus{}, fmt.Errorf("clear gateway credential: %w", err)
	}
	return gatewayCredentialStatus(catalog), nil
}

// gatewayCatalog reads the erun-level gateway catalog, nil when none exists.
func (a *App) gatewayCatalog() (*eruncommon.OpenRouterConfig, error) {
	config, _, err := a.deps.store.LoadERunConfig()
	if err != nil {
		return nil, err
	}
	return config.OpenRouter, nil
}

// gatewayCredentialRef names the store ref this install's credential lives
// under: the catalog's own ref, or the conventional default.
func gatewayCredentialRef(catalog *eruncommon.OpenRouterConfig) string {
	if ref := catalog.AuthTokenRefName(); ref != "" {
		return ref
	}
	return eruncommon.DefaultOpenRouterAuthTokenRef
}

// gatewayCredentialStatus resolves the credential a deploy would deliver,
// reporting where it comes from rather than what it is.
//
// It mirrors resolveGatewayCredential's precedence — saved value first, this
// machine's own key second and only for the same endpoint — so the catalog
// cannot tell an operator one thing while a deploy does another. Reporting a
// host key as usable against a different gateway would be the crueller version
// of that mistake: the operator would be told their key would be sent somewhere
// it will not be, and cannot be.
func gatewayCredentialStatus(catalog *eruncommon.OpenRouterConfig) uiGatewayCredentialStatus {
	status := uiGatewayCredentialStatus{Ref: gatewayCredentialRef(catalog)}
	if saved, ok := savedGatewayCredential(status.Ref); ok {
		status.Source = gatewayCredentialSourceSaved
		status.Hint = credentialHint(saved)
		return status
	}
	status.HostEndpoint = eruncommon.HostClaudeGatewayEndpoint()
	if token, ok := eruncommon.HostClaudeGatewayCredentialFor(catalog.Endpoint()); ok {
		status.Source = gatewayCredentialSourceHost
		status.Hint = credentialHint(token)
	}
	return status
}

// savedGatewayCredential reads the explicitly saved value, reporting "none" for
// a store that is absent or unreadable: this feeds a status line, and a broken
// store must not stop the catalog opening.
func savedGatewayCredential(ref string) (string, bool) {
	store, err := eruncommon.DefaultCloudSecretStore()
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

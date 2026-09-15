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
	// authenticates with, used when nothing is saved.
	gatewayCredentialSourceHost = "host"
)

// uiGatewayCredentialStatus is what the catalog shows about the credential a
// deploy will deliver: where it comes from, and which value, identified rather
// than revealed.
type uiGatewayCredentialStatus struct {
	// Ref is the operator secret store ref the credential lives under.
	Ref string `json:"ref"`
	// Source is gatewayCredentialSourceSaved, gatewayCredentialSourceHost, or
	// empty when neither exists — in which case a deploy cannot authenticate.
	Source string `json:"source,omitempty"`
	// Hint identifies the value without revealing it.
	Hint string `json:"hint,omitempty"`
}

// LoadGatewayCredentialStatus reports which credential a deploy would deliver.
func (a *App) LoadGatewayCredentialStatus() (uiGatewayCredentialStatus, error) {
	ref, err := a.gatewayCredentialRef()
	if err != nil {
		return uiGatewayCredentialStatus{}, err
	}
	return gatewayCredentialStatus(ref), nil
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
	ref, err := a.gatewayCredentialRef()
	if err != nil {
		return uiGatewayCredentialStatus{}, err
	}
	store, err := eruncommon.DefaultCloudSecretStore()
	if err != nil {
		return uiGatewayCredentialStatus{}, fmt.Errorf("resolve cloud secret store: %w", err)
	}
	if err := store.SaveCloudSecret(ref, trimmed); err != nil {
		return uiGatewayCredentialStatus{}, fmt.Errorf("save gateway credential: %w", err)
	}
	return gatewayCredentialStatus(ref), nil
}

// ClearGatewayCredential removes a saved credential, returning the catalog to
// this machine's own Claude Code key rather than leaving the gateway
// unauthenticated.
func (a *App) ClearGatewayCredential() (uiGatewayCredentialStatus, error) {
	ref, err := a.gatewayCredentialRef()
	if err != nil {
		return uiGatewayCredentialStatus{}, err
	}
	store, err := eruncommon.DefaultCloudSecretStore()
	if err != nil {
		return uiGatewayCredentialStatus{}, fmt.Errorf("resolve cloud secret store: %w", err)
	}
	if err := store.DeleteCloudSecret(ref); err != nil {
		return uiGatewayCredentialStatus{}, fmt.Errorf("clear gateway credential: %w", err)
	}
	return gatewayCredentialStatus(ref), nil
}

// gatewayCredentialRef names the store ref this install's credential lives
// under: the catalog's own ref, or the conventional default when it names none.
func (a *App) gatewayCredentialRef() (string, error) {
	config, _, err := a.deps.store.LoadERunConfig()
	if err != nil {
		return "", err
	}
	if config.OpenRouter != nil {
		if named := strings.TrimSpace(config.OpenRouter.AuthTokenRef); named != "" {
			return named, nil
		}
	}
	return eruncommon.DefaultOpenRouterAuthTokenRef, nil
}

// gatewayCredentialStatus resolves the credential a deploy would deliver,
// reporting where it comes from rather than what it is.
//
// It mirrors gatewayCredentialForDeploy's precedence — saved value first, this
// machine's own key second — so the catalog cannot tell an operator one thing
// while a deploy does another.
func gatewayCredentialStatus(ref string) uiGatewayCredentialStatus {
	status := uiGatewayCredentialStatus{Ref: ref}
	if saved, ok := savedGatewayCredential(ref); ok {
		status.Source = gatewayCredentialSourceSaved
		status.Hint = credentialHint(saved)
		return status
	}
	if token, ok := eruncommon.HostClaudeGatewayCredential(); ok {
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

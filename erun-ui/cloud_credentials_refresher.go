package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/primitive/open"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

const (
	// AWS temp creds default to ~1h; 45 min leaves headroom for in-pod SDK caching.
	credentialRefreshInterval = 45 * time.Minute

	credentialRefreshLeadTime = 5 * time.Minute

	credentialRefreshBackoff = 30 * time.Second

	credentialMCPReadyTimeout = 5 * time.Minute
)

type cloudCredentialsRefresher struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func (a *App) cloudCredentialsRefresherKey(selection uiSelection) string {
	return strings.TrimSpace(selection.Tenant) + "/" + strings.TrimSpace(selection.Environment)
}

// startCloudCredentialsRefresherForSelection pushes temporary host credentials to
// the env's pod; an attached AWS alias is the opt-in. Idempotent per selection.
func (a *App) startCloudCredentialsRefresherForSelection(selection uiSelection) {
	selection = normalizeSelection(selection)
	alias, result, ok := a.resolveCloudCredentialsRefreshTarget(selection)
	if !ok {
		return
	}

	key := a.cloudCredentialsRefresherKey(selection)
	a.mu.Lock()
	if a.credentialRefreshers == nil {
		a.credentialRefreshers = make(map[string]*cloudCredentialsRefresher)
	}
	if _, ok := a.credentialRefreshers[key]; ok {
		a.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	refresher := &cloudCredentialsRefresher{cancel: cancel, done: make(chan struct{})}
	a.credentialRefreshers[key] = refresher
	a.mu.Unlock()

	go func() {
		defer close(refresher.done)
		a.runCloudCredentialsRefresher(ctx, selection, alias, result)
	}()
}

func (a *App) resolveCloudCredentialsRefreshTarget(selection uiSelection) (string, config.OpenResult, bool) {
	if selection.Tenant == "" || selection.Environment == "" {
		return "", config.OpenResult{}, false
	}
	envConfig, _, err := a.deps.store.LoadEnvConfig(selection.Tenant, selection.Environment)
	if err != nil {
		return "", config.OpenResult{}, false
	}
	// Not gated on env type: every pod reaches AWS only through the profile pushed here.
	// CloudProviderAlias holds only AWS aliases; Cloudflare ships at deploy time instead.
	if strings.TrimSpace(envConfig.CloudProviderAlias) == "" {
		return "", config.OpenResult{}, false
	}
	// Defense in depth: never export temporary credentials from a token-based provider.
	provider, err := cloud.ResolveCloudProvider(a.deps.store, envConfig.CloudProviderAlias)
	if err != nil || provider.Provider != config.CloudProviderAWS {
		return "", config.OpenResult{}, false
	}
	result, err := open.ResolveOpen(a.deps.store, shared.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return "", config.OpenResult{}, false
	}
	return envConfig.CloudProviderAlias, result, true
}

// stopCloudCredentialsRefresherForSelection stops the refresher and clears the in-pod credentials.
func (a *App) stopCloudCredentialsRefresherForSelection(selection uiSelection) {
	selection = normalizeSelection(selection)
	key := a.cloudCredentialsRefresherKey(selection)
	a.mu.Lock()
	refresher, ok := a.credentialRefreshers[key]
	if ok {
		delete(a.credentialRefreshers, key)
	}
	a.mu.Unlock()
	if !ok {
		return
	}
	refresher.cancel()
	<-refresher.done
	result, err := open.ResolveOpen(a.deps.store, shared.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return
	}
	endpoint := mcpEndpointForOpenResult(result)
	clearCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = clearCloudHostCredentialsViaMCP(clearCtx, endpoint, a.mcpBearer(selection.Tenant, selection.Environment))
}

func (a *App) stopAllCloudCredentialsRefreshersLocked() {
	for key, refresher := range a.credentialRefreshers {
		refresher.cancel()
		delete(a.credentialRefreshers, key)
	}
}

// reconcileCloudCredentialsRefresherForSelection applies a saved setting without re-opening the env.
func (a *App) reconcileCloudCredentialsRefresherForSelection(selection uiSelection, enabled bool) {
	a.stopCloudCredentialsRefresherForSelection(selection)
	if enabled {
		a.startCloudCredentialsRefresherForSelection(selection)
	}
}

func (a *App) runCloudCredentialsRefresher(ctx context.Context, selection uiSelection, alias string, result config.OpenResult) {
	endpoint := mcpEndpointForOpenResult(result)
	mcpPort := config.MCPPortForResult(result)
	notifiedFailure := false
	for {
		if err := a.waitForMCPReady(ctx, mcpPort); err != nil {
			return
		}
		creds, err := cloud.ExportCloudProviderCredentials(core.Context{}, a.deps.store, alias, a.deps.cloudDeps)
		if err != nil {
			a.surfaceCredentialRefreshFailure(selection, fmt.Errorf("export host credentials: %w", err), &notifiedFailure)
			if !sleepWithCancel(ctx, credentialRefreshBackoff) {
				return
			}
			continue
		}
		bearer := a.mcpBearer(result.Tenant, result.EnvConfig.Name)
		if err := injectCloudHostCredentialsViaMCP(ctx, endpoint, bearer, creds.AccessKeyID, creds.SecretAccessKey, creds.SessionToken, creds.Expiration); err != nil {
			a.surfaceCredentialRefreshFailure(selection, fmt.Errorf("inject host credentials into runtime: %w", err), &notifiedFailure)
			if !sleepWithCancel(ctx, credentialRefreshBackoff) {
				return
			}
			continue
		}
		notifiedFailure = false
		if !sleepWithCancel(ctx, nextCredentialRefreshDelay(creds.Expiration)) {
			return
		}
	}
}

func (a *App) waitForMCPReady(ctx context.Context, port int) error {
	if a.deps.canReachMCPEndpoint == nil || a.deps.canReachMCPEndpoint(port) {
		return nil
	}
	deadline := time.NewTimer(credentialMCPReadyTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("mcp port %d not reachable", port)
		case <-ticker.C:
			if a.deps.canReachMCPEndpoint(port) {
				return nil
			}
		}
	}
}

func (a *App) surfaceCredentialRefreshFailure(selection uiSelection, err error, notified *bool) {
	if notified != nil && *notified {
		return
	}
	if notified != nil {
		*notified = true
	}
	a.emitEnvNotification("error", selection.Tenant, selection.Environment, notificationSourceCredentialRefreshFailed,
		fmt.Sprintf("Host credential refresh failed for %s/%s: %v", selection.Tenant, selection.Environment, err), "")
}

func nextCredentialRefreshDelay(expiration time.Time) time.Duration {
	if expiration.IsZero() {
		return credentialRefreshInterval
	}
	candidate := time.Until(expiration) - credentialRefreshLeadTime
	if candidate <= 0 {
		return credentialRefreshBackoff
	}
	if candidate < credentialRefreshInterval {
		return candidate
	}
	return credentialRefreshInterval
}

func sleepWithCancel(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

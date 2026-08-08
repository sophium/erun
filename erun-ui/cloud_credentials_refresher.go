package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

const (
	// AWS temp creds default to a ~1h TTL; 45 min leaves headroom even when
	// the pod's SDK caches creds in memory before re-reading the file.
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

// startCloudCredentialsRefresherForSelection seeds an env's runtime pod with
// temporary host credentials on the operator's behalf. Attaching an AWS cloud
// alias is itself the opt-in, so no separate toggle gates it. Idempotent per
// selection.
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

func (a *App) resolveCloudCredentialsRefreshTarget(selection uiSelection) (string, eruncommon.OpenResult, bool) {
	if selection.Tenant == "" || selection.Environment == "" {
		return "", eruncommon.OpenResult{}, false
	}
	envConfig, _, err := a.deps.store.LoadEnvConfig(selection.Tenant, selection.Environment)
	if err != nil {
		return "", eruncommon.OpenResult{}, false
	}
	// Deliberately not gated on env type. Where the worktree lives says nothing
	// about whose identity the pod should act as, and the pod is in the cluster
	// for every type — so it can only reach AWS through the profile pushed here,
	// which the chart wires into AWS_PROFILE whenever an AWS alias is attached.
	// CloudProviderAlias is the legacy scalar that only ever holds an AWS alias;
	// non-AWS aliases (Cloudflare) live in CloudProviderAliases, so a
	// Cloudflare-only env correctly reads empty here. Cloudflare credentials ship
	// at deploy time via a chart Secret minted by the erun binary, not this timer.
	if strings.TrimSpace(envConfig.CloudProviderAlias) == "" {
		return "", eruncommon.OpenResult{}, false
	}
	// Defense in depth: even if a Cloudflare alias somehow landed in the scalar,
	// the strict provider-type gate keeps the AWS-only credential push from
	// trying to export temporary credentials from a token-based provider.
	provider, err := eruncommon.ResolveCloudProvider(a.deps.store, envConfig.CloudProviderAlias)
	if err != nil || provider.Provider != eruncommon.CloudProviderAWS {
		return "", eruncommon.OpenResult{}, false
	}
	result, err := eruncommon.ResolveOpen(a.deps.store, eruncommon.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return "", eruncommon.OpenResult{}, false
	}
	return envConfig.CloudProviderAlias, result, true
}

// stopCloudCredentialsRefresherForSelection stops the refresher and clears the
// in-pod credentials so they do not linger after opt-out. Safe to call when
// none is running.
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
	result, err := eruncommon.ResolveOpen(a.deps.store, eruncommon.OpenParams{
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

// reconcileCloudCredentialsRefresherForSelection restarts the refresher to match
// a saved setting so the change takes effect without re-opening the env.
func (a *App) reconcileCloudCredentialsRefresherForSelection(selection uiSelection, enabled bool) {
	a.stopCloudCredentialsRefresherForSelection(selection)
	if enabled {
		a.startCloudCredentialsRefresherForSelection(selection)
	}
}

func (a *App) runCloudCredentialsRefresher(ctx context.Context, selection uiSelection, alias string, result eruncommon.OpenResult) {
	endpoint := mcpEndpointForOpenResult(result)
	mcpPort := eruncommon.MCPPortForResult(result)
	notifiedFailure := false
	for {
		if err := a.waitForMCPReady(ctx, mcpPort); err != nil {
			return
		}
		creds, err := eruncommon.ExportCloudProviderCredentials(eruncommon.Context{}, a.deps.store, alias, a.deps.cloudDeps)
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
	a.emitAppStatus(fmt.Sprintf("Host credential refresh failed for %s/%s: %v", selection.Tenant, selection.Environment, err), false)
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

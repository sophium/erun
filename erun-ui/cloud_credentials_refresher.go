package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

const (
	// credentialRefreshInterval caps how long the refresher sleeps between
	// pushes. AWS hands out temp creds with ~1h TTL by default; refreshing
	// every 45 min leaves comfortable headroom even when the SDK in the pod
	// holds creds in memory for a few minutes before re-reading the file.
	credentialRefreshInterval = 45 * time.Minute

	// credentialRefreshLeadTime is the safety margin before the recorded
	// expiration when scheduling the next refresh.
	credentialRefreshLeadTime = 5 * time.Minute

	// credentialRefreshBackoff is how long the refresher waits after a
	// transient failure (network blip, MCP port not yet ready) before
	// retrying. A persistent failure surfaces a notification once and then
	// falls back to the regular interval, so the user is not spammed.
	credentialRefreshBackoff = 30 * time.Second

	// credentialMCPReadyTimeout caps how long the refresher waits for the
	// runtime pod's MCP port-forward to become reachable before giving up
	// on this push cycle and retrying later.
	credentialMCPReadyTimeout = 5 * time.Minute
)

type cloudCredentialsRefresher struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func (a *App) cloudCredentialsRefresherKey(selection uiSelection) string {
	return strings.TrimSpace(selection.Tenant) + "/" + strings.TrimSpace(selection.Environment)
}

// startCloudCredentialsRefresherForSelection arms a background goroutine that
// keeps the runtime pod's ~/.aws/credentials seeded with temporary credentials
// derived from the host profile picked by the env's CloudProviderAlias. The
// refresh is opt-in per env (EnvConfig.RemoteHostCredentials) and only
// applies to AWS-backed remotes. Idempotent: a second call for the same
// selection is a no-op.
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

// resolveCloudCredentialsRefreshTarget evaluates the per-env preconditions that
// gate the host-credential refresher: a non-empty tenant/env, the
// RemoteHostCredentials opt-in on a remote worktree, a configured AWS-backed
// cloud provider alias, and a resolvable open target. It returns the provider
// alias and resolved OpenResult plus ok=true only when every precondition holds;
// any failing check returns ok=false so the caller becomes a no-op.
func (a *App) resolveCloudCredentialsRefreshTarget(selection uiSelection) (string, eruncommon.OpenResult, bool) {
	if selection.Tenant == "" || selection.Environment == "" {
		return "", eruncommon.OpenResult{}, false
	}
	envConfig, _, err := a.deps.store.LoadEnvConfig(selection.Tenant, selection.Environment)
	if err != nil {
		return "", eruncommon.OpenResult{}, false
	}
	if !envConfig.RemoteHostCredentials || !envConfig.RemoteWorktree() {
		return "", eruncommon.OpenResult{}, false
	}
	// CloudProviderAlias is the legacy scalar that always holds the env's AWS
	// alias; non-AWS aliases (Cloudflare) live in EnvConfig.CloudProviderAliases
	// and never reach this scalar. So a Cloudflare-attached env that carries no
	// AWS alias reads as "" here and the refresher no-ops — exactly the desired
	// behavior. Cloudflare credentials are delivered at deploy time via a chart
	// Secret minted by the erun binary, not pushed by this host-credential timer.
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

// stopCloudCredentialsRefresherForSelection signals the refresher to exit and
// pushes one best-effort clear so the remote ~/.aws/credentials no longer
// carries the erun-host profile. Safe to call when no refresher is running.
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
	_ = clearCloudHostCredentialsViaMCP(clearCtx, endpoint)
}

func (a *App) stopAllCloudCredentialsRefreshersLocked() {
	for key, refresher := range a.credentialRefreshers {
		refresher.cancel()
		delete(a.credentialRefreshers, key)
	}
}

// reconcileCloudCredentialsRefresherForSelection ensures the refresher matches
// the latest env config: turning the toggle off stops a running refresher;
// turning it on (with the env open) starts one. Called from
// SaveEnvironmentConfig so the change takes effect without re-opening.
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
		if err := injectCloudHostCredentialsViaMCP(ctx, endpoint, creds.AccessKeyID, creds.SecretAccessKey, creds.SessionToken, creds.Expiration); err != nil {
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
	if a.deps.canConnectLocalPort == nil || a.deps.canConnectLocalPort(port) {
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
			if a.deps.canConnectLocalPort(port) {
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

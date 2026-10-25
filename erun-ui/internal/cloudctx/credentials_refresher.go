package cloudctx

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud/providerconfig"
	"github.com/sophium/erun/erun-common/infra/cloud/providertoken"
	"github.com/sophium/erun/erun-common/primitive/open/resolve"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

const (
	// AWS temp creds default to ~1h; 45 min leaves headroom for in-pod SDK caching.
	credentialRefreshInterval = 45 * time.Minute

	credentialRefreshLeadTime = 5 * time.Minute

	credentialRefreshBackoff = 30 * time.Second

	credentialMCPReadyTimeout = 5 * time.Minute
)

type refresher struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func refresherKey(selection uimodel.Selection) string {
	return strings.TrimSpace(selection.Tenant) + "/" + strings.TrimSpace(selection.Environment)
}

// StartRefresher pushes temporary host credentials to the env's pod; an
// attached AWS alias is the opt-in. Idempotent per selection.
func (s *Service) StartRefresher(selection uimodel.Selection) {
	selection = uimodel.NormalizeSelection(selection)
	alias, result, ok := s.resolveRefreshTarget(selection)
	if !ok {
		return
	}

	key := refresherKey(selection)
	s.mu.Lock()
	if _, ok := s.refreshers[key]; ok {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &refresher{cancel: cancel, done: make(chan struct{})}
	s.refreshers[key] = r
	s.mu.Unlock()

	go func() {
		defer close(r.done)
		s.runRefresher(ctx, selection, alias, result)
	}()
}

func (s *Service) resolveRefreshTarget(selection uimodel.Selection) (string, config.OpenResult, bool) {
	if selection.Tenant == "" || selection.Environment == "" {
		return "", config.OpenResult{}, false
	}
	envConfig, _, err := s.d.Store.LoadEnvConfig(selection.Tenant, selection.Environment)
	if err != nil {
		return "", config.OpenResult{}, false
	}
	// Not gated on env type: every pod reaches AWS only through the profile pushed here.
	// CloudProviderAlias holds only AWS aliases; Cloudflare ships at deploy time instead.
	if strings.TrimSpace(envConfig.CloudProviderAlias) == "" {
		return "", config.OpenResult{}, false
	}
	// Defense in depth: never export temporary credentials from a token-based provider.
	provider, err := providerconfig.ResolveCloudProvider(s.d.Store, envConfig.CloudProviderAlias)
	if err != nil || provider.Provider != config.CloudProviderAWS {
		return "", config.OpenResult{}, false
	}
	result, err := resolve.ResolveOpen(s.d.Store, model.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return "", config.OpenResult{}, false
	}
	return envConfig.CloudProviderAlias, result, true
}

// StopRefresher stops the selection's refresher and clears the in-pod credentials.
func (s *Service) StopRefresher(selection uimodel.Selection) {
	selection = uimodel.NormalizeSelection(selection)
	key := refresherKey(selection)
	s.mu.Lock()
	r, ok := s.refreshers[key]
	if ok {
		delete(s.refreshers, key)
	}
	s.mu.Unlock()
	if !ok {
		return
	}
	r.cancel()
	<-r.done
	result, err := resolve.ResolveOpen(s.d.Store, model.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return
	}
	endpoint := s.d.MCPEndpoint(result)
	clearCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = s.clearHostCredentials(clearCtx, endpoint, s.d.MCPBearer(selection.Tenant, selection.Environment))
}

// StopAllRefreshers cancels every refresher without waiting or clearing
// in-pod credentials; it is the shutdown path.
func (s *Service) StopAllRefreshers() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, r := range s.refreshers {
		r.cancel()
		delete(s.refreshers, key)
	}
}

// ReconcileRefresher applies a saved setting without re-opening the env.
func (s *Service) ReconcileRefresher(selection uimodel.Selection, enabled bool) {
	s.StopRefresher(selection)
	if enabled {
		s.StartRefresher(selection)
	}
}

func (s *Service) runRefresher(ctx context.Context, selection uimodel.Selection, alias string, result config.OpenResult) {
	endpoint := s.d.MCPEndpoint(result)
	mcpPort := config.MCPPortForResult(result)
	notifiedFailure := false
	for {
		if err := s.waitForMCPReady(ctx, mcpPort); err != nil {
			return
		}
		creds, err := providertoken.ExportCloudProviderCredentials(core.Context{}, s.d.Store, alias, s.d.Cloud)
		if err != nil {
			s.surfaceRefreshFailure(selection, fmt.Errorf("export host credentials: %w", err), &notifiedFailure)
			if !sleepWithCancel(ctx, credentialRefreshBackoff) {
				return
			}
			continue
		}
		bearer := s.d.MCPBearer(result.Tenant, result.EnvConfig.Name)
		if err := s.injectHostCredentials(ctx, endpoint, bearer, creds.AccessKeyID, creds.SecretAccessKey, creds.SessionToken, creds.Expiration); err != nil {
			s.surfaceRefreshFailure(selection, fmt.Errorf("inject host credentials into runtime: %w", err), &notifiedFailure)
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

func (s *Service) waitForMCPReady(ctx context.Context, port int) error {
	if s.d.MCPReachable(port) {
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
			if s.d.MCPReachable(port) {
				return nil
			}
		}
	}
}

func (s *Service) surfaceRefreshFailure(selection uimodel.Selection, err error, notified *bool) {
	if notified != nil && *notified {
		return
	}
	if notified != nil {
		*notified = true
	}
	s.d.NotifyRefreshFailure(selection,
		fmt.Sprintf("Host credential refresh failed for %s/%s: %v", selection.Tenant, selection.Environment, err))
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

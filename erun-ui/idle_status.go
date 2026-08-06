package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

func (a *App) LoadIdleStatus(selection uiSelection) (uiIdleStatus, error) {
	selection = normalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" {
		return uiIdleStatus{}, fmt.Errorf("tenant and environment are required")
	}
	result, err := eruncommon.ResolveOpen(a.deps.store, eruncommon.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return uiIdleStatus{}, err
	}
	mcpPort := eruncommon.MCPPortForResult(result)
	if !a.deps.canConnectLocalPort(mcpPort) {
		status, err := a.loadLocalIdleStatus(result)
		if err == nil {
			a.maybeStopIdleCloudEnvironment(result, status.status)
		}
		return status.ui, err
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	status, err := a.deps.loadIdleStatus(ctx, mcpEndpointForOpenResult(result), a.mcpBearer(result.Tenant, result.EnvConfig.Name))
	if err != nil {
		status, err := a.loadLocalIdleStatus(result)
		if err == nil {
			a.maybeStopIdleCloudEnvironment(result, status.status)
		}
		return status.ui, err
	}
	merged := a.mergeLocalIdleActivity(result, status)
	a.maybeStopIdleCloudEnvironment(result, merged)
	return a.idleStatusToUI(result, merged), nil
}

type resolvedUIIdleStatus struct {
	ui     uiIdleStatus
	status eruncommon.EnvironmentIdleStatus
}

func (a *App) loadLocalIdleStatus(result eruncommon.OpenResult) (resolvedUIIdleStatus, error) {
	status, err := eruncommon.ResolveStoredEnvironmentIdleStatus(a.deps.store, result.Tenant, result.Environment, time.Now())
	if err != nil {
		return resolvedUIIdleStatus{}, err
	}
	return resolvedUIIdleStatus{ui: a.idleStatusToUI(result, status), status: status}, nil
}

func (a *App) idleStatusToUI(result eruncommon.OpenResult, status eruncommon.EnvironmentIdleStatus) uiIdleStatus {
	ui := idleStatusToUI(status)
	cloudContext, ok, err := a.linkedCloudContext(result.EnvConfig)
	if err != nil || !ok {
		return ui
	}
	ui.CloudContextName = strings.TrimSpace(cloudContext.Name)
	ui.CloudContextStatus = strings.TrimSpace(cloudContext.Status)
	ui.CloudContextLabel = cloudContextDisplayName(cloudContext)
	// idle-stop.log on the pod persists across pod and host restarts. If the
	// context is observably running again, the recorded error is from a
	// previous lifetime and should not be advertised next to a healthy env.
	if ui.CloudContextStatus == eruncommon.CloudContextStatusRunning {
		ui.StopError = ""
	}
	return ui
}

func idleStatusToUI(status eruncommon.EnvironmentIdleStatus) uiIdleStatus {
	markers := make([]uiIdleMarker, 0, len(status.Markers))
	for _, marker := range status.Markers {
		markers = append(markers, uiIdleMarker{
			Name:             strings.TrimSpace(marker.Name),
			Idle:             marker.Idle,
			Reason:           strings.TrimSpace(marker.Reason),
			SecondsRemaining: marker.SecondsRemaining,
			Clients:          idleMarkerClientsToUI(marker.Clients),
		})
	}
	return uiIdleStatus{
		TimeoutSeconds:      int64(status.Policy.Timeout / time.Second),
		SecondsUntilStop:    activitySecondsUntilIdle(status),
		StopEligible:        status.StopEligible,
		OutsideWorkingHours: status.OutsideWorkingHours,
		ManagedCloud:        status.ManagedCloud,
		StopBlockedReason:   strings.TrimSpace(status.StopBlockedReason),
		StopError:           strings.TrimSpace(status.StopError),
		Markers:             markers,
		// The desktop only observes the pending-stop fields, never computing
		// them, so the pill renders the same state whichever client armed the stop.
		StopPendingSince:       strings.TrimSpace(status.StopPendingSince),
		SecondsUntilForcedStop: status.SecondsUntilForcedStop,
		GracePeriodSeconds:     status.GracePeriodSeconds,
	}
}

func idleMarkerClientsToUI(clients []eruncommon.EnvironmentIdleMarkerClient) []uiIdleMarkerClient {
	if len(clients) == 0 {
		return nil
	}
	out := make([]uiIdleMarkerClient, 0, len(clients))
	for _, client := range clients {
		out = append(out, uiIdleMarkerClient{
			Address:    strings.TrimSpace(client.Address),
			Bytes:      client.Bytes,
			SecondsAgo: client.SecondsAgo,
		})
	}
	return out
}

func (a *App) mergeLocalIdleActivity(result eruncommon.OpenResult, status eruncommon.EnvironmentIdleStatus) eruncommon.EnvironmentIdleStatus {
	local, err := eruncommon.ResolveStoredEnvironmentIdleStatus(a.deps.store, result.Tenant, result.Environment, time.Now())
	if err != nil {
		return status
	}
	if len(status.Activity) > 0 {
		remoteWithLocalPolicy, err := eruncommon.ResolveEnvironmentIdleStatus(result.EnvConfig.Idle, status.Activity, status.Leases, time.Now())
		if err == nil {
			remoteWithLocalPolicy.ManagedCloud = status.ManagedCloud
			remoteWithLocalPolicy.StopBlockedReason = status.StopBlockedReason
			remoteWithLocalPolicy.StopError = status.StopError
			status = remoteWithLocalPolicy
		}
	}
	return mergeNewerActivityMarkers(status, local)
}

func mergeNewerActivityMarkers(status, local eruncommon.EnvironmentIdleStatus) eruncommon.EnvironmentIdleStatus {
	status.ManagedCloud = local.ManagedCloud
	status.StopBlockedReason = local.StopBlockedReason
	for _, localMarker := range local.Markers {
		if localMarker.Name == "working-hours" || localMarker.LastActivity.IsZero() {
			continue
		}
		found := false
		for index, marker := range status.Markers {
			if marker.Name != localMarker.Name {
				continue
			}
			found = true
			if localMarker.LastActivity.After(marker.LastActivity) {
				status.Markers[index] = localMarker
				break
			}
		}
		if !found {
			status.Markers = append(status.Markers, localMarker)
		}
	}
	return recomputeStopEligible(status)
}

func recomputeStopEligible(status eruncommon.EnvironmentIdleStatus) eruncommon.EnvironmentIdleStatus {
	if !status.ManagedCloud {
		status.StopEligible = false
		if status.StopBlockedReason == "" {
			status.StopBlockedReason = "environment is not cloud-managed"
		}
		return status
	}
	if status.OutsideWorkingHours {
		status.StopEligible = true
		status.StopBlockedReason = ""
		return status
	}
	for _, marker := range status.Markers {
		if marker.Name == "working-hours" {
			continue
		}
		if !marker.Idle {
			status.StopEligible = false
			status.StopBlockedReason = uiStopBlockedReason(status.Markers)
			return status
		}
	}
	status.StopEligible = true
	status.StopBlockedReason = ""
	return status
}

func uiStopBlockedReason(markers []eruncommon.EnvironmentIdleMarker) string {
	for _, marker := range markers {
		if marker.Name == "working-hours" || marker.Idle {
			continue
		}
		name := strings.TrimSpace(marker.Name)
		reason := strings.TrimSpace(marker.Reason)
		if name == "" {
			return reason
		}
		if reason == "" {
			return name
		}
		return name + ": " + reason
	}
	return ""
}

// The desktop no longer fires auto-stop — the in-pod idle monitor owns that.
// This only clears the post-fire latch when an env reappears as running, so a
// stale "already fired" flag never sticks forever for future start callers.
func (a *App) maybeStopIdleCloudEnvironment(result eruncommon.OpenResult, _ eruncommon.EnvironmentIdleStatus) {
	cloudContext, ok, err := a.linkedCloudContext(result.EnvConfig)
	if err != nil || !ok {
		return
	}
	if strings.TrimSpace(cloudContext.Status) == eruncommon.CloudContextStatusRunning {
		a.clearIdleStopsForCloudContext(cloudContext.Name)
	}
}

// CancelPendingIdleStop dismisses the grace-period auto-stop warning for the
// env. The dismissal is shared state, so the in-pod monitor and any other
// client observe it too.
func (a *App) CancelPendingIdleStop(selection uiSelection) error {
	selection = normalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" {
		return fmt.Errorf("tenant and environment are required")
	}
	result, err := eruncommon.ResolveOpen(a.deps.store, eruncommon.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return err
	}
	endpoint := mcpEndpointForOpenResult(result)
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	bearer := a.mcpBearer(selection.Tenant, selection.Environment)
	if err := cancelStopPendingViaMCP(ctx, endpoint, bearer, selection.Tenant, selection.Environment); err != nil {
		return err
	}
	a.emitAppNotification("info", fmt.Sprintf("Cancelled pending auto-stop for %s/%s.", selection.Tenant, selection.Environment))
	return nil
}

// recordManualStopForCloudContext records a manual-stop audit entry for every
// env linked to the cloud context, so the History tab explains operator-clicked
// stops alongside the monitor's auto-stops.
//
// Best-effort by design: the stop has already succeeded by the time we get here,
// so a failed audit write must not make the manual stop look failed.
func (a *App) recordManualStopForCloudContext(ctx context.Context, cloudContextName string) {
	selections := a.selectionsForCloudContext(cloudContextName)
	for _, selection := range selections {
		result, err := eruncommon.ResolveOpen(a.deps.store, eruncommon.OpenParams{
			Tenant:      selection.Tenant,
			Environment: selection.Environment,
		})
		if err != nil {
			a.emitAppNotification("warning", fmt.Sprintf("Could not record manual stop for %s/%s: %s", selection.Tenant, selection.Environment, err.Error()))
			continue
		}
		mcpPort := eruncommon.MCPPortForResult(result)
		if !a.deps.canConnectLocalPort(mcpPort) {
			// Best-effort audit: no port-forward to reach the tool, and the
			// stop already succeeded, so skip silently.
			continue
		}
		endpoint := mcpEndpointForOpenResult(result)
		bearer := a.mcpBearer(selection.Tenant, selection.Environment)
		if err := recordManualStopViaMCP(ctx, endpoint, bearer, selection.Tenant, selection.Environment, "Manual stop via desktop", cloudContextName); err != nil {
			a.emitAppNotification("warning", fmt.Sprintf("Could not record manual stop for %s/%s: %s", selection.Tenant, selection.Environment, err.Error()))
		}
	}
}

// LoadStopHistory returns the env's stop audit records newest first, reading the
// canonical history the in-pod monitor records rather than any local copy.
func (a *App) LoadStopHistory(selection uiSelection) ([]uiLastStopEvent, error) {
	selection = normalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" {
		return nil, fmt.Errorf("tenant and environment are required")
	}
	result, err := eruncommon.ResolveOpen(a.deps.store, eruncommon.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return nil, err
	}
	endpoint := mcpEndpointForOpenResult(result)
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	bearer := a.mcpBearer(selection.Tenant, selection.Environment)
	entries, err := loadStopHistoryViaMCP(ctx, endpoint, bearer, selection.Tenant, selection.Environment)
	if err != nil {
		return nil, err
	}
	out := make([]uiLastStopEvent, 0, len(entries))
	for _, entry := range entries {
		ui := uiLastStopEvent{
			StoppedAt:        entry.StoppedAt.UTC().Format(time.RFC3339),
			GraceSeconds:     entry.GraceSeconds,
			Source:           strings.TrimSpace(entry.Source),
			Reason:           strings.TrimSpace(entry.Reason),
			CloudContextName: strings.TrimSpace(entry.CloudContextName),
		}
		if !entry.ArmedAt.IsZero() {
			ui.ArmedAt = entry.ArmedAt.UTC().Format(time.RFC3339)
		}
		if !idlePolicyIsZero(entry.Policy) {
			ui.Policy = &uiIdlePolicy{
				TimeoutSeconds:   int64(entry.Policy.Timeout / time.Second),
				WorkingHours:     strings.TrimSpace(entry.Policy.WorkingHours),
				Timezone:         strings.TrimSpace(entry.Policy.Timezone),
				IdleTrafficBytes: entry.Policy.IdleTrafficBytes,
			}
		}
		for _, marker := range entry.Markers {
			ui.Markers = append(ui.Markers, uiLastStopMarker{
				Name:           strings.TrimSpace(marker.Name),
				Idle:           marker.Idle,
				Reason:         strings.TrimSpace(marker.Reason),
				SecondsIdleFor: marker.SecondsIdleFor,
			})
		}
		out = append(out, ui)
	}
	return out, nil
}

// idlePolicyIsZero reports whether the entry's policy snapshot is
// unset — older history rows pre-date the snapshot, so we render
// nothing rather than a misleading "Timeout: 0s" line.
func idlePolicyIsZero(p eruncommon.EnvironmentIdlePolicy) bool {
	return p.Timeout == 0 && strings.TrimSpace(p.WorkingHours) == "" && strings.TrimSpace(p.Timezone) == "" && p.IdleTrafficBytes == 0
}

// clearIdleStopsForCloudContext returns true when a latched auto-stop flag was
// actually removed, letting callers distinguish an externally-restarted context
// from one that was already in sync.
func (a *App) clearIdleStopsForCloudContext(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	cleared := a.selectionKeysForCloudContext(name)
	if len(cleared) == 0 {
		return false
	}
	removed := false
	a.mu.Lock()
	for _, key := range cleared {
		if _, ok := a.idleStops[key]; ok {
			delete(a.idleStops, key)
			removed = true
		}
	}
	a.mu.Unlock()
	return removed
}

func (a *App) selectionKeysForCloudContext(name string) []string {
	tenants, err := a.deps.store.ListTenantConfigs()
	if err != nil {
		return nil
	}
	cleared := make([]string, 0)
	for _, tenant := range tenants {
		envs, err := a.deps.store.ListEnvConfigs(tenant.Name)
		if err != nil {
			continue
		}
		for _, env := range envs {
			cloudContext, ok, err := a.linkedCloudContext(env)
			if err != nil || !ok || strings.TrimSpace(cloudContext.Name) != name {
				continue
			}
			cleared = append(cleared, selectionKey(uiSelection{Tenant: tenant.Name, Environment: env.Name}))
		}
	}
	return cleared
}

func activitySecondsUntilIdle(status eruncommon.EnvironmentIdleStatus) int64 {
	var seconds int64
	for _, marker := range status.Markers {
		if marker.Name == "working-hours" || marker.Idle {
			continue
		}
		if marker.SecondsRemaining > seconds {
			seconds = marker.SecondsRemaining
		}
	}
	return seconds
}

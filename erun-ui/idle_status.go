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
	status, err := a.deps.loadIdleStatus(ctx, mcpEndpointForOpenResult(result))
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
		// Pending-stop fields are owned by the shared
		// `MaybeArmOrFireIdleStop` decision function, written into
		// the env's stop-pending.json on the pod's shared PVC by the
		// in-pod monitor, and surfaced through
		// `ResolveStoredEnvironmentIdleStatus`. The desktop only
		// observes them — no local map, no clock injection — so the
		// pill renders the same state whether the in-pod monitor or
		// any other client armed it.
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
		remoteWithLocalPolicy, err := eruncommon.ResolveEnvironmentIdleStatus(result.EnvConfig.Idle, status.Activity, time.Now())
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

// maybeStopIdleCloudEnvironment used to fire the auto-stop from the
// desktop side. That responsibility now lives entirely in the in-pod
// idle monitor (erun-devops/docker/erun-devops/entrypoint.sh ->
// `erun activity stop-ready` -> `MaybeArmOrFireIdleStop` in
// erun-common), so the desktop only needs to clear the post-fire
// `idleStops` latch when an env reappears as running — that keeps
// `StartCloudContext` callers from latching the "we already fired"
// flag forever and matches the historical behavior the existing
// tests pin down. All grace-period state lives on the pod's shared
// PVC and is surfaced through the MCP `idle` tool response, so the
// desktop is now a pure observer.
func (a *App) maybeStopIdleCloudEnvironment(result eruncommon.OpenResult, _ eruncommon.EnvironmentIdleStatus) {
	cloudContext, ok, err := a.linkedCloudContext(result.EnvConfig)
	if err != nil || !ok {
		return
	}
	if strings.TrimSpace(cloudContext.Status) == eruncommon.CloudContextStatusRunning {
		a.clearIdleStopsForCloudContext(cloudContext.Name)
	}
}

// CancelPendingIdleStop dismisses the grace-period warning for the
// supplied env by calling the in-pod MCP `idle_stop_cancel` tool.
// Cleared state lives on the pod's PVC so any subsequent client
// (the in-pod monitor's next tick, another desktop, the History
// tab) sees the dismissal. Returns an error when MCP is unreachable
// (e.g., the env is mid-stop or the port-forward is broken); the UI
// surfaces the error verbatim.
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
	if err := cancelStopPendingViaMCP(ctx, endpoint, selection.Tenant, selection.Environment); err != nil {
		return err
	}
	a.emitAppNotification("info", fmt.Sprintf("Cancelled pending auto-stop for %s/%s.", selection.Tenant, selection.Environment))
	return nil
}

// LoadStopHistory returns the env's last N auto-stop audit records,
// newest first. Reads through the in-pod MCP `idle_stop_history`
// tool so the canonical history (the one the in-pod monitor wrote
// after each `stop_cloud_host` call) is what the desktop renders.
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
	entries, err := loadStopHistoryViaMCP(ctx, endpoint, selection.Tenant, selection.Environment)
	if err != nil {
		return nil, err
	}
	out := make([]uiLastStopEvent, 0, len(entries))
	for _, entry := range entries {
		ui := uiLastStopEvent{
			StoppedAt:        entry.StoppedAt.UTC().Format(time.RFC3339),
			GraceSeconds:     entry.GraceSeconds,
			Reason:           strings.TrimSpace(entry.Reason),
			CloudContextName: strings.TrimSpace(entry.CloudContextName),
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

// clearIdleStopsForCloudContext removes any latched auto-stop flag
// for environments linked to the supplied cloud context name. Returns
// true when at least one flag was actually removed, so callers can
// distinguish "we just observed an externally-restarted context" from
// "nothing to clear, already in sync."
func (a *App) clearIdleStopsForCloudContext(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	tenants, err := a.deps.store.ListTenantConfigs()
	if err != nil {
		return false
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

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
	}
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
		for index, marker := range status.Markers {
			if marker.Name == localMarker.Name && localMarker.LastActivity.After(marker.LastActivity) {
				status.Markers[index] = localMarker
			}
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

func (a *App) maybeStopIdleCloudEnvironment(result eruncommon.OpenResult, status eruncommon.EnvironmentIdleStatus) {
	if !status.ManagedCloud || !status.StopEligible {
		return
	}
	cloudContext, ok, err := a.linkedCloudContext(result.EnvConfig)
	if err != nil || !ok {
		return
	}
	key := selectionKey(uiSelection{Tenant: result.Tenant, Environment: result.Environment})
	busyKey := environmentBusyKey(uiSelection{Tenant: result.Tenant, Environment: result.Environment})
	a.mu.Lock()
	if a.busyEnvs[busyKey] > 0 {
		a.mu.Unlock()
		return
	}
	if _, exists := a.idleStops[key]; exists {
		a.mu.Unlock()
		return
	}
	a.idleStops[key] = struct{}{}
	a.mu.Unlock()

	a.emitAppStatus(fmt.Sprintf("Stopping idle cloud context %s...", cloudContextDisplayName(cloudContext)), true)
	go func() {
		ctx := a.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		if _, err := a.deps.stopCloudContext(ctx, cloudContext.Name); err != nil {
			a.mu.Lock()
			delete(a.idleStops, key)
			a.mu.Unlock()
			a.emitAppStatus(fmt.Sprintf("Failed to stop idle cloud context %s: %s", cloudContextDisplayName(cloudContext), err.Error()), false)
			return
		}
		a.emitAppStatus(fmt.Sprintf("Stopped idle cloud context %s.", cloudContextDisplayName(cloudContext)), false)
	}()
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

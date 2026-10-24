package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	jobspkg "github.com/sophium/erun/erun-common/command/jobs"
	"github.com/sophium/erun/erun-common/infra/activity"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/primitive/open/resolve"
)

func (a *App) LoadIdleStatus(selection uiSelection) (uiIdleStatus, error) {
	selection = normalizeSelection(selection)
	if err := errMissingTenantOrEnvironment("load idle status", selection.Tenant, selection.Environment); err != nil {
		return uiIdleStatus{}, err
	}
	result, err := resolve.ResolveOpen(a.deps.store, model.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return uiIdleStatus{}, err
	}
	mcpPort := config.MCPPortForResult(result)
	if !a.deps.canReachMCPEndpoint(mcpPort) {
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
	return a.idleStatusToUI(result, merged, true), nil
}

type resolvedUIIdleStatus struct {
	ui     uiIdleStatus
	status model.EnvironmentIdleStatus
}

// loadLocalIdleStatus is the host-side fallback when the pod cannot be asked;
// FromPod is always false and the reading may be stale.
func (a *App) loadLocalIdleStatus(result config.OpenResult) (resolvedUIIdleStatus, error) {
	status, err := activity.ResolveStoredEnvironmentIdleStatus(a.deps.store, result.Tenant, result.Environment, time.Now())
	if err != nil {
		return resolvedUIIdleStatus{}, err
	}
	return resolvedUIIdleStatus{ui: a.idleStatusToUI(result, status, false), status: status}, nil
}

func (a *App) idleStatusToUI(result config.OpenResult, status model.EnvironmentIdleStatus, fromPod bool) uiIdleStatus {
	ui := idleStatusToUI(status)
	ui.FromPod = fromPod
	a.verifyLeaseJobIDs(result.Tenant, result.Environment, ui.Leases)
	cloudContext, ok, err := a.linkedCloudContext(result.EnvConfig)
	if err != nil || !ok {
		return ui
	}
	ui.CloudContextName = strings.TrimSpace(cloudContext.Name)
	ui.CloudContextStatus = strings.TrimSpace(cloudContext.Status)
	ui.CloudContextLabel = cloudContextDisplayName(cloudContext)
	// idle-stop.log outlives restarts; hide its error once the context runs again.
	if ui.CloudContextStatus == cloud.CloudContextStatusRunning {
		ui.StopError = ""
	}
	return ui
}

func idleStatusToUI(status model.EnvironmentIdleStatus) uiIdleStatus {
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
		Leases:              environmentLeasesToUI(status.Leases, time.Now()),
		// Observed, never computed here, so every client renders the same state.
		StopPendingSince:       strings.TrimSpace(status.StopPendingSince),
		SecondsUntilForcedStop: status.SecondsUntilForcedStop,
		GracePeriodSeconds:     status.GracePeriodSeconds,
	}
}

// The JobID is only an id-shape match; verifyLeaseJobIDs confirms it.
func environmentLeasesToUI(leases []model.EnvironmentActivityLease, now time.Time) []uiEnvironmentLease {
	if len(leases) == 0 {
		return nil
	}
	out := make([]uiEnvironmentLease, 0, len(leases))
	for _, lease := range leases {
		entry := uiEnvironmentLease{Name: strings.TrimSpace(lease.Name)}
		if jobID, ok := jobspkg.EnvironmentJobIDFromLeaseID(lease.ID); ok {
			entry.JobID = jobID
		}
		if !lease.StartedAt.IsZero() && now.After(lease.StartedAt) {
			entry.SecondsHeld = int64(now.Sub(lease.StartedAt) / time.Second)
		}
		out = append(out, entry)
	}
	return out
}

// verifyLeaseJobIDs clears a candidate JobID no job record backs, so a
// hand-taken "job-*" lease never offers "View jobs". Fails closed on error.
func (a *App) verifyLeaseJobIDs(tenant, environment string, leases []uiEnvironmentLease) {
	var candidates []int
	for i := range leases {
		if leases[i].JobID != "" {
			candidates = append(candidates, i)
		}
	}
	if len(candidates) == 0 {
		return
	}
	jobs, err := a.LoadEnvironmentJobs(tenant, environment)
	if err != nil {
		for _, i := range candidates {
			leases[i].JobID = ""
		}
		return
	}
	known := make(map[string]bool, len(jobs))
	for _, job := range jobs {
		known[job.ID] = true
	}
	for _, i := range candidates {
		if !known[leases[i].JobID] {
			leases[i].JobID = ""
		}
	}
}

func idleMarkerClientsToUI(clients []model.EnvironmentIdleMarkerClient) []uiIdleMarkerClient {
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

func (a *App) mergeLocalIdleActivity(result config.OpenResult, status model.EnvironmentIdleStatus) model.EnvironmentIdleStatus {
	local, err := activity.ResolveStoredEnvironmentIdleStatus(a.deps.store, result.Tenant, result.Environment, time.Now())
	if err != nil {
		return status
	}
	if len(status.Activity) > 0 {
		remoteWithLocalPolicy, err := activity.ResolveEnvironmentIdleStatus(result.EnvConfig.Idle, status.Activity, status.Leases, time.Now())
		if err == nil {
			remoteWithLocalPolicy.ManagedCloud = status.ManagedCloud
			remoteWithLocalPolicy.StopBlockedReason = status.StopBlockedReason
			remoteWithLocalPolicy.StopError = status.StopError
			status = remoteWithLocalPolicy
		}
	}
	return mergeNewerActivityMarkers(status, local)
}

func mergeNewerActivityMarkers(status, local model.EnvironmentIdleStatus) model.EnvironmentIdleStatus {
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

func recomputeStopEligible(status model.EnvironmentIdleStatus) model.EnvironmentIdleStatus {
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

func uiStopBlockedReason(markers []model.EnvironmentIdleMarker) string {
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

// The in-pod monitor owns auto-stop; this only clears the post-fire latch when
// an env runs again.
func (a *App) maybeStopIdleCloudEnvironment(result config.OpenResult, _ model.EnvironmentIdleStatus) {
	cloudContext, ok, err := a.linkedCloudContext(result.EnvConfig)
	if err != nil || !ok {
		return
	}
	if strings.TrimSpace(cloudContext.Status) == cloud.CloudContextStatusRunning {
		a.clearIdleStopsForCloudContext(cloudContext.Name)
	}
}

// CancelPendingIdleStop dismisses the env's pending auto-stop for every client.
func (a *App) CancelPendingIdleStop(selection uiSelection) error {
	selection = normalizeSelection(selection)
	if err := errMissingTenantOrEnvironment("cancel pending idle stop", selection.Tenant, selection.Environment); err != nil {
		return err
	}
	result, err := resolve.ResolveOpen(a.deps.store, model.OpenParams{
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

// Best-effort: the stop already succeeded, so a failed audit write must not
// make it look failed.
func (a *App) recordManualStopForCloudContext(ctx context.Context, cloudContextName string) {
	selections := a.selectionsForCloudContext(cloudContextName)
	for _, selection := range selections {
		result, err := resolve.ResolveOpen(a.deps.store, model.OpenParams{
			Tenant:      selection.Tenant,
			Environment: selection.Environment,
		})
		if err != nil {
			a.emitAppNotification("warning", fmt.Sprintf("Could not record manual stop for %s/%s: %s", selection.Tenant, selection.Environment, err.Error()))
			continue
		}
		mcpPort := config.MCPPortForResult(result)
		if !a.deps.canReachMCPEndpoint(mcpPort) {
			continue
		}
		endpoint := mcpEndpointForOpenResult(result)
		bearer := a.mcpBearer(selection.Tenant, selection.Environment)
		if err := recordManualStopViaMCP(ctx, endpoint, bearer, selection.Tenant, selection.Environment, "Manual stop via desktop", cloudContextName); err != nil {
			a.emitAppNotification("warning", fmt.Sprintf("Could not record manual stop for %s/%s: %s", selection.Tenant, selection.Environment, err.Error()))
		}
	}
}

// LoadStopHistory returns the in-pod stop history, newest first.
func (a *App) LoadStopHistory(selection uiSelection) ([]uiLastStopEvent, error) {
	selection = normalizeSelection(selection)
	if err := errMissingTenantOrEnvironment("load stop history", selection.Tenant, selection.Environment); err != nil {
		return nil, err
	}
	result, err := resolve.ResolveOpen(a.deps.store, model.OpenParams{
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

// Older rows pre-date the policy snapshot; render nothing, not "Timeout: 0s".
func idlePolicyIsZero(p model.EnvironmentIdlePolicy) bool {
	return p.Timeout == 0 && strings.TrimSpace(p.WorkingHours) == "" && strings.TrimSpace(p.Timezone) == "" && p.IdleTrafficBytes == 0
}

// Returns true when a latched flag was removed, so callers can tell an
// externally restarted context from one already in sync.
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

func activitySecondsUntilIdle(status model.EnvironmentIdleStatus) int64 {
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

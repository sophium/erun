package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// idleStopPendingEntry captures the moment an env first became
// StopEligible plus a snapshot of the markers and the linked cloud
// context. The snapshot is the source of truth for the last-stop.json
// audit record when the grace window expires; freezing it at
// eligibility time keeps the record honest even if the next poll's
// markers shift mid-grace.
type idleStopPendingEntry struct {
	since            time.Time
	graceSeconds     int64
	cloudContextName string
	cloudContextLabel string
	tenant           string
	environment      string
	markers          []eruncommon.EnvironmentIdleMarker
	reasonSummary    string
}

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
		return a.idleStatusUIWithPending(result, status.ui), err
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
		return a.idleStatusUIWithPending(result, status.ui), err
	}
	merged := a.mergeLocalIdleActivity(result, status)
	a.maybeStopIdleCloudEnvironment(result, merged)
	return a.idleStatusUIWithPending(result, a.idleStatusToUI(result, merged)), nil
}

// idleStatusUIWithPending overlays the pending auto-stop window onto a
// freshly-resolved uiIdleStatus. The pending state lives in
// App.idleStopPending (set/cleared by maybeStopIdleCloudEnvironment),
// and surfacing it on every LoadIdleStatus tick lets the frontend
// render a persistent countdown banner instead of relying on a
// transient toast.
func (a *App) idleStatusUIWithPending(result eruncommon.OpenResult, ui uiIdleStatus) uiIdleStatus {
	key := selectionKey(uiSelection{Tenant: result.Tenant, Environment: result.Environment})
	a.mu.Lock()
	entry, ok := a.idleStopPending[key]
	a.mu.Unlock()
	if !ok {
		return ui
	}
	elapsed := int64(a.nowOrNow().Sub(entry.since).Seconds())
	remaining := entry.graceSeconds - elapsed
	if remaining < 0 {
		remaining = 0
	}
	ui.StopPendingSince = entry.since.UTC().Format(time.RFC3339)
	ui.SecondsUntilForcedStop = remaining
	ui.GracePeriodSeconds = entry.graceSeconds
	return ui
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

func (a *App) maybeStopIdleCloudEnvironment(result eruncommon.OpenResult, status eruncommon.EnvironmentIdleStatus) {
	cloudContext, ok, err := a.linkedCloudContext(result.EnvConfig)
	if err != nil || !ok {
		return
	}
	// Reconcile stale idleStops markers with the current cloud context.
	// If the context is running again — manually restarted via the
	// titlebar Play button, restarted by `erun open` preflight in a
	// terminal we don't track, or restarted by anything outside the
	// desktop — our prior auto-stop has been undone and a fresh stop
	// should be allowed to fire when markers next expire. Without this
	// reconcile, the idleStops flag latches forever after the first stop
	// and the env can never auto-stop a second time.
	//
	// When clearing actually removes a stale flag, return early: the
	// idle markers loaded for this call were computed before the
	// restart was observed and may indicate "everything has been idle
	// for hours," which would otherwise refire a stop on a context the
	// user just brought back up. The next idle poll re-runs in ~1s
	// with fresh markers; let that one decide whether to stop.
	if strings.TrimSpace(cloudContext.Status) == eruncommon.CloudContextStatusRunning {
		if a.clearIdleStopsForCloudContext(cloudContext.Name) {
			return
		}
	}
	key := selectionKey(uiSelection{Tenant: result.Tenant, Environment: result.Environment})
	if !status.ManagedCloud || !status.StopEligible {
		// Eligibility lapsed (activity resumed, working-hours started,
		// or the env became local) — clear any pending warning so the
		// next eligibility re-arms the grace period from scratch.
		a.clearIdleStopPending(key)
		return
	}
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
	now := a.nowOrNow()
	graceSeconds := idleStopGraceSeconds(status, result.EnvConfig)
	pending, hasPending := a.idleStopPending[key]
	if !hasPending {
		pending = idleStopPendingEntry{
			since:             now,
			graceSeconds:      graceSeconds,
			cloudContextName:  cloudContext.Name,
			cloudContextLabel: cloudContextDisplayName(cloudContext),
			tenant:            result.Tenant,
			environment:       result.Environment,
			markers:           cloneIdleMarkers(status.Markers),
			reasonSummary:     idleStopReasonSummary(status),
		}
		a.idleStopPending[key] = pending
		a.mu.Unlock()
		// Persistent banner data is exposed through LoadIdleStatus; the
		// one-shot toast names the duration so a user looking away
		// from the titlebar still has a chance to register the warning.
		a.emitAppNotification(
			"warning",
			fmt.Sprintf(
				"Auto-stop pending: %s will stop in %s unless you act.",
				cloudContextDisplayName(cloudContext),
				formatGraceDuration(graceSeconds),
			),
		)
		return
	}
	if now.Sub(pending.since) < time.Duration(pending.graceSeconds)*time.Second {
		a.mu.Unlock()
		return
	}
	delete(a.idleStopPending, key)
	a.idleStops[key] = struct{}{}
	snapshot := pending
	a.mu.Unlock()

	a.emitAppStatus(fmt.Sprintf("Stopping idle cloud context %s...", snapshot.cloudContextLabel), true)
	go a.fireIdleStop(snapshot, key)
}

// fireIdleStop actually requests the cloud-context stop after the
// grace window has elapsed, persists the audit record, and emits the
// success/failure notification. Split out of
// maybeStopIdleCloudEnvironment so the locked-section above stays
// short and the goroutine body can read the pending snapshot
// directly.
func (a *App) fireIdleStop(entry idleStopPendingEntry, key string) {
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	stopped, err := a.deps.stopCloudContext(ctx, entry.cloudContextName)
	if err != nil {
		a.mu.Lock()
		delete(a.idleStops, key)
		a.mu.Unlock()
		a.emitAppStatus(fmt.Sprintf("Failed to stop idle cloud context %s: %s", entry.cloudContextLabel, err.Error()), false)
		return
	}
	a.setCloudContextStatusInCache(stopped.Name, stopped.Status)
	if writeErr := a.writeLastStopEvent(entry); writeErr != nil {
		// last-stop.json is best-effort audit metadata; surface the
		// failure as a log line via the status banner but keep the
		// success notification — the user's actionable signal is that
		// the env did stop.
		a.emitAppStatus(fmt.Sprintf("Stopped idle cloud context %s (audit log write failed: %s).", entry.cloudContextLabel, writeErr.Error()), false)
	}
	a.emitAppNotification(
		"info",
		fmt.Sprintf(
			"Stopped idle cloud context %s — %s.",
			entry.cloudContextLabel,
			entry.reasonSummary,
		),
	)
}

// CancelPendingIdleStop dismisses the grace-period warning for the
// named cloud context without changing AWS state. The next idle poll
// re-evaluates eligibility from scratch — if the env is still
// eligible the warning re-arms, but the user has gotten another
// `gracePeriodSeconds` window to resume real activity.
func (a *App) CancelPendingIdleStop(cloudContextName string) error {
	name := strings.TrimSpace(cloudContextName)
	if name == "" {
		return fmt.Errorf("cloud context name is required")
	}
	a.mu.Lock()
	cleared := false
	for key, entry := range a.idleStopPending {
		if entry.cloudContextName == name {
			delete(a.idleStopPending, key)
			cleared = true
		}
	}
	a.mu.Unlock()
	if !cleared {
		return fmt.Errorf("no pending auto-stop for cloud context %q", name)
	}
	a.emitAppNotification("info", fmt.Sprintf("Cancelled pending auto-stop for %s.", name))
	return nil
}

// clearIdleStopPending removes any pending warning entry for the
// supplied env key, with the same lock semantics as the rest of the
// idleStops accounting.
func (a *App) clearIdleStopPending(key string) {
	a.mu.Lock()
	delete(a.idleStopPending, key)
	a.mu.Unlock()
}

// idleStopGraceSeconds picks the grace-period length for an env. The
// user spec (#410 follow-up) is "at least the idle timeout"; we use
// the env's resolved idle timeout verbatim so a 10-minute idle
// timeout gives a 10-minute warning window. Falls back to a 5-minute
// floor when the env config does not configure a timeout (which
// should not happen in practice but keeps the contract safe).
func idleStopGraceSeconds(status eruncommon.EnvironmentIdleStatus, _ eruncommon.EnvConfig) int64 {
	timeout := int64(status.Policy.Timeout / time.Second)
	if timeout <= 0 {
		return 5 * 60
	}
	return timeout
}

// idleStopReasonSummary builds a single-line summary of why the env
// is being stopped, used in toasts and the last-stop record. Lists
// the markers that were idle and how long they had been quiet.
func idleStopReasonSummary(status eruncommon.EnvironmentIdleStatus) string {
	var parts []string
	for _, marker := range status.Markers {
		if marker.Name == "working-hours" {
			continue
		}
		if !marker.Idle {
			continue
		}
		name := strings.TrimSpace(marker.Name)
		if name == "" {
			continue
		}
		parts = append(parts, name)
	}
	if len(parts) == 0 {
		if status.OutsideWorkingHours {
			return "outside working hours"
		}
		return "idle policy met"
	}
	return "idle: " + strings.Join(parts, ", ")
}

// formatGraceDuration renders seconds as "Nm Ss" / "Ns", matching the
// pattern used elsewhere in the titlebar tooltip.
func formatGraceDuration(seconds int64) string {
	if seconds <= 0 {
		return "0s"
	}
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	minutes := seconds / 60
	rem := seconds % 60
	if rem == 0 {
		return fmt.Sprintf("%dm", minutes)
	}
	return fmt.Sprintf("%dm %ds", minutes, rem)
}

// cloneIdleMarkers copies the marker slice so a later mutation by the
// next idle poll cannot mutate the snapshot used for the audit
// record.
func cloneIdleMarkers(markers []eruncommon.EnvironmentIdleMarker) []eruncommon.EnvironmentIdleMarker {
	if len(markers) == 0 {
		return nil
	}
	out := make([]eruncommon.EnvironmentIdleMarker, len(markers))
	copy(out, markers)
	return out
}

// writeLastStopEvent persists a per-env audit record describing why
// the auto-stop fired. The record is read back by
// LoadLastStopEvent and surfaced in the idle-status tooltip so the
// user can answer "why did my env stop?" without trawling logs.
func (a *App) writeLastStopEvent(entry idleStopPendingEntry) error {
	dir, err := lastStopDir(entry.tenant, entry.environment)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	now := a.nowOrNow().UTC()
	record := uiLastStopEvent{
		StoppedAt:        now.Format(time.RFC3339),
		GraceSeconds:     entry.graceSeconds,
		Reason:           entry.reasonSummary,
		CloudContextName: entry.cloudContextName,
	}
	for _, marker := range entry.markers {
		if marker.Name == "working-hours" {
			continue
		}
		record.Markers = append(record.Markers, uiLastStopMarker{
			Name:           strings.TrimSpace(marker.Name),
			Idle:           marker.Idle,
			Reason:         strings.TrimSpace(marker.Reason),
			SecondsIdleFor: secondsIdleFor(marker, now),
		})
	}
	body, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "last-stop.json")
	return os.WriteFile(path, body, 0o600)
}

// LoadLastStopEvent returns the most recent auto-stop audit record
// for the supplied env, or zero value when none has been recorded.
// Reads <userConfig>/erun/<tenant>/<env>/last-stop.json.
func (a *App) LoadLastStopEvent(selection uiSelection) (uiLastStopEvent, error) {
	selection = normalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" {
		return uiLastStopEvent{}, fmt.Errorf("tenant and environment are required")
	}
	dir, err := lastStopDir(selection.Tenant, selection.Environment)
	if err != nil {
		return uiLastStopEvent{}, err
	}
	body, err := os.ReadFile(filepath.Join(dir, "last-stop.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return uiLastStopEvent{}, nil
		}
		return uiLastStopEvent{}, err
	}
	var record uiLastStopEvent
	if err := json.Unmarshal(body, &record); err != nil {
		return uiLastStopEvent{}, err
	}
	return record, nil
}

// lastStopDir resolves the per-env directory used for the
// last-stop.json audit record. Anchored on os.UserConfigDir so the
// same path resolves on macOS, Linux, and Windows.
func lastStopDir(tenant, environment string) (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "erun", tenant, environment), nil
}

// secondsIdleFor reports how long a marker has been quiet by the time
// of `now`. SecondsRemaining is the marker's countdown to eligibility;
// once eligible (Idle=true) the countdown is at 0 and what matters is
// how long since LastActivity. Returns 0 when LastActivity is
// unrecorded.
func secondsIdleFor(marker eruncommon.EnvironmentIdleMarker, now time.Time) int64 {
	if marker.LastActivity.IsZero() {
		return 0
	}
	delta := int64(now.Sub(marker.LastActivity).Seconds())
	if delta < 0 {
		return 0
	}
	return delta
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

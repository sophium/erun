package idlestatus

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/activity/activitylease"
	"github.com/sophium/erun/erun-common/infra/activity/idlestop"
	"github.com/sophium/erun/erun-common/infra/activity/managedcloud"
	"github.com/sophium/erun/erun-common/infra/activity/records"
	"github.com/sophium/erun/erun-common/infra/activity/workinghours"
)

type EnvironmentIdleStore interface {
	configpkg.CloudReadStore
	LoadEnvConfig(tenant, environment string) (configpkg.EnvConfig, string, error)
}

func ResolveEnvironmentIdleStatus(config configpkg.EnvironmentIdleConfig, activity map[string]model.EnvironmentActivitySnapshot, leases []model.EnvironmentActivityLease, now time.Time) (model.EnvironmentIdleStatus, error) {
	policy, err := config.Resolve()
	if err != nil {
		return model.EnvironmentIdleStatus{}, err
	}
	if now.IsZero() {
		now = time.Now()
	}
	if activity == nil {
		activity = map[string]model.EnvironmentActivitySnapshot{}
	}

	outsideWorkingHours, secondsUntilWorkingHoursEnd, err := workinghours.WorkingHoursStatus(policy.WorkingHours, policy.Timezone, now)
	if err != nil {
		return model.EnvironmentIdleStatus{}, err
	}

	markers := environmentIdleMarkers(activity, leases, policy, now, outsideWorkingHours, secondsUntilWorkingHoursEnd)
	stopEligible, secondsUntilStop := environmentStopEligibility(markers, outsideWorkingHours)
	stopBlockedReason := environmentBlockedReason(markers, stopEligible)

	return model.EnvironmentIdleStatus{
		Policy:              policy,
		OutsideWorkingHours: outsideWorkingHours,
		StopEligible:        stopEligible,
		StopBlockedReason:   stopBlockedReason,
		SecondsUntilStop:    secondsUntilStop,
		Markers:             markers,
		Activity:            activity,
		Leases:              leases,
	}, nil
}

func environmentIdleMarkers(activity map[string]model.EnvironmentActivitySnapshot, leases []model.EnvironmentActivityLease, policy model.EnvironmentIdlePolicy, now time.Time, outsideWorkingHours bool, secondsUntilWorkingHoursEnd int64) []model.EnvironmentIdleMarker {
	markers := []model.EnvironmentIdleMarker{{
		Name:             "working-hours",
		Idle:             outsideWorkingHours,
		Reason:           workingHoursReason(outsideWorkingHours, policy.WorkingHours),
		SecondsRemaining: secondsUntilWorkingHoursEnd,
	}}
	for _, kind := range records.EnvironmentActivityKinds {
		snapshot := activity[kind]
		markers = append(markers, activityIdleMarker(kind, snapshot, policy, now))
	}
	return append(markers, leaseIdleMarker(leases, now))
}

func environmentStopEligibility(markers []model.EnvironmentIdleMarker, outsideWorkingHours bool) (bool, int64) {
	stopEligible := true
	secondsUntilStop := int64(0)
	for _, marker := range markers {
		if marker.Name == "working-hours" || marker.Idle {
			continue
		}
		// A lease blocks the stop even outside working hours: it is a job running now.
		if outsideWorkingHours && marker.Name != environmentActivityLeaseMarker {
			continue
		}
		stopEligible = false
		if marker.SecondsRemaining > secondsUntilStop {
			secondsUntilStop = marker.SecondsRemaining
		}
	}
	if stopEligible {
		return true, 0
	}
	return false, secondsUntilStop
}

func environmentBlockedReason(markers []model.EnvironmentIdleMarker, stopEligible bool) string {
	if stopEligible {
		return ""
	}
	return environmentStopBlockedReason(markers)
}

func ResolveStoredEnvironmentIdleStatus(store EnvironmentIdleStore, tenant, environment string, now time.Time) (model.EnvironmentIdleStatus, error) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if store == nil {
		return model.EnvironmentIdleStatus{}, fmt.Errorf("store is required")
	}
	if err := core.ErrMissingTenantOrEnvironment("resolve environment idle status", tenant, environment); err != nil {
		return model.EnvironmentIdleStatus{}, err
	}
	config, _, err := store.LoadEnvConfig(tenant, environment)
	if err != nil {
		return model.EnvironmentIdleStatus{}, err
	}
	activity, err := records.LoadEnvironmentActivity(tenant, environment)
	if err != nil {
		return model.EnvironmentIdleStatus{}, err
	}
	leases, err := activitylease.LoadEnvironmentActivityLeases(tenant, environment, now)
	if err != nil {
		return model.EnvironmentIdleStatus{}, err
	}
	status, err := ResolveEnvironmentIdleStatus(config.Idle, activity, leases, now)
	if err != nil {
		return model.EnvironmentIdleStatus{}, err
	}
	managedCloud, err := managedcloud.ManagedCloudEnvironment(store, config)
	if err != nil {
		return model.EnvironmentIdleStatus{}, err
	}
	status.ManagedCloud = managedCloud
	if !managedCloud {
		status.StopEligible = false
		status.StopBlockedReason = "environment is not cloud-managed"
	}
	status.StopError = loadEnvironmentIdleStopError(tenant, environment)
	status = overlayStopPending(status, tenant, environment, now)
	return status, nil
}

// overlayStopPending exposes the grace window to read-only consumers too.
func overlayStopPending(status model.EnvironmentIdleStatus, tenant, environment string, now time.Time) model.EnvironmentIdleStatus {
	if !status.ManagedCloud || !status.StopEligible {
		// Eligibility lapsed; the next MaybeArmOrFireIdleStop clears the stale entry.
		return status
	}
	pending, ok, err := idlestop.LoadEnvironmentStopPending(tenant, environment)
	if err != nil || !ok {
		return status
	}
	if now.IsZero() {
		now = time.Now()
	}
	elapsed := int64(now.Sub(pending.Since).Seconds())
	if elapsed < 0 {
		elapsed = 0
	}
	remaining := pending.GraceSeconds - elapsed
	if remaining < 0 {
		remaining = 0
	}
	status.StopPendingSince = pending.Since.UTC().Format(time.RFC3339)
	status.GracePeriodSeconds = pending.GraceSeconds
	status.SecondsUntilForcedStop = remaining
	return status
}

func loadEnvironmentIdleStopError(tenant, environment string) string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	path := environmentIdleStopLogPath(home, tenant, environment)
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return ""
		}
		legacy := filepath.Join(home, ".erun", "idle-stop.log")
		data, err = os.ReadFile(legacy)
		if err != nil {
			return ""
		}
	}
	value := strings.TrimSpace(string(data))
	const maxStopErrorLength = 4000
	if len(value) <= maxStopErrorLength {
		return value
	}
	return value[len(value)-maxStopErrorLength:]
}

// Keyed by tenant and env since one $HOME may host several.
func environmentIdleStopLogPath(homeDir, tenant, environment string) string {
	return filepath.Join(homeDir, ".erun", tenant, environment, "idle-stop.log")
}

func activityIdleMarker(kind string, snapshot model.EnvironmentActivitySnapshot, policy model.EnvironmentIdlePolicy, now time.Time) model.EnvironmentIdleMarker {
	marker := model.EnvironmentIdleMarker{
		Name:         kind,
		LastActivity: snapshot.LastActivity,
		LastSeen:     snapshot.LastSeen,
		Clients:      environmentIdleMarkerClients(snapshot.Clients, now),
	}

	if snapshot.LastActivity.IsZero() {
		marker.Idle = true
		marker.Reason = "no activity recorded"
		return marker
	}
	if now.Sub(snapshot.LastActivity) > policy.Timeout {
		marker.Idle = true
		if kind == configpkg.ActivityKindCodex && !snapshot.LastSeen.IsZero() && now.Sub(snapshot.LastSeen) <= policy.Timeout {
			marker.Reason = "codex is open but idle"
		} else {
			marker.Reason = "last activity exceeded timeout"
		}
		return marker
	}

	marker.Idle = false
	marker.Reason = "recent activity"
	marker.SecondsRemaining = secondsRemaining(policy.Timeout - now.Sub(snapshot.LastActivity))
	return marker
}

// Nil, not empty, so omitempty drops it.
func environmentIdleMarkerClients(clients map[string]model.EnvironmentActivityClient, now time.Time) []model.EnvironmentIdleMarkerClient {
	if len(clients) == 0 {
		return nil
	}
	out := make([]model.EnvironmentIdleMarkerClient, 0, len(clients))
	for address, client := range clients {
		entry := model.EnvironmentIdleMarkerClient{
			Address:      address,
			Bytes:        client.Bytes,
			LastActivity: client.LastActivity,
		}
		if !client.LastActivity.IsZero() {
			delta := now.Sub(client.LastActivity)
			if delta < 0 {
				delta = 0
			}
			entry.SecondsAgo = int64(delta / time.Second)
		}
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].LastActivity.After(out[j].LastActivity)
	})
	return out
}

// environmentOperatorPresenceMarkers name only a human at the console. "codex" and "process"
// also fire for orchestrator jobs, so gating on them would make a job refuse itself.
var environmentOperatorPresenceMarkers = []string{configpkg.ActivityKindSSH}

// EnvironmentOperatorPresenceReason infers operator presence from markers, worded like the sidebar.
func EnvironmentOperatorPresenceReason(status model.EnvironmentIdleStatus) (string, bool) {
	for _, marker := range status.Markers {
		if marker.Idle {
			continue
		}
		for _, kind := range environmentOperatorPresenceMarkers {
			if marker.Name == kind {
				return EnvironmentActivityMarkerDetail(kind), true
			}
		}
	}
	return "", false
}

// EnvironmentActivityMarkerDetail renders a marker as the phrase every busy surface shares.
func EnvironmentActivityMarkerDetail(marker string) string {
	switch marker {
	case records.ActivityKindProcess:
		return "running build or agent processes"
	case configpkg.ActivityKindMCP:
		return "an agent is driving it over MCP"
	case configpkg.ActivityKindCodex:
		return "an AI session is working"
	case configpkg.ActivityKindSSH:
		return "an SSH session is active"
	case configpkg.ActivityKindAPI:
		return "serving API traffic"
	case configpkg.ActivityKindCLI:
		return "running erun commands"
	default:
		return "working"
	}
}

func environmentStopBlockedReason(markers []model.EnvironmentIdleMarker) string {
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

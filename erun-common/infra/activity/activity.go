package activity

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
)

var environmentActivityKinds = []string{configpkg.ActivityKindSSH, configpkg.ActivityKindAPI, configpkg.ActivityKindMCP, configpkg.ActivityKindCLI, configpkg.ActivityKindCodex, ActivityKindProcess}

type EnvironmentActivityParams struct {
	Tenant        string
	Environment   string
	Kind          string
	Seen          bool
	Bytes         int64
	ClientUpdates []EnvironmentActivityClientUpdate
	Now           time.Time
}

// EnvironmentActivityClientUpdate carries a per-client byte delta for attribution.
type EnvironmentActivityClientUpdate struct {
	Address string
	Bytes   int64
}

type EnvironmentIdleStore interface {
	configpkg.CloudReadStore
	LoadEnvConfig(tenant, environment string) (configpkg.EnvConfig, string, error)
}

// Timestamps use omitzero: omitempty never omits a time.Time, so it would render a
// never-set field as the zero instant.

// EnvironmentActivitySnapshot is the on-disk record for one activity kind; Clients is bounded.
type EnvironmentActivitySnapshot struct {
	LastActivity time.Time                            `json:"lastActivity,omitzero"`
	LastSeen     time.Time                            `json:"lastSeen,omitzero"`
	Bytes        int64                                `json:"bytes,omitempty"`
	Clients      map[string]EnvironmentActivityClient `json:"clients,omitempty"`
}

type EnvironmentActivityClient struct {
	Bytes        int64     `json:"bytes,omitempty"`
	LastActivity time.Time `json:"lastActivity,omitzero"`
}

type EnvironmentIdleMarker struct {
	Name             string                        `json:"name"`
	Idle             bool                          `json:"idle"`
	Reason           string                        `json:"reason,omitempty"`
	SecondsRemaining int64                         `json:"secondsRemaining,omitempty"`
	LastActivity     time.Time                     `json:"lastActivity,omitzero"`
	LastSeen         time.Time                     `json:"lastSeen,omitzero"`
	Clients          []EnvironmentIdleMarkerClient `json:"clients,omitempty"`
}

// EnvironmentIdleMarkerClient is the marker's per-client view for renderers.
type EnvironmentIdleMarkerClient struct {
	Address      string    `json:"address"`
	Bytes        int64     `json:"bytes,omitempty"`
	LastActivity time.Time `json:"lastActivity,omitzero"`
	SecondsAgo   int64     `json:"secondsAgo,omitempty"`
}

const environmentActivityClientCap = 8

type EnvironmentIdleStatus struct {
	Policy              configpkg.EnvironmentIdlePolicy        `json:"policy"`
	OutsideWorkingHours bool                                   `json:"outsideWorkingHours"`
	ManagedCloud        bool                                   `json:"managedCloud"`
	StopEligible        bool                                   `json:"stopEligible"`
	StopBlockedReason   string                                 `json:"stopBlockedReason,omitempty"`
	StopError           string                                 `json:"stopError,omitempty"`
	SecondsUntilStop    int64                                  `json:"secondsUntilStop,omitempty"`
	Markers             []EnvironmentIdleMarker                `json:"markers"`
	Activity            map[string]EnvironmentActivitySnapshot `json:"activity,omitempty"`
	// Leases say what is deferring auto-stop.
	Leases []EnvironmentActivityLease `json:"leases,omitempty"`
	// StopPendingSince is when the auto-stop grace period was armed (RFC3339).
	StopPendingSince       string `json:"stopPendingSince,omitempty"`
	SecondsUntilForcedStop int64  `json:"secondsUntilForcedStop,omitempty"`
	GracePeriodSeconds     int64  `json:"gracePeriodSeconds,omitempty"`
}

func ResolveEnvironmentIdleStatus(config configpkg.EnvironmentIdleConfig, activity map[string]EnvironmentActivitySnapshot, leases []EnvironmentActivityLease, now time.Time) (EnvironmentIdleStatus, error) {
	policy, err := config.Resolve()
	if err != nil {
		return EnvironmentIdleStatus{}, err
	}
	if now.IsZero() {
		now = time.Now()
	}
	if activity == nil {
		activity = map[string]EnvironmentActivitySnapshot{}
	}

	outsideWorkingHours, secondsUntilWorkingHoursEnd, err := WorkingHoursStatus(policy.WorkingHours, policy.Timezone, now)
	if err != nil {
		return EnvironmentIdleStatus{}, err
	}

	markers := environmentIdleMarkers(activity, leases, policy, now, outsideWorkingHours, secondsUntilWorkingHoursEnd)
	stopEligible, secondsUntilStop := environmentStopEligibility(markers, outsideWorkingHours)
	stopBlockedReason := environmentBlockedReason(markers, stopEligible)

	return EnvironmentIdleStatus{
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

func environmentIdleMarkers(activity map[string]EnvironmentActivitySnapshot, leases []EnvironmentActivityLease, policy configpkg.EnvironmentIdlePolicy, now time.Time, outsideWorkingHours bool, secondsUntilWorkingHoursEnd int64) []EnvironmentIdleMarker {
	markers := []EnvironmentIdleMarker{{
		Name:             "working-hours",
		Idle:             outsideWorkingHours,
		Reason:           workingHoursReason(outsideWorkingHours, policy.WorkingHours),
		SecondsRemaining: secondsUntilWorkingHoursEnd,
	}}
	for _, kind := range environmentActivityKinds {
		snapshot := activity[kind]
		markers = append(markers, activityIdleMarker(kind, snapshot, policy, now))
	}
	return append(markers, leaseIdleMarker(leases, now))
}

func environmentStopEligibility(markers []EnvironmentIdleMarker, outsideWorkingHours bool) (bool, int64) {
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

func environmentBlockedReason(markers []EnvironmentIdleMarker, stopEligible bool) string {
	if stopEligible {
		return ""
	}
	return environmentStopBlockedReason(markers)
}

func ResolveStoredEnvironmentIdleStatus(store EnvironmentIdleStore, tenant, environment string, now time.Time) (EnvironmentIdleStatus, error) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if store == nil {
		return EnvironmentIdleStatus{}, fmt.Errorf("store is required")
	}
	if err := model.ErrMissingTenantOrEnvironment("resolve environment idle status", tenant, environment); err != nil {
		return EnvironmentIdleStatus{}, err
	}
	config, _, err := store.LoadEnvConfig(tenant, environment)
	if err != nil {
		return EnvironmentIdleStatus{}, err
	}
	activity, err := LoadEnvironmentActivity(tenant, environment)
	if err != nil {
		return EnvironmentIdleStatus{}, err
	}
	leases, err := LoadEnvironmentActivityLeases(tenant, environment, now)
	if err != nil {
		return EnvironmentIdleStatus{}, err
	}
	status, err := ResolveEnvironmentIdleStatus(config.Idle, activity, leases, now)
	if err != nil {
		return EnvironmentIdleStatus{}, err
	}
	managedCloud, err := ManagedCloudEnvironment(store, config)
	if err != nil {
		return EnvironmentIdleStatus{}, err
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
func overlayStopPending(status EnvironmentIdleStatus, tenant, environment string, now time.Time) EnvironmentIdleStatus {
	if !status.ManagedCloud || !status.StopEligible {
		// Eligibility lapsed; the next MaybeArmOrFireIdleStop clears the stale entry.
		return status
	}
	pending, ok, err := LoadEnvironmentStopPending(tenant, environment)
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

func ManagedCloudEnvironment(store configpkg.CloudReadStore, env configpkg.EnvConfig) (bool, error) {
	if env.ManagedCloud {
		return true, nil
	}
	if !env.RemoteWorktree() {
		return false, nil
	}
	status, ok, err := configpkg.FindCloudContextForKubernetesContext(store, env.KubernetesContext)
	if err != nil || !ok {
		return false, err
	}
	if alias := strings.TrimSpace(env.CloudProviderAlias); alias != "" && strings.TrimSpace(status.CloudProviderAlias) != alias {
		return false, nil
	}
	return true, nil
}

func RecordEnvironmentActivity(params EnvironmentActivityParams) error {
	tenant := strings.TrimSpace(params.Tenant)
	environment := strings.TrimSpace(params.Environment)
	kind := strings.TrimSpace(params.Kind)
	if err := model.ErrMissingTenantOrEnvironment("record environment activity", tenant, environment); err != nil {
		return err
	}
	if !validEnvironmentActivityKind(kind) {
		return fmt.Errorf("unsupported activity kind %q", kind)
	}
	now := params.Now
	if now.IsZero() {
		now = time.Now()
	}
	dir, err := EnvironmentActivityDir(tenant, environment)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	path := filepath.Join(dir, kind+".json")
	snapshot, _ := loadEnvironmentActivitySnapshot(path)
	if params.Seen {
		snapshot.LastSeen = now
	} else {
		snapshot.LastActivity = now
		snapshot.LastSeen = now
	}
	if params.Bytes > 0 {
		snapshot.Bytes += params.Bytes
	}
	mergeEnvironmentActivityClients(&snapshot, params.ClientUpdates, now)

	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	// Replace atomically: every idle read parses every snapshot.
	return configpkg.ReplaceFileAtomically(path, data, 0o644)
}

func mergeEnvironmentActivityClients(snapshot *EnvironmentActivitySnapshot, updates []EnvironmentActivityClientUpdate, now time.Time) {
	if len(updates) == 0 {
		return
	}
	if snapshot.Clients == nil {
		snapshot.Clients = make(map[string]EnvironmentActivityClient, len(updates))
	}
	for _, update := range updates {
		address := strings.TrimSpace(update.Address)
		if address == "" {
			continue
		}
		client := snapshot.Clients[address]
		if update.Bytes > 0 {
			client.Bytes += update.Bytes
			client.LastActivity = now
		}
		snapshot.Clients[address] = client
	}
	evictOldestEnvironmentActivityClients(snapshot.Clients, environmentActivityClientCap)
}

func evictOldestEnvironmentActivityClients(clients map[string]EnvironmentActivityClient, limit int) {
	if limit <= 0 || len(clients) <= limit {
		return
	}
	for len(clients) > limit {
		oldestKey := ""
		var oldestActivity time.Time
		first := true
		for key, client := range clients {
			if first || client.LastActivity.Before(oldestActivity) {
				oldestKey = key
				oldestActivity = client.LastActivity
				first = false
			}
		}
		if oldestKey == "" {
			return
		}
		delete(clients, oldestKey)
	}
}

func LoadEnvironmentActivity(tenant, environment string) (map[string]EnvironmentActivitySnapshot, error) {
	dir, err := EnvironmentActivityDir(tenant, environment)
	if err != nil {
		return nil, err
	}
	result := map[string]EnvironmentActivitySnapshot{}
	for _, kind := range environmentActivityKinds {
		snapshot, err := loadEnvironmentActivitySnapshot(filepath.Join(dir, kind+".json"))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		result[kind] = snapshot
	}
	return result, nil
}

func EnvironmentActivityDir(tenant, environment string) (string, error) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if err := model.ErrMissingTenantOrEnvironment("resolve environment activity directory", tenant, environment); err != nil {
		return "", err
	}
	return configpkg.ResolveCacheFilePath(filepath.Join("erun", "activity", tenant, environment))
}

func activityIdleMarker(kind string, snapshot EnvironmentActivitySnapshot, policy configpkg.EnvironmentIdlePolicy, now time.Time) EnvironmentIdleMarker {
	marker := EnvironmentIdleMarker{
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
func environmentIdleMarkerClients(clients map[string]EnvironmentActivityClient, now time.Time) []EnvironmentIdleMarkerClient {
	if len(clients) == 0 {
		return nil
	}
	out := make([]EnvironmentIdleMarkerClient, 0, len(clients))
	for address, client := range clients {
		entry := EnvironmentIdleMarkerClient{
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
func EnvironmentOperatorPresenceReason(status EnvironmentIdleStatus) (string, bool) {
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
	case ActivityKindProcess:
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

func environmentStopBlockedReason(markers []EnvironmentIdleMarker) string {
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

func loadEnvironmentActivitySnapshot(path string) (EnvironmentActivitySnapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return EnvironmentActivitySnapshot{}, err
	}
	var snapshot EnvironmentActivitySnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return EnvironmentActivitySnapshot{}, err
	}
	return snapshot, nil
}

func validEnvironmentActivityKind(kind string) bool {
	for _, candidate := range environmentActivityKinds {
		if kind == candidate {
			return true
		}
	}
	return false
}

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

// EnvironmentActivityClientUpdate carries a per-client byte delta so the
// desktop tooltip can attribute activity to the peer keeping the marker
// active, not just report a total.
type EnvironmentActivityClientUpdate struct {
	Address string
	Bytes   int64
}

type EnvironmentIdleStore interface {
	configpkg.CloudReadStore
	LoadEnvConfig(tenant, environment string) (configpkg.EnvConfig, string, error)
}

// Every timestamp below carries omitzero rather than omitempty: a value
// time.Time is never "empty" to encoding/json, so omitempty silently renders a
// never-set field as the zero instant — a fabricated timestamp a consumer
// cannot tell from a real one. A marker that has never seen activity must
// carry no timestamp at all.

// EnvironmentActivitySnapshot is the on-disk record for one activity kind.
// Clients is bounded so a long-lived runtime cannot grow the file without
// bound under churn (e.g. ephemeral source ports on a NAT'd peer).
//
// The timestamp tags are `omitzero`, not `omitempty`: `omitempty` has no empty
// case for a time.Time, so a marker that had never been touched serialized as
// Go's zero instant and a consumer could not tell it from one seen at the zero
// instant. `omitzero` is what delivers the omission the tag names.
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

// EnvironmentIdleMarkerClient is the marker's view for the desktop tooltip
// and the CLI --json output. SecondsAgo is precomputed so renderers need
// not recompute it off LastActivity every frame.
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
	// Leases are the work claims currently holding the environment. They are
	// carried on the status so a client can say *what* is deferring auto-stop,
	// not merely that something is.
	Leases []EnvironmentActivityLease `json:"leases,omitempty"`
	// StopPendingSince is the RFC3339 time the auto-stop grace period was
	// first armed. While set, the in-pod monitor and the desktop treat the
	// env as "warning, not yet ready to fire"; MaybeArmOrFireIdleStop fires
	// only once now - since >= grace.
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
		// Outside working hours the quiet signals no longer hold the environment
		// up. A lease is not a quiet signal: it is a job saying it is running
		// right now, so it blocks the stop whatever the clock says — otherwise an
		// agent run started at 19:59 loses its environment a minute later.
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

// overlayStopPending surfaces the auto-stop grace window on the resolved
// status so every consumer sees it — the in-pod monitor, the desktop via
// the MCP `idle` tool, and future external clients — even though only
// callers driving MaybeArmOrFireIdleStop write the pending state.
func overlayStopPending(status EnvironmentIdleStatus, tenant, environment string, now time.Time) EnvironmentIdleStatus {
	if !status.ManagedCloud || !status.StopEligible {
		// Eligibility lapsed: don't surface a stale pending entry to
		// read-only consumers; the next MaybeArmOrFireIdleStop clears it.
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

// environmentIdleStopLogPath keys the idle-stop log by tenant and env so a
// later doctor/activity read can attribute a shutdown failure to the right
// environment when one $HOME hosts multiple tenants.
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
	// Replaced, never rewritten in place: every idle-status read parses every
	// snapshot, and one truncated mid-write fails that whole read.
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

// environmentIdleMarkerClients returns nil (not an empty slice) when no
// clients were recorded so JSON omitempty keeps the payload terse for
// kinds that never populate it.
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

// environmentOperatorPresenceMarkers are the activity kinds that name a human
// at the console, and only that. "ssh" is unambiguous — no agent job opens an
// interactive shell into its own pod as part of normal work. "codex" and
// "process" were deliberately left out despite naming an AI session, because
// they are not unambiguous: the "codex" marker names any invocation of the AI
// tools, including the one a detached orchestrator job starts (job_agent.go
// execs the same "claude"/"codex" on PATH), and the resident sampler's
// "process" marker fires for that same resident process.
// Both are environment-wide, not scoped to a worktree, so gating on either
// would make an orchestrator refuse itself, and would make a second
// legitimate job in a *different* clone of the same pod refuse against the
// first one's resident process — exactly the "not a global mutex" outcome
// this feature must avoid. Phase 2 as shipped here is therefore narrower than
// the issue's description: SSH presence only. Codex/claude session presence
// remains visible (the marker is unchanged and still surfaces on `idle` and
// the sidebar) but does not by itself refuse an exclusive take.
var environmentOperatorPresenceMarkers = []string{configpkg.ActivityKindSSH}

// EnvironmentOperatorPresenceReason reports why an environment reads as having
// an operator physically present at an interactive shell, in the same words
// environmentBusyFromIdleStatus / the desktop sidebar already use, so an
// exclusive lease refusal and the sidebar's busy detail never disagree about
// what "busy" means. The operator never takes a lease, so this is inferred
// from markers rather than from a competing claim.
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

// EnvironmentActivityMarkerDetail renders an idle marker's name as the phrase
// an operator would use to describe it, so every surface that reports why an
// environment is busy - the desktop sidebar, an exclusive lease refusal -
// says the same thing.
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

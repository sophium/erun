package eruncommon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultEnvironmentIdleTimeout      = 5 * time.Minute
	DefaultEnvironmentWorkingHours     = "08:00-20:00"
	DefaultEnvironmentIdleTrafficBytes = 0

	ActivityKindSSH   = "ssh"
	ActivityKindAPI   = "api"
	ActivityKindMCP   = "mcp"
	ActivityKindCLI   = "cli"
	ActivityKindCodex = "codex"
)

var environmentActivityKinds = []string{ActivityKindSSH, ActivityKindAPI, ActivityKindMCP, ActivityKindCLI, ActivityKindCodex, ActivityKindProcess}

type EnvironmentIdleConfig struct {
	Timeout          string `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	WorkingHours     string `yaml:"workinghours,omitempty" json:"workingHours,omitempty"`
	Timezone         string `yaml:"timezone,omitempty" json:"timezone,omitempty"`
	IdleTrafficBytes int64  `yaml:"idletrafficbytes,omitempty" json:"idleTrafficBytes,omitempty"`
}

type EnvironmentIdlePolicy struct {
	Timeout          time.Duration `json:"timeout"`
	WorkingHours     string        `json:"workingHours"`
	Timezone         string        `json:"timezone,omitempty"`
	IdleTrafficBytes int64         `json:"idleTrafficBytes"`
}

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
	CloudReadStore
	LoadEnvConfig(tenant, environment string) (EnvConfig, string, error)
}

// EnvironmentActivitySnapshot is the on-disk record for one activity kind.
// Clients is bounded so a long-lived runtime cannot grow the file without
// bound under churn (e.g. ephemeral source ports on a NAT'd peer).
type EnvironmentActivitySnapshot struct {
	LastActivity time.Time                            `json:"lastActivity,omitempty"`
	LastSeen     time.Time                            `json:"lastSeen,omitempty"`
	Bytes        int64                                `json:"bytes,omitempty"`
	Clients      map[string]EnvironmentActivityClient `json:"clients,omitempty"`
}

type EnvironmentActivityClient struct {
	Bytes        int64     `json:"bytes,omitempty"`
	LastActivity time.Time `json:"lastActivity,omitempty"`
}

type EnvironmentIdleMarker struct {
	Name             string                        `json:"name"`
	Idle             bool                          `json:"idle"`
	Reason           string                        `json:"reason,omitempty"`
	SecondsRemaining int64                         `json:"secondsRemaining,omitempty"`
	LastActivity     time.Time                     `json:"lastActivity,omitempty"`
	LastSeen         time.Time                     `json:"lastSeen,omitempty"`
	Clients          []EnvironmentIdleMarkerClient `json:"clients,omitempty"`
}

// EnvironmentIdleMarkerClient is the marker's view for the desktop tooltip
// and the CLI --json output. SecondsAgo is precomputed so renderers need
// not recompute it off LastActivity every frame.
type EnvironmentIdleMarkerClient struct {
	Address      string    `json:"address"`
	Bytes        int64     `json:"bytes,omitempty"`
	LastActivity time.Time `json:"lastActivity,omitempty"`
	SecondsAgo   int64     `json:"secondsAgo,omitempty"`
}

const environmentActivityClientCap = 8

type EnvironmentIdleStatus struct {
	Policy              EnvironmentIdlePolicy                  `json:"policy"`
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

func (c EnvironmentIdleConfig) Resolve() (EnvironmentIdlePolicy, error) {
	timeout := strings.TrimSpace(c.Timeout)
	if timeout == "" {
		timeout = DefaultEnvironmentIdleTimeout.String()
	}
	duration, err := time.ParseDuration(timeout)
	if err != nil {
		return EnvironmentIdlePolicy{}, fmt.Errorf("invalid environment idle timeout %q", timeout)
	}
	if duration <= 0 {
		return EnvironmentIdlePolicy{}, fmt.Errorf("environment idle timeout must be greater than zero")
	}

	workingHours := strings.TrimSpace(c.WorkingHours)
	if workingHours == "" {
		workingHours = DefaultEnvironmentWorkingHours
	}
	if err := validateWorkingHours(workingHours); err != nil {
		return EnvironmentIdlePolicy{}, err
	}

	timezone := strings.TrimSpace(c.Timezone)
	if timezone != "" {
		if _, err := time.LoadLocation(timezone); err != nil {
			return EnvironmentIdlePolicy{}, fmt.Errorf("invalid environment idle timezone %q: %w", timezone, err)
		}
	}

	idleTrafficBytes := c.IdleTrafficBytes
	if idleTrafficBytes < 0 {
		return EnvironmentIdlePolicy{}, fmt.Errorf("environment idle traffic threshold must not be negative")
	}

	return EnvironmentIdlePolicy{
		Timeout:          duration,
		WorkingHours:     workingHours,
		Timezone:         timezone,
		IdleTrafficBytes: idleTrafficBytes,
	}, nil
}

func ResolveEnvironmentIdleStatus(config EnvironmentIdleConfig, activity map[string]EnvironmentActivitySnapshot, leases []EnvironmentActivityLease, now time.Time) (EnvironmentIdleStatus, error) {
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

	outsideWorkingHours, secondsUntilWorkingHoursEnd, err := workingHoursStatus(policy.WorkingHours, policy.Timezone, now)
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

func environmentIdleMarkers(activity map[string]EnvironmentActivitySnapshot, leases []EnvironmentActivityLease, policy EnvironmentIdlePolicy, now time.Time, outsideWorkingHours bool, secondsUntilWorkingHoursEnd int64) []EnvironmentIdleMarker {
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
	if err := errMissingTenantOrEnvironment("resolve environment idle status", tenant, environment); err != nil {
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
	managedCloud, err := managedCloudEnvironment(store, config)
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

func managedCloudEnvironment(store CloudReadStore, env EnvConfig) (bool, error) {
	if env.ManagedCloud {
		return true, nil
	}
	if !env.RemoteWorktree() {
		return false, nil
	}
	status, ok, err := findCloudContextForKubernetesContext(store, env.KubernetesContext)
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
	if err := errMissingTenantOrEnvironment("record environment activity", tenant, environment); err != nil {
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
	return os.WriteFile(path, data, 0o644)
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

func evictOldestEnvironmentActivityClients(clients map[string]EnvironmentActivityClient, cap int) {
	if cap <= 0 || len(clients) <= cap {
		return
	}
	for len(clients) > cap {
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
	if err := errMissingTenantOrEnvironment("resolve environment activity directory", tenant, environment); err != nil {
		return "", err
	}
	return resolveCacheFilePath(filepath.Join("erun", "activity", tenant, environment))
}

func activityIdleMarker(kind string, snapshot EnvironmentActivitySnapshot, policy EnvironmentIdlePolicy, now time.Time) EnvironmentIdleMarker {
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
		if kind == ActivityKindCodex && !snapshot.LastSeen.IsZero() && now.Sub(snapshot.LastSeen) <= policy.Timeout {
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
// they are not unambiguous the way the issue's description assumed: the
// codex-wrapper touches the "codex" marker for *any* invocation of the
// codex/claude binaries, including the one a detached orchestrator job starts
// (job_agent.go execs the same wrapped "claude"/"codex" on PATH), and the
// resident sampler's "process" marker fires for that same resident process.
// Both are environment-wide, not scoped to a worktree, so gating on either
// would make an orchestrator refuse itself, and would make a second
// legitimate job in a *different* clone of the same pod refuse against the
// first one's resident process — exactly the "not a global mutex" outcome
// this feature must avoid. Phase 2 as shipped here is therefore narrower than
// the issue's description: SSH presence only. Codex/claude session presence
// remains visible (the marker is unchanged and still surfaces on `idle` and
// the sidebar) but does not by itself refuse an exclusive take.
var environmentOperatorPresenceMarkers = []string{ActivityKindSSH}

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
	case ActivityKindMCP:
		return "an agent is driving it over MCP"
	case ActivityKindCodex:
		return "an AI session is working"
	case ActivityKindSSH:
		return "an SSH session is active"
	case ActivityKindAPI:
		return "serving API traffic"
	case ActivityKindCLI:
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

func validateWorkingHours(value string) error {
	start, end, err := parseWorkingHours(value)
	if err != nil {
		return err
	}
	if start == end {
		return fmt.Errorf("environment working hours start and end must differ")
	}
	return nil
}

func workingHoursStatus(value string, timezone string, now time.Time) (bool, int64, error) {
	start, end, err := parseWorkingHours(value)
	if err != nil {
		return false, 0, err
	}
	if timezone = strings.TrimSpace(timezone); timezone != "" {
		loc, locErr := time.LoadLocation(timezone)
		if locErr != nil {
			return false, 0, fmt.Errorf("invalid environment idle timezone %q: %w", timezone, locErr)
		}
		now = now.In(loc)
	}
	minute := now.Hour()*60 + now.Minute()
	if start < end {
		outside := minute < start || minute >= end
		if outside {
			return true, 0, nil
		}
		return false, int64((end-minute)*60 - now.Second()), nil
	}
	outside := minute >= end && minute < start
	if outside {
		return true, 0, nil
	}
	remainingMinutes := end - minute
	if remainingMinutes <= 0 {
		remainingMinutes += 24 * 60
	}
	return false, int64(remainingMinutes*60 - now.Second()), nil
}

func parseWorkingHours(value string) (int, int, error) {
	parts := strings.Split(strings.TrimSpace(value), "-")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("environment working hours must use HH:MM-HH:MM")
	}
	start, err := parseClockMinute(parts[0])
	if err != nil {
		return 0, 0, err
	}
	end, err := parseClockMinute(parts[1])
	if err != nil {
		return 0, 0, err
	}
	return start, end, nil
}

func parseClockMinute(value string) (int, error) {
	parts := strings.Split(strings.TrimSpace(value), ":")
	if len(parts) != 2 {
		return 0, fmt.Errorf("environment working hours must use HH:MM-HH:MM")
	}
	hour, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, fmt.Errorf("invalid environment working hour %q", value)
	}
	minute, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, fmt.Errorf("invalid environment working minute %q", value)
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, fmt.Errorf("environment working hours must use valid 24-hour times")
	}
	return hour*60 + minute, nil
}

func workingHoursReason(outside bool, workingHours string) string {
	if outside {
		return "outside working hours " + workingHours
	}
	return "inside working hours " + workingHours
}

func secondsRemaining(duration time.Duration) int64 {
	if duration <= 0 {
		return 0
	}
	return int64((duration + time.Second - 1) / time.Second)
}

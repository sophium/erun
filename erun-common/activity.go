package eruncommon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/adrg/xdg"
)

const (
	DefaultEnvironmentIdleTimeout      = 5 * time.Minute
	DefaultEnvironmentWorkingHours     = "08:00-20:00"
	DefaultEnvironmentIdleTrafficBytes = 0

	ActivityKindSSH   = "ssh"
	ActivityKindMCP   = "mcp"
	ActivityKindCLI   = "cli"
	ActivityKindCodex = "codex"
)

var environmentActivityKinds = []string{ActivityKindSSH, ActivityKindMCP, ActivityKindCLI, ActivityKindCodex}

type EnvironmentIdleConfig struct {
	Timeout          string `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	WorkingHours     string `yaml:"workinghours,omitempty" json:"workingHours,omitempty"`
	IdleTrafficBytes int64  `yaml:"idletrafficbytes,omitempty" json:"idleTrafficBytes,omitempty"`
}

type EnvironmentIdlePolicy struct {
	Timeout          time.Duration `json:"timeout"`
	WorkingHours     string        `json:"workingHours"`
	IdleTrafficBytes int64         `json:"idleTrafficBytes"`
}

type EnvironmentActivityParams struct {
	Tenant      string
	Environment string
	Kind        string
	Seen        bool
	Bytes       int64
	Now         time.Time
}

type EnvironmentIdleStore interface {
	CloudReadStore
	LoadEnvConfig(tenant, environment string) (EnvConfig, string, error)
}

type EnvironmentActivitySnapshot struct {
	LastActivity time.Time `json:"lastActivity,omitempty"`
	LastSeen     time.Time `json:"lastSeen,omitempty"`
	Bytes        int64     `json:"bytes,omitempty"`
}

type EnvironmentIdleMarker struct {
	Name             string    `json:"name"`
	Idle             bool      `json:"idle"`
	Reason           string    `json:"reason,omitempty"`
	SecondsRemaining int64     `json:"secondsRemaining,omitempty"`
	LastActivity     time.Time `json:"lastActivity,omitempty"`
	LastSeen         time.Time `json:"lastSeen,omitempty"`
}

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

	idleTrafficBytes := c.IdleTrafficBytes
	if idleTrafficBytes < 0 {
		return EnvironmentIdlePolicy{}, fmt.Errorf("environment idle traffic threshold must not be negative")
	}

	return EnvironmentIdlePolicy{
		Timeout:          duration,
		WorkingHours:     workingHours,
		IdleTrafficBytes: idleTrafficBytes,
	}, nil
}

func DefaultEnvironmentIdleConfig() EnvironmentIdleConfig {
	return EnvironmentIdleConfig{
		Timeout:          DefaultEnvironmentIdleTimeout.String(),
		WorkingHours:     DefaultEnvironmentWorkingHours,
		IdleTrafficBytes: DefaultEnvironmentIdleTrafficBytes,
	}
}

func ResolveEnvironmentIdleStatus(config EnvironmentIdleConfig, activity map[string]EnvironmentActivitySnapshot, now time.Time) (EnvironmentIdleStatus, error) {
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

	outsideWorkingHours, secondsUntilWorkingHoursEnd, err := workingHoursStatus(policy.WorkingHours, now)
	if err != nil {
		return EnvironmentIdleStatus{}, err
	}

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

	secondsUntilStop := int64(0)
	stopEligible := outsideWorkingHours
	if !outsideWorkingHours {
		stopEligible = true
		for _, marker := range markers {
			if marker.Name == "working-hours" {
				continue
			}
			if !marker.Idle {
				stopEligible = false
				if marker.SecondsRemaining > secondsUntilStop {
					secondsUntilStop = marker.SecondsRemaining
				}
			}
		}
	}
	if stopEligible {
		secondsUntilStop = 0
	}
	stopBlockedReason := ""
	if !stopEligible {
		stopBlockedReason = environmentStopBlockedReason(markers)
	}

	return EnvironmentIdleStatus{
		Policy:              policy,
		OutsideWorkingHours: outsideWorkingHours,
		StopEligible:        stopEligible,
		StopBlockedReason:   stopBlockedReason,
		SecondsUntilStop:    secondsUntilStop,
		Markers:             markers,
		Activity:            activity,
	}, nil
}

func ResolveStoredEnvironmentIdleStatus(store EnvironmentIdleStore, tenant, environment string, now time.Time) (EnvironmentIdleStatus, error) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if store == nil {
		return EnvironmentIdleStatus{}, fmt.Errorf("store is required")
	}
	if tenant == "" || environment == "" {
		return EnvironmentIdleStatus{}, fmt.Errorf("tenant and environment are required")
	}
	config, _, err := store.LoadEnvConfig(tenant, environment)
	if err != nil {
		return EnvironmentIdleStatus{}, err
	}
	activity, err := LoadEnvironmentActivity(tenant, environment)
	if err != nil {
		return EnvironmentIdleStatus{}, err
	}
	status, err := ResolveEnvironmentIdleStatus(config.Idle, activity, now)
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
	status.StopError = loadEnvironmentIdleStopError()
	return status, nil
}

func loadEnvironmentIdleStopError() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(home, ".erun", "idle-stop.log"))
	if err != nil {
		return ""
	}
	value := strings.TrimSpace(string(data))
	const maxStopErrorLength = 4000
	if len(value) <= maxStopErrorLength {
		return value
	}
	return value[len(value)-maxStopErrorLength:]
}

func managedCloudEnvironment(store CloudReadStore, env EnvConfig) (bool, error) {
	if !env.Remote {
		return false, nil
	}
	if env.ManagedCloud {
		return true, nil
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
	if tenant == "" || environment == "" {
		return fmt.Errorf("tenant and environment are required")
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

	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
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
	if tenant == "" || environment == "" {
		return "", fmt.Errorf("tenant and environment are required")
	}
	dir, err := xdg.CacheFile(filepath.Join("erun", "activity", tenant, environment))
	if err != nil {
		return "", err
	}
	return dir, nil
}

func activityIdleMarker(kind string, snapshot EnvironmentActivitySnapshot, policy EnvironmentIdlePolicy, now time.Time) EnvironmentIdleMarker {
	marker := EnvironmentIdleMarker{
		Name:         kind,
		LastActivity: snapshot.LastActivity,
		LastSeen:     snapshot.LastSeen,
	}

	if kind == ActivityKindSSH && snapshot.Bytes <= policy.IdleTrafficBytes {
		marker.Idle = true
		marker.Reason = "traffic is at or below idle threshold"
		return marker
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

func workingHoursStatus(value string, now time.Time) (bool, int64, error) {
	start, end, err := parseWorkingHours(value)
	if err != nil {
		return false, 0, err
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

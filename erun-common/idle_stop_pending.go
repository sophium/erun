package eruncommon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// IdleStopActionSkip / Arm / Wait / Fire are the outcomes of
// MaybeArmOrFireIdleStop. The string values are stable — they are
// surfaced through `erun activity stop-ready --json`.
const (
	IdleStopActionSkip = "skip"
	IdleStopActionArm  = "arm"
	IdleStopActionWait = "wait"
	IdleStopActionFire = "fire"
)

// StopHistoryCap bounds the per-env stop history: low enough to stay
// human-readable, deep enough to spot a recurring stop pattern.
const StopHistoryCap = 10

// EnvironmentStopPending is the on-disk stop-pending record. Writing
// it arms the grace-period warning; readers use it to compute the time
// remaining before a forced stop without re-running the decision logic.
type EnvironmentStopPending struct {
	Since            time.Time               `json:"since"`
	GraceSeconds     int64                   `json:"graceSeconds"`
	CloudContextName string                  `json:"cloudContextName,omitempty"`
	ReasonSummary    string                  `json:"reasonSummary,omitempty"`
	Markers          []EnvironmentIdleMarker `json:"markers,omitempty"`
	// Policy is snapshotted when the window is armed, so a stop-history
	// row stays interpretable after the user later edits the policy.
	Policy EnvironmentIdlePolicy `json:"policy"`
}

// StopHistorySourcePodMonitor / StopHistorySourceHostManual are the
// stable Source values for a stop-history row — the idle_stop_record
// MCP tool validates against them.
const (
	StopHistorySourcePodMonitor = "pod-monitor"
	StopHistorySourceHostManual = "host-manual"
)

// EnvironmentStopHistoryEntry is one audit row a user reads to answer
// "why did my env stop?" — and "why repeatedly?" — without trawling
// logs. ArmedAt is set only on pod-monitor stops; host-manual entries
// leave it zero. Policy is snapshotted at arm/fire time so the row
// stays interpretable after the user later edits the policy.
type EnvironmentStopHistoryEntry struct {
	StoppedAt        time.Time                      `json:"stoppedAt"`
	ArmedAt          time.Time                      `json:"armedAt,omitzero"`
	GraceSeconds     int64                          `json:"graceSeconds"`
	Source           string                         `json:"source,omitempty"`
	Reason           string                         `json:"reason"`
	CloudContextName string                         `json:"cloudContextName,omitempty"`
	Policy           EnvironmentIdlePolicy          `json:"policy,omitzero"`
	Markers          []EnvironmentStopHistoryMarker `json:"markers,omitempty"`
}

// EnvironmentStopHistoryMarker is the audit-record subset of
// EnvironmentIdleMarker. Idle=false records a marker still active when
// the stop fired — useful when chasing a regression where a marker's
// activity isn't being counted.
type EnvironmentStopHistoryMarker struct {
	Name           string `json:"name"`
	Idle           bool   `json:"idle"`
	Reason         string `json:"reason,omitempty"`
	SecondsIdleFor int64  `json:"secondsIdleFor,omitempty"`
}

// MaybeArmOrFireIdleStopParams are the inputs to MaybeArmOrFireIdleStop.
// CloudContextName and ReasonSummary are carried into the pending entry
// so the warning toast can name the env and reason without re-resolving
// idle status.
type MaybeArmOrFireIdleStopParams struct {
	Tenant           string
	Environment      string
	Status           EnvironmentIdleStatus
	CloudContextName string
	ReasonSummary    string
	Now              time.Time
}

// MaybeArmOrFireIdleStopResult carries the chosen action and the
// resulting on-disk state. State is empty on Skip and Fire (the pending
// file is cleared) and populated otherwise.
type MaybeArmOrFireIdleStopResult struct {
	Action           string
	State            EnvironmentStopPending
	SecondsRemaining int64
}

// MaybeArmOrFireIdleStop is the shared arm/wait/fire decision, owned in
// one place so the in-pod monitor and the desktop behave identically;
// the only per-caller difference is who performs the AWS stop on Fire.
func MaybeArmOrFireIdleStop(params MaybeArmOrFireIdleStopParams) (MaybeArmOrFireIdleStopResult, error) {
	tenant := strings.TrimSpace(params.Tenant)
	environment := strings.TrimSpace(params.Environment)
	if err := errMissingTenantOrEnvironment("resolve idle-stop arm/wait/fire decision", tenant, environment); err != nil {
		return MaybeArmOrFireIdleStopResult{}, err
	}
	now := params.Now
	if now.IsZero() {
		now = time.Now()
	}
	if !params.Status.ManagedCloud || !params.Status.StopEligible {
		if err := ClearEnvironmentStopPending(tenant, environment); err != nil {
			return MaybeArmOrFireIdleStopResult{}, err
		}
		return MaybeArmOrFireIdleStopResult{Action: IdleStopActionSkip}, nil
	}
	pending, ok, err := LoadEnvironmentStopPending(tenant, environment)
	if err != nil {
		return MaybeArmOrFireIdleStopResult{}, err
	}
	if !ok {
		return armIdleStopGraceWindow(tenant, environment, params, now)
	}
	return resolveArmedIdleStopWindow(tenant, environment, pending, now)
}

func armIdleStopGraceWindow(tenant, environment string, params MaybeArmOrFireIdleStopParams, now time.Time) (MaybeArmOrFireIdleStopResult, error) {
	grace := idleStopGraceSeconds(params.Status)
	armed := EnvironmentStopPending{
		Since:            now,
		GraceSeconds:     grace,
		CloudContextName: strings.TrimSpace(params.CloudContextName),
		ReasonSummary:    strings.TrimSpace(params.ReasonSummary),
		Markers:          cloneIdleMarkersForPending(params.Status.Markers),
		Policy:           params.Status.Policy,
	}
	if err := SaveEnvironmentStopPending(tenant, environment, armed); err != nil {
		return MaybeArmOrFireIdleStopResult{}, err
	}
	return MaybeArmOrFireIdleStopResult{
		Action:           IdleStopActionArm,
		State:            armed,
		SecondsRemaining: grace,
	}, nil
}

// resolveArmedIdleStopWindow clears the pending file before reporting
// Fire so a caller that crashes mid-stop does not strand the env
// "armed forever".
func resolveArmedIdleStopWindow(tenant, environment string, pending EnvironmentStopPending, now time.Time) (MaybeArmOrFireIdleStopResult, error) {
	elapsed := now.Sub(pending.Since)
	if elapsed < time.Duration(pending.GraceSeconds)*time.Second {
		remaining := pending.GraceSeconds - int64(elapsed.Seconds())
		if remaining < 0 {
			remaining = 0
		}
		return MaybeArmOrFireIdleStopResult{
			Action:           IdleStopActionWait,
			State:            pending,
			SecondsRemaining: remaining,
		}, nil
	}
	if err := ClearEnvironmentStopPending(tenant, environment); err != nil {
		return MaybeArmOrFireIdleStopResult{}, err
	}
	return MaybeArmOrFireIdleStopResult{
		Action: IdleStopActionFire,
		State:  pending,
	}, nil
}

// idleStopGraceSeconds uses the resolved idle timeout verbatim as the
// grace window — the spec is "warn for at least the idle timeout".
func idleStopGraceSeconds(status EnvironmentIdleStatus) int64 {
	seconds := int64(status.Policy.Timeout / time.Second)
	if seconds <= 0 {
		seconds = int64(DefaultEnvironmentIdleTimeout / time.Second)
	}
	return seconds
}

// cloneIdleMarkersForPending copies only the audit-relevant marker
// fields; client IP lists are omitted because the record needs "which
// markers were idle when grace was armed", not who last sent bytes.
func cloneIdleMarkersForPending(markers []EnvironmentIdleMarker) []EnvironmentIdleMarker {
	if len(markers) == 0 {
		return nil
	}
	out := make([]EnvironmentIdleMarker, 0, len(markers))
	for _, marker := range markers {
		out = append(out, EnvironmentIdleMarker{
			Name:             strings.TrimSpace(marker.Name),
			Idle:             marker.Idle,
			Reason:           strings.TrimSpace(marker.Reason),
			SecondsRemaining: marker.SecondsRemaining,
			LastActivity:     marker.LastActivity,
		})
	}
	return out
}

// LoadEnvironmentStopPending returns ok=false and no error when the
// file is absent ("no warning armed"), and an error on malformed
// contents so a corrupt file is loud rather than silently ignored.
func LoadEnvironmentStopPending(tenant, environment string) (EnvironmentStopPending, bool, error) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if err := errMissingTenantOrEnvironment("load environment stop-pending state", tenant, environment); err != nil {
		return EnvironmentStopPending{}, false, err
	}
	path, err := EnvironmentStopPendingPath(tenant, environment)
	if err != nil {
		return EnvironmentStopPending{}, false, err
	}
	body, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return EnvironmentStopPending{}, false, nil
		}
		return EnvironmentStopPending{}, false, err
	}
	var pending EnvironmentStopPending
	if err := json.Unmarshal(body, &pending); err != nil {
		return EnvironmentStopPending{}, false, fmt.Errorf("invalid %s: %w", path, err)
	}
	return pending, true, nil
}

// SaveEnvironmentStopPending writes the pending entry. Callers should
// not write the file directly except in tests; go through
// MaybeArmOrFireIdleStop.
func SaveEnvironmentStopPending(tenant, environment string, pending EnvironmentStopPending) error {
	path, err := EnvironmentStopPendingPath(tenant, environment)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(pending, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	return os.WriteFile(path, body, 0o600)
}

// ClearEnvironmentStopPending removes the pending file, or no-ops when
// it is absent.
func ClearEnvironmentStopPending(tenant, environment string) error {
	path, err := EnvironmentStopPendingPath(tenant, environment)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// EnvironmentStopPendingPath resolves the pending-file path. It sits on
// the shared home PVC alongside idle-stop.log so both survive pod
// restarts.
func EnvironmentStopPendingPath(tenant, environment string) (string, error) {
	dir, err := environmentRuntimeStateDir(tenant, environment)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "stop-pending.json"), nil
}

// EnvironmentStopHistoryPath resolves the stop-history file path.
func EnvironmentStopHistoryPath(tenant, environment string) (string, error) {
	dir, err := environmentRuntimeStateDir(tenant, environment)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "stop-history.json"), nil
}

// AppendStopHistoryEntry records one entry newest-first. Only the
// caller that fires the actual AWS stop calls it, so the history
// reflects real stops, not arming events.
func AppendStopHistoryEntry(tenant, environment string, entry EnvironmentStopHistoryEntry) error {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if err := errMissingTenantOrEnvironment("append stop-history entry", tenant, environment); err != nil {
		return err
	}
	path, err := EnvironmentStopHistoryPath(tenant, environment)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	history, err := readStopHistoryFile(path)
	if err != nil {
		return err
	}
	history = append([]EnvironmentStopHistoryEntry{entry}, history...)
	if len(history) > StopHistoryCap {
		history = history[:StopHistoryCap]
	}
	body, err := json.MarshalIndent(history, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	return os.WriteFile(path, body, 0o600)
}

// LoadEnvironmentStopHistory returns the audit array newest-first, or
// an empty slice (no error) when the file is absent.
func LoadEnvironmentStopHistory(tenant, environment string) ([]EnvironmentStopHistoryEntry, error) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if err := errMissingTenantOrEnvironment("load stop-history", tenant, environment); err != nil {
		return nil, err
	}
	path, err := EnvironmentStopHistoryPath(tenant, environment)
	if err != nil {
		return nil, err
	}
	return readStopHistoryFile(path)
}

func readStopHistoryFile(path string) ([]EnvironmentStopHistoryEntry, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []EnvironmentStopHistoryEntry{}, nil
		}
		return nil, err
	}
	var history []EnvironmentStopHistoryEntry
	if err := json.Unmarshal(body, &history); err != nil {
		return nil, fmt.Errorf("invalid %s: %w", path, err)
	}
	if history == nil {
		return []EnvironmentStopHistoryEntry{}, nil
	}
	return history, nil
}

// environmentRuntimeStateDir resolves the per-env HOME/.erun state
// directory. The path must match entrypoint.sh
// (erun-devops/docker/erun-devops/entrypoint.sh) so the in-pod monitor
// and any tool under the same HOME share the same files.
func environmentRuntimeStateDir(tenant, environment string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(home) == "" {
		return "", fmt.Errorf("$HOME is not set")
	}
	return filepath.Join(home, ".erun", tenant, environment), nil
}

// IdleStopReasonSummary builds the one-line reason shown in both the
// grace-arm warning toast and the stop-history record.
func IdleStopReasonSummary(status EnvironmentIdleStatus) string {
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

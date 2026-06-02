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

// IdleStopActionSkip / IdleStopActionArm / IdleStopActionWait /
// IdleStopActionFire are the four outcomes of MaybeArmOrFireIdleStop.
// They name the decision the caller (the in-pod monitor loop or the
// desktop) should take: drop the pending entry and do nothing, arm a
// fresh grace window, keep waiting because the window is open, or
// fire the actual ec2:StopInstances. The string values are stable
// and surfaced through `erun activity stop-ready --json`.
const (
	IdleStopActionSkip = "skip"
	IdleStopActionArm  = "arm"
	IdleStopActionWait = "wait"
	IdleStopActionFire = "fire"
)

// StopHistoryCap limits the per-env stop history file. Newer entries
// push older ones off the tail. Kept on the low end so the file stays
// human-readable while still being deep enough to diagnose a
// recurring stop pattern.
const StopHistoryCap = 10

// EnvironmentStopPending is the on-disk record at
// <home>/.erun/<tenant>/<env>/stop-pending.json. Writing the file
// arms the grace-period warning; reading it lets readers (the MCP
// `idle` tool, the desktop, anything driving `stop-ready`) compute
// the seconds remaining until forced stop without re-running the
// decision logic.
type EnvironmentStopPending struct {
	Since            time.Time               `json:"since"`
	GraceSeconds     int64                   `json:"graceSeconds"`
	CloudContextName string                  `json:"cloudContextName,omitempty"`
	ReasonSummary    string                  `json:"reasonSummary,omitempty"`
	Markers          []EnvironmentIdleMarker `json:"markers,omitempty"`
	// Policy is the resolved idle policy at the moment the grace
	// window was armed. Threaded into the history record on fire so
	// History rows can answer "what was the timeout when this fired?"
	// even after the user later edits the policy.
	Policy EnvironmentIdlePolicy `json:"policy"`
}

// Source values for EnvironmentStopHistoryEntry.Source. Stable
// strings — the desktop renders them as a row badge and the
// idle_stop_record MCP tool validates against them.
const (
	StopHistorySourcePodMonitor = "pod-monitor"
	StopHistorySourceHostManual = "host-manual"
)

// EnvironmentStopHistoryEntry is one row in the
// <home>/.erun/<tenant>/<env>/stop-history.json array. Each entry
// captures the per-marker idle/active state at grace-arm time plus
// the moment the actual stop fired, so a user reading the History
// tab can answer "why did my env stop?" — and "why has it stopped
// repeatedly?" — without trawling logs.
//
// Source distinguishes auto-stops fired by the in-pod idle monitor
// (entrypoint.sh's stop-ready loop) from manual stops fired by the
// desktop's Stop button. ArmedAt is the moment the grace window
// began — only set on pod-monitor stops; host-manual entries leave
// it zero. Policy snapshots the resolved idle policy at arm/fire
// time so a History row stays interpretable after the user later
// edits the timeout or working hours.
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

// EnvironmentStopHistoryMarker mirrors EnvironmentIdleMarker's
// salient fields without dragging the full marker shape (which has
// snapshot timestamps and client lists irrelevant to the audit
// record). Idle=true means the marker had elapsed its activity
// timeout when the auto-stop fired; Idle=false records markers that
// were still active despite the stop firing — useful when chasing a
// regression where one marker's activity isn't being counted.
type EnvironmentStopHistoryMarker struct {
	Name           string `json:"name"`
	Idle           bool   `json:"idle"`
	Reason         string `json:"reason,omitempty"`
	SecondsIdleFor int64  `json:"secondsIdleFor,omitempty"`
}

// MaybeArmOrFireIdleStopParams collects the inputs for the central
// decision function so callers do not have to thread eight scalars
// every time. CloudContextName + ReasonSummary populate the pending
// entry written on first arm so the desktop's warning toast can name
// the env and the reason without re-running ResolveStoredEnvironmentIdleStatus.
type MaybeArmOrFireIdleStopParams struct {
	Tenant           string
	Environment      string
	Status           EnvironmentIdleStatus
	CloudContextName string
	ReasonSummary    string
	Now              time.Time
}

// MaybeArmOrFireIdleStopResult names the action the caller should
// take. State is the pending entry as it exists on disk after the
// call: empty when the action is Skip or Fire (the file is cleared),
// populated otherwise. SecondsRemaining is the time left in the
// grace window when Action == Wait.
type MaybeArmOrFireIdleStopResult struct {
	Action           string
	State            EnvironmentStopPending
	SecondsRemaining int64
}

// MaybeArmOrFireIdleStop is the shared decision function that both
// the in-pod idle monitor (`erun activity stop-ready`) and the
// desktop (`maybeStopIdleCloudEnvironment`) call. It owns the
// arm/wait/fire transitions and the stop-pending.json file so the
// behavior is identical on both sides; the only difference is who
// performs the AWS call when the result is Fire.
func MaybeArmOrFireIdleStop(params MaybeArmOrFireIdleStopParams) (MaybeArmOrFireIdleStopResult, error) {
	tenant := strings.TrimSpace(params.Tenant)
	environment := strings.TrimSpace(params.Environment)
	if tenant == "" || environment == "" {
		return MaybeArmOrFireIdleStopResult{}, fmt.Errorf("tenant and environment are required")
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
	grace := idleStopGraceSeconds(params.Status)
	pending, ok, err := LoadEnvironmentStopPending(tenant, environment)
	if err != nil {
		return MaybeArmOrFireIdleStopResult{}, err
	}
	if !ok {
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
	// Grace elapsed: clear the pending file before reporting Fire so
	// a crashing caller does not leave the env stuck "armed forever"
	// after a successful stop attempt that didn't get a chance to
	// clean up. The history entry is written separately by the
	// caller on successful AWS stop.
	if err := ClearEnvironmentStopPending(tenant, environment); err != nil {
		return MaybeArmOrFireIdleStopResult{}, err
	}
	return MaybeArmOrFireIdleStopResult{
		Action: IdleStopActionFire,
		State:  pending,
	}, nil
}

// idleStopGraceSeconds picks the grace-period length for an env.
// The user-facing spec (#410 follow-up) is "at least the idle
// timeout", so we use the resolved idle timeout verbatim. A
// 10-minute idle timeout yields a 10-minute warning window.
func idleStopGraceSeconds(status EnvironmentIdleStatus) int64 {
	seconds := int64(status.Policy.Timeout / time.Second)
	if seconds <= 0 {
		seconds = int64(DefaultEnvironmentIdleTimeout / time.Second)
	}
	return seconds
}

// cloneIdleMarkersForPending drops snapshot-only timestamps from the
// markers so the pending file stays compact and stable across
// reads. Client lists are intentionally omitted — the audit need is
// "which markers were idle when grace was armed", not "which IPs
// last sent bytes".
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

// LoadEnvironmentStopPending reads the pending file for the env.
// Returns ok=false (no error) when the file is absent — the caller
// can treat that as "no warning armed". Returns an error on
// malformed contents so a corrupt file is loud rather than silently
// suppressed.
func LoadEnvironmentStopPending(tenant, environment string) (EnvironmentStopPending, bool, error) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if tenant == "" || environment == "" {
		return EnvironmentStopPending{}, false, fmt.Errorf("tenant and environment are required")
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

// SaveEnvironmentStopPending atomically writes the pending entry.
// Used by MaybeArmOrFireIdleStop on the "arm" transition; callers
// should not write the file directly except in tests.
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

// ClearEnvironmentStopPending removes the pending file. No-op when
// the file is absent. Used by MaybeArmOrFireIdleStop on the Skip
// and Fire transitions, and by `erun activity cancel-stop-pending`.
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

// EnvironmentStopPendingPath resolves to
// <home>/.erun/<tenant>/<env>/stop-pending.json. The file lives
// next to idle-stop.log so the same shared home PVC carries both
// signals through pod restarts.
func EnvironmentStopPendingPath(tenant, environment string) (string, error) {
	dir, err := environmentRuntimeStateDir(tenant, environment)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "stop-pending.json"), nil
}

// EnvironmentStopHistoryPath resolves to
// <home>/.erun/<tenant>/<env>/stop-history.json.
func EnvironmentStopHistoryPath(tenant, environment string) (string, error) {
	dir, err := environmentRuntimeStateDir(tenant, environment)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "stop-history.json"), nil
}

// AppendStopHistoryEntry prepends the entry to stop-history.json
// (newest-first) and truncates to StopHistoryCap. Called by the
// caller that actually fires the AWS stop (the in-pod monitor or
// the desktop's manual Stop button) so the on-disk array always
// reflects real ec2:StopInstances calls, not just arming events.
func AppendStopHistoryEntry(tenant, environment string, entry EnvironmentStopHistoryEntry) error {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if tenant == "" || environment == "" {
		return fmt.Errorf("tenant and environment are required")
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

// LoadEnvironmentStopHistory returns the env's auto-stop audit
// array newest-first. Returns an empty slice (no error) when the
// file is absent.
func LoadEnvironmentStopHistory(tenant, environment string) ([]EnvironmentStopHistoryEntry, error) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if tenant == "" || environment == "" {
		return nil, fmt.Errorf("tenant and environment are required")
	}
	path, err := EnvironmentStopHistoryPath(tenant, environment)
	if err != nil {
		return nil, err
	}
	return readStopHistoryFile(path)
}

// readStopHistoryFile is the shared reader for stop-history.json.
// Returns an empty slice when the file is missing and an error when
// the JSON is malformed.
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

// environmentRuntimeStateDir resolves the per-env directory under
// HOME/.erun where the runtime persists state across pod restarts —
// idle-stop.log, stop-pending.json, stop-history.json. The path
// matches the one used by the entrypoint shell script
// (erun-devops/docker/erun-devops/entrypoint.sh) so the in-pod
// monitor and any tool running under the same HOME (including the
// desktop side, where applicable) read and write the same files.
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

// IdleStopReasonSummary builds the one-line reason string used for
// both the grace-arm warning toast and the stop-history record.
// Lists idle markers (excluding working-hours) by name; falls back
// to "outside working hours" when only that marker is idle, or to a
// generic "idle policy met" when the markers slice is empty.
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

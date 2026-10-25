package idlestop

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

// Outcomes of MaybeArmOrFireIdleStop; the values are stable JSON output.
const (
	IdleStopActionSkip = "skip"
	IdleStopActionArm  = "arm"
	IdleStopActionWait = "wait"
	IdleStopActionFire = "fire"
)

// StopHistoryCap bounds the per-env stop history.
const StopHistoryCap = 10

// Stable stop-history Source values; the idle_stop_record MCP tool validates against them.
const (
	StopHistorySourcePodMonitor = "pod-monitor"
	StopHistorySourceHostManual = "host-manual"
)

// MaybeArmOrFireIdleStop is the one arm/wait/fire decision shared by the pod monitor and
// the desktop; only who performs the stop differs.
func MaybeArmOrFireIdleStop(params model.MaybeArmOrFireIdleStopParams) (model.MaybeArmOrFireIdleStopResult, error) {
	tenant := strings.TrimSpace(params.Tenant)
	environment := strings.TrimSpace(params.Environment)
	if err := core.ErrMissingTenantOrEnvironment("resolve idle-stop arm/wait/fire decision", tenant, environment); err != nil {
		return model.MaybeArmOrFireIdleStopResult{}, err
	}
	now := params.Now
	if now.IsZero() {
		now = time.Now()
	}
	if !params.Status.ManagedCloud || !params.Status.StopEligible {
		if err := ClearEnvironmentStopPending(tenant, environment); err != nil {
			return model.MaybeArmOrFireIdleStopResult{}, err
		}
		return model.MaybeArmOrFireIdleStopResult{Action: IdleStopActionSkip}, nil
	}
	pending, ok, err := LoadEnvironmentStopPending(tenant, environment)
	if err != nil {
		return model.MaybeArmOrFireIdleStopResult{}, err
	}
	if !ok {
		return armIdleStopGraceWindow(tenant, environment, params, now)
	}
	return resolveArmedIdleStopWindow(tenant, environment, pending, now)
}

func armIdleStopGraceWindow(tenant, environment string, params model.MaybeArmOrFireIdleStopParams, now time.Time) (model.MaybeArmOrFireIdleStopResult, error) {
	grace := idleStopGraceSeconds(params.Status)
	armed := model.EnvironmentStopPending{
		Since:            now,
		GraceSeconds:     grace,
		CloudContextName: strings.TrimSpace(params.CloudContextName),
		ReasonSummary:    strings.TrimSpace(params.ReasonSummary),
		Markers:          cloneIdleMarkersForPending(params.Status.Markers),
		Policy:           params.Status.Policy,
	}
	if err := SaveEnvironmentStopPending(tenant, environment, armed); err != nil {
		return model.MaybeArmOrFireIdleStopResult{}, err
	}
	return model.MaybeArmOrFireIdleStopResult{
		Action:           IdleStopActionArm,
		State:            armed,
		SecondsRemaining: grace,
	}, nil
}

// Clear the pending file before Fire so a crash mid-stop never leaves the env armed forever.
func resolveArmedIdleStopWindow(tenant, environment string, pending model.EnvironmentStopPending, now time.Time) (model.MaybeArmOrFireIdleStopResult, error) {
	elapsed := now.Sub(pending.Since)
	if elapsed < time.Duration(pending.GraceSeconds)*time.Second {
		remaining := pending.GraceSeconds - int64(elapsed.Seconds())
		if remaining < 0 {
			remaining = 0
		}
		return model.MaybeArmOrFireIdleStopResult{
			Action:           IdleStopActionWait,
			State:            pending,
			SecondsRemaining: remaining,
		}, nil
	}
	if err := ClearEnvironmentStopPending(tenant, environment); err != nil {
		return model.MaybeArmOrFireIdleStopResult{}, err
	}
	return model.MaybeArmOrFireIdleStopResult{
		Action: IdleStopActionFire,
		State:  pending,
	}, nil
}

// The grace window is the idle timeout.
func idleStopGraceSeconds(status model.EnvironmentIdleStatus) int64 {
	seconds := int64(status.Policy.Timeout / time.Second)
	if seconds <= 0 {
		seconds = int64(config.DefaultEnvironmentIdleTimeout / time.Second)
	}
	return seconds
}

func cloneIdleMarkersForPending(markers []model.EnvironmentIdleMarker) []model.EnvironmentIdleMarker {
	if len(markers) == 0 {
		return nil
	}
	out := make([]model.EnvironmentIdleMarker, 0, len(markers))
	for _, marker := range markers {
		out = append(out, model.EnvironmentIdleMarker{
			Name:             strings.TrimSpace(marker.Name),
			Idle:             marker.Idle,
			Reason:           strings.TrimSpace(marker.Reason),
			SecondsRemaining: marker.SecondsRemaining,
			LastActivity:     marker.LastActivity,
		})
	}
	return out
}

// LoadEnvironmentStopPending returns ok=false when absent and an error when malformed.
func LoadEnvironmentStopPending(tenant, environment string) (model.EnvironmentStopPending, bool, error) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if err := core.ErrMissingTenantOrEnvironment("load environment stop-pending state", tenant, environment); err != nil {
		return model.EnvironmentStopPending{}, false, err
	}
	path, err := EnvironmentStopPendingPath(tenant, environment)
	if err != nil {
		return model.EnvironmentStopPending{}, false, err
	}
	body, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return model.EnvironmentStopPending{}, false, nil
		}
		return model.EnvironmentStopPending{}, false, err
	}
	var pending model.EnvironmentStopPending
	if err := json.Unmarshal(body, &pending); err != nil {
		return model.EnvironmentStopPending{}, false, fmt.Errorf("invalid %s: %w", path, err)
	}
	return pending, true, nil
}

// SaveEnvironmentStopPending writes the pending entry; outside tests, use MaybeArmOrFireIdleStop.
func SaveEnvironmentStopPending(tenant, environment string, pending model.EnvironmentStopPending) error {
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

// ClearEnvironmentStopPending removes the pending file; absence is not an error.
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

// EnvironmentStopPendingPath is on the home PVC so it survives pod restarts.
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

// AppendStopHistoryEntry records newest-first; only the caller performing the stop calls it.
func AppendStopHistoryEntry(tenant, environment string, entry model.EnvironmentStopHistoryEntry) error {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if err := core.ErrMissingTenantOrEnvironment("append stop-history entry", tenant, environment); err != nil {
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
	history = append([]model.EnvironmentStopHistoryEntry{entry}, history...)
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

// LoadEnvironmentStopHistory returns newest-first, empty when absent.
func LoadEnvironmentStopHistory(tenant, environment string) ([]model.EnvironmentStopHistoryEntry, error) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if err := core.ErrMissingTenantOrEnvironment("load stop-history", tenant, environment); err != nil {
		return nil, err
	}
	path, err := EnvironmentStopHistoryPath(tenant, environment)
	if err != nil {
		return nil, err
	}
	return readStopHistoryFile(path)
}

func readStopHistoryFile(path string) ([]model.EnvironmentStopHistoryEntry, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []model.EnvironmentStopHistoryEntry{}, nil
		}
		return nil, err
	}
	var history []model.EnvironmentStopHistoryEntry
	if err := json.Unmarshal(body, &history); err != nil {
		return nil, fmt.Errorf("invalid %s: %w", path, err)
	}
	if history == nil {
		return []model.EnvironmentStopHistoryEntry{}, nil
	}
	return history, nil
}

// environmentRuntimeStateDir is the per-env state dir the in-pod monitor writes.
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

// IdleStopReasonSummary is the one-line reason shared by the grace warning and stop history.
func IdleStopReasonSummary(status model.EnvironmentIdleStatus) string {
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

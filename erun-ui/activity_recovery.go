package main

import (
	"bytes"
	"fmt"
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"
)

// activityRecoveryResult carries a recovery action's outcome to the
// frontend, which renders Output verbatim even on failure so the operator
// always sees what the recovery did.
type activityRecoveryResult struct {
	OK     bool   `json:"ok"`
	Output string `json:"output"`
	Error  string `json:"error,omitempty"`
}

// RecoverPendingHelmRelease clears the pending-operation lock a killed
// deploy leaves behind, which otherwise blocks every later deploy on that
// release. The entry is force-dismissed so the drawer re-syncs with the
// actual cluster state.
func (a *App) RecoverPendingHelmRelease(id string) activityRecoveryResult {
	entry, errResult := a.resolveRecoverableHelmEntry(id)
	if errResult != nil {
		return *errResult
	}
	var stdout, stderr bytes.Buffer
	err := eruncommon.ClearHelmReleasePendingOperation(eruncommon.HelmReleaseRecoveryParams{
		ReleaseName:       strings.TrimSpace(entry.Release),
		Namespace:         strings.TrimSpace(entry.Namespace),
		KubernetesContext: strings.TrimSpace(entry.KubernetesContext),
		Verbosity:         eruncommon.VerbosityDebug,
		Stdout:            &stdout,
		Stderr:            &stderr,
	})
	combined := combineRecoveryOutput(stdout.String(), stderr.String())
	if err != nil {
		return activityRecoveryResult{
			OK:     false,
			Output: combined,
			Error:  fmt.Sprintf("clear pending helm release: %s", err.Error()),
		}
	}
	if final, _, ok := a.activityQueue.forceDismiss(entry.ID); ok {
		a.unlockTerminalsForActivity(final)
		a.emitActivityState(final)
	}
	return activityRecoveryResult{OK: true, Output: combined}
}

func (a *App) resolveRecoverableHelmEntry(id string) (activityQueueEntry, *activityRecoveryResult) {
	if a.activityQueue == nil {
		return activityQueueEntry{}, &activityRecoveryResult{OK: false, Error: "activity queue is not initialized"}
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return activityQueueEntry{}, &activityRecoveryResult{OK: false, Error: "activity id is required"}
	}
	var entry activityQueueEntry
	for _, candidate := range a.activityQueue.list() {
		if candidate.ID == id {
			entry = candidate
			break
		}
	}
	if entry.ID == "" {
		return activityQueueEntry{}, &activityRecoveryResult{OK: false, Error: "activity not found"}
	}
	if entry.Source != "helm" {
		return activityQueueEntry{}, &activityRecoveryResult{OK: false, Error: "recovery only applies to helm-source entries"}
	}
	if strings.TrimSpace(entry.Release) == "" || strings.TrimSpace(entry.Namespace) == "" {
		return activityQueueEntry{}, &activityRecoveryResult{OK: false, Error: "activity is missing release/namespace; nothing to recover"}
	}
	return entry, nil
}

func combineRecoveryOutput(stdout, stderr string) string {
	combined := strings.TrimSpace(stdout)
	if errOut := strings.TrimSpace(stderr); errOut != "" {
		if combined != "" {
			combined += "\n"
		}
		combined += errOut
	}
	return combined
}

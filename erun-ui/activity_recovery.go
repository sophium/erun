package main

import (
	"bytes"
	"fmt"
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"
)

// activityRecoveryResult is the JSON payload returned to the frontend
// after a recovery action runs. The frontend renders Output verbatim in
// a recovery dialog so the user sees what happened (e.g. helm's stdout
// and any deletion confirmations) regardless of success.
type activityRecoveryResult struct {
	OK     bool   `json:"ok"`
	Output string `json:"output"`
	Error  string `json:"error,omitempty"`
}

// RecoverPendingHelmRelease deletes the helm pending-operation lock for
// the activity entry's release. Used when a CLI deploy crashed (or was
// kill -9'd) leaving the helm secret tagged
// status=pending-install/upgrade/rollback so subsequent deploys cannot
// proceed. The entry is force-dismissed after the recovery so the
// drawer reflects the cluster state again. Wails-exported.
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

// resolveRecoverableHelmEntry validates the recovery preconditions and
// returns the matching helm-source entry. On any precondition failure it
// returns a non-nil error result the caller should hand back verbatim, so
// RecoverPendingHelmRelease stays under the cyclomatic-complexity limit
// without changing any of the error messages.
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

// combineRecoveryOutput concatenates the helm recovery command's stdout and
// stderr into the single Output blob the frontend renders, joining them with
// a newline only when both are non-empty.
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

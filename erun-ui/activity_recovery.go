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
	if a.activityQueue == nil {
		return activityRecoveryResult{OK: false, Error: "activity queue is not initialized"}
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return activityRecoveryResult{OK: false, Error: "activity id is required"}
	}
	var entry activityQueueEntry
	for _, candidate := range a.activityQueue.list() {
		if candidate.ID == id {
			entry = candidate
			break
		}
	}
	if entry.ID == "" {
		return activityRecoveryResult{OK: false, Error: "activity not found"}
	}
	if entry.Source != "helm" {
		return activityRecoveryResult{OK: false, Error: "recovery only applies to helm-source entries"}
	}
	release := strings.TrimSpace(entry.Release)
	namespace := strings.TrimSpace(entry.Namespace)
	if release == "" || namespace == "" {
		return activityRecoveryResult{OK: false, Error: "activity is missing release/namespace; nothing to recover"}
	}
	var stdout, stderr bytes.Buffer
	err := eruncommon.ClearHelmReleasePendingOperation(eruncommon.HelmReleaseRecoveryParams{
		ReleaseName:       release,
		Namespace:         namespace,
		KubernetesContext: strings.TrimSpace(entry.KubernetesContext),
		Stdout:            &stdout,
		Stderr:            &stderr,
	})
	combined := strings.TrimSpace(stdout.String())
	if errOut := strings.TrimSpace(stderr.String()); errOut != "" {
		if combined != "" {
			combined += "\n"
		}
		combined += errOut
	}
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

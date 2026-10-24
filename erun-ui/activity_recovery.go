package main

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/seam"
	"github.com/sophium/erun/erun-common/primitive/deploy"
)

// activityRecoveryResult's Output is shown verbatim, even on failure.
type activityRecoveryResult struct {
	OK     bool   `json:"ok"`
	Output string `json:"output"`
	Error  string `json:"error,omitempty"`
}

// RecoverPendingHelmRelease clears the pending-operation lock a killed deploy
// leaves, which blocks every later deploy on that release.
func (a *App) RecoverPendingHelmRelease(id string) activityRecoveryResult {
	entry, errResult := a.resolveRecoverableHelmEntry(id)
	if errResult != nil {
		return *errResult
	}
	var stdout, stderr bytes.Buffer
	err := deploy.ClearHelmReleasePendingOperation(seam.HelmReleaseRecoveryParams{
		ReleaseName:       strings.TrimSpace(entry.Release),
		Namespace:         strings.TrimSpace(entry.Namespace),
		KubernetesContext: strings.TrimSpace(entry.KubernetesContext),
		Verbosity:         core.VerbosityDebug,
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

package activity

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/seam"
	"github.com/sophium/erun/erun-common/primitive/deploy/recovery"
)

// RecoveryResult's Output is shown verbatim, even on failure.
type RecoveryResult struct {
	OK     bool   `json:"ok"`
	Output string `json:"output"`
	Error  string `json:"error,omitempty"`
}

// RecoverPendingHelmRelease clears the pending-operation lock a killed deploy
// leaves, which blocks every later deploy on that release.
func (s *Service) RecoverPendingHelmRelease(id string) RecoveryResult {
	entry, errResult := s.resolveRecoverableHelmEntry(id)
	if errResult != nil {
		return *errResult
	}
	var stdout, stderr bytes.Buffer
	err := recovery.ClearHelmReleasePendingOperation(seam.HelmReleaseRecoveryParams{
		ReleaseName:       strings.TrimSpace(entry.Release),
		Namespace:         strings.TrimSpace(entry.Namespace),
		KubernetesContext: strings.TrimSpace(entry.KubernetesContext),
		Verbosity:         core.VerbosityDebug,
		Stdout:            &stdout,
		Stderr:            &stderr,
	})
	combined := combineRecoveryOutput(stdout.String(), stderr.String())
	if err != nil {
		return RecoveryResult{
			OK:     false,
			Output: combined,
			Error:  fmt.Sprintf("clear pending helm release: %s", err.Error()),
		}
	}
	if final, _, ok := s.queue.forceDismiss(entry.ID); ok {
		s.unlockTerminalsForActivity(final)
		s.emitState(final)
	}
	return RecoveryResult{OK: true, Output: combined}
}

func (s *Service) resolveRecoverableHelmEntry(id string) (Entry, *RecoveryResult) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Entry{}, &RecoveryResult{OK: false, Error: "activity id is required"}
	}
	var entry Entry
	for _, candidate := range s.queue.list() {
		if candidate.ID == id {
			entry = candidate
			break
		}
	}
	if entry.ID == "" {
		return Entry{}, &RecoveryResult{OK: false, Error: "activity not found"}
	}
	if entry.Source != "helm" {
		return Entry{}, &RecoveryResult{OK: false, Error: "recovery only applies to helm-source entries"}
	}
	if strings.TrimSpace(entry.Release) == "" || strings.TrimSpace(entry.Namespace) == "" {
		return Entry{}, &RecoveryResult{OK: false, Error: "activity is missing release/namespace; nothing to recover"}
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

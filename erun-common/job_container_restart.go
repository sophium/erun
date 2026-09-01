package eruncommon

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"
)

// jobSupervisorContainerRestart asks the Kubernetes API for this pod's own
// container status, so a job whose supervisor vanished from a still-running
// pod (job.Hostname equals the pod reconciling it) can report a real
// container restart -- reason and exit code -- instead of guessing pod
// replacement. It reuses the same `kubectl get pod -o json` shape
// open_runtime_diagnostics.go already parses.
//
// ok is false whenever the answer cannot be trusted: kubectl is unavailable,
// the API call failed, the named container is missing from the response, or
// its terminated timestamp does not parse. A caller must never read ok=false
// as "did not restart" -- only as "could not check".
func jobSupervisorContainerRestart(podName, containerName string, runner openKubectlRunnerFunc) (restarted bool, reason string, exitCode int, finishedAt time.Time, ok bool) {
	podName = strings.TrimSpace(podName)
	if podName == "" || runner == nil {
		return false, "", 0, time.Time{}, false
	}
	var stdout, stderr bytes.Buffer
	if err := runner([]string{"get", "pod", podName, "-o", "json"}, &stdout, &stderr); err != nil {
		return false, "", 0, time.Time{}, false
	}
	var pod runtimePodDiagnostic
	if err := json.Unmarshal(stdout.Bytes(), &pod); err != nil {
		return false, "", 0, time.Time{}, false
	}
	for _, container := range pod.Status.ContainerStatuses {
		if container.Name != containerName {
			continue
		}
		terminated := container.LastState.Terminated
		if terminated == nil {
			// The pod is real and answered: no restart is recorded for this
			// container, a definite (not missing) answer.
			return false, "", 0, time.Time{}, true
		}
		parsed, err := time.Parse(time.RFC3339, terminated.FinishedAt)
		if err != nil {
			return false, "", 0, time.Time{}, false
		}
		return true, strings.TrimSpace(terminated.Reason), terminated.ExitCode, parsed, true
	}
	return false, "", 0, time.Time{}, false
}

// jobLastKnownAliveAt is the anchor a restart's finishedAt must fall at or
// after to be attributed to this job, rather than a stale restart from long
// before it started. LastAliveAt is the more precise signal (the supervisor's
// own last heartbeat); StartedAt is the fallback for a job that never beat.
func jobLastKnownAliveAt(job EnvironmentJob) time.Time {
	if !job.LastAliveAt.IsZero() {
		return job.LastAliveAt
	}
	return job.StartedAt
}

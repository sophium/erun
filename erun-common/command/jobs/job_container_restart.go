package jobs

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/seam"
	"github.com/sophium/erun/erun-common/infra/kube"
	"github.com/sophium/erun/erun-common/primitive/open"
	"github.com/sophium/erun/erun-common/primitive/shared/jobstate"
)

// jobSupervisorContainerRestart reports this pod's own container restart.
// ok=false means "could not check", never "did not restart".
func jobSupervisorContainerRestart(podName, containerName string, runner seam.OpenKubectlRunnerFunc) (restarted bool, reason string, exitCode int, finishedAt time.Time, ok bool) {
	podName = strings.TrimSpace(podName)
	if podName == "" {
		return false, "", 0, time.Time{}, false
	}
	if config.CurrentExecutionMode(kube.KubectlPodGetExecutionOperation) == config.ExecutionModeLibrary {
		return libraryJobSupervisorContainerRestart(podName, containerName)
	}
	return defaultJobSupervisorContainerRestart(podName, containerName, runner)
}

// defaultJobSupervisorContainerRestart is the kubectl subprocess path.
func defaultJobSupervisorContainerRestart(podName, containerName string, runner seam.OpenKubectlRunnerFunc) (restarted bool, reason string, exitCode int, finishedAt time.Time, ok bool) {
	if runner == nil {
		return false, "", 0, time.Time{}, false
	}
	var stdout, stderr bytes.Buffer
	if err := runner(kube.KubectlGetPodArgs(podName), &stdout, &stderr); err != nil {
		return false, "", 0, time.Time{}, false
	}
	var pod open.RuntimePodDiagnostic
	if err := json.Unmarshal(stdout.Bytes(), &pod); err != nil {
		return false, "", 0, time.Time{}, false
	}
	for _, container := range pod.Status.ContainerStatuses {
		if container.Name != containerName {
			continue
		}
		terminated := container.LastState.Terminated
		if terminated == nil {
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

// libraryJobSupervisorContainerRestart is the client-go path.
func libraryJobSupervisorContainerRestart(podName, containerName string) (restarted bool, reason string, exitCode int, finishedAt time.Time, ok bool) {
	pod, err := kube.LibraryGetPod(podName)
	if err != nil {
		return false, "", 0, time.Time{}, false
	}
	for _, container := range pod.Status.ContainerStatuses {
		if container.Name != containerName {
			continue
		}
		terminated := container.LastTerminationState.Terminated
		if terminated == nil {
			return false, "", 0, time.Time{}, true
		}
		return true, strings.TrimSpace(terminated.Reason), int(terminated.ExitCode), terminated.FinishedAt.Time, true
	}
	return false, "", 0, time.Time{}, false
}

// jobLastKnownAliveAt is the earliest time a restart can be attributed to this job.
func jobLastKnownAliveAt(job jobstate.EnvironmentJob) time.Time {
	if !job.LastAliveAt.IsZero() {
		return job.LastAliveAt
	}
	return job.StartedAt
}

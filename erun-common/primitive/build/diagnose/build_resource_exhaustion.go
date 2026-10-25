package diagnose

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam/kubeapi"
	"github.com/sophium/erun/erun-common/infra/kube"
	"github.com/sophium/erun/erun-common/primitive/build/jobhost"
	"github.com/sophium/erun/erun-common/primitive/shared/opentarget"
	corev1 "k8s.io/api/core/v1"
)

// Memory markers count only inside BuildKit's own failure framing, never in step output.
var dockerBuildMemoryFramingMarkers = []string{
	"cannot allocate memory",
	"resourceexhausted",
}

// os/exec renders SIGKILL from a cancelled context identically, so this marker counts only
// when a process the build itself owns reports the kill.
const dockerBuildSignalKilledMarker = "signal: killed"

// 128 + SIGKILL(9): a step ended by a signal rather than by its own status.
const dockerBuildStepSigkillExitCode = 137

// Matches BuildKit's `did not complete successfully: exit code: N` step framing.
var buildkitStepExitCodePattern = regexp.MustCompile(`exit code: (\d+)`)

// dockerBuildFramingFailureText returns BuildKit's ERROR and failed-to-solve lines, stripping
// step output: test logs routinely print strings that look like infrastructure diagnoses.
func dockerBuildFramingFailureText(output string) string {
	var framing strings.Builder
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(strings.TrimRight(line, "\r"))
		if trimmed == "" {
			continue
		}
		if match := buildkitStepErrorPattern.FindStringSubmatch(trimmed); match != nil {
			framing.WriteString(match[2])
			framing.WriteString("\n")
			continue
		}
		if strings.HasPrefix(trimmed, "ERROR:") || strings.HasPrefix(trimmed, "error:") {
			framing.WriteString(trimmed)
			framing.WriteString("\n")
		}
	}
	return framing.String()
}

// buildkitStepExitCode returns the last step exit status in BuildKit's framing; ok is false without one.
func buildkitStepExitCode(output string) (code int, ok bool) {
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(strings.TrimRight(line, "\r"))
		if trimmed == "" {
			continue
		}
		if match := buildkitStepErrorPattern.FindStringSubmatch(trimmed); match != nil {
			trimmed = match[2]
		} else if !strings.HasPrefix(trimmed, "ERROR:") && !strings.HasPrefix(trimmed, "error:") {
			continue
		}
		codeMatch := buildkitStepExitCodePattern.FindStringSubmatch(trimmed)
		if codeMatch == nil {
			continue
		}
		parsed, err := strconv.Atoi(codeMatch[1])
		if err != nil {
			continue
		}
		code, ok = parsed, true
	}
	return code, ok
}

// dockerBuildProcessExitSignal names the signal that ended `docker build` itself, or "".
func dockerBuildProcessExitSignal(err error) string {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return ""
	}
	return core.EnvironmentJobExitSignal(exitErr.ProcessState)
}

// dockerBuildResourceExhaustionEvidence reports a resource kill only when the build's own
// processes establish it; a marker in step output alone is never enough.
func dockerBuildResourceExhaustionEvidence(output string, buildErr error) (string, bool) {
	// SIGTERM is a cancellation or timeout, not the kernel reclaiming memory.
	if signal := dockerBuildProcessExitSignal(buildErr); signal == syscall.SIGKILL.String() {
		return fmt.Sprintf("signal %q terminated the docker build process itself", signal), true
	}
	framing := strings.ToLower(dockerBuildFramingFailureText(output))
	for _, marker := range dockerBuildMemoryFramingMarkers {
		if strings.Contains(framing, marker) {
			return fmt.Sprintf("BuildKit reported %q for the step that failed", marker), true
		}
	}
	if code, ok := buildkitStepExitCode(output); ok && code == dockerBuildStepSigkillExitCode &&
		strings.Contains(strings.ToLower(output), dockerBuildSignalKilledMarker) {
		return fmt.Sprintf("BuildKit reported exit code %d for the step that failed and %q appears in its output", code, dockerBuildSignalKilledMarker), true
	}
	return "", false
}

// DockerBuildResourceExhaustionDiagnosis returns a headline for an established resource kill,
// naming the in-pod build container and its memory limit when read back from Kubernetes.
func DockerBuildResourceExhaustionDiagnosis(output string, buildErr error) (string, bool) {
	return dockerBuildResourceExhaustionDiagnosisWithRunner(output, buildErr, os.Getenv, opentarget.RunOpenKubectl)
}

func dockerBuildResourceExhaustionDiagnosisWithRunner(output string, buildErr error, getenv func(string) string, runner kubeapi.OpenKubectlRunnerFunc) (string, bool) {
	evidence, ok := dockerBuildResourceExhaustionEvidence(output, buildErr)
	if !ok {
		return "", false
	}
	diagnosis := fmt.Sprintf("docker build was killed by resource exhaustion (%s), not by a defect in the code it built", evidence)
	if container, limit, ok := dockerBuildBlamedContainer(getenv, runner); ok {
		if limit != "" {
			diagnosis += fmt.Sprintf("; every image build in this pod runs in the %s container, whose configured memory limit is %s", container, limit)
		} else {
			diagnosis += fmt.Sprintf("; every image build in this pod runs in the %s container", container)
		}
	}
	return diagnosis, true
}

// dockerBuildBlamedContainer names the in-pod dind container and best-effort reads its memory
// limit; ok is false off-pod or when the container is not in this pod's spec.
func dockerBuildBlamedContainer(getenv func(string) string, runner kubeapi.OpenKubectlRunnerFunc) (container, limit string, ok bool) {
	if getenv == nil || strings.TrimSpace(getenv("KUBERNETES_SERVICE_HOST")) == "" {
		return "", "", false
	}
	podName := jobhost.CurrentJobHostname()
	if podName == "" {
		return "", "", false
	}
	memory, found := dockerBuildContainerMemoryLimit(podName, model.RuntimeDindContainerName, runner)
	if !found {
		return "", "", false
	}
	return model.RuntimeDindContainerName, memory, true
}

type dockerBuildPodSpecDiagnostic struct {
	Spec struct {
		Containers []struct {
			Name      string `json:"name"`
			Resources struct {
				Limits struct {
					Memory string `json:"memory"`
				} `json:"limits"`
			} `json:"resources"`
		} `json:"containers"`
	} `json:"spec"`
}

// dockerBuildContainerMemoryLimit reads a container's memory limit from this pod's spec; found is false when untrustworthy.
func dockerBuildContainerMemoryLimit(podName, containerName string, runner kubeapi.OpenKubectlRunnerFunc) (limit string, found bool) {
	podName = strings.TrimSpace(podName)
	if podName == "" {
		return "", false
	}
	if config.CurrentExecutionMode(kube.KubectlPodGetExecutionOperation) == config.ExecutionModeLibrary {
		return LibraryDockerBuildContainerMemoryLimit(podName, containerName)
	}
	return defaultDockerBuildContainerMemoryLimit(podName, containerName, runner)
}

func defaultDockerBuildContainerMemoryLimit(podName, containerName string, runner kubeapi.OpenKubectlRunnerFunc) (limit string, found bool) {
	if runner == nil {
		return "", false
	}
	var stdout, stderr bytes.Buffer
	if err := runner(kube.KubectlGetPodArgs(podName), &stdout, &stderr); err != nil {
		return "", false
	}
	var pod dockerBuildPodSpecDiagnostic
	if err := json.Unmarshal(stdout.Bytes(), &pod); err != nil {
		return "", false
	}
	for _, container := range pod.Spec.Containers {
		if container.Name != containerName {
			continue
		}
		limit = strings.TrimSpace(container.Resources.Limits.Memory)
		return limit, limit != ""
	}
	return "", false
}

// LibraryDockerBuildContainerMemoryLimit is the client-go variant of defaultDockerBuildContainerMemoryLimit.
func LibraryDockerBuildContainerMemoryLimit(podName, containerName string) (limit string, found bool) {
	pod, err := kube.LibraryGetPod(podName)
	if err != nil {
		return "", false
	}
	for _, container := range pod.Spec.Containers {
		if container.Name != containerName {
			continue
		}
		quantity, ok := container.Resources.Limits[corev1.ResourceMemory]
		if !ok {
			return "", false
		}
		limit = strings.TrimSpace(quantity.String())
		return limit, limit != ""
	}
	return "", false
}

// DockerBuildResourceExhaustionError is a `docker build` failure killed for memory. Error() is
// the diagnosis alone; the raw output was already streamed.
type DockerBuildResourceExhaustionError struct {
	Diagnosis string
	Err       error
}

func (e DockerBuildResourceExhaustionError) Error() string {
	return e.Diagnosis
}

func (e DockerBuildResourceExhaustionError) Unwrap() error {
	return e.Err
}

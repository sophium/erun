package eruncommon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

// dockerBuildResourceExhaustionMarkers are the tells that a build step's own
// process was killed by the kernel or BuildKit running out of memory, not a
// defect in the code it built. "signal: killed" is what a Go compiler reports
// when SIGKILLed -- an OOM kill, almost always, since nothing else routinely
// sends a bare SIGKILL -- and it is easy to miss: it lands inside a
// parenthetical next to what otherwise reads as a real import error, with the
// "cannot allocate memory" / "ResourceExhausted" line that actually explains
// it dozens of lines further down, behind a BuildKit frame.
var dockerBuildResourceExhaustionMarkers = []string{
	"cannot allocate memory",
	"resourceexhausted",
	"signal: killed",
}

// dockerBuildResourceExhaustionDiagnosis inspects a failed `docker build`'s
// captured output for those tells and, when found, returns a headline that
// names the real cause -- and, when this process is itself running inside an
// erun runtime pod, the container that actually ran the build and its
// configured memory limit, read back from the Kubernetes API rather than
// assumed. ok is false when none of the markers appear, in which case the
// caller returns the plain build error unchanged.
func dockerBuildResourceExhaustionDiagnosis(output string) (string, bool) {
	return dockerBuildResourceExhaustionDiagnosisWithRunner(output, os.Getenv, runOpenKubectl)
}

// dockerBuildResourceExhaustionDiagnosisWithRunner is
// dockerBuildResourceExhaustionDiagnosis with the environment lookup and
// Kubernetes call injectable, so a test can simulate being in-pod (or not)
// and supply a fake pod response instead of shelling out to a real kubectl.
func dockerBuildResourceExhaustionDiagnosisWithRunner(output string, getenv func(string) string, runner openKubectlRunnerFunc) (string, bool) {
	lower := strings.ToLower(output)
	matched := ""
	for _, marker := range dockerBuildResourceExhaustionMarkers {
		if strings.Contains(lower, marker) {
			matched = marker
			break
		}
	}
	if matched == "" {
		return "", false
	}
	diagnosis := fmt.Sprintf("docker build was killed by resource exhaustion (%q appears in its output below), not by a defect in the code it built", matched)
	if container, limit, ok := dockerBuildBlamedContainer(getenv, runner); ok {
		if limit != "" {
			diagnosis += fmt.Sprintf("; every image build in this pod runs in the %s container, whose configured memory limit is %s", container, limit)
		} else {
			diagnosis += fmt.Sprintf("; every image build in this pod runs in the %s container", container)
		}
	}
	return diagnosis, true
}

// dockerBuildBlamedContainer names the container that actually ran the build
// when this process is running inside an erun runtime pod with a dind
// sidecar, and best-effort resolves its configured memory limit from the
// Kubernetes API. ok is false off-pod (a plain workstation build has no
// sidecar to blame) or when the container cannot be found in this pod's own
// spec -- this never guesses a container or a limit it did not read back.
func dockerBuildBlamedContainer(getenv func(string) string, runner openKubectlRunnerFunc) (container, limit string, ok bool) {
	if getenv == nil || strings.TrimSpace(getenv("KUBERNETES_SERVICE_HOST")) == "" {
		return "", "", false
	}
	podName := currentJobHostname()
	if podName == "" {
		return "", "", false
	}
	memory, found := dockerBuildContainerMemoryLimit(podName, runtimeDindContainerName, runner)
	if !found {
		return "", "", false
	}
	return runtimeDindContainerName, memory, true
}

// dockerBuildPodSpecDiagnostic is the minimal `kubectl get pod -o json` shape
// dockerBuildContainerMemoryLimit needs -- the pod's declared spec, not its
// observed status (runtimePodDiagnostic in open_runtime_diagnostics.go covers
// that side already).
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

// dockerBuildContainerMemoryLimit reads one named container's configured
// memory limit from this pod's own spec. found is false whenever the answer
// cannot be trusted: kubectl unavailable, the API call failed, or the
// container is absent from the response. Dispatches to the subprocess or
// library path per the kubectl-pod-get execution mode (see
// execution_mode.go).
func dockerBuildContainerMemoryLimit(podName, containerName string, runner openKubectlRunnerFunc) (limit string, found bool) {
	podName = strings.TrimSpace(podName)
	if podName == "" {
		return "", false
	}
	if currentExecutionMode(kubectlPodGetExecutionOperation) == ExecutionModeLibrary {
		return libraryDockerBuildContainerMemoryLimit(podName, containerName)
	}
	return defaultDockerBuildContainerMemoryLimit(podName, containerName, runner)
}

// defaultDockerBuildContainerMemoryLimit is the subprocess-backed path
// dockerBuildContainerMemoryLimit dispatches to by default.
func defaultDockerBuildContainerMemoryLimit(podName, containerName string, runner openKubectlRunnerFunc) (limit string, found bool) {
	if runner == nil {
		return "", false
	}
	var stdout, stderr bytes.Buffer
	if err := runner(kubectlGetPodArgs(podName), &stdout, &stderr); err != nil {
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

// libraryDockerBuildContainerMemoryLimit is the library-backed alternative to
// defaultDockerBuildContainerMemoryLimit, resolving the same pod via
// k8s.io/client-go instead of shelling out to kubectl.
func libraryDockerBuildContainerMemoryLimit(podName, containerName string) (limit string, found bool) {
	pod, err := libraryGetPod(podName)
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

// DockerBuildResourceExhaustionError wraps a failed `docker build` whose
// output showed the process was killed for memory rather than failing on its
// own merits. Its Error() is the diagnosis alone -- the raw build output was
// already streamed to stderr before this wraps it -- so the top-level error a
// caller prints states the real cause instead of "exit status 1".
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

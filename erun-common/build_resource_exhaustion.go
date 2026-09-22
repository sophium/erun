package eruncommon

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

	corev1 "k8s.io/api/core/v1"
)

// dockerBuildMemoryFramingMarkers are the phrases BuildKit itself uses when the
// step it ran could not get memory: "cannot allocate memory" from the kernel
// refusing an allocation, "ResourceExhausted" from BuildKit's own solver. They
// are evidence only inside the build's own failure framing -- see
// dockerBuildFramingFailureText for why the step's own output cannot be.
var dockerBuildMemoryFramingMarkers = []string{
	"cannot allocate memory",
	"resourceexhausted",
}

// dockerBuildSignalKilledMarker is what os/exec renders for any child a SIGKILL
// ended. On its own it is not evidence of anything about the environment:
// os/exec sends SIGKILL itself whenever a caller cancels an exec.CommandContext
// context, so the code under test emits this exact string for its own reasons.
// A test that cancels its own context logs "fetchError=... signal: killed:
// error=... context canceled" and the build around it then runs to completion
// and fails on a real defect. The marker counts only once a process the build
// itself owns reports the kill (see dockerBuildResourceExhaustionEvidence).
const dockerBuildSignalKilledMarker = "signal: killed"

// dockerBuildStepSigkillExitCode is 128 + SIGKILL(9): what BuildKit reports for
// a step whose own process a signal ended, as opposed to a step that ran and
// returned a status of its own choosing.
const dockerBuildStepSigkillExitCode = 137

// buildkitStepExitCodePattern pulls the exit status out of the failure framing
// BuildKit prints for the step that stopped the build, which reads
// `ERROR: process "/bin/sh -c make check && touch /test-ok" did not complete
// successfully: exit code: 2` under BuildKit's own step-number prefix.
var buildkitStepExitCodePattern = regexp.MustCompile(`exit code: (\d+)`)

// dockerBuildFramingFailureText returns what BuildKit says about the failure --
// its per-step ERROR lines and its failed-to-solve line -- with every line the
// step itself printed stripped out.
//
// The distinction is the whole point. BuildKit's framing is the build
// infrastructure talking about a process it ran and how that process ended.
// Step output is the log of the code under test, which is user-controlled,
// arrives under BuildKit's "#N <elapsed> " prefix rather than an ERROR token,
// and will keep producing strings that look like infrastructure diagnoses --
// "signal: killed" from a cancelled context, a gRPC RESOURCE_EXHAUSTED a test
// asserts on -- while the build fails for an unrelated reason. Reading the
// whole captured output as if it were BuildKit's own words is how a real test
// failure gets reported to the operator as a resource problem, sending them to
// resize a dind that was never the problem.
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

// buildkitStepExitCode returns the exit status BuildKit reported for the step
// that stopped the build -- the last such status in the framing, matching which
// step dockerBuildFailureReason reads the failure from. ok is false when the
// output carries no such framing, in which case nothing is known about how that
// step ended.
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

// dockerBuildProcessExitSignal names the signal that ended the `docker build`
// process itself, or "" when it was not terminated by one. Windows has no
// equivalent (a terminated process reports a code, not a signal), which is why
// this defers to the platform-split helper the job supervisor already owns
// rather than growing a second one.
func dockerBuildProcessExitSignal(err error) string {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return ""
	}
	return environmentJobExitSignal(exitErr.ProcessState)
}

// dockerBuildResourceExhaustionEvidence decides whether the build's own
// processes establish a resource kill, and returns the half of the story that
// text cannot supply.
//
// Both halves are required, the same way dockerBuildNetworkDiagnosis requires a
// stall and a measured mismatch: a marker in the log alone has a dozen innocent
// explanations, and an environment condition alone has been observed to mask a
// real defect. ok is false whenever the evidence does not reach that bar --
// including when "signal: killed" appears but only in the code under test's own
// output, which is the case this function exists to stop misreporting. Declining
// is deliberate: the caller then keeps the ordinary build failure reason, which
// names the step that actually failed and the status it returned.
func dockerBuildResourceExhaustionEvidence(output string, buildErr error) (string, bool) {
	// A SIGKILL of the `docker build` process itself. SIGTERM is deliberately
	// excluded: it is a cancellation or a timeout, not the kernel reclaiming
	// memory, and reporting it as exhaustion is the same misattribution one
	// level up.
	if signal := dockerBuildProcessExitSignal(buildErr); signal == syscall.SIGKILL.String() {
		return fmt.Sprintf("signal %q terminated the docker build process itself", signal), true
	}
	framing := strings.ToLower(dockerBuildFramingFailureText(output))
	for _, marker := range dockerBuildMemoryFramingMarkers {
		if strings.Contains(framing, marker) {
			return fmt.Sprintf("BuildKit reported %q for the step that failed", marker), true
		}
	}
	// BuildKit's own record that the step's process was ended by a signal,
	// corroborated by the marker saying which one. A step whose own process the
	// kernel killed cannot have chosen its exit status, so this is a kill the
	// build infrastructure observed rather than text the code printed.
	if code, ok := buildkitStepExitCode(output); ok && code == dockerBuildStepSigkillExitCode &&
		strings.Contains(strings.ToLower(output), dockerBuildSignalKilledMarker) {
		return fmt.Sprintf("BuildKit reported exit code %d for the step that failed and %q appears in its output", code, dockerBuildSignalKilledMarker), true
	}
	return "", false
}

// dockerBuildResourceExhaustionDiagnosis inspects a failed `docker build`'s
// captured output *and its own process outcome*, and when the two together
// establish a resource kill, returns a headline that names that cause -- and,
// when this process is itself running inside an erun runtime pod, the container
// that actually ran the build and its configured memory limit, read back from
// the Kubernetes API rather than assumed. ok is false when the evidence does
// not establish one, in which case the caller returns the plain build failure
// reason unchanged.
func dockerBuildResourceExhaustionDiagnosis(output string, buildErr error) (string, bool) {
	return dockerBuildResourceExhaustionDiagnosisWithRunner(output, buildErr, os.Getenv, runOpenKubectl)
}

// dockerBuildResourceExhaustionDiagnosisWithRunner is
// dockerBuildResourceExhaustionDiagnosis with the environment lookup and
// Kubernetes call injectable, so a test can simulate being in-pod (or not)
// and supply a fake pod response instead of shelling out to a real kubectl.
func dockerBuildResourceExhaustionDiagnosisWithRunner(output string, buildErr error, getenv func(string) string, runner openKubectlRunnerFunc) (string, bool) {
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

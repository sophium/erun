package eruncommon

import (
	"bytes"
	"fmt"
	"io"
	"strings"
)

const runtimeDindContainerName = "erun-dind"

type (
	RuntimeContainerCommandRunnerFunc func(ShellLaunchParams, string, string) (RemoteCommandResult, error)
	DoctorAction                      string
)

const (
	DoctorActionPruneImages     DoctorAction = "prune_images"
	DoctorActionPruneBuildCache DoctorAction = "prune_build_cache"
	DoctorActionPruneContainers DoctorAction = "prune_containers"
)

type DoctorInspectionResult struct {
	Daemon DoctorDockerDaemon
	Stdout string
	Stderr string
}

func ResolveDoctorTarget(store OpenStore, params OpenParams) (OpenResult, error) {
	return ResolveOpen(store, params)
}

func DoctorActions() []DoctorAction {
	return []DoctorAction{
		DoctorActionPruneImages,
		DoctorActionPruneBuildCache,
		DoctorActionPruneContainers,
	}
}

func DoctorActionPromptLabel(action DoctorAction, result OpenResult) string {
	target := result.Tenant + "/" + result.Environment
	switch action {
	case DoctorActionPruneImages:
		return fmt.Sprintf("Prune unused Docker images in %s?", target)
	case DoctorActionPruneBuildCache:
		return fmt.Sprintf("Prune unused BuildKit cache in %s?", target)
	case DoctorActionPruneContainers:
		return fmt.Sprintf("Prune stopped Docker containers in %s?", target)
	default:
		return fmt.Sprintf("Run doctor action %q in %s?", action, target)
	}
}

func DoctorActionDescription(action DoctorAction) string {
	switch action {
	case DoctorActionPruneImages:
		return "Remove unused Docker images"
	case DoctorActionPruneBuildCache:
		return "Remove unused BuildKit cache"
	case DoctorActionPruneContainers:
		return "Remove stopped Docker containers"
	default:
		return string(action)
	}
}

// RunDoctorInspection reads the environment's docker storage: disk and inode
// usage at the daemon's root, and what the daemon's own stores reclaim. The
// daemon is resolved from the environment (ResolveDoctorDockerDaemon) and is
// named in the result, so the caller can say which daemon every figure below it
// came from; an environment with no daemon holding build images refuses with
// DoctorDockerDaemonUnavailableError rather than reading whatever daemon
// happens to be reachable.
func RunDoctorInspection(ctx Context, runner RuntimeContainerCommandRunnerFunc, req ShellLaunchParams) (DoctorInspectionResult, error) {
	daemon := ResolveDoctorDockerDaemon(req)
	if daemon.Unavailable() {
		return DoctorInspectionResult{Daemon: daemon}, DoctorDockerDaemonUnavailableError{Daemon: daemon}
	}
	if err := waitForDoctorDaemon(ctx, req, daemon); err != nil {
		return DoctorInspectionResult{Daemon: daemon}, err
	}
	result, err := runDoctorDockerSteps(ctx, runner, req, daemon, "doctor-inspect", doctorInspectionSteps())
	return DoctorInspectionResult{Daemon: daemon, Stdout: result.Stdout, Stderr: result.Stderr}, err
}

// RunDoctorAction prunes the daemon that actually holds this environment's
// build images, through the transport that daemon is reachable by. It returns
// the daemon beside the output so the caller can name it, and the store
// readings taken around the prune so a prune that freed nothing can be reported
// as that instead of as a success.
func RunDoctorAction(ctx Context, runner RuntimeContainerCommandRunnerFunc, req ShellLaunchParams, action DoctorAction) (DoctorDockerResult, error) {
	daemon := ResolveDoctorDockerDaemon(req)
	if daemon.Unavailable() {
		return DoctorDockerResult{Daemon: daemon}, DoctorDockerDaemonUnavailableError{Daemon: daemon}
	}
	steps, err := doctorActionSteps(action)
	if err != nil {
		return DoctorDockerResult{Daemon: daemon}, err
	}
	if err := waitForDoctorDaemon(ctx, req, daemon); err != nil {
		return DoctorDockerResult{Daemon: daemon}, err
	}
	return runDoctorDockerSteps(ctx, runner, req, daemon, "doctor-"+string(action), steps)
}

// waitForDoctorDaemon waits for the pod a pod-hosted daemon lives in to be
// available before exec'ing into it. A daemon this process reaches directly
// (the host kind) needs no wait and no cluster: waiting on a deployment a host
// environment does not have would fail the read for a reason that is not about
// the daemon at all.
func waitForDoctorDaemon(ctx Context, req ShellLaunchParams, daemon DoctorDockerDaemon) error {
	if daemon.Kind == DoctorDockerDaemonHost {
		return nil
	}
	return traceAndWaitForRuntime(ctx, req)
}

func traceAndWaitForRuntime(ctx Context, req ShellLaunchParams) error {
	args := kubectlDeploymentWaitArgs(req)
	ctx.TraceCommand("", "kubectl", args...)
	if ctx.DryRun {
		return nil
	}
	if currentExecutionMode(kubectlDeploymentWaitExecutionOperation) == ExecutionModeLibrary {
		if err := libraryWaitForDeploymentAvailable(req.KubernetesContext, req.Namespace, RuntimeReleaseName(req.Tenant), defaultShellLaunchWaitTimeout); err != nil {
			// normalizeDoctorKubectlError's diagnostics (doctorKubectlDiagnostic)
			// pattern-match literal kubectl CLI stderr text, which a client-go
			// error never produces, so library-mode failures return unadorned
			// rather than trying to force them through that string matching.
			return err
		}
		return nil
	}
	stderr, err := runDoctorKubectl(args, io.Discard)
	if err == nil {
		return nil
	}
	return normalizeDoctorKubectlError(req, stderr, err)
}

func PreviewRuntimeContainerCommand(req ShellLaunchParams, container, script string) RemoteCommandPreview {
	return RemoteCommandPreview{
		Args:   kubectlContainerExecArgs(req, container, script),
		Script: script,
	}
}

func RunTracedRuntimeContainerCommand(ctx Context, runner RuntimeContainerCommandRunnerFunc, req ShellLaunchParams, container, label, script string) (RemoteCommandResult, error) {
	preview := PreviewRuntimeContainerCommand(req, container, script)
	traceArgs := append([]string{}, preview.Args...)
	if len(traceArgs) > 0 {
		traceArgs[len(traceArgs)-1] = "<remote-script>"
	}
	ctx.TraceCommand("", "kubectl", traceArgs...)
	ctx.TraceBlock(label, script)
	if ctx.DryRun {
		return RemoteCommandResult{}, nil
	}
	if runner == nil {
		runner = RunRuntimeContainerCommand
	}
	result, err := runner(req, container, script)
	if err != nil {
		return result, normalizeDoctorKubectlError(req, result.Stderr, err)
	}
	return result, nil
}

func RunRuntimeContainerCommand(req ShellLaunchParams, container, script string) (RemoteCommandResult, error) {
	cmd := Command("kubectl", kubectlContainerExecArgs(req, container, script)...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return RemoteCommandResult{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}, err
}

func runDoctorKubectl(args []string, stdout io.Writer) (string, error) {
	cmd := Command("kubectl", args...)
	if stdout != nil {
		cmd.Stdout = stdout
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stderr.String(), err
}

func normalizeDoctorKubectlError(req ShellLaunchParams, stderr string, err error) error {
	diagnostic := doctorKubectlDiagnostic(req, stderr)
	stderr = strings.TrimSpace(stderr)
	if diagnostic == "" {
		// No recognized cause, but kubectl's own stderr was still captured --
		// include it rather than falling back to the bare "%w" (which renders
		// as content-free "exit status N") that this branch used to return.
		if stderr == "" {
			return err
		}
		return fmt.Errorf("%w: %s", err, stderr)
	}
	if stderr == "" {
		return fmt.Errorf("%s: %w", diagnostic, err)
	}
	return fmt.Errorf("%s: %s", diagnostic, stderr)
}

func doctorKubectlDiagnostic(req ShellLaunchParams, stderr string) string {
	trimmed := strings.TrimSpace(stderr)
	lower := strings.ToLower(trimmed)
	switch {
	case strings.Contains(lower, "disk i/o error") || strings.Contains(lower, "input/output error"):
		return fmt.Sprintf("runtime access failed because local Kubernetes storage for %s/%s is unhealthy", req.Tenant, req.Environment)
	case doctorNamespaceLookupFailed(req, trimmed) && doctorNamespaceIsListed(req):
		return fmt.Sprintf("runtime namespace %q is listed, but direct Kubernetes API access is failing", req.Namespace)
	default:
		return ""
	}
}

func doctorNamespaceLookupFailed(req ShellLaunchParams, stderr string) bool {
	namespace := strings.TrimSpace(req.Namespace)
	return namespace != "" && strings.Contains(stderr, fmt.Sprintf("namespaces %q not found", namespace))
}

func doctorNamespaceIsListed(req ShellLaunchParams) bool {
	args := make([]string, 0, 6)
	if strings.TrimSpace(req.KubernetesContext) != "" {
		args = append(args, "--context", strings.TrimSpace(req.KubernetesContext))
	}
	args = append(args, "get", "namespaces", "-o", "name")
	var stdout bytes.Buffer
	_, err := runDoctorKubectl(args, &stdout)
	if err != nil {
		return false
	}
	want := "namespace/" + strings.TrimSpace(req.Namespace)
	for _, line := range strings.Split(stdout.String(), "\n") {
		if strings.TrimSpace(line) == want {
			return true
		}
	}
	return false
}

func kubectlContainerExecArgs(req ShellLaunchParams, container, script string) []string {
	args := kubectlTargetArgs(req)
	args = append(args, "exec", "-c", strings.TrimSpace(container))
	args = append(args, "deployment/"+RuntimeReleaseName(req.Tenant), "--", "/bin/sh", "-lc", script)
	return args
}

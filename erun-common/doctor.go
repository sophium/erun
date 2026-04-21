package eruncommon

import (
	"bytes"
	"fmt"
	"io"
	"os/exec"
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

func RunDoctorInspection(ctx Context, runner RuntimeContainerCommandRunnerFunc, req ShellLaunchParams) (DoctorInspectionResult, error) {
	if err := traceAndWaitForRuntime(ctx, req); err != nil {
		return DoctorInspectionResult{}, err
	}
	result, err := RunTracedRuntimeContainerCommand(ctx, runner, req, runtimeDindContainerName, "doctor-inspect", doctorInspectionScript())
	return DoctorInspectionResult(result), err
}

func RunDoctorAction(ctx Context, runner RuntimeContainerCommandRunnerFunc, req ShellLaunchParams, action DoctorAction) (RemoteCommandResult, error) {
	if err := traceAndWaitForRuntime(ctx, req); err != nil {
		return RemoteCommandResult{}, err
	}
	script, err := doctorActionScript(action)
	if err != nil {
		return RemoteCommandResult{}, err
	}
	return RunTracedRuntimeContainerCommand(ctx, runner, req, runtimeDindContainerName, "doctor-"+string(action), script)
}

func traceAndWaitForRuntime(ctx Context, req ShellLaunchParams) error {
	args := kubectlDeploymentWaitArgs(req)
	ctx.TraceCommand("", "kubectl", args...)
	if ctx.DryRun {
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
	cmd := exec.Command("kubectl", kubectlContainerExecArgs(req, container, script)...)
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
	cmd := exec.Command("kubectl", args...)
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
	if diagnostic == "" {
		return err
	}
	stderr = strings.TrimSpace(stderr)
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

func doctorInspectionScript() string {
	return strings.Join([]string{
		"set -eu",
		"printf '== Disk usage (/var/lib/docker) ==\\n'",
		"df -h /var/lib/docker",
		"printf '\\n== Inode usage (/var/lib/docker) ==\\n'",
		"df -i /var/lib/docker",
		"printf '\\n== Docker system df ==\\n'",
		"docker system df",
	}, "\n")
}

func doctorActionScript(action DoctorAction) (string, error) {
	switch action {
	case DoctorActionPruneImages:
		return strings.Join([]string{
			"set -eu",
			"docker image prune -a -f",
			"docker system df",
		}, "\n"), nil
	case DoctorActionPruneBuildCache:
		return strings.Join([]string{
			"set -eu",
			"docker builder prune -a -f",
			"docker system df",
		}, "\n"), nil
	case DoctorActionPruneContainers:
		return strings.Join([]string{
			"set -eu",
			"docker container prune -f",
			"docker system df",
		}, "\n"), nil
	default:
		return "", fmt.Errorf("unsupported doctor action %q", action)
	}
}

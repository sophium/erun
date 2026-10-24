package eruncommon

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/kube"
)

const RuntimeDindContainerName = "erun-dind"

type (
	RuntimeContainerCommandRunnerFunc func(config.ShellLaunchParams, string, string) (kube.RemoteCommandResult, error)
)

func ResolveDoctorTarget(store config.OpenStore, params OpenParams) (config.OpenResult, error) {
	return ResolveOpen(store, params)
}

func TraceAndWaitForRuntime(ctx core.Context, req config.ShellLaunchParams) error {
	args := KubectlDeploymentWaitArgs(req)
	ctx.TraceCommand("", "kubectl", args...)
	if ctx.DryRun {
		return nil
	}
	if config.CurrentExecutionMode(kube.KubectlDeploymentWaitExecutionOperation) == config.ExecutionModeLibrary {
		if err := kube.LibraryWaitForDeploymentAvailable(req.KubernetesContext, req.Namespace, config.RuntimeReleaseName(req.Tenant), DefaultShellLaunchWaitTimeout); err != nil {
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
	return NormalizeDoctorKubectlError(req, stderr, err)
}

func PreviewRuntimeContainerCommand(req config.ShellLaunchParams, container, script string) kube.RemoteCommandPreview {
	return kube.RemoteCommandPreview{
		Args:   kubectlContainerExecArgs(req, container, script),
		Script: script,
	}
}

func RunTracedRuntimeContainerCommand(ctx core.Context, runner RuntimeContainerCommandRunnerFunc, req config.ShellLaunchParams, container, label, script string) (kube.RemoteCommandResult, error) {
	preview := PreviewRuntimeContainerCommand(req, container, script)
	traceArgs := append([]string{}, preview.Args...)
	if len(traceArgs) > 0 {
		traceArgs[len(traceArgs)-1] = "<remote-script>"
	}
	ctx.TraceCommand("", "kubectl", traceArgs...)
	ctx.TraceBlock(label, script)
	if ctx.DryRun {
		return kube.RemoteCommandResult{}, nil
	}
	if runner == nil {
		runner = RunRuntimeContainerCommand
	}
	result, err := runner(req, container, script)
	if err != nil {
		return result, NormalizeDoctorKubectlError(req, result.Stderr, err)
	}
	return result, nil
}

func RunRuntimeContainerCommand(req config.ShellLaunchParams, container, script string) (kube.RemoteCommandResult, error) {
	cmd := core.Command("kubectl", kubectlContainerExecArgs(req, container, script)...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return kube.RemoteCommandResult{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}, err
}

func runDoctorKubectl(args []string, stdout io.Writer) (string, error) {
	cmd := core.Command("kubectl", args...)
	if stdout != nil {
		cmd.Stdout = stdout
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stderr.String(), err
}

func NormalizeDoctorKubectlError(req config.ShellLaunchParams, stderr string, err error) error {
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

func doctorKubectlDiagnostic(req config.ShellLaunchParams, stderr string) string {
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

func doctorNamespaceLookupFailed(req config.ShellLaunchParams, stderr string) bool {
	namespace := strings.TrimSpace(req.Namespace)
	return namespace != "" && strings.Contains(stderr, fmt.Sprintf("namespaces %q not found", namespace))
}

func doctorNamespaceIsListed(req config.ShellLaunchParams) bool {
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

func kubectlContainerExecArgs(req config.ShellLaunchParams, container, script string) []string {
	args := config.KubectlTargetArgs(req)
	args = append(args, "exec", "-c", strings.TrimSpace(container))
	args = append(args, "deployment/"+config.RuntimeReleaseName(req.Tenant), "--", "/bin/sh", "-lc", script)
	return args
}

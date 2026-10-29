package runtimewait

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/config/execmode"
	"github.com/sophium/erun/erun-common/base/config/runtimetarget"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/kube"
)

func TraceAndWaitForRuntime(ctx core.Context, req config.ShellLaunchParams) error {
	args := KubectlDeploymentWaitArgs(req)
	ctx.TraceCommand("", "kubectl", args...)
	if ctx.DryRun {
		return nil
	}
	if execmode.CurrentExecutionMode(kube.KubectlDeploymentWaitExecutionOperation) == execmode.ExecutionModeLibrary {
		if err := kube.LibraryWaitForDeploymentAvailable(req.KubernetesContext, req.Namespace, runtimetarget.RuntimeReleaseName(req.Tenant), DefaultShellLaunchWaitTimeout); err != nil {
			// The diagnostics match kubectl stderr text, which client-go errors never produce.
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

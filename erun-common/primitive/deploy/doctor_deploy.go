package deploy

import (
	"bytes"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

func helmStatusArgs(req config.ShellLaunchParams) []string {
	args := []string{"status", config.RuntimeReleaseName(req.Tenant)}
	if strings.TrimSpace(req.Namespace) != "" {
		args = append(args, "--namespace", req.Namespace)
	}
	if strings.TrimSpace(req.KubernetesContext) != "" {
		args = append(args, "--kube-context", req.KubernetesContext)
	}
	return args
}

func deployDiagnosisPodArgs(req config.ShellLaunchParams) []string {
	args := config.KubectlTargetArgs(req)
	return append(args, "get", "pods", "-o", "wide")
}

// RunDeployDiagnosis probes why a deploy may have failed. It is read-only, so it
// also runs under --dry-run.
func RunDeployDiagnosis(ctx core.Context, req config.ShellLaunchParams) model.DeployDiagnosisResult {
	helmArgs := helmStatusArgs(req)
	ctx.TraceCommand("", "helm", helmArgs...)
	podArgs := deployDiagnosisPodArgs(req)
	ctx.TraceCommand("", "kubectl", podArgs...)
	helmStatus, helmErr := runDoctorDiagnosisCommand("helm", helmArgs)
	// Resolve applicability first so every return distinguishes "not applicable" from "could not run".
	result := model.DeployDiagnosisResult{
		HelmStatus:       helmStatus,
		AgentCredentials: runtimeAgentCredentialApplicability(req),
	}
	if helmErr != nil && !IsHelmReleaseNotFound(helmStatus) {
		result.HelmReadError = ObserveHelmReadErrorMessage(config.RuntimeReleaseName(req.Tenant), req.Namespace, helmStatus, helmErr)
		result.ClusterUnreachable = kubernetesAPIServerUnreachableSignal(helmStatus)
	}
	if result.ClusterUnreachable {
		return result
	}
	pods, podsErr := runDoctorDiagnosisCommand("kubectl", podArgs)
	result.Pods = pods
	if podsErr != nil {
		result.ClusterUnreachable = kubernetesAPIServerUnreachableSignal(pods)
	}
	if result.ClusterUnreachable {
		return result
	}
	result.AgentCredentials = inspectRuntimeAgentCredentials(ctx, req)
	return result
}

// runDoctorDiagnosisCommand folds stderr into the output, which is itself
// diagnostic, and still returns the command's error.
func runDoctorDiagnosisCommand(name string, args []string) (string, error) {
	cmd := core.Command(name, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return strings.TrimSpace(out.String()), err
}

// RecommendedDeployRecovery picks one recovery: clearing pending and rolling back
// are alternatives, and running both would step back a further revision. It never
// recommends rollback when HelmReadError says the read is untrusted.
func RecommendedDeployRecovery(diagnosis model.DeployDiagnosisResult) (model.DeployRecoveryAction, bool) {
	if strings.TrimSpace(diagnosis.HelmReadError) != "" {
		return "", false
	}
	status := strings.ToLower(strings.TrimSpace(diagnosis.HelmStatus))
	switch {
	case status == "":
		return "", false
	case strings.Contains(status, "not found"):
		return "", false
	case strings.Contains(status, "pending-install"),
		strings.Contains(status, "pending-upgrade"),
		strings.Contains(status, "pending-rollback"):
		return model.DeployRecoveryClearPendingHelm, true
	case strings.Contains(status, "status: deployed"):
		return "", false
	default:
		return model.DeployRecoveryRollback, true
	}
}

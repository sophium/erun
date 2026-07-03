package eruncommon

import (
	"bytes"
	"fmt"
	"strings"
)

// DeployDiagnosisResult holds the deploy-failure diagnosis for the caller to interpret.
type DeployDiagnosisResult struct {
	HelmStatus string
	Pods       string
}

func helmStatusArgs(req ShellLaunchParams) []string {
	args := []string{"status", RuntimeReleaseName(req.Tenant)}
	if strings.TrimSpace(req.Namespace) != "" {
		args = append(args, "--namespace", req.Namespace)
	}
	if strings.TrimSpace(req.KubernetesContext) != "" {
		args = append(args, "--kube-context", req.KubernetesContext)
	}
	return args
}

func deployDiagnosisPodArgs(req ShellLaunchParams) []string {
	args := kubectlTargetArgs(req)
	return append(args, "get", "pods", "-o", "wide")
}

// RunDeployDiagnosis probes why a deploy may have failed. It is strictly
// read-only, so it is safe to run on every `erun doctor`; a missing release or
// unreachable cluster is itself part of the diagnosis, not a hard error.
func RunDeployDiagnosis(ctx Context, req ShellLaunchParams) DeployDiagnosisResult {
	helmArgs := helmStatusArgs(req)
	ctx.TraceCommand("", "helm", helmArgs...)
	podArgs := deployDiagnosisPodArgs(req)
	ctx.TraceCommand("", "kubectl", podArgs...)
	if ctx.DryRun {
		return DeployDiagnosisResult{}
	}
	return DeployDiagnosisResult{
		HelmStatus: runDoctorDiagnosisCommand("helm", helmArgs),
		Pods:       runDoctorDiagnosisCommand("kubectl", podArgs),
	}
}

// runDoctorDiagnosisCommand folds command errors into the returned output —
// `helm status` stderr on a missing release is itself diagnostic — so the
// caller always has something to show.
func runDoctorDiagnosisCommand(name string, args []string) string {
	cmd := Command(name, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	_ = cmd.Run()
	return strings.TrimSpace(out.String())
}

// DeployRecoveryAction names a mutating recovery `erun doctor` can run against a
// failing runtime release; because these mutate the live release, callers gate
// them behind a prompt or flag.
type DeployRecoveryAction string

const (
	// DeployRecoveryClearPendingHelm clears a stuck helm pending-install /
	// pending-upgrade / pending-rollback lock so the next deploy can proceed.
	DeployRecoveryClearPendingHelm DeployRecoveryAction = "clear_pending_helm"
	// DeployRecoveryRollback rolls the release back to its previous (last
	// successfully deployed) revision — the general recovery from a bad or
	// non-converging deploy.
	DeployRecoveryRollback DeployRecoveryAction = "rollback"
)

// RecommendedDeployRecovery picks the single recovery action that fits the
// diagnosis so `erun doctor` recommends one fix instead of offering every
// action at once. Clearing a stuck pending lock and rolling back are
// alternative fixes for different failure modes, not additive steps: clearing
// pending leaves the release at its last deployed revision, so a rollback run
// straight after would step back a further revision. The bool is false when no
// helm-level recovery applies — a healthy release, or no release to act on (a
// missing release is recovered by `erun deploy --force`, not by helm).
func RecommendedDeployRecovery(diagnosis DeployDiagnosisResult) (DeployRecoveryAction, bool) {
	status := strings.ToLower(strings.TrimSpace(diagnosis.HelmStatus))
	switch {
	case status == "":
		return "", false
	case strings.Contains(status, "not found"):
		return "", false
	case strings.Contains(status, "pending-install"),
		strings.Contains(status, "pending-upgrade"),
		strings.Contains(status, "pending-rollback"):
		return DeployRecoveryClearPendingHelm, true
	case strings.Contains(status, "status: deployed"):
		return "", false
	default:
		return DeployRecoveryRollback, true
	}
}

// DeployRecoveryActionPromptLabel is the interactive confirm shown before the
// action runs.
func DeployRecoveryActionPromptLabel(action DeployRecoveryAction, req ShellLaunchParams) string {
	target := strings.TrimSpace(req.Tenant) + "/" + strings.TrimSpace(req.Environment)
	switch action {
	case DeployRecoveryClearPendingHelm:
		return fmt.Sprintf("Clear the stuck pending helm release for %s?", target)
	case DeployRecoveryRollback:
		return fmt.Sprintf("Roll back %s to its last successful revision?", target)
	default:
		return fmt.Sprintf("Run deploy recovery %q for %s?", action, target)
	}
}

// DeployRecoveryActionDescription is the one-line "Running: …" label.
func DeployRecoveryActionDescription(action DeployRecoveryAction) string {
	switch action {
	case DeployRecoveryClearPendingHelm:
		return "Clear pending helm release"
	case DeployRecoveryRollback:
		return "Roll back to the last successful revision"
	default:
		return string(action)
	}
}

func helmRollbackArgs(req ShellLaunchParams) []string {
	// `helm rollback <release>` with no revision rolls back to the previous
	// release revision (the last one helm recorded as deployed).
	args := []string{"rollback", RuntimeReleaseName(req.Tenant)}
	if strings.TrimSpace(req.Namespace) != "" {
		args = append(args, "--namespace", req.Namespace)
	}
	if strings.TrimSpace(req.KubernetesContext) != "" {
		args = append(args, "--kube-context", req.KubernetesContext)
	}
	return append(args, "--wait", "--timeout", defaultShellLaunchWaitTimeout)
}

// RunDeployRecovery runs the chosen helm-level recovery against the runtime
// release, mutating the cluster only on a real (non-dry-run) run.
func RunDeployRecovery(ctx Context, req ShellLaunchParams, action DeployRecoveryAction) (string, error) {
	switch action {
	case DeployRecoveryClearPendingHelm:
		clear := HelmReleaseRecoveryParams{
			ReleaseName:       RuntimeReleaseName(req.Tenant),
			Namespace:         strings.TrimSpace(req.Namespace),
			KubernetesContext: strings.TrimSpace(req.KubernetesContext),
			Verbosity:         ctx.Verbosity,
		}
		ctx.TraceCommand("", clear.command().Name, clear.command().Args...)
		if ctx.DryRun {
			return "", nil
		}
		var out bytes.Buffer
		clear.Stdout = &out
		clear.Stderr = &out
		err := ClearHelmReleasePendingOperation(clear)
		return strings.TrimSpace(out.String()), err
	case DeployRecoveryRollback:
		args := helmRollbackArgs(req)
		ctx.TraceCommand("", "helm", args...)
		if ctx.DryRun {
			return "", nil
		}
		cmd := Command("helm", args...)
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		err := cmd.Run()
		return strings.TrimSpace(out.String()), err
	default:
		return "", fmt.Errorf("unsupported deploy recovery action %q", action)
	}
}

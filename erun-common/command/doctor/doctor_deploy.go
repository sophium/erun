package doctor

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/primitive/deploy"
)

// DeployRecoveryActionPromptLabel is the confirm prompt shown before the action runs.
func DeployRecoveryActionPromptLabel(action model.DeployRecoveryAction, req config.ShellLaunchParams) string {
	target := strings.TrimSpace(req.Tenant) + "/" + strings.TrimSpace(req.Environment)
	switch action {
	case model.DeployRecoveryClearPendingHelm:
		return fmt.Sprintf("Clear the stuck pending helm release for %s?", target)
	case model.DeployRecoveryRollback:
		return fmt.Sprintf("Roll back %s to its last successful revision?", target)
	default:
		return fmt.Sprintf("Run deploy recovery %q for %s?", action, target)
	}
}

// DeployRecoveryActionWithoutPromptHint names the flag that skips the prompt,
// for a caller whose stdin hit EOF.
func DeployRecoveryActionWithoutPromptHint(action model.DeployRecoveryAction) string {
	switch action {
	case model.DeployRecoveryClearPendingHelm:
		return "Re-run with --clear-pending-helm to run it without a prompt."
	case model.DeployRecoveryRollback:
		return "Re-run with --rollback to run it without a prompt."
	default:
		return "Re-run with the matching flag to run it without a prompt."
	}
}

// DeployRecoveryActionDescription is the one-line "Running: …" label.
func DeployRecoveryActionDescription(action model.DeployRecoveryAction) string {
	switch action {
	case model.DeployRecoveryClearPendingHelm:
		return "Clear pending helm release"
	case model.DeployRecoveryRollback:
		return "Roll back to the last successful revision"
	default:
		return string(action)
	}
}

func helmRollbackArgs(req config.ShellLaunchParams) []string {
	// With no revision, helm rolls back to the previous release.
	args := []string{"rollback", config.RuntimeReleaseName(req.Tenant)}
	if strings.TrimSpace(req.Namespace) != "" {
		args = append(args, "--namespace", req.Namespace)
	}
	if strings.TrimSpace(req.KubernetesContext) != "" {
		args = append(args, "--kube-context", req.KubernetesContext)
	}
	return append(args, "--wait", "--timeout", cloud.DefaultShellLaunchWaitTimeout)
}

// RunDeployRecovery runs the helm recovery; dry-run does not mutate the cluster.
func RunDeployRecovery(ctx core.Context, req config.ShellLaunchParams, action model.DeployRecoveryAction) (string, error) {
	switch action {
	case model.DeployRecoveryClearPendingHelm:
		recovery := seam.HelmReleaseRecoveryParams{
			ReleaseName:       config.RuntimeReleaseName(req.Tenant),
			Namespace:         strings.TrimSpace(req.Namespace),
			KubernetesContext: strings.TrimSpace(req.KubernetesContext),
			Verbosity:         ctx.Verbosity,
		}
		ctx.TraceCommand("", deploy.HelmReleaseRecoveryCommand(recovery).Name, deploy.HelmReleaseRecoveryCommand(recovery).Args...)
		if ctx.DryRun {
			return "", nil
		}
		var out bytes.Buffer
		recovery.Stdout = &out
		recovery.Stderr = &out
		err := deploy.ClearHelmReleasePendingOperation(recovery)
		return strings.TrimSpace(out.String()), err
	case model.DeployRecoveryRollback:
		args := helmRollbackArgs(req)
		ctx.TraceCommand("", "helm", args...)
		if ctx.DryRun {
			return "", nil
		}
		cmd := core.Command("helm", args...)
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		err := cmd.Run()
		return strings.TrimSpace(out.String()), err
	default:
		return "", fmt.Errorf("unsupported deploy recovery action %q", action)
	}
}

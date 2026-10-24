package doctor

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
)

// DeployRecoveryActionPromptLabel is the interactive confirm shown before the
// action runs.
func DeployRecoveryActionPromptLabel(action eruncommon.DeployRecoveryAction, req config.ShellLaunchParams) string {
	target := strings.TrimSpace(req.Tenant) + "/" + strings.TrimSpace(req.Environment)
	switch action {
	case eruncommon.DeployRecoveryClearPendingHelm:
		return fmt.Sprintf("Clear the stuck pending helm release for %s?", target)
	case eruncommon.DeployRecoveryRollback:
		return fmt.Sprintf("Roll back %s to its last successful revision?", target)
	default:
		return fmt.Sprintf("Run deploy recovery %q for %s?", action, target)
	}
}

// DeployRecoveryActionWithoutPromptHint names the flag that runs this recovery
// with no prompt, so a caller whose stdin reached EOF is told how to proceed
// rather than only that the action did not run. It sits beside the prompt label
// because it is the same question's non-interactive answer.
func DeployRecoveryActionWithoutPromptHint(action eruncommon.DeployRecoveryAction) string {
	switch action {
	case eruncommon.DeployRecoveryClearPendingHelm:
		return "Re-run with --clear-pending-helm to run it without a prompt."
	case eruncommon.DeployRecoveryRollback:
		return "Re-run with --rollback to run it without a prompt."
	default:
		return "Re-run with the matching flag to run it without a prompt."
	}
}

// DeployRecoveryActionDescription is the one-line "Running: …" label.
func DeployRecoveryActionDescription(action eruncommon.DeployRecoveryAction) string {
	switch action {
	case eruncommon.DeployRecoveryClearPendingHelm:
		return "Clear pending helm release"
	case eruncommon.DeployRecoveryRollback:
		return "Roll back to the last successful revision"
	default:
		return string(action)
	}
}

func helmRollbackArgs(req config.ShellLaunchParams) []string {
	// `helm rollback <release>` with no revision rolls back to the previous
	// release revision (the last one helm recorded as deployed).
	args := []string{"rollback", config.RuntimeReleaseName(req.Tenant)}
	if strings.TrimSpace(req.Namespace) != "" {
		args = append(args, "--namespace", req.Namespace)
	}
	if strings.TrimSpace(req.KubernetesContext) != "" {
		args = append(args, "--kube-context", req.KubernetesContext)
	}
	return append(args, "--wait", "--timeout", eruncommon.DefaultShellLaunchWaitTimeout)
}

// RunDeployRecovery runs the chosen helm-level recovery against the runtime
// release, mutating the cluster only on a real (non-dry-run) run.
func RunDeployRecovery(ctx core.Context, req config.ShellLaunchParams, action eruncommon.DeployRecoveryAction) (string, error) {
	switch action {
	case eruncommon.DeployRecoveryClearPendingHelm:
		clear := eruncommon.HelmReleaseRecoveryParams{
			ReleaseName:       config.RuntimeReleaseName(req.Tenant),
			Namespace:         strings.TrimSpace(req.Namespace),
			KubernetesContext: strings.TrimSpace(req.KubernetesContext),
			Verbosity:         ctx.Verbosity,
		}
		ctx.TraceCommand("", clear.Command().Name, clear.Command().Args...)
		if ctx.DryRun {
			return "", nil
		}
		var out bytes.Buffer
		clear.Stdout = &out
		clear.Stderr = &out
		err := eruncommon.ClearHelmReleasePendingOperation(clear)
		return strings.TrimSpace(out.String()), err
	case eruncommon.DeployRecoveryRollback:
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

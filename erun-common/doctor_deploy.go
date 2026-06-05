package eruncommon

import (
	"bytes"
	"fmt"
	"strings"
)

// DeployDiagnosisResult holds the captured output of the deploy-failure
// diagnosis: the helm release status and the pod listing for the runtime
// release's namespace. Rendering and interpretation are left to the caller
// (and to the desktop's one-click fixes) so the probe itself stays a
// transparent, side-effect-free read.
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

// RunDeployDiagnosis probes why a deploy may have failed by reporting the helm
// release status and the runtime namespace's pods. It is strictly read-only —
// it never mutates the release or the cluster — so it is safe to run on every
// `erun doctor`. The commands are traced for --dry-run; on a real run it
// captures their output (best effort: a missing release or unreachable cluster
// is part of the diagnosis, not a hard error) for the caller to render.
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

// runDoctorDiagnosisCommand runs a read-only diagnosis command and returns its
// combined output. Errors are folded into the output (the stderr of `helm
// status` on a missing release, for instance, is itself diagnostic), so the
// caller always has something to show.
func runDoctorDiagnosisCommand(name string, args []string) string {
	cmd := Command(name, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	_ = cmd.Run()
	return strings.TrimSpace(out.String())
}

// DeployRecoveryAction names a recovery `erun doctor` can run against a failing
// runtime release once the diagnosis surfaces a problem. These mutate the live
// release, so callers gate them behind a prompt/flag; the commands are traced
// for --dry-run so the exact helm/kubectl call is auditable before it runs.
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

// DeployRecoveryActions lists the helm-level recovery actions doctor offers.
// Force rebuild & redeploy is driven by the CLI doctor through the deploy flow
// (it is not a single helm command) and so is not in this list.
func DeployRecoveryActions() []DeployRecoveryAction {
	return []DeployRecoveryAction{DeployRecoveryClearPendingHelm, DeployRecoveryRollback}
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
// release. It traces the exact command for --dry-run and only mutates the
// cluster on a real run. The combined command output (helm/kubectl stdout +
// stderr) is returned so the caller can render what happened.
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

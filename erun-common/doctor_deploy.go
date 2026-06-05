package eruncommon

import (
	"bytes"
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

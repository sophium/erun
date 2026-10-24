package eruncommon

import (
	"bytes"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
)

// DeployDiagnosisResult holds the deploy-failure diagnosis for the caller to
// interpret. HelmStatus always carries the raw `helm status` output — including
// stderr on a failed read, which is itself diagnostic — but a caller deciding
// whether to recommend a mutating recovery must consult HelmReadError first:
// it is non-empty exactly when the read itself could not be trusted (RBAC, no
// helm on PATH, an API-server timeout), as opposed to a confirmed answer such
// as "no release exists" or "release failed". A caller must never derive the
// same "nothing to recover" answer for both a confirmed-healthy release and a
// read that could not tell (mirrors ObservedHelmRelease's Found/Error contract
// in observe_helm_release.go).
type DeployDiagnosisResult struct {
	HelmStatus    string
	HelmReadError string
	Pods          string
	// ClusterUnreachable is true when a probe above already confirmed the
	// Kubernetes API server itself could not be reached, as opposed to any
	// other helm/kubectl failure (a missing release, RBAC, a malformed
	// chart). Every other doctor section that needs the runtime pod
	// (host credentials, git push access, the docker-storage inspection)
	// reads this instead of re-probing the same unreachable cluster and
	// paying its own multi-minute kubectl timeout to rediscover the exact
	// fact this diagnosis already established.
	ClusterUnreachable bool
	// AgentCredentials answers whether the deployed pod template carries the
	// model-provider wiring erun configured for this environment -- the one
	// deployment fact helm status and pod readiness cannot show, because an
	// environment whose chart predates that wiring deploys cleanly and reports
	// healthy while being unable to start an agent at all. It is
	// NotApplicable for an environment no gateway routes, which asserts nothing
	// about such an environment's capability; see
	// runtime_agent_credentials.go.
	AgentCredentials RuntimeAgentCredentialStatus
}

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

// RunDeployDiagnosis probes why a deploy may have failed. It is strictly
// read-only, so it is safe to run on every `erun doctor`, including under
// `--dry-run` — that flag scopes itself to mutations (root AGENTS.md
// "Command primitives vs orchestration"), and a missing release or
// unreachable cluster is itself part of the diagnosis, not a hard error.
//
// The pods probe is skipped once the helm status read already confirms the
// Kubernetes API server itself is unreachable: a second kubectl call against
// the same unreachable cluster would only pay its own multi-minute timeout to
// rediscover the exact fact the helm read just established. ClusterUnreachable
// carries that determination forward so every later doctor section can skip
// its own probe the same way instead of independently rediscovering it.
func RunDeployDiagnosis(ctx core.Context, req config.ShellLaunchParams) DeployDiagnosisResult {
	helmArgs := helmStatusArgs(req)
	ctx.TraceCommand("", "helm", helmArgs...)
	podArgs := deployDiagnosisPodArgs(req)
	ctx.TraceCommand("", "kubectl", podArgs...)
	helmStatus, helmErr := runDoctorDiagnosisCommand("helm", helmArgs)
	// Applicability is resolved up front, from the request alone, so every way
	// out of this function -- including the early return for an unreachable
	// cluster -- still distinguishes "this check does not apply here" from "it
	// could not run". The read below only ever refines it.
	result := DeployDiagnosisResult{
		HelmStatus:       helmStatus,
		AgentCredentials: runtimeAgentCredentialApplicability(req),
	}
	if helmErr != nil && !isHelmReleaseNotFound(helmStatus) {
		result.HelmReadError = observeHelmReadErrorMessage(config.RuntimeReleaseName(req.Tenant), req.Namespace, helmStatus, helmErr)
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
		// Same reasoning as the skipped pods probe above: the agent-credential
		// read is one more kubectl call against the cluster this diagnosis has
		// just established is unreachable, and it would report a read failure
		// where the honest answer is "not observed".
		return result
	}
	result.AgentCredentials = inspectRuntimeAgentCredentials(ctx, req)
	return result
}

// runDoctorDiagnosisCommand folds command errors into the returned output —
// `helm status` stderr on a missing release is itself diagnostic — so the
// caller always has something to show, while still returning the command's own
// error so a caller that needs to know whether the read succeeded can tell.
func runDoctorDiagnosisCommand(name string, args []string) (string, error) {
	cmd := core.Command(name, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return strings.TrimSpace(out.String()), err
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
// helm-level recovery applies — a healthy release, no release to act on (a
// missing release is recovered by `erun deploy --force`, not by helm), or a
// release the read could not confirm one way or the other: recommending the
// destructive rollback for a read failure would act on a guess, not a
// diagnosis, so HelmReadError is checked before any HelmStatus text.
func RecommendedDeployRecovery(diagnosis DeployDiagnosisResult) (DeployRecoveryAction, bool) {
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
		return DeployRecoveryClearPendingHelm, true
	case strings.Contains(status, "status: deployed"):
		return "", false
	default:
		return DeployRecoveryRollback, true
	}
}

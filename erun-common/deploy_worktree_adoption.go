package eruncommon

import (
	"fmt"
	"strings"
)

// worktreeClaimSuffix names the runtime release's dedicated worktree volume; it
// mirrors the runtime chart's `{{ .Release.Name }}-worktree` claim.
const worktreeClaimSuffix = "-worktree"

// podWorktreePath mirrors the runtime chart's $repoPath: the in-pod git folder is
// derived from the repo name alone, whatever the env's host-side repo path is.
func podWorktreePath(repoName string) string {
	repoName = strings.TrimSpace(repoName)
	if repoName == "" {
		return ""
	}
	return "/home/erun/git/" + repoName
}

// announceWorktreeVolumeChange reports a deploy that moves the environment's
// worktree onto the dedicated worktree volume.
//
// An environment whose checkout predates that volume keeps it on the home
// volume, at the very path the claim mounts at, so the deploy that first creates
// the claim relocates the worktree between volumes. The pod's init container
// adopts the existing tree rather than letting the empty claim mask it, but the
// relocation still has to be visible: a rollout that reported plain success
// while swapping the volume under a populated worktree is what made the loss
// invisible in the first place.
//
// Best-effort by design — when the claim's existence cannot be read, the notice
// is printed anyway. Staying quiet is the failure mode being fixed.
func announceWorktreeVolumeChange(ctx Context, spec HelmDeploySpec) {
	if spec.WorktreeStorage != WorktreeStoragePVC {
		return
	}
	// Only the runtime release owns the worktree volume; a component chart's
	// deploy carries the same env fields but creates no claim.
	if strings.TrimSpace(spec.ReleaseName) != RuntimeReleaseName(spec.Tenant) {
		return
	}
	worktreePath := podWorktreePath(spec.WorktreeRepoName)
	if worktreePath == "" {
		return
	}
	claim := spec.ReleaseName + worktreeClaimSuffix

	args := kubectlWorktreeClaimArgs(spec, claim)
	ctx.TraceCommand("", "kubectl", args...)
	if ctx.DryRun {
		ctx.Trace(fmt.Sprintf("deploy: worktree %s is served by the %s volume; a deploy that creates that volume adopts an existing tree from the %s-home volume before the pod starts", worktreePath, claim, spec.ReleaseName))
		return
	}

	headline := "==> Worktree volume " + claim + " is not in place yet for " + spec.Tenant + "/" + spec.Environment
	exists, err := worktreeClaimExists(spec.KubernetesContext, spec.Namespace, claim)
	if err != nil {
		ctx.Trace("deploy: could not read worktree volume " + claim + ": " + err.Error())
		headline = "==> Worktree volume " + claim + " could not be read for " + spec.Tenant + "/" + spec.Environment + "; this deploy may be the one that creates it"
	} else if exists {
		ctx.Trace("deploy: worktree volume " + claim + " already exists; " + worktreePath + " stays on it")
		return
	}

	ctx.Info(headline)
	ctx.Info("    " + worktreePath + " is served by that volume, not by " + spec.ReleaseName + "-home")
	ctx.Info("    an existing repository there is adopted onto the new volume before the pod starts, and the pre-move copy is kept at " + worktreePath + ".pre-worktree-volume")
	ctx.Info("    the worktree starts empty when there is nothing to adopt")
}

// kubectlGetPVCArgs is the single source of the `kubectl get pvc <claim> -o
// name` argv, shared by kubectlWorktreeClaimArgs (which renders it for both
// dry-run and audit purposes) and defaultWorktreeClaimExists (which also
// executes it as a subprocess), so the dry-run trace can never drift from
// either execution path.
func kubectlGetPVCArgs(contextName, namespace, claim string) []string {
	args := make([]string, 0, 8)
	if contextName = strings.TrimSpace(contextName); contextName != "" {
		args = append(args, "--context", contextName)
	}
	if namespace = strings.TrimSpace(namespace); namespace != "" {
		args = append(args, "--namespace", namespace)
	}
	return append(args, "get", "pvc", claim, "-o", "name")
}

func kubectlWorktreeClaimArgs(spec HelmDeploySpec, claim string) []string {
	return kubectlGetPVCArgs(spec.KubernetesContext, spec.Namespace, claim)
}

// worktreeClaimExists dispatches to the subprocess or library path per the
// kubectl-pvc-get execution mode (see execution_mode.go), distinguishing "the
// claim is not there" from "the answer is unknown" either way, so a cluster
// erun cannot read never passes for a settled worktree.
func worktreeClaimExists(contextName, namespace, claim string) (bool, error) {
	if currentExecutionMode(kubectlPVCGetExecutionOperation) == ExecutionModeLibrary {
		return libraryPersistentVolumeClaimExists(contextName, namespace, claim)
	}
	return defaultWorktreeClaimExists(contextName, namespace, claim)
}

func defaultWorktreeClaimExists(contextName, namespace, claim string) (bool, error) {
	output, err := Command("kubectl", kubectlGetPVCArgs(contextName, namespace, claim)...).CombinedOutput()
	if err == nil {
		return true, nil
	}
	message := strings.TrimSpace(string(output))
	if KubernetesResourceNotFound(message) {
		return false, nil
	}
	if message == "" {
		return false, err
	}
	return false, fmt.Errorf("%w: %s", err, message)
}

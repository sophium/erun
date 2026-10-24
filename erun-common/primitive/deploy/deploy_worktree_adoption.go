package deploy

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/seam"
	"github.com/sophium/erun/erun-common/infra/kube"
)

// Mirrors the runtime chart's `{{ .Release.Name }}-worktree` claim.
const worktreeClaimSuffix = "-worktree"

// podWorktreePath mirrors the runtime chart's $repoPath.
func podWorktreePath(repoName string) string {
	repoName = strings.TrimSpace(repoName)
	if repoName == "" {
		return ""
	}
	return "/home/erun/git/" + repoName
}

// announceWorktreeVolumeChange reports a deploy that first creates the worktree
// claim and so relocates an existing worktree between volumes. When the claim
// cannot be read the notice prints anyway: silence is the failure being avoided.
func announceWorktreeVolumeChange(ctx core.Context, spec seam.HelmDeploySpec) {
	if spec.WorktreeStorage != WorktreeStoragePVC {
		return
	}
	// Component releases carry the same env fields but create no worktree claim.
	if strings.TrimSpace(spec.ReleaseName) != config.RuntimeReleaseName(spec.Tenant) {
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

// kubectlGetPVCArgs is the single argv source so the dry-run trace cannot drift
// from execution.
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

func kubectlWorktreeClaimArgs(spec seam.HelmDeploySpec, claim string) []string {
	return kubectlGetPVCArgs(spec.KubernetesContext, spec.Namespace, claim)
}

// worktreeClaimExists distinguishes "absent" from "unknown" so an unreadable
// cluster never passes as settled.
func worktreeClaimExists(contextName, namespace, claim string) (bool, error) {
	if config.CurrentExecutionMode(kube.KubectlPVCGetExecutionOperation) == config.ExecutionModeLibrary {
		return kube.LibraryPersistentVolumeClaimExists(contextName, namespace, claim)
	}
	return defaultWorktreeClaimExists(contextName, namespace, claim)
}

func defaultWorktreeClaimExists(contextName, namespace, claim string) (bool, error) {
	output, err := core.Command("kubectl", kubectlGetPVCArgs(contextName, namespace, claim)...).CombinedOutput()
	if err == nil {
		return true, nil
	}
	message := strings.TrimSpace(string(output))
	if kube.KubernetesResourceNotFound(message) {
		return false, nil
	}
	if message == "" {
		return false, err
	}
	return false, fmt.Errorf("%w: %s", err, message)
}

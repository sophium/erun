package lifecycle

import (
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/gitx"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// AnnotatePinPlanBaseFreshness is best-effort: an unreadable branch, ref, or remote leaves the
// plan unannotated rather than failing it.
func AnnotatePinPlanBaseFreshness(ctx core.Context, plan *model.PinPlan) {
	if plan == nil {
		return
	}
	freshness, ok := resolvePinBaseFreshness(ctx, plan.ProjectRoot)
	if !ok {
		return
	}
	plan.BaseFreshness = &freshness
}

func resolvePinBaseFreshness(ctx core.Context, projectRoot string) (model.PinBaseFreshness, bool) {
	if strings.TrimSpace(projectRoot) == "" {
		return model.PinBaseFreshness{}, false
	}
	branch, err := gitx.GitCurrentBranch(ctx, projectRoot)
	if err != nil {
		return model.PinBaseFreshness{}, false
	}
	branch = strings.TrimSpace(branch)
	ref, remoteBranch, ok := pinBaseTrackingRef(ctx, projectRoot, branch)
	if !ok {
		return model.PinBaseFreshness{}, false
	}
	remoteRead := refreshPinBaseTrackingRef(ctx, projectRoot, remoteBranch)
	behind, err := shared.GitCommitsAhead(ctx, projectRoot, "HEAD", ref)
	if err != nil {
		return model.PinBaseFreshness{}, false
	}
	return model.PinBaseFreshness{Branch: branch, Ref: ref, Behind: behind, RemoteRead: remoteRead}, true
}

// The branch's upstream, or origin's default branch when detached. remoteBranch is empty
// when the ref cannot be fetched by name.
func pinBaseTrackingRef(ctx core.Context, projectRoot, branch string) (ref, remoteBranch string, ok bool) {
	if branch != "" && branch != "HEAD" {
		upstream, found := shared.ResolveBranchUpstreamCandidate(ctx, projectRoot, branch)
		if !found {
			return "", "", false
		}
		if strings.HasPrefix(upstream, "origin/") {
			return upstream, strings.TrimPrefix(upstream, "origin/"), true
		}
		// A non-origin upstream is still counted, just not refreshed.
		return upstream, "", true
	}
	if _, resolved, err := shared.GitResolvedRef(ctx, projectRoot, "origin/HEAD"); err != nil || !resolved {
		return "", "", false
	}
	return "origin/HEAD", strings.TrimPrefix(pinBaseRemoteHeadBranch(ctx, projectRoot), "origin/"), true
}

func pinBaseRemoteHeadBranch(ctx core.Context, projectRoot string) string {
	ctx.TraceCommand("", "git", "-C", projectRoot, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	output, err := core.Command("git", "-C", projectRoot, "symbolic-ref", "--short", "refs/remotes/origin/HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

// Fetch first so the count reflects the remote, not the last fetch. Best-effort: returns
// whether the remote was reached.
func refreshPinBaseTrackingRef(ctx core.Context, projectRoot, remoteBranch string) bool {
	if remoteBranch == "" {
		return false
	}
	ctx.TraceCommand("", "git", "-C", projectRoot, "fetch", "origin", remoteBranch)
	return core.Command("git", "-C", projectRoot, "fetch", "origin", remoteBranch).Run() == nil
}

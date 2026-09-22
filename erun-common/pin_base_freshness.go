package eruncommon

import (
	"fmt"
	"strings"
)

// A pin plan is only as current as the checkout it was resolved from. Every
// site it lists is read out of that tree, so a checkout sitting behind the
// remote it tracks yields a plan that is internally consistent -- every site
// agrees, the counts look right, the skip notes are the same ones a current
// tree would produce -- while describing an older base than the remote holds.
//
// Nothing about such a plan looks wrong, and that is what makes it dangerous.
// Applying it moves every listed site from the stale version to the target and
// silently reverts whatever the missing commits changed elsewhere in the tree,
// and the result reviews as a routine pin move because that is exactly what it
// looks like. A reported case read a checkout 44 releases behind its remote and
// produced twelve sites that all agreed on the wrong base.
//
// PinBaseFreshness is the missing fact: how the resolved checkout stands
// against the ref it tracks. It is carried on the plan rather than printed by a
// transport so the CLI, the MCP tool, and any caller reading the result JSON
// all see the same verdict instead of each deciding whether to look.
type PinBaseFreshness struct {
	// Branch is the branch the checkout is on, or "HEAD" when detached.
	Branch string `json:"branch,omitempty"`
	// Ref is the ref Behind is counted against, e.g. "origin/main".
	Ref string `json:"ref"`
	// Behind is how many commits Ref carries that the checkout does not.
	Behind int `json:"behind"`
	// RemoteRead reports whether the remote was reached to bring Ref up to
	// date before counting. False means Ref is only as current as this
	// checkout's own last look at it, which is the weaker answer and is said
	// so rather than presented as agreement.
	RemoteRead bool `json:"remoteRead"`
}

// Stale reports whether the checkout is behind the ref it tracks, which is the
// condition a pin plan must never present as clean.
func (f PinBaseFreshness) Stale() bool { return f.Behind > 0 }

// BaseFreshnessNote renders the divergence for the surfaces that print a plan.
// Empty when the checkout is current or when no tracked ref could be read, so a
// caller renders whatever it returns without deciding what counts as a warning.
//
// It names how far behind and against which ref, because a warning that says
// only "stale" leaves the reader unable to tell a one-commit lag from the
// months-old tree this exists to surface.
func (p PinPlan) BaseFreshnessNote() string {
	if p.BaseFreshness == nil || !p.BaseFreshness.Stale() {
		return ""
	}
	freshness := *p.BaseFreshness
	note := fmt.Sprintf("the checkout this plan was resolved from is %d commit(s) behind %s", freshness.Behind, freshness.Ref)
	if !freshness.RemoteRead {
		note += " as this checkout last saw it"
	}
	return note + fmt.Sprintf(", so the plan describes that older base rather than what %s holds now — applying it would also revert anything those commits changed elsewhere in the tree", freshness.Ref)
}

// AnnotatePinPlanBaseFreshness records on the plan how the checkout it was
// resolved from stands against the ref that checkout tracks.
//
// It is best-effort by design: a checkout with no readable branch, no tracked
// ref, or no remote to compare against leaves the plan unannotated rather than
// failing it. The tree is still a usable base for a pin; what is not acceptable
// is silently presenting a *known* divergence as if it were agreement, and only
// a successful read of both sides can establish that there is one.
func AnnotatePinPlanBaseFreshness(ctx Context, plan *PinPlan) {
	if plan == nil {
		return
	}
	freshness, ok := resolvePinBaseFreshness(ctx, plan.ProjectRoot)
	if !ok {
		return
	}
	plan.BaseFreshness = &freshness
}

// resolvePinBaseFreshness answers how projectRoot stands against the ref it
// tracks, reporting false when there is no ref to compare against.
func resolvePinBaseFreshness(ctx Context, projectRoot string) (PinBaseFreshness, bool) {
	if strings.TrimSpace(projectRoot) == "" {
		return PinBaseFreshness{}, false
	}
	branch, err := GitCurrentBranch(ctx, projectRoot)
	if err != nil {
		return PinBaseFreshness{}, false
	}
	branch = strings.TrimSpace(branch)
	ref, remoteBranch, ok := pinBaseTrackingRef(ctx, projectRoot, branch)
	if !ok {
		return PinBaseFreshness{}, false
	}
	remoteRead := refreshPinBaseTrackingRef(ctx, projectRoot, remoteBranch)
	behind, err := gitCommitsAhead(ctx, projectRoot, "HEAD", ref)
	if err != nil {
		return PinBaseFreshness{}, false
	}
	return PinBaseFreshness{Branch: branch, Ref: ref, Behind: behind, RemoteRead: remoteRead}, true
}

// pinBaseTrackingRef answers which ref the count should be against: the branch's
// own upstream when it has one, and the remote's default branch when the
// checkout is detached and so has no upstream of its own. remoteBranch is the
// branch to fetch on that remote, empty when the ref cannot be fetched by name.
func pinBaseTrackingRef(ctx Context, projectRoot, branch string) (ref, remoteBranch string, ok bool) {
	if branch != "" && branch != "HEAD" {
		upstream, found := resolveBranchUpstreamCandidate(ctx, projectRoot, branch)
		if !found {
			return "", "", false
		}
		if strings.HasPrefix(upstream, "origin/") {
			return upstream, strings.TrimPrefix(upstream, "origin/"), true
		}
		// A non-origin remote still gives a ref worth counting against; it is
		// just not one this can refresh through `origin`.
		return upstream, "", true
	}
	// Detached: origin/HEAD names the remote's default branch, which is the only
	// ref left that can say whether this checkout is behind the remote at all.
	if _, resolved, err := gitResolvedRef(ctx, projectRoot, "origin/HEAD"); err != nil || !resolved {
		return "", "", false
	}
	return "origin/HEAD", strings.TrimPrefix(pinBaseRemoteHeadBranch(ctx, projectRoot), "origin/"), true
}

// pinBaseRemoteHeadBranch names the branch origin/HEAD points at, so a detached
// checkout can refresh it. Empty when the symref cannot be read, which leaves
// the count against this checkout's own last look.
func pinBaseRemoteHeadBranch(ctx Context, projectRoot string) string {
	ctx.TraceCommand("", "git", "-C", projectRoot, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	output, err := Command("git", "-C", projectRoot, "symbolic-ref", "--short", "refs/remotes/origin/HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

// refreshPinBaseTrackingRef brings ref up to date with the remote before it is
// counted against, so "behind origin/main" is a statement about the remote
// rather than about whenever this checkout last happened to look. A clone that
// has not fetched in months would otherwise count zero and be reported current,
// which is the same silent staleness one level up.
//
// Best-effort: it reports whether the remote was reached instead of failing, so
// an unreachable remote weakens the answer visibly rather than turning a pin
// into an error.
func refreshPinBaseTrackingRef(ctx Context, projectRoot, remoteBranch string) bool {
	if remoteBranch == "" {
		return false
	}
	ctx.TraceCommand("", "git", "-C", projectRoot, "fetch", "origin", remoteBranch)
	return Command("git", "-C", projectRoot, "fetch", "origin", remoteBranch).Run() == nil
}

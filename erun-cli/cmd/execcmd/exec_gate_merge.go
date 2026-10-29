package execcmd

import (
	"fmt"
	"io"
	"strings"

	"github.com/sophium/erun/cmd/shared/clicmd"
	configprojectroot "github.com/sophium/erun/erun-common/base/config/projectroot"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	seamprojectroot "github.com/sophium/erun/erun-common/base/seam/projectroot"
	"github.com/sophium/erun/erun-common/command/mergequeue"
	"github.com/sophium/erun/erun-common/command/review"
	"github.com/spf13/cobra"
)

func newExecGateMergeCmd(findProjectRoot seamprojectroot.ProjectFinderFunc) *cobra.Command {
	var (
		sources    []string
		target     string
		remote     string
		underLease string
	)
	cmd := &cobra.Command{
		Use:   "gate-merge --source SOURCE_BRANCH [--source SOURCE_BRANCH...]",
		Short: "Prepare the prospective merge a merge queue promotion (or batch) builds",
		Long: "Fetch --target and every --source from the remote, check out a fresh local branch named --target " +
			"at its own current remote tip, then squash-merge each --source onto it in turn, each as its own " +
			"commit — leaving a stack of one commit per landed source. Repeat --source to batch several " +
			"unmerged branches into one prospective merge, so the build that follows tests whether they compile " +
			"*together*, not just individually; a single --source is the ordinary one-branch merge. This is for " +
			"the environment a review's merge queue promotes to MERGE: gate-merge, then a plain `erun build` " +
			"of the result, then `erun review record-build` with the version that build minted and, only on " +
			"success, `erun exec push` and `erun review report-merged`.\n\n" +
			"Commit messages are read verbatim from stdin, never a shell argument, so nothing in them is " +
			"reinterpreted: one message per --source, in the same order, separated by NUL bytes (a single " +
			"--source needs no separator at all).\n\n" +
			"Each source branch's own load-bearing trailers — `Closes #N`, `Defect-Fix:`, and the " +
			"`Reproduces:` / `Regression-Test:` lines a defect fix declares — are appended beneath the message it is given, so " +
			"what the branch declared about itself survives a squash that replaces the rest of its history. " +
			"The message stays the commit's subject and the whole of its own body, and a trailer the message " +
			"already carries is not repeated. They are read from every line of the branch's own commit bodies " +
			"rather than from a trailing trailer block, so a declaration is not lost to whatever paragraph an " +
			"author put after it, and each landed source reports the trailers it carried — including when it " +
			"carried none, which is otherwise invisible in the squash message.\n\n" +
			"The working tree must already be clean: this checks out a different local branch than whatever the " +
			"tree is currently on, so uncommitted work there is refused rather than silently carried onto the " +
			"prospective merge.\n\n" +
			"A source whose squash conflicts is skipped, not fatal: the working tree is reset back to a clean " +
			"state and the conflict recorded in the result, and the rest of the batch still builds against the " +
			"tree as it stood before that attempt. A conflict confined to atlas.sum, the regenerable migration " +
			"checksum, is the exception: it is regenerated from the migration files already on disk and staged, " +
			"and the source lands like a clean squash rather than being skipped. A source that contributes " +
			"nothing is skipped the same way: when its squash stages no changes because its content is already " +
			"on the target, it is recorded as skipped with that reason rather than failing the batch, so an " +
			"already-landed branch is a no-op instead of a dead merge. A defect fix whose declaration does not " +
			"resolve against what its squash staged is skipped the same way, with a `regression-test policy:` " +
			"reason. A batch where every source is skipped lands nothing and exits non-zero.\n\n" +
			"Refused outright while something else holds this environment exclusively: this rewrites the one " +
			"shared worktree, so two gate-merges in flight at once do not merely slow each other down, they " +
			"corrupt each other's accounting — a drive has already reported pushing a commit that belonged to " +
			"another batch's tree. A caller that took the claim itself passes --under-lease so its own hold " +
			"does not refuse it.\n\n" +
			"--dry-run traces the fetch, checkout, and each squash merge, trailer read and commit without running them.",
		Example: "  echo 'Add widget' | erun exec gate-merge --source feature/add-widget --target main\n" +
			"  printf 'Add widget\\0Add gadget' | erun exec gate-merge --source feature/add-widget " +
			"--source feature/add-gadget --target main\n" +
			"  echo 'Add widget' | erun exec gate-merge --source feature/add-widget --target main --remote upstream",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExecGateMergeCommand(clicmd.CommandContext(cmd), findProjectRoot, sources, target, remote, underLease)
		},
	}
	cmd.Flags().StringArrayVar(&sources, "source", nil, "Branch to fetch and squash-merge in; repeat to batch several branches onto one prospective merge (required, at least once)")
	cmd.Flags().StringVar(&target, "target", "", "Target branch the squash merge(s) land onto (required)")
	cmd.Flags().StringVar(&remote, "remote", "", "Git remote to fetch and merge from (defaults to origin)")
	cmd.Flags().StringVar(&underLease, "under-lease", "", "Id of an exclusive environment claim this caller already holds, so its own hold does not refuse it")
	clicmd.AddDryRunFlag(cmd)
	return cmd
}

func runExecGateMergeCommand(ctx core.Context, findProjectRoot seamprojectroot.ProjectFinderFunc, sourceBranches []string, targetBranch, remote, underLease string) error {
	if len(sourceBranches) == 0 {
		return fmt.Errorf("at least one --source is required")
	}
	if strings.TrimSpace(targetBranch) == "" {
		return fmt.Errorf("--target is required")
	}
	if findProjectRoot == nil {
		findProjectRoot = configprojectroot.FindProjectRoot
	}
	_, projectRoot, err := findProjectRoot()
	if err != nil {
		return err
	}
	sources, err := readGateMergeSources(ctx, sourceBranches)
	if err != nil {
		return err
	}
	result, err := mergequeue.GateMergeWorkingTree(ctx, projectRoot, model.GateMergeWorkingTreeParams{
		Sources:      sources,
		TargetBranch: targetBranch,
		Remote:       remote,
		UnderLeaseID: underLease,
	}, mergequeue.GateMergeWorkingTreeDependencies{})
	if err != nil {
		return err
	}
	if ctx.DryRun {
		return nil
	}
	return reportGateMergeResult(ctx, result)
}

// readGateMergeSources reads NUL-separated commit messages from stdin in --source order.
func readGateMergeSources(ctx core.Context, sourceBranches []string) ([]model.GateMergeSource, error) {
	stdin, err := io.ReadAll(ctx.Stdin)
	if err != nil {
		return nil, fmt.Errorf("read commit message(s) from stdin: %w", err)
	}
	messages := strings.Split(string(stdin), "\x00")
	if len(messages) != len(sourceBranches) {
		return nil, fmt.Errorf("expected %d NUL-separated commit message(s) on stdin (one per --source), got %d", len(sourceBranches), len(messages))
	}
	sources := make([]model.GateMergeSource, len(sourceBranches))
	for i, branch := range sourceBranches {
		sources[i] = model.GateMergeSource{Branch: branch, Message: messages[i]}
	}
	return sources, nil
}

// An empty carriage is spelled out; it is otherwise invisible to the caller.
func gateMergeCarriedTrailerSummary(trailers []string) string {
	if len(trailers) == 0 {
		return "no trailers of its own"
	}
	return "its own trailers: " + strings.Join(trailers, "; ")
}

// reportGateMergeResult refuses when the batch landed nothing.
func reportGateMergeResult(ctx core.Context, result model.GateMergeWorkingTreeResult) error {
	for _, landed := range result.Landed {
		ctx.Info(fmt.Sprintf("Squash-merged %s/%s onto %s (%s), carrying %s.",
			result.Remote, landed.SourceBranch, result.TargetBranch, landed.Commit,
			gateMergeCarriedTrailerSummary(landed.CarriedTrailers)))
	}
	for _, skipped := range result.Skipped {
		ctx.Info(fmt.Sprintf("Skipped %s/%s: %s.", result.Remote, skipped.SourceBranch, skipped.Reason))
	}
	if len(result.Landed) == 0 {
		return fmt.Errorf("no source branch landed; every one of %d was skipped", len(result.Skipped))
	}
	return ctx.WriteResult(result)
}

func newExecReportCommitStatusCmd() *cobra.Command {
	var (
		state       string
		statusCtx   string
		description string
		targetURL   string
		remoteURL   string
	)
	cmd := &cobra.Command{
		Use:   "report-commit-status COMMIT",
		Short: "Report a GitHub commit status for a merge queue build result",
		Long: "Report a commit status on GitHub for COMMIT. This is the last step in the merge queue " +
			"(`erun exec gate-merge`, `erun build`, `erun review record-build`): report success once the " +
			"recorded build succeeded, or failure the moment it did not, naming which step failed in " +
			"--description. A required status check on the remote's branch protection has nothing to require " +
			"until this reports it.\n\n" +
			"COMMIT should be the review's source branch tip — the pull request's own head commit — never the " +
			"local prospective squash-merge commit `gate-merge` produces: GitHub only evaluates a required check " +
			"against a commit reachable from the open pull request, and the squash commit does not exist there " +
			"until after the build has already succeeded and been pushed.\n\n" +
			"--dry-run traces the request without sending it.",
		Example: "  erun exec report-commit-status $(git rev-parse HEAD) --state failure \\\n" +
			"    --description 'erun build failed against the prospective merge' \\\n" +
			"    --remote-url https://github.com/org/repo.git\n" +
			"  erun exec report-commit-status $(git rev-parse HEAD) --state success \\\n" +
			"    --description 'merge queue build succeeded' --remote-url https://github.com/org/repo.git",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExecReportCommitStatusCommand(clicmd.CommandContext(cmd), args[0], state, statusCtx, description, targetURL, remoteURL)
		},
	}
	cmd.Flags().StringVar(&state, "state", "", "Commit status state: success, failure, error, or pending (required)")
	cmd.Flags().StringVar(&statusCtx, "context", "", "Status check name a required-status-checks rule names (defaults to erun/merge-gate)")
	cmd.Flags().StringVar(&description, "description", "", "Short human-readable status, naming which step failed on failure (required)")
	cmd.Flags().StringVar(&targetURL, "target-url", "", "URL a reader clicks through to from the status (optional)")
	cmd.Flags().StringVar(&remoteURL, "remote-url", "", "The github.com remote to report the status against (required)")
	clicmd.AddDryRunFlag(cmd)
	return cmd
}

func runExecReportCommitStatusCommand(ctx core.Context, commit, state, statusCtx, description, targetURL, remoteURL string) error {
	result, err := review.ReportCommitStatus(ctx, model.ReportCommitStatusParams{
		RemoteURL:   remoteURL,
		Commit:      commit,
		State:       model.ReportCommitStatusState(state),
		Context:     statusCtx,
		Description: description,
		TargetURL:   targetURL,
	}, review.ReportCommitStatusDependencies{})
	if err != nil {
		return err
	}
	if ctx.DryRun {
		return nil
	}
	ctx.Info(fmt.Sprintf("Reported %s status %q on %s/%s@%s.", result.State, result.Context, result.Owner, result.Repo, result.Commit))
	return ctx.WriteResult(result)
}

func newExecClosePRCmd() *cobra.Command {
	var (
		target        string
		remoteURL     string
		gatedCommit   string
		landingCommit string
	)
	cmd := &cobra.Command{
		Use:   "close-pr BRANCH",
		Short: "Close the GitHub pull request a merge queue actually shipped",
		Long: "Close BRANCH's open pull request on GitHub and record LANDING_COMMIT on it. This runs after `erun " +
			"review report-merged` has already succeeded: gate-merge's squash commit is never the branch head GitHub " +
			"tracks, so GitHub never reconciles a queued merge with its pull request on its own, and the commit that " +
			"actually shipped exists nowhere the pull request can see.\n\n" +
			"Safe when BRANCH has no open pull request against --target: this is a no-op, not an error, since " +
			"queueing a plain branch with no review is legitimate.\n\n" +
			"Refuses, loudly, when the pull request's current head does not match --gated-commit — something " +
			"pushed to BRANCH after the merge queue fetched it, so the built content is not what closing would discard.\n\n" +
			"--dry-run traces the lookup without closing or commenting on anything.",
		Example: "  erun exec close-pr feature/add-widget --target main \\\n" +
			"    --remote-url https://github.com/org/repo.git \\\n" +
			"    --gated-commit $(git rev-parse origin/feature/add-widget) --landing-commit $(git rev-parse HEAD)",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExecClosePRCommand(clicmd.CommandContext(cmd), args[0], target, remoteURL, gatedCommit, landingCommit)
		},
	}
	cmd.Flags().StringVar(&target, "target", "", "The pull request's base branch (required)")
	cmd.Flags().StringVar(&remoteURL, "remote-url", "", "The github.com remote the pull request lives on (required)")
	cmd.Flags().StringVar(&gatedCommit, "gated-commit", "", "BRANCH's tip when the merge queue actually fetched and built it (required)")
	cmd.Flags().StringVar(&landingCommit, "landing-commit", "", "The commit that actually landed on --target, recorded in a comment on the pull request (required)")
	clicmd.AddDryRunFlag(cmd)
	return cmd
}

func runExecClosePRCommand(ctx core.Context, branch, target, remoteURL, gatedCommit, landingCommit string) error {
	result, err := review.ClosePullRequest(ctx, model.ClosePullRequestParams{
		RemoteURL:     remoteURL,
		Branch:        branch,
		TargetBranch:  target,
		GatedCommit:   gatedCommit,
		LandingCommit: landingCommit,
	}, review.ClosePullRequestDependencies{})
	if err != nil {
		return err
	}
	if ctx.DryRun {
		return nil
	}
	if !result.Found {
		ctx.Info(fmt.Sprintf("No open pull request for %s -> %s; nothing to close.", result.Branch, target))
		return ctx.WriteResult(result)
	}
	ctx.Info(fmt.Sprintf("Closed pull request #%d for %s/%s.", result.Number, result.Owner, result.Repo))
	return ctx.WriteResult(result)
}

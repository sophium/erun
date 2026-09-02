package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

func newExecCmd(findProjectRoot common.ProjectFinderFunc, runGit common.GitCommandRunnerFunc, runRaw common.RawCommandRunnerFunc, resolveOpen OpenResolver, store common.CloudReadStore, deps common.CloudDependencies) *cobra.Command {
	// job's supervise leaf is mounted only here, under exec, since
	// environmentJobSupervisorArgs always re-execs `exec job supervise`
	// regardless of which entry point (this one, or the deprecated top-level
	// `erun job` alias) actually started the job.
	jobCmd := newJobCmd(resolveOpen)
	jobCmd.AddCommand(newJobSuperviseCmd())

	return newCommandGroup(
		"exec",
		"Repository execution utilities",
		newExecDiffCmd(findProjectRoot, runGit),
		newExecRawCmd(findProjectRoot, runRaw),
		newExecWriteCmd(findProjectRoot),
		newExecCommitCmd(findProjectRoot),
		newExecPushCmd(findProjectRoot),
		newExecMergeCmd(findProjectRoot),
		newExecGateMergeCmd(findProjectRoot),
		newExecReportCommitStatusCmd(),
		newExecClosePRCmd(),
		newExecGateRunCmd(store, deps),
		jobCmd,
	)
}

// newExecGateRunCmd builds `erun exec gate-run`, the pair of self-report
// calls the environment driving a gate uses to make its own attempt visible
// on the erun platform — start/report, never a human-facing
// listing (that is `erun gate list`/`show`).
func newExecGateRunCmd(store common.CloudReadStore, deps common.CloudDependencies) *cobra.Command {
	var alias string
	cmd := newCommandGroup(
		"gate-run",
		"Report a gate run's start and outcome to the erun platform",
		newExecGateRunStartCmd(store, &alias, deps),
		newExecGateRunReportCmd(store, &alias, deps),
	)
	cmd.PersistentFlags().StringVar(&alias, "erun-alias", "", "erun platform cloud alias to target (defaults to the sole configured erun-type alias)")
	return cmd
}

func newExecGateRunStartCmd(store common.CloudReadStore, alias *string, deps common.CloudDependencies) *cobra.Command {
	var params common.GateRunStartParams
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Record the beginning of one gate attempt",
		Long: "Record the beginning of one attempt to gate a prospective merge -- the branch, the prospective " +
			"squash-merge commit, and the target -- so `erun gate list` can show it as currently gating, " +
			"independent of whether an erun review exists for the change. Prints the new gate run's id; pass it " +
			"to `erun exec gate-run report` once the gate finishes.\n\n" +
			"A run with no trackable running phase at all -- a squash conflict before any build ever starts -- " +
			"may set --status directly to failed or inconclusive and omit --merge-commit.\n\n" +
			"--dry-run traces the request without sending it.",
		Example: "  erun exec gate-run start --source-branch feature/x --target-branch main \\\n" +
			"    --source-commit $(git rev-parse feature/x) --merge-commit $(git rev-parse HEAD)",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := commandContext(cmd)
			run, err := common.RunGateRunStart(ctx, store, *alias, params, deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun exec gate-run start planned.")
				return err
			}
			if ctx.Output != common.OutputJSON {
				if _, err := fmt.Fprintf(ctx.Stdout, "Started gate run %s (%s -> %s, status=%s).\n",
					run.GateRunID, run.SourceBranch, run.TargetBranch, run.Status); err != nil {
					return err
				}
			}
			return ctx.WriteResult(run)
		},
	}
	cmd.Flags().StringVar(&params.SourceBranch, "source-branch", "", "Branch being gated (required)")
	cmd.Flags().StringVar(&params.TargetBranch, "target-branch", "", "Branch the prospective merge lands onto (required)")
	cmd.Flags().StringVar(&params.SourceCommit, "source-commit", "", "Source branch tip commit this run tested (required)")
	cmd.Flags().StringVar(&params.MergeCommit, "merge-commit", "", "Prospective squash-merge commit this run tested (required unless --status is failed or inconclusive)")
	cmd.Flags().StringVar(&params.ReviewID, "review-id", "", "erun review this run gates, if one exists")
	cmd.Flags().StringVar(&params.Status, "status", "", "Status to start at: running (default), failed, or inconclusive")
	cmd.Flags().StringVar(&params.FailingStep, "failing-step", "", "Which gate step failed (required when --status is failed)")
	cmd.Flags().StringVar(&params.LogRef, "log-ref", "", "Where to read this run's own output (a job id, URL, or path)")
	addDryRunFlag(cmd)
	return cmd
}

func newExecGateRunReportCmd(store common.CloudReadStore, alias *string, deps common.CloudDependencies) *cobra.Command {
	var params common.GateRunReportParams
	cmd := &cobra.Command{
		Use:   "report GATE_RUN_ID",
		Short: "Report a gate run's outcome",
		Long: "Move GATE_RUN_ID from running to a terminal verdict: passed, failed, or inconclusive.\n\n" +
			"A wrapper that hit its own timeout, or a run interrupted by an environment-specific fault, must " +
			"report inconclusive -- never failed, which asserts a real gate step actually produced a red " +
			"verdict. --failing-step is required when --status is failed.\n\n" +
			"Reporting against a gate run that already has an outcome is refused: a verdict is immutable once " +
			"reached.\n\n" +
			"--dry-run traces the request without sending it.",
		Example: "  erun exec gate-run report abc123 --status passed\n" +
			"  erun exec gate-run report abc123 --status failed --failing-step 'erun build' --log-ref /tmp/build.json\n" +
			"  erun exec gate-run report abc123 --status inconclusive --log-ref 'wrapper hit its own 8m cap'",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := commandContext(cmd)
			params.GateRunID = args[0]
			run, err := common.RunGateRunReport(ctx, store, *alias, params, deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun exec gate-run report planned.")
				return err
			}
			if ctx.Output != common.OutputJSON {
				if _, err := fmt.Fprintf(ctx.Stdout, "Reported gate run %s as %s.\n", run.GateRunID, run.Status); err != nil {
					return err
				}
			}
			return ctx.WriteResult(run)
		},
	}
	cmd.Flags().StringVar(&params.Status, "status", "", "Outcome: passed, failed, or inconclusive (required)")
	cmd.Flags().StringVar(&params.FailingStep, "failing-step", "", "Which gate step failed (required when --status is failed)")
	cmd.Flags().StringVar(&params.LogRef, "log-ref", "", "Where to read this run's own output (a job id, URL, or path)")
	cmd.Flags().StringVar(&params.MergeCommit, "merge-commit", "", "Prospective squash-merge commit, if not already set when the run started")
	addDryRunFlag(cmd)
	return cmd
}

func newExecDiffCmd(findProjectRoot common.ProjectFinderFunc, runGit common.GitCommandRunnerFunc) *cobra.Command {
	var (
		jsonOutput     bool
		scope          string
		selectedCommit string
	)
	cmd := &cobra.Command{
		Use:          "diff",
		Short:        "Show the current git diff",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runExecDiffCommand(commandContext(cmd), findProjectRoot, runGit, execDiffOptions{
				JSON:           jsonOutput,
				Scope:          scope,
				SelectedCommit: selectedCommit,
			})
		},
	}
	addDryRunFlag(cmd)
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Write the parsed diff as JSON instead of raw text")
	cmd.Flags().StringVar(&scope, "scope", "", "Diff scope: current (default), all, or commit")
	cmd.Flags().StringVar(&selectedCommit, "selected-commit", "", "Oldest commit hash to include when --scope=commit")
	return cmd
}

func newExecRawCmd(findProjectRoot common.ProjectFinderFunc, runRaw common.RawCommandRunnerFunc) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "raw COMMAND [ARG...]",
		Short: "Run a raw command from the project root",
		Long: "Run an arbitrary command from the project root.\n\n" +
			"The command runs directly, not through a shell — wrap it in 'sh -c \"…\"' if you need " +
			"shell features. --dry-run traces the command (with secret-looking arguments redacted) " +
			"without running it. Use -- to pass flags through to the wrapped command.",
		Example:            "  erun exec raw go test ./...\n  erun exec raw --dry-run -- kubectl get pods --all-namespaces",
		Args:               cobra.MinimumNArgs(1),
		SilenceUsage:       true,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if rawCommandWantsHelp(args) {
				return cmd.Help()
			}
			rawArgs, dryRun := extractDryRunFlag(args)
			ctx := commandContext(cmd)
			if dryRun {
				ctx.DryRun = true
			}
			return runExecRawCommand(ctx, findProjectRoot, runRaw, rawArgs)
		},
	}
	addDryRunFlag(cmd)
	return cmd
}

// rawCommandWantsHelp intercepts help for `exec raw` itself: DisableFlagParsing
// stops cobra from handling --help, so without this `erun exec raw --help` would
// try to exec a binary named "--help". A literal `--` ends the scan so the
// wrapped command keeps its own --help.
func rawCommandWantsHelp(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if arg == "--help" || arg == "-h" {
			return true
		}
	}
	return false
}

// extractDryRunFlag pulls erun's own --dry-run out of the pass-through args:
// DisableFlagParsing hands the whole arg list through verbatim, so without this
// --dry-run would reach the wrapped command instead of enabling erun's dry-run.
// A literal `--` ends the scan so the wrapped command keeps its own --dry-run.
func extractDryRunFlag(args []string) ([]string, bool) {
	cleaned := make([]string, 0, len(args))
	dryRun := false
	passthrough := false
	for _, arg := range args {
		if passthrough {
			cleaned = append(cleaned, arg)
			continue
		}
		if arg == "--" {
			passthrough = true
			continue
		}
		switch arg {
		case "--dry-run", "--dry-run=true":
			dryRun = true
		case "--dry-run=false":
			dryRun = false
		default:
			cleaned = append(cleaned, arg)
		}
	}
	return cleaned, dryRun
}

func newExecWriteCmd(findProjectRoot common.ProjectFinderFunc) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "write PATH",
		Short: "Write stdin to a file in the project working tree",
		Long: "Write stdin to PATH inside the project working tree, byte-for-byte, creating parent directories as " +
			"needed. Content never passes through a shell, so nothing in it is reinterpreted. The write is refused " +
			"if PATH would resolve outside the project root.\n\n" +
			"--dry-run resolves the path and reports the byte count it would write, without writing.",
		Example:      "  erun exec write values.yaml < new-values.yaml\n  printf 'hello\\n' | erun exec write notes/todo.txt",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExecWriteCommand(commandContext(cmd), findProjectRoot, args[0])
		},
	}
	addDryRunFlag(cmd)
	return cmd
}

func runExecWriteCommand(ctx common.Context, findProjectRoot common.ProjectFinderFunc, path string) error {
	if findProjectRoot == nil {
		findProjectRoot = common.FindProjectRoot
	}
	_, projectRoot, err := findProjectRoot()
	if err != nil {
		return err
	}
	content, err := io.ReadAll(ctx.Stdin)
	if err != nil {
		return fmt.Errorf("read content from stdin: %w", err)
	}
	result, err := common.WriteWorkingTreeFile(ctx, projectRoot, common.WriteWorkingTreeFileParams{
		Path:    path,
		Content: string(content),
	})
	if err != nil {
		return err
	}
	if ctx.DryRun {
		return nil
	}
	ctx.Info(fmt.Sprintf("Wrote %s (%d bytes).", result.Path, result.Bytes))
	return ctx.WriteResult(result)
}

func newExecCommitCmd(findProjectRoot common.ProjectFinderFunc) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "commit BRANCH [PATH...]",
		Short: "Commit every change, or only the declared paths, in the project working tree",
		Long: "Stage every change in the project working tree and commit it with a message read verbatim from " +
			"stdin — never a shell, so nothing in the message is reinterpreted. BRANCH must match the working " +
			"tree's actual current branch; the commit is refused, loudly, when it does not, rather than landing " +
			"on whatever branch HEAD happens to be on.\n\n" +
			"Pass one or more PATH arguments to stage and commit only those paths instead of every change. A " +
			"scoped commit is also refused, loudly, if the tree has changes outside the declared paths — an " +
			"unrelated writer's edits can never be absorbed into a commit that did not ask for them.\n\n" +
			"--dry-run verifies the branch and traces the files that would be committed, without staging or committing.",
		Example: "  echo 'fix the values typo' | erun exec commit main\n" +
			"  echo 'fix the values typo' | erun exec commit main values.yaml",
		Args:         cobra.MinimumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExecCommitCommand(commandContext(cmd), findProjectRoot, args[0], args[1:])
		},
	}
	addDryRunFlag(cmd)
	return cmd
}

func runExecCommitCommand(ctx common.Context, findProjectRoot common.ProjectFinderFunc, branch string, paths []string) error {
	if findProjectRoot == nil {
		findProjectRoot = common.FindProjectRoot
	}
	_, projectRoot, err := findProjectRoot()
	if err != nil {
		return err
	}
	message, err := io.ReadAll(ctx.Stdin)
	if err != nil {
		return fmt.Errorf("read commit message from stdin: %w", err)
	}
	result, err := common.CommitWorkingTree(ctx, projectRoot, common.CommitWorkingTreeParams{
		Branch:  branch,
		Message: string(message),
		Paths:   paths,
	}, common.CommitWorkingTreeDependencies{})
	if err != nil {
		return err
	}
	if ctx.DryRun {
		return nil
	}
	ctx.Info(fmt.Sprintf("Committed %d file(s) as %s on %s.", len(result.Files), result.Commit, result.Branch))
	return ctx.WriteResult(result)
}

func newExecPushCmd(findProjectRoot common.ProjectFinderFunc) *cobra.Command {
	var remote string
	cmd := &cobra.Command{
		Use:   "push BRANCH",
		Short: "Push the project working tree's current branch to a remote",
		Long: "Push the project working tree's current branch to a remote.\n\n" +
			"BRANCH must match the working tree's actual current branch; the push is refused, loudly, " +
			"when it does not, rather than pushing whatever branch HEAD happens to be on. A real, " +
			"immediate mutation of shared remote state: a branch a hosted review or another reviewer " +
			"can only ever fetch once it has actually landed there.\n\n" +
			"--dry-run traces the push without running it.",
		Example:      "  erun exec push feature/add-widget\n  erun exec push feature/add-widget --remote upstream",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExecPushCommand(commandContext(cmd), findProjectRoot, args[0], remote)
		},
	}
	cmd.Flags().StringVar(&remote, "remote", "", "Git remote to push to (defaults to origin)")
	addDryRunFlag(cmd)
	return cmd
}

func runExecPushCommand(ctx common.Context, findProjectRoot common.ProjectFinderFunc, branch, remote string) error {
	if findProjectRoot == nil {
		findProjectRoot = common.FindProjectRoot
	}
	_, projectRoot, err := findProjectRoot()
	if err != nil {
		return err
	}
	result, err := common.PushWorkingTreeBranch(ctx, projectRoot, common.PushWorkingTreeBranchParams{
		Branch: branch,
		Remote: remote,
	}, common.PushWorkingTreeBranchDependencies{})
	if err != nil {
		return err
	}
	if ctx.DryRun {
		return nil
	}
	ctx.Info(fmt.Sprintf("Pushed %s to %s (%s).", result.Branch, result.Remote, result.Commit))
	return ctx.WriteResult(result)
}

func newExecMergeCmd(findProjectRoot common.ProjectFinderFunc) *cobra.Command {
	var remote string
	cmd := &cobra.Command{
		Use:   "merge TARGET_BRANCH",
		Short: "Merge a branch into the project working tree's current branch",
		Long: "Fetch TARGET_BRANCH from the remote and merge it into the project working tree's current branch " +
			"with an explicit merge commit. Never rebases: review comments anchor to a commit id, and a rewrite " +
			"would orphan every thread on an open review.\n\n" +
			"A conflicted merge is reported as a distinct, named outcome rather than a generic failure. The " +
			"worktree is left exactly as git left it, mid-merge — resolve the conflicted files and commit, or " +
			"run `git merge --abort` to back out, before doing anything else.\n\n" +
			"--dry-run traces the fetch and merge without running them.",
		Example:      "  erun exec merge main\n  erun exec merge release/2026.9 --remote upstream",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExecMergeCommand(commandContext(cmd), findProjectRoot, args[0], remote)
		},
	}
	cmd.Flags().StringVar(&remote, "remote", "", "Git remote to fetch and merge from (defaults to origin)")
	addDryRunFlag(cmd)
	return cmd
}

func runExecMergeCommand(ctx common.Context, findProjectRoot common.ProjectFinderFunc, targetBranch, remote string) error {
	if findProjectRoot == nil {
		findProjectRoot = common.FindProjectRoot
	}
	_, projectRoot, err := findProjectRoot()
	if err != nil {
		return err
	}
	result, err := common.MergeWorkingTreeBranch(ctx, projectRoot, common.MergeWorkingTreeBranchParams{
		TargetBranch: targetBranch,
		Remote:       remote,
	}, common.MergeWorkingTreeBranchDependencies{})
	if err != nil {
		return err
	}
	if ctx.DryRun {
		return nil
	}
	ctx.Info(fmt.Sprintf("Merged %s/%s into %s (%s).", result.Remote, result.TargetBranch, result.Branch, result.Commit))
	return ctx.WriteResult(result)
}

func newExecGateMergeCmd(findProjectRoot common.ProjectFinderFunc) *cobra.Command {
	var (
		target string
		remote string
	)
	cmd := &cobra.Command{
		Use:   "gate-merge SOURCE_BRANCH",
		Short: "Build the prospective squash merge a merge queue promotion gates",
		Long: "Fetch --target and SOURCE_BRANCH from the remote, check out a fresh local branch named --target at " +
			"its own current remote tip, and squash-merge SOURCE_BRANCH onto it as one commit — the commit message " +
			"read verbatim from stdin, never a shell, so nothing in it is reinterpreted. This is for the " +
			"environment a review's merge queue promotes to MERGE: gate-merge, then `erun build` against the " +
			"result, then `erun review record-build --gate` and, only on success, `erun exec push` and " +
			"`erun review report-merged`.\n\n" +
			"The working tree must already be clean: this checks out a different local branch than whatever the " +
			"tree is currently on, so uncommitted work there is refused rather than silently carried onto the " +
			"prospective merge.\n\n" +
			"A conflicted squash is reported as a distinct, named outcome. The worktree is left exactly as git " +
			"left it, mid-conflict — resolve the conflicted files and commit, or run `git merge --abort` to back " +
			"out, before doing anything else.\n\n" +
			"--dry-run traces the fetch, checkout, squash merge, and commit without running them.",
		Example: "  echo 'Add widget' | erun exec gate-merge feature/add-widget --target main\n" +
			"  echo 'Add widget' | erun exec gate-merge feature/add-widget --target main --remote upstream",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExecGateMergeCommand(commandContext(cmd), findProjectRoot, args[0], target, remote)
		},
	}
	cmd.Flags().StringVar(&target, "target", "", "Target branch the squash merge lands onto (required)")
	cmd.Flags().StringVar(&remote, "remote", "", "Git remote to fetch and merge from (defaults to origin)")
	addDryRunFlag(cmd)
	return cmd
}

func runExecGateMergeCommand(ctx common.Context, findProjectRoot common.ProjectFinderFunc, sourceBranch, targetBranch, remote string) error {
	if strings.TrimSpace(targetBranch) == "" {
		return fmt.Errorf("--target is required")
	}
	if findProjectRoot == nil {
		findProjectRoot = common.FindProjectRoot
	}
	_, projectRoot, err := findProjectRoot()
	if err != nil {
		return err
	}
	message, err := io.ReadAll(ctx.Stdin)
	if err != nil {
		return fmt.Errorf("read commit message from stdin: %w", err)
	}
	result, err := common.GateMergeWorkingTree(ctx, projectRoot, common.GateMergeWorkingTreeParams{
		SourceBranch: sourceBranch,
		TargetBranch: targetBranch,
		Message:      string(message),
		Remote:       remote,
	}, common.GateMergeWorkingTreeDependencies{})
	if err != nil {
		return err
	}
	if ctx.DryRun {
		return nil
	}
	ctx.Info(fmt.Sprintf("Squash-merged %s/%s onto %s (%s).", result.Remote, result.SourceBranch, result.TargetBranch, result.Commit))
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
		Short: "Report a GitHub commit status for a merge queue gate result",
		Long: "Report a commit status on GitHub for COMMIT. This is the last step in the merge queue gate " +
			"(`erun exec gate-merge`, `erun build`, `erun review record-build --gate`): report success once the " +
			"gate build is green, or failure the moment it is not, naming which gate step failed in " +
			"--description. A required status check on the remote's branch protection has nothing to require " +
			"until this reports it.\n\n" +
			"COMMIT should be the review's source branch tip — the pull request's own head commit — never the " +
			"local prospective squash-merge commit `gate-merge` produces: GitHub only evaluates a required check " +
			"against a commit reachable from the open pull request, and the squash commit does not exist there " +
			"until after the gate has already passed and pushed.\n\n" +
			"--dry-run traces the request without sending it.",
		Example: "  erun exec report-commit-status $(git rev-parse HEAD) --state failure \\\n" +
			"    --description 'erun build failed against the prospective merge' \\\n" +
			"    --remote-url https://github.com/org/repo.git\n" +
			"  erun exec report-commit-status $(git rev-parse HEAD) --state success \\\n" +
			"    --description 'gate build passed' --remote-url https://github.com/org/repo.git",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExecReportCommitStatusCommand(commandContext(cmd), args[0], state, statusCtx, description, targetURL, remoteURL)
		},
	}
	cmd.Flags().StringVar(&state, "state", "", "Commit status state: success, failure, error, or pending (required)")
	cmd.Flags().StringVar(&statusCtx, "context", "", "Status check name a required-status-checks rule names (defaults to erun/merge-gate)")
	cmd.Flags().StringVar(&description, "description", "", "Short human-readable status, naming which gate step failed on failure (required)")
	cmd.Flags().StringVar(&targetURL, "target-url", "", "URL a reader clicks through to from the status (optional)")
	cmd.Flags().StringVar(&remoteURL, "remote-url", "", "The github.com remote to report the status against (required)")
	addDryRunFlag(cmd)
	return cmd
}

func runExecReportCommitStatusCommand(ctx common.Context, commit, state, statusCtx, description, targetURL, remoteURL string) error {
	result, err := common.ReportCommitStatus(ctx, common.ReportCommitStatusParams{
		RemoteURL:   remoteURL,
		Commit:      commit,
		State:       common.ReportCommitStatusState(state),
		Context:     statusCtx,
		Description: description,
		TargetURL:   targetURL,
	}, common.ReportCommitStatusDependencies{})
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
		Short: "Close the GitHub pull request a merge queue gate actually shipped",
		Long: "Close BRANCH's open pull request on GitHub and record LANDING_COMMIT on it. This runs after `erun " +
			"review report-merged` has already succeeded: gate-merge's squash commit is never the branch head GitHub " +
			"tracks, so GitHub never reconciles a queued merge with its pull request on its own, and the commit that " +
			"actually shipped exists nowhere the pull request can see.\n\n" +
			"Safe when BRANCH has no open pull request against --target: this is a no-op, not an error, since " +
			"queueing a plain branch with no review is legitimate.\n\n" +
			"Refuses, loudly, when the pull request's current head does not match --gated-commit — something " +
			"pushed to BRANCH after the gate fetched it, so the gated content is not what closing would discard.\n\n" +
			"--dry-run traces the lookup without closing or commenting on anything.",
		Example: "  erun exec close-pr feature/add-widget --target main \\\n" +
			"    --remote-url https://github.com/org/repo.git \\\n" +
			"    --gated-commit $(git rev-parse origin/feature/add-widget) --landing-commit $(git rev-parse HEAD)",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExecClosePRCommand(commandContext(cmd), args[0], target, remoteURL, gatedCommit, landingCommit)
		},
	}
	cmd.Flags().StringVar(&target, "target", "", "The pull request's base branch (required)")
	cmd.Flags().StringVar(&remoteURL, "remote-url", "", "The github.com remote the pull request lives on (required)")
	cmd.Flags().StringVar(&gatedCommit, "gated-commit", "", "BRANCH's tip when the gate actually fetched and tested it (required)")
	cmd.Flags().StringVar(&landingCommit, "landing-commit", "", "The commit that actually landed on --target, recorded in a comment on the pull request (required)")
	addDryRunFlag(cmd)
	return cmd
}

func runExecClosePRCommand(ctx common.Context, branch, target, remoteURL, gatedCommit, landingCommit string) error {
	result, err := common.ClosePullRequest(ctx, common.ClosePullRequestParams{
		RemoteURL:     remoteURL,
		Branch:        branch,
		TargetBranch:  target,
		GatedCommit:   gatedCommit,
		LandingCommit: landingCommit,
	}, common.ClosePullRequestDependencies{})
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

type execDiffOptions struct {
	JSON           bool
	Scope          string
	SelectedCommit string
}

func runExecDiffCommand(ctx common.Context, findProjectRoot common.ProjectFinderFunc, runGit common.GitCommandRunnerFunc, opts execDiffOptions) error {
	if findProjectRoot == nil {
		findProjectRoot = common.FindProjectRoot
	}
	_, projectRoot, err := findProjectRoot()
	if err != nil {
		return err
	}
	ctx.TraceCommand(projectRoot, "git", "diff", "--no-color", "--no-ext-diff")
	if ctx.DryRun {
		return nil
	}
	result, err := resolveExecDiff(projectRoot, runGit, opts)
	if err != nil {
		return err
	}
	if opts.JSON {
		encoder := json.NewEncoder(ctx.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}
	return common.WriteRawDiff(ctx.Stdout, result)
}

func resolveExecDiff(projectRoot string, runGit common.GitCommandRunnerFunc, opts execDiffOptions) (common.DiffResult, error) {
	if opts.Scope == "" && opts.SelectedCommit == "" {
		return common.ResolveGitDiff(projectRoot, runGit)
	}
	if opts.SelectedCommit != "" && opts.Scope != "commit" {
		return common.DiffResult{}, fmt.Errorf("--selected-commit requires --scope=commit")
	}
	return common.ResolveGitDiffWithOptions(projectRoot, common.DiffOptions{
		Scope:          opts.Scope,
		SelectedCommit: opts.SelectedCommit,
	}, runGit)
}

func runExecRawCommand(ctx common.Context, findProjectRoot common.ProjectFinderFunc, runRaw common.RawCommandRunnerFunc, args []string) error {
	if findProjectRoot == nil {
		findProjectRoot = common.FindProjectRoot
	}
	_, projectRoot, err := findProjectRoot()
	if err != nil {
		return err
	}
	return common.RunRawCommand(ctx, common.RawCommandSpec{
		Dir:  projectRoot,
		Args: args,
	}, runRaw)
}

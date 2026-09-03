package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
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
		newExecReconcileBypassCmd(store, deps),
		newExecPlanRulesetBypassCmd(),
		newExecRouteCheckCmd(store, deps),
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
			"--status failed is reported as inconclusive instead when --failing-step, --log-ref, or a local " +
			"file --log-ref points at names a known erun infrastructure failure (a registry or the network " +
			"giving up, e.g. a ghcr.io TLS handshake timeout) rather than a real verdict about the change.\n\n" +
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
			"--status failed is reported as inconclusive instead when --failing-step, --log-ref, or a local " +
			"file --log-ref points at names a known erun infrastructure failure (a registry or the network " +
			"giving up, e.g. a ghcr.io TLS handshake timeout) rather than a real verdict about the change.\n\n" +
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
		sources    []string
		target     string
		remote     string
		underLease string
	)
	cmd := &cobra.Command{
		Use:   "gate-merge --source SOURCE_BRANCH [--source SOURCE_BRANCH...]",
		Short: "Build the prospective merge a merge queue promotion (or batch) gates",
		Long: "Fetch --target and every --source from the remote, check out a fresh local branch named --target " +
			"at its own current remote tip, then squash-merge each --source onto it in turn, each as its own " +
			"commit — leaving a stack of one commit per landed source. Repeat --source to batch several " +
			"unmerged branches into one prospective merge, so the gate that follows tests whether they compile " +
			"*together*, not just individually; a single --source is the ordinary one-branch gate. This is for " +
			"the environment a review's merge queue promotes to MERGE: gate-merge, then `erun build` against " +
			"the result, then `erun review record-build --gate` and, only on success, `erun exec push` and " +
			"`erun review report-merged`.\n\n" +
			"Commit messages are read verbatim from stdin, never a shell argument, so nothing in them is " +
			"reinterpreted: one message per --source, in the same order, separated by NUL bytes (a single " +
			"--source needs no separator at all).\n\n" +
			"The working tree must already be clean: this checks out a different local branch than whatever the " +
			"tree is currently on, so uncommitted work there is refused rather than silently carried onto the " +
			"prospective merge.\n\n" +
			"A source whose squash conflicts is skipped, not fatal: the working tree is reset back to a clean " +
			"state and the conflict recorded in the result, and the rest of the batch still gates against the " +
			"tree as it stood before that attempt. A batch where every source is skipped lands nothing and " +
			"exits non-zero.\n\n" +
			"Refused outright while something else holds this environment exclusively: this rewrites the one " +
			"shared worktree, so two gate-merges in flight at once do not merely slow each other down, they " +
			"corrupt each other's accounting — a drive has already reported pushing a commit that belonged to " +
			"another batch's tree. A caller that took the claim itself passes --under-lease so its own hold " +
			"does not refuse it.\n\n" +
			"--dry-run traces the fetch, checkout, and each squash merge and commit without running them.",
		Example: "  echo 'Add widget' | erun exec gate-merge --source feature/add-widget --target main\n" +
			"  printf 'Add widget\\0Add gadget' | erun exec gate-merge --source feature/add-widget " +
			"--source feature/add-gadget --target main\n" +
			"  echo 'Add widget' | erun exec gate-merge --source feature/add-widget --target main --remote upstream",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExecGateMergeCommand(commandContext(cmd), findProjectRoot, sources, target, remote, underLease)
		},
	}
	cmd.Flags().StringArrayVar(&sources, "source", nil, "Branch to fetch and squash-merge in; repeat to batch several branches onto one prospective merge (required, at least once)")
	cmd.Flags().StringVar(&target, "target", "", "Target branch the squash merge(s) land onto (required)")
	cmd.Flags().StringVar(&remote, "remote", "", "Git remote to fetch and merge from (defaults to origin)")
	cmd.Flags().StringVar(&underLease, "under-lease", "", "Id of an exclusive environment claim this caller already holds, so its own hold does not refuse it")
	addDryRunFlag(cmd)
	return cmd
}

func runExecGateMergeCommand(ctx common.Context, findProjectRoot common.ProjectFinderFunc, sourceBranches []string, targetBranch, remote, underLease string) error {
	if len(sourceBranches) == 0 {
		return fmt.Errorf("at least one --source is required")
	}
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
	sources, err := readGateMergeSources(ctx, sourceBranches)
	if err != nil {
		return err
	}
	result, err := common.GateMergeWorkingTree(ctx, projectRoot, common.GateMergeWorkingTreeParams{
		Sources:      sources,
		TargetBranch: targetBranch,
		Remote:       remote,
		UnderLeaseID: underLease,
	}, common.GateMergeWorkingTreeDependencies{})
	if err != nil {
		return err
	}
	if ctx.DryRun {
		return nil
	}
	return reportGateMergeResult(ctx, result)
}

// readGateMergeSources reads one NUL-separated commit message per source
// branch from stdin, in the same order as the --source flags.
func readGateMergeSources(ctx common.Context, sourceBranches []string) ([]common.GateMergeSource, error) {
	stdin, err := io.ReadAll(ctx.Stdin)
	if err != nil {
		return nil, fmt.Errorf("read commit message(s) from stdin: %w", err)
	}
	messages := strings.Split(string(stdin), "\x00")
	if len(messages) != len(sourceBranches) {
		return nil, fmt.Errorf("expected %d NUL-separated commit message(s) on stdin (one per --source), got %d", len(sourceBranches), len(messages))
	}
	sources := make([]common.GateMergeSource, len(sourceBranches))
	for i, branch := range sourceBranches {
		sources[i] = common.GateMergeSource{Branch: branch, Message: messages[i]}
	}
	return sources, nil
}

// reportGateMergeResult prints each landed and skipped source, then refuses
// if the batch landed nothing at all — there is nothing to gate against an
// unchanged target.
func reportGateMergeResult(ctx common.Context, result common.GateMergeWorkingTreeResult) error {
	for _, landed := range result.Landed {
		ctx.Info(fmt.Sprintf("Squash-merged %s/%s onto %s (%s).", result.Remote, landed.SourceBranch, result.TargetBranch, landed.Commit))
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

func newExecReconcileBypassCmd(store common.CloudReadStore, deps common.CloudDependencies) *cobra.Command {
	var (
		remoteURL      string
		rulesetID      int64
		targetBranch   string
		since          string
		expectedActors []string
		alias          string
	)
	cmd := &cobra.Command{
		Use:   "reconcile-bypass",
		Short: "Check every ruleset-bypassed push against a real passed gate run",
		Long: "Cross-reference GitHub's own bypass ledger (`GET .../rulesets/rule-suites`) for --ruleset-id on " +
			"--target-branch against erun's gate runs: every push that bypassed --ruleset-id is reported next to " +
			"what accounts for it. A push is RECONCILED when a PASSED gate run built one of the commits it " +
			"landed, RELEASE when a tag in the repository points at one of them (a release stamps, tags and then " +
			"pushes, so its own commits were never gated as a merge), and UNRECONCILED when nothing accounts for " +
			"it at all.\n\n" +
			"--expected-actor names the identities allowed to hold the bypass grant. Any other actor's bypass is " +
			"reported UNEXPECTED_ACTOR even when the content it landed was really gated -- that is what makes " +
			"narrowing the grant to one non-human identity (`erun exec plan-ruleset-bypass`) observable rather " +
			"than merely configured.\n\n" +
			"Exits non-zero, after printing the full report, when any push is unaccounted for or any unnamed " +
			"identity bypassed -- a bypass nobody can explain is loud, never silent.\n\n" +
			"--dry-run traces the GitHub and platform lookups without sending them.",
		Example: "  erun exec reconcile-bypass --ruleset-id 11081432 --target-branch main \\\n" +
			"    --expected-actor erun-merge-queue",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runExecReconcileBypassCommand(commandContext(cmd), store, alias, common.ReconcileBypassParams{
				RemoteURL:      remoteURL,
				RulesetID:      rulesetID,
				TargetBranch:   targetBranch,
				Since:          since,
				ExpectedActors: expectedActors,
			}, deps)
		},
	}
	cmd.Flags().StringVar(&remoteURL, "remote-url", "", "The github.com remote the ruleset lives on (defaults to the current checkout's origin)")
	cmd.Flags().Int64Var(&rulesetID, "ruleset-id", 0, "The ruleset to check bypasses against (required)")
	cmd.Flags().StringVar(&targetBranch, "target-branch", "", "The ruleset's protected branch (required)")
	cmd.Flags().StringVar(&since, "since", "", "Narrow the GitHub lookup window: hour, day, week, or month (defaults to GitHub's own window)")
	cmd.Flags().StringArrayVar(&expectedActors, "expected-actor", nil, "An identity allowed to hold the bypass grant; repeatable. Any other actor's bypass is reported UNEXPECTED_ACTOR")
	cmd.Flags().StringVar(&alias, "erun-alias", "", "erun platform cloud alias to target (defaults to the sole configured erun-type alias)")
	addDryRunFlag(cmd)
	return cmd
}

// newExecPlanRulesetBypassCmd builds `erun exec plan-ruleset-bypass`: the
// read-only half of narrowing a protected branch's bypass grant to one
// non-human identity. It resolves the edit from the live ruleset, proves the
// identity can already push, and writes the payload each stage and the
// rollback apply — it never changes repository settings itself.
func newExecPlanRulesetBypassCmd() *cobra.Command {
	var params common.PlanRulesetBypassParams
	cmd := &cobra.Command{
		Use:   "plan-ruleset-bypass",
		Short: "Plan narrowing a ruleset's bypass grant to one non-human queue identity",
		Long: "A merge queue's push is itself a direct push, so it must bypass the branch's pull-request rule. " +
			"That is only accountable if exactly one nameable, non-human identity can bypass at all -- and " +
			"GitHub's bypass is per-actor, not per-rule, so adding a required status check on top of a broad " +
			"`always` grant changes nothing for whoever holds it.\n\n" +
			"This resolves the exact edit from the ruleset as it actually is, in two stages that never leave the " +
			"branch unmergeable: stage 1 grants --queue-actor an `always` bypass alongside today's actors (both " +
			"paths open, so the queue can be proven under the new identity first), stage 2 demotes every other " +
			"`always` actor to `pull_request` (an emergency lever that still requires a pull request). It refuses " +
			"up front when the queue identity cannot already push, when GitHub will not show the ruleset's bypass " +
			"actors (plan with an admin token), or when --target-branch is not a branch this ruleset governs.\n\n" +
			"Writes three payload files -- stage1, stage2 and rollback (today's bypass list, exactly) -- and " +
			"prints the `gh api` calls that apply, verify and revert them. It never writes to GitHub: a ruleset " +
			"governs every contributor's merges, so applying the edit stays a deliberate, human step.\n\n" +
			"--dry-run traces the GitHub lookups and the files it would write without sending or writing anything.",
		Example: "  erun exec plan-ruleset-bypass --ruleset-id 11081432 --target-branch main \\\n" +
			"    --queue-actor erun-merge-queue --out-dir .erun/ruleset-plan",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runExecPlanRulesetBypassCommand(commandContext(cmd), params)
		},
	}
	cmd.Flags().StringVar(&params.RemoteURL, "remote-url", "", "The github.com remote the ruleset lives on (defaults to the current checkout's origin)")
	cmd.Flags().Int64Var(&params.RulesetID, "ruleset-id", 0, "The ruleset whose bypass grant is being narrowed (required)")
	cmd.Flags().StringVar(&params.TargetBranch, "target-branch", "", "The protected branch the ruleset must govern, checked against its own conditions")
	cmd.Flags().StringVar(&params.QueueActorType, "queue-actor-type", "User", "The queue identity's GitHub actor type: User, Integration, Team, RepositoryRole, OrganizationAdmin, or DeployKey")
	cmd.Flags().StringVar(&params.QueueActor, "queue-actor", "", "The queue identity: a login for User, otherwise the numeric actor id (required)")
	cmd.Flags().StringVar(&params.OutDir, "out-dir", ".", "Directory the stage1, stage2 and rollback payload files are written to")
	addDryRunFlag(cmd)
	return cmd
}

func runExecPlanRulesetBypassCommand(ctx common.Context, params common.PlanRulesetBypassParams) error {
	result, err := common.PlanRulesetBypass(ctx, params, common.PlanRulesetBypassDependencies{})
	if err != nil {
		return err
	}
	if ctx.DryRun {
		_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun exec plan-ruleset-bypass planned.")
		return err
	}
	if ctx.Output != common.OutputJSON {
		if err := writeRulesetBypassPlan(ctx, result); err != nil {
			return err
		}
	}
	return ctx.WriteResult(result)
}

// writeRulesetBypassPlan renders the plan as the ordered set of commands an
// operator runs: apply, verify, and — if any of it goes wrong — revert. A plan
// that named the edit without naming its verification and its way back would
// be exactly the dead end this command exists to avoid.
func writeRulesetBypassPlan(ctx common.Context, result common.PlanRulesetBypassResult) error {
	out := new(strings.Builder)
	fmt.Fprintf(out, "Ruleset %d (%q) on %s/%s\n", result.RulesetID, result.RulesetName, result.Owner, result.Repo)
	fmt.Fprintf(out, "  bypass actors today (%d):\n", len(result.CurrentBypassActors))
	for _, actor := range result.CurrentBypassActors {
		fmt.Fprintf(out, "    %-18s %-8s %s\n", actor.ActorType, formatRulesetActorID(actor.ActorID), actor.BypassMode)
	}
	fmt.Fprintf(out, "  this token's own bypass: %s\n", orUnknown(result.CallerCanBypass))
	fmt.Fprintf(out, "  queue identity: %s %s (actor id %d)%s\n", result.QueueActorType, result.QueueActor,
		result.QueueActorID, formatQueueActorAccess(result.QueueActorPushAccess))

	apply := "gh api --method PUT repos/" + result.Owner + "/" + result.Repo + "/rulesets/" +
		strconv.FormatInt(result.RulesetID, 10) + " --input "
	fmt.Fprintf(out, "\nStage 1 -- grant the queue identity bypass alongside today's actors:\n  %s%s\n", apply, result.Stage1File)
	fmt.Fprintf(out, "Then prove the queue under the new identity: run one real gated merge as %s before stage 2.\n", result.QueueActor)
	fmt.Fprintf(out, "\nStage 2 -- demote every other always-bypass actor to pull_request:\n  %s%s\n", apply, result.Stage2File)

	read := "gh api repos/" + result.Owner + "/" + result.Repo + "/rulesets/" +
		strconv.FormatInt(result.RulesetID, 10) + " --jq .current_user_can_bypass"
	fmt.Fprintf(out, "\nVerify (GitHub answers this per token, so run it as each identity):\n")
	fmt.Fprintf(out, "  GH_TOKEN=<%s's token> %s   # want: always\n", result.QueueActor, read)
	fmt.Fprintf(out, "  GH_TOKEN=<a human's token> %s   # want: pull_requests_only after stage 2\n", read)
	fmt.Fprintf(out, "  erun exec reconcile-bypass --ruleset-id %d --target-branch %s --expected-actor %s\n",
		result.RulesetID, orUnknown(result.TargetBranch), result.QueueActor)
	fmt.Fprintf(out, "\nRollback (restores today's bypass list exactly):\n  %s%s\n", apply, result.RollbackFile)
	_, err := fmt.Fprint(ctx.Stdout, out.String())
	return err
}

func formatRulesetActorID(actorID *int64) string {
	if actorID == nil {
		return "-"
	}
	return strconv.FormatInt(*actorID, 10)
}

func formatQueueActorAccess(access string) string {
	if access == "" {
		return ", push access not verifiable for this actor type"
	}
	return ", " + access + " access"
}

// orUnknown keeps a plan readable when GitHub answered nothing for a field
// rather than printing a bare blank the reader cannot interpret.
func orUnknown(value string) string {
	if value == "" {
		return "(unknown)"
	}
	return value
}

// newExecRouteCheckCmd builds `erun exec route-check`: proves every route
// erun-backend-api's router registers is actually reachable on a deployed
// plane. A route can merge, get unit-tested, and close its issue while
// still 404ing on the live control plane for months, because nothing but a
// human running the CLI by hand had ever exercised the deployed route.
func newExecRouteCheckCmd(store common.CloudReadStore, deps common.CloudDependencies) *cobra.Command {
	var (
		routesDir string
		alias     string
	)
	cmd := &cobra.Command{
		Use:   "route-check",
		Short: "Prove every registered API route is reachable on a deployed plane",
		Long: "Reads erun-backend-api's own registered routes straight out of its source (never a hand-maintained " +
			"list) and GETs each one against the plane --erun-alias resolves. It first sanity-probes GET /v1/whoami " +
			"and refuses outright if that does not answer, so a down or misconfigured plane is never reported as " +
			"every route missing. Every probe is a plain GET regardless of a route's own registered method -- Go's " +
			"router reports 405 for a path it knows under a different method, so this never risks creating, " +
			"updating, or deleting anything -- and only the plane's own unmodified \"404 page not found\" means a " +
			"route was never registered at all; an application-level 404 (a well-formed request for an id that " +
			"doesn't exist) always looks different and is reported reachable.\n\n" +
			"Exits non-zero, after printing the full report, when the plane cannot be reached at all or when any " +
			"registered route comes back missing.\n\n" +
			"--dry-run traces the resolved plane and the route inventory without sending any request.",
		Example:      "  erun exec route-check --erun-alias erun+api.erunpaas.com@erun",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runExecRouteCheckCommand(commandContext(cmd), store, alias, common.RouteCheckParams{
				RoutesDir: routesDir,
			}, deps)
		},
	}
	cmd.Flags().StringVar(&routesDir, "routes-dir", "",
		"Path to erun-backend-api/internal/routes (defaults to that path under the current checkout's project root)")
	cmd.Flags().StringVar(&alias, "erun-alias", "", "erun platform cloud alias to target (defaults to the sole configured erun-type alias)")
	addDryRunFlag(cmd)
	return cmd
}

func runExecRouteCheckCommand(ctx common.Context, store common.CloudReadStore, alias string, params common.RouteCheckParams, deps common.CloudDependencies) error {
	result, err := common.RunRouteCheck(ctx, store, alias, params, deps)
	if err != nil {
		return err
	}
	if ctx.DryRun {
		_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun exec route-check planned.")
		return err
	}
	if ctx.Output != common.OutputJSON {
		if err := writeRouteCheckReport(ctx, result); err != nil {
			return err
		}
	}
	if err := ctx.WriteResult(result); err != nil {
		return err
	}
	return routeCheckExitError(result)
}

// writeRouteCheckReport prints one line per missing route -- the loud
// failure this command exists to surface -- and a summary line always, so a
// clean run is visibly clean rather than silent.
func writeRouteCheckReport(ctx common.Context, result common.RouteCheckResult) error {
	if !result.PlaneReachable {
		_, err := fmt.Fprintf(ctx.Stdout, "%s did not answer the sanity probe (%s); no routes were checked.\n",
			result.APIURL, result.UnreachableReason)
		return err
	}
	for _, missing := range result.Missing {
		if _, err := fmt.Fprintf(ctx.Stdout, "MISSING  %-7s %s\n", missing.Method, missing.Path); err != nil {
			return err
		}
	}
	for _, probeErr := range result.Errors {
		if _, err := fmt.Fprintf(ctx.Stdout, "ERROR    %-7s %s: %s\n", probeErr.Method, probeErr.Path, probeErr.Detail); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(ctx.Stdout, "%d/%d route(s) reachable on %s.\n",
		result.RoutesChecked-len(result.Missing)-len(result.Errors), result.RoutesChecked, result.APIURL)
	return err
}

// routeCheckExitError makes a missing route (or a probe that could not even
// complete) a non-zero exit, after the full report has already printed --
// the same "print everything, then fail loudly" shape reconcileBypassExitError
// uses.
func routeCheckExitError(result common.RouteCheckResult) error {
	if !result.PlaneReachable {
		return fmt.Errorf("route-check: %s did not answer the sanity probe (%s); refusing to report on an unreachable plane",
			result.APIURL, result.UnreachableReason)
	}
	var problems []string
	if len(result.Missing) > 0 {
		problems = append(problems, fmt.Sprintf("%d registered route(s) are missing on the deployed plane", len(result.Missing)))
	}
	if len(result.Errors) > 0 {
		problems = append(problems, fmt.Sprintf("%d route probe(s) could not complete", len(result.Errors)))
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("of %d route(s) checked on %s: %s", result.RoutesChecked, result.APIURL, strings.Join(problems, "; "))
}

func runExecReconcileBypassCommand(ctx common.Context, store common.CloudReadStore, alias string, params common.ReconcileBypassParams, deps common.CloudDependencies) error {
	result, err := common.ReconcileBypass(ctx, store, alias, params, deps, common.ReconcileBypassDependencies{})
	if err != nil {
		return err
	}
	if ctx.DryRun {
		_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun exec reconcile-bypass planned.")
		return err
	}
	if ctx.Output != common.OutputJSON {
		if err := writeReconcileBypassReport(ctx, result); err != nil {
			return err
		}
	}
	if err := ctx.WriteResult(result); err != nil {
		return err
	}
	return reconcileBypassExitError(result)
}

// reconcileBypassExitError turns the report's two distinct failures into one
// non-zero exit, naming both rather than collapsing them: content nobody
// gated and an identity nobody expected are different problems with different
// remedies.
func reconcileBypassExitError(result common.ReconcileBypassResult) error {
	var problems []string
	if result.Unreconciled > 0 {
		problems = append(problems, fmt.Sprintf("%d have no passed gate run and no release tag accounting for them",
			result.Unreconciled))
	}
	if result.UnexpectedActors > 0 {
		problems = append(problems, fmt.Sprintf("%d were bypassed by an unexpected identity", result.UnexpectedActors))
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("of %d bypassed push(es) on %s: %s",
		len(result.Pushes), result.TargetBranch, strings.Join(problems, "; "))
}

func writeReconcileBypassReport(ctx common.Context, result common.ReconcileBypassResult) error {
	if len(result.Pushes) == 0 {
		_, err := fmt.Fprintf(ctx.Stdout, "No bypassed pushes found for %s/%s ruleset %d on %s.\n",
			result.Owner, result.Repo, result.RulesetID, result.TargetBranch)
		return err
	}
	for _, push := range result.Pushes {
		detail := push.Reason
		if detail == "" {
			detail = "gate run " + push.GateRunID
		}
		if _, err := fmt.Fprintf(ctx.Stdout, "%-17s %s  %s  bypassed %s by %s (%s)\n",
			push.Verdict, push.Commit, push.PushedAt, push.BypassedRule, push.Actor, detail); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(ctx.Stdout, "%d/%d unaccounted for, %d by an unexpected identity.\n",
		result.Unreconciled, len(result.Pushes), result.UnexpectedActors)
	return err
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

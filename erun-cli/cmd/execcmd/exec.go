package execcmd

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/sophium/erun/cmd/shared/clicmd"
	"github.com/sophium/erun/cmd/shared/envscope"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam"
	"github.com/sophium/erun/erun-common/command/lifecycle"
	"github.com/sophium/erun/erun-common/command/mergequeue"
	"github.com/sophium/erun/erun-common/command/review"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/gitx"
	"github.com/spf13/cobra"
)

func NewExecCmd(findProjectRoot seam.ProjectFinderFunc, runGit seam.GitCommandRunnerFunc, runRaw core.RawCommandRunnerFunc, resolveOpen envscope.OpenResolver, store config.CloudReadStore, deps cloud.CloudDependencies) *cobra.Command {
	// supervise mounts only under exec; supervisor re-execs always use `exec job supervise`.
	jobCmd := newJobCmd(resolveOpen)
	jobCmd.AddCommand(newJobSuperviseCmd())

	return clicmd.NewCommandGroup(
		"exec",
		"Repository execution utilities",
		newExecDiffCmd(findProjectRoot, runGit),
		newExecRawCmd(findProjectRoot, runRaw),
		newExecWriteCmd(findProjectRoot),
		newExecCommitCmd(findProjectRoot),
		newExecPushCmd(findProjectRoot, store, deps),
		newExecMergeCmd(findProjectRoot),
		newExecGateMergeCmd(findProjectRoot),
		newExecReportCommitStatusCmd(),
		newExecClosePRCmd(),
		newExecReconcileBypassCmd(store, deps),
		newExecPlanRulesetBypassCmd(),
		newExecRouteCheckCmd(store, deps),
		jobCmd,
	)
}

func newExecDiffCmd(findProjectRoot seam.ProjectFinderFunc, runGit seam.GitCommandRunnerFunc) *cobra.Command {
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
			commandCtx := clicmd.CommandContext(cmd)
			return runExecDiffCommand(commandCtx, findProjectRoot, runGit, execDiffOptions{
				JSON:           clicmd.CommandWantsJSON(commandCtx, jsonOutput),
				Scope:          scope,
				SelectedCommit: selectedCommit,
			})
		},
	}
	clicmd.AddDryRunFlag(cmd)
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Write the parsed diff as JSON instead of raw text (alias for --output json)")
	cmd.Flags().StringVar(&scope, "scope", "", "Diff scope: current (default), all, or commit")
	cmd.Flags().StringVar(&selectedCommit, "selected-commit", "", "Oldest commit hash to include when --scope=commit")
	return cmd
}

func newExecRawCmd(findProjectRoot seam.ProjectFinderFunc, runRaw core.RawCommandRunnerFunc) *cobra.Command {
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
			ctx := clicmd.CommandContext(cmd)
			if dryRun {
				ctx.DryRun = true
			}
			return runExecRawCommand(ctx, findProjectRoot, runRaw, rawArgs)
		},
	}
	clicmd.AddDryRunFlag(cmd)
	return cmd
}

// rawCommandWantsHelp handles help for `exec raw` itself, since DisableFlagParsing
// bypasses cobra's; a literal `--` ends the scan.
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

// extractDryRunFlag takes erun's own --dry-run out of the pass-through args;
// a literal `--` ends the scan.
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

func newExecWriteCmd(findProjectRoot seam.ProjectFinderFunc) *cobra.Command {
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
			return runExecWriteCommand(clicmd.CommandContext(cmd), findProjectRoot, args[0])
		},
	}
	clicmd.AddDryRunFlag(cmd)
	return cmd
}

func runExecWriteCommand(ctx core.Context, findProjectRoot seam.ProjectFinderFunc, path string) error {
	if findProjectRoot == nil {
		findProjectRoot = config.FindProjectRoot
	}
	_, projectRoot, err := findProjectRoot()
	if err != nil {
		return err
	}
	content, err := io.ReadAll(ctx.Stdin)
	if err != nil {
		return fmt.Errorf("read content from stdin: %w", err)
	}
	result, err := gitx.WriteWorkingTreeFile(ctx, projectRoot, model.WriteWorkingTreeFileParams{
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

func newExecCommitCmd(findProjectRoot seam.ProjectFinderFunc) *cobra.Command {
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
			return runExecCommitCommand(clicmd.CommandContext(cmd), findProjectRoot, args[0], args[1:])
		},
	}
	clicmd.AddDryRunFlag(cmd)
	return cmd
}

func runExecCommitCommand(ctx core.Context, findProjectRoot seam.ProjectFinderFunc, branch string, paths []string) error {
	if findProjectRoot == nil {
		findProjectRoot = config.FindProjectRoot
	}
	_, projectRoot, err := findProjectRoot()
	if err != nil {
		return err
	}
	message, err := io.ReadAll(ctx.Stdin)
	if err != nil {
		return fmt.Errorf("read commit message from stdin: %w", err)
	}
	result, err := gitx.CommitWorkingTree(ctx, projectRoot, model.CommitWorkingTreeParams{
		Branch:  branch,
		Message: string(message),
		Paths:   paths,
	}, gitx.CommitWorkingTreeDependencies{})
	if err != nil {
		return err
	}
	if ctx.DryRun {
		return nil
	}
	ctx.Info(fmt.Sprintf("Committed %d file(s) as %s on %s.", len(result.Files), result.Commit, result.Branch))
	return ctx.WriteResult(result)
}

func newExecPushCmd(findProjectRoot seam.ProjectFinderFunc, store config.CloudReadStore, cloudDeps cloud.CloudDependencies) *cobra.Command {
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
			return runExecPushCommand(clicmd.CommandContext(cmd), findProjectRoot, store, cloudDeps, gitx.PushWorkingTreeBranchDependencies{}, args[0], remote)
		},
	}
	cmd.Flags().StringVar(&remote, "remote", "", "Git remote to push to (defaults to origin)")
	clicmd.AddDryRunFlag(cmd)
	return cmd
}

func runExecPushCommand(ctx core.Context, findProjectRoot seam.ProjectFinderFunc, store config.CloudReadStore, cloudDeps cloud.CloudDependencies, pushDeps gitx.PushWorkingTreeBranchDependencies, branch, remote string) error {
	if findProjectRoot == nil {
		findProjectRoot = config.FindProjectRoot
	}
	_, projectRoot, err := findProjectRoot()
	if err != nil {
		return err
	}
	result, err := gitx.PushWorkingTreeBranch(ctx, projectRoot, model.PushWorkingTreeBranchParams{
		Branch: branch,
		Remote: remote,
	}, pushDeps)
	if err != nil {
		return err
	}
	// The unqueued-branch warning is best-effort and never fails the push.
	if ctx.DryRun {
		review.WarnPushedBranchWithoutReview(ctx, store, cloudDeps, model.PushedBranchReviewNoticeParams{
			ProjectRoot: projectRoot,
			Branch:      result.Branch,
			Remote:      result.Remote,
		})
		return nil
	}
	ctx.Info(fmt.Sprintf("Pushed %s to %s (%s).", result.Branch, result.Remote, result.Commit))
	review.WarnPushedBranchWithoutReview(ctx, store, cloudDeps, model.PushedBranchReviewNoticeParams{
		ProjectRoot: projectRoot,
		Branch:      result.Branch,
		Remote:      result.Remote,
	})
	return ctx.WriteResult(result)
}

func newExecMergeCmd(findProjectRoot seam.ProjectFinderFunc) *cobra.Command {
	var remote string
	cmd := &cobra.Command{
		Use:   "merge TARGET_BRANCH",
		Short: "Merge a branch into the project working tree's current branch",
		Long: "Fetch TARGET_BRANCH from the remote and merge it into the project working tree's current branch " +
			"with an explicit merge commit. Never rebases: review comments anchor to a commit id, and a rewrite " +
			"would orphan every thread on an open review.\n\n" +
			"A conflicted merge is reported as a distinct, named outcome rather than a generic failure. The " +
			"worktree is left exactly as git left it, mid-merge — resolve the conflicted files and commit, or " +
			"run `git merge --abort` to back out, before doing anything else. A conflict confined to atlas.sum, " +
			"the regenerable migration checksum, is the exception: each is regenerated from the migration " +
			"files already on disk and staged, the merge commit is completed, and the merge is reported as " +
			"landed rather than as a conflict.\n\n" +
			"--dry-run traces the fetch and merge without running them.",
		Example:      "  erun exec merge main\n  erun exec merge release/2026.9 --remote upstream",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExecMergeCommand(clicmd.CommandContext(cmd), findProjectRoot, args[0], remote)
		},
	}
	cmd.Flags().StringVar(&remote, "remote", "", "Git remote to fetch and merge from (defaults to origin)")
	clicmd.AddDryRunFlag(cmd)
	return cmd
}

func runExecMergeCommand(ctx core.Context, findProjectRoot seam.ProjectFinderFunc, targetBranch, remote string) error {
	if findProjectRoot == nil {
		findProjectRoot = config.FindProjectRoot
	}
	_, projectRoot, err := findProjectRoot()
	if err != nil {
		return err
	}
	result, err := mergequeue.MergeWorkingTreeBranch(ctx, projectRoot, model.MergeWorkingTreeBranchParams{
		TargetBranch: targetBranch,
		Remote:       remote,
	}, mergequeue.MergeWorkingTreeBranchDependencies{})
	if err != nil {
		return err
	}
	if ctx.DryRun {
		return nil
	}
	ctx.Info(fmt.Sprintf("Merged %s/%s into %s (%s).", result.Remote, result.TargetBranch, result.Branch, result.Commit))
	return ctx.WriteResult(result)
}

type execDiffOptions struct {
	JSON           bool
	Scope          string
	SelectedCommit string
}

func runExecDiffCommand(ctx core.Context, findProjectRoot seam.ProjectFinderFunc, runGit seam.GitCommandRunnerFunc, opts execDiffOptions) error {
	if findProjectRoot == nil {
		findProjectRoot = config.FindProjectRoot
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
	return lifecycle.WriteRawDiff(ctx.Stdout, result)
}

func resolveExecDiff(projectRoot string, runGit seam.GitCommandRunnerFunc, opts execDiffOptions) (model.DiffResult, error) {
	if opts.Scope == "" && opts.SelectedCommit == "" {
		return lifecycle.ResolveGitDiff(projectRoot, runGit)
	}
	if opts.SelectedCommit != "" && opts.Scope != "commit" {
		return model.DiffResult{}, fmt.Errorf("--selected-commit requires --scope=commit")
	}
	return lifecycle.ResolveGitDiffWithOptions(projectRoot, model.DiffOptions{
		Scope:          opts.Scope,
		SelectedCommit: opts.SelectedCommit,
	}, runGit)
}

func runExecRawCommand(ctx core.Context, findProjectRoot seam.ProjectFinderFunc, runRaw core.RawCommandRunnerFunc, args []string) error {
	if findProjectRoot == nil {
		findProjectRoot = config.FindProjectRoot
	}
	_, projectRoot, err := findProjectRoot()
	if err != nil {
		return err
	}
	return core.RunRawCommand(ctx, model.RawCommandSpec{
		Dir:  projectRoot,
		Args: args,
	}, runRaw)
}

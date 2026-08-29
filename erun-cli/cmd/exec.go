package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

func newExecCmd(findProjectRoot common.ProjectFinderFunc, runGit common.GitCommandRunnerFunc, runRaw common.RawCommandRunnerFunc, resolveOpen OpenResolver) *cobra.Command {
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
		jobCmd,
	)
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

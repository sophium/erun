package cmd

import (
	"encoding/json"
	"fmt"
	"io"

	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

func newExecCmd(findProjectRoot common.ProjectFinderFunc, runGit common.GitCommandRunnerFunc, runRaw common.RawCommandRunnerFunc) *cobra.Command {
	return newCommandGroup(
		"exec",
		"Repository execution utilities",
		newExecDiffCmd(findProjectRoot, runGit),
		newExecRawCmd(findProjectRoot, runRaw),
		newExecWriteCmd(findProjectRoot),
		newExecCommitCmd(findProjectRoot),
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

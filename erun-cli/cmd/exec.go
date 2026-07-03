package cmd

import (
	"encoding/json"
	"fmt"

	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

func newExecCmd(findProjectRoot common.ProjectFinderFunc, runGit common.GitCommandRunnerFunc, runRaw common.RawCommandRunnerFunc) *cobra.Command {
	return newCommandGroup(
		"exec",
		"Repository execution utilities",
		newExecDiffCmd(findProjectRoot, runGit),
		newExecRawCmd(findProjectRoot, runRaw),
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

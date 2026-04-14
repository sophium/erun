package cmd

import (
	"fmt"
	"io"

	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

func newReleaseCmd(findProjectRoot common.ProjectFinderFunc, runGit common.GitCommandRunnerFunc) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "release",
		Short:         "Plan and execute a project release",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := commandContext(cmd)
			spec, err := common.ResolveReleaseSpec(findProjectRoot, common.ReleaseParams{})
			if err != nil {
				return err
			}
			if _, err := fmt.Fprintln(ctx.Stdout, spec.Version); err != nil {
				return err
			}
			return common.RunReleaseSpec(ctx, spec, func(dir string, stdout, stderr io.Writer, args ...string) error {
				if runGit == nil {
					runGit = common.GitCommandRunner
				}
				return runGit(dir, ctx.Stderr, stderr, args...)
			}, func(dir, path string, env []string, stdin io.Reader, stdout, stderr io.Writer) error {
				return common.BuildScriptRunner(dir, path, env, stdin, ctx.Stderr, stderr)
			})
		},
	}
	addDryRunFlag(cmd)
	cmd.Example = "  erun release --dry-run\n  erun -v release --dry-run"
	cmd.Long = "Plan and execute a project release.\n\nRelease policy is loaded from .erun/config.yaml.\n\nDry-run:\n  --dry-run resolves the release version, file updates, and git actions without executing them."
	return cmd
}

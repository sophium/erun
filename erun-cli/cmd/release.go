package cmd

import (
	"fmt"
	"io"

	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

func newReleaseCmd(findProjectRoot common.ProjectFinderFunc, runGit common.GitCommandRunnerFunc) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:           "release",
		Short:         "Cut a release: stamp the version, tag it, and push the tag",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := commandContext(cmd)
			spec, err := common.ResolveReleaseSpec(ctx, findProjectRoot, common.ReleaseParams{Force: force})
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
	cmd.Flags().BoolVar(&force, "force", false, "Delete and recreate conflicting release tags before tagging")
	cmd.Example = "  erun release --dry-run\n  erun -v release --dry-run"
	cmd.Long = "Cut a release from the current branch.\n\nResolves the next version, updates the version files, commits, tags, and pushes the tag to origin. Which branch releases as a stable version vs a prerelease comes from .erun/config.yaml.\n\nThe release step of the build → release → push → deploy flow.\n\nDry-run:\n  --dry-run resolves the version, file updates, and git actions without executing them."
	return cmd
}

package cmd

import (
	"fmt"
	"io"
	"strings"

	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

func newReleaseCmd(store common.DockerStore, findProjectRoot common.ProjectFinderFunc, resolveBuildContext common.BuildContextResolverFunc, now common.NowFunc, runGit common.GitCommandRunnerFunc, runBuildScript common.BuildScriptRunnerFunc, buildDockerImage common.DockerImageBuilderFunc, loginToDockerRegistry common.DockerRegistryLoginFunc, selectRunner SelectRunner, push common.DockerPushFunc) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:           "release",
		Short:         "Cut a release: publish the version's images and charts, then tag and announce it",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReleaseCommand(commandContext(cmd), store, findProjectRoot, resolveBuildContext, now, force, runGit, runBuildScript, buildDockerImage, loginToDockerRegistry, selectRunner, push)
		},
	}
	addDryRunFlag(cmd)
	cmd.Flags().BoolVar(&force, "force", false, "Delete and recreate conflicting release tags before tagging")
	cmd.Example = "  erun release --dry-run\n  erun -v release --dry-run"
	cmd.Long = "Cut a release from the current branch.\n\n" +
		"Resolves the release version from the version file, stamps it into the charts and package-manager metadata, and commits and tags it locally. It then builds that version's container images for both architectures and publishes them and their helm charts, reading each one back from the registry to prove it resolves. Only then does it push the tag, sync packaging checksums, prepare the next patch version, and push the branches.\n\n" +
		"Publishing before tagging is the contract: a release that exits 0 means `erun deploy --version <version>` can pull the image and the chart, and a release that cannot publish fails while nothing is public yet.\n\n" +
		"Which branch releases as a stable version vs a candidate comes from .erun/config.yaml.\n\n" +
		"High blast radius: pushes tags and branches to origin and publishes images and charts to the registry, so the released version becomes public and consumable.\n\n" +
		"The release step of the build → release → push → deploy flow. It composes build and push itself, so a separate `erun push` afterwards only republishes what release already put in the registry.\n\n" +
		"Dry-run:\n  --dry-run resolves the version, file updates, git actions, image builds, and chart publishes without executing them."
	return cmd
}

func runReleaseCommand(ctx common.Context, store common.DockerStore, findProjectRoot common.ProjectFinderFunc, resolveBuildContext common.BuildContextResolverFunc, now common.NowFunc, force bool, runGit common.GitCommandRunnerFunc, runBuildScript common.BuildScriptRunnerFunc, buildDockerImage common.DockerImageBuilderFunc, loginToDockerRegistry common.DockerRegistryLoginFunc, selectRunner SelectRunner, push common.DockerPushFunc) error {
	execution, err := common.ResolveBuildExecution(ctx, store, findProjectRoot, resolveBuildContext, now, common.DockerCommandTarget{Release: true, Force: force})
	if err != nil {
		return err
	}
	version := strings.TrimSpace(common.NewBuildResult(execution).Version)
	if _, err := fmt.Fprintln(ctx.Stdout, version); err != nil {
		return err
	}

	if runGit == nil {
		runGit = common.GitCommandRunner
	}
	if runBuildScript == nil {
		runBuildScript = common.BuildScriptRunner
	}
	// stdout carries the released version and nothing else, so git and script
	// chatter is redirected to stderr for orchestrators reading the version.
	gitToStderr := func(dir string, stdout, stderr io.Writer, args ...string) error {
		return runGit(dir, ctx.Stderr, stderr, args...)
	}
	scriptToStderr := func(dir, path string, env []string, stdin io.Reader, stdout, stderr io.Writer) error {
		return runBuildScript(dir, path, env, stdin, ctx.Stderr, stderr)
	}
	buildWithRetry := pushBuildWithRetry(ctx, buildDockerImage, loginToDockerRegistry, selectRunner)
	return common.RunReleaseExecution(ctx, execution, gitToStderr, scriptToStderr, buildWithRetry, push)
}

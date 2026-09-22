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
		Short:         "Cut a release: stamp, tag, and push the release's source control",
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
		"Resolves the release version from the version file, stamps it into the charts and package-manager metadata, and commits and tags it locally. It then pushes the tag, syncs packaging checksums, prepares the next patch version, and pushes the branches.\n\n" +
		"It marks source control and nothing else: it never builds, publishes, or verifies an artifact, so it exits 0 having published nothing and says so. Build and publish belong to `erun build --release`, which composes this same stamp/tag work with the build, and to `erun push --version <version>`. A tag whose artifacts never landed names a dead version rather than corrupting anything, and a dead version is not deployable by accident because `erun deploy` never builds — the remedy is to fix the source and release again.\n\n" +
		"Which branch releases as a stable version vs a candidate comes from .erun/config.yaml.\n\n" +
		"High blast radius: pushes tags and branches to origin, so the released version's source becomes public.\n\n" +
		"The release step of the build → release → push → deploy flow. It performs no build and no publish; run `erun build --release` to produce and publish a version's artifacts.\n\n" +
		"Dry-run:\n  --dry-run resolves the version, file updates, and git actions without executing them."
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
	// Source control only: release stamps, commits, tags and pushes the version
	// and never builds or publishes an artifact. Build and publish are
	// `erun build --release` and `erun push --version`. See RunReleaseSpec.
	spec, ok := common.BuildExecutionReleaseSpec(execution)
	if !ok {
		return fmt.Errorf("release: the resolved plan is not a release")
	}
	return common.RunReleaseSpec(ctx, spec, gitToStderr, scriptToStderr)
}

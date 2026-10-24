package build

import (
	"fmt"
	"io"
	"strings"

	"github.com/sophium/erun/cmd/shared/clicmd"
	"github.com/sophium/erun/cmd/shared/prompt"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam"
	"github.com/sophium/erun/erun-common/infra/gitx"
	buildrun "github.com/sophium/erun/erun-common/orchestration/build"
	"github.com/sophium/erun/erun-common/orchestration/execute"
	"github.com/sophium/erun/erun-common/primitive/build"
	"github.com/sophium/erun/erun-common/primitive/release"
	"github.com/spf13/cobra"
)

func NewReleaseCmd(store seam.DockerStore, findProjectRoot seam.ProjectFinderFunc, resolveBuildContext seam.BuildContextResolverFunc, now seam.NowFunc, runGit seam.GitCommandRunnerFunc, buildDockerImage build.DockerImageBuilderFunc, loginToDockerRegistry seam.DockerRegistryLoginFunc, selectRunner prompt.SelectRunner, push build.DockerPushFunc) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:           "release",
		Short:         "Cut a release: stamp, tag, and push the release's source control",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReleaseCommand(clicmd.CommandContext(cmd), store, findProjectRoot, resolveBuildContext, now, force, runGit, buildDockerImage, loginToDockerRegistry, selectRunner, push)
		},
	}
	clicmd.AddDryRunFlag(cmd)
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

func runReleaseCommand(ctx core.Context, store seam.DockerStore, findProjectRoot seam.ProjectFinderFunc, resolveBuildContext seam.BuildContextResolverFunc, now seam.NowFunc, force bool, runGit seam.GitCommandRunnerFunc, buildDockerImage build.DockerImageBuilderFunc, loginToDockerRegistry seam.DockerRegistryLoginFunc, selectRunner prompt.SelectRunner, push build.DockerPushFunc) error {
	execution, err := buildrun.ResolveBuildExecution(ctx, store, findProjectRoot, resolveBuildContext, now, model.DockerCommandTarget{Release: true, Force: force})
	if err != nil {
		return err
	}
	version := strings.TrimSpace(buildrun.NewBuildResult(execution).Version)
	if _, err := fmt.Fprintln(ctx.Stdout, version); err != nil {
		return err
	}

	if runGit == nil {
		runGit = gitx.GitCommandRunner
	}
	// stdout carries only the released version.
	gitToStderr := func(dir string, stdout, stderr io.Writer, args ...string) error {
		return runGit(dir, ctx.Stderr, stderr, args...)
	}
	// Release never builds or publishes; see RunReleaseSpec.
	spec, ok := execute.BuildExecutionReleaseSpec(execution)
	if !ok {
		return fmt.Errorf("release: the resolved plan is not a release")
	}
	return release.RunReleaseSpec(ctx, spec, gitToStderr)
}

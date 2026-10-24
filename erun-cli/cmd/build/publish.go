package build

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/cmd/shared"
	"github.com/sophium/erun/erun-common/command/e2e"
	"github.com/sophium/erun/erun-common/orchestration/release"
	"github.com/sophium/erun/erun-common/primitive/deploy"
	sharedpkg "github.com/sophium/erun/erun-common/primitive/shared"
	"github.com/spf13/cobra"
)

func NewPublishCmd(store deploy.DeployStore, findProjectRoot sharedpkg.ProjectFinderFunc, resolveBuildContext sharedpkg.BuildContextResolverFunc, resolveDeployContext deploy.DeployContextResolverFunc, now sharedpkg.NowFunc) *cobra.Command {
	target := deploy.DeployTarget{}
	cmd := &cobra.Command{
		Use:   "publish [TENANT] [ENVIRONMENT]",
		Short: "Mirror a built version's images to the shared registry",
		Long: "Mirror an already-built version's images from the FROM registry to each TO " +
			"registry, without deploying.\n\n" +
			"publish is a pure primitive: it neither builds nor deploys. Use it to hand a " +
			"version you have iterated on and tested to other users — it copies that exact " +
			"multi-arch image (no rebuild) from the environment's FROM registry (e.g. your " +
			"cluster registry) to every TO registry (e.g. ghcr.io/<org>). A version is " +
			"required (--version, produced by `erun build`/`erun push`), and the environment " +
			"must mark a FROM source and at least one TO destination in .erun/config.yaml.",
		Example:       "  erun publish team dev --version 1.2.3",
		Args:          cobra.MaximumNArgs(2),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := shared.WithCloudContextPreflight(shared.CommandContext(cmd), store)
			publishTarget, err := shared.ResolveDeployTargetArgs(cmd.CommandPath(), args, target)
			if err != nil {
				return err
			}
			if strings.TrimSpace(publishTarget.VersionOverride) == "" {
				return fmt.Errorf("publish requires a version: pass --version <version> produced by `erun build`/`erun push`")
			}
			var closeEnvTrace func()
			ctx, closeEnvTrace = e2e.ActivateEnvTrace(ctx, publishTarget.Tenant, publishTarget.Environment)
			defer closeEnvTrace()
			return release.RunPublish(ctx, store, findProjectRoot, resolveBuildContext, resolveDeployContext, now, publishTarget)
		},
	}
	shared.AddDryRunFlag(cmd)
	cmd.Flags().StringVar(&target.VersionOverride, "version", "", "Version to publish (produced by `erun build`/`erun push`)")
	cmd.Flags().StringVar(&target.Tenant, "tenant", "", "Publish for a specific tenant")
	cmd.Flags().StringVar(&target.Environment, "environment", "", "Publish for a specific environment; requires --tenant")
	cmd.Flags().StringVar(&target.RepoPath, "repo-path", "", "Repo path override for internal tooling")
	_ = cmd.Flags().MarkHidden("repo-path")
	return cmd
}

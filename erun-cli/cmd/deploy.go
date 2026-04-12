package cmd

import (
	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

func newDeployCmd(store common.DeployStore, findProjectRoot common.ProjectFinderFunc, resolveBuildContext common.BuildContextResolverFunc, resolveDeployContext common.DeployContextResolverFunc, now common.NowFunc, buildDockerImage common.DockerImageBuilderFunc, push common.DockerPushFunc, deployHelmChart common.HelmChartDeployerFunc) *cobra.Command {
	target := common.DeployTarget{}
	cmd := &cobra.Command{
		Use:           "deploy",
		Short:         "Deploy the current Helm chart or all charts in the current devops k8s scope",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := commandContext(cmd)
			deploySpecs, err := common.ResolveCurrentDeploySpecs(store, findProjectRoot, resolveBuildContext, resolveDeployContext, now, target)
			if err != nil {
				return err
			}
			return common.RunDeploySpecs(ctx, deploySpecs, buildDockerImage, push, deployHelmChart)
		},
	}
	addDryRunFlag(cmd)
	addDeployCommandTargetFlags(cmd, &target)
	return cmd
}

func newK8sDeployCmd(store common.DeployStore, findProjectRoot common.ProjectFinderFunc, resolveBuildContext common.BuildContextResolverFunc, resolveDeployContext common.DeployContextResolverFunc, now common.NowFunc, buildDockerImage common.DockerImageBuilderFunc, push common.DockerPushFunc, deployHelmChart common.HelmChartDeployerFunc) *cobra.Command {
	target := common.DeployTarget{}
	cmd := &cobra.Command{
		Use:           "deploy COMPONENT",
		Short:         "Deploy a component Helm chart",
		Args:          cobra.ExactArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := commandContext(cmd)
			deploySpec, err := common.ResolveDeploySpec(store, findProjectRoot, resolveBuildContext, resolveDeployContext, now, target, args[0], "")
			if err != nil {
				return err
			}
			return common.RunDeploySpec(ctx, deploySpec, buildDockerImage, push, deployHelmChart)
		},
	}
	addDryRunFlag(cmd)
	addDeployCommandTargetFlags(cmd, &target)
	return cmd
}

func addDeployCommandTargetFlags(cmd *cobra.Command, target *common.DeployTarget) {
	cmd.Flags().StringVar(&target.Tenant, "tenant", "", "Tenant override for internal tooling")
	cmd.Flags().StringVar(&target.Environment, "environment", "", "Environment override for internal tooling")
	cmd.Flags().StringVar(&target.RepoPath, "repo-path", "", "Repo path override for internal tooling")
	_ = cmd.Flags().MarkHidden("tenant")
	_ = cmd.Flags().MarkHidden("environment")
	_ = cmd.Flags().MarkHidden("repo-path")
}

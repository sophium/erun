package cmd

import (
	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

func newDeployCmd(store common.DeployStore, findProjectRoot common.ProjectFinderFunc, resolveBuildContext common.BuildContextResolverFunc, resolveDeployContext common.DeployContextResolverFunc, now common.NowFunc, buildDockerImage common.DockerImageBuilderFunc, push common.DockerPushFunc, deployHelmChart common.HelmChartDeployerFunc) *cobra.Command {
	target := common.DeployTarget{}
	var snapshot bool
	var noSnapshot bool
	cmd := &cobra.Command{
		Use:           "deploy [TENANT] [ENVIRONMENT]",
		Short:         "Deploy the current Helm chart or all charts in the current devops k8s scope",
		Args:          cobra.MaximumNArgs(2),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := commandContext(cmd)
			deployTarget, err := resolveDeployTargetArgs(args, target)
			if err != nil {
				return err
			}
			snapshotOverride, err := resolveSnapshotFlagOverride(cmd, snapshot, noSnapshot)
			if err != nil {
				return err
			}
			if snapshotOverride == nil {
				snapshotOverride = &snapshot
			}
			deployTarget.Snapshot = snapshotOverride
			deploySpecs, err := common.ResolveCurrentDeploySpecs(store, findProjectRoot, resolveBuildContext, resolveDeployContext, now, deployTarget)
			if err != nil {
				return err
			}
			return common.RunDeploySpecs(ctx, deploySpecs, buildDockerImage, push, deployHelmChart)
		},
	}
	addDryRunFlag(cmd)
	addDeployCommandTargetFlags(cmd, &target, &snapshot, &noSnapshot)
	return cmd
}

func newK8sDeployCmd(store common.DeployStore, findProjectRoot common.ProjectFinderFunc, resolveBuildContext common.BuildContextResolverFunc, resolveDeployContext common.DeployContextResolverFunc, now common.NowFunc, buildDockerImage common.DockerImageBuilderFunc, push common.DockerPushFunc, deployHelmChart common.HelmChartDeployerFunc) *cobra.Command {
	target := common.DeployTarget{}
	var snapshot bool
	var noSnapshot bool
	cmd := &cobra.Command{
		Use:           "deploy COMPONENT",
		Short:         "Deploy a component Helm chart",
		Args:          cobra.ExactArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := commandContext(cmd)
			snapshotOverride, err := resolveSnapshotFlagOverride(cmd, snapshot, noSnapshot)
			if err != nil {
				return err
			}
			if snapshotOverride == nil {
				snapshotOverride = &snapshot
			}
			target.Snapshot = snapshotOverride
			deploySpec, err := common.ResolveDeploySpec(store, findProjectRoot, resolveBuildContext, resolveDeployContext, now, target, args[0], "")
			if err != nil {
				return err
			}
			return common.RunDeploySpec(ctx, deploySpec, buildDockerImage, push, deployHelmChart)
		},
	}
	addDryRunFlag(cmd)
	addDeployCommandTargetFlags(cmd, &target, &snapshot, &noSnapshot)
	return cmd
}

func addDeployCommandTargetFlags(cmd *cobra.Command, target *common.DeployTarget, snapshot, noSnapshot *bool) {
	cmd.Flags().StringVar(&target.VersionOverride, "version", "", "Override the deployed chart and image version")
	addSnapshotFlags(cmd, snapshot, noSnapshot, "Build and deploy local snapshot images in the local environment")
	cmd.Flags().StringVar(&target.Tenant, "tenant", "", "Deploy for a specific tenant")
	cmd.Flags().StringVar(&target.Environment, "environment", "", "Deploy for a specific environment; requires --tenant")
	cmd.Flags().StringVar(&target.RepoPath, "repo-path", "", "Repo path override for internal tooling")
	_ = cmd.Flags().MarkHidden("repo-path")
}

func resolveDeployTargetArgs(args []string, target common.DeployTarget) (common.DeployTarget, error) {
	params, err := resolveOpenParams(args, common.OpenParams{
		Tenant:      target.Tenant,
		Environment: target.Environment,
	})
	if err != nil {
		return common.DeployTarget{}, err
	}
	target.Tenant = params.Tenant
	target.Environment = params.Environment
	return target, nil
}

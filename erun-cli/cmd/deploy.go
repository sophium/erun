package cmd

import (
	"fmt"

	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

func newDeployCmd(store common.DeployStore, saveEnvConfig common.EnvConfigSaver, findProjectRoot common.ProjectFinderFunc, resolveBuildContext common.BuildContextResolverFunc, resolveDeployContext common.DeployContextResolverFunc, now common.NowFunc, buildDockerImage common.DockerImageBuilderFunc, push common.DockerPushFunc, deployHelmChart common.HelmChartDeployerFunc) *cobra.Command {
	target := common.DeployTarget{}
	var snapshot bool
	var noSnapshot bool
	var components []string
	cmd := &cobra.Command{
		Use:   "deploy [TENANT] [ENVIRONMENT]",
		Short: "Roll the project's charts out to an environment",
		Long: "Roll the project's charts out to an environment.\n\n" +
			"The deploy step of the build → release → push → deploy flow. Builds and pushes the " +
			"images the charts need, then runs the rollout against the target environment. " +
			"Defaults to the current scope; pass TENANT and ENVIRONMENT (or --tenant/--environment) " +
			"to target another.",
		Example:       "  erun deploy\n  erun deploy team dev\n  erun deploy team prod --version 1.2.3",
		Args:          cobra.MaximumNArgs(2),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := withCloudContextPreflight(commandContext(cmd), store)
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
			deployTarget.Components = components
			var closeEnvTrace func()
			ctx, closeEnvTrace = common.ActivateEnvTrace(ctx, deployTarget.Tenant, deployTarget.Environment)
			defer closeEnvTrace()
			ctx.Trace(fmt.Sprintf("deploy: tenant=%s environment=%s version-override=%s snapshot=%v components=%v force=%v publish=%v",
				deployTarget.Tenant, deployTarget.Environment, deployTarget.VersionOverride,
				snapshotOverride != nil && *snapshotOverride, components, deployTarget.Force, deployTarget.Publish))
			deploySpecs, err := common.ResolveCurrentDeploySpecs(ctx, store, findProjectRoot, resolveBuildContext, resolveDeployContext, now, deployTarget)
			if err != nil {
				ctx.Trace("deploy: spec resolution failed: " + err.Error())
				return err
			}
			ctx.Trace(fmt.Sprintf("deploy: resolved %d spec(s)", len(deploySpecs)))
			if err := common.RunDeploySpecs(ctx, deploySpecs, buildDockerImage, push, deployHelmChart); err != nil {
				return err
			}
			return common.PersistRuntimeVersionFromDeploySpecs(ctx, deploySpecs, saveEnvConfig, common.ResolveDeployedHelmReleaseVersion)
		},
	}
	addDryRunFlag(cmd)
	addDeployCommandTargetFlags(cmd, &target, &snapshot, &noSnapshot)
	cmd.Flags().StringSliceVar(&components, "components", nil, "Opt-in components to include alongside the runtime chart (erun-backend-postgres, erun-backend-db, erun-backend-api)")
	return cmd
}

func newK8sDeployCmd(store common.DeployStore, saveEnvConfig common.EnvConfigSaver, findProjectRoot common.ProjectFinderFunc, resolveBuildContext common.BuildContextResolverFunc, resolveDeployContext common.DeployContextResolverFunc, now common.NowFunc, buildDockerImage common.DockerImageBuilderFunc, push common.DockerPushFunc, deployHelmChart common.HelmChartDeployerFunc) *cobra.Command {
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
			ctx := withCloudContextPreflight(commandContext(cmd), store)
			snapshotOverride, err := resolveSnapshotFlagOverride(cmd, snapshot, noSnapshot)
			if err != nil {
				return err
			}
			if snapshotOverride == nil {
				snapshotOverride = &snapshot
			}
			target.Snapshot = snapshotOverride
			deploySpec, err := common.ResolveDeploySpec(ctx, store, findProjectRoot, resolveBuildContext, resolveDeployContext, now, target, args[0], "")
			if err != nil {
				return err
			}
			if err := common.RunDeploySpec(ctx, deploySpec, buildDockerImage, push, deployHelmChart); err != nil {
				return err
			}
			return common.PersistRuntimeVersionFromDeploySpecs(ctx, []common.DeploySpec{deploySpec}, saveEnvConfig, common.ResolveDeployedHelmReleaseVersion)
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
	cmd.Flags().BoolVar(&target.Force, "force", false, "Bypass the fingerprint cache and re-run helm upgrade even when no source change is detected")
	cmd.Flags().BoolVar(&target.Publish, "publish", false, "Package and push each resolved chart to the environment's container registry (oci://<containerRegistry>/<chart>:<version>) before helm upgrade")
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

package cmd

import (
	"fmt"
	"strings"

	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

func newDeployCmd(store common.DeployStore, saveEnvConfig common.EnvConfigSaver, findProjectRoot common.ProjectFinderFunc, resolveBuildContext common.BuildContextResolverFunc, resolveDeployContext common.DeployContextResolverFunc, now common.NowFunc, buildDockerImage common.DockerImageBuilderFunc, push common.DockerPushFunc, deployHelmChart common.HelmChartDeployerFunc) *cobra.Command {
	target := common.DeployTarget{}
	var components []string
	cmd := &cobra.Command{
		Use:   "deploy [TENANT] [ENVIRONMENT]",
		Short: "Roll the project's charts out to an environment",
		Long: "Roll the project's charts out to an environment.\n\n" +
			"The deploy step of the build → release → push → deploy flow. With no --version it builds and " +
			"pushes the images the charts need from the working tree, then runs the rollout against the " +
			"target environment. With --version it installs that already-published version by reference " +
			"without building (a version is an identity, not a label for a fresh build). " +
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
			deployTarget.Components = components
			// An explicit --version on deploy is an install target, not a
			// build label: address the already-published version rather than
			// rebuilding the working tree under it (#556).
			deployTarget.InstallExistingVersion = strings.TrimSpace(deployTarget.VersionOverride) != ""
			var closeEnvTrace func()
			ctx, closeEnvTrace = common.ActivateEnvTrace(ctx, deployTarget.Tenant, deployTarget.Environment)
			defer closeEnvTrace()
			ctx.Trace(fmt.Sprintf("deploy: tenant=%s environment=%s version-override=%s components=%v force=%v publish=%v",
				deployTarget.Tenant, deployTarget.Environment, deployTarget.VersionOverride,
				components, deployTarget.Force, deployTarget.Publish))
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
	addDeployCommandTargetFlags(cmd, &target)
	cmd.Flags().StringSliceVar(&components, "components", nil, "Opt-in components to include alongside the runtime chart (erun-backend-postgres, erun-backend-db, erun-backend-api)")
	return cmd
}

func newK8sDeployCmd(store common.DeployStore, saveEnvConfig common.EnvConfigSaver, findProjectRoot common.ProjectFinderFunc, resolveBuildContext common.BuildContextResolverFunc, resolveDeployContext common.DeployContextResolverFunc, now common.NowFunc, buildDockerImage common.DockerImageBuilderFunc, push common.DockerPushFunc, deployHelmChart common.HelmChartDeployerFunc) *cobra.Command {
	target := common.DeployTarget{}
	cmd := &cobra.Command{
		Use:           "deploy COMPONENT",
		Short:         "Deploy a component Helm chart",
		Args:          cobra.ExactArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := withCloudContextPreflight(commandContext(cmd), store)
			// An explicit --version is an install target, not a build label
			// (#556).
			target.InstallExistingVersion = strings.TrimSpace(target.VersionOverride) != ""
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
	addDeployCommandTargetFlags(cmd, &target)
	return cmd
}

func addDeployCommandTargetFlags(cmd *cobra.Command, target *common.DeployTarget) {
	cmd.Flags().StringVar(&target.VersionOverride, "version", "", "Install an already-published version by reference instead of building from the working tree; fails if that version's image is absent")
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

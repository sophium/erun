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
	var useCurrent bool
	cmd := &cobra.Command{
		Use:   "deploy [TENANT] [ENVIRONMENT]",
		Short: "Install a published version into an environment",
		Long: "Install a published version into an environment with helm.\n\n" +
			"deploy is a pure consume operation: it installs the image and chart already published " +
			"at a version, by reference. It never builds or pushes — produce a version with `erun build` " +
			"and `erun push` (or `erun build --deploy` to chain them) first. Pass the version with " +
			"--version; use --current to redeploy the version this environment already runs. " +
			"Defaults to the current scope; pass TENANT and ENVIRONMENT (or --tenant/--environment) to target another.\n\n" +
			"deploy waits for the rollout to become ready — default 5m, or the env's `deploy.timeout`, " +
			"or --rollout-timeout — and watches the new pods: it keeps waiting while an image is still " +
			"pulling and aborts early on a real container failure (crash, config error, or a permanent " +
			"image-pull rejection) instead of waiting out the timeout.",
		Example:       "  erun deploy team prod --version 1.2.3\n  erun deploy team dev --current\n  erun deploy team prod --version 1.2.3 --rollout-timeout 10m",
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
			// Version is required: deploy installs a content identity by
			// reference, it does not mint one. With no explicit --version,
			// --current redeploys the env's persisted version; otherwise the
			// operator has not said what to deploy.
			if strings.TrimSpace(deployTarget.VersionOverride) == "" && !useCurrent {
				return fmt.Errorf("deploy requires a version: pass --version <version> produced by `erun build`/`erun push`, or --current to redeploy the version this environment already runs")
			}
			var closeEnvTrace func()
			ctx, closeEnvTrace = common.ActivateEnvTrace(ctx, deployTarget.Tenant, deployTarget.Environment)
			defer closeEnvTrace()
			ctx.Trace(fmt.Sprintf("deploy: tenant=%s environment=%s version-override=%s components=%v force=%v current=%v",
				deployTarget.Tenant, deployTarget.Environment, deployTarget.VersionOverride,
				components, deployTarget.Force, useCurrent))
			deploySpecs, err := common.ResolveCurrentDeploySpecs(ctx, store, findProjectRoot, resolveBuildContext, resolveDeployContext, now, deployTarget)
			if err != nil {
				ctx.Trace("deploy: spec resolution failed: " + err.Error())
				return err
			}
			ctx.Trace(fmt.Sprintf("deploy: resolved %d spec(s)", len(deploySpecs)))
			if err := common.RunDeploySpecs(ctx, deploySpecs, deployHelmChart); err != nil {
				return err
			}
			return common.PersistRuntimeVersionFromDeploySpecs(ctx, deploySpecs, saveEnvConfig, common.ResolveDeployedHelmReleaseVersion)
		},
	}
	addDryRunFlag(cmd)
	addDeployCommandTargetFlags(cmd, &target)
	cmd.Flags().BoolVar(&useCurrent, "current", false, "Redeploy the version this environment already runs (its persisted runtime version) instead of passing --version")
	cmd.Flags().StringSliceVar(&components, "components", nil, fmt.Sprintf("Opt-in components to include alongside the runtime chart (%s)", strings.Join(common.OptInDeployComponentNames(), ", ")))
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
			deploySpec, err := common.ResolveDeploySpec(ctx, store, findProjectRoot, resolveBuildContext, resolveDeployContext, now, target, args[0], "")
			if err != nil {
				return err
			}
			if err := common.RunDeploySpec(ctx, deploySpec, deployHelmChart); err != nil {
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
	cmd.Flags().StringVar(&target.VersionOverride, "version", "", "Version to install by reference (produced by `erun build`/`erun push`); fails if that version's image is absent")
	cmd.Flags().StringVar(&target.Tenant, "tenant", "", "Deploy for a specific tenant")
	cmd.Flags().StringVar(&target.Environment, "environment", "", "Deploy for a specific environment; requires --tenant")
	cmd.Flags().BoolVar(&target.Force, "force", false, "Re-run the helm upgrade even when the deployed release already matches the requested version")
	cmd.Flags().StringVar(&target.RolloutTimeout, "rollout-timeout", "", "Override the helm rollout wait for this deploy (Go duration, e.g. 8m); empty uses the env's deploy.timeout or the 5m default")
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

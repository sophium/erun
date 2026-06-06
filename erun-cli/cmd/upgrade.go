package cmd

import (
	"fmt"
	"strings"

	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

func newUpgradeCmd(store common.DeployStore, saveEnvConfig common.EnvConfigSaver, findProjectRoot common.ProjectFinderFunc, resolveBuildContext common.BuildContextResolverFunc, resolveDeployContext common.DeployContextResolverFunc, now common.NowFunc, buildDockerImage common.DockerImageBuilderFunc, push common.DockerPushFunc, deployHelmChart common.HelmChartDeployerFunc) *cobra.Command {
	var versionOverride string
	var tenant string
	var environment string
	var force bool
	cmd := &cobra.Command{
		Use:   "upgrade [TENANT] [ENVIRONMENT]",
		Short: "Redeploy opted-in environments to the latest version for their channel",
		Long: "Redeploy every environment opted into \"Upgrade all\" (autoupgrade) whose runtime " +
			"version lags the latest for its channel (stable or snapshot).\n\n" +
			"High blast radius: this rolls out new runtime images to multiple — possibly remote — " +
			"environments, which restarts their pods and can spend cloud money. Run with --dry-run " +
			"first to review the resolved plan (each member, its channel, and current → target) and " +
			"the exact deploy actions before anything ships. Scope it with TENANT/ENVIRONMENT to " +
			"upgrade a subset; --version pins one version across the set, skipping channel resolution.",
		Example:       "  erun upgrade --dry-run\n  erun upgrade\n  erun upgrade team\n  erun upgrade team prod --version 1.2.3",
		Args:          cobra.MaximumNArgs(2),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := withCloudContextPreflight(commandContext(cmd), store)
			if len(args) >= 1 {
				tenant = args[0]
			}
			if len(args) >= 2 {
				environment = args[1]
			}
			if strings.TrimSpace(environment) != "" && strings.TrimSpace(tenant) == "" {
				return fmt.Errorf("environment requires a tenant: pass TENANT ENVIRONMENT or --tenant with --environment")
			}
			target := common.UpgradeTarget{
				Tenant:          strings.TrimSpace(tenant),
				Environment:     strings.TrimSpace(environment),
				VersionOverride: strings.TrimSpace(versionOverride),
				Force:           force,
			}
			ctx.Trace(fmt.Sprintf("upgrade: tenant=%s environment=%s version-override=%s force=%v",
				target.Tenant, target.Environment, target.VersionOverride, target.Force))

			plan, err := common.ResolveUpgradePlanForStore(ctx, store, target, common.DefaultRuntimeVersionsResolver)
			if err != nil {
				ctx.Trace("upgrade: plan resolution failed: " + err.Error())
				return err
			}
			if len(plan.Items) == 0 {
				ctx.Info("==> No environments opted into Upgrade all" + scopeSuffix(target))
				return nil
			}
			lagging := plan.Lagging()
			ctx.Info(fmt.Sprintf("==> Upgrade plan: %d member(s), %d lagging", len(plan.Items), len(lagging)))
			for _, item := range plan.Items {
				ctx.Info(fmt.Sprintf("    %s/%s [%s] %s -> %s%s",
					item.Tenant, item.Environment, item.Channel,
					displayUpgradeVersion(item.Current), displayUpgradeVersion(item.Target),
					laggingSuffix(item)))
			}

			deployer := func(ctx common.Context, item common.UpgradePlanItem) error {
				deployTarget := common.DeployTarget{
					Tenant:          item.Tenant,
					Environment:     item.Environment,
					VersionOverride: item.Target,
					Force:           target.Force,
				}
				specs, err := common.ResolveCurrentDeploySpecs(ctx, store, findProjectRoot, resolveBuildContext, resolveDeployContext, now, deployTarget)
				if err != nil {
					return err
				}
				if err := common.RunDeploySpecs(ctx, specs, buildDockerImage, push, deployHelmChart); err != nil {
					return err
				}
				return common.PersistRuntimeVersionFromDeploySpecs(ctx, specs, saveEnvConfig)
			}
			result := common.RunUpgradePlan(ctx, plan, deployer)
			if len(result.Failed) > 0 {
				names := make([]string, 0, len(result.Failed))
				for _, failure := range result.Failed {
					names = append(names, failure.Item.Tenant+"/"+failure.Item.Environment)
				}
				return fmt.Errorf("upgrade: %d environment(s) failed: %s", len(result.Failed), strings.Join(names, ", "))
			}
			return nil
		},
	}
	addDryRunFlag(cmd)
	cmd.Flags().StringVar(&versionOverride, "version", "", "Deploy this exact version to every opted-in environment, skipping channel resolution")
	cmd.Flags().StringVar(&tenant, "tenant", "", "Restrict the upgrade to a specific tenant")
	cmd.Flags().StringVar(&environment, "environment", "", "Restrict the upgrade to a specific environment; requires --tenant")
	cmd.Flags().BoolVar(&force, "force", false, "Bypass the fingerprint cache and re-run helm upgrade even when no source change is detected")
	return cmd
}

func scopeSuffix(target common.UpgradeTarget) string {
	if target.Environment != "" {
		return fmt.Sprintf(" for %s/%s", target.Tenant, target.Environment)
	}
	if target.Tenant != "" {
		return " for tenant " + target.Tenant
	}
	return ""
}

func laggingSuffix(item common.UpgradePlanItem) string {
	if item.Lagging {
		return "  (will upgrade)"
	}
	if strings.TrimSpace(item.Target) == "" {
		return "  (target unresolved)"
	}
	return "  (up to date)"
}

func displayUpgradeVersion(v string) string {
	if strings.TrimSpace(v) == "" {
		return "(unset)"
	}
	return v
}

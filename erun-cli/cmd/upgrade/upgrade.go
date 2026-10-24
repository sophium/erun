package upgrade

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/cmd/shared/clicmd"
	"github.com/sophium/erun/cmd/shared/cloudcli"
	"github.com/sophium/erun/cmd/shared/deploynotice"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam"
	"github.com/sophium/erun/erun-common/command/lifecycle"
	"github.com/sophium/erun/erun-common/infra/registry"
	"github.com/sophium/erun/erun-common/primitive/deploy/run"
	"github.com/sophium/erun/erun-common/primitive/deploy/runtimeversion"
	"github.com/sophium/erun/erun-common/primitive/deploy/spec"
	"github.com/spf13/cobra"
)

func NewUpgradeCmd(store seam.DeployStore, saveEnvConfig seam.EnvConfigSaver, findProjectRoot seam.ProjectFinderFunc, resolveBuildContext seam.BuildContextResolverFunc, resolveDeployContext seam.DeployContextResolverFunc, now seam.NowFunc, buildDockerImage seam.DockerImageBuilderFunc, push seam.DockerPushFunc, deployHelmChart seam.HelmChartDeployerFunc) *cobra.Command {
	var versionOverride string
	var tenant string
	var environment string
	var gateEnvironment string
	var force, fleet, overrideLease bool
	var orchestrator string
	cmd := &cobra.Command{
		Use:   "upgrade [TENANT] [ENVIRONMENT]",
		Short: "Redeploy opted-in environments to the latest version for their channel",
		Long: "Redeploy every environment opted into \"Upgrade all\" (autoupgrade) whose runtime " +
			"version lags the latest for its channel (stable or snapshot). Snapshot-channel " +
			"environments adopt a stable release once one is published on top of the latest " +
			"snapshot.\n\n" +
			"Pass --fleet to instead roll every environment in --tenant to --version regardless of " +
			"its own Upgrade-all opt-in -- the explicit way to remediate version drift found by " +
			"`erun list --tenant` (a tenant's environments do not all have to opt into the routine " +
			"cadence to be rolled together once). Add --gate-environment to name the environment " +
			"driving that tenant's merge-queue gate: it is always included and always rolled first, " +
			"regardless of --fleet, so the gate never validates a change against code it is itself " +
			"behind (the release-cadence policy's \"immediate, unconditional\" gate redeploy).\n\n" +
			"High blast radius: this rolls out new runtime images to multiple — possibly remote — " +
			"environments, which restarts their pods and can spend cloud money. Run with --dry-run " +
			"first to review the resolved plan (each member, its channel, and current → target, in " +
			"the exact order it will deploy) and the exact deploy actions before anything ships. " +
			"Scope it with TENANT/ENVIRONMENT to upgrade a subset (one environment at a time is a " +
			"plain positional scope); --version pins one version across the set, skipping channel " +
			"resolution. Because a roll restarts the runtime pod, each environment is refused — " +
			"naming the holder — while it is held by another worker (a running build, deploy, or " +
			"agent session); pass --override-lease to roll it anyway.\n\n" +
			"Out of scope: the installed desktop app bundle (ERun.app / erun-app) is not something this " +
			"command upgrades -- it only rolls remote runtime environments. `erun doctor` reports the " +
			"installed bundle's version and flags drift from this CLI; there is no automated way to " +
			"update the bundle itself today.",
		Example: "  erun upgrade --dry-run\n  erun upgrade\n  erun upgrade team\n  erun upgrade team prod --version 1.2.3\n" +
			"  erun upgrade team --fleet --version 1.2.3 --gate-environment build --dry-run\n" +
			"  erun upgrade team --fleet --version 1.2.3 --gate-environment build\n" +
			"  erun upgrade team build --version 1.2.3\n" +
			"  erun upgrade team prod --version 1.2.3 --override-lease",
		Args:          cobra.MaximumNArgs(2),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := upgradeTargetFromArgs(args, tenant, environment, gateEnvironment, versionOverride, force, fleet)
			if err != nil {
				return err
			}
			holder := model.EnvironmentActivityLeaseHolder{Orchestrator: strings.TrimSpace(orchestrator)}
			deployer := newUpgradeDeployer(store, saveEnvConfig, findProjectRoot, resolveBuildContext, resolveDeployContext, now, buildDockerImage, push, deployHelmChart, target.Force)
			deployer = lifecycle.LeaseGuardedUpgradeDeployer(deployer, overrideLease, holder, nil)
			return runUpgrade(cloudcli.WithCloudContextPreflight(clicmd.CommandContext(cmd), store), store, target, deployer)
		},
	}
	clicmd.AddDryRunFlag(cmd)
	cmd.Flags().StringVar(&versionOverride, "version", "", "Deploy this exact version to every opted-in environment, skipping channel resolution")
	cmd.Flags().StringVar(&tenant, "tenant", "", "Restrict the upgrade to a specific tenant")
	cmd.Flags().StringVar(&environment, "environment", "", "Restrict the upgrade to a specific environment; requires --tenant")
	cmd.Flags().BoolVar(&force, "force", false, "Bypass the fingerprint cache and re-run helm upgrade even when no source change is detected")
	cmd.Flags().BoolVar(&fleet, "fleet", false, "Include every environment in --tenant regardless of its own Upgrade-all opt-in; requires --tenant")
	cmd.Flags().StringVar(&gateEnvironment, "gate-environment", "", "Name the environment driving --tenant's merge-queue gate; always included and always rolled first, regardless of --fleet; requires --tenant")
	cmd.Flags().BoolVar(&overrideLease, "override-lease", false, "Roll an environment even though it is currently held by another worker")
	cmd.Flags().StringVar(&orchestrator, "orchestrator", "", "The calling orchestrator's own id, recorded on each deploy's lease and on any override")
	return cmd
}

func upgradeTargetFromArgs(args []string, tenant, environment, gateEnvironment, versionOverride string, force, fleet bool) (model.UpgradeTarget, error) {
	if len(args) >= 1 {
		tenant = args[0]
	}
	if len(args) >= 2 {
		environment = args[1]
	}
	if strings.TrimSpace(environment) != "" && strings.TrimSpace(tenant) == "" {
		return model.UpgradeTarget{}, fmt.Errorf("environment requires a tenant: pass TENANT ENVIRONMENT or --tenant with --environment")
	}
	return model.UpgradeTarget{
		Tenant:          strings.TrimSpace(tenant),
		Environment:     strings.TrimSpace(environment),
		VersionOverride: strings.TrimSpace(versionOverride),
		Force:           force,
		Fleet:           fleet,
		GateEnvironment: strings.TrimSpace(gateEnvironment),
	}, nil
}

func runUpgrade(ctx core.Context, store seam.DeployStore, target model.UpgradeTarget, deploy seam.UpgradeItemDeployer) error {
	// Only a scoped run has one env to attribute the trace to.
	if target.Tenant != "" && target.Environment != "" {
		var closeEnvTrace func()
		ctx, closeEnvTrace = core.ActivateEnvTrace(ctx, target.Tenant, target.Environment)
		defer closeEnvTrace()
	}
	ctx.Trace(fmt.Sprintf("upgrade: tenant=%s environment=%s version-override=%s force=%v fleet=%v gate-environment=%s",
		target.Tenant, target.Environment, target.VersionOverride, target.Force, target.Fleet, target.GateEnvironment))

	plan, err := lifecycle.ResolveUpgradePlanForStore(ctx, store, target, lifecycle.UpgradeVersionsResolverForStore(store, registry.ResolveRuntimeImageRegistryVersions))
	if err != nil {
		ctx.Trace("upgrade: plan resolution failed: " + err.Error())
		return err
	}
	if len(plan.Items) == 0 {
		ctx.Info("==> No environments" + upgradeScopeReason(target) + scopeSuffix(target))
		return printUpgradeDryRunMarker(ctx)
	}
	lagging := plan.Lagging()
	ctx.Info(fmt.Sprintf("==> Upgrade plan: %d member(s), %d lagging, in this order:", len(plan.Items), len(lagging)))
	for i, item := range plan.Items {
		ctx.Info(fmt.Sprintf("    %d. %s/%s [%s] %s -> %s%s%s",
			i+1, item.Tenant, item.Environment, item.Channel,
			displayUpgradeVersion(item.Current), displayUpgradeVersion(item.Target),
			laggingSuffix(item), gateSuffix(item)))
	}

	result := lifecycle.RunUpgradePlan(ctx, plan, deploy)
	if err := printUpgradeDryRunMarker(ctx); err != nil {
		return err
	}
	if len(result.Failed) > 0 {
		names := make([]string, 0, len(result.Failed))
		for _, failure := range result.Failed {
			names = append(names, failure.Item.Tenant+"/"+failure.Item.Environment)
		}
		return fmt.Errorf("upgrade: %d environment(s) failed: %s", len(result.Failed), strings.Join(names, ", "))
	}
	return nil
}

// printUpgradeDryRunMarker prints the dry-run terminal line so a preview never reads as a rollout.
func printUpgradeDryRunMarker(ctx core.Context) error {
	if !ctx.DryRun {
		return nil
	}
	_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun upgrade planned.")
	return err
}

func newUpgradeDeployer(store seam.DeployStore, saveEnvConfig seam.EnvConfigSaver, findProjectRoot seam.ProjectFinderFunc, resolveBuildContext seam.BuildContextResolverFunc, resolveDeployContext seam.DeployContextResolverFunc, now seam.NowFunc, buildDockerImage seam.DockerImageBuilderFunc, push seam.DockerPushFunc, deployHelmChart seam.HelmChartDeployerFunc, force bool) seam.UpgradeItemDeployer {
	return func(ctx core.Context, item model.UpgradePlanItem) error {
		deployTarget := model.DeployTarget{
			Tenant:          item.Tenant,
			Environment:     item.Environment,
			VersionOverride: item.Target,
			Force:           force,
		}
		specs, err := spec.ResolveCurrentDeploySpecs(ctx, store, findProjectRoot, resolveBuildContext, resolveDeployContext, now, deployTarget)
		if err != nil {
			return err
		}
		if err := run.RunDeploySpecs(ctx, specs, deployHelmChart); err != nil {
			return err
		}
		if err := runtimeversion.PersistRuntimeVersionFromDeploySpecs(ctx, specs, saveEnvConfig, runtimeversion.ResolveDeployedHelmReleaseVersion); err != nil {
			return err
		}
		deploynotice.NoticeStaleRuntimePortForwards(ctx, specs)
		return nil
	}
}

// upgradeScopeReason explains an empty plan. --fleet and --gate-environment ignore
// opt-in, so an empty plan there means the tenant has no environments.
func upgradeScopeReason(target model.UpgradeTarget) string {
	if target.Fleet || target.GateEnvironment != "" {
		return " to roll"
	}
	return " opted into Upgrade all"
}

func gateSuffix(item model.UpgradePlanItem) string {
	if item.IsGate {
		return "  [gate]"
	}
	return ""
}

func scopeSuffix(target model.UpgradeTarget) string {
	if target.Environment != "" {
		return fmt.Sprintf(" for %s/%s", target.Tenant, target.Environment)
	}
	if target.Tenant != "" {
		return " for tenant " + target.Tenant
	}
	return ""
}

func laggingSuffix(item model.UpgradePlanItem) string {
	if item.Lagging {
		return "  (will upgrade)"
	}
	if strings.TrimSpace(item.Target) == "" {
		if reason := strings.TrimSpace(item.UnresolvedReason); reason != "" {
			return "  (target unresolved: " + reason + ")"
		}
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

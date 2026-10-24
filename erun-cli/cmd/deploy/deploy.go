package deploy

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/cmd/shared/clicmd"
	"github.com/sophium/erun/cmd/shared/cloudcli"
	"github.com/sophium/erun/cmd/shared/deploynotice"
	"github.com/sophium/erun/cmd/shared/opentarget"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam"
	"github.com/sophium/erun/erun-common/primitive/build"
	"github.com/sophium/erun/erun-common/primitive/deploy"
	"github.com/spf13/cobra"
)

func NewDeployCmd(store seam.DeployStore, saveEnvConfig seam.EnvConfigSaver, findProjectRoot seam.ProjectFinderFunc, resolveBuildContext seam.BuildContextResolverFunc, resolveDeployContext seam.DeployContextResolverFunc, now seam.NowFunc, buildDockerImage build.DockerImageBuilderFunc, push build.DockerPushFunc, deployHelmChart deploy.HelmChartDeployerFunc) *cobra.Command {
	target := model.DeployTarget{}
	var components []string
	var useCurrent bool
	var erunAlias string
	cmd := &cobra.Command{
		Use:   "deploy [TENANT] [ENVIRONMENT]",
		Short: "Install a published version into an environment",
		Long: "Install a published version into an environment with helm.\n\n" +
			"deploy is a pure consume operation: it installs the image and chart already published " +
			"at a version, by reference. It never builds or pushes — produce a version with `erun build` " +
			"and `erun push` (or `erun build --deploy` to chain them) first. Pass the version with " +
			"--version; use --current to redeploy the version this environment already runs. " +
			"Component selection is opt-in: deploy rolls out exactly the charts you select and nothing else. " +
			"With no selection it deploys the environment's runtime chart alone (bootstrapping or healing it); " +
			"use --components to deploy specific charts this run, or save a default selection " +
			"(the desktop Runtime tab writes it to the env's deploy.components). " +
			"Pass --runtime-image to install the canonical ERun base image (or any image) via the " +
			"published runtime chart — useful to bootstrap an environment before its own image is built. " +
			"Defaults to the current scope; pass TENANT and ENVIRONMENT (or --tenant/--environment) to target another.\n\n" +
			"deploy waits for the rollout to become ready — default 15m, or the env's `deploy.timeout`, " +
			"or --rollout-timeout — and watches the new pods: it keeps waiting while an image is still " +
			"pulling and aborts early on a real container failure (crash, config error, or a permanent " +
			"image-pull rejection) instead of waiting out the timeout. The default is sized for a cold " +
			"pull of the runtime image, which the rollout has to wait out with the previous pod already " +
			"torn down; raise it if a genuinely cold pull of a bigger image needs longer. " +
			"Pass --max-cpu/--max-memory/--max-storage together to cap the environment's namespace with a " +
			"Kubernetes ResourceQuota+LimitRange for this deploy; omit to use the env's saved namespace quota, if any.\n\n" +
			"A runtime deploy also hands the pod this host's own signed-in erun platform identity — the " +
			"credential its `erun review` calls act as — for an environment that records " +
			"none yet; pass --erun-alias to say which alias that delegates when this host has several " +
			"configured, since erun cannot guess. An environment that already records one keeps it.",
		Example:       "  erun deploy team prod --version 1.2.3\n  erun deploy team dev --current\n  erun deploy team dev --version 1.2.3 --runtime-image ghcr.io/sophium/erun-devops\n  erun deploy team prod --version 1.2.3 --rollout-timeout 10m\n  erun deploy team prod --version 1.2.3 --max-cpu 4 --max-memory 8Gi --max-storage 80Gi\n  erun deploy team prod --version 1.2.3 --erun-alias erun+api.acme.services.erunpaas.com@erun",
		Args:          cobra.MaximumNArgs(2),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cloudcli.WithCloudContextPreflight(clicmd.CommandContext(cmd), store)
			ctx.PlatformAlias = strings.TrimSpace(erunAlias)
			deployTarget, err := opentarget.ResolveDeployTargetArgs(cmd.CommandPath(), args, target)
			if err != nil {
				return err
			}
			deployTarget.Components = components
			// deploy never mints a version.
			if strings.TrimSpace(deployTarget.VersionOverride) == "" && !useCurrent {
				return fmt.Errorf("deploy requires a version: pass --version <version> produced by `erun build`/`erun push`, or --current to redeploy the version this environment already runs")
			}
			// Resolve the scope up front so failure traces name the env.
			tenant, environment := deploy.ResolveDeployTargetScope(store, findProjectRoot, deployTarget)
			var closeEnvTrace func()
			ctx, closeEnvTrace = core.ActivateEnvTrace(ctx, tenant, environment)
			defer closeEnvTrace()
			ctx.Trace(fmt.Sprintf("deploy: tenant=%s environment=%s version-override=%s components=%v force=%v current=%v",
				tenant, environment, deployTarget.VersionOverride,
				components, deployTarget.Force, useCurrent))
			if err := runDeploy(ctx, store, saveEnvConfig, findProjectRoot, resolveBuildContext, resolveDeployContext, now, deployHelmChart, deployTarget); err != nil {
				// The desktop activity queue reacts only to `==> ...` lines.
				ctx.Trace(deployFailedTrace(tenant, environment, err))
				return err
			}
			return nil
		},
	}
	clicmd.AddDryRunFlag(cmd)
	addDeployCommandTargetFlags(cmd, &target)
	cmd.Flags().StringVar(&target.RuntimeImageOverride, "runtime-image", "", "Install the runtime running this image via the published erun-devops chart (imageOverrides.erun-devops), pinned to --version, even when the env has a repo-local runtime chart; mirrors `erun open --runtime-image`")
	cmd.Flags().StringVar(&target.RuntimeChartOverride, "runtime-chart", "", "Install this runtime chart, as an OCI reference that may carry its own version (oci://registry/charts/erun-devops:1.0.178). States the chart as its own coordinate instead of deriving it from --version and the registry a previous deploy recorded, which is what lets the runtime image be versioned on a different release line than the chart")
	cmd.Flags().BoolVar(&useCurrent, "current", false, "Redeploy the version this environment already runs (its persisted runtime version) instead of passing --version")
	cmd.Flags().StringVar(&erunAlias, "erun-alias", "", "erun platform cloud alias to hand the environment's runtime as its platform identity when it records none (defaults to the sole configured erun-type alias; only needed to disambiguate when more than one is configured)")
	cmd.Flags().StringSliceVar(&components, "components", nil, "Deploy exactly these charts this run — chart directory names under <tenant>-devops/k8s/, or the runtime release name (<tenant>-devops); overrides the env's saved selection and the k8s.deployments plan. Empty falls back to the saved selection, then the plan, then the runtime chart alone")
	return cmd
}

// deployFailedTrace omits the env when the scope never resolved.
func deployFailedTrace(tenant, environment string, err error) string {
	if tenant != "" && environment != "" {
		return fmt.Sprintf("==> Deploy failed %s/%s: %s", tenant, environment, err.Error())
	}
	return fmt.Sprintf("==> Deploy failed: %s", err.Error())
}

func runDeploy(ctx core.Context, store seam.DeployStore, saveEnvConfig seam.EnvConfigSaver, findProjectRoot seam.ProjectFinderFunc, resolveBuildContext seam.BuildContextResolverFunc, resolveDeployContext seam.DeployContextResolverFunc, now seam.NowFunc, deployHelmChart deploy.HelmChartDeployerFunc, deployTarget model.DeployTarget) error {
	deploySpecs, err := deploy.ResolveCurrentDeploySpecs(ctx, store, findProjectRoot, resolveBuildContext, resolveDeployContext, now, deployTarget)
	if err != nil {
		ctx.Trace("deploy: spec resolution failed: " + err.Error())
		return err
	}
	ctx.Trace(fmt.Sprintf("deploy: resolved %d spec(s)", len(deploySpecs)))
	if err := deploy.RunDeploySpecs(ctx, deploySpecs, deployHelmChart); err != nil {
		return err
	}
	if err := deploy.PersistRuntimeVersionFromDeploySpecs(ctx, deploySpecs, saveEnvConfig, deploy.ResolveDeployedHelmReleaseVersion); err != nil {
		return err
	}
	deploynotice.NoticeStaleRuntimePortForwards(ctx, deploySpecs)
	return nil
}

func NewK8sDeployCmd(store seam.DeployStore, saveEnvConfig seam.EnvConfigSaver, findProjectRoot seam.ProjectFinderFunc, resolveBuildContext seam.BuildContextResolverFunc, resolveDeployContext seam.DeployContextResolverFunc, now seam.NowFunc, buildDockerImage build.DockerImageBuilderFunc, push build.DockerPushFunc, deployHelmChart deploy.HelmChartDeployerFunc) *cobra.Command {
	target := model.DeployTarget{}
	cmd := &cobra.Command{
		Use:           "deploy COMPONENT",
		Short:         "Deploy a component Helm chart",
		Args:          cobra.ExactArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cloudcli.WithCloudContextPreflight(clicmd.CommandContext(cmd), store)
			deploySpec, err := deploy.ResolveDeploySpec(ctx, store, findProjectRoot, resolveBuildContext, resolveDeployContext, now, target, args[0], "")
			if err != nil {
				return err
			}
			if err := deploy.RunDeploySpec(ctx, deploySpec, deployHelmChart); err != nil {
				return err
			}
			return deploy.PersistRuntimeVersionFromDeploySpecs(ctx, []deploy.DeploySpec{deploySpec}, saveEnvConfig, deploy.ResolveDeployedHelmReleaseVersion)
		},
	}
	clicmd.AddDryRunFlag(cmd)
	addDeployCommandTargetFlags(cmd, &target)
	return cmd
}

func addDeployCommandTargetFlags(cmd *cobra.Command, target *model.DeployTarget) {
	cmd.Flags().StringVar(&target.VersionOverride, "version", "", "Version to install by reference (produced by `erun build`/`erun push`); fails if that version's image is absent")
	cmd.Flags().StringVar(&target.Tenant, "tenant", "", "Deploy for a specific tenant")
	cmd.Flags().StringVar(&target.Environment, "environment", "", "Deploy for a specific environment; requires --tenant")
	cmd.Flags().BoolVar(&target.Force, "force", false, "Re-run the helm upgrade even when the deployed release already matches the requested version")
	cmd.Flags().StringVar(&target.RolloutTimeout, "rollout-timeout", "", "Override the helm rollout wait for this deploy (Go duration, e.g. 20m); empty uses the env's deploy.timeout or the 15m default")
	cmd.Flags().StringVar(&target.NamespaceQuotaOverride.CPU, "max-cpu", "", "Cap the environment's namespace to this much CPU (Kubernetes quantity, e.g. 4) via a ResourceQuota+LimitRange; requires --max-memory and --max-storage too, and overrides the env's saved namespace quota for this deploy only")
	cmd.Flags().StringVar(&target.NamespaceQuotaOverride.Memory, "max-memory", "", "Cap the environment's namespace to this much memory (Kubernetes quantity, e.g. 8Gi); requires --max-cpu and --max-storage too")
	cmd.Flags().StringVar(&target.NamespaceQuotaOverride.Storage, "max-storage", "", "Cap the environment's namespace to this much storage (Kubernetes quantity, e.g. 80Gi); requires --max-cpu and --max-memory too")
	cmd.Flags().StringVar(&target.MCPAuthPublicKeyPath, "mcp-auth-public-key", "", "Require the env's MCP edge to authenticate bearer tokens signed by this PEM public key, and record it on the env so later redeploys keep authenticating; omit to reuse the recorded key")
	cmd.Flags().BoolVar(&target.DisableMCPAuth, "no-mcp-auth", false, "Deploy the env's MCP edge unauthenticated (loopback-only) and forget its recorded public key; required to turn authentication off, which deploy otherwise refuses to do by omission")
	cmd.Flags().StringVar(&target.RepoPath, "repo-path", "", "Repo path override for internal tooling")
	_ = cmd.Flags().MarkHidden("repo-path")
}

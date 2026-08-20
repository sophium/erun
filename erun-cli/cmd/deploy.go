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
			"Component selection is opt-in: deploy rolls out exactly the charts you select and nothing else. " +
			"With no selection it deploys the environment's runtime chart alone (bootstrapping or healing it); " +
			"use --components to deploy specific charts this run, or save a default selection " +
			"(the desktop Runtime tab writes it to the env's deploy.components). " +
			"Pass --runtime-image to install the canonical ERun base image (or any image) via the " +
			"published runtime chart — useful to bootstrap an environment before its own image is built. " +
			"Defaults to the current scope; pass TENANT and ENVIRONMENT (or --tenant/--environment) to target another.\n\n" +
			"deploy waits for the rollout to become ready — default 5m, or the env's `deploy.timeout`, " +
			"or --rollout-timeout — and watches the new pods: it keeps waiting while an image is still " +
			"pulling and aborts early on a real container failure (crash, config error, or a permanent " +
			"image-pull rejection) instead of waiting out the timeout. " +
			"Pass --max-cpu/--max-memory/--max-storage together to cap the environment's namespace with a " +
			"Kubernetes ResourceQuota+LimitRange for this deploy; omit to use the env's saved namespace quota, if any.",
		Example:       "  erun deploy team prod --version 1.2.3\n  erun deploy team dev --current\n  erun deploy team dev --version 1.2.3 --runtime-image ghcr.io/sophium/erun-devops\n  erun deploy team prod --version 1.2.3 --rollout-timeout 10m\n  erun deploy team prod --version 1.2.3 --max-cpu 4 --max-memory 8Gi --max-storage 80Gi",
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
			// deploy consumes a version by reference and never mints one, so it
			// must be told which to install.
			if strings.TrimSpace(deployTarget.VersionOverride) == "" && !useCurrent {
				return fmt.Errorf("deploy requires a version: pass --version <version> produced by `erun build`/`erun push`, or --current to redeploy the version this environment already runs")
			}
			// A scope-defaulted deploy carries no tenant/environment on the
			// target, so name them up front: the per-env trace log and every
			// `==> ...` line must identify the env even when the deploy fails
			// before spec resolution names it.
			tenant, environment := common.ResolveDeployTargetScope(store, findProjectRoot, deployTarget)
			var closeEnvTrace func()
			ctx, closeEnvTrace = common.ActivateEnvTrace(ctx, tenant, environment)
			defer closeEnvTrace()
			ctx.Trace(fmt.Sprintf("deploy: tenant=%s environment=%s version-override=%s components=%v force=%v current=%v",
				tenant, environment, deployTarget.VersionOverride,
				components, deployTarget.Force, useCurrent))
			if err := runDeploy(ctx, store, saveEnvConfig, findProjectRoot, resolveBuildContext, resolveDeployContext, now, deployHelmChart, deployTarget); err != nil {
				// Transports that react only to `==> ...` trace lines (the desktop
				// activity queue) need one for a deploy that fails before rollout,
				// or they surface nothing.
				ctx.Trace(deployFailedTrace(tenant, environment, err))
				return err
			}
			return nil
		},
	}
	addDryRunFlag(cmd)
	addDeployCommandTargetFlags(cmd, &target)
	cmd.Flags().StringVar(&target.RuntimeImageOverride, "runtime-image", "", "Install the runtime running this image via the published erun-devops chart (imageOverrides.erun-devops), pinned to --version, even when the env has a repo-local runtime chart; mirrors `erun open --runtime-image`")
	cmd.Flags().StringVar(&target.RuntimeChartOverride, "runtime-chart", "", "Install this runtime chart, as an OCI reference that may carry its own version (oci://registry/charts/erun-devops:1.0.178). States the chart as its own coordinate instead of deriving it from --version and the registry a previous deploy recorded, which is what lets the runtime image be versioned on a different release line than the chart")
	cmd.Flags().BoolVar(&useCurrent, "current", false, "Redeploy the version this environment already runs (its persisted runtime version) instead of passing --version")
	cmd.Flags().StringSliceVar(&components, "components", nil, "Deploy exactly these charts this run — chart directory names under <tenant>-devops/k8s/, or the runtime release name (<tenant>-devops); overrides the env's saved selection and the k8s.deployments plan. Empty falls back to the saved selection, then the plan, then the runtime chart alone")
	return cmd
}

// deployFailedTrace renders the failure header transports parse for the failed
// env. When the scope never resolved there is no env to name, so the pair is
// dropped rather than rendered as a bare separator no reader or parser can use.
func deployFailedTrace(tenant, environment string, err error) string {
	if tenant != "" && environment != "" {
		return fmt.Sprintf("==> Deploy failed %s/%s: %s", tenant, environment, err.Error())
	}
	return fmt.Sprintf("==> Deploy failed: %s", err.Error())
}

func runDeploy(ctx common.Context, store common.DeployStore, saveEnvConfig common.EnvConfigSaver, findProjectRoot common.ProjectFinderFunc, resolveBuildContext common.BuildContextResolverFunc, resolveDeployContext common.DeployContextResolverFunc, now common.NowFunc, deployHelmChart common.HelmChartDeployerFunc, deployTarget common.DeployTarget) error {
	deploySpecs, err := common.ResolveCurrentDeploySpecs(ctx, store, findProjectRoot, resolveBuildContext, resolveDeployContext, now, deployTarget)
	if err != nil {
		ctx.Trace("deploy: spec resolution failed: " + err.Error())
		return err
	}
	ctx.Trace(fmt.Sprintf("deploy: resolved %d spec(s)", len(deploySpecs)))
	if err := common.RunDeploySpecs(ctx, deploySpecs, deployHelmChart); err != nil {
		return err
	}
	if err := common.PersistRuntimeVersionFromDeploySpecs(ctx, deploySpecs, saveEnvConfig, common.ResolveDeployedHelmReleaseVersion); err != nil {
		return err
	}
	noticeStaleRuntimePortForwards(ctx, deploySpecs)
	return nil
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
	cmd.Flags().StringVar(&target.NamespaceQuotaOverride.CPU, "max-cpu", "", "Cap the environment's namespace to this much CPU (Kubernetes quantity, e.g. 4) via a ResourceQuota+LimitRange; requires --max-memory and --max-storage too, and overrides the env's saved namespace quota for this deploy only")
	cmd.Flags().StringVar(&target.NamespaceQuotaOverride.Memory, "max-memory", "", "Cap the environment's namespace to this much memory (Kubernetes quantity, e.g. 8Gi); requires --max-cpu and --max-storage too")
	cmd.Flags().StringVar(&target.NamespaceQuotaOverride.Storage, "max-storage", "", "Cap the environment's namespace to this much storage (Kubernetes quantity, e.g. 80Gi); requires --max-cpu and --max-memory too")
	cmd.Flags().StringVar(&target.MCPAuthPublicKeyPath, "mcp-auth-public-key", "", "Require the env's MCP edge to authenticate bearer tokens signed by this PEM public key, and record it on the env so later redeploys keep authenticating; omit to reuse the recorded key")
	cmd.Flags().BoolVar(&target.DisableMCPAuth, "no-mcp-auth", false, "Deploy the env's MCP edge unauthenticated (loopback-only) and forget its recorded public key; required to turn authentication off, which deploy otherwise refuses to do by omission")
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

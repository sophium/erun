package erunmcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

type UpgradeInput struct {
	Version   string `json:"version,omitempty" jsonschema:"deploy this exact version instead of the channel latest, skipping registry resolution"`
	Force     bool   `json:"force,omitempty" jsonschema:"when true, bypass the fingerprint cache and re-run helm upgrade even when no source change is detected"`
	Preview   bool   `json:"preview,omitempty" jsonschema:"when true, resolve and print the upgrade plan (channel, current -> target) without deploying"`
	Verbosity int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
}

// upgradeTool resolves the upgrade plan for the runtime's environment and, when
// it is opted in (autoupgrade) and lags its channel latest, redeploys it. The
// in-pod runtime serves a single tenant/environment, so the plan is scoped to
// it; preview gates execution like --dry-run.
func upgradeTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, UpgradeInput) (*mcp.CallToolResult, CommandOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input UpgradeInput) (*mcp.CallToolResult, CommandOutput, error) {
		output, err := runRuntimeCommand(runtime, input.Preview, input.Verbosity, func(runCtx eruncommon.Context, workDir string) error {
			target := eruncommon.UpgradeTarget{
				Tenant:          strings.TrimSpace(runtime.Context.Tenant),
				Environment:     strings.TrimSpace(runtime.Context.Environment),
				VersionOverride: strings.TrimSpace(input.Version),
				Force:           input.Force,
			}
			plan, err := eruncommon.ResolveUpgradePlanForStore(runCtx, runtime.Store, target, eruncommon.UpgradeVersionsResolverForStore(runtime.Store, eruncommon.ResolveRuntimeImageRegistryVersions))
			if err != nil {
				return err
			}

			deployer := func(ctx eruncommon.Context, item eruncommon.UpgradePlanItem) error {
				findProjectRoot := func() (string, string, error) {
					return runtimeFindProjectRoot(runtime.Context, workDir)
				}
				resolveBuildContext := func() (eruncommon.DockerBuildContext, error) {
					return eruncommon.DockerBuildContextAtDir(workDir)
				}
				resolveDeployContext := func() (eruncommon.KubernetesDeployContext, error) {
					return eruncommon.KubernetesDeployContextAtDir(workDir), nil
				}
				deployTarget := eruncommon.DeployTarget{
					Tenant:          item.Tenant,
					Environment:     item.Environment,
					RepoPath:        workDir,
					VersionOverride: item.Target,
					Force:           target.Force,
				}
				specs, err := eruncommon.ResolveCurrentDeploySpecs(ctx, runtime.Store, findProjectRoot, resolveBuildContext, resolveDeployContext, nil, deployTarget)
				if err != nil {
					return err
				}
				if err := eruncommon.RunDeploySpecs(ctx, specs, runtime.DeployHelmChart); err != nil {
					return err
				}
				return eruncommon.PersistRuntimeVersionFromDeploySpecs(ctx, specs, runtime.Store.SaveEnvConfig, eruncommon.ResolveDeployedHelmReleaseVersion)
			}

			result := eruncommon.RunUpgradePlan(runCtx, plan, deployer)
			if len(result.Failed) > 0 {
				names := make([]string, 0, len(result.Failed))
				for _, failure := range result.Failed {
					names = append(names, failure.Item.Tenant+"/"+failure.Item.Environment)
				}
				return fmt.Errorf("upgrade: %d environment(s) failed: %s", len(result.Failed), strings.Join(names, ", "))
			}
			return nil
		})
		return nil, output, err
	}
}

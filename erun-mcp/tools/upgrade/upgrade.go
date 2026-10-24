package upgrade

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/command/lifecycle"
	"github.com/sophium/erun/erun-common/infra/registry"
	"github.com/sophium/erun/erun-mcp/tools/shared"
)

type UpgradeInput struct {
	Version   string `json:"version,omitempty" jsonschema:"deploy this exact version instead of the channel latest, skipping registry resolution"`
	Force     bool   `json:"force,omitempty" jsonschema:"when true, bypass the fingerprint cache and re-run helm upgrade even when no source change is detected"`
	Preview   bool   `json:"preview,omitempty" jsonschema:"when true, resolve and print the upgrade plan (channel, current -> target) without deploying"`
	Verbosity int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
	shared.JobEnvelopeInput
}

// UpgradeTool redeploys the runtime's environment only when it is opted into
// autoupgrade and lags its channel latest. The in-pod runtime serves one
// tenant/environment, so the plan is scoped to that single target.
func UpgradeTool(runtime shared.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, UpgradeInput) (*mcp.CallToolResult, shared.JobEnvelopeOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input UpgradeInput) (*mcp.CallToolResult, shared.JobEnvelopeOutput, error) {
		execute := shared.SimpleJobExecute(runtime, input.Verbosity, func(runCtx core.Context, workDir string) error {
			target := lifecycle.UpgradeTarget{
				Tenant:          strings.TrimSpace(runtime.Context.Tenant),
				Environment:     strings.TrimSpace(runtime.Context.Environment),
				VersionOverride: strings.TrimSpace(input.Version),
				Force:           input.Force,
			}
			plan, err := lifecycle.ResolveUpgradePlanForStore(runCtx, runtime.Store, target, lifecycle.UpgradeVersionsResolverForStore(runtime.Store, registry.ResolveRuntimeImageRegistryVersions))
			if err != nil {
				return err
			}

			deployer := func(ctx core.Context, item lifecycle.UpgradePlanItem) error {
				findProjectRoot := func() (string, string, error) {
					return shared.RuntimeFindProjectRoot(runtime.Context, workDir)
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

			result := lifecycle.RunUpgradePlan(runCtx, plan, deployer)
			if len(result.Failed) > 0 {
				names := make([]string, 0, len(result.Failed))
				for _, failure := range result.Failed {
					names = append(names, failure.Item.Tenant+"/"+failure.Item.Environment)
				}
				return fmt.Errorf("upgrade: %d environment(s) failed: %s", len(result.Failed), strings.Join(names, ", "))
			}
			return nil
		})
		envelope, err := shared.RunJobEnvelope(runtime, "upgrade", input.JobEnvelopeInput, input.Preview, execute)
		return nil, envelope, err
	}
}

package upgrade

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/lifecycle"
	"github.com/sophium/erun/erun-common/infra/registry"
	"github.com/sophium/erun/erun-common/primitive/deploy/kubecheck"
	"github.com/sophium/erun/erun-common/primitive/deploy/run"
	"github.com/sophium/erun/erun-common/primitive/deploy/runtimeversion"
	"github.com/sophium/erun/erun-common/primitive/deploy/spec"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerspec"
	"github.com/sophium/erun/erun-mcp/tools/shared/jobenvelope"
	"github.com/sophium/erun/erun-mcp/tools/shared/runcmd"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
	"github.com/sophium/erun/erun-mcp/tools/shared/toolio"
)

type UpgradeInput struct {
	Version   string `json:"version,omitempty" jsonschema:"deploy this exact version instead of the channel latest, skipping registry resolution"`
	Force     bool   `json:"force,omitempty" jsonschema:"when true, bypass the fingerprint cache and re-run helm upgrade even when no source change is detected"`
	Preview   bool   `json:"preview,omitempty" jsonschema:"when true, resolve and print the upgrade plan (channel, current -> target) without deploying"`
	Verbosity int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
	jobenvelope.JobEnvelopeInput
}

// UpgradeTool redeploys the runtime's environment only when it opts into
// autoupgrade and lags its channel latest.
func UpgradeTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, UpgradeInput) (*mcp.CallToolResult, jobenvelope.JobEnvelopeOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input UpgradeInput) (*mcp.CallToolResult, jobenvelope.JobEnvelopeOutput, error) {
		execute := runcmd.SimpleJobExecute(runtime, input.Verbosity, func(runCtx core.Context, workDir string) error {
			target := model.UpgradeTarget{
				Tenant:          strings.TrimSpace(runtime.Context.Tenant),
				Environment:     strings.TrimSpace(runtime.Context.Environment),
				VersionOverride: strings.TrimSpace(input.Version),
				Force:           input.Force,
			}
			plan, err := lifecycle.ResolveUpgradePlanForStore(runCtx, runtime.Store, target, lifecycle.UpgradeVersionsResolverForStore(runtime.Store, registry.ResolveRuntimeImageRegistryVersions))
			if err != nil {
				return err
			}

			deployer := func(ctx core.Context, item model.UpgradePlanItem) error {
				findProjectRoot := func() (string, string, error) {
					return toolio.RuntimeFindProjectRoot(runtime.Context, workDir)
				}
				resolveBuildContext := func() (model.DockerBuildContext, error) {
					return dockerspec.DockerBuildContextAtDir(workDir)
				}
				resolveDeployContext := func() (model.KubernetesDeployContext, error) {
					return kubecheck.KubernetesDeployContextAtDir(workDir), nil
				}
				deployTarget := model.DeployTarget{
					Tenant:          item.Tenant,
					Environment:     item.Environment,
					RepoPath:        workDir,
					VersionOverride: item.Target,
					Force:           target.Force,
				}
				specs, err := spec.ResolveCurrentDeploySpecs(ctx, runtime.Store, findProjectRoot, resolveBuildContext, resolveDeployContext, nil, deployTarget)
				if err != nil {
					return err
				}
				if err := run.RunDeploySpecs(ctx, specs, runtime.DeployHelmChart); err != nil {
					return err
				}
				return runtimeversion.PersistRuntimeVersionFromDeploySpecs(ctx, specs, runtime.Store.SaveEnvConfig, runtimeversion.ResolveDeployedHelmReleaseVersion)
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
		envelope, err := jobenvelope.RunJobEnvelope(runtime, "upgrade", input.JobEnvelopeInput, input.Preview, execute)
		return nil, envelope, err
	}
}

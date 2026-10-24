package pin

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/lifecycle"
	"github.com/sophium/erun/erun-mcp/tools/shared"
)

// PinInput selects which erun version an environment's references move to.
type PinInput struct {
	Tenant      string `json:"tenant,omitempty" jsonschema:"tenant whose environment to re-pin; defaults to the MCP runtime context tenant"`
	Environment string `json:"environment,omitempty" jsonschema:"environment to re-pin; defaults to the MCP runtime context environment"`
	// ProjectRoot names the checkout to rewrite; an MCP caller has no cwd.
	ProjectRoot string `json:"projectRoot,omitempty" jsonschema:"repo root holding the sites to re-pin (Terraform, umbrella charts, the build-env image tag); defaults to the runtime repo path. Required when that default is not resolvable -- there is no cwd fallback here"`
	Version     string `json:"version,omitempty" jsonschema:"erun version to pin every reference to; omit to pin to the latest published stable release"`
	Revert      bool   `json:"revert,omitempty" jsonschema:"when true, pin back to the version recorded before the last re-pin"`
	Preview     bool   `json:"preview,omitempty" jsonschema:"when true, resolve and return the full plan (every site, old and new) without writing anything"`
	Verbosity   int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
	shared.JobEnvelopeInput
}

func PinTool(runtime shared.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, PinInput) (*mcp.CallToolResult, shared.JobEnvelopeOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input PinInput) (*mcp.CallToolResult, shared.JobEnvelopeOutput, error) {
		execute := func(preview bool, log io.Writer) (shared.CommandOutput, error) {
			var pinned *shared.PinOutput
			output, err := shared.RunRuntimeCommand(runtime, preview, input.Verbosity, log, func(runCtx core.Context, _ string) error {
				out, err := runPinToolCommand(runtime, input, runCtx)
				if err != nil {
					return err
				}
				pinned = out
				return nil
			})
			if err == nil && pinned != nil {
				output.Pin = pinned
			}
			return output, err
		}
		envelope, err := shared.RunJobEnvelope(runtime, "pin", input.JobEnvelopeInput, input.Preview, execute)
		return nil, envelope, err
	}
}

func runPinToolCommand(runtime shared.RuntimeConfig, input PinInput, runCtx core.Context) (*shared.PinOutput, error) {
	tenant, environment, err := shared.ResolveLocalTarget(runtime, input.Tenant, input.Environment)
	if err != nil {
		return nil, err
	}
	projectRoot, err := resolvePinToolProjectRoot(runtime, input)
	if err != nil {
		return nil, err
	}
	envConfig, _, err := runtime.Store.LoadEnvConfig(tenant, environment)
	if err != nil {
		return nil, err
	}
	// Background, not the request context: an async pin outlives the call.
	target, err := resolvePinToolTarget(context.Background(), runtime, input, projectRoot, tenant, environment)
	if err != nil {
		return nil, err
	}
	plan, err := lifecycle.ResolvePinPlan(projectRoot, tenant, environment, envConfig, target)
	if err != nil {
		return nil, err
	}
	lifecycle.AnnotatePinPlanBaseFreshness(runCtx, &plan)
	if note := plan.BaseFreshnessNote(); note != "" {
		runCtx.Trace("pin " + note)
	}
	for _, site := range plan.Sites {
		runCtx.Trace(fmt.Sprintf("pin %s %s: %s -> %s", site.Kind, site.Path+site.Detail, site.Current, site.Target))
	}
	for _, note := range plan.Skipped {
		runCtx.Trace("pin skipped: " + note)
	}
	out, err := applyPinPlanUnlessPreviewing(runtime, runCtx, plan, projectRoot, tenant, environment, envConfig)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// applyPinPlanUnlessPreviewing records the previous version first so a revert
// works even after a partial rewrite.
func applyPinPlanUnlessPreviewing(runtime shared.RuntimeConfig, runCtx core.Context, plan model.PinPlan, workDir, tenant, environment string, envConfig config.EnvConfig) (shared.PinOutput, error) {
	out := shared.PinOutput{Plan: plan, Changed: len(plan.Changes())}
	if runCtx.DryRun || plan.Aligned() {
		return out, nil
	}
	if err := lifecycle.RecordPinPrevious(workDir, tenant, environment, plan.Previous); err != nil {
		return shared.PinOutput{}, err
	}
	if err := lifecycle.ApplyPinPlan(plan); err != nil {
		return shared.PinOutput{}, err
	}
	// Runtime version/image/chart live in the config store, not the repo.
	if updated, changed := lifecycle.ApplyPinnedEnvConfig(envConfig, plan); changed {
		if err := runtime.Store.SaveEnvConfig(tenant, updated); err != nil {
			return shared.PinOutput{}, err
		}
	}
	// deploy refuses a Chart.lock that disagrees with its Chart.yaml.
	if err := lifecycle.RefreshPinnedChartLocks(runCtx, plan, nil); err != nil {
		return shared.PinOutput{}, err
	}
	out.Applied = true
	return out, nil
}

// resolvePinToolProjectRoot never falls back to the working directory, which is
// not the tenant repo for an MCP caller.
func resolvePinToolProjectRoot(runtime shared.RuntimeConfig, input PinInput) (string, error) {
	projectRoot := shared.FirstNonEmpty(strings.TrimSpace(input.ProjectRoot), strings.TrimSpace(runtime.Context.RepoPath))
	if projectRoot == "" {
		return "", fmt.Errorf("no project root to pin: this MCP server has no repo path bound to it, and the call did not supply projectRoot either -- pass projectRoot explicitly, naming the checkout pin should rewrite")
	}
	return projectRoot, nil
}

// resolvePinToolTarget refuses an unpublished version, which would only fail
// later at terraform init or chart pull.
func resolvePinToolTarget(ctx context.Context, runtime shared.RuntimeConfig, input PinInput, workDir, tenant, environment string) (string, error) {
	if input.Revert {
		previous, ok := lifecycle.PinPrevious(workDir, tenant, environment)
		if !ok {
			return "", fmt.Errorf("no previous pin is recorded for %s/%s, so there is nothing to revert to", tenant, environment)
		}
		return previous, nil
	}
	versions, err := runtime.ResolveRuntimeRegistryVersions(ctx)
	if err != nil {
		return "", err
	}
	explicit := strings.TrimSpace(input.Version)
	if explicit == "" {
		if strings.TrimSpace(versions.LatestStable) == "" {
			return "", fmt.Errorf("no published stable erun release was found to pin to")
		}
		return versions.LatestStable, nil
	}
	// An empty listing means the registry was unreadable, not that the version is absent.
	if len(versions.Tags) > 0 {
		wanted := strings.TrimPrefix(explicit, "v")
		for _, tag := range versions.Tags {
			if strings.TrimPrefix(strings.TrimSpace(tag), "v") == wanted {
				return explicit, nil
			}
		}
		return "", fmt.Errorf("erun version %s is not published in %s — pin to a released version", explicit, versions.Image)
	}
	return explicit, nil
}

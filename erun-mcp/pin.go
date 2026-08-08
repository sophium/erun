package erunmcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

// PinInput selects which erun version an environment's references move to.
type PinInput struct {
	Version   string `json:"version,omitempty" jsonschema:"erun version to pin every reference to; omit to pin to the latest published stable release"`
	Revert    bool   `json:"revert,omitempty" jsonschema:"when true, pin back to the version recorded before the last re-pin"`
	Preview   bool   `json:"preview,omitempty" jsonschema:"when true, resolve and return the full plan (every site, old and new) without writing anything"`
	Verbosity int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
}

// PinOutput is the resolved plan, so a caller sees exactly which references
// moved rather than having to diff the tree afterwards.
type PinOutput struct {
	Plan    eruncommon.PinPlan `json:"plan"`
	Changed int                `json:"changed"`
	Applied bool               `json:"applied"`
}

func pinTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, PinInput) (*mcp.CallToolResult, CommandOutput, error) {
	return func(callCtx context.Context, _ *mcp.CallToolRequest, input PinInput) (*mcp.CallToolResult, CommandOutput, error) {
		var pinned *PinOutput
		output, err := runRuntimeCommand(runtime, input.Preview, input.Verbosity, func(runCtx eruncommon.Context, workDir string) error {
			envConfig, _, err := runtime.Store.LoadEnvConfig(runtime.Context.Tenant, runtime.Context.Environment)
			if err != nil {
				return err
			}
			target, err := resolvePinToolTarget(callCtx, input, workDir, runtime.Context.Tenant, runtime.Context.Environment)
			if err != nil {
				return err
			}
			plan, err := eruncommon.ResolvePinPlan(workDir, runtime.Context.Tenant, runtime.Context.Environment, envConfig, target)
			if err != nil {
				return err
			}
			for _, site := range plan.Sites {
				runCtx.Trace(fmt.Sprintf("pin %s %s: %s -> %s", site.Kind, site.Path+site.Detail, site.Current, site.Target))
			}
			out, err := applyPinPlanUnlessPreviewing(runCtx, plan, workDir, runtime.Context.Tenant, runtime.Context.Environment)
			if err != nil {
				return err
			}
			pinned = &out
			return nil
		})
		if err == nil && pinned != nil {
			output.Pin = pinned
		}
		return nil, output, err
	}
}

// applyPinPlanUnlessPreviewing writes the plan unless this is a preview or the
// tree is already aligned. The previous version is recorded before anything
// moves, so a revert has somewhere to go even if the rewrite fails partway.
func applyPinPlanUnlessPreviewing(runCtx eruncommon.Context, plan eruncommon.PinPlan, workDir, tenant, environment string) (PinOutput, error) {
	out := PinOutput{Plan: plan, Changed: len(plan.Changes())}
	if runCtx.DryRun || plan.Aligned() {
		return out, nil
	}
	if err := eruncommon.RecordPinPrevious(workDir, tenant, environment, plan.Previous); err != nil {
		return PinOutput{}, err
	}
	if err := eruncommon.ApplyPinPlan(plan); err != nil {
		return PinOutput{}, err
	}
	out.Applied = true
	return out, nil
}

// resolvePinToolTarget answers which version to move to, and refuses one that is
// not published — a pin to an unpublished version only fails much later, at a
// terraform init or a chart pull, far from the thing that caused it.
func resolvePinToolTarget(ctx context.Context, input PinInput, workDir, tenant, environment string) (string, error) {
	if input.Revert {
		previous, ok := eruncommon.PinPrevious(workDir, tenant, environment)
		if !ok {
			return "", fmt.Errorf("no previous pin is recorded for %s/%s, so there is nothing to revert to", tenant, environment)
		}
		return previous, nil
	}
	versions, err := eruncommon.ResolveDefaultRuntimeRegistryVersions(ctx)
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
	// An empty listing means the registry could not be read, which is not the
	// same as the version being absent.
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

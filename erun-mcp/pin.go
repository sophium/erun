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
	Tenant      string `json:"tenant,omitempty" jsonschema:"tenant whose environment to re-pin; defaults to the MCP runtime context tenant"`
	Environment string `json:"environment,omitempty" jsonschema:"environment to re-pin; defaults to the MCP runtime context environment"`
	// ProjectRoot names the checkout to rewrite. A human at a shell has a cwd
	// the skill's own guidance can point at ("run from inside the tenant
	// repository"); an MCP caller has no cwd to be inside of, so this is its
	// only way to say which repository it means when the server's own bound
	// repo path is missing or wrong.
	ProjectRoot string `json:"projectRoot,omitempty" jsonschema:"repo root holding the sites to re-pin (Terraform, umbrella charts, the build-env image tag); defaults to the runtime repo path. Required when that default is not resolvable -- there is no cwd fallback here"`
	Version     string `json:"version,omitempty" jsonschema:"erun version to pin every reference to; omit to pin to the latest published stable release"`
	Revert      bool   `json:"revert,omitempty" jsonschema:"when true, pin back to the version recorded before the last re-pin"`
	Preview     bool   `json:"preview,omitempty" jsonschema:"when true, resolve and return the full plan (every site, old and new) without writing anything"`
	Verbosity   int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
	Wait        *bool  `json:"wait,omitempty" jsonschema:"when true (the default this release), run synchronously and return the full result inline, exactly as before this input existed. Set false to start the work as a background job and get back {jobId, state: running} immediately instead -- poll exec_job_status/exec_job_await/exec_job_output for the outcome. This default flips to false in a future release, with true kept callable for one more release as the compatibility switch"`
}

// PinOutput is the resolved plan, so a caller sees exactly which references
// moved rather than having to diff the tree afterwards.
type PinOutput struct {
	Plan    eruncommon.PinPlan `json:"plan"`
	Changed int                `json:"changed"`
	Applied bool               `json:"applied"`
}

func pinTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, PinInput) (*mcp.CallToolResult, JobEnvelopeOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input PinInput) (*mcp.CallToolResult, JobEnvelopeOutput, error) {
		execute := func(preview bool) (CommandOutput, error) {
			var pinned *PinOutput
			output, err := runRuntimeCommand(runtime, preview, input.Verbosity, func(runCtx eruncommon.Context, _ string) error {
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
		envelope, err := runJobEnvelope(runtime, "pin", input.Wait, input.Preview, execute)
		return nil, envelope, err
	}
}

// runPinToolCommand resolves the plan and applies it unless previewing. Split
// out of pinTool's execute closure so the tenant/environment/root resolution,
// the plan build, and the apply each read as one step rather than piling into
// a single over-complex function.
func runPinToolCommand(runtime RuntimeConfig, input PinInput, runCtx eruncommon.Context) (*PinOutput, error) {
	tenant, environment, err := resolveLocalTarget(runtime, input.Tenant, input.Environment)
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
	// Not the MCP call's own context: an async pin runs in a background task
	// job that outlives this call, so tying the registry lookup to a context
	// the request already cancelled would fail it every time.
	target, err := resolvePinToolTarget(context.Background(), input, projectRoot, tenant, environment)
	if err != nil {
		return nil, err
	}
	plan, err := eruncommon.ResolvePinPlan(projectRoot, tenant, environment, envConfig, target)
	if err != nil {
		return nil, err
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

// applyPinPlanUnlessPreviewing writes the plan unless this is a preview or the
// tree is already aligned. The previous version is recorded before anything
// moves, so a revert has somewhere to go even if the rewrite fails partway.
func applyPinPlanUnlessPreviewing(runtime RuntimeConfig, runCtx eruncommon.Context, plan eruncommon.PinPlan, workDir, tenant, environment string, envConfig eruncommon.EnvConfig) (PinOutput, error) {
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
	// The CLI transport's own runtimeversion/runtimeimage/runtimechart sites
	// live in erun's config store, not a repo file, so ApplyPinPlan never
	// touches them -- this half was missing here entirely, leaving even
	// runtimeversion unmoved by an MCP-driven re-pin.
	if updated, changed := eruncommon.ApplyPinnedEnvConfig(envConfig, plan); changed {
		if err := runtime.Store.SaveEnvConfig(tenant, updated); err != nil {
			return PinOutput{}, err
		}
	}
	// The lock beside a rewritten Chart.yaml still names the old versions, and
	// deploy refuses a lock that disagrees with its chart — so an agent that
	// re-pinned and stopped here would have left a tree deploy cannot use.
	if err := eruncommon.RefreshPinnedChartLocks(runCtx, plan, nil); err != nil {
		return PinOutput{}, err
	}
	out.Applied = true
	return out, nil
}

// resolvePinToolProjectRoot answers which checkout pin rewrites. Unlike the CLI
// (resolvePinProjectRoot, erun-cli/cmd/pin.go), this never falls back to a
// working directory: the CLI's cwd fallback stands in for a human who ran the
// command from inside the tenant repo, but an MCP caller has no cwd to be
// inside of, so a directory this process happens to be running in (its own
// cwd, a pod home directory) is never an acceptable stand-in for "the tenant's
// repo" — a missing root refuses instead of widening to whatever is there.
func resolvePinToolProjectRoot(runtime RuntimeConfig, input PinInput) (string, error) {
	projectRoot := firstNonEmpty(strings.TrimSpace(input.ProjectRoot), strings.TrimSpace(runtime.Context.RepoPath))
	if projectRoot == "" {
		return "", fmt.Errorf("no project root to pin: this MCP server has no repo path bound to it, and the call did not supply projectRoot either -- pass projectRoot explicitly, naming the checkout pin should rewrite")
	}
	return projectRoot, nil
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

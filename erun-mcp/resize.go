package erunmcp

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

// ResizeInput mirrors the CLI's `erun resize` flags. tenant/environment must
// name this server's own environment (see resolveLocalTarget) — an MCP server
// runs in exactly one pod, so it can only ever roll that pod.
type ResizeInput struct {
	Tenant      string `json:"tenant,omitempty" jsonschema:"tenant whose environment to resize; defaults to the server tenant context, and must match it: this server only acts on its own environment"`
	Environment string `json:"environment,omitempty" jsonschema:"environment to resize; defaults to the server environment context, and must match it: this server only acts on its own environment"`
	CPU         string `json:"cpu,omitempty" jsonschema:"explicit CPU limit (Kubernetes quantity, e.g. 6); omit to leave CPU unchanged unless applyRecommendation is set"`
	Memory      string `json:"memory,omitempty" jsonschema:"explicit memory limit (Kubernetes quantity, e.g. 12Gi); omit to leave memory unchanged unless applyRecommendation is set"`
	// ApplyRecommendation reads the standing recommendation from this
	// environment's own retained usage history -- the recommendation is only
	// readable in-pod, which is exactly where this tool runs, unlike a
	// host-side CLI invocation.
	ApplyRecommendation bool `json:"applyRecommendation,omitempty" jsonschema:"size from this environment's own standing sizing recommendation instead of cpu/memory -- resolved from usage history retained in this pod, so the value is never retyped by the caller"`
	// OverrideLease and Orchestrator mirror the activity-lease tools: a
	// resize rolls the runtime pod (Recreate strategy), which would kill any
	// live session inside it, so it is refused, naming every holder, unless
	// explicitly overridden.
	OverrideLease bool   `json:"overrideLease,omitempty" jsonschema:"roll the runtime pod even though the environment is currently held by another worker (an agent job, a build, another orchestrator); required to proceed when the environment is not idle"`
	Orchestrator  string `json:"orchestrator,omitempty" jsonschema:"the calling orchestrator's own id (its $ERUN_ORCHESTRATOR_ID), recorded on the resize's own lease and on the override if one was needed"`
	Preview       bool   `json:"preview,omitempty" jsonschema:"when true, resolve and trace the planned resize (current -> target per resource, held leases, whether an override was needed) without changing anything"`
	Verbosity     int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
	Wait          *bool  `json:"wait,omitempty" jsonschema:"when true (the default this release), run synchronously and return the full result inline. Set false to start the work as a background job and get back {jobId, state: running} immediately instead -- poll exec_job_status/exec_job_await/exec_job_output for the outcome. This default flips to false in a future release, with true kept callable for one more release as the compatibility switch"`
}

func resizeTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ResizeInput) (*mcp.CallToolResult, JobEnvelopeOutput, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input ResizeInput) (*mcp.CallToolResult, JobEnvelopeOutput, error) {
		tenant, environment, err := resolveLocalTarget(runtime, input.Tenant, input.Environment)
		if err != nil {
			return nil, JobEnvelopeOutput{}, err
		}
		identity := authIdentityFrom(ctx)
		execute := simpleJobExecute(runtime, input.Verbosity, func(runCtx eruncommon.Context, workDir string) error {
			findProjectRoot := func() (string, string, error) {
				return runtimeFindProjectRoot(runtime.Context, workDir)
			}
			resolveBuildContext := func() (eruncommon.DockerBuildContext, error) {
				return eruncommon.DockerBuildContextAtDir(workDir)
			}
			resolveDeployContext := func() (eruncommon.KubernetesDeployContext, error) {
				return eruncommon.KubernetesDeployContextAtDir(workDir), nil
			}
			_, err := eruncommon.RunRuntimeResize(runCtx, eruncommon.RuntimeResizeDependencies{
				Store:                          runtime.Store,
				SaveEnvConfig:                  runtime.Store.SaveEnvConfig,
				FindProjectRoot:                findProjectRoot,
				ResolveDockerBuildContext:      resolveBuildContext,
				ResolveKubernetesDeployContext: resolveDeployContext,
				DeployHelmChart:                runtime.DeployHelmChart,
			}, eruncommon.RuntimeResizeParams{
				Tenant:      tenant,
				Environment: environment,
				Input: eruncommon.RuntimeResizeInput{
					CPU:                 strings.TrimSpace(input.CPU),
					Memory:              strings.TrimSpace(input.Memory),
					ApplyRecommendation: input.ApplyRecommendation,
				},
				OverrideLease: input.OverrideLease,
				Holder: eruncommon.EnvironmentActivityLeaseHolder{
					Orchestrator: input.Orchestrator,
					Tenant:       identity.Tenant,
					User:         identity.User,
				},
			})
			return err
		})
		envelope, err := runJobEnvelope(runtime, "resize", input.Wait, input.Preview, execute)
		return nil, envelope, err
	}
}

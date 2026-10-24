package resize

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/lifecycle"
	"github.com/sophium/erun/erun-common/primitive/deploy"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerspec"
	"github.com/sophium/erun/erun-mcp/tools/shared/callerid"
	"github.com/sophium/erun/erun-mcp/tools/shared/jobenvelope"
	"github.com/sophium/erun/erun-mcp/tools/shared/runcmd"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
	"github.com/sophium/erun/erun-mcp/tools/shared/toolio"
)

// ResizeInput's tenant/environment must name this server's own environment.
type ResizeInput struct {
	Tenant      string `json:"tenant,omitempty" jsonschema:"tenant whose environment to resize; defaults to the server tenant context, and must match it: this server only acts on its own environment"`
	Environment string `json:"environment,omitempty" jsonschema:"environment to resize; defaults to the server environment context, and must match it: this server only acts on its own environment"`
	CPU         string `json:"cpu,omitempty" jsonschema:"explicit CPU limit for the runtime pod (Kubernetes quantity, e.g. 6); omit to leave CPU unchanged unless applyRecommendation is set"`
	Memory      string `json:"memory,omitempty" jsonschema:"explicit memory limit for the runtime pod (Kubernetes quantity, e.g. 12Gi); omit to leave memory unchanged unless applyRecommendation is set"`
	DindCPU     string `json:"dindCpu,omitempty" jsonschema:"explicit CPU limit for the erun-dind sidecar (Kubernetes quantity, e.g. 6); omit to leave it unchanged"`
	DindMemory  string `json:"dindMemory,omitempty" jsonschema:"explicit memory limit for the erun-dind sidecar (Kubernetes quantity, e.g. 16Gi); omit to leave it unchanged. Raise this when a multi-arch erun release/erun build --release OOMs inside the sidecar"`
	// ApplyRecommendation sizes only the runtime pod, from in-pod usage history.
	ApplyRecommendation bool `json:"applyRecommendation,omitempty" jsonschema:"size the runtime pod from this environment's own standing sizing recommendation instead of cpu/memory -- resolved from usage history retained in this pod, so the value is never retyped by the caller; never sizes the erun-dind sidecar"`
	// A resize recreates the pod, so active leases refuse it unless overridden.
	OverrideLease bool   `json:"overrideLease,omitempty" jsonschema:"roll the runtime pod even though the environment is currently held by another worker (an agent job, a build, another orchestrator); required to proceed when the environment is not idle"`
	Orchestrator  string `json:"orchestrator,omitempty" jsonschema:"the calling orchestrator's own id (its $ERUN_ORCHESTRATOR_ID), recorded on the resize's own lease and on the override if one was needed"`
	Preview       bool   `json:"preview,omitempty" jsonschema:"when true, resolve and trace the planned resize (current -> target per resource, held leases, whether an override was needed) without changing anything"`
	Verbosity     int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
	jobenvelope.JobEnvelopeInput
}

func ResizeTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ResizeInput) (*mcp.CallToolResult, jobenvelope.JobEnvelopeOutput, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input ResizeInput) (*mcp.CallToolResult, jobenvelope.JobEnvelopeOutput, error) {
		tenant, environment, err := runtimecfg.ResolveLocalTarget(runtime, input.Tenant, input.Environment)
		if err != nil {
			return nil, jobenvelope.JobEnvelopeOutput{}, err
		}
		identity := callerid.AuthIdentityFrom(ctx)
		execute := runcmd.SimpleJobExecute(runtime, input.Verbosity, func(runCtx core.Context, workDir string) error {
			findProjectRoot := func() (string, string, error) {
				return toolio.RuntimeFindProjectRoot(runtime.Context, workDir)
			}
			resolveBuildContext := func() (model.DockerBuildContext, error) {
				return dockerspec.DockerBuildContextAtDir(workDir)
			}
			resolveDeployContext := func() (model.KubernetesDeployContext, error) {
				return deploy.KubernetesDeployContextAtDir(workDir), nil
			}
			_, err := lifecycle.RunRuntimeResize(runCtx, lifecycle.RuntimeResizeDependencies{
				Store:                          runtime.Store,
				SaveEnvConfig:                  runtime.Store.SaveEnvConfig,
				FindProjectRoot:                findProjectRoot,
				ResolveDockerBuildContext:      resolveBuildContext,
				ResolveKubernetesDeployContext: resolveDeployContext,
				DeployHelmChart:                runtime.DeployHelmChart,
			}, model.RuntimeResizeParams{
				Tenant:      tenant,
				Environment: environment,
				Input: model.RuntimeResizeInput{
					CPU:                 strings.TrimSpace(input.CPU),
					Memory:              strings.TrimSpace(input.Memory),
					DindCPU:             strings.TrimSpace(input.DindCPU),
					DindMemory:          strings.TrimSpace(input.DindMemory),
					ApplyRecommendation: input.ApplyRecommendation,
				},
				OverrideLease: input.OverrideLease,
				Holder: model.EnvironmentActivityLeaseHolder{
					Orchestrator: input.Orchestrator,
					Tenant:       identity.Tenant,
					User:         identity.User,
				},
			})
			return err
		})
		envelope, err := jobenvelope.RunJobEnvelope(runtime, "resize", input.JobEnvelopeInput, input.Preview, execute)
		return nil, envelope, err
	}
}

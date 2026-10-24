package e2e

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/e2e"
	"github.com/sophium/erun/erun-mcp/tools/shared/jobenvelope"
	"github.com/sophium/erun/erun-mcp/tools/shared/runcmd"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
)

type E2EInput struct {
	Component string `json:"component,omitempty" jsonschema:"run only the named suite (a directory under <tenant>-devops/e2e/); omitted runs every suite"`
	BaseURL   string `json:"baseUrl,omitempty" jsonschema:"run the suites against this http(s) URL instead of the exposed service's, skipping the exposure and certificate checks; the suites expect this environment's deployed version unless version is given"`
	Version   string `json:"version,omitempty" jsonschema:"version the suites expect (ERUN_E2E_VERSION) instead of this environment's deployed version; with baseUrl too, the environment is not read at all"`
	Preview   bool   `json:"preview,omitempty" jsonschema:"when true, resolve and trace the discovered suites, URLs, deployed version, and exact docker invocations without running them"`
	Verbosity int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
	jobenvelope.JobEnvelopeInput
}

// E2eTool runs each <tenant>-devops/e2e/<component> suite against this server's
// own environment; no suites is a no-op.
func E2eTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, E2EInput) (*mcp.CallToolResult, jobenvelope.JobEnvelopeOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input E2EInput) (*mcp.CallToolResult, jobenvelope.JobEnvelopeOutput, error) {
		component := strings.TrimSpace(input.Component)
		baseURL := strings.TrimSpace(input.BaseURL)
		version := strings.TrimSpace(input.Version)
		execute := runcmd.SimpleJobExecute(runtime, input.Verbosity, func(runCtx core.Context, workDir string) error {
			runCtx.MCPTool = "e2e"
			plan, err := e2e.ResolveE2EPlan(config.FindProjectRoot, component)
			if err != nil {
				return err
			}
			if len(plan.Suites) == 0 {
				runCtx.Info("e2e: no e2e/<component>/Dockerfile suite found; nothing to run")
				return nil
			}
			_, err = e2e.RunE2E(runCtx, model.E2EParams{
				Plan: plan,
				Target: model.E2ETarget{
					Tenant:            runtime.Context.Tenant,
					Environment:       runtime.Context.Environment,
					Namespace:         config.KubernetesNamespaceName(runtime.Context.Tenant, runtime.Context.Environment),
					KubernetesContext: strings.TrimSpace(runtime.Context.KubernetesContext),
				},
				BaseURL:     baseURL,
				Version:     version,
				TargetNamed: true,
			})
			return err
		})
		envelope, err := jobenvelope.RunJobEnvelope(runtime, "e2e", input.JobEnvelopeInput, input.Preview, execute)
		return nil, envelope, err
	}
}

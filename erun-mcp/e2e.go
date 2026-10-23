package erunmcp

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
)

type E2EInput struct {
	Component string `json:"component,omitempty" jsonschema:"run only the named suite (a directory under <tenant>-devops/e2e/); omitted runs every suite"`
	BaseURL   string `json:"baseUrl,omitempty" jsonschema:"run the suites against this http(s) URL instead of the exposed service's, skipping the exposure and certificate checks; the suites expect this environment's deployed version unless version is given"`
	Version   string `json:"version,omitempty" jsonschema:"version the suites expect (ERUN_E2E_VERSION) instead of this environment's deployed version; with baseUrl too, the environment is not read at all"`
	Preview   bool   `json:"preview,omitempty" jsonschema:"when true, resolve and trace the discovered suites, URLs, deployed version, and exact docker invocations without running them"`
	Verbosity int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
	JobEnvelopeInput
}

// e2eTool discovers this project's <tenant>-devops/e2e/<component>/Dockerfile
// suites the way build discovers docker/ and deploy discovers k8s/, then runs
// each one's `e2e` stage against this server's own resolved tenant/environment
// -- never a different one, the same same-environment constraint every other
// typed tool in this module applies. A project with no e2e/ suite is a clean
// no-op.
func e2eTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, E2EInput) (*mcp.CallToolResult, JobEnvelopeOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input E2EInput) (*mcp.CallToolResult, JobEnvelopeOutput, error) {
		component := strings.TrimSpace(input.Component)
		baseURL := strings.TrimSpace(input.BaseURL)
		version := strings.TrimSpace(input.Version)
		execute := simpleJobExecute(runtime, input.Verbosity, func(runCtx core.Context, workDir string) error {
			runCtx.MCPTool = "e2e"
			plan, err := eruncommon.ResolveE2EPlan(config.FindProjectRoot, component)
			if err != nil {
				return err
			}
			if len(plan.Suites) == 0 {
				runCtx.Info("e2e: no e2e/<component>/Dockerfile suite found; nothing to run")
				return nil
			}
			_, err = eruncommon.RunE2E(runCtx, eruncommon.E2EParams{
				Plan: plan,
				Target: eruncommon.E2ETarget{
					Tenant:            runtime.Context.Tenant,
					Environment:       runtime.Context.Environment,
					Namespace:         eruncommon.KubernetesNamespaceName(runtime.Context.Tenant, runtime.Context.Environment),
					KubernetesContext: strings.TrimSpace(runtime.Context.KubernetesContext),
				},
				BaseURL: baseURL,
				Version: version,
				// This server's own environment is always named: it is the
				// one environment every typed tool here targets.
				TargetNamed: true,
			})
			return err
		})
		envelope, err := runJobEnvelope(runtime, "e2e", input.JobEnvelopeInput, input.Preview, execute)
		return nil, envelope, err
	}
}

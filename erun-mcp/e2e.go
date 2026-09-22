package erunmcp

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

type E2EInput struct {
	Component string `json:"component,omitempty" jsonschema:"run the named component's suite when playwright/ has more than one"`
	Preview   bool   `json:"preview,omitempty" jsonschema:"when true, resolve and trace the discovered suite, base URL, and deployed version without running Playwright"`
	Verbosity int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
	JobEnvelopeInput
}

// e2eTool discovers this project's playwright/ folder the way build discovers
// docker/ and deploy discovers k8s/, then runs it once against this server's
// own resolved tenant/environment -- never a different one, the same
// same-environment constraint every other typed tool in this module applies.
// A project with no playwright/ folder is a clean no-op.
func e2eTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, E2EInput) (*mcp.CallToolResult, JobEnvelopeOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input E2EInput) (*mcp.CallToolResult, JobEnvelopeOutput, error) {
		component := strings.TrimSpace(input.Component)
		execute := simpleJobExecute(runtime, input.Verbosity, func(runCtx eruncommon.Context, workDir string) error {
			runCtx.MCPTool = "e2e"
			suite, err := eruncommon.ResolveCurrentPlaywrightSuite(eruncommon.FindProjectRoot, component)
			if err != nil {
				return err
			}
			if suite == nil {
				runCtx.Info("e2e: no playwright/ suite found; nothing to run")
				return nil
			}
			target := eruncommon.E2ETarget{
				Component:         component,
				Tenant:            runtime.Context.Tenant,
				Environment:       runtime.Context.Environment,
				Namespace:         eruncommon.KubernetesNamespaceName(runtime.Context.Tenant, runtime.Context.Environment),
				KubernetesContext: strings.TrimSpace(runtime.Context.KubernetesContext),
			}
			_, err = eruncommon.RunE2E(runCtx, *suite, target, nil)
			return err
		})
		envelope, err := runJobEnvelope(runtime, "e2e", input.JobEnvelopeInput, input.Preview, execute)
		return nil, envelope, err
	}
}

package erunmcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

type DeleteInput struct {
	Tenant       string `json:"tenant,omitempty" jsonschema:"tenant name for the environment to delete; defaults to the MCP runtime context tenant"`
	Environment  string `json:"environment,omitempty" jsonschema:"environment name to delete; defaults to the MCP runtime context environment"`
	Confirmation string `json:"confirmation,omitempty" jsonschema:"must exactly match tenant-environment when preview is false"`
	Preview      bool   `json:"preview,omitempty" jsonschema:"when true, resolve and print the planned actions without executing them"`
	Verbosity    int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
	Wait         *bool  `json:"wait,omitempty" jsonschema:"when true (the default this release), run synchronously and return the full result inline, exactly as before this input existed. Set false to start the work as a background job and get back {jobId, state: running} immediately instead -- poll exec_job_status/exec_job_await/exec_job_output for the outcome. This default flips to false in a future release, with true kept callable for one more release as the compatibility switch"`
}

func deleteTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, DeleteInput) (*mcp.CallToolResult, JobEnvelopeOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input DeleteInput) (*mcp.CallToolResult, JobEnvelopeOutput, error) {
		tenant, environment, err := scopedTenantEnv(input.Tenant, input.Environment, runtime)
		if err != nil {
			return nil, JobEnvelopeOutput{}, err
		}
		expected := eruncommon.DeleteEnvironmentConfirmation(tenant, environment)
		if expected == "" {
			return nil, JobEnvelopeOutput{}, fmt.Errorf(
				"tenant/environment not resolved: this MCP server was not started bound to a tenant/environment, and the call did not supply them either -- pass tenant and environment explicitly in the call, or run this edge for an environment that has them configured",
			)
		}
		if !input.Preview && strings.TrimSpace(input.Confirmation) != expected {
			return nil, JobEnvelopeOutput{}, fmt.Errorf("delete confirmation must match %q", expected)
		}

		deleteStore, ok := any(runtime.Store).(eruncommon.DeleteStore)
		if !ok {
			deleteStore = eruncommon.ConfigStore{}
		}

		execute := simpleJobExecute(runtime, input.Verbosity, func(runCtx eruncommon.Context, _ string) error {
			result, err := eruncommon.RunDeleteEnvironment(runCtx, eruncommon.DeleteEnvironmentParams{
				Tenant:      tenant,
				Environment: environment,
			}, deleteStore, runtime.DeleteKubernetesNamespace)
			if err != nil {
				return err
			}
			if result.NamespaceDeleteError != "" {
				_, _ = fmt.Fprintf(runCtx.Stderr, "warning: failed to delete namespace %q in context %q: %s\n", result.Namespace, result.KubernetesContext, result.NamespaceDeleteError)
			}
			return nil
		})
		envelope, err := runJobEnvelope(runtime, "delete", input.Wait, input.Preview, execute)
		return nil, envelope, err
	}
}

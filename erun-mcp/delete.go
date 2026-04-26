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
}

func deleteTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, DeleteInput) (*mcp.CallToolResult, CommandOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input DeleteInput) (*mcp.CallToolResult, CommandOutput, error) {
		tenant := firstNonEmpty(input.Tenant, runtime.Context.Tenant)
		environment := firstNonEmpty(input.Environment, runtime.Context.Environment)
		expected := eruncommon.DeleteEnvironmentConfirmation(tenant, environment)
		if expected == "" {
			return nil, CommandOutput{}, fmt.Errorf("tenant and environment are required")
		}
		if !input.Preview && strings.TrimSpace(input.Confirmation) != expected {
			return nil, CommandOutput{}, fmt.Errorf("delete confirmation must match %q", expected)
		}

		deleteStore, ok := any(runtime.Store).(eruncommon.DeleteStore)
		if !ok {
			deleteStore = eruncommon.ConfigStore{}
		}

		output, err := runRuntimeCommand(runtime.Context, input.Preview, input.Verbosity, func(runCtx eruncommon.Context, _ string) error {
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
		return nil, output, err
	}
}

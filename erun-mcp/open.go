package erunmcp

import (
	"bytes"
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

type OpenOutput struct {
	Tenant            string   `json:"tenant"`
	Environment       string   `json:"environment"`
	RepoPath          string   `json:"repoPath"`
	KubernetesContext string   `json:"kubernetesContext"`
	Namespace         string   `json:"namespace"`
	LocalShellSetup   string   `json:"localShellSetup,omitempty"`
	Trace             []string `json:"trace,omitempty"`
}

type OpenInput struct {
	Verbosity int `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
}

func openTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, OpenInput) (*mcp.CallToolResult, OpenOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input OpenInput) (*mcp.CallToolResult, OpenOutput, error) {
		traceOutput := new(bytes.Buffer)
		ctx := runtimeCallContext(false, input.Verbosity, nil, traceOutput, traceOutput)

		result, err := resolveRuntimeOpenResult(runtime)
		if err != nil {
			return nil, OpenOutput{}, err
		}

		namespace := strings.TrimSpace(runtime.Context.Namespace)
		if namespace == "" {
			namespace = eruncommon.KubernetesNamespaceName(result.Tenant, result.Environment)
		}

		output := OpenOutput{
			Tenant:            result.Tenant,
			Environment:       result.Environment,
			RepoPath:          result.RepoPath,
			KubernetesContext: result.EnvConfig.KubernetesContext,
			Namespace:         namespace,
			LocalShellSetup:   eruncommon.LocalShellSetupScript(result),
		}
		ctx.TraceCommand("", "kubectl", "config", "use-context", strings.TrimSpace(result.EnvConfig.KubernetesContext))
		ctx.TraceCommand("", "kubectl", "config", "set-context", "--current", "--namespace="+namespace)
		ctx.TraceCommand("", "cd", result.RepoPath)
		output.Trace = normalizeTraceLines(traceOutput.String())
		return nil, output, nil
	}
}

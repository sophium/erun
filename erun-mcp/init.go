package erunmcp

import (
	"bytes"
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

type InitInput struct {
	Tenant                   string `json:"tenant,omitempty" jsonschema:"optional tenant name to initialize"`
	SelectedTenant           string `json:"selectedTenant,omitempty" jsonschema:"selected tenant name returned by a prior init interaction event"`
	InitializeCurrentProject bool   `json:"initializeCurrentProject,omitempty" jsonschema:"when true, answer the tenant selection interaction by choosing the current project"`
	ProjectRoot              string `json:"projectRoot,omitempty" jsonschema:"optional project root to bind to the tenant"`
	Environment              string `json:"environment,omitempty" jsonschema:"optional environment name to initialize"`
	KubernetesContext        string `json:"kubernetesContext,omitempty" jsonschema:"optional kubernetes context to associate with the environment"`
	ContainerRegistry        string `json:"containerRegistry,omitempty" jsonschema:"optional container registry to associate with the environment"`
	ConfirmTenant            *bool  `json:"confirmTenant,omitempty" jsonschema:"response to a prior tenant confirmation interaction"`
	ConfirmEnvironment       *bool  `json:"confirmEnvironment,omitempty" jsonschema:"response to a prior environment confirmation interaction"`
	AutoApprove              bool   `json:"autoApprove,omitempty" jsonschema:"when true, automatically approve initialization prompts"`
	Preview                  bool   `json:"preview,omitempty" jsonschema:"when true, resolve and print the planned actions without executing them"`
	Verbosity                int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
}

type InitOutput struct {
	CommandOutput
	Interaction *eruncommon.BootstrapInitInteraction `json:"interaction,omitempty"`
}

func initTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, InitInput) (*mcp.CallToolResult, InitOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input InitInput) (*mcp.CallToolResult, InitOutput, error) {
		workDir, err := runtimeRepoPath(runtime.Context)
		if err != nil {
			return nil, InitOutput{}, err
		}

		traceOutput := new(bytes.Buffer)
		ctx := runtimeCallContext(input.Preview, input.Verbosity, nil, traceOutput, traceOutput)

		params := eruncommon.BootstrapInitParams{
			Tenant:                   firstNonEmpty(strings.TrimSpace(input.Tenant), strings.TrimSpace(runtime.Context.Tenant)),
			SelectedTenant:           strings.TrimSpace(input.SelectedTenant),
			InitializeCurrentProject: input.InitializeCurrentProject,
			ProjectRoot:              firstNonEmpty(strings.TrimSpace(input.ProjectRoot), strings.TrimSpace(runtime.Context.RepoPath)),
			Environment:              firstNonEmpty(strings.TrimSpace(input.Environment), strings.TrimSpace(runtime.Context.Environment)),
			KubernetesContext:        firstNonEmpty(strings.TrimSpace(input.KubernetesContext), strings.TrimSpace(runtime.Context.KubernetesContext)),
			ContainerRegistry:        strings.TrimSpace(input.ContainerRegistry),
			ConfirmTenant:            input.ConfirmTenant,
			ConfirmEnvironment:       input.ConfirmEnvironment,
			AutoApprove:              input.AutoApprove,
		}

		_, err = eruncommon.RunBootstrapInit(
			ctx,
			params,
			eruncommon.TraceBootstrapStore(ctx, runtime.Store),
			func() (string, string, error) {
				return eruncommon.FindProjectRootFromDir(workDir)
			},
			func() (string, error) {
				return workDir, nil
			},
			nil,
			nil,
			nil,
			nil,
			eruncommon.TraceNamespaceEnsurer(ctx, runtime.EnsureKubernetesNamespace),
			eruncommon.LoadProjectConfig,
			eruncommon.TraceProjectConfigSaver(ctx, eruncommon.SaveProjectConfig),
		)
		if err != nil {
			if interaction, ok := eruncommon.AsBootstrapInitInteraction(err); ok {
				return nil, InitOutput{
					CommandOutput: CommandOutput{
						Executed:         false,
						WorkingDirectory: workDir,
						Trace:            normalizeTraceLines(traceOutput.String()),
					},
					Interaction: &interaction,
				}, nil
			}
			return nil, InitOutput{}, err
		}

		output := InitOutput{
			CommandOutput: CommandOutput{
				Executed:         !ctx.DryRun,
				WorkingDirectory: workDir,
				Trace:            normalizeTraceLines(traceOutput.String()),
			},
		}
		return nil, output, nil
	}
}

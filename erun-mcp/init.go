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
	Version                  string `json:"version,omitempty" jsonschema:"optional runtime image version to initialize and deploy"`
	KubernetesContext        string `json:"kubernetesContext,omitempty" jsonschema:"optional kubernetes context to associate with the environment"`
	ContainerRegistry        string `json:"containerRegistry,omitempty" jsonschema:"optional container registry to associate with the environment"`
	Remote                   bool   `json:"remote,omitempty" jsonschema:"when true, initialize the repository inside the runtime pod"`
	RemoteRepositoryURL      string `json:"remoteRepositoryURL,omitempty" jsonschema:"optional SSH repository URL used when creating the remote checkout"`
	ConfirmTenant            *bool  `json:"confirmTenant,omitempty" jsonschema:"response to a prior tenant confirmation interaction"`
	ConfirmEnvironment       *bool  `json:"confirmEnvironment,omitempty" jsonschema:"response to a prior environment confirmation interaction"`
	ConfirmRemoteKeyImport   *bool  `json:"confirmRemoteKeyImport,omitempty" jsonschema:"response to a prior remote SSH key import confirmation interaction"`
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
			RuntimeVersion:           firstNonEmpty(strings.TrimSpace(input.Version), CurrentBuildInfo().Version),
			KubernetesContext:        firstNonEmpty(strings.TrimSpace(input.KubernetesContext), strings.TrimSpace(runtime.Context.KubernetesContext)),
			ContainerRegistry:        strings.TrimSpace(input.ContainerRegistry),
			ConfirmTenant:            input.ConfirmTenant,
			ConfirmEnvironment:       input.ConfirmEnvironment,
			AutoApprove:              input.AutoApprove,
		}

		params.Remote = input.Remote
		params.RemoteRepositoryURL = strings.TrimSpace(input.RemoteRepositoryURL)
		params.ConfirmRemoteKeyImport = input.ConfirmRemoteKeyImport

		_, err = eruncommon.RunBootstrapInitWithDependencies(eruncommon.BootstrapInitDependencies{
			Store: eruncommon.TraceBootstrapStore(ctx, runtime.Store),
			FindProjectRoot: func() (string, string, error) {
				return eruncommon.FindProjectRootFromDir(workDir)
			},
			GetWorkingDir: func() (string, error) {
				return workDir, nil
			},
			EnsureKubernetesNamespace: eruncommon.TraceNamespaceEnsurer(ctx, runtime.EnsureKubernetesNamespace),
			LoadProjectConfig:         eruncommon.LoadProjectConfig,
			SaveProjectConfig:         eruncommon.TraceProjectConfigSaver(ctx, eruncommon.SaveProjectConfig),
			WaitForRemoteRuntime:      runtime.WaitForRemoteRuntime,
			RunRemoteCommand:          runtime.RunRemoteCommand,
			DeployHelmChart:           runtime.DeployHelmChart,
			Context:                   ctx,
		}, params)
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

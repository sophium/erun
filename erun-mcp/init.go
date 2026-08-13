package erunmcp

import (
	"bytes"
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

type InitInput struct {
	Tenant                   string   `json:"tenant,omitempty" jsonschema:"optional tenant name to initialize"`
	SelectedTenant           string   `json:"selectedTenant,omitempty" jsonschema:"selected tenant name returned by a prior init interaction event"`
	InitializeCurrentProject bool     `json:"initializeCurrentProject,omitempty" jsonschema:"when true, answer the tenant selection interaction by choosing the current project"`
	ProjectRoot              string   `json:"projectRoot,omitempty" jsonschema:"optional project root to bind to the tenant"`
	Environment              string   `json:"environment,omitempty" jsonschema:"optional environment name to initialize"`
	Version                  string   `json:"version,omitempty" jsonschema:"optional runtime image version to initialize and deploy"`
	RuntimeImage             string   `json:"runtimeImage,omitempty" jsonschema:"optional runtime image repository the environment runs; a bare name resolves against the environment registry and its runtime version, a full reference is used verbatim"`
	ImagePullSecrets         []string `json:"imagePullSecrets,omitempty" jsonschema:"names of Kubernetes dockerconfigjson secrets the runtime pod pulls its image with; required when the runtime image lives in a private registry, since the pod cannot start without one"`
	RuntimeCPU               string   `json:"runtimeCpu,omitempty" jsonschema:"optional runtime pod CPU limit"`
	RuntimeMemory            string   `json:"runtimeMemory,omitempty" jsonschema:"optional runtime pod memory limit"`
	KubernetesContext        string   `json:"kubernetesContext,omitempty" jsonschema:"optional kubernetes context to associate with the environment"`
	ContainerRegistry        string   `json:"containerRegistry,omitempty" jsonschema:"optional container registry; seeds the project's registry list with this host marked build and deploy"`
	Type                     string   `json:"type,omitempty" jsonschema:"optional environment type (local-agent, remote-agent, runtime); takes precedence over remote"`
	Remote                   bool     `json:"remote,omitempty" jsonschema:"deprecated alias for type=remote-agent; prefer type instead"`
	NoGit                    bool     `json:"noGit,omitempty" jsonschema:"when true with remote initialization, create the remote worktree directory without configuring a Git checkout"`
	RemoteRepositoryURL      string   `json:"remoteRepositoryURL,omitempty" jsonschema:"optional SSH repository URL used when creating the remote checkout"`
	CodeCommitSSHKeyID       string   `json:"codeCommitSSHKeyID,omitempty" jsonschema:"optional AWS CodeCommit SSH public key ID used when the remote repository URL is a CodeCommit SSH URL"`
	Bootstrap                bool     `json:"bootstrap,omitempty" jsonschema:"deprecated and ignored; remote runtimes deploy the published erun-devops chart"`
	ConfirmTenant            *bool    `json:"confirmTenant,omitempty" jsonschema:"response to a prior tenant confirmation interaction"`
	ConfirmEnvironment       *bool    `json:"confirmEnvironment,omitempty" jsonschema:"response to a prior environment confirmation interaction"`
	ConfirmRemoteHostConfig  *bool    `json:"confirmRemoteHostConfig,omitempty" jsonschema:"response to a prior existing remote SSH host config confirmation interaction"`
	ConfirmRemoteKeyImport   *bool    `json:"confirmRemoteKeyImport,omitempty" jsonschema:"response to a prior remote SSH key import confirmation interaction"`
	AutoApprove              bool     `json:"autoApprove,omitempty" jsonschema:"when true, automatically approve initialization prompts"`
	DisableBuildScript       bool     `json:"disableBuildScript,omitempty" jsonschema:"when true, ignore any project build.sh for this env; erun build resolves docker/release contexts directly"`
	PlatformAccount          bool     `json:"platformAccount,omitempty" jsonschema:"when true, make this env a cluster platform account: deploy binds its runtime ServiceAccount to cluster-admin so in-pod platform terraform (cluster edge) and component installs can manage cluster-scoped resources"`
	Preview                  bool     `json:"preview,omitempty" jsonschema:"when true, resolve and print the planned actions without executing them"`
	Verbosity                int      `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
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
		tenant, environment, err := scopedTenantEnv(input.Tenant, input.Environment, runtime)
		if err != nil {
			return nil, InitOutput{}, err
		}

		traceOutput := new(bytes.Buffer)
		ctx := runtimeCallContext(input.Preview, input.Verbosity, nil, traceOutput, traceOutput)
		ctx.KubernetesContextPreflight = eruncommon.CloudContextPreflight(runtime.Store, eruncommon.CloudContextDependencies{})

		params := eruncommon.BootstrapInitParams{
			Tenant:                   tenant,
			SelectedTenant:           strings.TrimSpace(input.SelectedTenant),
			InitializeCurrentProject: input.InitializeCurrentProject,
			ProjectRoot:              firstNonEmpty(strings.TrimSpace(input.ProjectRoot), strings.TrimSpace(runtime.Context.RepoPath)),
			Environment:              environment,
			RuntimeVersion:           firstNonEmpty(strings.TrimSpace(input.Version), CurrentBuildInfo().Version),
			RuntimePod: eruncommon.RuntimePodResources{
				CPU:    strings.TrimSpace(input.RuntimeCPU),
				Memory: strings.TrimSpace(input.RuntimeMemory),
			},
			NoGit:                   input.NoGit,
			Bootstrap:               input.Bootstrap,
			KubernetesContext:       firstNonEmpty(strings.TrimSpace(input.KubernetesContext), strings.TrimSpace(runtime.Context.KubernetesContext)),
			ContainerRegistry:       strings.TrimSpace(input.ContainerRegistry),
			ConfirmTenant:           input.ConfirmTenant,
			ConfirmEnvironment:      input.ConfirmEnvironment,
			ConfirmRemoteHostConfig: input.ConfirmRemoteHostConfig,
			AutoApprove:             input.AutoApprove,
			RuntimeImage:            strings.TrimSpace(input.RuntimeImage),
			ImagePullSecrets:        input.ImagePullSecrets,
			DisableBuildScript:      input.DisableBuildScript,
			PlatformAccount:         input.PlatformAccount,
		}

		if envType := eruncommon.EnvironmentType(strings.TrimSpace(input.Type)); envType.IsValid() {
			params.Type = envType
			params.Remote = envType != eruncommon.EnvironmentTypeLocalAgent
		} else {
			params.Remote = input.Remote
		}
		params.RemoteRepositoryURL = strings.TrimSpace(input.RemoteRepositoryURL)
		params.CodeCommitSSHKeyID = strings.TrimSpace(input.CodeCommitSSHKeyID)
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

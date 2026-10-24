package initenv

import (
	"bytes"
	"context"
	"io"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/setup"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/primitive/open"
	"github.com/sophium/erun/erun-mcp/tools/shared"
)

type InitInput struct {
	Tenant                   string    `json:"tenant,omitempty" jsonschema:"optional tenant name to initialize"`
	SelectedTenant           string    `json:"selectedTenant,omitempty" jsonschema:"selected tenant name returned by a prior init interaction event"`
	InitializeCurrentProject bool      `json:"initializeCurrentProject,omitempty" jsonschema:"when true, answer the tenant selection interaction by choosing the current project"`
	ProjectRoot              string    `json:"projectRoot,omitempty" jsonschema:"optional project root to bind to the tenant"`
	Environment              string    `json:"environment,omitempty" jsonschema:"optional environment name to initialize"`
	Version                  string    `json:"version,omitempty" jsonschema:"optional runtime image version to record; omitted, a new environment takes erun's own version and an existing environment keeps the one it runs. A brand-new remote-worktree environment (type remote-agent or runtime) deploys at this version immediately; on an existing environment this only re-pins the version in config, it does not redeploy — call the deploy tool separately to roll it out"`
	RuntimeImage             string    `json:"runtimeImage,omitempty" jsonschema:"optional runtime image repository the environment runs; a bare name resolves against the environment registry and its runtime version, a full reference is used verbatim"`
	RuntimeRegistry          string    `json:"runtimeRegistry,omitempty" jsonschema:"optional registry the environment resolves erun's own artifacts from — the runtime chart and the platform images the pod pulls; set it when the environment's deploy registry holds only this project's images, so erun's chart is not published there"`
	ImagePullSecrets         []string  `json:"imagePullSecrets,omitempty" jsonschema:"names of Kubernetes dockerconfigjson secrets the runtime pod pulls its image with; required when the runtime image lives in a private registry, since the pod cannot start without one"`
	RuntimeCPU               string    `json:"runtimeCpu,omitempty" jsonschema:"optional runtime pod CPU limit; omitted, an existing environment keeps its recorded limit"`
	RuntimeMemory            string    `json:"runtimeMemory,omitempty" jsonschema:"optional runtime pod memory limit; omitted, an existing environment keeps its recorded limit"`
	RuntimeDindCPU           string    `json:"runtimeDindCpu,omitempty" jsonschema:"optional erun-dind sidecar CPU limit — the container that actually runs erun build/erun release; omitted, an existing environment keeps its recorded limit"`
	RuntimeDindMemory        string    `json:"runtimeDindMemory,omitempty" jsonschema:"optional erun-dind sidecar memory limit; raise this when a multi-arch erun release/erun build --release OOMs inside the sidecar. Omitted, an existing environment keeps its recorded limit"`
	KubernetesContext        string    `json:"kubernetesContext,omitempty" jsonschema:"optional kubernetes context to associate with the environment"`
	ContainerRegistry        string    `json:"containerRegistry,omitempty" jsonschema:"optional container registry; seeds the project's registry list with this host marked build and deploy"`
	Type                     string    `json:"type,omitempty" jsonschema:"optional environment type (local-agent, remote-agent, runtime, host); takes precedence over remote. host names a directory on this machine with no pod and no cluster at all — for desktop-app builds and tasks needing host-wide credentials. On an existing environment this changes the type; omitted, the environment keeps the type it has"`
	Remote                   bool      `json:"remote,omitempty" jsonschema:"deprecated alias for type=remote-agent; prefer type instead"`
	NoGit                    bool      `json:"noGit,omitempty" jsonschema:"when true with remote initialization, create the remote worktree directory without configuring a Git checkout"`
	RemoteRepositoryURL      string    `json:"remoteRepositoryURL,omitempty" jsonschema:"optional SSH repository URL used when creating the remote checkout"`
	CodeCommitSSHKeyID       string    `json:"codeCommitSSHKeyID,omitempty" jsonschema:"optional AWS CodeCommit SSH public key ID used when the remote repository URL is a CodeCommit SSH URL"`
	Bootstrap                bool      `json:"bootstrap,omitempty" jsonschema:"deprecated and ignored; remote runtimes deploy the published erun-devops chart"`
	ConfirmTenant            *bool     `json:"confirmTenant,omitempty" jsonschema:"response to a prior tenant confirmation interaction"`
	ConfirmEnvironment       *bool     `json:"confirmEnvironment,omitempty" jsonschema:"response to a prior environment confirmation interaction"`
	ConfirmRemoteHostConfig  *bool     `json:"confirmRemoteHostConfig,omitempty" jsonschema:"response to a prior existing remote SSH host config confirmation interaction"`
	ConfirmRemoteKeyImport   *bool     `json:"confirmRemoteKeyImport,omitempty" jsonschema:"response to a prior remote SSH key import confirmation interaction"`
	AutoApprove              bool      `json:"autoApprove,omitempty" jsonschema:"when true, automatically approve initialization prompts"`
	DisableBuildScript       bool      `json:"disableBuildScript,omitempty" jsonschema:"when true, ignore any project build.sh for this env; erun build resolves docker/release contexts directly"`
	PlatformAccount          bool      `json:"platformAccount,omitempty" jsonschema:"when true, make this env a cluster platform account: deploy binds its runtime ServiceAccount to cluster-admin so in-pod platform terraform (cluster edge) and component installs can manage cluster-scoped resources"`
	Components               *[]string `json:"components,omitempty" jsonschema:"optional saved default deploy component selection for this environment — what the deploy tool rolls out with no components override of its own. Omit to leave a saved selection untouched; pass an empty array to clear one and return the environment to its repo k8s.deployments plan"`
	Preview                  bool      `json:"preview,omitempty" jsonschema:"when true, resolve and print the planned actions without executing them"`
	Verbosity                int       `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
	shared.JobEnvelopeInput
}

func InitTool(runtime shared.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, InitInput) (*mcp.CallToolResult, shared.JobEnvelopeOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input InitInput) (*mcp.CallToolResult, shared.JobEnvelopeOutput, error) {
		execute, err := initToolExecute(runtime, input)
		if err != nil {
			return nil, shared.JobEnvelopeOutput{}, err
		}
		envelope, err := shared.RunJobEnvelope(runtime, "init", input.JobEnvelopeInput, input.Preview, execute)
		return nil, envelope, err
	}
}

// initToolExecute resolves the target synchronously so a bad input is refused
// before an async job starts.
func initToolExecute(runtime shared.RuntimeConfig, input InitInput) (func(bool, io.Writer) (shared.CommandOutput, error), error) {
	workDir, err := shared.RuntimeRepoPath(runtime.Context)
	if err != nil {
		return nil, err
	}
	tenant, environment, err := shared.ScopedTenantEnv(input.Tenant, input.Environment, runtime)
	if err != nil {
		return nil, err
	}

	return func(preview bool, log io.Writer) (shared.CommandOutput, error) {
		traceOutput := new(bytes.Buffer)
		traceSink := shared.MirrorToJobLog(traceOutput, log)
		ctx := shared.RuntimeCallContext(preview, input.Verbosity, nil, traceSink, traceSink)
		ctx.KubernetesContextPreflight = cloud.CloudContextPreflight(runtime.Store, cloud.CloudContextDependencies{})

		params := open.BootstrapInitParams{
			Tenant:                   tenant,
			SelectedTenant:           strings.TrimSpace(input.SelectedTenant),
			InitializeCurrentProject: input.InitializeCurrentProject,
			ProjectRoot:              shared.FirstNonEmpty(strings.TrimSpace(input.ProjectRoot), strings.TrimSpace(runtime.Context.RepoPath)),
			Environment:              environment,
			RuntimeVersion:           strings.TrimSpace(input.Version),
			RuntimeVersionDefault:    shared.CurrentBuildInfo().Version,
			RuntimePod: model.RuntimePodResources{
				CPU:    strings.TrimSpace(input.RuntimeCPU),
				Memory: strings.TrimSpace(input.RuntimeMemory),
			},
			RuntimeDindPod: model.RuntimePodResources{
				CPU:    strings.TrimSpace(input.RuntimeDindCPU),
				Memory: strings.TrimSpace(input.RuntimeDindMemory),
			},
			NoGit:                   input.NoGit,
			Bootstrap:               input.Bootstrap,
			KubernetesContext:       shared.FirstNonEmpty(strings.TrimSpace(input.KubernetesContext), strings.TrimSpace(runtime.Context.KubernetesContext)),
			ContainerRegistry:       strings.TrimSpace(input.ContainerRegistry),
			ConfirmTenant:           input.ConfirmTenant,
			ConfirmEnvironment:      input.ConfirmEnvironment,
			ConfirmRemoteHostConfig: input.ConfirmRemoteHostConfig,
			AutoApprove:             input.AutoApprove,
			RuntimeImage:            strings.TrimSpace(input.RuntimeImage),
			RuntimeRegistry:         strings.TrimSpace(input.RuntimeRegistry),
			ImagePullSecrets:        input.ImagePullSecrets,
			DisableBuildScript:      input.DisableBuildScript,
			PlatformAccount:         input.PlatformAccount,
			Components:              input.Components,
		}

		if envType := config.EnvironmentType(strings.TrimSpace(input.Type)); envType.IsValid() {
			params.Type = envType
			// Derived from RemoteWorktree so host reports false.
			params.Remote = (open.BootstrapInitParams{Type: envType}).RemoteWorktree()
		} else {
			params.Remote = input.Remote
		}
		params.RemoteRepositoryURL = strings.TrimSpace(input.RemoteRepositoryURL)
		params.CodeCommitSSHKeyID = strings.TrimSpace(input.CodeCommitSSHKeyID)
		params.ConfirmRemoteKeyImport = input.ConfirmRemoteKeyImport

		_, err := setup.RunBootstrapInitWithDependencies(setup.BootstrapInitDependencies{
			Store: setup.TraceBootstrapStore(ctx, runtime.Store),
			FindProjectRoot: func() (string, string, error) {
				return config.FindProjectRootFromDir(workDir)
			},
			GetWorkingDir: func() (string, error) {
				return workDir, nil
			},
			EnsureKubernetesNamespace: setup.TraceNamespaceEnsurer(ctx, runtime.EnsureKubernetesNamespace),
			LoadProjectConfig:         config.LoadProjectConfig,
			SaveProjectConfig:         setup.TraceProjectConfigSaver(ctx, config.SaveProjectConfig),
			WaitForRemoteRuntime:      runtime.WaitForRemoteRuntime,
			RunRemoteCommand:          runtime.RunRemoteCommand,
			DeployHelmChart:           runtime.DeployHelmChart,
			Context:                   ctx,
		}, params)
		if err != nil {
			if interaction, ok := setup.AsBootstrapInitInteraction(err); ok {
				return shared.CommandOutput{
					Executed:         false,
					WorkingDirectory: workDir,
					Trace:            shared.NormalizeTraceLines(traceOutput.String()),
					Interaction:      &interaction,
				}, nil
			}
			return shared.CommandOutput{}, err
		}

		return shared.CommandOutput{
			Executed:         !ctx.DryRun,
			WorkingDirectory: workDir,
			Trace:            shared.NormalizeTraceLines(traceOutput.String()),
		}, nil
	}, nil
}

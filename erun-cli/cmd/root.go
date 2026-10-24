package cmd

import (
	"os"
	"runtime"
	"time"

	"github.com/manifoldco/promptui"
	"github.com/sophium/erun/cmd/activity"
	"github.com/sophium/erun/cmd/adopt"
	"github.com/sophium/erun/cmd/apiserver"
	"github.com/sophium/erun/cmd/app"
	"github.com/sophium/erun/cmd/build"
	cloudpkg "github.com/sophium/erun/cmd/cloud"
	"github.com/sophium/erun/cmd/contribute"
	"github.com/sophium/erun/cmd/deploy"
	"github.com/sophium/erun/cmd/dind"
	"github.com/sophium/erun/cmd/doctor"
	"github.com/sophium/erun/cmd/e2e"
	"github.com/sophium/erun/cmd/envdelete"
	"github.com/sophium/erun/cmd/execcmd"
	"github.com/sophium/erun/cmd/expose"
	"github.com/sophium/erun/cmd/idle"
	"github.com/sophium/erun/cmd/initcmd"
	"github.com/sophium/erun/cmd/inputs"
	"github.com/sophium/erun/cmd/jobs"
	"github.com/sophium/erun/cmd/kubecontext"
	"github.com/sophium/erun/cmd/list"
	"github.com/sophium/erun/cmd/mcp"
	"github.com/sophium/erun/cmd/observe"
	"github.com/sophium/erun/cmd/open"
	"github.com/sophium/erun/cmd/orchestrator"
	"github.com/sophium/erun/cmd/outputs"
	"github.com/sophium/erun/cmd/pin"
	"github.com/sophium/erun/cmd/platform"
	"github.com/sophium/erun/cmd/resize"
	"github.com/sophium/erun/cmd/review"
	"github.com/sophium/erun/cmd/services"
	"github.com/sophium/erun/cmd/shared"
	"github.com/sophium/erun/cmd/sshd"
	"github.com/sophium/erun/cmd/stop"
	"github.com/sophium/erun/cmd/terraform"
	"github.com/sophium/erun/cmd/upgrade"
	"github.com/sophium/erun/cmd/usage"
	"github.com/sophium/erun/cmd/version"
	common "github.com/sophium/erun/erun-common"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/gitx"
	"github.com/sophium/erun/erun-common/infra/kube"
	"github.com/sophium/erun/erun-common/infra/registry"
	"github.com/spf13/cobra"
)

func runSelect(prompt promptui.Select) (int, string, error) {
	if shared.WriterIsTerminal(os.Stdout) && runtime.GOOS != "windows" {
		prompt.Stdin = os.Stdin
		return prompt.Run()
	}
	return shared.RunPlainSelect(prompt)
}

func Execute() error {
	deps := newRootDependencies()
	return deps.rootCommand().Execute()
}

type rootDependencies struct {
	configStore               config.ConfigStore
	store                     rootStore
	deployHelmChart           common.HelmChartDeployerFunc
	recoveringDeployHelmChart common.HelmChartDeployerFunc
	runInit                   func(core.Context, common.BootstrapInitParams) error
	runInitForArgs            func(core.Context, []string) error
	runInitForOpen            func(core.Context, common.OpenParams) error
	dockerRegistryLogin       common.DockerRegistryLoginFunc
	push                      common.DockerPushFunc
	resolveOpen               func(common.OpenParams) (config.OpenResult, error)
	resolveRuntimeDeploySpec  func(core.Context, config.OpenResult, bool) (common.DeploySpec, error)
	activateMCP               shared.MCPForwarder
	activateAPI               shared.APIForwarder
	activateSSHD              shared.SSHDActivator
	runManagedDeploy          func(core.Context, config.OpenResult) error
}

func newRootDependencies() rootDependencies {
	configStore := config.ConfigStore{}
	store := rootStore(configStore)
	dockerRegistryLogin := common.DockerRegistryLoginWithHostedRegistry(store, shared.CloudDependencies())
	deployHelmChart := common.WrapHelmChartDeployerWithNamespaceEnsure(initcmd.EnsureKubernetesNamespace, common.DeployHelmChart)
	recoveringDeployHelmChart := shared.WrapHelmDeployWithReleaseRecovery(shared.RunPrompt, deployHelmChart, common.ClearHelmReleasePendingOperation)
	runInit := newRunInit(store, config.FindProjectRoot, shared.RunPrompt, runSelect, initcmd.ListKubernetesContexts, initcmd.EnsureKubernetesNamespace, common.WaitForShellDeployment, kube.RunRemoteCommand, recoveringDeployHelmChart)
	deps := rootDependencies{
		configStore:               configStore,
		store:                     store,
		deployHelmChart:           deployHelmChart,
		recoveringDeployHelmChart: recoveringDeployHelmChart,
		runInit:                   runInit,
		runInitForArgs:            newRunInitForArgs(store, runInit),
		runInitForOpen:            newRunInitForOpen(store, runInit),
		dockerRegistryLogin:       dockerRegistryLogin,
		push:                      newPushOperation(nil, dockerRegistryLogin, runSelect),
		activateMCP:               mcp.NewMCPForwarder(),
		activateAPI:               apiserver.NewAPIForwarder(),
		activateSSHD:              sshd.NewSSHDActivator(kube.RunRemoteCommand),
	}
	deps.resolveOpen = deps.resolveOpenResult
	deps.resolveRuntimeDeploySpec = deps.resolveRuntimeDeploySpecForOpenTarget
	deps.runManagedDeploy = deps.runManagedDeployForOpen
	return deps
}

func (d rootDependencies) resolveOpenResult(params common.OpenParams) (config.OpenResult, error) {
	return common.ResolveOpen(d.store, params)
}

func (d rootDependencies) resolveRuntimeDeploySpecForOpenTarget(ctx core.Context, target config.OpenResult, allowLocalBuilds bool) (common.DeploySpec, error) {
	return resolveRuntimeDeploySpecForOpen(ctx, d.store, config.FindProjectRoot, common.ResolveDockerBuildContext, common.ResolveKubernetesDeployContext, time.Now, shared.CurrentBuildInfo(), target, allowLocalBuilds)
}

func (d rootDependencies) runManagedDeployForOpen(ctx core.Context, target config.OpenResult) error {
	ctx = shared.WithCloudContextPreflight(ctx, d.store)
	specs, err := common.ResolveCurrentDeploySpecs(
		ctx,
		d.store,
		config.FindProjectRoot,
		common.ResolveDockerBuildContext,
		common.ResolveKubernetesDeployContext,
		time.Now,
		common.DeployTarget{
			Tenant:      target.Tenant,
			Environment: target.Environment,
			RepoPath:    target.RepoPath,
		},
	)
	if err != nil {
		return err
	}
	return common.RunDeploySpecs(ctx, specs, d.recoveringDeployHelmChart)
}

func (d rootDependencies) rootCommand() *cobra.Command {
	cmd := newRootCommand(d.runRoot)
	shared.AddCommands(cmd, d.commands()...)
	return cmd
}

func (d rootDependencies) commands() []*cobra.Command {
	containerCmd := d.containerCommand()
	k8sCmd := d.k8sCommand()
	devopsCmd := shared.NewCommandGroup("devops", "DevOps utilities", containerCmd, k8sCmd)
	return []*cobra.Command{
		initcmd.NewInitCmd(d.runInit),
		d.openCommand(),
		stop.NewStopCmd(d.resolveOpen, d.store.SaveEnvConfig),
		d.sshdCommand(),
		d.pinCommand(),
		devopsCmd,
		d.optionalBuildCommand(),
		d.optionalPushCommand(),
		d.deployCommand(),
		d.resizeCommand(),
		d.publishCommand(),
		d.upgradeCommand(),
		mcp.NewMCPCmd(d.resolveOpen, d.runInitForArgs, mcp.LaunchMCPProcess),
		apiserver.NewAPICmd(d.resolveOpen, d.runInitForArgs, apiserver.LaunchAPIProcess),
		app.NewAppCmd(app.LaunchAppProcess),
		execcmd.NewExecCmd(config.FindProjectRoot, gitx.GitCommandRunner, nil, d.resolveOpen, d.configStore, shared.CloudDependencies()),
		cloudpkg.NewCloudCmd(d.configStore, shared.RunPrompt, runSelect, shared.CloudDependencies()),
		orchestrator.NewOrchestratorCmd(d.configStore),
		kubecontext.NewContextCmd(d.configStore, shared.RunPrompt, runSelect, cloud.CloudContextDependencies{}),
		platform.NewPlatformCmd(d.configStore, shared.RunPrompt, shared.CloudDependencies()),
		review.NewReviewCmd(d.configStore, shared.CloudDependencies()),
		jobs.NewJobsCmd(d.configStore, shared.CloudDependencies()),
		list.NewListCmd(d.configStore, config.FindProjectRoot),
		outputs.NewOutputsCmd(d.resolveOpen),
		inputs.NewInputsCmd(d.resolveOpen),
		doctor.NewDoctorCmd(d.resolveOpen, d.configStore, shared.CloudDependencies(), cloud.CloudContextDependencies{}, shared.RunPrompt),
		observe.NewObserveCmd(d.resolveOpen),
		services.NewServicesCmd(d.resolveOpen),
		usage.NewUsageCmd(d.resolveOpen),
		e2e.NewE2ECmd(d.resolveOpen, config.FindProjectRoot),
		envdelete.NewDeleteCmd(d.configStore, shared.RunPrompt, kube.DeleteKubernetesNamespace),
		expose.NewExposeCmd(d.configStore, d.configStore, shared.CloudDependencies(), config.FindProjectRoot),
		expose.NewUnexposeCmd(d.configStore, d.configStore, shared.CloudDependencies(), config.FindProjectRoot),
		terraform.NewTerraformCmd(d.configStore, config.FindProjectRoot),
		contribute.NewContributeCmd(gitx.GitCommandRunner),
		idle.NewIdleCmd(d.configStore, d.resolveOpen),
		orchestrator.NewWhipCmd(d.configStore, d.resolveOpen),
		execcmd.DeprecatedTopLevelJobCmd(d.resolveOpen),
		build.NewReleaseCmd(d.store, config.FindProjectRoot, common.ResolveDockerBuildContext, time.Now, gitx.GitCommandRunner, common.DockerImageBuilder, d.dockerRegistryLogin, runSelect, d.push),
		version.NewVersionCmd(func() (version.VersionCommandInfo, error) {
			return version.ResolveVersionCommandBuildInfo(config.FindProjectRoot)
		}, registry.ResolveDefaultRuntimeRegistryVersions),
		activity.NewActivityCmd(d.configStore, d.resolveOpen),
		adopt.NewAdoptWorktreeCmd(),
		dind.NewDindEntrypointCmd(),
		initcmd.NewRuntimeInitCmd(),
		initcmd.NewTrimCacheCmd(),
		initcmd.NewInstallSkillsCmd(),
	}
}

func (d rootDependencies) openCommand() *cobra.Command {
	return open.NewOpenCmd(
		func(ctx core.Context) core.Context {
			return shared.WithCloudContextPreflight(ctx, d.store)
		},
		d.resolveOpen,
		d.store.SaveEnvConfig,
		d.runInitForOpen,
		shared.RunPrompt,
		open.NewOpenShellRunner(common.WaitForShellDeployment, common.ExecShell),
		d.runManagedDeploy,
		common.CheckKubernetesDeployment,
		d.resolveRuntimeDeploySpec,
		d.deployHelmChart,
		d.activateMCP,
		d.activateAPI,
		d.activateSSHD,
		open.LaunchVSCode,
		open.LaunchIntelliJ,
	)
}

func (d rootDependencies) pinCommand() *cobra.Command {
	return pin.NewPinCmd(func(ctx core.Context) core.Context {
		return shared.WithCloudContextPreflight(ctx, d.store)
	}, d.resolveOpen, d.store.SaveEnvConfig, d.store.ListEnvConfigs, config.FindProjectRoot)
}

func (d rootDependencies) sshdCommand() *cobra.Command {
	return sshd.NewSSHDCmd(func(ctx core.Context) core.Context {
		return shared.WithCloudContextPreflight(ctx, d.store)
	}, d.resolveOpen, d.store.SaveEnvConfig, d.runInitForOpen, config.FindProjectRoot, d.resolveRuntimeDeploySpec, d.recoveringDeployHelmChart, kube.RunRemoteCommand, shared.WriteLocalSSHConfig)
}

func (d rootDependencies) containerCommand() *cobra.Command {
	return shared.NewCommandGroup(
		"container",
		"Container utilities",
		build.NewBuildCmd(d.store, config.FindProjectRoot, common.ResolveDockerBuildContext, common.ResolveKubernetesDeployContext, time.Now, common.BuildScriptRunner, common.DockerImageBuilder, d.dockerRegistryLogin, runSelect, d.push, d.recoveringDeployHelmChart, d.configStore, shared.CloudDependencies()),
		build.NewPushCmd(d.store, config.FindProjectRoot, common.ResolveDockerBuildContext, time.Now, common.DockerImageBuilder, d.dockerRegistryLogin, runSelect, d.push),
	)
}

func (d rootDependencies) k8sCommand() *cobra.Command {
	return shared.NewCommandGroup(
		"k8s",
		"Kubernetes utilities",
		deploy.NewK8sDeployCmd(d.store, d.store.SaveEnvConfig, config.FindProjectRoot, common.ResolveDockerBuildContext, common.ResolveKubernetesDeployContext, time.Now, common.DockerImageBuilder, d.push, d.recoveringDeployHelmChart),
	)
}

func (d rootDependencies) optionalBuildCommand() *cobra.Command {
	if !hasOptionalBuildCmd(config.FindProjectRoot, common.ResolveDockerBuildContext) {
		return nil
	}
	buildCmd := build.NewBuildCmd(d.store, config.FindProjectRoot, common.ResolveDockerBuildContext, common.ResolveKubernetesDeployContext, time.Now, common.BuildScriptRunner, common.DockerImageBuilder, d.dockerRegistryLogin, runSelect, d.push, d.recoveringDeployHelmChart, d.configStore, shared.CloudDependencies())
	buildCmd.Short = optionalBuildCmdShort(d.store, config.FindProjectRoot, common.ResolveDockerBuildContext)
	return buildCmd
}

func (d rootDependencies) optionalPushCommand() *cobra.Command {
	if !hasOptionalPushCmd(config.FindProjectRoot, common.ResolveDockerBuildContext) {
		return nil
	}
	pushCmd := build.NewRootPushCmd(d.store, config.FindProjectRoot, common.ResolveDockerBuildContext, time.Now, common.BuildScriptRunner, common.DockerImageBuilder, d.dockerRegistryLogin, runSelect, d.push, d.configStore, shared.CloudDependencies())
	pushCmd.Short = optionalPushCmdShort(config.FindProjectRoot, common.ResolveDockerBuildContext)
	return pushCmd
}

// Registered unconditionally: the desktop Redeploy button invokes "erun deploy
// --version X" from a cwd that may lack a kubernetes deploy context, so the
// command must exist even where no context resolves.
func (d rootDependencies) deployCommand() *cobra.Command {
	return deploy.NewDeployCmd(d.store, d.store.SaveEnvConfig, config.FindProjectRoot, common.ResolveDockerBuildContext, common.ResolveKubernetesDeployContext, time.Now, common.DockerImageBuilder, d.push, d.recoveringDeployHelmChart)
}

func (d rootDependencies) resizeCommand() *cobra.Command {
	return resize.NewResizeCmd(d.store, d.store.SaveEnvConfig, config.FindProjectRoot, common.ResolveDockerBuildContext, common.ResolveKubernetesDeployContext, d.recoveringDeployHelmChart, d.resolveOpen)
}

func (d rootDependencies) publishCommand() *cobra.Command {
	return build.NewPublishCmd(d.store, config.FindProjectRoot, common.ResolveDockerBuildContext, common.ResolveKubernetesDeployContext, time.Now)
}

func (d rootDependencies) upgradeCommand() *cobra.Command {
	return upgrade.NewUpgradeCmd(d.store, d.store.SaveEnvConfig, config.FindProjectRoot, common.ResolveDockerBuildContext, common.ResolveKubernetesDeployContext, time.Now, common.DockerImageBuilder, d.push, d.recoveringDeployHelmChart)
}

func (d rootDependencies) runRoot(cmd *cobra.Command, args []string) error {
	ctx := shared.WithCloudContextPreflight(shared.CommandContext(cmd), d.store)
	result, initRan, err := open.ResolveOpenWithInitStop(ctx, args, shared.ShouldInitRootCommand, d.resolveOpen, d.runInitForArgs)
	if err != nil {
		return err
	}
	if initRan {
		return nil
	}
	return open.RunResolvedOpenCommandWithAPI(ctx, result, open.OpenOptions{}, shared.RunPrompt, open.NewOpenShellRunner(common.WaitForShellDeployment, common.ExecShell), d.runManagedDeploy, common.CheckKubernetesDeployment, d.resolveRuntimeDeploySpec, d.deployHelmChart, d.activateMCP, d.activateAPI, d.activateSSHD, open.LaunchVSCode, open.LaunchIntelliJ)
}

package cmd

import (
	"io"
	"time"

	"github.com/manifoldco/promptui"
	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

type (
	PromptRunner             func(promptui.Prompt) (string, error)
	SelectRunner             func(promptui.Select) (int, string, error)
	KubernetesContextsLister func() ([]string, error)
	MCPLauncher              func(io.Reader, io.Writer, io.Writer, []string) error
	APILauncher              func(io.Reader, io.Writer, io.Writer, []string) error
)

func runPrompt(prompt promptui.Prompt) (string, error) {
	return prompt.Run()
}

func runSelect(prompt promptui.Select) (int, string, error) {
	return prompt.Run()
}

func Execute() error {
	deps := newRootDependencies()
	return deps.rootCommand().Execute()
}

type rootDependencies struct {
	configStore               common.ConfigStore
	store                     rootStore
	deployHelmChart           common.HelmChartDeployerFunc
	recoveringDeployHelmChart common.HelmChartDeployerFunc
	runInit                   func(common.Context, common.BootstrapInitParams) error
	runInitForArgs            func(common.Context, []string) error
	runInitForOpen            func(common.Context, common.OpenParams) error
	push                      common.DockerPushFunc
	resolveOpen               func(common.OpenParams) (common.OpenResult, error)
	resolveRuntimeDeploySpec  func(common.OpenResult) (common.DeploySpec, error)
	activateMCP               MCPForwarder
	activateAPI               APIForwarder
	activateSSHD              SSHDActivator
	runManagedDeploy          func(common.Context, common.OpenResult) error
}

func newRootDependencies() rootDependencies {
	configStore := common.ConfigStore{}
	store := rootStore(configStore)
	deployHelmChart := common.WrapHelmChartDeployerWithNamespaceEnsure(ensureKubernetesNamespace, common.DeployHelmChart)
	recoveringDeployHelmChart := wrapHelmDeployWithReleaseRecovery(runPrompt, deployHelmChart, common.ClearHelmReleasePendingOperation)
	runInit := newRunInit(store, common.FindProjectRoot, runPrompt, runSelect, listKubernetesContexts, ensureKubernetesNamespace, common.WaitForShellDeployment, common.RunRemoteCommand, recoveringDeployHelmChart)
	deps := rootDependencies{
		configStore:               configStore,
		store:                     store,
		deployHelmChart:           deployHelmChart,
		recoveringDeployHelmChart: recoveringDeployHelmChart,
		runInit:                   runInit,
		runInitForArgs:            newRunInitForArgs(store, runInit),
		runInitForOpen:            newRunInitForOpen(store, runInit),
		push:                      newPushOperation(nil, common.DockerRegistryLogin, runSelect),
		activateMCP:               newMCPForwarder(),
		activateAPI:               newAPIForwarder(),
		activateSSHD:              newSSHDActivator(common.RunRemoteCommand),
	}
	deps.resolveOpen = deps.resolveOpenResult
	deps.resolveRuntimeDeploySpec = deps.resolveRuntimeDeploySpecForOpenTarget
	deps.runManagedDeploy = deps.runManagedDeployForOpen
	return deps
}

func (d rootDependencies) resolveOpenResult(params common.OpenParams) (common.OpenResult, error) {
	return common.ResolveOpen(d.store, params)
}

func (d rootDependencies) resolveRuntimeDeploySpecForOpenTarget(target common.OpenResult) (common.DeploySpec, error) {
	return resolveRuntimeDeploySpecForOpen(d.store, common.FindProjectRoot, common.ResolveDockerBuildContext, common.ResolveKubernetesDeployContext, time.Now, currentBuildInfo(), target)
}

func (d rootDependencies) runManagedDeployForOpen(ctx common.Context, target common.OpenResult) error {
	ctx = withCloudContextPreflight(ctx, d.store)
	specs, err := common.ResolveCurrentDeploySpecs(
		d.store,
		common.FindProjectRoot,
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
	return common.RunDeploySpecs(ctx, specs, common.DockerImageBuilder, d.push, d.recoveringDeployHelmChart)
}

func (d rootDependencies) rootCommand() *cobra.Command {
	cmd := newRootCommand(d.runRoot)
	addCommands(cmd, d.commands()...)
	return cmd
}

func (d rootDependencies) commands() []*cobra.Command {
	containerCmd := d.containerCommand()
	k8sCmd := d.k8sCommand()
	devopsCmd := newCommandGroup("devops", "DevOps utilities", containerCmd, k8sCmd)
	return []*cobra.Command{
		newInitCmd(d.runInit),
		d.openCommand(),
		d.sshdCommand(),
		devopsCmd,
		d.optionalBuildCommand(),
		d.optionalPushCommand(),
		d.optionalDeployCommand(),
		newMCPCmd(d.resolveOpen, d.runInitForArgs, launchMCPProcess),
		newAPICmd(d.resolveOpen, d.runInitForArgs, launchAPIProcess),
		newAppCmd(launchAppProcess),
		newExecCmd(common.FindProjectRoot, common.GitCommandRunner, nil),
		newCloudCmd(d.configStore, runPrompt, runSelect, common.CloudDependencies{}),
		newContextCmd(d.configStore, runPrompt, runSelect, common.CloudContextDependencies{}),
		newListCmd(d.configStore, common.FindProjectRoot),
		newDoctorCmd(d.resolveOpen, runPrompt),
		newDeleteCmd(d.configStore, runPrompt, common.DeleteKubernetesNamespace),
		newIdleCmd(d.configStore),
		newReleaseCmd(common.FindProjectRoot, common.GitCommandRunner),
		newVersionCmd(func() (common.BuildInfo, string, error) {
			return resolveVersionCommandBuildInfo(common.FindProjectRoot)
		}, common.ResolveDefaultRuntimeRegistryVersions),
		newActivityCmd(d.configStore),
	}
}

func (d rootDependencies) openCommand() *cobra.Command {
	return newOpenCmd(
		func(ctx common.Context) common.Context {
			return withCloudContextPreflight(ctx, d.store)
		},
		d.resolveOpen,
		d.store.SaveEnvConfig,
		d.runInitForOpen,
		runPrompt,
		newOpenShellRunner(common.WaitForShellDeployment, common.ExecShell),
		d.runManagedDeploy,
		common.CheckKubernetesDeployment,
		d.resolveRuntimeDeploySpec,
		d.deployHelmChart,
		d.activateMCP,
		d.activateAPI,
		d.activateSSHD,
		launchVSCode,
		launchIntelliJ,
	)
}

func (d rootDependencies) sshdCommand() *cobra.Command {
	return newSSHDCmd(func(ctx common.Context) common.Context {
		return withCloudContextPreflight(ctx, d.store)
	}, d.resolveOpen, d.store.SaveEnvConfig, d.runInitForOpen, d.resolveRuntimeDeploySpec, d.recoveringDeployHelmChart, common.RunRemoteCommand, writeLocalSSHConfig)
}

func (d rootDependencies) containerCommand() *cobra.Command {
	return newCommandGroup(
		"container",
		"Container utilities",
		newBuildCmd(d.store, common.FindProjectRoot, common.ResolveDockerBuildContext, common.ResolveKubernetesDeployContext, time.Now, common.BuildScriptRunner, common.DockerImageBuilder, common.DockerRegistryLogin, runSelect, d.push, d.recoveringDeployHelmChart),
		newPushCmd(d.store, common.FindProjectRoot, common.ResolveDockerBuildContext, time.Now, common.DockerImageBuilder, d.push),
	)
}

func (d rootDependencies) k8sCommand() *cobra.Command {
	return newCommandGroup(
		"k8s",
		"Kubernetes utilities",
		newK8sDeployCmd(d.store, common.FindProjectRoot, common.ResolveDockerBuildContext, common.ResolveKubernetesDeployContext, time.Now, common.DockerImageBuilder, d.push, d.recoveringDeployHelmChart),
	)
}

func (d rootDependencies) optionalBuildCommand() *cobra.Command {
	if !hasOptionalBuildCmd(common.FindProjectRoot, common.ResolveDockerBuildContext) {
		return nil
	}
	buildCmd := newBuildCmd(d.store, common.FindProjectRoot, common.ResolveDockerBuildContext, common.ResolveKubernetesDeployContext, time.Now, common.BuildScriptRunner, common.DockerImageBuilder, common.DockerRegistryLogin, runSelect, d.push, d.recoveringDeployHelmChart)
	buildCmd.Short = optionalBuildCmdShort(common.FindProjectRoot, common.ResolveDockerBuildContext)
	return buildCmd
}

func (d rootDependencies) optionalPushCommand() *cobra.Command {
	if !hasOptionalPushCmd(common.FindProjectRoot, common.ResolveDockerBuildContext) {
		return nil
	}
	pushCmd := newPushCmd(d.store, common.FindProjectRoot, common.ResolveDockerBuildContext, time.Now, common.DockerImageBuilder, d.push)
	pushCmd.Short = optionalPushCmdShort(common.FindProjectRoot, common.ResolveDockerBuildContext)
	return pushCmd
}

func (d rootDependencies) optionalDeployCommand() *cobra.Command {
	if !hasOptionalDeployCmd(common.ResolveKubernetesDeployContext) {
		return nil
	}
	return newDeployCmd(d.store, common.FindProjectRoot, common.ResolveDockerBuildContext, common.ResolveKubernetesDeployContext, time.Now, common.DockerImageBuilder, d.push, d.recoveringDeployHelmChart)
}

func (d rootDependencies) runRoot(cmd *cobra.Command, args []string) error {
	ctx := withCloudContextPreflight(commandContext(cmd), d.store)
	result, initRan, err := resolveOpenWithInitStop(ctx, args, shouldInitRootCommand, d.resolveOpen, d.runInitForArgs)
	if err != nil {
		return err
	}
	if initRan {
		return nil
	}
	return runResolvedOpenCommandWithAPI(ctx, result, openOptions{}, runPrompt, newOpenShellRunner(common.WaitForShellDeployment, common.ExecShell), d.runManagedDeploy, common.CheckKubernetesDeployment, d.resolveRuntimeDeploySpec, d.deployHelmChart, d.activateMCP, d.activateAPI, d.activateSSHD, launchVSCode, launchIntelliJ)
}

func withCloudContextPreflight(ctx common.Context, store any) common.Context {
	cloudStore, ok := store.(common.CloudContextStore)
	if !ok {
		return ctx
	}
	ctx.KubernetesContextPreflight = common.CloudContextPreflight(cloudStore, common.CloudContextDependencies{})
	return ctx
}

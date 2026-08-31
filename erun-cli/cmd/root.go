package cmd

import (
	"io"
	"os"
	"runtime"
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

// A piped stdout falls back to plain prompts because promptui repaints from its
// own goroutines, so its cursor-control frames' final flush would otherwise race
// process exit.
//
// Windows takes the plain reader for interactive terminals too: the desktop app's
// terminal is a ConPTY, whose repaints re-send promptui's in-place cursor redraws
// as duplicate lines, so each answered prompt echoes twice. A single-write plain
// prompt gives ConPTY nothing to re-paint. Masked prompts stay on promptui so the
// secret input remains hidden — keeping it masked matters more than the repaint.
//
// The promptui branch binds its reader to os.Stdin explicitly: on Windows
// readline otherwise opens the console directly and cannot read piped stdin at
// all, so a forced-TTY scenario (or any scripted run) would read EOF. Passing
// os.Stdin is a no-op for a real interactive terminal — that is already the
// default source — and lets piped input drive the prompt on every host.
func runPrompt(prompt promptui.Prompt) (string, error) {
	if writerIsTerminal(os.Stdout) && (runtime.GOOS != "windows" || prompt.Mask != 0) {
		prompt.Stdin = os.Stdin
		return prompt.Run()
	}
	return runPlainPrompt(prompt)
}

func runSelect(prompt promptui.Select) (int, string, error) {
	if writerIsTerminal(os.Stdout) && runtime.GOOS != "windows" {
		prompt.Stdin = os.Stdin
		return prompt.Run()
	}
	return runPlainSelect(prompt)
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
	dockerRegistryLogin       common.DockerRegistryLoginFunc
	push                      common.DockerPushFunc
	resolveOpen               func(common.OpenParams) (common.OpenResult, error)
	resolveRuntimeDeploySpec  func(common.Context, common.OpenResult, bool) (common.DeploySpec, error)
	activateMCP               MCPForwarder
	activateAPI               APIForwarder
	activateSSHD              SSHDActivator
	runManagedDeploy          func(common.Context, common.OpenResult) error
}

func newRootDependencies() rootDependencies {
	configStore := common.ConfigStore{}
	store := rootStore(configStore)
	dockerRegistryLogin := common.DockerRegistryLoginWithHostedRegistry(store, cloudDependencies())
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
		dockerRegistryLogin:       dockerRegistryLogin,
		push:                      newPushOperation(nil, dockerRegistryLogin, runSelect),
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

func (d rootDependencies) resolveRuntimeDeploySpecForOpenTarget(ctx common.Context, target common.OpenResult, allowLocalBuilds bool) (common.DeploySpec, error) {
	return resolveRuntimeDeploySpecForOpen(ctx, d.store, common.FindProjectRoot, common.ResolveDockerBuildContext, common.ResolveKubernetesDeployContext, time.Now, currentBuildInfo(), target, allowLocalBuilds)
}

func (d rootDependencies) runManagedDeployForOpen(ctx common.Context, target common.OpenResult) error {
	ctx = withCloudContextPreflight(ctx, d.store)
	specs, err := common.ResolveCurrentDeploySpecs(
		ctx,
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
	return common.RunDeploySpecs(ctx, specs, d.recoveringDeployHelmChart)
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
		newStopCmd(d.resolveOpen, d.store.SaveEnvConfig),
		d.sshdCommand(),
		d.pinCommand(),
		devopsCmd,
		d.optionalBuildCommand(),
		d.optionalPushCommand(),
		d.deployCommand(),
		d.resizeCommand(),
		d.publishCommand(),
		d.upgradeCommand(),
		newMCPCmd(d.resolveOpen, d.runInitForArgs, launchMCPProcess),
		newAPICmd(d.resolveOpen, d.runInitForArgs, launchAPIProcess),
		newAppCmd(launchAppProcess),
		newExecCmd(common.FindProjectRoot, common.GitCommandRunner, nil, d.resolveOpen),
		newCloudCmd(d.configStore, runPrompt, runSelect, cloudDependencies()),
		newOrchestratorCmd(d.configStore),
		newContextCmd(d.configStore, runPrompt, runSelect, common.CloudContextDependencies{}),
		newPlatformCmd(d.configStore, runPrompt, cloudDependencies()),
		newReviewCmd(d.configStore, cloudDependencies()),
		newListCmd(d.configStore, common.FindProjectRoot),
		newOutputsCmd(d.resolveOpen),
		newInputsCmd(d.resolveOpen),
		newDoctorCmd(d.resolveOpen, d.configStore, cloudDependencies(), common.CloudContextDependencies{}, runPrompt),
		newObserveCmd(d.resolveOpen),
		newUsageCmd(d.resolveOpen),
		newDeleteCmd(d.configStore, runPrompt, common.DeleteKubernetesNamespace),
		newExposeCmd(d.configStore, common.FindProjectRoot),
		newUnexposeCmd(d.configStore, common.FindProjectRoot),
		newTerraformCmd(d.configStore, common.FindProjectRoot),
		newContributeCmd(common.GitCommandRunner),
		newIdleCmd(d.configStore, d.resolveOpen),
		newWhipCmd(d.configStore, d.resolveOpen),
		deprecatedTopLevelJobCmd(d.resolveOpen),
		newReleaseCmd(d.store, common.FindProjectRoot, common.ResolveDockerBuildContext, time.Now, common.GitCommandRunner, common.BuildScriptRunner, common.DockerImageBuilder, d.dockerRegistryLogin, runSelect, d.push),
		newVersionCmd(func() (versionCommandInfo, error) {
			return resolveVersionCommandBuildInfo(common.FindProjectRoot)
		}, common.ResolveDefaultRuntimeRegistryVersions),
		newActivityCmd(d.configStore, d.resolveOpen),
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

func (d rootDependencies) pinCommand() *cobra.Command {
	return newPinCmd(func(ctx common.Context) common.Context {
		return withCloudContextPreflight(ctx, d.store)
	}, d.resolveOpen, d.store.SaveEnvConfig, d.store.ListEnvConfigs, common.FindProjectRoot)
}

func (d rootDependencies) sshdCommand() *cobra.Command {
	return newSSHDCmd(func(ctx common.Context) common.Context {
		return withCloudContextPreflight(ctx, d.store)
	}, d.resolveOpen, d.store.SaveEnvConfig, d.runInitForOpen, common.FindProjectRoot, d.resolveRuntimeDeploySpec, d.recoveringDeployHelmChart, common.RunRemoteCommand, writeLocalSSHConfig)
}

func (d rootDependencies) containerCommand() *cobra.Command {
	return newCommandGroup(
		"container",
		"Container utilities",
		newBuildCmd(d.store, common.FindProjectRoot, common.ResolveDockerBuildContext, common.ResolveKubernetesDeployContext, time.Now, common.BuildScriptRunner, common.DockerImageBuilder, d.dockerRegistryLogin, runSelect, d.push, d.recoveringDeployHelmChart),
		newPushCmd(d.store, common.FindProjectRoot, common.ResolveDockerBuildContext, time.Now, common.DockerImageBuilder, d.dockerRegistryLogin, runSelect, d.push),
	)
}

func (d rootDependencies) k8sCommand() *cobra.Command {
	return newCommandGroup(
		"k8s",
		"Kubernetes utilities",
		newK8sDeployCmd(d.store, d.store.SaveEnvConfig, common.FindProjectRoot, common.ResolveDockerBuildContext, common.ResolveKubernetesDeployContext, time.Now, common.DockerImageBuilder, d.push, d.recoveringDeployHelmChart),
	)
}

func (d rootDependencies) optionalBuildCommand() *cobra.Command {
	if !hasOptionalBuildCmd(common.FindProjectRoot, common.ResolveDockerBuildContext) {
		return nil
	}
	buildCmd := newBuildCmd(d.store, common.FindProjectRoot, common.ResolveDockerBuildContext, common.ResolveKubernetesDeployContext, time.Now, common.BuildScriptRunner, common.DockerImageBuilder, d.dockerRegistryLogin, runSelect, d.push, d.recoveringDeployHelmChart)
	buildCmd.Short = optionalBuildCmdShort(d.store, common.FindProjectRoot, common.ResolveDockerBuildContext)
	return buildCmd
}

func (d rootDependencies) optionalPushCommand() *cobra.Command {
	if !hasOptionalPushCmd(common.FindProjectRoot, common.ResolveDockerBuildContext) {
		return nil
	}
	pushCmd := newRootPushCmd(d.store, common.FindProjectRoot, common.ResolveDockerBuildContext, time.Now, common.BuildScriptRunner, common.DockerImageBuilder, d.dockerRegistryLogin, runSelect, d.push)
	pushCmd.Short = optionalPushCmdShort(common.FindProjectRoot, common.ResolveDockerBuildContext)
	return pushCmd
}

// Registered unconditionally: the desktop Redeploy button invokes "erun deploy
// --version X" from a cwd that may lack a kubernetes deploy context, so the
// command must exist even where no context resolves.
func (d rootDependencies) deployCommand() *cobra.Command {
	return newDeployCmd(d.store, d.store.SaveEnvConfig, common.FindProjectRoot, common.ResolveDockerBuildContext, common.ResolveKubernetesDeployContext, time.Now, common.DockerImageBuilder, d.push, d.recoveringDeployHelmChart)
}

func (d rootDependencies) resizeCommand() *cobra.Command {
	return newResizeCmd(d.store, d.store.SaveEnvConfig, common.FindProjectRoot, common.ResolveDockerBuildContext, common.ResolveKubernetesDeployContext, d.recoveringDeployHelmChart)
}

func (d rootDependencies) publishCommand() *cobra.Command {
	return newPublishCmd(d.store, common.FindProjectRoot, common.ResolveDockerBuildContext, common.ResolveKubernetesDeployContext, time.Now)
}

func (d rootDependencies) upgradeCommand() *cobra.Command {
	return newUpgradeCmd(d.store, d.store.SaveEnvConfig, common.FindProjectRoot, common.ResolveDockerBuildContext, common.ResolveKubernetesDeployContext, time.Now, common.DockerImageBuilder, d.push, d.recoveringDeployHelmChart)
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

package cmd

import (
	"io"
	"os"
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

// runPrompt and runSelect keep promptui's interactive rendering for real
// terminals and fall back to plain line-based prompts when stdout is a pipe.
// promptui repaints from its own goroutines, so piped output otherwise carries
// cursor-control frames whose final flush races process exit (#520).
func runPrompt(prompt promptui.Prompt) (string, error) {
	if writerIsTerminal(os.Stdout) {
		return prompt.Run()
	}
	return runPlainPrompt(prompt)
}

func runSelect(prompt promptui.Select) (int, string, error) {
	if writerIsTerminal(os.Stdout) {
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
		d.sshdCommand(),
		devopsCmd,
		d.optionalBuildCommand(),
		d.optionalPushCommand(),
		d.deployCommand(),
		d.upgradeCommand(),
		newMCPCmd(d.resolveOpen, d.runInitForArgs, launchMCPProcess),
		newAPICmd(d.resolveOpen, d.runInitForArgs, launchAPIProcess),
		newAppCmd(launchAppProcess),
		newExecCmd(common.FindProjectRoot, common.GitCommandRunner, nil),
		newCloudCmd(d.configStore, runPrompt, runSelect, common.CloudDependencies{}),
		newContextCmd(d.configStore, runPrompt, runSelect, common.CloudContextDependencies{}),
		newListCmd(d.configStore, common.FindProjectRoot),
		newOutputsCmd(d.resolveOpen),
		newDoctorCmd(d.resolveOpen, d.configStore, common.CloudDependencies{}, common.CloudContextDependencies{}, runPrompt),
		newDeleteCmd(d.configStore, runPrompt, common.DeleteKubernetesNamespace),
		newContributeCmd(common.GitCommandRunner),
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
	}, d.resolveOpen, d.store.SaveEnvConfig, d.runInitForOpen, common.FindProjectRoot, d.resolveRuntimeDeploySpec, d.recoveringDeployHelmChart, common.RunRemoteCommand, writeLocalSSHConfig)
}

func (d rootDependencies) containerCommand() *cobra.Command {
	return newCommandGroup(
		"container",
		"Container utilities",
		newBuildCmd(d.store, common.FindProjectRoot, common.ResolveDockerBuildContext, common.ResolveKubernetesDeployContext, time.Now, common.BuildScriptRunner, common.DockerImageBuilder, common.DockerRegistryLogin, runSelect, d.push, d.recoveringDeployHelmChart),
		newPushCmd(d.store, common.FindProjectRoot, common.ResolveDockerBuildContext, time.Now, common.DockerImageBuilder, common.DockerRegistryLogin, runSelect, d.push),
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
	buildCmd := newBuildCmd(d.store, common.FindProjectRoot, common.ResolveDockerBuildContext, common.ResolveKubernetesDeployContext, time.Now, common.BuildScriptRunner, common.DockerImageBuilder, common.DockerRegistryLogin, runSelect, d.push, d.recoveringDeployHelmChart)
	buildCmd.Short = optionalBuildCmdShort(d.store, common.FindProjectRoot, common.ResolveDockerBuildContext)
	return buildCmd
}

func (d rootDependencies) optionalPushCommand() *cobra.Command {
	if !hasOptionalPushCmd(common.FindProjectRoot, common.ResolveDockerBuildContext) {
		return nil
	}
	pushCmd := newRootPushCmd(d.store, common.FindProjectRoot, common.ResolveDockerBuildContext, time.Now, common.BuildScriptRunner, common.DockerImageBuilder, common.DockerRegistryLogin, runSelect, d.push)
	pushCmd.Short = optionalPushCmdShort(common.FindProjectRoot, common.ResolveDockerBuildContext)
	return pushCmd
}

// deployCommand returns the always-registered deploy subcommand. The desktop
// Redeploy button invokes "erun deploy --version X" from the app's cwd, which
// may not contain a kubernetes deploy context. The command must always be
// present so Cobra recognizes its flags; ResolveCurrentDeploySpecs surfaces a
// clear error when invoked outside a deploy context.
func (d rootDependencies) deployCommand() *cobra.Command {
	return newDeployCmd(d.store, d.store.SaveEnvConfig, common.FindProjectRoot, common.ResolveDockerBuildContext, common.ResolveKubernetesDeployContext, time.Now, common.DockerImageBuilder, d.push, d.recoveringDeployHelmChart)
}

// upgradeCommand returns the always-registered upgrade subcommand. It composes
// the same deploy flow as deployCommand for each lagging opted-in env, so it
// shares deployCommand's dependency wiring.
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

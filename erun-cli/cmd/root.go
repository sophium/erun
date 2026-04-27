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
)

func runPrompt(prompt promptui.Prompt) (string, error) {
	return prompt.Run()
}

func runSelect(prompt promptui.Select) (int, string, error) {
	return prompt.Run()
}

func Execute() error {
	configStore := common.ConfigStore{}
	store := rootStore(configStore)
	deployHelmChart := common.WrapHelmChartDeployerWithNamespaceEnsure(ensureKubernetesNamespace, common.DeployHelmChart)
	recoveringDeployHelmChart := wrapHelmDeployWithReleaseRecovery(runPrompt, deployHelmChart, common.ClearHelmReleasePendingOperation)
	runInit := newRunInit(store, common.FindProjectRoot, runPrompt, runSelect, listKubernetesContexts, ensureKubernetesNamespace, common.WaitForShellDeployment, common.RunRemoteCommand, recoveringDeployHelmChart)
	runInitForArgs := newRunInitForArgs(store, runInit)
	runInitForOpen := newRunInitForOpen(store, runInit)
	push := newPushOperation(nil, common.DockerRegistryLogin, runSelect)
	resolveOpen := func(params common.OpenParams) (common.OpenResult, error) {
		return common.ResolveOpen(store, params)
	}
	resolveRuntimeDeploySpec := func(target common.OpenResult) (common.DeploySpec, error) {
		return resolveRuntimeDeploySpecForOpen(store, common.FindProjectRoot, common.ResolveDockerBuildContext, common.ResolveKubernetesDeployContext, time.Now, currentBuildInfo(), target)
	}
	activateMCP := newMCPForwarder()
	activateSSHD := newSSHDActivator(common.RunRemoteCommand)
	runManagedDeploy := func(ctx common.Context, target common.OpenResult) error {
		ctx = withCloudContextPreflight(ctx, store)
		specs, err := common.ResolveCurrentDeploySpecs(
			store,
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
		return common.RunDeploySpecs(ctx, specs, common.DockerImageBuilder, push, recoveringDeployHelmChart)
	}

	initCmd := newInitCmd(runInit)
	openCmd := newOpenCmd(
		func(ctx common.Context) common.Context {
			return withCloudContextPreflight(ctx, store)
		},
		resolveOpen,
		store.SaveEnvConfig,
		runInitForOpen,
		runPrompt,
		newOpenShellRunner(common.WaitForShellDeployment, common.ExecShell),
		runManagedDeploy,
		common.CheckKubernetesDeployment,
		resolveRuntimeDeploySpec,
		deployHelmChart,
		activateMCP,
		activateSSHD,
		launchVSCode,
		launchIntelliJ,
	)
	sshdCmd := newSSHDCmd(func(ctx common.Context) common.Context {
		return withCloudContextPreflight(ctx, store)
	}, resolveOpen, store.SaveEnvConfig, runInitForOpen, resolveRuntimeDeploySpec, recoveringDeployHelmChart, common.RunRemoteCommand, writeLocalSSHConfig)
	containerCmd := newCommandGroup(
		"container",
		"Container utilities",
		newBuildCmd(store, common.FindProjectRoot, common.ResolveDockerBuildContext, common.ResolveKubernetesDeployContext, time.Now, common.BuildScriptRunner, common.DockerImageBuilder, common.DockerRegistryLogin, runSelect, push, recoveringDeployHelmChart),
		newPushCmd(store, common.FindProjectRoot, common.ResolveDockerBuildContext, time.Now, common.DockerImageBuilder, push),
	)
	k8sCmd := newCommandGroup(
		"k8s",
		"Kubernetes utilities",
		newK8sDeployCmd(store, common.FindProjectRoot, common.ResolveDockerBuildContext, common.ResolveKubernetesDeployContext, time.Now, common.DockerImageBuilder, push, recoveringDeployHelmChart),
	)
	devopsCmd := newCommandGroup("devops", "DevOps utilities", containerCmd, k8sCmd)

	var buildCmd *cobra.Command
	if hasOptionalBuildCmd(common.FindProjectRoot, common.ResolveDockerBuildContext) {
		buildCmd = newBuildCmd(store, common.FindProjectRoot, common.ResolveDockerBuildContext, common.ResolveKubernetesDeployContext, time.Now, common.BuildScriptRunner, common.DockerImageBuilder, common.DockerRegistryLogin, runSelect, push, recoveringDeployHelmChart)
		buildCmd.Short = optionalBuildCmdShort(common.FindProjectRoot, common.ResolveDockerBuildContext)
	}
	var pushCmd *cobra.Command
	if hasOptionalPushCmd(common.FindProjectRoot, common.ResolveDockerBuildContext) {
		pushCmd = newPushCmd(store, common.FindProjectRoot, common.ResolveDockerBuildContext, time.Now, common.DockerImageBuilder, push)
		pushCmd.Short = optionalPushCmdShort(common.FindProjectRoot, common.ResolveDockerBuildContext)
	}
	var deployCmd *cobra.Command
	if hasOptionalDeployCmd(common.ResolveKubernetesDeployContext) {
		deployCmd = newDeployCmd(store, common.FindProjectRoot, common.ResolveDockerBuildContext, common.ResolveKubernetesDeployContext, time.Now, common.DockerImageBuilder, push, recoveringDeployHelmChart)
	}

	mcpCmd := newMCPCmd(resolveOpen, runInitForArgs, launchMCPProcess)
	appCmd := newAppCmd(launchAppProcess)
	execCmd := newExecCmd(common.FindProjectRoot, common.GitCommandRunner, nil)
	cloudCmd := newCloudCmd(configStore, runPrompt, runSelect, common.CloudDependencies{})
	contextCmd := newContextCmd(configStore, runPrompt, runSelect, common.CloudContextDependencies{})
	listCmd := newListCmd(configStore, common.FindProjectRoot)
	doctorCmd := newDoctorCmd(resolveOpen, runPrompt)
	deleteCmd := newDeleteCmd(configStore, runPrompt, common.DeleteKubernetesNamespace)
	releaseCmd := newReleaseCmd(common.FindProjectRoot, common.GitCommandRunner)
	versionCmd := newVersionCmd(func() (common.BuildInfo, string, error) {
		return resolveVersionCommandBuildInfo(common.FindProjectRoot)
	}, common.ResolveDefaultRuntimeRegistryVersions)

	runRoot := func(cmd *cobra.Command, args []string) error {
		ctx := withCloudContextPreflight(commandContext(cmd), store)
		result, initRan, err := resolveOpenWithInitStop(ctx, args, shouldInitRootCommand, resolveOpen, runInitForArgs)
		if err != nil {
			return err
		}
		if initRan {
			return nil
		}
		return runResolvedOpenCommand(ctx, result, openOptions{}, runPrompt, newOpenShellRunner(common.WaitForShellDeployment, common.ExecShell), runManagedDeploy, common.CheckKubernetesDeployment, resolveRuntimeDeploySpec, deployHelmChart, activateMCP, activateSSHD, launchVSCode, launchIntelliJ)
	}

	cmd := newRootCommand(runRoot)
	addCommands(cmd, initCmd, openCmd, sshdCmd, devopsCmd, buildCmd, pushCmd, deployCmd, mcpCmd, appCmd, execCmd, cloudCmd, contextCmd, listCmd, doctorCmd, deleteCmd, releaseCmd, versionCmd)
	return cmd.Execute()
}

func withCloudContextPreflight(ctx common.Context, store any) common.Context {
	cloudStore, ok := store.(common.CloudContextStore)
	if !ok {
		return ctx
	}
	ctx.KubernetesContextPreflight = common.CloudContextPreflight(cloudStore, common.CloudContextDependencies{})
	return ctx
}

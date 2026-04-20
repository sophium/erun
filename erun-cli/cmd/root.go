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
	runInit := newRunInit(store, common.FindProjectRoot, runPrompt, runSelect, listKubernetesContexts, ensureKubernetesNamespace, common.WaitForShellDeployment, common.RunRemoteCommand, deployHelmChart)
	runInitForArgs := newRunInitForArgs(store, runInit)
	runInitForOpen := newRunInitForOpen(store, runInit)
	push := newPushOperation(nil, common.DockerRegistryLogin, runSelect)
	resolveOpen := func(params common.OpenParams) (common.OpenResult, error) {
		return common.ResolveOpen(store, params)
	}
	resolveRuntimeDeploySpec := func(target common.OpenResult) (common.DeploySpec, error) {
		return resolveRuntimeDeploySpecForOpen(store, common.FindProjectRoot, common.ResolveDockerBuildContext, common.ResolveKubernetesDeployContext, time.Now, currentBuildInfo(), target)
	}
	activateSSHD := newSSHDActivator(common.RunRemoteCommand)
	runManagedDeploy := func(ctx common.Context, target common.OpenResult) error {
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
		return common.RunDeploySpecs(ctx, specs, common.DockerImageBuilder, push, deployHelmChart)
	}

	initCmd := newInitCmd(runInit)
	openCmd := newOpenCmd(
		resolveOpen,
		store.SaveEnvConfig,
		runInitForOpen,
		runPrompt,
		newOpenShellRunner(common.WaitForShellDeployment, common.ExecShell),
		runManagedDeploy,
		common.CheckKubernetesDeployment,
		resolveRuntimeDeploySpec,
		deployHelmChart,
		activateSSHD,
		launchVSCode,
		launchIntelliJ,
	)
	sshdCmd := newSSHDCmd(resolveOpen, store.SaveEnvConfig, runInitForOpen, resolveRuntimeDeploySpec, deployHelmChart, common.RunRemoteCommand, writeLocalSSHConfig)
	containerCmd := newCommandGroup(
		"container",
		"Container utilities",
		newBuildCmd(store, common.FindProjectRoot, common.ResolveDockerBuildContext, common.ResolveKubernetesDeployContext, time.Now, common.BuildScriptRunner, common.DockerImageBuilder, common.DockerRegistryLogin, runSelect, push, deployHelmChart),
		newPushCmd(store, common.FindProjectRoot, common.ResolveDockerBuildContext, time.Now, common.DockerImageBuilder, push),
	)
	k8sCmd := newCommandGroup(
		"k8s",
		"Kubernetes utilities",
		newK8sDeployCmd(store, common.FindProjectRoot, common.ResolveDockerBuildContext, common.ResolveKubernetesDeployContext, time.Now, common.DockerImageBuilder, push, deployHelmChart),
	)
	devopsCmd := newCommandGroup("devops", "DevOps utilities", containerCmd, k8sCmd)

	var buildCmd *cobra.Command
	if hasOptionalBuildCmd(common.FindProjectRoot, common.ResolveDockerBuildContext) {
		buildCmd = newBuildCmd(store, common.FindProjectRoot, common.ResolveDockerBuildContext, common.ResolveKubernetesDeployContext, time.Now, common.BuildScriptRunner, common.DockerImageBuilder, common.DockerRegistryLogin, runSelect, push, deployHelmChart)
		buildCmd.Short = optionalBuildCmdShort(common.FindProjectRoot, common.ResolveDockerBuildContext)
	}
	var pushCmd *cobra.Command
	if hasOptionalPushCmd(common.FindProjectRoot, common.ResolveDockerBuildContext) {
		pushCmd = newPushCmd(store, common.FindProjectRoot, common.ResolveDockerBuildContext, time.Now, common.DockerImageBuilder, push)
		pushCmd.Short = optionalPushCmdShort(common.FindProjectRoot, common.ResolveDockerBuildContext)
	}
	var deployCmd *cobra.Command
	if hasOptionalDeployCmd(common.ResolveKubernetesDeployContext) {
		deployCmd = newDeployCmd(store, common.FindProjectRoot, common.ResolveDockerBuildContext, common.ResolveKubernetesDeployContext, time.Now, common.DockerImageBuilder, push, deployHelmChart)
	}

	mcpCmd := newMCPCmd(resolveOpen, runInitForArgs, launchMCPProcess)
	appCmd := newAppCmd(launchAppProcess)
	listCmd := newListCmd(configStore, common.FindProjectRoot)
	releaseCmd := newReleaseCmd(common.FindProjectRoot, common.GitCommandRunner)
	versionCmd := newVersionCmd(func() (common.BuildInfo, string, error) {
		return resolveVersionCommandBuildInfo(common.FindProjectRoot)
	})

	runRoot := func(cmd *cobra.Command, args []string) error {
		ctx := commandContext(cmd)
		result, initRan, err := resolveOpenWithInitStop(ctx, args, shouldInitRootCommand, resolveOpen, runInitForArgs)
		if err != nil {
			return err
		}
		if initRan {
			return nil
		}
		return runResolvedOpenCommand(ctx, result, openOptions{}, runPrompt, newOpenShellRunner(common.WaitForShellDeployment, common.ExecShell), runManagedDeploy, common.CheckKubernetesDeployment, resolveRuntimeDeploySpec, deployHelmChart, activateSSHD, launchVSCode, launchIntelliJ)
	}

	cmd := newRootCommand(runRoot)
	addCommands(cmd, initCmd, openCmd, sshdCmd, devopsCmd, buildCmd, pushCmd, deployCmd, mcpCmd, appCmd, listCmd, releaseCmd, versionCmd)
	return cmd.Execute()
}

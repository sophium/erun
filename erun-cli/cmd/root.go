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
	runInit := newRunInit(store, common.FindProjectRoot, runPrompt, runSelect, listKubernetesContexts, ensureKubernetesNamespace)
	runInitForArgs := newRunInitForArgs(store, runInit)
	push := newPushOperation(nil, common.DockerRegistryLogin, runSelect)
	resolveOpen := func(params common.OpenParams) (common.OpenResult, error) {
		return common.ResolveOpen(store, params)
	}
	resolveRuntimeDeploySpec := func(target common.OpenResult) (common.DeploySpec, error) {
		return common.ResolveOpenRuntimeDeploySpec(store, common.FindProjectRoot, common.ResolveDockerBuildContext, common.ResolveKubernetesDeployContext, time.Now, target)
	}

	initCmd := newInitCmd(runInit)
	openCmd := newOpenCmd(
		resolveOpen,
		runInitForArgs,
		runPrompt,
		common.LaunchShell,
		common.CheckKubernetesDeployment,
		resolveRuntimeDeploySpec,
		common.DeployHelmChart,
	)
	containerCmd := newCommandGroup(
		"container",
		"Container utilities",
		newBuildCmd(store, common.FindProjectRoot, common.ResolveDockerBuildContext, common.ResolveKubernetesDeployContext, time.Now, common.BuildScriptRunner, common.DockerImageBuilder, push, common.DeployHelmChart),
		newPushCmd(store, common.FindProjectRoot, common.ResolveDockerBuildContext, time.Now, common.DockerImageBuilder, push),
	)
	k8sCmd := newCommandGroup(
		"k8s",
		"Kubernetes utilities",
		newK8sDeployCmd(store, common.FindProjectRoot, common.ResolveDockerBuildContext, common.ResolveKubernetesDeployContext, time.Now, common.DockerImageBuilder, push, common.DeployHelmChart),
	)
	devopsCmd := newCommandGroup("devops", "DevOps utilities", containerCmd, k8sCmd)

	var buildCmd *cobra.Command
	if hasOptionalBuildCmd(common.FindProjectRoot, common.ResolveDockerBuildContext) {
		buildCmd = newBuildCmd(store, common.FindProjectRoot, common.ResolveDockerBuildContext, common.ResolveKubernetesDeployContext, time.Now, common.BuildScriptRunner, common.DockerImageBuilder, push, common.DeployHelmChart)
		buildCmd.Short = optionalBuildCmdShort(common.FindProjectRoot, common.ResolveDockerBuildContext)
	}
	var pushCmd *cobra.Command
	if hasOptionalPushCmd(common.FindProjectRoot, common.ResolveDockerBuildContext) {
		pushCmd = newPushCmd(store, common.FindProjectRoot, common.ResolveDockerBuildContext, time.Now, common.DockerImageBuilder, push)
		pushCmd.Short = optionalPushCmdShort(common.FindProjectRoot, common.ResolveDockerBuildContext)
	}
	var deployCmd *cobra.Command
	if hasOptionalDeployCmd(common.ResolveKubernetesDeployContext) {
		deployCmd = newDeployCmd(store, common.FindProjectRoot, common.ResolveDockerBuildContext, common.ResolveKubernetesDeployContext, time.Now, common.DockerImageBuilder, push, common.DeployHelmChart)
	}

	mcpCmd := newMCPCmd(resolveOpen, runInitForArgs, launchMCPProcess)
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
		return runResolvedOpenCommand(ctx, result, openOptions{}, runPrompt, common.LaunchShell, common.CheckKubernetesDeployment, resolveRuntimeDeploySpec, common.DeployHelmChart)
	}

	cmd := newRootCommand(runRoot)
	addCommands(cmd, initCmd, openCmd, devopsCmd, buildCmd, pushCmd, deployCmd, mcpCmd, listCmd, releaseCmd, versionCmd)
	return cmd.Execute()
}

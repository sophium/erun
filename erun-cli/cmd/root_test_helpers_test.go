package cmd

import (
	"time"

	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

type testRootDeps struct {
	Store                          rootStore
	FindProjectRoot                common.ProjectFinderFunc
	OptionalBuildFindProjectRoot   common.ProjectFinderFunc
	PromptRunner                   PromptRunner
	SelectRunner                   SelectRunner
	ListKubernetesContexts         KubernetesContextsLister
	EnsureKubernetesNamespace      common.NamespaceEnsurerFunc
	ResolveDockerBuildContext      common.BuildContextResolverFunc
	ResolveKubernetesDeployContext common.DeployContextResolverFunc
	CheckKubernetesDeployment      common.KubernetesDeploymentCheckerFunc
	RunBuildScript                 common.BuildScriptRunnerFunc
	BuildDockerImage               common.DockerImageBuilderFunc
	PushDockerImage                common.DockerImagePusherFunc
	LoginToDockerRegistry          common.DockerRegistryLoginFunc
	DeployHelmChart                common.HelmChartDeployerFunc
	LaunchMCP                      MCPLauncher
	LaunchShell                    common.ShellLauncherFunc
	Now                            common.NowFunc
}

func newTestRootCmd(deps testRootDeps) *cobra.Command {
	store := deps.Store
	if store == nil {
		store = rootStore(common.ConfigStore{})
	}
	listDataStore, ok := any(store).(common.ListStore)
	if !ok {
		listDataStore = common.ConfigStore{}
	}
	findProjectRoot := deps.FindProjectRoot
	if findProjectRoot == nil {
		findProjectRoot = common.FindProjectRoot
	}
	optionalBuildFindProjectRoot := deps.OptionalBuildFindProjectRoot
	if optionalBuildFindProjectRoot == nil {
		optionalBuildFindProjectRoot = common.FindProjectRoot
	}
	promptRunner := deps.PromptRunner
	if promptRunner == nil {
		promptRunner = runPrompt
	}
	selectRunner := deps.SelectRunner
	if selectRunner == nil {
		selectRunner = runSelect
	}
	listKubernetesContexts := deps.ListKubernetesContexts
	resolveDockerBuildContext := deps.ResolveDockerBuildContext
	if resolveDockerBuildContext == nil {
		resolveDockerBuildContext = common.ResolveDockerBuildContext
	}
	resolveKubernetesDeployContext := deps.ResolveKubernetesDeployContext
	if resolveKubernetesDeployContext == nil {
		resolveKubernetesDeployContext = common.ResolveKubernetesDeployContext
	}
	buildDockerImage := deps.BuildDockerImage
	if buildDockerImage == nil {
		buildDockerImage = common.DockerImageBuilder
	}
	runBuildScript := deps.RunBuildScript
	if runBuildScript == nil {
		runBuildScript = common.BuildScriptRunner
	}
	pushDockerImage := deps.PushDockerImage
	if pushDockerImage == nil {
		pushDockerImage = common.DockerImagePusher
	}
	loginToDockerRegistry := deps.LoginToDockerRegistry
	if loginToDockerRegistry == nil {
		loginToDockerRegistry = common.DockerRegistryLogin
	}
	deployHelmChart := deps.DeployHelmChart
	if deployHelmChart == nil {
		deployHelmChart = common.DeployHelmChart
	}
	launchMCP := deps.LaunchMCP
	if launchMCP == nil {
		launchMCP = launchMCPProcess
	}
	launchShell := deps.LaunchShell
	if launchShell == nil {
		launchShell = common.LaunchShell
	}
	now := deps.Now
	if now == nil {
		now = common.NowFunc(time.Now)
	}

	openDeployHelmChart := common.HelmChartDeployerFunc(nil)
	if deps.DeployHelmChart != nil {
		openDeployHelmChart = deployHelmChart
	}

	resolveOpen := func(params common.OpenParams) (common.OpenResult, error) {
		return common.ResolveOpen(store, params)
	}
	resolveRuntimeDeploySpec := func(target common.OpenResult) (common.DeploySpec, error) {
		return common.ResolveOpenRuntimeDeploySpec(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, target)
	}
	ensureKubernetesNamespace := func(contextName, namespace string) error {
		if deps.EnsureKubernetesNamespace == nil {
			return nil
		}
		return deps.EnsureKubernetesNamespace(contextName, namespace)
	}
	runInit := newRunInit(store, findProjectRoot, promptRunner, selectRunner, listKubernetesContexts, ensureKubernetesNamespace)
	runInitForArgs := newRunInitForArgs(store, runInit)
	push := newPushOperation(pushDockerImage, loginToDockerRegistry, selectRunner)

	initCmd := newInitCmd(runInit)
	openCmd := newOpenCmd(resolveOpen, runInitForArgs, launchShell, deps.CheckKubernetesDeployment, resolveRuntimeDeploySpec, openDeployHelmChart)
	containerCmd := newCommandGroup(
		"container",
		"Container utilities",
		newBuildCmd(store, findProjectRoot, resolveDockerBuildContext, now, runBuildScript, buildDockerImage),
		newPushCmd(store, findProjectRoot, resolveDockerBuildContext, now, buildDockerImage, push),
	)
	k8sCmd := newCommandGroup(
		"k8s",
		"Kubernetes utilities",
		newK8sDeployCmd(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, buildDockerImage, push, deployHelmChart),
	)
	devopsCmd := newCommandGroup("devops", "DevOps utilities", containerCmd, k8sCmd)

	var buildCmd *cobra.Command
	if hasOptionalBuildCmd(optionalBuildFindProjectRoot, resolveDockerBuildContext) {
		buildCmd = newBuildCmd(store, findProjectRoot, resolveDockerBuildContext, now, runBuildScript, buildDockerImage)
		buildCmd.Short = optionalBuildCmdShort(optionalBuildFindProjectRoot, resolveDockerBuildContext)
	}
	var pushCmd *cobra.Command
	if hasOptionalPushCmd(optionalBuildFindProjectRoot, resolveDockerBuildContext) {
		pushCmd = newPushCmd(store, findProjectRoot, resolveDockerBuildContext, now, buildDockerImage, push)
		pushCmd.Short = optionalPushCmdShort(optionalBuildFindProjectRoot, resolveDockerBuildContext)
	}
	var deployCmd *cobra.Command
	if hasOptionalDeployCmd(resolveKubernetesDeployContext) {
		deployCmd = newDeployCmd(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, buildDockerImage, push, deployHelmChart)
	}

	mcpCmd := newMCPCmd(resolveOpen, runInitForArgs, launchMCP)
	listCmd := newListCmd(listDataStore, findProjectRoot)
	versionCmd := newVersionCmd(func() (common.BuildInfo, string, error) {
		return resolveVersionCommandBuildInfo(findProjectRoot)
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
		return runResolvedOpenCommand(ctx, result, openOptions{}, launchShell, deps.CheckKubernetesDeployment, resolveRuntimeDeploySpec, openDeployHelmChart)
	}

	cmd := newRootCommand(runRoot)
	addCommands(cmd, initCmd, openCmd, devopsCmd, buildCmd, pushCmd, deployCmd, mcpCmd, listCmd, versionCmd)
	return cmd
}

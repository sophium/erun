package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	RunGit                         common.GitCommandRunnerFunc
	RunBuildScript                 common.BuildScriptRunnerFunc
	BuildDockerImage               common.DockerImageBuilderFunc
	PushDockerImage                common.DockerImagePusherFunc
	LoginToDockerRegistry          common.DockerRegistryLoginFunc
	DeployHelmChart                common.HelmChartDeployerFunc
	LaunchMCP                      MCPLauncher
	ForwardMCP                     MCPForwarder
	LaunchApp                      AppLauncher
	LaunchShell                    common.ShellLauncherFunc
	WaitForRemoteRuntime           common.RemoteRuntimeWaitFunc
	RunRemoteCommand               common.RemoteCommandRunnerFunc
	LaunchVSCode                   VSCodeLauncher
	LaunchIntelliJ                 IntelliJLauncher
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
		optionalBuildFindProjectRoot = func() (string, string, error) {
			return "", "", common.ErrNotInGitRepository
		}
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
	if deps.EnsureKubernetesNamespace != nil {
		deployHelmChart = common.WrapHelmChartDeployerWithNamespaceEnsure(deps.EnsureKubernetesNamespace, deployHelmChart)
	}
	launchMCP := deps.LaunchMCP
	if launchMCP == nil {
		launchMCP = launchMCPProcess
	}
	launchApp := deps.LaunchApp
	if launchApp == nil {
		launchApp = launchAppProcess
	}
	runGit := deps.RunGit
	if runGit == nil {
		runGit = common.GitCommandRunner
	}
	openShell := newOpenShellRunner(common.WaitForShellDeployment, common.ExecShell)
	if deps.LaunchShell != nil {
		openShell = func(_ common.Context, req common.ShellLaunchParams) error {
			return deps.LaunchShell(req)
		}
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
		return resolveRuntimeDeploySpecForOpen(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, currentBuildInfo(), target)
	}
	activateMCP := deps.ForwardMCP
	if activateMCP == nil {
		activateMCP = func(common.Context, common.OpenResult) error { return nil }
	}
	activateSSHD := newSSHDActivator(deps.RunRemoteCommand)
	launchVSCodeCmd := deps.LaunchVSCode
	if launchVSCodeCmd == nil {
		launchVSCodeCmd = launchVSCode
	}
	launchIntelliJCmd := deps.LaunchIntelliJ
	if launchIntelliJCmd == nil {
		launchIntelliJCmd = launchIntelliJ
	}
	push := newPushOperation(pushDockerImage, loginToDockerRegistry, selectRunner)
	runManagedDeploy := func(ctx common.Context, target common.OpenResult) error {
		specs, err := common.ResolveCurrentDeploySpecs(
			store,
			findProjectRoot,
			resolveDockerBuildContext,
			resolveKubernetesDeployContext,
			now,
			common.DeployTarget{
				Tenant:      target.Tenant,
				Environment: target.Environment,
				RepoPath:    target.RepoPath,
			},
		)
		if err != nil {
			return err
		}
		return common.RunDeploySpecs(ctx, specs, buildDockerImage, push, deployHelmChart)
	}
	ensureKubernetesNamespace := func(contextName, namespace string) error {
		if deps.EnsureKubernetesNamespace == nil {
			return nil
		}
		return deps.EnsureKubernetesNamespace(contextName, namespace)
	}
	runInit := newRunInit(store, findProjectRoot, promptRunner, selectRunner, listKubernetesContexts, ensureKubernetesNamespace, deps.WaitForRemoteRuntime, deps.RunRemoteCommand, deployHelmChart)
	runInitForArgs := newRunInitForArgs(store, runInit)
	runInitForOpen := newRunInitForOpen(store, runInit)

	initCmd := newInitCmd(runInit)
	openCmd := newOpenCmd(resolveOpen, store.SaveEnvConfig, runInitForOpen, promptRunner, openShell, runManagedDeploy, deps.CheckKubernetesDeployment, resolveRuntimeDeploySpec, openDeployHelmChart, activateMCP, activateSSHD, launchVSCodeCmd, launchIntelliJCmd)
	sshdCmd := newSSHDCmd(resolveOpen, store.SaveEnvConfig, runInitForOpen, resolveRuntimeDeploySpec, openDeployHelmChart, deps.RunRemoteCommand, writeLocalSSHConfig)
	containerCmd := newCommandGroup(
		"container",
		"Container utilities",
		newBuildCmd(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, runBuildScript, buildDockerImage, loginToDockerRegistry, selectRunner, push, deployHelmChart),
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
		buildCmd = newBuildCmd(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, runBuildScript, buildDockerImage, loginToDockerRegistry, selectRunner, push, deployHelmChart)
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
	appCmd := newAppCmd(launchApp)
	listCmd := newListCmd(listDataStore, findProjectRoot)
	doctorCmd := newDoctorCmd(resolveOpen, promptRunner)
	releaseCmd := newReleaseCmd(findProjectRoot, runGit)
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
		return runResolvedOpenCommand(ctx, result, openOptions{}, promptRunner, openShell, runManagedDeploy, deps.CheckKubernetesDeployment, resolveRuntimeDeploySpec, openDeployHelmChart, activateMCP, activateSSHD, launchVSCodeCmd, launchIntelliJCmd)
	}

	cmd := newRootCommand(runRoot)
	addCommands(cmd, initCmd, openCmd, sshdCmd, devopsCmd, buildCmd, pushCmd, deployCmd, mcpCmd, appCmd, listCmd, doctorCmd, releaseCmd, versionCmd)
	return cmd
}

func stubKubectlContexts(t *testing.T, contexts []string, current string) {
	t.Helper()

	kubectlDir := t.TempDir()
	kubectlPath := filepath.Join(kubectlDir, "kubectl")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"config\" ] && [ \"$2\" = \"get-contexts\" ] && [ \"$3\" = \"-o=name\" ]; then\n" +
		"  cat <<'EOF'\n" + strings.Join(contexts, "\n") + "\nEOF\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = \"config\" ] && [ \"$2\" = \"current-context\" ]; then\n" +
		"  printf '%s\\n' '" + current + "'\n" +
		"  exit 0\n" +
		"fi\n" +
		"echo \"unexpected kubectl invocation: $@\" >&2\n" +
		"exit 1\n"
	if err := os.WriteFile(kubectlPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write kubectl stub: %v", err)
	}
	t.Setenv("PATH", kubectlDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

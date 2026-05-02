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
	DeleteKubernetesNamespace      common.NamespaceDeleterFunc
	RunGit                         common.GitCommandRunnerFunc
	RunRawCommand                  common.RawCommandRunnerFunc
	RunBuildScript                 common.BuildScriptRunnerFunc
	BuildDockerImage               common.DockerImageBuilderFunc
	PushDockerImage                common.DockerImagePusherFunc
	LoginToDockerRegistry          common.DockerRegistryLoginFunc
	DeployHelmChart                common.HelmChartDeployerFunc
	RecoverHelmRelease             common.HelmReleaseRecovererFunc
	LaunchMCP                      MCPLauncher
	LaunchAPI                      APILauncher
	ForwardMCP                     MCPForwarder
	ForwardAPI                     APIForwarder
	LaunchApp                      AppLauncher
	LaunchShell                    common.ShellLauncherFunc
	WaitForRemoteRuntime           common.RemoteRuntimeWaitFunc
	RunRemoteCommand               common.RemoteCommandRunnerFunc
	LaunchVSCode                   VSCodeLauncher
	LaunchIntelliJ                 IntelliJLauncher
	ResolveRuntimeRegistryVersions common.RuntimeRegistryVersionResolverFunc
	Now                            common.NowFunc
}

func requireNoError(t *testing.T, err error, context string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", context, err)
	}
}

func requireMkdirAll(t *testing.T, path string, perm os.FileMode, context string) {
	t.Helper()
	requireNoError(t, os.MkdirAll(path, perm), context)
}

func requireWriteFile(t *testing.T, path string, data []byte, perm os.FileMode, context string) {
	t.Helper()
	requireNoError(t, os.WriteFile(path, data, perm), context)
}

func requireStringSlicesEqual(t *testing.T, got, want []string, context string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: got %v want %v", context, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s: got %v want %v", context, got, want)
		}
	}
}

func testRootStoreOrDefault(store rootStore) rootStore {
	if store == nil {
		return rootStore(common.ConfigStore{})
	}
	return store
}

func testListStoreOrDefault(store rootStore) common.ListStore {
	listDataStore, ok := any(store).(common.ListStore)
	if !ok {
		return common.ConfigStore{}
	}
	return listDataStore
}

func testCloudStoreOrDefault(store rootStore) cloudCommandStoreInterface {
	cloudStore, ok := any(store).(cloudCommandStoreInterface)
	if !ok {
		return common.ConfigStore{}
	}
	return cloudStore
}

func testDeleteStoreOrDefault(store rootStore) common.DeleteStore {
	deleteStore, ok := any(store).(common.DeleteStore)
	if !ok {
		return common.ConfigStore{}
	}
	return deleteStore
}

func testNamespaceDeleterOrDefault(deleteNamespace common.NamespaceDeleterFunc) common.NamespaceDeleterFunc {
	if deleteNamespace == nil {
		return common.DeleteKubernetesNamespace
	}
	return deleteNamespace
}

func testProjectFinderOrDefault(findProjectRoot common.ProjectFinderFunc) common.ProjectFinderFunc {
	if findProjectRoot == nil {
		return common.FindProjectRoot
	}
	return findProjectRoot
}

func testOptionalProjectFinderOrDefault(findProjectRoot common.ProjectFinderFunc) common.ProjectFinderFunc {
	if findProjectRoot == nil {
		return func() (string, string, error) {
			return "", "", common.ErrNotInGitRepository
		}
	}
	return findProjectRoot
}

func testPromptRunnerOrDefault(promptRunner PromptRunner) PromptRunner {
	if promptRunner == nil {
		return runPrompt
	}
	return promptRunner
}

func testSelectRunnerOrDefault(selectRunner SelectRunner) SelectRunner {
	if selectRunner == nil {
		return runSelect
	}
	return selectRunner
}

func testDockerBuildContextResolverOrDefault(resolve common.BuildContextResolverFunc) common.BuildContextResolverFunc {
	if resolve == nil {
		return common.ResolveDockerBuildContext
	}
	return resolve
}

func testKubernetesDeployContextResolverOrDefault(resolve common.DeployContextResolverFunc) common.DeployContextResolverFunc {
	if resolve == nil {
		return common.ResolveKubernetesDeployContext
	}
	return resolve
}

func testDockerImageBuilderOrDefault(build common.DockerImageBuilderFunc) common.DockerImageBuilderFunc {
	if build == nil {
		return common.DockerImageBuilder
	}
	return build
}

func testBuildScriptRunnerOrDefault(run common.BuildScriptRunnerFunc) common.BuildScriptRunnerFunc {
	if run == nil {
		return common.BuildScriptRunner
	}
	return run
}

func testDockerImagePusherOrDefault(push common.DockerImagePusherFunc) common.DockerImagePusherFunc {
	if push == nil {
		return common.DockerImagePusher
	}
	return push
}

func testDockerRegistryLoginOrDefault(login common.DockerRegistryLoginFunc) common.DockerRegistryLoginFunc {
	if login == nil {
		return common.DockerRegistryLogin
	}
	return login
}

func testHelmDeployerOrDefault(deploy common.HelmChartDeployerFunc, ensure common.NamespaceEnsurerFunc) common.HelmChartDeployerFunc {
	if deploy == nil {
		deploy = common.DeployHelmChart
	}
	if ensure != nil {
		return common.WrapHelmChartDeployerWithNamespaceEnsure(ensure, deploy)
	}
	return deploy
}

func testMCPLauncherOrDefault(launch MCPLauncher) MCPLauncher {
	if launch == nil {
		return launchMCPProcess
	}
	return launch
}

func testAPILauncherOrDefault(launch APILauncher) APILauncher {
	if launch == nil {
		return launchAPIProcess
	}
	return launch
}

func testAppLauncherOrDefault(launch AppLauncher) AppLauncher {
	if launch == nil {
		return launchAppProcess
	}
	return launch
}

func testGitRunnerOrDefault(runGit common.GitCommandRunnerFunc) common.GitCommandRunnerFunc {
	if runGit == nil {
		return common.GitCommandRunner
	}
	return runGit
}

func testOpenShellRunnerOrDefault(launchShell common.ShellLauncherFunc) OpenShellRunner {
	if launchShell == nil {
		return newOpenShellRunner(common.WaitForShellDeployment, common.ExecShell)
	}
	return func(_ common.Context, req common.ShellLaunchParams) error {
		return launchShell(req)
	}
}

func testNowOrDefault(now common.NowFunc) common.NowFunc {
	if now == nil {
		return common.NowFunc(time.Now)
	}
	return now
}

func testOpenHelmDeployer(deploy common.HelmChartDeployerFunc, openDeploy common.HelmChartDeployerFunc) common.HelmChartDeployerFunc {
	if deploy == nil {
		return nil
	}
	return openDeploy
}

func testMCPForwarderOrDefault(forward MCPForwarder) MCPForwarder {
	if forward == nil {
		return func(common.Context, common.OpenResult) error { return nil }
	}
	return forward
}

func testAPIForwarderOrDefault(forward APIForwarder) APIForwarder {
	if forward == nil {
		return func(common.Context, common.OpenResult) error { return nil }
	}
	return forward
}

func testVSCodeLauncherOrDefault(launch VSCodeLauncher) VSCodeLauncher {
	if launch == nil {
		return launchVSCode
	}
	return launch
}

func testIntelliJLauncherOrDefault(launch IntelliJLauncher) IntelliJLauncher {
	if launch == nil {
		return launchIntelliJ
	}
	return launch
}

type testRootCmdParts struct {
	deps                           testRootDeps
	store                          rootStore
	listDataStore                  common.ListStore
	findProjectRoot                common.ProjectFinderFunc
	optionalBuildFindProjectRoot   common.ProjectFinderFunc
	promptRunner                   PromptRunner
	selectRunner                   SelectRunner
	resolveDockerBuildContext      common.BuildContextResolverFunc
	resolveKubernetesDeployContext common.DeployContextResolverFunc
	buildDockerImage               common.DockerImageBuilderFunc
	runBuildScript                 common.BuildScriptRunnerFunc
	loginToDockerRegistry          common.DockerRegistryLoginFunc
	recoveringDeployHelmChart      common.HelmChartDeployerFunc
	launchMCP                      MCPLauncher
	launchAPI                      APILauncher
	launchApp                      AppLauncher
	runGit                         common.GitCommandRunnerFunc
	openShell                      OpenShellRunner
	now                            common.NowFunc
	openDeployHelmChart            common.HelmChartDeployerFunc
	resolveOpen                    func(common.OpenParams) (common.OpenResult, error)
	resolveRuntimeDeploySpec       func(common.OpenResult) (common.DeploySpec, error)
	activateMCP                    MCPForwarder
	activateAPI                    APIForwarder
	activateSSHD                   SSHDActivator
	launchVSCodeCmd                VSCodeLauncher
	launchIntelliJCmd              IntelliJLauncher
	push                           common.DockerPushFunc
	runManagedDeploy               func(common.Context, common.OpenResult) error
	runInit                        func(common.Context, common.BootstrapInitParams) error
	runInitForArgs                 func(common.Context, []string) error
	runInitForOpen                 func(common.Context, common.OpenParams) error
}

func newTestRootCmd(deps testRootDeps) *cobra.Command {
	store := testRootStoreOrDefault(deps.Store)
	listDataStore := testListStoreOrDefault(store)
	findProjectRoot := testProjectFinderOrDefault(deps.FindProjectRoot)
	optionalBuildFindProjectRoot := testOptionalProjectFinderOrDefault(deps.OptionalBuildFindProjectRoot)
	promptRunner := testPromptRunnerOrDefault(deps.PromptRunner)
	selectRunner := testSelectRunnerOrDefault(deps.SelectRunner)
	listKubernetesContexts := deps.ListKubernetesContexts
	resolveDockerBuildContext := testDockerBuildContextResolverOrDefault(deps.ResolveDockerBuildContext)
	resolveKubernetesDeployContext := testKubernetesDeployContextResolverOrDefault(deps.ResolveKubernetesDeployContext)
	buildDockerImage := testDockerImageBuilderOrDefault(deps.BuildDockerImage)
	runBuildScript := testBuildScriptRunnerOrDefault(deps.RunBuildScript)
	pushDockerImage := testDockerImagePusherOrDefault(deps.PushDockerImage)
	loginToDockerRegistry := testDockerRegistryLoginOrDefault(deps.LoginToDockerRegistry)
	deployHelmChart := testHelmDeployerOrDefault(deps.DeployHelmChart, deps.EnsureKubernetesNamespace)
	recoveringDeployHelmChart := wrapHelmDeployWithReleaseRecovery(promptRunner, deployHelmChart, deps.RecoverHelmRelease)
	launchMCP := testMCPLauncherOrDefault(deps.LaunchMCP)
	launchAPI := testAPILauncherOrDefault(deps.LaunchAPI)
	launchApp := testAppLauncherOrDefault(deps.LaunchApp)
	runGit := testGitRunnerOrDefault(deps.RunGit)
	openShell := testOpenShellRunnerOrDefault(deps.LaunchShell)
	now := testNowOrDefault(deps.Now)
	openDeployHelmChart := testOpenHelmDeployer(deps.DeployHelmChart, deployHelmChart)

	resolveOpen := func(params common.OpenParams) (common.OpenResult, error) {
		return common.ResolveOpen(store, params)
	}
	resolveRuntimeDeploySpec := func(target common.OpenResult) (common.DeploySpec, error) {
		return resolveRuntimeDeploySpecForOpen(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, currentBuildInfo(), target)
	}
	activateMCP := testMCPForwarderOrDefault(deps.ForwardMCP)
	activateAPI := testAPIForwarderOrDefault(deps.ForwardAPI)
	activateSSHD := newSSHDActivator(deps.RunRemoteCommand)
	launchVSCodeCmd := testVSCodeLauncherOrDefault(deps.LaunchVSCode)
	launchIntelliJCmd := testIntelliJLauncherOrDefault(deps.LaunchIntelliJ)
	push := newPushOperation(pushDockerImage, loginToDockerRegistry, selectRunner)
	runManagedDeploy := func(ctx common.Context, target common.OpenResult) error {
		ctx = withCloudContextPreflight(ctx, store)
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
		return common.RunDeploySpecs(ctx, specs, buildDockerImage, push, recoveringDeployHelmChart)
	}
	ensureKubernetesNamespace := func(contextName, namespace string) error {
		if deps.EnsureKubernetesNamespace == nil {
			return nil
		}
		return deps.EnsureKubernetesNamespace(contextName, namespace)
	}
	runInit := newRunInit(store, findProjectRoot, promptRunner, selectRunner, listKubernetesContexts, ensureKubernetesNamespace, deps.WaitForRemoteRuntime, deps.RunRemoteCommand, recoveringDeployHelmChart)
	runInitForArgs := newRunInitForArgs(store, runInit)
	runInitForOpen := newRunInitForOpen(store, runInit)

	return assembleTestRootCmd(testRootCmdParts{
		deps:                           deps,
		store:                          store,
		listDataStore:                  listDataStore,
		findProjectRoot:                findProjectRoot,
		optionalBuildFindProjectRoot:   optionalBuildFindProjectRoot,
		promptRunner:                   promptRunner,
		selectRunner:                   selectRunner,
		resolveDockerBuildContext:      resolveDockerBuildContext,
		resolveKubernetesDeployContext: resolveKubernetesDeployContext,
		buildDockerImage:               buildDockerImage,
		runBuildScript:                 runBuildScript,
		loginToDockerRegistry:          loginToDockerRegistry,
		recoveringDeployHelmChart:      recoveringDeployHelmChart,
		launchMCP:                      launchMCP,
		launchAPI:                      launchAPI,
		launchApp:                      launchApp,
		runGit:                         runGit,
		openShell:                      openShell,
		now:                            now,
		openDeployHelmChart:            openDeployHelmChart,
		resolveOpen:                    resolveOpen,
		resolveRuntimeDeploySpec:       resolveRuntimeDeploySpec,
		activateMCP:                    activateMCP,
		activateAPI:                    activateAPI,
		activateSSHD:                   activateSSHD,
		launchVSCodeCmd:                launchVSCodeCmd,
		launchIntelliJCmd:              launchIntelliJCmd,
		push:                           push,
		runManagedDeploy:               runManagedDeploy,
		runInit:                        runInit,
		runInitForArgs:                 runInitForArgs,
		runInitForOpen:                 runInitForOpen,
	})
}

func assembleTestRootCmd(parts testRootCmdParts) *cobra.Command {
	initCmd := newInitCmd(parts.runInit)
	openCmd := newOpenCmd(func(ctx common.Context) common.Context {
		return withCloudContextPreflight(ctx, parts.store)
	}, parts.resolveOpen, parts.store.SaveEnvConfig, parts.runInitForOpen, parts.promptRunner, parts.openShell, parts.runManagedDeploy, parts.deps.CheckKubernetesDeployment, parts.resolveRuntimeDeploySpec, parts.openDeployHelmChart, parts.activateMCP, parts.activateAPI, parts.activateSSHD, parts.launchVSCodeCmd, parts.launchIntelliJCmd)
	sshdCmd := newSSHDCmd(func(ctx common.Context) common.Context {
		return withCloudContextPreflight(ctx, parts.store)
	}, parts.resolveOpen, parts.store.SaveEnvConfig, parts.runInitForOpen, parts.resolveRuntimeDeploySpec, parts.openDeployHelmChart, parts.deps.RunRemoteCommand, writeLocalSSHConfig)
	containerCmd := newCommandGroup(
		"container",
		"Container utilities",
		newBuildCmd(parts.store, parts.findProjectRoot, parts.resolveDockerBuildContext, parts.resolveKubernetesDeployContext, parts.now, parts.runBuildScript, parts.buildDockerImage, parts.loginToDockerRegistry, parts.selectRunner, parts.push, parts.recoveringDeployHelmChart),
		newPushCmd(parts.store, parts.findProjectRoot, parts.resolveDockerBuildContext, parts.now, parts.buildDockerImage, parts.push),
	)
	k8sCmd := newCommandGroup(
		"k8s",
		"Kubernetes utilities",
		newK8sDeployCmd(parts.store, parts.findProjectRoot, parts.resolveDockerBuildContext, parts.resolveKubernetesDeployContext, parts.now, parts.buildDockerImage, parts.push, parts.recoveringDeployHelmChart),
	)
	devopsCmd := newCommandGroup("devops", "DevOps utilities", containerCmd, k8sCmd)
	buildCmd := optionalTestBuildCmd(parts)
	pushCmd := optionalTestPushCmd(parts)
	deployCmd := optionalTestDeployCmd(parts)
	versionCmd := newVersionCmd(func() (common.BuildInfo, string, error) {
		return resolveVersionCommandBuildInfo(parts.findProjectRoot)
	}, parts.deps.ResolveRuntimeRegistryVersions)
	runRoot := testRootRunner(parts)
	cmd := newRootCommand(runRoot)
	addCommands(cmd,
		initCmd, openCmd, sshdCmd, devopsCmd, buildCmd, pushCmd, deployCmd,
		newMCPCmd(parts.resolveOpen, parts.runInitForArgs, parts.launchMCP),
		newAPICmd(parts.resolveOpen, parts.runInitForArgs, parts.launchAPI),
		newAppCmd(parts.launchApp),
		newExecCmd(parts.findProjectRoot, parts.runGit, parts.deps.RunRawCommand),
		newCloudCmd(testCloudStoreOrDefault(parts.store), parts.promptRunner, parts.selectRunner, common.CloudDependencies{}),
		newListCmd(parts.listDataStore, parts.findProjectRoot),
		newDoctorCmd(parts.resolveOpen, parts.promptRunner),
		newDeleteCmd(testDeleteStoreOrDefault(parts.store), parts.promptRunner, testNamespaceDeleterOrDefault(parts.deps.DeleteKubernetesNamespace)),
		newReleaseCmd(parts.findProjectRoot, parts.runGit),
		versionCmd,
	)
	return cmd
}

func optionalTestBuildCmd(parts testRootCmdParts) *cobra.Command {
	if !hasOptionalBuildCmd(parts.optionalBuildFindProjectRoot, parts.resolveDockerBuildContext) {
		return nil
	}
	buildCmd := newBuildCmd(parts.store, parts.findProjectRoot, parts.resolveDockerBuildContext, parts.resolveKubernetesDeployContext, parts.now, parts.runBuildScript, parts.buildDockerImage, parts.loginToDockerRegistry, parts.selectRunner, parts.push, parts.recoveringDeployHelmChart)
	buildCmd.Short = optionalBuildCmdShort(parts.optionalBuildFindProjectRoot, parts.resolveDockerBuildContext)
	return buildCmd
}

func optionalTestPushCmd(parts testRootCmdParts) *cobra.Command {
	if !hasOptionalPushCmd(parts.optionalBuildFindProjectRoot, parts.resolveDockerBuildContext) {
		return nil
	}
	pushCmd := newPushCmd(parts.store, parts.findProjectRoot, parts.resolveDockerBuildContext, parts.now, parts.buildDockerImage, parts.push)
	pushCmd.Short = optionalPushCmdShort(parts.optionalBuildFindProjectRoot, parts.resolveDockerBuildContext)
	return pushCmd
}

func optionalTestDeployCmd(parts testRootCmdParts) *cobra.Command {
	if !hasOptionalDeployCmd(parts.resolveKubernetesDeployContext) {
		return nil
	}
	return newDeployCmd(parts.store, parts.findProjectRoot, parts.resolveDockerBuildContext, parts.resolveKubernetesDeployContext, parts.now, parts.buildDockerImage, parts.push, parts.recoveringDeployHelmChart)
}

func testRootRunner(parts testRootCmdParts) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		ctx := withCloudContextPreflight(commandContext(cmd), parts.store)
		result, initRan, err := resolveOpenWithInitStop(ctx, args, shouldInitRootCommand, parts.resolveOpen, parts.runInitForArgs)
		if err != nil {
			return err
		}
		if initRan {
			return nil
		}
		return runResolvedOpenCommandWithAPI(ctx, result, openOptions{}, parts.promptRunner, parts.openShell, parts.runManagedDeploy, parts.deps.CheckKubernetesDeployment, parts.resolveRuntimeDeploySpec, parts.openDeployHelmChart, parts.activateMCP, parts.activateAPI, parts.activateSSHD, parts.launchVSCodeCmd, parts.launchIntelliJCmd)
	}
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
	requireNoError(t, os.WriteFile(kubectlPath, []byte(script), 0o755), "write kubectl stub")
	t.Setenv("PATH", kubectlDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	common "github.com/sophium/erun/erun-common"
	"github.com/sophium/erun/internal"
	"github.com/spf13/cobra"
)

type openNoShellDialect string

const (
	openNoShellDialectPOSIX      openNoShellDialect = "posix"
	openNoShellDialectPowerShell openNoShellDialect = "powershell"
)

var currentHostOS = func() common.HostOS { return common.DetectHost().OS }

func newOpenCmd(prepareContext func(common.Context) common.Context, resolveOpen func(common.OpenParams) (common.OpenResult, error), saveEnvConfig func(string, common.EnvConfig) error, runInitForOpen func(common.Context, common.OpenParams) error, promptRunner PromptRunner, openShell OpenShellRunner, runManagedDeploy func(common.Context, common.OpenResult) error, checkKubernetesDeployment common.KubernetesDeploymentCheckerFunc, resolveRuntimeDeploySpec func(common.OpenResult) (common.DeploySpec, error), deployHelmChart common.HelmChartDeployerFunc, activateMCP MCPForwarder, activateAPI APIForwarder, activateSSHD SSHDActivator, launchVSCode VSCodeLauncher, launchIntelliJ IntelliJLauncher) *cobra.Command {
	var noShell bool
	var vscode bool
	var intellij bool
	var snapshot bool
	var noSnapshot bool
	var noAliasPrompt bool
	var versionOverride string
	var runtimeImage string
	target := common.OpenParams{}

	cmd := &cobra.Command{
		Use:          "open [TENANT] [ENVIRONMENT]",
		Short:        "Open a shell in the tenant environment worktree",
		Args:         cobra.MaximumNArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := commandContext(cmd)
			if prepareContext != nil {
				ctx = prepareContext(ctx)
			}
			if vscode && intellij {
				return fmt.Errorf("--vscode and --intellij cannot be used together")
			}
			params, err := resolveOpenParams(args, target)
			if err != nil {
				return err
			}
			result, initRan, err := resolveOpenWithInitStopForParams(ctx, params, shouldRunInitForOpenCommand, resolveOpen, runInitForOpen)
			if err != nil {
				return err
			}
			if initRan {
				return nil
			}
			snapshotOverride, err := resolveSnapshotFlagOverride(cmd, snapshot, noSnapshot)
			if err != nil {
				return err
			}
			result, err = applyOpenSnapshotPreference(result, snapshotOverride, saveEnvConfig)
			if err != nil {
				return err
			}
			return runResolvedOpenCommandWithAPI(ctx, result, openOptions{
				NoShell:         noShell,
				NoAliasPrompt:   noAliasPrompt,
				VSCode:          vscode,
				IntelliJ:        intellij,
				VersionOverride: versionOverride,
				RuntimeImage:    runtimeImage,
				SaveEnvConfig:   saveEnvConfig,
			}, promptRunner, openShell, runManagedDeploy, checkKubernetesDeployment, resolveRuntimeDeploySpec, deployHelmChart, activateMCP, activateAPI, activateSSHD, launchVSCode, launchIntelliJ)
		},
	}

	addDryRunFlag(cmd)
	cmd.Flags().StringVar(&target.Tenant, "tenant", "", "Open a specific tenant")
	cmd.Flags().StringVar(&target.Environment, "environment", "", "Open a specific environment")
	cmd.Flags().BoolVar(&noShell, "no-shell", false, "Print shell commands to switch kubectl context, namespace, and worktree locally")
	cmd.Flags().BoolVar(&noAliasPrompt, "no-alias-prompt", false, "Skip prompting to add a local shell alias with --no-shell")
	cmd.Flags().BoolVar(&vscode, "vscode", false, "Open the remote environment in VS Code instead of a shell")
	cmd.Flags().BoolVar(&intellij, "intellij", false, "Open the remote environment in IntelliJ IDEA instead of a shell")
	cmd.Flags().StringVar(&versionOverride, "version", "", "Override the runtime chart and image version before opening")
	cmd.Flags().StringVar(&runtimeImage, "runtime-image", "", "Override the runtime image repository before opening")
	addSnapshotFlags(cmd, &snapshot, &noSnapshot, "Build and deploy a local snapshot when opening the local environment")
	return cmd
}

type openOptions struct {
	NoShell         bool
	NoAliasPrompt   bool
	VSCode          bool
	IntelliJ        bool
	VersionOverride string
	RuntimeImage    string
	SaveEnvConfig   func(string, common.EnvConfig) error
}

func applyOpenSnapshotPreference(result common.OpenResult, enabled *bool, saveEnvConfig func(string, common.EnvConfig) error) (common.OpenResult, error) {
	if enabled == nil || !strings.EqualFold(strings.TrimSpace(result.Environment), common.DefaultEnvironment) {
		return result, nil
	}

	result.EnvConfig.SetSnapshot(*enabled)
	if saveEnvConfig == nil {
		return result, nil
	}
	if err := saveEnvConfig(result.Tenant, result.EnvConfig); err != nil {
		return common.OpenResult{}, err
	}
	return result, nil
}

func persistOpenRuntimeVersion(result common.OpenResult, version string, saveEnvConfig func(string, common.EnvConfig) error) (common.OpenResult, error) {
	version = strings.TrimSpace(version)
	if version == "" || saveEnvConfig == nil {
		return result, nil
	}

	updated := result.EnvConfig
	changed := false
	if strings.TrimSpace(updated.RuntimeVersion) != version {
		updated.RuntimeVersion = version
		changed = true
	}
	snapshot := strings.Contains(version, "-snapshot-")
	if updated.Snapshot == nil || *updated.Snapshot != snapshot {
		updated.SetSnapshot(snapshot)
		changed = true
	}
	if !changed {
		return result, nil
	}

	result.EnvConfig = updated
	if err := saveEnvConfig(result.Tenant, updated); err != nil {
		return common.OpenResult{}, err
	}
	return result, nil
}

func resolveOpenArgs(args []string, resolveOpen func(common.OpenParams) (common.OpenResult, error)) (common.OpenParams, common.OpenResult, error) {
	params, err := common.OpenParamsForArgs(args)
	if err != nil {
		return common.OpenParams{}, common.OpenResult{}, err
	}

	result, err := resolveOpen(params)
	return params, result, err
}

func resolveOpenParams(args []string, overrides common.OpenParams) (common.OpenParams, error) {
	params, err := common.OpenParamsForArgs(args)
	if err != nil {
		return common.OpenParams{}, err
	}
	if tenant := strings.TrimSpace(overrides.Tenant); tenant != "" {
		params.Tenant = tenant
	}
	if environment := strings.TrimSpace(overrides.Environment); environment != "" {
		params.Environment = environment
	}

	switch {
	case strings.TrimSpace(params.Tenant) == "" && strings.TrimSpace(params.Environment) == "":
		params.UseDefaultTenant = true
		params.UseDefaultEnvironment = true
	case strings.TrimSpace(params.Tenant) == "":
		params.UseDefaultTenant = true
		params.UseDefaultEnvironment = false
	case strings.TrimSpace(params.Environment) == "":
		params.UseDefaultTenant = false
		params.UseDefaultEnvironment = true
	default:
		params.UseDefaultTenant = false
		params.UseDefaultEnvironment = false
	}

	return params, nil
}

func runInitBeforeOpen(ctx common.Context, args []string, runInitForArgs func(common.Context, []string) error) error {
	ctx.Logger.Debug("running init before resolving open target")
	return runInitForArgs(ctx, args)
}

func runInitBeforeOpenForParams(ctx common.Context, params common.OpenParams, runInitForOpen func(common.Context, common.OpenParams) error) error {
	ctx.Logger.Debug("running init before resolving open target")
	return runInitForOpen(ctx, params)
}

func resolveOpenWithInitStop(ctx common.Context, args []string, shouldRunInit func(error) bool, resolveOpen func(common.OpenParams) (common.OpenResult, error), runInitForArgs func(common.Context, []string) error) (common.OpenResult, bool, error) {
	_, result, err := resolveOpenArgs(args, resolveOpen)
	if !shouldRunInit(err) {
		return result, false, err
	}

	if initErr := runInitBeforeOpen(ctx, args, runInitForArgs); initErr != nil {
		return common.OpenResult{}, true, initErr
	}

	return common.OpenResult{}, true, nil
}

func resolveOpenWithInitRetry(ctx common.Context, args []string, shouldRunInit func(error) bool, resolveOpen func(common.OpenParams) (common.OpenResult, error), runInitForArgs func(common.Context, []string) error) (common.OpenResult, bool, error) {
	params, result, err := resolveOpenArgs(args, resolveOpen)
	if !shouldRunInit(err) {
		return result, false, err
	}

	if initErr := runInitBeforeOpen(ctx, args, runInitForArgs); initErr != nil {
		return common.OpenResult{}, true, initErr
	}

	result, err = resolveOpen(params)
	return result, true, err
}

func resolveOpenWithInitStopForParams(ctx common.Context, params common.OpenParams, shouldRunInit func(error) bool, resolveOpen func(common.OpenParams) (common.OpenResult, error), runInitForOpen func(common.Context, common.OpenParams) error) (common.OpenResult, bool, error) {
	result, err := resolveOpen(params)
	if !shouldRunInit(err) {
		return result, false, err
	}

	if initErr := runInitBeforeOpenForParams(ctx, params, runInitForOpen); initErr != nil {
		return common.OpenResult{}, true, initErr
	}

	return common.OpenResult{}, true, nil
}

func resolveOpenWithInitRetryForParams(ctx common.Context, params common.OpenParams, shouldRunInit func(error) bool, resolveOpen func(common.OpenParams) (common.OpenResult, error), runInitForOpen func(common.Context, common.OpenParams) error) (common.OpenResult, bool, error) {
	result, err := resolveOpen(params)
	if !shouldRunInit(err) {
		return result, false, err
	}

	if initErr := runInitBeforeOpenForParams(ctx, params, runInitForOpen); initErr != nil {
		return common.OpenResult{}, true, initErr
	}

	result, err = resolveOpen(params)
	return result, true, err
}

func runResolvedOpenCommand(ctx common.Context, result common.OpenResult, options openOptions, promptRunner PromptRunner, openShell OpenShellRunner, runManagedDeploy func(common.Context, common.OpenResult) error, checkKubernetesDeployment common.KubernetesDeploymentCheckerFunc, resolveRuntimeDeploySpec func(common.OpenResult) (common.DeploySpec, error), deployHelmChart common.HelmChartDeployerFunc, activateMCP MCPForwarder, activateSSHD SSHDActivator, launchVSCode VSCodeLauncher, launchIntelliJ IntelliJLauncher) error {
	return runResolvedOpenCommandWithAPI(ctx, result, options, promptRunner, openShell, runManagedDeploy, checkKubernetesDeployment, resolveRuntimeDeploySpec, deployHelmChart, activateMCP, nil, activateSSHD, launchVSCode, launchIntelliJ)
}

func runResolvedOpenCommandWithAPI(ctx common.Context, result common.OpenResult, options openOptions, promptRunner PromptRunner, openShell OpenShellRunner, runManagedDeploy func(common.Context, common.OpenResult) error, checkKubernetesDeployment common.KubernetesDeploymentCheckerFunc, resolveRuntimeDeploySpec func(common.OpenResult) (common.DeploySpec, error), deployHelmChart common.HelmChartDeployerFunc, activateMCP MCPForwarder, activateAPI APIForwarder, activateSSHD SSHDActivator, launchVSCode VSCodeLauncher, launchIntelliJ IntelliJLauncher) error {
	runner := resolvedOpenRunner{
		ctx:                       ctx,
		result:                    result,
		options:                   options,
		promptRunner:              promptRunner,
		openShell:                 openShell,
		runManagedDeploy:          runManagedDeploy,
		checkKubernetesDeployment: checkKubernetesDeployment,
		resolveRuntimeDeploySpec:  resolveRuntimeDeploySpec,
		deployHelmChart:           deployHelmChart,
		activateMCP:               activateMCP,
		activateAPI:               activateAPI,
		activateSSHD:              activateSSHD,
		launchVSCode:              launchVSCode,
		launchIntelliJ:            launchIntelliJ,
	}
	return runner.run()
}

type resolvedOpenRunner struct {
	ctx                       common.Context
	result                    common.OpenResult
	options                   openOptions
	promptRunner              PromptRunner
	openShell                 OpenShellRunner
	runManagedDeploy          func(common.Context, common.OpenResult) error
	checkKubernetesDeployment common.KubernetesDeploymentCheckerFunc
	resolveRuntimeDeploySpec  func(common.OpenResult) (common.DeploySpec, error)
	deployHelmChart           common.HelmChartDeployerFunc
	activateMCP               MCPForwarder
	activateAPI               APIForwarder
	activateSSHD              SSHDActivator
	launchVSCode              VSCodeLauncher
	launchIntelliJ            IntelliJLauncher
}

func (r *resolvedOpenRunner) run() error {
	if err := r.ctx.EnsureKubernetesContext(r.result.EnvConfig.KubernetesContext); err != nil {
		return err
	}
	r.recordActivity()
	if err := r.validateIDEOptions(); err != nil {
		return err
	}

	shellReq := common.ShellLaunchParamsFromResult(r.result)
	if err := r.maybeDeployRuntime(shellReq); err != nil {
		return err
	}
	if err := r.activateForwarders(); err != nil {
		return err
	}
	if launched, err := r.maybeLaunchIDE(); launched || err != nil {
		return err
	}
	if r.options.NoShell {
		return r.emitNoShellSetup()
	}

	r.traceShellPreview(shellReq)
	if r.ctx.DryRun {
		return nil
	}
	return r.runShellLoop(shellReq)
}

func (r *resolvedOpenRunner) recordActivity() {
	if !r.ctx.DryRun && os.Getenv("ERUN_IDLE_PROBE") != "1" {
		_ = common.RecordEnvironmentActivity(common.EnvironmentActivityParams{
			Tenant:      r.result.Tenant,
			Environment: r.result.Environment,
			Kind:        common.ActivityKindCLI,
		})
	}
}

func (r *resolvedOpenRunner) validateIDEOptions() error {
	if r.options.VSCode && r.options.IntelliJ {
		return fmt.Errorf("--vscode and --intellij cannot be used together")
	}
	if (r.options.VSCode || r.options.IntelliJ) && !r.result.EnvConfig.SSHD.Enabled {
		flag := "--vscode"
		if r.options.IntelliJ {
			flag = "--intellij"
		}
		return fmt.Errorf("%s requires sshd-enabled remote environment; run `erun sshd init %s %s` first", flag, r.result.Tenant, r.result.Environment)
	}
	return nil
}

func (r *resolvedOpenRunner) maybeDeployRuntime(shellReq common.ShellLaunchParams) error {
	if r.resolveRuntimeDeploySpec == nil || r.deployHelmChart == nil {
		return nil
	}
	execution, err := r.resolveRuntimeExecution()
	if err != nil {
		return err
	}
	shouldDeploy, err := r.shouldDeployRuntime(shellReq, execution)
	if err != nil {
		return err
	}
	if !shouldDeploy {
		return nil
	}
	return r.deployRuntime(execution)
}

func (r *resolvedOpenRunner) resolveRuntimeExecution() (common.DeploySpec, error) {
	execution, err := r.resolveRuntimeDeploySpec(r.result)
	if err != nil {
		return common.DeploySpec{}, err
	}
	execution, err = maybeCreateMissingRuntimeChart(r.ctx, r.result, r.promptRunner, r.resolveRuntimeDeploySpec, execution)
	if err != nil {
		return common.DeploySpec{}, err
	}
	execution, err = applyRuntimeDeployImageOverride(r.result, execution, r.options.RuntimeImage)
	if err != nil {
		return common.DeploySpec{}, err
	}
	return applyRuntimeDeployVersionOverride(execution, r.options.VersionOverride), nil
}

func (r *resolvedOpenRunner) shouldDeployRuntime(shellReq common.ShellLaunchParams, execution common.DeploySpec) (bool, error) {
	if len(execution.Builds) > 0 || strings.TrimSpace(r.options.VersionOverride) != "" || strings.TrimSpace(r.options.RuntimeImage) != "" {
		return true, nil
	}
	if r.checkKubernetesDeployment == nil {
		return false, nil
	}
	deployed, err := r.checkKubernetesDeployment(common.KubernetesDeploymentCheckParams{
		Name:               common.RuntimeReleaseName(r.result.Tenant),
		Namespace:          common.KubernetesNamespaceName(r.result.Tenant, r.result.Environment),
		KubernetesContext:  r.result.EnvConfig.KubernetesContext,
		ExpectedRepoPath:   common.RemoteShellWorktreePath(shellReq),
		ExpectedSSHD:       sshdExpectationForDeployment(r.result),
		ExpectedMCPPort:    common.MCPPortForResult(r.result),
		ExpectedAPIPort:    common.APIPortForResult(r.result),
		ExpectedSSHPort:    common.SSHLocalPortForResult(r.result),
		ExpectedRuntimePod: r.result.EnvConfig.RuntimePod,
	})
	if err != nil {
		return false, err
	}
	return !deployed, nil
}

func (r *resolvedOpenRunner) deployRuntime(execution common.DeploySpec) error {
	if r.options.VSCode || r.options.IntelliJ {
		return fmt.Errorf("opening %s requires updating the runtime deployment for %s/%s; run `erun sshd init %s %s` or `erun open %s %s` first, then retry", ideOpenLabel(r.options), r.result.Tenant, r.result.Environment, r.result.Tenant, r.result.Environment, r.result.Tenant, r.result.Environment)
	}
	if r.result.EnvConfig.SSHD.Enabled {
		execution.Deploy.SSHDEnabled = true
	}
	r.ctx.Logger.Debug("deploying the devops runtime before opening the shell")
	if err := common.RunDeploySpec(r.ctx, execution, common.DockerImageBuilder, runOpenDockerPush, r.openHelmDeployer(execution)); err != nil {
		return err
	}
	return r.persistRuntimeVersion(execution.Deploy.Version)
}

func runOpenDockerPush(ctx common.Context, pushInput common.DockerPushSpec) error {
	return common.RunDockerPush(ctx, pushInput, common.DockerImagePusher)
}

func (r *resolvedOpenRunner) openHelmDeployer(execution common.DeploySpec) common.HelmChartDeployerFunc {
	return wrapHelmDeployWithReleaseRecovery(
		r.promptRunner,
		wrapOpenHelmDeployWithSpinner(r.ctx, execution.Deploy.ReleaseName, r.deployHelmChart),
		common.ClearHelmReleasePendingOperation,
	)
}

func (r *resolvedOpenRunner) persistRuntimeVersion(version string) error {
	if r.ctx.DryRun {
		return nil
	}
	result, err := persistOpenRuntimeVersion(r.result, version, r.options.SaveEnvConfig)
	if err != nil {
		return err
	}
	r.result = result
	return nil
}

func (r *resolvedOpenRunner) activateForwarders() error {
	if r.activateSSHD != nil && r.result.EnvConfig.SSHD.Enabled {
		if err := r.activateSSHD(r.ctx, r.result); err != nil {
			return err
		}
	}
	if r.activateMCP == nil {
		if r.activateAPI == nil {
			return nil
		}
		return r.activateAPI(r.ctx, r.result)
	}
	if err := r.activateMCP(r.ctx, r.result); err != nil {
		return err
	}
	if r.activateAPI == nil {
		return nil
	}
	return r.activateAPI(r.ctx, r.result)
}

func (r *resolvedOpenRunner) maybeLaunchIDE() (bool, error) {
	if r.options.VSCode {
		if r.launchVSCode == nil {
			return true, fmt.Errorf("VS Code launcher is required")
		}
		return true, r.launchVSCode(r.ctx, r.result)
	}
	if r.options.IntelliJ {
		if r.launchIntelliJ == nil {
			return true, fmt.Errorf("IntelliJ launcher is required")
		}
		return true, r.launchIntelliJ(r.ctx, r.result, r.promptRunner)
	}
	return false, nil
}

func (r *resolvedOpenRunner) emitNoShellSetup() error {
	namespace := common.KubernetesNamespaceName(r.result.Tenant, r.result.Environment)
	r.ctx.TraceCommand("", "kubectl", "config", "use-context", strings.TrimSpace(r.result.EnvConfig.KubernetesContext))
	r.ctx.TraceCommand("", "kubectl", "config", "set-context", "--current", "--namespace="+namespace)
	r.ctx.TraceCommand("", "cd", r.result.RepoPath)
	promptRunner := r.promptRunner
	if r.options.NoAliasPrompt {
		promptRunner = nil
	}
	return emitLocalShellSetupForOpenResult(r.result, promptRunner, r.ctx.Stdout, r.ctx.Stderr)
}

func (r *resolvedOpenRunner) traceShellPreview(shellReq common.ShellLaunchParams) {
	if preview, err := common.PreviewShellLaunch(shellReq); err == nil {
		r.ctx.TraceCommand("", "kubectl", preview.WaitArgs...)
		execArgs := append([]string{}, preview.ExecArgs...)
		if len(execArgs) > 0 {
			execArgs[len(execArgs)-1] = "<bootstrap-script>"
		}
		r.ctx.TraceCommand("", "kubectl", execArgs...)
		r.ctx.TraceBlock("bootstrap-script", preview.Script)
	} else {
		r.ctx.Logger.Debug("unable to render remote shell bootstrap trace: " + err.Error())
	}
}

func (r *resolvedOpenRunner) runShellLoop(shellReq common.ShellLaunchParams) error {
	for {
		err := r.openShell(r.ctx, shellReq)
		if !errors.Is(err, common.ErrShellReattachDeploy) {
			return err
		}
		if r.runManagedDeploy == nil {
			return err
		}
		if err := r.runManagedDeploy(r.ctx, r.result); err != nil {
			return err
		}
	}
}

func ideOpenLabel(options openOptions) string {
	if options.IntelliJ {
		return "IntelliJ IDEA"
	}
	if options.VSCode {
		return "VS Code"
	}
	return "the IDE"
}

func sshdExpectationForDeployment(result common.OpenResult) *bool {
	if !result.EnvConfig.SSHD.Enabled {
		return nil
	}
	expected := true
	return &expected
}

func wrapOpenHelmDeployWithSpinner(ctx common.Context, releaseName string, deployHelmChart common.HelmChartDeployerFunc) common.HelmChartDeployerFunc {
	if deployHelmChart == nil {
		return nil
	}
	return func(params common.HelmDeployParams) error {
		return runWithSpinner(
			ctx,
			" deploying "+releaseName+" with helm",
			"deployment updated: "+releaseName+"\n",
			func() error {
				return deployHelmChart(params)
			},
		)
	}
}

func maybeCreateMissingRuntimeChart(ctx common.Context, result common.OpenResult, promptRunner PromptRunner, resolveRuntimeDeploySpec func(common.OpenResult) (common.DeploySpec, error), execution common.DeploySpec) (common.DeploySpec, error) {
	if ctx.DryRun || promptRunner == nil || resolveRuntimeDeploySpec == nil {
		return execution, nil
	}
	if result.RemoteRepo() {
		return execution, nil
	}
	if !common.IsDefaultDevopsChartPath(execution.Deploy.ChartPath) {
		return execution, nil
	}

	moduleName := common.RuntimeReleaseName(result.Tenant)
	ok, err := confirmPrompt(promptRunner, fmt.Sprintf("create %s chart in %s", moduleName, result.RepoPath))
	if err != nil {
		return common.DeploySpec{}, err
	}
	if !ok {
		return execution, nil
	}

	if err := common.EnsureDefaultDevopsChart(ctx, result.RepoPath, result.Tenant, result.Environment); err != nil {
		return common.DeploySpec{}, err
	}

	return resolveRuntimeDeploySpec(result)
}

func applyRuntimeDeployImageOverride(result common.OpenResult, execution common.DeploySpec, runtimeImage string) (common.DeploySpec, error) {
	runtimeImage = strings.TrimSpace(runtimeImage)
	if runtimeImage == "" {
		return execution, nil
	}
	if result.RemoteRepo() {
		return common.ResolveDefaultDevopsDeploySpecWithImage(result, runtimeImage)
	}
	if !common.IsDefaultDevopsChartPath(execution.Deploy.ChartPath) {
		return execution, nil
	}
	return common.ResolveDefaultDevopsDeploySpecWithImage(result, runtimeImage)
}

func applyRuntimeDeployVersionOverride(execution common.DeploySpec, versionOverride string) common.DeploySpec {
	versionOverride = strings.TrimSpace(versionOverride)
	if versionOverride == "" {
		return execution
	}
	execution.Builds = nil
	execution.Deploy.Version = versionOverride
	return execution
}

func emitLocalShellSetupForOpenResult(result common.OpenResult, promptRunner PromptRunner, stdout, stderr io.Writer) error {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}

	dialect := openNoShellDialectForShell(os.Getenv("SHELL"))
	if file, ok := stdout.(*os.File); ok {
		if info, err := file.Stat(); err == nil && (info.Mode()&os.ModeCharDevice) != 0 {
			if err := maybeConfigureOpenNoShellAlias(result, promptRunner, os.Getenv("SHELL"), stderr); err != nil {
				return err
			}
		}
	}

	_, err := io.WriteString(stdout, localShellSetupScript(result, dialect))
	return err
}

func maybeConfigureOpenNoShellAlias(result common.OpenResult, promptRunner PromptRunner, shellPath string, stderr io.Writer) error {
	dialect := openNoShellDialectForShell(shellPath)
	aliasName := openNoShellAliasName(result)
	startupFile, aliasConfigured := detectOpenNoShellAliasStartupFile(result, shellPath)
	if aliasConfigured {
		writeOpenNoShellHintLines(stderr, result, shellPath)
		return nil
	}
	if startupFile == "" || promptRunner == nil || dialect == openNoShellDialectPowerShell {
		writeOpenNoShellHintLines(stderr, result, shellPath)
		return nil
	}

	ok, err := confirmPrompt(promptRunner, fmt.Sprintf("add %s to %s", aliasName, startupFile))
	if err != nil {
		return err
	}
	if !ok {
		writeOpenNoShellHintLines(stderr, result, shellPath)
		return nil
	}

	if err := appendOpenNoShellAlias(startupFile, openNoShellAliasCommand(result, shellPath)); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stderr, "added %s to %s\n", aliasName, startupFile)
	_, _ = fmt.Fprintf(stderr, "open a new shell to use %s\n", aliasName)
	return nil
}

func writeOpenNoShellHintLines(stderr io.Writer, result common.OpenResult, shellPath string) {
	for _, line := range openNoShellHintLines(result, shellPath) {
		_, _ = fmt.Fprintln(stderr, line)
	}
}

func openNoShellHintLines(result common.OpenResult, shellPath string) []string {
	dialect := openNoShellDialectForShell(shellPath)
	aliasName := openNoShellAliasName(result)
	aliasCommand := openNoShellAliasCommand(result, shellPath)
	startupFile, aliasConfigured := detectOpenNoShellAliasStartupFile(result, shellPath)
	if aliasConfigured {
		return []string{
			fmt.Sprintf("configured in your shell startup file: open a new shell to use %s", aliasName),
		}
	}
	if startupFile == "" || dialect == openNoShellDialectPowerShell {
		return []string{
			openNoShellHintPrefix(dialect),
			aliasCommand,
		}
	}
	return []string{
		openNoShellHintPrefix(dialect),
		aliasCommand,
	}
}

func openNoShellAliasName(result common.OpenResult) string {
	if strings.TrimSpace(result.Title) != "" {
		return strings.TrimSpace(result.Title)
	}
	return strings.TrimSpace(result.Tenant) + "-" + strings.TrimSpace(result.Environment)
}

func openNoShellAliasCommand(result common.OpenResult, shellPath string) string {
	aliasName := openNoShellAliasName(result)
	command := fmt.Sprintf("erun open %s %s --no-shell", result.Tenant, result.Environment)
	dialect := openNoShellDialectForShell(shellPath)
	if dialect == openNoShellDialectPowerShell {
		return "function " + aliasName + " { " + command + " | Invoke-Expression }"
	}
	if filepath.Base(strings.TrimSpace(shellPath)) == "fish" {
		return "alias " + aliasName + " 'eval (" + command + ")'"
	}
	return "alias " + aliasName + `='eval "$(` + command + `)"'`
}

func detectOpenNoShellAliasStartupFile(result common.OpenResult, shellPath string) (string, bool) {
	if openNoShellDialectForShell(shellPath) == openNoShellDialectPowerShell {
		return "", false
	}
	homeDir, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(homeDir) == "" {
		return "", false
	}

	preferred, candidates := openNoShellStartupFiles(homeDir, shellPath)
	for _, candidate := range candidates {
		configured, err := startupFileHasAlias(candidate, openNoShellAliasName(result))
		if err != nil {
			continue
		}
		if configured {
			return candidate, true
		}
	}
	return preferred, false
}

func openNoShellStartupFiles(homeDir, shellPath string) (string, []string) {
	switch filepath.Base(strings.TrimSpace(shellPath)) {
	case "bash":
		preferred := filepath.Join(homeDir, ".bashrc")
		return preferred, []string{
			preferred,
			filepath.Join(homeDir, ".bash_profile"),
			filepath.Join(homeDir, ".profile"),
		}
	case "fish":
		preferred := filepath.Join(homeDir, ".config", "fish", "config.fish")
		return preferred, []string{preferred}
	default:
		preferred := filepath.Join(homeDir, ".zshrc")
		return preferred, []string{preferred}
	}
}

func startupFileHasAlias(path, aliasName string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}

	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "alias "+aliasName+"=") || strings.HasPrefix(trimmed, "alias "+aliasName+" ") {
			return true, nil
		}
	}
	return false, nil
}

func appendOpenNoShellAlias(path, aliasCommand string) error {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if strings.Contains(string(data), aliasCommand) {
		return nil
	}

	content := string(data)
	if strings.TrimSpace(content) != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += aliasCommand + "\n"

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func openNoShellDialectForShell(shellPath string) openNoShellDialect {
	return detectOpenNoShellDialect(currentHostOS(), shellPath)
}

func detectOpenNoShellDialect(hostOS common.HostOS, shellPath string) openNoShellDialect {
	switch strings.ToLower(filepath.Base(strings.TrimSpace(shellPath))) {
	case "pwsh", "pwsh.exe", "powershell", "powershell.exe":
		return openNoShellDialectPowerShell
	case "bash", "bash.exe", "zsh", "zsh.exe", "sh", "sh.exe", "fish", "fish.exe":
		return openNoShellDialectPOSIX
	}
	if hostOS == common.HostOSWindows {
		return openNoShellDialectPowerShell
	}
	return openNoShellDialectPOSIX
}

func localShellSetupScript(result common.OpenResult, dialect openNoShellDialect) string {
	switch dialect {
	case openNoShellDialectPowerShell:
		commands := []string{
			"kubectl config use-context " + powerShellQuote(strings.TrimSpace(result.EnvConfig.KubernetesContext)) + " | Out-Null",
			"kubectl config set-context --current " + powerShellQuote("--namespace="+common.KubernetesNamespaceName(result.Tenant, result.Environment)) + " | Out-Null",
			"Set-Location -LiteralPath " + powerShellQuote(result.RepoPath),
		}
		return strings.Join(commands, "\n") + "\n"
	default:
		return common.LocalShellSetupScript(result)
	}
}

func openNoShellHintPrefix(dialect openNoShellDialect) string {
	if dialect == openNoShellDialectPowerShell {
		return "one-liner function:"
	}
	return "one-liner alias:"
}

func powerShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func openerIsDefaultError(err error) bool {
	return errors.Is(err, common.ErrDefaultTenantNotConfigured) ||
		errors.Is(err, common.ErrDefaultEnvironmentNotConfigured) ||
		errors.Is(err, common.ErrNotInitialized)
}

func shouldInitOpenCommand(err error) bool {
	return errors.Is(err, common.ErrKubernetesContextNotConfigured)
}

func shouldRunInitForOpenCommand(err error) bool {
	return shouldInitRootCommand(err) ||
		errors.Is(err, common.ErrTenantNotFound) ||
		errors.Is(err, common.ErrEnvironmentNotFound)
}

func shouldInitRootCommand(err error) bool {
	return openerIsDefaultError(err) ||
		shouldInitOpenCommand(err) ||
		errors.Is(err, common.ErrNotInGitRepository) ||
		internal.IsReported(err)
}

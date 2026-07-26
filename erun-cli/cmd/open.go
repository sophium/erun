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

func newOpenCmd(prepareContext func(common.Context) common.Context, resolveOpen func(common.OpenParams) (common.OpenResult, error), saveEnvConfig func(string, common.EnvConfig) error, runInitForOpen func(common.Context, common.OpenParams) error, promptRunner PromptRunner, openShell OpenShellRunner, runManagedDeploy func(common.Context, common.OpenResult) error, checkKubernetesDeployment common.KubernetesDeploymentCheckerFunc, resolveRuntimeDeploySpec func(common.Context, common.OpenResult, bool) (common.DeploySpec, error), deployHelmChart common.HelmChartDeployerFunc, activateMCP MCPForwarder, activateAPI APIForwarder, activateSSHD SSHDActivator, launchVSCode VSCodeLauncher, launchIntelliJ IntelliJLauncher) *cobra.Command {
	var noShell bool
	var vscode bool
	var intellij bool
	var noAliasPrompt bool
	var versionOverride string
	var runtimeImage string
	var appSession string
	var deployRuntime bool
	var aiTab bool
	var contributeTab bool
	target := common.OpenParams{}

	cmd := &cobra.Command{
		Use:   "open [TENANT] [ENVIRONMENT]",
		Short: "Open a shell in the tenant environment",
		Long: "Open a shell in the tenant environment.\n\n" +
			"open is a pure primitive: it starts the port-forwards and drops you into a shell " +
			"against the runtime that is already deployed. It does not deploy on its own — run " +
			"`erun deploy` first, or pass --deploy to deploy before opening (the operator-convenience " +
			"shortcut: builds-here envs build→push→deploy, runtime envs install the current version). " +
			"Use --vscode or --intellij to open an IDE instead, or --no-shell to print the setup " +
			"commands for your current shell.",
		Example:      "  erun open\n  erun open team dev\n  erun open team dev --deploy\n  erun open team dev --vscode",
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
			result, err = common.EnsureLocalPortRangePersisted(ctx, saveEnvConfig, result)
			if err != nil {
				return err
			}
			allowLocalBuilds := result.EnvConfig.BuildsHere()
			return runResolvedOpenCommandWithAPI(ctx, result, openOptions{
				NoShell:          noShell,
				NoAliasPrompt:    noAliasPrompt,
				VSCode:           vscode,
				IntelliJ:         intellij,
				VersionOverride:  versionOverride,
				RuntimeImage:     runtimeImage,
				AllowLocalBuilds: allowLocalBuilds,
				SaveEnvConfig:    saveEnvConfig,
				AppSession:       strings.TrimSpace(appSession),
				AI:               aiTab,
				Contribute:       contributeTab,
				// A --version or --runtime-image override also implies a deploy:
				// pinning a version is only meaningful if it rolls out.
				Deploy: deployRuntime || strings.TrimSpace(versionOverride) != "" || strings.TrimSpace(runtimeImage) != "",
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
	// Desktop-integration flags: the app runs the remote shell as a persistent,
	// reattachable session so closing and reopening a tab reconnects to the
	// running shell. Hidden because they only make sense when the desktop
	// manages the session lifecycle.
	cmd.Flags().StringVar(&appSession, "app-session", "", "Reattach to a persistent terminal session with this id")
	cmd.Flags().BoolVar(&aiTab, "ai", false, "Launch the configured AI tool as the persistent session's program")
	cmd.Flags().BoolVar(&contributeTab, "contribute", false, "Start the persistent session in the contribute clone")
	cmd.Flags().BoolVar(&deployRuntime, "deploy", false, "Deploy the runtime before opening (operator convenience: builds-here envs build→push→deploy, runtime envs install the current version)")
	_ = cmd.Flags().MarkHidden("app-session")
	_ = cmd.Flags().MarkHidden("ai")
	_ = cmd.Flags().MarkHidden("contribute")
	return cmd
}

type openOptions struct {
	NoShell          bool
	NoAliasPrompt    bool
	VSCode           bool
	IntelliJ         bool
	VersionOverride  string
	RuntimeImage     string
	AllowLocalBuilds bool
	SaveEnvConfig    func(string, common.EnvConfig) error
	AppSession       string
	AI               bool
	Deploy           bool
	Contribute       bool
}

func persistOpenRuntimeVersion(result common.OpenResult, version, registry string, saveEnvConfig func(string, common.EnvConfig) error) (common.OpenResult, error) {
	version = strings.TrimSpace(version)
	registry = strings.TrimSpace(registry)
	if version == "" || saveEnvConfig == nil {
		return result, nil
	}

	updated := result.EnvConfig
	if strings.TrimSpace(updated.RuntimeVersion) == version && strings.TrimSpace(updated.RuntimeRegistry) == registry {
		return result, nil
	}
	updated.RuntimeVersion = version
	updated.RuntimeRegistry = registry

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

func runResolvedOpenCommandWithAPI(ctx common.Context, result common.OpenResult, options openOptions, promptRunner PromptRunner, openShell OpenShellRunner, runManagedDeploy func(common.Context, common.OpenResult) error, checkKubernetesDeployment common.KubernetesDeploymentCheckerFunc, resolveRuntimeDeploySpec func(common.Context, common.OpenResult, bool) (common.DeploySpec, error), deployHelmChart common.HelmChartDeployerFunc, activateMCP MCPForwarder, activateAPI APIForwarder, activateSSHD SSHDActivator, launchVSCode VSCodeLauncher, launchIntelliJ IntelliJLauncher) error {
	ctx, closeEnvTrace := common.ActivateEnvTrace(ctx, result.Tenant, result.Environment)
	defer closeEnvTrace()
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
		resolveDeployedVersion:    common.ResolveDeployedHelmReleaseVersion,
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
	resolveRuntimeDeploySpec  func(common.Context, common.OpenResult, bool) (common.DeploySpec, error)
	deployHelmChart           common.HelmChartDeployerFunc
	resolveDeployedVersion    common.HelmReleaseVersionResolverFunc
	activateMCP               MCPForwarder
	activateAPI               APIForwarder
	activateSSHD              SSHDActivator
	launchVSCode              VSCodeLauncher
	launchIntelliJ            IntelliJLauncher
}

func (r *resolvedOpenRunner) run() error {
	r.ctx.Trace(fmt.Sprintf("open: tenant=%s environment=%s kubernetes-context=%s remote=%v no-shell=%v ide=%s",
		r.result.Tenant, r.result.Environment,
		r.result.EnvConfig.KubernetesContext, r.result.RemoteRepo(),
		r.options.NoShell, openIDEKindLabel(r.options)))
	if err := r.ctx.EnsureKubernetesContext(r.result.EnvConfig.KubernetesContext); err != nil {
		return err
	}
	r.recordActivity()
	if err := r.validateIDEOptions(); err != nil {
		return err
	}

	shellReq := common.ShellLaunchParamsFromResult(r.result)
	shellReq.AppSession = r.options.AppSession
	shellReq.AI = r.options.AI
	shellReq.Contribute = r.options.Contribute
	if r.options.Deploy {
		// --deploy is operator-convenience only (root AGENTS.md § "Command
		// primitives vs orchestration"): programmatic callers like the desktop
		// must NOT use it — they compose build→push→deploy themselves and open
		// the pure shell.
		if err := r.maybeDeployRuntime(shellReq); err != nil {
			return err
		}
	} else {
		r.ctx.Trace("open: pure primitive — not deploying (run `erun deploy` first, or pass --deploy to deploy before opening)")
		if err := r.ensureRuntimeDeployed(); err != nil {
			return err
		}
	}
	r.activateForwarders()
	if launched, err := r.maybeLaunchIDE(); launched || err != nil {
		return err
	}
	if r.options.NoShell {
		r.ctx.Trace("open: --no-shell selected, emitting setup commands instead of launching shell")
		return r.emitNoShellSetup()
	}

	r.traceShellPreview(shellReq)
	if r.ctx.DryRun {
		r.ctx.Trace("open: dry-run complete; would have launched shell")
		return nil
	}
	return r.runShellLoop(shellReq)
}

func openIDEKindLabel(options openOptions) string {
	switch {
	case options.VSCode:
		return "vscode"
	case options.IntelliJ:
		return "intellij"
	default:
		return "shell"
	}
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
	// Heal envs created by older binaries: their tenant Chart.yaml is pinned to
	// the literal "1.0.0" placeholder, so helm keeps pulling an erun-mcp:1.0.0
	// image that was never published and every rollout fails at pod startup.
	appVersion := strings.TrimSpace(r.result.EnvConfig.RuntimeVersion)
	if appVersion == "" {
		appVersion = currentBuildInfo().Version
	}
	if err := common.MigrateDefaultDevopsChartAppVersion(r.ctx, r.result.RepoPath, r.result.Tenant, appVersion); err != nil {
		return common.DeploySpec{}, err
	}
	execution, err := r.resolveRuntimeDeploySpec(r.ctx, r.result, r.options.AllowLocalBuilds)
	if err != nil {
		return common.DeploySpec{}, err
	}
	execution, err = applyRuntimeDeployImageOverride(r.ctx, r.result, execution, r.options.RuntimeImage)
	if err != nil {
		return common.DeploySpec{}, err
	}
	return applyRuntimeDeployVersionOverride(execution, r.options.VersionOverride), nil
}

func (r *resolvedOpenRunner) shouldDeployRuntime(shellReq common.ShellLaunchParams, execution common.DeploySpec) (bool, error) {
	if strings.TrimSpace(r.options.VersionOverride) != "" || strings.TrimSpace(r.options.RuntimeImage) != "" {
		return true, nil
	}
	if r.checkKubernetesDeployment == nil {
		return false, nil
	}
	deployed, err := r.checkKubernetesDeployment(r.ctx, common.KubernetesDeploymentCheckParams{
		Name:               common.RuntimeReleaseName(r.result.Tenant),
		Namespace:          common.KubernetesNamespaceName(r.result.Tenant, r.result.Environment),
		KubernetesContext:  r.result.EnvConfig.KubernetesContext,
		ExpectedRepoPath:   common.RemoteShellWorktreePath(shellReq),
		ExpectedSSHD:       sshdExpectationForDeployment(r.result),
		ExpectedMCPPort:    common.MCPPortForResult(r.result),
		ExpectedSSHPort:    common.SSHLocalPortForResult(r.result),
		ExpectedRuntimePod: r.result.EnvConfig.RuntimePod,
	})
	if err != nil {
		if r.ctx.DryRun {
			r.ctx.Trace("open: dry-run: kubernetes deployment check failed (" + err.Error() + "), assuming not deployed")
			return true, nil
		}
		return false, err
	}
	return !deployed, nil
}

func (r *resolvedOpenRunner) deployRuntime(execution common.DeploySpec) error {
	if r.options.VSCode || r.options.IntelliJ {
		if r.ctx.DryRun {
			r.ctx.Trace(fmt.Sprintf("open: dry-run: would deploy runtime for %s/%s before launching %s; in real mode the user must run `erun sshd init %s %s` or `erun deploy %s %s` first", r.result.Tenant, r.result.Environment, ideOpenLabel(r.options), r.result.Tenant, r.result.Environment, r.result.Tenant, r.result.Environment))
			return nil
		}
		return fmt.Errorf("opening %s requires updating the runtime deployment for %s/%s; run `erun sshd init %s %s` or `erun deploy %s %s` first, then retry", ideOpenLabel(r.options), r.result.Tenant, r.result.Environment, r.result.Tenant, r.result.Environment, r.result.Tenant, r.result.Environment)
	}
	if r.result.EnvConfig.SSHD.Enabled {
		execution.Deploy.SSHDEnabled = true
	}
	r.ctx.Logger.Debug("deploying the devops runtime before opening the shell")
	if err := common.RunDeploySpec(r.ctx, execution, r.openHelmDeployer(execution)); err != nil {
		return err
	}
	if execution.SkipHelm {
		// All runtime images came from the fingerprint cache, so
		// execution.Deploy.Version is a freshly minted snapshot timestamp that
		// was never pushed. Persisting it would point the env config — and the
		// desktop runtime dialog — at a phantom version the deploy picker can
		// never offer (it gates on registry presence), so heal to the version the
		// release is actually running instead. Twin of the deploy-command guard in
		// PersistRuntimeVersionFromDeploySpecs.
		running := r.resolveRunningRuntimeVersion(execution)
		if running == "" {
			r.ctx.Trace("open: runtime images all cached (no rebuild); could not read the deployed version, leaving persisted runtime version unchanged")
			return nil
		}
		r.ctx.Trace("open: runtime images all cached (no rebuild); persisting the running runtime version " + running)
		return r.persistRuntimeVersion(running, execution.Deploy.ContainerRegistry)
	}
	return r.persistRuntimeVersion(execution.Deploy.Version, execution.Deploy.ContainerRegistry)
}

func (r *resolvedOpenRunner) openHelmDeployer(execution common.DeploySpec) common.HelmChartDeployerFunc {
	return wrapHelmDeployWithReleaseRecovery(
		r.promptRunner,
		wrapOpenHelmDeployWithSpinner(r.ctx, execution.Deploy.ReleaseName, r.deployHelmChart),
		common.ClearHelmReleasePendingOperation,
	)
}

func (r *resolvedOpenRunner) persistRuntimeVersion(version, registry string) error {
	if r.ctx.DryRun {
		return nil
	}
	result, err := persistOpenRuntimeVersion(r.result, version, registry, r.options.SaveEnvConfig)
	if err != nil {
		return err
	}
	r.result = result
	return nil
}

// resolveRunningRuntimeVersion returns "" when the running version can't be read
// (dry-run, no resolver, helm error), signalling the caller to leave the
// persisted version untouched rather than record a phantom.
func (r *resolvedOpenRunner) resolveRunningRuntimeVersion(execution common.DeploySpec) string {
	if r.ctx.DryRun || r.resolveDeployedVersion == nil {
		return ""
	}
	version, err := r.resolveDeployedVersion(r.ctx, execution.Deploy.ReleaseName, execution.Deploy.Namespace, execution.Deploy.KubernetesContext)
	if err != nil {
		r.ctx.Trace("open: reading the deployed runtime version failed: " + err.Error())
		return ""
	}
	return strings.TrimSpace(version)
}

// ensureRuntimeDeployed fails a pure `open` when the env's runtime is not
// deployed. Because a port-forward that cannot bind is no longer fatal, a
// genuinely undeployed runtime is detected here by deployment presence rather
// than inferred from a downstream forward timeout — this is the accurate signal
// the desktop surfaces as "deploy this environment".
func (r *resolvedOpenRunner) ensureRuntimeDeployed() error {
	release := common.RuntimeReleaseName(r.result.Tenant)
	namespace := common.KubernetesNamespaceName(r.result.Tenant, r.result.Environment)
	if r.ctx.DryRun {
		// Show the check in the dry-run plan, then assume present so the rest of
		// the plan still renders. CheckKubernetesDeployment emits this same line
		// itself in real mode.
		r.ctx.TraceCommand("", "kubectl", runtimeDeploymentPresenceArgs(r.result.EnvConfig.KubernetesContext, namespace, release)...)
		return nil
	}
	if r.checkKubernetesDeployment == nil {
		return nil
	}
	present, err := r.checkKubernetesDeployment(r.ctx, common.KubernetesDeploymentCheckParams{
		Name:              release,
		Namespace:         namespace,
		KubernetesContext: r.result.EnvConfig.KubernetesContext,
	})
	if err != nil {
		return err
	}
	if !present {
		return fmt.Errorf("runtime for %s/%s is not deployed (deployment %q not found in namespace %q); run `erun deploy %s %s` first",
			r.result.Tenant, r.result.Environment, release, namespace, r.result.Tenant, r.result.Environment)
	}
	return nil
}

func runtimeDeploymentPresenceArgs(kubernetesContext, namespace, release string) []string {
	args := make([]string, 0, 7)
	if strings.TrimSpace(kubernetesContext) != "" {
		args = append(args, "--context", kubernetesContext)
	}
	if strings.TrimSpace(namespace) != "" {
		args = append(args, "--namespace", namespace)
	}
	return append(args, "get", "deployment", release, "-o", "name")
}

// activateForwarders binds the env's laptop-side port-forwards (SSHD, MCP, API)
// as best-effort conveniences. None is a prerequisite for the session being
// opened: the remote shell/AI session runs in-pod via `kubectl exec` and never
// uses these forwards — they only back local tooling (the desktop app's panels,
// `erun api`, `erun mcp`). A forward that cannot bind is surfaced as a warning
// and skipped so a laptop-side convenience never aborts the in-pod session.
func (r *resolvedOpenRunner) activateForwarders() {
	if r.activateSSHD != nil && r.result.EnvConfig.SSHD.Enabled {
		if err := r.activateSSHD(r.ctx, r.result); err != nil {
			r.warnForwarderUnavailable("SSH", err)
		}
	}
	if r.activateMCP != nil {
		if err := r.activateMCP(r.ctx, r.result); err != nil {
			r.warnForwarderUnavailable("MCP", err)
		}
	}
	if r.activateAPI != nil {
		if err := r.activateAPI(r.ctx, r.result); err != nil {
			r.warnForwarderUnavailable("API", err)
		}
	}
}

func (r *resolvedOpenRunner) warnForwarderUnavailable(name string, err error) {
	r.ctx.Trace(fmt.Sprintf("open: %s port-forward unavailable; continuing without it (local %s-backed tooling will be degraded): %s", name, name, strings.TrimSpace(err.Error())))
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
	return emitLocalShellSetupForOpenResult(r.ctx, r.result, promptRunner, r.ctx.Stdout, r.ctx.Stderr)
}

func (r *resolvedOpenRunner) traceShellPreview(shellReq common.ShellLaunchParams) {
	if preview, err := common.PreviewShellLaunch(shellReq); err == nil {
		if len(preview.SeedArgs) > 0 {
			r.ctx.TraceCommand("", "kubectl", preview.SeedArgs...)
			r.ctx.Trace("open: SSH private key streamed to the runtime pod on stdin (kept off the command line)")
		}
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
		if errors.Is(err, common.ErrShellPodReplaced) {
			continue
		}
		if errors.Is(err, common.ErrShellSessionTakenOver) {
			// Another ERun window re-attached this persistent session; it keeps
			// running there, so end this viewer cleanly. The notice line is the
			// desktop's signal to stop its reconnect loop instead of stealing the
			// session straight back.
			r.ctx.Info(common.ShellSessionTakenOverNotice)
			return nil
		}
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

// applyRuntimeDeployImageOverride applies a runtime-image override only for
// published-chart envs; envs on a repo-local chart ignore it because the local
// chart's templates own their image references.
func applyRuntimeDeployImageOverride(ctx common.Context, result common.OpenResult, execution common.DeploySpec, runtimeImage string) (common.DeploySpec, error) {
	runtimeImage = strings.TrimSpace(runtimeImage)
	if runtimeImage == "" {
		return execution, nil
	}
	if !result.RemoteRepo() && !common.IsOCIChartReference(execution.Deploy.ChartPath) {
		return execution, nil
	}
	result.EnvConfig.RuntimeImage = runtimeImage
	return common.ResolvePublishedDevopsDeploySpec(ctx, result, "")
}

func applyRuntimeDeployVersionOverride(execution common.DeploySpec, versionOverride string) common.DeploySpec {
	versionOverride = strings.TrimSpace(versionOverride)
	if versionOverride == "" {
		return execution
	}
	execution.Deploy.Version = versionOverride
	return execution
}

func emitLocalShellSetupForOpenResult(ctx common.Context, result common.OpenResult, promptRunner PromptRunner, stdout, stderr io.Writer) error {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}

	dialect := openNoShellDialectForShell(os.Getenv("SHELL"))
	if stdoutIsTerminalForAliasSetup(stdout) {
		if err := maybeConfigureOpenNoShellAlias(ctx, result, promptRunner, os.Getenv("SHELL"), stderr); err != nil {
			return err
		}
	}

	_, err := io.WriteString(stdout, localShellSetupScript(result, dialect))
	return err
}

func stdoutIsTerminalForAliasSetup(stdout io.Writer) bool {
	return writerIsTerminal(stdout)
}

// nopWriteCloser adapts a writer to promptui's io.WriteCloser Stdout field so
// promptui can render to it but never closes the underlying stream.
type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

// promptStderr targets the confirm render at the process's real stderr (the same
// TTY as stdout), wrapped so promptui cannot close it.
func promptStderr() io.WriteCloser { return nopWriteCloser{os.Stderr} }

func maybeConfigureOpenNoShellAlias(ctx common.Context, result common.OpenResult, promptRunner PromptRunner, shellPath string, stderr io.Writer) error {
	dialect := openNoShellDialectForShell(shellPath)
	aliasName := openNoShellAliasName(result)
	startupFile, aliasConfigured := detectOpenNoShellAliasStartupFile(result, shellPath)
	if aliasConfigured {
		return nil
	}
	if startupFile == "" || promptRunner == nil || dialect == openNoShellDialectPowerShell {
		writeOpenNoShellHintLines(stderr, result, shellPath)
		return nil
	}

	// Route the confirm to stderr: promptui repaints from a readline goroutine
	// and the eval-able setup script prints to stdout right after, so sharing
	// stdout lets the two interleave (see confirmPromptTo). os.Stderr is the same
	// TTY, wrapped so promptui cannot close the process's real stderr.
	ok, err := confirmPromptTo(promptRunner, fmt.Sprintf("add %s to %s", aliasName, startupFile), promptStderr())
	if err != nil {
		return err
	}
	if !ok {
		writeOpenNoShellHintLines(stderr, result, shellPath)
		return nil
	}

	ctx.Trace(fmt.Sprintf("open: append %s alias to %s", aliasName, startupFile))
	if ctx.DryRun {
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
	return []string{
		openNoShellHintPrefix(dialect),
		openNoShellAliasCommand(result, shellPath),
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

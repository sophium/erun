package open

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/sophium/erun/cmd/shared/clicmd"
	"github.com/sophium/erun/cmd/shared/opentarget"
	"github.com/sophium/erun/cmd/shared/portfwd"
	"github.com/sophium/erun/cmd/shared/prompt"
	"github.com/sophium/erun/cmd/shared/sshdlink"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam"
	"github.com/sophium/erun/erun-common/infra/activity"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/primitive/deploy"
	"github.com/sophium/erun/erun-common/primitive/open"
	"github.com/spf13/cobra"
)

// refuseOpenForHostEnvironment refuses a host env before any side effect. It checks
// ResolvedType, not !HasPod(), so legacy untyped envs keep opening.
func refuseOpenForHostEnvironment(result config.OpenResult) error {
	if result.EnvConfig.ResolvedType() != config.EnvironmentTypeHost {
		return nil
	}
	return fmt.Errorf("open %s/%s: %s is a host environment — it has no pod and no cluster to open a shell into; its worktree is already %s, so open that directory directly", result.Tenant, result.Environment, result.Environment, result.RepoPath)
}

// resolveOpenTarget resolves everything needed before opening; done means init handled it.
func resolveOpenTarget(
	ctx core.Context,
	args []string,
	target model.OpenParams,
	vscode bool,
	intellij bool,
	resolveOpen func(model.OpenParams) (config.OpenResult, error),
	runInitForOpen func(core.Context, model.OpenParams) error,
	saveEnvConfig func(string, config.EnvConfig) error,
) (config.OpenResult, bool, error) {
	if vscode && intellij {
		return config.OpenResult{}, false, fmt.Errorf("--vscode and --intellij cannot be used together")
	}
	params, err := opentarget.ResolveOpenParams(ctx.Command, args, target)
	if err != nil {
		return config.OpenResult{}, false, err
	}
	result, initRan, err := resolveOpenWithInitStopForParams(ctx, params, opentarget.ShouldRunInitForOpenCommand, resolveOpen, runInitForOpen)
	if err != nil {
		return config.OpenResult{}, false, err
	}
	if initRan {
		return config.OpenResult{}, true, nil
	}
	if err := refuseOpenForHostEnvironment(result); err != nil {
		return config.OpenResult{}, false, err
	}
	result, err = open.EnsureLocalPortRangePersisted(ctx, saveEnvConfig, result)
	if err != nil {
		return config.OpenResult{}, false, err
	}
	return result, false, nil
}

func NewOpenCmd(prepareContext func(core.Context) core.Context, resolveOpen func(model.OpenParams) (config.OpenResult, error), saveEnvConfig func(string, config.EnvConfig) error, runInitForOpen func(core.Context, model.OpenParams) error, promptRunner prompt.PromptRunner, openShell OpenShellRunner, runManagedDeploy func(core.Context, config.OpenResult) error, checkKubernetesDeployment seam.KubernetesDeploymentCheckerFunc, resolveRuntimeDeploySpec func(core.Context, config.OpenResult, bool) (seam.DeploySpec, error), deployHelmChart seam.HelmChartDeployerFunc, activateMCP portfwd.MCPForwarder, activateAPI portfwd.APIForwarder, activateSSHD sshdlink.SSHDActivator, launchVSCode VSCodeLauncher, launchIntelliJ IntelliJLauncher) *cobra.Command {
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
	var reconnect bool
	target := model.OpenParams{}

	cmd := &cobra.Command{
		Use:   "open [TENANT] [ENVIRONMENT]",
		Short: "Open a shell in the tenant environment",
		Long: "Open a shell in the tenant environment.\n\n" +
			"open is a pure primitive: it starts the port-forwards and drops you into a shell " +
			"against the runtime that is already deployed. It does not deploy on its own — run " +
			"`erun deploy` first, or pass --deploy to deploy before opening (the operator-convenience " +
			"shortcut: builds-here envs build→push→deploy, runtime envs install the current version). " +
			"Use --vscode or --intellij to open an IDE instead, or --no-shell to print the setup " +
			"commands for your current shell. Without a TTY on stdin (an MCP client, an " +
			"orchestrator, a script) open skips the shell automatically and behaves like " +
			"--no-shell instead: the port-forwards still come up, but open never launches a " +
			"shell that would read EOF and exit immediately.\n\n" +
			"Opening is also how you start an environment you stopped with `erun stop`. That makes " +
			"open an operator gesture: anything that respawns it automatically to re-establish a " +
			"dropped session must pass --reconnect, which reattaches without starting a stopped " +
			"environment or discarding the recorded stop.",
		Example:      "  erun open\n  erun open team dev\n  erun open team dev --deploy\n  erun open team dev --vscode",
		Args:         cobra.MaximumNArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := clicmd.CommandContext(cmd)
			if prepareContext != nil {
				ctx = prepareContext(ctx)
			}
			result, done, err := resolveOpenTarget(ctx, args, target, vscode, intellij, resolveOpen, runInitForOpen, saveEnvConfig)
			if err != nil {
				return err
			}
			if done {
				return nil
			}
			allowLocalBuilds := result.EnvConfig.BuildsHere()
			return RunResolvedOpenCommandWithAPI(ctx, result, OpenOptions{
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
				Reconnect:        reconnect,
				// Pinning a version only means something if it rolls out.
				Deploy: deployRuntime || strings.TrimSpace(versionOverride) != "" || strings.TrimSpace(runtimeImage) != "",
			}, promptRunner, openShell, runManagedDeploy, checkKubernetesDeployment, resolveRuntimeDeploySpec, deployHelmChart, activateMCP, activateAPI, activateSSHD, launchVSCode, launchIntelliJ)
		},
	}

	clicmd.AddDryRunFlag(cmd)
	cmd.Flags().StringVar(&target.Tenant, "tenant", "", "Open a specific tenant")
	cmd.Flags().StringVar(&target.Environment, "environment", "", "Open a specific environment")
	cmd.Flags().BoolVar(&noShell, "no-shell", false, "Print shell commands to switch kubectl context, namespace, and worktree locally")
	cmd.Flags().BoolVar(&noAliasPrompt, "no-alias-prompt", false, "Skip prompting to add a local shell alias with --no-shell")
	cmd.Flags().BoolVar(&vscode, "vscode", false, "Open the remote environment in VS Code instead of a shell")
	cmd.Flags().BoolVar(&intellij, "intellij", false, "Open the remote environment in IntelliJ IDEA instead of a shell")
	cmd.Flags().StringVar(&versionOverride, "version", "", "Override the runtime chart and image version before opening")
	cmd.Flags().StringVar(&runtimeImage, "runtime-image", "", "Override the runtime image repository before opening")
	// Hidden desktop-integration flags for reattachable sessions.
	cmd.Flags().StringVar(&appSession, "app-session", "", "Reattach to a persistent terminal session with this id")
	cmd.Flags().BoolVar(&aiTab, "ai", false, "Launch the configured AI tool as the persistent session's program")
	cmd.Flags().BoolVar(&contributeTab, "contribute", false, "Start the persistent session in the contribute clone")
	cmd.Flags().BoolVar(&deployRuntime, "deploy", false, "Deploy the runtime before opening (operator convenience: builds-here envs build→push→deploy, runtime envs install the current version)")
	cmd.Flags().BoolVar(&reconnect, "reconnect", false, "Reattach a dropped session instead of opening: never starts a stopped environment and never clears a recorded stop")
	_ = cmd.Flags().MarkHidden("app-session")
	_ = cmd.Flags().MarkHidden("ai")
	_ = cmd.Flags().MarkHidden("contribute")
	return cmd
}

type OpenOptions struct {
	NoShell          bool
	NoAliasPrompt    bool
	VSCode           bool
	IntelliJ         bool
	VersionOverride  string
	RuntimeImage     string
	AllowLocalBuilds bool
	SaveEnvConfig    func(string, config.EnvConfig) error
	AppSession       string
	AI               bool
	Deploy           bool
	Contribute       bool
	Reconnect        bool
}

func ResolveOpenWithInitStop(ctx core.Context, args []string, shouldRunInit func(error) bool, resolveOpen func(model.OpenParams) (config.OpenResult, error), runInitForArgs func(core.Context, []string) error) (config.OpenResult, bool, error) {
	_, result, err := opentarget.ResolveOpenArgs(ctx.Command, args, resolveOpen)
	if !shouldRunInit(err) {
		return result, false, err
	}

	if initErr := opentarget.RunInitBeforeOpen(ctx, args, runInitForArgs); initErr != nil {
		return config.OpenResult{}, true, initErr
	}

	return config.OpenResult{}, true, nil
}

func resolveOpenWithInitStopForParams(ctx core.Context, params model.OpenParams, shouldRunInit func(error) bool, resolveOpen func(model.OpenParams) (config.OpenResult, error), runInitForOpen func(core.Context, model.OpenParams) error) (config.OpenResult, bool, error) {
	result, err := resolveOpen(params)
	if !shouldRunInit(err) {
		return result, false, err
	}

	if initErr := opentarget.RunInitBeforeOpenForParams(ctx, params, runInitForOpen); initErr != nil {
		return config.OpenResult{}, true, initErr
	}

	return config.OpenResult{}, true, nil
}

func RunResolvedOpenCommandWithAPI(ctx core.Context, result config.OpenResult, options OpenOptions, promptRunner prompt.PromptRunner, openShell OpenShellRunner, runManagedDeploy func(core.Context, config.OpenResult) error, checkKubernetesDeployment seam.KubernetesDeploymentCheckerFunc, resolveRuntimeDeploySpec func(core.Context, config.OpenResult, bool) (seam.DeploySpec, error), deployHelmChart seam.HelmChartDeployerFunc, activateMCP portfwd.MCPForwarder, activateAPI portfwd.APIForwarder, activateSSHD sshdlink.SSHDActivator, launchVSCode VSCodeLauncher, launchIntelliJ IntelliJLauncher) error {
	ctx, closeEnvTrace := core.ActivateEnvTrace(ctx, result.Tenant, result.Environment)
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
		resolveDeployedVersion:    deploy.ResolveDeployedHelmReleaseVersion,
		activateMCP:               activateMCP,
		activateAPI:               activateAPI,
		activateSSHD:              activateSSHD,
		launchVSCode:              launchVSCode,
		launchIntelliJ:            launchIntelliJ,
	}
	return runner.run()
}

type resolvedOpenRunner struct {
	ctx                       core.Context
	result                    config.OpenResult
	options                   OpenOptions
	promptRunner              prompt.PromptRunner
	openShell                 OpenShellRunner
	runManagedDeploy          func(core.Context, config.OpenResult) error
	checkKubernetesDeployment seam.KubernetesDeploymentCheckerFunc
	resolveRuntimeDeploySpec  func(core.Context, config.OpenResult, bool) (seam.DeploySpec, error)
	deployHelmChart           seam.HelmChartDeployerFunc
	resolveDeployedVersion    seam.HelmReleaseVersionResolverFunc
	activateMCP               portfwd.MCPForwarder
	activateAPI               portfwd.APIForwarder
	activateSSHD              sshdlink.SSHDActivator
	launchVSCode              VSCodeLauncher
	launchIntelliJ            IntelliJLauncher
}

func (r *resolvedOpenRunner) run() error {
	r.ctx.Trace(fmt.Sprintf("open: tenant=%s environment=%s kubernetes-context=%s remote=%v no-shell=%v ide=%s",
		r.result.Tenant, r.result.Environment,
		r.result.EnvConfig.KubernetesContext, r.result.RemoteRepo(),
		r.options.NoShell, openIDEKindLabel(r.options)))
	if err := r.ensureKubernetesContext(); err != nil {
		return err
	}
	r.recordActivity()
	if err := r.validateIDEOptions(); err != nil {
		return err
	}

	shellReq := cloud.ShellLaunchParamsFromResult(r.result)
	shellReq.AppSession = r.options.AppSession
	shellReq.AI = r.options.AI
	shellReq.Contribute = r.options.Contribute
	// Clear any recorded stop before rendering, or the deploy re-applies replicas: 0.
	// A reconnect does not; see clearStopIntent.
	if err := r.clearStopIntent(); err != nil {
		return err
	}
	if err := r.prepareRuntime(shellReq); err != nil {
		return err
	}
	r.refreshHostCredentials()
	forwarderErr := r.activateForwarders()
	if launched, err := r.maybeLaunchIDE(); launched || err != nil {
		return err
	}
	if done, err := r.emitNoShellFallbackIfNeeded(forwarderErr); done {
		return err
	}

	r.traceShellPreview(shellReq)
	if r.ctx.DryRun {
		r.ctx.Trace("open: dry-run complete; would have launched shell")
		return nil
	}
	return r.runShellLoop(shellReq)
}

// emitNoShellFallbackIfNeeded handles --no-shell and non-TTY callers. --no-shell fails
// on an unreachable forward (it is the deliverable); the non-TTY fallback only traces it.
func (r *resolvedOpenRunner) emitNoShellFallbackIfNeeded(forwarderErr error) (done bool, err error) {
	switch {
	case r.options.NoShell:
		if forwarderErr != nil {
			return true, forwarderErr
		}
		r.ctx.Trace("open: --no-shell selected, emitting setup commands instead of launching shell")
		return true, r.emitNoShellSetup()
	case !prompt.StdinIsTerminal():
		r.ctx.Trace("open: stdin is not a TTY, so an interactive shell would read EOF and exit immediately; keeping the port-forwards up and emitting setup commands instead of a shell that cannot stay open")
		return true, r.emitNoShellSetup()
	default:
		return false, nil
	}
}

func openIDEKindLabel(options OpenOptions) string {
	switch {
	case options.VSCode:
		return "vscode"
	case options.IntelliJ:
		return "intellij"
	default:
		return "shell"
	}
}

// ensureKubernetesContext starts a stopped context's machine; a reconnect skips it,
// since a reattach must not undo a stop.
func (r *resolvedOpenRunner) ensureKubernetesContext() error {
	if r.options.Reconnect {
		r.ctx.Trace("open: --reconnect, so a stopped cloud context is left stopped rather than started to reach it")
		return nil
	}
	return r.ctx.EnsureKubernetesContext(r.result.EnvConfig.KubernetesContext)
}

func (r *resolvedOpenRunner) recordActivity() {
	if !r.ctx.DryRun && os.Getenv("ERUN_IDLE_PROBE") != "1" {
		_ = activity.RecordEnvironmentActivity(model.EnvironmentActivityParams{
			Tenant:      r.result.Tenant,
			Environment: r.result.Environment,
			Kind:        config.ActivityKindCLI,
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

// refreshHostCredentials re-injects short-lived AWS credentials into the awake pod.
// Best-effort: a lapsed SSO session must not block opening.
func (r *resolvedOpenRunner) refreshHostCredentials() {
	if !r.result.EnvConfig.HasAWSCloudAlias() {
		return
	}
	refresh, err := RefreshEnvironmentHostCredentials(r.ctx, r.result)
	if err != nil {
		r.ctx.Trace("open: host AWS credential refresh failed; the environment keeps whatever credentials it already had: " + strings.TrimSpace(err.Error()))
		return
	}
	if r.ctx.DryRun {
		return
	}
	r.ctx.Trace(fmt.Sprintf("open: refreshed host AWS credentials for %s into the %s profile", refresh.Alias, refresh.Profile))
}

// activateForwarders attempts every laptop-side port-forward, tracing failures. The
// returned error matters only when the forwards are the deliverable (no shell).
func (r *resolvedOpenRunner) activateForwarders() error {
	// Reclaim orphaned records before the forward store is read and appended to.
	ReclaimOrphanedPortForwardRecords(r.ctx)
	var failures []string
	if r.activateSSHD != nil && r.result.EnvConfig.SSHD.Enabled {
		if err := r.activateSSHD(r.ctx, r.result); err != nil {
			r.warnForwarderUnavailable("SSH", err)
			failures = append(failures, fmt.Sprintf("SSH: %s", strings.TrimSpace(err.Error())))
		}
	}
	if r.activateMCP != nil {
		if err := r.activateMCP(r.ctx, r.result); err != nil {
			r.warnForwarderUnavailable("MCP", err)
			failures = append(failures, fmt.Sprintf("MCP: %s", strings.TrimSpace(err.Error())))
		}
	}
	if r.activateAPI != nil {
		if err := r.activateAPI(r.ctx, r.result); err != nil {
			r.warnForwarderUnavailable("API", err)
			failures = append(failures, fmt.Sprintf("API: %s", strings.TrimSpace(err.Error())))
		}
	}
	if len(failures) == 0 {
		return nil
	}
	return fmt.Errorf("open: port-forward setup failed, so there is no local channel to wait on: %s", strings.Join(failures, "; "))
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
	namespace := config.KubernetesNamespaceName(r.result.Tenant, r.result.Environment)
	r.ctx.TraceCommand("", "kubectl", "config", "use-context", strings.TrimSpace(r.result.EnvConfig.KubernetesContext))
	r.ctx.TraceCommand("", "kubectl", "config", "set-context", "--current", "--namespace="+namespace)
	r.ctx.TraceCommand("", "cd", r.result.RepoPath)
	promptRunner := r.promptRunner
	if r.options.NoAliasPrompt {
		promptRunner = nil
	}
	return emitLocalShellSetupForOpenResult(r.ctx, r.result, promptRunner, r.ctx.Stdout, r.ctx.Stderr)
}

func (r *resolvedOpenRunner) traceShellPreview(shellReq config.ShellLaunchParams) {
	if preview, err := open.PreviewShellLaunch(shellReq); err == nil {
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

func (r *resolvedOpenRunner) runShellLoop(shellReq config.ShellLaunchParams) error {
	for {
		err := r.openShell(r.ctx, shellReq)
		if errors.Is(err, open.ErrShellPodReplaced) {
			continue
		}
		if errors.Is(err, open.ErrShellSessionTakenOver) {
			// Another window took over this session; the notice stops the desktop's reconnect loop.
			r.ctx.Info(open.ShellSessionTakenOverNotice)
			return nil
		}
		if !errors.Is(err, open.ErrShellReattachDeploy) {
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

func ideOpenLabel(options OpenOptions) string {
	if options.IntelliJ {
		return "IntelliJ IDEA"
	}
	if options.VSCode {
		return "VS Code"
	}
	return "the IDE"
}

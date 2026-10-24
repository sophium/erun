package open

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/cmd/shared"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/command/lifecycle"
	"github.com/sophium/erun/erun-common/primitive/deploy"
	"github.com/sophium/erun/erun-common/primitive/open"
)

func (r *resolvedOpenRunner) maybeDeployRuntime(shellReq config.ShellLaunchParams) error {
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

func (r *resolvedOpenRunner) resolveRuntimeExecution() (deploy.DeploySpec, error) {
	// Heal envs created by older binaries: their tenant Chart.yaml is pinned to
	// the literal "1.0.0" placeholder, so helm keeps pulling an erun-devops:1.0.0
	// image that was never published and every rollout fails at pod startup.
	appVersion := strings.TrimSpace(r.result.EnvConfig.RuntimeVersion)
	if appVersion == "" {
		appVersion = shared.CurrentBuildInfo().Version
	}
	if err := deploy.MigrateDefaultDevopsChartAppVersion(r.ctx, r.result.RepoPath, r.result.Tenant, appVersion); err != nil {
		return deploy.DeploySpec{}, err
	}
	execution, err := r.resolveRuntimeDeploySpec(r.ctx, r.result, r.options.AllowLocalBuilds)
	if err != nil {
		return deploy.DeploySpec{}, err
	}
	execution, err = applyRuntimeDeployImageOverride(r.ctx, r.result, execution, r.options.RuntimeImage)
	if err != nil {
		return deploy.DeploySpec{}, err
	}
	return applyRuntimeDeployVersionOverride(execution, r.options.VersionOverride), nil
}

func (r *resolvedOpenRunner) shouldDeployRuntime(shellReq config.ShellLaunchParams, execution deploy.DeploySpec) (bool, error) {
	if strings.TrimSpace(r.options.VersionOverride) != "" || strings.TrimSpace(r.options.RuntimeImage) != "" {
		return true, nil
	}
	if r.checkKubernetesDeployment == nil {
		return false, nil
	}
	deployed, err := r.checkKubernetesDeployment(r.ctx, deploy.KubernetesDeploymentCheckParams{
		Name:               config.RuntimeReleaseName(r.result.Tenant),
		Namespace:          config.KubernetesNamespaceName(r.result.Tenant, r.result.Environment),
		KubernetesContext:  r.result.EnvConfig.KubernetesContext,
		ExpectedRepoPath:   open.RemoteShellWorktreePath(shellReq),
		ExpectedSSHD:       sshdExpectationForDeployment(r.result),
		ExpectedMCPPort:    config.MCPPortForResult(r.result),
		ExpectedSSHPort:    config.SSHLocalPortForResult(r.result),
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

func (r *resolvedOpenRunner) deployRuntime(execution deploy.DeploySpec) error {
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
	if err := deploy.RunDeploySpec(r.ctx, execution, r.openHelmDeployer(execution)); err != nil {
		return err
	}
	// Persist through the same shared writer `erun deploy`/`erun upgrade` use
	// (common.PersistRuntimeVersionFromDeploySpecs) instead of a hand-rolled
	// subset: the hand-rolled version only ever wrote RuntimeVersion and
	// RuntimeRegistry, so an operator's own --runtime-image on `open --deploy`
	// installed correctly this one time and then was silently forgotten on the
	// next plain open/deploy. The shared writer also heals RuntimeRunningImage, a
	// stated RuntimeChart, and the MCP-auth key, and already knows to heal to
	// the running version instead of a never-pushed snapshot when every image
	// came from the fingerprint cache (SkipHelm).
	return deploy.PersistRuntimeVersionFromDeploySpecs(r.ctx, []deploy.DeploySpec{execution}, r.options.SaveEnvConfig, r.resolveDeployedVersion)
}

func (r *resolvedOpenRunner) openHelmDeployer(execution deploy.DeploySpec) deploy.HelmChartDeployerFunc {
	return shared.WrapHelmDeployWithReleaseRecovery(
		r.promptRunner,
		wrapOpenHelmDeployWithSpinner(r.ctx, execution.Deploy.ReleaseName, r.deployHelmChart),
		deploy.ClearHelmReleasePendingOperation,
	)
}

// ensureRuntimeDeployed fails a pure `open` when the env's runtime is not
// deployed. Because a port-forward that cannot bind is no longer fatal, a
// genuinely undeployed runtime is detected here by deployment presence rather
// than inferred from a downstream forward timeout — this is the accurate signal
// the desktop surfaces as "deploy this environment".
func (r *resolvedOpenRunner) ensureRuntimeDeployed() error {
	release := config.RuntimeReleaseName(r.result.Tenant)
	namespace := config.KubernetesNamespaceName(r.result.Tenant, r.result.Environment)
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
	present, err := r.checkKubernetesDeployment(r.ctx, deploy.KubernetesDeploymentCheckParams{
		Name:              release,
		Namespace:         namespace,
		KubernetesContext: r.result.EnvConfig.KubernetesContext,
	})
	if err != nil {
		return err
	}
	if !present {
		// Builds on common.KubernetesDeploymentAbsentMessageMarker so the
		// desktop, reading only this text across a CLI subprocess boundary, can
		// tell a genuine absence apart from a check that could not resolve an
		// answer without re-deriving kubectl's own error grammar.
		return fmt.Errorf("runtime for %s/%s "+deploy.KubernetesDeploymentAbsentMessageMarker+" %q not found in namespace %q); run `erun deploy %s %s` first",
			r.result.Tenant, r.result.Environment, release, namespace, r.result.Tenant, r.result.Environment)
	}
	return nil
}

// prepareRuntime brings the environment to a state the rest of open can use:
// deploy when the operator asked for it, otherwise verify the runtime is there,
// and either way start it if it was stopped. The wake comes last because it is
// the step that must hold immediately before the port-forwards.
func (r *resolvedOpenRunner) prepareRuntime(shellReq config.ShellLaunchParams) error {
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
	// Wake before the forwards: kubectl port-forward cannot attach to a
	// Deployment with zero replicas, so a stopped environment would otherwise
	// surface as a forward timeout instead of starting.
	return r.ensureRuntimeAwake()
}

// clearStopIntent records that this environment is wanted running again. It
// touches only the config, so it is safe to run before the runtime is known to
// exist (a first `open --deploy` has no Deployment yet). A reconnect declares
// itself as machine-initiated and is therefore not that statement: the operator
// stopped the environment, which is what dropped the session being reattached.
func (r *resolvedOpenRunner) clearStopIntent() error {
	if r.options.Reconnect {
		r.ctx.Trace("open: --reconnect, so any recorded stop stays recorded and a stopped runtime is not started")
		return nil
	}
	updated, err := lifecycle.ClearEnvironmentStopIntent(r.ctx, r.result, r.options.SaveEnvConfig)
	if err != nil {
		return err
	}
	r.result.EnvConfig = updated
	return nil
}

// ensureRuntimeAwake scales a stopped environment back to one replica and waits
// for the rollout. It is quiet for an environment that is already running: the
// single read below decides, and no scale call is made. A reconnect never
// scales — a stopped environment fails the reattach instead of being restarted
// behind the operator.
func (r *resolvedOpenRunner) ensureRuntimeAwake() error {
	wake, err := lifecycle.EnsureEnvironmentAwake(r.ctx, lifecycle.WakeEnvironmentParams{
		Result:        r.result,
		SaveEnvConfig: r.options.SaveEnvConfig,
		Reconnect:     r.options.Reconnect,
	})
	if err != nil {
		return err
	}
	r.result.EnvConfig = wake.EnvConfig
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

func sshdExpectationForDeployment(result config.OpenResult) *bool {
	if !result.EnvConfig.SSHD.Enabled {
		return nil
	}
	expected := true
	return &expected
}

func wrapOpenHelmDeployWithSpinner(ctx core.Context, releaseName string, deployHelmChart deploy.HelmChartDeployerFunc) deploy.HelmChartDeployerFunc {
	if deployHelmChart == nil {
		return nil
	}
	return func(params deploy.HelmDeployParams) error {
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
func applyRuntimeDeployImageOverride(ctx core.Context, result config.OpenResult, execution deploy.DeploySpec, runtimeImage string) (deploy.DeploySpec, error) {
	runtimeImage = strings.TrimSpace(runtimeImage)
	if runtimeImage == "" {
		return execution, nil
	}
	if !result.RemoteRepo() && !deploy.IsOCIChartReference(execution.Deploy.ChartPath) {
		return execution, nil
	}
	result.EnvConfig.RuntimeImage = runtimeImage
	return deploy.ResolvePublishedDevopsDeploySpec(ctx, result, "")
}

func applyRuntimeDeployVersionOverride(execution deploy.DeploySpec, versionOverride string) deploy.DeploySpec {
	versionOverride = strings.TrimSpace(versionOverride)
	if versionOverride == "" {
		return execution
	}
	execution.Deploy.Version = versionOverride
	return execution
}

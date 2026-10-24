package open

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/cmd/shared"
	"github.com/sophium/erun/cmd/shared/helmrecovery"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam"
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

func (r *resolvedOpenRunner) resolveRuntimeExecution() (seam.DeploySpec, error) {
	// Older envs pin Chart.yaml to the unpublished "1.0.0" placeholder.
	appVersion := strings.TrimSpace(r.result.EnvConfig.RuntimeVersion)
	if appVersion == "" {
		appVersion = shared.CurrentBuildInfo().Version
	}
	if err := deploy.MigrateDefaultDevopsChartAppVersion(r.ctx, r.result.RepoPath, r.result.Tenant, appVersion); err != nil {
		return seam.DeploySpec{}, err
	}
	execution, err := r.resolveRuntimeDeploySpec(r.ctx, r.result, r.options.AllowLocalBuilds)
	if err != nil {
		return seam.DeploySpec{}, err
	}
	execution, err = applyRuntimeDeployImageOverride(r.ctx, r.result, execution, r.options.RuntimeImage)
	if err != nil {
		return seam.DeploySpec{}, err
	}
	return applyRuntimeDeployVersionOverride(execution, r.options.VersionOverride), nil
}

func (r *resolvedOpenRunner) shouldDeployRuntime(shellReq config.ShellLaunchParams, execution seam.DeploySpec) (bool, error) {
	if strings.TrimSpace(r.options.VersionOverride) != "" || strings.TrimSpace(r.options.RuntimeImage) != "" {
		return true, nil
	}
	if r.checkKubernetesDeployment == nil {
		return false, nil
	}
	deployed, err := r.checkKubernetesDeployment(r.ctx, model.KubernetesDeploymentCheckParams{
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

func (r *resolvedOpenRunner) deployRuntime(execution seam.DeploySpec) error {
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
	// The shared writer also persists RuntimeImage and heals to the running version on a cached no-op.
	return deploy.PersistRuntimeVersionFromDeploySpecs(r.ctx, []seam.DeploySpec{execution}, r.options.SaveEnvConfig, r.resolveDeployedVersion)
}

func (r *resolvedOpenRunner) openHelmDeployer(execution seam.DeploySpec) seam.HelmChartDeployerFunc {
	return helmrecovery.WrapHelmDeployWithReleaseRecovery(
		r.promptRunner,
		wrapOpenHelmDeployWithSpinner(r.ctx, execution.Deploy.ReleaseName, r.deployHelmChart),
		deploy.ClearHelmReleasePendingOperation,
	)
}

// ensureRuntimeDeployed detects an undeployed runtime by deployment presence, the
// signal the desktop surfaces as "deploy this environment".
func (r *resolvedOpenRunner) ensureRuntimeDeployed() error {
	release := config.RuntimeReleaseName(r.result.Tenant)
	namespace := config.KubernetesNamespaceName(r.result.Tenant, r.result.Environment)
	if r.ctx.DryRun {
		// Dry-run shows the check and assumes present.
		r.ctx.TraceCommand("", "kubectl", runtimeDeploymentPresenceArgs(r.result.EnvConfig.KubernetesContext, namespace, release)...)
		return nil
	}
	if r.checkKubernetesDeployment == nil {
		return nil
	}
	present, err := r.checkKubernetesDeployment(r.ctx, model.KubernetesDeploymentCheckParams{
		Name:              release,
		Namespace:         namespace,
		KubernetesContext: r.result.EnvConfig.KubernetesContext,
	})
	if err != nil {
		return err
	}
	if !present {
		// The marker lets the desktop tell absence from an unresolved check across the CLI boundary.
		return fmt.Errorf("runtime for %s/%s "+deploy.KubernetesDeploymentAbsentMessageMarker+" %q not found in namespace %q); run `erun deploy %s %s` first",
			r.result.Tenant, r.result.Environment, release, namespace, r.result.Tenant, r.result.Environment)
	}
	return nil
}

// prepareRuntime deploys or verifies the runtime, then wakes it right before the port-forwards.
func (r *resolvedOpenRunner) prepareRuntime(shellReq config.ShellLaunchParams) error {
	if r.options.Deploy {
		// --deploy is operator convenience; programmatic callers compose build→push→deploy themselves.
		if err := r.maybeDeployRuntime(shellReq); err != nil {
			return err
		}
	} else {
		r.ctx.Trace("open: pure primitive — not deploying (run `erun deploy` first, or pass --deploy to deploy before opening)")
		if err := r.ensureRuntimeDeployed(); err != nil {
			return err
		}
	}
	// kubectl port-forward cannot attach to a zero-replica Deployment.
	return r.ensureRuntimeAwake()
}

// clearStopIntent touches only config, so it is safe before the runtime exists. A
// reconnect is not a request to run, so it keeps the stop.
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

// ensureRuntimeAwake scales a stopped env to one replica; a reconnect never scales.
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

func wrapOpenHelmDeployWithSpinner(ctx core.Context, releaseName string, deployHelmChart seam.HelmChartDeployerFunc) seam.HelmChartDeployerFunc {
	if deployHelmChart == nil {
		return nil
	}
	return func(params seam.HelmDeployParams) error {
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

// applyRuntimeDeployImageOverride is ignored for repo-local charts, which own their image refs.
func applyRuntimeDeployImageOverride(ctx core.Context, result config.OpenResult, execution seam.DeploySpec, runtimeImage string) (seam.DeploySpec, error) {
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

func applyRuntimeDeployVersionOverride(execution seam.DeploySpec, versionOverride string) seam.DeploySpec {
	versionOverride = strings.TrimSpace(versionOverride)
	if versionOverride == "" {
		return execution
	}
	execution.Deploy.Version = versionOverride
	return execution
}

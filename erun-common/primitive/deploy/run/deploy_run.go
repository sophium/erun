package run

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sophium/erun/erun-common/base/config/runtimetarget"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/seam/helmdeploy"
	"github.com/sophium/erun/erun-common/base/seam/kubeapi"
	"github.com/sophium/erun/erun-common/infra/helm"
	"github.com/sophium/erun/erun-common/infra/kube"
	"github.com/sophium/erun/erun-common/primitive/deploy/chartref"
	"github.com/sophium/erun/erun-common/primitive/deploy/gateway"
	"github.com/sophium/erun/erun-common/primitive/deploy/helmrun"
	"github.com/sophium/erun/erun-common/primitive/deploy/mcpauth"
	planpkg "github.com/sophium/erun/erun-common/primitive/deploy/plan"
	"github.com/sophium/erun/erun-common/primitive/deploy/platformalias"
	"github.com/sophium/erun/erun-common/primitive/deploy/registrycopy"
)

func runDeploySpecsPlan(ctx core.Context, executions []helmdeploy.DeploySpec, deploy helmdeploy.HelmChartDeployerFunc) error {
	plan, err := planpkg.LoadProjectK8sPlanForDeploy(executions)
	if err != nil {
		return err
	}
	groups := planpkg.GroupDeploySpecsByPlan(executions, plan)
	for stepIndex, group := range groups {
		if err := runDeployStep(ctx, stepIndex, group, deploy); err != nil {
			return err
		}
	}
	return nil
}

// parallelDeployCapture gives each parallel goroutine private writers: the
// shared Context's writers may be plain bytes.Buffers (MCP edge), and output
// would interleave mid-line. Replayed in spec order after wg.Wait().
type parallelDeployCapture struct {
	loggerStdout *bytes.Buffer
	loggerStderr *bytes.Buffer
	ctxStdout    *bytes.Buffer
	ctxStderr    *bytes.Buffer
}

func isolateContextForParallelDeploy(ctx core.Context) (core.Context, *parallelDeployCapture) {
	capture := &parallelDeployCapture{
		loggerStdout: new(bytes.Buffer),
		loggerStderr: new(bytes.Buffer),
	}
	ctx.Logger = ctx.Logger.WithWriters(capture.loggerStdout, capture.loggerStderr)
	if ctx.Stdout != nil {
		capture.ctxStdout = new(bytes.Buffer)
		ctx.Stdout = capture.ctxStdout
	}
	if ctx.Stderr != nil {
		capture.ctxStderr = new(bytes.Buffer)
		ctx.Stderr = capture.ctxStderr
	}
	return ctx, capture
}

func replayParallelDeployCapture(ctx core.Context, capture *parallelDeployCapture) {
	_, _ = io.Copy(ctx.Logger.StdoutWriter(), capture.loggerStdout)
	_, _ = io.Copy(ctx.Logger.StderrWriter(), capture.loggerStderr)
	if ctx.Stdout != nil && capture.ctxStdout != nil {
		_, _ = io.Copy(ctx.Stdout, capture.ctxStdout)
	}
	if ctx.Stderr != nil && capture.ctxStderr != nil {
		_, _ = io.Copy(ctx.Stderr, capture.ctxStderr)
	}
}

// runDeployStep runs serially for one spec or --dry-run so traces stay
// deterministic.
func runDeployStep(ctx core.Context, stepIndex int, specs []helmdeploy.DeploySpec, deploy helmdeploy.HelmChartDeployerFunc) error {
	if len(specs) == 0 {
		return nil
	}
	if len(specs) > 1 {
		names := make([]string, 0, len(specs))
		for _, spec := range specs {
			names = append(names, spec.DeployContext.ComponentName)
		}
		ctx.Trace(fmt.Sprintf("deploy: step %d (parallel): %s", stepIndex+1, strings.Join(names, ", ")))
	}
	if ctx.DryRun || len(specs) == 1 {
		for _, spec := range specs {
			if err := RunDeploySpec(ctx, spec, deploy); err != nil {
				return err
			}
		}
		return nil
	}
	var wg sync.WaitGroup
	errs := make([]error, len(specs))
	captures := make([]*parallelDeployCapture, len(specs))
	for i, spec := range specs {
		wg.Add(1)
		specCtx, capture := isolateContextForParallelDeploy(ctx)
		captures[i] = capture
		go func(i int, spec helmdeploy.DeploySpec, specCtx core.Context) {
			defer wg.Done()
			errs[i] = RunDeploySpec(specCtx, spec, deploy)
		}(i, spec, specCtx)
	}
	wg.Wait()
	for _, capture := range captures {
		replayParallelDeployCapture(ctx, capture)
	}
	return errors.Join(errs...)
}

// chartDependencyBuildPlan builds a local umbrella's declared dependencies, which
// helm upgrade --install requires in charts/; it pulls only pinned published
// charts.
func chartDependencyBuildPlan(ctx core.Context, deployInput helmdeploy.HelmDeploySpec) (*helm.CommandSpec, error) {
	chartPath := strings.TrimSpace(deployInput.ChartPath)
	declares, err := localChartDeclaresDependencies(chartPath)
	if err != nil || !declares {
		return nil, err
	}
	build := helm.CommandSpec{Name: "helm", Args: []string{"dependency", "build", chartPath}}
	ctx.TraceCommand(build.Dir, build.Name, build.Args...)
	return &build, nil
}

func localChartDeclaresDependencies(chartPath string) (bool, error) {
	if chartPath == "" || chartref.IsOCIChartReference(chartPath) {
		return false, nil
	}
	return helm.HelmChartDeclaresDependencies(chartPath)
}

// Called after the single-flight acquire so deploys never race on charts/.
func runChartDependencyBuild(ctx core.Context, deployInput helmdeploy.HelmDeploySpec, build *helm.CommandSpec, target string) error {
	if build == nil {
		return nil
	}
	ctx.Info("==> Building chart dependencies for " + target)
	if err := helm.RunHelmCommand(ctx, *build); err != nil {
		return fmt.Errorf("deploy %s: helm dependency build: %w", deployInput.ReleaseName, err)
	}
	return nil
}

type publishedValuesPull struct {
	command helm.CommandSpec
	dest    string
}

// planPublishedValuesPull unpacks a by-reference umbrella so -f can use its
// bundled values.<env>.yaml, which helm never applies on its own. The dest is
// deterministic so repeated deploys trace identically.
func planPublishedValuesPull(ctx core.Context, deployInput *helmdeploy.HelmDeploySpec) *publishedValuesPull {
	ref := strings.TrimSpace(deployInput.ChartPath)
	if !chartref.IsOCIChartReference(ref) || strings.TrimSpace(deployInput.SubchartKey) == "" {
		return nil
	}
	chartName := ref[strings.LastIndex(ref, "/")+1:]
	dest := filepath.Join(os.TempDir(), "erun-deploy-values-"+chartName)
	envSlug := strings.ToLower(strings.TrimSpace(deployInput.Environment))
	deployInput.PulledValuesFilePath = filepath.Join(dest, chartName, "values."+envSlug+".yaml")
	pull := helm.CommandSpec{Name: "helm", Args: []string{"pull", ref, "--version", chartref.ResolvedChartVersion(*deployInput), "--untar", "--untardir", dest}}
	ctx.TraceCommand(pull.Dir, pull.Name, pull.Args...)
	return &publishedValuesPull{command: pull, dest: dest}
}

// The dest is cleared first so a stale extraction never masks the current values.
func runPublishedValuesPull(ctx core.Context, pull *publishedValuesPull, target string) (func(), error) {
	if pull == nil {
		return func() {}, nil
	}
	cleanup := func() { _ = os.RemoveAll(pull.dest) }
	if err := os.RemoveAll(pull.dest); err != nil {
		return cleanup, fmt.Errorf("prepare chart-values dir: %w", err)
	}
	if err := os.MkdirAll(pull.dest, 0o755); err != nil {
		return cleanup, fmt.Errorf("prepare chart-values dir: %w", err)
	}
	ctx.Info("==> Fetching " + target + " chart values")
	if err := helm.RunHelmCommand(ctx, pull.command); err != nil {
		return cleanup, fmt.Errorf("helm pull chart values: %w", err)
	}
	return cleanup, nil
}

// A var so a test can prove it runs before the secret applies.
var ensureDeployNamespace kubeapi.NamespaceEnsurerFunc = kube.EnsureKubernetesNamespace

// applyPreRolloutResources ensures the namespace first: on a first deploy the
// secret applies below would otherwise fail with "namespaces not found".
func applyPreRolloutResources(ctx core.Context, deployInput *helmdeploy.HelmDeploySpec) error {
	if !ctx.DryRun {
		if err := ensureDeployNamespace(deployInput.KubernetesContext, deployInput.Namespace); err != nil {
			return err
		}
	}
	if err := applyCloudflareCredentialsSecret(ctx, *deployInput); err != nil {
		return err
	}
	if err := gateway.ApplyGatewayCredentialsSecret(ctx, *deployInput); err != nil {
		return err
	}
	if err := mcpauth.ApplyMCPAuthSecret(ctx, *deployInput); err != nil {
		return err
	}
	if err := refreshImagePullSecrets(ctx, *deployInput); err != nil {
		return err
	}
	// Takes the spec so the Secret name reaches the upgrade command.
	if err := platformalias.ReconcilePlatformAliasSecret(ctx, deployInput); err != nil {
		return err
	}
	return mcpauth.RecordMCPAuthKeyOnEnv(ctx, *deployInput)
}

func RunHelmDeploy(ctx core.Context, deployInput helmdeploy.HelmDeploySpec, deploy helmdeploy.HelmChartDeployerFunc) error {
	if deploy == nil {
		return fmt.Errorf("helm deployer is required")
	}
	deployInput.Verbosity = ctx.Verbosity
	if err := ctx.RequireKubernetesContext(deployInput.KubernetesContext); err != nil {
		return fmt.Errorf("deploy %s: %w", deployInput.ReleaseName, err)
	}
	if err := ensureRuntimeImagePullSecret(ctx, &deployInput, deployRuntimeImagePullProbe); err != nil {
		return fmt.Errorf("deploy %s: %w", deployInput.ReleaseName, err)
	}
	kube.TraceEnsureKubernetesNamespace(ctx, deployInput.KubernetesContext, deployInput.Namespace)
	kube.TraceApplyKubernetesResourceQuota(ctx, deployInput.KubernetesContext, deployInput.Namespace, deployInput.NamespaceQuota)
	announceWorktreeVolumeChange(ctx, deployInput)
	if err := applyPreRolloutResources(ctx, &deployInput); err != nil {
		return fmt.Errorf("deploy %s: %w", deployInput.ReleaseName, err)
	}
	depBuild, err := chartDependencyBuildPlan(ctx, deployInput)
	if err != nil {
		return fmt.Errorf("deploy %s: %w", deployInput.ReleaseName, err)
	}
	valuesPull := planPublishedValuesPull(ctx, &deployInput)
	command := helmrun.HelmDeployCommand(deployInput)
	ctx.TraceCommand(command.Dir, command.Name, command.Args...)
	helmrun.TracePodWatchAction(ctx, deployInput.ReleaseName, deployInput.Namespace, deployInput.KubernetesContext)

	outcome, handle, err := helmrun.AcquireHelmDeploySingleFlight(ctx, deployInput)
	if err != nil {
		return err
	}
	target := helmDeployTargetLabel(deployInput)
	if outcome == helmrun.HelmDeploySingleFlightSkipDuplicate {
		ctx.Info("==> Skipping " + target + " (identical deploy already in progress)")
		return nil
	}
	defer handle.Release()

	if ctx.DryRun {
		return gateway.PruneGatewayCredentialsSecret(ctx, deployInput)
	}
	stepCtx, finish := ctx.StartTimingStep(target)
	err = runHelmDeployExecute(stepCtx, deployInput, valuesPull, depBuild, deploy, target)
	finish(err)
	return err
}

func runHelmDeployExecute(ctx core.Context, deployInput helmdeploy.HelmDeploySpec, valuesPull *publishedValuesPull, depBuild *helm.CommandSpec, deploy helmdeploy.HelmChartDeployerFunc, target string) error {
	cleanupValues, err := runPublishedValuesPull(ctx, valuesPull, target)
	if err != nil {
		return fmt.Errorf("deploy %s: %w", deployInput.ReleaseName, err)
	}
	defer cleanupValues()
	if err := runChartDependencyBuild(ctx, deployInput, depBuild, target); err != nil {
		return err
	}
	if err := runHelmDeployRollout(ctx, deployInput, deploy, target); err != nil {
		return err
	}
	if err := gateway.PruneGatewayCredentialsSecret(ctx, deployInput); err != nil {
		return fmt.Errorf("deploy %s: %w", deployInput.ReleaseName, err)
	}
	return nil
}

// helmDeployTargetLabel names non-runtime releases so a single-component deploy
// is not mistaken for a full-env redeploy.
func helmDeployTargetLabel(deployInput helmdeploy.HelmDeploySpec) string {
	target := deployInput.Tenant + "/" + deployInput.Environment
	if release := strings.TrimSpace(deployInput.ReleaseName); release != "" && release != runtimetarget.RuntimeReleaseName(deployInput.Tenant) {
		target += " · " + release
	}
	if version := strings.TrimSpace(deployInput.Version); version != "" {
		target += " " + version
	}
	return target
}

// The "==> Deploy of <release> failed" / "==> Deployed <target> in <elapsed>"
// lines are parsed by the desktop activity queue.
func runHelmDeployRollout(ctx core.Context, deployInput helmdeploy.HelmDeploySpec, deploy helmdeploy.HelmChartDeployerFunc, target string) error {
	ctx.Info("==> Deploying " + target)
	ctx.Info("    namespace " + deployInput.Namespace + " on context " + deployInput.KubernetesContext)
	if timeout := strings.TrimSpace(deployInput.Timeout); timeout != "" {
		ctx.Info("    waiting for helm rollout (timeout " + timeout + ")...")
	} else {
		ctx.Info("    waiting for helm rollout...")
	}

	started := time.Now()
	deployErr := helmrun.RunHelmUpgradeWithSelectorRecovery(ctx, deployInput, deploy, target)
	elapsed := time.Since(started).Round(time.Second)
	if deployErr != nil {
		failed := "==> Deploy failed after " + elapsed.String()
		if rel := strings.TrimSpace(deployInput.ReleaseName); rel != "" {
			failed = "==> Deploy of " + rel + " failed after " + elapsed.String()
		}
		ctx.Info(failed)
		return deployErr
	}
	ctx.Info("==> Deployed " + target + " in " + elapsed.String())
	return nil
}

// RunDeploySpec installs already-published artifacts; it builds and pushes
// nothing. SkipHelm avoids rolling unchanged pods.
func RunDeploySpec(ctx core.Context, execution helmdeploy.DeploySpec, deploy helmdeploy.HelmChartDeployerFunc) error {
	if execution.SkipHelm {
		ctx.Trace("deploy: skipping helm upgrade for " + execution.DeployContext.ComponentName + " (all images cached, no rebuild)")
		return nil
	}
	if err := registrycopy.RunDeployRegistryCopies(ctx, execution); err != nil {
		return err
	}
	return RunHelmDeploy(ctx, execution.Deploy, deploy)
}

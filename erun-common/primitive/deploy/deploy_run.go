package deploy

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

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/helm"
	"github.com/sophium/erun/erun-common/infra/kube"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

func runDeploySpecsPlan(ctx core.Context, executions []DeploySpec, deploy HelmChartDeployerFunc) error {
	plan, err := loadProjectK8sPlanForDeploy(executions)
	if err != nil {
		return err
	}
	groups := groupDeploySpecsByPlan(executions, plan)
	for stepIndex, group := range groups {
		if err := runDeployStep(ctx, stepIndex, group, deploy); err != nil {
			return err
		}
	}
	return nil
}

// loadProjectK8sPlanForDeploy reads the k8s deploy plan from the project root
// of the first spec that has a usable RepoPath. All specs in a single deploy
// share a target/repo and an environment, so the first one is authoritative.
// A missing project config yields an empty plan, which the grouper treats as
// "one chart per step, in default order"; a malformed project config
// surfaces as an error so silent misconfiguration cannot ship a wrong plan.
func loadProjectK8sPlanForDeploy(executions []DeploySpec) (configpkg.ProjectK8sConfig, error) {
	for _, execution := range executions {
		plan, err := loadProjectK8sPlanForRepo(execution.Target.RepoPath, execution.Target.Environment)
		if err != nil {
			return configpkg.ProjectK8sConfig{}, err
		}
		if !plan.IsZero() {
			return plan, nil
		}
	}
	return configpkg.ProjectK8sConfig{}, nil
}

func loadProjectK8sPlanForRepo(repoPath, environment string) (configpkg.ProjectK8sConfig, error) {
	repoPath = strings.TrimSpace(repoPath)
	if repoPath == "" {
		return configpkg.ProjectK8sConfig{}, nil
	}
	config, _, err := configpkg.LoadProjectConfig(repoPath)
	if err != nil {
		if errors.Is(err, configpkg.ErrNotInitialized) {
			return configpkg.ProjectK8sConfig{}, nil
		}
		return configpkg.ProjectK8sConfig{}, err
	}
	// A malformed platform block would otherwise surface only later, when
	// deploy templates platform artifacts (PowerDNS zone, exposure hostnames)
	// from these values. Reject it here so an inconsistent base-domain /
	// services-zone / authoritative-IP fails the deploy plan up front.
	if err := config.Platform.Validate(); err != nil {
		return configpkg.ProjectK8sConfig{}, err
	}
	return config.K8sForEnvironment(environment), nil
}

// resolvePublishedDeployPlan resolves the k8s.deployments plan for a
// sourceless (remote-repo) deploy target. The environment's configured repo
// path names where its *own* pod finds the project (e.g. a runtime env's
// in-pod checkout), which is not necessarily reachable from wherever this
// deploy is actually running — the normal case for a runtime environment
// driven from an operator's host. Falling back to the project rooted at the
// invocation's own working directory covers that case: an operator running
// `erun deploy` from inside the tenant's own checkout gets the plan read from
// underneath them, the same as a local-repo deploy would. Every branch traces
// its decision so "the plan is genuinely absent" and "the plan could not be
// reached" are never the same silent outcome (#1116).
func resolvePublishedDeployPlan(ctx core.Context, findProjectRoot shared.ProjectFinderFunc, resolvedTarget configpkg.OpenResult) (configpkg.ProjectK8sConfig, error) {
	configuredPath := strings.TrimSpace(resolvedTarget.RepoPath)
	plan, err := loadProjectK8sPlanForRepo(configuredPath, resolvedTarget.Environment)
	if err != nil {
		return configpkg.ProjectK8sConfig{}, err
	}
	if !plan.IsZero() {
		return plan, nil
	}
	if configuredPath == "" || projectDirReachable(configuredPath) {
		// A reachable configured path with no plan is a legitimate empty plan —
		// no reason to go hunting through an unrelated cwd project for one.
		return configpkg.ProjectK8sConfig{}, nil
	}

	unreachableNotice := "deploy: configured repo path " + configuredPath + " is not present on this machine; "
	fallbackPath := resolveDeployPlanFallbackPath(findProjectRoot, configuredPath)
	if fallbackPath == "" {
		ctx.Trace(unreachableNotice + "no k8s.deployments plan could be read")
		return configpkg.ProjectK8sConfig{}, nil
	}
	fallbackPlan, err := loadProjectK8sPlanForRepo(fallbackPath, resolvedTarget.Environment)
	if err != nil {
		return configpkg.ProjectK8sConfig{}, err
	}
	if fallbackPlan.IsZero() {
		ctx.Trace(unreachableNotice + "no k8s.deployments plan could be read")
		return configpkg.ProjectK8sConfig{}, nil
	}
	ctx.Trace(unreachableNotice + "using the k8s.deployments plan from " + fallbackPath + " instead")
	return fallbackPlan, nil
}

// resolveDeployPlanFallbackPath returns the project root rooted at this
// invocation's own working directory, or "" when none was found or it is the
// same path already tried.
func resolveDeployPlanFallbackPath(findProjectRoot shared.ProjectFinderFunc, configuredPath string) string {
	_, fallbackPath, err := findProjectRoot()
	fallbackPath = strings.TrimSpace(fallbackPath)
	if err != nil || fallbackPath == "" || filepath.Clean(fallbackPath) == filepath.Clean(configuredPath) {
		return ""
	}
	return fallbackPath
}

// projectDirReachable reports whether path names a real directory on this
// machine, distinguishing "the configured repo path does not exist here" (a
// misconfiguration for this deploy's vantage point) from "it exists but has
// no k8s.deployments plan for this environment" (a legitimate empty plan).
func projectDirReachable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// parallelDeployCapture is one goroutine's private output destination for a
// parallel deploy step. Context.Stdout/Stderr and a Logger's own stdout/stderr
// are io.Writer values copied by value into every goroutine's Context, so
// concurrent RunDeploySpec calls would otherwise write them from more than one
// goroutine at once -- a data race when the underlying writer is a
// *bytes.Buffer, as it is on the MCP edge (erun-mcp/runtime.go builds its
// Context from plain bytes.Buffer values). Even where the destination
// tolerates concurrent writers, unsynchronized parallel components interleave
// mid-line into something unreadable. Each goroutine captures its own output
// here; runDeployStep replays every capture back onto the real ctx, in spec
// order, only after wg.Wait() has joined every goroutine.
type parallelDeployCapture struct {
	loggerStdout *bytes.Buffer
	loggerStderr *bytes.Buffer
	ctxStdout    *bytes.Buffer
	ctxStderr    *bytes.Buffer
}

// isolateContextForParallelDeploy returns the Context one parallel-step
// goroutine writes through in place of ctx, plus the buffers to replay
// afterward. A writer left nil on ctx stays nil -- there is nothing to
// isolate or replay for it.
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

// replayParallelDeployCapture writes one goroutine's captured output onto
// ctx's real destinations. Called only after wg.Wait() has joined every
// goroutine, so every capture buffer is done being written to and no
// synchronization is needed for the replay itself.
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

// runDeployStep runs every spec in the group. Single-spec steps and dry-run
// invocations execute serially so traces stay deterministic; a real multi-spec
// step runs them in parallel, each against its own isolated Context, and joins
// their errors and their output once every goroutine has finished.
func runDeployStep(ctx core.Context, stepIndex int, specs []DeploySpec, deploy HelmChartDeployerFunc) error {
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
		go func(i int, spec DeploySpec, specCtx core.Context) {
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

// chartDependencyBuildPlan traces the `helm dependency build` a local umbrella
// chart needs before install and returns the command to run at rollout, or nil
// when none is needed.
//
// A chart that declares dependencies vendors its published subcharts into
// charts/; helm upgrade --install fails when they are absent, and erun deploy is
// the only step that installs the chart — so it builds them first. This stays a
// pure consume primitive: the build pulls already-published dependency charts
// pinned in Chart.lock (it mints nothing) and branches on the chart artifact
// (does it declare deps), never on env type or name.
func chartDependencyBuildPlan(ctx core.Context, deployInput HelmDeploySpec) (*helm.CommandSpec, error) {
	chartPath := strings.TrimSpace(deployInput.ChartPath)
	declares, err := localChartDeclaresDependencies(chartPath)
	if err != nil || !declares {
		return nil, err
	}
	build := helm.CommandSpec{Name: "helm", Args: []string{"dependency", "build", chartPath}}
	ctx.TraceCommand(build.Dir, build.Name, build.Args...)
	return &build, nil
}

// localChartDeclaresDependencies reports whether chartPath is a local chart
// that declares dependencies; an OCI reference is pulled whole.
func localChartDeclaresDependencies(chartPath string) (bool, error) {
	if chartPath == "" || isOCIChartReference(chartPath) {
		return false, nil
	}
	return helm.HelmChartDeclaresDependencies(chartPath)
}

// runChartDependencyBuild executes the planned dependency build (a no-op when
// there is nothing to build). Called after the single-flight acquire so a
// deduped or parallel deploy never races on the shared charts/ dir.
func runChartDependencyBuild(ctx core.Context, deployInput HelmDeploySpec, build *helm.CommandSpec, target string) error {
	if build == nil {
		return nil
	}
	ctx.Info("==> Building chart dependencies for " + target)
	if err := helm.RunHelmCommand(ctx, *build); err != nil {
		return fmt.Errorf("deploy %s: helm dependency build: %w", deployInput.ReleaseName, err)
	}
	return nil
}

// publishedValuesPull is a planned deploy-time `helm pull --untar` of a
// by-reference umbrella, plus the temp dir it unpacks into (removed after the
// rollout).
type publishedValuesPull struct {
	command helm.CommandSpec
	dest    string
}

// planPublishedValuesPull plans the pull of a by-reference umbrella's bundled
// values.<env>.yaml and records the local path on deployInput so command() -f's
// it. A tenant's published <tenant>-<base> umbrella ships its per-env subchart
// values (pod-shape, overrides authored under the subchart key) inside the chart,
// but helm applies only the default values.yaml — so those never reach a
// by-reference deploy. This unpacks the published chart to a temp dir and points
// -f at the bundled file, the by-reference analogue of a worktree deploy's local
// values.<env>.yaml. Returns nil for a canonical chart installed directly (no
// wrapper, no bundled per-env values) and every local chart (which already -f
// their worktree values.<env>.yaml). The dest path is deterministic so a deduped
// or repeated deploy traces byte-identically.
func planPublishedValuesPull(ctx core.Context, deployInput *HelmDeploySpec) *publishedValuesPull {
	ref := strings.TrimSpace(deployInput.ChartPath)
	if !isOCIChartReference(ref) || strings.TrimSpace(deployInput.SubchartKey) == "" {
		return nil
	}
	chartName := ref[strings.LastIndex(ref, "/")+1:]
	dest := filepath.Join(os.TempDir(), "erun-deploy-values-"+chartName)
	envSlug := strings.ToLower(strings.TrimSpace(deployInput.Environment))
	deployInput.PulledValuesFilePath = filepath.Join(dest, chartName, "values."+envSlug+".yaml")
	pull := helm.CommandSpec{Name: "helm", Args: []string{"pull", ref, "--version", deployInput.resolvedChartVersion(), "--untar", "--untardir", dest}}
	ctx.TraceCommand(pull.Dir, pull.Name, pull.Args...)
	return &publishedValuesPull{command: pull, dest: dest}
}

// runPublishedValuesPull executes the planned chart-values pull and returns a
// cleanup that removes the temp dir. The dest is cleared first so a stale
// extraction from a prior deploy never masks the current version's values.
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

// ensureDeployNamespace creates the deploy's namespace if it is missing. A var
// so a test can prove it runs before the secret applies that require it --
// trace order cannot, because the trace already claimed this order while the
// code did not follow it.
var ensureDeployNamespace NamespaceEnsurerFunc = kube.EnsureKubernetesNamespace

// applyPreRolloutResources creates the namespace and applies everything that
// must exist in it before the chart rollout, including the credentials the
// deployment plan resolved on this host. Grouped rather than inline so the
// ordering is stated in one place as this list grows.
//
// The namespace is ensured here and not only in the chart deployer, because the
// secrets below are real writes into it: on a first deploy -- a freshly
// provisioned hosted environment, where nothing has created it yet -- they fail
// with "namespaces not found". An environment that already exists hid this,
// which is why it only ever broke provisioning. Ensuring is idempotent, so the
// deployer ensuring it again afterwards costs nothing.
func applyPreRolloutResources(ctx core.Context, deployInput *HelmDeploySpec) error {
	if !ctx.DryRun {
		if err := ensureDeployNamespace(deployInput.KubernetesContext, deployInput.Namespace); err != nil {
			return err
		}
	}
	if err := applyCloudflareCredentialsSecret(ctx, *deployInput); err != nil {
		return err
	}
	if err := applyGatewayCredentialsSecret(ctx, *deployInput); err != nil {
		return err
	}
	if err := applyMCPAuthSecret(ctx, *deployInput); err != nil {
		return err
	}
	if err := refreshImagePullSecrets(ctx, *deployInput); err != nil {
		return err
	}
	// Takes the spec rather than a copy: the chart mounts the platform-alias
	// volume only when the upgrade names the Secret, so a retrofit has to reach
	// the command built below from this same spec.
	if err := reconcilePlatformAliasSecret(ctx, deployInput); err != nil {
		return err
	}
	return recordMCPAuthKeyOnEnv(ctx, *deployInput)
}

func RunHelmDeploy(ctx core.Context, deployInput HelmDeploySpec, deploy HelmChartDeployerFunc) error {
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
	// A by-reference umbrella's bundled values.<env>.yaml is pulled before the
	// rollout; planning it here sets deployInput.PulledValuesFilePath so the
	// upgrade command below -f's it.
	valuesPull := planPublishedValuesPull(ctx, &deployInput)
	command := deployInput.command()
	ctx.TraceCommand(command.Dir, command.Name, command.Args...)
	tracePodWatchAction(ctx, deployInput.ReleaseName, deployInput.Namespace, deployInput.KubernetesContext)

	outcome, handle, err := AcquireHelmDeploySingleFlight(ctx, deployInput)
	if err != nil {
		return err
	}
	target := helmDeployTargetLabel(deployInput)
	if outcome == HelmDeploySingleFlightSkipDuplicate {
		ctx.Info("==> Skipping " + target + " (identical deploy already in progress)")
		return nil
	}
	defer handle.Release()

	if ctx.DryRun {
		return pruneGatewayCredentialsSecret(ctx, deployInput)
	}
	stepCtx, finish := ctx.StartTimingStep(target)
	err = runHelmDeployExecute(stepCtx, deployInput, valuesPull, depBuild, deploy, target)
	finish(err)
	return err
}

// runHelmDeployExecute performs the real-run pre-rollout steps and the rollout:
// pull a by-reference umbrella's bundled values, build a local umbrella's
// dependencies, then upgrade. Split out of RunHelmDeploy so the orchestrator
// stays a thin trace-then-execute shell.
func runHelmDeployExecute(ctx core.Context, deployInput HelmDeploySpec, valuesPull *publishedValuesPull, depBuild *helm.CommandSpec, deploy HelmChartDeployerFunc, target string) error {
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
	if err := pruneGatewayCredentialsSecret(ctx, deployInput); err != nil {
		return fmt.Errorf("deploy %s: %w", deployInput.ReleaseName, err)
	}
	return nil
}

// helmDeployTargetLabel builds the label used in deploy feedback. It names the
// release for a non-runtime component so a single-component deploy (e.g.
// erun-backend-postgres) is not mistaken for a full-env redeploy. The runtime
// chart's line stays "<tenant>/<env> <version>" — it *is* the env, carries the
// meaningful runtime version, and feeds the helm-release poller + version
// persistence.
func helmDeployTargetLabel(deployInput HelmDeploySpec) string {
	target := deployInput.Tenant + "/" + deployInput.Environment
	if release := strings.TrimSpace(deployInput.ReleaseName); release != "" && release != configpkg.RuntimeReleaseName(deployInput.Tenant) {
		target += " · " + release
	}
	if version := strings.TrimSpace(deployInput.Version); version != "" {
		target += " " + version
	}
	return target
}

// runHelmDeployRollout performs the real-run helm rollout for an acquired,
// non-duplicate deploy. The "==> Deploy of <release> failed" / "==> Deployed
// <target> in <elapsed>" lines are part of the trace contract the desktop
// activity queue parses.
func runHelmDeployRollout(ctx core.Context, deployInput HelmDeploySpec, deploy HelmChartDeployerFunc, target string) error {
	ctx.Info("==> Deploying " + target)
	ctx.Info("    namespace " + deployInput.Namespace + " on context " + deployInput.KubernetesContext)
	if timeout := strings.TrimSpace(deployInput.Timeout); timeout != "" {
		ctx.Info("    waiting for helm rollout (timeout " + timeout + ")...")
	} else {
		ctx.Info("    waiting for helm rollout...")
	}

	started := time.Now()
	deployErr := runHelmUpgradeWithSelectorRecovery(ctx, deployInput, deploy, target)
	elapsed := time.Since(started).Round(time.Second)
	if deployErr != nil {
		// Name the release so a parallel step (multiple charts at once) makes
		// clear which one failed.
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

// RunDeploySpec runs the pure helm install for a resolved deploy plan. It
// builds nothing, pushes nothing, and publishes nothing — the image and chart
// it installs were produced and published by `build` and `push` before deploy
// ran. It may still mirror an already-published image between registries when
// the env marks a FROM/TO pair (a consume-side manifest copy, not a build).
// SkipHelm (set by an orchestrator when every referenced image was cached)
// suppresses the upgrade so unchanged pods are not rolled.
func RunDeploySpec(ctx core.Context, execution DeploySpec, deploy HelmChartDeployerFunc) error {
	if execution.SkipHelm {
		ctx.Trace("deploy: skipping helm upgrade for " + execution.DeployContext.ComponentName + " (all images cached, no rebuild)")
		return nil
	}
	if err := RunDeployRegistryCopies(ctx, execution); err != nil {
		return err
	}
	return RunHelmDeploy(ctx, execution.Deploy, deploy)
}

package eruncommon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

const DefaultHelmDeploymentTimeout = "2m0s"

const DevopsComponentName = "erun-devops"

const (
	WorktreeStorageHost = "host"
	WorktreeStoragePVC  = "pvc"
	// WorktreeStorageNone is reported for runtime envs that have no worktree.
	// The erun-devops chart is not deployed for runtime envs, so this value
	// is informational; chart-side compat treats it as "skip worktree mount".
	WorktreeStorageNone = "none"
)

type DeployStore interface {
	OpenStore
	ListTenantConfigs() ([]TenantConfig, error)
	ListEnvConfigs(string) ([]EnvConfig, error)
}

type (
	DeployContextResolverFunc       func() (KubernetesDeployContext, error)
	KubernetesDeploymentCheckerFunc func(Context, KubernetesDeploymentCheckParams) (bool, error)
	HelmChartDeployerFunc           func(HelmDeployParams) error
	HelmReleaseRecovererFunc        func(HelmReleaseRecoveryParams) error
)

type deployKubernetesContextResolver interface {
	ResolveDeployKubernetesContext(environment, configured string) string
}

type KubernetesDeployContext struct {
	Dir           string
	ComponentName string
	ChartPath     string
}

type HelmDeployParams struct {
	ReleaseName        string
	ChartPath          string
	ValuesFilePath     string
	Tenant             string
	Environment        string
	Namespace          string
	KubernetesContext  string
	WorktreeStorage    string
	WorktreeRepoName   string
	WorktreeHostPath   string
	SSHDEnabled        bool
	MCPPort            int
	APIPort            int
	SSHPort            int
	ManagedCloud       bool
	CloudContextName   string
	CloudProvider      string
	CloudProviderAlias string
	CloudRegion        string
	CloudInstanceID    string
	UseHostCredentials bool
	OIDCAllowedIssuers string
	ContainerRegistry  string
	ImageOverrides     map[string]string
	ResetDatabase      bool
	Idle               EnvironmentIdleConfig
	Claude             EnvironmentClaudeConfig
	RuntimePod         RuntimePodResources
	Version            string
	Timeout            string
	Verbosity          int
	Stdout             io.Writer
	Stderr             io.Writer
}

type HelmDeploySpec struct {
	ReleaseName        string
	ChartPath          string
	ValuesFilePath     string
	Tenant             string
	Environment        string
	Namespace          string
	KubernetesContext  string
	WorktreeStorage    string
	WorktreeRepoName   string
	WorktreeHostPath   string
	SSHDEnabled        bool
	MCPPort            int
	APIPort            int
	SSHPort            int
	ManagedCloud       bool
	CloudContextName   string
	CloudProvider      string
	CloudProviderAlias string
	CloudRegion        string
	CloudInstanceID    string
	UseHostCredentials bool
	OIDCAllowedIssuers string
	ContainerRegistry  string
	ImageOverrides     map[string]string
	ResetDatabase      bool
	Idle               EnvironmentIdleConfig
	Claude             EnvironmentClaudeConfig
	RuntimePod         RuntimePodResources
	Version            string
	Timeout            string
	Verbosity          int
}

type HelmReleaseRecoveryParams struct {
	ReleaseName       string
	Namespace         string
	KubernetesContext string
	Verbosity         int
	Stdout            io.Writer
	Stderr            io.Writer
}

type HelmReleasePendingOperationError struct {
	ReleaseName       string
	Namespace         string
	KubernetesContext string
	Message           string
	Err               error
}

type KubernetesDeploymentCheckParams struct {
	Name               string
	Namespace          string
	KubernetesContext  string
	ExpectedRepoPath   string
	ExpectedSSHD       *bool
	ExpectedMCPPort    int
	ExpectedSSHPort    int
	ExpectedRuntimePod RuntimePodResources
}

type DeployTarget struct {
	Tenant          string
	Environment     string
	RepoPath        string
	VersionOverride string
	// Components lists optional opt-in charts to include alongside the
	// always-on charts (e.g. the per-tenant runtime). Names must come from
	// optInDeployComponents; unknown names produce an error during resolve.
	Components []string
	// Force re-runs the helm upgrade even when the deployed release already
	// matches the requested version (no-op rollouts are otherwise skipped).
	Force bool
}

// DeploySpec is a pure helm-install plan: it installs the image and chart
// already published at a version, by reference. deploy does not build, push,
// or publish — those are the build and push primitives, composed above deploy
// by an orchestrator (the `build --deploy` shortcut, `erun open --deploy`, or
// the UI). The image reference rides in via Deploy.ImageOverrides /
// Deploy.Version; the chart reference is DeployContext.ChartPath (a local path
// or a published OCI ref), resolved by the caller.
type DeploySpec struct {
	Target        OpenResult
	DeployContext KubernetesDeployContext
	Deploy        HelmDeploySpec
	// SkipHelm lets an orchestrator suppress the helm upgrade when every
	// image this chart references was promoted from cache (no rebuild), so
	// unchanged pods are not rolled. A pure deploy leaves it false; the
	// build orchestration sets it from its build results.
	SkipHelm bool
}

// EnvConfigSaver writes an updated env config to disk. The contract
// matches what (ConfigStore).SaveEnvConfig provides; CLI and MCP wire
// it through their stores so shared code can persist post-deploy
// state without depending on the full ConfigStore.
type EnvConfigSaver func(tenant string, config EnvConfig) error

// PersistRuntimeVersionFromDeploySpecs writes the version and source
// registry of the runtime chart that was just deployed back into the
// env config so downstream readers (the desktop runtime dialog, `erun
// list`, and any `erun open` invocation that doesn't pass --version)
// reflect the deployed state. Without this, `erun deploy --version
// 1.0.54` left env config's runtimeversion at the previously persisted
// value and the dialog kept rendering the stale string.
//
// The registry is recorded alongside the version as provenance so a
// subsequent reopen can address the same image even if the user later
// edits the project's container registry. See issue #363.
//
// Looks for the spec whose ReleaseName equals <tenant>-devops; if
// found and its Deploy.Version or Deploy.ContainerRegistry differ from
// the env config, the env config is rewritten with the deployed pair.
// Component-only deploys (no runtime chart in the spec list) are
// no-ops, as are dry-runs and calls with a nil saver.
//
// A runtime chart whose helm upgrade was skipped (SkipHelm: every image
// promoted from the fingerprint cache, so nothing was rebuilt, pushed, or
// rolled out) keeps a freshly minted Deploy.Version that was never pushed.
// Persisting that would point the env config — and the desktop runtime dialog —
// at a phantom version the deploy picker can never offer (it gates on registry
// presence). Instead, heal RuntimeVersion to the version the release is actually
// running (resolveDeployedVersion reads the live helm appVersion), which is
// guaranteed pushed. When that cannot be read, leave RuntimeVersion untouched
// rather than record a phantom. See issue #475.
func PersistRuntimeVersionFromDeploySpecs(ctx Context, specs []DeploySpec, save EnvConfigSaver, resolveDeployedVersion HelmReleaseVersionResolverFunc) error {
	if save == nil || ctx.DryRun || len(specs) == 0 {
		return nil
	}
	for _, spec := range specs {
		if !specDeploysRuntimeChart(spec) {
			continue
		}
		if spec.SkipHelm {
			version := resolveRunningRuntimeVersion(ctx, spec, resolveDeployedVersion)
			if version == "" {
				return nil
			}
			return persistRuntimeVersionIfChanged(spec, version, save)
		}
		version := strings.TrimSpace(spec.Deploy.Version)
		if version == "" {
			continue
		}
		return persistRuntimeVersionIfChanged(spec, version, save)
	}
	return nil
}

// resolveRunningRuntimeVersion returns the version the runtime release is
// actually running, used to heal RuntimeVersion after a cached (SkipHelm)
// deploy. An empty string means "could not determine" — the caller then leaves
// the persisted version untouched rather than recording the never-pushed mint.
func resolveRunningRuntimeVersion(ctx Context, spec DeploySpec, resolveDeployedVersion HelmReleaseVersionResolverFunc) string {
	if resolveDeployedVersion == nil {
		return ""
	}
	version, err := resolveDeployedVersion(ctx, spec.Deploy.ReleaseName, spec.Deploy.Namespace, spec.Deploy.KubernetesContext)
	if err != nil {
		ctx.Trace("persist runtime version: reading deployed version for " + spec.Deploy.ReleaseName + " failed: " + err.Error())
		return ""
	}
	return strings.TrimSpace(version)
}

func persistRuntimeVersionIfChanged(spec DeploySpec, version string, save EnvConfigSaver) error {
	registry := strings.TrimSpace(spec.Deploy.ContainerRegistry)
	envConfig := spec.Target.EnvConfig
	if strings.TrimSpace(envConfig.RuntimeVersion) == version && strings.TrimSpace(envConfig.RuntimeRegistry) == registry {
		return nil
	}
	envConfig.RuntimeVersion = version
	envConfig.RuntimeRegistry = registry
	if err := save(spec.Target.Tenant, envConfig); err != nil {
		return fmt.Errorf("persist runtime version after deploy: %w", err)
	}
	return nil
}

// HelmReleaseVersionResolverFunc reads the appVersion of a deployed helm release
// — the runtime version the cluster is actually running — so a cached (SkipHelm)
// deploy can heal EnvConfig.RuntimeVersion to a real, pushed version instead of
// leaving a stale or phantom value. See issue #475.
type HelmReleaseVersionResolverFunc func(ctx Context, releaseName, namespace, kubernetesContext string) (string, error)

// ResolveDeployedHelmReleaseVersion returns the appVersion of the named helm
// release (the runtime version the cluster is actually running). It returns an
// empty string with a nil error when the release is absent or helm cannot be
// queried, so reading the version never blocks a deploy or open.
func ResolveDeployedHelmReleaseVersion(ctx Context, releaseName, namespace, kubernetesContext string) (string, error) {
	releaseName = strings.TrimSpace(releaseName)
	namespace = strings.TrimSpace(namespace)
	if releaseName == "" || namespace == "" {
		return "", nil
	}
	args := []string{"get", "metadata", releaseName, "--namespace", namespace, "-o", "json"}
	if kubernetesContext = strings.TrimSpace(kubernetesContext); kubernetesContext != "" {
		args = append(args, "--kube-context", kubernetesContext)
	}
	output, err := Command("helm", args...).Output()
	if err != nil {
		return "", nil
	}
	var metadata struct {
		AppVersion string `json:"appVersion"`
	}
	if err := json.Unmarshal(output, &metadata); err != nil {
		return "", nil
	}
	return strings.TrimSpace(metadata.AppVersion), nil
}

func specDeploysRuntimeChart(spec DeploySpec) bool {
	tenant := strings.TrimSpace(spec.Target.Tenant)
	return tenant != "" && strings.TrimSpace(spec.Deploy.ReleaseName) == RuntimeReleaseName(tenant)
}

func RunDeploySpecs(ctx Context, executions []DeploySpec, deploy HelmChartDeployerFunc) error {
	if len(executions) == 0 {
		return nil
	}
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
func loadProjectK8sPlanForDeploy(executions []DeploySpec) (ProjectK8sConfig, error) {
	for _, execution := range executions {
		plan, err := loadProjectK8sPlanForRepo(execution.Target.RepoPath, execution.Target.Environment)
		if err != nil {
			return ProjectK8sConfig{}, err
		}
		if !plan.IsZero() {
			return plan, nil
		}
	}
	return ProjectK8sConfig{}, nil
}

func loadProjectK8sPlanForRepo(repoPath, environment string) (ProjectK8sConfig, error) {
	repoPath = strings.TrimSpace(repoPath)
	if repoPath == "" {
		return ProjectK8sConfig{}, nil
	}
	config, _, err := LoadProjectConfig(repoPath)
	if err != nil {
		if errors.Is(err, ErrNotInitialized) {
			return ProjectK8sConfig{}, nil
		}
		return ProjectK8sConfig{}, err
	}
	return config.K8sForEnvironment(environment), nil
}

// runDeployStep runs every spec in the group. Single-spec steps and dry-run
// invocations execute serially so traces remain deterministic; real runs with
// multiple specs in the same step launch goroutines and wait for all to
// finish, surfacing the joined error if any failed.
func runDeployStep(ctx Context, stepIndex int, specs []DeploySpec, deploy HelmChartDeployerFunc) error {
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
	for i, spec := range specs {
		wg.Add(1)
		go func(i int, spec DeploySpec) {
			defer wg.Done()
			errs[i] = RunDeploySpec(ctx, spec, deploy)
		}(i, spec)
	}
	wg.Wait()
	return errors.Join(errs...)
}

func RunHelmDeploy(ctx Context, deployInput HelmDeploySpec, deploy HelmChartDeployerFunc) error {
	if deploy == nil {
		return fmt.Errorf("helm deployer is required")
	}
	deployInput.Verbosity = ctx.Verbosity
	if err := ctx.RequireKubernetesContext(deployInput.KubernetesContext); err != nil {
		return fmt.Errorf("deploy %s: %w", deployInput.ReleaseName, err)
	}
	TraceEnsureKubernetesNamespace(ctx, deployInput.KubernetesContext, deployInput.Namespace)
	command := deployInput.command()
	ctx.TraceCommand(command.Dir, command.Name, command.Args...)
	tracePodWatchAction(ctx, deployInput.ReleaseName, deployInput.Namespace, deployInput.KubernetesContext)

	outcome, handle, err := AcquireHelmDeploySingleFlight(ctx, deployInput)
	if err != nil {
		return err
	}
	target := deployInput.Tenant + "/" + deployInput.Environment
	if version := strings.TrimSpace(deployInput.Version); version != "" {
		target += " " + version
	}
	if outcome == HelmDeploySingleFlightSkipDuplicate {
		ctx.Info("==> Skipping " + target + " (identical deploy already in progress)")
		return nil
	}
	defer handle.Release()

	if ctx.DryRun {
		return nil
	}

	ctx.Info("==> Deploying " + target)
	ctx.Info("    namespace " + deployInput.Namespace + " on context " + deployInput.KubernetesContext)
	if timeout := strings.TrimSpace(deployInput.Timeout); timeout != "" {
		ctx.Info("    waiting for helm rollout (timeout " + timeout + ")...")
	} else {
		ctx.Info("    waiting for helm rollout...")
	}

	started := time.Now()
	spinner := StartSpinner(ctx.Stderr, "deploying "+target)
	deployErr := deploy(deployInput.Params(ctx.Stdout, ctx.Stderr))
	spinner.Stop()
	elapsed := time.Since(started).Round(time.Second)
	if deployErr != nil {
		// Name the release so a parallel step (multiple charts deploying at
		// once) makes clear which one failed; the helm reason is in the
		// returned error, surfaced by the command's error handler.
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
func RunDeploySpec(ctx Context, execution DeploySpec, deploy HelmChartDeployerFunc) error {
	if execution.SkipHelm {
		ctx.Trace("deploy: skipping helm upgrade for " + execution.DeployContext.ComponentName + " (all images cached, no rebuild)")
		return nil
	}
	if err := runDeployRegistryCopies(ctx, execution); err != nil {
		return err
	}
	return RunHelmDeploy(ctx, execution.Deploy, deploy)
}

func ResolveDeploySpec(ctx Context, store DeployStore, findProjectRoot ProjectFinderFunc, resolveDockerBuildContext BuildContextResolverFunc, resolveKubernetesDeployContext DeployContextResolverFunc, now NowFunc, target DeployTarget, componentName, versionOverride string) (DeploySpec, error) {
	store, findProjectRoot, resolveDockerBuildContext, _, now = normalizeDeployDependencies(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now)
	versionOverride = resolveDeployVersionOverride(target, versionOverride)

	resolvedTarget, err := resolveDeployTarget(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, target)
	if err != nil {
		return DeploySpec{}, err
	}
	spec, err := resolveDeploySpecForOpenResult(ctx, store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, resolvedTarget, componentName, versionOverride, nil)
	if err != nil {
		return DeploySpec{}, err
	}
	return spec, nil
}

// ResolveCurrentDeploySpecs resolves specs for `erun deploy` — the pure deploy
// primitive that installs a version by reference and never builds.
func ResolveCurrentDeploySpecs(ctx Context, store DeployStore, findProjectRoot ProjectFinderFunc, resolveDockerBuildContext BuildContextResolverFunc, resolveKubernetesDeployContext DeployContextResolverFunc, now NowFunc, target DeployTarget) ([]DeploySpec, error) {
	return resolveCurrentDeploySpecs(ctx, store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, target, false)
}

// resolveCurrentDeploySpecs is shared by `erun deploy` (buildOrchestration
// false → pure, version required) and the `build --deploy` orchestration
// (buildOrchestration true → reference the just-built working-tree image of a
// builds-here env). The build/push of that image is run by the build phase
// before these specs deploy; here it only supplies the image reference.
func resolveCurrentDeploySpecs(ctx Context, store DeployStore, findProjectRoot ProjectFinderFunc, resolveDockerBuildContext BuildContextResolverFunc, resolveKubernetesDeployContext DeployContextResolverFunc, now NowFunc, target DeployTarget, buildOrchestration bool) ([]DeploySpec, error) {
	store, findProjectRoot, resolveDockerBuildContext, _, now = normalizeDeployDependencies(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now)
	now = freezeNow(now)

	resolvedTarget, err := resolveDeployTarget(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, target)
	if err != nil {
		return nil, err
	}

	if resolvedTarget.RemoteRepo() {
		spec, err := resolvePublishedDevopsDeploySpec(ctx, resolvedTarget, target.VersionOverride)
		if err != nil {
			return nil, err
		}
		if err := configureDeployInputMetadata(store, resolvedTarget, &spec.Deploy); err != nil {
			return nil, err
		}
		return []DeploySpec{spec}, nil
	}

	deployContexts, err := ResolveCurrentKubernetesDeployContexts(findProjectRoot, resolveKubernetesDeployContext, resolvedTarget.RepoPath)
	if err != nil {
		return nil, err
	}
	projectK8s, err := loadProjectK8sPlanForRepo(resolvedTarget.RepoPath, resolvedTarget.Environment)
	if err != nil {
		return nil, err
	}
	deployContexts, err = filterDeployContextsByComponents(deployContexts, target.Components, projectK8s)
	if err != nil {
		return nil, err
	}
	sortDeployContextsByDeployOrder(deployContexts, projectK8s)

	var currentBuild *DockerBuildSpec
	if buildOrchestration && resolvedTarget.EnvConfig.BuildsHere() {
		// The build phase produces this version (a minted snapshot, or the
		// explicit --version); deploy references the built image by tag rather
		// than install-by-reference, which would verify a registry tag the
		// build has not pushed yet.
		currentBuild, err = resolveCurrentDockerComponentBuildForDeploy(store, findProjectRoot, resolveDockerBuildContext, now, resolvedTarget.RepoPath, resolvedTarget.Environment, target.VersionOverride)
		if err != nil {
			return nil, err
		}
	}

	specs := make([]DeploySpec, 0, len(deployContexts))
	for _, deployContext := range deployContexts {
		spec, err := resolveDeploySpecForContext(ctx, store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, resolvedTarget, deployContext, target.VersionOverride, currentBuild)
		if err != nil {
			return nil, err
		}
		specs = append(specs, spec)
	}

	return specs, nil
}

func resolveDeploySpecForOpenResult(ctx Context, store DeployStore, findProjectRoot ProjectFinderFunc, resolveDockerBuildContext BuildContextResolverFunc, resolveKubernetesDeployContext DeployContextResolverFunc, now NowFunc, target OpenResult, componentName, versionOverride string, currentBuild *DockerBuildSpec) (DeploySpec, error) {
	store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now = normalizeDeployDependencies(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now)
	now = freezeNow(now)

	deployContext, err := resolveDeployContextForTarget(findProjectRoot, resolveKubernetesDeployContext, target, componentName)
	if err != nil {
		return DeploySpec{}, err
	}

	return resolveDeploySpecForContext(ctx, store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, target, deployContext, versionOverride, currentBuild)
}

func resolveDeploySpecForContext(ctx Context, store DeployStore, findProjectRoot ProjectFinderFunc, resolveDockerBuildContext BuildContextResolverFunc, resolveKubernetesDeployContext DeployContextResolverFunc, now NowFunc, target OpenResult, deployContext KubernetesDeployContext, versionOverride string, currentBuild *DockerBuildSpec) (DeploySpec, error) {
	// A pure deploy installs by reference; only the store is consulted (for
	// cloud/tenant metadata and image verification). findProjectRoot,
	// resolveDockerBuildContext, resolveKubernetesDeployContext, and now are
	// retained on the signature for the shared resolution contract but are not
	// needed once deploy stopped building.
	store, _, _, _, _ = normalizeDeployDependencies(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now)
	target = applyDeployKubernetesContext(store, target)

	// Build-orchestration path: a `build --deploy` / `open --deploy` / UI
	// orchestration has already built and pushed the working-tree image and
	// hands it to deploy by reference. deploy installs it via an ImageOverride
	// at the built version; it does not build it (see resolveDeploySpecForCurrentDockerBuild).
	if currentBuild != nil && deployContextOwnsDockerBuild(deployContext, *currentBuild) {
		return resolveDeploySpecForCurrentDockerBuild(store, target, deployContext, *currentBuild)
	}

	// Pure deploy: install the image + chart already published at a version,
	// by reference. A version is a content identity minted by build/push;
	// deploy never builds or synthesizes one. The version comes from the
	// explicit override or, for a redeploy of the env's current version, the
	// persisted RuntimeVersion (the CLI --current switch). With neither, the
	// operator has not said what to deploy — that is an error, not a trigger
	// to build the working tree.
	version := strings.TrimSpace(versionOverride)
	versionFromPersist := false
	if version == "" {
		version = strings.TrimSpace(target.EnvConfig.RuntimeVersion)
		if version != "" {
			versionFromPersist = true
		}
	}
	if version == "" {
		return DeploySpec{}, fmt.Errorf("deploy: a version is required — pass a version produced by `erun build`/`erun push`, or `--current` to redeploy the version this environment already runs")
	}
	return resolveInstallExistingVersionDeploySpec(ctx, store, target, deployContext, version, versionFromPersist)
}

// resolveInstallExistingVersionDeploySpec resolves a deploy that installs the
// image already published at the pinned version, with no local build and no
// push. deploy and upgrade are consume operations: a version names a content
// identity, so a builds-here env addresses the published tag by reference
// rather than rebuilding the working tree under that label. The chart's
// referenced images are verified to exist (locally or in the registry) so a
// version that was never built fails fast — deploy installs, it does not
// build. helm still runs (no builds means SkipHelm stays false), pinned to the
// requested version. See issue #556.
func resolveInstallExistingVersionDeploySpec(ctx Context, store DeployStore, target OpenResult, deployContext KubernetesDeployContext, version string, versionFromPersist bool) (DeploySpec, error) {
	deployInput, err := newHelmDeploySpec(target, deployContext, version)
	if err != nil {
		return DeploySpec{}, err
	}
	// Pull-path provenance: when installing the persisted version (a --current
	// redeploy or an open ensure), address the same registry the previous
	// deploy used, so a reopen survives the operator editing the project's
	// container registry. See issue #363.
	if versionFromPersist && deployContextOwnsRuntimeChart(deployContext, target.Tenant) {
		if registry := strings.TrimSpace(target.EnvConfig.RuntimeRegistry); registry != "" {
			deployInput.ContainerRegistry = registry
		}
	}
	// Mirror the snapshot DB-reset decision so re-installing a snapshot behaves
	// the same as first deploying one (#270).
	deployInput.ResetDatabase = deployResetsDatabase(true, deployInput.Version)
	if err := configureDeployInputMetadata(store, target, &deployInput); err != nil {
		return DeploySpec{}, err
	}

	ctx.Trace("deploy: version " + deployInput.Version + " pinned; installing the published image, no local build")
	images, err := findDockerImagesInChart(deployContext.ChartPath, deployInput.Version)
	if err != nil {
		return DeploySpec{}, err
	}
	for _, image := range images {
		if err := verifyExistingDeployImage(ctx, image, deployInput.Version, deployInput.ContainerRegistry); err != nil {
			return DeploySpec{}, err
		}
	}

	return DeploySpec{
		Target:        target,
		DeployContext: deployContext,
		Deploy:        deployInput,
	}, nil
}

// verifyExistingDeployImage fails when the image a chart pulls at the pinned
// version is absent both locally and in the registry. deploy installs an
// existing version; a missing image means the version was never built, which
// is an error, not a trigger to build it from the working tree.
//
// Only images at the deploy version are checked: charts also reference pinned
// infra/base images (dind, binfmt) at their own versions, which are not the
// version being installed and are skipped. Registry-less chart refs (the app
// images, which get their registry from --set containerRegistry) are qualified
// with the deploy registry so the lookup hits the published tag. Dry-run traces
// the lookup and skips the network so the plan stays offline and deterministic;
// a registry error that is not a definitive "absent" does not block the deploy
// (the rollout would surface a real pull failure).
func verifyExistingDeployImage(ctx Context, ref, version, registry string) error {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil
	}
	nameTag := ref
	qualified := false
	if idx := strings.LastIndex(ref, "/"); idx >= 0 {
		nameTag = ref[idx+1:]
		qualified = true
	}
	_, refVersion, ok := strings.Cut(nameTag, ":")
	if !ok || strings.TrimSpace(refVersion) != strings.TrimSpace(version) {
		// Not the version being installed (pinned infra/base image) — not ours
		// to verify.
		return nil
	}

	tag := ref
	if !qualified && strings.TrimSpace(registry) != "" {
		tag = strings.TrimRight(strings.TrimSpace(registry), "/") + "/" + ref
	}

	ctx.TraceCommand("", "docker", "manifest", "inspect", tag)
	if ctx.DryRun {
		return nil
	}
	remote, remoteErr := DockerManifestExists(tag)
	if remote {
		return nil
	}
	if local, _ := DockerImageExists(tag); local {
		return nil
	}
	if remoteErr != nil {
		// The registry could not confirm presence (network/auth, not a
		// definitive "absent"); don't block the deploy on it — the rollout
		// surfaces a real pull failure if the image is genuinely missing.
		ctx.Trace("deploy: could not verify " + tag + " in the registry (" + remoteErr.Error() + "); proceeding")
		return nil
	}
	return fmt.Errorf("deploy --version %s: image %s is not present locally or in the registry; deploy installs an existing version and does not build it — run erun build/push to create it first", version, tag)
}

func configureDeployInputMetadata(store DeployStore, target OpenResult, deployInput *HelmDeploySpec) error {
	issuers, err := ResolveTenantCloudProviderIssuers(store, target.TenantConfig)
	if err != nil {
		return err
	}
	deployInput.OIDCAllowedIssuers = strings.Join(issuers, ",")
	managedCloud, err := managedCloudEnvironment(store, target.EnvConfig)
	if err != nil {
		return err
	}
	deployInput.ManagedCloud = managedCloud
	deployInput.UseHostCredentials = target.EnvConfig.RemoteHostCredentials
	applyCloudProviderDeployMetadata(store, target.EnvConfig, deployInput)
	if managedCloud {
		applyCloudContextStopMetadata(store, target.EnvConfig, deployInput)
	}
	return nil
}

// resolveDeploySpecForCurrentDockerBuild builds the pure deploy plan for a
// `build --deploy` orchestration: the working-tree image has already been
// built and pushed by the build phase, so deploy references it by tag via an
// ImageOverride and never builds. SkipHelm stays false (a build-and-deploy
// rolls the chart); the orchestrator may set it when nothing was rebuilt.
func resolveDeploySpecForCurrentDockerBuild(store DeployStore, target OpenResult, deployContext KubernetesDeployContext, build DockerBuildSpec) (DeploySpec, error) {
	deployInput, err := newHelmDeploySpec(target, deployContext, "")
	if err != nil {
		return DeploySpec{}, err
	}
	deployInput.ResetDatabase = deployResetsDatabase(false, build.Image.Version)
	if err := configureDeployInputMetadata(store, target, &deployInput); err != nil {
		return DeploySpec{}, err
	}
	deployInput.ImageOverrides = map[string]string{
		build.Image.ImageName: build.Image.Tag,
	}

	return DeploySpec{
		Target:        target,
		DeployContext: deployContext,
		Deploy:        deployInput,
	}, nil
}

// resolveCurrentDockerComponentBuildForDeploy resolves the working-tree docker
// build of the current component for a `build --deploy` orchestration. The
// caller gates this on the env being a builds-here type; this helper only
// requires that the current directory is a docker build context
// (<module>/docker/<component>). Returns nil when there is no such context.
func resolveCurrentDockerComponentBuildForDeploy(store DockerStore, findProjectRoot ProjectFinderFunc, resolveDockerBuildContext BuildContextResolverFunc, now NowFunc, projectRoot, environment, versionOverride string) (*DockerBuildSpec, error) {
	_, _, resolveDockerBuildContext, now = normalizeDockerDependencies(store, findProjectRoot, resolveDockerBuildContext, now)
	if resolveDockerBuildContext == nil {
		return nil, nil
	}

	buildContext, err := resolveDockerBuildContext()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(buildContext.DockerfilePath) == "" || filepath.Base(filepath.Dir(buildContext.Dir)) != "docker" {
		return nil, nil
	}

	build, err := newDockerBuildSpec(now, projectRoot, environment, buildContext, versionOverride)
	if err != nil {
		return nil, err
	}
	return &build, nil
}

func deployContextOwnsRuntimeChart(deployContext KubernetesDeployContext, tenant string) bool {
	tenant = strings.TrimSpace(tenant)
	if tenant == "" {
		return false
	}
	return strings.TrimSpace(deployContext.ComponentName) == RuntimeReleaseName(tenant)
}

func deployContextOwnsDockerBuild(deployContext KubernetesDeployContext, build DockerBuildSpec) bool {
	chartPath := filepath.Clean(strings.TrimSpace(deployContext.ChartPath))
	dockerfilePath := filepath.Clean(strings.TrimSpace(build.DockerfilePath))
	if chartPath == "" || dockerfilePath == "" {
		return false
	}

	moduleRoot := filepath.Dir(filepath.Dir(chartPath))
	buildDir := filepath.Dir(dockerfilePath)
	relative, err := filepath.Rel(moduleRoot, buildDir)
	if err != nil || relative == "." {
		return false
	}
	return !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && relative != ".."
}

func applyDeployKubernetesContext(store DeployStore, target OpenResult) OpenResult {
	if resolver, ok := store.(deployKubernetesContextResolver); ok {
		target.EnvConfig.KubernetesContext = resolver.ResolveDeployKubernetesContext(target.Environment, target.EnvConfig.KubernetesContext)
	}
	return target
}

func ResolveOpenRuntimeDeploySpec(ctx Context, store DeployStore, findProjectRoot ProjectFinderFunc, resolveDockerBuildContext BuildContextResolverFunc, resolveKubernetesDeployContext DeployContextResolverFunc, now NowFunc, target OpenResult, allowLocalBuilds bool) (DeploySpec, error) {
	store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now = normalizeDeployDependencies(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now)
	now = freezeNow(now)
	return resolveOpenRuntimeDeploySpec(ctx, store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, target, allowLocalBuilds)
}

type BuildDeployStore interface {
	DeployStore
	DockerStore
}

func ResolveCurrentDeploySpecsForDockerTarget(ctx Context, store BuildDeployStore, findProjectRoot ProjectFinderFunc, resolveDockerBuildContext BuildContextResolverFunc, resolveKubernetesDeployContext DeployContextResolverFunc, now NowFunc, target DockerCommandTarget) ([]DeploySpec, error) {
	store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now = normalizeBuildDeployDependencies(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now)

	target, _, err := ResolveDockerBuildTarget(findProjectRoot, target)
	if err != nil {
		return nil, err
	}

	deployTarget, err := resolveDeployTargetForDockerTarget(store, findProjectRoot, target)
	if err != nil {
		return nil, err
	}

	return resolveCurrentDeploySpecs(ctx, store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, deployTarget, true)
}

func resolveDeployTarget(store DeployStore, findProjectRoot ProjectFinderFunc, resolveDockerBuildContext BuildContextResolverFunc, resolveKubernetesDeployContext DeployContextResolverFunc, now NowFunc, target DeployTarget) (OpenResult, error) {
	store, findProjectRoot, _, _, _ = normalizeDeployDependencies(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now)

	if strings.TrimSpace(target.Tenant) != "" || strings.TrimSpace(target.Environment) != "" || strings.TrimSpace(target.RepoPath) != "" {
		if strings.TrimSpace(target.Tenant) == "" || strings.TrimSpace(target.Environment) == "" {
			return OpenResult{}, fmt.Errorf("tenant and environment overrides are required together")
		}

		result, err := resolveOpenWithFinder(store, findProjectRoot, OpenParams{
			Tenant:      strings.TrimSpace(target.Tenant),
			Environment: strings.TrimSpace(target.Environment),
		})
		if err != nil {
			return OpenResult{}, err
		}
		if repoPath := strings.TrimSpace(target.RepoPath); repoPath != "" && filepath.Clean(result.RepoPath) != filepath.Clean(repoPath) {
			return OpenResult{}, fmt.Errorf("resolved repo path %q does not match override %q", result.RepoPath, repoPath)
		}
		return result, nil
	}

	return resolveOpenWithFinder(store, findProjectRoot, OpenParams{
		UseDefaultTenant:      true,
		UseDefaultEnvironment: true,
	})
}

func normalizeDeployDependencies(store DeployStore, findProjectRoot ProjectFinderFunc, resolveDockerBuildContext BuildContextResolverFunc, resolveKubernetesDeployContext DeployContextResolverFunc, now NowFunc) (DeployStore, ProjectFinderFunc, BuildContextResolverFunc, DeployContextResolverFunc, NowFunc) {
	if store == nil {
		store = ConfigStore{}
	}
	if findProjectRoot == nil {
		findProjectRoot = FindProjectRoot
	}
	if resolveDockerBuildContext == nil {
		resolveDockerBuildContext = ResolveDockerBuildContext
	}
	if resolveKubernetesDeployContext == nil {
		resolveKubernetesDeployContext = ResolveKubernetesDeployContext
	}
	if now == nil {
		now = time.Now
	}
	return store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now
}

func normalizeBuildDeployDependencies(store BuildDeployStore, findProjectRoot ProjectFinderFunc, resolveDockerBuildContext BuildContextResolverFunc, resolveKubernetesDeployContext DeployContextResolverFunc, now NowFunc) (BuildDeployStore, ProjectFinderFunc, BuildContextResolverFunc, DeployContextResolverFunc, NowFunc) {
	if store == nil {
		store = ConfigStore{}
	}
	if findProjectRoot == nil {
		findProjectRoot = FindProjectRoot
	}
	if resolveDockerBuildContext == nil {
		resolveDockerBuildContext = ResolveDockerBuildContext
	}
	if resolveKubernetesDeployContext == nil {
		resolveKubernetesDeployContext = ResolveKubernetesDeployContext
	}
	if now == nil {
		now = time.Now
	}
	return store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now
}

// freezeNow pins now to a single instant so every snapshot version minted while
// resolving one deploy (or build) shares the same timestamp. The per-chart build
// (ResolveDockerBuildForComponent), the cwd "current build"
// (resolveCurrentDockerComponentBuildForDeploy), and each image's version
// (resolveDockerImageVersion) otherwise call now() independently; across a
// multi-image / multi-spec deploy those timestamps drift apart, so the runtime
// chart's persisted RuntimeVersion can end up differing from the tag actually
// built and pushed — a phantom version the deploy picker can never offer because
// it gates on registry presence. Capturing now() once at the resolution
// entrypoint and threading the frozen clock downstream keeps build, push, helm,
// and persist on one identical tag. freezeNow is idempotent: freezing an
// already-frozen clock reproduces the same instant, so applying it at more than
// one entrypoint on the same call path is safe. See issue #475.
func freezeNow(now NowFunc) NowFunc {
	if now == nil {
		now = time.Now
	}
	frozen := now()
	return func() time.Time { return frozen }
}

func resolveDeployTargetForDockerTarget(store BuildDeployStore, findProjectRoot ProjectFinderFunc, target DockerCommandTarget) (DeployTarget, error) {
	projectRoot, err := resolveDockerBuildProjectRoot(findProjectRoot, target)
	if err != nil {
		return DeployTarget{}, err
	}
	if projectRoot == "" {
		return DeployTarget{}, fmt.Errorf("cannot determine project root for Helm deployment")
	}

	environment, err := resolveDockerBuildEnvironment(store, findProjectRoot, projectRoot, target.Environment)
	if err != nil {
		return DeployTarget{}, err
	}

	tenant, err := resolveProjectTenantForRoot(store, projectRoot)
	if err != nil {
		return DeployTarget{}, err
	}

	return DeployTarget{
		Tenant:          tenant,
		Environment:     environment,
		RepoPath:        projectRoot,
		VersionOverride: strings.TrimSpace(target.VersionOverride),
		Force:           target.NoIncremental,
	}, nil
}

func resolveDeployVersionOverride(target DeployTarget, versionOverride string) string {
	if versionOverride = strings.TrimSpace(versionOverride); versionOverride != "" {
		return versionOverride
	}
	return strings.TrimSpace(target.VersionOverride)
}

func deployResetsDatabase(snapshotEnabled bool, version string) bool {
	return snapshotEnabled || strings.Contains(strings.TrimSpace(version), "-snapshot-")
}

func resolveDeployContextForTarget(findProjectRoot ProjectFinderFunc, resolveKubernetesDeployContext DeployContextResolverFunc, target OpenResult, componentName string) (KubernetesDeployContext, error) {
	componentName = strings.TrimSpace(componentName)
	if componentName == "" {
		return resolveDeployContext(findProjectRoot, resolveKubernetesDeployContext, componentName)
	}

	chartPath, err := findComponentHelmChartPath(target.RepoPath, componentName)
	if err != nil {
		return KubernetesDeployContext{}, err
	}

	return KubernetesDeployContext{
		Dir:           target.RepoPath,
		ComponentName: componentName,
		ChartPath:     chartPath,
	}, nil
}

func resolveDeployContext(findProjectRoot ProjectFinderFunc, resolveKubernetesDeployContext DeployContextResolverFunc, componentName string) (KubernetesDeployContext, error) {
	componentName = strings.TrimSpace(componentName)
	if componentName == "" {
		context, err := resolveKubernetesDeployContext()
		if err != nil {
			return KubernetesDeployContext{}, err
		}
		if strings.TrimSpace(context.ChartPath) == "" || strings.TrimSpace(context.ComponentName) == "" {
			return KubernetesDeployContext{}, fmt.Errorf("helm chart not found in current component directory")
		}
		context.ComponentName = strings.TrimSpace(context.ComponentName)
		context.ChartPath = filepath.Clean(context.ChartPath)
		if err := ValidateHelmChartPath(context.ChartPath); err != nil {
			return KubernetesDeployContext{}, err
		}
		return context, nil
	}

	projectRoot, err := resolveDockerBuildProjectRoot(findProjectRoot, DockerCommandTarget{})
	if err != nil {
		return KubernetesDeployContext{}, err
	}
	if projectRoot == "" {
		return KubernetesDeployContext{}, fmt.Errorf("cannot determine project root for Helm deployment")
	}

	chartPath, err := findComponentHelmChartPath(projectRoot, componentName)
	if err != nil {
		return KubernetesDeployContext{}, err
	}

	return KubernetesDeployContext{
		Dir:           projectRoot,
		ComponentName: componentName,
		ChartPath:     chartPath,
	}, nil
}

func resolveProjectTenantForRoot(store DeployStore, projectRoot string) (string, error) {
	tenants, err := store.ListTenantConfigs()
	if err != nil {
		return "", err
	}

	cleanProjectRoot := filepath.Clean(projectRoot)
	matches := make([]TenantConfig, 0, len(tenants))
	for _, tenant := range tenants {
		if filepath.Clean(tenant.ProjectRoot) == cleanProjectRoot {
			matches = append(matches, tenant)
		}
	}

	defaultTenant, defaultErr := loadDefaultTenant(store)
	if defaultErr == nil {
		for _, tenant := range matches {
			if tenant.Name == defaultTenant {
				return tenant.Name, nil
			}
		}
	}

	if len(matches) == 1 {
		return matches[0].Name, nil
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("multiple tenants are configured for project %q", cleanProjectRoot)
	}

	return "", fmt.Errorf("no tenant is configured for project %q", cleanProjectRoot)
}

func loadDefaultTenant(store DeployStore) (string, error) {
	toolConfig, _, err := store.LoadERunConfig()
	if err != nil {
		return "", err
	}
	if toolConfig.DefaultTenant == "" {
		return "", ErrDefaultTenantNotConfigured
	}
	return toolConfig.DefaultTenant, nil
}

func newHelmDeploySpec(target OpenResult, deployContext KubernetesDeployContext, versionOverride string) (HelmDeploySpec, error) {
	valuesFilePath, err := resolveKubernetesDeployValuesFile(deployContext.ChartPath, target.Environment)
	if err != nil {
		return HelmDeploySpec{}, err
	}
	return newHelmDeploySpecWithValues(target, deployContext, versionOverride, valuesFilePath)
}

// newHelmDeploySpecWithValues is newHelmDeploySpec for callers that resolve
// the values overlay themselves — a published OCI chart has no local chart
// directory to look in, so it passes an empty path.
func newHelmDeploySpecWithValues(target OpenResult, deployContext KubernetesDeployContext, versionOverride, valuesFilePath string) (HelmDeploySpec, error) {
	version := strings.TrimSpace(versionOverride)
	ports := LocalPortsForResult(target)

	return HelmDeploySpec{
		ReleaseName:        deployContext.ComponentName,
		ChartPath:          deployContext.ChartPath,
		ValuesFilePath:     valuesFilePath,
		Tenant:             target.Tenant,
		Environment:        target.Environment,
		Namespace:          KubernetesNamespaceName(target.Tenant, target.Environment),
		KubernetesContext:  target.EnvConfig.KubernetesContext,
		WorktreeStorage:    resolveWorktreeStorage(target),
		WorktreeRepoName:   resolveWorktreeRepoName(target.RepoPath),
		WorktreeHostPath:   resolveWorktreeHostPath(target.RepoPath),
		SSHDEnabled:        target.EnvConfig.SSHD.Enabled,
		MCPPort:            ports.MCP,
		APIPort:            ports.API,
		SSHPort:            ports.SSH,
		CloudProviderAlias: target.EnvConfig.CloudProviderAlias,
		ContainerRegistry:  resolveProjectContainerRegistry(target.RepoPath, target.Environment),
		Idle:               target.EnvConfig.Idle,
		Claude:             target.EnvConfig.Claude,
		RuntimePod:         NormalizeRuntimePodResources(target.EnvConfig.RuntimePod),
		Version:            version,
		Timeout:            DefaultHelmDeploymentTimeout,
	}, nil
}

// resolveProjectContainerRegistry resolves the registry the cluster pulls from
// — the DEPLOY-marked entry of the environment's configured registry list.
// Returns "" when the project is uninitialized or marks no list, so callers'
// own fallbacks (deploy provenance, default) still apply.
func resolveProjectContainerRegistry(projectRoot, environment string) string {
	projectRoot = strings.TrimSpace(projectRoot)
	if projectRoot == "" {
		return ""
	}
	projectConfig, _, err := LoadProjectConfig(projectRoot)
	if err != nil {
		return ""
	}
	list := projectConfig.ContainerRegistriesForEnvironment(environment)
	if list.IsZero() {
		return ""
	}
	registry, _ := list.DeployRegistry()
	return registry
}

func applyCloudContextStopMetadata(store CloudReadStore, env EnvConfig, deployInput *HelmDeploySpec) {
	if deployInput == nil {
		return
	}
	status, ok, err := findCloudContextForKubernetesContext(store, env.KubernetesContext)
	if err != nil || !ok {
		return
	}
	deployInput.CloudContextName = status.Name
	deployInput.CloudProvider = status.Provider
	deployInput.CloudProviderAlias = status.CloudProviderAlias
	deployInput.CloudRegion = status.Region
	deployInput.CloudInstanceID = status.InstanceID
}

func applyCloudProviderDeployMetadata(store CloudReadStore, env EnvConfig, deployInput *HelmDeploySpec) {
	if deployInput == nil {
		return
	}
	alias := strings.TrimSpace(env.CloudProviderAlias)
	deployInput.CloudProviderAlias = alias
	if alias != "" {
		if provider, err := ResolveCloudProvider(store, alias); err == nil {
			deployInput.CloudProvider = provider.Provider
		} else if provider := cloudProviderFromAlias(alias); provider != "" {
			deployInput.CloudProvider = provider
		}
	}

	status, ok, err := findCloudContextForKubernetesContext(store, env.KubernetesContext)
	if err == nil && ok {
		if alias == "" || strings.TrimSpace(status.CloudProviderAlias) == alias {
			deployInput.CloudContextName = status.Name
			deployInput.CloudProvider = status.Provider
			deployInput.CloudProviderAlias = status.CloudProviderAlias
			deployInput.CloudRegion = status.Region
			return
		}
	}
	if deployInput.CloudRegion == "" && deployInput.CloudProvider == CloudProviderAWS {
		deployInput.CloudRegion = cloudContextRegionFromName(env.KubernetesContext)
	}
}

func cloudProviderFromAlias(alias string) string {
	_, provider, ok := strings.Cut(strings.TrimSpace(alias), "@")
	if !ok {
		return ""
	}
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == CloudProviderAWS {
		return provider
	}
	return ""
}

func cloudContextRegionFromName(name string) string {
	name = strings.TrimSpace(name)
	for _, region := range CloudContextRegions() {
		if strings.HasSuffix(name, "-"+region) || name == region {
			return region
		}
	}
	return ""
}

func resolveWorktreeStorage(target OpenResult) string {
	if target.EnvConfig.Type.IsValid() {
		switch target.EnvConfig.Type {
		case EnvironmentTypeRuntime:
			return WorktreeStorageNone
		case EnvironmentTypeRemoteAgent:
			return WorktreeStoragePVC
		}
		return WorktreeStorageHost
	}
	if target.RemoteRepo() {
		return WorktreeStoragePVC
	}
	return WorktreeStorageHost
}

func resolveWorktreeRepoName(repoPath string) string {
	repoName := strings.TrimSpace(filepath.Base(strings.TrimSpace(repoPath)))
	if repoName == "" || repoName == "." || repoName == string(filepath.Separator) {
		return "worktree"
	}
	return repoName
}

func resolveWorktreeHostPath(repoPath string) string {
	repoPath = strings.TrimSpace(repoPath)
	if repoPath == "" {
		return ""
	}

	cleaned := filepath.Clean(repoPath)
	resolved, err := filepath.EvalSymlinks(cleaned)
	if err != nil || strings.TrimSpace(resolved) == "" {
		return cleaned
	}

	return resolved
}

func (d HelmDeploySpec) Params(stdout, stderr io.Writer) HelmDeployParams {
	return HelmDeployParams{
		ReleaseName:        d.ReleaseName,
		ChartPath:          d.ChartPath,
		ValuesFilePath:     d.ValuesFilePath,
		Tenant:             d.Tenant,
		Environment:        d.Environment,
		Namespace:          d.Namespace,
		KubernetesContext:  d.KubernetesContext,
		WorktreeStorage:    d.WorktreeStorage,
		WorktreeRepoName:   d.WorktreeRepoName,
		WorktreeHostPath:   d.WorktreeHostPath,
		SSHDEnabled:        d.SSHDEnabled,
		MCPPort:            d.MCPPort,
		APIPort:            d.APIPort,
		SSHPort:            d.SSHPort,
		ManagedCloud:       d.ManagedCloud,
		CloudContextName:   d.CloudContextName,
		CloudProvider:      d.CloudProvider,
		CloudProviderAlias: d.CloudProviderAlias,
		CloudRegion:        d.CloudRegion,
		CloudInstanceID:    d.CloudInstanceID,
		UseHostCredentials: d.UseHostCredentials,
		OIDCAllowedIssuers: d.OIDCAllowedIssuers,
		ContainerRegistry:  d.ContainerRegistry,
		ImageOverrides:     cloneStringMap(d.ImageOverrides),
		ResetDatabase:      d.ResetDatabase,
		Idle:               d.Idle,
		Claude:             d.Claude,
		RuntimePod:         NormalizeRuntimePodResources(d.RuntimePod),
		Version:            d.Version,
		Timeout:            d.Timeout,
		Verbosity:          d.Verbosity,
		Stdout:             stdout,
		Stderr:             stderr,
	}
}

func (d HelmDeploySpec) command() commandSpec {
	args := []string{
		"upgrade",
		"--install",
		"--wait",
		"--wait-for-jobs",
		"--timeout", d.Timeout,
		"--namespace", d.Namespace,
	}
	if d.Verbosity >= VerbosityDebug {
		args = append(args, "--debug")
	}
	if strings.TrimSpace(d.KubernetesContext) != "" {
		args = append(args, "--kube-context", d.KubernetesContext)
	}
	// A published OCI chart has no local values overlay file; every other
	// chart resolves one (possibly empty) next to its templates.
	if strings.TrimSpace(d.ValuesFilePath) != "" {
		args = append(args, "-f", d.ValuesFilePath)
	}
	args = append(args,
		"--set-string", "tenant="+d.Tenant,
		"--set-string", "environment="+d.Environment,
		"--set-string", "worktreeStorage="+d.WorktreeStorage,
		"--set-string", "worktreeRepoName="+d.WorktreeRepoName,
		"--set-string", "worktreeHostPath="+d.WorktreeHostPath,
		"--set", "sshdEnabled="+formatHelmBool(d.SSHDEnabled),
		"--set", "mcpPort="+formatHelmPort(d.MCPPort, MCPServicePort),
		"--set", "apiPort="+formatHelmPort(d.APIPort, APIServicePort),
		"--set", "sshPort="+formatHelmPort(d.SSHPort, DefaultSSHLocalPort),
		"--set", "managedCloud="+formatHelmBool(d.ManagedCloud),
		"--set-string", "cloudContext.name="+d.CloudContextName,
		"--set-string", "cloudContext.provider="+d.CloudProvider,
		"--set-string", "cloudContext.providerAlias="+d.CloudProviderAlias,
		"--set-string", "cloudContext.region="+d.CloudRegion,
		"--set-string", "cloudContext.instanceId="+d.CloudInstanceID,
		"--set", "cloudContext.useHostCredentials="+formatHelmBool(d.UseHostCredentials),
		"--set-string", "api.oidcAllowedIssuers="+escapeHelmSetValue(d.OIDCAllowedIssuers),
		"--set", "api.postgres.reset="+formatHelmBool(d.ResetDatabase),
	)
	if registry := strings.TrimSpace(d.ContainerRegistry); registry != "" {
		args = append(args, "--set-string", "containerRegistry="+registry)
	}
	for _, key := range sortedStringMapKeys(d.ImageOverrides) {
		args = append(args, "--set-string", "imageOverrides."+key+"="+d.ImageOverrides[key])
	}
	args = append(args,
		"--set-string", "idle.timeout="+helmIdleTimeout(d.Idle),
		"--set-string", "idle.workingHours="+helmIdleWorkingHours(d.Idle),
		"--set-string", "idle.timezone="+helmIdleTimezone(d.Idle),
		"--set", "idle.trafficBytes="+formatHelmInt64(helmIdleTrafficBytes(d.Idle)),
		"--set-string", "runtime.resources.limits.cpu="+NormalizeRuntimePodResources(d.RuntimePod).CPU,
		"--set-string", "runtime.resources.limits.memory="+NormalizeRuntimePodResources(d.RuntimePod).Memory,
	)
	args = append(args, helmClaudeSetArgs(d.Claude)...)
	args = append(args,
		d.ReleaseName,
		d.ChartPath,
	)
	// A published OCI chart is pinned by --version (one version covers
	// chart and image, stamped at release); local charts get their
	// Chart.yaml stamped by prepareHelmChartForDeploy instead.
	dir := d.ChartPath
	if isOCIChartReference(d.ChartPath) {
		args = append(args, "--version", d.Version)
		dir = ""
	}

	return commandSpec{
		Dir:  dir,
		Name: "helm",
		Args: args,
	}
}

// isOCIChartReference reports whether the chart path addresses a published
// OCI chart (oci://<registry>/charts/<name>) rather than a local chart
// directory.
func isOCIChartReference(chartPath string) bool {
	return strings.HasPrefix(strings.TrimSpace(chartPath), "oci://")
}

func (p HelmReleaseRecoveryParams) command() commandSpec {
	args := []string{}
	if p.Verbosity >= VerbosityDebug {
		args = append(args, "--v=4")
	}
	if strings.TrimSpace(p.KubernetesContext) != "" {
		args = append(args, "--context", p.KubernetesContext)
	}
	args = append(args,
		"--namespace", p.Namespace,
		"delete",
		"secrets,configmaps",
		"-l", helmPendingReleaseOperationSelector(p.ReleaseName),
		"--ignore-not-found",
	)

	return commandSpec{
		Name: "kubectl",
		Args: args,
	}
}

func helmPendingReleaseOperationSelector(releaseName string) string {
	return "owner=helm,name=" + releaseName + ",status in (pending-install,pending-upgrade,pending-rollback)"
}

func (e *HelmReleasePendingOperationError) Error() string {
	if e == nil {
		return ""
	}
	message := strings.TrimSpace(e.Message)
	if message == "" && e.Err != nil {
		message = e.Err.Error()
	}
	if message == "" {
		message = "helm release operation is already in progress"
	}
	return fmt.Sprintf("%s; recover with: %s", message, e.RecoveryCommand())
}

func (e *HelmReleasePendingOperationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func (e *HelmReleasePendingOperationError) RecoveryParams(verbosity int, stdout, stderr io.Writer) HelmReleaseRecoveryParams {
	if e == nil {
		return HelmReleaseRecoveryParams{Verbosity: verbosity, Stdout: stdout, Stderr: stderr}
	}
	return HelmReleaseRecoveryParams{
		ReleaseName:       e.ReleaseName,
		Namespace:         e.Namespace,
		KubernetesContext: e.KubernetesContext,
		Verbosity:         verbosity,
		Stdout:            stdout,
		Stderr:            stderr,
	}
}

func (e *HelmReleasePendingOperationError) RecoveryCommand() string {
	if e == nil {
		return ""
	}
	command := e.RecoveryParams(0, nil, nil).command()
	return formatShellCommand(command.Dir, command.Name, command.Args...)
}

func formatHelmBool(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func formatHelmPort(value, fallback int) string {
	if value <= 0 {
		value = fallback
	}
	return fmt.Sprintf("%d", value)
}

func formatHelmInt64(value int64) string {
	return fmt.Sprintf("%d", value)
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	clone := make(map[string]string, len(values))
	for key, value := range values {
		clone[key] = value
	}
	return clone
}

func sortedStringMapKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func helmIdleTimeout(config EnvironmentIdleConfig) string {
	policy, err := config.Resolve()
	if err != nil {
		return DefaultEnvironmentIdleTimeout.String()
	}
	return policy.Timeout.String()
}

func helmIdleWorkingHours(config EnvironmentIdleConfig) string {
	policy, err := config.Resolve()
	if err != nil {
		return DefaultEnvironmentWorkingHours
	}
	return policy.WorkingHours
}

func helmIdleTrafficBytes(config EnvironmentIdleConfig) int64 {
	policy, err := config.Resolve()
	if err != nil {
		return DefaultEnvironmentIdleTrafficBytes
	}
	return policy.IdleTrafficBytes
}

func helmIdleTimezone(config EnvironmentIdleConfig) string {
	policy, err := config.Resolve()
	if err != nil {
		return ""
	}
	return policy.Timezone
}

func helmClaudeSetArgs(config EnvironmentClaudeConfig) []string {
	args := make([]string, 0, 8)
	args = append(args, "--set-string", "claude.useMantle="+claudeFlagValue(resolveClaudeBool(config.UseMantle, DefaultClaudeUseMantle)))
	args = append(args, "--set-string", "claude.useBedrock="+claudeFlagValue(resolveClaudeBool(config.UseBedrock, DefaultClaudeUseBedrock)))
	if models := formatClaudeModels(config.Models); models != "" {
		args = append(args, "--set-string", "claude.availableModels="+escapeHelmSetValue(models))
	}
	if config.MaxOutputTokens != nil {
		args = append(args, "--set-string", "claude.maxOutputTokens="+strconv.Itoa(*config.MaxOutputTokens))
	}
	return args
}

func resolveClaudeBool(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

// claudeFlagValue returns the "1"/"0" form expected by the chart template and
// the entrypoint script, distinct from formatHelmBool's "true"/"false" form.
func claudeFlagValue(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

// escapeHelmSetValue escapes characters that Helm's --set/--set-string parser
// treats as structural so the input is preserved as a literal scalar value.
// Helm splits values on commas, so a comma inside the value must be backslash-escaped.
func escapeHelmSetValue(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `,`, `\,`)
	return replacer.Replace(value)
}

func resolveDeployKubernetesContext(environment, configured string, currentContext func() (string, error)) string {
	environment = strings.TrimSpace(environment)
	configured = strings.TrimSpace(configured)
	if environment != DefaultEnvironment || configured != "" || currentContext == nil {
		return configured
	}

	current, err := currentContext()
	if err != nil {
		return configured
	}
	current = strings.TrimSpace(current)
	if current == "" {
		return configured
	}
	return current
}

func ResolveKubernetesDeployContext() (KubernetesDeployContext, error) {
	dir, err := os.Getwd()
	if err != nil {
		return KubernetesDeployContext{}, err
	}

	return KubernetesDeployContextAtDir(dir), nil
}

func ResolveCurrentKubernetesDeployContexts(findProjectRoot ProjectFinderFunc, resolveDeployContext DeployContextResolverFunc, projectRootOverride string) ([]KubernetesDeployContext, error) {
	if resolveDeployContext == nil {
		return nil, fmt.Errorf("helm chart not found in current directory")
	}

	deployContext, err := resolveDeployContext()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(deployContext.ChartPath) != "" && strings.TrimSpace(deployContext.ComponentName) != "" {
		deployContext.ComponentName = strings.TrimSpace(deployContext.ComponentName)
		deployContext.ChartPath = filepath.Clean(deployContext.ChartPath)
		if err := ValidateHelmChartPath(deployContext.ChartPath); err != nil {
			return nil, err
		}
		return []KubernetesDeployContext{deployContext}, nil
	}

	if deployContexts, err := ResolveKubernetesDeployContextsAtDir(deployContext.Dir); err == nil {
		return deployContexts, nil
	}

	k8sDir, ok, err := resolveCurrentDevopsK8sDir(findProjectRoot, deployContext.Dir, projectRootOverride)
	if err != nil {
		return nil, err
	}
	if ok {
		return ResolveKubernetesDeployContextsAtDir(k8sDir)
	}

	return nil, fmt.Errorf("helm chart not found in current directory")
}

func KubernetesDeployContextAtDir(dir string) KubernetesDeployContext {
	context := KubernetesDeployContext{Dir: dir}
	componentName := filepath.Base(dir)
	parentName := filepath.Base(filepath.Dir(dir))

	switch parentName {
	case "k8s":
		if hasHelmChart(filepath.Join(dir, "Chart.yaml")) {
			context.ComponentName = componentName
			context.ChartPath = dir
		}
	case "docker":
		chartPath := filepath.Join(filepath.Dir(filepath.Dir(dir)), "k8s", componentName)
		if hasHelmChart(filepath.Join(chartPath, "Chart.yaml")) {
			context.ComponentName = componentName
			context.ChartPath = chartPath
		}
	}

	return context
}

func ResolveKubernetesDeployContextsAtDir(dir string) ([]KubernetesDeployContext, error) {
	dir = filepath.Clean(strings.TrimSpace(dir))
	if dir == "" || filepath.Base(dir) != "k8s" {
		return nil, fmt.Errorf("helm chart not found in current directory")
	}

	deployContexts, err := KubernetesDeployContextsUnderDir(dir)
	if err != nil {
		return nil, err
	}
	if len(deployContexts) == 0 {
		return nil, fmt.Errorf("helm chart not found in current directory")
	}

	return deployContexts, nil
}

func KubernetesDeployContextsUnderDir(dir string) ([]KubernetesDeployContext, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	deployContexts := make([]KubernetesDeployContext, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		chartPath := filepath.Join(dir, entry.Name())
		if !hasHelmChart(filepath.Join(chartPath, "Chart.yaml")) {
			continue
		}

		deployContexts = append(deployContexts, KubernetesDeployContext{
			Dir:           dir,
			ComponentName: entry.Name(),
			ChartPath:     chartPath,
		})
	}

	return deployContexts, nil
}

func resolveCurrentDevopsK8sDir(findProjectRoot ProjectFinderFunc, dir, projectRootOverride string) (string, bool, error) {
	dir = filepath.Clean(strings.TrimSpace(dir))
	if dir == "" {
		return "", false, nil
	}

	k8sDir := filepath.Join(dir, "k8s")
	if strings.HasSuffix(filepath.Base(dir), "-devops") {
		if ok, err := isKubernetesDeployModuleDir(k8sDir); err != nil {
			return "", false, err
		} else if ok {
			return k8sDir, true, nil
		}
	}

	if k8sDir, ok, err := resolveAncestorDevopsK8sDir(dir); err != nil || ok {
		return k8sDir, ok, err
	}

	if projectRoot := strings.TrimSpace(projectRootOverride); projectRoot != "" {
		return resolveProjectRootDevopsK8sDir(findProjectRoot, projectRoot)
	}

	projectRoot, err := resolveDockerBuildProjectRoot(findProjectRoot, DockerCommandTarget{})
	if err != nil {
		return "", false, err
	}
	if projectRoot == "" || dir != filepath.Clean(projectRoot) {
		return "", false, nil
	}

	return resolveProjectRootDevopsK8sDir(findProjectRoot, projectRoot)
}

func resolveAncestorDevopsK8sDir(dir string) (string, bool, error) {
	dir = filepath.Clean(strings.TrimSpace(dir))
	if dir == "" {
		return "", false, nil
	}

	for current := dir; ; {
		parent := filepath.Dir(current)
		if parent == current {
			return "", false, nil
		}
		current = parent
		if !strings.HasSuffix(filepath.Base(current), "-devops") {
			continue
		}

		k8sDir := filepath.Join(current, "k8s")
		ok, err := isKubernetesDeployModuleDir(k8sDir)
		if err != nil || ok {
			return k8sDir, ok, err
		}
	}
}

func resolveProjectRootDevopsK8sDir(findProjectRoot ProjectFinderFunc, projectRoot string) (string, bool, error) {
	projectRoot = filepath.Clean(strings.TrimSpace(projectRoot))
	if projectRoot == "" {
		return "", false, nil
	}

	k8sDir, ok, err := detectedProjectRootDevopsK8sDir(findProjectRoot, projectRoot)
	if err != nil || ok {
		return k8sDir, ok, err
	}

	candidates, err := findDevopsK8sDirs(projectRoot)
	if err != nil {
		return "", false, err
	}
	switch len(candidates) {
	case 0:
		return "", false, nil
	case 1:
		return candidates[0], true, nil
	default:
		return "", false, fmt.Errorf("multiple devops k8s directories found under project root")
	}
}

func detectedProjectRootDevopsK8sDir(findProjectRoot ProjectFinderFunc, projectRoot string) (string, bool, error) {
	tenant, detectedProjectRoot, err := findProjectRoot()
	if err != nil || filepath.Clean(strings.TrimSpace(detectedProjectRoot)) != projectRoot || strings.TrimSpace(tenant) == "" {
		return "", false, nil
	}
	k8sDir := filepath.Join(projectRoot, RuntimeReleaseName(tenant), "k8s")
	if ok, err := isKubernetesDeployModuleDir(k8sDir); err != nil {
		return "", false, err
	} else if ok {
		return k8sDir, true, nil
	}
	return "", false, nil
}

func findDevopsK8sDirs(projectRoot string) ([]string, error) {
	entries, err := os.ReadDir(projectRoot)
	if err != nil {
		return nil, err
	}

	candidates := make([]string, 0, 1)
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasSuffix(entry.Name(), "-devops") {
			continue
		}

		k8sDir := filepath.Join(projectRoot, entry.Name(), "k8s")
		ok, err := isKubernetesDeployModuleDir(k8sDir)
		if err != nil {
			return nil, err
		}
		if ok {
			candidates = append(candidates, k8sDir)
		}
	}
	return candidates, nil
}

func isKubernetesDeployModuleDir(dir string) (bool, error) {
	deployContexts, err := ResolveKubernetesDeployContextsAtDir(dir)
	if err != nil {
		if err.Error() == "helm chart not found in current directory" {
			return false, nil
		}
		return false, err
	}
	return len(deployContexts) > 0, nil
}

func DeployHelmChart(params HelmDeployParams) error {
	chartPath := params.ChartPath
	var cleanup func()
	// A published OCI chart cannot be copied and stamped locally; its
	// version is pinned on the helm command line instead (see command()).
	if strings.TrimSpace(params.Version) != "" && !isOCIChartReference(params.ChartPath) {
		var err error
		chartPath, cleanup, err = prepareHelmChartForDeploy(params.ChartPath, params.Version)
		if err != nil {
			return err
		}
		defer cleanup()
	}

	command := HelmDeploySpec{
		ReleaseName:        params.ReleaseName,
		ChartPath:          chartPath,
		ValuesFilePath:     params.ValuesFilePath,
		Tenant:             params.Tenant,
		Environment:        params.Environment,
		Namespace:          params.Namespace,
		KubernetesContext:  params.KubernetesContext,
		WorktreeStorage:    params.WorktreeStorage,
		WorktreeRepoName:   params.WorktreeRepoName,
		WorktreeHostPath:   params.WorktreeHostPath,
		SSHDEnabled:        params.SSHDEnabled,
		MCPPort:            params.MCPPort,
		APIPort:            params.APIPort,
		SSHPort:            params.SSHPort,
		ManagedCloud:       params.ManagedCloud,
		CloudContextName:   params.CloudContextName,
		CloudProvider:      params.CloudProvider,
		CloudProviderAlias: params.CloudProviderAlias,
		CloudRegion:        params.CloudRegion,
		CloudInstanceID:    params.CloudInstanceID,
		UseHostCredentials: params.UseHostCredentials,
		OIDCAllowedIssuers: params.OIDCAllowedIssuers,
		ContainerRegistry:  params.ContainerRegistry,
		ImageOverrides:     cloneStringMap(params.ImageOverrides),
		ResetDatabase:      params.ResetDatabase,
		Idle:               params.Idle,
		Claude:             params.Claude,
		RuntimePod:         params.RuntimePod,
		Version:            params.Version,
		Timeout:            params.Timeout,
		Verbosity:          params.Verbosity,
	}.command()

	cmd := Command(command.Name, command.Args...)
	cmd.Dir = command.Dir
	// At VerbosityInfo helm output is captured silently so a successful run is
	// quiet; the buffer feeds back into the returned error on failure. At
	// VerbosityDebug or higher the output is also teed to params.Stdout/Stderr
	// so the user sees the live --debug stream.
	helmOutput := new(bytes.Buffer)
	if params.Verbosity >= VerbosityDebug {
		cmd.Stdout = teeWriter(params.Stdout, helmOutput)
	} else {
		cmd.Stdout = helmOutput
	}
	stderr := new(strings.Builder)
	stderrWriters := []io.Writer{stderr, helmOutput}
	if params.Verbosity >= VerbosityDebug && params.Stderr != nil {
		stderrWriters = append(stderrWriters, params.Stderr)
	}
	cmd.Stderr = io.MultiWriter(stderrWriters...)

	if err := cmd.Start(); err != nil {
		return err
	}

	watchCtx, cancelWatch := context.WithCancel(context.Background())
	defer cancelWatch()
	watchDone := make(chan podWatchOutcome, 1)
	go func() {
		watchDone <- watchReleasePods(watchCtx, podWatchParams{
			ReleaseName:       params.ReleaseName,
			Namespace:         params.Namespace,
			KubernetesContext: params.KubernetesContext,
			StatusOut:         params.Stderr,
		})
	}()

	helmDone := make(chan error, 1)
	go func() { helmDone <- cmd.Wait() }()

	var (
		helmErr       error
		watchOutcome  podWatchOutcome
		helmFinished  bool
		watchFinished bool
	)
	for !helmFinished || !watchFinished {
		select {
		case helmErr = <-helmDone:
			helmFinished = true
			cancelWatch()
		case watchOutcome = <-watchDone:
			watchFinished = true
			if watchOutcome.Failure != nil && !helmFinished {
				_ = cmd.Process.Signal(os.Interrupt)
				go func() {
					time.Sleep(2 * time.Second)
					_ = cmd.Process.Kill()
				}()
				helmErr = <-helmDone
				helmFinished = true
				cancelWatch()
			}
		}
	}

	if watchOutcome.Failure != nil {
		failure := watchOutcome.Failure
		failure.Err = helmErr
		return failure
	}
	if helmErr != nil && isHelmReleasePendingOperationMessage(stderr.String()) {
		return &HelmReleasePendingOperationError{
			ReleaseName:       params.ReleaseName,
			Namespace:         params.Namespace,
			KubernetesContext: params.KubernetesContext,
			Message:           stderr.String(),
			Err:               helmErr,
		}
	}
	// Match against the dedicated stderr capture, not helmOutput: helm errors
	// land on stderr, and helmOutput doubles as cmd.Stdout so a stderr-only
	// failure can race to empty there (the pending-operation check above reads
	// stderr for the same reason).
	if helmErr != nil && isOCIChartReference(params.ChartPath) && isHelmChartNotFoundMessage(stderr.String()) {
		return &PublishedChartNotFoundError{
			ChartReference: params.ChartPath,
			Version:        params.Version,
			Registry:       params.ContainerRegistry,
			HelmOutput:     strings.TrimSpace(stderr.String()),
			Err:            helmErr,
		}
	}
	if helmErr != nil && params.Verbosity < VerbosityDebug {
		if output := strings.TrimSpace(helmOutput.String()); output != "" {
			return fmt.Errorf("%w\n%s", helmErr, output)
		}
	}
	return helmErr
}

// isHelmChartNotFoundMessage reports whether helm's captured output indicates
// the chart reference could not be resolved in the registry (a missing or
// pruned OCI tag), as opposed to a rollout, values, or connectivity failure.
// Scoped by the caller to OCI charts, it turns the opaque chart-pull exit
// status into an actionable PublishedChartNotFoundError.
func isHelmChartNotFoundMessage(output string) bool {
	lower := strings.ToLower(output)
	return strings.Contains(lower, "not found") ||
		strings.Contains(lower, "manifest unknown") ||
		strings.Contains(lower, "manifestunknown")
}

// tracePodWatchAction records the watcher action in the dry-run trace so the
// --dry-run contract holds: every action a real run would take must appear
// in the trace. The watcher itself only fires in real-run mode (DeployHelmChart
// runs after RunHelmDeploy's DryRun early-return), but the trace here lets a
// reader audit the plan before executing it.
func tracePodWatchAction(ctx Context, releaseName, namespace, kubernetesContext string) {
	releaseName = strings.TrimSpace(releaseName)
	namespace = strings.TrimSpace(namespace)
	if releaseName == "" || namespace == "" {
		return
	}
	descriptor := "deploy: watching pods in " + namespace
	if c := strings.TrimSpace(kubernetesContext); c != "" {
		descriptor += " on context " + c
	}
	descriptor += " for early container failure (release " + releaseName + ")"
	ctx.Trace(descriptor)
	args := []string{}
	if c := strings.TrimSpace(kubernetesContext); c != "" {
		args = append(args, "--context", c)
	}
	args = append(args, "--namespace", namespace, "get", "pods", "-o", "json")
	ctx.TraceCommand("", "kubectl", args...)
}

func ClearHelmReleasePendingOperation(params HelmReleaseRecoveryParams) error {
	if strings.TrimSpace(params.ReleaseName) == "" {
		return fmt.Errorf("helm release name is required")
	}
	if strings.TrimSpace(params.Namespace) == "" {
		return fmt.Errorf("helm release namespace is required")
	}

	command := params.command()
	cmd := Command(command.Name, command.Args...)
	capture := new(bytes.Buffer)
	if params.Verbosity >= VerbosityDebug {
		cmd.Stdout = teeWriter(params.Stdout, capture)
		cmd.Stderr = teeWriter(params.Stderr, capture)
	} else {
		cmd.Stdout = capture
		cmd.Stderr = capture
	}
	if err := cmd.Run(); err != nil {
		if params.Verbosity < VerbosityDebug {
			if output := strings.TrimSpace(capture.String()); output != "" {
				return fmt.Errorf("%w\n%s", err, output)
			}
		}
		return err
	}
	return nil
}

func isHelmReleasePendingOperationMessage(message string) bool {
	message = strings.ToLower(message)
	return strings.Contains(message, "another operation") &&
		strings.Contains(message, "install/upgrade/rollback") &&
		strings.Contains(message, "in progress")
}

func prepareHelmChartForDeploy(chartPath, version string) (string, func(), error) {
	version = strings.TrimSpace(version)
	if version == "" {
		return chartPath, func() {}, nil
	}

	tempRoot, err := os.MkdirTemp("", "erun-helm-chart-*")
	if err != nil {
		return "", nil, err
	}

	cleanup := func() {
		_ = os.RemoveAll(tempRoot)
	}

	tempChartPath := filepath.Join(tempRoot, filepath.Base(chartPath))
	if err := copyDirectory(chartPath, tempChartPath); err != nil {
		cleanup()
		return "", nil, err
	}
	if err := overrideHelmChartVersion(tempChartPath, version); err != nil {
		cleanup()
		return "", nil, err
	}

	return tempChartPath, cleanup, nil
}

func copyDirectory(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		relativePath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		targetPath := filepath.Join(dst, relativePath)

		info, err := d.Info()
		if err != nil {
			return err
		}

		if d.IsDir() {
			return os.MkdirAll(targetPath, info.Mode().Perm())
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlinks are not supported in Helm charts: %s", path)
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return err
		}
		return os.WriteFile(targetPath, data, info.Mode().Perm())
	})
}

func overrideHelmChartVersion(chartPath, version string) error {
	chartFilePath := filepath.Join(chartPath, "Chart.yaml")
	data, err := os.ReadFile(chartFilePath)
	if err != nil {
		return err
	}

	var chart map[string]interface{}
	if err := yaml.Unmarshal(data, &chart); err != nil {
		return err
	}
	if chart == nil {
		return errors.New("chart.yaml is empty")
	}

	chart["version"] = version
	chart["appVersion"] = version

	updated, err := yaml.Marshal(chart)
	if err != nil {
		return err
	}

	return os.WriteFile(chartFilePath, updated, 0o644)
}

func CheckKubernetesDeployment(ctx Context, params KubernetesDeploymentCheckParams) (bool, error) {
	deployed, output, err := checkKubernetesDeploymentWithContext(ctx, params, params.KubernetesContext)
	if err == nil {
		return deployed, nil
	}
	// `kubectl --context <name>` returns a "context does not exist"
	// error when the requested context is missing from the local
	// kubeconfig. The most common case is the contribute clone
	// running inside an env's pod: the env config it reads has the
	// *outer* desktop's context name (e.g.
	// `erun-001-…-eu-west-2`), but the pod's kubeconfig only has
	// `in-cluster`. Falling back to a context-less call uses
	// kubectl's current-context, which on the pod is the in-cluster
	// service-account context — exactly what we want. On a desktop
	// where the configured context name *is* valid, we never enter
	// this branch.
	if isKubernetesContextMissingMessage(output) && strings.TrimSpace(params.KubernetesContext) != "" {
		ctx.Trace(fmt.Sprintf("kubernetes deployment check: context %q not found in kubeconfig; retrying with current-context", params.KubernetesContext))
		fallback, _, fallbackErr := checkKubernetesDeploymentWithContext(ctx, params, "")
		if fallbackErr == nil {
			return fallback, nil
		}
		return false, fallbackErr
	}
	return false, err
}

// checkKubernetesDeploymentWithContext runs the underlying kubectl
// get + (optional) match check for the supplied context. Returns
// the captured combined output alongside the error so the caller can
// inspect kubectl's stderr (which carries the "context does not
// exist" string that drives the retry-with-current-context fallback
// in CheckKubernetesDeployment).
func checkKubernetesDeploymentWithContext(ctx Context, params KubernetesDeploymentCheckParams, kubectlContext string) (bool, string, error) {
	args := make([]string, 0, 8)
	if strings.TrimSpace(kubectlContext) != "" {
		args = append(args, "--context", kubectlContext)
	}
	if strings.TrimSpace(params.Namespace) != "" {
		args = append(args, "--namespace", params.Namespace)
	}
	args = append(args, "get", "deployment", params.Name, "-o", "name")

	ctx.TraceCommand("", "kubectl", args...)
	rawOutput, err := Command("kubectl", args...).CombinedOutput()
	output := string(rawOutput)
	if err == nil {
		if !hasExpectedDeploymentSettings(params) {
			return true, output, nil
		}
		matchParams := params
		matchParams.KubernetesContext = kubectlContext
		deployed, matchErr := deploymentMatchesExpectedSettings(ctx, matchParams)
		return deployed, output, matchErr
	}

	message := strings.ToLower(output)
	if strings.Contains(message, "notfound") || strings.Contains(message, "not found") || strings.Contains(message, "no resources found") {
		return false, output, nil
	}

	return false, output, fmt.Errorf("failed to check deployment %q: %w", params.Name, err)
}

// isKubernetesContextMissingMessage matches the family of kubectl
// error messages that indicate the requested --context name is not
// in the kubeconfig. kubectl reports this as:
//
//	error: context "<name>" does not exist
//	context "<name>" does not exist
//	no context exists with the name: "<name>"
//
// "does not exist" is the stable signal across kubectl versions;
// "no context exists with the name" is the older shape that some
// distributions still ship. Match case-insensitively.
func isKubernetesContextMissingMessage(output string) bool {
	msg := strings.ToLower(output)
	if strings.Contains(msg, "does not exist") {
		return true
	}
	if strings.Contains(msg, "no context exists with the name") {
		return true
	}
	return false
}

func hasExpectedDeploymentSettings(params KubernetesDeploymentCheckParams) bool {
	return strings.TrimSpace(params.ExpectedRepoPath) != "" ||
		params.ExpectedSSHD != nil ||
		params.ExpectedMCPPort > 0 ||
		params.ExpectedSSHPort > 0 ||
		params.ExpectedRuntimePod != (RuntimePodResources{})
}

type deploymentEnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func deploymentMatchesExpectedSettings(ctx Context, params KubernetesDeploymentCheckParams) (bool, error) {
	args := make([]string, 0, 8)
	if strings.TrimSpace(params.KubernetesContext) != "" {
		args = append(args, "--context", params.KubernetesContext)
	}
	if strings.TrimSpace(params.Namespace) != "" {
		args = append(args, "--namespace", params.Namespace)
	}
	args = append(args, "get", "deployment", params.Name, "-o", "json")

	ctx.TraceCommand("", "kubectl", args...)
	output, err := Command("kubectl", args...).CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("failed to inspect deployment %q: %w", params.Name, err)
	}

	var deployment struct {
		Spec struct {
			Template struct {
				Spec struct {
					Containers []struct {
						Name      string             `json:"name"`
						Env       []deploymentEnvVar `json:"env"`
						Resources struct {
							Limits RuntimePodResources `json:"limits"`
						} `json:"resources"`
					} `json:"containers"`
				} `json:"spec"`
			} `json:"template"`
		} `json:"spec"`
	}
	if err := yaml.Unmarshal(output, &deployment); err != nil {
		return false, fmt.Errorf("failed to parse deployment %q: %w", params.Name, err)
	}

	matches := expectedDeploymentMatches(params)
	var runtimeLimits RuntimePodResources
	runtimeFound := false
	for _, container := range deployment.Spec.Template.Spec.Containers {
		for _, env := range container.Env {
			matches.apply(params, env.Name, env.Value)
		}
		if strings.TrimSpace(container.Name) == params.Name {
			runtimeLimits = container.Resources.Limits
			runtimeFound = true
		}
	}
	if !runtimeFound {
		return false, nil
	}
	matches.runtimePod = matchesExpectedRuntimePod(runtimeLimits, params.ExpectedRuntimePod)
	return matches.ok(), nil
}

type deploymentExpectedMatches struct {
	repoPath   bool
	sshd       bool
	mcpPort    bool
	sshPort    bool
	runtimePod bool
}

// expectedDeploymentMatches seeds the per-field match state. Each field
// starts true when the corresponding expectation is unset (so a caller
// that didn't ask about it doesn't gate the result), and false otherwise
// — apply() flips it back to true on the first container env var that
// confirms the expected value.
//
// ERUN_API_PORT is deliberately not part of this matcher: the erun-api
// service is a separate Kubernetes deployment with its own port-forward
// path and presence check. Tenant-owned <tenant>-devops charts can
// legitimately omit ERUN_API_PORT from the runtime pod, and demanding it
// here would force a redeploy on every open for those tenants.
func expectedDeploymentMatches(params KubernetesDeploymentCheckParams) deploymentExpectedMatches {
	return deploymentExpectedMatches{
		repoPath:   strings.TrimSpace(params.ExpectedRepoPath) == "",
		sshd:       params.ExpectedSSHD == nil,
		mcpPort:    params.ExpectedMCPPort <= 0,
		sshPort:    params.ExpectedSSHPort <= 0,
		runtimePod: params.ExpectedRuntimePod == (RuntimePodResources{}),
	}
}

func (m *deploymentExpectedMatches) apply(params KubernetesDeploymentCheckParams, name, value string) {
	switch strings.TrimSpace(name) {
	case "ERUN_REPO_PATH":
		m.repoPath = matchesExpectedRepoPath(value, params.ExpectedRepoPath)
	case "ERUN_SSHD_ENABLED":
		m.sshd = matchesExpectedBool(value, params.ExpectedSSHD)
	case "ERUN_MCP_PORT":
		m.mcpPort = matchesExpectedPort(value, params.ExpectedMCPPort)
	case "ERUN_SSHD_PORT":
		m.sshPort = matchesExpectedPort(value, params.ExpectedSSHPort)
	}
}

func (m deploymentExpectedMatches) ok() bool {
	return m.repoPath && m.sshd && m.mcpPort && m.sshPort && m.runtimePod
}

func matchesExpectedRepoPath(value, expected string) bool {
	if strings.TrimSpace(expected) == "" {
		return true
	}
	return filepath.Clean(strings.TrimSpace(value)) == filepath.Clean(strings.TrimSpace(expected))
}

func matchesExpectedBool(value string, expected *bool) bool {
	if expected == nil {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(value), formatHelmBool(*expected))
}

func matchesExpectedPort(value string, expected int) bool {
	if expected <= 0 {
		return true
	}
	return strings.TrimSpace(value) == fmt.Sprintf("%d", expected)
}

func matchesExpectedRuntimePod(value, expected RuntimePodResources) bool {
	if expected == (RuntimePodResources{}) {
		return true
	}
	value = NormalizeRuntimePodResources(value)
	expected = NormalizeRuntimePodResources(expected)
	valueCPU, valueCPUErr := ParseKubernetesCPUToMilli(value.CPU)
	expectedCPU, expectedCPUErr := ParseKubernetesCPUToMilli(expected.CPU)
	valueMemory, valueMemoryErr := ParseKubernetesMemoryToMi(value.Memory)
	expectedMemory, expectedMemoryErr := ParseKubernetesMemoryToMi(expected.Memory)
	return valueCPUErr == nil &&
		expectedCPUErr == nil &&
		valueMemoryErr == nil &&
		expectedMemoryErr == nil &&
		valueCPU == expectedCPU &&
		valueMemory == expectedMemory
}

func resolveKubernetesDeployValuesFile(chartPath, environment string) (string, error) {
	valuesFilePath := filepath.Join(chartPath, fmt.Sprintf("values.%s.yaml", strings.ToLower(strings.TrimSpace(environment))))
	info, err := os.Stat(valuesFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("values file not found for environment %q: %s", environment, valuesFilePath)
		}
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("values file path is a directory: %s", valuesFilePath)
	}
	return valuesFilePath, nil
}

func findComponentHelmChartPath(projectRoot, componentName string) (string, error) {
	componentName = strings.TrimSpace(componentName)
	if componentName == "" {
		return "", fmt.Errorf("component name is required")
	}

	matches := make([]string, 0, 1)
	err := filepath.WalkDir(projectRoot, func(path string, d fs.DirEntry, err error) error {
		chartPath, ok, walkErr := componentHelmChartCandidate(path, d, componentName, err)
		if ok {
			matches = append(matches, chartPath)
		}
		return walkErr
	})
	if err != nil {
		return "", err
	}

	if len(matches) == 0 {
		return "", fmt.Errorf("helm chart not found for component %q", componentName)
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("multiple Helm charts found for component %q", componentName)
	}
	return matches[0], nil
}

func componentHelmChartCandidate(path string, d fs.DirEntry, componentName string, err error) (string, bool, error) {
	if err != nil {
		return "", false, err
	}
	if d.IsDir() {
		if d.Name() == ".git" {
			return "", false, fs.SkipDir
		}
		return "", false, nil
	}
	if d.Name() != "Chart.yaml" {
		return "", false, nil
	}
	chartPath := filepath.Dir(path)
	if filepath.Base(chartPath) != componentName || filepath.Base(filepath.Dir(chartPath)) != "k8s" {
		return "", false, nil
	}
	return chartPath, true, nil
}

func ValidateHelmChartPath(chartPath string) error {
	chartPath = filepath.Clean(chartPath)
	chartFilePath := filepath.Join(chartPath, "Chart.yaml")
	info, err := os.Stat(chartFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("helm chart not found: %s", chartPath)
		}
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("helm chart path is invalid: %s", chartPath)
	}
	return nil
}

func hasHelmChart(chartFilePath string) bool {
	info, err := os.Stat(chartFilePath)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

func findDockerImagesInChart(chartPath, appVersion string) ([]string, error) {
	images := make([]string, 0, 4)
	seen := make(map[string]struct{}, 4)
	templatesPath := filepath.Join(chartPath, "templates")

	err := filepath.WalkDir(templatesPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		for _, line := range strings.Split(string(data), "\n") {
			value := dockerImageFromChartLine(line, appVersion)
			if value == "" {
				continue
			}
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			images = append(images, value)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return images, nil
}

func dockerImageFromChartLine(line, appVersion string) string {
	value, ok := chartImageValue(line)
	if !ok {
		return ""
	}
	if idx := strings.Index(value, "#"); idx >= 0 {
		value = strings.TrimSpace(value[:idx])
	}
	value = strings.Trim(strings.TrimSpace(value), `"'`)
	value = resolveChartVersionImageTag(value, appVersion)
	if value == "" || strings.Contains(value, "{{") || strings.Contains(value, "%") {
		return ""
	}
	return value
}

func resolveChartVersionImageTag(value, appVersion string) string {
	appVersion = strings.TrimSpace(appVersion)
	if appVersion == "" {
		return value
	}
	replacer := strings.NewReplacer(
		"{{ .Chart.AppVersion }}", appVersion,
		"{{.Chart.AppVersion}}", appVersion,
	)
	return replacer.Replace(value)
}

func chartImageValue(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(trimmed, "image:"):
		return strings.TrimPrefix(trimmed, "image:"), true
	case strings.HasPrefix(trimmed, "- image:"):
		return strings.TrimPrefix(trimmed, "- image:"), true
	default:
		return chartTemplateImageValue(trimmed)
	}
}

func chartTemplateImageValue(line string) (string, bool) {
	for _, marker := range []string{`"`, `'`} {
		remaining := line
		for {
			start := strings.Index(remaining, marker)
			if start < 0 {
				break
			}
			remaining = remaining[start+len(marker):]
			end := strings.Index(remaining, marker)
			if end < 0 {
				break
			}
			value := remaining[:end]
			remaining = remaining[end+len(marker):]
			if !strings.Contains(value, "/") || !strings.Contains(value, ":") {
				continue
			}
			if strings.Contains(value, "%s") && strings.Contains(line, ".Chart.AppVersion") {
				if strings.Count(value, "%s") == 2 && strings.HasPrefix(value, "%s/") {
					value = value[len("%s/"):]
				}
				value = strings.ReplaceAll(value, "%s", "{{ .Chart.AppVersion }}")
			}
			return value, true
		}
	}
	return "", false
}

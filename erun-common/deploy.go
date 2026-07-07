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
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// DefaultHelmDeploymentTimeout is the fallback helm rollout timeout when an
// environment sets no deploy.timeout. It is 5m so the first deploy of a large
// image against a cold node cache is not failed mid-pull: a ~1GB runtime image
// can take minutes to pull, and the pod watcher waits out an in-progress pull up
// to this bound, aborting early only on a real, non-pull failure.
const DefaultHelmDeploymentTimeout = "5m0s"

// EnvironmentDeployConfig carries per-environment deploy tuning persisted on
// EnvConfig's `deploy:` block.
type EnvironmentDeployConfig struct {
	// Timeout is this env's helm rollout timeout; empty falls back to
	// DefaultHelmDeploymentTimeout.
	Timeout string `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	// Components is the operator's per-machine saved deploy selection for this
	// env — what `erun deploy` rolls out when no --components flag is passed.
	// Empty means no saved selection: deploy falls back to the repo plan, then
	// the runtime chart alone. Selection is opt-in-only: only named charts (plus
	// the runtime when named or when the selection is empty) deploy.
	Components []string `yaml:"components,omitempty" json:"components,omitempty"`
}

// Resolve returns the canonical rollout-timeout string, falling back to
// DefaultHelmDeploymentTimeout when empty. A malformed or non-positive duration
// is a hard error so a misconfigured environment fails the deploy loudly rather
// than silently revert to the default.
func (c EnvironmentDeployConfig) Resolve() (string, error) {
	timeout := strings.TrimSpace(c.Timeout)
	if timeout == "" {
		return DefaultHelmDeploymentTimeout, nil
	}
	duration, err := time.ParseDuration(timeout)
	if err != nil {
		return "", fmt.Errorf("invalid environment deploy timeout %q", timeout)
	}
	if duration <= 0 {
		return "", fmt.Errorf("environment deploy timeout must be greater than zero")
	}
	return duration.String(), nil
}

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
	ReleaseName          string
	ChartPath            string
	ValuesFilePath       string
	PulledValuesFilePath string
	SubchartKey          string
	Tenant               string
	Environment          string
	Namespace            string
	KubernetesContext    string
	WorktreeStorage      string
	WorktreeRepoName     string
	WorktreeHostPath     string
	// RepoURL / RepoRef drive the runtime pod's clone-at-boot of a mutable
	// source worktree for a runtime env that opted into MountSource. RepoRef is
	// the release tag (v<version>) the checkout starts from. Both empty for
	// every other env.
	RepoURL            string
	RepoRef            string
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
	ReleaseName    string
	ChartPath      string
	ValuesFilePath string
	// PulledValuesFilePath is the local path to a by-reference umbrella's bundled
	// values.<env>.yaml, extracted from the published chart by a deploy-time
	// `helm pull --untar`. helm applies only a chart's default values.yaml, so a
	// tenant umbrella's per-env subchart values (pod-shape and overrides authored
	// under the subchart key) never reach a by-reference deploy otherwise. It is
	// -f'd before ValuesFilePath so an operator's config-dir overlay still wins.
	// Empty for local charts (their values.<env>.yaml is ValuesFilePath) and for a
	// canonical chart installed directly. Set by RunHelmDeploy, not at resolve.
	PulledValuesFilePath string
	// SubchartKey is set when the chart is an umbrella wrapping a canonical
	// erun-<base> chart as a subchart — the repo-local runtime umbrella (the
	// erun-devops dependency's alias/name) or a tenant's published <tenant>-<base>
	// umbrella deployed by reference (the wrapped erun-<base>). helm does not pass
	// top-level --set values into subchart scope, so command() prefixes every
	// --set key with this key. Empty for a canonical chart installed directly and
	// a forked top-level chart — those keep byte-identical top-level --sets.
	SubchartKey       string
	Tenant            string
	Environment       string
	Namespace         string
	KubernetesContext string
	WorktreeStorage   string
	WorktreeRepoName  string
	WorktreeHostPath  string
	// RepoURL / RepoRef mirror the HelmDeployParams fields of the same name; see
	// their doc comment there. They surface as chart repoUrl/repoRef --sets only
	// for a mount-source runtime deploy.
	RepoURL            string
	RepoRef            string
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
	// Cloudflare* deliver a delegated Cloudflare token to the runtime pod.
	// CloudflareTokenRef is a handle into the secret store, never the token
	// itself, resolved at execution time.
	CloudflareEnabled    bool
	CloudflareAccountID  string
	CloudflareSecretName string
	CloudflareTokenRef   string
	// RuntimeRegistry is the env's runtime image-ref / runtime-version registry,
	// distinct from ContainerRegistry (the DEPLOY-marked registry the cluster
	// pulls from). Injected so in-pod runtime image-ref resolution does not fall
	// back to ghcr.io/sophium.
	RuntimeRegistry string
	// ContainerRegistries is the env's full marked registry list, injected so
	// in-pod build/push role resolution works on remote/runtime pods whose list
	// lives only on the env config.
	ContainerRegistries ContainerRegistries
	// DisableBuildScript mirrors EnvConfig.DisableBuildScript so a remote-agent
	// pod's in-pod build honours the operator's build.sh-discovery choice.
	DisableBuildScript bool
	// Platform is the resolved per-instance platform config. Zero for
	// non-platform projects; when set, deploy threads it to every chart as
	// platform.* values so the PowerDNS singleton can bootstrap its authoritative
	// zone.
	Platform PlatformConfig
	// MCPAuth* require the per-env erun-mcp edge to authenticate bearer tokens
	// against a trusted public key, delivered out-of-band as a Secret like the
	// Cloudflare token. Empty leaves the edge in legacy loopback-only
	// pass-through, so non-desktop deploys are byte-for-byte unchanged.
	MCPAuthEnabled      bool
	MCPAuthPublicKeyPEM string
	MCPAuthIssuer       string
	MCPAuthAudience     string
	MCPAuthSecretName   string
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
	// Components is the explicit, one-shot deploy selection for this run. When
	// non-empty it fully determines what deploys (opt-in-only), overriding the
	// env's saved deploy.components and the repo plan; the runtime deploys only
	// if named, and a name matching no chart or runtime alias errors at resolve.
	// Empty means no explicit selection: deploy falls back to the saved set, then
	// the plan, then the runtime chart alone.
	Components []string
	// Force re-runs the helm upgrade even when the deployed release already
	// matches the requested version (no-op rollouts are otherwise skipped).
	Force bool
	// RolloutTimeout overrides the helm rollout timeout for this deploy only,
	// taking precedence over the env's deploy.timeout and
	// DefaultHelmDeploymentTimeout. Empty leaves the resolved per-env/default
	// value untouched.
	RolloutTimeout string
	// MCPAuthPublicKeyPath, when set, points at a PEM public key the runtime
	// chart trusts so the per-env erun-mcp edge requires a bearer token signed by
	// it. Empty leaves the edge in legacy loopback-only mode, so non-desktop
	// deploys are unchanged.
	MCPAuthPublicKeyPath string
	// RuntimeImageOverride lets an operator stand up an env on the shared ERun
	// base image (or any external image) before the env's own <tenant>-devops
	// image exists, then switch to the tenant image once it is built. Empty
	// leaves runtime-chart resolution untouched.
	RuntimeImageOverride string
}

// DeploySpec is a pure helm-install plan: it installs the image and chart
// already published at a version, by reference. deploy does not build, push, or
// publish — those are the build and push primitives, composed above deploy by an
// orchestrator (the build --deploy shortcut, erun open --deploy, or the UI).
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

// EnvConfigSaver writes an updated env config to disk.
type EnvConfigSaver func(tenant string, config EnvConfig) error

// PersistRuntimeVersionFromDeploySpecs writes the just-deployed runtime chart's
// version and source registry back into the env config so downstream readers
// (the desktop runtime dialog, `erun list`, and a bare `erun open`) reflect the
// deployed state rather than a stale persisted version. The registry is recorded
// as provenance so a later reopen addresses the same image even if the user
// edits the project's container registry.
//
// A runtime chart whose upgrade was skipped (SkipHelm: every image promoted from
// cache, nothing pushed) carries a freshly minted version that was never pushed;
// persisting it would point the env config at a phantom version the deploy
// picker can never offer. Instead, heal RuntimeVersion to the version the release
// is actually running, which is guaranteed pushed, and leave it untouched when
// that cannot be read.
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
// actually running. An empty string means "could not determine" — the caller
// then leaves the persisted version untouched rather than record the
// never-pushed mint.
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
// leaving a stale or phantom value.
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
	// A malformed platform block would otherwise surface only later, when
	// deploy templates platform artifacts (PowerDNS zone, exposure hostnames)
	// from these values. Reject it here so an inconsistent base-domain /
	// services-zone / authoritative-IP fails the deploy plan up front.
	if err := config.Platform.Validate(); err != nil {
		return ProjectK8sConfig{}, err
	}
	return config.K8sForEnvironment(environment), nil
}

// runDeployStep runs every spec in the group. Single-spec steps and dry-run
// invocations execute serially so traces stay deterministic; a real multi-spec
// step runs them in parallel and joins their errors.
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
func chartDependencyBuildPlan(ctx Context, deployInput HelmDeploySpec) (*commandSpec, error) {
	chartPath := strings.TrimSpace(deployInput.ChartPath)
	if chartPath == "" || isOCIChartReference(chartPath) {
		return nil, nil
	}
	declares, err := helmChartDeclaresDependencies(chartPath)
	if err != nil {
		return nil, err
	}
	if !declares {
		return nil, nil
	}
	build := commandSpec{Name: "helm", Args: []string{"dependency", "build", chartPath}}
	ctx.TraceCommand(build.Dir, build.Name, build.Args...)
	return &build, nil
}

// runChartDependencyBuild executes the planned dependency build (a no-op when
// there is nothing to build). Called after the single-flight acquire so a
// deduped or parallel deploy never races on the shared charts/ dir.
func runChartDependencyBuild(ctx Context, deployInput HelmDeploySpec, build *commandSpec, target string) error {
	if build == nil {
		return nil
	}
	ctx.Info("==> Building chart dependencies for " + target)
	if err := runHelmCommand(ctx, *build); err != nil {
		return fmt.Errorf("deploy %s: helm dependency build: %w", deployInput.ReleaseName, err)
	}
	return nil
}

// publishedValuesPull is a planned deploy-time `helm pull --untar` of a
// by-reference umbrella, plus the temp dir it unpacks into (removed after the
// rollout).
type publishedValuesPull struct {
	command commandSpec
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
func planPublishedValuesPull(ctx Context, deployInput *HelmDeploySpec) *publishedValuesPull {
	ref := strings.TrimSpace(deployInput.ChartPath)
	if !isOCIChartReference(ref) || strings.TrimSpace(deployInput.SubchartKey) == "" {
		return nil
	}
	chartName := ref[strings.LastIndex(ref, "/")+1:]
	dest := filepath.Join(os.TempDir(), "erun-deploy-values-"+chartName)
	envSlug := strings.ToLower(strings.TrimSpace(deployInput.Environment))
	deployInput.PulledValuesFilePath = filepath.Join(dest, chartName, "values."+envSlug+".yaml")
	pull := commandSpec{Name: "helm", Args: []string{"pull", ref, "--version", deployInput.Version, "--untar", "--untardir", dest}}
	ctx.TraceCommand(pull.Dir, pull.Name, pull.Args...)
	return &publishedValuesPull{command: pull, dest: dest}
}

// runPublishedValuesPull executes the planned chart-values pull and returns a
// cleanup that removes the temp dir. The dest is cleared first so a stale
// extraction from a prior deploy never masks the current version's values.
func runPublishedValuesPull(ctx Context, pull *publishedValuesPull, target string) (func(), error) {
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
	if err := runHelmCommand(ctx, pull.command); err != nil {
		return cleanup, fmt.Errorf("helm pull chart values: %w", err)
	}
	return cleanup, nil
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
	if err := applyCloudflareCredentialsSecret(ctx, deployInput); err != nil {
		return fmt.Errorf("deploy %s: %w", deployInput.ReleaseName, err)
	}
	if err := applyMCPAuthSecret(ctx, deployInput); err != nil {
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
		return nil
	}
	return runHelmDeployExecute(ctx, deployInput, valuesPull, depBuild, deploy, target)
}

// runHelmDeployExecute performs the real-run pre-rollout steps and the rollout:
// pull a by-reference umbrella's bundled values, build a local umbrella's
// dependencies, then upgrade. Split out of RunHelmDeploy so the orchestrator
// stays a thin trace-then-execute shell.
func runHelmDeployExecute(ctx Context, deployInput HelmDeploySpec, valuesPull *publishedValuesPull, depBuild *commandSpec, deploy HelmChartDeployerFunc, target string) error {
	cleanupValues, err := runPublishedValuesPull(ctx, valuesPull, target)
	if err != nil {
		return fmt.Errorf("deploy %s: %w", deployInput.ReleaseName, err)
	}
	defer cleanupValues()
	if err := runChartDependencyBuild(ctx, deployInput, depBuild, target); err != nil {
		return err
	}
	return runHelmDeployRollout(ctx, deployInput, deploy, target)
}

// helmDeployTargetLabel builds the label used in deploy feedback. It names the
// release for a non-runtime component so a single-component deploy (e.g.
// erun-backend-postgres) is not mistaken for a full-env redeploy. The runtime
// chart's line stays "<tenant>/<env> <version>" — it *is* the env, carries the
// meaningful runtime version, and feeds the helm-release poller + version
// persistence.
func helmDeployTargetLabel(deployInput HelmDeploySpec) string {
	target := deployInput.Tenant + "/" + deployInput.Environment
	if release := strings.TrimSpace(deployInput.ReleaseName); release != "" && release != RuntimeReleaseName(deployInput.Tenant) {
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
func runHelmDeployRollout(ctx Context, deployInput HelmDeploySpec, deploy HelmChartDeployerFunc, target string) error {
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
	override, ok, err := resolveRolloutTimeoutOverride(target.RolloutTimeout)
	if err != nil {
		return DeploySpec{}, err
	}
	if ok {
		spec.Deploy.Timeout = override
	}
	if err := applyMCPAuthToRuntimeSpec(target, &spec); err != nil {
		return DeploySpec{}, err
	}
	return spec, nil
}

// ResolveCurrentDeploySpecs resolves specs for `erun deploy` — the pure deploy
// primitive that installs a version by reference and never builds.
func ResolveCurrentDeploySpecs(ctx Context, store DeployStore, findProjectRoot ProjectFinderFunc, resolveDockerBuildContext BuildContextResolverFunc, resolveKubernetesDeployContext DeployContextResolverFunc, now NowFunc, target DeployTarget) ([]DeploySpec, error) {
	specs, err := resolveCurrentDeploySpecs(ctx, store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, target, false)
	if err != nil {
		return nil, err
	}
	override, ok, err := resolveRolloutTimeoutOverride(target.RolloutTimeout)
	if err != nil {
		return nil, err
	}
	if ok {
		for i := range specs {
			specs[i].Deploy.Timeout = override
		}
	}
	for i := range specs {
		if err := applyMCPAuthToRuntimeSpec(target, &specs[i]); err != nil {
			return nil, err
		}
	}
	return specs, nil
}

// resolveRolloutTimeoutOverride validates an explicit per-deploy rollout-timeout
// override. A malformed or non-positive duration is a hard error so the deploy
// fails loudly rather than silently ignore the operator's input. Precedence:
// this override > env deploy.timeout > DefaultHelmDeploymentTimeout.
func resolveRolloutTimeoutOverride(raw string) (string, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false, nil
	}
	duration, err := time.ParseDuration(raw)
	if err != nil {
		return "", false, fmt.Errorf("invalid rollout timeout %q", raw)
	}
	if duration <= 0 {
		return "", false, fmt.Errorf("rollout timeout must be greater than zero")
	}
	return duration.String(), true, nil
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

	runtimeImageOverride := strings.TrimSpace(target.RuntimeImageOverride)
	if runtimeImageOverride != "" {
		// The operator chose the runtime image explicitly. Record it on the env
		// so a later open/redeploy addresses the same image, and so the override
		// wins even when a repo-local runtime chart exists.
		resolvedTarget.EnvConfig.RuntimeImage = runtimeImageOverride
	}

	if resolvedTarget.RemoteRepo() {
		return resolvePublishedDeploySpecs(ctx, store, resolvedTarget, target)
	}

	return resolveSelectedLocalDeploySpecs(ctx, store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, resolvedTarget, target, buildOrchestration, runtimeImageOverride)
}

// resolveSelectedLocalDeploySpecs resolves the opt-in-only deploy set for a
// local-repo target. It deploys exactly the selected charts — precedence:
// --components > the env's saved deploy.components > the repo k8s.deployments
// plan; an empty selection defaults to the runtime chart alone (bootstrap/heal).
// When the runtime is selected but the tenant vendors no repo-local runtime
// chart, the published erun-devops chart is appended so a plain deploy can
// bootstrap or heal the runtime.
func resolveSelectedLocalDeploySpecs(ctx Context, store DeployStore, findProjectRoot ProjectFinderFunc, resolveDockerBuildContext BuildContextResolverFunc, resolveKubernetesDeployContext DeployContextResolverFunc, now NowFunc, resolvedTarget OpenResult, target DeployTarget, buildOrchestration bool, runtimeImageOverride string) ([]DeploySpec, error) {
	plan, err := loadProjectK8sPlanForRepo(resolvedTarget.RepoPath, resolvedTarget.Environment)
	if err != nil {
		return nil, err
	}
	selected, selectionSource := resolveSelectedDeployComponents(target.Components, resolvedTarget.EnvConfig.Deploy.Components, plan)
	traceDeployComponentSelection(ctx, selected, selectionSource)

	deployContexts, err := resolveCurrentLocalDeployContexts(findProjectRoot, resolveKubernetesDeployContext, resolvedTarget, selected, plan)
	if err != nil {
		return nil, err
	}
	runtimeSelected := deploySelectionIncludesRuntime(selected, resolvedTarget.Tenant)
	hasLocalRuntime := containsRuntimeContext(deployContexts, resolvedTarget.Tenant)

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

	specs, err := resolveDeploySpecsForContexts(ctx, store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, resolvedTarget, deployContexts, target.VersionOverride, currentBuild, runtimeImageOverride)
	if err != nil {
		return nil, err
	}
	specs, err = appendRuntimeFallbackSpecs(ctx, store, resolvedTarget, target.VersionOverride, specs, runtimeSelected, hasLocalRuntime)
	if err != nil {
		return nil, err
	}
	if len(specs) == 0 {
		return nil, fmt.Errorf("deploy: no components selected for %s/%s — pass --components with a chart name, save a default selection, or select the runtime to bootstrap the environment", resolvedTarget.Tenant, resolvedTarget.Environment)
	}
	return specs, nil
}

func traceDeployComponentSelection(ctx Context, selected []string, source string) {
	if len(selected) == 0 {
		ctx.Trace("deploy: component selection source " + source + "; deploying the runtime chart alone")
		return
	}
	ctx.Trace("deploy: component selection source " + source + "; components " + strings.Join(selected, ", "))
}

// resolvePublishedDeploySpecs resolves a sourceless deploy for a target whose
// repo is not local (a runtime or remote-agent env): every selected chart is
// installed by reference from the published registry — each platform component
// via its published erun-<component> chart (top-level, no local umbrella) and
// the runtime via the published erun-devops chart. Selection comes from
// --components or the env's saved deploy.components; the repo k8s.deployments
// plan needs local source, so ordering falls back to the default component
// rank. An empty selection deploys the runtime alone (bootstrap/heal), matching
// the prior published-runtime-only behaviour.
func resolvePublishedDeploySpecs(ctx Context, store DeployStore, resolvedTarget OpenResult, target DeployTarget) ([]DeploySpec, error) {
	selected, selectionSource := resolveSelectedDeployComponents(target.Components, resolvedTarget.EnvConfig.Deploy.Components, ProjectK8sConfig{})
	traceDeployComponentSelection(ctx, selected, selectionSource)

	tenantComponents := selectedPublishableComponents(selected, resolvedTarget.Tenant)
	runtimeSelected := deploySelectionIncludesRuntime(selected, resolvedTarget.Tenant)

	// Deploying the tenant's own component charts binds the whole deploy to the
	// tenant's version line: the tenant runtime chart (<tenant>-devops) and every
	// selected component chart must be published at this version. Verify up front so
	// an incoherent version fails fast — the runtime must not silently fall back to
	// the shared erun-devops chart, and a component missing at the version must not
	// half-apply before a mid-rollout chart pull aborts the deploy. An erun-only /
	// bootstrap deploy (no tenant components) keeps the published-fallback path.
	if len(tenantComponents) > 0 {
		if err := ensureTenantChartsPublished(ctx, resolvedTarget, target.VersionOverride, runtimeSelected, tenantComponents); err != nil {
			return nil, err
		}
	}

	specs := make([]DeploySpec, 0, len(tenantComponents)+1)
	for _, component := range tenantComponents {
		spec, err := resolvePublishedComponentDeploySpec(ctx, resolvedTarget, component, target.VersionOverride)
		if err != nil {
			return nil, err
		}
		if err := configureDeployInputMetadata(store, resolvedTarget, &spec.Deploy); err != nil {
			return nil, err
		}
		specs = append(specs, spec)
	}
	if runtimeSelected {
		runtimeSpecs, err := resolvePublishedDevopsDeploySpecs(ctx, store, resolvedTarget, target.VersionOverride)
		if err != nil {
			return nil, err
		}
		specs = append(specs, runtimeSpecs...)
	}
	if len(specs) == 0 {
		return nil, fmt.Errorf("deploy: no components selected for %s/%s — pass --components with a publishable component, save a default selection, or select the runtime to bootstrap the environment", resolvedTarget.Tenant, resolvedTarget.Environment)
	}
	return specs, nil
}

// appendRuntimeFallbackSpecs appends the published erun-devops runtime spec when
// the runtime is selected but the tenant has no repo-local runtime chart — the
// deploy counterpart of erun open's published fallback, letting a plain deploy
// bootstrap or heal the runtime from the published ERun image.
func appendRuntimeFallbackSpecs(ctx Context, store DeployStore, resolvedTarget OpenResult, versionOverride string, specs []DeploySpec, runtimeSelected, hasLocalRuntime bool) ([]DeploySpec, error) {
	if !runtimeSelected || hasLocalRuntime {
		return specs, nil
	}
	ctx.Trace("deploy: runtime selected with no repo-local runtime chart; installing the published " + DevopsComponentName + " chart")
	publishedSpecs, err := resolvePublishedDevopsDeploySpecs(ctx, store, resolvedTarget, versionOverride)
	if err != nil {
		return nil, err
	}
	return append(specs, publishedSpecs...), nil
}

func resolveDeploySpecsForContexts(ctx Context, store DeployStore, findProjectRoot ProjectFinderFunc, resolveDockerBuildContext BuildContextResolverFunc, resolveKubernetesDeployContext DeployContextResolverFunc, now NowFunc, target OpenResult, deployContexts []KubernetesDeployContext, versionOverride string, currentBuild *DockerBuildSpec, runtimeImageOverride string) ([]DeploySpec, error) {
	specs := make([]DeploySpec, 0, len(deployContexts))
	for _, deployContext := range deployContexts {
		spec, err := resolveDeploySpecForContext(ctx, store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, target, deployContext, versionOverride, currentBuild, runtimeImageOverride)
		if err != nil {
			return nil, err
		}
		specs = append(specs, spec)
	}
	return specs, nil
}

// resolvePublishedDevopsDeploySpecs resolves the single published-devops deploy
// spec for a target with no local k8s tree to install — a remote-repo target, or
// a local env bootstrapping on --runtime-image before its own <tenant>-devops
// chart exists.
func resolvePublishedDevopsDeploySpecs(ctx Context, store DeployStore, resolvedTarget OpenResult, versionOverride string) ([]DeploySpec, error) {
	spec, err := resolvePublishedDevopsDeploySpec(ctx, resolvedTarget, versionOverride)
	if err != nil {
		return nil, err
	}
	if err := configureDeployInputMetadata(store, resolvedTarget, &spec.Deploy); err != nil {
		return nil, err
	}
	return []DeploySpec{spec}, nil
}

// resolveCurrentLocalDeployContexts discovers, filters, and orders the local k8s
// deploy contexts for the target's repo. A missing or empty <tenant>-devops/k8s
// tree is not an error: it yields an empty set, and the caller stands the runtime
// up via the published erun-devops chart. Any other discovery error (a malformed
// chart) still propagates.
func resolveCurrentLocalDeployContexts(findProjectRoot ProjectFinderFunc, resolveKubernetesDeployContext DeployContextResolverFunc, resolvedTarget OpenResult, selected []string, plan ProjectK8sConfig) ([]KubernetesDeployContext, error) {
	deployContexts, err := ResolveCurrentKubernetesDeployContexts(findProjectRoot, resolveKubernetesDeployContext, resolvedTarget.RepoPath)
	if err != nil {
		if !isNoLocalDeployChartsError(err) {
			return nil, err
		}
		deployContexts = nil
	}
	deployContexts, err = filterDeployContextsBySelection(deployContexts, selected, resolvedTarget.Tenant)
	if err != nil {
		return nil, err
	}
	sortDeployContextsByDeployOrder(deployContexts, plan)
	return deployContexts, nil
}

// isNoLocalDeployChartsError reports whether a discovery error means "this repo
// has no local deploy charts to install" — either the <tenant>-devops/k8s tree
// is absent (fs.ErrNotExist) or present but empty of charts (the "helm chart
// not found in current directory" sentinel ResolveCurrentKubernetesDeployContexts
// returns). Both are tolerated so the runtime can still deploy via the published
// erun-devops chart; a malformed-chart error is not this class and propagates.
func isNoLocalDeployChartsError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, fs.ErrNotExist) {
		return true
	}
	return err.Error() == "helm chart not found in current directory"
}

func resolveDeploySpecForOpenResult(ctx Context, store DeployStore, findProjectRoot ProjectFinderFunc, resolveDockerBuildContext BuildContextResolverFunc, resolveKubernetesDeployContext DeployContextResolverFunc, now NowFunc, target OpenResult, componentName, versionOverride string, currentBuild *DockerBuildSpec) (DeploySpec, error) {
	store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now = normalizeDeployDependencies(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now)
	now = freezeNow(now)

	deployContext, err := resolveDeployContextForTarget(findProjectRoot, resolveKubernetesDeployContext, target, componentName)
	if err != nil {
		return DeploySpec{}, err
	}

	return resolveDeploySpecForContext(ctx, store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, target, deployContext, versionOverride, currentBuild, "")
}

func resolveDeploySpecForContext(ctx Context, store DeployStore, findProjectRoot ProjectFinderFunc, resolveDockerBuildContext BuildContextResolverFunc, resolveKubernetesDeployContext DeployContextResolverFunc, now NowFunc, target OpenResult, deployContext KubernetesDeployContext, versionOverride string, currentBuild *DockerBuildSpec, runtimeImageOverride string) (DeploySpec, error) {
	// A pure deploy installs by reference and only consults the store; the other
	// dependencies are kept on the signature for the shared resolution contract.
	store, _, _, _, _ = normalizeDeployDependencies(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now)
	target = applyDeployKubernetesContext(store, target)

	// Build-orchestration path: a build --deploy / open --deploy / UI run has
	// already built and pushed the working-tree image and hands it to deploy by
	// reference; deploy installs it via an ImageOverride and never builds it.
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
	// A runtime-image override lets an operator stand up a new env on the shared
	// ERun base image before the tenant's own <tenant>-devops image exists.
	if runtimeImageOverride != "" && deployContextOwnsRuntimeChart(deployContext, target.Tenant) {
		return resolvePublishedDevopsDeploySpecWithReason(ctx, target, version, "bypassing the repo-local runtime chart for the runtime image override "+runtimeImageOverride)
	}
	return resolveInstallExistingVersionDeploySpec(ctx, store, target, deployContext, version, versionFromPersist)
}

// resolveInstallExistingVersionDeploySpec resolves a deploy that installs the
// image already published at the pinned version, without building or pushing.
// A builds-here env addresses the published tag by reference rather than
// rebuilding the working tree under that label. The chart's referenced images
// are verified to exist so a version that was never built fails fast. helm still
// runs (no builds means SkipHelm stays false).
func resolveInstallExistingVersionDeploySpec(ctx Context, store DeployStore, target OpenResult, deployContext KubernetesDeployContext, version string, versionFromPersist bool) (DeploySpec, error) {
	deployInput, err := newHelmDeploySpec(target, deployContext, version)
	if err != nil {
		return DeploySpec{}, err
	}
	// Pull-path provenance: when installing the persisted version (a --current
	// redeploy or an open ensure), address the same registry the previous
	// deploy used, so a reopen survives the operator editing the project's
	// container registry.
	if versionFromPersist && deployContextOwnsRuntimeChart(deployContext, target.Tenant) {
		if registry := strings.TrimSpace(target.EnvConfig.RuntimeRegistry); registry != "" {
			deployInput.ContainerRegistry = registry
		}
	}
	// Mirror the snapshot DB-reset decision so re-installing a snapshot behaves
	// the same as first deploying one.
	deployInput.ResetDatabase = deployResetsDatabase(true, deployInput.Version)
	if err := configureDeployInputMetadata(store, target, &deployInput); err != nil {
		return DeploySpec{}, err
	}

	ctx.Trace("deploy: version " + deployInput.Version + " pinned; installing the published image by reference (deploy never builds)")
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
	return confirmDeployImagePresent(ctx, tag, version)
}

// confirmDeployImagePresent checks that the pinned tag exists locally or in the
// registry, returning an error only when its absence is definitive. A registry
// error that is not a "absent" verdict (network/auth) is traced and tolerated:
// the rollout surfaces a real pull failure if the image is genuinely missing.
func confirmDeployImagePresent(ctx Context, tag, version string) error {
	remote, remoteErr := DockerManifestExists(tag)
	if remote {
		return nil
	}
	if local, _ := DockerImageExists(tag); local {
		return nil
	}
	if remoteErr != nil {
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
	deployInput.UseHostCredentials = target.EnvConfig.HasAWSCloudAlias()
	applyCloudProviderDeployMetadata(store, target.EnvConfig, deployInput)
	applyCloudflareDeployMetadata(store, target.EnvConfig, deployInput)
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
	name := strings.TrimSpace(deployContext.ComponentName)
	if name == "" {
		return false
	}
	// Match either runtime alias (<tenant>-devops or the canonical erun-devops)
	// so a tenant tree that vendors the runtime chart under either name resolves
	// — the same dual-lookup erun open uses (runtimeComponentNames).
	return slices.Contains(runtimeComponentNames(tenant), name)
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
// resolving one deploy (or build) shares the same timestamp. Otherwise each
// image's version calls now() independently, and across a multi-image deploy
// those timestamps drift apart — the runtime chart's persisted RuntimeVersion
// can end up differing from the tag actually built and pushed, a phantom version
// the deploy picker can never offer. freezeNow is idempotent, so applying it at
// more than one entrypoint on the same call path is safe.
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

// tenantOwnsProjectRoot reports whether one of the tenant's envs records the
// given project root as its local repo path. A tenant whose envs aren't
// initialized simply doesn't own the root.
func tenantOwnsProjectRoot(store DeployStore, tenant, cleanProjectRoot string) (bool, error) {
	envs, err := store.ListEnvConfigs(tenant)
	if err != nil {
		if errors.Is(err, ErrNotInitialized) {
			return false, nil
		}
		return false, err
	}
	for _, env := range envs {
		path := strings.TrimSpace(env.EffectiveLocalRepoPath())
		if path != "" && filepath.Clean(path) == cleanProjectRoot {
			return true, nil
		}
	}
	return false, nil
}

func resolveProjectTenantForRoot(store DeployStore, projectRoot string) (string, error) {
	tenants, err := store.ListTenantConfigs()
	if err != nil {
		return "", err
	}

	cleanProjectRoot := filepath.Clean(projectRoot)
	matches := make([]TenantConfig, 0, len(tenants))
	for _, tenant := range tenants {
		owns, ownErr := tenantOwnsProjectRoot(store, tenant.Name, cleanProjectRoot)
		if ownErr != nil {
			return "", ownErr
		}
		if owns {
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

	// The full marked registry list the pod acts on (remote-agent build/push;
	// runtime deploy). deployTargetContainerRegistries validates markers, so a
	// bad list surfaces here at deploy time rather than silently in-pod.
	containerRegistries, err := deployTargetContainerRegistries(target)
	if err != nil {
		return HelmDeploySpec{}, err
	}

	// Per-env rollout timeout (config > default). A flag/MCP override, when
	// present, is applied on top of this by the public resolve entrypoints.
	rolloutTimeout, err := target.EnvConfig.Deploy.Resolve()
	if err != nil {
		return HelmDeploySpec{}, err
	}

	// A repo-local runtime umbrella wraps the published erun-devops chart as a
	// subchart; helm won't pass the runtime --sets into subchart scope, so record
	// the subchart key and let command() prefix every runtime value with it. A
	// by-reference umbrella (published OCI chart) has no local Chart.yaml to read,
	// so its key is set by the published-spec resolvers instead.
	subchartKey := ""
	if deployContextOwnsRuntimeChart(deployContext, target.Tenant) && !isOCIChartReference(deployContext.ChartPath) {
		subchartKey, err = helmChartRuntimeSubchartKey(deployContext.ChartPath)
		if err != nil {
			return HelmDeploySpec{}, err
		}
	}

	return HelmDeploySpec{
		ReleaseName:         deployContext.ComponentName,
		ChartPath:           deployContext.ChartPath,
		ValuesFilePath:      valuesFilePath,
		SubchartKey:         subchartKey,
		Tenant:              target.Tenant,
		Environment:         target.Environment,
		Namespace:           KubernetesNamespaceName(target.Tenant, target.Environment),
		KubernetesContext:   target.EnvConfig.KubernetesContext,
		WorktreeStorage:     resolveWorktreeStorage(target),
		WorktreeRepoName:    resolveWorktreeRepoName(target.RepoPath),
		WorktreeHostPath:    resolveWorktreeHostPath(target.RepoPath),
		SSHDEnabled:         target.EnvConfig.SSHD.Enabled,
		MCPPort:             ports.MCP,
		APIPort:             ports.API,
		SSHPort:             ports.SSH,
		CloudProviderAlias:  target.EnvConfig.CloudProviderAlias,
		ContainerRegistry:   resolveProjectContainerRegistry(target.RepoPath, target.Environment),
		RuntimeRegistry:     strings.TrimSpace(target.EnvConfig.RuntimeRegistry),
		ContainerRegistries: containerRegistries,
		Platform:            resolveProjectPlatform(target.RepoPath),
		DisableBuildScript:  target.EnvConfig.DisableBuildScript,
		Idle:                target.EnvConfig.Idle,
		Claude:              target.EnvConfig.Claude,
		RuntimePod:          NormalizeRuntimePodResources(target.EnvConfig.RuntimePod),
		Version:             version,
		Timeout:             rolloutTimeout,
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

// resolveProjectPlatform loads the per-instance platform config from the
// project's .erun/config.yaml and returns it with defaults resolved. Returns a
// zero PlatformConfig when the project is uninitialized or declares no platform
// block, so deploy threads no platform.* values for non-platform projects.
//
// On the `erun deploy` / `build --deploy` path the block is validated up front
// (loadProjectK8sPlanForRepo), so a malformed block fails the plan before
// reaching here. The open-runtime deploy path does not pass through that plan
// step, so it reaches here unvalidated — harmless today because the runtime
// chart it deploys ignores the threaded platform.* values (only the PowerDNS
// singleton reads them, and open never deploys it). Do not rely on this function
// having validated the block.
func resolveProjectPlatform(projectRoot string) PlatformConfig {
	projectRoot = strings.TrimSpace(projectRoot)
	if projectRoot == "" {
		return PlatformConfig{}
	}
	projectConfig, _, err := LoadProjectConfig(projectRoot)
	if err != nil {
		return PlatformConfig{}
	}
	return projectConfig.Platform.Resolve()
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
		deployInput.CloudProvider = resolveCloudProviderForAlias(store, alias, deployInput.CloudProvider)
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

// resolveCloudProviderForAlias resolves the cloud provider for a non-empty
// alias, preferring the store's configured provider and falling back to the
// provider parsed from the alias suffix. When neither resolves, the existing
// value is preserved.
func resolveCloudProviderForAlias(store CloudReadStore, alias, current string) string {
	if provider, err := ResolveCloudProvider(store, alias); err == nil {
		return provider.Provider
	}
	if provider := cloudProviderFromAlias(alias); provider != "" {
		return provider
	}
	return current
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
			// A runtime env is sourceless by default; opting into a mutable
			// source worktree gives it a PVC checkout the pod clones at boot.
			if target.EnvConfig.MountsRuntimeSource() {
				return WorktreeStoragePVC
			}
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
		ReleaseName:          d.ReleaseName,
		ChartPath:            d.ChartPath,
		ValuesFilePath:       d.ValuesFilePath,
		PulledValuesFilePath: d.PulledValuesFilePath,
		SubchartKey:          d.SubchartKey,
		Tenant:               d.Tenant,
		Environment:          d.Environment,
		Namespace:            d.Namespace,
		KubernetesContext:    d.KubernetesContext,
		WorktreeStorage:      d.WorktreeStorage,
		WorktreeRepoName:     d.WorktreeRepoName,
		WorktreeHostPath:     d.WorktreeHostPath,
		RepoURL:              d.RepoURL,
		RepoRef:              d.RepoRef,
		SSHDEnabled:          d.SSHDEnabled,
		MCPPort:              d.MCPPort,
		APIPort:              d.APIPort,
		SSHPort:              d.SSHPort,
		ManagedCloud:         d.ManagedCloud,
		CloudContextName:     d.CloudContextName,
		CloudProvider:        d.CloudProvider,
		CloudProviderAlias:   d.CloudProviderAlias,
		CloudRegion:          d.CloudRegion,
		CloudInstanceID:      d.CloudInstanceID,
		UseHostCredentials:   d.UseHostCredentials,
		OIDCAllowedIssuers:   d.OIDCAllowedIssuers,
		ContainerRegistry:    d.ContainerRegistry,
		ImageOverrides:       cloneStringMap(d.ImageOverrides),
		ResetDatabase:        d.ResetDatabase,
		Idle:                 d.Idle,
		Claude:               d.Claude,
		RuntimePod:           NormalizeRuntimePodResources(d.RuntimePod),
		Version:              d.Version,
		Timeout:              d.Timeout,
		Verbosity:            d.Verbosity,
		Stdout:               stdout,
		Stderr:               stderr,
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
	// A by-reference umbrella's bundled values.<env>.yaml (pulled from the
	// published chart) forwards the tenant's authored subchart values; it is
	// applied first so an operator's config-dir overlay (ValuesFilePath) layered
	// next still wins, and the re-scoped --sets below win over both.
	if strings.TrimSpace(d.PulledValuesFilePath) != "" {
		args = append(args, "-f", d.PulledValuesFilePath)
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
	// Runtime source mount: only a runtime env that opted into MountSource
	// carries a repo URL, so these --sets are absent for every other deploy and
	// existing plans stay byte-for-byte unchanged. The pod clones repoUrl at
	// repoRef into the PVC worktree on first boot.
	if strings.TrimSpace(d.RepoURL) != "" {
		args = append(args,
			"--set-string", "repoUrl="+d.RepoURL,
			"--set-string", "repoRef="+d.RepoRef,
		)
	}
	// Cloudflare credential wiring is appended only when an env attached a
	// Cloudflare alias, so existing (AWS-only / no-cloud) deploy plans are
	// byte-for-byte unchanged. The token is never a --set value — it is
	// delivered out-of-band as a Secret (applyCloudflareCredentialsSecret).
	if d.CloudflareEnabled {
		args = append(args,
			"--set", "cloudContext.cloudflare.enabled=true",
			"--set-string", "cloudContext.cloudflare.accountId="+d.CloudflareAccountID,
			"--set-string", "cloudContext.cloudflare.secretName="+d.CloudflareSecretName,
		)
	}
	// MCP auth is appended only when a trusted key is injected (desktop deploy),
	// so existing deploy plans are byte-for-byte unchanged. The public key rides
	// out-of-band as a Secret (applyMCPAuthSecret); only its name + the issuer
	// and per-env audience are helm values.
	if d.MCPAuthEnabled {
		args = append(args,
			"--set", "mcpAuth.enabled=true",
			"--set-string", "mcpAuth.secretName="+d.MCPAuthSecretName,
			"--set-string", "mcpAuth.issuer="+escapeHelmSetValue(d.MCPAuthIssuer),
			"--set-string", "mcpAuth.audience="+escapeHelmSetValue(d.MCPAuthAudience),
		)
	}
	args = append(args, helmRegistrySetArgs(d)...)
	// disableBuildScript is always set — a boolean projection must be able to
	// reconcile a flip in either direction, so the chart always receives the
	// actual value.
	args = append(args, "--set", "disableBuildScript="+formatHelmBool(d.DisableBuildScript))
	for _, key := range sortedStringMapKeys(d.ImageOverrides) {
		args = append(args, "--set-string", "imageOverrides."+key+"="+d.ImageOverrides[key])
	}
	args = append(args, helmPlatformSetArgs(d.Platform)...)
	args = append(args,
		"--set-string", "idle.timeout="+helmIdleTimeout(d.Idle),
		"--set-string", "idle.workingHours="+helmIdleWorkingHours(d.Idle),
		"--set-string", "idle.timezone="+helmIdleTimezone(d.Idle),
		"--set", "idle.trafficBytes="+formatHelmInt64(helmIdleTrafficBytes(d.Idle)),
		"--set-string", "runtime.resources.limits.cpu="+NormalizeRuntimePodResources(d.RuntimePod).CPU,
		"--set-string", "runtime.resources.limits.memory="+NormalizeRuntimePodResources(d.RuntimePod).Memory,
	)
	args = append(args, helmClaudeSetArgs(d.Claude)...)
	// When the chart is an umbrella wrapping a canonical erun-<base> chart, every
	// --set targets the wrapped subchart's value scope, so prefix each key with
	// the subchart key. No-op (empty prefix) for a chart installed directly.
	prefixHelmSetKeys(args, d.SubchartKey)
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

func isOCIChartReference(chartPath string) bool {
	return strings.HasPrefix(strings.TrimSpace(chartPath), "oci://")
}

// prefixHelmSetKeys nests every helm --set/--set-string/--set-json key under
// prefix (a wrapped umbrella's subchart key), rewriting `key=value` to
// `prefix.key=value` in place. An empty prefix is a no-op, so a chart installed
// directly keeps byte-identical top-level --sets. Only --set* value args are
// touched; base flags, -f, the release name, chart path, and --version are not.
func prefixHelmSetKeys(args []string, prefix string) {
	if strings.TrimSpace(prefix) == "" {
		return
	}
	for i := 0; i+1 < len(args); i++ {
		if !strings.HasPrefix(args[i], "--set") {
			continue
		}
		if !strings.Contains(args[i+1], "=") {
			continue
		}
		args[i+1] = prefix + "." + args[i+1]
	}
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

// HelmImmutableSelectorError is returned by DeployHelmChart when helm aborts an
// upgrade because a Deployment's spec.selector — an immutable field — differs
// from the installed object. RunHelmDeploy recovers by deleting the named
// Deployment (whose PVCs are separate objects and survive) and retrying the
// upgrade once, so the recreated Deployment carries the new selector.
type HelmImmutableSelectorError struct {
	Deployment string
	Err        error
}

func (e *HelmImmutableSelectorError) Error() string {
	if e == nil {
		return ""
	}
	message := "deploy failed: Deployment " + e.Deployment + " has an immutable selector that changed"
	if e.Err != nil {
		message += ": " + e.Err.Error()
	}
	return message
}

func (e *HelmImmutableSelectorError) Unwrap() error {
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

// helmRegistrySetArgs returns the registry-projection helm --set args, each
// guarded on presence so an env that carries none renders nothing and an old
// chart with no .Values for them is unaffected.
func helmRegistrySetArgs(d HelmDeploySpec) []string {
	var args []string
	if registry := strings.TrimSpace(d.ContainerRegistry); registry != "" {
		args = append(args, "--set-string", "containerRegistry="+registry)
	}
	if registry := strings.TrimSpace(d.RuntimeRegistry); registry != "" {
		args = append(args, "--set-string", "runtimeRegistry="+registry)
	}
	if len(d.ContainerRegistries) > 0 {
		if encoded, marshalErr := json.Marshal(d.ContainerRegistries); marshalErr == nil {
			args = append(args, "--set-json", "containerRegistries="+string(encoded))
		}
	}
	return args
}

// helmPlatformSetArgs returns the per-instance platform.* helm --set args,
// guarded on presence so non-platform deploys (every existing env) render none.
// Threaded to every chart; only the PowerDNS singleton reads them (to bootstrap
// its services zone).
func helmPlatformSetArgs(p PlatformConfig) []string {
	if p.IsZero() {
		return nil
	}
	args := []string{
		"--set-string", "platform.baseDomain=" + escapeHelmSetValue(p.BaseDomain),
		"--set-string", "platform.env=" + escapeHelmSetValue(p.Env),
		"--set-string", "platform.servicesZone=" + escapeHelmSetValue(p.ServicesZone),
		"--set-string", "platform.authoritativeIP=" + escapeHelmSetValue(p.AuthoritativeIP),
		"--set-string", "platform.authHost=" + escapeHelmSetValue(p.AuthHost),
	}
	if len(p.Nameservers) > 0 {
		if encoded, marshalErr := json.Marshal(p.Nameservers); marshalErr == nil {
			args = append(args, "--set-json", "platform.nameservers="+string(encoded))
		}
	}
	return args
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

	if k8sDir, ok, err := resolveDirDevopsK8sDir(dir); err != nil || ok {
		return k8sDir, ok, err
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

// resolveDirDevopsK8sDir returns dir/k8s when dir is itself a "-devops" module
// directory whose k8s subdir holds helm charts.
func resolveDirDevopsK8sDir(dir string) (string, bool, error) {
	if !strings.HasSuffix(filepath.Base(dir), "-devops") {
		return "", false, nil
	}
	k8sDir := filepath.Join(dir, "k8s")
	ok, err := isKubernetesDeployModuleDir(k8sDir)
	if err != nil {
		return "", false, err
	}
	if ok {
		return k8sDir, true, nil
	}
	return "", false, nil
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

// resolveHelmDeployChartPath returns the chart path to deploy and an optional
// cleanup the caller must defer. A published OCI chart cannot be copied and
// stamped locally; its version is pinned on the helm command line instead (see
// command()), so it deploys from its original reference with no cleanup. A
// local chart at a pinned version is copied and stamped into a temp dir, whose
// removal is the returned cleanup.
func resolveHelmDeployChartPath(params HelmDeployParams) (string, func(), error) {
	if strings.TrimSpace(params.Version) == "" || isOCIChartReference(params.ChartPath) {
		return params.ChartPath, nil, nil
	}
	chartPath, cleanup, err := prepareHelmChartForDeploy(params.ChartPath, params.Version)
	if err != nil {
		return "", nil, err
	}
	return chartPath, cleanup, nil
}

func helmDeployCommandSpec(params HelmDeployParams, chartPath string) commandSpec {
	return HelmDeploySpec{
		ReleaseName:          params.ReleaseName,
		ChartPath:            chartPath,
		ValuesFilePath:       params.ValuesFilePath,
		PulledValuesFilePath: params.PulledValuesFilePath,
		SubchartKey:          params.SubchartKey,
		Tenant:               params.Tenant,
		Environment:          params.Environment,
		Namespace:            params.Namespace,
		KubernetesContext:    params.KubernetesContext,
		WorktreeStorage:      params.WorktreeStorage,
		WorktreeRepoName:     params.WorktreeRepoName,
		WorktreeHostPath:     params.WorktreeHostPath,
		RepoURL:              params.RepoURL,
		RepoRef:              params.RepoRef,
		SSHDEnabled:          params.SSHDEnabled,
		MCPPort:              params.MCPPort,
		APIPort:              params.APIPort,
		SSHPort:              params.SSHPort,
		ManagedCloud:         params.ManagedCloud,
		CloudContextName:     params.CloudContextName,
		CloudProvider:        params.CloudProvider,
		CloudProviderAlias:   params.CloudProviderAlias,
		CloudRegion:          params.CloudRegion,
		CloudInstanceID:      params.CloudInstanceID,
		UseHostCredentials:   params.UseHostCredentials,
		OIDCAllowedIssuers:   params.OIDCAllowedIssuers,
		ContainerRegistry:    params.ContainerRegistry,
		ImageOverrides:       cloneStringMap(params.ImageOverrides),
		ResetDatabase:        params.ResetDatabase,
		Idle:                 params.Idle,
		Claude:               params.Claude,
		RuntimePod:           params.RuntimePod,
		Version:              params.Version,
		Timeout:              params.Timeout,
		Verbosity:            params.Verbosity,
	}.command()
}

// configureHelmDeployCmdOutput wires the command's stdout/stderr and returns
// the capture buffers used for error classification. At VerbosityInfo helm
// output is captured silently so a successful run is quiet; the buffer feeds
// back into the returned error on failure. At VerbosityDebug or higher the
// output is also teed to params.Stdout/Stderr so the user sees the live --debug
// stream.
func configureHelmDeployCmdOutput(cmd *exec.Cmd, params HelmDeployParams) (*bytes.Buffer, *strings.Builder) {
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
	return helmOutput, stderr
}

// runHelmDeployWithPodWatch runs the already-started helm command alongside the
// release pod watcher and returns once both finish. If the watcher reports an
// early container failure before helm exits, helm is interrupted (then killed
// after a grace period) so the deploy fails fast instead of waiting out the
// helm timeout.
func runHelmDeployWithPodWatch(cmd *exec.Cmd, params HelmDeployParams) (podWatchOutcome, error) {
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
	return watchOutcome, helmErr
}

func DeployHelmChart(params HelmDeployParams) error {
	chartPath, cleanup, err := resolveHelmDeployChartPath(params)
	if err != nil {
		return err
	}
	if cleanup != nil {
		defer cleanup()
	}

	command := helmDeployCommandSpec(params, chartPath)
	cmd := Command(command.Name, command.Args...)
	cmd.Dir = command.Dir
	helmOutput, stderr := configureHelmDeployCmdOutput(cmd, params)

	if err := cmd.Start(); err != nil {
		return err
	}

	watchOutcome, helmErr := runHelmDeployWithPodWatch(cmd, params)
	return classifyHelmDeployResult(params, watchOutcome, helmErr, helmOutput, stderr)
}

// classifyHelmDeployResult turns the raw helm/pod-watch outcome into the final
// error: an early container failure wins over a helm error, then the
// pending-operation and published-chart-not-found stderr cases are recognized,
// and otherwise the helm error is returned with its captured output appended
// (at non-debug verbosity, where the stream was silenced).
func classifyHelmDeployResult(params HelmDeployParams, watchOutcome podWatchOutcome, helmErr error, helmOutput *bytes.Buffer, stderr *strings.Builder) error {
	if watchOutcome.Failure != nil {
		failure := watchOutcome.Failure
		failure.Err = helmErr
		return failure
	}
	if helmErr == nil {
		return nil
	}
	// The string matches below read the dedicated stderr capture, not
	// helmOutput: helm errors land on stderr, and helmOutput doubles as
	// cmd.Stdout so a stderr-only failure can race to empty there.
	if isHelmReleasePendingOperationMessage(stderr.String()) {
		return &HelmReleasePendingOperationError{
			ReleaseName:       params.ReleaseName,
			Namespace:         params.Namespace,
			KubernetesContext: params.KubernetesContext,
			Message:           stderr.String(),
			Err:               helmErr,
		}
	}
	if deployment := immutableSelectorConflictDeployment(stderr.String()); deployment != "" {
		return &HelmImmutableSelectorError{Deployment: deployment, Err: helmErr}
	}
	if isOCIChartReference(params.ChartPath) && isHelmChartNotFoundMessage(stderr.String()) {
		return &PublishedChartNotFoundError{
			ChartReference: params.ChartPath,
			Version:        params.Version,
			Registry:       params.ContainerRegistry,
			HelmOutput:     strings.TrimSpace(stderr.String()),
			Err:            helmErr,
		}
	}
	if params.Verbosity < VerbosityDebug {
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

// runHelmUpgradeWithSelectorRecovery runs the helm upgrade once and, when it
// fails because a Deployment's immutable spec.selector changed, recreates that
// Deployment and retries the upgrade a single time. helm cannot patch an
// immutable selector (e.g. an env first installed under the per-tenant chart
// that labelled pods app=<release>, now upgraded by a chart that renders a
// different selector), so the only fix is delete-and-recreate. The Deployment's
// PVCs are separate objects and survive, so build cache and /home/erun are
// preserved.
func runHelmUpgradeWithSelectorRecovery(ctx Context, deployInput HelmDeploySpec, deploy HelmChartDeployerFunc, target string) error {
	spinner := StartSpinner(ctx.Stderr, "deploying "+target)
	deployErr := deploy(deployInput.Params(ctx.Stdout, ctx.Stderr))
	spinner.Stop()
	var immutableSelector *HelmImmutableSelectorError
	if errors.As(deployErr, &immutableSelector) {
		return recreateDeploymentAndRetry(ctx, deployInput, deploy, immutableSelector.Deployment)
	}
	return deployErr
}

// recreateDeploymentAndRetry deletes the Deployment whose immutable selector
// blocked the helm upgrade and retries the upgrade once. The delete removes only
// the Deployment; the release's PVCs (home, docker, worktree) are separate
// objects, so the recreated pod re-mounts the same data.
func recreateDeploymentAndRetry(ctx Context, deployInput HelmDeploySpec, deploy HelmChartDeployerFunc, deployment string) error {
	ctx.Trace("deploy: Deployment " + deployment + " selector is immutable and changed; deleting it (PVCs preserved) and retrying the upgrade")
	command := deleteDeploymentCommand(deployment, deployInput.Namespace, deployInput.KubernetesContext)
	ctx.TraceCommand(command.Dir, command.Name, command.Args...)
	if err := runDeleteDeploymentCommand(command, ctx.Verbosity, ctx.Stdout, ctx.Stderr); err != nil {
		return fmt.Errorf("recreate deployment %s: %w", deployment, err)
	}
	spinner := StartSpinner(ctx.Stderr, "redeploying "+deployInput.ReleaseName)
	err := deploy(deployInput.Params(ctx.Stdout, ctx.Stderr))
	spinner.Stop()
	return err
}

func deleteDeploymentCommand(deployment, namespace, kubernetesContext string) commandSpec {
	args := []string{}
	if c := strings.TrimSpace(kubernetesContext); c != "" {
		args = append(args, "--context", c)
	}
	args = append(args, "--namespace", namespace, "delete", "deployment", deployment, "--ignore-not-found")
	return commandSpec{Name: "kubectl", Args: args}
}

func runDeleteDeploymentCommand(command commandSpec, verbosity int, stdout, stderr io.Writer) error {
	cmd := Command(command.Name, command.Args...)
	cmd.Dir = command.Dir
	capture := new(bytes.Buffer)
	if verbosity >= VerbosityDebug {
		cmd.Stdout = teeWriter(stdout, capture)
		cmd.Stderr = teeWriter(stderr, capture)
	} else {
		cmd.Stdout = capture
		cmd.Stderr = capture
	}
	if err := cmd.Run(); err != nil {
		if verbosity < VerbosityDebug {
			if output := strings.TrimSpace(capture.String()); output != "" {
				return fmt.Errorf("%w\n%s", err, output)
			}
		}
		return err
	}
	return nil
}

var immutableSelectorDeploymentPattern = regexp.MustCompile(`(?i)Deployment\.apps "([^"]+)" is invalid`)

// immutableSelectorConflictDeployment returns the Deployment name from a helm
// upgrade failure caused by an immutable spec.selector change, or "" when the
// failure is something else. Scoped to spec.selector specifically so an
// unrelated immutable-field error never triggers a Deployment delete.
func immutableSelectorConflictDeployment(message string) string {
	lower := strings.ToLower(message)
	if !strings.Contains(lower, "spec.selector") || !strings.Contains(lower, "field is immutable") {
		return ""
	}
	match := immutableSelectorDeploymentPattern.FindStringSubmatch(message)
	if len(match) < 2 {
		return ""
	}
	return strings.TrimSpace(match[1])
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

// discoverComponentChartDirs walks projectRoot for every chart directory whose
// parent is a k8s/ directory (**/k8s/<chart>/Chart.yaml) — the same places the
// per-image lookup found charts, but returning all of them regardless of image
// name so a module's image-less component charts (a tenant's wrappers) are
// included. It skips .git; vendored subcharts under charts/ are excluded by the
// parent-is-k8s check.
func discoverComponentChartDirs(projectRoot string) ([]KubernetesDeployContext, error) {
	var contexts []KubernetesDeployContext
	err := filepath.WalkDir(projectRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		if d.Name() != "Chart.yaml" {
			return nil
		}
		chartPath := filepath.Dir(path)
		if filepath.Base(filepath.Dir(chartPath)) != "k8s" {
			return nil
		}
		contexts = append(contexts, KubernetesDeployContext{
			Dir:           filepath.Dir(chartPath),
			ComponentName: filepath.Base(chartPath),
			ChartPath:     chartPath,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return contexts, nil
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
			return rewriteChartTemplateImageValue(value, line), true
		}
	}
	return "", false
}

// rewriteChartTemplateImageValue rewrites a quoted Sprintf-style image template
// (%s placeholders fed .Chart.AppVersion) into the same {{ .Chart.AppVersion }}
// form the helm-rendered values produce. A leading "%s/" registry placeholder
// is dropped so the value matches the registry-less chart refs. Values without
// the %s/.Chart.AppVersion pattern are returned unchanged.
func rewriteChartTemplateImageValue(value, line string) string {
	if !strings.Contains(value, "%s") || !strings.Contains(line, ".Chart.AppVersion") {
		return value
	}
	if strings.Count(value, "%s") == 2 && strings.HasPrefix(value, "%s/") {
		value = value[len("%s/"):]
	}
	return strings.ReplaceAll(value, "%s", "{{ .Chart.AppVersion }}")
}

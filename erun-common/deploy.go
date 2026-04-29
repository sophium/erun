package eruncommon

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const DefaultHelmDeploymentTimeout = "2m0s"

const DevopsComponentName = "erun-devops"

const (
	WorktreeStorageHost = "host"
	WorktreeStoragePVC  = "pvc"
)

type DeployStore interface {
	OpenStore
	ListTenantConfigs() ([]TenantConfig, error)
}

type (
	DeployContextResolverFunc       func() (KubernetesDeployContext, error)
	KubernetesDeploymentCheckerFunc func(KubernetesDeploymentCheckParams) (bool, error)
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
	SSHPort            int
	ManagedCloud       bool
	CloudContextName   string
	CloudProviderAlias string
	CloudRegion        string
	CloudInstanceID    string
	Idle               EnvironmentIdleConfig
	Version            string
	Timeout            string
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
	SSHPort            int
	ManagedCloud       bool
	CloudContextName   string
	CloudProviderAlias string
	CloudRegion        string
	CloudInstanceID    string
	Idle               EnvironmentIdleConfig
	Version            string
	Timeout            string
}

type HelmReleaseRecoveryParams struct {
	ReleaseName       string
	Namespace         string
	KubernetesContext string
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
	Name              string
	Namespace         string
	KubernetesContext string
	ExpectedRepoPath  string
	ExpectedSSHD      *bool
	ExpectedMCPPort   int
	ExpectedSSHPort   int
}

type DeployTarget struct {
	Tenant          string
	Environment     string
	RepoPath        string
	VersionOverride string
	Snapshot        *bool
}

type DeploySpec struct {
	Target        OpenResult
	DeployContext KubernetesDeployContext
	Builds        []DockerBuildSpec
	Deploy        HelmDeploySpec
}

func RunDeploySpecs(ctx Context, executions []DeploySpec, build DockerImageBuilderFunc, push DockerPushFunc, deploy HelmChartDeployerFunc) error {
	for _, execution := range executions {
		if err := RunDeploySpec(ctx, execution, build, push, deploy); err != nil {
			return err
		}
	}
	return nil
}

func RunHelmDeploy(ctx Context, deployInput HelmDeploySpec, deploy HelmChartDeployerFunc) error {
	if deploy == nil {
		return fmt.Errorf("helm deployer is required")
	}
	if err := ctx.EnsureKubernetesContext(deployInput.KubernetesContext); err != nil {
		return err
	}
	TraceEnsureKubernetesNamespace(ctx, deployInput.KubernetesContext, deployInput.Namespace)
	command := deployInput.command()
	ctx.TraceCommand(command.Dir, command.Name, command.Args...)
	if ctx.DryRun {
		return nil
	}
	return deploy(deployInput.Params(ctx.Stdout, ctx.Stderr))
}

func RunDeploySpec(ctx Context, execution DeploySpec, build DockerImageBuilderFunc, push DockerPushFunc, deploy HelmChartDeployerFunc) error {
	for _, buildInput := range execution.Builds {
		if err := RunDockerBuild(ctx, buildInput, build); err != nil {
			return err
		}
		if buildInput.Push {
			continue
		}
		pushInput := NewDockerPushSpec(buildInput.ContextDir, buildInput.Image)
		if push != nil {
			if err := push(ctx, pushInput); err != nil {
				return err
			}
			continue
		}
		if err := RunDockerPush(ctx, pushInput, nil); err != nil {
			return err
		}
	}
	return RunHelmDeploy(ctx, execution.Deploy, deploy)
}

func ResolveDeploySpec(store DeployStore, findProjectRoot ProjectFinderFunc, resolveDockerBuildContext BuildContextResolverFunc, resolveKubernetesDeployContext DeployContextResolverFunc, now NowFunc, target DeployTarget, componentName, versionOverride string) (DeploySpec, error) {
	store, findProjectRoot, resolveDockerBuildContext, _, now = normalizeDeployDependencies(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now)
	versionOverride = resolveDeployVersionOverride(target, versionOverride)

	resolvedTarget, err := resolveDeployTarget(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, target)
	if err != nil {
		return DeploySpec{}, err
	}
	return resolveDeploySpecForOpenResult(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, resolvedTarget, componentName, versionOverride, deployTargetSnapshotEnabled(resolvedTarget, target.Snapshot))
}

func ResolveCurrentDeploySpecs(store DeployStore, findProjectRoot ProjectFinderFunc, resolveDockerBuildContext BuildContextResolverFunc, resolveKubernetesDeployContext DeployContextResolverFunc, now NowFunc, target DeployTarget) ([]DeploySpec, error) {
	store, findProjectRoot, resolveDockerBuildContext, _, now = normalizeDeployDependencies(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now)

	resolvedTarget, err := resolveDeployTarget(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, target)
	if err != nil {
		return nil, err
	}

	deployContexts, err := ResolveCurrentKubernetesDeployContexts(findProjectRoot, resolveKubernetesDeployContext, target.RepoPath)
	if err != nil {
		return nil, err
	}

	specs := make([]DeploySpec, 0, len(deployContexts))
	allowLocalBuilds := deployTargetSnapshotEnabled(resolvedTarget, target.Snapshot)
	for _, deployContext := range deployContexts {
		spec, err := resolveDeploySpecForContext(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, resolvedTarget, deployContext, target.VersionOverride, allowLocalBuilds)
		if err != nil {
			return nil, err
		}
		specs = append(specs, spec)
	}

	return specs, nil
}

func ResolveDeploySpecForOpenResult(store DeployStore, findProjectRoot ProjectFinderFunc, resolveDockerBuildContext BuildContextResolverFunc, resolveKubernetesDeployContext DeployContextResolverFunc, now NowFunc, target OpenResult, componentName, versionOverride string) (DeploySpec, error) {
	return resolveDeploySpecForOpenResult(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, target, componentName, versionOverride, true)
}

func resolveDeploySpecForOpenResult(store DeployStore, findProjectRoot ProjectFinderFunc, resolveDockerBuildContext BuildContextResolverFunc, resolveKubernetesDeployContext DeployContextResolverFunc, now NowFunc, target OpenResult, componentName, versionOverride string, allowLocalBuilds bool) (DeploySpec, error) {
	store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now = normalizeDeployDependencies(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now)

	deployContext, err := resolveDeployContextForTarget(findProjectRoot, resolveKubernetesDeployContext, target, componentName)
	if err != nil {
		return DeploySpec{}, err
	}

	return resolveDeploySpecForContext(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, target, deployContext, versionOverride, allowLocalBuilds)
}

func resolveDeploySpecForContext(store DeployStore, findProjectRoot ProjectFinderFunc, resolveDockerBuildContext BuildContextResolverFunc, resolveKubernetesDeployContext DeployContextResolverFunc, now NowFunc, target OpenResult, deployContext KubernetesDeployContext, versionOverride string, allowLocalBuilds bool) (DeploySpec, error) {
	store, findProjectRoot, resolveDockerBuildContext, _, now = normalizeDeployDependencies(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now)
	target = applyDeployKubernetesContext(store, target)

	builds := make([]DockerBuildSpec, 0, 2)
	if allowLocalBuilds && strings.TrimSpace(versionOverride) == "" {
		buildInput, err := ResolveDockerBuildForComponent(store, findProjectRoot, resolveDockerBuildContext, now, target.RepoPath, target.Environment, deployContext.ComponentName, "")
		if err != nil {
			return DeploySpec{}, err
		}
		if buildInput != nil {
			builds = append(builds, *buildInput)
			versionOverride = buildInput.Image.Version
		}
	}

	deployInput, err := newHelmDeploySpec(target, deployContext, versionOverride)
	if err != nil {
		return DeploySpec{}, err
	}
	managedCloud, err := managedCloudEnvironment(store, target.EnvConfig)
	if err != nil {
		return DeploySpec{}, err
	}
	deployInput.ManagedCloud = managedCloud
	if managedCloud {
		applyCloudContextStopMetadata(store, target.EnvConfig, &deployInput)
	}

	dependencyBuilds, err := resolveAdditionalDockerBuildsForDeploy(store, findProjectRoot, resolveDockerBuildContext, now, target.RepoPath, target.Environment, deployContext.ChartPath, builds)
	if err != nil {
		return DeploySpec{}, err
	}
	builds = append(builds, dependencyBuilds...)
	builds = configureDockerBuildsForDeploy(builds)

	return DeploySpec{
		Target:        target,
		DeployContext: deployContext,
		Builds:        builds,
		Deploy:        deployInput,
	}, nil
}

func applyDeployKubernetesContext(store DeployStore, target OpenResult) OpenResult {
	if resolver, ok := store.(deployKubernetesContextResolver); ok {
		target.EnvConfig.KubernetesContext = resolver.ResolveDeployKubernetesContext(target.Environment, target.EnvConfig.KubernetesContext)
	}
	return target
}

func ResolveOpenRuntimeDeploySpec(store DeployStore, findProjectRoot ProjectFinderFunc, resolveDockerBuildContext BuildContextResolverFunc, resolveKubernetesDeployContext DeployContextResolverFunc, now NowFunc, target OpenResult) (DeploySpec, error) {
	store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now = normalizeDeployDependencies(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now)
	return resolveOpenRuntimeDeploySpec(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, target)
}

type BuildDeployStore interface {
	DeployStore
	DockerStore
}

func ResolveCurrentDeploySpecsForDockerTarget(store BuildDeployStore, findProjectRoot ProjectFinderFunc, resolveDockerBuildContext BuildContextResolverFunc, resolveKubernetesDeployContext DeployContextResolverFunc, now NowFunc, target DockerCommandTarget) ([]DeploySpec, error) {
	store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now = normalizeBuildDeployDependencies(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now)

	target, _, err := ResolveDockerBuildTarget(findProjectRoot, target)
	if err != nil {
		return nil, err
	}

	deployTarget, err := resolveDeployTargetForDockerTarget(store, findProjectRoot, target)
	if err != nil {
		return nil, err
	}

	return ResolveCurrentDeploySpecs(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, deployTarget)
}

func ResolveDeploySpecForDockerTarget(store BuildDeployStore, findProjectRoot ProjectFinderFunc, resolveDockerBuildContext BuildContextResolverFunc, resolveKubernetesDeployContext DeployContextResolverFunc, now NowFunc, target DockerCommandTarget, componentName string) (DeploySpec, error) {
	store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now = normalizeBuildDeployDependencies(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now)

	target, _, err := ResolveDockerBuildTarget(findProjectRoot, target)
	if err != nil {
		return DeploySpec{}, err
	}

	deployTarget, err := resolveDeployTargetForDockerTarget(store, findProjectRoot, target)
	if err != nil {
		return DeploySpec{}, err
	}

	return ResolveDeploySpec(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, deployTarget, componentName, target.VersionOverride)
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
	}, nil
}

func resolveDeployVersionOverride(target DeployTarget, versionOverride string) string {
	if versionOverride = strings.TrimSpace(versionOverride); versionOverride != "" {
		return versionOverride
	}
	return strings.TrimSpace(target.VersionOverride)
}

func deployTargetSnapshotEnabled(target OpenResult, override *bool) bool {
	if override != nil {
		return *override
	}
	return target.EnvConfig.SnapshotEnabled()
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

func resolveAdditionalDockerBuildsForDeploy(store DeployStore, findProjectRoot ProjectFinderFunc, resolveDockerBuildContext BuildContextResolverFunc, now NowFunc, projectRoot, environment, chartPath string, existing []DockerBuildSpec) ([]DockerBuildSpec, error) {
	images, err := findLiteralDockerImagesInChart(chartPath)
	if err != nil {
		return nil, err
	}

	seenTags := make(map[string]struct{}, len(existing))
	for _, plan := range existing {
		seenTags[plan.Image.Tag] = struct{}{}
	}

	builds := make([]DockerBuildSpec, 0, len(images))
	for _, image := range images {
		buildInput, ok, err := ResolveDockerBuildForImageReference(store, findProjectRoot, resolveDockerBuildContext, now, projectRoot, environment, image)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		if _, exists := seenTags[buildInput.Image.Tag]; exists {
			continue
		}
		seenTags[buildInput.Image.Tag] = struct{}{}
		builds = append(builds, buildInput)
	}

	return builds, nil
}

func configureDockerBuildsForDeploy(builds []DockerBuildSpec) []DockerBuildSpec {
	for i := range builds {
		builds[i].Platforms = slices.Clone(multiPlatformDockerBuilds)
		builds[i].Push = true
	}
	return builds
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
		SSHPort:            ports.SSH,
		CloudProviderAlias: target.EnvConfig.CloudProviderAlias,
		Idle:               target.EnvConfig.Idle,
		Version:            version,
		Timeout:            DefaultHelmDeploymentTimeout,
	}, nil
}

func applyCloudContextStopMetadata(store CloudReadStore, env EnvConfig, deployInput *HelmDeploySpec) {
	if deployInput == nil {
		return
	}
	deployInput.CloudProviderAlias = strings.TrimSpace(env.CloudProviderAlias)
	status, ok, err := findCloudContextForKubernetesContext(store, env.KubernetesContext)
	if err != nil || !ok {
		return
	}
	deployInput.CloudContextName = status.Name
	deployInput.CloudProviderAlias = status.CloudProviderAlias
	deployInput.CloudRegion = status.Region
	deployInput.CloudInstanceID = status.InstanceID
}

func resolveWorktreeStorage(target OpenResult) string {
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
		SSHPort:            d.SSHPort,
		ManagedCloud:       d.ManagedCloud,
		CloudContextName:   d.CloudContextName,
		CloudProviderAlias: d.CloudProviderAlias,
		CloudRegion:        d.CloudRegion,
		CloudInstanceID:    d.CloudInstanceID,
		Idle:               d.Idle,
		Version:            d.Version,
		Timeout:            d.Timeout,
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
	if strings.TrimSpace(d.KubernetesContext) != "" {
		args = append(args, "--kube-context", d.KubernetesContext)
	}
	args = append(args,
		"-f", d.ValuesFilePath,
		"--set-string", "tenant="+d.Tenant,
		"--set-string", "environment="+d.Environment,
		"--set-string", "worktreeStorage="+d.WorktreeStorage,
		"--set-string", "worktreeRepoName="+d.WorktreeRepoName,
		"--set-string", "worktreeHostPath="+d.WorktreeHostPath,
		"--set", "sshdEnabled="+formatHelmBool(d.SSHDEnabled),
		"--set", "mcpPort="+formatHelmPort(d.MCPPort, MCPServicePort),
		"--set", "sshPort="+formatHelmPort(d.SSHPort, DefaultSSHLocalPort),
		"--set", "managedCloud="+formatHelmBool(d.ManagedCloud),
		"--set-string", "cloudContext.name="+d.CloudContextName,
		"--set-string", "cloudContext.providerAlias="+d.CloudProviderAlias,
		"--set-string", "cloudContext.region="+d.CloudRegion,
		"--set-string", "cloudContext.instanceId="+d.CloudInstanceID,
		"--set-string", "idle.timeout="+helmIdleTimeout(d.Idle),
		"--set-string", "idle.workingHours="+helmIdleWorkingHours(d.Idle),
		"--set", "idle.trafficBytes="+formatHelmInt64(helmIdleTrafficBytes(d.Idle)),
		d.ReleaseName,
		d.ChartPath,
	)

	return commandSpec{
		Dir:  d.ChartPath,
		Name: "helm",
		Args: args,
	}
}

func (p HelmReleaseRecoveryParams) command() commandSpec {
	args := []string{}
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

func (e *HelmReleasePendingOperationError) RecoveryParams(stdout, stderr io.Writer) HelmReleaseRecoveryParams {
	if e == nil {
		return HelmReleaseRecoveryParams{Stdout: stdout, Stderr: stderr}
	}
	return HelmReleaseRecoveryParams{
		ReleaseName:       e.ReleaseName,
		Namespace:         e.Namespace,
		KubernetesContext: e.KubernetesContext,
		Stdout:            stdout,
		Stderr:            stderr,
	}
}

func (e *HelmReleasePendingOperationError) RecoveryCommand() string {
	if e == nil {
		return ""
	}
	command := e.RecoveryParams(nil, nil).command()
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

	projectRoot := strings.TrimSpace(projectRootOverride)
	if projectRoot == "" {
		var err error
		projectRoot, err = resolveDockerBuildProjectRoot(findProjectRoot, DockerCommandTarget{})
		if err != nil {
			return "", false, err
		}
	}
	if projectRoot == "" || dir != filepath.Clean(projectRoot) {
		return "", false, nil
	}

	return resolveProjectRootDevopsK8sDir(findProjectRoot, projectRoot)
}

func resolveProjectRootDevopsK8sDir(findProjectRoot ProjectFinderFunc, projectRoot string) (string, bool, error) {
	projectRoot = filepath.Clean(strings.TrimSpace(projectRoot))
	if projectRoot == "" {
		return "", false, nil
	}

	if tenant, detectedProjectRoot, err := findProjectRoot(); err == nil &&
		filepath.Clean(strings.TrimSpace(detectedProjectRoot)) == projectRoot &&
		strings.TrimSpace(tenant) != "" {
		k8sDir := filepath.Join(projectRoot, RuntimeReleaseName(tenant), "k8s")
		if ok, err := isKubernetesDeployModuleDir(k8sDir); err != nil {
			return "", false, err
		} else if ok {
			return k8sDir, true, nil
		}
	}

	entries, err := os.ReadDir(projectRoot)
	if err != nil {
		return "", false, err
	}

	candidates := make([]string, 0, 1)
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasSuffix(entry.Name(), "-devops") {
			continue
		}

		k8sDir := filepath.Join(projectRoot, entry.Name(), "k8s")
		ok, err := isKubernetesDeployModuleDir(k8sDir)
		if err != nil {
			return "", false, err
		}
		if ok {
			candidates = append(candidates, k8sDir)
		}
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
	if strings.TrimSpace(params.Version) != "" {
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
		SSHPort:            params.SSHPort,
		ManagedCloud:       params.ManagedCloud,
		CloudContextName:   params.CloudContextName,
		CloudProviderAlias: params.CloudProviderAlias,
		CloudRegion:        params.CloudRegion,
		CloudInstanceID:    params.CloudInstanceID,
		Idle:               params.Idle,
		Timeout:            params.Timeout,
	}.command()

	cmd := exec.Command(command.Name, command.Args...)
	cmd.Dir = command.Dir
	cmd.Stdout = params.Stdout
	stderr := new(strings.Builder)
	if params.Stderr != nil {
		cmd.Stderr = io.MultiWriter(params.Stderr, stderr)
	} else {
		cmd.Stderr = stderr
	}
	err := cmd.Run()
	if err != nil && isHelmReleasePendingOperationMessage(stderr.String()) {
		return &HelmReleasePendingOperationError{
			ReleaseName:       params.ReleaseName,
			Namespace:         params.Namespace,
			KubernetesContext: params.KubernetesContext,
			Message:           stderr.String(),
			Err:               err,
		}
	}
	return err
}

func ClearHelmReleasePendingOperation(params HelmReleaseRecoveryParams) error {
	if strings.TrimSpace(params.ReleaseName) == "" {
		return fmt.Errorf("helm release name is required")
	}
	if strings.TrimSpace(params.Namespace) == "" {
		return fmt.Errorf("helm release namespace is required")
	}

	command := params.command()
	cmd := exec.Command(command.Name, command.Args...)
	cmd.Stdout = params.Stdout
	cmd.Stderr = params.Stderr
	return cmd.Run()
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

func CheckKubernetesDeployment(params KubernetesDeploymentCheckParams) (bool, error) {
	args := make([]string, 0, 8)
	if strings.TrimSpace(params.KubernetesContext) != "" {
		args = append(args, "--context", params.KubernetesContext)
	}
	if strings.TrimSpace(params.Namespace) != "" {
		args = append(args, "--namespace", params.Namespace)
	}
	args = append(args, "get", "deployment", params.Name, "-o", "name")

	output, err := exec.Command("kubectl", args...).CombinedOutput()
	if err == nil {
		if strings.TrimSpace(params.ExpectedRepoPath) == "" && params.ExpectedSSHD == nil && params.ExpectedMCPPort == 0 && params.ExpectedSSHPort == 0 {
			return true, nil
		}
		return deploymentMatchesExpectedSettings(params)
	}

	message := strings.ToLower(string(output))
	if strings.Contains(message, "notfound") || strings.Contains(message, "not found") || strings.Contains(message, "no resources found") {
		return false, nil
	}

	return false, fmt.Errorf("failed to check deployment %q: %w", params.Name, err)
}

func deploymentMatchesExpectedSettings(params KubernetesDeploymentCheckParams) (bool, error) {
	args := make([]string, 0, 8)
	if strings.TrimSpace(params.KubernetesContext) != "" {
		args = append(args, "--context", params.KubernetesContext)
	}
	if strings.TrimSpace(params.Namespace) != "" {
		args = append(args, "--namespace", params.Namespace)
	}
	args = append(args, "get", "deployment", params.Name, "-o", "json")

	output, err := exec.Command("kubectl", args...).CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("failed to inspect deployment %q: %w", params.Name, err)
	}

	var deployment struct {
		Spec struct {
			Template struct {
				Spec struct {
					Containers []struct {
						Name string `json:"name"`
						Env  []struct {
							Name  string `json:"name"`
							Value string `json:"value"`
						} `json:"env"`
					} `json:"containers"`
				} `json:"spec"`
			} `json:"template"`
		} `json:"spec"`
	}
	if err := yaml.Unmarshal(output, &deployment); err != nil {
		return false, fmt.Errorf("failed to parse deployment %q: %w", params.Name, err)
	}

	expectedRepoPath := strings.TrimSpace(params.ExpectedRepoPath)
	for _, container := range deployment.Spec.Template.Spec.Containers {
		if strings.TrimSpace(container.Name) != params.Name {
			continue
		}
		matchesRepoPath := strings.TrimSpace(expectedRepoPath) == ""
		matchesSSHD := params.ExpectedSSHD == nil
		matchesMCPPort := params.ExpectedMCPPort <= 0
		matchesSSHPort := params.ExpectedSSHPort <= 0
		for _, env := range container.Env {
			switch strings.TrimSpace(env.Name) {
			case "ERUN_REPO_PATH":
				if strings.TrimSpace(expectedRepoPath) != "" {
					matchesRepoPath = filepath.Clean(strings.TrimSpace(env.Value)) == filepath.Clean(expectedRepoPath)
				}
			case "ERUN_SSHD_ENABLED":
				if params.ExpectedSSHD != nil {
					matchesSSHD = strings.EqualFold(strings.TrimSpace(env.Value), formatHelmBool(*params.ExpectedSSHD))
				}
			case "ERUN_MCP_PORT":
				if params.ExpectedMCPPort > 0 {
					matchesMCPPort = strings.TrimSpace(env.Value) == fmt.Sprintf("%d", params.ExpectedMCPPort)
				}
			case "ERUN_SSHD_PORT":
				if params.ExpectedSSHPort > 0 {
					matchesSSHPort = strings.TrimSpace(env.Value) == fmt.Sprintf("%d", params.ExpectedSSHPort)
				}
			}
		}
		return matchesRepoPath && matchesSSHD && matchesMCPPort && matchesSSHPort, nil
	}

	return false, nil
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
		if err != nil {
			return err
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
		if filepath.Base(chartPath) != componentName || filepath.Base(filepath.Dir(chartPath)) != "k8s" {
			return nil
		}

		matches = append(matches, chartPath)
		return nil
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

func findLiteralDockerImagesInChart(chartPath string) ([]string, error) {
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
			trimmed := strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(trimmed, "image:"):
				trimmed = strings.TrimPrefix(trimmed, "image:")
			case strings.HasPrefix(trimmed, "- image:"):
				trimmed = strings.TrimPrefix(trimmed, "- image:")
			default:
				continue
			}
			value := strings.TrimSpace(trimmed)
			if idx := strings.Index(value, "#"); idx >= 0 {
				value = strings.TrimSpace(value[:idx])
			}
			value = strings.Trim(value, `"'`)
			if value == "" || strings.Contains(value, "{{") {
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

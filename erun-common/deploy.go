package eruncommon

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const DefaultHelmDeploymentTimeout = "2m0s"

const DevopsComponentName = "erun-devops"

type DeployStore interface {
	OpenStore
	ListTenantConfigs() ([]TenantConfig, error)
}

type (
	DeployContextResolverFunc       func() (KubernetesDeployContext, error)
	KubernetesDeploymentCheckerFunc func(KubernetesDeploymentCheckParams) (bool, error)
	HelmChartDeployerFunc           func(HelmDeployParams) error
)

type KubernetesDeployContext struct {
	Dir           string
	ComponentName string
	ChartPath     string
}

type HelmDeployParams struct {
	ReleaseName       string
	ChartPath         string
	ValuesFilePath    string
	Namespace         string
	KubernetesContext string
	WorktreeHostPath  string
	Version           string
	Timeout           string
	Stdout            io.Writer
	Stderr            io.Writer
}

type HelmDeploySpec struct {
	ReleaseName       string
	ChartPath         string
	ValuesFilePath    string
	Namespace         string
	KubernetesContext string
	WorktreeHostPath  string
	Version           string
	Timeout           string
}

type KubernetesDeploymentCheckParams struct {
	Name              string
	Namespace         string
	KubernetesContext string
	ExpectedRepoPath  string
}

type DeployTarget struct {
	Tenant      string
	Environment string
	RepoPath    string
}

type DeploySpec struct {
	Target        OpenResult
	DeployContext KubernetesDeployContext
	Builds        []DockerBuildSpec
	Deploy        HelmDeploySpec
}

func RunHelmDeploy(ctx Context, deployInput HelmDeploySpec, deploy HelmChartDeployerFunc) error {
	if deploy == nil {
		return fmt.Errorf("helm deployer is required")
	}
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
	store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now = normalizeDeployDependencies(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now)

	resolvedTarget, err := resolveDeployTarget(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, target)
	if err != nil {
		return DeploySpec{}, err
	}
	return ResolveDeploySpecForOpenResult(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, resolvedTarget, componentName, versionOverride)
}

func ResolveDeploySpecForOpenResult(store DeployStore, findProjectRoot ProjectFinderFunc, resolveDockerBuildContext BuildContextResolverFunc, resolveKubernetesDeployContext DeployContextResolverFunc, now NowFunc, target OpenResult, componentName, versionOverride string) (DeploySpec, error) {
	store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now = normalizeDeployDependencies(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now)

	deployContext, err := resolveDeployContextForTarget(findProjectRoot, resolveKubernetesDeployContext, target, componentName)
	if err != nil {
		return DeploySpec{}, err
	}

	builds := make([]DockerBuildSpec, 0, 2)
	if strings.TrimSpace(versionOverride) == "" {
		buildInput, err := ResolveDockerBuildForComponent(store, findProjectRoot, resolveDockerBuildContext, now, target.RepoPath, target.Environment, deployContext.ComponentName)
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

	dependencyBuilds, err := resolveAdditionalDockerBuildsForDeploy(store, findProjectRoot, resolveDockerBuildContext, now, target.RepoPath, deployContext.ChartPath, builds)
	if err != nil {
		return DeploySpec{}, err
	}
	builds = append(builds, dependencyBuilds...)

	return DeploySpec{
		Target:        target,
		DeployContext: deployContext,
		Builds:        builds,
		Deploy:        deployInput,
	}, nil
}

func ResolveOpenRuntimeDeploySpec(store DeployStore, findProjectRoot ProjectFinderFunc, resolveDockerBuildContext BuildContextResolverFunc, resolveKubernetesDeployContext DeployContextResolverFunc, now NowFunc, target OpenResult) (DeploySpec, error) {
	store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now = normalizeDeployDependencies(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now)
	return resolveOpenRuntimeDeploySpec(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, target)
}

func resolveDeployTarget(store DeployStore, findProjectRoot ProjectFinderFunc, resolveDockerBuildContext BuildContextResolverFunc, resolveKubernetesDeployContext DeployContextResolverFunc, now NowFunc, target DeployTarget) (OpenResult, error) {
	store, findProjectRoot, _, _, _ = normalizeDeployDependencies(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now)

	if strings.TrimSpace(target.Tenant) != "" || strings.TrimSpace(target.Environment) != "" || strings.TrimSpace(target.RepoPath) != "" {
		if strings.TrimSpace(target.Tenant) == "" || strings.TrimSpace(target.Environment) == "" {
			return OpenResult{}, fmt.Errorf("tenant and environment overrides are required together")
		}

		result, err := ResolveOpen(store, OpenParams{
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

	projectRoot, err := resolveDockerBuildProjectRoot(findProjectRoot, DockerCommandTarget{})
	if err != nil {
		return OpenResult{}, err
	}
	if projectRoot == "" {
		return OpenResult{}, fmt.Errorf("cannot determine project root for Helm deployment")
	}

	tenant, err := resolveProjectTenantForRoot(store, projectRoot)
	if err != nil {
		return OpenResult{}, err
	}

	environment, err := loadDefaultEnvironment(store, tenant)
	if err != nil {
		return OpenResult{}, err
	}

	return ResolveOpen(store, OpenParams{
		Tenant:      tenant,
		Environment: environment,
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

func resolveAdditionalDockerBuildsForDeploy(store DeployStore, findProjectRoot ProjectFinderFunc, resolveDockerBuildContext BuildContextResolverFunc, now NowFunc, projectRoot, chartPath string, existing []DockerBuildSpec) ([]DockerBuildSpec, error) {
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
		buildInput, ok, err := ResolveDockerBuildForImageReference(store, findProjectRoot, resolveDockerBuildContext, now, projectRoot, image)
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

func loadDefaultEnvironment(store DeployStore, tenant string) (string, error) {
	tenantConfig, _, err := store.LoadTenantConfig(tenant)
	if err != nil {
		return "", err
	}
	if tenantConfig.DefaultEnvironment == "" {
		return "", ErrDefaultEnvironmentNotConfigured
	}
	return tenantConfig.DefaultEnvironment, nil
}

func newHelmDeploySpec(target OpenResult, deployContext KubernetesDeployContext, versionOverride string) (HelmDeploySpec, error) {
	valuesFilePath, err := resolveKubernetesDeployValuesFile(deployContext.ChartPath, target.Environment)
	if err != nil {
		return HelmDeploySpec{}, err
	}

	version := strings.TrimSpace(versionOverride)

	return HelmDeploySpec{
		ReleaseName:       deployContext.ComponentName,
		ChartPath:         deployContext.ChartPath,
		ValuesFilePath:    valuesFilePath,
		Namespace:         KubernetesNamespaceName(target.Tenant, target.Environment),
		KubernetesContext: target.EnvConfig.KubernetesContext,
		WorktreeHostPath:  target.RepoPath,
		Version:           version,
		Timeout:           DefaultHelmDeploymentTimeout,
	}, nil
}

func (d HelmDeploySpec) Params(stdout, stderr io.Writer) HelmDeployParams {
	return HelmDeployParams{
		ReleaseName:       d.ReleaseName,
		ChartPath:         d.ChartPath,
		ValuesFilePath:    d.ValuesFilePath,
		Namespace:         d.Namespace,
		KubernetesContext: d.KubernetesContext,
		WorktreeHostPath:  d.WorktreeHostPath,
		Version:           d.Version,
		Timeout:           d.Timeout,
		Stdout:            stdout,
		Stderr:            stderr,
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
		"--set-string", "worktreeHostPath="+d.WorktreeHostPath,
		d.ReleaseName,
		d.ChartPath,
	)

	return commandSpec{
		Dir:  d.ChartPath,
		Name: "helm",
		Args: args,
	}
}

func ResolveKubernetesDeployContext() (KubernetesDeployContext, error) {
	dir, err := os.Getwd()
	if err != nil {
		return KubernetesDeployContext{}, err
	}

	return KubernetesDeployContextAtDir(dir), nil
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

	args := []string{
		"upgrade",
		"--install",
		"--wait",
		"--wait-for-jobs",
		"--timeout", params.Timeout,
		"--namespace", params.Namespace,
	}
	if strings.TrimSpace(params.KubernetesContext) != "" {
		args = append(args, "--kube-context", params.KubernetesContext)
	}
	args = append(args,
		"-f", params.ValuesFilePath,
		"--set-string", "worktreeHostPath="+params.WorktreeHostPath,
		params.ReleaseName,
		chartPath,
	)

	cmd := exec.Command("helm", args...)
	cmd.Dir = chartPath
	cmd.Stdout = params.Stdout
	cmd.Stderr = params.Stderr
	return cmd.Run()
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
		if strings.TrimSpace(params.ExpectedRepoPath) == "" {
			return true, nil
		}
		return deploymentUsesExpectedRepoPath(params)
	}

	message := strings.ToLower(string(output))
	if strings.Contains(message, "notfound") || strings.Contains(message, "not found") || strings.Contains(message, "no resources found") {
		return false, nil
	}

	return false, fmt.Errorf("failed to check deployment %q: %w", params.Name, err)
}

func deploymentUsesExpectedRepoPath(params KubernetesDeploymentCheckParams) (bool, error) {
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
		for _, env := range container.Env {
			if strings.TrimSpace(env.Name) == "ERUN_REPO_PATH" {
				return filepath.Clean(strings.TrimSpace(env.Value)) == filepath.Clean(expectedRepoPath), nil
			}
		}
		return false, nil
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

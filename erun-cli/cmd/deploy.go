package cmd

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/sophium/erun/internal"
	"github.com/sophium/erun/internal/bootstrap"
	"github.com/sophium/erun/internal/opener"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const defaultHelmDeploymentTimeout = "2m0s"

type KubernetesDeployContext struct {
	Dir           string
	ComponentName string
	ChartPath     string
}

type HelmDeployRequest struct {
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

type HelmDeployPlan struct {
	ReleaseName       string
	ChartPath         string
	ValuesFilePath    string
	Namespace         string
	KubernetesContext string
	WorktreeHostPath  string
	Version           string
	Timeout           string
}

type KubernetesDeploymentCheckRequest struct {
	Name              string
	Namespace         string
	KubernetesContext string
}

const devopsComponentName = "erun-devops"

func newK8sCmd(deps Dependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "k8s",
		Short:         "Kubernetes utilities",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	cmd.AddCommand(newK8sDeployCmd(deps))
	return cmd
}

func NewDeployCmd(deps Dependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "deploy",
		Short:         "Deploy the current component Helm chart",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runKubernetesDeployCommand(cmd, deps, "")
		},
	}
	addDryRunFlag(cmd)
	return cmd
}

func newK8sDeployCmd(deps Dependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "deploy COMPONENT",
		Short:         "Deploy a component Helm chart",
		Args:          cobra.ExactArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runKubernetesDeployCommand(cmd, deps, args[0])
		},
	}
	addDryRunFlag(cmd)
	return cmd
}

func runKubernetesDeployCommand(cmd *cobra.Command, deps Dependencies, componentName string) error {
	deps = withDependencyDefaults(deps)

	buildPlans := make([]DockerBuildPlan, 0, 2)
	decisionNotes := make([]string, 0, 4)
	var deployContext KubernetesDeployContext
	if componentName == "" {
		currentContext, err := resolveKubernetesDeployContext(deps, "")
		if err != nil {
			return err
		}
		deployContext = currentContext
		if shouldBuildBeforeDeploy(currentContext) {
			decisionNotes = append(decisionNotes, "decision: building the current component before deploy because the current directory is a docker component")
			plan, err := resolveDockerBuildPlan(deps)
			if err != nil {
				return err
			}
			buildPlans = append(buildPlans, plan)
		}
	}

	target, err := resolveKubernetesDeploymentTarget(deps)
	if err != nil {
		return err
	}

	if strings.TrimSpace(deployContext.ComponentName) == "" {
		currentContext, err := resolveKubernetesDeployContextForTarget(deps, target, componentName)
		if err != nil {
			return err
		}
		deployContext = currentContext
	}

	if len(buildPlans) == 0 {
		plan, err := resolveDockerBuildPlanForComponent(deps, target.RepoPath, target.Environment, deployContext.ComponentName)
		if err != nil {
			return err
		}
		if plan != nil {
			decisionNotes = append(decisionNotes, "decision: resolved a local component image build for the deployment target")
			buildPlans = append(buildPlans, *plan)
		}
	}

	versionOverride := ""
	if len(buildPlans) > 0 && buildPlans[0].Image.IsLocalBuild {
		versionOverride = buildPlans[0].Image.Version
		decisionNotes = append(decisionNotes, "decision: propagating the primary built image version into Helm chart version and appVersion")
	}

	deployPlan, err := newHelmDeployPlan(target, deployContext, versionOverride)
	if err != nil {
		return err
	}

	dependencyBuildPlans, err := resolveAdditionalDockerBuildPlansForDeploy(target.RepoPath, deployContext.ChartPath, buildPlans)
	if err != nil {
		return err
	}
	buildPlans = append(buildPlans, dependencyBuildPlans...)
	if len(dependencyBuildPlans) > 0 {
		decisionNotes = append(decisionNotes, fmt.Sprintf("decision: resolved %d additional image build(s) from literal chart image references", len(dependencyBuildPlans)))
	}

	emitTraceNotes(cmd, cmd.ErrOrStderr(), decisionNotes...)
	for _, buildPlan := range buildPlans {
		emitCommandTrace(cmd, cmd.ErrOrStderr(), buildPlan.Trace())
		emitTraceNotes(cmd, cmd.ErrOrStderr(), buildPlan.DecisionNotes()...)
		emitCommandTrace(cmd, cmd.ErrOrStderr(), newDockerPushPlan(buildPlan.ContextDir, buildPlan.Image).Trace())
	}
	traceNotes := make([]string, 0, 1)
	if strings.TrimSpace(deployPlan.Version) != "" {
		traceNotes = append(traceNotes, "chart version override="+deployPlan.Version)
	}
	emitCommandTrace(cmd, cmd.ErrOrStderr(), deployPlan.Trace(), traceNotes...)
	if isDryRunCommand(cmd) {
		return nil
	}

	for _, buildPlan := range buildPlans {
		if err := deps.BuildDockerImage(buildPlan.Request(cmd.OutOrStdout(), cmd.ErrOrStderr())); err != nil {
			return err
		}
		if err := executeDockerPushPlan(cmd, deps, newDockerPushPlan(buildPlan.ContextDir, buildPlan.Image)); err != nil {
			return err
		}
	}

	return deps.DeployHelmChart(deployPlan.Request(cmd.OutOrStdout(), cmd.ErrOrStderr()))
}

func shouldBuildBeforeDeploy(context KubernetesDeployContext) bool {
	dir := filepath.Clean(strings.TrimSpace(context.Dir))
	if dir == "" {
		return false
	}

	return filepath.Base(filepath.Dir(dir)) == "docker"
}

func deployComponentForTarget(deps Dependencies, target opener.Result, componentName string, stdout, stderr io.Writer) error {
	return deployComponentForTargetWithVersionOverride(deps, target, componentName, "", stdout, stderr)
}

func deployComponentForTargetWithVersionOverride(deps Dependencies, target opener.Result, componentName, versionOverride string, stdout, stderr io.Writer) error {
	buildPlans, plan, err := resolveDeployExecutionForTarget(deps, target, componentName, versionOverride)
	if err != nil {
		return err
	}

	for _, buildPlan := range buildPlans {
		if err := deps.BuildDockerImage(buildPlan.Request(stdout, stderr)); err != nil {
			return err
		}
		pushPlan := newDockerPushPlan(buildPlan.ContextDir, buildPlan.Image)
		if err := deps.PushDockerImage(pushPlan.Request(stdout, stderr)); err != nil {
			return err
		}
	}

	return deps.DeployHelmChart(plan.Request(stdout, stderr))
}

func resolveDeployExecutionForTarget(deps Dependencies, target opener.Result, componentName, versionOverride string) ([]DockerBuildPlan, HelmDeployPlan, error) {
	deployContext, err := resolveKubernetesDeployContextForTarget(deps, target, componentName)
	if err != nil {
		return nil, HelmDeployPlan{}, err
	}

	buildPlans := make([]DockerBuildPlan, 0, 2)
	if strings.TrimSpace(versionOverride) == "" {
		buildPlan, err := resolveDockerBuildPlanForComponent(deps, target.RepoPath, target.Environment, deployContext.ComponentName)
		if err != nil {
			return nil, HelmDeployPlan{}, err
		}
		if buildPlan != nil {
			buildPlans = append(buildPlans, *buildPlan)
			versionOverride = buildPlan.Image.Version
		}
	}

	plan, err := newHelmDeployPlan(target, deployContext, versionOverride)
	if err != nil {
		return nil, HelmDeployPlan{}, err
	}

	dependencyBuildPlans, err := resolveAdditionalDockerBuildPlansForDeploy(target.RepoPath, deployContext.ChartPath, buildPlans)
	if err != nil {
		return nil, HelmDeployPlan{}, err
	}
	buildPlans = append(buildPlans, dependencyBuildPlans...)

	return buildPlans, plan, nil
}

func emitResolvedDeployExecutionTrace(cmd *cobra.Command, buildPlans []DockerBuildPlan, deployPlan HelmDeployPlan) {
	for _, buildPlan := range buildPlans {
		emitCommandTrace(cmd, cmd.ErrOrStderr(), buildPlan.Trace())
		emitTraceNotes(cmd, cmd.ErrOrStderr(), buildPlan.DecisionNotes()...)
		emitCommandTrace(cmd, cmd.ErrOrStderr(), newDockerPushPlan(buildPlan.ContextDir, buildPlan.Image).Trace())
	}

	traceNotes := make([]string, 0, 1)
	if strings.TrimSpace(deployPlan.Version) != "" {
		traceNotes = append(traceNotes, "chart version override="+deployPlan.Version)
	}
	emitCommandTrace(cmd, cmd.ErrOrStderr(), deployPlan.Trace(), traceNotes...)
}

func resolveKubernetesDeployContextForTarget(deps Dependencies, target opener.Result, componentName string) (KubernetesDeployContext, error) {
	componentName = strings.TrimSpace(componentName)
	if componentName == "" {
		return resolveKubernetesDeployContext(deps, componentName)
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

func newHelmDeployPlan(target opener.Result, deployContext KubernetesDeployContext, versionOverride string) (HelmDeployPlan, error) {
	valuesFilePath, err := resolveKubernetesDeployValuesFile(deployContext.ChartPath, target.Environment)
	if err != nil {
		return HelmDeployPlan{}, err
	}

	version, err := resolveHelmDeployVersionForTarget(versionOverride)
	if err != nil {
		return HelmDeployPlan{}, err
	}

	return HelmDeployPlan{
		ReleaseName:       deployContext.ComponentName,
		ChartPath:         deployContext.ChartPath,
		ValuesFilePath:    valuesFilePath,
		Namespace:         bootstrap.KubernetesNamespaceName(target.Tenant, target.Environment),
		KubernetesContext: target.EnvConfig.KubernetesContext,
		WorktreeHostPath:  target.RepoPath,
		Version:           version,
		Timeout:           defaultHelmDeploymentTimeout,
	}, nil
}

func resolveAdditionalDockerBuildPlansForDeploy(projectRoot, chartPath string, existing []DockerBuildPlan) ([]DockerBuildPlan, error) {
	images, err := findLiteralDockerImagesInChart(chartPath)
	if err != nil {
		return nil, err
	}

	seenTags := make(map[string]struct{}, len(existing))
	for _, plan := range existing {
		seenTags[plan.Image.Tag] = struct{}{}
	}

	plans := make([]DockerBuildPlan, 0, len(images))
	for _, image := range images {
		plan, ok, err := resolveDockerBuildPlanForImageReference(projectRoot, image)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		if _, exists := seenTags[plan.Image.Tag]; exists {
			continue
		}
		seenTags[plan.Image.Tag] = struct{}{}
		plans = append(plans, plan)
	}

	return plans, nil
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

func resolveDockerBuildPlanForImageReference(projectRoot, image string) (DockerBuildPlan, bool, error) {
	image = strings.TrimSpace(image)
	if image == "" {
		return DockerBuildPlan{}, false, nil
	}

	nameTag := image
	registry := ""
	if idx := strings.LastIndex(image, "/"); idx >= 0 {
		registry = image[:idx]
		nameTag = image[idx+1:]
	}

	imageName, version, ok := strings.Cut(nameTag, ":")
	if !ok || strings.TrimSpace(imageName) == "" || strings.TrimSpace(version) == "" {
		return DockerBuildPlan{}, false, nil
	}

	buildContext, ok, err := findComponentDockerBuildContext(projectRoot, imageName)
	if err != nil || !ok {
		return DockerBuildPlan{}, false, err
	}

	return DockerBuildPlan{
		ContextDir:     resolveDockerBuildContextDirForProject(buildContext.Dir, projectRoot),
		DockerfilePath: buildContext.DockerfilePath,
		Image: DockerImageReference{
			ProjectRoot: projectRoot,
			Registry:    registry,
			ImageName:   imageName,
			Version:     version,
			Tag:         image,
		},
	}, true, nil
}

func resolveHelmDeployVersionForTarget(versionOverride string) (string, error) {
	if strings.TrimSpace(versionOverride) != "" {
		return strings.TrimSpace(versionOverride), nil
	}
	return "", nil
}

func (p HelmDeployPlan) Request(stdout, stderr io.Writer) HelmDeployRequest {
	return HelmDeployRequest{
		ReleaseName:       p.ReleaseName,
		ChartPath:         p.ChartPath,
		ValuesFilePath:    p.ValuesFilePath,
		Namespace:         p.Namespace,
		KubernetesContext: p.KubernetesContext,
		WorktreeHostPath:  p.WorktreeHostPath,
		Version:           p.Version,
		Timeout:           p.Timeout,
		Stdout:            stdout,
		Stderr:            stderr,
	}
}

func (p HelmDeployPlan) Trace() CommandTrace {
	args := []string{
		"upgrade",
		"--install",
		"--wait",
		"--wait-for-jobs",
		"--timeout", p.Timeout,
		"--namespace", p.Namespace,
	}
	if strings.TrimSpace(p.KubernetesContext) != "" {
		args = append(args, "--kube-context", p.KubernetesContext)
	}
	args = append(args,
		"-f", p.ValuesFilePath,
		"--set-string", "worktreeHostPath="+p.WorktreeHostPath,
		p.ReleaseName,
		p.ChartPath,
	)

	return CommandTrace{
		Dir:  p.ChartPath,
		Name: "helm",
		Args: args,
	}
}

func resolveKubernetesDeployContext(deps Dependencies, componentName string) (KubernetesDeployContext, error) {
	componentName = strings.TrimSpace(componentName)
	if componentName == "" {
		context, err := deps.ResolveKubernetesDeployContext()
		if err != nil {
			return KubernetesDeployContext{}, err
		}
		if strings.TrimSpace(context.ChartPath) == "" || strings.TrimSpace(context.ComponentName) == "" {
			return KubernetesDeployContext{}, fmt.Errorf("helm chart not found in current component directory")
		}
		context.ComponentName = strings.TrimSpace(context.ComponentName)
		context.ChartPath = filepath.Clean(context.ChartPath)
		if err := validateHelmChartPath(context.ChartPath); err != nil {
			return KubernetesDeployContext{}, err
		}
		return context, nil
	}

	projectRoot, err := resolveDockerBuildProjectRoot(deps)
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

func defaultKubernetesDeployContextResolver() (KubernetesDeployContext, error) {
	dir, err := os.Getwd()
	if err != nil {
		return KubernetesDeployContext{}, err
	}

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

	return context, nil
}

func defaultHelmChartDeployer(req HelmDeployRequest) error {
	chartPath := req.ChartPath
	var cleanup func()
	if strings.TrimSpace(req.Version) != "" {
		var err error
		chartPath, cleanup, err = prepareHelmChartForDeploy(req.ChartPath, req.Version)
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
		"--timeout", req.Timeout,
		"--namespace", req.Namespace,
	}
	if strings.TrimSpace(req.KubernetesContext) != "" {
		args = append(args, "--kube-context", req.KubernetesContext)
	}
	args = append(args,
		"-f", req.ValuesFilePath,
		"--set-string", "worktreeHostPath="+req.WorktreeHostPath,
		req.ReleaseName,
		chartPath,
	)

	cmd := exec.Command("helm", args...)
	cmd.Dir = chartPath
	cmd.Stdout = req.Stdout
	cmd.Stderr = req.Stderr
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

func defaultKubernetesDeploymentChecker(req KubernetesDeploymentCheckRequest) (bool, error) {
	args := make([]string, 0, 8)
	if strings.TrimSpace(req.KubernetesContext) != "" {
		args = append(args, "--context", req.KubernetesContext)
	}
	if strings.TrimSpace(req.Namespace) != "" {
		args = append(args, "--namespace", req.Namespace)
	}
	args = append(args, "get", "deployment", req.Name, "-o", "name")

	output, err := exec.Command("kubectl", args...).CombinedOutput()
	if err == nil {
		return true, nil
	}

	message := strings.ToLower(string(output))
	if strings.Contains(message, "notfound") || strings.Contains(message, "not found") || strings.Contains(message, "no resources found") {
		return false, nil
	}

	return false, fmt.Errorf("failed to check deployment %q: %w", req.Name, err)
}

func resolveKubernetesDeploymentTarget(deps Dependencies) (opener.Result, error) {
	projectRoot, err := resolveDockerBuildProjectRoot(deps)
	if err != nil {
		return opener.Result{}, err
	}
	if projectRoot == "" {
		return opener.Result{}, fmt.Errorf("cannot determine project root for Helm deployment")
	}

	tenant, err := resolveProjectTenantForRoot(deps, projectRoot)
	if err != nil {
		return opener.Result{}, err
	}

	environment, err := loadDefaultEnvironment(deps.Store, tenant)
	if err != nil {
		return opener.Result{}, err
	}

	return resolveOpenCommand(deps, opener.Request{
		Tenant:      tenant,
		Environment: environment,
	})
}

func resolveProjectTenantForRoot(deps Dependencies, projectRoot string) (string, error) {
	store := deps.Store
	if store == nil {
		store = bootstrap.ConfigStore{}
	}

	tenants, err := store.ListTenantConfigs()
	if err != nil {
		return "", err
	}

	cleanProjectRoot := filepath.Clean(projectRoot)
	matches := make([]internal.TenantConfig, 0, len(tenants))
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

func validateHelmChartPath(chartPath string) error {
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

package eruncommon

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed assets/default-devops-chart/Chart.yaml assets/default-devops-chart/values.local.yaml assets/default-devops-chart/templates/service.yaml
var defaultDevopsChartFiles embed.FS

type defaultDevopsChartTemplate struct {
	AssetPath  string
	TargetPath string
	Mode       os.FileMode
}

var defaultDevopsChartTemplates = []defaultDevopsChartTemplate{
	{
		AssetPath:  "assets/default-devops-chart/Chart.yaml",
		TargetPath: "__MODULE_NAME__/k8s/__MODULE_NAME__/Chart.yaml",
		Mode:       0o644,
	},
	{
		AssetPath:  "assets/default-devops-chart/values.local.yaml",
		TargetPath: "__MODULE_NAME__/k8s/__MODULE_NAME__/values.local.yaml",
		Mode:       0o644,
	},
	{
		AssetPath:  "assets/default-devops-chart/templates/service.yaml",
		TargetPath: "__MODULE_NAME__/k8s/__MODULE_NAME__/templates/service.yaml",
		Mode:       0o644,
	},
}

func EnsureDefaultDevopsChart(ctx Context, projectRoot, tenant, environment string) error {
	projectRoot = strings.TrimSpace(projectRoot)
	tenant = strings.TrimSpace(tenant)
	if projectRoot == "" || tenant == "" {
		return nil
	}
	projectRoot = filepath.Clean(projectRoot)

	moduleName := RuntimeReleaseName(tenant)
	replacer := strings.NewReplacer("__MODULE_NAME__", moduleName)
	for _, templateFile := range defaultDevopsChartTemplates {
		data, err := defaultDevopsChartFiles.ReadFile(templateFile.AssetPath)
		if err != nil {
			return err
		}

		targetPath := replacer.Replace(templateFile.TargetPath)
		resolvedPath := filepath.Join(projectRoot, filepath.FromSlash(targetPath))
		content := renderDefaultDevopsChartTemplate(templateFile.AssetPath, moduleName, moduleName, data)
		if err := ensureDefaultDevopsFile(ctx, resolvedPath, templateFile.Mode, content); err != nil {
			return err
		}
	}

	valuesFilePath := filepath.Join(projectRoot, moduleName, "k8s", moduleName, "values."+strings.ToLower(strings.TrimSpace(environment))+".yaml")
	if strings.TrimSpace(environment) != "" && !isLocalEnvironment(environment) {
		if err := ensureDefaultDevopsFile(ctx, valuesFilePath, 0o644, nil); err != nil {
			return err
		}
	}

	return nil
}

func renderDefaultDevopsChartTemplate(assetPath, moduleName, imageName string, data []byte) []byte {
	content := strings.ReplaceAll(string(data), "__MODULE_NAME__", moduleName)
	imageName = strings.TrimSpace(imageName)
	if imageName == "" {
		imageName = moduleName
	}
	if assetPath == "assets/default-devops-chart/templates/service.yaml" {
		content = strings.Replace(content, `printf "ghcr.io/sophium/erun-devops:%s"`, `printf "ghcr.io/sophium/`+imageName+`:%s"`, 1)
		content = strings.Replace(content, `index $imageOverrides "erun-devops"`, `index $imageOverrides "`+imageName+`"`, 1)
	}
	return []byte(content)
}

func resolveOpenRuntimeDeploySpec(store DeployStore, findProjectRoot ProjectFinderFunc, resolveDockerBuildContext BuildContextResolverFunc, resolveKubernetesDeployContext DeployContextResolverFunc, now NowFunc, target OpenResult) (DeploySpec, error) {
	if target.RemoteRepo() {
		return resolveDefaultDevopsDeploySpecWithImage(target, DevopsComponentName)
	}

	allowLocalBuilds := deployTargetSnapshotEnabled(target, nil)
	for _, componentName := range openRuntimeComponentNames(target.Tenant) {
		spec, err := resolveDeploySpecForOpenResult(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, target, componentName, "", allowLocalBuilds)
		if err == nil {
			spec.Deploy.ReleaseName = RuntimeReleaseName(target.Tenant)
			return spec, nil
		}
		if !isHelmChartNotFoundForComponent(err) {
			return DeploySpec{}, err
		}
	}

	return resolveDefaultDevopsDeploySpec(target)
}

func openRuntimeComponentNames(tenant string) []string {
	names := []string{DevopsComponentName}
	tenant = strings.TrimSpace(tenant)
	if tenant == "" {
		return names
	}

	tenantComponent := tenant + "-devops"
	if tenantComponent == DevopsComponentName {
		return names
	}
	return append([]string{tenantComponent}, names...)
}

func isHelmChartNotFoundForComponent(err error) bool {
	return err != nil && strings.HasPrefix(err.Error(), "helm chart not found for component ")
}

func resolveDefaultDevopsDeploySpec(target OpenResult) (DeploySpec, error) {
	return resolveDefaultDevopsDeploySpecWithImage(target, RuntimeReleaseName(target.Tenant))
}

func ResolveDefaultDevopsDeploySpecWithImage(target OpenResult, imageName string) (DeploySpec, error) {
	return resolveDefaultDevopsDeploySpecWithImage(target, imageName)
}

func resolveDefaultDevopsDeploySpecWithImage(target OpenResult, imageName string) (DeploySpec, error) {
	moduleName := RuntimeReleaseName(target.Tenant)
	chartPath, err := materializeDefaultDevopsChart(moduleName, imageName)
	if err != nil {
		return DeploySpec{}, err
	}
	if err := ensureDefaultDevopsValuesFile(chartPath, target.Environment); err != nil {
		return DeploySpec{}, err
	}

	deployContext := KubernetesDeployContext{
		Dir:           chartPath,
		ComponentName: DevopsComponentName,
		ChartPath:     chartPath,
	}
	deployInput, err := newHelmDeploySpec(target, deployContext, "")
	if err != nil {
		return DeploySpec{}, err
	}
	deployInput.ReleaseName = moduleName
	if runtimeVersion := strings.TrimSpace(target.EnvConfig.RuntimeVersion); runtimeVersion != "" {
		deployInput.Version = runtimeVersion
	}

	return DeploySpec{
		Target:        target,
		DeployContext: deployContext,
		Deploy:        deployInput,
	}, nil
}

func IsDefaultDevopsChartPath(chartPath string) bool {
	chartPath = filepath.Clean(strings.TrimSpace(chartPath))
	if chartPath == "" {
		return false
	}

	return strings.HasPrefix(filepath.Base(chartPath), "erun-default-devops-chart-")
}

func materializeDefaultDevopsChart(moduleName, imageName string) (string, error) {
	hash, err := defaultDevopsChartHash()
	if err != nil {
		return "", err
	}
	moduleName = strings.TrimSpace(moduleName)
	if moduleName == "" {
		moduleName = DevopsComponentName
	}
	imageName = strings.TrimSpace(imageName)
	if imageName == "" {
		imageName = moduleName
	}

	chartPath := filepath.Join(os.TempDir(), "erun-default-devops-chart-"+moduleName+"-"+imageName+"-"+hash)
	if err := os.MkdirAll(chartPath, 0o755); err != nil {
		return "", err
	}

	entries := []string{
		"assets/default-devops-chart/Chart.yaml",
		"assets/default-devops-chart/values.local.yaml",
		"assets/default-devops-chart/templates/service.yaml",
	}
	for _, name := range entries {
		data, err := defaultDevopsChartFiles.ReadFile(name)
		if err != nil {
			return "", err
		}
		data = renderDefaultDevopsChartTemplate(name, moduleName, imageName, data)

		relativePath := strings.TrimPrefix(name, "assets/default-devops-chart/")
		targetPath := filepath.Join(chartPath, filepath.FromSlash(relativePath))
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(targetPath, data, 0o644); err != nil {
			return "", err
		}
	}

	return chartPath, nil
}

func defaultDevopsChartHash() (string, error) {
	names := []string{}
	if err := fs.WalkDir(defaultDevopsChartFiles, "assets/default-devops-chart", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		names = append(names, path)
		return nil
	}); err != nil {
		return "", err
	}

	sort.Strings(names)
	sum := sha256.New()
	for _, name := range names {
		data, err := defaultDevopsChartFiles.ReadFile(name)
		if err != nil {
			return "", err
		}
		_, _ = sum.Write([]byte(name))
		_, _ = sum.Write(data)
	}

	return hex.EncodeToString(sum.Sum(nil))[:16], nil
}

func ensureDefaultDevopsValuesFile(chartPath, environment string) error {
	environment = strings.ToLower(strings.TrimSpace(environment))
	if environment == "" || environment == "local" {
		return nil
	}

	valuesFilePath := filepath.Join(chartPath, "values."+environment+".yaml")
	return ensureDefaultDevopsValuesFileAtPath(valuesFilePath)
}

func ensureDefaultDevopsValuesFileAtPath(valuesFilePath string) error {
	if _, err := os.Stat(valuesFilePath); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}

	return os.WriteFile(valuesFilePath, nil, 0o644)
}

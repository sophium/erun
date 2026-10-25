package chartfiles

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerbuild"
	"gopkg.in/yaml.v3"
)

func PrepareHelmChartForDeploy(chartPath, version string) (string, func(), error) {
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

// DiscoverComponentChartDirs returns every <k8s>/<chart>/Chart.yaml in the tenant-scoped devops
// k8s dir, image-less charts included; vendored subcharts are excluded.
func DiscoverComponentChartDirs(projectRoot string) ([]model.KubernetesDeployContext, error) {
	k8sDir, ok, err := componentChartsK8sDir(projectRoot)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	var contexts []model.KubernetesDeployContext
	err = filepath.WalkDir(k8sDir, func(path string, d fs.DirEntry, walkErr error) error {
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
		contexts = append(contexts, model.KubernetesDeployContext{
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

// componentChartsK8sDir resolves the component charts dir like deploy's runtime chart lookup,
// considering only -devops modules so a brownfield tenant's own k8s trees stay excluded.
func componentChartsK8sDir(projectRoot string) (string, bool, error) {
	projectRoot = filepath.Clean(strings.TrimSpace(projectRoot))
	if projectRoot == "" {
		return "", false, nil
	}
	if dir, ok, err := dockerbuild.ConfiguredK8sDir(projectRoot); err != nil || ok {
		return dir, ok, err
	}
	dockerDir, declared, err := dockerbuild.ConfiguredDockerDir(projectRoot)
	if err != nil {
		return "", false, err
	}
	if declared {
		return declaredDevopsModuleK8sDir(dockerDir)
	}
	return dockerbuild.ResolveProjectRootDevopsK8sDir(configpkg.FindProjectRoot, projectRoot)
}

// declaredDevopsModuleK8sDir resolves charts inside the paths.docker-declared module only;
// a migrating tenant must not publish the legacy module it is leaving.
func declaredDevopsModuleK8sDir(dockerDir string) (string, bool, error) {
	k8sDir := filepath.Join(filepath.Dir(dockerDir), "k8s")
	isModule, err := dockerbuild.IsKubernetesDeployModuleDir(k8sDir)
	if err != nil || !isModule {
		return "", false, err
	}
	return k8sDir, true, nil
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

func FindDockerImagesInChart(chartPath, appVersion string) ([]string, error) {
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

// rewriteChartTemplateImageValue rewrites a %s/.Chart.AppVersion image template into
// {{ .Chart.AppVersion }} form, dropping a leading "%s/" registry; others are unchanged.
func rewriteChartTemplateImageValue(value, line string) string {
	if !strings.Contains(value, "%s") || !strings.Contains(line, ".Chart.AppVersion") {
		return value
	}
	if strings.Count(value, "%s") == 2 && strings.HasPrefix(value, "%s/") {
		value = value[len("%s/"):]
	}
	return strings.ReplaceAll(value, "%s", "{{ .Chart.AppVersion }}")
}

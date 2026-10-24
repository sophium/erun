package deploy

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/primitive/shared"
	"gopkg.in/yaml.v3"
)

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

// DiscoverComponentChartDirs returns the component-chart directories under the
// project's devops k8s dir (<k8s>/<chart>/Chart.yaml), returning all of them
// regardless of image name so a module's image-less component charts (a tenant's
// wrappers) are included. It resolves that k8s dir scoped to the tenant — see
// componentChartsK8sDir — rather than walking the whole project, so a brownfield
// tenant's own unrelated k8s/*/Chart.yaml trees are never treated as publishable
// component charts. It skips .git; vendored subcharts under charts/ are excluded
// by the parent-is-k8s check.
func DiscoverComponentChartDirs(projectRoot string) ([]shared.KubernetesDeployContext, error) {
	k8sDir, ok, err := componentChartsK8sDir(projectRoot)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	var contexts []shared.KubernetesDeployContext
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
		contexts = append(contexts, shared.KubernetesDeployContext{
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

// componentChartsK8sDir resolves the directory whose subdirectories are the
// project's component charts, scoped so a brownfield tenant's own unrelated
// k8s/*/Chart.yaml trees are never treated as publishable component charts.
// The paths.k8s override wins; otherwise resolution matches deploy's runtime
// chart lookup exactly (resolveProjectRootDevopsK8sDir): the tenant's own
// <tenant>-devops/k8s when present, else the single -devops/k8s convention
// module. Only -devops modules are ever considered, so unrelated k8s trees stay
// excluded whichever branch resolves — and build/push/deploy discover the same
// charts, which the release-repo shape (repo basename != devops module prefix)
// relies on.
func componentChartsK8sDir(projectRoot string) (string, bool, error) {
	projectRoot = filepath.Clean(strings.TrimSpace(projectRoot))
	if projectRoot == "" {
		return "", false, nil
	}
	if dir, ok, err := shared.ConfiguredK8sDir(projectRoot); err != nil || ok {
		return dir, ok, err
	}
	dockerDir, declared, err := shared.ConfiguredDockerDir(projectRoot)
	if err != nil {
		return "", false, err
	}
	if declared {
		return declaredDevopsModuleK8sDir(dockerDir)
	}
	return shared.ResolveProjectRootDevopsK8sDir(configpkg.FindProjectRoot, projectRoot)
}

// declaredDevopsModuleK8sDir resolves component charts inside the devops module
// the project declares through paths.docker. Declaring that module names which
// module erun owns, so only its charts are publishable: a module carrying no
// k8s/ yet has nothing to publish, which is not a reason to look elsewhere.
// Without this a tenant migrating onto erun fell through to the single -devops
// convention match and published the legacy module it is migrating away from.
func declaredDevopsModuleK8sDir(dockerDir string) (string, bool, error) {
	k8sDir := filepath.Join(filepath.Dir(dockerDir), "k8s")
	isModule, err := shared.IsKubernetesDeployModuleDir(k8sDir)
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

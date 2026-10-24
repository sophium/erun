package deploy

import (
	"bytes"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/primitive/shared/projectseam"
)

// The embedded Chart.yaml exists only to migrate legacy scaffolded tenant charts.
//
//go:embed assets/default-devops-chart/Chart.yaml
var defaultDevopsChartFiles embed.FS

// MigrateDefaultDevopsChartAppVersion rewrites only a legacy placeholder-pinned tenant Chart.yaml.
// It emits only trace lines, so it is safe on the dry-run open path.
func MigrateDefaultDevopsChartAppVersion(ctx core.Context, projectRoot, tenant, appVersion string) error {
	projectRoot = strings.TrimSpace(projectRoot)
	tenant = strings.TrimSpace(tenant)
	if projectRoot == "" || tenant == "" {
		return nil
	}
	projectRoot = filepath.Clean(projectRoot)

	moduleName := config.RuntimeReleaseName(tenant)
	chartPath := filepath.Join(projectRoot, moduleName, "k8s", moduleName, "Chart.yaml")
	if _, err := os.Stat(chartPath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	resolvedAppVersion := defaultDevopsChartAppVersion(appVersion)
	data, err := defaultDevopsChartFiles.ReadFile("assets/default-devops-chart/Chart.yaml")
	if err != nil {
		return err
	}
	content := renderDefaultDevopsChartTemplate(moduleName, resolvedAppVersion, data)
	return ensureDefaultDevopsFile(ctx, chartPath, 0o644, content)
}

func defaultDevopsChartAppVersion(appVersion string) string {
	appVersion = strings.TrimSpace(appVersion)
	if appVersion == "" {
		// "dev" matches NormalizeBuildInfo's unbuilt-binary fallback.
		return "dev"
	}
	return appVersion
}

func renderDefaultDevopsChartTemplate(moduleName, appVersion string, data []byte) []byte {
	content := strings.ReplaceAll(string(data), "__MODULE_NAME__", moduleName)
	content = strings.ReplaceAll(content, "__APP_VERSION__", appVersion)
	return []byte(content)
}

func resolveOpenRuntimeDeploySpec(ctx core.Context, store DeployStore, findProjectRoot projectseam.ProjectFinderFunc, resolveDockerBuildContext projectseam.BuildContextResolverFunc, resolveKubernetesDeployContext DeployContextResolverFunc, now projectseam.NowFunc, target config.OpenResult, allowLocalBuilds bool) (DeploySpec, error) {
	// Before any --runtime-image override, a RuntimeImage here is a persisted memo, not this invocation's choice.
	if target.RemoteRepo() {
		return ResolvePublishedDevopsDeploySpecWithOverrides(ctx, target, "", "", false)
	}

	for _, componentName := range runtimeComponentNames(target.Tenant) {
		spec, err := resolveDeploySpecForOpenResult(ctx, store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, target, componentName, "", nil)
		if err == nil {
			spec.Deploy.ReleaseName = config.RuntimeReleaseName(target.Tenant)
			return spec, nil
		}
		if !isHelmChartNotFoundForComponent(err) {
			return DeploySpec{}, err
		}
	}

	return ResolvePublishedDevopsDeploySpecWithOverrides(ctx, target, "", "", false)
}

// runtimeComponentNames lists candidate runtime chart names, most specific first.
func runtimeComponentNames(tenant string) []string {
	names := []string{config.DevopsComponentName}
	tenant = strings.TrimSpace(tenant)
	if tenant == "" {
		return names
	}

	tenantComponent := tenant + "-devops"
	if tenantComponent == config.DevopsComponentName {
		return names
	}
	return append([]string{tenantComponent}, names...)
}

func isHelmChartNotFoundForComponent(err error) bool {
	return err != nil && strings.HasPrefix(err.Error(), "helm chart not found for component ")
}

func ensureDefaultDevopsFile(ctx core.Context, path string, mode os.FileMode, content []byte) error {
	replace, err := shouldWriteDefaultDevopsFile(path, content)
	if err != nil || !replace {
		return err
	}

	ctx.TraceCommand("", "mkdir", "-p", filepath.Dir(path))
	ctx.TraceCommand("", "write-file", path)
	if ctx.DryRun {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, content, mode); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}

func shouldWriteDefaultDevopsFile(path string, content []byte) (bool, error) {
	info, err := os.Stat(path)
	switch {
	case err == nil && info.IsDir():
		return false, fmt.Errorf("%q is a directory", path)
	case err == nil:
		return shouldWriteExistingDefaultDevopsFile(path, content)
	case os.IsNotExist(err):
		return true, nil
	default:
		return false, err
	}
}

func shouldWriteExistingDefaultDevopsFile(path string, content []byte) (bool, error) {
	existing, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	if bytes.Equal(existing, content) {
		return false, nil
	}
	return shouldReplaceDefaultDevopsFile(existing, content), nil
}

// shouldReplaceDefaultDevopsFile replaces only a Chart.yaml still pinned to the "1.0.0" placeholder.
func shouldReplaceDefaultDevopsFile(existing, content []byte) bool {
	current := strings.TrimSpace(string(existing))
	for _, candidate := range legacyDevopsChartYAMLCandidates(content) {
		if current == strings.TrimSpace(candidate) {
			return true
		}
	}
	return false
}

// The `description: ERun DevOps` guard excludes backend-* charts sharing the base name.
func legacyDevopsChartYAMLCandidates(content []byte) []string {
	if !bytes.Contains(content, []byte("description: ERun DevOps")) {
		return nil
	}
	pinned := chartYAMLPinnedToLegacyVersion(string(content))
	if pinned == "" {
		return nil
	}
	return []string{pinned}
}

func chartYAMLPinnedToLegacyVersion(content string) string {
	lines := strings.Split(content, "\n")
	changed := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "version:") || strings.HasPrefix(trimmed, "appVersion:") {
			indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
			key := strings.SplitN(trimmed, ":", 2)[0]
			next := indent + key + `: "1.0.0"`
			if next != line {
				lines[i] = next
				changed = true
			}
		}
	}
	if !changed {
		return ""
	}
	return strings.Join(lines, "\n")
}

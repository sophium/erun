package eruncommon

import (
	"bytes"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The embedded Chart.yaml exists only to migrate legacy scaffolded tenant
// charts (see MigrateDefaultDevopsChartAppVersion). New environments deploy
// the published erun-devops chart directly (#505); the per-tenant scaffold
// copies — and the drift they accumulated (#510) — are retired.
//
//go:embed assets/default-devops-chart/Chart.yaml
var defaultDevopsChartFiles embed.FS

// MigrateDefaultDevopsChartAppVersion rewrites an existing tenant
// `<tenant>-devops/k8s/<tenant>-devops/Chart.yaml` whose appVersion
// still carries the pre-#361 literal placeholder. Tenants with a
// scaffolded devops module keep deploying their local chart (the
// published-chart flow only applies when no local chart exists), so
// this compat migration stays. The legacy detection already used by
// ensureDefaultDevopsFile (description-scoped to "ERun DevOps",
// version + appVersion lines forced to "1.0.0") keeps the rewrite from
// clobbering hand-customised tenant charts.
//
// The trace lines emitted by ensureDefaultDevopsFile remain the
// only side effect in dry-run mode, so adding this call into the
// open path keeps the --dry-run contract intact.
func MigrateDefaultDevopsChartAppVersion(ctx Context, projectRoot, tenant, appVersion string) error {
	projectRoot = strings.TrimSpace(projectRoot)
	tenant = strings.TrimSpace(tenant)
	if projectRoot == "" || tenant == "" {
		return nil
	}
	projectRoot = filepath.Clean(projectRoot)

	moduleName := RuntimeReleaseName(tenant)
	chartPath := filepath.Join(projectRoot, moduleName, "k8s", moduleName, "Chart.yaml")
	if _, err := os.Stat(chartPath); err != nil {
		if os.IsNotExist(err) {
			// Nothing to migrate: the env has no scaffolded chart and
			// deploys the published erun-devops chart instead.
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
		// NormalizeBuildInfo uses "dev" as the unbuilt-binary fallback;
		// keep the placeholder consistent so tests and integration
		// goldens read uniformly.
		return "dev"
	}
	return appVersion
}

func renderDefaultDevopsChartTemplate(moduleName, appVersion string, data []byte) []byte {
	content := strings.ReplaceAll(string(data), "__MODULE_NAME__", moduleName)
	content = strings.ReplaceAll(content, "__APP_VERSION__", appVersion)
	return []byte(content)
}

func resolveOpenRuntimeDeploySpec(ctx Context, store DeployStore, findProjectRoot ProjectFinderFunc, resolveDockerBuildContext BuildContextResolverFunc, resolveKubernetesDeployContext DeployContextResolverFunc, now NowFunc, target OpenResult, allowLocalBuilds bool) (DeploySpec, error) {
	if target.RemoteRepo() {
		return resolvePublishedDevopsDeploySpec(ctx, target, "")
	}

	for _, componentName := range openRuntimeComponentNames(target.Tenant) {
		spec, err := resolveDeploySpecForOpenResult(ctx, store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, target, componentName, "", nil)
		if err == nil {
			spec.Deploy.ReleaseName = RuntimeReleaseName(target.Tenant)
			return spec, nil
		}
		if !isHelmChartNotFoundForComponent(err) {
			return DeploySpec{}, err
		}
	}

	return resolvePublishedDevopsDeploySpec(ctx, target, "")
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

func ensureDefaultDevopsFile(ctx Context, path string, mode os.FileMode, content []byte) error {
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

// shouldReplaceDefaultDevopsFile only ever replaces a legacy scaffolded
// Chart.yaml still pinned to the pre-#361 "1.0.0" placeholder; any other
// existing content is treated as hand-customised and left alone.
func shouldReplaceDefaultDevopsFile(existing, content []byte) bool {
	current := strings.TrimSpace(string(existing))
	for _, candidate := range legacyDevopsChartYAMLCandidates(content) {
		if current == strings.TrimSpace(candidate) {
			return true
		}
	}
	return false
}

// legacyDevopsChartYAMLCandidates returns the pre-#361 shapes of a
// tenant devops chart's Chart.yaml so an existing tenant chart still
// pinned to the literal placeholder `appVersion: "1.0.0"` gets
// auto-rewritten on the next open. Only the devops chart is in scope —
// `description: ERun DevOps` is what disambiguates from the backend-*
// charts that share the base name.
//
// The candidates are the *new* content with the version + appVersion
// lines forced back to "1.0.0", so byte-exact comparison in
// shouldReplaceDefaultDevopsFile picks up the upgrade target without
// needing structural YAML diffing.
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

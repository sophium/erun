package eruncommon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestFindLiteralDockerImagesInChartReadsDashPrefixedImageEntries(t *testing.T) {
	chartPath := filepath.Join(t.TempDir(), "erun-devops")
	templatePath := filepath.Join(chartPath, "templates", "deployment.yaml")
	if err := os.MkdirAll(filepath.Dir(templatePath), 0o755); err != nil {
		t.Fatalf("mkdir templates dir: %v", err)
	}
	if err := os.WriteFile(templatePath, []byte("apiVersion: apps/v1\nkind: Deployment\nspec:\n  template:\n    spec:\n      containers:\n        - image: erunpaas/erun-dind:28.1.1-dind\n          name: dind\n        - image: erunpaas/erun-devops:{{ .Chart.AppVersion }}\n          name: erun-devops\n"), 0o644); err != nil {
		t.Fatalf("write deployment template: %v", err)
	}

	images, err := findLiteralDockerImagesInChart(chartPath)
	if err != nil {
		t.Fatalf("findLiteralDockerImagesInChart failed: %v", err)
	}

	if len(images) != 1 || images[0] != "erunpaas/erun-dind:28.1.1-dind" {
		t.Fatalf("unexpected images: %+v", images)
	}
}

func TestResolveKubernetesDeployContextDetectsDockerComponentDir(t *testing.T) {
	projectRoot := t.TempDir()
	chartPath := createHelmChartFixture(t, projectRoot, "erun-devops")
	workdir := filepath.Join(projectRoot, "erun-devops", "docker", "erun-devops")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatalf("mkdir docker dir: %v", err)
	}

	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(previousDir) })
	if err := os.Chdir(workdir); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}

	result, err := ResolveKubernetesDeployContext()
	if err != nil {
		t.Fatalf("ResolveKubernetesDeployContext failed: %v", err)
	}

	resolvedChartPath, err := filepath.EvalSymlinks(chartPath)
	if err != nil {
		t.Fatalf("EvalSymlinks(chartPath) failed: %v", err)
	}
	if result.ComponentName != "erun-devops" || result.ChartPath != resolvedChartPath {
		t.Fatalf("unexpected deploy context: %+v", result)
	}
}

func TestResolveKubernetesDeployContextDetectsK8sComponentDir(t *testing.T) {
	projectRoot := t.TempDir()
	chartPath := createHelmChartFixture(t, projectRoot, "erun-devops")

	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(previousDir) })
	if err := os.Chdir(chartPath); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}

	result, err := ResolveKubernetesDeployContext()
	if err != nil {
		t.Fatalf("ResolveKubernetesDeployContext failed: %v", err)
	}

	resolvedChartPath, err := filepath.EvalSymlinks(chartPath)
	if err != nil {
		t.Fatalf("EvalSymlinks(chartPath) failed: %v", err)
	}
	if result.ComponentName != "erun-devops" || result.ChartPath != resolvedChartPath {
		t.Fatalf("unexpected deploy context: %+v", result)
	}
}

func TestPrepareHelmChartForDeployOverridesVersionAndAppVersion(t *testing.T) {
	projectRoot := t.TempDir()
	chartPath := createHelmChartFixture(t, projectRoot, "erun-devops")

	preparedChartPath, cleanup, err := prepareHelmChartForDeploy(chartPath, "1.0.0-snapshot-20260406123000")
	if err != nil {
		t.Fatalf("prepareHelmChartForDeploy failed: %v", err)
	}
	defer cleanup()

	data, err := os.ReadFile(filepath.Join(preparedChartPath, "Chart.yaml"))
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}

	var chart struct {
		Version    string `yaml:"version"`
		AppVersion string `yaml:"appVersion"`
	}
	if err := yaml.Unmarshal(data, &chart); err != nil {
		t.Fatalf("yaml.Unmarshal failed: %v", err)
	}

	if chart.Version != "1.0.0-snapshot-20260406123000" {
		t.Fatalf("unexpected chart version: %+v", chart)
	}
	if chart.AppVersion != "1.0.0-snapshot-20260406123000" {
		t.Fatalf("unexpected chart appVersion: %+v", chart)
	}
}

func TestResolveOpenRuntimeDeploySpecUsesTenantSpecificComponentBeforeSharedDefault(t *testing.T) {
	projectRoot := t.TempDir()
	chartPath := createComponentHelmChartFixture(t, projectRoot, "frs-devops")
	workdir := filepath.Join(projectRoot, "frs-devops", "docker", "frs-devops")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatalf("mkdir docker dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "frs-devops", "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write module VERSION: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "VERSION"), []byte("1.1.0\n"), 0o644); err != nil {
		t.Fatalf("write local VERSION: %v", err)
	}
	if err := SaveProjectConfig(projectRoot, ProjectConfig{ContainerRegistry: "erunpaas"}); err != nil {
		t.Fatalf("save project config: %v", err)
	}

	spec, err := ResolveOpenRuntimeDeploySpec(ConfigStore{}, FindProjectRoot, ResolveDockerBuildContext, ResolveKubernetesDeployContext, nil, OpenResult{
		Tenant:      "frs",
		Environment: DefaultEnvironment,
		RepoPath:    projectRoot,
		EnvConfig:   EnvConfig{KubernetesContext: "cluster-local"},
	})
	if err != nil {
		t.Fatalf("ResolveOpenRuntimeDeploySpec failed: %v", err)
	}
	if spec.DeployContext.ComponentName != "frs-devops" {
		t.Fatalf("expected tenant-specific runtime component, got %+v", spec.DeployContext)
	}
	if spec.Deploy.ReleaseName != "frs-devops" {
		t.Fatalf("expected tenant-specific runtime release, got %+v", spec.Deploy)
	}
	if spec.Deploy.ChartPath != chartPath {
		t.Fatalf("expected tenant-specific chart path, got %q", spec.Deploy.ChartPath)
	}
}

func TestResolveOpenRuntimeDeploySpecFallsBackToEmbeddedDefaultChart(t *testing.T) {
	projectRoot := t.TempDir()

	spec, err := ResolveOpenRuntimeDeploySpec(ConfigStore{}, FindProjectRoot, ResolveDockerBuildContext, ResolveKubernetesDeployContext, nil, OpenResult{
		Tenant:      "frs",
		Environment: "dev",
		RepoPath:    projectRoot,
		EnvConfig:   EnvConfig{KubernetesContext: "cluster-dev"},
	})
	if err != nil {
		t.Fatalf("ResolveOpenRuntimeDeploySpec failed: %v", err)
	}
	if spec.DeployContext.ComponentName != DevopsComponentName {
		t.Fatalf("unexpected default runtime component: %+v", spec.DeployContext)
	}
	if spec.Deploy.ReleaseName != "frs-devops" {
		t.Fatalf("expected tenant runtime release for embedded chart, got %+v", spec.Deploy)
	}
	if len(spec.Builds) != 0 {
		t.Fatalf("expected no local builds for embedded default chart, got %+v", spec.Builds)
	}
	if !strings.Contains(spec.Deploy.ChartPath, "erun-default-devops-chart-") {
		t.Fatalf("expected embedded chart path, got %q", spec.Deploy.ChartPath)
	}
	if got := filepath.Base(spec.Deploy.ValuesFilePath); got != "values.dev.yaml" {
		t.Fatalf("expected values.dev.yaml fallback, got %q", got)
	}
}

func TestCheckKubernetesDeploymentReturnsFalseWhenRuntimeRepoPathDiffers(t *testing.T) {
	kubectlDir := t.TempDir()
	kubectlPath := filepath.Join(kubectlDir, "kubectl")
	if err := os.WriteFile(kubectlPath, []byte(`#!/bin/sh
if [ "$1" = "--context" ]; then shift 2; fi
if [ "$1" = "--namespace" ]; then shift 2; fi
if [ "$1" = "get" ] && [ "$2" = "deployment" ] && [ "$3" = "frs-devops" ] && [ "$4" = "-o" ] && [ "$5" = "name" ]; then
  echo deployment/frs-devops
  exit 0
fi
if [ "$1" = "get" ] && [ "$2" = "deployment" ] && [ "$3" = "frs-devops" ] && [ "$4" = "-o" ] && [ "$5" = "json" ]; then
  cat <<'EOF'
{"spec":{"template":{"spec":{"containers":[{"name":"erun-devops","env":[{"name":"ERUN_REPO_PATH","value":"/home/erun/git/erun"}]}]}}}}
EOF
  exit 0
fi
echo "unexpected kubectl invocation: $@" >&2
exit 1
`), 0o755); err != nil {
		t.Fatalf("write kubectl stub: %v", err)
	}
	t.Setenv("PATH", kubectlDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	deployed, err := CheckKubernetesDeployment(KubernetesDeploymentCheckParams{
		Name:              "frs-devops",
		Namespace:         "frs-local",
		KubernetesContext: "cluster-local",
		ExpectedRepoPath:  "/home/erun/git/frs",
	})
	if err != nil {
		t.Fatalf("CheckKubernetesDeployment failed: %v", err)
	}
	if deployed {
		t.Fatalf("expected repo-path mismatch to require redeploy")
	}
}

func createHelmChartFixture(t *testing.T, projectRoot, componentName string) string {
	t.Helper()

	chartPath := filepath.Join(projectRoot, "erun-devops", "k8s", componentName)
	if err := os.MkdirAll(chartPath, 0o755); err != nil {
		t.Fatalf("mkdir chart dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(chartPath, "Chart.yaml"), []byte("apiVersion: v2\nname: "+componentName+"\nversion: 1.0.0\nappVersion: 1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write Chart.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(chartPath, "values.local.yaml"), nil, 0o644); err != nil {
		t.Fatalf("write values.local.yaml: %v", err)
	}
	return chartPath
}

func createComponentHelmChartFixture(t *testing.T, projectRoot, componentName string) string {
	t.Helper()

	componentRoot := filepath.Join(projectRoot, componentName)
	chartPath := filepath.Join(componentRoot, "k8s", componentName)
	if err := os.MkdirAll(chartPath, 0o755); err != nil {
		t.Fatalf("mkdir chart dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(chartPath, "Chart.yaml"), []byte("apiVersion: v2\nname: "+componentName+"\nversion: 1.0.0\nappVersion: 1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write Chart.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(chartPath, "values.local.yaml"), nil, 0o644); err != nil {
		t.Fatalf("write values.local.yaml: %v", err)
	}
	return chartPath
}

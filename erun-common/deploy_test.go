package eruncommon

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

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

func TestResolveCurrentKubernetesDeployContextsUsesProjectRootDevopsModule(t *testing.T) {
	projectRoot := t.TempDir()
	moduleRoot := filepath.Join(projectRoot, "tenant-a-devops")
	chartA := filepath.Join(moduleRoot, "k8s", "tenant-a-devops")
	chartB := filepath.Join(moduleRoot, "k8s", "erun-dind")
	for _, chartPath := range []string{chartA, chartB} {
		if err := os.MkdirAll(chartPath, 0o755); err != nil {
			t.Fatalf("mkdir chart dir: %v", err)
		}
		componentName := filepath.Base(chartPath)
		if err := os.WriteFile(filepath.Join(chartPath, "Chart.yaml"), []byte("apiVersion: v2\nname: "+componentName+"\nversion: 1.0.0\nappVersion: 1.0.0\n"), 0o644); err != nil {
			t.Fatalf("write Chart.yaml: %v", err)
		}
		if err := os.WriteFile(filepath.Join(chartPath, "values.local.yaml"), nil, 0o644); err != nil {
			t.Fatalf("write values.local.yaml: %v", err)
		}
	}

	deployContexts, err := ResolveCurrentKubernetesDeployContexts(
		func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		func() (KubernetesDeployContext, error) {
			return KubernetesDeployContext{Dir: projectRoot}, nil
		},
		"",
	)
	if err != nil {
		t.Fatalf("ResolveCurrentKubernetesDeployContexts failed: %v", err)
	}
	if len(deployContexts) != 2 {
		t.Fatalf("unexpected deploy contexts: %+v", deployContexts)
	}
	if deployContexts[0].ComponentName != "erun-dind" || deployContexts[0].ChartPath != chartB {
		t.Fatalf("unexpected first deploy context: %+v", deployContexts[0])
	}
	if deployContexts[1].ComponentName != "tenant-a-devops" || deployContexts[1].ChartPath != chartA {
		t.Fatalf("unexpected second deploy context: %+v", deployContexts[1])
	}
}

func TestResolveDeployTargetUsesCurrentDirectoryTenantWhenTenantProjectRootDiffers(t *testing.T) {
	projectRoot := filepath.Join(t.TempDir(), "tenant-a")
	defaultRepo := filepath.Join(t.TempDir(), "tenant-b")
	for _, dir := range []string{projectRoot, defaultRepo} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir repo: %v", err)
		}
	}

	store := openStore{
		toolConfig: ERunConfig{DefaultTenant: "tenant-b"},
		tenantConfigs: map[string]TenantConfig{
			"tenant-a": {
				Name:               "tenant-a",
				ProjectRoot:        "/home/erun/git/tenant-a",
				DefaultEnvironment: "dev",
				Remote:             true,
			},
			"tenant-b": {
				Name:               "tenant-b",
				ProjectRoot:        defaultRepo,
				DefaultEnvironment: "prod",
			},
		},
		envConfigs: map[string]EnvConfig{
			"tenant-a/dev": {
				Name:              "dev",
				RepoPath:          projectRoot,
				KubernetesContext: "cluster-a",
			},
			"tenant-b/prod": {
				Name:              "prod",
				RepoPath:          defaultRepo,
				KubernetesContext: "cluster-b",
			},
		},
	}

	result, err := resolveDeployTarget(
		store,
		func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		nil,
		nil,
		nil,
		DeployTarget{},
	)
	if err != nil {
		t.Fatalf("resolveDeployTarget failed: %v", err)
	}
	if result.Tenant != "tenant-a" || result.Environment != "dev" {
		t.Fatalf("expected current directory tenant target, got %+v", result)
	}
	if result.RepoPath != projectRoot {
		t.Fatalf("expected repo path %q, got %+v", projectRoot, result)
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

func TestRuntimeChartsInstallBinfmtForMultiArchBuilds(t *testing.T) {
	paths := []string{
		filepath.Join("..", "erun-devops", "k8s", "erun-devops", "templates", "service.yaml"),
		filepath.Join("assets", "default-devops-chart", "templates", "service.yaml"),
	}

	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %q: %v", path, err)
		}
		content := string(data)
		if !strings.Contains(content, "tonistiigi/binfmt:qemu-v10.0.4-56") {
			t.Fatalf("expected %q to install binfmt, got:\n%s", path, content)
		}
		if !strings.Contains(content, "name: install-binfmt") {
			t.Fatalf("expected %q to define install-binfmt init container, got:\n%s", path, content)
		}
		if !strings.Contains(content, "amd64,arm64") {
			t.Fatalf("expected %q to install amd64 and arm64 binfmt support, got:\n%s", path, content)
		}
	}
}

func TestRuntimeChartsExposeMCPAndSSHPorts(t *testing.T) {
	paths := []string{
		filepath.Join("..", "erun-devops", "k8s", "erun-devops", "templates", "service.yaml"),
		filepath.Join("assets", "default-devops-chart", "templates", "service.yaml"),
	}

	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %q: %v", path, err)
		}
		content := string(data)
		for _, want := range []string{
			"containerPort: 17000",
			"name: mcp",
			"containerPort: 2222",
			"name: ssh",
		} {
			if !strings.Contains(content, want) {
				t.Fatalf("expected %q to contain %q, got:\n%s", path, want, content)
			}
		}
	}
}

func TestRuntimeChartsGrantLocalRuntimeCrossNamespaceBootstrapAccess(t *testing.T) {
	paths := []string{
		filepath.Join("..", "erun-devops", "k8s", "erun-devops", "templates", "service.yaml"),
		filepath.Join("assets", "default-devops-chart", "templates", "service.yaml"),
	}

	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %q: %v", path, err)
		}
		content := string(data)
		for _, want := range []string{
			`{{- if eq (required "environment is required" .Values.environment) "local" }}`,
			"kind: ClusterRole",
			"resources:",
			"      - namespaces",
			"      - list",
			"      - create",
			"kind: ClusterRoleBinding",
		} {
			if !strings.Contains(content, want) {
				t.Fatalf("expected %q to include %q, got:\n%s", path, want, content)
			}
		}
	}
}

func TestNewHelmDeploySpecCanonicalizesWorktreeHostPath(t *testing.T) {
	projectRoot := t.TempDir()
	repoRoot := filepath.Join(projectRoot, "repo")
	if err := os.MkdirAll(repoRoot, 0o755); err != nil {
		t.Fatalf("mkdir repo root: %v", err)
	}

	linkParent := filepath.Join(projectRoot, "links")
	if err := os.MkdirAll(linkParent, 0o755); err != nil {
		t.Fatalf("mkdir link parent: %v", err)
	}
	linkPath := filepath.Join(linkParent, "repo-link")
	if err := os.Symlink(repoRoot, linkPath); err != nil {
		t.Fatalf("symlink repo root: %v", err)
	}

	chartPath := createHelmChartFixture(t, projectRoot, "erun-devops")
	spec, err := newHelmDeploySpec(
		OpenResult{
			Tenant:      "tenant-a",
			Environment: DefaultEnvironment,
			RepoPath:    linkPath,
			EnvConfig:   EnvConfig{KubernetesContext: "cluster-local"},
		},
		KubernetesDeployContext{
			ComponentName: "erun-devops",
			ChartPath:     chartPath,
		},
		"",
	)
	if err != nil {
		t.Fatalf("newHelmDeploySpec failed: %v", err)
	}
	resolvedRepoRoot, err := filepath.EvalSymlinks(repoRoot)
	if err != nil {
		t.Fatalf("EvalSymlinks(repoRoot) failed: %v", err)
	}
	if spec.WorktreeHostPath != resolvedRepoRoot {
		t.Fatalf("expected canonical worktree host path %q, got %q", resolvedRepoRoot, spec.WorktreeHostPath)
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

func TestResolveOpenRuntimeDeploySpecSkipsLocalBuildsWhenSnapshotDisabled(t *testing.T) {
	projectRoot := t.TempDir()
	createComponentHelmChartFixture(t, projectRoot, "frs-devops")
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
	if err := SaveProjectConfig(projectRoot, ProjectConfig{ContainerRegistry: "erunpaas"}); err != nil {
		t.Fatalf("save project config: %v", err)
	}

	snapshot := false
	spec, err := ResolveOpenRuntimeDeploySpec(ConfigStore{}, FindProjectRoot, ResolveDockerBuildContext, ResolveKubernetesDeployContext, nil, OpenResult{
		Tenant:      "frs",
		Environment: DefaultEnvironment,
		RepoPath:    projectRoot,
		EnvConfig: EnvConfig{
			KubernetesContext: "cluster-local",
			Snapshot:          &snapshot,
		},
	})
	if err != nil {
		t.Fatalf("ResolveOpenRuntimeDeploySpec failed: %v", err)
	}
	if len(spec.Builds) != 0 {
		t.Fatalf("expected local runtime builds to be skipped, got %+v", spec.Builds)
	}
	if spec.Deploy.Version != "" {
		t.Fatalf("expected deploy version override to remain empty, got %+v", spec.Deploy)
	}
}

func TestResolveOpenRuntimeDeploySpecIgnoresTenantSnapshotSetting(t *testing.T) {
	projectRoot := t.TempDir()
	createComponentHelmChartFixture(t, projectRoot, "frs-devops")
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
	if err := SaveProjectConfig(projectRoot, ProjectConfig{ContainerRegistry: "erunpaas"}); err != nil {
		t.Fatalf("save project config: %v", err)
	}

	snapshot := false
	spec, err := ResolveOpenRuntimeDeploySpec(ConfigStore{}, FindProjectRoot, ResolveDockerBuildContext, ResolveKubernetesDeployContext, nil, OpenResult{
		Tenant:      "frs",
		Environment: DefaultEnvironment,
		RepoPath:    projectRoot,
		TenantConfig: TenantConfig{
			Snapshot: &snapshot,
		},
		EnvConfig: EnvConfig{
			KubernetesContext: "cluster-local",
		},
	})
	if err != nil {
		t.Fatalf("ResolveOpenRuntimeDeploySpec failed: %v", err)
	}
	if len(spec.Builds) == 0 {
		t.Fatalf("expected tenant snapshot setting to be ignored, got %+v", spec.Builds)
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

func TestRemoteBootstrapRuntimeUsesCanonicalImageWithTenantRelease(t *testing.T) {
	spec, err := resolveDefaultDevopsDeploySpecWithImage(OpenResult{
		Tenant:      "test",
		Environment: "env",
		RepoPath:    "/home/erun/git/test",
		EnvConfig: EnvConfig{
			KubernetesContext: "cluster-env",
			Remote:            true,
			RuntimeVersion:    "1.0.50",
		},
	}, DevopsComponentName)
	if err != nil {
		t.Fatalf("resolveDefaultDevopsDeploySpecWithImage failed: %v", err)
	}
	if spec.Deploy.ReleaseName != "test-devops" {
		t.Fatalf("expected tenant release name, got %+v", spec.Deploy)
	}

	templatePath := filepath.Join(spec.Deploy.ChartPath, "templates", "service.yaml")
	data, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatalf("read rendered chart template: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "name: test-devops") {
		t.Fatalf("expected tenant deployment identity in chart, got:\n%s", content)
	}
	if !strings.Contains(content, "image: erunpaas/erun-devops:{{ .Chart.AppVersion }}") {
		t.Fatalf("expected canonical runtime image in bootstrap chart, got:\n%s", content)
	}
	if strings.Contains(content, "image: erunpaas/test-devops:") {
		t.Fatalf("bootstrap chart must not require tenant image before it exists, got:\n%s", content)
	}
}

func TestResolveOpenRuntimeDeploySpecUsesRemoteEnvRuntimeVersionForEmbeddedChart(t *testing.T) {
	spec, err := ResolveOpenRuntimeDeploySpec(ConfigStore{}, FindProjectRoot, ResolveDockerBuildContext, ResolveKubernetesDeployContext, nil, OpenResult{
		Tenant:      "frs",
		Environment: "remote",
		RepoPath:    "/home/erun/git/frs",
		TenantConfig: TenantConfig{
			Name: "frs",
		},
		EnvConfig: EnvConfig{
			KubernetesContext: "cluster-remote",
			RepoPath:          "/home/erun/git/frs",
			Remote:            true,
			RuntimeVersion:    "1.0.31",
		},
	})
	if err != nil {
		t.Fatalf("ResolveOpenRuntimeDeploySpec failed: %v", err)
	}
	if spec.Deploy.Version != "1.0.31" {
		t.Fatalf("expected embedded chart deploy version override, got %+v", spec.Deploy)
	}
	if len(spec.Builds) != 0 {
		t.Fatalf("expected no local builds for remote embedded chart, got %+v", spec.Builds)
	}
	data, err := os.ReadFile(filepath.Join(spec.Deploy.ChartPath, "templates", "service.yaml"))
	if err != nil {
		t.Fatalf("read rendered chart template: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "image: erunpaas/erun-devops:{{ .Chart.AppVersion }}") {
		t.Fatalf("expected canonical runtime image for remote deploy, got:\n%s", content)
	}
	if strings.Contains(content, "image: erunpaas/frs-devops:") {
		t.Fatalf("remote deploy must not require tenant image before it exists, got:\n%s", content)
	}
}

func TestResolveDeploySpecForContextUsesSelectedKubernetesContextForLocalEnvironment(t *testing.T) {
	projectRoot := t.TempDir()
	chartPath := createHelmChartFixture(t, projectRoot, "erun-devops")

	spec, err := resolveDeploySpecForContext(
		openStore{
			resolveDeployKubernetesContext: func(environment, configured string) string {
				if environment != DefaultEnvironment || configured != "cluster-configured" {
					t.Fatalf("unexpected deploy context resolution inputs: environment=%q configured=%q", environment, configured)
				}
				return "cluster-selected"
			},
		},
		nil,
		nil,
		nil,
		nil,
		OpenResult{
			Tenant:      "tenant-a",
			Environment: DefaultEnvironment,
			RepoPath:    projectRoot,
			EnvConfig: EnvConfig{
				KubernetesContext: "cluster-configured",
			},
		},
		KubernetesDeployContext{
			ComponentName: "erun-devops",
			ChartPath:     chartPath,
		},
		"",
		false,
	)
	if err != nil {
		t.Fatalf("resolveDeploySpecForContext failed: %v", err)
	}
	if spec.Deploy.KubernetesContext != "cluster-selected" {
		t.Fatalf("expected selected kubernetes context, got %+v", spec.Deploy)
	}
}

func TestResolveDeployKubernetesContextKeepsConfiguredContextForLocalEnvironment(t *testing.T) {
	called := false
	got := resolveDeployKubernetesContext(DefaultEnvironment, "cluster-configured", func() (string, error) {
		called = true
		return "cluster-current", nil
	})
	if got != "cluster-configured" {
		t.Fatalf("resolveDeployKubernetesContext returned %q, want %q", got, "cluster-configured")
	}
	if called {
		t.Fatal("did not expect current-context lookup when deploy context is already configured")
	}
}

func TestResolveDeployKubernetesContextFallsBackToCurrentContextWhenUnconfigured(t *testing.T) {
	got := resolveDeployKubernetesContext(DefaultEnvironment, "", func() (string, error) {
		return "cluster-current", nil
	})
	if got != "cluster-current" {
		t.Fatalf("resolveDeployKubernetesContext returned %q, want %q", got, "cluster-current")
	}
}

func TestResolveDeploySpecForContextPublishesLocalRuntimeBuildsAsMultiPlatform(t *testing.T) {
	projectRoot := t.TempDir()
	componentRoot := filepath.Join(projectRoot, "tenant-a-devops")
	chartPath := createComponentHelmChartFixture(t, projectRoot, "tenant-a-devops")
	templatesPath := filepath.Join(chartPath, "templates")
	if err := os.MkdirAll(templatesPath, 0o755); err != nil {
		t.Fatalf("mkdir templates dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(templatesPath, "deployment.yaml"), []byte("apiVersion: apps/v1\nkind: Deployment\nspec:\n  template:\n    spec:\n      containers:\n        - name: tenant-a-devops\n          image: erunpaas/tenant-a-devops:{{ .Chart.AppVersion }}\n        - name: erun-dind\n          image: erunpaas/erun-dind:28.1.1\n"), 0o644); err != nil {
		t.Fatalf("write deployment template: %v", err)
	}

	runtimeWorkdir := filepath.Join(componentRoot, "docker", "tenant-a-devops")
	if err := os.MkdirAll(runtimeWorkdir, 0o755); err != nil {
		t.Fatalf("mkdir runtime docker dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(runtimeWorkdir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatalf("write runtime Dockerfile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(componentRoot, "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write runtime VERSION: %v", err)
	}

	dindWorkdir := filepath.Join(componentRoot, "docker", "erun-dind")
	if err := os.MkdirAll(dindWorkdir, 0o755); err != nil {
		t.Fatalf("mkdir dind docker dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dindWorkdir, "Dockerfile"), []byte("FROM docker:28.1.1-dind\n"), 0o644); err != nil {
		t.Fatalf("write dind Dockerfile: %v", err)
	}
	projectConfig := ProjectConfig{}
	projectConfig.SetContainerRegistryForEnvironment(DefaultEnvironment, "erunpaas")
	projectConfig.Environments[DefaultEnvironment] = ProjectEnvironmentConfig{
		ContainerRegistry: "erunpaas",
		Docker: ProjectDockerConfig{
			SkipIfExists: []string{"erunpaas/erun-dind"},
		},
	}
	if err := SaveProjectConfig(projectRoot, projectConfig); err != nil {
		t.Fatalf("save project config: %v", err)
	}

	spec, err := resolveDeploySpecForContext(
		ConfigStore{},
		func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		func() (DockerBuildContext, error) {
			return DockerBuildContext{
				Dir:            runtimeWorkdir,
				DockerfilePath: filepath.Join(runtimeWorkdir, "Dockerfile"),
			}, nil
		},
		nil,
		func() time.Time {
			return time.Date(2026, time.April, 21, 18, 24, 44, 0, time.UTC)
		},
		OpenResult{
			Tenant:      "tenant-a",
			Environment: DefaultEnvironment,
			RepoPath:    projectRoot,
			TenantConfig: TenantConfig{
				Name:        "tenant-a",
				ProjectRoot: projectRoot,
			},
			EnvConfig: EnvConfig{
				Name:              DefaultEnvironment,
				RepoPath:          projectRoot,
				KubernetesContext: "erun",
			},
		},
		KubernetesDeployContext{
			Dir:           runtimeWorkdir,
			ComponentName: "tenant-a-devops",
			ChartPath:     chartPath,
		},
		"",
		true,
	)
	if err != nil {
		t.Fatalf("resolveDeploySpecForContext failed: %v", err)
	}

	if len(spec.Builds) != 2 {
		t.Fatalf("unexpected builds: %+v", spec.Builds)
	}
	for _, build := range spec.Builds {
		if !build.Push {
			t.Fatalf("expected deploy build to push via buildx, got %+v", build)
		}
		if !reflect.DeepEqual(build.Platforms, []string{"linux/amd64", "linux/arm64"}) {
			t.Fatalf("expected deploy build to target both platforms, got %+v", build)
		}
	}
	for _, build := range spec.Builds {
		if build.Image.Tag == "erunpaas/erun-dind:28.1.1" && !build.SkipIfExists {
			t.Fatalf("expected dind dependency build to be skippable, got %+v", build)
		}
	}
}

func TestRunDeploySpecSkipsSeparatePushWhenBuildAlreadyPushes(t *testing.T) {
	buildCalls := 0
	pushCalls := 0
	ctx := Context{
		Logger: NewLoggerWithWriters(2, io.Discard, io.Discard),
		Stdout: new(bytes.Buffer),
		Stderr: new(bytes.Buffer),
	}
	err := RunDeploySpec(
		ctx,
		DeploySpec{
			Builds: []DockerBuildSpec{{
				ContextDir:     "/tmp/runtime",
				DockerfilePath: "/tmp/runtime/Dockerfile",
				Image: DockerImageReference{
					Tag: "erunpaas/tenant-a-devops:1.0.0",
				},
				Platforms: []string{"linux/amd64", "linux/arm64"},
				Push:      true,
			}},
			Deploy: HelmDeploySpec{
				ReleaseName:       "tenant-a-devops",
				ChartPath:         "/tmp/chart",
				ValuesFilePath:    "/tmp/chart/values.local.yaml",
				Namespace:         "tenant-a-local",
				KubernetesContext: "erun",
				Timeout:           DefaultHelmDeploymentTimeout,
			},
		},
		func(buildInput DockerBuildSpec, stdout, stderr io.Writer) error {
			buildCalls++
			return nil
		},
		func(ctx Context, pushInput DockerPushSpec) error {
			pushCalls++
			return nil
		},
		func(params HelmDeployParams) error {
			return nil
		},
	)
	if err != nil {
		t.Fatalf("RunDeploySpec failed: %v", err)
	}
	if buildCalls != 1 {
		t.Fatalf("expected one build call, got %d", buildCalls)
	}
	if pushCalls != 0 {
		t.Fatalf("expected no separate push call, got %d", pushCalls)
	}
}

func TestDeployHelmChartPassesSSHDEnabledValue(t *testing.T) {
	helmDir := t.TempDir()
	argsPath := filepath.Join(helmDir, "helm-args.txt")
	helmPath := filepath.Join(helmDir, "helm")
	if err := os.WriteFile(helmPath, []byte(`#!/bin/sh
printf '%s
' "$@" > "$ERUN_HELM_ARGS_FILE"
`), 0o755); err != nil {
		t.Fatalf("write helm stub: %v", err)
	}
	t.Setenv("ERUN_HELM_ARGS_FILE", argsPath)
	t.Setenv("PATH", helmDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	chartPath := createHelmChartFixture(t, t.TempDir(), "erun-devops")
	if err := DeployHelmChart(HelmDeployParams{
		ReleaseName:       "erun-devops",
		ChartPath:         chartPath,
		ValuesFilePath:    filepath.Join(chartPath, "values.local.yaml"),
		Tenant:            "erun",
		Environment:       "remote",
		Namespace:         "erun-remote",
		KubernetesContext: "rancher-desktop",
		WorktreeStorage:   WorktreeStoragePVC,
		WorktreeRepoName:  "erun",
		WorktreeHostPath:  "/home/erun/git/erun",
		SSHDEnabled:       true,
		Timeout:           DefaultHelmDeploymentTimeout,
	}); err != nil {
		t.Fatalf("DeployHelmChart failed: %v", err)
	}

	data, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read helm args: %v", err)
	}
	args := string(data)
	if !strings.Contains(args, "--set\nsshdEnabled=true\n") {
		t.Fatalf("expected helm args to include sshdEnabled=true, got:\n%s", args)
	}
}

func TestDeployHelmChartReturnsPendingOperationError(t *testing.T) {
	helmDir := t.TempDir()
	helmPath := filepath.Join(helmDir, "helm")
	if err := os.WriteFile(helmPath, []byte(`#!/bin/sh
echo "Error: UPGRADE FAILED: another operation (install/upgrade/rollback) is in progress" >&2
exit 1
`), 0o755); err != nil {
		t.Fatalf("write helm stub: %v", err)
	}
	t.Setenv("PATH", helmDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	chartPath := createHelmChartFixture(t, t.TempDir(), "erun-devops")
	stderr := new(bytes.Buffer)
	err := DeployHelmChart(HelmDeployParams{
		ReleaseName:       "erun-devops",
		ChartPath:         chartPath,
		ValuesFilePath:    filepath.Join(chartPath, "values.local.yaml"),
		Tenant:            "erun",
		Environment:       "local",
		Namespace:         "erun-local",
		KubernetesContext: "rancher-desktop",
		WorktreeStorage:   WorktreeStorageHost,
		WorktreeRepoName:  "erun",
		WorktreeHostPath:  "/home/erun/git/erun",
		Timeout:           DefaultHelmDeploymentTimeout,
		Stderr:            stderr,
	})

	var pending *HelmReleasePendingOperationError
	if !errors.As(err, &pending) {
		t.Fatalf("expected pending operation error, got %T: %v", err, err)
	}
	if pending.ReleaseName != "erun-devops" || pending.Namespace != "erun-local" || pending.KubernetesContext != "rancher-desktop" {
		t.Fatalf("unexpected pending operation details: %+v", pending)
	}
	if !strings.Contains(stderr.String(), "another operation") {
		t.Fatalf("expected helm stderr to remain streamed, got %q", stderr.String())
	}
	wantCommand := "kubectl --context rancher-desktop --namespace erun-local delete 'secrets,configmaps' -l 'owner=helm,name=erun-devops,status in (pending-install,pending-upgrade,pending-rollback)' --ignore-not-found"
	if pending.RecoveryCommand() != wantCommand {
		t.Fatalf("unexpected recovery command %q, want %q", pending.RecoveryCommand(), wantCommand)
	}
}

func TestClearHelmReleasePendingOperationDeletesOnlyPendingMetadata(t *testing.T) {
	kubectlDir := t.TempDir()
	argsPath := filepath.Join(kubectlDir, "kubectl-args.txt")
	kubectlPath := filepath.Join(kubectlDir, "kubectl")
	if err := os.WriteFile(kubectlPath, []byte(`#!/bin/sh
printf '%s
' "$@" > "$ERUN_KUBECTL_ARGS_FILE"
`), 0o755); err != nil {
		t.Fatalf("write kubectl stub: %v", err)
	}
	t.Setenv("ERUN_KUBECTL_ARGS_FILE", argsPath)
	t.Setenv("PATH", kubectlDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if err := ClearHelmReleasePendingOperation(HelmReleaseRecoveryParams{
		ReleaseName:       "erun-devops",
		Namespace:         "erun-local",
		KubernetesContext: "rancher-desktop",
	}); err != nil {
		t.Fatalf("ClearHelmReleasePendingOperation failed: %v", err)
	}

	data, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read kubectl args: %v", err)
	}
	args := string(data)
	if strings.Contains(args, "uninstall\n") {
		t.Fatalf("did not expect helm uninstall args:\n%s", args)
	}
	want := "--context\nrancher-desktop\n--namespace\nerun-local\ndelete\nsecrets,configmaps\n-l\nowner=helm,name=erun-devops,status in (pending-install,pending-upgrade,pending-rollback)\n--ignore-not-found\n"
	if args != want {
		t.Fatalf("unexpected kubectl recovery args:\n%s", args)
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

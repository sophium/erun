package eruncommon

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
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
	writeDeployChartFixture(t, chartA)
	writeDeployChartFixture(t, chartB)

	deployContexts, err := ResolveCurrentKubernetesDeployContexts(
		func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		func() (KubernetesDeployContext, error) {
			return KubernetesDeployContext{Dir: projectRoot}, nil
		},
		"",
	)
	requireNoError(t, err, "ResolveCurrentKubernetesDeployContexts failed")
	requireDeployContexts(t, deployContexts, chartA, chartB)
}

func writeDeployChartFixture(t *testing.T, chartPath string) {
	t.Helper()
	requireNoError(t, os.MkdirAll(chartPath, 0o755), "mkdir chart dir")
	componentName := filepath.Base(chartPath)
	chart := []byte("apiVersion: v2\nname: " + componentName + "\nversion: 1.0.0\nappVersion: 1.0.0\n")
	requireNoError(t, os.WriteFile(filepath.Join(chartPath, "Chart.yaml"), chart, 0o644), "write Chart.yaml")
	requireNoError(t, os.WriteFile(filepath.Join(chartPath, "values.local.yaml"), nil, 0o644), "write values.local.yaml")
}

func requireDeployContexts(t *testing.T, deployContexts []KubernetesDeployContext, chartA, chartB string) {
	t.Helper()
	requireEqual(t, len(deployContexts), 2, "deploy context count")
	requireCondition(t, deployContexts[0].ComponentName == "erun-dind" && deployContexts[0].ChartPath == chartB, "unexpected first deploy context: %+v", deployContexts[0])
	requireCondition(t, deployContexts[1].ComponentName == "tenant-a-devops" && deployContexts[1].ChartPath == chartA, "unexpected second deploy context: %+v", deployContexts[1])
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

func TestRuntimeChartsExposeMCPAPIAndSSHPorts(t *testing.T) {
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
			`{{- $mcpPort := default 17000 .Values.mcpPort -}}`,
			`{{- $apiPort := default 17033 .Values.apiPort -}}`,
			`{{- $sshPort := default 17022 .Values.sshPort -}}`,
			`{{- $cloudContext := default dict .Values.cloudContext -}}`,
			`{{- $cloudContextName := default "" $cloudContext.name -}}`,
			`{{- $cloudProviderAlias := default "" $cloudContext.providerAlias -}}`,
			`{{- $cloudRegion := default "" $cloudContext.region -}}`,
			`{{- $cloudInstanceID := default "" $cloudContext.instanceId -}}`,
			"name: ERUN_CLOUD_CONTEXT_NAME",
			"name: ERUN_CLOUD_PROVIDER_ALIAS",
			"name: ERUN_CLOUD_REGION",
			"name: ERUN_CLOUD_INSTANCE_ID",
			"name: ERUN_MCP_PORT",
			"name: ERUN_API_PORT",
			"name: ERUN_SSHD_PORT",
			"containerPort: {{ $mcpPort }}",
			"name: mcp",
			"containerPort: {{ $apiPort }}",
			"name: api",
			"containerPort: {{ $sshPort }}",
			"name: ssh",
		} {
			if !strings.Contains(content, want) {
				t.Fatalf("expected %q to contain %q, got:\n%s", path, want, content)
			}
		}
		for _, forbidden := range []string{
			"ERUN_SSHD_TARGET_PORT",
			"sshTargetPort",
			"containerPort: 17023",
		} {
			if strings.Contains(content, forbidden) {
				t.Fatalf("expected %q not to expose SSHD target port marker %q, got:\n%s", path, forbidden, content)
			}
		}
	}
}

func TestRuntimeChartsUseNilSafeNestedValueDefaults(t *testing.T) {
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
			`{{- $idle := default dict .Values.idle -}}`,
			`{{- $idleTimeout := default "5m0s" $idle.timeout -}}`,
			`{{- $idleWorkingHours := default "08:00-20:00" $idle.workingHours -}}`,
			`{{- $idleTrafficBytes := default 0 $idle.trafficBytes -}}`,
			`{{- $runtime := default dict .Values.runtime -}}`,
			`{{- $runtimeResources := default dict $runtime.resources -}}`,
			`{{- $runtimeLimits := default dict $runtimeResources.limits -}}`,
			`{{- $runtimeRequests := default dict $runtimeResources.requests -}}`,
			`{{- $runtimeCPU := default "4" $runtimeLimits.cpu -}}`,
			`{{- $runtimeMemory := default "8916Mi" $runtimeLimits.memory -}}`,
			`{{- $runtimeRequestCPU := default "0.25" $runtimeRequests.cpu -}}`,
			`{{- $runtimeRequestMemory := default "1024Mi" $runtimeRequests.memory -}}`,
			`{{- $cloudContext := default dict .Values.cloudContext -}}`,
		} {
			if !strings.Contains(content, want) {
				t.Fatalf("expected %q to contain nil-safe default %q, got:\n%s", path, want, content)
			}
		}
		for _, forbidden := range []string{
			".Values.idle.timeout",
			".Values.runtime.resources.limits.cpu",
			".Values.runtime.resources.requests.cpu",
			".Values.cloudContext.name",
		} {
			if strings.Contains(content, forbidden) {
				t.Fatalf("expected %q not to contain direct nested lookup %q, got:\n%s", path, forbidden, content)
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

func TestHelmDeploySpecIncludesRuntimePodResourceLimits(t *testing.T) {
	spec := HelmDeploySpec{
		ReleaseName:     "erun-devops",
		ChartPath:       "/tmp/chart",
		ValuesFilePath:  "/tmp/chart/values.local.yaml",
		Tenant:          "erun",
		Environment:     "local",
		Namespace:       "erun-local",
		WorktreeStorage: WorktreeStorageHost,
		RuntimePod: RuntimePodResources{
			CPU:    "6",
			Memory: "12Gi",
		},
		Timeout: DefaultHelmDeploymentTimeout,
	}

	args := strings.Join(spec.command().Args, "\n")
	for _, want := range []string{
		"runtime.resources.limits.cpu=6",
		"runtime.resources.limits.memory=12Gi",
	} {
		if !strings.Contains(args, want) {
			t.Fatalf("expected Helm command to include %q, got:\n%s", want, args)
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

func TestNewHelmDeploySpecUsesResolvedEnvironmentPorts(t *testing.T) {
	projectRoot := t.TempDir()
	chartPath := createHelmChartFixture(t, projectRoot, "erun-devops")
	spec, err := newHelmDeploySpec(
		OpenResult{
			Tenant:      "tenant-a",
			Environment: DefaultEnvironment,
			RepoPath:    projectRoot,
			EnvConfig: EnvConfig{
				Name: DefaultEnvironment,
				SSHD: SSHDConfig{
					LocalPort: 62222,
				},
			},
			LocalPorts: EnvironmentLocalPorts{
				RangeStart: 17100,
				RangeEnd:   17199,
				MCP:        17100,
				API:        17133,
				SSH:        17122,
			},
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
	if spec.MCPPort != 17100 || spec.APIPort != 17133 || spec.SSHPort != 62222 {
		t.Fatalf("expected resolved ports to be preserved, got mcp=%d api=%d ssh=%d", spec.MCPPort, spec.APIPort, spec.SSHPort)
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

	tenantSnapshot := false
	envSnapshot := true
	spec, err := ResolveOpenRuntimeDeploySpec(ConfigStore{}, FindProjectRoot, ResolveDockerBuildContext, ResolveKubernetesDeployContext, nil, OpenResult{
		Tenant:      "frs",
		Environment: DefaultEnvironment,
		RepoPath:    projectRoot,
		TenantConfig: TenantConfig{
			Snapshot: &tenantSnapshot,
		},
		EnvConfig: EnvConfig{
			KubernetesContext: "cluster-local",
			Snapshot:          &envSnapshot,
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

func TestResolveDeploySpecForContextAddsCloudHostStopMetadata(t *testing.T) {
	projectRoot := t.TempDir()
	chartPath := createHelmChartFixture(t, projectRoot, "erun-devops")
	if err := os.WriteFile(filepath.Join(chartPath, "values.rihards-develop.yaml"), nil, 0o644); err != nil {
		t.Fatalf("write remote values file: %v", err)
	}

	spec, err := resolveDeploySpecForContext(
		openStore{
			toolConfig: ERunConfig{
				CloudContexts: []CloudContextConfig{{
					Name:               "erun-001-020362606330-eu-west-2",
					CloudProviderAlias: "team-cloud",
					Region:             "eu-west-2",
					InstanceID:         "i-073c3338f26fbb000",
					KubernetesContext:  "cluster-dev",
					Status:             CloudContextStatusRunning,
				}},
			},
		},
		nil,
		nil,
		nil,
		nil,
		OpenResult{
			Tenant:      "petios",
			Environment: "rihards-develop",
			RepoPath:    projectRoot,
			EnvConfig: EnvConfig{
				KubernetesContext:  "cluster-dev",
				CloudProviderAlias: "team-cloud",
				Remote:             true,
				ManagedCloud:       true,
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
	if !spec.Deploy.ManagedCloud || spec.Deploy.CloudContextName != "erun-001-020362606330-eu-west-2" || spec.Deploy.CloudProviderAlias != "team-cloud" || spec.Deploy.CloudRegion != "eu-west-2" || spec.Deploy.CloudInstanceID != "i-073c3338f26fbb000" {
		t.Fatalf("expected cloud host stop metadata, got %+v", spec.Deploy)
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
	writeRuntimeDeploymentTemplate(t, templatesPath)

	runtimeWorkdir := filepath.Join(componentRoot, "docker", "tenant-a-devops")
	writeDockerBuildFixture(t, runtimeWorkdir)
	writeVersionFileForTest(t, filepath.Join(componentRoot, "VERSION"), "1.0.0")

	dindWorkdir := filepath.Join(componentRoot, "docker", "erun-dind")
	requireNoError(t, os.MkdirAll(dindWorkdir, 0o755), "mkdir dind docker dir")
	requireNoError(t, os.WriteFile(filepath.Join(dindWorkdir, "Dockerfile"), []byte("FROM docker:28.1.1-dind\n"), 0o644), "write dind Dockerfile")
	projectConfig := ProjectConfig{}
	projectConfig.SetContainerRegistryForEnvironment(DefaultEnvironment, "erunpaas")
	projectConfig.Environments[DefaultEnvironment] = ProjectEnvironmentConfig{
		ContainerRegistry: "erunpaas",
		Docker: ProjectDockerConfig{
			SkipIfExists: []string{"erunpaas/erun-dind"},
		},
	}
	requireNoError(t, SaveProjectConfig(projectRoot, projectConfig), "save project config")

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
	requireNoError(t, err, "resolveDeploySpecForContext failed")

	requireMultiPlatformDeployBuilds(t, spec.Builds)
}

func writeRuntimeDeploymentTemplate(t *testing.T, templatesPath string) {
	t.Helper()
	requireNoError(t, os.MkdirAll(templatesPath, 0o755), "mkdir templates dir")
	template := []byte("apiVersion: apps/v1\nkind: Deployment\nspec:\n  template:\n    spec:\n      containers:\n        - name: tenant-a-devops\n          image: erunpaas/tenant-a-devops:{{ .Chart.AppVersion }}\n        - name: erun-dind\n          image: erunpaas/erun-dind:28.1.1\n")
	requireNoError(t, os.WriteFile(filepath.Join(templatesPath, "deployment.yaml"), template, 0o644), "write deployment template")
}

func requireMultiPlatformDeployBuilds(t *testing.T, builds []DockerBuildSpec) {
	t.Helper()
	requireEqual(t, len(builds), 2, "deploy build count")
	for _, build := range builds {
		requireMultiPlatformPushedBuild(t, build)
		if build.Image.Tag == "erunpaas/erun-dind:28.1.1" {
			requireCondition(t, build.SkipIfExists, "expected dind dependency build to be skippable, got %+v", build)
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
		ReleaseName:        "erun-devops",
		ChartPath:          chartPath,
		ValuesFilePath:     filepath.Join(chartPath, "values.local.yaml"),
		Tenant:             "erun",
		Environment:        "remote",
		Namespace:          "erun-remote",
		KubernetesContext:  "rancher-desktop",
		WorktreeStorage:    WorktreeStoragePVC,
		WorktreeRepoName:   "erun",
		WorktreeHostPath:   "/home/erun/git/erun",
		SSHDEnabled:        true,
		MCPPort:            17100,
		APIPort:            17133,
		SSHPort:            17122,
		ManagedCloud:       true,
		CloudContextName:   "erun-001-020362606330-eu-west-2",
		CloudProviderAlias: "team-cloud",
		CloudRegion:        "eu-west-2",
		CloudInstanceID:    "i-073c3338f26fbb000",
		Timeout:            DefaultHelmDeploymentTimeout,
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
	if !strings.Contains(args, "--set\nmcpPort=17100\n") {
		t.Fatalf("expected helm args to include mcpPort=17100, got:\n%s", args)
	}
	if !strings.Contains(args, "--set\napiPort=17133\n") {
		t.Fatalf("expected helm args to include apiPort=17133, got:\n%s", args)
	}
	if !strings.Contains(args, "--set\nsshPort=17122\n") {
		t.Fatalf("expected helm args to include sshPort=17122, got:\n%s", args)
	}
	for _, want := range []string{
		"--set\nmanagedCloud=true\n",
		"--set-string\ncloudContext.name=erun-001-020362606330-eu-west-2\n",
		"--set-string\ncloudContext.providerAlias=team-cloud\n",
		"--set-string\ncloudContext.region=eu-west-2\n",
		"--set-string\ncloudContext.instanceId=i-073c3338f26fbb000\n",
	} {
		if !strings.Contains(args, want) {
			t.Fatalf("expected helm args to include %q, got:\n%s", want, args)
		}
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

func TestCheckKubernetesDeploymentReturnsFalseWhenRuntimePortDiffers(t *testing.T) {
	kubectlDir := t.TempDir()
	kubectlPath := filepath.Join(kubectlDir, "kubectl")
	if err := os.WriteFile(kubectlPath, []byte(`#!/bin/sh
if [ "$1" = "--context" ]; then shift 2; fi
if [ "$1" = "--namespace" ]; then shift 2; fi
if [ "$1" = "get" ] && [ "$2" = "deployment" ] && [ "$3" = "erun-devops" ] && [ "$4" = "-o" ] && [ "$5" = "name" ]; then
  echo deployment/erun-devops
  exit 0
fi
if [ "$1" = "get" ] && [ "$2" = "deployment" ] && [ "$3" = "erun-devops" ] && [ "$4" = "-o" ] && [ "$5" = "json" ]; then
  cat <<'EOF'
{"spec":{"template":{"spec":{"containers":[{"name":"erun-devops","env":[{"name":"ERUN_REPO_PATH","value":"/home/erun/git/erun"},{"name":"ERUN_SSHD_ENABLED","value":"false"},{"name":"ERUN_MCP_PORT","value":"17000"},{"name":"ERUN_API_PORT","value":"17033"},{"name":"ERUN_SSHD_PORT","value":"17022"}]}]}}}}
EOF
  exit 0
fi
echo "unexpected kubectl invocation: $@" >&2
exit 1
`), 0o755); err != nil {
		t.Fatalf("write kubectl stub: %v", err)
	}
	t.Setenv("PATH", kubectlDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	sshd := false
	deployed, err := CheckKubernetesDeployment(KubernetesDeploymentCheckParams{
		Name:              "erun-devops",
		Namespace:         "erun-test",
		KubernetesContext: "cluster-local",
		ExpectedRepoPath:  "/home/erun/git/erun",
		ExpectedSSHD:      &sshd,
		ExpectedMCPPort:   17200,
		ExpectedAPIPort:   17233,
		ExpectedSSHPort:   17222,
	})
	if err != nil {
		t.Fatalf("CheckKubernetesDeployment failed: %v", err)
	}
	if deployed {
		t.Fatalf("expected port mismatch to require redeploy")
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

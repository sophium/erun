package eruncommon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adrg/xdg"
)

func setupXDGConfigHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	xdg.Reload()
	t.Cleanup(func() {
		xdg.Reload()
	})
	return dir
}

type bootstrapTestRunner struct {
	Store                     BootstrapStore
	FindProjectRoot           ProjectFinderFunc
	GetWorkingDir             WorkDirFunc
	SelectTenant              SelectTenantFunc
	Confirm                   ConfirmFunc
	PromptKubernetesContext   PromptValueFunc
	PromptContainerRegistry   PromptValueFunc
	PromptRemoteRepositoryURL PromptValueFunc
	PromptCodeCommitSSHKeyID  PromptValueFunc
	EnsureKubernetesNamespace NamespaceEnsurerFunc
	LoadProjectConfig         ProjectConfigLoaderFunc
	SaveProjectConfig         ProjectConfigSaverFunc
	WaitForRemoteRuntime      RemoteRuntimeWaitFunc
	RunRemoteCommand          RemoteCommandRunnerFunc
	DeployHelmChart           HelmChartDeployerFunc
	Sleep                     SleepFunc
	Context                   Context
}

func (r bootstrapTestRunner) Run(params BootstrapInitParams) (BootstrapInitResult, error) {
	return RunBootstrapInitWithDependencies(BootstrapInitDependencies(r), params)
}

func (r bootstrapTestRunner) saveProjectContainerRegistry(projectRoot, envName, registry string) error {
	return bootstrapRunner{
		BootstrapInitDependencies: BootstrapInitDependencies{
			Store:             r.Store,
			LoadProjectConfig: r.LoadProjectConfig,
			SaveProjectConfig: r.SaveProjectConfig,
			Context:           r.Context,
		},
		Context: r.Context,
	}.saveProjectContainerRegistry(projectRoot, envName, registry, false)
}

func TestBootstrapEnsureKubernetesNamespaceRunsContextPreflightFirst(t *testing.T) {
	var actions []string
	runner := bootstrapRunner{
		BootstrapInitDependencies: BootstrapInitDependencies{
			EnsureKubernetesNamespace: func(contextName, namespace string) error {
				actions = append(actions, "namespace "+contextName+" "+namespace)
				return nil
			},
		},
		Context: Context{
			KubernetesContextPreflight: func(_ Context, contextName string) error {
				actions = append(actions, "preflight "+contextName)
				return nil
			},
		},
	}
	runner.BootstrapInitDependencies.Context = runner.Context

	if err := runner.ensureKubernetesNamespace("tenant-a", "dev", "", "cluster-dev"); err != nil {
		t.Fatalf("ensureKubernetesNamespace failed: %v", err)
	}

	got := strings.Join(actions, "\n")
	want := "preflight cluster-dev\nnamespace cluster-dev tenant-a-dev"
	if got != want {
		t.Fatalf("unexpected action order:\n%s", got)
	}
}

func TestBootstrapRunLoadsExistingConfiguration(t *testing.T) {
	setupXDGConfigHome(t)

	tenant := "tenant-a"
	projectRoot := filepath.Join(t.TempDir(), "project")
	if err := SaveERunConfig(ERunConfig{DefaultTenant: tenant}); err != nil {
		t.Fatalf("save erun config: %v", err)
	}
	if err := SaveTenantConfig(TenantConfig{
		Name:               tenant,
		ProjectRoot:        projectRoot,
		DefaultEnvironment: DefaultEnvironment,
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := SaveEnvConfig(tenant, EnvConfig{Name: DefaultEnvironment, RepoPath: projectRoot, KubernetesContext: "cluster-local"}); err != nil {
		t.Fatalf("save env config: %v", err)
	}

	service := bootstrapTestRunner{
		Store: ConfigStore{},
		FindProjectRoot: func() (string, string, error) {
			return "", "", ErrNotInGitRepository
		},
		Confirm: func(label string) (bool, error) {
			t.Fatalf("unexpected confirmation: %s", label)
			return false, nil
		},
	}

	result, err := service.Run(BootstrapInitParams{})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if result.CreatedERunConfig || result.CreatedTenantConfig || result.CreatedEnvConfig {
		t.Fatalf("expected existing config to be reused, got %+v", result)
	}
	if result.TenantConfig.ProjectRoot != projectRoot {
		t.Fatalf("unexpected project root: %s", result.TenantConfig.ProjectRoot)
	}
}

func TestBootstrapRunRespectsExistingTenantDefaultEnvironment(t *testing.T) {
	setupXDGConfigHome(t)

	tenant := "tenant-a"
	projectRoot := filepath.Join(t.TempDir(), "project")
	if err := SaveERunConfig(ERunConfig{DefaultTenant: tenant}); err != nil {
		t.Fatalf("save erun config: %v", err)
	}
	if err := SaveTenantConfig(TenantConfig{
		Name:               tenant,
		ProjectRoot:        projectRoot,
		DefaultEnvironment: "prod",
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := SaveEnvConfig(tenant, EnvConfig{Name: "prod", RepoPath: projectRoot, KubernetesContext: "cluster-prod"}); err != nil {
		t.Fatalf("save env config: %v", err)
	}

	service := bootstrapTestRunner{
		Store: ConfigStore{},
		FindProjectRoot: func() (string, string, error) {
			return tenant, projectRoot, nil
		},
		Confirm: func(label string) (bool, error) {
			t.Fatalf("unexpected confirmation: %s", label)
			return false, nil
		},
	}

	result, err := service.Run(BootstrapInitParams{})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result.EnvConfig.Name != "prod" {
		t.Fatalf("expected tenant default environment to be used, got %+v", result.EnvConfig)
	}
}

func TestBootstrapRunResolveTenantUsesCurrentDirectoryTenantBeforeDefault(t *testing.T) {
	setupXDGConfigHome(t)

	projectRootA := filepath.Join(t.TempDir(), "tenant-a")
	projectRootB := filepath.Join(t.TempDir(), "tenant-b")
	for _, tenant := range []struct {
		name        string
		projectRoot string
	}{
		{name: "tenant-a", projectRoot: projectRootA},
		{name: "tenant-b", projectRoot: projectRootB},
	} {
		if err := SaveTenantConfig(TenantConfig{
			Name:               tenant.name,
			ProjectRoot:        tenant.projectRoot,
			DefaultEnvironment: DefaultEnvironment,
		}); err != nil {
			t.Fatalf("save tenant config: %v", err)
		}
		if err := SaveEnvConfig(tenant.name, EnvConfig{Name: DefaultEnvironment, RepoPath: tenant.projectRoot, KubernetesContext: "cluster-" + tenant.name}); err != nil {
			t.Fatalf("save env config: %v", err)
		}
	}
	if err := SaveERunConfig(ERunConfig{DefaultTenant: "tenant-b"}); err != nil {
		t.Fatalf("save erun config: %v", err)
	}

	service := bootstrapTestRunner{
		Store: ConfigStore{},
		GetWorkingDir: func() (string, error) {
			return filepath.Join(projectRootA, "nested"), nil
		},
		SelectTenant: func([]TenantConfig) (TenantSelectionResult, error) {
			t.Fatal("unexpected tenant selection")
			return TenantSelectionResult{}, nil
		},
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRootA, nil
		},
		Confirm: func(label string) (bool, error) {
			t.Fatalf("unexpected confirmation: %s", label)
			return false, nil
		},
	}

	result, err := service.Run(BootstrapInitParams{ResolveTenant: true})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result.TenantConfig.Name != "tenant-a" {
		t.Fatalf("expected current directory tenant to win, got %+v", result.TenantConfig)
	}
}

func TestBootstrapRunPromptsForKubernetesContextAndContainerRegistryWhenCreatingEnvironment(t *testing.T) {
	setupXDGConfigHome(t)

	projectRoot := t.TempDir()
	ensuredContext := ""
	ensuredNamespace := ""
	promptedRegistryLabel := ""
	service := bootstrapTestRunner{
		Store: ConfigStore{},
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		Confirm: func(string) (bool, error) {
			return true, nil
		},
		PromptKubernetesContext: func(label string) (string, error) {
			want := kubernetesContextLabel("tenant-a", DefaultEnvironment)
			if label != want {
				t.Fatalf("unexpected kubernetes context label: %q", label)
			}
			return "cluster-local", nil
		},
		PromptContainerRegistry: func(label string) (string, error) {
			promptedRegistryLabel = label
			return "", nil
		},
		EnsureKubernetesNamespace: func(contextName, namespace string) error {
			ensuredContext = contextName
			ensuredNamespace = namespace
			return nil
		},
	}

	result, err := service.Run(BootstrapInitParams{})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result.EnvConfig.KubernetesContext != "cluster-local" {
		t.Fatalf("expected kubernetes context to be saved, got %+v", result.EnvConfig)
	}
	if result.EnvConfig.ContainerRegistry != DefaultContainerRegistry {
		t.Fatalf("expected default container registry to be saved, got %+v", result.EnvConfig)
	}
	if ensuredContext != "cluster-local" || ensuredNamespace != "tenant-a-local" {
		t.Fatalf("unexpected namespace ensure request: context=%q namespace=%q", ensuredContext, ensuredNamespace)
	}
	if promptedRegistryLabel != containerRegistryLabel("tenant-a", DefaultEnvironment) {
		t.Fatalf("unexpected container registry label: %q", promptedRegistryLabel)
	}
}

func TestBootstrapRunCreatesTenantDevopsModuleAndChart(t *testing.T) {
	setupXDGConfigHome(t)

	projectRoot := t.TempDir()
	service := bootstrapTestRunner{
		Store: ConfigStore{},
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		Confirm: func(string) (bool, error) {
			return true, nil
		},
		PromptKubernetesContext: func(string) (string, error) {
			return "cluster-local", nil
		},
		PromptContainerRegistry: func(string) (string, error) {
			return DefaultContainerRegistry, nil
		},
		EnsureKubernetesNamespace: func(string, string) error {
			return nil
		},
	}

	if _, err := service.Run(BootstrapInitParams{RuntimeVersion: "1.2.3"}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	moduleRoot := filepath.Join(projectRoot, "tenant-a-devops")
	requireTenantDevopsFiles(t, moduleRoot)

	dockerfilePath := filepath.Join(moduleRoot, "docker", "tenant-a-devops", "Dockerfile")
	dockerfile, err := os.ReadFile(dockerfilePath)
	requireNoError(t, err, "read Dockerfile")
	requireTenantDevopsDockerfile(t, string(dockerfile))

	chartPath := filepath.Join(moduleRoot, "k8s", "tenant-a-devops", "Chart.yaml")
	chart, err := os.ReadFile(chartPath)
	if err != nil {
		t.Fatalf("read Chart.yaml: %v", err)
	}
	if !strings.Contains(string(chart), "name: tenant-a-devops") {
		t.Fatalf("expected tenant chart name, got %q", string(chart))
	}

	serviceTemplatePath := filepath.Join(moduleRoot, "k8s", "tenant-a-devops", "templates", "service.yaml")
	serviceTemplate, err := os.ReadFile(serviceTemplatePath)
	if err != nil {
		t.Fatalf("read service template: %v", err)
	}
	if !strings.Contains(string(serviceTemplate), "image: erunpaas/tenant-a-devops:{{ .Chart.AppVersion }}") {
		t.Fatalf("expected tenant image reference, got %q", string(serviceTemplate))
	}
	if !strings.Contains(string(serviceTemplate), "name: tenant-a-devops") {
		t.Fatalf("expected tenant container name, got %q", string(serviceTemplate))
	}
	if !strings.Contains(string(serviceTemplate), "type: DirectoryOrCreate") {
		t.Fatalf("expected repo worktree hostPath to allow missing directories, got %q", string(serviceTemplate))
	}
}

func requireTenantDevopsFiles(t *testing.T, moduleRoot string) {
	t.Helper()
	for _, path := range []string{
		filepath.Join(moduleRoot, "VERSION"),
		filepath.Join(moduleRoot, "docker", "tenant-a-devops", "Dockerfile"),
		filepath.Join(moduleRoot, "k8s", "tenant-a-devops", "Chart.yaml"),
		filepath.Join(moduleRoot, "k8s", "tenant-a-devops", "values.local.yaml"),
		filepath.Join(moduleRoot, "k8s", "tenant-a-devops", "templates", "service.yaml"),
	} {
		_, err := os.Stat(path)
		requireNoError(t, err, "expected "+path+" to exist")
	}
}

func requireTenantDevopsDockerfile(t *testing.T, dockerfile string) {
	t.Helper()
	requireStringContains(t, dockerfile, "FROM ${ERUN_BASE_TAG}", "expected thin wrapper Dockerfile")
	requireStringContains(t, dockerfile, "ARG ERUN_BASE_TAG=erunpaas/erun-devops:1.2.3", "expected Dockerfile base tag to match init runtime version")
	requireStringContains(t, dockerfile, "ENTRYPOINT [\"erun-devops-entrypoint\"]", "expected wrapper Dockerfile to delegate to base entrypoint")
	requireCondition(t, !strings.Contains(dockerfile, "exec /bin/bash -i") && !strings.Contains(dockerfile, "entrypoint.sh") && !strings.Contains(dockerfile, "terraform"), "expected no duplicated runtime setup in Dockerfile, got %q", dockerfile)
}

func TestEnsureDefaultDevopsChartMigratesLegacyGeneratedServiceTemplate(t *testing.T) {
	projectRoot := t.TempDir()
	moduleName := "tenant-a-devops"
	serviceTemplatePath := filepath.Join(projectRoot, moduleName, "k8s", moduleName, "templates", "service.yaml")
	current, err := defaultDevopsChartFiles.ReadFile("assets/default-devops-chart/templates/service.yaml")
	if err != nil {
		t.Fatalf("read embedded service template: %v", err)
	}
	rendered := renderDefaultDevopsChartTemplate("assets/default-devops-chart/templates/service.yaml", moduleName, moduleName, current)
	if err := os.MkdirAll(filepath.Dir(serviceTemplatePath), 0o755); err != nil {
		t.Fatalf("mkdir service template dir: %v", err)
	}
	if err := os.WriteFile(serviceTemplatePath, []byte(legacyDefaultDevopsServiceTemplate(rendered)), 0o644); err != nil {
		t.Fatalf("write legacy service template: %v", err)
	}

	if err := EnsureDefaultDevopsChart(Context{}, projectRoot, "tenant-a", DefaultEnvironment); err != nil {
		t.Fatalf("EnsureDefaultDevopsChart failed: %v", err)
	}

	migrated, err := os.ReadFile(serviceTemplatePath)
	if err != nil {
		t.Fatalf("read migrated service template: %v", err)
	}
	content := string(migrated)
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
		"containerPort: {{ $apiPort }}",
		"containerPort: {{ $sshPort }}",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected migrated service template to contain %q, got:\n%s", want, content)
		}
	}
}

func TestEnsureDefaultDevopsChartPreservesModifiedServiceTemplate(t *testing.T) {
	projectRoot := t.TempDir()
	moduleName := "tenant-a-devops"
	serviceTemplatePath := filepath.Join(projectRoot, moduleName, "k8s", moduleName, "templates", "service.yaml")
	current, err := defaultDevopsChartFiles.ReadFile("assets/default-devops-chart/templates/service.yaml")
	if err != nil {
		t.Fatalf("read embedded service template: %v", err)
	}
	rendered := renderDefaultDevopsChartTemplate("assets/default-devops-chart/templates/service.yaml", moduleName, moduleName, current)
	modified := legacyDefaultDevopsServiceTemplate(rendered) + "\n# tenant customization\n"
	if err := os.MkdirAll(filepath.Dir(serviceTemplatePath), 0o755); err != nil {
		t.Fatalf("mkdir service template dir: %v", err)
	}
	if err := os.WriteFile(serviceTemplatePath, []byte(modified), 0o644); err != nil {
		t.Fatalf("write modified service template: %v", err)
	}

	if err := EnsureDefaultDevopsChart(Context{}, projectRoot, "tenant-a", DefaultEnvironment); err != nil {
		t.Fatalf("EnsureDefaultDevopsChart failed: %v", err)
	}

	preserved, err := os.ReadFile(serviceTemplatePath)
	if err != nil {
		t.Fatalf("read service template: %v", err)
	}
	if string(preserved) != modified {
		t.Fatalf("expected modified service template to be preserved, got:\n%s", preserved)
	}
}

func TestBootstrapRunCreatesTenantDevopsEnvironmentValuesFile(t *testing.T) {
	setupXDGConfigHome(t)

	projectRoot := t.TempDir()
	service := bootstrapTestRunner{
		Store: ConfigStore{},
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		Confirm: func(string) (bool, error) {
			return true, nil
		},
		PromptKubernetesContext: func(string) (string, error) {
			return "cluster-dev", nil
		},
		PromptContainerRegistry: func(string) (string, error) {
			return DefaultContainerRegistry, nil
		},
		EnsureKubernetesNamespace: func(string, string) error {
			return nil
		},
	}

	if _, err := service.Run(BootstrapInitParams{Environment: "dev"}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	valuesPath := filepath.Join(projectRoot, "tenant-a-devops", "k8s", "tenant-a-devops", "values.dev.yaml")
	if _, err := os.Stat(valuesPath); err != nil {
		t.Fatalf("expected %s to exist: %v", valuesPath, err)
	}

	data, err := os.ReadFile(valuesPath)
	if err != nil {
		t.Fatalf("read values.dev.yaml: %v", err)
	}
	if len(data) != 0 {
		t.Fatalf("expected empty values.dev.yaml, got %q", string(data))
	}
}

func TestBootstrapRunUpdatesLegacyGeneratedTenantDevopsDockerfile(t *testing.T) {
	setupXDGConfigHome(t)

	projectRoot := t.TempDir()
	moduleRoot := filepath.Join(projectRoot, "tenant-a-devops")
	dockerfilePath := filepath.Join(moduleRoot, "docker", "tenant-a-devops", "Dockerfile")
	if err := os.MkdirAll(filepath.Dir(dockerfilePath), 0o755); err != nil {
		t.Fatalf("mkdir docker dir: %v", err)
	}
	legacyDockerfile := "ARG ERUN_BASE_TAG=erunpaas/erun-devops:1.0.0\n\nFROM ${ERUN_BASE_TAG}\n\nENTRYPOINT [\"/bin/sh\", \"-lc\", \"if [ \\\"${1:-}\\\" = shell ]; then shift; repo_dir=\\\"${ERUN_REPO_PATH:-${HOME}/git/erun}\\\"; [ -d \\\"$repo_dir\\\" ] && cd \\\"$repo_dir\\\"; exec /bin/bash -i; fi; exec erun-devops-entrypoint \\\"$@\\\"\", \"erun-devops-wrapper\"]\n"
	if err := os.WriteFile(dockerfilePath, []byte(legacyDockerfile), 0o644); err != nil {
		t.Fatalf("write legacy Dockerfile: %v", err)
	}

	service := bootstrapTestRunner{
		Store: ConfigStore{},
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		Confirm: func(string) (bool, error) {
			return true, nil
		},
		PromptKubernetesContext: func(string) (string, error) {
			return "cluster-local", nil
		},
		PromptContainerRegistry: func(string) (string, error) {
			return DefaultContainerRegistry, nil
		},
		EnsureKubernetesNamespace: func(string, string) error {
			return nil
		},
	}

	if _, err := service.Run(BootstrapInitParams{}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	dockerfile, err := os.ReadFile(dockerfilePath)
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}
	if !strings.Contains(string(dockerfile), "ENTRYPOINT [\"erun-devops-entrypoint\"]") {
		t.Fatalf("expected legacy Dockerfile to be replaced with entrypoint wrapper, got %q", string(dockerfile))
	}
}

func TestBootstrapRunReturnsTenantConfirmationInteractionWhenPromptIsNeeded(t *testing.T) {
	setupXDGConfigHome(t)

	projectRoot := t.TempDir()
	service := bootstrapTestRunner{
		Store: ConfigStore{},
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
	}

	_, err := service.Run(BootstrapInitParams{})
	interaction, ok := AsBootstrapInitInteraction(err)
	if !ok {
		t.Fatalf("expected bootstrap init interaction, got %v", err)
	}
	if interaction.Type != BootstrapInitInteractionConfirmTenant {
		t.Fatalf("unexpected interaction: %+v", interaction)
	}
	if interaction.Label != tenantConfirmationLabel("tenant-a", projectRoot) {
		t.Fatalf("unexpected interaction label: %+v", interaction)
	}
}

func TestBootstrapRunReturnsKubernetesContextInteractionWhenPromptIsNeeded(t *testing.T) {
	setupXDGConfigHome(t)

	projectRoot := t.TempDir()
	service := bootstrapTestRunner{
		Store: ConfigStore{},
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		Confirm: func(string) (bool, error) {
			return true, nil
		},
	}

	_, err := service.Run(BootstrapInitParams{})
	interaction, ok := AsBootstrapInitInteraction(err)
	if !ok {
		t.Fatalf("expected bootstrap init interaction, got %v", err)
	}
	if interaction.Type != BootstrapInitInteractionKubernetesContext {
		t.Fatalf("unexpected interaction: %+v", interaction)
	}
	if interaction.Label != kubernetesContextLabel("tenant-a", DefaultEnvironment) {
		t.Fatalf("unexpected interaction label: %+v", interaction)
	}
}

func TestBootstrapRunUpdatesDefaultTenantWhenConfirmedAgainstExistingDefault(t *testing.T) {
	setupXDGConfigHome(t)

	projectRoot := filepath.Join(t.TempDir(), "frs")
	if err := SaveERunConfig(ERunConfig{DefaultTenant: "erun"}); err != nil {
		t.Fatalf("save erun config: %v", err)
	}

	service := bootstrapTestRunner{
		Store: ConfigStore{},
		FindProjectRoot: func() (string, string, error) {
			return "frs", projectRoot, nil
		},
		Confirm: func(string) (bool, error) {
			return true, nil
		},
		PromptKubernetesContext: func(string) (string, error) {
			return "rancher-desktop", nil
		},
		PromptContainerRegistry: func(string) (string, error) {
			return "", nil
		},
		EnsureKubernetesNamespace: func(string, string) error {
			return nil
		},
	}

	if _, err := service.Run(BootstrapInitParams{}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	toolConfig, _, err := LoadERunConfig()
	if err != nil {
		t.Fatalf("LoadERunConfig failed: %v", err)
	}
	if toolConfig.DefaultTenant != "frs" {
		t.Fatalf("expected default tenant to be updated to frs, got %+v", toolConfig)
	}
}

func TestBootstrapRunPromptsForMissingExistingKubernetesContext(t *testing.T) {
	setupXDGConfigHome(t)

	tenant := "tenant-a"
	projectRoot := filepath.Join(t.TempDir(), "project")
	if err := SaveERunConfig(ERunConfig{DefaultTenant: tenant}); err != nil {
		t.Fatalf("save erun config: %v", err)
	}
	if err := SaveTenantConfig(TenantConfig{
		Name:               tenant,
		ProjectRoot:        projectRoot,
		DefaultEnvironment: DefaultEnvironment,
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := SaveEnvConfig(tenant, EnvConfig{Name: DefaultEnvironment, RepoPath: projectRoot}); err != nil {
		t.Fatalf("save env config: %v", err)
	}

	ensuredContext := ""
	ensuredNamespace := ""
	service := bootstrapTestRunner{
		Store: ConfigStore{},
		FindProjectRoot: func() (string, string, error) {
			return tenant, projectRoot, nil
		},
		Confirm: func(string) (bool, error) {
			t.Fatal("unexpected confirmation")
			return false, nil
		},
		PromptKubernetesContext: func(label string) (string, error) {
			want := kubernetesContextLabel(tenant, DefaultEnvironment)
			if label != want {
				t.Fatalf("unexpected kubernetes context label: %q", label)
			}
			return "cluster-local", nil
		},
		PromptContainerRegistry: func(string) (string, error) {
			t.Fatal("unexpected container registry prompt")
			return "", nil
		},
		EnsureKubernetesNamespace: func(contextName, namespace string) error {
			ensuredContext = contextName
			ensuredNamespace = namespace
			return nil
		},
	}

	result, err := service.Run(BootstrapInitParams{})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result.CreatedEnvConfig {
		t.Fatalf("expected existing environment to be updated, got %+v", result)
	}
	if result.EnvConfig.KubernetesContext != "cluster-local" {
		t.Fatalf("expected kubernetes context to be stored, got %+v", result.EnvConfig)
	}
	if ensuredContext != "cluster-local" || ensuredNamespace != "tenant-a-local" {
		t.Fatalf("unexpected namespace ensure request: context=%q namespace=%q", ensuredContext, ensuredNamespace)
	}
}

func TestBootstrapRunUsesProjectContainerRegistryWhenCreatingEnvironment(t *testing.T) {
	setupXDGConfigHome(t)

	projectRoot := t.TempDir()
	service := bootstrapTestRunner{
		Store: ConfigStore{},
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		Confirm: func(string) (bool, error) {
			return true, nil
		},
		PromptKubernetesContext: func(string) (string, error) {
			return "cluster-local", nil
		},
		PromptContainerRegistry: func(string) (string, error) {
			t.Fatal("unexpected container registry prompt")
			return "", nil
		},
		EnsureKubernetesNamespace: func(string, string) error {
			return nil
		},
		LoadProjectConfig: func(root string) (ProjectConfig, string, error) {
			if root != projectRoot {
				t.Fatalf("unexpected project root: %s", root)
			}
			return ProjectConfig{
				Environments: map[string]ProjectEnvironmentConfig{
					DefaultEnvironment: {ContainerRegistry: "project-registry"},
				},
			}, "", nil
		},
		SaveProjectConfig: func(string, ProjectConfig) error {
			t.Fatal("unexpected project config save")
			return nil
		},
	}

	result, err := service.Run(BootstrapInitParams{})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result.EnvConfig.ContainerRegistry != "project-registry" {
		t.Fatalf("expected project container registry, got %+v", result.EnvConfig)
	}
}

func TestBootstrapRunMigratesExistingEnvironmentContainerRegistryToProjectConfig(t *testing.T) {
	setupXDGConfigHome(t)

	tenant := "tenant-a"
	projectRoot := t.TempDir()
	if err := SaveERunConfig(ERunConfig{DefaultTenant: tenant}); err != nil {
		t.Fatalf("save erun config: %v", err)
	}
	if err := SaveTenantConfig(TenantConfig{
		Name:               tenant,
		ProjectRoot:        projectRoot,
		DefaultEnvironment: DefaultEnvironment,
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := SaveEnvConfig(tenant, EnvConfig{
		Name:              DefaultEnvironment,
		RepoPath:          projectRoot,
		KubernetesContext: "cluster-local",
		ContainerRegistry: "legacy-registry",
	}); err != nil {
		t.Fatalf("save env config: %v", err)
	}

	savedRoot := ""
	savedConfig := ProjectConfig{}
	service := bootstrapTestRunner{
		Store: ConfigStore{},
		FindProjectRoot: func() (string, string, error) {
			return "", "", ErrNotInGitRepository
		},
		SaveProjectConfig: func(root string, config ProjectConfig) error {
			savedRoot = root
			savedConfig = config
			return nil
		},
	}

	result, err := service.Run(BootstrapInitParams{})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result.EnvConfig.ContainerRegistry != "legacy-registry" {
		t.Fatalf("expected legacy registry to remain effective, got %+v", result.EnvConfig)
	}
	if savedRoot != projectRoot || savedConfig.ContainerRegistryForEnvironment(DefaultEnvironment) != "legacy-registry" {
		t.Fatalf("unexpected project config save: root=%q config=%+v", savedRoot, savedConfig)
	}
	if savedConfig.ContainerRegistry != "" {
		t.Fatalf("expected legacy project registry to be migrated, got %+v", savedConfig)
	}
}

func TestBootstrapSaveProjectContainerRegistryPreservesExistingProjectWideRegistry(t *testing.T) {
	saved := false
	service := bootstrapTestRunner{
		LoadProjectConfig: func(string) (ProjectConfig, string, error) {
			return ProjectConfig{ContainerRegistry: "shared-registry"}, "", nil
		},
		SaveProjectConfig: func(string, ProjectConfig) error {
			saved = true
			return nil
		},
	}

	if err := service.saveProjectContainerRegistry("/tmp/project", DefaultEnvironment, "shared-registry"); err != nil {
		t.Fatalf("saveProjectContainerRegistry failed: %v", err)
	}
	if saved {
		t.Fatal("did not expect project config to be rewritten when the shared registry already matches")
	}
}

func TestBootstrapSaveProjectContainerRegistryPreservesProjectWideFallbackForOtherEnvironments(t *testing.T) {
	savedConfig := ProjectConfig{}
	service := bootstrapTestRunner{
		LoadProjectConfig: func(string) (ProjectConfig, string, error) {
			return ProjectConfig{ContainerRegistry: "shared-registry"}, "", nil
		},
		SaveProjectConfig: func(_ string, config ProjectConfig) error {
			savedConfig = config
			return nil
		},
	}

	if err := service.saveProjectContainerRegistry("/tmp/project", "prod", "prod-registry"); err != nil {
		t.Fatalf("saveProjectContainerRegistry failed: %v", err)
	}
	if savedConfig.ContainerRegistry != "shared-registry" {
		t.Fatalf("expected shared registry fallback to be preserved, got %+v", savedConfig)
	}
	if got := savedConfig.ContainerRegistryForEnvironment("prod"); got != "prod-registry" {
		t.Fatalf("unexpected prod registry: %q", got)
	}
	if got := savedConfig.ContainerRegistryForEnvironment(DefaultEnvironment); got != "shared-registry" {
		t.Fatalf("unexpected default environment fallback: %q", got)
	}
}

func TestKubernetesNamespaceNameNormalizesTenantAndEnvironment(t *testing.T) {
	got := KubernetesNamespaceName("Tenant_A", "Dev.Env")
	if got != "tenant-a-dev-env" {
		t.Fatalf("unexpected namespace name: %q", got)
	}
}

func TestBootstrapRunResolveTenantSelectionCancelled(t *testing.T) {
	setupXDGConfigHome(t)

	projectRoot := filepath.Join(t.TempDir(), "tenant-a")
	if err := SaveTenantConfig(TenantConfig{
		Name:               "tenant-a",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: DefaultEnvironment,
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := SaveEnvConfig("tenant-a", EnvConfig{Name: DefaultEnvironment, RepoPath: projectRoot, KubernetesContext: "cluster-local"}); err != nil {
		t.Fatalf("save env config: %v", err)
	}

	service := bootstrapTestRunner{
		Store: ConfigStore{},
		GetWorkingDir: func() (string, error) {
			return t.TempDir(), nil
		},
		FindProjectRoot: func() (string, string, error) {
			return "", "", ErrNotInGitRepository
		},
		SelectTenant: func([]TenantConfig) (TenantSelectionResult, error) {
			return TenantSelectionResult{}, nil
		},
	}

	if _, err := service.Run(BootstrapInitParams{ResolveTenant: true}); !errors.Is(err, ErrTenantSelectionCancelled) {
		t.Fatalf("expected ErrTenantSelectionCancelled, got %v", err)
	}
}

func TestBootstrapRunBootstrapsNewProjectWithAutoApprove(t *testing.T) {
	setupXDGConfigHome(t)

	projectRoot := t.TempDir()
	service := bootstrapTestRunner{
		Store: ConfigStore{},
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
	}

	result, err := service.Run(BootstrapInitParams{AutoApprove: true})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if !result.CreatedERunConfig || !result.CreatedTenantConfig || !result.CreatedEnvConfig {
		t.Fatalf("expected all configs to be created, got %+v", result)
	}
	if result.TenantConfig.Name != "tenant-a" || result.EnvConfig.Name != DefaultEnvironment || result.EnvConfig.RepoPath != projectRoot {
		t.Fatalf("unexpected init result: %+v", result)
	}
	if result.EnvConfig.ContainerRegistry != DefaultContainerRegistry {
		t.Fatalf("expected default container registry, got %+v", result.EnvConfig)
	}
}

func TestBootstrapRunFailsOutsideGitRepository(t *testing.T) {
	setupXDGConfigHome(t)

	service := bootstrapTestRunner{
		Store: ConfigStore{},
		FindProjectRoot: func() (string, string, error) {
			return "", "", ErrNotInGitRepository
		},
	}

	if _, err := service.Run(BootstrapInitParams{AutoApprove: true}); !errors.Is(err, ErrNotInGitRepository) {
		t.Fatalf("expected ErrNotInGitRepository, got %v", err)
	}
}

func TestBootstrapRunLogsProgress(t *testing.T) {
	setupXDGConfigHome(t)

	projectRoot := t.TempDir()
	logger := &testTraceLogger{}
	service := bootstrapTestRunner{
		Store: ConfigStore{},
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		Context: testContextWithLogger(logger),
	}

	if _, err := service.Run(BootstrapInitParams{AutoApprove: true}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	for _, want := range []string{
		"Loading erun tool configuration",
		"Trying to detect current project directory",
		"Saving default config",
		"Loaded erun tool configuration",
		"Loading tenant configuration",
		"Adding new tenant",
		"Loaded tenant configuration",
		"Loading environment configuration",
		"Adding new environment",
		"Configuration initialized OK",
	} {
		if !logger.containsTrace(want) {
			t.Fatalf("expected trace log containing %q, got %+v", want, logger.traces)
		}
	}
}

func TestBootstrapRunLogsOutsideGitRepository(t *testing.T) {
	setupXDGConfigHome(t)

	logger := &testTraceLogger{}
	service := bootstrapTestRunner{
		Store: ConfigStore{},
		FindProjectRoot: func() (string, string, error) {
			return "", "", ErrNotInGitRepository
		},
		Context: testContextWithLogger(logger),
	}

	_, err := service.Run(BootstrapInitParams{AutoApprove: true})
	if !errors.Is(err, ErrNotInGitRepository) {
		t.Fatalf("expected ErrNotInGitRepository, got %v", err)
	}
	if !logger.containsError("erun config is not initialized. Run erun in project directory.") {
		t.Fatalf("expected error log, got %+v", logger.errors)
	}
}

func TestBootstrapRunDecliningDefaultTenantStillInitializes(t *testing.T) {
	setupXDGConfigHome(t)

	projectRoot := t.TempDir()
	prompts := make([]string, 0, 2)
	service := bootstrapTestRunner{
		Store: ConfigStore{},
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		Confirm: func(label string) (bool, error) {
			prompts = append(prompts, label)
			return label != tenantConfirmationLabel("tenant-a", projectRoot), nil
		},
		PromptKubernetesContext: func(string) (string, error) {
			return "cluster-review", nil
		},
		PromptContainerRegistry: func(string) (string, error) {
			return DefaultContainerRegistry, nil
		},
		EnsureKubernetesNamespace: func(string, string) error {
			return nil
		},
	}

	result, err := service.Run(BootstrapInitParams{Environment: "review"})
	requireNoError(t, err, "Run failed")

	requireDeclinedDefaultTenantResult(t, result)
	if _, _, err := LoadERunConfig(); !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("expected default tenant config to remain unset, got %v", err)
	}
	wantPrompts := []string{
		tenantConfirmationLabel("tenant-a", projectRoot),
		environmentConfirmationLabel("tenant-a", "review"),
	}
	requireDeepEqual(t, prompts, wantPrompts, "unexpected prompts")
}

func requireDeclinedDefaultTenantResult(t *testing.T, result BootstrapInitResult) {
	t.Helper()
	requireCondition(t, !result.CreatedERunConfig, "did not expect erun config to be created, got %+v", result)
	requireCondition(t, result.CreatedTenantConfig && result.CreatedEnvConfig, "expected tenant and environment config to be created, got %+v", result)
	requireCondition(t, result.TenantConfig.Name == "tenant-a" && result.EnvConfig.Name == "review", "unexpected init result: %+v", result)
}

func TestBootstrapRunDecliningDefaultTenantViaParamStillInitializes(t *testing.T) {
	setupXDGConfigHome(t)

	projectRoot := t.TempDir()
	service := bootstrapTestRunner{
		Store: ConfigStore{},
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		PromptKubernetesContext: func(string) (string, error) {
			return "cluster-review", nil
		},
		PromptContainerRegistry: func(string) (string, error) {
			return DefaultContainerRegistry, nil
		},
		EnsureKubernetesNamespace: func(string, string) error {
			return nil
		},
	}

	confirmTenant := false
	confirmEnvironment := true
	result, err := service.Run(BootstrapInitParams{
		Environment:        "review",
		ConfirmTenant:      &confirmTenant,
		ConfirmEnvironment: &confirmEnvironment,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result.CreatedERunConfig {
		t.Fatalf("did not expect erun config to be created, got %+v", result)
	}
	if _, _, err := LoadERunConfig(); !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("expected default tenant config to remain unset, got %v", err)
	}
}

func TestBootstrapRunEnvironmentConfirmationRejected(t *testing.T) {
	setupXDGConfigHome(t)

	tenant := "tenant-a"
	projectRoot := t.TempDir()
	if err := SaveERunConfig(ERunConfig{DefaultTenant: tenant}); err != nil {
		t.Fatalf("save erun config: %v", err)
	}
	if err := SaveTenantConfig(TenantConfig{
		Name:               tenant,
		ProjectRoot:        projectRoot,
		DefaultEnvironment: DefaultEnvironment,
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}

	service := bootstrapTestRunner{
		Store: ConfigStore{},
		FindProjectRoot: func() (string, string, error) {
			return tenant, projectRoot, nil
		},
		Confirm: func(string) (bool, error) {
			return false, nil
		},
	}

	if _, err := service.Run(BootstrapInitParams{}); !errors.Is(err, ErrEnvironmentInitializationCancelled) {
		t.Fatalf("expected environment cancellation, got %v", err)
	}
}

func TestBootstrapRunPropagatesSaveErrors(t *testing.T) {
	setupXDGConfigHome(t)

	configPath := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "erun")
	if err := os.MkdirAll(configPath, 0o555); err != nil {
		t.Fatalf("mkdir config path: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(configPath, 0o755); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("reset chmod: %v", err)
		}
	})

	projectRoot := t.TempDir()
	service := bootstrapTestRunner{
		Store: ConfigStore{},
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		Confirm: func(string) (bool, error) {
			return true, nil
		},
	}

	if _, err := service.Run(BootstrapInitParams{}); !errors.Is(err, ErrFailedToSaveConfig) {
		t.Fatalf("expected save error, got %v", err)
	}
}

func TestBootstrapRunRemoteInitializesTenantInPodWorktree(t *testing.T) {
	setupXDGConfigHome(t)

	if err := SaveERunConfig(ERunConfig{
		CloudContexts: []CloudContextConfig{{
			Name:               "remote-cloud",
			Provider:           CloudProviderAWS,
			CloudProviderAlias: "team-cloud",
			KubernetesContext:  "cluster-remote",
		}},
	}); err != nil {
		t.Fatalf("SaveERunConfig failed: %v", err)
	}

	var waited ShellLaunchParams
	scripts := make([]string, 0, 3)
	service := bootstrapTestRunner{
		Context:         testContextWithLogger(&testTraceLogger{}),
		Store:           ConfigStore{},
		FindProjectRoot: unexpectedProjectDetection(t),
		Confirm: func(string) (bool, error) {
			return true, nil
		},
		PromptKubernetesContext: func(string) (string, error) {
			return "cluster-remote", nil
		},
		PromptContainerRegistry: func(string) (string, error) {
			return "registry.example.com/remote", nil
		},
		PromptRemoteRepositoryURL: remoteRepositoryPrompt(t, "frs", "dev", "git@github.com:sophium/frs.git"),
		EnsureKubernetesNamespace: remoteNamespaceEnsurer(t, "cluster-remote", "frs-dev"),
		DeployHelmChart:           remoteHelmDeployChecker(t, "frs", "1.2.3", "image: erunpaas/frs-devops:{{ .Chart.AppVersion }}"),
		WaitForRemoteRuntime: func(req ShellLaunchParams) error {
			waited = req
			return nil
		},
		RunRemoteCommand: remoteCommandSequence(t, &scripts, []remoteCommandReply{
			{result: RemoteCommandResult{Stdout: "repo_missing\n__ERUN_REMOTE_PUBLIC_KEY__\nssh-ed25519 AAAATEST remote\n__ERUN_REMOTE_CODECOMMIT_PUBLIC_KEY__\nssh-rsa AAAACODECOMMITRSA remote\n"}},
			{},
			{},
		}),
	}

	result, err := service.Run(BootstrapInitParams{
		Tenant:         "frs",
		Environment:    "dev",
		Remote:         true,
		RuntimeVersion: "1.2.3",
		RuntimeImage:   "frs-devops",
	})
	requireNoError(t, err, "Run failed")

	remotePath := RemoteWorktreePathForRepoName("frs")
	requireRemoteInitResult(t, result, remotePath)
	requireCondition(t, waited.Dir == remotePath && waited.KubernetesContext == "cluster-remote", "unexpected wait request: %+v", waited)
	requireEqual(t, len(scripts), 3, "remote command count")

	savedEnv, _, err := LoadEnvConfig("frs", "dev")
	requireNoError(t, err, "LoadEnvConfig failed")
	requireSavedRemoteEnv(t, savedEnv)
}

type remoteCommandReply struct {
	result RemoteCommandResult
	err    error
}

func unexpectedProjectDetection(t *testing.T) ProjectFinderFunc {
	t.Helper()
	return func() (string, string, error) {
		t.Fatal("did not expect local project detection for remote init")
		return "", "", nil
	}
}

func remoteRepositoryPrompt(t *testing.T, tenant, envName, repoURL string) PromptValueFunc {
	t.Helper()
	return func(label string) (string, error) {
		requireEqual(t, label, remoteRepositoryLabel(tenant, envName), "repository label")
		return repoURL, nil
	}
}

func remoteNamespaceEnsurer(t *testing.T, wantContext, wantNamespace string) NamespaceEnsurerFunc {
	t.Helper()
	return func(contextName, namespace string) error {
		requireCondition(t, contextName == wantContext && namespace == wantNamespace, "unexpected namespace ensure: %s %s", contextName, namespace)
		return nil
	}
}

func remoteHelmDeployChecker(t *testing.T, repoName, version, imageLine string) HelmChartDeployerFunc {
	t.Helper()
	return func(params HelmDeployParams) error {
		requireEqual(t, params.WorktreeStorage, WorktreeStoragePVC, "worktree storage")
		requireEqual(t, params.WorktreeRepoName, repoName, "worktree repo name")
		requireEqual(t, params.Version, version, "remote runtime version")
		service, err := os.ReadFile(filepath.Join(params.ChartPath, "templates", "service.yaml"))
		requireNoError(t, err, "ReadFile failed")
		requireStringContains(t, string(service), imageLine, "expected remote runtime image override")
		return nil
	}
}

func remoteCommandSequence(t *testing.T, scripts *[]string, replies []remoteCommandReply) RemoteCommandRunnerFunc {
	t.Helper()
	return func(req ShellLaunchParams, script string) (RemoteCommandResult, error) {
		*scripts = append(*scripts, script)
		index := len(*scripts) - 1
		if index >= len(replies) {
			t.Fatalf("unexpected remote command script:\n%s", script)
		}
		return replies[index].result, replies[index].err
	}
}

func requireRemoteInitResult(t *testing.T, result BootstrapInitResult, remotePath string) {
	t.Helper()
	requireEqual(t, result.TenantConfig.ProjectRoot, remotePath, "tenant project root")
	requireCondition(t, !result.TenantConfig.Remote, "did not expect tenant config to be marked remote: %+v", result.TenantConfig)
	requireCondition(t, result.EnvConfig.RepoPath == remotePath && result.EnvConfig.Remote, "unexpected env config: %+v", result.EnvConfig)
	requireEqual(t, result.EnvConfig.RuntimeVersion, "1.2.3", "remote runtime version")
	requireEqual(t, result.EnvConfig.ContainerRegistry, "registry.example.com/remote", "remote container registry")
	requireEqual(t, result.EnvConfig.CloudProviderAlias, "team-cloud", "cloud provider alias")
}

func requireSavedRemoteEnv(t *testing.T, savedEnv EnvConfig) {
	t.Helper()
	requireCondition(t, savedEnv.Remote && savedEnv.ContainerRegistry == "registry.example.com/remote", "unexpected saved env config: %+v", savedEnv)
	requireEqual(t, savedEnv.CloudProviderAlias, "team-cloud", "saved cloud provider alias")
}

func TestBootstrapRunRemoteNoGitCreatesWorktreeWithoutRepositoryPrompts(t *testing.T) {
	setupXDGConfigHome(t)

	requireNoError(t, SaveTenantConfig(TenantConfig{
		Name:               "erun",
		ProjectRoot:        RemoteWorktreePathForRepoName("erun"),
		DefaultEnvironment: "local",
	}), "SaveTenantConfig failed")
	for _, envName := range []string{"local", "proxmox1"} {
		requireNoError(t, SaveEnvConfig("erun", EnvConfig{
			Name:              envName,
			RepoPath:          RemoteWorktreePathForRepoName("erun"),
			KubernetesContext: "cluster-remote",
			Remote:            true,
		}), "SaveEnvConfig failed")
	}

	scripts := make([]string, 0, 1)
	var deployed HelmDeployParams
	service := bootstrapTestRunner{
		Context:         testContextWithLogger(&testTraceLogger{}),
		Store:           ConfigStore{},
		FindProjectRoot: unexpectedProjectDetection(t),
		Confirm: func(string) (bool, error) {
			return true, nil
		},
		PromptKubernetesContext: func(string) (string, error) {
			return "cluster-remote", nil
		},
		PromptContainerRegistry: func(string) (string, error) {
			return "registry.example.com/remote", nil
		},
		PromptRemoteRepositoryURL: unexpectedPrompt(t, "Git remote URL prompt"),
		EnsureKubernetesNamespace: func(string, string) error {
			return nil
		},
		DeployHelmChart: func(params HelmDeployParams) error {
			deployed = params
			return nil
		},
		WaitForRemoteRuntime: func(ShellLaunchParams) error {
			return nil
		},
		RunRemoteCommand: func(req ShellLaunchParams, script string) (RemoteCommandResult, error) {
			scripts = append(scripts, script)
			return RemoteCommandResult{}, nil
		},
	}

	result, err := service.Run(BootstrapInitParams{
		Tenant:      "erun",
		Environment: "test",
		Remote:      true,
		NoGit:       true,
	})
	requireNoError(t, err, "Run failed")

	remotePath := RemoteWorktreePathForRepoName("erun")
	requireRemoteNoGitResult(t, result, deployed, scripts, remotePath)
}

func unexpectedPrompt(t *testing.T, name string) PromptValueFunc {
	t.Helper()
	return func(string) (string, error) {
		t.Fatal("did not expect " + name)
		return "", nil
	}
}

func requireRemoteNoGitResult(t *testing.T, result BootstrapInitResult, deployed HelmDeployParams, scripts []string, remotePath string) {
	t.Helper()
	requireCondition(t, result.EnvConfig.RepoPath == remotePath && result.EnvConfig.Remote, "unexpected env config: %+v", result.EnvConfig)
	requireCondition(t, deployed.MCPPort == 17200 && deployed.SSHPort == 17222, "expected remote init deploy to use allocated ports, got mcp=%d ssh=%d", deployed.MCPPort, deployed.SSHPort)
	requireEqual(t, len(scripts), 1, "remote command count")
	requireStringContains(t, scripts[0], "mkdir -p "+shellQuote(remotePath), "expected worktree directory creation")
	requireRemoteNoGitScript(t, scripts[0])
}

func requireRemoteNoGitScript(t *testing.T, script string) {
	t.Helper()
	for _, unexpected := range []string{"ssh-keygen", "git clone", "git -C"} {
		requireCondition(t, !strings.Contains(script, unexpected), "did not expect %q in no-git script:\n%s", unexpected, script)
	}
}

func TestBootstrapRunRemoteBootstrapCreatesTenantDevopsModuleAndChart(t *testing.T) {
	setupXDGConfigHome(t)

	scripts := make([]string, 0, 2)
	projectRoot := RemoteWorktreePathForRepoName("frs")
	service := bootstrapTestRunner{
		Context: testContextWithLogger(&testTraceLogger{}),
		Store:   ConfigStore{},
		Confirm: func(string) (bool, error) {
			return true, nil
		},
		PromptKubernetesContext: func(string) (string, error) {
			return "cluster-remote", nil
		},
		PromptContainerRegistry: func(string) (string, error) {
			return DefaultContainerRegistry, nil
		},
		PromptRemoteRepositoryURL: func(string) (string, error) {
			t.Fatal("did not expect Git remote URL prompt")
			return "", nil
		},
		EnsureKubernetesNamespace: func(string, string) error {
			return nil
		},
		DeployHelmChart: func(HelmDeployParams) error {
			return nil
		},
		WaitForRemoteRuntime: func(ShellLaunchParams) error {
			return nil
		},
		RunRemoteCommand: func(_ ShellLaunchParams, script string) (RemoteCommandResult, error) {
			scripts = append(scripts, script)
			return RemoteCommandResult{}, nil
		},
	}

	if _, err := service.Run(BootstrapInitParams{
		Tenant:         "frs",
		Environment:    "dev",
		Remote:         true,
		NoGit:          true,
		Bootstrap:      true,
		RuntimeVersion: "1.2.3",
	}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if len(scripts) != 2 {
		t.Fatalf("expected worktree and bootstrap scripts, got %d: %#v", len(scripts), scripts)
	}
	bootstrapScript := scripts[1]
	for _, path := range []string{
		filepath.Join(projectRoot, "frs-devops", "VERSION"),
		filepath.Join(projectRoot, "frs-devops", "docker", "frs-devops", "Dockerfile"),
		filepath.Join(projectRoot, "frs-devops", "k8s", "frs-devops", "Chart.yaml"),
		filepath.Join(projectRoot, "frs-devops", "k8s", "frs-devops", "values.dev.yaml"),
	} {
		if !strings.Contains(bootstrapScript, shellQuote(path)) {
			t.Fatalf("expected bootstrap script to write %s, got:\n%s", path, bootstrapScript)
		}
	}
	if !strings.Contains(bootstrapScript, "ARG ERUN_BASE_TAG=erunpaas/erun-devops:1.2.3") {
		t.Fatalf("expected runtime version in bootstrap Dockerfile, got:\n%s", bootstrapScript)
	}
}

func TestBootstrapRunRemoteConfiguresCodeCommitSSHRepository(t *testing.T) {
	setupXDGConfigHome(t)

	var promptedKeyID bool
	scripts := make([]string, 0, 3)
	service := bootstrapTestRunner{
		Context: testContextWithLogger(&testTraceLogger{}),
		Store:   ConfigStore{},
		Confirm: func(string) (bool, error) {
			return true, nil
		},
		PromptKubernetesContext: func(string) (string, error) {
			return "cluster-remote", nil
		},
		PromptContainerRegistry: func(string) (string, error) {
			return DefaultContainerRegistry, nil
		},
		PromptRemoteRepositoryURL: func(string) (string, error) {
			return "git-codecommit.eu-west-1.amazonaws.com/v1/repos/petios", nil
		},
		PromptCodeCommitSSHKeyID: func(label string) (string, error) {
			requireEqual(t, label, codeCommitSSHKeyIDLabel("petios", "dev"), "CodeCommit SSH key ID label")
			promptedKeyID = true
			return "APKATESTCODECOMMITKEY", nil
		},
		EnsureKubernetesNamespace: func(string, string) error {
			return nil
		},
		DeployHelmChart: func(HelmDeployParams) error {
			return nil
		},
		WaitForRemoteRuntime: func(ShellLaunchParams) error {
			return nil
		},
		RunRemoteCommand: remoteCommandSequence(t, &scripts, []remoteCommandReply{
			{result: RemoteCommandResult{Stdout: "repo_missing\n__ERUN_REMOTE_PUBLIC_KEY__\nssh-ed25519 AAAATEST remote\n"}},
			{},
			{},
		}),
	}

	_, err := service.Run(BootstrapInitParams{
		Tenant:      "petios",
		Environment: "dev",
		Remote:      true,
	})
	requireNoError(t, err, "Run failed")

	requireCondition(t, promptedKeyID, "expected CodeCommit SSH key ID prompt")
	requireCodeCommitRemoteScripts(t, scripts)
}

func requireCodeCommitRemoteScripts(t *testing.T, scripts []string) {
	t.Helper()
	requireEqual(t, len(scripts), 3, "remote script count")
	for _, want := range []string{`ssh-keygen -t ed25519`, `ssh-keygen -t rsa -b 4096`, `id_rsa_codecommit`, `__ERUN_REMOTE_CODECOMMIT_PUBLIC_KEY__`} {
		requireStringContains(t, scripts[0], want, "expected repository state script content")
	}
	for _, script := range scripts[1:] {
		requireCodeCommitScript(t, script)
	}
}

func requireCodeCommitScript(t *testing.T, script string) {
	t.Helper()
	for _, want := range []string{
		"Host git-codecommit.eu-west-1.amazonaws.com",
		"User APKATESTCODECOMMITKEY",
		"IdentityFile ~/.ssh/id_rsa_codecommit",
		`ssh_command='ssh -F "$HOME/.ssh/config"'`,
		"ssh://git-codecommit.eu-west-1.amazonaws.com/v1/repos/petios",
	} {
		requireStringContains(t, script, want, "expected CodeCommit script content")
	}
}

func TestBootstrapRunRemoteWaitsForSSHKeyImportAndRetries(t *testing.T) {
	setupXDGConfigHome(t)

	logger := &testTraceLogger{}
	var sleepCalls int
	scripts := make([]string, 0, 5)
	service := bootstrapTestRunner{
		Context: testContextWithLogger(logger),
		Store:   ConfigStore{},
		Confirm: func(string) (bool, error) {
			return true, nil
		},
		PromptKubernetesContext: func(string) (string, error) {
			return "cluster-remote", nil
		},
		PromptContainerRegistry: func(string) (string, error) {
			return DefaultContainerRegistry, nil
		},
		PromptRemoteRepositoryURL: func(string) (string, error) {
			return "git@github.com:sophium/frs.git", nil
		},
		EnsureKubernetesNamespace: func(string, string) error {
			return nil
		},
		DeployHelmChart: func(HelmDeployParams) error {
			return nil
		},
		WaitForRemoteRuntime: func(ShellLaunchParams) error {
			return nil
		},
		RunRemoteCommand: remoteCommandSequence(t, &scripts, []remoteCommandReply{
			{result: RemoteCommandResult{Stdout: "repo_missing\n__ERUN_REMOTE_PUBLIC_KEY__\nssh-ed25519 AAAATEST remote\n"}},
			{result: RemoteCommandResult{Stderr: "Permission denied (publickey)."}, err: fmt.Errorf("exit status 128")},
			{result: RemoteCommandResult{Stderr: "Permission denied (publickey)."}, err: fmt.Errorf("exit status 128")},
			{},
			{},
		}),
		Sleep: func(duration time.Duration) {
			sleepCalls++
			requireEqual(t, duration, remoteRepositoryAccessRetryInterval, "sleep duration")
		},
	}

	_, err := service.Run(BootstrapInitParams{
		Tenant:      "frs",
		Environment: "dev",
		Remote:      true,
	})
	requireNoError(t, err, "Run failed")

	requireRemoteRetryResult(t, logger, sleepCalls, scripts)
}

func requireRemoteRetryResult(t *testing.T, logger *testTraceLogger, sleepCalls int, scripts []string) {
	t.Helper()
	requireEqual(t, sleepCalls, 2, "sleep call count")
	requireEqual(t, len(scripts), 5, "remote command count")
	requireCondition(t, logger.containsTrace("Waiting for the SSH key to be deployed to the git host. Rechecking every 2 seconds. Press Ctrl+C to cancel."), "expected waiting message, got %+v", logger.traces)
	requireCondition(t, logger.containsTrace("SSH key not active yet; retrying in 2 seconds..."), "expected retry message, got %+v", logger.traces)
	requireCondition(t, logger.containsTrace("Remote repository access confirmed."), "expected success message, got %+v", logger.traces)
}

func TestBootstrapRunRemoteOffersExistingSSHHostConfigBeforeKeyImport(t *testing.T) {
	setupXDGConfigHome(t)

	logger := &testTraceLogger{}
	var promptedHostConfig bool
	scripts := make([]string, 0, 3)
	service := bootstrapTestRunner{
		Context: testContextWithLogger(logger),
		Store:   ConfigStore{},
		Confirm: func(label string) (bool, error) {
			if label == remoteHostConfigLabel("frs", "dev") {
				promptedHostConfig = true
			}
			return true, nil
		},
		PromptKubernetesContext: func(string) (string, error) {
			return "cluster-remote", nil
		},
		PromptContainerRegistry: func(string) (string, error) {
			return DefaultContainerRegistry, nil
		},
		PromptRemoteRepositoryURL: func(string) (string, error) {
			return "git@github.com:sophium/frs.git", nil
		},
		EnsureKubernetesNamespace: func(string, string) error {
			return nil
		},
		DeployHelmChart: func(HelmDeployParams) error {
			return nil
		},
		WaitForRemoteRuntime: func(ShellLaunchParams) error {
			return nil
		},
		RunRemoteCommand: func(req ShellLaunchParams, script string) (RemoteCommandResult, error) {
			scripts = append(scripts, script)
			switch len(scripts) {
			case 1:
				return RemoteCommandResult{
					Stdout: strings.Join([]string{
						"repo_missing",
						"__ERUN_REMOTE_PUBLIC_KEY__",
						"ssh-ed25519 AAAATEST remote",
						"__ERUN_REMOTE_CODECOMMIT_PUBLIC_KEY__",
						"ssh-rsa AAAACODECOMMITRSA remote",
						"__ERUN_REMOTE_SSH_CONFIG__",
						"exists",
					}, "\n") + "\n",
				}, nil
			case 2:
				requireExistingHostConfigScript(t, script, "access check")
				return RemoteCommandResult{}, nil
			case 3:
				requireExistingHostConfigScript(t, script, "clone")
				return RemoteCommandResult{}, nil
			default:
				t.Fatalf("unexpected remote command script:\n%s", script)
				return RemoteCommandResult{}, nil
			}
		},
	}

	_, err := service.Run(BootstrapInitParams{
		Tenant:      "frs",
		Environment: "dev",
		Remote:      true,
	})
	requireNoError(t, err, "Run failed")

	requireExistingHostConfigResult(t, logger, promptedHostConfig, scripts)
}

func requireExistingHostConfigScript(t *testing.T, script, label string) {
	t.Helper()
	requireCondition(t, strings.Contains(script, `ssh -F "$HOME/.ssh/config"`) && !strings.Contains(script, "id_ed25519"), "expected existing host config %s, got:\n%s", label, script)
}

func requireExistingHostConfigResult(t *testing.T, logger *testTraceLogger, promptedHostConfig bool, scripts []string) {
	t.Helper()
	requireCondition(t, promptedHostConfig, "expected existing SSH host config confirmation")
	requireEqual(t, len(scripts), 3, "remote command count")
	requireCondition(t, !logger.containsTrace("Import this SSH public key into your git host before continuing:"), "did not expect SSH public key import instructions, got %+v", logger.traces)
	requireCondition(t, !logger.containsTrace("Waiting for the SSH key to be deployed to the git host."), "did not expect SSH key import wait, got %+v", logger.traces)
}

func TestBootstrapRunRemoteRequestsRepositoryURLWhenCheckoutMissing(t *testing.T) {
	setupXDGConfigHome(t)

	service := bootstrapTestRunner{
		Context: testContextWithLogger(&testTraceLogger{}),
		Store:   ConfigStore{},
		Confirm: func(string) (bool, error) {
			return true, nil
		},
		PromptKubernetesContext: func(string) (string, error) {
			return "cluster-remote", nil
		},
		PromptContainerRegistry: func(string) (string, error) {
			return DefaultContainerRegistry, nil
		},
		DeployHelmChart: func(HelmDeployParams) error {
			return nil
		},
		WaitForRemoteRuntime: func(ShellLaunchParams) error {
			return nil
		},
		RunRemoteCommand: func(req ShellLaunchParams, script string) (RemoteCommandResult, error) {
			return RemoteCommandResult{
				Stdout: "repo_missing\n__ERUN_REMOTE_PUBLIC_KEY__\nssh-ed25519 AAAATEST remote\n",
			}, nil
		},
	}

	_, err := service.Run(BootstrapInitParams{
		Tenant:      "frs",
		Environment: "dev",
		Remote:      true,
	})
	interaction, ok := AsBootstrapInitInteraction(err)
	if !ok {
		t.Fatalf("expected interaction, got %v", err)
	}
	if interaction.Type != BootstrapInitInteractionRemoteRepository {
		t.Fatalf("unexpected interaction: %+v", interaction)
	}
}

type testTraceLogger struct {
	traces []string
	errors []string
}

func testContextWithLogger(logger *testTraceLogger) Context {
	return Context{
		Logger: NewLoggerWithWriters(2, testTraceWriter{logger}, testErrorWriter{logger}),
	}
}

type testTraceWriter struct {
	logger *testTraceLogger
}

func (w testTraceWriter) Write(p []byte) (int, error) {
	w.logger.traces = append(w.logger.traces, strings.TrimSpace(string(p)))
	return len(p), nil
}

type testErrorWriter struct {
	logger *testTraceLogger
}

func (w testErrorWriter) Write(p []byte) (int, error) {
	w.logger.errors = append(w.logger.errors, strings.TrimSpace(string(p)))
	return len(p), nil
}

func (l *testTraceLogger) containsTrace(want string) bool {
	for _, got := range l.traces {
		if strings.Contains(got, want) {
			return true
		}
	}
	return false
}

func (l *testTraceLogger) containsError(want string) bool {
	for _, got := range l.errors {
		if strings.Contains(got, want) {
			return true
		}
	}
	return false
}

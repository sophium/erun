package eruncommon

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	EnsureKubernetesNamespace NamespaceEnsurerFunc
	LoadProjectConfig         ProjectConfigLoaderFunc
	SaveProjectConfig         ProjectConfigSaverFunc
	Context                   Context
}

func (r bootstrapTestRunner) Run(params BootstrapInitParams) (BootstrapInitResult, error) {
	return RunBootstrapInit(
		r.Context,
		params,
		r.Store,
		r.FindProjectRoot,
		r.GetWorkingDir,
		r.SelectTenant,
		r.Confirm,
		r.PromptKubernetesContext,
		r.PromptContainerRegistry,
		r.EnsureKubernetesNamespace,
		r.LoadProjectConfig,
		r.SaveProjectConfig,
	)
}

func (r bootstrapTestRunner) saveProjectContainerRegistry(projectRoot, envName, registry string) error {
	return bootstrapRunner(r).saveProjectContainerRegistry(projectRoot, envName, registry)
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

func TestBootstrapRunCreatesTenantDevopsModule(t *testing.T) {
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

	if _, err := service.Run(BootstrapInitParams{}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	moduleRoot := filepath.Join(projectRoot, "tenant-a-devops")
	for _, path := range []string{
		filepath.Join(moduleRoot, "VERSION"),
		filepath.Join(moduleRoot, "docker", "tenant-a-devops", "Dockerfile"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s to exist: %v", path, err)
		}
	}

	dockerfilePath := filepath.Join(moduleRoot, "docker", "tenant-a-devops", "Dockerfile")
	dockerfile, err := os.ReadFile(dockerfilePath)
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}
	if !strings.Contains(string(dockerfile), "FROM ${ERUN_BASE_TAG}") {
		t.Fatalf("expected thin wrapper Dockerfile, got %q", string(dockerfile))
	}
	if !strings.Contains(string(dockerfile), "ENTRYPOINT [\"erun-devops-entrypoint\"]") {
		t.Fatalf("expected wrapper Dockerfile to delegate to base entrypoint, got %q", string(dockerfile))
	}
	if strings.Contains(string(dockerfile), "exec /bin/bash -i") || strings.Contains(string(dockerfile), "entrypoint.sh") || strings.Contains(string(dockerfile), "terraform") {
		t.Fatalf("expected no duplicated runtime setup in Dockerfile, got %q", string(dockerfile))
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

func TestBootstrapRunTenantConfirmationRejected(t *testing.T) {
	setupXDGConfigHome(t)

	projectRoot := t.TempDir()
	service := bootstrapTestRunner{
		Store: ConfigStore{},
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		Confirm: func(string) (bool, error) {
			return false, nil
		},
	}

	if _, err := service.Run(BootstrapInitParams{}); !errors.Is(err, ErrTenantInitializationCancelled) {
		t.Fatalf("expected tenant cancellation, got %v", err)
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

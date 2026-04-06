package bootstrap

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sophium/erun/internal"
)

const (
	DefaultEnvironment       = "local"
	DefaultContainerRegistry = "erunpaas"
)

var (
	ErrTenantInitializationCancelled      = errors.New("tenant initialization cancelled by user")
	ErrEnvironmentInitializationCancelled = errors.New("environment initialization cancelled by user")
	ErrKubernetesContextCancelled         = errors.New("kubernetes context association cancelled by user")
	ErrContainerRegistryCancelled         = errors.New("container registry configuration cancelled by user")
	ErrTenantSelectionCancelled           = errors.New("tenant selection cancelled by user")
)

type Store interface {
	LoadERunConfig() (internal.ERunConfig, string, error)
	SaveERunConfig(internal.ERunConfig) error
	ListTenantConfigs() ([]internal.TenantConfig, error)
	LoadTenantConfig(string) (internal.TenantConfig, string, error)
	SaveTenantConfig(internal.TenantConfig) error
	LoadEnvConfig(string, string) (internal.EnvConfig, string, error)
	SaveEnvConfig(string, internal.EnvConfig) error
}

type (
	ProjectFinder       func() (string, string, error)
	WorkDirFunc         func() (string, error)
	SelectTenantFunc    func([]internal.TenantConfig) (TenantSelectionResult, error)
	PromptValueFunc     func(string) (string, error)
	NamespaceEnsurer    func(string, string) error
	ProjectConfigLoader func(string) (internal.ProjectConfig, string, error)
	ProjectConfigSaver  func(string, internal.ProjectConfig) error
)

type (
	ConfirmFunc           func(label string) (bool, error)
	TenantSelectionResult struct {
		Tenant     string
		Initialize bool
	}
	Logger interface {
		Trace(string)
		Error(string)
	}
)

type ConfigStore struct{}

func (ConfigStore) LoadERunConfig() (internal.ERunConfig, string, error) {
	return internal.LoadERunConfig()
}

func (ConfigStore) SaveERunConfig(config internal.ERunConfig) error {
	return internal.SaveERunConfig(config)
}

func (ConfigStore) ListTenantConfigs() ([]internal.TenantConfig, error) {
	return internal.ListTenantConfigs()
}

func (ConfigStore) LoadTenantConfig(tenant string) (internal.TenantConfig, string, error) {
	return internal.LoadTenantConfig(tenant)
}

func (ConfigStore) SaveTenantConfig(config internal.TenantConfig) error {
	return internal.SaveTenantConfig(config)
}

func (ConfigStore) LoadEnvConfig(tenant, envName string) (internal.EnvConfig, string, error) {
	return internal.LoadEnvConfig(tenant, envName)
}

func (ConfigStore) SaveEnvConfig(tenant string, config internal.EnvConfig) error {
	return internal.SaveEnvConfig(tenant, config)
}

type InitRequest struct {
	Tenant            string
	ProjectRoot       string
	Environment       string
	KubernetesContext string
	ContainerRegistry string
	AutoApprove       bool
	ResolveTenant     bool
}

type InitResult struct {
	ERunConfig          internal.ERunConfig
	TenantConfig        internal.TenantConfig
	EnvConfig           internal.EnvConfig
	CreatedERunConfig   bool
	CreatedTenantConfig bool
	CreatedEnvConfig    bool
}

type Service struct {
	Store                     Store
	FindProjectRoot           ProjectFinder
	GetWorkingDir             WorkDirFunc
	SelectTenant              SelectTenantFunc
	Confirm                   ConfirmFunc
	PromptKubernetesContext   PromptValueFunc
	PromptContainerRegistry   PromptValueFunc
	EnsureKubernetesNamespace NamespaceEnsurer
	LoadProjectConfig         ProjectConfigLoader
	SaveProjectConfig         ProjectConfigSaver
	Logger                    Logger
}

func (s Service) Run(req InitRequest) (InitResult, error) {
	s = s.withDefaults()
	req = normalizeRequest(req)

	var result InitResult
	var detected projectContext
	var tenants []internal.TenantConfig
	var tenantsLoaded bool
	var tenantConfirmed bool

	findProject := func() (projectContext, error) {
		if detected.loaded {
			return detected, nil
		}
		tenant, root, err := s.FindProjectRoot()
		if err != nil {
			return projectContext{}, err
		}
		detected = projectContext{
			tenant: tenant,
			root:   root,
			loaded: true,
		}
		return detected, nil
	}

	detectProject := func() (projectContext, error) {
		s.Logger.Trace("Trying to detect current project directory")
		project, err := findProject()
		if err != nil {
			if errors.Is(err, internal.ErrNotInGitRepository) {
				s.Logger.Error("erun config is not initialized. Run erun in project directory.")
				return projectContext{}, internal.MarkReported(err)
			}
			return projectContext{}, err
		}
		return project, nil
	}

	loadTenants := func() ([]internal.TenantConfig, error) {
		if tenantsLoaded {
			return tenants, nil
		}
		loadedTenants, err := s.Store.ListTenantConfigs()
		if err != nil {
			return nil, err
		}
		tenants = loadedTenants
		tenantsLoaded = true
		return tenants, nil
	}

	toolConfig, configPath, err := s.Store.LoadERunConfig()
	toolConfigMissing := false
	s.Logger.Trace("Loading erun tool configuration, configPath=" + configPath)
	switch {
	case err == nil:
	case errors.Is(err, internal.ErrNotInitialized):
		toolConfigMissing = true
		if req.ResolveTenant {
			break
		}
		tenant := req.Tenant
		projectRoot := req.ProjectRoot
		if tenant == "" || projectRoot == "" {
			project, detectErr := detectProject()
			if detectErr != nil {
				return result, detectErr
			}
			if tenant == "" {
				tenant = project.tenant
			}
			if projectRoot == "" {
				projectRoot = project.root
			}
		}

		if err := s.confirmTenant(req.AutoApprove, tenant, projectRoot); err != nil {
			return result, err
		}
		tenantConfirmed = true

		s.Logger.Trace("Saving default config")
		toolConfig = internal.ERunConfig{DefaultTenant: tenant}
		if err := s.Store.SaveERunConfig(toolConfig); err != nil {
			return result, err
		}
		result.CreatedERunConfig = true
		toolConfigMissing = false
	case err != nil:
		return result, err
	}
	s.Logger.Trace("Loaded erun tool configuration")

	tenant := req.Tenant
	if tenant == "" && req.ResolveTenant {
		loadedTenants, err := loadTenants()
		if err != nil {
			return result, err
		}
		if len(loadedTenants) > 0 {
			workingDir, err := s.GetWorkingDir()
			if err != nil {
				return result, err
			}
			if currentTenant, found := findTenantForDirectory(workingDir, loadedTenants); found {
				tenant = currentTenant.Name
			}
		}
	}
	if tenant == "" {
		tenant = toolConfig.DefaultTenant
	}
	if tenant == "" && req.ResolveTenant {
		loadedTenants, err := loadTenants()
		if err != nil {
			return result, err
		}
		if len(loadedTenants) > 0 {
			selection, err := s.selectTenant(loadedTenants)
			if err != nil {
				return result, err
			}
			if !selection.Initialize {
				tenant = selection.Tenant
			}
		}
	}
	if tenant == "" {
		project, detectErr := detectProject()
		if detectErr != nil {
			return result, detectErr
		}
		tenant = project.tenant
	}

	s.Logger.Trace("Loading tenant configuration")
	tenantConfig, _, err := s.Store.LoadTenantConfig(tenant)
	switch {
	case err == nil:
	case errors.Is(err, internal.ErrNotInitialized):
		projectRoot := req.ProjectRoot
		if projectRoot == "" {
			project, detectErr := detectProject()
			if detectErr != nil {
				return result, detectErr
			}
			projectRoot = project.root
		}

		if !tenantConfirmed {
			if err := s.confirmTenant(req.AutoApprove, tenant, projectRoot); err != nil {
				return result, err
			}
		}

		defaultEnvironment := req.Environment
		if defaultEnvironment == "" {
			defaultEnvironment = DefaultEnvironment
		}

		s.Logger.Trace("Adding new tenant")
		tenantConfig = internal.TenantConfig{
			Name:               tenant,
			ProjectRoot:        projectRoot,
			DefaultEnvironment: defaultEnvironment,
		}
		if err := s.Store.SaveTenantConfig(tenantConfig); err != nil {
			return result, err
		}
		result.CreatedTenantConfig = true
	case err != nil:
		return result, err
	}

	if tenantConfig.Name == "" {
		tenantConfig.Name = tenant
	}
	s.Logger.Trace("Loaded tenant configuration")

	envName := req.Environment
	if envName == "" {
		envName = tenantConfig.DefaultEnvironment
	}
	if envName == "" {
		envName = DefaultEnvironment
	}
	if tenantConfig.DefaultEnvironment == "" {
		tenantConfig.DefaultEnvironment = envName
		if err := s.Store.SaveTenantConfig(tenantConfig); err != nil {
			return result, err
		}
	}

	s.Logger.Trace("Loading environment configuration")
	envConfig, _, err := s.Store.LoadEnvConfig(tenant, envName)
	switch {
	case err == nil:
	case errors.Is(err, internal.ErrNotInitialized):
		envProjectRoot := req.ProjectRoot
		if envProjectRoot == "" {
			envProjectRoot = tenantConfig.ProjectRoot
		}
		if envProjectRoot == "" {
			project, detectErr := findProject()
			if detectErr == nil {
				envProjectRoot = project.root
			} else if !errors.Is(detectErr, internal.ErrNotInGitRepository) {
				return result, detectErr
			}
		}

		if err := s.confirmEnvironment(req.AutoApprove, tenant, envName); err != nil {
			return result, err
		}

		kubernetesContext, err := s.resolveKubernetesContext(req, tenant, envName, "")
		if err != nil {
			return result, err
		}
		if err := s.ensureKubernetesNamespace(tenant, envName, "", kubernetesContext); err != nil {
			return result, err
		}
		containerRegistry, err := s.resolveContainerRegistry(req, tenant, envName, envProjectRoot, "", true)
		if err != nil {
			return result, err
		}
		if err := s.saveProjectContainerRegistry(envProjectRoot, envName, containerRegistry); err != nil {
			return result, err
		}

		s.Logger.Trace("Adding new environment")
		envConfig = internal.EnvConfig{
			Name:              envName,
			RepoPath:          envProjectRoot,
			KubernetesContext: kubernetesContext,
		}
		if err := saveEnvConfig(s.Store, tenant, envConfig); err != nil {
			return result, err
		}
		envConfig.ContainerRegistry = containerRegistry
		result.CreatedEnvConfig = true
	case err != nil:
		return result, err
	}

	kubernetesContext, err := s.resolveKubernetesContext(req, tenant, envName, envConfig.KubernetesContext)
	if err != nil {
		return result, err
	}
	if kubernetesContext != envConfig.KubernetesContext {
		if err := s.ensureKubernetesNamespace(tenant, envName, envConfig.KubernetesContext, kubernetesContext); err != nil {
			return result, err
		}
		envConfig.KubernetesContext = kubernetesContext
		if err := saveEnvConfig(s.Store, tenant, envConfig); err != nil {
			return result, err
		}
	}
	projectRoot := envConfig.RepoPath
	if projectRoot == "" {
		projectRoot = tenantConfig.ProjectRoot
	}
	containerRegistry, err := s.resolveContainerRegistry(req, tenant, envName, projectRoot, envConfig.ContainerRegistry, false)
	if err != nil {
		return result, err
	}
	if containerRegistry != "" {
		if err := s.saveProjectContainerRegistry(projectRoot, envName, containerRegistry); err != nil {
			return result, err
		}
	}
	if containerRegistry != "" && containerRegistry != envConfig.ContainerRegistry {
		envConfig.ContainerRegistry = containerRegistry
		if err := saveEnvConfig(s.Store, tenant, envConfig); err != nil {
			return result, err
		}
	}

	if toolConfig.DefaultTenant == "" {
		s.Logger.Trace("Saving default config")
		toolConfig.DefaultTenant = tenant
		if err := s.Store.SaveERunConfig(toolConfig); err != nil {
			return result, err
		}
		if toolConfigMissing {
			result.CreatedERunConfig = true
		}
	}

	result.ERunConfig = toolConfig
	result.TenantConfig = tenantConfig
	result.EnvConfig = envConfig
	s.Logger.Trace("Configuration initialized OK")
	return result, nil
}

func normalizeRequest(req InitRequest) InitRequest {
	req.KubernetesContext = strings.TrimSpace(req.KubernetesContext)
	req.ContainerRegistry = strings.TrimSpace(req.ContainerRegistry)
	return req
}

func TenantConfirmationLabel(tenant, projectRoot string) string {
	return fmt.Sprintf(
		"Initialize tenant %q (path: %s) as the default tenant?",
		tenant,
		projectRoot,
	)
}

func EnvironmentConfirmationLabel(tenant, envName string) string {
	return fmt.Sprintf(
		"Initialize default environment %q for tenant %q?",
		envName,
		tenant,
	)
}

func KubernetesContextLabel(tenant, envName string) string {
	return fmt.Sprintf(
		"Kubernetes context for environment %q in tenant %q",
		envName,
		tenant,
	)
}

func ContainerRegistryLabel(tenant, envName string) string {
	return fmt.Sprintf(
		"Container registry for environment %q in tenant %q",
		envName,
		tenant,
	)
}

func KubernetesNamespaceName(tenant, envName string) string {
	return normalizeNamespaceName(tenant + "-" + envName)
}

func (s Service) withDefaults() Service {
	if s.Store == nil {
		s.Store = ConfigStore{}
	}
	if s.FindProjectRoot == nil {
		s.FindProjectRoot = internal.FindProjectRoot
	}
	if s.GetWorkingDir == nil {
		s.GetWorkingDir = os.Getwd
	}
	if s.Logger == nil {
		s.Logger = noopLogger{}
	}
	return s
}

func (s Service) confirmTenant(autoApprove bool, tenant, projectRoot string) error {
	if autoApprove {
		return nil
	}
	return s.confirm(TenantConfirmationLabel(tenant, projectRoot), ErrTenantInitializationCancelled)
}

func (s Service) confirmEnvironment(autoApprove bool, tenant, envName string) error {
	if autoApprove {
		return nil
	}
	return s.confirm(EnvironmentConfirmationLabel(tenant, envName), ErrEnvironmentInitializationCancelled)
}

func (s Service) confirm(label string, cancelled error) error {
	if s.Confirm == nil {
		return fmt.Errorf("confirmation required for %q", label)
	}
	confirmed, err := s.Confirm(label)
	if err != nil {
		return err
	}
	if !confirmed {
		return cancelled
	}
	return nil
}

func (s Service) resolveKubernetesContext(req InitRequest, tenant, envName, current string) (string, error) {
	if req.KubernetesContext != "" {
		return req.KubernetesContext, nil
	}

	current = strings.TrimSpace(current)
	if current != "" || req.AutoApprove {
		return current, nil
	}

	if s.PromptKubernetesContext == nil {
		return "", fmt.Errorf("kubernetes context prompt required")
	}

	context, err := s.PromptKubernetesContext(KubernetesContextLabel(tenant, envName))
	if err != nil {
		return "", err
	}
	context = strings.TrimSpace(context)
	if context == "" {
		return "", ErrKubernetesContextCancelled
	}
	return context, nil
}

func (s Service) resolveContainerRegistry(req InitRequest, tenant, envName, projectRoot, current string, creating bool) (string, error) {
	if req.ContainerRegistry != "" {
		return req.ContainerRegistry, nil
	}

	if projectRoot != "" && s.LoadProjectConfig != nil {
		projectConfig, _, err := s.LoadProjectConfig(projectRoot)
		if err != nil && !errors.Is(err, internal.ErrNotInitialized) {
			return "", err
		}
		if err == nil {
			projectRegistry := projectConfig.ContainerRegistryForEnvironment(envName)
			if projectRegistry != "" {
				return projectRegistry, nil
			}
		}
	}

	current = strings.TrimSpace(current)
	if current != "" {
		return current, nil
	}
	if !creating {
		return "", nil
	}
	if req.AutoApprove {
		return DefaultContainerRegistry, nil
	}
	if s.PromptContainerRegistry == nil {
		return "", fmt.Errorf("container registry prompt required")
	}

	registry, err := s.PromptContainerRegistry(ContainerRegistryLabel(tenant, envName))
	if err != nil {
		return "", err
	}
	registry = strings.TrimSpace(registry)
	if registry == "" {
		return DefaultContainerRegistry, nil
	}
	return registry, nil
}

func (s Service) saveProjectContainerRegistry(projectRoot, envName, registry string) error {
	if projectRoot == "" || envName == "" || registry == "" || s.SaveProjectConfig == nil {
		return nil
	}

	projectConfig := internal.ProjectConfig{}
	if s.LoadProjectConfig != nil {
		loadedConfig, _, err := s.LoadProjectConfig(projectRoot)
		if err != nil && !errors.Is(err, internal.ErrNotInitialized) {
			return err
		}
		if err == nil {
			projectConfig = loadedConfig
		}
	}

	if strings.TrimSpace(projectConfig.ContainerRegistry) == "" {
		if projectConfig.Environments != nil {
			if envConfig, ok := projectConfig.Environments[envName]; ok && strings.TrimSpace(envConfig.ContainerRegistry) == registry {
				return nil
			}
		}
	}

	projectConfig.SetContainerRegistryForEnvironment(envName, registry)
	return s.SaveProjectConfig(projectRoot, projectConfig)
}

func (s Service) ensureKubernetesNamespace(tenant, envName, currentContext, nextContext string) error {
	if s.EnsureKubernetesNamespace == nil {
		return nil
	}

	nextContext = strings.TrimSpace(nextContext)
	if nextContext == "" || nextContext == strings.TrimSpace(currentContext) {
		return nil
	}

	namespace := KubernetesNamespaceName(tenant, envName)
	if namespace == "" {
		return fmt.Errorf("kubernetes namespace name is empty for tenant %q and environment %q", tenant, envName)
	}

	return s.EnsureKubernetesNamespace(nextContext, namespace)
}

func (s Service) selectTenant(tenants []internal.TenantConfig) (TenantSelectionResult, error) {
	if s.SelectTenant == nil {
		return TenantSelectionResult{}, fmt.Errorf("tenant selection required")
	}
	selection, err := s.SelectTenant(tenants)
	if err != nil {
		return TenantSelectionResult{}, err
	}
	if selection.Initialize {
		return selection, nil
	}
	if selection.Tenant == "" {
		return TenantSelectionResult{}, ErrTenantSelectionCancelled
	}
	return selection, nil
}

func findTenantForDirectory(dir string, tenants []internal.TenantConfig) (internal.TenantConfig, bool) {
	cleanDir := filepath.Clean(dir)
	bestIndex := -1

	for i, tenant := range tenants {
		if tenant.ProjectRoot == "" {
			continue
		}
		if !isWithinDirectory(cleanDir, filepath.Clean(tenant.ProjectRoot)) {
			continue
		}
		if bestIndex == -1 || len(tenant.ProjectRoot) > len(tenants[bestIndex].ProjectRoot) {
			bestIndex = i
		}
	}

	if bestIndex == -1 {
		return internal.TenantConfig{}, false
	}
	return tenants[bestIndex], true
}

func isWithinDirectory(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func normalizeNamespaceName(value string) string {
	var builder strings.Builder
	lastHyphen := false

	for _, r := range strings.ToLower(value) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			builder.WriteRune(r)
			lastHyphen = false
		case !lastHyphen && builder.Len() > 0:
			builder.WriteByte('-')
			lastHyphen = true
		}
	}

	result := strings.Trim(builder.String(), "-")
	if len(result) > 63 {
		result = strings.Trim(result[:63], "-")
	}
	return result
}

func saveEnvConfig(store Store, tenant string, config internal.EnvConfig) error {
	stored := config
	stored.ContainerRegistry = ""
	return store.SaveEnvConfig(tenant, stored)
}

type projectContext struct {
	tenant string
	root   string
	loaded bool
}

type noopLogger struct{}

func (noopLogger) Trace(string) {}

func (noopLogger) Error(string) {}

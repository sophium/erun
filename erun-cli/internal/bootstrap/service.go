package bootstrap

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sophium/erun/internal"
)

const DefaultEnvironment = "local"

var (
	ErrTenantInitializationCancelled      = errors.New("tenant initialization cancelled by user")
	ErrEnvironmentInitializationCancelled = errors.New("environment initialization cancelled by user")
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
	CurrentBranchFinder func(string) (string, error)
	WorkDirFunc         func() (string, error)
	SelectTenantFunc    func([]internal.TenantConfig) (TenantSelectionResult, error)
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
	Tenant                  string
	ProjectRoot             string
	Environment             string
	Branch                  string
	DetectEnvironmentBranch bool
	AutoApprove             bool
	ResolveTenant           bool
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
	Store             Store
	FindProjectRoot   ProjectFinder
	FindCurrentBranch CurrentBranchFinder
	GetWorkingDir     WorkDirFunc
	SelectTenant      SelectTenantFunc
	Confirm           ConfirmFunc
	Logger            Logger
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

		envBranch := req.Branch
		if envBranch == "" && req.DetectEnvironmentBranch {
			detectedBranch, detectErr := s.FindCurrentBranch(envProjectRoot)
			if detectErr != nil && !errors.Is(detectErr, internal.ErrNotInGitRepository) {
				return result, detectErr
			}
			envBranch = detectedBranch
		}

		if err := s.confirmEnvironment(req.AutoApprove, tenant, envName, envBranch); err != nil {
			return result, err
		}

		s.Logger.Trace("Adding new environment")
		envConfig = internal.EnvConfig{
			Name:     envName,
			RepoPath: envProjectRoot,
			Branch:   envBranch,
		}
		if err := s.Store.SaveEnvConfig(tenant, envConfig); err != nil {
			return result, err
		}
		result.CreatedEnvConfig = true
	case err != nil:
		return result, err
	}

	if envConfig.Name == "" {
		envConfig.Name = envName
	}
	if req.Branch != "" && envConfig.Branch != req.Branch {
		envConfig.Branch = req.Branch
		if err := s.Store.SaveEnvConfig(tenant, envConfig); err != nil {
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
	return EnvironmentConfirmationLabelWithBranch(tenant, envName, "")
}

func EnvironmentConfirmationLabelWithBranch(tenant, envName, branch string) string {
	if branch == "" {
		return fmt.Sprintf(
			"Initialize default environment %q for tenant %q?",
			envName,
			tenant,
		)
	}
	return fmt.Sprintf(
		"Initialize default environment %q for tenant %q with default worktree branch %q?",
		envName,
		tenant,
		branch,
	)
}

func (s Service) withDefaults() Service {
	if s.Store == nil {
		s.Store = ConfigStore{}
	}
	if s.FindProjectRoot == nil {
		s.FindProjectRoot = internal.FindProjectRoot
	}
	if s.FindCurrentBranch == nil {
		s.FindCurrentBranch = internal.FindCurrentBranch
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

func (s Service) confirmEnvironment(autoApprove bool, tenant, envName, branch string) error {
	if autoApprove {
		return nil
	}
	return s.confirm(EnvironmentConfirmationLabelWithBranch(tenant, envName, branch), ErrEnvironmentInitializationCancelled)
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

type projectContext struct {
	tenant string
	root   string
	loaded bool
}

type noopLogger struct{}

func (noopLogger) Trace(string) {}

func (noopLogger) Error(string) {}

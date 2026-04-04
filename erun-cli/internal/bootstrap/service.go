package bootstrap

import (
	"errors"
	"fmt"

	"github.com/sophium/erun/internal"
)

const DefaultEnvironment = "local"

var (
	ErrTenantInitializationCancelled      = errors.New("tenant initialization cancelled by user")
	ErrEnvironmentInitializationCancelled = errors.New("environment initialization cancelled by user")
)

type Store interface {
	LoadERunConfig() (internal.ERunConfig, string, error)
	SaveERunConfig(internal.ERunConfig) error
	LoadTenantConfig(string) (internal.TenantConfig, string, error)
	SaveTenantConfig(internal.TenantConfig) error
	LoadEnvConfig(string, string) (internal.EnvConfig, string, error)
	SaveEnvConfig(string, internal.EnvConfig) error
}

type ProjectFinder func() (string, string, error)

type (
	ConfirmFunc func(label string) (bool, error)
	Logger      interface {
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
	Tenant      string
	ProjectRoot string
	Environment string
	AutoApprove bool
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
	Store           Store
	FindProjectRoot ProjectFinder
	Confirm         ConfirmFunc
	Logger          Logger
}

func (s Service) Run(req InitRequest) (InitResult, error) {
	s = s.withDefaults()
	req = normalizeRequest(req)

	var result InitResult
	var detected projectContext

	detectProject := func() (projectContext, error) {
		if detected.loaded {
			return detected, nil
		}
		s.Logger.Trace("Trying to detect current project directory")
		tenant, root, err := s.FindProjectRoot()
		if err != nil {
			if errors.Is(err, internal.ErrNotInGitRepository) {
				s.Logger.Error("erun config is not initialized. Run erun in project directory.")
				return projectContext{}, internal.MarkReported(err)
			}
			return projectContext{}, err
		}
		detected = projectContext{
			tenant: tenant,
			root:   root,
			loaded: true,
		}
		return detected, nil
	}

	toolConfig, configPath, err := s.Store.LoadERunConfig()
	s.Logger.Trace("Loading erun tool configuration, configPath=" + configPath)
	switch {
	case err == nil:
	case errors.Is(err, internal.ErrNotInitialized):
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

		s.Logger.Trace("Saving default config")
		toolConfig = internal.ERunConfig{DefaultTenant: tenant}
		if err := s.Store.SaveERunConfig(toolConfig); err != nil {
			return result, err
		}
		result.CreatedERunConfig = true
	case err != nil:
		return result, err
	}
	s.Logger.Trace("Loaded erun tool configuration")

	tenant := req.Tenant
	if tenant == "" {
		tenant = toolConfig.DefaultTenant
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
		if err := s.confirmEnvironment(req.AutoApprove, tenant, envName); err != nil {
			return result, err
		}

		s.Logger.Trace("Adding new environment")
		envConfig = internal.EnvConfig{Name: envName}
		if err := s.Store.SaveEnvConfig(tenant, envConfig); err != nil {
			return result, err
		}
		result.CreatedEnvConfig = true
	case err != nil:
		return result, err
	}

	if toolConfig.DefaultTenant == "" {
		toolConfig.DefaultTenant = tenant
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
	return fmt.Sprintf(
		"Initialize default environment %q for tenant %q?",
		envName,
		tenant,
	)
}

func (s Service) withDefaults() Service {
	if s.Store == nil {
		s.Store = ConfigStore{}
	}
	if s.FindProjectRoot == nil {
		s.FindProjectRoot = internal.FindProjectRoot
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

type projectContext struct {
	tenant string
	root   string
	loaded bool
}

type noopLogger struct{}

func (noopLogger) Trace(string) {}

func (noopLogger) Error(string) {}

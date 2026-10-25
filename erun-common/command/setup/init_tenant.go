package setup

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam"
	"github.com/sophium/erun/erun-common/primitive/open/runtimeref"
)

func (s *bootstrapRunState) loadTenants() ([]model.TenantConfig, error) {
	if s.tenantsLoaded {
		return s.tenants, nil
	}
	tenants, err := s.runner.Store.ListTenantConfigs()
	if err != nil {
		return nil, err
	}
	s.tenants = tenants
	s.tenantsLoaded = true
	return s.tenants, nil
}

func (s *bootstrapRunState) resolveDefaultTenant(tenant, projectRoot string) error {
	if s.defaultTenantResolved {
		return nil
	}
	updateDefaultTenant, err := s.runner.confirmTenant(s.params, tenant, projectRoot)
	if err != nil {
		return err
	}
	s.setDefaultTenant = updateDefaultTenant
	s.defaultTenantResolved = true
	return nil
}

func (s *bootstrapRunState) loadToolConfig() error {
	toolConfig, configPath, err := s.runner.Store.LoadERunConfig()
	s.toolConfig = toolConfig
	s.runner.Context.Trace("Loading erun tool configuration, configPath=" + configPath)
	switch {
	case err == nil:
	case errors.Is(err, configpkg.ErrNotInitialized):
		if err := s.initializeMissingToolConfig(); err != nil {
			return err
		}
	case err != nil:
		return err
	}
	s.runner.Context.Trace("Loaded erun tool configuration")
	return nil
}

func (s *bootstrapRunState) initializeMissingToolConfig() error {
	s.toolConfigMissing = true
	if s.params.ResolveTenant {
		return nil
	}
	tenant, projectRoot, err := s.defaultTenantCandidate()
	if err != nil {
		return err
	}
	if err := s.resolveDefaultTenant(tenant, projectRoot); err != nil {
		return err
	}
	if !s.setDefaultTenant {
		return nil
	}
	s.runner.Context.Trace("Saving default config")
	s.toolConfig = configpkg.ERunConfig{DefaultTenant: tenant}
	if err := s.runner.Store.SaveERunConfig(s.toolConfig); err != nil {
		return err
	}
	s.result.CreatedERunConfig = true
	s.toolConfigMissing = false
	return nil
}

func (s *bootstrapRunState) defaultTenantCandidate() (string, string, error) {
	tenant := s.params.Tenant
	projectRoot := s.params.ProjectRoot
	if s.params.RemoteWorktree() {
		return tenant, runtimeref.RemoteWorktreePathForRepoName(tenant), nil
	}
	if tenant != "" && projectRoot != "" {
		return tenant, projectRoot, nil
	}
	project, err := s.detectProject()
	if err != nil {
		return "", "", err
	}
	if tenant == "" {
		tenant = project.tenant
	}
	if projectRoot == "" {
		projectRoot = project.root
	}
	return tenant, projectRoot, nil
}

func (s *bootstrapRunState) resolveTenant() error {
	s.tenant = s.params.Tenant
	if err := s.resolveTenantFromProject(); err != nil {
		return err
	}
	if err := s.resolveTenantFromDirectory(); err != nil {
		return err
	}
	if s.tenant == "" && !s.params.RemoteWorktree() {
		s.tenant = s.toolConfig.DefaultTenant
	}
	if err := s.resolveTenantFromSelection(); err != nil {
		return err
	}
	if s.tenant == "" && !s.params.RemoteWorktree() {
		project, err := s.detectProject()
		if err != nil {
			return err
		}
		s.tenant = project.tenant
	}
	if s.params.RemoteWorktree() {
		s.params.ProjectRoot = runtimeref.RemoteWorktreePathForRepoName(s.tenant)
	}
	return nil
}

func (s *bootstrapRunState) resolveTenantFromProject() error {
	if s.tenant != "" || s.params.RemoteWorktree() {
		return nil
	}
	project, err := s.findProject()
	switch {
	case err == nil:
		s.tenant = project.tenant
	case errors.Is(err, configpkg.ErrNotInGitRepository):
	case err != nil:
		return err
	}
	return nil
}

func (s *bootstrapRunState) resolveTenantFromDirectory() error {
	if s.tenant != "" || !s.params.ResolveTenant {
		return nil
	}
	tenants, err := s.loadTenants()
	if err != nil {
		return err
	}
	if len(tenants) == 0 {
		return nil
	}
	workingDir, err := s.runner.GetWorkingDir()
	if err != nil {
		return err
	}
	if currentTenant, found := findTenantForDirectory(workingDir, tenants, s.envsByTenant(tenants)); found {
		s.tenant = currentTenant.Name
	}
	return nil
}

// envsByTenant is best-effort: load failures yield no envs for that tenant.
func (s *bootstrapRunState) envsByTenant(tenants []model.TenantConfig) map[string][]configpkg.EnvConfig {
	lister, ok := s.runner.Store.(interface {
		ListEnvConfigs(string) ([]configpkg.EnvConfig, error)
	})
	if !ok {
		return nil
	}
	envsByTenant := make(map[string][]configpkg.EnvConfig, len(tenants))
	for _, tenant := range tenants {
		if envs, err := lister.ListEnvConfigs(tenant.Name); err == nil {
			envsByTenant[tenant.Name] = envs
		}
	}
	return envsByTenant
}

func (s *bootstrapRunState) resolveTenantFromSelection() error {
	if s.tenant != "" || !s.params.ResolveTenant {
		return nil
	}
	tenants, err := s.loadTenants()
	if err != nil {
		return err
	}
	if len(tenants) == 0 {
		return nil
	}
	selection, err := s.runner.selectTenant(s.params, tenants)
	if err != nil {
		return err
	}
	if !selection.Initialize {
		s.tenant = selection.Tenant
	}
	return nil
}

func (s *bootstrapRunState) loadTenantConfig() error {
	s.runner.Context.Trace("Loading tenant configuration")
	tenantConfig, _, err := s.runner.Store.LoadTenantConfig(s.tenant)
	switch {
	case err == nil:
		s.tenantConfig = tenantConfig
	case errors.Is(err, configpkg.ErrNotInitialized):
		if err := s.createTenantConfig(); err != nil {
			return err
		}
	case err != nil:
		return err
	}
	s.normalizeTenantConfig()
	s.runner.Context.Trace("Loaded tenant configuration")
	return nil
}

func (s *bootstrapRunState) createTenantConfig() error {
	if err := configpkg.ValidateTenantName(s.tenant); err != nil {
		return err
	}
	projectRoot, err := s.tenantProjectRoot()
	if err != nil {
		return err
	}
	if err := s.resolveDefaultTenant(s.tenant, projectRoot); err != nil {
		return err
	}
	s.runner.Context.Trace("Adding new tenant")
	s.tenantConfig = model.TenantConfig{
		Name:               s.tenant,
		DefaultEnvironment: defaultBootstrapEnvironment(s.params.Environment),
	}
	if err := s.runner.Store.SaveTenantConfig(s.tenantConfig); err != nil {
		return err
	}
	s.result.CreatedTenantConfig = true
	return nil
}

func (s *bootstrapRunState) tenantProjectRoot() (string, error) {
	projectRoot := s.params.ProjectRoot
	if projectRoot != "" || s.params.RemoteWorktree() {
		return projectRoot, nil
	}
	project, err := s.detectProject()
	if err != nil {
		return "", err
	}
	return project.root, nil
}

func tenantNameList(tenants []model.TenantConfig) []string {
	names := make([]string, 0, len(tenants))
	for _, tenant := range tenants {
		names = append(names, tenant.Name)
	}
	return names
}

func (s BootstrapRunner) selectTenant(params seam.BootstrapInitParams, tenants []model.TenantConfig) (model.TenantSelectionResult, error) {
	if params.InitializeCurrentProject {
		return model.TenantSelectionResult{Initialize: true}, nil
	}
	if params.SelectedTenant != "" {
		return model.TenantSelectionResult{Tenant: params.SelectedTenant}, nil
	}
	if s.SelectTenant == nil {
		options := make([]string, 0, len(tenants)+1)
		for _, tenant := range tenants {
			options = append(options, tenant.Name)
		}
		options = append(options, configpkg.InitializeCurrentProject)
		return model.TenantSelectionResult{}, BootstrapInitInteractionError{Interaction: model.BootstrapInitInteraction{
			Type:    model.BootstrapInitInteractionSelectTenant,
			Label:   "Select tenant",
			Options: options,
		}}
	}
	if s.TenantSelectionUnavailable {
		// A sole tenant is what an empty answer would pick anyway.
		if len(tenants) == 1 {
			return model.TenantSelectionResult{Tenant: tenants[0].Name}, nil
		}
		return model.TenantSelectionResult{}, model.TenantSelectionUnavailableError{Tenants: tenantNameList(tenants)}
	}
	selection, err := s.SelectTenant(tenants)
	if err != nil {
		return model.TenantSelectionResult{}, err
	}
	if selection.Initialize {
		return selection, nil
	}
	if selection.Tenant == "" {
		return model.TenantSelectionResult{}, ErrTenantSelectionCancelled
	}
	return selection, nil
}

// findTenantForDirectory picks the longest matching env repo path; ties across
// tenants resolve to no match.
func findTenantForDirectory(dir string, tenants []model.TenantConfig, envsByTenant map[string][]configpkg.EnvConfig) (model.TenantConfig, bool) {
	cleanDir := filepath.Clean(dir)
	bestTenant := -1
	bestLen := -1
	ambiguous := false

	for i, tenant := range tenants {
		for _, env := range envsByTenant[tenant.Name] {
			repoPath := strings.TrimSpace(env.EffectiveLocalRepoPath())
			if repoPath == "" {
				continue
			}
			if !isWithinDirectory(cleanDir, filepath.Clean(repoPath)) {
				continue
			}
			switch {
			case len(repoPath) > bestLen:
				bestTenant, bestLen, ambiguous = i, len(repoPath), false
			case len(repoPath) == bestLen && i != bestTenant:
				ambiguous = true
			}
		}
	}

	if bestTenant == -1 || ambiguous {
		return model.TenantConfig{}, false
	}
	return tenants[bestTenant], true
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

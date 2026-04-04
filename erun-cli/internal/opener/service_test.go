package opener

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/internal"
)

func TestResolveUsesDefaultTenantAndEnvironment(t *testing.T) {
	repoPath := t.TempDir()
	service := Service{
		Store: openerStore{
			toolConfig: internal.ERunConfig{DefaultTenant: "tenant-a"},
			tenantConfigs: map[string]internal.TenantConfig{
				"tenant-a": {
					Name:               "tenant-a",
					ProjectRoot:        filepath.Join(t.TempDir(), "fallback"),
					DefaultEnvironment: "dev",
				},
			},
			envConfigs: map[string]internal.EnvConfig{
				"tenant-a/dev": {
					Name:     "dev",
					RepoPath: repoPath,
				},
			},
		},
	}

	result, err := service.Resolve(Request{
		UseDefaultTenant:      true,
		UseDefaultEnvironment: true,
	})
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if result.Tenant != "tenant-a" || result.Environment != "dev" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.RepoPath != repoPath || result.Title != "tenant-a-dev" {
		t.Fatalf("unexpected shell target: %+v", result)
	}
}

func TestResolveFallsBackToTenantProjectRoot(t *testing.T) {
	repoPath := t.TempDir()
	service := Service{
		Store: openerStore{
			tenantConfigs: map[string]internal.TenantConfig{
				"tenant-a": {
					Name:               "tenant-a",
					ProjectRoot:        repoPath,
					DefaultEnvironment: "dev",
				},
			},
			envConfigs: map[string]internal.EnvConfig{
				"tenant-a/dev": {Name: "dev"},
			},
		},
	}

	result, err := service.Resolve(Request{
		Tenant:      "tenant-a",
		Environment: "dev",
	})
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if result.RepoPath != repoPath {
		t.Fatalf("expected tenant project root fallback, got %+v", result)
	}
}

func TestResolveRequiresDefaultTenant(t *testing.T) {
	service := Service{Store: openerStore{loadERunErr: internal.ErrNotInitialized}}

	if _, err := service.Resolve(Request{UseDefaultTenant: true}); !errors.Is(err, ErrDefaultTenantNotConfigured) {
		t.Fatalf("expected ErrDefaultTenantNotConfigured, got %v", err)
	}
}

func TestResolveRequiresDefaultEnvironment(t *testing.T) {
	service := Service{
		Store: openerStore{
			toolConfig: internal.ERunConfig{DefaultTenant: "tenant-a"},
			tenantConfigs: map[string]internal.TenantConfig{
				"tenant-a": {Name: "tenant-a"},
			},
		},
	}

	if _, err := service.Resolve(Request{
		UseDefaultTenant:      true,
		UseDefaultEnvironment: true,
	}); !errors.Is(err, ErrDefaultEnvironmentNotConfigured) {
		t.Fatalf("expected ErrDefaultEnvironmentNotConfigured, got %v", err)
	}
}

func TestResolveReportsMissingTenant(t *testing.T) {
	service := Service{Store: openerStore{}}

	_, err := service.Resolve(Request{
		Tenant:      "dog",
		Environment: "me",
	})
	if !errors.Is(err, ErrTenantNotFound) {
		t.Fatalf("expected ErrTenantNotFound, got %v", err)
	}
	if got := err.Error(); got == "" || !strings.Contains(got, "no such tenant exists") {
		t.Fatalf("expected missing tenant message, got %q", got)
	}
}

func TestRunLaunchesShell(t *testing.T) {
	repoPath := t.TempDir()
	launched := ShellLaunchRequest{}
	service := Service{
		Store: openerStore{
			tenantConfigs: map[string]internal.TenantConfig{
				"tenant-a": {
					Name:               "tenant-a",
					ProjectRoot:        repoPath,
					DefaultEnvironment: "dev",
				},
			},
			envConfigs: map[string]internal.EnvConfig{
				"tenant-a/dev": {
					Name:     "dev",
					RepoPath: repoPath,
				},
			},
		},
		LaunchShell: func(req ShellLaunchRequest) error {
			launched = req
			return nil
		},
	}

	if _, err := service.Run(Request{Tenant: "tenant-a", Environment: "dev"}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if launched.Dir != repoPath || launched.Title != "tenant-a-dev" {
		t.Fatalf("unexpected shell launch: %+v", launched)
	}
}

type openerStore struct {
	toolConfig    internal.ERunConfig
	loadERunErr   error
	tenantConfigs map[string]internal.TenantConfig
	envConfigs    map[string]internal.EnvConfig
}

func (s openerStore) LoadERunConfig() (internal.ERunConfig, string, error) {
	if s.loadERunErr != nil {
		return internal.ERunConfig{}, "", s.loadERunErr
	}
	return s.toolConfig, "", nil
}

func (s openerStore) LoadTenantConfig(tenant string) (internal.TenantConfig, string, error) {
	config, ok := s.tenantConfigs[tenant]
	if !ok {
		return internal.TenantConfig{}, "", internal.ErrNotInitialized
	}
	return config, "", nil
}

func (s openerStore) LoadEnvConfig(tenant, environment string) (internal.EnvConfig, string, error) {
	config, ok := s.envConfigs[tenant+"/"+environment]
	if !ok {
		return internal.EnvConfig{}, "", internal.ErrNotInitialized
	}
	return config, "", nil
}

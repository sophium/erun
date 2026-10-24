package setup

import (
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
)

type platformAliasEmptyBootstrapStore struct{}

func (platformAliasEmptyBootstrapStore) LoadERunConfig() (config.ERunConfig, string, error) {
	return config.ERunConfig{}, "", nil
}
func (platformAliasEmptyBootstrapStore) SaveERunConfig(config.ERunConfig) error { return nil }
func (platformAliasEmptyBootstrapStore) ListTenantConfigs() ([]config.TenantConfig, error) {
	return nil, nil
}

func (platformAliasEmptyBootstrapStore) LoadTenantConfig(string) (config.TenantConfig, string, error) {
	return config.TenantConfig{}, "", config.ErrNotInitialized
}
func (platformAliasEmptyBootstrapStore) SaveTenantConfig(config.TenantConfig) error { return nil }
func (platformAliasEmptyBootstrapStore) LoadEnvConfig(string, string) (config.EnvConfig, string, error) {
	return config.EnvConfig{}, "", config.ErrNotInitialized
}
func (platformAliasEmptyBootstrapStore) SaveEnvConfig(string, config.EnvConfig) error { return nil }

// The host holds two aliases (ambiguous) while the injected store holds none.
func TestRemoteInitResolvesThePlatformAliasAgainstItsOwnStore(t *testing.T) {
	redirectConfigHomeForTest(t)
	if err := (config.ConfigStore{}).SaveERunConfig(config.ERunConfig{CloudProviders: []config.CloudProviderConfig{
		{Alias: "one@erun", Provider: config.CloudProviderERun},
		{Alias: "two@erun", Provider: config.CloudProviderERun},
	}}); err != nil {
		t.Fatalf("seed host config: %v", err)
	}

	runner := BootstrapRunner{
		BootstrapInitDependencies: BootstrapInitDependencies{Store: platformAliasEmptyBootstrapStore{}},
	}
	name, err := runner.resolvePlatformAliasSecret(config.OpenResult{Tenant: "acme", Environment: "dev"})
	if err != nil && strings.Contains(err.Error(), "multiple erun platform cloud provider aliases") {
		t.Fatalf("alias resolved against the host's config instead of the injected store: %v", err)
	}
	if err != nil {
		t.Fatalf("resolvePlatformAliasSecret: %v", err)
	}
	if name != "" {
		t.Fatalf("secret = %q, want none provisioned for a store with no alias", name)
	}
}

package eruncommon

import (
	"strings"
	"testing"
)

type platformAliasEmptyBootstrapStore struct{}

func (platformAliasEmptyBootstrapStore) LoadERunConfig() (ERunConfig, string, error) {
	return ERunConfig{}, "", nil
}
func (platformAliasEmptyBootstrapStore) SaveERunConfig(ERunConfig) error { return nil }
func (platformAliasEmptyBootstrapStore) ListTenantConfigs() ([]TenantConfig, error) {
	return nil, nil
}

func (platformAliasEmptyBootstrapStore) LoadTenantConfig(string) (TenantConfig, string, error) {
	return TenantConfig{}, "", ErrNotInitialized
}
func (platformAliasEmptyBootstrapStore) SaveTenantConfig(TenantConfig) error { return nil }
func (platformAliasEmptyBootstrapStore) LoadEnvConfig(string, string) (EnvConfig, string, error) {
	return EnvConfig{}, "", ErrNotInitialized
}
func (platformAliasEmptyBootstrapStore) SaveEnvConfig(string, EnvConfig) error { return nil }

// Init resolved the platform alias for a remote environment against the
// default config store rather than the store it was given, so a caller with
// its own store -- the MCP server, every test that injects one -- had the
// alias decided by whatever config happened to be on the host. Here the host
// holds two erun aliases, which the default store reads as an ambiguity, while
// the injected store holds none, which is no alias to provision.
func TestRemoteInitResolvesThePlatformAliasAgainstItsOwnStore(t *testing.T) {
	redirectConfigHomeForTest(t)
	if err := (ConfigStore{}).SaveERunConfig(ERunConfig{CloudProviders: []CloudProviderConfig{
		{Alias: "one@erun", Provider: CloudProviderERun},
		{Alias: "two@erun", Provider: CloudProviderERun},
	}}); err != nil {
		t.Fatalf("seed host config: %v", err)
	}

	runner := bootstrapRunner{
		BootstrapInitDependencies: BootstrapInitDependencies{Store: platformAliasEmptyBootstrapStore{}},
	}
	name, err := runner.resolvePlatformAliasSecret(OpenResult{Tenant: "acme", Environment: "dev"})
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

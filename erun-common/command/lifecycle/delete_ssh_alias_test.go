package lifecycle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common"
	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
)

// deleteSSHConfigStore is a minimal DeleteStore over an in-memory environment
// list, enough to exercise the local-state teardown RunDeleteEnvironment runs.
type deleteSSHConfigStore struct {
	envs map[string][]configpkg.EnvConfig
}

func (s *deleteSSHConfigStore) LoadTenantConfig(tenant string) (configpkg.TenantConfig, string, error) {
	return configpkg.TenantConfig{Name: tenant}, "", nil
}

func (s *deleteSSHConfigStore) SaveTenantConfig(configpkg.TenantConfig) error { return nil }

func (s *deleteSSHConfigStore) DeleteTenantConfig(string) error { return nil }

func (s *deleteSSHConfigStore) LoadERunConfig() (configpkg.ERunConfig, string, error) {
	return configpkg.ERunConfig{}, "", nil
}

func (s *deleteSSHConfigStore) SaveERunConfig(configpkg.ERunConfig) error { return nil }

func (s *deleteSSHConfigStore) LoadEnvConfig(tenant, environment string) (configpkg.EnvConfig, string, error) {
	for _, env := range s.envs[tenant] {
		if env.Name == environment {
			return env, filepath.Join("/tmp", tenant, environment), nil
		}
	}
	return configpkg.EnvConfig{}, "", configpkg.ErrNotInitialized
}

func (s *deleteSSHConfigStore) ListEnvConfigs(tenant string) ([]configpkg.EnvConfig, error) {
	return s.envs[tenant], nil
}

func (s *deleteSSHConfigStore) DeleteEnvConfig(tenant, environment string) error {
	kept := make([]configpkg.EnvConfig, 0, len(s.envs[tenant]))
	for _, env := range s.envs[tenant] {
		if env.Name != environment {
			kept = append(kept, env)
		}
	}
	s.envs[tenant] = kept
	return nil
}

// sandboxSSHConfig points HOME (and XDG, which the port-forward state files
// use) at a temp dir and returns the ssh config path inside it, so a test never
// touches the machine's real ~/.ssh/config.
func sandboxSSHConfig(t *testing.T) string {
	t.Helper()
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(homeDir, ".config"))
	return filepath.Join(homeDir, ".ssh", "config")
}

func writeSSHConfigBlock(t *testing.T, path, alias string, port int) {
	t.Helper()
	if err := configpkg.UpsertSSHConfig(path, configpkg.SSHHostEntry{
		Alias:        alias,
		HostKeyAlias: alias,
		HostName:     "127.0.0.1",
		Port:         port,
		User:         "erun",
	}); err != nil {
		t.Fatalf("write %s block: %v", alias, err)
	}
}

func readSSHConfig(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read ssh config: %v", err)
	}
	return string(data)
}

// The removal itself, including that a sibling env's block and a
// hand-maintained entry survive it, is pinned end to end through the compiled
// binary by
// erun-integration TestDelete/real_run_removes_the_deleted_envs_ssh_config_block.
// What stays here are the decisions that scenario cannot isolate: the
// alias-ownership guard, the dry-run preview, and the content-level rules.

func TestDeleteEnvironmentKeepsSSHConfigBlockAnotherEnvironmentOwns(t *testing.T) {
	configPath := sandboxSSHConfig(t)
	// Two names that sanitize to one alias: deleting dev_1 must not strip the
	// block that live env dev-1 resolves through.
	alias := eruncommon.SSHHostAlias("team", "dev_1")
	if alias != eruncommon.SSHHostAlias("team", "dev-1") {
		t.Fatalf("expected dev_1 and dev-1 to share an alias, got %q and %q", alias, eruncommon.SSHHostAlias("team", "dev-1"))
	}
	writeSSHConfigBlock(t, configPath, alias, 17022)

	store := &deleteSSHConfigStore{envs: map[string][]configpkg.EnvConfig{
		"team": {{Name: "dev_1"}, {Name: "dev-1"}},
	}}
	result, err := RunDeleteEnvironment(core.Context{}, DeleteEnvironmentParams{
		Tenant: "team", Environment: "dev_1",
	}, store, nil)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if result.RemovedSSHHostAlias != "" {
		t.Errorf("delete must not claim an alias a live environment still owns, got %q", result.RemovedSSHHostAlias)
	}
	if config := readSSHConfig(t, configPath); !strings.Contains(config, "Host "+alias) {
		t.Errorf("a live environment lost the block it owns:\n%s", config)
	}
}

func TestDeleteEnvironmentDryRunReportsBlockWithoutRemovingIt(t *testing.T) {
	configPath := sandboxSSHConfig(t)
	writeSSHConfigBlock(t, configPath, eruncommon.SSHHostAlias("team", "dev"), 17022)

	store := &deleteSSHConfigStore{envs: map[string][]configpkg.EnvConfig{"team": {{Name: "dev"}}}}
	result, err := RunDeleteEnvironment(core.Context{DryRun: true}, DeleteEnvironmentParams{
		Tenant: "team", Environment: "dev",
	}, store, nil)
	if err != nil {
		t.Fatalf("dry-run delete: %v", err)
	}
	if result.RemovedSSHHostAlias != "erun-team-dev" {
		t.Errorf("dry run should preview the removal, got %q", result.RemovedSSHHostAlias)
	}
	if config := readSSHConfig(t, configPath); !strings.Contains(config, "Host erun-team-dev") {
		t.Errorf("dry run removed the block:\n%s", config)
	}
}

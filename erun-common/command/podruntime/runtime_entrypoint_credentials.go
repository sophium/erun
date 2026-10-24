package podruntime

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/primitive/deploy"
	"gopkg.in/yaml.v3"
)

func (b runtimeBoot) dockerConfigPath() string {
	return b.path(".docker", "config.json")
}

func describeRuntimeRegistryCredential(boot runtimeBoot) string {
	if _, err := os.Stat(boot.registryCredentialPath); err != nil {
		return ""
	}
	return "add the registries " + boot.registryCredentialPath + " carries to " + boot.dockerConfigPath() + " where it has none"
}

// syncRuntimeRegistryCredential adds only registry hosts the pod lacks: the pod's own
// login is more current than what init resolved on the host.
func syncRuntimeRegistryCredential(_ core.Context, boot runtimeBoot, _ *runtimeSupervisor) error {
	provisioned, err := readRuntimeJSONObject(boot.registryCredentialPath)
	if err != nil {
		return err
	}
	provisionedAuths := runtimeJSONObjectField(provisioned, "auths")
	if len(provisionedAuths) == 0 {
		return nil
	}
	dest := boot.dockerConfigPath()
	existing, err := readRuntimeJSONObject(dest)
	if err != nil && !os.IsNotExist(err) {
		existing = map[string]json.RawMessage{}
	}
	if existing == nil {
		existing = map[string]json.RawMessage{}
	}
	auths := runtimeJSONObjectField(existing, "auths")
	changed := false
	for host, entry := range provisionedAuths {
		if _, ok := auths[host]; ok {
			continue
		}
		auths[host] = entry
		changed = true
	}
	if !changed {
		return nil
	}
	encodedAuths, err := json.Marshal(auths)
	if err != nil {
		return err
	}
	existing["auths"] = encodedAuths
	return writeRuntimeJSON(dest, existing)
}

// A file that is not a JSON object reads as empty so a corrupt config is replaced.
func readRuntimeJSONObject(path string) (map[string]json.RawMessage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	object := map[string]json.RawMessage{}
	if json.Unmarshal(data, &object) == nil && object != nil {
		return object, nil
	}
	return map[string]json.RawMessage{}, nil
}

func runtimeJSONObjectField(object map[string]json.RawMessage, key string) map[string]json.RawMessage {
	field := map[string]json.RawMessage{}
	if raw, ok := object[key]; ok {
		if json.Unmarshal(raw, &field) != nil || field == nil {
			return map[string]json.RawMessage{}
		}
	}
	return field
}

func writeRuntimeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

func (b runtimeBoot) platformAliasEntryPath() string {
	return filepath.Join(b.platformAliasDir, deploy.PlatformAliasEntryKey)
}

func describeRuntimePlatformAlias(boot runtimeBoot) string {
	if _, err := os.Stat(boot.platformAliasEntryPath()); err != nil {
		return ""
	}
	return "add the platform alias in " + boot.platformAliasDir + " to " + config.RuntimeRootConfigPath(boot.configHome()) + " unless it already carries it"
}

// syncRuntimePlatformAlias seeds the platform alias `erun init` minted so an unattended
// pod can call the platform API. Existing aliases and token files are kept.
func syncRuntimePlatformAlias(_ core.Context, boot runtimeBoot, _ *runtimeSupervisor) error {
	providers, err := readRuntimePlatformAliasEntry(boot.platformAliasEntryPath())
	if err != nil {
		return err
	}
	configHome := boot.configHome()
	root, _, err := config.LoadRuntimeRootConfigForSync(configHome)
	if err != nil {
		root = config.ERunConfig{}
	}
	changed := false
	for _, provider := range providers {
		if _, found := config.FindCloudProvider(root.CloudProviders, provider.Alias); found {
			continue
		}
		root.CloudProviders = cloud.UpsertCloudProvider(root.CloudProviders, provider)
		changed = true
	}
	if changed {
		if err := writeRuntimeBootYAML(config.RuntimeRootConfigPath(configHome), root); err != nil {
			return err
		}
	}
	return seedRuntimePlatformAliasToken(boot, configHome)
}

func readRuntimePlatformAliasEntry(path string) ([]config.CloudProviderConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var providers []config.CloudProviderConfig
	if err := yaml.Unmarshal(data, &providers); err != nil {
		return nil, fmt.Errorf("read the platform alias entry %s: %w", path, err)
	}
	return providers, nil
}

// The token file name ships from `erun init`, so nothing here re-derives it.
func seedRuntimePlatformAliasToken(boot runtimeBoot, configHome string) error {
	nameData, nameOK := readFileIfReadable(filepath.Join(boot.platformAliasDir, deploy.PlatformAliasSecretFileKey))
	token, tokenOK := readFileIfReadable(filepath.Join(boot.platformAliasDir, deploy.PlatformAliasSecretTokenKey))
	if !nameOK || !tokenOK {
		return nil
	}
	name := strings.TrimSpace(string(nameData))
	if name == "" || name != filepath.Base(name) || name == "." || name == ".." {
		return fmt.Errorf("the platform alias secret file name %q is not a plain file name", name)
	}
	dir := filepath.Join(configHome, config.ConfigRoot, "cloud-secrets")
	dest := filepath.Join(dir, name)
	if _, err := os.Stat(dest); err == nil {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	_ = os.Chmod(dir, 0o700)
	return os.WriteFile(dest, token, 0o600)
}

package setup

import (
	"os"
	"path/filepath"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/infra/cloud"
)

func testPlatformAliasProvider() configpkg.CloudProviderConfig {
	return configpkg.CloudProviderConfig{
		Alias:         "erun+api.erunpaas.com@erun",
		Provider:      configpkg.CloudProviderERun,
		Username:      "erun",
		AccountID:     "api.erunpaas.com",
		OIDCIssuerURL: "https://api.erunpaas.com",
		ERun: &configpkg.ERunProviderConfig{
			APIURL:          cloud.HostedPlatformAPIURL,
			ClientID:        "erun-cli",
			RefreshTokenRef: cloud.ErunRefreshTokenRef("erun+api.erunpaas.com@erun"),
		},
	}
}

type staticCloudStore struct {
	config configpkg.ERunConfig
}

func (s staticCloudStore) LoadERunConfig() (configpkg.ERunConfig, string, error) {
	return s.config, "", nil
}

func installCapturingKubectl(t *testing.T) *capturedManifest {
	t.Helper()
	dir := t.TempDir()
	capture := filepath.Join(dir, "stdin.yaml")
	script := "#!/bin/sh\ncat > " + capture + "\n"
	path := filepath.Join(dir, "kubectl-capture")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	t.Setenv("ERUN_KUBECTL_BIN", path)
	return &capturedManifest{path: capture}
}

type capturedManifest struct {
	path string
}

func (c *capturedManifest) read(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(c.path)
	if err != nil {
		t.Fatalf("kubectl stub captured nothing: %v", err)
	}
	return string(data)
}

func secondPlatformAliasProvider() configpkg.CloudProviderConfig {
	return configpkg.CloudProviderConfig{
		Alias:         "erun+api.acme.services.erunpaas.com@acme",
		Provider:      configpkg.CloudProviderERun,
		Username:      "acme",
		AccountID:     "api.acme.services.erunpaas.com",
		OIDCIssuerURL: "https://api.acme.services.erunpaas.com",
		ERun: &configpkg.ERunProviderConfig{
			APIURL:          cloud.HostedPlatformAPIURL,
			ClientID:        "erun-cli",
			RefreshTokenRef: cloud.ErunRefreshTokenRef("erun+api.acme.services.erunpaas.com@acme"),
		},
	}
}

// Both sessions are stored, so only the ambiguity can cause a decline.
func ambiguousHostStore(t *testing.T) staticCloudStore {
	t.Helper()
	redirectConfigHomeForTest(t)
	secretStore, err := cloud.DefaultCloudSecretStore()
	if err != nil {
		t.Fatalf("default cloud secret store: %v", err)
	}
	providers := []configpkg.CloudProviderConfig{testPlatformAliasProvider(), secondPlatformAliasProvider()}
	for _, provider := range providers {
		if err := secretStore.SaveCloudSecret(provider.ERun.RefreshTokenRef, "refresh-token-value"); err != nil {
			t.Fatalf("save refresh token for %s: %v", provider.Alias, err)
		}
	}
	if err := configpkg.SaveERunConfig(configpkg.ERunConfig{DefaultTenant: "team", CloudProviders: providers}); err != nil {
		t.Fatalf("save root config: %v", err)
	}
	return staticCloudStore{config: configpkg.ERunConfig{CloudProviders: providers}}
}

package podruntime

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud/providerconfig"
	"github.com/sophium/erun/erun-common/infra/cloud/providertoken"
	"github.com/sophium/erun/erun-common/infra/cloud/secretstore"
	"github.com/sophium/erun/erun-common/primitive/shared/cloudconfig"
	"gopkg.in/yaml.v3"
)

func testPlatformAliasProvider() model.CloudProviderConfig {
	return model.CloudProviderConfig{
		Alias:         "erun+api.erunpaas.com@erun",
		Provider:      configpkg.CloudProviderERun,
		Username:      "erun",
		AccountID:     "api.erunpaas.com",
		OIDCIssuerURL: "https://api.erunpaas.com",
		ERun: &model.ERunProviderConfig{
			APIURL:          providertoken.HostedPlatformAPIURL,
			ClientID:        "erun-cli",
			RefreshTokenRef: providertoken.ErunRefreshTokenRef("erun+api.erunpaas.com@erun"),
		},
	}
}

type staticCloudStore struct {
	config configpkg.ERunConfig
}

func (s staticCloudStore) LoadERunConfig() (configpkg.ERunConfig, string, error) {
	return s.config, "", nil
}

// TestProvisionedAliasResolvesInThePod covers the seam between init's render and the boot's seed.
func TestProvisionedAliasResolvesInThePod(t *testing.T) {
	boot := newTestRuntimeBoot(t, map[string]string{
		"ERUN_TENANT": "team", "ERUN_ENVIRONMENT": "dev",
		"ERUN_CLOUD_PROVIDER": "aws", "ERUN_CLOUD_PROVIDER_ALIAS": "dev-aws",
	})
	provider := writePlatformAliasSecret(t, boot)
	if err := writeRuntimeBootConfig(core.Context{}, boot, nil); err != nil {
		t.Fatalf("write the pod's config: %v", err)
	}
	rootPath := configpkg.RuntimeRootConfigPath(boot.configHome())
	unprovisioned, err := os.ReadFile(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	assertUnprovisionedAliasIsUnusable(t, string(unprovisioned))

	if err := syncRuntimePlatformAlias(core.Context{}, boot, nil); err != nil {
		t.Fatalf("seed the platform alias: %v", err)
	}
	var podConfig configpkg.ERunConfig
	readTestYAML(t, rootPath, &podConfig)

	resolved, err := cloudconfig.ResolveERunPlatformAlias(staticCloudStore{config: podConfig}, "")
	if err != nil {
		t.Fatalf("the pod still cannot resolve a platform alias after provisioning: %v", err)
	}
	if resolved.Alias != provider.Alias {
		t.Fatalf("got alias %q, want %q", resolved.Alias, provider.Alias)
	}
	store := secretstore.NewFileCloudSecretStore(filepath.Join(boot.configHome(), configpkg.ConfigRoot, "cloud-secrets"))
	token, err := store.LoadCloudSecret(resolved.ERun.RefreshTokenRef)
	if err != nil {
		t.Fatalf("the pod's secret store cannot read the token at the ref the resolved alias names: %v", err)
	}
	if token != "refresh-token-value" {
		t.Fatalf("got token %q, want the provisioned refresh token", token)
	}
}

func assertUnprovisionedAliasIsUnusable(t *testing.T, document string) {
	t.Helper()
	var before configpkg.ERunConfig
	if err := yaml.Unmarshal([]byte(document), &before); err != nil {
		t.Fatalf("the unprovisioned config does not parse: %v", err)
	}
	_, err := cloudconfig.ResolveERunPlatformAlias(staticCloudStore{config: before}, "")
	if !errors.Is(err, providerconfig.ErrPlatformAliasUnusable) {
		t.Fatalf("expected the reported unusable-alias failure before provisioning, got %v", err)
	}
	if !strings.Contains(err.Error(), "no erun platform cloud provider alias is configured") {
		t.Fatalf("the pre-provisioning failure should be the reported one, got: %v", err)
	}
}

package podruntime

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common"
	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/primitive/shared"
	"gopkg.in/yaml.v3"
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

// staticCloudStore is a CloudReadStore holding one already-resolved root config,
// so alias resolution is exercised without touching the operator's own config.
type staticCloudStore struct {
	config configpkg.ERunConfig
}

func (s staticCloudStore) LoadERunConfig() (configpkg.ERunConfig, string, error) {
	return s.config, "", nil
}

// TestProvisionedAliasResolvesInThePod is the reproduction of the reported
// failure. Before this change an agent pod's root config carried no erun alias
// and nothing could give it one, so every platform call resolved through
// newPlatformClientForAlias and aborted with "no erun platform cloud provider
// alias is configured" before its first network call -- `erun review show` among
// them, exiting 127, with no remedy reachable from inside the pod.
//
// This drives the pod's side for real: the entry init rendered, mounted where
// the chart mounts it, seeded by the runtime boot into the root config it wrote
// for an env whose chart injected an infrastructure provider, and the token
// written at the filename init shipped. It then asserts the pod's own alias
// resolution gets past the point that used to fail. It is the seam between the
// two halves -- init's render and the boot's seed -- which neither half's own
// test can cover.
func TestProvisionedAliasResolvesInThePod(t *testing.T) {
	boot := newTestRuntimeBoot(t, map[string]string{
		"ERUN_TENANT": "team", "ERUN_ENVIRONMENT": "dev",
		"ERUN_CLOUD_PROVIDER": "aws", "ERUN_CLOUD_PROVIDER_ALIAS": "dev-aws",
	})
	provider := writePlatformAliasSecret(t, boot)
	if err := writeRuntimeBootConfig(core.Context{}, boot, nil); err != nil {
		t.Fatalf("write the pod's config: %v", err)
	}
	rootPath := eruncommon.RuntimeRootConfigPath(boot.configHome())
	unprovisioned, err := os.ReadFile(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	// The reported state, asserted rather than assumed.
	assertUnprovisionedAliasIsUnusable(t, string(unprovisioned))

	if err := syncRuntimePlatformAlias(core.Context{}, boot, nil); err != nil {
		t.Fatalf("seed the platform alias: %v", err)
	}
	var podConfig configpkg.ERunConfig
	readTestYAML(t, rootPath, &podConfig)

	resolved, err := shared.ResolveERunPlatformAlias(staticCloudStore{config: podConfig}, "")
	if err != nil {
		t.Fatalf("the pod still cannot resolve a platform alias after provisioning: %v", err)
	}
	if resolved.Alias != provider.Alias {
		t.Fatalf("got alias %q, want %q", resolved.Alias, provider.Alias)
	}
	store := cloud.NewFileCloudSecretStore(filepath.Join(boot.configHome(), configpkg.ConfigRoot, "cloud-secrets"))
	token, err := store.LoadCloudSecret(resolved.ERun.RefreshTokenRef)
	if err != nil {
		t.Fatalf("the pod's secret store cannot read the token at the ref the resolved alias names: %v", err)
	}
	if token != "refresh-token-value" {
		t.Fatalf("got token %q, want the provisioned refresh token", token)
	}
}

// assertUnprovisionedAliasIsUnusable asserts the reported state rather than
// assuming it: a pod config carrying an infrastructure provider but no platform
// alias aborts at alias resolution, with the message and sentinel the report
// quoted, before any network call.
func assertUnprovisionedAliasIsUnusable(t *testing.T, document string) {
	t.Helper()
	var before configpkg.ERunConfig
	if err := yaml.Unmarshal([]byte(document), &before); err != nil {
		t.Fatalf("the unprovisioned config does not parse: %v", err)
	}
	_, err := shared.ResolveERunPlatformAlias(staticCloudStore{config: before}, "")
	if !errors.Is(err, cloud.ErrPlatformAliasUnusable) {
		t.Fatalf("expected the reported unusable-alias failure before provisioning, got %v", err)
	}
	if !strings.Contains(err.Error(), "no erun platform cloud provider alias is configured") {
		t.Fatalf("the pre-provisioning failure should be the reported one, got: %v", err)
	}
}

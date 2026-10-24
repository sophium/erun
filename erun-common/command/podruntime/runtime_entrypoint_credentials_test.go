package podruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/primitive/deploy"
)

func TestSyncRuntimeRegistryCredentialAddsOnlyMissingHosts(t *testing.T) {
	boot := newTestRuntimeBoot(t, nil)
	if describeRuntimeRegistryCredential(boot) != "" {
		t.Fatal("with nothing mounted the step has nothing to do")
	}
	writeAssetFile(t, boot.registryCredentialPath, `{"auths":{"ghcr.io":{"auth":"cHJvdmlzaW9uZWQ="},"quay.io":{"auth":"cXVheQ=="}}}`)
	writeAssetFile(t, boot.dockerConfigPath(), `{"auths":{"ghcr.io":{"auth":"b3BlcmF0b3I="},"docker.io":{"auth":"ZXhpc3Rpbmc="}},"credsStore":"keep"}`)
	if err := syncRuntimeRegistryCredential(core.Context{}, boot, nil); err != nil {
		t.Fatal(err)
	}
	config := readTestJSON(t, boot.dockerConfigPath())
	auths := testJSONObject(t, config["auths"])
	if testJSONObject(t, auths["ghcr.io"])["auth"] != "b3BlcmF0b3I=" {
		t.Fatalf("the pod's own ghcr.io login must not be overwritten: %v", auths)
	}
	if auths["quay.io"] == nil || auths["docker.io"] == nil || config["credsStore"] != "keep" {
		t.Fatalf("a missing host is added and everything else kept: %v", config)
	}
	info, err := os.Stat(boot.dockerConfigPath())
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("the docker config holds credentials and must be 0600: %v %v", info.Mode(), err)
	}
}

func writePlatformAliasSecret(t *testing.T, boot runtimeBoot) model.CloudProviderConfig {
	t.Helper()
	provider := testPlatformAliasProvider()
	entry, err := deploy.RenderPlatformAliasEntry(provider)
	if err != nil {
		t.Fatal(err)
	}
	writeAssetFile(t, boot.platformAliasEntryPath(), entry)
	writeAssetFile(t, filepath.Join(boot.platformAliasDir, deploy.PlatformAliasSecretFileKey), cloud.CloudSecretFileName(provider.ERun.RefreshTokenRef))
	writeAssetFile(t, filepath.Join(boot.platformAliasDir, deploy.PlatformAliasSecretTokenKey), "refresh-token-value")
	return provider
}

func TestRuntimeBootSeedsThePlatformAliasBesideTheInfrastructureProvider(t *testing.T) {
	boot := newTestRuntimeBoot(t, map[string]string{
		"ERUN_TENANT": "team", "ERUN_ENVIRONMENT": "dev",
		"ERUN_CLOUD_PROVIDER": "aws", "ERUN_CLOUD_PROVIDER_ALIAS": "dev-aws", "ERUN_CLOUD_REGION": "eu-west-1",
	})
	provider := writePlatformAliasSecret(t, boot)
	if err := writeRuntimeBootConfig(core.Context{}, boot, nil); err != nil {
		t.Fatal(err)
	}
	if err := syncRuntimePlatformAlias(core.Context{}, boot, nil); err != nil {
		t.Fatal(err)
	}
	rootPath := configpkg.RuntimeRootConfigPath(boot.configHome())
	data, _ := os.ReadFile(rootPath)
	if strings.Count(string(data), "cloudproviders:") != 1 {
		t.Fatalf("one cloudproviders key expected:\n%s", data)
	}
	var root configpkg.ERunConfig
	readTestYAML(t, rootPath, &root)
	if root.DefaultTenant != "team" {
		t.Fatalf("seeding the alias must not disturb the rest of the root config: %+v", root)
	}
	if _, found := configpkg.FindCloudProvider(root.CloudProviders, "dev-aws"); !found {
		t.Fatalf("the infrastructure provider must survive: %+v", root.CloudProviders)
	}
	if _, found := configpkg.FindCloudProvider(root.CloudProviders, provider.Alias); !found {
		t.Fatalf("the platform alias should be seeded: %+v", root.CloudProviders)
	}
	token := filepath.Join(boot.configHome(), configpkg.ConfigRoot, "cloud-secrets", cloud.CloudSecretFileName(provider.ERun.RefreshTokenRef))
	info, err := os.Stat(token)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("the token should be written 0600 at the shipped name: %v %v", info, err)
	}
	assertAssetFile(t, token, "refresh-token-value")
}

func TestRuntimeBootKeepsAPlatformAliasAndTokenThePodAlreadyHolds(t *testing.T) {
	boot := newTestRuntimeBoot(t, map[string]string{"ERUN_TENANT": "team", "ERUN_ENVIRONMENT": "dev"})
	provider := writePlatformAliasSecret(t, boot)
	token := filepath.Join(boot.configHome(), configpkg.ConfigRoot, "cloud-secrets", cloud.CloudSecretFileName(provider.ERun.RefreshTokenRef))
	writeAssetFile(t, token, "the pod's own newer token")
	own := provider
	own.Username = "pod-login"
	if err := writeRuntimeBootYAML(configpkg.RuntimeRootConfigPath(boot.configHome()), configpkg.ERunConfig{DefaultTenant: "team", CloudProviders: []model.CloudProviderConfig{own}}); err != nil {
		t.Fatal(err)
	}
	if err := syncRuntimePlatformAlias(core.Context{}, boot, nil); err != nil {
		t.Fatal(err)
	}
	var root configpkg.ERunConfig
	readTestYAML(t, configpkg.RuntimeRootConfigPath(boot.configHome()), &root)
	if got, _ := configpkg.FindCloudProvider(root.CloudProviders, provider.Alias); got.Username != "pod-login" {
		t.Fatalf("an alias the pod already carries must be kept: %+v", got)
	}
	assertAssetFile(t, token, "the pod's own newer token")
}

func TestRuntimeBootSeedsNoPlatformAliasWithoutAMountedSecret(t *testing.T) {
	boot := newTestRuntimeBoot(t, map[string]string{"ERUN_TENANT": "team", "ERUN_ENVIRONMENT": "dev"})
	if describeRuntimePlatformAlias(boot) != "" {
		t.Fatal("with nothing mounted the platform alias step has nothing to do")
	}
	if err := writeRuntimeBootConfig(core.Context{}, boot, nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(configpkg.RuntimeRootConfigPath(boot.configHome()))
	if err != nil {
		t.Fatalf("the root config should still be written: %v", err)
	}
	if strings.Contains(string(data), "cloudproviders") {
		t.Fatalf("no provider should be written without one injected:\n%s", data)
	}
}

package platformalias

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/cloud/providertoken"
	"github.com/sophium/erun/erun-common/infra/cloud/secretstore"
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

func signedInHostStore() staticCloudStore {
	return staticCloudStore{config: configpkg.ERunConfig{CloudProviders: []model.CloudProviderConfig{testPlatformAliasProvider()}}}
}

// A second `cloudproviders:` key is a duplicate mapping the reader refuses, so the
// alias must arrive as another item and parse back as two aliases.
func TestRenderPlatformAliasEntryCoexistsWithAnInfrastructureProvider(t *testing.T) {
	entry, err := RenderPlatformAliasEntry(testPlatformAliasProvider())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(entry, "cloudproviders:") {
		t.Fatalf("entry must not carry its own cloudproviders key:\n%s", entry)
	}
	infrastructure := "cloudproviders:\n" +
		"  - alias: dev-aws\n" +
		"    provider: aws\n" +
		"    profile: dev-profile\n"
	document := infrastructure + entry

	var config configpkg.ERunConfig
	if err := yaml.Unmarshal([]byte(document), &config); err != nil {
		t.Fatalf("provisioned config does not parse: %v\n%s", err, document)
	}
	if len(config.CloudProviders) != 2 {
		t.Fatalf("got %d cloud providers, want 2 (infrastructure + platform):\n%s", len(config.CloudProviders), document)
	}
	if config.CloudProviders[0].Alias != "dev-aws" {
		t.Errorf("infrastructure alias was displaced: %+v", config.CloudProviders[0])
	}
	platform := config.CloudProviders[1]
	if platform.Provider != configpkg.CloudProviderERun || platform.Alias != "erun+api.erunpaas.com@erun" {
		t.Fatalf("platform alias did not round-trip: %+v", platform)
	}
	if platform.ERun == nil || platform.ERun.APIURL != providertoken.HostedPlatformAPIURL {
		t.Fatalf("platform api configuration did not round-trip: %+v", platform.ERun)
	}
}

// A second implementation of the store's hash would yield a pod that looks
// provisioned but can never mint a token.
func TestRenderPlatformAliasEntryResolvesToTheSecretStorePath(t *testing.T) {
	provider := testPlatformAliasProvider()
	ref := provider.ERun.RefreshTokenRef
	store := secretstore.NewFileCloudSecretStore(filepath.Join(t.TempDir(), secretstore.CloudSecretStoreDirName))
	if err := store.SaveCloudSecret(ref, "refresh-token-value"); err != nil {
		t.Fatalf("save: %v", err)
	}
	fileStore, ok := store.(secretstore.FileCloudSecretStore)
	if !ok {
		t.Fatalf("store is %T, want cloud.FileCloudSecretStore", store)
	}
	written := filepath.Base(fileStore.Path(ref))
	if got := secretstore.CloudSecretFileName(ref); got != written {
		t.Fatalf("cloudSecretFileName = %q, but the store wrote %q", got, written)
	}
}

func TestProvisionPlatformAliasSecretReportsTheAmbiguityWithoutFailing(t *testing.T) {
	store := ambiguousHostStore(t)
	t.Setenv("ERUN_KUBECTL_BIN", failingBinaryPath(t))

	var log bytes.Buffer
	ctx := core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, &log, &log)}
	name, err := ProvisionPlatformAliasSecret(ctx, store, "team", "team-dev", "", cloud.DefaultCloudDependencies())
	if err != nil {
		t.Fatalf("an ambiguous host is a decline for init, not a failure: %v", err)
	}
	if name != "" {
		t.Fatalf("an ambiguous host has no alias to give, got %q", name)
	}
	if trace := log.String(); !strings.Contains(trace, "multiple erun platform cloud provider aliases are configured") {
		t.Fatalf("the ambiguity must be reported rather than read as an absent alias, got:\n%s", trace)
	}
}

func TestProvisionPlatformAliasSecretCarriesEntryFileNameAndToken(t *testing.T) {
	captured := installCapturingKubectl(t)
	deps := cloud.CloudDependencies{CloudSecretStore: secretstore.NewFileCloudSecretStore(filepath.Join(t.TempDir(), secretstore.CloudSecretStoreDirName))}
	provider := testPlatformAliasProvider()
	if err := deps.CloudSecretStore.SaveCloudSecret(provider.ERun.RefreshTokenRef, "refresh-token-value"); err != nil {
		t.Fatalf("save: %v", err)
	}

	name, err := ProvisionPlatformAliasSecret(testTraceContext(false), signedInHostStore(), "team", "team-dev", "", deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "team-devops-platform-alias"; name != want {
		t.Fatalf("got %q, want %q", name, want)
	}
	manifest := captured.read(t)
	for _, want := range []string{
		"kind: Secret",
		"name: team-devops-platform-alias",
		"namespace: team-dev",
		"type: Opaque",
		PlatformAliasEntryKey,
		PlatformAliasSecretFileKey,
		PlatformAliasSecretTokenKey,
		"alias: erun+api.erunpaas.com@erun",
		"apiurl: " + providertoken.HostedPlatformAPIURL,
		"refresh-token-value",
		secretstore.CloudSecretFileName(provider.ERun.RefreshTokenRef),
	} {
		if !strings.Contains(manifest, want) {
			t.Errorf("manifest missing %q:\n%s", want, manifest)
		}
	}
}

// installCapturingKubectl points ERUN_KUBECTL_BIN at a stub that records its stdin.
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

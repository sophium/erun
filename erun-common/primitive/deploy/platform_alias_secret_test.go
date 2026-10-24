package deploy

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/cloud"
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

func signedInHostStore() staticCloudStore {
	return staticCloudStore{config: configpkg.ERunConfig{CloudProviders: []configpkg.CloudProviderConfig{testPlatformAliasProvider()}}}
}

// TestRenderPlatformAliasEntryCoexistsWithAnInfrastructureProvider is the
// regression this file exists for. The pod's root config already renders a
// `cloudproviders:` key for the infrastructure provider the chart injected.
// Emitting a second key for the platform alias makes the document a duplicate
// mapping key, which the config reader refuses outright -- so the alias has to
// arrive as another *item*, indented to match, and the pair must parse back as
// two aliases rather than fail or silently drop one.
func TestRenderPlatformAliasEntryCoexistsWithAnInfrastructureProvider(t *testing.T) {
	entry, err := RenderPlatformAliasEntry(testPlatformAliasProvider())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(entry, "cloudproviders:") {
		t.Fatalf("entry must not carry its own cloudproviders key:\n%s", entry)
	}
	// The shape the pod's root config carries for the infrastructure
	// provider, reproduced here so the two items are proven to share one block.
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
	if platform.ERun == nil || platform.ERun.APIURL != cloud.HostedPlatformAPIURL {
		t.Fatalf("platform api configuration did not round-trip: %+v", platform.ERun)
	}
}

// TestRenderPlatformAliasEntryResolvesToTheSecretStorePath proves the shipped
// filename is the one the file store will later look for: the pod's entrypoint
// writes the token to whatever basename init shipped, and the store resolves the
// alias's ref through its own hash. Two independent implementations of that hash
// would mean a pod that looks provisioned and can never mint a token.
func TestRenderPlatformAliasEntryResolvesToTheSecretStorePath(t *testing.T) {
	provider := testPlatformAliasProvider()
	ref := provider.ERun.RefreshTokenRef
	store := cloud.NewFileCloudSecretStore(filepath.Join(t.TempDir(), cloud.CloudSecretStoreDirName))
	if err := store.SaveCloudSecret(ref, "refresh-token-value"); err != nil {
		t.Fatalf("save: %v", err)
	}
	fileStore, ok := store.(cloud.FileCloudSecretStore)
	if !ok {
		t.Fatalf("store is %T, want cloud.FileCloudSecretStore", store)
	}
	written := filepath.Base(fileStore.Path(ref))
	if got := cloud.CloudSecretFileName(ref); got != written {
		t.Fatalf("cloudSecretFileName = %q, but the store wrote %q", got, written)
	}
}

func TestResolveHostPlatformAliasNeedsASession(t *testing.T) {
	secretsDir := t.TempDir()
	deps := cloud.CloudDependencies{CloudSecretStore: cloud.NewFileCloudSecretStore(filepath.Join(secretsDir, cloud.CloudSecretStoreDirName))}

	t.Run("nil store", func(t *testing.T) {
		assertNoAliasToGive(t, nil, deps, "a nil config store")
	})

	t.Run("no configured alias", func(t *testing.T) {
		assertNoAliasToGive(t, staticCloudStore{}, deps, "a host with no erun alias")
	})

	t.Run("alias with no stored session", func(t *testing.T) {
		assertNoAliasToGive(t, signedInHostStore(), deps, "an alias whose token was never stored")
	})

	t.Run("nil secret store", func(t *testing.T) {
		assertNoAliasToGive(t, signedInHostStore(), cloud.CloudDependencies{}, "a host with no readable secret store")
	})

	t.Run("signed in", func(t *testing.T) {
		provider := testPlatformAliasProvider()
		if err := deps.CloudSecretStore.SaveCloudSecret(provider.ERun.RefreshTokenRef, "refresh-token-value"); err != nil {
			t.Fatalf("save: %v", err)
		}
		got, token, ok, err := ResolveHostPlatformAlias(signedInHostStore(), deps, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !ok {
			t.Fatal("a signed-in host must resolve its alias")
		}
		if got.Alias != provider.Alias {
			t.Fatalf("got alias %q, want %q", got.Alias, provider.Alias)
		}
		if token != "refresh-token-value" {
			t.Fatalf("got token %q, want the stored refresh token", token)
		}
	})
}

// assertNoAliasToGive asserts a resolve that declined for a reason no operator
// can act on: nothing to hand the environment, and nothing to tell them either.
// Every caller of resolveHostPlatformAlias treats that as a no-op, and the
// ambiguity is the only unnamed decline that is not one -- so a decline that
// quietly acquired a reason here is how the silence those callers rely on would
// turn into a line a routine deploy carries.
func assertNoAliasToGive(t *testing.T, store configpkg.CloudReadStore, deps cloud.CloudDependencies, what string) {
	t.Helper()
	_, _, ok, cause := ResolveHostPlatformAlias(store, deps, "")
	if ok {
		t.Fatalf("%s must resolve to nothing", what)
	}
	if cause != nil {
		t.Fatalf("%s is a host with nothing to give, not a reason to report: %v", what, cause)
	}
}

// TestResolveHostPlatformAliasIgnoresNonERunProviders proves the resolver is
// not satisfied by "some alias exists": an AWS alias is not a platform
// credential, and provisioning one would leave the pod exactly as blocked.
func TestResolveHostPlatformAliasIgnoresNonERunProviders(t *testing.T) {
	deps := cloud.CloudDependencies{CloudSecretStore: cloud.NewFileCloudSecretStore(filepath.Join(t.TempDir(), cloud.CloudSecretStoreDirName))}
	store := staticCloudStore{config: configpkg.ERunConfig{CloudProviders: []configpkg.CloudProviderConfig{
		{Alias: "dev-aws", Provider: configpkg.CloudProviderAWS},
	}}}
	if _, _, ok, _ := ResolveHostPlatformAlias(store, deps, ""); ok {
		t.Fatal("an infrastructure alias must not be provisioned as a platform alias")
	}
}

// TestProvisionPlatformAliasSecretReportsTheAmbiguityWithoutFailing is the init
// half of the same decline. A host with two aliases has nothing to hand the
// environment, which is no reason to fail init -- but it is a reason to say
// which of the two silences it is: the line a host with no alias at all gets
// names a cause that is not this one, and init is where the operator is
// watching.
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

// TestProvisionPlatformAliasSecretCarriesEntryFileNameAndToken applies the real
// manifest through a stub kubectl and reads back what the pod's entrypoint would
// consume: the alias entry, the hashed secret filename the store will look for,
// and the token itself.
func TestProvisionPlatformAliasSecretCarriesEntryFileNameAndToken(t *testing.T) {
	captured := installCapturingKubectl(t)
	deps := cloud.CloudDependencies{CloudSecretStore: cloud.NewFileCloudSecretStore(filepath.Join(t.TempDir(), cloud.CloudSecretStoreDirName))}
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
		"apiurl: " + cloud.HostedPlatformAPIURL,
		"refresh-token-value",
		cloud.CloudSecretFileName(provider.ERun.RefreshTokenRef),
	} {
		if !strings.Contains(manifest, want) {
			t.Errorf("manifest missing %q:\n%s", want, manifest)
		}
	}
}

// installCapturingKubectl points ERUN_KUBECTL_BIN at a stub that records its
// stdin, so the applied manifest can be asserted on rather than mocked away.
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

func TestHelmPlatformAliasSecretSetArgs(t *testing.T) {
	if got := helmPlatformAliasSecretSetArgs(""); got != nil {
		t.Fatalf("empty name must render no args, got %v", got)
	}
	got := helmPlatformAliasSecretSetArgs(" team-devops-platform-alias ")
	want := []string{"--set-string", "platformAliasSecretName=team-devops-platform-alias"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("got %v, want %v", got, want)
	}
}

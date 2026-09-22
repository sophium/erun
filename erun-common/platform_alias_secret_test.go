package eruncommon

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func testPlatformAliasProvider() CloudProviderConfig {
	return CloudProviderConfig{
		Alias:         "erun+api.erunpaas.com@erun",
		Provider:      CloudProviderERun,
		Username:      "erun",
		AccountID:     "api.erunpaas.com",
		OIDCIssuerURL: "https://api.erunpaas.com",
		ERun: &ERunProviderConfig{
			APIURL:          HostedPlatformAPIURL,
			ClientID:        "erun-cli",
			RefreshTokenRef: erunRefreshTokenRef("erun+api.erunpaas.com@erun"),
		},
	}
}

// staticCloudStore is a CloudReadStore holding one already-resolved root config,
// so alias resolution is exercised without touching the operator's own config.
type staticCloudStore struct {
	config ERunConfig
}

func (s staticCloudStore) LoadERunConfig() (ERunConfig, string, error) { return s.config, "", nil }

func signedInHostStore() staticCloudStore {
	return staticCloudStore{config: ERunConfig{CloudProviders: []CloudProviderConfig{testPlatformAliasProvider()}}}
}

func TestPlatformAliasSecretName(t *testing.T) {
	if got, want := platformAliasSecretName("team"), "team-devops-platform-alias"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestRenderPlatformAliasEntryCoexistsWithAnInfrastructureProvider is the
// regression this file exists for. The pod's root config already renders a
// `cloudproviders:` key for the infrastructure provider the chart injected.
// Emitting a second key for the platform alias makes the document a duplicate
// mapping key, which the config reader refuses outright -- so the alias has to
// arrive as another *item*, indented to match, and the pair must parse back as
// two aliases rather than fail or silently drop one.
func TestRenderPlatformAliasEntryCoexistsWithAnInfrastructureProvider(t *testing.T) {
	entry, err := renderPlatformAliasEntry(testPlatformAliasProvider())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(entry, "cloudproviders:") {
		t.Fatalf("entry must not carry its own cloudproviders key:\n%s", entry)
	}
	// The exact shape initialize_erun_config emits for the infrastructure
	// provider, reproduced here so the two items are proven to share one block.
	infrastructure := "cloudproviders:\n" +
		"  - alias: dev-aws\n" +
		"    provider: aws\n" +
		"    profile: dev-profile\n"
	document := infrastructure + entry

	var config ERunConfig
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
	if platform.Provider != CloudProviderERun || platform.Alias != "erun+api.erunpaas.com@erun" {
		t.Fatalf("platform alias did not round-trip: %+v", platform)
	}
	if platform.ERun == nil || platform.ERun.APIURL != HostedPlatformAPIURL {
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
	store := NewFileCloudSecretStore(filepath.Join(t.TempDir(), cloudSecretStoreDirName))
	if err := store.SaveCloudSecret(ref, "refresh-token-value"); err != nil {
		t.Fatalf("save: %v", err)
	}
	written := filepath.Base(store.(fileCloudSecretStore).path(ref))
	if got := cloudSecretFileName(ref); got != written {
		t.Fatalf("cloudSecretFileName = %q, but the store wrote %q", got, written)
	}
}

// TestProvisionedAliasResolvesInThePod is the reproduction of the reported
// failure. Before this change an agent pod's root config carried no erun alias
// and nothing could give it one, so every platform call resolved through
// newPlatformClientForAlias and aborted with "no erun platform cloud provider
// alias is configured" before its first network call -- `erun gate list` among
// them, exiting 127, with no remedy reachable from inside the pod.
//
// This composes on the pod's side exactly what the entrypoint composes in
// shell: the entry init rendered, placed in the root config initialize_erun_config
// writes, and the token written at the filename init shipped. It then asserts
// the pod's own alias resolution gets past the point that used to fail. It is
// the seam between the two halves -- init's render and the entrypoint's seed --
// which neither half's own test can cover.
func TestProvisionedAliasResolvesInThePod(t *testing.T) {
	provider := testPlatformAliasProvider()
	entry, err := renderPlatformAliasEntry(provider)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The root config as initialize_erun_config writes it for an env whose
	// chart injected an infrastructure provider -- with the entry, and without
	// it, so the reported state is asserted rather than assumed.
	unprovisioned := "defaulttenant: team\n" +
		"cloudproviders:\n" +
		"  - alias: dev-aws\n" +
		"    provider: aws\n"
	document := unprovisioned + entry

	assertUnprovisionedAliasIsUnusable(t, unprovisioned)

	var podConfig ERunConfig
	if err := yaml.Unmarshal([]byte(document), &podConfig); err != nil {
		t.Fatalf("the pod's provisioned config does not parse: %v\n%s", err, document)
	}

	// The token, at the basename init shipped rather than a name this test
	// picks -- that basename is the whole reason init computes it.
	dir := t.TempDir()
	store := NewFileCloudSecretStore(dir)
	if err := os.WriteFile(filepath.Join(dir, cloudSecretFileName(provider.ERun.RefreshTokenRef)), []byte("refresh-token-value"), 0o600); err != nil {
		t.Fatalf("write token at the shipped basename: %v", err)
	}

	resolved, err := ResolveERunPlatformAlias(staticCloudStore{config: podConfig}, "")
	if err != nil {
		t.Fatalf("the pod still cannot resolve a platform alias after provisioning: %v", err)
	}
	if resolved.Alias != provider.Alias {
		t.Fatalf("got alias %q, want %q", resolved.Alias, provider.Alias)
	}
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
	var before ERunConfig
	if err := yaml.Unmarshal([]byte(document), &before); err != nil {
		t.Fatalf("the unprovisioned config does not parse: %v", err)
	}
	_, err := ResolveERunPlatformAlias(staticCloudStore{config: before}, "")
	if !errors.Is(err, ErrPlatformAliasUnusable) {
		t.Fatalf("expected the reported unusable-alias failure before provisioning, got %v", err)
	}
	if !strings.Contains(err.Error(), "no erun platform cloud provider alias is configured") {
		t.Fatalf("the pre-provisioning failure should be the reported one, got: %v", err)
	}
}

func TestResolveHostPlatformAliasNeedsASession(t *testing.T) {
	secretsDir := t.TempDir()
	deps := CloudDependencies{CloudSecretStore: NewFileCloudSecretStore(filepath.Join(secretsDir, cloudSecretStoreDirName))}

	t.Run("nil store", func(t *testing.T) {
		if _, _, ok := resolveHostPlatformAlias(nil, deps); ok {
			t.Fatal("a nil config store must resolve to nothing")
		}
	})

	t.Run("no configured alias", func(t *testing.T) {
		if _, _, ok := resolveHostPlatformAlias(staticCloudStore{}, deps); ok {
			t.Fatal("a host with no erun alias must resolve to nothing")
		}
	})

	t.Run("alias with no stored session", func(t *testing.T) {
		if _, _, ok := resolveHostPlatformAlias(signedInHostStore(), deps); ok {
			t.Fatal("an alias whose token was never stored must resolve to nothing")
		}
	})

	t.Run("nil secret store", func(t *testing.T) {
		if _, _, ok := resolveHostPlatformAlias(signedInHostStore(), CloudDependencies{}); ok {
			t.Fatal("a host with no readable secret store must resolve to nothing")
		}
	})

	t.Run("signed in", func(t *testing.T) {
		provider := testPlatformAliasProvider()
		if err := deps.CloudSecretStore.SaveCloudSecret(provider.ERun.RefreshTokenRef, "refresh-token-value"); err != nil {
			t.Fatalf("save: %v", err)
		}
		got, token, ok := resolveHostPlatformAlias(signedInHostStore(), deps)
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

// TestResolveHostPlatformAliasIgnoresNonERunProviders proves the resolver is
// not satisfied by "some alias exists": an AWS alias is not a platform
// credential, and provisioning one would leave the pod exactly as blocked.
func TestResolveHostPlatformAliasIgnoresNonERunProviders(t *testing.T) {
	deps := CloudDependencies{CloudSecretStore: NewFileCloudSecretStore(filepath.Join(t.TempDir(), cloudSecretStoreDirName))}
	store := staticCloudStore{config: ERunConfig{CloudProviders: []CloudProviderConfig{
		{Alias: "dev-aws", Provider: CloudProviderAWS},
	}}}
	if _, _, ok := resolveHostPlatformAlias(store, deps); ok {
		t.Fatal("an infrastructure alias must not be provisioned as a platform alias")
	}
}

func TestProvisionPlatformAliasSecretNoSignedInHostAlias(t *testing.T) {
	t.Setenv("ERUN_KUBECTL_BIN", failingBinaryPath(t))
	deps := CloudDependencies{CloudSecretStore: NewFileCloudSecretStore(filepath.Join(t.TempDir(), cloudSecretStoreDirName))}

	name, err := provisionPlatformAliasSecret(testTraceContext(false), staticCloudStore{}, "team", "team-dev", "", deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "" {
		t.Fatalf("got %q, want empty when the host has no signed-in alias to give", name)
	}
}

func TestProvisionPlatformAliasSecretDryRunSkipsApply(t *testing.T) {
	t.Setenv("ERUN_KUBECTL_BIN", failingBinaryPath(t))
	deps := CloudDependencies{CloudSecretStore: NewFileCloudSecretStore(filepath.Join(t.TempDir(), cloudSecretStoreDirName))}
	provider := testPlatformAliasProvider()
	if err := deps.CloudSecretStore.SaveCloudSecret(provider.ERun.RefreshTokenRef, "refresh-token-value"); err != nil {
		t.Fatalf("save: %v", err)
	}

	name, err := provisionPlatformAliasSecret(testTraceContext(true), signedInHostStore(), "team", "team-dev", "", deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "team-devops-platform-alias"; name != want {
		t.Fatalf("got %q, want %q", name, want)
	}
}

// TestProvisionPlatformAliasSecretCarriesEntryFileNameAndToken applies the real
// manifest through a stub kubectl and reads back what the pod's entrypoint would
// consume: the alias entry, the hashed secret filename the store will look for,
// and the token itself.
func TestProvisionPlatformAliasSecretCarriesEntryFileNameAndToken(t *testing.T) {
	captured := installCapturingKubectl(t)
	deps := CloudDependencies{CloudSecretStore: NewFileCloudSecretStore(filepath.Join(t.TempDir(), cloudSecretStoreDirName))}
	provider := testPlatformAliasProvider()
	if err := deps.CloudSecretStore.SaveCloudSecret(provider.ERun.RefreshTokenRef, "refresh-token-value"); err != nil {
		t.Fatalf("save: %v", err)
	}

	name, err := provisionPlatformAliasSecret(testTraceContext(false), signedInHostStore(), "team", "team-dev", "", deps)
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
		platformAliasEntryKey,
		platformAliasSecretFileKey,
		platformAliasSecretTokenKey,
		"alias: erun+api.erunpaas.com@erun",
		"apiurl: " + HostedPlatformAPIURL,
		"refresh-token-value",
		cloudSecretFileName(provider.ERun.RefreshTokenRef),
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

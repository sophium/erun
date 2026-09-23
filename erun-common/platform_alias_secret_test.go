package eruncommon

import (
	"bytes"
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

// signedInDefaultHostStore is the host side of the retrofit as production
// resolves it: the real config store and the real default secret store, over
// whatever XDG config home the test redirected to. resolveHostPlatformAlias is
// reached with a static store everywhere else in this file, which cannot prove
// the deploying host's own config is what gets read.
func signedInDefaultHostStore(t *testing.T, provider CloudProviderConfig) {
	t.Helper()
	store, err := DefaultCloudSecretStore()
	if err != nil {
		t.Fatalf("default cloud secret store: %v", err)
	}
	if err := store.SaveCloudSecret(provider.ERun.RefreshTokenRef, "refresh-token-value"); err != nil {
		t.Fatalf("save refresh token: %v", err)
	}
	if err := SaveERunConfig(ERunConfig{DefaultTenant: "team", CloudProviders: []CloudProviderConfig{provider}}); err != nil {
		t.Fatalf("save root config: %v", err)
	}
}

// TestReconcilePlatformAliasSecretRetrofitsAnEnvironmentInitialisedWithoutOne is
// the reproduction of the reported failure. An environment initialised before
// anything provisioned a platform alias records no Secret name, so the runtime
// chart mounts nothing, so the entrypoint's seeder finds nothing to seed -- and
// nothing on the deploy path ever changed that, leaving the pod permanently
// unable to call the platform API (the reported `erun gate list` abort). Before
// this change a deploy of that environment carried no platform-alias Secret and
// no name for the chart to mount; this drives one against a host that is signed
// in and asserts the environment leaves the deploy with both, and with the record
// that keeps them on every later deploy.
func TestReconcilePlatformAliasSecretRetrofitsAnEnvironmentInitialisedWithoutOne(t *testing.T) {
	redirectConfigHomeForTest(t)
	captured := installCapturingKubectl(t)
	provider := testPlatformAliasProvider()
	signedInDefaultHostStore(t, provider)

	// The environment exactly as a pre-fix init left it: no platform-alias
	// Secret named anywhere.
	if err := SaveEnvConfig("team", EnvConfig{Name: "dev"}); err != nil {
		t.Fatalf("save env config: %v", err)
	}
	deployInput := HelmDeploySpec{
		Tenant:      "team",
		Environment: "dev",
		ReleaseName: RuntimeReleaseName("team"),
		Namespace:   "team-dev",
	}
	// The reported state, asserted rather than assumed: with no name recorded
	// the upgrade renders no platform-alias argument at all, which is what made
	// the chart mount nothing.
	if got := helmPlatformAliasSecretSetArgs(deployInput.PlatformAliasSecretName); got != nil {
		t.Fatalf("the unprovisioned deploy already named a platform alias: %v", got)
	}

	var log bytes.Buffer
	ctx := Context{Logger: NewLoggerWithWriters(VerbosityInfo, &log, &log)}
	if err := reconcilePlatformAliasSecret(ctx, &deployInput); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if want := "team-devops-platform-alias"; deployInput.PlatformAliasSecretName != want {
		t.Fatalf("the deploy still names no platform-alias Secret: got %q, want %q", deployInput.PlatformAliasSecretName, want)
	}
	manifest := captured.read(t)
	for _, want := range []string{
		"name: team-devops-platform-alias",
		"alias: " + provider.Alias,
		cloudSecretFileName(provider.ERun.RefreshTokenRef),
		"refresh-token-value",
	} {
		if !strings.Contains(manifest, want) {
			t.Errorf("the retrofitted Secret is missing %q:\n%s", want, manifest)
		}
	}
	if !strings.Contains(log.String(), "apply platform alias secret team-devops-platform-alias") {
		t.Errorf("the retrofit is not visible in the trace:\n%s", log.String())
	}

	// The record is what keeps it: without it the next deploy reads back an
	// empty field, and a deploy from a host that has since lost its alias would
	// pass no name and drop the mount from an environment that had one.
	recorded, _, err := LoadEnvConfig("team", "dev")
	if err != nil {
		t.Fatalf("load env config: %v", err)
	}
	if want := "team-devops-platform-alias"; recorded.PlatformAliasSecretName != want {
		t.Fatalf("the environment did not record the Secret: got %q, want %q", recorded.PlatformAliasSecretName, want)
	}
}

// TestReconcilePlatformAliasSecretLeavesAnEnvironmentThatAlreadyNamesOneAlone is
// the safety half: the Secret carries the delegating operator's own identity, so
// an environment that already names one is not re-provisioned from whoever
// happens to run this deploy. The host here is signed in, so only the gate stops
// the apply; a kubectl that must not run proves none happened.
func TestReconcilePlatformAliasSecretLeavesAnEnvironmentThatAlreadyNamesOneAlone(t *testing.T) {
	redirectConfigHomeForTest(t)
	t.Setenv("ERUN_KUBECTL_BIN", failingBinaryPath(t))
	signedInDefaultHostStore(t, testPlatformAliasProvider())
	if err := SaveEnvConfig("team", EnvConfig{Name: "dev", PlatformAliasSecretName: "team-devops-platform-alias"}); err != nil {
		t.Fatalf("save env config: %v", err)
	}

	deployInput := HelmDeploySpec{
		Tenant:                  "team",
		Environment:             "dev",
		ReleaseName:             RuntimeReleaseName("team"),
		Namespace:               "team-dev",
		PlatformAliasSecretName: "team-devops-platform-alias",
	}
	if err := reconcilePlatformAliasSecret(testTraceContext(false), &deployInput); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "team-devops-platform-alias"; deployInput.PlatformAliasSecretName != want {
		t.Fatalf("an already-provisioned environment's alias was changed: got %q, want %q", deployInput.PlatformAliasSecretName, want)
	}
	recorded, _, err := LoadEnvConfig("team", "dev")
	if err != nil {
		t.Fatalf("load env config: %v", err)
	}
	if want := "team-devops-platform-alias"; recorded.PlatformAliasSecretName != want {
		t.Fatalf("the recorded alias changed: got %q, want %q", recorded.PlatformAliasSecretName, want)
	}
}

// TestReconcilePlatformAliasSecretSilentlySkipsWhenTheHostHasNothingToGive
// pins the no-op: a host with no signed-in platform alias leaves the deploy
// byte-for-byte as it was -- no Secret applied, no name threaded, no line added
// to a trace every runtime deploy carries. Without the last part a deploy on a
// machine that never signed in would report a platform-alias decision it did
// not make.
func TestReconcilePlatformAliasSecretSilentlySkipsWhenTheHostHasNothingToGive(t *testing.T) {
	redirectConfigHomeForTest(t)
	t.Setenv("ERUN_KUBECTL_BIN", failingBinaryPath(t))
	if err := SaveEnvConfig("team", EnvConfig{Name: "dev"}); err != nil {
		t.Fatalf("save env config: %v", err)
	}

	deployInput := HelmDeploySpec{
		Tenant:      "team",
		Environment: "dev",
		ReleaseName: RuntimeReleaseName("team"),
		Namespace:   "team-dev",
	}
	var log bytes.Buffer
	ctx := Context{Logger: NewLoggerWithWriters(VerbosityInfo, &log, &log)}
	if err := reconcilePlatformAliasSecret(ctx, &deployInput); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deployInput.PlatformAliasSecretName != "" {
		t.Fatalf("got %q, want no name when the host has no alias to give", deployInput.PlatformAliasSecretName)
	}
	if got := log.String(); strings.TrimSpace(got) != "" {
		t.Fatalf("a host with nothing to give must add nothing to the trace, got:\n%s", got)
	}
	recorded, _, err := LoadEnvConfig("team", "dev")
	if err != nil {
		t.Fatalf("load env config: %v", err)
	}
	if recorded.PlatformAliasSecretName != "" {
		t.Fatalf("got %q, want the environment left unrecorded", recorded.PlatformAliasSecretName)
	}
}

// TestReconcilePlatformAliasSecretScopesItselfToTheRuntimeRelease keeps the
// retrofit off component releases, which never carry the platform-alias volume
// -- a component chart naming one would be a value nothing consumes.
func TestReconcilePlatformAliasSecretScopesItselfToTheRuntimeRelease(t *testing.T) {
	redirectConfigHomeForTest(t)
	t.Setenv("ERUN_KUBECTL_BIN", failingBinaryPath(t))
	signedInDefaultHostStore(t, testPlatformAliasProvider())

	deployInput := HelmDeploySpec{
		Tenant:      "team",
		Environment: "dev",
		ReleaseName: "team-dev-api",
		Namespace:   "team-dev",
	}
	if err := reconcilePlatformAliasSecret(testTraceContext(false), &deployInput); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deployInput.PlatformAliasSecretName != "" {
		t.Fatalf("a component release must not carry a platform alias, got %q", deployInput.PlatformAliasSecretName)
	}
}

// TestRetrofittedAliasRecordSurvivesTheDeploysOwnVersionPersistence is the
// reproduction of the reported failure. Every environment of a tenant whose
// hosts deploys it was found recording no platformaliassecretname, and the pod
// side of that tenant carried no platform-alias mount and no Secret at all.
//
// The retrofit writes the record while its own deploy is already under way --
// reconcilePlatformAliasSecret runs inside the pre-rollout step, after the
// deploy resolved the env config it will later persist from. That snapshot
// predates the write, so the version persistence that closes every ordinary
// deploy used to save it back wholesale and discard the record the deploy had
// just written: the Secret is applied, the chart mounts it for that rollout,
// and the environment is left naming nothing, so the next deploy reads an empty
// field and -- when its host has no alias to give -- passes no name at all,
// dropping the mount silently.
//
// This drives the two writes in the order an ordinary deploy performs them.
func TestRetrofittedAliasRecordSurvivesTheDeploysOwnVersionPersistence(t *testing.T) {
	redirectConfigHomeForTest(t)
	installCapturingKubectl(t)
	signedInDefaultHostStore(t, testPlatformAliasProvider())
	mustSaveEnvConfig(t, EnvConfig{Name: "dev"})

	// The spec's own view of the environment: resolved before the rollout, so
	// it cannot carry a name this deploy has not provisioned yet.
	spec := DeploySpec{
		Target: OpenResult{Tenant: "team", Environment: "dev", EnvConfig: mustLoadEnvConfig(t)},
		Deploy: HelmDeploySpec{
			Tenant:               "team",
			Environment:          "dev",
			ReleaseName:          RuntimeReleaseName("team"),
			Namespace:            "team-dev",
			Version:              "1.0.0",
			ResolvedRuntimeImage: "registry.example/test/team-devops:1.0.0",
		},
	}

	var log bytes.Buffer
	ctx := Context{Logger: NewLoggerWithWriters(VerbosityInfo, &log, &log)}
	if err := reconcilePlatformAliasSecret(ctx, &spec.Deploy); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if want := "team-devops-platform-alias"; spec.Deploy.PlatformAliasSecretName != want {
		t.Fatalf("the retrofit did not thread a name into the upgrade: got %q, want %q", spec.Deploy.PlatformAliasSecretName, want)
	}
	// The state the deploy leaves behind if nothing else writes, asserted so the
	// loss below is attributable to the persistence and not to the retrofit.
	if got := strings.TrimSpace(mustLoadEnvConfig(t).PlatformAliasSecretName); got != "team-devops-platform-alias" {
		t.Fatalf("the retrofit recorded nothing: got %q", got)
	}

	// The deploy then persists what it rolled out, from the snapshot it
	// resolved -- the write that used to discard the record above.
	if err := persistRuntimeVersionIfChanged(spec, "1.0.0", SaveEnvConfig); err != nil {
		t.Fatalf("persist runtime version: %v", err)
	}
	recorded := mustLoadEnvConfig(t)
	if got := strings.TrimSpace(recorded.PlatformAliasSecretName); got != "team-devops-platform-alias" {
		t.Fatalf("the deploy discarded the platform-alias record it had just written: got %q, want %q", got, "team-devops-platform-alias")
	}
	// The persistence still does its own job: the memo exists so downstream
	// readers see the version that actually rolled out.
	if recorded.RuntimeVersion != "1.0.0" {
		t.Fatalf("the runtime-version memo did not land: got %q", recorded.RuntimeVersion)
	}
}

// mustSaveEnvConfig and mustLoadEnvConfig are the "team"/"dev" environment's
// two config-store round trips, so a scenario that drives several writes in
// sequence reads as the sequence rather than as error plumbing.
func mustSaveEnvConfig(t *testing.T, config EnvConfig) {
	t.Helper()
	if err := SaveEnvConfig("team", config); err != nil {
		t.Fatalf("save env config: %v", err)
	}
}

func mustLoadEnvConfig(t *testing.T) EnvConfig {
	t.Helper()
	config, _, err := LoadEnvConfig("team", "dev")
	if err != nil {
		t.Fatalf("load env config: %v", err)
	}
	return config
}

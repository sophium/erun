package platformalias

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam/helmdeploy"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/cloud/providertoken"
	"github.com/sophium/erun/erun-common/infra/cloud/secretstore"
	"github.com/sophium/erun/erun-common/primitive/shared/cloudconfig"
)

func secondPlatformAliasProvider() model.CloudProviderConfig {
	return model.CloudProviderConfig{
		Alias:         "erun+api.acme.services.erunpaas.com@acme",
		Provider:      configpkg.CloudProviderERun,
		Username:      "acme",
		AccountID:     "api.acme.services.erunpaas.com",
		OIDCIssuerURL: "https://api.acme.services.erunpaas.com",
		ERun: &model.ERunProviderConfig{
			APIURL:          providertoken.HostedPlatformAPIURL,
			ClientID:        "erun-cli",
			RefreshTokenRef: providertoken.ErunRefreshTokenRef("erun+api.acme.services.erunpaas.com@acme"),
		},
	}
}

// ambiguousHostStore is signed in to two erun aliases, so only ambiguity can cause a decline.
func ambiguousHostStore(t *testing.T) staticCloudStore {
	t.Helper()
	redirectConfigHomeForTest(t)
	secretStore, err := secretstore.DefaultCloudSecretStore()
	if err != nil {
		t.Fatalf("default cloud secret store: %v", err)
	}
	providers := []model.CloudProviderConfig{testPlatformAliasProvider(), secondPlatformAliasProvider()}
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

func TestResolveHostPlatformAliasSelectsTheNamedAliasFromSeveral(t *testing.T) {
	store := ambiguousHostStore(t)
	deps := cloud.DefaultCloudDependencies()
	chosen := secondPlatformAliasProvider()

	got, token, ok, err := ResolveHostPlatformAlias(store, deps, chosen.Alias)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("an explicitly named alias on an ambiguous host must resolve")
	}
	if got.Alias != chosen.Alias {
		t.Fatalf("got alias %q, want the named %q", got.Alias, chosen.Alias)
	}
	if token != "refresh-token-value" {
		t.Fatalf("got token %q, want the named alias's stored refresh token", token)
	}

	_, _, ok, cause := ResolveHostPlatformAlias(store, deps, "")
	if ok {
		t.Fatal("an ambiguous host must not pick one of its aliases without a selection")
	}
	if !IsPlatformAliasAmbiguity(cause) {
		t.Fatalf("an ambiguous host must report the ambiguity rather than decline as an absence, got %v", cause)
	}
}

// Each transport words the cause differently, so it must be distinguishable without matching text.
func TestPlatformAliasAmbiguityIsNotTheAbsentAliasCause(t *testing.T) {
	several := staticCloudStore{config: configpkg.ERunConfig{CloudProviders: []model.CloudProviderConfig{
		testPlatformAliasProvider(), secondPlatformAliasProvider(),
	}}}

	_, err := cloudconfig.ResolveERunPlatformAlias(several, "")
	if err == nil {
		t.Fatal("several configured aliases with none selected must not resolve")
	}
	if !IsPlatformAliasAmbiguity(err) {
		t.Fatalf("several configured aliases must report the ambiguity, got: %v", err)
	}

	_, err = cloudconfig.ResolveERunPlatformAlias(staticCloudStore{}, "")
	if err == nil {
		t.Fatal("a host with no erun alias must not resolve")
	}
	if IsPlatformAliasAmbiguity(err) {
		t.Fatalf("an absent alias must not be reported as the ambiguity, got: %v", err)
	}
}

func TestResolveHostPlatformAliasReportsAnExplicitSelectionItCannotHonour(t *testing.T) {
	store := ambiguousHostStore(t)
	deps := cloud.DefaultCloudDependencies()
	const alias = "erun+api.nowhere.example@erun"

	t.Run("names nothing configured", func(t *testing.T) {
		assertSelectionRejected(t, store, deps, alias, "")
	})

	t.Run("names an infrastructure alias", func(t *testing.T) {
		awsStore := staticCloudStore{config: configpkg.ERunConfig{CloudProviders: []model.CloudProviderConfig{
			{Alias: "dev-aws", Provider: configpkg.CloudProviderAWS},
		}}}
		assertSelectionRejected(t, awsStore, deps, "dev-aws", "not an erun platform alias")
	})

	t.Run("names a configured alias with no session", func(t *testing.T) {
		empty := cloud.CloudDependencies{CloudSecretStore: secretstore.NewFileCloudSecretStore(filepath.Join(t.TempDir(), secretstore.CloudSecretStoreDirName))}
		assertSelectionRejected(t, store, empty, testPlatformAliasProvider().Alias, "erun cloud login")
	})

	t.Run("names an alias with no readable secret store", func(t *testing.T) {
		assertSelectionRejected(t, store, cloud.CloudDependencies{}, testPlatformAliasProvider().Alias, "--erun-alias")
	})
}

func assertSelectionRejected(t *testing.T, store staticCloudStore, deps cloud.CloudDependencies, alias, wantCause string) {
	t.Helper()
	_, _, ok, err := ResolveHostPlatformAlias(store, deps, alias)
	if err == nil {
		t.Fatalf("the selection %q cannot be honoured, so it must fail rather than decline", alias)
	}
	if ok {
		t.Fatal("a selection that failed must not also report a usable alias")
	}
	if !strings.Contains(err.Error(), "--erun-alias") || !strings.Contains(err.Error(), alias) {
		t.Errorf("the failure must name the selection and the alias, got: %v", err)
	}
	if wantCause != "" && !strings.Contains(err.Error(), wantCause) {
		t.Errorf("the failure must say %q, got: %v", wantCause, err)
	}
}

func TestReconcilePlatformAliasSecretHonoursTheOperatorsExplicitAlias(t *testing.T) {
	captured := installCapturingKubectl(t)
	ambiguousHostStore(t)
	chosen := secondPlatformAliasProvider()
	if err := configpkg.SaveEnvConfig("team", configpkg.EnvConfig{Name: "dev"}); err != nil {
		t.Fatalf("save env config: %v", err)
	}

	deployInput := helmdeploy.HelmDeploySpec{
		Tenant:      "team",
		Environment: "dev",
		ReleaseName: configpkg.RuntimeReleaseName("team"),
		Namespace:   "team-dev",
	}
	var log bytes.Buffer
	ctx := core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, &log, &log), PlatformAlias: chosen.Alias}
	if err := ReconcilePlatformAliasSecret(ctx, &deployInput); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if want := "team-devops-platform-alias"; deployInput.PlatformAliasSecretName != want {
		t.Fatalf("the deploy names no platform-alias Secret: got %q, want %q", deployInput.PlatformAliasSecretName, want)
	}
	manifest := captured.read(t)
	if !strings.Contains(manifest, "alias: "+chosen.Alias) {
		t.Errorf("the retrofitted Secret does not carry the alias the operator named (%s):\n%s", chosen.Alias, manifest)
	}
	if strings.Contains(manifest, "alias: "+testPlatformAliasProvider().Alias) {
		t.Errorf("the retrofit provisioned another configured alias instead of the named one:\n%s", manifest)
	}
	if !strings.Contains(log.String(), "(alias "+chosen.Alias) {
		t.Errorf("the trace does not name the alias that was provisioned:\n%s", log.String())
	}
	recorded, _, err := configpkg.LoadEnvConfig("team", "dev")
	if err != nil {
		t.Fatalf("load env config: %v", err)
	}
	if want := "team-devops-platform-alias"; recorded.PlatformAliasSecretName != want {
		t.Fatalf("the environment did not record the Secret: got %q, want %q", recorded.PlatformAliasSecretName, want)
	}
}

// Both sessions are signed in, so ambiguity is the only reason to decline.
func TestReconcilePlatformAliasSecretReportsAnAmbiguousHostWithoutTheFlag(t *testing.T) {
	ambiguousHostStore(t)
	t.Setenv("ERUN_KUBECTL_BIN", failingBinaryPath(t))
	if err := configpkg.SaveEnvConfig("team", configpkg.EnvConfig{Name: "dev"}); err != nil {
		t.Fatalf("save env config: %v", err)
	}

	deployInput := helmdeploy.HelmDeploySpec{
		Tenant:      "team",
		Environment: "dev",
		ReleaseName: configpkg.RuntimeReleaseName("team"),
		Namespace:   "team-dev",
	}
	var log bytes.Buffer
	ctx := core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, &log, &log)}
	if err := ReconcilePlatformAliasSecret(ctx, &deployInput); err != nil {
		t.Fatalf("a decline the operator never named is not a failed deploy: %v", err)
	}
	if deployInput.PlatformAliasSecretName != "" {
		t.Fatalf("an ambiguous host must still decline when no alias is named, got %q", deployInput.PlatformAliasSecretName)
	}
	trace := log.String()
	for _, want := range []string{
		"multiple erun platform cloud provider aliases are configured",
		"--erun-alias",
		"no alias was provisioned for this environment",
	} {
		if !strings.Contains(trace, want) {
			t.Errorf("the declined retrofit must say %q, got:\n%s", want, trace)
		}
	}
	recorded, _, err := configpkg.LoadEnvConfig("team", "dev")
	if err != nil {
		t.Fatalf("load env config: %v", err)
	}
	if recorded.PlatformAliasSecretName != "" {
		t.Fatalf("got %q, want the environment left unrecorded", recorded.PlatformAliasSecretName)
	}
}

func TestReconcilePlatformAliasSecretLeavesARecordedEnvironmentAloneOnAnAmbiguousHost(t *testing.T) {
	ambiguousHostStore(t)
	t.Setenv("ERUN_KUBECTL_BIN", failingBinaryPath(t))
	if err := configpkg.SaveEnvConfig("team", configpkg.EnvConfig{Name: "dev", PlatformAliasSecretName: "team-devops-platform-alias"}); err != nil {
		t.Fatalf("save env config: %v", err)
	}

	deployInput := helmdeploy.HelmDeploySpec{
		Tenant:                  "team",
		Environment:             "dev",
		ReleaseName:             configpkg.RuntimeReleaseName("team"),
		Namespace:               "team-dev",
		PlatformAliasSecretName: "team-devops-platform-alias",
	}
	var log bytes.Buffer
	ctx := core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, &log, &log)}
	if err := ReconcilePlatformAliasSecret(ctx, &deployInput); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "team-devops-platform-alias"; deployInput.PlatformAliasSecretName != want {
		t.Fatalf("an already-provisioned environment's alias was changed: got %q, want %q", deployInput.PlatformAliasSecretName, want)
	}
	if got := log.String(); strings.TrimSpace(got) != "" {
		t.Fatalf("a recorded environment owes the trace nothing, got:\n%s", got)
	}
	recorded, _, err := configpkg.LoadEnvConfig("team", "dev")
	if err != nil {
		t.Fatalf("load env config: %v", err)
	}
	if want := "team-devops-platform-alias"; recorded.PlatformAliasSecretName != want {
		t.Fatalf("the recorded alias changed: got %q, want %q", recorded.PlatformAliasSecretName, want)
	}
}

func TestReconcilePlatformAliasSecretSaysWhenAnExplicitAliasWasNotUsed(t *testing.T) {
	redirectConfigHomeForTest(t)
	t.Setenv("ERUN_KUBECTL_BIN", failingBinaryPath(t))
	signedInDefaultHostStore(t, testPlatformAliasProvider())
	if err := configpkg.SaveEnvConfig("team", configpkg.EnvConfig{Name: "dev", PlatformAliasSecretName: "team-devops-platform-alias"}); err != nil {
		t.Fatalf("save env config: %v", err)
	}

	deployInput := helmdeploy.HelmDeploySpec{
		Tenant:                  "team",
		Environment:             "dev",
		ReleaseName:             configpkg.RuntimeReleaseName("team"),
		Namespace:               "team-dev",
		PlatformAliasSecretName: "team-devops-platform-alias",
	}
	var log bytes.Buffer
	ctx := core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, &log, &log), PlatformAlias: secondPlatformAliasProvider().Alias}
	if err := ReconcilePlatformAliasSecret(ctx, &deployInput); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "team-devops-platform-alias"; deployInput.PlatformAliasSecretName != want {
		t.Fatalf("an already-provisioned environment's alias was changed: got %q, want %q", deployInput.PlatformAliasSecretName, want)
	}
	trace := log.String()
	for _, want := range []string{"already records team-devops-platform-alias", "--erun-alias " + secondPlatformAliasProvider().Alias} {
		if !strings.Contains(trace, want) {
			t.Errorf("the trace must say the selection went unused (%q missing):\n%s", want, trace)
		}
	}
}

func TestReconcilePlatformAliasSecretReportsAnExplicitAliasOnADeploy(t *testing.T) {
	ambiguousHostStore(t)
	t.Setenv("ERUN_KUBECTL_BIN", failingBinaryPath(t))
	if err := configpkg.SaveEnvConfig("team", configpkg.EnvConfig{Name: "dev"}); err != nil {
		t.Fatalf("save env config: %v", err)
	}

	const alias = "erun+api.nowhere.example@erun"
	deployInput := helmdeploy.HelmDeploySpec{
		Tenant:      "team",
		Environment: "dev",
		ReleaseName: configpkg.RuntimeReleaseName("team"),
		Namespace:   "team-dev",
	}
	var log bytes.Buffer
	ctx := core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, &log, &log), PlatformAlias: alias}
	err := ReconcilePlatformAliasSecret(ctx, &deployInput)
	if err == nil {
		t.Fatal("a named alias that cannot be used must fail the deploy rather than decline")
	}
	if !strings.Contains(err.Error(), alias) {
		t.Fatalf("the failure must name the alias the operator gave, got: %v", err)
	}
	if deployInput.PlatformAliasSecretName != "" {
		t.Fatalf("a failed provisioning must not thread a name into the upgrade, got %q", deployInput.PlatformAliasSecretName)
	}
	recorded, _, loadErr := configpkg.LoadEnvConfig("team", "dev")
	if loadErr != nil {
		t.Fatalf("load env config: %v", loadErr)
	}
	if recorded.PlatformAliasSecretName != "" {
		t.Fatalf("a failed provisioning must not record a Secret, got %q", recorded.PlatformAliasSecretName)
	}
}

func TestReconcilePlatformAliasSecretScopesItselfToTheRuntimeRelease(t *testing.T) {
	redirectConfigHomeForTest(t)
	t.Setenv("ERUN_KUBECTL_BIN", failingBinaryPath(t))
	signedInDefaultHostStore(t, testPlatformAliasProvider())

	deployInput := helmdeploy.HelmDeploySpec{
		Tenant:      "team",
		Environment: "dev",
		ReleaseName: "team-dev-api",
		Namespace:   "team-dev",
	}
	if err := ReconcilePlatformAliasSecret(testTraceContext(false), &deployInput); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deployInput.PlatformAliasSecretName != "" {
		t.Fatalf("a component release must not carry a platform alias, got %q", deployInput.PlatformAliasSecretName)
	}
}

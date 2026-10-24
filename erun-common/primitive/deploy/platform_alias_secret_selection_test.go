package deploy

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// secondPlatformAliasProvider is a second erun-type alias on the same host:
// what makes resolution ambiguous, and therefore what the operator's own
// selection has to disambiguate.
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

// ambiguousHostStore is a host configured with two erun-type aliases and
// signed in to both: the state the sole-alias default cannot answer, and the
// state the explicit selection exists for. Both sessions are stored so the only
// reason to decline is the ambiguity itself, never a missing token.
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

// TestResolveHostPlatformAliasSelectsTheNamedAliasFromSeveral pins the selection
// itself, away from the deploy path. With two erun aliases configured an
// explicit name decides which one is resolved; with none the host still
// declines rather than guessing, which is the behaviour every caller had before
// the selection existed and the one a flag must not quietly replace. The
// decline comes back as the ambiguity rather than as nothing, because it is the
// one decline the operator named nothing for that they can still lift.
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

// TestPlatformAliasAmbiguityIsNotTheAbsentAliasCause pins the one distinction
// the retrofit's decline turns on: several erun aliases with none selected,
// versus no erun alias at all. The resolver has always reported both as the
// same silence, and only the first is the operator's to lift, so they have to
// be tellable apart without matching on wording -- which each transport spells
// its own way, naming its own selection.
func TestPlatformAliasAmbiguityIsNotTheAbsentAliasCause(t *testing.T) {
	several := staticCloudStore{config: configpkg.ERunConfig{CloudProviders: []configpkg.CloudProviderConfig{
		testPlatformAliasProvider(), secondPlatformAliasProvider(),
	}}}

	_, err := shared.ResolveERunPlatformAlias(several, "")
	if err == nil {
		t.Fatal("several configured aliases with none selected must not resolve")
	}
	if !IsPlatformAliasAmbiguity(err) {
		t.Fatalf("several configured aliases must report the ambiguity, got: %v", err)
	}

	_, err = shared.ResolveERunPlatformAlias(staticCloudStore{}, "")
	if err == nil {
		t.Fatal("a host with no erun alias must not resolve")
	}
	if IsPlatformAliasAmbiguity(err) {
		t.Fatalf("an absent alias must not be reported as the ambiguity, got: %v", err)
	}
}

// TestResolveHostPlatformAliasReportsAnExplicitSelectionItCannotHonour pins the
// other half of the explicit path: a name the operator gave is never a silent
// no-op, whatever stopped it being usable. Each failure has to say what could
// not happen and what to do next, because "the retrofit declined" is
// indistinguishable from "the retrofit never ran" once the deploy has reported
// success.
func TestResolveHostPlatformAliasReportsAnExplicitSelectionItCannotHonour(t *testing.T) {
	store := ambiguousHostStore(t)
	deps := cloud.DefaultCloudDependencies()
	const alias = "erun+api.nowhere.example@erun"

	t.Run("names nothing configured", func(t *testing.T) {
		assertSelectionRejected(t, store, deps, alias, "")
	})

	t.Run("names an infrastructure alias", func(t *testing.T) {
		awsStore := staticCloudStore{config: configpkg.ERunConfig{CloudProviders: []configpkg.CloudProviderConfig{
			{Alias: "dev-aws", Provider: configpkg.CloudProviderAWS},
		}}}
		assertSelectionRejected(t, awsStore, deps, "dev-aws", "not an erun platform alias")
	})

	t.Run("names a configured alias with no session", func(t *testing.T) {
		// A secret store holding nothing, so the only thing missing is the
		// session -- the state a fresh machine is in for an alias it has been
		// told about but never signed in to.
		empty := cloud.CloudDependencies{CloudSecretStore: cloud.NewFileCloudSecretStore(filepath.Join(t.TempDir(), cloud.CloudSecretStoreDirName))}
		assertSelectionRejected(t, store, empty, testPlatformAliasProvider().Alias, "erun cloud login")
	})

	t.Run("names an alias with no readable secret store", func(t *testing.T) {
		assertSelectionRejected(t, store, cloud.CloudDependencies{}, testPlatformAliasProvider().Alias, "--erun-alias")
	})
}

// assertSelectionRejected drives one unnamed-or-unusable selection and asserts
// it fails the way the flag's contract requires: an error naming the selection
// and the alias, never a usable alias alongside it. wantCause, when set, is the
// phrase the failure owes the operator -- the reason, or the command that fixes
// it.
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

// TestReconcilePlatformAliasSecretHonoursTheOperatorsExplicitAlias is the
// direction the opt-in exists for. A host signed in to more than one erun-type
// alias has no unambiguous answer to "whose identity", so the retrofit declined
// on it and the environment stayed unable to call the platform with nothing
// saying why -- while the desktop resolves that same choice from the tenant's
// own primary alias. This drives an explicit selection end to end and asserts
// the Secret the pod mounts carries the alias the operator named, not the first
// one configured.
func TestReconcilePlatformAliasSecretHonoursTheOperatorsExplicitAlias(t *testing.T) {
	captured := installCapturingKubectl(t)
	ambiguousHostStore(t)
	chosen := secondPlatformAliasProvider()
	if err := configpkg.SaveEnvConfig("team", configpkg.EnvConfig{Name: "dev"}); err != nil {
		t.Fatalf("save env config: %v", err)
	}

	deployInput := HelmDeploySpec{
		Tenant:      "team",
		Environment: "dev",
		ReleaseName: configpkg.RuntimeReleaseName("team"),
		Namespace:   "team-dev",
	}
	var log bytes.Buffer
	ctx := core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, &log, &log), PlatformAlias: chosen.Alias}
	if err := reconcilePlatformAliasSecret(ctx, &deployInput); err != nil {
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

// TestReconcilePlatformAliasSecretReportsAnAmbiguousHostWithoutTheFlag is the
// reproduction of the reported failure. An ambiguous host has no answer the
// retrofit could pick without guessing whose identity an environment acts as,
// so declining is right -- but it declined as the same silence a host with
// nothing to give gets, and that is the host this retrofit exists for. Every
// deploy from it provisioned nothing, recorded nothing, and left nothing in the
// trace, so the only symptom was a refusal inside the pod naming a cause that
// was not this one. The decline still provisions nothing and threads nothing;
// it must also say what happened and name the flag that lifts it.
//
// Both sessions here are signed in, so ambiguity is the only reason to decline;
// had the selection leaked into the default path, this is where it would show,
// and the recorded environment is asserted because a decline is not a licence
// to write one.
func TestReconcilePlatformAliasSecretReportsAnAmbiguousHostWithoutTheFlag(t *testing.T) {
	ambiguousHostStore(t)
	t.Setenv("ERUN_KUBECTL_BIN", failingBinaryPath(t))
	if err := configpkg.SaveEnvConfig("team", configpkg.EnvConfig{Name: "dev"}); err != nil {
		t.Fatalf("save env config: %v", err)
	}

	deployInput := HelmDeploySpec{
		Tenant:      "team",
		Environment: "dev",
		ReleaseName: configpkg.RuntimeReleaseName("team"),
		Namespace:   "team-dev",
	}
	var log bytes.Buffer
	ctx := core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, &log, &log)}
	if err := reconcilePlatformAliasSecret(ctx, &deployInput); err != nil {
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

// TestReconcilePlatformAliasSecretLeavesARecordedEnvironmentAloneOnAnAmbiguousHost
// keeps the ambiguity out of the one path that must stay silent whatever the
// host looks like. The identity a recorded Secret carries is the operator's,
// and an environment that already names one is never re-provisioned -- a
// decision this deploy does not revisit, so it owes the trace nothing, and the
// line the ambiguity earns on an unprovisioned environment would here be a
// report of a decision that was not made on this host's behalf at all.
func TestReconcilePlatformAliasSecretLeavesARecordedEnvironmentAloneOnAnAmbiguousHost(t *testing.T) {
	ambiguousHostStore(t)
	t.Setenv("ERUN_KUBECTL_BIN", failingBinaryPath(t))
	if err := configpkg.SaveEnvConfig("team", configpkg.EnvConfig{Name: "dev", PlatformAliasSecretName: "team-devops-platform-alias"}); err != nil {
		t.Fatalf("save env config: %v", err)
	}

	deployInput := HelmDeploySpec{
		Tenant:                  "team",
		Environment:             "dev",
		ReleaseName:             configpkg.RuntimeReleaseName("team"),
		Namespace:               "team-dev",
		PlatformAliasSecretName: "team-devops-platform-alias",
	}
	var log bytes.Buffer
	ctx := core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, &log, &log)}
	if err := reconcilePlatformAliasSecret(ctx, &deployInput); err != nil {
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

// TestReconcilePlatformAliasSecretSaysWhenAnExplicitAliasWasNotUsed closes the
// flag's last silent path. An environment that already records a Secret keeps
// it -- re-provisioning is a change of identity, not a refresh -- so a deploy
// naming an alias has nothing to apply. Saying that in the trace is what keeps
// a flag the operator passed from reading as a flag that took effect; the
// recorded Secret must still be the one left in place.
func TestReconcilePlatformAliasSecretSaysWhenAnExplicitAliasWasNotUsed(t *testing.T) {
	redirectConfigHomeForTest(t)
	t.Setenv("ERUN_KUBECTL_BIN", failingBinaryPath(t))
	signedInDefaultHostStore(t, testPlatformAliasProvider())
	if err := configpkg.SaveEnvConfig("team", configpkg.EnvConfig{Name: "dev", PlatformAliasSecretName: "team-devops-platform-alias"}); err != nil {
		t.Fatalf("save env config: %v", err)
	}

	deployInput := HelmDeploySpec{
		Tenant:                  "team",
		Environment:             "dev",
		ReleaseName:             configpkg.RuntimeReleaseName("team"),
		Namespace:               "team-dev",
		PlatformAliasSecretName: "team-devops-platform-alias",
	}
	var log bytes.Buffer
	ctx := core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, &log, &log), PlatformAlias: secondPlatformAliasProvider().Alias}
	if err := reconcilePlatformAliasSecret(ctx, &deployInput); err != nil {
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

// TestReconcilePlatformAliasSecretReportsAnExplicitAliasOnADeploy is the same
// failure reaching the caller of the deploy path: the retrofit has to return it,
// not swallow it, and must leave the environment untouched so the next run can
// be told what went wrong instead of reading a half-applied state.
func TestReconcilePlatformAliasSecretReportsAnExplicitAliasOnADeploy(t *testing.T) {
	ambiguousHostStore(t)
	t.Setenv("ERUN_KUBECTL_BIN", failingBinaryPath(t))
	if err := configpkg.SaveEnvConfig("team", configpkg.EnvConfig{Name: "dev"}); err != nil {
		t.Fatalf("save env config: %v", err)
	}

	const alias = "erun+api.nowhere.example@erun"
	deployInput := HelmDeploySpec{
		Tenant:      "team",
		Environment: "dev",
		ReleaseName: configpkg.RuntimeReleaseName("team"),
		Namespace:   "team-dev",
	}
	var log bytes.Buffer
	ctx := core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, &log, &log), PlatformAlias: alias}
	err := reconcilePlatformAliasSecret(ctx, &deployInput)
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

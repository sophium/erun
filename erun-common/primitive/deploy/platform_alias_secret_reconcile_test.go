package deploy

import (
	"bytes"
	"strings"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/cloud"
)

// signedInDefaultHostStore is the host side of the retrofit as production
// resolves it: the real config store and the real default secret store, over
// whatever XDG config home the test redirected to. resolveHostPlatformAlias is
// reached with a static store everywhere else in this file, which cannot prove
// the deploying host's own config is what gets read.
func signedInDefaultHostStore(t *testing.T, provider configpkg.CloudProviderConfig) {
	t.Helper()
	store, err := cloud.DefaultCloudSecretStore()
	if err != nil {
		t.Fatalf("default cloud secret store: %v", err)
	}
	if err := store.SaveCloudSecret(provider.ERun.RefreshTokenRef, "refresh-token-value"); err != nil {
		t.Fatalf("save refresh token: %v", err)
	}
	if err := configpkg.SaveERunConfig(configpkg.ERunConfig{DefaultTenant: "team", CloudProviders: []configpkg.CloudProviderConfig{provider}}); err != nil {
		t.Fatalf("save root config: %v", err)
	}
}

// TestReconcilePlatformAliasSecretRetrofitsAnEnvironmentInitialisedWithoutOne is
// the reproduction of the reported failure. An environment initialised before
// anything provisioned a platform alias records no Secret name, so the runtime
// chart mounts nothing, so the runtime boot's seeder finds nothing to seed -- and
// nothing on the deploy path ever changed that, leaving the pod permanently
// unable to call the platform API (the reported platform-command abort). Before
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
	if err := configpkg.SaveEnvConfig("team", configpkg.EnvConfig{Name: "dev"}); err != nil {
		t.Fatalf("save env config: %v", err)
	}
	deployInput := HelmDeploySpec{
		Tenant:      "team",
		Environment: "dev",
		ReleaseName: configpkg.RuntimeReleaseName("team"),
		Namespace:   "team-dev",
	}
	// The reported state, asserted rather than assumed: with no name recorded
	// the upgrade renders no platform-alias argument at all, which is what made
	// the chart mount nothing.
	if got := helmPlatformAliasSecretSetArgs(deployInput.PlatformAliasSecretName); got != nil {
		t.Fatalf("the unprovisioned deploy already named a platform alias: %v", got)
	}

	var log bytes.Buffer
	ctx := core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, &log, &log)}
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
		cloud.CloudSecretFileName(provider.ERun.RefreshTokenRef),
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
	recorded, _, err := configpkg.LoadEnvConfig("team", "dev")
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
	if err := reconcilePlatformAliasSecret(testTraceContext(false), &deployInput); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "team-devops-platform-alias"; deployInput.PlatformAliasSecretName != want {
		t.Fatalf("an already-provisioned environment's alias was changed: got %q, want %q", deployInput.PlatformAliasSecretName, want)
	}
	recorded, _, err := configpkg.LoadEnvConfig("team", "dev")
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
		t.Fatalf("unexpected error: %v", err)
	}
	if deployInput.PlatformAliasSecretName != "" {
		t.Fatalf("got %q, want no name when the host has no alias to give", deployInput.PlatformAliasSecretName)
	}
	if got := log.String(); strings.TrimSpace(got) != "" {
		t.Fatalf("a host with nothing to give must add nothing to the trace, got:\n%s", got)
	}
	recorded, _, err := configpkg.LoadEnvConfig("team", "dev")
	if err != nil {
		t.Fatalf("load env config: %v", err)
	}
	if recorded.PlatformAliasSecretName != "" {
		t.Fatalf("got %q, want the environment left unrecorded", recorded.PlatformAliasSecretName)
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
	mustSaveEnvConfig(t, configpkg.EnvConfig{Name: "dev"})

	// The spec's own view of the environment: resolved before the rollout, so
	// it cannot carry a name this deploy has not provisioned yet.
	spec := DeploySpec{
		Target: configpkg.OpenResult{Tenant: "team", Environment: "dev", EnvConfig: mustLoadEnvConfig(t)},
		Deploy: HelmDeploySpec{
			Tenant:               "team",
			Environment:          "dev",
			ReleaseName:          configpkg.RuntimeReleaseName("team"),
			Namespace:            "team-dev",
			Version:              "1.0.0",
			ResolvedRuntimeImage: "registry.example/test/team-devops:1.0.0",
		},
	}

	var log bytes.Buffer
	ctx := core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, &log, &log)}
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
	if err := persistRuntimeVersionIfChanged(spec, "1.0.0", configpkg.SaveEnvConfig); err != nil {
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
func mustSaveEnvConfig(t *testing.T, config configpkg.EnvConfig) {
	t.Helper()
	if err := configpkg.SaveEnvConfig("team", config); err != nil {
		t.Fatalf("save env config: %v", err)
	}
}

func mustLoadEnvConfig(t *testing.T) configpkg.EnvConfig {
	t.Helper()
	config, _, err := configpkg.LoadEnvConfig("team", "dev")
	if err != nil {
		t.Fatalf("load env config: %v", err)
	}
	return config
}

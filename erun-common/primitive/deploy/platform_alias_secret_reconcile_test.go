package deploy

import (
	"bytes"
	"strings"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud"
)

// signedInDefaultHostStore uses the real config and secret stores, proving the
// deploying host's own config is what gets read.
func signedInDefaultHostStore(t *testing.T, provider model.CloudProviderConfig) {
	t.Helper()
	store, err := cloud.DefaultCloudSecretStore()
	if err != nil {
		t.Fatalf("default cloud secret store: %v", err)
	}
	if err := store.SaveCloudSecret(provider.ERun.RefreshTokenRef, "refresh-token-value"); err != nil {
		t.Fatalf("save refresh token: %v", err)
	}
	if err := configpkg.SaveERunConfig(configpkg.ERunConfig{DefaultTenant: "team", CloudProviders: []model.CloudProviderConfig{provider}}); err != nil {
		t.Fatalf("save root config: %v", err)
	}
}

func TestReconcilePlatformAliasSecretRetrofitsAnEnvironmentInitialisedWithoutOne(t *testing.T) {
	redirectConfigHomeForTest(t)
	captured := installCapturingKubectl(t)
	provider := testPlatformAliasProvider()
	signedInDefaultHostStore(t, provider)

	if err := configpkg.SaveEnvConfig("team", configpkg.EnvConfig{Name: "dev"}); err != nil {
		t.Fatalf("save env config: %v", err)
	}
	deployInput := HelmDeploySpec{
		Tenant:      "team",
		Environment: "dev",
		ReleaseName: configpkg.RuntimeReleaseName("team"),
		Namespace:   "team-dev",
	}
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

	// Without the record, a later deploy from a host without the alias drops the mount.
	recorded, _, err := configpkg.LoadEnvConfig("team", "dev")
	if err != nil {
		t.Fatalf("load env config: %v", err)
	}
	if want := "team-devops-platform-alias"; recorded.PlatformAliasSecretName != want {
		t.Fatalf("the environment did not record the Secret: got %q, want %q", recorded.PlatformAliasSecretName, want)
	}
}

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

// The retrofit writes the record after the deploy resolved its env snapshot, so
// version persistence must not save that stale snapshot back over it.
func TestRetrofittedAliasRecordSurvivesTheDeploysOwnVersionPersistence(t *testing.T) {
	redirectConfigHomeForTest(t)
	installCapturingKubectl(t)
	signedInDefaultHostStore(t, testPlatformAliasProvider())
	mustSaveEnvConfig(t, configpkg.EnvConfig{Name: "dev"})

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
	if got := strings.TrimSpace(mustLoadEnvConfig(t).PlatformAliasSecretName); got != "team-devops-platform-alias" {
		t.Fatalf("the retrofit recorded nothing: got %q", got)
	}

	if err := persistRuntimeVersionIfChanged(spec, "1.0.0", configpkg.SaveEnvConfig); err != nil {
		t.Fatalf("persist runtime version: %v", err)
	}
	recorded := mustLoadEnvConfig(t)
	if got := strings.TrimSpace(recorded.PlatformAliasSecretName); got != "team-devops-platform-alias" {
		t.Fatalf("the deploy discarded the platform-alias record it had just written: got %q, want %q", got, "team-devops-platform-alias")
	}
	if recorded.RuntimeVersion != "1.0.0" {
		t.Fatalf("the runtime-version memo did not land: got %q", recorded.RuntimeVersion)
	}
}

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

package platformalias

import (
	"bytes"
	"strings"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam/helmdeploy"
	"github.com/sophium/erun/erun-common/infra/cloud/secretstore"
	"github.com/sophium/erun/erun-common/primitive/deploy/helmargs"
)

// signedInDefaultHostStore uses the real config and secret stores, proving the
// deploying host's own config is what gets read.
func signedInDefaultHostStore(t *testing.T, provider model.CloudProviderConfig) {
	t.Helper()
	store, err := secretstore.DefaultCloudSecretStore()
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
	deployInput := helmdeploy.HelmDeploySpec{
		Tenant:      "team",
		Environment: "dev",
		ReleaseName: configpkg.RuntimeReleaseName("team"),
		Namespace:   "team-dev",
	}
	if got := helmargs.HelmPlatformAliasSecretSetArgs(deployInput.PlatformAliasSecretName); got != nil {
		t.Fatalf("the unprovisioned deploy already named a platform alias: %v", got)
	}

	var log bytes.Buffer
	ctx := core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, &log, &log)}
	if err := ReconcilePlatformAliasSecret(ctx, &deployInput); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if want := "team-devops-platform-alias"; deployInput.PlatformAliasSecretName != want {
		t.Fatalf("the deploy still names no platform-alias Secret: got %q, want %q", deployInput.PlatformAliasSecretName, want)
	}
	manifest := captured.read(t)
	for _, want := range []string{
		"name: team-devops-platform-alias",
		"alias: " + provider.Alias,
		secretstore.CloudSecretFileName(provider.ERun.RefreshTokenRef),
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

	deployInput := helmdeploy.HelmDeploySpec{
		Tenant:                  "team",
		Environment:             "dev",
		ReleaseName:             configpkg.RuntimeReleaseName("team"),
		Namespace:               "team-dev",
		PlatformAliasSecretName: "team-devops-platform-alias",
	}
	if err := ReconcilePlatformAliasSecret(testTraceContext(false), &deployInput); err != nil {
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

package runtimeversion

import (
	"bytes"
	"strings"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam/helmdeploy"
	"github.com/sophium/erun/erun-common/infra/cloud/secretstore"
	"github.com/sophium/erun/erun-common/primitive/deploy/platformalias"
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

// The retrofit writes the record after the deploy resolved its env snapshot, so
// version persistence must not save that stale snapshot back over it.
func TestRetrofittedAliasRecordSurvivesTheDeploysOwnVersionPersistence(t *testing.T) {
	redirectConfigHomeForTest(t)
	installCapturingKubectl(t)
	signedInDefaultHostStore(t, testPlatformAliasProvider())
	mustSaveEnvConfig(t, configpkg.EnvConfig{Name: "dev"})

	spec := helmdeploy.DeploySpec{
		Target: configpkg.OpenResult{Tenant: "team", Environment: "dev", EnvConfig: mustLoadEnvConfig(t)},
		Deploy: helmdeploy.HelmDeploySpec{
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
	if err := platformalias.ReconcilePlatformAliasSecret(ctx, &spec.Deploy); err != nil {
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

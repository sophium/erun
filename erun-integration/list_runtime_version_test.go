package integration

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func testListRuntimeVersionLineAlignedTenantLine(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	envConfigPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
	existing, err := os.ReadFile(envConfigPath)
	if err != nil {
		t.Fatalf("read env config: %v", err)
	}
	mustWriteFile(t, envConfigPath, string(existing)+"runtimerunningimage: ghcr.io/sophium/team-devops:1.0.84\n")
	result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "list/runtime_version_line_aligned_tenant_line", normalize.Apply(result.Combined))
}

func testListRuntimeVersionLineStockErunLine(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "erun", "build")
	envConfigPath := filepath.Join(setup.ConfigHome, "erun", "erun", "build", "config.yaml")
	existing, err := os.ReadFile(envConfigPath)
	if err != nil {
		t.Fatalf("read env config: %v", err)
	}
	mustWriteFile(t, envConfigPath, string(existing)+"runtimerunningimage: ghcr.io/sophium/erun-devops:1.0.221\n")
	result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "list/runtime_version_line_stock_erun_line", normalize.Apply(result.Combined))
}

func testListRuntimeVersionLineReleaseNameDisagreesWithImage(t *testing.T) {
	// The tenant's own release running the stock erun-devops image is flagged,
	// since it is easily misread as the tenant's own line.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "frs", "build")
	envConfigPath := filepath.Join(setup.ConfigHome, "erun", "frs", "build", "config.yaml")
	existing, err := os.ReadFile(envConfigPath)
	if err != nil {
		t.Fatalf("read env config: %v", err)
	}
	mustWriteFile(t, envConfigPath, string(existing)+"runtimerunningimage: ghcr.io/sophium/erun-devops:1.0.203\n")
	result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "list/runtime_version_line_release_name_disagrees_with_image", normalize.Apply(result.Combined))
}

func testListRuntimeVersionLineUndeterminedOnMalformedRecordedImage(t *testing.T) {
	// An unparseable image reference reads as undetermined, never guessed.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	envConfigPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
	existing, err := os.ReadFile(envConfigPath)
	if err != nil {
		t.Fatalf("read env config: %v", err)
	}
	mustWriteFile(t, envConfigPath, string(existing)+"runtimerunningimage: not-a-valid-image-reference\n")
	result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "list/runtime_version_line_undetermined_on_malformed_recorded_image", normalize.Apply(result.Combined))
}

func testListErunVersionReadsOffAStatedRuntimeChartReference(t *testing.T) {
	// A runtime chart on its own version line renders its version separately from
	// the runtime image's.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	envConfigPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
	existing, err := os.ReadFile(envConfigPath)
	if err != nil {
		t.Fatalf("read env config: %v", err)
	}
	mustWriteFile(t, envConfigPath, string(existing)+
		"runtimerunningimage: ghcr.io/sophium/erun-devops:1.0.0\n"+
		"runtimechart: oci://ghcr.io/sophium/charts/erun-devops:1.0.201\n")
	result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "list/erun_version_reads_off_a_stated_runtime_chart_reference", normalize.Apply(result.Combined))
}

// seedTenantEnvOnErunLine seeds an environment running the erun-devops image at
// version. It rewrites the env config wholesale because yaml.v3 refuses the
// duplicate runtimeversion key SeedTenantEnv already wrote.
func seedTenantEnvOnErunLine(t testing.TB, setup env.Setup, tenant, environment, version string) {
	t.Helper()
	fixture.SeedTenantEnv(t, setup, tenant, environment)
	envConfigPath := filepath.Join(setup.ConfigHome, "erun", tenant, environment, "config.yaml")
	mustWrite(t, envConfigPath,
		"name: "+environment+"\n"+
			"repopath: "+setup.Cwd+"\n"+
			"kubernetescontext: test-context\n"+
			"containerregistry: registry.example/test\n"+
			"type: local-agent\n"+
			"runtimeversion: "+version+"\n"+
			"runtimerunningimage: ghcr.io/sophium/erun-devops:"+version+"\n",
	)
}

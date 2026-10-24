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
	// The tenant's own devops release runs the tenant's own image at the
	// same name (team-devops release, team-devops image) -- the ordinary,
	// unsurprising case. The line reads as "team", not "erun".
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
	// The tenant literally named "erun" rides the stock erun-devops image
	// by construction (RuntimeReleaseName("erun") == "erun-devops"), so
	// the release name and the image agree and the line reads "erun"
	// with no disagreement flag.
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
	// The trap: the tenant's own frs-devops release is
	// running the stock erun-devops image (a leftover explicit override,
	// or a deploy that never needed a tenant-specific image). The line is
	// still "erun" -- that is genuinely what is running -- but flagged
	// distinctly because it is the row most likely to be misread as
	// "frs's own line".
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
	// A hand-edited or corrupted runtimerunningimage that doesn't parse as
	// a real image reference (no tag) must still read as undetermined,
	// not crash and not guess a line from the tenant name.
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
	// When an env's runtime chart rides its own version line (rather than
	// following runtimeversion), ResolveErunVersion reads the version off
	// the stated runtimechart reference via SplitChartReference. The
	// chart's version (1.0.201) must render distinctly from the runtime
	// image's own version (1.0.0, from SeedTenantEnv's default
	// runtimeversion).
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

// seedTenantEnvOnErunLine seeds tenant/environment (calling this again for a
// second environment under an already-seeded tenant is fine -- each call
// only writes its own env directory, plus the tenant/root config every
// SeedTenantEnv call already rewrites identically) with a resolved
// erun-devops runtime image recorded at version, which is what makes
// ResolveErunVersion read version off RuntimeVersion rather than reporting
// the environment as unresolved. Rewrites the env config wholesale rather
// than appending, since SeedTenantEnv already writes a runtimeversion of its
// own and yaml.v3 refuses a duplicate mapping key.
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

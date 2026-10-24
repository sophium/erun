package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

// A tenant runtimeimage tag rides the tenant's release line; rewriting it to the erun
// target would ImagePullBackOff, so pin skips it and says so.
func testPinSkipsATenantRuntimeimageTagAndSaysSo(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	seedDriftedPins(t, setup.Cwd, filepath.Join(setup.ConfigHome, "erun"))
	envConfigPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
	existing, err := os.ReadFile(envConfigPath)
	if err != nil {
		t.Fatalf("read env config: %v", err)
	}
	if err := os.WriteFile(envConfigPath, append(existing, []byte("runtimeimage: ghcr.io/sophium/team-devops:old-tag\n")...), 0o644); err != nil {
		t.Fatalf("write env config: %v", err)
	}

	result := erun.Run(t, []string{"pin", "team", "dev", "--version", "1.0.175", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "pin/skips_a_tenant_runtimeimage_tag_and_says_so", normalize.Apply(result.Combined))
}

func testPinRealRunLeavesATenantImagedRuntimeversionAlone(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	seedDriftedPins(t, setup.Cwd, filepath.Join(setup.ConfigHome, "erun"))
	envConfigPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
	existing, err := os.ReadFile(envConfigPath)
	if err != nil {
		t.Fatalf("read env config: %v", err)
	}
	if err := os.WriteFile(envConfigPath, append(existing, []byte("runtimeimage: ghcr.io/sophium/team-devops:1.0.76\n")...), 0o644); err != nil {
		t.Fatalf("write env config: %v", err)
	}
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinary(t, stubs, "helm", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "helm")...)

	result := erun.Run(t, []string{"pin", "team", "dev", "--version", "1.0.175"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	terraform := readPinnedFile(t, setup.Cwd, "terraform-team/dev/main.tf")
	if !strings.Contains(terraform, "?ref=v1.0.175") {
		t.Fatalf("erun terraform ref not re-pinned:\n%s", terraform)
	}
	after, err := os.ReadFile(envConfigPath)
	if err != nil {
		t.Fatalf("read env config after: %v", err)
	}
	if !strings.Contains(string(after), "runtimeversion: 1.0.0") {
		t.Fatalf("a tenant-imaged env's runtimeversion must be left alone:\n%s", after)
	}
}

// A stock-erun runtimechart is on erun's release line, so the plan must move it.
func testPinDryRunReportsAStaleRuntimechartReference(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	seedUnreachableRuntimeRegistry(t, filepath.Join(setup.ConfigHome, "erun"))
	envConfigPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
	existing, err := os.ReadFile(envConfigPath)
	if err != nil {
		t.Fatalf("read env config: %v", err)
	}
	if err := os.WriteFile(envConfigPath, append(existing, []byte("runtimechart: oci://ghcr.io/sophium/charts/erun-devops:1.0.201\n")...), 0o644); err != nil {
		t.Fatalf("write env config: %v", err)
	}

	result := erun.Run(t, []string{"pin", "team", "dev", "--version", "1.0.175", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "pin/dry_run_reports_a_stale_runtimechart_reference", normalize.Apply(result.Combined))
}

// A stated-stock runtimeimage and runtimechart move together with runtimeversion.
func testPinRealRunPinsRuntimeversionRuntimeimageAndRuntimechartTogether(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	seedUnreachableRuntimeRegistry(t, filepath.Join(setup.ConfigHome, "erun"))
	envConfigPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
	existing, err := os.ReadFile(envConfigPath)
	if err != nil {
		t.Fatalf("read env config: %v", err)
	}
	body := string(existing) +
		"runtimeimage: ghcr.io/sophium/erun-devops:1.0.201\n" +
		"runtimechart: oci://ghcr.io/sophium/charts/erun-devops:1.0.201\n"
	if err := os.WriteFile(envConfigPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write env config: %v", err)
	}

	result := erun.Run(t, []string{"pin", "team", "dev", "--version", "1.0.175"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	after, err := os.ReadFile(envConfigPath)
	if err != nil {
		t.Fatalf("read env config after: %v", err)
	}
	for _, want := range []string{
		"runtimeversion: 1.0.175",
		"runtimeimage: ghcr.io/sophium/erun-devops:1.0.175",
		"runtimechart: oci://ghcr.io/sophium/charts/erun-devops:1.0.175",
	} {
		if !strings.Contains(string(after), want) {
			t.Fatalf("expected %q in the env config after pinning, got:\n%s", want, after)
		}
	}
}

// A tenant-own runtimechart line pins the whole coordinate, including runtimeversion.
func testPinRealRunLeavesATenantOwnRuntimechartLineUnchanged(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	seedUnreachableRuntimeRegistry(t, filepath.Join(setup.ConfigHome, "erun"))
	envConfigPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
	existing, err := os.ReadFile(envConfigPath)
	if err != nil {
		t.Fatalf("read env config: %v", err)
	}
	if err := os.WriteFile(envConfigPath, append(existing, []byte("runtimechart: oci://ghcr.io/sophium/charts/team-devops:1.0.76\n")...), 0o644); err != nil {
		t.Fatalf("write env config: %v", err)
	}

	result := erun.Run(t, []string{"pin", "team", "dev", "--version", "1.0.175"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	after, err := os.ReadFile(envConfigPath)
	if err != nil {
		t.Fatalf("read env config after: %v", err)
	}
	if !strings.Contains(string(after), "runtimechart: oci://ghcr.io/sophium/charts/team-devops:1.0.76") {
		t.Fatalf("a tenant's own runtimechart line must be left alone, got:\n%s", after)
	}
	if !strings.Contains(string(after), "runtimeversion: 1.0.0") {
		t.Fatalf("an own-umbrella env's runtimeversion must be left alone too, got:\n%s", after)
	}
}

// With no stated runtimeimage the env's own umbrella names its line, so runtimeversion
// is skipped with a note.
func testPinSkipsAnOwnUmbrellaEnvsRuntimeversionAndSaysSo(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "frs", "build")
	seedDriftedPins(t, setup.Cwd, filepath.Join(setup.ConfigHome, "erun"))
	envConfigPath := filepath.Join(setup.ConfigHome, "erun", "frs", "build", "config.yaml")
	existing, err := os.ReadFile(envConfigPath)
	if err != nil {
		t.Fatalf("read env config: %v", err)
	}
	if err := os.WriteFile(envConfigPath, append(existing, []byte("runtimechart: oci://ghcr.io/sophium/charts/frs-devops:1.0.134\n")...), 0o644); err != nil {
		t.Fatalf("write env config: %v", err)
	}

	result := erun.Run(t, []string{"pin", "frs", "build", "--version", "1.0.175", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "pin/skips_an_own_umbrella_envs_runtimeversion_and_says_so", normalize.Apply(result.Combined))
}

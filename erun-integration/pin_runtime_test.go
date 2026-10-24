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

// A tenant's own runtimeimage tag rides the tenant's own release
// line, not erun's. Rewriting it to the erun target version would name a
// tag that line never publishes, guaranteeing an ImagePullBackOff on the
// very next deploy — produced by the command whose whole purpose is
// keeping version references consistent. pin must leave it alone and say
// so in the plan, so the operator is not left assuming it was covered.
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

// The reported bug, for real: applying a re-pin for a tenant-imaged
// env must not overwrite its persisted runtimeversion — that would name a
// tag the tenant's own image line never publishes, guaranteeing an
// ImagePullBackOff on the next deploy — while the rest of the repo's
// erun-owned references still move.
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

// The reported bug, for real: a runtimechart naming erun's own stock
// chart is versioned on erun's own release line, just like runtimeversion
// and a stated runtimeimage — a re-pin's dry-run plan must name it as a
// site to move rather than reporting a plan that looks complete while
// leaving it behind.
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

// The whole coordinate, applied for real: a stated-stock runtimeimage and
// runtimechart move to the target alongside runtimeversion, in the one
// write that realigns the environment's own config. Before this fix, only
// runtimeversion moved — runtimechart (this report) and a stated
// runtimeimage (a latent instance of the same gap, found while fixing
// this) were both left behind.
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

// A tenant's own runtimechart line is a real, deliberate configuration —
// --runtime-chart exists precisely so the chart can be versioned
// separately from the image and the erun release. A re-pin must leave the
// whole coordinate exactly as stated: the env running that umbrella runs
// the image the umbrella publishes, so its runtimeversion is that line's
// number too, and writing the erun target into it hands the environment a
// version its own release line never publishes.
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

// The reported case, binary-reachable: the environment states no
// runtimeimage, so only its own umbrella names the line its runtime pod
// runs. The plan must skip runtimeversion with a note saying what it read,
// rather than adding the erun target to an environment whose own release
// line never publishes it.
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

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

func buildOutputAndScriptScenarios(t *testing.T) {
	t.Run("real_run_default_verbosity_stays_quiet_when_the_build_succeeds", testBuildRealRunDefaultVerbosityStaysQuietWhenTheBuildSucceeds)
	t.Run("real_run_default_verbosity_surfaces_docker_builds_own_output_on_failure", testBuildRealRunDefaultVerbositySurfacesDockerBuildsOwnOutputOnFailure)
	t.Run("real_run_step_timing_breaks_a_platform_build_down_into_dockerfile_steps_and_make_phases", testBuildRealRunStepTimingBreaksAPlatformBuildDownIntoDockerfileStepsAndMakePhases)
	t.Run("dry_run_no_incremental_skips_fingerprint_short_circuit", testBuildDryRunNoIncrementalSkipsFingerprintShortCircuit)
	t.Run("dry_run_with_project_build_script_traces_script_invocation", testBuildDryRunWithProjectBuildScriptTracesScriptInvocation)
	t.Run("dry_run_disable_build_script_ignores_project_build_sh", testBuildDryRunDisableBuildScriptIgnoresProjectBuildSh)
	t.Run("dry_run_refuses_script_fallback_when_the_project_has_a_docker_module", testBuildDryRunRefusesScriptFallbackWhenTheProjectHasADockerModule)
	t.Run("real_run_with_project_build_script_executes_script", testBuildRealRunWithProjectBuildScriptExecutesScript)
}

func testBuildRealRunDefaultVerbosityStaysQuietWhenTheBuildSucceeds(t *testing.T) {
	// docker build always uses --progress=plain, so erun itself must keep
	// BuildKit chatter out of a successful plain build.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryWithScript(t, stubs, "docker", strings.Join([]string{
		`case "$1" in`,
		`  image) case "$2" in inspect) exit 1 ;; *) exit 0 ;; esac ;;`,
		`  buildx) case "$2" in inspect) echo "Platforms: linux/arm64*, linux/amd64" ;; *) exit 0 ;; esac ;;`,
		`  build) echo "#1 [internal] load build definition" ; exit 0 ;;`,
		`  *) exit 0 ;;`,
		`esac`,
	}, "\n"))
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker")...)
	envVars = append(envVars, stubHelmSilent(t, setup)...)
	result := erun.Run(t, []string{"build"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if strings.Contains(result.Combined, "internal] load build definition") {
		t.Fatalf("a successful build at default verbosity must stay quiet, got docker's own output leaked:\n%s", result.Combined)
	}
}

func testBuildRealRunDefaultVerbositySurfacesDockerBuildsOwnOutputOnFailure(t *testing.T) {
	// A failed build must show docker's own output at default verbosity.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryWithScript(t, stubs, "docker", strings.Join([]string{
		`case "$1" in`,
		`  image) case "$2" in inspect) exit 1 ;; *) exit 0 ;; esac ;;`,
		`  buildx) case "$2" in inspect) echo "Platforms: linux/arm64*, linux/amd64" ;; *) exit 0 ;; esac ;;`,
		`  build) echo "make check: FAIL erun-cli/cmd TestSomething" >&2 ; exit 1 ;;`,
		`  *) exit 0 ;;`,
		`esac`,
	}, "\n"))
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker")...)
	envVars = append(envVars, stubHelmSilent(t, setup)...)
	result := erun.Run(t, []string{"build"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected the build failure to fail the command, got exit 0:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "make check: FAIL erun-cli/cmd TestSomething") {
		t.Fatalf("a failed build at default verbosity must surface docker's own output, got:\n%s", result.Combined)
	}
}

func testBuildRealRunStepTimingBreaksAPlatformBuildDownIntoDockerfileStepsAndMakePhases(t *testing.T) {
	// BuildKit step DONE lines and in-step `>> <phase>` markers become their
	// own timing rows.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryWithScript(t, stubs, "docker", strings.Join([]string{
		`case "$1" in`,
		`  image) case "$2" in inspect) exit 1 ;; *) exit 0 ;; esac ;;`,
		`  buildx) case "$2" in inspect) echo "Platforms: linux/arm64*, linux/amd64" ;; *) exit 0 ;; esac ;;`,
		`  build) echo "#4 [3/3] RUN make check"; echo "#4 0.10 >> golangci-lint"; echo "#4 5.00 >> go test"; echo "#4 DONE 12.00s"; exit 0 ;;`,
		`  *) exit 0 ;;`,
		`esac`,
	}, "\n"))
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker")...)
	envVars = append(envVars, stubHelmSilent(t, setup)...)
	result := erun.Run(t, []string{"build"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "step timing") {
		t.Fatalf("expected the step timing table, got:\n%s", result.Combined)
	}
	for _, want := range []string{"RUN make check", "golangci-lint", "go test"} {
		if !strings.Contains(result.Combined, want) {
			t.Fatalf("expected the step timing table to name %q as its own row (a Dockerfile step / make phase, not just the whole platform build), got:\n%s", want, result.Combined)
		}
	}
}

func testBuildDryRunNoIncrementalSkipsFingerprintShortCircuit(t *testing.T) {
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	result := erun.Run(t, []string{"build", "--dry-run", "--no-incremental"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "build/dry_run_no_incremental_skips_fingerprint_short_circuit", normalize.Apply(result.Combined))
}

func testBuildDryRunWithProjectBuildScriptTracesScriptInvocation(t *testing.T) {
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	if err := os.WriteFile(filepath.Join(setup.Cwd, "build.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write build.sh: %v", err)
	}
	fixture.RunGit(t, setup.Cwd, "add", "build.sh")
	fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "add build script")
	result := erun.Run(t, []string{"build", "--dry-run", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "build/dry_run_with_project_build_script_traces_script_invocation", normalize.Apply(result.Combined))
}

func testBuildDryRunDisableBuildScriptIgnoresProjectBuildSh(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	envCfg := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
	data, err := os.ReadFile(envCfg)
	if err != nil {
		t.Fatalf("read env config: %v", err)
	}
	if err := os.WriteFile(envCfg, append(data, []byte("disablebuildscript: true\n")...), 0o644); err != nil {
		t.Fatalf("write env config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(setup.Cwd, "build.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write build.sh: %v", err)
	}
	result := erun.Run(t, []string{"build", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit (build.sh ignored, no docker context), got 0: %s", result.Combined)
	}
	golden.Equal(t, "build/dry_run_disable_build_script_ignores_project_build_sh", normalize.Apply(result.Combined))
}

func testBuildDryRunRefusesScriptFallbackWhenTheProjectHasADockerModule(t *testing.T) {
	// A nested build script would exit zero having built no image: a false green.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	nestedDir := filepath.Join(setup.Cwd, "erun-ui")
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatalf("mkdir nested dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nestedDir, "build.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write nested build.sh: %v", err)
	}
	fixture.RunGit(t, setup.Cwd, "add", ".")
	fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "add nested build script")
	result := erun.Run(t, []string{"build", "--dry-run"}, erun.RunOptions{Cwd: nestedDir, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit (project has a docker module, no image resolved), got 0: %s", result.Combined)
	}
	golden.Equal(t, "build/dry_run_refuses_script_fallback_when_the_project_has_a_docker_module", normalize.Apply(result.Combined))
}

func testBuildRealRunWithProjectBuildScriptExecutesScript(t *testing.T) {
	// The script's marker file proves it ran.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	marker := filepath.Join(setup.Cwd, "build-script-marker")
	scriptBody := "#!/bin/sh\nprintf 'ran with %s\\n' \"$ERUN_BUILD_VERSION\" > '" + marker + "'\nexit 0\n"
	if err := os.WriteFile(filepath.Join(setup.Cwd, "build.sh"), []byte(scriptBody), 0o755); err != nil {
		t.Fatalf("write build.sh: %v", err)
	}
	fixture.RunGit(t, setup.Cwd, "add", "build.sh")
	fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "add build script")
	result := erun.Run(t, []string{"build"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	body, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	if !strings.HasPrefix(string(body), "ran with ") {
		t.Errorf("expected marker prefix 'ran with ', got: %q", body)
	}
}

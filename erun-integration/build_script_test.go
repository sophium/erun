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
	// docker build always runs with --progress=plain now (never
	// --quiet, which suppressed a failing step's own output at the source),
	// so quiet-on-success below debug verbosity has to come from erun
	// capturing the output itself rather than replaying it. This is the
	// success half of that contract: BuildKit's plain-progress chatter must
	// not leak into a plain `erun build` even though docker produced it.
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
	// The release path builds quietly, so when an in-build test stage
	// failed, all that survived was
	// `process "/bin/sh -c ..." did not complete successfully:
	// exit code: 2` — no lint finding, no failing test name. docker build's
	// own output (what BuildKit's plain progress captured, including a
	// failing RUN step's stdout/stderr) must now reach the user even at
	// default verbosity, since a failed build is the one time quiet is the
	// wrong default.
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
	// A gate build renders one image as ~99% of total wall clock, so the
	// per-platform timing row used to be the finest granularity available --
	// it could say a build was slow, never which part. BuildKit's own
	// --progress=plain output (already captured for the two scenarios above)
	// carries a per-Dockerfile-step DONE line, and a step's own
	// `>> <phase>` markers ride inside that step's output lines. Both must now surface as their own rows in the step timing table
	// instead of collapsing into the platform's one duration.
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
	// --no-incremental forces `docker build` for every image even when a
	// fingerprint tag exists — no `docker image inspect` short-circuit, no
	// `(skipping)` lines.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	result := erun.Run(t, []string{"build", "--dry-run", "--no-incremental"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "build/dry_run_no_incremental_skips_fingerprint_short_circuit", normalize.Apply(result.Combined))
}

func testBuildDryRunWithProjectBuildScriptTracesScriptInvocation(t *testing.T) {
	// When a build.sh exists at the project root, the build flow calls the
	// script instead of running docker builds. Dry-run traces the resolved
	// script path with ERUN_BUILD_VERSION and skips the docker build chain.
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
	// An env with disablebuildscript: true makes erun build ignore the
	// project build.sh and resolve docker/release contexts directly. With no
	// docker context the build ends at the no-buildable-context error rather
	// than tracing ./build.sh (which dry_run_with_project_build_script does).
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
	// A project whose docker module holds images must never degrade to a
	// nested project build script: that exits zero having built no image and
	// run no gate, which the caller reading the exit code cannot tell from a
	// real pass. Running from a directory that resolves no images is the
	// shape of that false green, and it must fail loudly instead.
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
	// Real-run companion to the dry-run script scenario: without --dry-run the
	// build flow actually invokes ./build.sh. The marker file the script writes
	// (a side effect outside the captured streams) proves the process ran.
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

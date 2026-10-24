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

func buildLinuxPackageScenarios(t *testing.T) {
	t.Run("dry_run_linux_package_from_component_dir", testBuildDryRunLinuxPackageFromComponentDir)
	t.Run("dry_run_linux_package_cgroup_parent_applies_inside_injected_runtime_pod", testBuildDryRunLinuxPackageCgroupParentAppliesInsideInjectedRuntimePod)
	t.Run("real_run_linux_packages_from_linux_dir", testBuildRealRunLinuxPackagesFromLinuxDir)
	t.Run("real_run_linux_packages_reuse_a_recorded_pass", testBuildRealRunLinuxPackagesReuseARecordedPass)
	t.Run("real_run_linux_package_without_test_stage_reports_none_declared_when_reused", testBuildRealRunLinuxPackageWithoutTestStageReportsNoneDeclaredWhenReused)
	t.Run("dry_run_project_root_walks_devops_linux_dir", testBuildDryRunProjectRootWalksDevopsLinuxDir)
	t.Run("dry_run_linux_package_without_test_or_build_stage_is_refused", testBuildDryRunLinuxPackageWithoutTestOrBuildStageIsRefused)
}

func testBuildDryRunLinuxPackageFromComponentDir(t *testing.T) {
	// A linux package is a Dockerfile under <tenant>-devops/linux/<name>/,
	// never an image: from its directory `erun build` resolves no image and
	// runs only the package's test stage to a cache-only output, from the
	// project root it takes as its context. Nothing about the host matters,
	// because the package builds inside docker.
	setup := env.New(t)
	pkgDir := filepath.Join(setup.Cwd, "team-devops", "linux", "erun-ui")
	mustWriteFile(t, filepath.Join(pkgDir, "Dockerfile"), testedLinuxPackageDockerfile)
	result := erun.Run(t, []string{"build", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: pkgDir, Env: append(setup.Env(), stubDockerArm64Daemon(t, setup)...)})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "build/dry_run_linux_package_from_component_dir", normalize.Apply(result.Combined))
}

func testBuildDryRunLinuxPackageCgroupParentAppliesInsideInjectedRuntimePod(t *testing.T) {
	// A package's test stage is as heavy as any image's (erun-ui runs its
	// whole suite there), so inside an injected runtime pod it nests its RUN
	// containers under the same CPU cap cgroup every image build uses,
	// rather than escaping the dind sidecar's limit.
	setup := env.New(t)
	pkgDir := filepath.Join(setup.Cwd, "team-devops", "linux", "erun-ui")
	mustWriteFile(t, filepath.Join(pkgDir, "Dockerfile"), testedLinuxPackageDockerfile)
	envVars := append(setup.Env(), "ERUN_TENANT=team", "ERUN_ENVIRONMENT=dev")
	envVars = append(envVars, stubDockerArm64Daemon(t, setup)...)
	result := erun.Run(t, []string{"build", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: pkgDir, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "--cgroup-parent /docker/erun-build-cpu-cap-") {
		t.Errorf("expected the package build to carry --cgroup-parent for this pod:\n%s", result.Combined)
	}
	golden.Equal(t, "build/dry_run_linux_package_cgroup_parent_applies_inside_injected_runtime_pod", normalize.Apply(result.Combined))
}

func testBuildRealRunLinuxPackagesFromLinuxDir(t *testing.T) {
	// From the linux/ parent dir every package builds for real: one with a
	// test stage runs it and reports `passed`, one without runs its build
	// stage and reports `none declared`. The docker stub records its argv
	// (a side effect outside the captured streams) to prove each build
	// exports nothing: no tag, no image, no push.
	setup := env.New(t)
	linuxDir := filepath.Join(setup.Cwd, "team-devops", "linux")
	mustWriteFile(t, filepath.Join(linuxDir, "erun-cli", "Dockerfile"), untestedLinuxPackageDockerfile)
	mustWriteFile(t, filepath.Join(linuxDir, "erun-ui", "Dockerfile"), testedLinuxPackageDockerfile)
	calls := filepath.Join(setup.Cwd, "docker-calls")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryWithScript(t, stubs, "docker", dockerStubRecordingTo(calls))
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker")...)
	result := erun.Run(t, []string{"build"}, erun.RunOptions{Cwd: linuxDir, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "build/real_run_linux_packages_from_linux_dir", normalize.Apply(result.Combined))
	recorded, err := os.ReadFile(calls)
	if err != nil {
		t.Fatalf("expected the package builds to run docker: %v", err)
	}
	builds := 0
	for _, line := range strings.Split(strings.TrimSpace(string(recorded)), "\n") {
		if !strings.HasPrefix(line, "build ") {
			continue
		}
		builds++
		if !strings.Contains(line, "--output type=cacheonly") || strings.Contains(line, " -t ") || strings.Contains(line, "--push") {
			t.Errorf("a package build must export nothing, got %q", line)
		}
	}
	if builds != 2 {
		t.Errorf("expected one docker build per package, got:\n%s", recorded)
	}
}

func testBuildRealRunLinuxPackagesReuseARecordedPass(t *testing.T) {
	// A package's pass is recorded locally against its fingerprint, so the
	// next plain build with the same inputs reports `test: reused` and runs
	// no docker build at all, rather than relying on BuildKit's layer cache
	// (which GC evicts). Changing a source its test stage copies runs it
	// again. The docker stub records its argv to count the builds.
	setup := env.New(t)
	linuxDir := filepath.Join(setup.Cwd, "team-devops", "linux")
	mustWriteFile(t, filepath.Join(linuxDir, "erun-ui", "Dockerfile"), "FROM golang:1.26.0 AS build\nCOPY app /src/app\n\nFROM build AS test\nRUN go test ./...\n")
	mustWriteFile(t, filepath.Join(setup.Cwd, "app", "main.go"), "package main\n")
	calls := filepath.Join(setup.Cwd, "docker-calls")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryWithScript(t, stubs, "docker", dockerStubRecordingTo(calls))
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker")...)
	builds := func() int {
		recorded, _ := os.ReadFile(calls)
		return strings.Count(string(recorded), "build --progress=plain")
	}
	run := func() erun.Result {
		result := erun.Run(t, []string{"build"}, erun.RunOptions{Cwd: linuxDir, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		return result
	}
	run()
	if got := builds(); got != 1 {
		t.Fatalf("the first build must run the test stage once, got %d builds", got)
	}
	golden.Equal(t, "build/real_run_linux_packages_reuse_a_recorded_pass", normalize.Apply(run().Combined))
	if got := builds(); got != 1 {
		t.Fatalf("an unchanged package must not run docker again, got %d builds", got)
	}
	mustWriteFile(t, filepath.Join(setup.Cwd, "app", "main.go"), "package main\n\nfunc main() {}\n")
	run()
	if got := builds(); got != 2 {
		t.Fatalf("a changed copy source must run the test stage again, got %d builds", got)
	}
}

func testBuildRealRunLinuxPackageWithoutTestStageReportsNoneDeclaredWhenReused(t *testing.T) {
	// A package with only a `build` stage has no tests, so a build that
	// reuses its recorded pass still reports `none declared`, never
	// `reused`, which would claim an earlier run's tests passed.
	setup := env.New(t)
	linuxDir := filepath.Join(setup.Cwd, "team-devops", "linux")
	mustWriteFile(t, filepath.Join(linuxDir, "erun-cli", "Dockerfile"), untestedLinuxPackageDockerfile)
	calls := filepath.Join(setup.Cwd, "docker-calls")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryWithScript(t, stubs, "docker", dockerStubRecordingTo(calls))
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker")...)
	run := func() erun.Result {
		result := erun.Run(t, []string{"build"}, erun.RunOptions{Cwd: linuxDir, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		return result
	}
	run()
	golden.Equal(t, "build/real_run_linux_package_without_test_stage_reports_none_declared_when_reused", normalize.Apply(run().Combined))
	recorded, _ := os.ReadFile(calls)
	if got := strings.Count(string(recorded), "build --progress=plain"); got != 1 {
		t.Fatalf("the second build must reuse the build stage's pass, got %d builds", got)
	}
}

func testBuildDryRunProjectRootWalksDevopsLinuxDir(t *testing.T) {
	// From the project root every build tests the linux packages beside the
	// images, so a merge gate's plain `erun build` runs them too.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedDevopsRuntimeDockerfile(t, setup, "team")
	fixture.SeedGitRepo(t, setup.Cwd)
	mustWriteFile(t, filepath.Join(setup.Cwd, "team-devops", "linux", "erun-ui", "Dockerfile"), testedLinuxPackageDockerfile)
	envVars := append(setup.Env(), stubDockerArm64Daemon(t, setup)...)
	result := erun.Run(t, []string{"build", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "build/dry_run_project_root_walks_devops_linux_dir", normalize.Apply(result.Combined))
}

func testBuildDryRunLinuxPackageWithoutTestOrBuildStageIsRefused(t *testing.T) {
	// A package a build cannot run anything of is refused by name rather
	// than skipped, which would read as a tested package.
	setup := env.New(t)
	pkgDir := filepath.Join(setup.Cwd, "team-devops", "linux", "erun-ui")
	mustWriteFile(t, filepath.Join(pkgDir, "Dockerfile"), "FROM debian:bookworm-slim AS dist\nRUN true\n")
	result := erun.Run(t, []string{"build", "--dry-run"}, erun.RunOptions{Cwd: pkgDir, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected a package with nothing to run to be refused, got exit 0: %s", result.Combined)
	}
	golden.Equal(t, "build/dry_run_linux_package_without_test_or_build_stage_is_refused", normalize.Apply(result.Combined))
}

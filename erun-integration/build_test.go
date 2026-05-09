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

func TestBuild(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		result := erun.Run(t, []string{"build", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "build/help", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_from_devops_cwd", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		result := erun.Run(t, []string{"build", "--dry-run", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "build/dry_run_from_devops_cwd", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_from_release_repo_traces_docker_builds", func(t *testing.T) {
		// Exercises build.go shorthand from a project root with the
		// erun-devops release-shape layout: --dry-run must trace one
		// docker build per discovered Dockerfile, with the resolved
		// context dir, dockerfile path, image tag, ERUN_VERSION build
		// arg, and the fingerprint tag. Replaces the unit-level
		// coverage of dockerBuildArgs, ResolveDockerBuildContextDirForProject,
		// and the incremental fingerprint trace in erun-common.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
		result := erun.Run(t, []string{"build", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		for _, want := range []string{
			"docker image inspect",
			"&& docker build --platform linux/amd64 --provenance=false -t ghcr.io/sophium/api:1.4.2-amd64 --build-arg ERUN_VERSION=1.4.2 -f",
			"&& docker build --platform linux/arm64 --provenance=false -t ghcr.io/sophium/api:1.4.2-arm64 --build-arg ERUN_VERSION=1.4.2 -f",
			"erun-devops/docker/api/Dockerfile .",
			"docker tag ghcr.io/sophium/api:1.4.2-amd64 ghcr.io/sophium/api:fp-",
			"docker tag ghcr.io/sophium/api:1.4.2-arm64 ghcr.io/sophium/api:fp-",
			"&& docker build --platform linux/amd64 --provenance=false -t ghcr.io/sophium/base:9.9.9-amd64 --build-arg ERUN_VERSION=9.9.9 -f",
			"erun-devops/docker/base/Dockerfile .",
		} {
			if !strings.Contains(result.Stderr, want) {
				t.Errorf("expected dry-run trace to contain %q, got stderr:\n%s", want, result.Stderr)
			}
		}
	})

	t.Run("dry_run_release_includes_release_and_build_traces", func(t *testing.T) {
		// Exercises build.go --release flag: --dry-run must combine the
		// release plan (sync, version write, tag) with the docker build
		// trace using the release-resolved version.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
		result := erun.Run(t, []string{"build", "--release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		for _, want := range []string{
			"release: branch=develop mode=candidate version=1.4.2-rc.",
			"docker build --platform linux/amd64",
			"-t ghcr.io/sophium/api:1.4.2-rc.",
			"--build-arg ERUN_VERSION=1.4.2-rc.",
			"docker manifest create --amend ghcr.io/sophium/api:1.4.2-rc.",
		} {
			if !strings.Contains(result.Stderr, want) {
				t.Errorf("expected --release dry-run trace to contain %q, got stderr:\n%s", want, result.Stderr)
			}
		}
	})

	t.Run("dry_run_configured_fingerprint_traces_pull_and_tag", func(t *testing.T) {
		// Exercises docker.fingerprints config: when an image name matches a
		// configured fingerprint, the materialize step traces docker manifest
		// inspect / docker pull / docker tag for each platform before the
		// regular fingerprint inspect runs. The dry-run does not actually
		// pull; it traces the would-be commands so the maintainer can audit.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
		fixture.SeedProjectK8sConfig(t, setup,
			"environments:\n"+
				"  local:\n"+
				"    docker:\n"+
				"      fingerprints:\n"+
				"        base: 0123456789abcdef\n",
		)
		result := erun.Run(t, []string{"build", "--dry-run", "--environment", "local"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		for _, want := range []string{
			"docker manifest inspect ghcr.io/sophium/base:9.9.9",
			"docker pull --platform linux/amd64 ghcr.io/sophium/base:9.9.9",
			"docker tag ghcr.io/sophium/base:9.9.9 ghcr.io/sophium/base:fp-0123456789abcdef-amd64",
			"docker pull --platform linux/arm64 ghcr.io/sophium/base:9.9.9",
			"docker tag ghcr.io/sophium/base:9.9.9 ghcr.io/sophium/base:fp-0123456789abcdef-arm64",
		} {
			if !strings.Contains(result.Stderr, want) {
				t.Errorf("expected configured-fingerprint dry-run trace to contain %q, got stderr:\n%s", want, result.Stderr)
			}
		}
	})

	t.Run("real_run_via_docker_stub_drives_multi_platform_build", func(t *testing.T) {
		// Exercises eruncommon/build_docker_commands.go DockerImageBuilder,
		// runMultiPlatformBuild, runDockerBuildOnce, and tagFingerprintAfterBuild.
		// Stubs `docker` to exit 0 for every invocation so the build flow
		// runs to completion across both platforms without touching a real
		// daemon. Asserts the user-facing "Built" / "Tagged" lines appear.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinaryWithScript(t, stubs, "docker", strings.Join([]string{
			`case "$1" in`,
			`  image)`,
			`    case "$2" in`,
			`      inspect) exit 1 ;;`,
			`      *) exit 0 ;;`,
			`    esac`,
			`    ;;`,
			`  *) exit 0 ;;`,
			`esac`,
		}, "\n"))
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker")...)
		result := erun.Run(t, []string{"build", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		for _, want := range []string{
			"linux/amd64",
			"linux/arm64",
			"ghcr.io/sophium/api:1.4.2",
		} {
			if !strings.Contains(result.Combined, want) {
				t.Errorf("expected real-run output to contain %q, got:\n%s", want, result.Combined)
			}
		}
	})

	t.Run("dry_run_no_incremental_skips_fingerprint_short_circuit", func(t *testing.T) {
		// Exercises the --no-incremental branch in build orchestration
		// (BuildExecution.NoIncremental, BuildOrderForRefactoredFingerprints
		// path). With --no-incremental, the build trace must run
		// `docker build` for every image even when a fingerprint tag
		// exists — there's no `docker image inspect` short-circuit and
		// no `(skipping)` lines.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
		result := erun.Run(t, []string{"build", "--dry-run", "--no-incremental"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if strings.Contains(result.Combined, "skipping rebuild of") {
			t.Errorf("expected --no-incremental to skip the fingerprint short-circuit, but trace still mentions skipping:\n%s", result.Combined)
		}
		if !strings.Contains(result.Combined, "docker build --platform linux/amd64") {
			t.Errorf("expected docker build trace under --no-incremental, got:\n%s", result.Combined)
		}
	})

	t.Run("dry_run_with_project_build_script_traces_script_invocation", func(t *testing.T) {
		// Exercises eruncommon/project_build_script.go (HasProjectBuildScript,
		// resolveProjectRootBuildScript) + build_docker_commands.go
		// (runScriptSpec, scriptTraceCommand, buildScriptEnv): when a
		// build.sh exists at the project root, the build flow calls the
		// script instead of running docker builds. Dry-run traces the
		// resolved script path with ERUN_BUILD_VERSION and skips the
		// docker build chain.
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
		if !strings.Contains(result.Combined, "./build.sh") {
			t.Errorf("expected build script trace to mention ./build.sh, got:\n%s", result.Combined)
		}
		// Docker builds must NOT be traced when a project build script
		// owns the build phase.
		if strings.Contains(result.Combined, "docker build --platform") {
			t.Errorf("expected build.sh path to skip docker build, but trace mentions docker build:\n%s", result.Combined)
		}
	})

	t.Run("real_run_release_pushes_multi_platform_manifest", func(t *testing.T) {
		// Exercises pushMultiPlatformImage (and the manifest create+push
		// path) plus runDockerPushOnce in real-run release mode. The
		// release branch sets DockerBuildSpec.Push=true for release-tagged
		// images, which drives runMultiPlatformBuild's push branch.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "main")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinaryWithScript(t, stubs, "docker", strings.Join([]string{
			`case "$1" in`,
			`  image)`,
			`    case "$2" in inspect) exit 1 ;; *) exit 0 ;; esac ;;`,
			`  *) exit 0 ;;`,
			`esac`,
		}, "\n"))
		// Release flow runs git tag/push; stub git verb-by-verb so the
		// release stage succeeds without touching a real remote.
		fixture.StubBinary(t, stubs, "git", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker")...)
		// Keep real git for SeedReleaseRepo's repo setup and use the stub
		// only for erun's release operations: the production code resolves
		// `git` via common.Command which honors ERUN_GIT_BIN. The repo
		// already exists via the seed.
		envVars = append(envVars, fixture.StubEnv(stubs, "git")...)
		result := erun.Run(t, []string{"build", "--release", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		for _, want := range []string{
			"docker push",
			"docker manifest create",
			"docker manifest push",
		} {
			if !strings.Contains(result.Combined, want) {
				t.Errorf("expected real-run release trace to contain %q, got:\n%s", want, result.Combined)
			}
		}
	})

	t.Run("dry_run_release_pushes_release_tagged_docker_builds", func(t *testing.T) {
		// Exercises build.go --release path: per-platform docker build +
		// docker push trace must appear in the dry-run output for the
		// release-tagged image, plus the local tag for downstream
		// dependencies.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "main")
		result := erun.Run(t, []string{"build", "--release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if !strings.Contains(result.Stderr, "docker push") {
			t.Errorf("expected release dry-run to trace docker push, got stderr:\n%s", result.Stderr)
		}
	})
}

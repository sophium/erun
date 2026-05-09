package integration

import (
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
